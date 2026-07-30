// 本文件验证 MCP 2026-07-28 HTTP 协议和安全中间件。
package mcphttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cymomaker/eacg/audit"
	"github.com/cymomaker/eacg/execution"
	"github.com/cymomaker/eacg/identity"
	"github.com/cymomaker/eacg/registry"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// validProtocolBody 创建包含新版协议元数据的 JSON-RPC 请求体。
func validProtocolBody(method string, params string) string {
	return `{"jsonrpc":"2.0","id":1,"method":"` + method +
		`","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28",` +
		`"io.modelcontextprotocol/clientCapabilities":{},` +
		`"io.modelcontextprotocol/clientInfo":{"name":"test-client","version":"v1"}}` +
		params + `}}`
}

// setProtocolHeaders 设置新版 MCP HTTP 请求头。
func setProtocolHeaders(header http.Header, method string) {
	header.Set("Authorization", "Bearer valid")
	header.Set("Content-Type", "application/json")
	header.Set("Accept", "application/json, text/event-stream")
	header.Set("Mcp-Protocol-Version", protocolVersion)
	header.Set("Mcp-Method", method)
}

// bearerTestAuthenticator 为 Bearer 协议测试返回固定身份。
type bearerTestAuthenticator struct{}

// Authenticate 为 Bearer 协议测试返回固定身份。
func (bearerTestAuthenticator) Authenticate(
	_ context.Context,
	request identity.AuthenticationRequest,
) (identity.Authentication, error) {
	if request.Credential != "valid" {
		return identity.Authentication{}, identity.ErrUnauthenticated
	}
	return identity.Authentication{
		Principal: identity.Principal{
			SubjectType:  identity.SubjectUser,
			TenantID:     "tenant-a",
			UserID:       "user-1",
			AuthMethod:   "bearer",
			CredentialID: "test-bearer",
		},
		ExpiresAt: time.Now().Add(time.Hour),
	}, nil
}

// testAuthenticator 为自定义 Header 测试返回复合身份。
type testAuthenticator struct{}

// Authenticate 为自定义 Header 测试返回固定复合身份。
func (testAuthenticator) Authenticate(
	_ context.Context,
	request identity.AuthenticationRequest,
) (identity.Authentication, error) {
	if request.Credential != "valid-key" ||
		request.Subject == nil ||
		request.Subject.Provider != "wecom" ||
		request.Subject.ExternalID == "" {
		return identity.Authentication{}, identity.ErrUnauthenticated
	}
	return identity.Authentication{
		Principal: identity.Principal{
			SubjectType:     identity.SubjectUser,
			TenantID:        "tenant-a",
			UserID:          request.Subject.ExternalID,
			AuthMethod:      "api_key",
			CredentialID:    "key-1",
			SubjectProvider: "wecom",
		},
		ExpiresAt: time.Now().Add(time.Hour),
	}, nil
}

// backendFailureAuthenticator 模拟认证后端不可用。
type backendFailureAuthenticator struct{}

// Authenticate 模拟包含敏感内部信息的认证后端错误。
func (backendFailureAuthenticator) Authenticate(
	_ context.Context,
	_ identity.AuthenticationRequest,
) (identity.Authentication, error) {
	return identity.Authentication{}, errors.New(
		"database failed with valid-key and user-1",
	)
}

// subjectCaptureAuthenticator 保存认证请求，便于检查可选用户声明。
type subjectCaptureAuthenticator struct {
	requests chan identity.AuthenticationRequest
}

// Authenticate 保存请求并返回固定用户身份。
func (a subjectCaptureAuthenticator) Authenticate(
	_ context.Context,
	request identity.AuthenticationRequest,
) (identity.Authentication, error) {
	a.requests <- request
	return identity.Authentication{
		Principal: identity.Principal{
			SubjectType:  identity.SubjectUser,
			TenantID:     "tenant-a",
			UserID:       "user-1",
			AuthMethod:   "bearer",
			CredentialID: "capture",
		},
		ExpiresAt: time.Now().Add(time.Hour),
	}, nil
}

