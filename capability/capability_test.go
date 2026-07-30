// 本文件验证类型化 Capability 和 JSON Schema 校验。
package capability

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/cymomaker/eacg/identity"
)

// greetInput 保存问候能力的输入。
type greetInput struct {
	Name string `json:"name" jsonschema:"来访者姓名"`
}

// TestCapabilityIdentityRequirement 验证默认和显式身份要求。
func TestCapabilityIdentityRequirement(t *testing.T) {
	t.Parallel()

	item, err := New(Descriptor{
		ID:          "any.v1",
		Name:        "any",
		Version:     "v1",
		Description: "允许任意合法身份",
		RiskLevel:   RiskR0,
		ReadOnly:    true,
	}, func(_ context.Context, _ RequestContext, input greetInput) (greetOutput, error) {
		return greetOutput{Message: input.Name}, nil
	})
	if err != nil {
		t.Fatalf("创建默认身份能力失败：%v", err)
	}
	if item.Descriptor().IdentityRequirement != IdentityAny {
		t.Fatalf("空身份要求应规范化为 any：%q", item.Descriptor().IdentityRequirement)
	}
	service := identity.Principal{
		SubjectType: identity.SubjectService,
		TenantID:    "tenant-a",
		ClientID:    "service-a",
	}
	if !item.Descriptor().AllowsPrincipal(service) {
		t.Fatal("IdentityAny 应允许合法服务身份")
	}

	_, err = New(Descriptor{
		ID:                  "invalid.v1",
		Name:                "invalid",
		Version:             "v1",
		Description:         "非法身份要求",
		RiskLevel:           RiskR0,
		ReadOnly:            true,
		IdentityRequirement: "unknown",
	}, func(_ context.Context, _ RequestContext, input greetInput) (greetOutput, error) {
		return greetOutput{Message: input.Name}, nil
	})
	if err == nil {
		t.Fatal("非法身份要求不应创建成功")
	}
}

// greetOutput 保存问候能力的输出。
type greetOutput struct {
	Message string `json:"message" jsonschema:"问候语"`
}

// TestCapabilityExecute 验证能力可以完成类型转换和执行。
func TestCapabilityExecute(t *testing.T) {
	t.Parallel()

	item, err := New(Descriptor{
		ID:          "greet.v1",
		Name:        "greet",
		Version:     "v1",
		Description: "返回问候语",
		RiskLevel:   RiskR0,
		ReadOnly:    true,
	}, func(_ context.Context, _ RequestContext, input greetInput) (greetOutput, error) {
		return greetOutput{Message: "你好，" + input.Name}, nil
	})
	if err != nil {
		t.Fatalf("创建能力失败：%v", err)
	}

	result, err := item.Execute(context.Background(), ExecutionRequest{
		Arguments: json.RawMessage(`{"name":"小明"}`),
	})
	if err != nil {
		t.Fatalf("执行能力失败：%v", err)
	}
	output := result.Value.(greetOutput)
	if output.Message != "你好，小明" {
		t.Fatalf("输出不正确：%+v", output)
	}
}

// TestCapabilityRejectsInvalidInput 验证错误输入不会进入业务处理函数。
func TestCapabilityRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	called := false
	item, err := New(Descriptor{
		ID:          "greet.v1",
		Name:        "greet",
		Version:     "v1",
		Description: "返回问候语",
		RiskLevel:   RiskR0,
		ReadOnly:    true,
	}, func(_ context.Context, _ RequestContext, input greetInput) (greetOutput, error) {
		called = true
		return greetOutput{Message: input.Name}, nil
	})
	if err != nil {
		t.Fatalf("创建能力失败：%v", err)
	}

	_, err = item.Execute(context.Background(), ExecutionRequest{
		Arguments: json.RawMessage(`{"name":123}`),
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("错误输入应返回 ErrInvalidInput，实际为：%v", err)
	}
	if called {
		t.Fatal("错误输入不应调用业务处理函数")
	}
}

// TestCapabilityRejectsWriteRisk 验证 MVP 不允许注册写能力。
func TestCapabilityRejectsWriteRisk(t *testing.T) {
	t.Parallel()

	_, err := New(Descriptor{
		ID:          "write.v1",
		Name:        "write",
		Version:     "v1",
		Description: "写数据",
		RiskLevel:   RiskR2,
	}, func(_ context.Context, _ RequestContext, input greetInput) (greetOutput, error) {
		return greetOutput{}, nil
	})
	if err == nil {
		t.Fatal("MVP 不应允许创建写能力")
	}
}
