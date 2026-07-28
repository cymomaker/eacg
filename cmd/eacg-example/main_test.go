package main

import (
	"context"
	"testing"

	"github.com/cymomaker/eacg/identity"
)

// TestNewAuthenticationJWT 验证示例默认创建 JWT 认证配置。
func TestNewAuthenticationJWT(t *testing.T) {
	t.Setenv("EACG_AUTH_MODE", "jwt")

	authentication, err := newAuthentication()
	if err != nil {
		t.Fatalf("创建 JWT 认证配置失败：%v", err)
	}
	if authentication.Authenticator == nil ||
		authentication.CredentialHeader != "" ||
		authentication.SubjectHeader != "" {
		t.Fatalf("JWT 认证配置不正确：%+v", authentication)
	}
}

// TestNewAuthenticationAPIKey 验证示例创建可用的 API Key 复合认证配置。
func TestNewAuthenticationAPIKey(t *testing.T) {
	rawKey := "0123456789abcdef0123456789abcdef"
	t.Setenv("EACG_AUTH_MODE", "api_key")
	t.Setenv("EACG_API_KEY", rawKey)

	authentication, err := newAuthentication()
	if err != nil {
		t.Fatalf("创建 API Key 认证配置失败：%v", err)
	}
	result, err := authentication.Authenticator.Authenticate(
		context.Background(),
		identity.AuthenticationRequest{
			Credential: rawKey,
			Subject: &identity.SubjectAssertion{
				Provider:   authentication.SubjectProvider,
				ExternalID: "zhangsan",
			},
		},
	)
	if err != nil {
		t.Fatalf("API Key 示例认证失败：%v", err)
	}
	if result.Principal.UserID != "zhangsan" ||
		!result.Principal.HasRole("reader") {
		t.Fatalf("API Key 示例身份不正确：%+v", result.Principal)
	}
}

// TestNewAuthenticationRejectsInvalidAPIKeyConfig 验证示例拒绝弱 Key 和非法时间。
func TestNewAuthenticationRejectsInvalidAPIKeyConfig(t *testing.T) {
	t.Setenv("EACG_AUTH_MODE", "api_key")
	t.Setenv("EACG_API_KEY", "short")
	if _, err := newAuthentication(); err == nil {
		t.Fatal("弱 API Key 不应创建成功")
	}

	t.Setenv("EACG_API_KEY", "0123456789abcdef0123456789abcdef")
	t.Setenv("EACG_API_KEY_EXPIRES_AT", "not-a-time")
	if _, err := newAuthentication(); err == nil {
		t.Fatal("非法 API Key 过期时间不应创建成功")
	}
}
