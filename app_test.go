// 本文件验证 EACG 应用组装、无状态调用和请求取消。
package eacg

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cymomaker/eacg/audit"
	"github.com/cymomaker/eacg/capability"
	"github.com/cymomaker/eacg/identity"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// staticAuthenticator 为应用测试返回固定身份。
type staticAuthenticator struct {
	principal identity.Principal
}

// countingAuthenticator 统计每次 HTTP 请求触发的认证次数。
type countingAuthenticator struct {
	delegate staticAuthenticator
	calls    atomic.Int64
}

// Authenticate 统计认证次数并返回固定身份。
func (a *countingAuthenticator) Authenticate(
	ctx context.Context,
	request identity.AuthenticationRequest,
) (identity.Authentication, error) {
	a.calls.Add(1)
	return a.delegate.Authenticate(ctx, request)
}

// Authenticate 为应用测试返回固定身份。
func (a staticAuthenticator) Authenticate(
	_ context.Context,
	_ identity.AuthenticationRequest,
) (identity.Authentication, error) {
	principal := a.principal
	if !principal.Valid() {
		principal = identity.Principal{
			SubjectType:  identity.SubjectUser,
			TenantID:     "tenant-a",
			UserID:       "user-1",
			AuthMethod:   "bearer",
			CredentialID: "test-credential",
		}
	}
	if principal.AuthMethod == "" {
		principal.AuthMethod = "bearer"
	}
	if principal.CredentialID == "" {
		principal.CredentialID = "test-credential"
	}
	return identity.Authentication{
		Principal: principal,
		ExpiresAt: time.Now().Add(time.Hour),
	}, nil
}

// bearerTransport 为测试请求增加 Bearer Token。
type bearerTransport struct {
	base  http.RoundTripper
	token string
}

// apiKeyTransport 为测试请求增加 API Key。
type apiKeyTransport struct {
	base http.RoundTripper
	key  string
}

// RoundTrip 为 MCP 请求增加固定 API Key Header。
func (t *apiKeyTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	cloned := request.Clone(request.Context())
	cloned.Header.Set("X-EACG-API-Key", t.key)
	return t.base.RoundTrip(cloned)
}

// RoundTrip 为 MCP 测试请求增加 Bearer Token。
func (t bearerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	cloned := request.Clone(request.Context())
	cloned.Header.Set("Authorization", "Bearer "+t.token)
	return t.base.RoundTrip(cloned)
}

// echoInput 保存回显 Tool 的输入。
type echoInput struct {
	Message string `json:"message"`
}

// echoOutput 保存回显 Tool 的输出。
type echoOutput struct {
	Message string `json:"message"`
}

// alternatingHandler 模拟多个无共享状态的服务实例。
type alternatingHandler struct {
	handlers []http.Handler
	next     atomic.Uint64
}

// ServeHTTP 把相邻请求轮流交给不同的无状态 Handler。
func (h *alternatingHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	index := h.next.Add(1) - 1
	h.handlers[index%uint64(len(h.handlers))].ServeHTTP(writer, request)
}

// newEchoTestCapability 创建应用测试共用的回显能力。
func newEchoTestCapability(t *testing.T) capability.Capability {
	t.Helper()
	item, err := capability.New(capability.Descriptor{
		ID:            "echo.v1",
		Name:          "echo",
		Version:       "v1",
		Description:   "返回输入文本",
		RiskLevel:     capability.RiskR0,
		ReadOnly:      true,
		RequiredRoles: []string{"reader"},
	}, func(_ context.Context, _ capability.RequestContext, input echoInput) (echoOutput, error) {
		return echoOutput(input), nil
	})
	if err != nil {
		t.Fatalf("创建能力失败：%v", err)
	}
	return item
}

// TestAppHealthAndReadiness 验证健康检查和就绪检查。
func TestAppHealthAndReadiness(t *testing.T) {
	t.Parallel()

	app, err := New(
		Config{Name: "test", Version: "v1"},
		HTTPAuthenticationConfig{Authenticator: staticAuthenticator{}},
		new(audit.MemorySink),
	)
	if err != nil {
		t.Fatalf("创建应用失败：%v", err)
	}
	handler, err := app.Handler()
	if err != nil {
		t.Fatalf("构建 Handler 失败：%v", err)
	}
	for _, route := range []string{"/health", "/ready"} {
		request := httptest.NewRequest(http.MethodGet, route, nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("%s 状态不正确：%d", route, response.Code)
		}
	}
}

