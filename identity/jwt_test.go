package identity

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// TestJWTAuthenticatorAuthenticate 验证合法和非法 JWT 的处理结果。
func TestJWTAuthenticatorAuthenticate(t *testing.T) {
	t.Parallel()

	secret := []byte("0123456789abcdef0123456789abcdef")
	authenticator, err := NewJWTAuthenticator(JWTConfig{
		Secret:   secret,
		Issuer:   "issuer",
		Audience: "eacg",
	})
	if err != nil {
		t.Fatalf("创建校验器失败：%v", err)
	}

	claims := jwtClaims{
		TenantID: "tenant-a",
		Roles:    []string{"reader"},
		Scope:    "read profile",
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "user-1",
			Issuer:    "issuer",
			Audience:  jwt.ClaimStrings{"eacg"},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}
	raw, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(secret)
	if err != nil {
		t.Fatalf("签发测试令牌失败：%v", err)
	}

	authentication, err := authenticator.Authenticate(context.Background(), AuthenticationRequest{
		Credential: raw,
	})
	if err != nil {
		t.Fatalf("校验合法令牌失败：%v", err)
	}
	if authentication.Principal.UserID != "user-1" ||
		!authentication.Principal.HasRole("reader") {
		t.Fatalf("身份内容不正确：%+v", authentication.Principal)
	}
	if len(authentication.Principal.Scopes) != 2 {
		t.Fatalf("scope 数量不正确：%v", authentication.Principal.Scopes)
	}
	if authentication.SessionBindingID == "" {
		t.Fatal("JWT 认证结果应包含 Session Binding ID")
	}

	_, err = authenticator.Authenticate(context.Background(), AuthenticationRequest{
		Credential: raw + "bad",
	})
	if !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("非法令牌应返回 ErrUnauthenticated，实际为：%v", err)
	}
}

// TestNewJWTAuthenticatorRejectsWeakConfig 验证弱密钥和空配置会被拒绝。
func TestNewJWTAuthenticatorRejectsWeakConfig(t *testing.T) {
	t.Parallel()

	if _, err := NewJWTAuthenticator(JWTConfig{Secret: []byte("short")}); err == nil {
		t.Fatal("弱密钥不应创建成功")
	}
}
