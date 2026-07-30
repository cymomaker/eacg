// 本文件实现 API Key 服务身份认证。
package identity

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

// ErrAPIKeyNotFound 表示没有找到对应的 API Key。
var ErrAPIKeyNotFound = errors.New("API Key 不存在")

// 认证限制用于控制租约和凭据长度。
const (
	defaultAuthenticationLease = 5 * time.Minute
	maxCredentialLength        = 8192
)

// APIKeyRecord 保存 API Key 对应的服务身份和权限。
type APIKeyRecord struct {
	CredentialID string
	TenantID     string
	ClientID     string
	AgentID      string
	Digest       [32]byte
	Roles        []string
	Scopes       []string
	Disabled     bool
	ExpiresAt    time.Time
}

// APIKeyStore 定义 API Key 摘要查询行为。
type APIKeyStore interface {
	LookupByDigest(context.Context, [32]byte) (APIKeyRecord, error)
}

// APIKeyAuthenticatorConfig 定义 API Key 认证器参数。
type APIKeyAuthenticatorConfig struct {
	AuthenticationLease time.Duration
}

// APIKeyAuthenticator 使用 API Key 认证系统服务。
type APIKeyAuthenticator struct {
	store APIKeyStore
	lease time.Duration
	now   func() time.Time
}

// NewAPIKeyAuthenticator 创建 API Key 服务身份认证器。
func NewAPIKeyAuthenticator(
	store APIKeyStore,
	config APIKeyAuthenticatorConfig,
) (*APIKeyAuthenticator, error) {
	if store == nil {
		return nil, fmt.Errorf("API Key Store 不能为空")
	}
	if config.AuthenticationLease <= 0 {
		config.AuthenticationLease = defaultAuthenticationLease
	}
	return &APIKeyAuthenticator{
		store: store,
		lease: config.AuthenticationLease,
		now:   time.Now,
	}, nil
}

// Authenticate 校验 API Key 并返回服务身份。
func (a *APIKeyAuthenticator) Authenticate(
	ctx context.Context,
	request AuthenticationRequest,
) (Authentication, error) {
	if !validCredential(request.Credential) {
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
	if err := validateAPIKeyRecord(record, now); err != nil {
		return Authentication{}, ErrUnauthenticated
	}

	principal := Principal{
		SubjectType:  SubjectService,
		TenantID:     record.TenantID,
		AgentID:      record.AgentID,
		ClientID:     record.ClientID,
		AuthMethod:   "api_key",
		CredentialID: record.CredentialID,
		Roles:        uniqueValues(record.Roles),
		Scopes:       uniqueValues(record.Scopes),
	}
	expiresAt := now.Add(a.lease)
	if !record.ExpiresAt.IsZero() && record.ExpiresAt.Before(expiresAt) {
		expiresAt = record.ExpiresAt
	}
	return Authentication{
		Principal: principal,
		ExpiresAt: expiresAt,
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
	if !validAPIKeyIdentity(record) {
		return fmt.Errorf("API Key 记录缺少必要身份字段")
	}
	if record.Digest == ([32]byte{}) {
		return fmt.Errorf("API Key 摘要不能为空")
	}
	cloned := record
	cloned.Roles = append([]string(nil), record.Roles...)
	cloned.Scopes = append([]string(nil), record.Scopes...)
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
	record.Roles = append([]string(nil), record.Roles...)
	record.Scopes = append([]string(nil), record.Scopes...)
	return record, nil
}

// validateAPIKeyRecord 检查服务凭据是否仍可使用。
func validateAPIKeyRecord(record APIKeyRecord, now time.Time) error {
	if !validAPIKeyIdentity(record) {
		return ErrUnauthenticated
	}
	if record.Disabled {
		return ErrUnauthenticated
	}
	if !record.ExpiresAt.IsZero() && !record.ExpiresAt.After(now) {
		return ErrUnauthenticated
	}
	return nil
}

// validAPIKeyIdentity 检查服务身份字段是否安全且完整。
func validAPIKeyIdentity(record APIKeyRecord) bool {
	if !validIdentityValue(record.CredentialID, 256) ||
		!validIdentityValue(record.TenantID, 256) ||
		!validIdentityValue(record.ClientID, 256) {
		return false
	}
	return record.AgentID == "" || validIdentityValue(record.AgentID, 256)
}

// uniqueValues 去除空值和重复权限，并保持稳定顺序。
func uniqueValues(values []string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

// validCredential 检查凭据长度和字符是否适合放入 HTTP Header。
func validCredential(value string) bool {
	return value != "" && len(value) <= maxCredentialLength && validHeaderValue(value)
}

// validIdentityValue 检查身份值的长度、空格和控制字符。
func validIdentityValue(value string, maxLength int) bool {
	return value != "" &&
		value == strings.TrimSpace(value) &&
		utf8.ValidString(value) &&
		len(value) <= maxLength &&
		validHeaderValue(value)
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
