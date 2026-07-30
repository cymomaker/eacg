// 本文件验证客户端配置和真实 EACG 调用流程。
package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cymomaker/eacg"
	"github.com/cymomaker/eacg/audit"
	"github.com/cymomaker/eacg/capability"
	"github.com/cymomaker/eacg/identity"
	"github.com/golang-jwt/jwt/v5"
)

// clientProfileInput 保存集成测试 Tool 的输入。
type clientProfileInput struct {
	UserID string `json:"user_id"`
}

// clientProfileOutput 保存集成测试 Tool 的输出。
type clientProfileOutput struct {
	UserID string `json:"user_id"`
	Name   string `json:"name"`
}

// TestLoadConfigUsesFlagsOverEnvironment 验证显式 Flag 会覆盖环境变量。
func TestLoadConfigUsesFlagsOverEnvironment(t *testing.T) {
	t.Parallel()

	environment := map[string]string{
		"EACG_CLIENT_ENDPOINT":  "http://environment.test/mcp",
		"EACG_CLIENT_AUTH_MODE": "jwt",
		"EACG_CLIENT_TOKEN":     "environment-token",
	}
	config, err := loadConfig([]string{
		"--endpoint", "http://flag.test/mcp",
		"--token", "flag-token",
		"--action", "list",
		"--trace=false",
	}, func(key string) string {
		return environment[key]
	})
	if err != nil {
		t.Fatalf("加载配置失败：%v", err)
	}
	if config.endpoint != "http://flag.test/mcp" ||
		config.token != "flag-token" ||
		config.action != "list" ||
		config.trace {
		t.Fatalf("Flag 没有正确覆盖环境变量：%+v", config)
	}
}

// TestLoadConfigRejectsInvalidValues 验证不安全或不完整配置会被拒绝。
func TestLoadConfigRejectsInvalidValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
	}{
		{name: "JWT 缺失", args: []string{"--auth-mode", "jwt"}},
		{name: "API Key 缺失", args: []string{"--auth-mode", "api_key"}},
		{name: "非法地址", args: []string{"--token", "token", "--endpoint", "http://user@example.test/mcp"}},
		{name: "地址包含 Query", args: []string{"--token", "token", "--endpoint", "http://example.test/mcp?token=x"}},
		{name: "非法动作", args: []string{"--token", "token", "--action", "unknown"}},
		{name: "Header 冲突", args: []string{
			"--auth-mode", "api_key",
			"--api-key", "key",
			"--requester-user", "user",
			"--credential-header", "X-Same",
			"--subject-header", "X-Same",
		}},
		{name: "参数不是对象", args: []string{"--token", "token", "--arguments", "[]"}},
		{name: "超时无效", args: []string{"--token", "token", "--timeout", "0s"}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			if _, err := loadConfig(test.args, func(string) string { return "" }); err == nil {
				t.Fatal("非法配置不应加载成功")
			}
		})
	}
}

// TestLoadConfigAllowsAPIKeyWithoutRequester 验证服务身份客户端不要求 userid。
func TestLoadConfigAllowsAPIKeyWithoutRequester(t *testing.T) {
	t.Parallel()

	config, err := loadConfig([]string{
		"--auth-mode", "api_key",
		"--api-key", "service-key",
	}, func(string) string { return "" })
	if err != nil {
		t.Fatalf("纯 API Key 配置应加载成功：%v", err)
	}
	if config.requesterUser != "" {
		t.Fatalf("纯服务配置不应自动生成用户：%q", config.requesterUser)
	}
}

// TestClientFlowWithJWT 验证 JWT 客户端完成发现、列表和调用。
func TestClientFlowWithJWT(t *testing.T) {
	t.Parallel()

	const secret = "0123456789abcdef0123456789abcdef"
	authenticator, err := identity.NewJWTAuthenticator(identity.JWTConfig{
		Secret:   []byte(secret),
		Issuer:   "client-test",
		Audience: "eacg",
	})
	if err != nil {
		t.Fatalf("创建 JWT 认证器失败：%v", err)
	}
	server := newClientTestServer(t, eacg.HTTPAuthenticationConfig{Authenticator: authenticator})
	token := newClientTestToken(t, secret)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := execute(
		context.Background(),
		[]string{"--endpoint", server.URL + "/mcp", "--token", token},
		func(string) string { return "" },
		&stdout,
		&stderr,
		nil,
	)
	if code != 0 {
		t.Fatalf("JWT 完整流程失败：%s", stderr.String())
	}
	if !strings.Contains(stdout.String(), `"get_profile"`) ||
		!strings.Contains(stdout.String(), `"user-1001"`) {
		t.Fatalf("客户端输出不完整：%s", stdout.String())
	}
	trace := stderr.String()
	if strings.Contains(trace, token) {
		t.Fatal("JWT 不应出现在协议追踪中")
	}
	if strings.Contains(trace, `"roots"`) ||
		strings.Contains(trace, `"sampling"`) ||
		strings.Contains(trace, `"logging"`) {
		t.Fatalf("Client 不应声明已废弃能力：%s", trace)
	}
}