// newTestHandler 创建协议测试使用的 HTTP Handler。
func newTestHandler(t *testing.T, origins []string) http.Handler {
	t.Helper()
	store := registry.New()
	engine, err := execution.New(store, new(audit.MemorySink), execution.Config{})
	if err != nil {
		t.Fatalf("创建执行引擎失败：%v", err)
	}
	handler, err := New(Config{
		Name:           "test",
		Version:        "v1",
		Registry:       store,
		Engine:         engine,
		Authenticator:  bearerTestAuthenticator{},
		AllowedOrigins: origins,
	})
	if err != nil {
		t.Fatalf("创建 MCP Handler 失败：%v", err)
	}
	return handler
}

// newAPIKeyTestHandler 创建自定义 Header 认证测试使用的 Handler。
func newAPIKeyTestHandler(t *testing.T) http.Handler {
	t.Helper()
	store := registry.New()
	engine, err := execution.New(store, new(audit.MemorySink), execution.Config{})
	if err != nil {
		t.Fatalf("创建执行引擎失败：%v", err)
	}
	handler, err := New(Config{
		Name:             "test",
		Version:          "v1",
		Registry:         store,
		Engine:           engine,
		Authenticator:    testAuthenticator{},
		CredentialHeader: "X-EACG-API-Key",
		SubjectHeader:    "X-EACG-Requester-UserID",
		SubjectProvider:  "wecom",
	})
	if err != nil {
		t.Fatalf("创建 API Key MCP Handler 失败：%v", err)
	}
	return handler
}

// TestHandlerRejectsMissingToken 验证匿名请求会被拒绝。
func TestHandlerRejectsMissingToken(t *testing.T) {
	t.Parallel()

	request := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	response := httptest.NewRecorder()
	newTestHandler(t, nil).ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("匿名请求应返回 401，实际为：%d", response.Code)
	}
	if response.Header().Get("X-Request-ID") == "" {
		t.Fatal("响应应包含 X-Request-ID")
	}
}

// TestHandlerLeavesSubjectNilWhenUnconfigured 验证未配置用户 Header 时不要求 userid。
func TestHandlerLeavesSubjectNilWhenUnconfigured(t *testing.T) {
	t.Parallel()

	store := registry.New()
	engine, err := execution.New(store, new(audit.MemorySink), execution.Config{})
	if err != nil {
		t.Fatalf("创建执行引擎失败：%v", err)
	}
	requests := make(chan identity.AuthenticationRequest, 1)
	handler, err := New(Config{
		Name:          "test",
		Version:       "v1",
		Registry:      store,
		Engine:        engine,
		Authenticator: subjectCaptureAuthenticator{requests: requests},
	})
	if err != nil {
		t.Fatalf("创建 Handler 失败：%v", err)
	}
	request := httptest.NewRequest(
		http.MethodPost,
		"/mcp",
		strings.NewReader(validProtocolBody("server/discover", "")),
	)
	setProtocolHeaders(request.Header, "server/discover")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("发现请求失败：code=%d body=%s", response.Code, response.Body.String())
	}
	captured := <-requests
	if captured.Subject != nil {
		t.Fatalf("未配置 Subject Header 时应传入 nil：%+v", captured.Subject)
	}
}

// TestHandlerRejectsUnknownOrigin 验证未授权 Origin 会被拒绝。
func TestHandlerRejectsUnknownOrigin(t *testing.T) {
	t.Parallel()

	request := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	request.Header.Set("Origin", "https://evil.example")
	request.Header.Set("Authorization", "Bearer valid")
	response := httptest.NewRecorder()
	newTestHandler(t, []string{"https://trusted.example"}).ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("未知 Origin 应返回 403，实际为：%d", response.Code)
	}
}

// TestToolErrorHidesInternalDetails 验证 Tool Error 不泄露内部错误。
func TestToolErrorHidesInternalDetails(t *testing.T) {
	t.Parallel()

	result := toolError(errors.New("database password=secret"))
	if !result.IsError || len(result.Content) != 1 {
		t.Fatalf("Tool Error 结构不正确：%+v", result)
	}
	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("错误内容类型不正确：%T", result.Content[0])
	}
	if text.Text == "database password=secret" {
		t.Fatal("Tool Error 不应返回内部错误")
	}
}

// TestNewRejectsIncompleteConfig 验证缺少核心依赖时创建失败。
func TestNewRejectsIncompleteConfig(t *testing.T) {
	t.Parallel()

	if _, err := New(Config{Name: "test", Version: "v1"}); err == nil {
		t.Fatal("缺少核心依赖时不应创建成功")
	}
}

