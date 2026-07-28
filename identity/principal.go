// Package identity 定义调用者身份和令牌校验接口。
package identity

import (
	"errors"
)

// ErrUnauthenticated 表示调用方身份无法通过认证。
var ErrUnauthenticated = errors.New("身份认证失败")

// Principal 表示当前调用者的企业身份。
type Principal struct {
	TenantID        string            `json:"tenant_id"`
	UserID          string            `json:"user_id"`
	AgentID         string            `json:"agent_id,omitempty"`
	ClientID        string            `json:"client_id,omitempty"`
	AuthMethod      string            `json:"auth_method,omitempty"`
	CredentialID    string            `json:"credential_id,omitempty"`
	SubjectProvider string            `json:"subject_provider,omitempty"`
	Roles           []string          `json:"roles,omitempty"`
	Scopes          []string          `json:"scopes,omitempty"`
	Attrs           map[string]string `json:"attrs,omitempty"`
}

// Valid 检查身份是否包含 MVP 必需字段。
func (p Principal) Valid() bool {
	return p.TenantID != "" && p.UserID != ""
}

// HasRole 检查调用者是否拥有指定角色。
func (p Principal) HasRole(role string) bool {
	for _, item := range p.Roles {
		if item == role {
			return true
		}
	}
	return false
}

// HasAllRoles 检查调用者是否拥有全部角色。
func (p Principal) HasAllRoles(roles []string) bool {
	for _, role := range roles {
		if !p.HasRole(role) {
			return false
		}
	}
	return true
}
