package capability

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

type greetInput struct {
	Name string `json:"name" jsonschema:"来访者姓名"`
}

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
