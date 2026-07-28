package identity

import (
	"context"
	"errors"
	"testing"
	"time"
)

type staticSubjectResolver struct {
	subject Subject
	err     error
}

// Resolve 为认证测试返回固定企业用户。
func (r staticSubjectResolver) Resolve(
	_ context.Context,
	_ SubjectResolveRequest,
) (Subject, error) {
	return r.subject, r.err
}

// TestAPIKeyAuthenticatorAuthenticate 验证服务权限和用户权限会取交集。
func TestAPIKeyAuthenticatorAuthenticate(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 27, 10, 0, 0, 0, time.UTC)
	rawKey := "0123456789abcdef0123456789abcdef"
	store, err := NewMemoryAPIKeyStore(APIKeyRecord{
		CredentialID:    "key-1",
		TenantID:        "tenant-a",
		ClientID:        "wecom-bot",
		AgentID:         "agent-1",
		Digest:          DigestAPIKey(rawKey),
		SubjectProvider: "wecom",
		AllowedRoles:    []string{"reader", "admin"},
		AllowedScopes:   []string{"profile:read"},
		Version:         "v1",
	})
	if err != nil {
		t.Fatalf("创建 Key Store 失败：%v", err)
	}
	authenticator, err := NewAPIKeyAuthenticator(store, staticSubjectResolver{
		subject: Subject{
			UserID:            "user-1",
			Roles:             []string{"reader", "operator"},
			Scopes:            []string{"profile:read", "profile:write"},
			Attrs:             map[string]string{"department": "engineering"},
			PermissionVersion: "p1",
		},
	}, APIKeyAuthenticatorConfig{})
	if err != nil {
		t.Fatalf("创建认证器失败：%v", err)
	}
	authenticator.now = func() time.Time { return now }

	result, err := authenticator.Authenticate(context.Background(), AuthenticationRequest{
		Credential: rawKey,
		Subject: &SubjectAssertion{
			Provider:   "wecom",
			ExternalID: "zhangsan",
		},
	})
	if err != nil {
		t.Fatalf("认证失败：%v", err)
	}
	if result.Principal.TenantID != "tenant-a" ||
		result.Principal.UserID != "user-1" ||
		result.Principal.AuthMethod != "api_key" {
		t.Fatalf("Principal 不正确：%+v", result.Principal)
	}
	if len(result.Principal.Roles) != 1 || result.Principal.Roles[0] != "reader" {
		t.Fatalf("角色交集不正确：%v", result.Principal.Roles)
	}
	if len(result.Principal.Scopes) != 1 || result.Principal.Scopes[0] != "profile:read" {
		t.Fatalf("Scope 交集不正确：%v", result.Principal.Scopes)
	}
	if result.ExpiresAt != now.Add(defaultAuthenticationLease) {
		t.Fatalf("默认认证租约不正确：%v", result.ExpiresAt)
	}
	if result.SessionBindingID == "" || result.SessionBindingID == rawKey {
		t.Fatal("会话绑定标识不能为空或包含明文 Key")
	}
}

// TestAPIKeyAuthenticatorUsesEarlierKeyExpiry 验证 Key 过期时间会限制认证租约。
func TestAPIKeyAuthenticatorUsesEarlierKeyExpiry(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 27, 10, 0, 0, 0, time.UTC)
	rawKey := "abcdef0123456789abcdef0123456789"
	keyExpiry := now.Add(time.Minute)
	store, err := NewMemoryAPIKeyStore(APIKeyRecord{
		CredentialID:    "key-1",
		TenantID:        "tenant-a",
		ClientID:        "wecom-bot",
		Digest:          DigestAPIKey(rawKey),
		SubjectProvider: "wecom",
		Version:         "v1",
		ExpiresAt:       keyExpiry,
	})
	if err != nil {
		t.Fatalf("创建 Key Store 失败：%v", err)
	}
	authenticator, err := NewAPIKeyAuthenticator(store, staticSubjectResolver{
		subject: Subject{UserID: "user-1", PermissionVersion: "p1"},
	}, APIKeyAuthenticatorConfig{AuthenticationLease: 5 * time.Minute})
	if err != nil {
		t.Fatalf("创建认证器失败：%v", err)
	}
	authenticator.now = func() time.Time { return now }

	result, err := authenticator.Authenticate(context.Background(), AuthenticationRequest{
		Credential: rawKey,
		Subject:    &SubjectAssertion{Provider: "wecom", ExternalID: "zhangsan"},
	})
	if err != nil {
		t.Fatalf("认证失败：%v", err)
	}
	if result.ExpiresAt != keyExpiry {
		t.Fatalf("认证有效期应取 Key 过期时间：%v", result.ExpiresAt)
	}
}

// TestAPIKeyAuthenticatorRejectsInvalidIdentity 验证非法凭据和用户身份会被拒绝。
func TestAPIKeyAuthenticatorRejectsInvalidIdentity(t *testing.T) {
	t.Parallel()

	rawKey := "0123456789abcdef0123456789abcdef"
	store, err := NewMemoryAPIKeyStore(APIKeyRecord{
		CredentialID:    "key-1",
		TenantID:        "tenant-a",
		ClientID:        "wecom-bot",
		Digest:          DigestAPIKey(rawKey),
		SubjectProvider: "wecom",
	})
	if err != nil {
		t.Fatalf("创建 Key Store 失败：%v", err)
	}
	authenticator, err := NewAPIKeyAuthenticator(store, staticSubjectResolver{
		subject: Subject{UserID: "user-1"},
	}, APIKeyAuthenticatorConfig{})
	if err != nil {
		t.Fatalf("创建认证器失败：%v", err)
	}

	tests := []AuthenticationRequest{
		{Credential: rawKey},
		{Credential: "wrong", Subject: &SubjectAssertion{Provider: "wecom", ExternalID: "user"}},
		{Credential: rawKey, Subject: &SubjectAssertion{Provider: "other", ExternalID: "user"}},
		{Credential: rawKey, Subject: &SubjectAssertion{Provider: "wecom", ExternalID: "\nuser"}},
	}
	for _, request := range tests {
		if _, err := authenticator.Authenticate(context.Background(), request); !errors.Is(err, ErrUnauthenticated) {
			t.Fatalf("非法认证请求应返回 ErrUnauthenticated，实际为：%v", err)
		}
	}
}

