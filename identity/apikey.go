package identity

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

// ErrAPIKeyNotFound 表示没有找到对应的 API Key。
var ErrAPIKeyNotFound = errors.New("API Key 不存在")

// ErrSubjectNotFound 表示没有找到对应的企业用户。
var ErrSubjectNotFound = errors.New("企业用户不存在")

const (
	defaultAuthenticationLease = 5 * time.Minute
	maxCredentialLength        = 8192
)

// APIKeyRecord 保存 API Key 对应的服务身份和最大权限。
type APIKeyRecord struct {
	CredentialID    string
	TenantID        string
	ClientID        string
	AgentID         string
	Digest          [32]byte
	SubjectProvider string
	AllowedRoles    []string
	AllowedScopes   []string
	Version         string
	Disabled        bool
	ExpiresAt       time.Time
}

// APIKeyStore 定义 API Key 摘要查询行为。
type APIKeyStore interface {
	LookupByDigest(context.Context, [32]byte) (APIKeyRecord, error)
}

// SubjectResolveRequest 保存用户目录查询需要的信息。
type SubjectResolveRequest struct {
	TenantID   string
	ClientID   string
	AgentID    string
	Provider   string
	ExternalID string
}

// Subject 保存企业用户目录返回的内部身份。
type Subject struct {
	UserID            string
	Roles             []string
	Scopes            []string
	Attrs             map[string]string
	PermissionVersion string
	Disabled          bool
}

// SubjectResolver 定义外部用户到企业内部身份的映射行为。
type SubjectResolver interface {
	Resolve(context.Context, SubjectResolveRequest) (Subject, error)
}

// APIKeyAuthenticatorConfig 定义 API Key 认证器参数。
type APIKeyAuthenticatorConfig struct {
	AuthenticationLease time.Duration
}

// APIKeyAuthenticator 组合服务凭据和企业用户身份。
type APIKeyAuthenticator struct {
	store    APIKeyStore
	resolver SubjectResolver
	lease    time.Duration
	now      func() time.Time
}

// NewAPIKeyAuthenticator 创建 API Key 复合身份认证器。
func NewAPIKeyAuthenticator(
	store APIKeyStore,
	resolver SubjectResolver,
	config APIKeyAuthenticatorConfig,
) (*APIKeyAuthenticator, error) {
	if store == nil || resolver == nil {
		return nil, fmt.Errorf("API Key Store 和 Subject Resolver 不能为空")
	}
	if config.AuthenticationLease <= 0 {
		config.AuthenticationLease = defaultAuthenticationLease
	}
	return &APIKeyAuthenticator{
		store:    store,
		resolver: resolver,
		lease:    config.AuthenticationLease,
		now:      time.Now,
	}, nil
}

// Authenticate 校验服务凭据并解析实际企业用户。
func (a *APIKeyAuthenticator) Authenticate(
	ctx context.Context,
	request AuthenticationRequest,
) (Authentication, error) {
	if !validCredential(request.Credential) || request.Subject == nil {
		return Authentication{}, ErrUnauthenticated
	}
	provider := request.Subject.Provider
	externalID := request.Subject.ExternalID
	if provider != strings.TrimSpace(provider) ||
		externalID != strings.TrimSpace(externalID) ||
		!validIdentityValue(provider, 128) ||
		!validIdentityValue(externalID, 256) {
		return Authentication{}, ErrUnauthenticated
	}

	record, err := a.store.LookupByDigest(ctx, DigestAPIKey(request.Credential))
	if err != nil {
		if errors.Is(err, ErrAPIKeyNotFound) {
			return Authentication{}, ErrUnauthenticated
		}
		return Authentication{}, fmt.Errorf("查询 API Key：%w", err)
	}
	now := a.now()
	if err := validateAPIKeyRecord(record, provider, now); err != nil {
		return Authentication{}, ErrUnauthenticated
	}

	subject, err := a.resolver.Resolve(ctx, SubjectResolveRequest{
		TenantID:   record.TenantID,
		ClientID:   record.ClientID,
		AgentID:    record.AgentID,
		Provider:   provider,
		ExternalID: externalID,
	})
	if err != nil {
		if errors.Is(err, ErrSubjectNotFound) {
			return Authentication{}, ErrUnauthenticated
		}
		return Authentication{}, fmt.Errorf("解析企业用户：%w", err)
	}
	if subject.Disabled || !validIdentityValue(subject.UserID, 256) {
		return Authentication{}, ErrUnauthenticated
	}

	roles := intersect(record.AllowedRoles, subject.Roles)
	scopes := intersect(record.AllowedScopes, subject.Scopes)
	principal := Principal{
		TenantID:        record.TenantID,
		UserID:          subject.UserID,
		AgentID:         record.AgentID,
		ClientID:        record.ClientID,
		AuthMethod:      "api_key",
		CredentialID:    record.CredentialID,
		SubjectProvider: provider,
		Roles:           roles,
		Scopes:          scopes,
		Attrs:           cloneAttrs(subject.Attrs),
	}
	bindingID, err := buildSessionBinding(sessionBindingInput{
		TenantID:          principal.TenantID,
		ClientID:          principal.ClientID,
		AgentID:           principal.AgentID,
		UserID:            principal.UserID,
		CredentialID:      record.CredentialID,
		CredentialVersion: record.Version,
		PermissionVersion: subject.PermissionVersion,
		Roles:             roles,
		Scopes:            scopes,
	})
	if err != nil {
		return Authentication{}, fmt.Errorf("生成会话绑定标识：%w", err)
	}

	expiresAt := now.Add(a.lease)
	if !record.ExpiresAt.IsZero() && record.ExpiresAt.Before(expiresAt) {
		expiresAt = record.ExpiresAt
	}
	return Authentication{
		Principal:        principal,
		CredentialID:     record.CredentialID,
		SessionBindingID: bindingID,
		ExpiresAt:        expiresAt,
	}, nil
}

