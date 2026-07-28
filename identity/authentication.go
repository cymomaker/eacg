package identity

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
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

// Authentication 保存认证后的企业身份和会话绑定信息。
type Authentication struct {
	Principal        Principal
	CredentialID     string
	SessionBindingID string
	ExpiresAt        time.Time
}

// Valid 检查认证结果是否可以安全用于 MCP 请求。
func (a Authentication) Valid(now time.Time) bool {
	return a.Principal.Valid() &&
		a.CredentialID != "" &&
		a.SessionBindingID != "" &&
		!a.ExpiresAt.IsZero() &&
		a.ExpiresAt.After(now)
}

// Authenticator 定义统一的身份认证行为。
type Authenticator interface {
	Authenticate(context.Context, AuthenticationRequest) (Authentication, error)
}

type sessionBindingInput struct {
	TenantID          string   `json:"tenant_id"`
	ClientID          string   `json:"client_id"`
	AgentID           string   `json:"agent_id"`
	UserID            string   `json:"user_id"`
	CredentialID      string   `json:"credential_id"`
	CredentialVersion string   `json:"credential_version"`
	PermissionVersion string   `json:"permission_version"`
	Roles             []string `json:"roles"`
	Scopes            []string `json:"scopes"`
}

// buildSessionBinding 根据安全身份字段生成不可逆的会话绑定标识。
func buildSessionBinding(input sessionBindingInput) (string, error) {
	input.Roles = sortedUnique(input.Roles)
	input.Scopes = sortedUnique(input.Scopes)
	raw, err := json.Marshal(input)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// sortedUnique 返回去重并排序后的字符串切片。
func sortedUnique(values []string) []string {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value != "" {
			set[value] = struct{}{}
		}
	}
	result := make([]string, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
