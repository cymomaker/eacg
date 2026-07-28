// Package audit 定义能力调用审计接口和默认实现。
package audit

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"sync"
	"time"
)

// Event 保存一次能力调用的安全审计信息。
type Event struct {
	RequestID       string        `json:"request_id"`
	TraceID         string        `json:"trace_id"`
	TenantID        string        `json:"tenant_id"`
	UserID          string        `json:"user_id"`
	AgentID         string        `json:"agent_id,omitempty"`
	ClientID        string        `json:"client_id,omitempty"`
	AuthMethod      string        `json:"auth_method,omitempty"`
	CredentialID    string        `json:"credential_id,omitempty"`
	SubjectProvider string        `json:"subject_provider,omitempty"`
	CapabilityName  string        `json:"capability_name"`
	StartedAt       time.Time     `json:"started_at"`
	Duration        time.Duration `json:"duration"`
	Allowed         bool          `json:"allowed"`
	Success         bool          `json:"success"`
	ErrorCode       string        `json:"error_code,omitempty"`
}

// Sink 定义审计事件写入行为。
type Sink interface {
	Write(context.Context, Event) error
}

// LoggerSink 把审计事件写入结构化日志。
type LoggerSink struct {
	logger *slog.Logger
}

// NewLoggerSink 创建结构化日志审计实现。
func NewLoggerSink(writer io.Writer) *LoggerSink {
	return &LoggerSink{
		logger: slog.New(slog.NewJSONHandler(writer, nil)),
	}
}

// Write 写入一条结构化审计日志。
func (s *LoggerSink) Write(_ context.Context, event Event) error {
	raw, err := json.Marshal(event)
	if err != nil {
		return err
	}
	s.logger.Info("capability_audit", "event", json.RawMessage(raw))
	return nil
}

// MemorySink 在内存中保存审计事件，主要用于测试。
type MemorySink struct {
	mu     sync.Mutex
	events []Event
}

// Write 保存一条审计事件。
func (s *MemorySink) Write(_ context.Context, event Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
	return nil
}

// Events 返回审计事件副本。
func (s *MemorySink) Events() []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Event(nil), s.events...)
}
