// 本文件验证执行管线的授权、输出保护和审计。
package execution

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/cymomaker/eacg/audit"
	"github.com/cymomaker/eacg/capability"
	"github.com/cymomaker/eacg/identity"
	"github.com/cymomaker/eacg/registry"
)

// accountInput 保存账户查询测试的输入。
type accountInput struct {
	ID string `json:"id"`
}

// accountOutput 保存账户查询测试的输出。
type accountOutput struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Password string `json:"password"`
}

// newTestEngine 创建执行管线测试对象。
func newTestEngine(t *testing.T) (*Engine, *audit.MemorySink) {
	t.Helper()
	store := registry.New()
	item, err := capability.New(capability.Descriptor{
		ID:                  "account.v1",
		Name:                "get_account",
		Version:             "v1",
		Description:         "查询账户",
		RiskLevel:           capability.RiskR1,
		ReadOnly:            true,
		RequiredRoles:       []string{"reader"},
		AllowedOutputFields: []string{"id", "name", "password"},
	}, func(_ context.Context, _ capability.RequestContext, input accountInput) (accountOutput, error) {
		return accountOutput{ID: input.ID, Name: "小明", Password: "private"}, nil
	})
	if err != nil {
		t.Fatalf("创建能力失败：%v", err)
	}
	if err := store.Register(item); err != nil {
		t.Fatalf("注册能力失败：%v", err)
	}
	sink := new(audit.MemorySink)
	engine, err := New(store, sink, Config{})
	if err != nil {
		t.Fatalf("创建执行引擎失败：%v", err)
	}
	return engine, sink
}

// TestEngineExecute 验证授权、输出遮盖和审计。
func TestEngineExecute(t *testing.T) {
	t.Parallel()

	engine, sink := newTestEngine(t)
	result, err := engine.Execute(context.Background(), Request{
		Principal: identity.Principal{
			SubjectType:     identity.SubjectUser,
			TenantID:        "tenant-a",
			UserID:          "user-1",
			AuthMethod:      "api_key",
			CredentialID:    "key-1",
			SubjectProvider: "wecom",
			Roles:           []string{"reader"},
		},
		Capability: "get_account",
		Arguments:  json.RawMessage(`{"id":"42"}`),
	})
	if err != nil {
		t.Fatalf("执行能力失败：%v", err)
	}
	output := result.Value.(map[string]any)
	if output["password"] != "[REDACTED]" {
		t.Fatalf("密码字段没有遮盖：%v", output)
	}
	if len(result.Warnings) != 1 {
		t.Fatalf("应返回敏感字段警告：%v", result.Warnings)
	}
	events := sink.Events()
	if len(events) != 1 || !events[0].Success || !events[0].Allowed {
		t.Fatalf("审计事件不正确：%+v", events)
	}
	if events[0].AuthMethod != "api_key" ||
		events[0].CredentialID != "key-1" ||
		events[0].SubjectProvider != "wecom" ||
		events[0].SubjectType != string(identity.SubjectUser) {
		t.Fatalf("审计认证身份不正确：%+v", events[0])
	}
}

// TestEngineRejectsServiceCallingUserCapability 验证服务身份不能绕过用户 Tool 限制。
func TestEngineRejectsServiceCallingUserCapability(t *testing.T) {
	t.Parallel()

	store := registry.New()
	item, err := capability.New(capability.Descriptor{
		ID:                  "user_only.v1",
		Name:                "user_only",
		Version:             "v1",
		Description:         "仅允许用户调用",
		RiskLevel:           capability.RiskR1,
		ReadOnly:            true,
		IdentityRequirement: capability.IdentityUser,
		RequiredRoles:       []string{"reader"},
	}, func(_ context.Context, _ capability.RequestContext, input accountInput) (accountOutput, error) {
		return accountOutput{ID: input.ID}, nil
	})
	if err != nil {
		t.Fatalf("创建用户能力失败：%v", err)
	}
	if err := store.Register(item); err != nil {
		t.Fatalf("注册用户能力失败：%v", err)
	}
	sink := new(audit.MemorySink)
	engine, err := New(store, sink, Config{})
	if err != nil {
		t.Fatalf("创建执行引擎失败：%v", err)
	}
	_, err = engine.Execute(context.Background(), Request{
		Principal: identity.Principal{
			SubjectType:  identity.SubjectService,
			TenantID:     "tenant-a",
			ClientID:     "service-a",
			AuthMethod:   "api_key",
			CredentialID: "key-1",
			Roles:        []string{"reader"},
		},
		Capability: "user_only",
		Arguments:  json.RawMessage(`{"id":"42"}`),
	})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("服务调用用户能力应返回 ErrForbidden，实际为：%v", err)
	}
	events := sink.Events()
	if len(events) != 1 ||
		events[0].SubjectType != string(identity.SubjectService) ||
		events[0].UserID != "" ||
		events[0].ClientID != "service-a" ||
		events[0].Allowed {
		t.Fatalf("服务身份审计不正确：%+v", events)
	}
}

// TestEngineExecutesAndAuditsServiceIdentity 验证通用 Tool 可由服务调用并正确审计。
func TestEngineExecutesAndAuditsServiceIdentity(t *testing.T) {
	t.Parallel()

	engine, sink := newTestEngine(t)
	_, err := engine.Execute(context.Background(), Request{
		Principal: identity.Principal{
			SubjectType:  identity.SubjectService,
			TenantID:     "tenant-a",
			ClientID:     "service-a",
			AgentID:      "agent-a",
			AuthMethod:   "api_key",
			CredentialID: "key-1",
			Roles:        []string{"reader"},
		},
		Capability: "get_account",
		Arguments:  json.RawMessage(`{"id":"42"}`),
	})
	if err != nil {
		t.Fatalf("服务身份执行通用能力失败：%v", err)
	}
	events := sink.Events()
	if len(events) != 1 ||
		!events[0].Allowed ||
		!events[0].Success ||
		events[0].SubjectType != string(identity.SubjectService) ||
		events[0].TenantID != "tenant-a" ||
		events[0].ClientID != "service-a" ||
		events[0].AgentID != "agent-a" ||
		events[0].CredentialID != "key-1" ||
		events[0].UserID != "" {
		t.Fatalf("服务身份成功审计不正确：%+v", events)
	}
}

// TestEngineRejectsUnauthorizedCall 验证越权调用被拒绝并审计。
func TestEngineRejectsUnauthorizedCall(t *testing.T) {
	t.Parallel()

	engine, sink := newTestEngine(t)
	_, err := engine.Execute(context.Background(), Request{
		Principal: identity.Principal{
			SubjectType: identity.SubjectUser,
			TenantID:    "tenant-a",
			UserID:      "user-1",
		},
		Capability: "get_account",
		Arguments:  json.RawMessage(`{"id":"42"}`),
	})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("越权调用应返回 ErrForbidden，实际为：%v", err)
	}
	events := sink.Events()
	if len(events) != 1 || events[0].Allowed {
		t.Fatalf("越权审计不正确：%+v", events)
	}
}
