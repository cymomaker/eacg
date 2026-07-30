// 本文件验证 API Key 服务身份认证。
package identity

import (
	"context"
	"errors"
	"testing"
	"time"
)

// failingAPIKeyStore 模拟 API Key 存储异常。
type failingAPIKeyStore struct{}

// LookupByDigest 返回固定的存储异常。
func (failingAPIKeyStore) LookupByDigest(context.Context, [32]byte) (APIKeyRecord, error) {
	return APIKeyRecord{}, errors.New("database unavailable")
}

// fixedAPIKeyStore 返回固定记录，便于验证外部存储中的脏数据。
type fixedAPIKeyStore struct {
	record APIKeyRecord
}

// LookupByDigest 返回固定 API Key 记录。
func (s fixedAPIKeyStore) LookupByDigest(context.Context, [32]byte) (APIKeyRecord, error) {
	return s.record, nil
}

// TestAPIKeyAuthenticatorAuthenticateService 验证 API Key 不需要用户身份。
func TestAPIKeyAuthenticatorAuthenticateService(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	rawKey := "0123456789abcdef0123456789abcdef"
	store, err := NewMemoryAPIKeyStore(APIKeyRecord{
		CredentialID: "key-1",
		TenantID:     "tenant-a",
		ClientID:     "insight-service",
		AgentID:      "agent-1",
		Digest:       DigestAPIKey(rawKey),
		Roles:        []string{"reader", "reader", ""},
		Scopes:       []string{"profile:read", "profile:read"},
	})
	if err != nil {
		t.Fatalf("创建 Key Store 失败：%v", err)
	}
	authenticator, err := NewAPIKeyAuthenticator(store, APIKeyAuthenticatorConfig{})
	if err != nil {
		t.Fatalf("创建认证器失败：%v", err)
	}
	authenticator.now = func() time.Time { return now }

	result, err := authenticator.Authenticate(context.Background(), AuthenticationRequest{
		Credential: rawKey,
	})
	if err != nil {
		t.Fatalf("认证失败：%v", err)
	}
	principal := result.Principal
	if principal.SubjectType != SubjectService ||
		principal.TenantID != "tenant-a" ||
		principal.ClientID != "insight-service" ||
		principal.UserID != "" ||
		principal.AuthMethod != "api_key" ||
		principal.CredentialID != "key-1" {
		t.Fatalf("服务 Principal 不正确：%+v", principal)
	}
	if len(principal.Roles) != 1 || principal.Roles[0] != "reader" {
		t.Fatalf("角色不正确：%v", principal.Roles)
	}
	if len(principal.Scopes) != 1 || principal.Scopes[0] != "profile:read" {
		t.Fatalf("Scope 不正确：%v", principal.Scopes)
	}
	if result.ExpiresAt != now.Add(defaultAuthenticationLease) {
		t.Fatalf("默认认证租约不正确：%v", result.ExpiresAt)
	}
}

// TestAPIKeyAuthenticatorIgnoresOptionalSubject 验证内置认证器不处理业务用户声明。
func TestAPIKeyAuthenticatorIgnoresOptionalSubject(t *testing.T) {
	t.Parallel()

	rawKey := "0123456789abcdef0123456789abcdef"
	store, err := NewMemoryAPIKeyStore(APIKeyRecord{
		CredentialID: "key-1",
		TenantID:     "tenant-a",
		ClientID:     "service-a",
		Digest:       DigestAPIKey(rawKey),
	})
	if err != nil {
		t.Fatalf("创建 Key Store 失败：%v", err)
	}
	authenticator, err := NewAPIKeyAuthenticator(store, APIKeyAuthenticatorConfig{})
	if err != nil {
		t.Fatalf("创建认证器失败：%v", err)
	}
	result, err := authenticator.Authenticate(context.Background(), AuthenticationRequest{
		Credential: rawKey,
		Subject:    &SubjectAssertion{Provider: "wecom", ExternalID: "zhangsan"},
	})
	if err != nil {
		t.Fatalf("认证失败：%v", err)
	}
	if result.Principal.SubjectType != SubjectService || result.Principal.UserID != "" {
		t.Fatalf("用户声明不应改变服务身份：%+v", result.Principal)
	}
}

