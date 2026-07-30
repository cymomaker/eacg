// 本文件实现 HMAC JWT 认证。
package identity

import (
	"context"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// JWTConfig 定义 HMAC JWT 校验参数。
type JWTConfig struct {
	Secret   []byte
	Issuer   string
	Audience string
}

// JWTAuthenticator 使用 HMAC 密钥认证 JWT。
type JWTAuthenticator struct {
	secret   []byte
	issuer   string
	audience string
}

// jwtClaims 定义 EACG 需要读取的 JWT 字段。
type jwtClaims struct {
	TenantID string            `json:"tenant_id"`
	AgentID  string            `json:"agent_id,omitempty"`
	ClientID string            `json:"client_id,omitempty"`
	Roles    []string          `json:"roles,omitempty"`
	Scope    string            `json:"scope,omitempty"`
	Attrs    map[string]string `json:"attrs,omitempty"`
	jwt.RegisteredClaims
}

// NewJWTAuthenticator 创建 HMAC JWT 认证器。
func NewJWTAuthenticator(config JWTConfig) (*JWTAuthenticator, error) {
	if len(config.Secret) < 32 {
		return nil, fmt.Errorf("JWT 密钥长度不能少于 32 字节")
	}
	if config.Issuer == "" || config.Audience == "" {
		return nil, fmt.Errorf("JWT issuer 和 audience 不能为空")
	}
	return &JWTAuthenticator{
		secret:   append([]byte(nil), config.Secret...),
		issuer:   config.Issuer,
		audience: config.Audience,
	}, nil
}

// Authenticate 校验 JWT 并返回统一认证结果。
func (a *JWTAuthenticator) Authenticate(
	_ context.Context,
	request AuthenticationRequest,
) (Authentication, error) {
	claims := new(jwtClaims)
	parsed, err := jwt.ParseWithClaims(
		request.Credential,
		claims,
		func(token *jwt.Token) (any, error) {
			if token.Method.Alg() != jwt.SigningMethodHS256.Alg() {
				return nil, fmt.Errorf("只允许 HS256")
			}
			return a.secret, nil
		},
		jwt.WithIssuer(a.issuer),
		jwt.WithAudience(a.audience),
		jwt.WithExpirationRequired(),
		jwt.WithLeeway(30*time.Second),
	)
	if err != nil || !parsed.Valid {
		return Authentication{}, ErrUnauthenticated
	}

	principal := Principal{
		SubjectType:  SubjectUser,
		TenantID:     claims.TenantID,
		UserID:       claims.Subject,
		AgentID:      claims.AgentID,
		ClientID:     claims.ClientID,
		AuthMethod:   "bearer",
		CredentialID: "jwt:" + a.issuer,
		Roles:        append([]string(nil), claims.Roles...),
		Scopes:       splitScope(claims.Scope),
		Attrs:        cloneAttrs(claims.Attrs),
	}
	if !principal.Valid() {
		return Authentication{}, ErrUnauthenticated
	}
	return Authentication{
		Principal: principal,
		ExpiresAt: claims.ExpiresAt.Time,
	}, nil
}

// splitScope 按空格拆分 OAuth scope。
func splitScope(scope string) []string {
	var result []string
	start := 0
	for i := 0; i <= len(scope); i++ {
		if i == len(scope) || scope[i] == ' ' {
			if start < i {
				result = append(result, scope[start:i])
			}
			start = i + 1
		}
	}
	return result
}

// cloneAttrs 复制身份属性，避免外部修改内部数据。
func cloneAttrs(attrs map[string]string) map[string]string {
	if attrs == nil {
		return nil
	}
	result := make(map[string]string, len(attrs))
	for key, value := range attrs {
		result[key] = value
	}
	return result
}
