// Package httpconnector 提供安全的下游 HTTP 调用能力。
package httpconnector

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
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

// Config 定义 HTTP Connector 参数。
type Config struct {
	BaseURL          string
	AllowedHosts     []string
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
	if !hostAllowed(baseURL.Hostname(), config.AllowedHosts) {
		return nil, fmt.Errorf("BaseURL 主机不在白名单中")
	}
	if ip := net.ParseIP(baseURL.Hostname()); ip != nil && isPrivateIP(ip) && !hostListed(baseURL.Hostname(), config.AllowedHosts) {
		return nil, fmt.Errorf("私有 IP 必须显式加入白名单")
	}

	if config.Timeout <= 0 {
		config.Timeout = 5 * time.Second
	}
	if config.MaxResponseBytes <= 0 {
		config.MaxResponseBytes = 1024 * 1024
	}
	client := config.Client
	if client == nil {
		client = &http.Client{Timeout: config.Timeout}
	}
	return &Connector{
		baseURL:          baseURL,
		client:           client,
		defaultHeaders:   config.DefaultHeaders.Clone(),
		maxResponseBytes: config.MaxResponseBytes,
	}, nil
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
		return Response{}, fmt.Errorf("%w：%v", ErrUpstream, err)
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

// hostAllowed 检查主机是否符合白名单。
func hostAllowed(host string, allowed []string) bool {
	if len(allowed) == 0 {
		return net.ParseIP(host) == nil
	}
	return hostListed(host, allowed)
}

// hostListed 检查主机是否被明确列出。
func hostListed(host string, allowed []string) bool {
	for _, item := range allowed {
		if strings.EqualFold(host, item) {
			return true
		}
	}
	return false
}

// isPrivateIP 判断 IP 是否属于本地或私有网络。
func isPrivateIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast()
}
