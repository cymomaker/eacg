// Package identity 定义调用者身份和令牌校验接口。
package identity

import (
	"errors"
)

// ErrUnauthenticated 表示调用方身份无法通过认证。
var ErrUnauthenticated = errors.New("身份认证失败")

// SubjectType 表示调用者是用户还是服务。
type SubjectType string

// 身份类型用于区分真实用户和系统服务。
const (
	// SubjectUser 表示由真实用户发起调用。
	SubjectUser SubjectType = "user"
	// SubjectService 表示由应用或服务发起调用。
	SubjectService SubjectType = "service"
)

// Principal 表示当前调用者的企业身份。
type Principal struct {
	SubjectType     SubjectType       `json:"subject_type"`
	TenantID        string            `json:"tenant_id"`
	UserID          string            `json:"user_id,omitempty"`
	AgentID         string            `json:"agent_id,omitempty"`
	ClientID        string            `json:"client_id,omitempty"`
	AuthMethod      string            `json:"auth_method,omitempty"`
	CredentialID    string            `json:"credential_id,omitempty"`
	SubjectProvider string            `json:"subject_provider,omitempty"`
	Roles           []string          `json:"roles,omitempty"`
	Scopes          []string          `json:"scopes,omitempty"`
	Attrs           map[string]string `json:"attrs,omitempty"`
}

// Valid 检查用户或服务身份是否包含必要字段。
func (p Principal) Valid() bool {
	if p.TenantID == "" {
		return false
	}
	switch p.SubjectType {
	case SubjectUser:
		return p.UserID != ""
	case SubjectService:
		return p.ClientID != ""
	default:
		return false
	}
}

// IsUser 检查当前身份是否为真实用户。
func (p Principal) IsUser() bool {
	return p.SubjectType == SubjectUser && p.TenantID != "" && p.UserID != ""
}

// IsService 检查当前身份是否为系统服务。
func (p Principal) IsService() bool {
	return p.SubjectType == SubjectService && p.TenantID != "" && p.ClientID != ""
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
