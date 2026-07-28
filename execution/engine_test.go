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

type accountInput struct {
	ID string `json:"id"`
}

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
		events[0].SubjectProvider != "wecom" {
		t.Fatalf("审计认证身份不正确：%+v", events[0])
	}
}

// TestEngineRejectsUnauthorizedCall 验证越权调用被拒绝并审计。
func TestEngineRejectsUnauthorizedCall(t *testing.T) {
	t.Parallel()

	engine, sink := newTestEngine(t)
	_, err := engine.Execute(context.Background(), Request{
		Principal:  identity.Principal{TenantID: "tenant-a", UserID: "user-1"},
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
