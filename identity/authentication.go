// 本文件定义传输无关的统一认证接口。
package identity

import (
	"context"
	"time"
)

// SubjectAssertion 保存外部系统传入的用户身份声明。
type SubjectAssertion struct {
	Provider   string
	ExternalID string
}

// AuthenticationRequest 保存一次认证需要的凭据和用户声明。
type AuthenticationRequest struct {
	Credential string
	Subject    *SubjectAssertion
}

// Authentication 保存认证后的企业身份和有效期。
type Authentication struct {
	Principal Principal
	ExpiresAt time.Time
}

// Valid 检查认证结果是否可以安全用于 MCP 请求。
func (a Authentication) Valid(now time.Time) bool {
	return a.Principal.Valid() &&
		a.Principal.AuthMethod != "" &&
		a.Principal.CredentialID != "" &&
		!a.ExpiresAt.IsZero() &&
		a.ExpiresAt.After(now)
}

// Authenticator 定义统一的身份认证行为。
type Authenticator interface {
	Authenticate(context.Context, AuthenticationRequest) (Authentication, error)
}
