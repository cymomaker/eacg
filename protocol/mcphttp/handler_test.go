package mcphttp

import (
	"bytes"
	"context"
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
		Principal:        identity.Principal{TenantID: "tenant-a", UserID: "user-1"},
		CredentialID:     "test-bearer",
		SessionBindingID: "tenant-a:user-1",
		ExpiresAt:        time.Now().Add(time.Hour),
	}, nil
}

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
			TenantID:        "tenant-a",
			UserID:          request.Subject.ExternalID,
			AuthMethod:      "api_key",
			CredentialID:    "key-1",
			SubjectProvider: "wecom",
		},
		CredentialID:     "key-1",
		SessionBindingID: "binding-" + request.Subject.ExternalID,
		ExpiresAt:        time.Now().Add(time.Hour),
	}, nil
}

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

	request := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("X-EACG-API-Key", "valid-key")
	request.Header.Set("X-EACG-Requester-UserID", "user-1")
	response := httptest.NewRecorder()
	newAPIKeyTestHandler(t).ServeHTTP(response, request)
	if response.Code == http.StatusUnauthorized {
		t.Fatalf("合法自定义认证不应返回 401：%s", response.Body.String())
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
