// Package execution 实现固定顺序的 MVP 能力执行管线。
package execution

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cymomaker/eacg/audit"
	"github.com/cymomaker/eacg/capability"
	"github.com/cymomaker/eacg/identity"
	"github.com/cymomaker/eacg/registry"
)

// ErrNotFound 表示能力不存在。
var ErrNotFound = errors.New("能力不存在")

// ErrForbidden 表示当前身份没有调用权限。
var ErrForbidden = errors.New("没有能力调用权限")

// Config 定义执行引擎参数。
type Config struct {
	Timeout time.Duration
}

// Request 保存执行管线需要的调用参数。
type Request struct {
	RequestID  string
	TraceID    string
	Principal  identity.Principal
	Capability string
	Arguments  json.RawMessage
}

// Result 保存处理后的安全输出。
type Result struct {
	Value    any
	Warnings []string
}

// Engine 按固定顺序执行认证后的能力调用。
type Engine struct {
	registry *registry.Registry
	audit    audit.Sink
	timeout  time.Duration
}

// New 创建能力执行引擎。
func New(store *registry.Registry, sink audit.Sink, config Config) (*Engine, error) {
	if store == nil {
		return nil, fmt.Errorf("能力注册表不能为空")
	}
	if sink == nil {
		return nil, fmt.Errorf("审计 Sink 不能为空")
	}
	if config.Timeout <= 0 {
		config.Timeout = 10 * time.Second
	}
	return &Engine{registry: store, audit: sink, timeout: config.Timeout}, nil
}

// Execute 完成鉴权、能力执行、输出过滤和审计。
func (e *Engine) Execute(ctx context.Context, request Request) (result Result, err error) {
	startedAt := time.Now()
	request.RequestID = valueOrID(request.RequestID)
	request.TraceID = valueOrID(request.TraceID)
	event := audit.Event{
		RequestID:       request.RequestID,
		TraceID:         request.TraceID,
		SubjectType:     string(request.Principal.SubjectType),
		TenantID:        request.Principal.TenantID,
		UserID:          request.Principal.UserID,
		AgentID:         request.Principal.AgentID,
		ClientID:        request.Principal.ClientID,
		AuthMethod:      request.Principal.AuthMethod,
		CredentialID:    request.Principal.CredentialID,
		SubjectProvider: request.Principal.SubjectProvider,
		CapabilityName:  request.Capability,
		StartedAt:       startedAt,
	}
	defer func() {
		event.Duration = time.Since(startedAt)
		event.Success = err == nil
		if err != nil {
			event.ErrorCode = errorCode(err)
		}
		_ = e.audit.Write(context.WithoutCancel(ctx), event)
	}()

	if !request.Principal.Valid() {
		return Result{}, ErrForbidden
	}
	item, exists := e.registry.GetByName(request.Capability)
	if !exists {
		return Result{}, ErrNotFound
	}
	descriptor := item.Descriptor()
	if !descriptor.AllowsPrincipal(request.Principal) ||
		!request.Principal.HasAllRoles(descriptor.RequiredRoles) {
		return Result{}, ErrForbidden
	}
	event.Allowed = true

	execCtx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()
	rawResult, err := item.Execute(execCtx, capability.ExecutionRequest{
		Context: capability.RequestContext{
			RequestID: request.RequestID,
			TraceID:   request.TraceID,
			Principal: request.Principal,
			StartedAt: startedAt,
		},
		Arguments: request.Arguments,
	})
	if err != nil {
		return Result{}, err
	}

	safeValue, warnings, err := secureOutput(rawResult.Value, item.Descriptor().AllowedOutputFields)
	if err != nil {
		return Result{}, fmt.Errorf("%w：%v", capability.ErrInvalidOutput, err)
	}
	return Result{
		Value:    safeValue,
		Warnings: append(rawResult.Warnings, warnings...),
	}, nil
}

// secureOutput 应用字段白名单并遮盖常见敏感字段。
func secureOutput(value any, allowedFields []string) (any, []string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, nil, err
	}
	var document any
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, nil, err
	}
	if len(raw) > 1024*1024 {
		return nil, nil, fmt.Errorf("输出超过 1 MiB")
	}

	if len(allowedFields) > 0 {
		object, ok := document.(map[string]any)
		if !ok {
			return nil, nil, fmt.Errorf("字段白名单只支持对象输出")
		}
		allowed := make(map[string]struct{}, len(allowedFields))
		for _, field := range allowedFields {
			allowed[field] = struct{}{}
		}
		for field := range object {
			if _, exists := allowed[field]; !exists {
				delete(object, field)
			}
		}
	}

	redacted := redactSecrets(document)
	if redacted {
		return document, []string{"输出中的敏感字段已遮盖"}, nil
	}
	return document, nil, nil
}

// redactSecrets 递归遮盖常见密钥字段并返回是否发生修改。
func redactSecrets(value any) bool {
	changed := false
	switch current := value.(type) {
	case map[string]any:
		for key, child := range current {
			if sensitiveKey(key) {
				current[key] = "[REDACTED]"
				changed = true
				continue
			}
			if redactSecrets(child) {
				changed = true
			}
		}
	case []any:
		for _, child := range current {
			if redactSecrets(child) {
				changed = true
			}
		}
	}
	return changed
}

// sensitiveKey 判断字段名是否可能保存密钥。
func sensitiveKey(key string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(key, "-", "_"))
	for _, word := range []string{"password", "secret", "token", "api_key", "access_key"} {
		if normalized == word || strings.HasSuffix(normalized, "_"+word) {
			return true
		}
	}
	return false
}

// valueOrID 在值为空时生成随机标识。
func valueOrID(value string) string {
	if value != "" {
		return value
	}
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		return fmt.Sprintf("fallback-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(data[:])
}

// errorCode 把内部错误转换为稳定审计代码。
func errorCode(err error) string {
	switch {
	case errors.Is(err, ErrNotFound):
		return "not_found"
	case errors.Is(err, ErrForbidden):
		return "forbidden"
	case errors.Is(err, capability.ErrInvalidInput):
		return "invalid_input"
	case errors.Is(err, capability.ErrInvalidOutput):
		return "invalid_output"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "canceled"
	default:
		return "execution_error"
	}
}
