// Package mcphttp 把 EACG 能力适配为 MCP Streamable HTTP。
package mcphttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode"

	"github.com/cymomaker/eacg/capability"
	"github.com/cymomaker/eacg/execution"
	"github.com/cymomaker/eacg/identity"
	"github.com/cymomaker/eacg/registry"
	mcpauth "github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const principalExtraKey = "eacg.principal"

type contextKey string

const (
	requestIDKey contextKey = "request_id"
	traceIDKey   contextKey = "trace_id"
)

// Config 定义 MCP HTTP Handler 参数。
type Config struct {
	Name             string
	Version          string
	Registry         *registry.Registry
	Engine           *execution.Engine
	Authenticator    identity.Authenticator
	CredentialHeader string
	SubjectHeader    string
	SubjectProvider  string
	Logger           *slog.Logger
	SessionTimeout   time.Duration
	AllowedOrigins   []string
	ResourceMetaURL  string
}

// New 创建带认证和 Origin 防护的 MCP HTTP Handler。
func New(config Config) (http.Handler, error) {
	if config.Name == "" || config.Version == "" {
		return nil, fmt.Errorf("应用名称和版本不能为空")
	}
	if config.Registry == nil || config.Engine == nil {
		return nil, fmt.Errorf("注册表和执行引擎不能为空")
	}
	if config.Authenticator == nil {
		return nil, fmt.Errorf("身份认证器不能为空")
	}
	if err := validateAuthenticationHeaders(config); err != nil {
		return nil, err
	}
	if config.Logger == nil {
		config.Logger = slog.Default()
	}
	if config.SessionTimeout <= 0 {
		config.SessionTimeout = 30 * time.Minute
	}

	streamable := mcp.NewStreamableHTTPHandler(
		func(request *http.Request) *mcp.Server {
			principal, ok := principalFromRequest(request)
			if !ok {
				return nil
			}
			return buildServer(config, principal)
		},
		&mcp.StreamableHTTPOptions{
			Logger:         config.Logger,
			SessionTimeout: config.SessionTimeout,
		},
	)

	verifier := adaptAuthenticator(config.Authenticator, config)
	authMiddleware := mcpauth.RequireBearerToken(verifier, &mcpauth.RequireBearerTokenOptions{
		ResourceMetadataURL: config.ResourceMetaURL,
	})
	handler := authMiddleware(streamable)
	if config.CredentialHeader != "" {
		handler = credentialHeaderMiddleware(config.CredentialHeader, config.Logger, handler)
	} else {
		handler = bearerCredentialLogMiddleware(config.Logger, handler)
	}
	handler = originMiddleware(config.AllowedOrigins, handler)
	handler = requestContextMiddleware(handler)
	return handler, nil
}

// buildServer 为当前身份创建只包含可见 Tool 的 MCP Server。
func buildServer(config Config, principal identity.Principal) *mcp.Server {
	server := mcp.NewServer(
		&mcp.Implementation{Name: config.Name, Version: config.Version},
		&mcp.ServerOptions{
			Logger:    config.Logger,
			KeepAlive: 30 * time.Second,
			Capabilities: &mcp.ServerCapabilities{
				Tools: &mcp.ToolCapabilities{ListChanged: false},
			},
		},
	)

	for _, item := range config.Registry.Visible(principal) {
		registerTool(server, config.Engine, item, principal)
	}
	return server
}

// registerTool 把一个 EACG Capability 注册成 MCP Tool。
func registerTool(
	server *mcp.Server,
	engine *execution.Engine,
	item capability.Capability,
	principal identity.Principal,
) {
	descriptor := item.Descriptor()
	server.AddTool(&mcp.Tool{
		Name:         descriptor.Name,
		Description:  descriptor.Description,
		InputSchema:  descriptor.InputSchema,
		OutputSchema: descriptor.OutputSchema,
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:   descriptor.ReadOnly,
			IdempotentHint: descriptor.Idempotent,
		},
	}, func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		result, err := engine.Execute(ctx, execution.Request{
			RequestID:  contextString(ctx, requestIDKey),
			TraceID:    contextString(ctx, traceIDKey),
			Principal:  principal,
			Capability: descriptor.Name,
			Arguments:  request.Params.Arguments,
		})
		if err != nil {
			return toolError(err), nil
		}
		raw, err := json.Marshal(result.Value)
		if err != nil {
			return toolError(fmt.Errorf("编码能力结果失败")), nil
		}
		content := []mcp.Content{&mcp.TextContent{Text: string(raw)}}
		for _, warning := range result.Warnings {
			content = append(content, &mcp.TextContent{Text: "警告：" + warning})
		}
		return &mcp.CallToolResult{
			Content:           content,
			StructuredContent: result.Value,
		}, nil
	})
}

