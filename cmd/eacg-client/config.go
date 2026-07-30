// 本文件负责解析教学客户端的参数和环境变量。
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"
)

const (
	defaultEndpoint = "http://127.0.0.1:8080/mcp"
	defaultTool     = "get_profile"
)

// clientConfig 保存一次客户端运行需要的配置。
type clientConfig struct {
	endpoint         string
	authMode         string
	token            string
	apiKey           string
	requesterUser    string
	credentialHeader string
	subjectHeader    string
	action           string
	tool             string
	arguments        map[string]any
	timeout          time.Duration
	trace            bool
}

// loadConfig 按“Flag 优先、环境变量其次”的规则加载配置。
func loadConfig(args []string, lookup func(string) string) (clientConfig, error) {
	if lookup == nil {
		lookup = func(string) string { return "" }
	}
	timeout, err := environmentDuration(lookup("EACG_CLIENT_TIMEOUT"), 10*time.Second)
	if err != nil {
		return clientConfig{}, err
	}

	var config clientConfig
	var rawArguments string
	flags := flag.NewFlagSet("eacg-client", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&config.endpoint, "endpoint", valueOrDefault(lookup("EACG_CLIENT_ENDPOINT"), defaultEndpoint), "MCP 地址")
	flags.StringVar(&config.authMode, "auth-mode", valueOrDefault(lookup("EACG_CLIENT_AUTH_MODE"), "jwt"), "认证模式")
	flags.StringVar(&config.token, "token", lookup("EACG_CLIENT_TOKEN"), "JWT")
	flags.StringVar(&config.apiKey, "api-key", lookup("EACG_CLIENT_API_KEY"), "API Key")
	flags.StringVar(
		&config.requesterUser,
		"requester-user",
		lookup("EACG_CLIENT_REQUESTER_USER_ID"),
		"可选外部用户",
	)
	flags.StringVar(
		&config.credentialHeader,
		"credential-header",
		valueOrDefault(lookup("EACG_CLIENT_CREDENTIAL_HEADER"), "X-EACG-API-Key"),
		"API Key Header",
	)
	flags.StringVar(
		&config.subjectHeader,
		"subject-header",
		valueOrDefault(lookup("EACG_CLIENT_SUBJECT_HEADER"), "X-EACG-Requester-UserID"),
		"用户 Header",
	)
	flags.StringVar(&config.action, "action", "flow", "执行动作")
	flags.StringVar(&config.tool, "tool", defaultTool, "Tool 名称")
	flags.StringVar(&rawArguments, "arguments", `{"user_id":"user-1001"}`, "Tool JSON 参数")
	flags.DurationVar(&config.timeout, "timeout", timeout, "整体超时")
	flags.BoolVar(&config.trace, "trace", true, "显示协议追踪")
	if err := flags.Parse(args); err != nil {
		return clientConfig{}, fmt.Errorf("解析命令参数：%w", err)
	}
	if flags.NArg() != 0 {
		return clientConfig{}, fmt.Errorf("不支持位置参数")
	}

	config.authMode = strings.ToLower(strings.TrimSpace(config.authMode))
	config.action = strings.ToLower(strings.TrimSpace(config.action))
	config.endpoint = strings.TrimSpace(config.endpoint)
	config.tool = strings.TrimSpace(config.tool)
	if err := validateConfig(config); err != nil {
		return clientConfig{}, err
	}
	if err := json.Unmarshal([]byte(rawArguments), &config.arguments); err != nil {
		return clientConfig{}, fmt.Errorf("Tool 参数必须是 JSON Object：%w", err)
	}
	if config.arguments == nil {
		return clientConfig{}, fmt.Errorf("Tool 参数必须是 JSON Object")
	}
	return config, nil
}

// validateConfig 检查客户端配置是否安全且完整。
func validateConfig(config clientConfig) error {
	endpoint, err := url.Parse(config.endpoint)
	if err != nil ||
		(endpoint.Scheme != "http" && endpoint.Scheme != "https") ||
		endpoint.Host == "" ||
		endpoint.User != nil ||
		endpoint.RawQuery != "" ||
		endpoint.Fragment != "" {
		return fmt.Errorf("MCP 地址必须是无 userinfo、query 和 fragment 的 HTTP/HTTPS URL")
	}
	switch config.authMode {
	case "jwt":
		if strings.TrimSpace(config.token) == "" {
			return fmt.Errorf("JWT 模式必须提供 EACG_CLIENT_TOKEN 或 --token")
		}
	case "api_key":
		if strings.TrimSpace(config.apiKey) == "" {
			return fmt.Errorf("API Key 模式必须提供 Key")
		}
	default:
		return fmt.Errorf("认证模式只支持 jwt 或 api_key")
	}
	switch config.action {
	case "flow", "discover", "list", "call":
	default:
		return fmt.Errorf("action 只支持 flow、discover、list 或 call")
	}
	if config.tool == "" {
		return fmt.Errorf("Tool 名称不能为空")
	}
	if config.timeout <= 0 {
		return fmt.Errorf("超时时间必须大于零")
	}
	if !validHeaderName(config.credentialHeader) || !validHeaderName(config.subjectHeader) {
		return fmt.Errorf("认证 Header 名称无效")
	}
	if strings.EqualFold(config.credentialHeader, config.subjectHeader) ||
		strings.EqualFold(config.credentialHeader, "Authorization") ||
		strings.EqualFold(config.subjectHeader, "Authorization") {
		return fmt.Errorf("认证 Header 名称不能冲突")
	}
	return nil
}

// validHeaderName 检查 Header 名称是否只包含标准字符。
func validHeaderName(value string) bool {
	if value == "" {
		return false
	}
	for _, current := range value {
		if !strings.ContainsRune("!#$%&'*+-.^_`|~", current) &&
			!(current >= '0' && current <= '9') &&
			!(current >= 'A' && current <= 'Z') &&
			!(current >= 'a' && current <= 'z') {
			return false
		}
	}
	return true
}

// environmentDuration 解析可选的环境变量时间。
func environmentDuration(value string, fallback time.Duration) (time.Duration, error) {
	if strings.TrimSpace(value) == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("EACG_CLIENT_TIMEOUT 无效：%w", err)
	}
	return parsed, nil
}

// valueOrDefault 在值为空时返回默认值。
func valueOrDefault(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
