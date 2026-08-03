// 本文件验证 MCP 双协议 HTTP 兼容和安全中间件。
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
	header.Set("Mcp-Protocol-Version", ProtocolVersion20260728)
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
	return newTestHandlerWithVersions(t, origins, nil)
}

// newTestHandlerWithVersions 创建指定协议版本的测试 Handler。
func newTestHandlerWithVersions(
	t *testing.T,
	origins []string,
	versions []string,
) http.Handler {
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
		Authenticator:    bearerTestAuthenticator{},
		AllowedOrigins:   origins,
		ProtocolVersions: versions,
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
	request.Header.Set("Mcp-Protocol-Version", ProtocolVersion20260728)
	request.Header.Set("Mcp-Method", "server/discover")
	request.Header.Set("X-EACG-API-Key", "valid-key")
	request.Header.Set("X-EACG-Requester-UserID", "user-1")
	response := httptest.NewRecorder()
	newAPIKeyTestHandler(t).ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("合法自定义认证应返回 200：code=%d body=%s", response.Code, response.Body.String())
	}
}

// TestHandlerAdvertisesDefaultProtocols 验证服务默认声明新旧两个协议。
func TestHandlerAdvertisesDefaultProtocols(t *testing.T) {
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
		len(document.Result.SupportedVersions) != 2 ||
		document.Result.SupportedVersions[0] != ProtocolVersion20260728 ||
		document.Result.SupportedVersions[1] != ProtocolVersion20250618 {
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

// TestHandlerSupportsWeComLegacyFlow 验证企业微信 2025-06-18 无状态流程。
func TestHandlerSupportsWeComLegacyFlow(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, nil)
	initialize := `{"method":"initialize","params":{"protocolVersion":"2025-06-18",` +
		`"capabilities":{},"clientInfo":{"name":"wework-mcp-client","version":"1.0.0"}},` +
		`"jsonrpc":"2.0","id":0}`
	request := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(initialize))
	setLegacyHeaders(request.Header, false)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK ||
		!strings.Contains(response.Body.String(), `"protocolVersion":"2025-06-18"`) {
		t.Fatalf("旧版 initialize 失败：code=%d body=%s", response.Code, response.Body.String())
	}
	if response.Header().Get("Mcp-Session-Id") != "" {
		t.Fatal("旧版无状态响应不能返回 Mcp-Session-Id")
	}

	initialized := `{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}`
	request = httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(initialized))
	setLegacyHeaders(request.Header, true)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("旧版 initialized 失败：code=%d body=%s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(
		http.MethodPost,
		"/mcp",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`),
	)
	setLegacyHeaders(request.Header, true)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"tools"`) {
		t.Fatalf("旧版 tools/list 失败：code=%d body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"cacheScope":"private"`) {
		t.Fatalf("旧版 Tool 列表必须使用私有缓存：%s", response.Body.String())
	}

	request = httptest.NewRequest(
		http.MethodPost,
		"/mcp",
		strings.NewReader(`{"jsonrpc":"2.0","id":2,"method":"ping","params":{}}`),
	)
	setLegacyHeaders(request.Header, true)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("旧版 ping 失败：code=%d body=%s", response.Code, response.Body.String())
	}
}

// setLegacyHeaders 设置企业微信旧协议请求头。
func setLegacyHeaders(header http.Header, includeVersion bool) {
	header.Set("Authorization", "Bearer valid")
	header.Set("Content-Type", "application/json")
	header.Set("Accept", "application/json, text/event-stream")
	if includeVersion {
		header.Set("Mcp-Protocol-Version", ProtocolVersion20250618)
	}
}