// TestHandlerAcceptsCustomCredentialHeader 验证自定义 API Key Header 可以进入 MCP 处理。
func TestHandlerAcceptsCustomCredentialHeader(t *testing.T) {
	t.Parallel()

	request := httptest.NewRequest(
		http.MethodPost,
		"/mcp",
		strings.NewReader(validProtocolBody("server/discover", "")),
	)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("Mcp-Protocol-Version", protocolVersion)
	request.Header.Set("Mcp-Method", "server/discover")
	request.Header.Set("X-EACG-API-Key", "valid-key")
	request.Header.Set("X-EACG-Requester-UserID", "user-1")
	response := httptest.NewRecorder()
	newAPIKeyTestHandler(t).ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("合法自定义认证应返回 200：code=%d body=%s", response.Code, response.Body.String())
	}
}

// TestHandlerSupportsOnlyNewProtocol 验证服务只声明并接受 2026-07-28。
func TestHandlerSupportsOnlyNewProtocol(t *testing.T) {
	t.Parallel()

	request := httptest.NewRequest(
		http.MethodPost,
		"/mcp",
		strings.NewReader(validProtocolBody("server/discover", "")),
	)
	setProtocolHeaders(request.Header, "server/discover")
	response := httptest.NewRecorder()
	newTestHandler(t, nil).ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("发现请求失败：code=%d body=%s", response.Code, response.Body.String())
	}
	if response.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("普通响应应使用 JSON：%s", response.Header().Get("Content-Type"))
	}
	if response.Header().Get("Mcp-Session-Id") != "" {
		t.Fatal("无状态响应不能返回 Mcp-Session-Id")
	}
	var document struct {
		Result struct {
			ResultType        string         `json:"resultType"`
			SupportedVersions []string       `json:"supportedVersions"`
			CacheScope        string         `json:"cacheScope"`
			Capabilities      map[string]any `json:"capabilities"`
			Meta              map[string]any `json:"_meta"`
		} `json:"result"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &document); err != nil {
		t.Fatalf("解析发现结果失败：%v", err)
	}
	if document.Result.ResultType != "complete" ||
		len(document.Result.SupportedVersions) != 1 ||
		document.Result.SupportedVersions[0] != protocolVersion {
		t.Fatalf("发现结果协议不正确：%+v", document.Result)
	}
	if document.Result.CacheScope != "public" ||
		document.Result.Meta[mcp.MetaKeyServerInfo] == nil {
		t.Fatalf("发现结果缓存或服务身份不正确：%+v", document.Result)
	}
	if len(document.Result.Capabilities) != 1 || document.Result.Capabilities["tools"] == nil {
		t.Fatalf("服务只能声明 Tool 能力：%+v", document.Result.Capabilities)
	}
}

// TestHandlerRejectsInvalidProtocolHeaders 验证缺失、旧版和会话 Header 会被拒绝。
func TestHandlerRejectsInvalidProtocolHeaders(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		setup func(http.Header)
		code  string
	}{
		{
			name: "缺少协议版本",
			setup: func(header http.Header) {
				header.Del("Mcp-Protocol-Version")
			},
			code: "-32022",
		},
		{
			name: "旧协议版本",
			setup: func(header http.Header) {
				header.Set("Mcp-Protocol-Version", "2025-11-25")
			},
			code: "-32022",
		},
		{
			name: "重复协议版本",
			setup: func(header http.Header) {
				header.Add("Mcp-Protocol-Version", protocolVersion)
			},
			code: "-32022",
		},
		{
			name: "缺少方法",
			setup: func(header http.Header) {
				header.Del("Mcp-Method")
			},
			code: "-32600",
		},
		{
			name: "旧会话标识",
			setup: func(header http.Header) {
				header.Set("Mcp-Session-Id", "legacy")
			},
			code: "-32600",
		},
		{
			name: "旧事件续传标识",
			setup: func(header http.Header) {
				header.Set("Last-Event-ID", "legacy")
			},
			code: "-32600",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(
				http.MethodPost,
				"/mcp",
				strings.NewReader(validProtocolBody("server/discover", "")),
			)
			setProtocolHeaders(request.Header, "server/discover")
			test.setup(request.Header)
			response := httptest.NewRecorder()
			newTestHandler(t, nil).ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest ||
				!strings.Contains(response.Body.String(), test.code) {
				t.Fatalf("应拒绝非法协议头：code=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

// TestHandlerRejectsHeaderBodyMismatch 验证 SDK 会拒绝 Header 与请求体不一致。
func TestHandlerRejectsHeaderBodyMismatch(t *testing.T) {
	t.Parallel()

	request := httptest.NewRequest(
		http.MethodPost,
		"/mcp",
		strings.NewReader(validProtocolBody("server/discover", "")),
	)
	setProtocolHeaders(request.Header, "tools/list")
	response := httptest.NewRecorder()
	newTestHandler(t, nil).ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "-32020") {
		t.Fatalf("Header 不一致应返回 -32020：code=%d body=%s", response.Code, response.Body.String())
	}
}

// TestHandlerRejectsDeprecatedMethods 验证新版协议不处理已删除的方法。
func TestHandlerRejectsDeprecatedMethods(t *testing.T) {
	t.Parallel()

	tests := []struct {
		method string
		params string
	}{
		{method: "initialize", params: `,"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"old","version":"v1"}`},
		{method: "notifications/initialized"},
		{method: "ping"},
		{method: "logging/setLevel", params: `,"level":"info"`},
	}
	for _, test := range tests {
		test := test
		t.Run(test.method, func(t *testing.T) {
			request := httptest.NewRequest(
				http.MethodPost,
				"/mcp",
				strings.NewReader(validProtocolBody(test.method, test.params)),
			)
			setProtocolHeaders(request.Header, test.method)
			response := httptest.NewRecorder()
			newTestHandler(t, nil).ServeHTTP(response, request)
			if !strings.Contains(response.Body.String(), "-32601") {
				t.Fatalf("%s 应返回 Method Not Found：code=%d body=%s",
					test.method, response.Code, response.Body.String())
			}
		})
	}
}

