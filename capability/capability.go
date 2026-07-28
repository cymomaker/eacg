// Package capability 定义 EACG 的核心能力模型。
package capability

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"time"

	"github.com/cymomaker/eacg/identity"
	"github.com/google/jsonschema-go/jsonschema"
)

// ErrInvalidInput 表示能力输入不符合约定。
var ErrInvalidInput = errors.New("能力输入无效")

// ErrInvalidOutput 表示能力输出不符合约定。
var ErrInvalidOutput = errors.New("能力输出无效")

// RiskLevel 表示能力风险等级。
type RiskLevel string

const (
	// RiskR0 表示公共只读能力。
	RiskR0 RiskLevel = "R0"
	// RiskR1 表示敏感只读能力。
	RiskR1 RiskLevel = "R1"
	// RiskR2 表示可逆写入能力。
	RiskR2 RiskLevel = "R2"
	// RiskR3 表示高风险写入能力。
	RiskR3 RiskLevel = "R3"
)

var toolNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

// Descriptor 描述一个可对 Agent 开放的企业能力。
type Descriptor struct {
	ID                  string
	Name                string
	Version             string
	Description         string
	RiskLevel           RiskLevel
	ReadOnly            bool
	Idempotent          bool
	RequiredRoles       []string
	AllowedOutputFields []string
	InputSchema         *jsonschema.Schema
	OutputSchema        *jsonschema.Schema
}

// Validate 检查能力描述是否满足 MVP 约束。
func (d Descriptor) Validate() error {
	if d.ID == "" || d.Name == "" || d.Version == "" || d.Description == "" {
		return fmt.Errorf("能力 ID、名称、版本和说明不能为空")
	}
	if !toolNamePattern.MatchString(d.Name) {
		return fmt.Errorf("能力名称只能包含字母、数字、下划线和短横线，且不能超过 128 字符")
	}
	if d.RiskLevel != RiskR0 && d.RiskLevel != RiskR1 {
		return fmt.Errorf("MVP 只允许 R0 和 R1 能力")
	}
	if !d.ReadOnly {
		return fmt.Errorf("MVP 只允许只读能力")
	}
	if d.InputSchema == nil || d.OutputSchema == nil {
		return fmt.Errorf("输入和输出 Schema 不能为空")
	}
	return nil
}

// RequestContext 保存能力处理函数需要的调用上下文。
type RequestContext struct {
	RequestID string
	TraceID   string
	Principal identity.Principal
	StartedAt time.Time
}

// ExecutionRequest 保存一次能力调用的原始输入。
type ExecutionRequest struct {
	Context   RequestContext
	Arguments json.RawMessage
}

// ExecutionResult 保存一次能力调用的结构化结果。
type ExecutionResult struct {
	Value    any
	Warnings []string
}

// Handler 定义有类型的能力处理函数。
type Handler[I, O any] func(context.Context, RequestContext, I) (O, error)

// Capability 定义运行时统一能力接口。
type Capability interface {
	Descriptor() Descriptor
	Execute(context.Context, ExecutionRequest) (ExecutionResult, error)
}

// typedCapability 把泛型处理函数转换成统一运行时能力。
type typedCapability[I, O any] struct {
	descriptor     Descriptor
	handler        Handler[I, O]
	inputResolved  *jsonschema.Resolved
	outputResolved *jsonschema.Resolved
}

// New 创建有类型的 Capability。
func New[I, O any](descriptor Descriptor, handler Handler[I, O]) (Capability, error) {
	if handler == nil {
		return nil, fmt.Errorf("能力处理函数不能为空")
	}

	inputSchema, err := schemaForType(reflect.TypeFor[I]())
	if err != nil {
		return nil, fmt.Errorf("生成输入 Schema：%w", err)
	}
	outputSchema, err := schemaForType(reflect.TypeFor[O]())
	if err != nil {
		return nil, fmt.Errorf("生成输出 Schema：%w", err)
	}
	descriptor.InputSchema = inputSchema
	descriptor.OutputSchema = outputSchema
	if err := descriptor.Validate(); err != nil {
		return nil, err
	}

	inputResolved, err := inputSchema.Resolve(nil)
	if err != nil {
		return nil, fmt.Errorf("解析输入 Schema：%w", err)
	}
	outputResolved, err := outputSchema.Resolve(nil)
	if err != nil {
		return nil, fmt.Errorf("解析输出 Schema：%w", err)
	}

	return &typedCapability[I, O]{
		descriptor:     cloneDescriptor(descriptor),
		handler:        handler,
		inputResolved:  inputResolved,
		outputResolved: outputResolved,
	}, nil
}

// Descriptor 返回不可直接修改的能力描述副本。
func (c *typedCapability[I, O]) Descriptor() Descriptor {
	return cloneDescriptor(c.descriptor)
}

// Execute 校验输入，调用业务处理函数，再校验输出。
func (c *typedCapability[I, O]) Execute(ctx context.Context, request ExecutionRequest) (ExecutionResult, error) {
	var input I
	if err := json.Unmarshal(request.Arguments, &input); err != nil {
		return ExecutionResult{}, fmt.Errorf("%w：%v", ErrInvalidInput, err)
	}
	if err := validateJSON(c.inputResolved, input); err != nil {
		return ExecutionResult{}, fmt.Errorf("%w：%v", ErrInvalidInput, err)
	}

	output, err := c.handler(ctx, request.Context, input)
	if err != nil {
		return ExecutionResult{}, err
	}
	if err := validateJSON(c.outputResolved, output); err != nil {
		return ExecutionResult{}, fmt.Errorf("%w：%v", ErrInvalidOutput, err)
	}
	return ExecutionResult{Value: output}, nil
}

// schemaForType 为 Go 类型生成 JSON Schema。
func schemaForType(valueType reflect.Type) (*jsonschema.Schema, error) {
	schema, err := jsonschema.ForType(valueType, nil)
	if err != nil {
		return nil, err
	}
	if schema.Type != "object" {
		return nil, fmt.Errorf("输入输出类型必须是结构体或对象")
	}
	return schema, nil
}

// validateJSON 使用 JSON 形式校验结构体，避免自定义类型差异。
func validateJSON(schema *jsonschema.Resolved, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	var document any
	if err := json.Unmarshal(raw, &document); err != nil {
		return err
	}
	return schema.Validate(document)
}

// cloneDescriptor 复制描述中的切片，避免外部修改内部状态。
func cloneDescriptor(source Descriptor) Descriptor {
	result := source
	result.RequiredRoles = append([]string(nil), source.RequiredRoles...)
	result.AllowedOutputFields = append([]string(nil), source.AllowedOutputFields...)
	return result
}