// DigestAPIKey 计算 API Key 的固定长度摘要。
func DigestAPIKey(raw string) [32]byte {
	return sha256.Sum256([]byte(raw))
}

// MemoryAPIKeyStore 在内存中按摘要保存 API Key，适合测试和示例。
type MemoryAPIKeyStore struct {
	mu      sync.RWMutex
	records map[[32]byte]APIKeyRecord
}

// NewMemoryAPIKeyStore 创建只保存摘要副本的内存 Key Store。
func NewMemoryAPIKeyStore(records ...APIKeyRecord) (*MemoryAPIKeyStore, error) {
	store := &MemoryAPIKeyStore{records: make(map[[32]byte]APIKeyRecord, len(records))}
	for _, record := range records {
		if err := store.Put(record); err != nil {
			return nil, err
		}
	}
	return store, nil
}

// Put 新增或替换一条 API Key 记录。
func (s *MemoryAPIKeyStore) Put(record APIKeyRecord) error {
	if record.CredentialID == "" || record.TenantID == "" ||
		record.ClientID == "" || record.SubjectProvider == "" {
		return fmt.Errorf("API Key 记录缺少必要身份字段")
	}
	if record.Digest == ([32]byte{}) {
		return fmt.Errorf("API Key 摘要不能为空")
	}
	cloned := record
	cloned.AllowedRoles = append([]string(nil), record.AllowedRoles...)
	cloned.AllowedScopes = append([]string(nil), record.AllowedScopes...)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records[record.Digest] = cloned
	return nil
}

// LookupByDigest 根据摘要查询 API Key 记录。
func (s *MemoryAPIKeyStore) LookupByDigest(_ context.Context, digest [32]byte) (APIKeyRecord, error) {
	s.mu.RLock()
	record, ok := s.records[digest]
	s.mu.RUnlock()
	if !ok {
		return APIKeyRecord{}, ErrAPIKeyNotFound
	}
	record.AllowedRoles = append([]string(nil), record.AllowedRoles...)
	record.AllowedScopes = append([]string(nil), record.AllowedScopes...)
	return record, nil
}

// validateAPIKeyRecord 检查服务凭据是否仍可使用。
func validateAPIKeyRecord(record APIKeyRecord, provider string, now time.Time) error {
	if record.CredentialID == "" || record.TenantID == "" || record.ClientID == "" {
		return ErrUnauthenticated
	}
	if record.Disabled || record.SubjectProvider != provider {
		return ErrUnauthenticated
	}
	if !record.ExpiresAt.IsZero() && !record.ExpiresAt.After(now) {
		return ErrUnauthenticated
	}
	return nil
}

// intersect 返回两组权限的有序交集。
func intersect(maximum, assigned []string) []string {
	allowed := make(map[string]struct{}, len(maximum))
	for _, value := range maximum {
		if value != "" {
			allowed[value] = struct{}{}
		}
	}
	var result []string
	seen := make(map[string]struct{}, len(assigned))
	for _, value := range assigned {
		if _, ok := allowed[value]; !ok {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return sortedUnique(result)
}

// validCredential 检查凭据长度和字符是否适合放入 HTTP Header。
func validCredential(value string) bool {
	return value != "" && len(value) <= maxCredentialLength && validHeaderValue(value)
}

// validIdentityValue 检查身份字段长度和控制字符。
func validIdentityValue(value string, maxLength int) bool {
	return value != "" && utf8.ValidString(value) && len(value) <= maxLength && validHeaderValue(value)
}

// validHeaderValue 拒绝换行符和其他控制字符。
func validHeaderValue(value string) bool {
	for _, current := range value {
		if unicode.IsControl(current) || current == unicode.ReplacementChar {
			return false
		}
	}
	return true
}