// toolError 把内部错误转换为不会泄露细节的 Tool Error。
func toolError(err error) *mcp.CallToolResult {
	message := "能力执行失败"
	switch {
	case errors.Is(err, execution.ErrForbidden):
		message = "没有能力调用权限"
	case errors.Is(err, execution.ErrNotFound):
		message = "能力不存在"
	case errors.Is(err, capability.ErrInvalidInput):
		message = "能力输入不符合要求"
	case errors.Is(err, context.DeadlineExceeded):
		message = "能力执行超时"
	case errors.Is(err, context.Canceled):
		message = "能力执行已取消"
	}
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: message}},
	}
}

// adaptAuthenticator 把 EACG 认证器适配为 MCP SDK 接口。
func adaptAuthenticator(authenticator identity.Authenticator, config Config) mcpauth.TokenVerifier {
	return func(ctx context.Context, raw string, request *http.Request) (*mcpauth.TokenInfo, error) {
		subject, err := subjectFromRequest(request, config.SubjectHeader, config.SubjectProvider)
		if err != nil {
			logAuthenticationFailure(
				config.Logger,
				request,
				"invalid_subject_header",
				credentialSource(config),
			)
			return nil, fmt.Errorf("%w：身份声明无效", mcpauth.ErrInvalidToken)
		}
		authentication, err := authenticator.Authenticate(ctx, identity.AuthenticationRequest{
			Credential: raw,
			Subject:    subject,
		})
		if err != nil {
			if errors.Is(err, identity.ErrUnauthenticated) {
				logAuthenticationFailure(
					config.Logger,
					request,
					"unauthenticated",
					credentialSource(config),
				)
				return nil, fmt.Errorf("%w：身份认证失败", mcpauth.ErrInvalidToken)
			}
			logAuthenticationFailure(
				config.Logger,
				request,
				"authentication_backend_error",
				credentialSource(config),
			)
			return nil, fmt.Errorf("authentication service unavailable")
		}
		if !authentication.Valid(time.Now()) {
			logAuthenticationFailure(
				config.Logger,
				request,
				"invalid_authentication_result",
				credentialSource(config),
			)
			return nil, fmt.Errorf("%w：认证结果无效", mcpauth.ErrInvalidToken)
		}
		return &mcpauth.TokenInfo{
			Scopes:     append([]string(nil), authentication.Principal.Scopes...),
			Expiration: authentication.ExpiresAt,
			UserID:     authentication.SessionBindingID,
			Extra: map[string]any{
				principalExtraKey: authentication.Principal,
			},
		}, nil
	}
}

// credentialHeaderMiddleware 把自定义凭据 Header 转成 SDK 可识别的 Bearer Header。
func credentialHeaderMiddleware(
	headerName string,
	logger *slog.Logger,
	next http.Handler,
) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "" {
			logAuthenticationFailure(logger, request, "ambiguous_credential", "custom_header")
			http.Error(writer, "ambiguous credential", http.StatusUnauthorized)
			return
		}
		values := request.Header.Values(headerName)
		if len(values) != 1 {
			logAuthenticationFailure(logger, request, "invalid_credential_header", "custom_header")
			http.Error(writer, "invalid credential", http.StatusUnauthorized)
			return
		}
		credential := strings.TrimSpace(values[0])
		if !validHTTPValue(credential, 8192) {
			logAuthenticationFailure(logger, request, "invalid_credential_header", "custom_header")
			http.Error(writer, "invalid credential", http.StatusUnauthorized)
			return
		}
		cloned := request.Clone(request.Context())
		cloned.Header = request.Header.Clone()
		cloned.Header.Set("Authorization", "Bearer "+credential)
		next.ServeHTTP(writer, cloned)
	})
}

// bearerCredentialLogMiddleware 记录缺失或格式错误的 Bearer Header。
func bearerCredentialLogMiddleware(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		fields := strings.Fields(request.Header.Get("Authorization"))
		if len(fields) != 2 || !strings.EqualFold(fields[0], "bearer") {
			logAuthenticationFailure(logger, request, "invalid_bearer_header", "bearer")
		}
		next.ServeHTTP(writer, request)
	})
}

// subjectFromRequest 从可信 Header 读取外部用户身份。
func subjectFromRequest(
	request *http.Request,
	headerName string,
	provider string,
) (*identity.SubjectAssertion, error) {
	if headerName == "" {
		return nil, nil
	}
	values := request.Header.Values(headerName)
	if len(values) != 1 {
		return nil, fmt.Errorf("用户身份 Header 必须且只能出现一次")
	}
	externalID := strings.TrimSpace(values[0])
	if !validHTTPValue(externalID, 256) {
		return nil, fmt.Errorf("用户身份 Header 无效")
	}
	return &identity.SubjectAssertion{
		Provider:   provider,
		ExternalID: externalID,
	}, nil
}