// TestHandlerRejectsLegacyHTTPMethods 验证无状态端点只允许 POST。
func TestHandlerRejectsLegacyHTTPMethods(t *testing.T) {
	t.Parallel()

	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		request := httptest.NewRequest(method, "/mcp", nil)
		request.Header.Set("Authorization", "Bearer valid")
		response := httptest.NewRecorder()
		newTestHandler(t, nil).ServeHTTP(response, request)
		if response.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s 应返回 405，实际为：%d", method, response.Code)
		}
	}
}

// TestHandlerRequiresToolNameHeader 验证 Tool 调用必须声明名称。
func TestHandlerRequiresToolNameHeader(t *testing.T) {
	t.Parallel()

	request := httptest.NewRequest(
		http.MethodPost,
		"/mcp",
		strings.NewReader(validProtocolBody("tools/call", `,"name":"unknown","arguments":{}`)),
	)
	setProtocolHeaders(request.Header, "tools/call")
	response := httptest.NewRecorder()
	newTestHandler(t, nil).ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "-32600") {
		t.Fatalf("缺少 Mcp-Name 应被拒绝：code=%d body=%s", response.Code, response.Body.String())
	}
}

// TestHandlerUsesPrivateToolListCache 验证用户 Tool 列表不能使用公共缓存。
func TestHandlerUsesPrivateToolListCache(t *testing.T) {
	t.Parallel()

	request := httptest.NewRequest(
		http.MethodPost,
		"/mcp",
		strings.NewReader(validProtocolBody("tools/list", "")),
	)
	setProtocolHeaders(request.Header, "tools/list")
	response := httptest.NewRecorder()
	newTestHandler(t, nil).ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("查询 Tool 失败：code=%d body=%s", response.Code, response.Body.String())
	}
	var document struct {
		Result struct {
			CacheScope string `json:"cacheScope"`
			TTLMs      int    `json:"ttlMs"`
		} `json:"result"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &document); err != nil {
		t.Fatalf("解析 Tool 列表失败：%v", err)
	}
	if document.Result.CacheScope != "private" || document.Result.TTLMs != 0 {
		t.Fatalf("Tool 缓存策略不正确：%+v", document.Result)
	}
}

// TestHandlerRejectsInvalidCustomHeaders 验证缺失、重复和冲突 Header 会被拒绝。
func TestHandlerRejectsInvalidCustomHeaders(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		target string
		setup  func(http.Header)
	}{
		{
			name:   "查询参数不能作为凭据",
			target: "/mcp?X-EACG-API-Key=valid-key",
			setup: func(header http.Header) {
				header.Set("X-EACG-Requester-UserID", "user-1")
			},
		},
		{
			name:   "缺少用户身份",
			target: "/mcp",
			setup: func(header http.Header) {
				header.Set("X-EACG-API-Key", "valid-key")
			},
		},
		{
			name:   "凭据来源冲突",
			target: "/mcp",
			setup: func(header http.Header) {
				header.Set("Authorization", "Bearer other")
				header.Set("X-EACG-API-Key", "valid-key")
				header.Set("X-EACG-Requester-UserID", "user-1")
			},
		},
		{
			name:   "重复用户身份",
			target: "/mcp",
			setup: func(header http.Header) {
				header.Set("X-EACG-API-Key", "valid-key")
				header.Add("X-EACG-Requester-UserID", "user-1")
				header.Add("X-EACG-Requester-UserID", "user-2")
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, test.target, nil)
			test.setup(request.Header)
			response := httptest.NewRecorder()
			newAPIKeyTestHandler(t).ServeHTTP(response, request)
			if response.Code != http.StatusUnauthorized {
				t.Fatalf("非法认证 Header 应返回 401，实际为：%d", response.Code)
			}
		})
	}
}

// TestNewRejectsInvalidAuthenticationHeaders 验证冲突的认证 Header 配置会被拒绝。
func TestNewRejectsInvalidAuthenticationHeaders(t *testing.T) {
	t.Parallel()

	store := registry.New()
	engine, err := execution.New(store, new(audit.MemorySink), execution.Config{})
	if err != nil {
		t.Fatalf("创建执行引擎失败：%v", err)
	}
	_, err = New(Config{
		Name:             "test",
		Version:          "v1",
		Registry:         store,
		Engine:           engine,
		Authenticator:    testAuthenticator{},
		CredentialHeader: "X-Shared",
		SubjectHeader:    "X-Shared",
		SubjectProvider:  "wecom",
	})
	if err == nil {
		t.Fatal("相同凭据和用户 Header 不应创建成功")
	}
}

// TestNewRejectsUnlimitedRequestBody 验证不能关闭 MCP 请求体大小限制。
func TestNewRejectsUnlimitedRequestBody(t *testing.T) {
	t.Parallel()

	store := registry.New()
	engine, err := execution.New(store, new(audit.MemorySink), execution.Config{})
	if err != nil {
		t.Fatalf("创建执行引擎失败：%v", err)
	}
	_, err = New(Config{
		Name:                "test",
		Version:             "v1",
		Registry:            store,
		Engine:              engine,
		Authenticator:       testAuthenticator{},
		MaxRequestBodyBytes: -1,
	})
	if err == nil {
		t.Fatal("负数请求体限制不应被接受")
	}
}

// TestAuthenticationFailureHidesSensitiveDetails 验证认证错误和日志不泄露敏感信息。
func TestAuthenticationFailureHidesSensitiveDetails(t *testing.T) {
	t.Parallel()

	store := registry.New()
	engine, err := execution.New(store, new(audit.MemorySink), execution.Config{})
	if err != nil {
		t.Fatalf("创建执行引擎失败：%v", err)
	}
	var logs bytes.Buffer
	handler, err := New(Config{
		Name:             "test",
		Version:          "v1",
		Registry:         store,
		Engine:           engine,
		Authenticator:    backendFailureAuthenticator{},
		CredentialHeader: "X-EACG-API-Key",
		SubjectHeader:    "X-EACG-Requester-UserID",
		SubjectProvider:  "wecom",
		Logger:           slog.New(slog.NewJSONHandler(&logs, nil)),
	})
	if err != nil {
		t.Fatalf("创建 Handler 失败：%v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	request.Header.Set("X-EACG-API-Key", "valid-key")
	request.Header.Set("X-EACG-Requester-UserID", "user-1")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	combined := response.Body.String() + logs.String()
	if strings.Contains(combined, "valid-key") || strings.Contains(combined, "user-1") {
		t.Fatalf("响应或日志泄露了敏感认证信息：%s", combined)
	}
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("认证后端错误应返回 500，实际为：%d", response.Code)
	}
}