// TestAPIKeyAuthenticatorUsesEarlierKeyExpiry 验证 Key 过期时间限制认证租约。
func TestAPIKeyAuthenticatorUsesEarlierKeyExpiry(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	rawKey := "abcdef0123456789abcdef0123456789"
	keyExpiry := now.Add(time.Minute)
	store, err := NewMemoryAPIKeyStore(APIKeyRecord{
		CredentialID: "key-1",
		TenantID:     "tenant-a",
		ClientID:     "service-a",
		Digest:       DigestAPIKey(rawKey),
		ExpiresAt:    keyExpiry,
	})
	if err != nil {
		t.Fatalf("创建 Key Store 失败：%v", err)
	}
	authenticator, err := NewAPIKeyAuthenticator(
		store,
		APIKeyAuthenticatorConfig{AuthenticationLease: 5 * time.Minute},
	)
	if err != nil {
		t.Fatalf("创建认证器失败：%v", err)
	}
	authenticator.now = func() time.Time { return now }
	result, err := authenticator.Authenticate(
		context.Background(),
		AuthenticationRequest{Credential: rawKey},
	)
	if err != nil {
		t.Fatalf("认证失败：%v", err)
	}
	if result.ExpiresAt != keyExpiry {
		t.Fatalf("认证有效期应取 Key 过期时间：%v", result.ExpiresAt)
	}
}

// TestAPIKeyAuthenticatorRejectsInvalidKeys 验证非法、停用和过期凭据被拒绝。
func TestAPIKeyAuthenticatorRejectsInvalidKeys(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	rawKey := "0123456789abcdef0123456789abcdef"
	records := []APIKeyRecord{
		{
			CredentialID: "disabled-key",
			TenantID:     "tenant-a",
			ClientID:     "service-a",
			Digest:       DigestAPIKey(rawKey),
			Disabled:     true,
		},
		{
			CredentialID: "expired-key",
			TenantID:     "tenant-a",
			ClientID:     "service-a",
			Digest:       DigestAPIKey(rawKey),
			ExpiresAt:    now.Add(-time.Minute),
		},
	}
	for _, record := range records {
		store, err := NewMemoryAPIKeyStore(record)
		if err != nil {
			t.Fatalf("创建 Key Store 失败：%v", err)
		}
		authenticator, err := NewAPIKeyAuthenticator(store, APIKeyAuthenticatorConfig{})
		if err != nil {
			t.Fatalf("创建认证器失败：%v", err)
		}
		authenticator.now = func() time.Time { return now }
		_, err = authenticator.Authenticate(
			context.Background(),
			AuthenticationRequest{Credential: rawKey},
		)
		if !errors.Is(err, ErrUnauthenticated) {
			t.Fatalf("停用或过期 Key 应认证失败，实际为：%v", err)
		}
	}

	store, err := NewMemoryAPIKeyStore(APIKeyRecord{
		CredentialID: "valid-key",
		TenantID:     "tenant-a",
		ClientID:     "service-a",
		Digest:       DigestAPIKey(rawKey),
	})
	if err != nil {
		t.Fatalf("创建 Key Store 失败：%v", err)
	}
	authenticator, err := NewAPIKeyAuthenticator(store, APIKeyAuthenticatorConfig{})
	if err != nil {
		t.Fatalf("创建认证器失败：%v", err)
	}
	if _, err := authenticator.Authenticate(
		context.Background(),
		AuthenticationRequest{Credential: "wrong"},
	); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("错误 Key 应认证失败，实际为：%v", err)
	}
}

// TestAPIKeyAuthenticatorReturnsStoreError 验证存储异常保留错误链。
func TestAPIKeyAuthenticatorReturnsStoreError(t *testing.T) {
	t.Parallel()

	authenticator, err := NewAPIKeyAuthenticator(failingAPIKeyStore{}, APIKeyAuthenticatorConfig{})
	if err != nil {
		t.Fatalf("创建认证器失败：%v", err)
	}
	_, err = authenticator.Authenticate(context.Background(), AuthenticationRequest{
		Credential: "0123456789abcdef0123456789abcdef",
	})
	if err == nil || errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("存储异常不应伪装成无效凭据：%v", err)
	}
}

// TestMemoryAPIKeyStoreRejectsMissingClientID 验证服务记录必须包含 ClientID。
func TestMemoryAPIKeyStoreRejectsMissingClientID(t *testing.T) {
	t.Parallel()

	_, err := NewMemoryAPIKeyStore(APIKeyRecord{
		CredentialID: "key-1",
		TenantID:     "tenant-a",
		Digest:       DigestAPIKey("0123456789abcdef0123456789abcdef"),
	})
	if err == nil {
		t.Fatal("缺少 ClientID 的服务凭据不应保存")
	}

	authenticator, err := NewAPIKeyAuthenticator(fixedAPIKeyStore{
		record: APIKeyRecord{
			CredentialID: "key-1",
			TenantID:     "tenant-a",
			Digest:       DigestAPIKey("0123456789abcdef0123456789abcdef"),
		},
	}, APIKeyAuthenticatorConfig{})
	if err != nil {
		t.Fatalf("创建认证器失败：%v", err)
	}
	_, err = authenticator.Authenticate(context.Background(), AuthenticationRequest{
		Credential: "0123456789abcdef0123456789abcdef",
	})
	if !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("外部存储缺少 ClientID 时应认证失败：%v", err)
	}
}
