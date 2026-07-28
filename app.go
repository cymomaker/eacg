// Package eacg 提供企业 Agent 能力网关的应用入口。
package eacg

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/cymomaker/eacg/audit"
	"github.com/cymomaker/eacg/capability"
	"github.com/cymomaker/eacg/execution"
	"github.com/cymomaker/eacg/identity"
	"github.com/cymomaker/eacg/protocol/mcphttp"
	"github.com/cymomaker/eacg/registry"
)

// Config 定义 EACG 应用参数。
type Config struct {
	Name              string
	Version           string
	Address           string
	ReadHeaderTimeout time.Duration
	ShutdownTimeout   time.Duration
	SessionTimeout    time.Duration
	ExecutionTimeout  time.Duration
	AllowedOrigins    []string
	ResourceMetaURL   string
}

// HTTPAuthenticationConfig 定义 MCP HTTP 端点的认证方式。
type HTTPAuthenticationConfig struct {
	Authenticator    identity.Authenticator
	CredentialHeader string
	SubjectHeader    string
	SubjectProvider  string
}

// App 负责组装能力、协议和 HTTP 生命周期。
type App struct {
	config         Config
	registry       *registry.Registry
	authentication HTTPAuthenticationConfig
	audit          audit.Sink
	logger         *slog.Logger

	mu      sync.Mutex
	handler http.Handler
	server  *http.Server
	ready   bool
}

// New 使用统一认证配置创建 EACG 应用。
func New(
	config Config,
	authentication HTTPAuthenticationConfig,
	sink audit.Sink,
) (*App, error) {
	if config.Name == "" || config.Version == "" {
		return nil, fmt.Errorf("应用名称和版本不能为空")
	}
	if authentication.Authenticator == nil {
		return nil, fmt.Errorf("身份认证器不能为空")
	}
	if config.Address == "" {
		config.Address = "127.0.0.1:8080"
	}
	if config.ReadHeaderTimeout <= 0 {
		config.ReadHeaderTimeout = 5 * time.Second
	}
	if config.ShutdownTimeout <= 0 {
		config.ShutdownTimeout = 10 * time.Second
	}
	if sink == nil {
		sink = audit.NewLoggerSink(os.Stdout)
	}
	return &App{
		config:         config,
		registry:       registry.New(),
		authentication: authentication,
		audit:          sink,
		logger:         slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}, nil
}

// SetLogger 替换应用使用的结构化日志器。
func (a *App) SetLogger(logger *slog.Logger) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if logger != nil {
		a.logger = logger
	}
}

// RegisterCapability 注册一个领域能力。
func (a *App) RegisterCapability(items ...capability.Capability) error {
	for _, item := range items {
		if err := a.registry.Register(item); err != nil {
			return err
		}
	}
	return nil
}

// Handler 构建可用于测试或外部 Server 的 HTTP Handler。
func (a *App) Handler() (http.Handler, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.handler != nil {
		return a.handler, nil
	}
	a.registry.Freeze()
	engine, err := execution.New(a.registry, a.audit, execution.Config{
		Timeout: a.config.ExecutionTimeout,
	})
	if err != nil {
		return nil, err
	}
	mcpHandler, err := mcphttp.New(mcphttp.Config{
		Name:             a.config.Name,
		Version:          a.config.Version,
		Registry:         a.registry,
		Engine:           engine,
		Authenticator:    a.authentication.Authenticator,
		CredentialHeader: a.authentication.CredentialHeader,
		SubjectHeader:    a.authentication.SubjectHeader,
		SubjectProvider:  a.authentication.SubjectProvider,
		Logger:           a.logger,
		SessionTimeout:   a.config.SessionTimeout,
		AllowedOrigins:   a.config.AllowedOrigins,
		ResourceMetaURL:  a.config.ResourceMetaURL,
	})
	if err != nil {
		return nil, err
	}

	mux := http.NewServeMux()
	mux.Handle("/mcp", mcpHandler)
	mux.HandleFunc("/health", a.health)
	mux.HandleFunc("/ready", a.readiness)
	a.handler = mux
	a.ready = true
	return a.handler, nil
}

// Run 启动 HTTP Server，并在上下文结束时优雅停机。
func (a *App) Run(ctx context.Context) error {
	handler, err := a.Handler()
	if err != nil {
		return err
	}

	a.mu.Lock()
	if a.server != nil {
		a.mu.Unlock()
		return fmt.Errorf("应用已经启动")
	}
	a.server = &http.Server{
		Addr:              a.config.Address,
		Handler:           handler,
		ReadHeaderTimeout: a.config.ReadHeaderTimeout,
	}
	server := a.server
	a.mu.Unlock()

	errChannel := make(chan error, 1)
	go func() {
		a.logger.Info("EACG HTTP 服务启动", "address", a.config.Address)
		errChannel <- server.ListenAndServe()
	}()

	select {
	case err := <-errChannel:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		return a.Stop(context.Background())
	}
}

// Stop 在超时时间内优雅停止 HTTP Server。
func (a *App) Stop(ctx context.Context) error {
	a.mu.Lock()
	server := a.server
	a.ready = false
	a.mu.Unlock()
	if server == nil {
		return nil
	}

	shutdownCtx, cancel := context.WithTimeout(ctx, a.config.ShutdownTimeout)
	defer cancel()
	return server.Shutdown(shutdownCtx)
}

// health 返回进程存活状态。
func (a *App) health(writer http.ResponseWriter, _ *http.Request) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(writer, `{"status":"ok"}`)
}

// readiness 返回应用是否已经完成初始化。
func (a *App) readiness(writer http.ResponseWriter, _ *http.Request) {
	a.mu.Lock()
	ready := a.ready
	a.mu.Unlock()
	writer.Header().Set("Content-Type", "application/json")
	if !ready {
		writer.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(writer, `{"status":"not_ready"}`)
		return
	}
	writer.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(writer, `{"status":"ready"}`)
}