// validateAuthenticationHeaders 检查认证 Header 配置是否安全。
func validateAuthenticationHeaders(config Config) error {
	if config.CredentialHeader != "" {
		if !validHeaderName(config.CredentialHeader) {
			return fmt.Errorf("凭据 Header 名称无效")
		}
		if strings.EqualFold(config.CredentialHeader, "Authorization") {
			return fmt.Errorf("自定义凭据 Header 不能使用 Authorization")
		}
	}
	if config.SubjectHeader == "" && config.SubjectProvider != "" {
		return fmt.Errorf("配置用户来源时必须配置用户身份 Header")
	}
	if config.SubjectHeader != "" {
		if !validHeaderName(config.SubjectHeader) || config.SubjectProvider == "" {
			return fmt.Errorf("用户身份 Header 和来源配置不完整")
		}
		if strings.EqualFold(config.SubjectHeader, config.CredentialHeader) ||
			strings.EqualFold(config.SubjectHeader, "Authorization") {
			return fmt.Errorf("用户身份 Header 不能与凭据 Header 相同")
		}
	}
	return nil
}

// validHeaderName 检查 HTTP Header 名称是否只包含标准 token 字符。
func validHeaderName(value string) bool {
	if value == "" {
		return false
	}
	for _, current := range value {
		if !strings.ContainsRune("!#$%&'*+-.^_`|~", current) &&
			!(current >= '0' && current <= '9') &&
			!(current >= 'A' && current <= 'Z') &&
			!(current >= 'a' && current <= 'z') {
			return false
		}
	}
	return true
}

// validHTTPValue 检查 Header 值长度并拒绝控制字符。
func validHTTPValue(value string, maxLength int) bool {
	if value == "" || len(value) > maxLength {
		return false
	}
	for _, current := range value {
		if unicode.IsControl(current) || current == unicode.ReplacementChar {
			return false
		}
	}
	return true
}

// logAuthenticationFailure 记录不包含凭据和用户标识的认证失败事件。
func logAuthenticationFailure(
	logger *slog.Logger,
	request *http.Request,
	code string,
	credentialSource string,
) {
	if logger == nil {
		return
	}
	logger.Warn(
		"mcp_authentication_failed",
		"request_id", contextString(request.Context(), requestIDKey),
		"error_code", code,
		"credential_source", credentialSource,
	)
}

// credentialSource 返回不会泄露 Header 名称的凭据来源类型。
func credentialSource(config Config) string {
	if config.CredentialHeader != "" {
		return "custom_header"
	}
	return "bearer"
}

// principalFromRequest 从已校验令牌中读取企业身份。
func principalFromRequest(request *http.Request) (identity.Principal, bool) {
	info := mcpauth.TokenInfoFromContext(request.Context())
	if info == nil {
		return identity.Principal{}, false
	}
	principal, ok := info.Extra[principalExtraKey].(identity.Principal)
	return principal, ok && principal.Valid()
}

// requestContextMiddleware 为每个 HTTP 请求补充 Request ID 和 Trace ID。
func requestContextMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requestID := strings.TrimSpace(request.Header.Get("X-Request-ID"))
		if requestID == "" {
			requestID = newRequestID()
		}
		traceID := strings.TrimSpace(request.Header.Get("X-Trace-ID"))
		if traceID == "" {
			traceID = requestID
		}
		writer.Header().Set("X-Request-ID", requestID)
		ctx := context.WithValue(request.Context(), requestIDKey, requestID)
		ctx = context.WithValue(ctx, traceIDKey, traceID)
		next.ServeHTTP(writer, request.WithContext(ctx))
	})
}

// originMiddleware 拒绝未加入白名单的浏览器跨域请求。
func originMiddleware(allowed []string, next http.Handler) http.Handler {
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, origin := range allowed {
		allowedSet[origin] = struct{}{}
	}
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		origin := request.Header.Get("Origin")
		if origin != "" {
			if _, ok := allowedSet[origin]; !ok {
				http.Error(writer, "origin forbidden", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(writer, request)
	})
}

// contextString 从上下文读取字符串。
func contextString(ctx context.Context, key contextKey) string {
	value, _ := ctx.Value(key).(string)
	return value
}

// newRequestID 生成简单的请求标识。
func newRequestID() string {
	return fmt.Sprintf("req-%d", time.Now().UnixNano())
}