// TestAppMCPFlow 验证 MCP 初始化、发现和调用的完整链路。
func TestAppMCPFlow(t *testing.T) {
	t.Parallel()

	authenticator := &countingAuthenticator{
		delegate: staticAuthenticator{
			principal: identity.Principal{
				SubjectType:  identity.SubjectUser,
				TenantID:     "tenant-a",
				UserID:       "user-1",
				AuthMethod:   "bearer",
				CredentialID: "test-credential",
				Roles:        []string{"reader"},
			},
		},
	}
	app, err := New(
		Config{Name: "test", Version: "v1"},
		HTTPAuthenticationConfig{
			Authenticator: authenticator,
		},
		new(audit.MemorySink),
	)
	if err != nil {
		t.Fatalf("创建应用失败：%v", err)
	}
	if err := app.RegisterCapability(newEchoTestCapability(t)); err != nil {
		t.Fatalf("注册能力失败：%v", err)
	}
	handler, err := app.Handler()
	if err != nil {
		t.Fatalf("构建 Handler 失败：%v", err)
	}
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()

	httpClient := &http.Client{
		Transport: bearerTransport{base: http.DefaultTransport, token: "test-token"},
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v1"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint:             httpServer.URL + "/mcp",
		HTTPClient:           httpClient,
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatalf("连接 MCP Server 失败：%v", err)
	}
	defer session.Close()

	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("查询 Tool 失败：%v", err)
	}
	if len(tools.Tools) != 1 || tools.Tools[0].Name != "echo" {
		t.Fatalf("Tool 列表不正确：%+v", tools.Tools)
	}
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "echo",
		Arguments: map[string]any{"message": "hello"},
	})
	if err != nil {
		t.Fatalf("调用 Tool 失败：%v", err)
	}
	raw, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatalf("编码 Tool 结果失败：%v", err)
	}
	if string(raw) != `{"message":"hello"}` {
		t.Fatalf("Tool 结果不正确：%s", raw)
	}
	if authenticator.calls.Load() < 3 {
		t.Fatalf("发现、列表和调用必须分别认证，实际次数：%d", authenticator.calls.Load())
	}
}

// TestAppMCPFlowAcrossHandlers 验证不同实例可以轮流处理同一个逻辑调用流程。
func TestAppMCPFlowAcrossHandlers(t *testing.T) {
	t.Parallel()

	var handlers []http.Handler
	for range 2 {
		app, err := New(
			Config{Name: "test", Version: "v1"},
			HTTPAuthenticationConfig{Authenticator: staticAuthenticator{
				principal: identity.Principal{
					SubjectType:  identity.SubjectUser,
					TenantID:     "tenant-a",
					UserID:       "user-1",
					AuthMethod:   "bearer",
					CredentialID: "test-credential",
					Roles:        []string{"reader"},
				},
			}},
			new(audit.MemorySink),
		)
		if err != nil {
			t.Fatalf("创建应用失败：%v", err)
		}
		if err := app.RegisterCapability(newEchoTestCapability(t)); err != nil {
			t.Fatalf("注册能力失败：%v", err)
		}
		handler, err := app.Handler()
		if err != nil {
			t.Fatalf("构建 Handler 失败：%v", err)
		}
		handlers = append(handlers, handler)
	}
	httpServer := httptest.NewServer(&alternatingHandler{handlers: handlers})
	defer httpServer.Close()

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v1"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint: httpServer.URL + "/mcp",
		HTTPClient: &http.Client{
			Transport: bearerTransport{base: http.DefaultTransport, token: "test-token"},
		},
	}, nil)
	if err != nil {
		t.Fatalf("连接无状态服务失败：%v", err)
	}
	defer session.Close()
	tools, err := session.ListTools(context.Background(), nil)
	if err != nil || len(tools.Tools) != 1 {
		t.Fatalf("跨实例查询 Tool 失败：tools=%+v err=%v", tools, err)
	}
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "echo",
		Arguments: map[string]any{"message": "distributed"},
	})
	if err != nil || result.IsError {
		t.Fatalf("跨实例调用 Tool 失败：result=%+v err=%v", result, err)
	}
}