// TestAPIKeySessionBindingChangesWithPermissionVersion 验证权限版本变化会使会话绑定失效。
func TestAPIKeySessionBindingChangesWithPermissionVersion(t *testing.T) {
	t.Parallel()

	rawKey := "0123456789abcdef0123456789abcdef"
	store, err := NewMemoryAPIKeyStore(APIKeyRecord{
		CredentialID:    "key-1",
		TenantID:        "tenant-a",
		ClientID:        "wecom-bot",
		Digest:          DigestAPIKey(rawKey),
		SubjectProvider: "wecom",
		AllowedRoles:    []string{"reader"},
		Version:         "v1",
	})
	if err != nil {
		t.Fatalf("创建 Key Store 失败：%v", err)
	}
	first, err := NewAPIKeyAuthenticator(store, staticSubjectResolver{
		subject: Subject{UserID: "user-1", Roles: []string{"reader"}, PermissionVersion: "p1"},
	}, APIKeyAuthenticatorConfig{})
	if err != nil {
		t.Fatalf("创建第一个认证器失败：%v", err)
	}
	second, err := NewAPIKeyAuthenticator(store, staticSubjectResolver{
		subject: Subject{UserID: "user-1", Roles: []string{"reader"}, PermissionVersion: "p2"},
	}, APIKeyAuthenticatorConfig{})
	if err != nil {
		t.Fatalf("创建第二个认证器失败：%v", err)
	}
	request := AuthenticationRequest{
		Credential: rawKey,
		Subject:    &SubjectAssertion{Provider: "wecom", ExternalID: "zhangsan"},
	}
	firstResult, err := first.Authenticate(context.Background(), request)
	if err != nil {
		t.Fatalf("第一次认证失败：%v", err)
	}
	secondResult, err := second.Authenticate(context.Background(), request)
	if err != nil {
		t.Fatalf("第二次认证失败：%v", err)
	}
	if firstResult.SessionBindingID == secondResult.SessionBindingID {
		t.Fatal("权限版本变化后会话绑定标识应变化")
	}
}

// TestAPIKeyAuthenticatorRejectsDisabledOrExpiredKey 验证停用和过期的 Key 无法认证。
func TestAPIKeyAuthenticatorRejectsDisabledOrExpiredKey(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 27, 10, 0, 0, 0, time.UTC)
	rawKey := "0123456789abcdef0123456789abcdef"
	tests := []APIKeyRecord{
		{
			CredentialID:    "disabled-key",
			TenantID:        "tenant-a",
			ClientID:        "wecom-bot",
			Digest:          DigestAPIKey(rawKey),
			SubjectProvider: "wecom",
			Disabled:        true,
		},
		{
			CredentialID:    "expired-key",
			TenantID:        "tenant-a",
			ClientID:        "wecom-bot",
			Digest:          DigestAPIKey(rawKey),
			SubjectProvider: "wecom",
			ExpiresAt:       now.Add(-time.Minute),
		},
	}
	for _, record := range tests {
		store, err := NewMemoryAPIKeyStore(record)
		if err != nil {
			t.Fatalf("创建 Key Store 失败：%v", err)
		}
		authenticator, err := NewAPIKeyAuthenticator(store, staticSubjectResolver{
			subject: Subject{UserID: "user-1"},
		}, APIKeyAuthenticatorConfig{})
		if err != nil {
			t.Fatalf("创建认证器失败：%v", err)
		}
		authenticator.now = func() time.Time { return now }
		_, err = authenticator.Authenticate(context.Background(), AuthenticationRequest{
			Credential: rawKey,
			Subject:    &SubjectAssertion{Provider: "wecom", ExternalID: "zhangsan"},
		})
		if !errors.Is(err, ErrUnauthenticated) {
			t.Fatalf("停用或过期 Key 应认证失败，实际为：%v", err)
		}
	}
}

// TestAPIKeyAuthenticatorRejectsDisabledSubject 验证停用用户无法认证。
func TestAPIKeyAuthenticatorRejectsDisabledSubject(t *testing.T) {
	t.Parallel()

	rawKey := "0123456789abcdef0123456789abcdef"
	store, err := NewMemoryAPIKeyStore(APIKeyRecord{
		CredentialID:    "key-1",
		TenantID:        "tenant-a",
		ClientID:        "wecom-bot",
		Digest:          DigestAPIKey(rawKey),
		SubjectProvider: "wecom",
	})
	if err != nil {
		t.Fatalf("创建 Key Store 失败：%v", err)
	}
	authenticator, err := NewAPIKeyAuthenticator(store, staticSubjectResolver{
		subject: Subject{UserID: "user-1", Disabled: true},
	}, APIKeyAuthenticatorConfig{})
	if err != nil {
		t.Fatalf("创建认证器失败：%v", err)
	}
	_, err = authenticator.Authenticate(context.Background(), AuthenticationRequest{
		Credential: rawKey,
		Subject:    &SubjectAssertion{Provider: "wecom", ExternalID: "zhangsan"},
	})
	if !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("停用用户应认证失败，实际为：%v", err)
	}
}
