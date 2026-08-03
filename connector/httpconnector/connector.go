// Package httpconnector 提供安全的下游 HTTP 调用能力。
package httpconnector

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

// ErrUpstream 表示下游服务返回失败状态。
var ErrUpstream = errors.New("下游服务调用失败")

// ErrResponseTooLarge 表示下游响应超过限制。
var ErrResponseTooLarge = errors.New("下游响应超过大小限制")

// ErrUnsafeRedirect 表示下游尝试重定向到 BaseURL 之外的 Origin。
var ErrUnsafeRedirect = errors.New("下游重定向目标不允许")

// Config 定义 HTTP Connector 参数。
type Config struct {
	BaseURL          string
	Timeout          time.Duration
	MaxResponseBytes int64
	DefaultHeaders   http.Header
	Client           *http.Client
}

// Request 定义一次下游 HTTP 请求。
type Request struct {
	Method  string
	Path    string
	Query   url.Values
	Headers http.Header
	Body    any
}

// Response 保存下游 HTTP 响应。
type Response struct {
	StatusCode int
	Headers    http.Header
	Body       json.RawMessage
}

// Connector 调用一个固定配置的 HTTP Provider。
type Connector struct {
	baseURL          *url.URL
	client           *http.Client
	defaultHeaders   http.Header
	maxResponseBytes int64
}

// New 创建 HTTP Connector 并检查目标地址。
func New(config Config) (*Connector, error) {
	baseURL, err := url.Parse(config.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("解析 BaseURL：%w", err)
	}
	if baseURL.Scheme != "http" && baseURL.Scheme != "https" {
		return nil, fmt.Errorf("BaseURL 只允许 http 或 https")
	}
	if baseURL.User != nil || baseURL.Hostname() == "" {
		return nil, fmt.Errorf("BaseURL 不能包含用户信息且必须包含主机")
	}
	if config.Timeout <= 0 {
		config.Timeout = 5 * time.Second
	}
	if config.MaxResponseBytes <= 0 {
		config.MaxResponseBytes = 1024 * 1024
	}
	client := cloneHTTPClient(config.Client, config.Timeout)
	client.CheckRedirect = secureRedirectPolicy(baseURL, client.CheckRedirect)
	return &Connector{
		baseURL:          baseURL,
		client:           client,
		defaultHeaders:   config.DefaultHeaders.Clone(),
		maxResponseBytes: config.MaxResponseBytes,
	}, nil
}

// cloneHTTPClient 复制调用方的 Client，避免安装安全策略时修改共享实例。
func cloneHTTPClient(source *http.Client, timeout time.Duration) *http.Client {
	if source == nil {
		return &http.Client{Timeout: timeout}
	}
	cloned := *source
	if cloned.Timeout <= 0 {
		cloned.Timeout = timeout
	}
	return &cloned
}

// secureRedirectPolicy 只允许固定 BaseURL Origin 内的有限次重定向。
func secureRedirectPolicy(baseURL *url.URL, original func(*http.Request, []*http.Request) error) func(*http.Request, []*http.Request) error {
	return func(request *http.Request, via []*http.Request) error {
		if request.URL.User != nil || !sameOrigin(baseURL, request.URL) {
			return fmt.Errorf("%w：%s", ErrUnsafeRedirect, request.URL.Redacted())
		}
		if len(via) >= 10 {
			return errors.New("下游重定向次数超过 10 次")
		}
		if original != nil {
			return original(request, via)
		}
		return nil
	}
}

// sameOrigin 比较协议、主机名和有效端口。
func sameOrigin(left, right *url.URL) bool {
	return strings.EqualFold(left.Scheme, right.Scheme) &&
		strings.EqualFold(left.Hostname(), right.Hostname()) &&
		effectivePort(left) == effectivePort(right)
}

func effectivePort(target *url.URL) string {
	if port := target.Port(); port != "" {
		return port
	}
	switch strings.ToLower(target.Scheme) {
	case "http":
		return "80"
	case "https":
		return "443"
	default:
		return ""
	}
}

// Invoke 调用下游 HTTP 服务并读取 JSON 响应。
func (c *Connector) Invoke(ctx context.Context, request Request) (Response, error) {
	target, err := c.resolveURL(request.Path, request.Query)
	if err != nil {
		return Response{}, err
	}
	body, err := marshalBody(request.Body)
	if err != nil {
		return Response{}, err
	}

	method := request.Method
	if method == "" {
		method = http.MethodGet
	}
	httpRequest, err := http.NewRequestWithContext(ctx, method, target.String(), body)
	if err != nil {
		return Response{}, fmt.Errorf("创建下游请求：%w", err)
	}
	copyHeaders(httpRequest.Header, c.defaultHeaders)
	copyHeaders(httpRequest.Header, request.Headers)
	if request.Body != nil && httpRequest.Header.Get("Content-Type") == "" {
		httpRequest.Header.Set("Content-Type", "application/json")
	}

	httpResponse, err := c.client.Do(httpRequest)
	if err != nil {
		return Response{}, fmt.Errorf("%w：%w", ErrUpstream, err)
	}
	defer httpResponse.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(httpResponse.Body, c.maxResponseBytes+1))
	if err != nil {
		return Response{}, fmt.Errorf("读取下游响应：%w", err)
	}
	if int64(len(raw)) > c.maxResponseBytes {
		return Response{}, ErrResponseTooLarge
	}
	if httpResponse.StatusCode < 200 || httpResponse.StatusCode >= 300 {
		return Response{}, fmt.Errorf("%w：HTTP %d", ErrUpstream, httpResponse.StatusCode)
	}
	if len(raw) > 0 && !json.Valid(raw) {
		return Response{}, fmt.Errorf("%w：响应不是合法 JSON", ErrUpstream)
	}
	return Response{
		StatusCode: httpResponse.StatusCode,
		Headers:    httpResponse.Header.Clone(),
		Body:       json.RawMessage(raw),
	}, nil
}

// resolveURL 拼接固定 BaseURL 和相对路径。
func (c *Connector) resolveURL(relativePath string, query url.Values) (*url.URL, error) {
	if strings.Contains(relativePath, "://") || strings.HasPrefix(relativePath, "//") {
		return nil, fmt.Errorf("请求路径必须是相对路径")
	}
	for _, segment := range strings.Split(strings.ReplaceAll(relativePath, "\\", "/"), "/") {
		if segment == ".." {
			return nil, fmt.Errorf("请求路径不能包含上级目录")
		}
	}
	target := *c.baseURL
	target.Path = path.Join(c.baseURL.Path, relativePath)
	target.RawQuery = query.Encode()
	return &target, nil
}

// marshalBody 把请求对象编码为 JSON。
func marshalBody(body any) (io.Reader, error) {
	if body == nil {
		return nil, nil
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("编码请求 Body：%w", err)
	}
	return bytes.NewReader(raw), nil
}

// copyHeaders 复制请求头，不共享原切片。
func copyHeaders(target, source http.Header) {
	for key, values := range source {
		target.Del(key)
		for _, value := range values {
			target.Add(key, value)
		}
	}
}