// TestAppPropagatesRequestCancellation 验证 HTTP 请求取消会终止能力执行。
func TestAppPropagatesRequestCancellation(t *testing.T) {
	t.Parallel()

	started := make(chan struct{})
	canceled := make(chan struct{})
	slow, err := capability.New(capability.Descriptor{
		ID:          "slow.v1",
		Name:        "slow",
		Version:     "v1",
		Description: "等待请求取消",
		RiskLevel:   capability.RiskR0,
		ReadOnly:    true,
	}, func(ctx context.Context, _ capability.RequestContext, _ echoInput) (echoOutput, error) {
		close(started)
		<-ctx.Done()
		close(canceled)
		return echoOutput{}, ctx.Err()
	})
	if err != nil {
		t.Fatalf("创建慢能力失败：%v", err)
	}
	app, err := New(
		Config{Name: "test", Version: "v1", ExecutionTimeout: time.Minute},
		HTTPAuthenticationConfig{Authenticator: staticAuthenticator{}},
		new(audit.MemorySink),
	)
	if err != nil {
		t.Fatalf("创建应用失败：%v", err)
	}
	if err := app.RegisterCapability(slow); err != nil {
		t.Fatalf("注册能力失败：%v", err)
	}
	handler, err := app.Handler()
	if err != nil {
		t.Fatalf("构建 Handler 失败：%v", err)
	}
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v1"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint: httpServer.URL + "/mcp",
		HTTPClient: &http.Client{
			Transport: bearerTransport{base: http.DefaultTransport, token: "test-token"},
		},
	}, nil)
	if err != nil {
		t.Fatalf("连接 MCP Server 失败：%v", err)
	}
	defer session.Close()

	callCtx, cancel := context.WithCancel(context.Background())
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		_, _ = session.CallTool(callCtx, &mcp.CallToolParams{
			Name:      "slow",
			Arguments: map[string]any{"message": "wait"},
		})
	}()
	select {
	case <-started:
		cancel()
	case <-time.After(2 * time.Second):
		t.Fatal("能力没有开始执行")
	}
	select {
	case <-canceled:
	case <-time.After(2 * time.Second):
		t.Fatal("请求取消没有传递给能力")
	}
	<-finished
}

// TestAppMCPRequiresBearerToken 验证 MCP 端点拒绝匿名请求。
func TestAppMCPRequiresBearerToken(t *testing.T) {
	t.Parallel()

	app, err := New(
		Config{Name: "test", Version: "v1"},
		HTTPAuthenticationConfig{Authenticator: staticAuthenticator{}},
		new(audit.MemorySink),
	)
	if err != nil {
		t.Fatalf("创建应用失败：%v", err)
	}
	handler, err := app.Handler()
	if err != nil {
		t.Fatalf("构建 Handler 失败：%v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("匿名请求应返回 401，实际为：%d", response.Code)
	}
}

// TestAppAPIKeyMCPFlowIsStateless 验证纯 API Key 请求之间不保存协议会话。
func TestAppAPIKeyMCPFlowIsStateless(t *testing.T) {
	t.Parallel()

	rawKey := "0123456789abcdef0123456789abcdef"
	store, err := identity.NewMemoryAPIKeyStore(identity.APIKeyRecord{
		CredentialID: "key-1",
		TenantID:     "tenant-a",
		ClientID:     "service-a",
		Digest:       identity.DigestAPIKey(rawKey),
		Roles:        []string{"reader"},
	})
	if err != nil {
		t.Fatalf("创建 Key Store 失败：%v", err)
	}
	authenticator, err := identity.NewAPIKeyAuthenticator(
		store,
		identity.APIKeyAuthenticatorConfig{},
	)
	if err != nil {
		t.Fatalf("创建 API Key 认证器失败：%v", err)
	}
	app, err := New(
		Config{Name: "test", Version: "v1"},
		HTTPAuthenticationConfig{
			Authenticator:    authenticator,
			CredentialHeader: "X-EACG-API-Key",
		},
		new(audit.MemorySink),
	)
	if err != nil {
		t.Fatalf("创建应用失败：%v", err)
	}
	echo, err := capability.New(capability.Descriptor{
		ID:            "echo.v1",
		Name:          "echo",
		Version:       "v1",
		Description:   "返回输入文本",
		RiskLevel:     capability.RiskR0,
		ReadOnly:      true,
		RequiredRoles: []string{"reader"},
	}, func(_ context.Context, _ capability.RequestContext, input echoInput) (echoOutput, error) {
		return echoOutput(input), nil
	})
	if err != nil {
		t.Fatalf("创建能力失败：%v", err)
	}
	if err := app.RegisterCapability(echo); err != nil {
		t.Fatalf("注册能力失败：%v", err)
	}
	handler, err := app.Handler()
	if err != nil {
		t.Fatalf("构建 Handler 失败：%v", err)
	}
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()

	transport := &apiKeyTransport{
		base: http.DefaultTransport,
		key:  rawKey,
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v1"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint:             httpServer.URL + "/mcp",
		HTTPClient:           &http.Client{Transport: transport},
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatalf("连接 API Key MCP Server 失败：%v", err)
	}
	defer session.Close()

	tools, err := session.ListTools(context.Background(), nil)
	if err != nil || len(tools.Tools) != 1 {
		t.Fatalf("查询 API Key Tool 失败：tools=%+v err=%v", tools, err)
	}
	tools, err = session.ListTools(context.Background(), nil)
	if err != nil || len(tools.Tools) != 1 {
		t.Fatalf("后续独立请求应重新认证：tools=%+v err=%v", tools, err)
	}
}
