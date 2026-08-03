// Command eacg-example 启动一个可学习和测试的 EACG 服务。
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/cymomaker/eacg"
	"github.com/cymomaker/eacg/capability"
	"github.com/cymomaker/eacg/connector/httpconnector"
	"github.com/cymomaker/eacg/identity"
)

// profileInput 保存用户资料 Tool 的输入。
type profileInput struct {
	UserID string `json:"user_id" jsonschema:"要查询的用户 ID"`
}

// profileOutput 保存用户资料 Tool 的安全输出。
type profileOutput struct {
	UserID string `json:"user_id"`
	Name   string `json:"name"`
	Email  string `json:"email"`
}

// upstreamProfile 保存演示下游接口返回的数据。
type upstreamProfile struct {
	UserID string `json:"user_id"`
	Name   string `json:"name"`
	Email  string `json:"email"`
}

// main 组装示例能力并启动 EACG。
func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	authentication, err := newAuthentication()
	if err != nil {
		log.Fatal(err)
	}

	upstreamURL := envOrDefault("EACG_UPSTREAM_URL", "http://127.0.0.1:8090")
	if upstreamURL == "http://127.0.0.1:8090" {
		go runDemoUpstream(ctx)
	}
	connector, err := httpconnector.New(httpconnector.Config{
		BaseURL: upstreamURL,
		Timeout: 3 * time.Second,
	})
	if err != nil {
		log.Fatal(err)
	}

	profileCapability, err := newProfileCapability(connector)
	if err != nil {
		log.Fatal(err)
	}
	app, err := eacg.New(eacg.Config{
		Name:                "eacg-example",
		Version:             "v0.3.0",
		Address:             envOrDefault("EACG_ADDRESS", "127.0.0.1:8080"),
		ExecutionTimeout:    5 * time.Second,
		MaxRequestBodyBytes: 4 << 20,
	}, authentication, nil)
	if err != nil {
		log.Fatal(err)
	}
	if err := app.RegisterCapability(profileCapability); err != nil {
		log.Fatal(err)
	}

	log.Printf(
		"EACG v0.3.0 示例启动，MCP 2026-07-28 / 2025-06-18 地址：http://%s/mcp",
		envOrDefault("EACG_ADDRESS", "127.0.0.1:8080"),
	)
	if err := app.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatal(err)
	}
}

// newAuthentication 根据环境变量创建 JWT 或 API Key 认证配置。
func newAuthentication() (eacg.HTTPAuthenticationConfig, error) {
	mode := strings.ToLower(envOrDefault("EACG_AUTH_MODE", "jwt"))
	switch mode {
	case "jwt":
		secret := envOrDefault("EACG_JWT_SECRET", "0123456789abcdef0123456789abcdef")
		authenticator, err := identity.NewJWTAuthenticator(identity.JWTConfig{
			Secret:   []byte(secret),
			Issuer:   envOrDefault("EACG_JWT_ISSUER", "eacg-example"),
			Audience: envOrDefault("EACG_JWT_AUDIENCE", "eacg"),
		})
		if err != nil {
			return eacg.HTTPAuthenticationConfig{}, err
		}
		return eacg.HTTPAuthenticationConfig{Authenticator: authenticator}, nil
	case "api_key":
		return newAPIKeyAuthentication()
	default:
		return eacg.HTTPAuthenticationConfig{}, fmt.Errorf(
			"EACG_AUTH_MODE 只支持 jwt 或 api_key",
		)
	}
}

// newAPIKeyAuthentication 创建纯服务身份的 API Key 认证配置。
func newAPIKeyAuthentication() (eacg.HTTPAuthenticationConfig, error) {
	rawKey := strings.TrimSpace(os.Getenv("EACG_API_KEY"))
	if len(rawKey) < 32 {
		return eacg.HTTPAuthenticationConfig{}, fmt.Errorf(
			"EACG_API_KEY 不能为空且长度不能少于 32 个字符",
		)
	}
	expiresAt, err := optionalTime(os.Getenv("EACG_API_KEY_EXPIRES_AT"))
	if err != nil {
		return eacg.HTTPAuthenticationConfig{}, err
	}
	store, err := identity.NewMemoryAPIKeyStore(identity.APIKeyRecord{
		CredentialID: envOrDefault("EACG_API_KEY_ID", "service-demo-key"),
		TenantID:     envOrDefault("EACG_TENANT_ID", "tenant-a"),
		ClientID:     envOrDefault("EACG_CLIENT_ID", "eacg-service"),
		AgentID:      os.Getenv("EACG_AGENT_ID"),
		Digest:       identity.DigestAPIKey(rawKey),
		Roles:        []string{"reader"},
		Scopes:       []string{"profile:read"},
		ExpiresAt:    expiresAt,
	})
	if err != nil {
		return eacg.HTTPAuthenticationConfig{}, err
	}
	authenticator, err := identity.NewAPIKeyAuthenticator(
		store,
		identity.APIKeyAuthenticatorConfig{},
	)
	if err != nil {
		return eacg.HTTPAuthenticationConfig{}, err
	}
	return eacg.HTTPAuthenticationConfig{
		Authenticator:    authenticator,
		CredentialHeader: envOrDefault("EACG_CREDENTIAL_HEADER", "X-EACG-API-Key"),
	}, nil
}

// optionalTime 解析可选的 RFC3339 时间。
func optionalTime(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("解析 EACG_API_KEY_EXPIRES_AT：%w", err)
	}
	return parsed, nil
}

// newProfileCapability 创建通过 HTTP 查询用户资料的示例能力。
func newProfileCapability(connector *httpconnector.Connector) (capability.Capability, error) {
	return capability.New(capability.Descriptor{
		ID:                  "get_profile.v1",
		Name:                "get_profile",
		Version:             "v1",
		Description:         "根据用户 ID 查询基础资料",
		RiskLevel:           capability.RiskR1,
		ReadOnly:            true,
		Idempotent:          true,
		RequiredRoles:       []string{"reader"},
		AllowedOutputFields: []string{"user_id", "name", "email"},
	}, func(ctx context.Context, request capability.RequestContext, input profileInput) (profileOutput, error) {
		response, err := connector.Invoke(ctx, httpconnector.Request{
			Method: http.MethodGet,
			Path:   "/profiles/" + url.PathEscape(input.UserID),
			Headers: http.Header{
				"X-Request-ID": []string{request.RequestID},
				"X-Tenant-ID":  []string{request.Principal.TenantID},
			},
		})
		if err != nil {
			return profileOutput{}, err
		}
		var profile upstreamProfile
		if err := json.Unmarshal(response.Body, &profile); err != nil {
			return profileOutput{}, fmt.Errorf("解析用户资料：%w", err)
		}
		return profileOutput(profile), nil
	})
}

// runDemoUpstream 启动本地演示用的下游 HTTP 服务。
func runDemoUpstream(ctx context.Context) {
	mux := http.NewServeMux()
	mux.HandleFunc("/profiles/", func(writer http.ResponseWriter, request *http.Request) {
		userID := request.URL.Path[len("/profiles/"):]
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(upstreamProfile{
			UserID: userID,
			Name:   "示例用户",
			Email:  userID + "@example.com",
		})
	})
	server := &http.Server{
		Addr:              "127.0.0.1:8090",
		Handler:           mux,
		ReadHeaderTimeout: 3 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Printf("演示下游服务异常：%v", err)
	}
}

// envOrDefault 读取环境变量，空值时返回默认值。
func envOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
