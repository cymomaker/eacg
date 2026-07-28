package eacg

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/cymomaker/eacg/audit"
	"github.com/cymomaker/eacg/capability"
	"github.com/cymomaker/eacg/identity"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type staticAuthenticator struct {
	principal identity.Principal
}

// Authenticate 为应用测试返回固定身份。
func (a staticAuthenticator) Authenticate(
	_ context.Context,
	_ identity.AuthenticationRequest,
) (identity.Authentication, error) {
	principal := a.principal
	if !principal.Valid() {
		principal = identity.Principal{TenantID: "tenant-a", UserID: "user-1"}
	}
	return identity.Authentication{
		Principal:        principal,
		CredentialID:     "test-credential",
		SessionBindingID: principal.TenantID + ":" + principal.UserID,
		ExpiresAt:        time.Now().Add(time.Hour),
	}, nil
}

type bearerTransport struct {
	base  http.RoundTripper
	token string
}

type apiKeyTransport struct {
	base http.RoundTripper
	key  string
	mu   sync.RWMutex
	user string
}

// RoundTrip 为 MCP 请求增加固定 API Key 和当前用户 Header。
func (t *apiKeyTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	t.mu.RLock()
	user := t.user
	t.mu.RUnlock()
	cloned := request.Clone(request.Context())
	cloned.Header.Set("X-EACG-API-Key", t.key)
	cloned.Header.Set("X-EACG-Requester-UserID", user)
	return t.base.RoundTrip(cloned)
}

// SetUser 修改后续请求携带的外部用户标识。
func (t *apiKeyTransport) SetUser(user string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.user = user
}

type appSubjectResolver struct{}

// Resolve 为应用集成测试映射企业用户。
func (appSubjectResolver) Resolve(
	_ context.Context,
	request identity.SubjectResolveRequest,
) (identity.Subject, error) {
	return identity.Subject{
		UserID:            request.ExternalID,
		Roles:             []string{"reader"},
		PermissionVersion: "p1",
	}, nil
}

// RoundTrip 为 MCP 测试请求增加 Bearer Token。
func (t bearerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	cloned := request.Clone(request.Context())
	cloned.Header.Set("Authorization", "Bearer "+t.token)
	return t.base.RoundTrip(cloned)
}

type echoInput struct {
	Message string `json:"message"`
}

type echoOutput struct {
	Message string `json:"message"`
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

	app, err := New(
		Config{Name: "test", Version: "v1"},
		HTTPAuthenticationConfig{
			Authenticator: staticAuthenticator{
				principal: identity.Principal{
					TenantID: "tenant-a",
					UserID:   "user-1",
					Roles:    []string{"reader"},
				},
			},
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

// TestAppAPIKeyMCPFlowAndSessionBinding 验证 API Key 双身份流程和 Session 用户绑定。
func TestAppAPIKeyMCPFlowAndSessionBinding(t *testing.T) {
	t.Parallel()

	rawKey := "0123456789abcdef0123456789abcdef"
	store, err := identity.NewMemoryAPIKeyStore(identity.APIKeyRecord{
		CredentialID:    "key-1",
		TenantID:        "tenant-a",
		ClientID:        "wecom-bot",
		Digest:          identity.DigestAPIKey(rawKey),
		SubjectProvider: "wecom",
		AllowedRoles:    []string{"reader"},
		Version:         "v1",
	})
	if err != nil {
		t.Fatalf("创建 Key Store 失败：%v", err)
	}
	authenticator, err := identity.NewAPIKeyAuthenticator(
		store,
		appSubjectResolver{},
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
			SubjectHeader:    "X-EACG-Requester-UserID",
			SubjectProvider:  "wecom",
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
		user: "user-1",
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
	transport.SetUser("user-2")
	if _, err := session.ListTools(context.Background(), nil); err == nil {
		t.Fatal("更换用户后复用 Session 应失败")
	}
}