// TestHandlerHonorsConfiguredProtocols 验证单协议部署会拒绝未启用版本。
func TestHandlerHonorsConfiguredProtocols(t *testing.T) {
	t.Parallel()

	initialize := `{"jsonrpc":"2.0","id":0,"method":"initialize","params":{` +
		`"protocolVersion":"2025-06-18","capabilities":{},` +
		`"clientInfo":{"name":"wecom","version":"1.0.0"}}}`
	request := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(initialize))
	setLegacyHeaders(request.Header, false)
	response := httptest.NewRecorder()
	newTestHandlerWithVersions(t, nil, []string{ProtocolVersion20260728}).ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest ||
		!strings.Contains(response.Body.String(), ProtocolVersion20260728) ||
		strings.Contains(response.Body.String(), `"supported":["2025-06-18"`) {
		t.Fatalf("新版单协议应拒绝旧版：code=%d body=%s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(
		http.MethodPost,
		"/mcp",
		strings.NewReader(validProtocolBody("server/discover", "")),
	)
	setProtocolHeaders(request.Header, "server/discover")
	response = httptest.NewRecorder()
	newTestHandlerWithVersions(t, nil, []string{ProtocolVersion20250618}).ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest ||
		!strings.Contains(response.Body.String(), `"supported":["2025-06-18"]`) {
		t.Fatalf("旧版单协议应拒绝新版：code=%d body=%s", response.Code, response.Body.String())
	}
}

// TestNewRejectsInvalidProtocolVersions 验证非法和重复协议配置会失败。
func TestNewRejectsInvalidProtocolVersions(t *testing.T) {
	t.Parallel()

	for _, versions := range [][]string{
		{},
		{"2025-11-25"},
		{ProtocolVersion20250618, ProtocolVersion20250618},
		{""},
	} {
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
			Authenticator:    bearerTestAuthenticator{},
			ProtocolVersions: versions,
		})
		if err == nil {
			t.Fatalf("非法协议配置应失败：%v", versions)
		}
	}
}

// TestHandlerRejectsInvalidLegacyRequests 验证旧版只允许指定方法并校验初始化版本。
func TestHandlerRejectsInvalidLegacyRequests(t *testing.T) {
	t.Parallel()

	conflict := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{`+
			`"protocolVersion":"2026-07-28","capabilities":{},`+
			`"clientInfo":{"name":"test","version":"1.0.0"}}}`,
	))
	setLegacyHeaders(conflict.Header, true)
	conflictResponse := httptest.NewRecorder()
	newTestHandler(t, nil).ServeHTTP(conflictResponse, conflict)
	if conflictResponse.Code != http.StatusBadRequest {
		t.Fatalf("旧版 Header/Body 版本冲突应返回 400，实际 %d：%s", conflictResponse.Code, conflictResponse.Body.String())
	}

	forbidden := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(
		`{"jsonrpc":"2.0","id":2,"method":"resources/list","params":{}}`,
	))
	setLegacyHeaders(forbidden.Header, true)
	forbiddenResponse := httptest.NewRecorder()
	newTestHandler(t, nil).ServeHTTP(forbiddenResponse, forbidden)
	if forbiddenResponse.Code != http.StatusBadRequest ||
		!strings.Contains(forbiddenResponse.Body.String(), `"code":-32601`) {
		t.Fatalf("旧版未允许方法应返回 Method Not Found，实际 %d：%s", forbiddenResponse.Code, forbiddenResponse.Body.String())
	}
}

// TestHandlerLimitsLegacyInitializeBody 验证旧版初始化也受正文大小限制。
func TestHandlerLimitsLegacyInitializeBody(t *testing.T) {
	t.Parallel()

	store := registry.New()
	engine, err := execution.New(store, new(audit.MemorySink), execution.Config{})
	if err != nil {
		t.Fatalf("创建执行引擎失败：%v", err)
	}
	handler, err := New(Config{
		Name:                "test",
		Version:             "v1",
		Registry:            store,
		Engine:              engine,
		Authenticator:       bearerTestAuthenticator{},
		MaxRequestBodyBytes: 64,
	})
	if err != nil {
		t.Fatalf("创建 Handler 失败：%v", err)
	}
	request := httptest.NewRequest(
		http.MethodPost,
		"/mcp",
		strings.NewReader(`{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2025-06-18","padding":"`+strings.Repeat("x", 128)+`"}}`),
	)
	setLegacyHeaders(request.Header, false)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("超大旧版初始化应返回 413：code=%d body=%s", response.Code, response.Body.String())
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
				header.Add("Mcp-Protocol-Version", ProtocolVersion20260728)
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