// TestClientFlowWithAPIKey 验证纯 API Key 客户端完成完整流程。
func TestClientFlowWithAPIKey(t *testing.T) {
	t.Parallel()

	const rawKey = "0123456789abcdef0123456789abcdef"
	store, err := identity.NewMemoryAPIKeyStore(identity.APIKeyRecord{
		CredentialID: "client-key",
		TenantID:     "tenant-a",
		ClientID:     "client-test",
		Digest:       identity.DigestAPIKey(rawKey),
		Roles:        []string{"reader"},
	})
	if err != nil {
		t.Fatalf("创建 API Key Store 失败：%v", err)
	}
	authenticator, err := identity.NewAPIKeyAuthenticator(
		store,
		identity.APIKeyAuthenticatorConfig{},
	)
	if err != nil {
		t.Fatalf("创建 API Key 认证器失败：%v", err)
	}
	server := newClientTestServer(t, eacg.HTTPAuthenticationConfig{
		Authenticator:    authenticator,
		CredentialHeader: "X-EACG-API-Key",
	})
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := execute(
		context.Background(),
		[]string{
			"--endpoint", server.URL + "/mcp",
			"--auth-mode", "api_key",
			"--api-key", rawKey,
		},
		func(string) string { return "" },
		&stdout,
		&stderr,
		nil,
	)
	if code != 0 {
		t.Fatalf("API Key 完整流程失败：%s", stderr.String())
	}
	if strings.Contains(stderr.String(), rawKey) {
		t.Fatal("API Key 不应出现在追踪中")
	}
}

// TestClientTraceCanBeDisabled 验证关闭追踪后不输出 HTTP 报文。
func TestClientTraceCanBeDisabled(t *testing.T) {
	t.Parallel()

	const secret = "0123456789abcdef0123456789abcdef"
	authenticator, err := identity.NewJWTAuthenticator(identity.JWTConfig{
		Secret:   []byte(secret),
		Issuer:   "client-test",
		Audience: "eacg",
	})
	if err != nil {
		t.Fatalf("创建 JWT 认证器失败：%v", err)
	}
	server := newClientTestServer(t, eacg.HTTPAuthenticationConfig{Authenticator: authenticator})
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := execute(
		context.Background(),
		[]string{
			"--endpoint", server.URL + "/mcp",
			"--token", newClientTestToken(t, secret),
			"--action", "discover",
			"--trace=false",
		},
		func(string) string { return "" },
		&stdout,
		&stderr,
		nil,
	)
	if code != 0 {
		t.Fatalf("关闭追踪后调用失败：%s", stderr.String())
	}
	if strings.Contains(stderr.String(), ">>>") || strings.Contains(stderr.String(), `"jsonrpc"`) {
		t.Fatalf("关闭追踪后不应输出协议报文：%s", stderr.String())
	}
}

// TestClientReturnsFailureForUnknownTool 验证调用不存在的 Tool 时返回失败退出码。
func TestClientReturnsFailureForUnknownTool(t *testing.T) {
	t.Parallel()

	const secret = "0123456789abcdef0123456789abcdef"
	authenticator, err := identity.NewJWTAuthenticator(identity.JWTConfig{
		Secret:   []byte(secret),
		Issuer:   "client-test",
		Audience: "eacg",
	})
	if err != nil {
		t.Fatalf("创建 JWT 认证器失败：%v", err)
	}
	server := newClientTestServer(t, eacg.HTTPAuthenticationConfig{Authenticator: authenticator})
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := execute(
		context.Background(),
		[]string{
			"--endpoint", server.URL + "/mcp",
			"--token", newClientTestToken(t, secret),
			"--action", "call",
			"--tool", "missing_tool",
			"--trace=false",
		},
		func(string) string { return "" },
		&stdout,
		&stderr,
		nil,
	)
	if code == 0 {
		t.Fatalf("调用不存在的 Tool 不应成功：%s", stdout.String())
	}
}

// TestClientReturnsFailureOnTimeout 验证请求超过整体超时时间后返回失败。
func TestClientReturnsFailureOnTimeout(t *testing.T) {
	t.Parallel()

	base := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		<-request.Context().Done()
		return nil, request.Context().Err()
	})
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := execute(
		context.Background(),
		[]string{
			"--endpoint", "http://timeout.test/mcp",
			"--token", "test-token",
			"--action", "discover",
			"--timeout", "20ms",
			"--trace=false",
		},
		func(string) string { return "" },
		&stdout,
		&stderr,
		base,
	)
	if code == 0 {
		t.Fatal("请求超时后不应返回成功")
	}
}

// newClientTestServer 创建真实 EACG Handler 的测试服务。
func newClientTestServer(
	t *testing.T,
	authentication eacg.HTTPAuthenticationConfig,
) *httptest.Server {
	t.Helper()
	app, err := eacg.New(
		eacg.Config{Name: "client-test", Version: "v0.2.0"},
		authentication,
		new(audit.MemorySink),
	)
	if err != nil {
		t.Fatalf("创建 EACG 应用失败：%v", err)
	}
	item, err := capability.New(capability.Descriptor{
		ID:            "get_profile.v1",
		Name:          "get_profile",
		Version:       "v1",
		Description:   "查询测试用户",
		RiskLevel:     capability.RiskR1,
		ReadOnly:      true,
		Idempotent:    true,
		RequiredRoles: []string{"reader"},
	}, func(
		_ context.Context,
		_ capability.RequestContext,
		input clientProfileInput,
	) (clientProfileOutput, error) {
		return clientProfileOutput{UserID: input.UserID, Name: "测试用户"}, nil
	})
	if err != nil {
		t.Fatalf("创建 Capability 失败：%v", err)
	}
	if err := app.RegisterCapability(item); err != nil {
		t.Fatalf("注册 Capability 失败：%v", err)
	}
	handler, err := app.Handler()
	if err != nil {
		t.Fatalf("创建 Handler 失败：%v", err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server
}

// newClientTestToken 创建 JWT 集成测试令牌。
func newClientTestToken(t *testing.T, secret string) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"tenant_id": "tenant-a",
		"sub":       "user-1",
		"roles":     []string{"reader"},
		"iss":       "client-test",
		"aud":       "eacg",
		"exp":       time.Now().Add(time.Hour).Unix(),
	})
	raw, err := token.SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("签名测试 JWT 失败：%v", err)
	}
	return raw
}
