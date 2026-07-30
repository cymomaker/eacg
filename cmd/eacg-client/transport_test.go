// 本文件验证客户端认证注入、协议限制和追踪脱敏。
package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

// roundTripFunc 把函数适配为 HTTP RoundTripper。
type roundTripFunc func(*http.Request) (*http.Response, error)

// RoundTrip 调用测试提供的处理函数。
func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

// newProtocolRequest 创建合法的新版 MCP HTTP 请求。
func newProtocolRequest(t *testing.T, method string) *http.Request {
	t.Helper()
	request, err := http.NewRequestWithContext(
		context.Background(),
		http.MethodPost,
		"http://example.test/mcp",
		strings.NewReader(`{"jsonrpc":"2.0","id":1}`),
	)
	if err != nil {
		t.Fatalf("创建请求失败：%v", err)
	}
	request.Header.Set("Mcp-Protocol-Version", requiredProtocolVersion)
	request.Header.Set("Mcp-Method", method)
	request.Header.Set("Content-Type", "application/json")
	if method == "tools/call" {
		request.Header.Set("Mcp-Name", "get_profile")
	}
	return request
}

// TestStrictTransportInjectsAPIKeyAndRedactsTrace 验证 API Key 双 Header 会注入且不会泄露。
func TestStrictTransportInjectsAPIKeyAndRedactsTrace(t *testing.T) {
	t.Parallel()

	const (
		key  = "secret-api-key"
		user = "sensitive-user"
	)
	var trace bytes.Buffer
	transport := &strictTransport{
		config: clientConfig{
			authMode:         "api_key",
			apiKey:           key,
			requesterUser:    user,
			credentialHeader: "X-EACG-API-Key",
			subjectHeader:    "X-EACG-Requester-UserID",
		},
		trace: &trace,
		base: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			if request.Header.Get("X-EACG-API-Key") != key ||
				request.Header.Get("X-EACG-Requester-UserID") != user {
				t.Fatal("API Key 认证 Header 没有正确注入")
			}
			raw, err := io.ReadAll(request.Body)
			if err != nil || !strings.Contains(string(raw), `"jsonrpc"`) {
				t.Fatalf("追踪后请求体不可读：raw=%s err=%v", raw, err)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body: io.NopCloser(strings.NewReader(
					`{"jsonrpc":"2.0","id":1,"result":{"requester":"sensitive-user"}}`,
				)),
				Request: request,
			}, nil
		}),
	}
	response, err := transport.RoundTrip(newProtocolRequest(t, "server/discover"))
	if err != nil {
		t.Fatalf("请求失败：%v", err)
	}
	raw, err := io.ReadAll(response.Body)
	if err != nil || !strings.Contains(string(raw), `"result"`) {
		t.Fatalf("追踪后响应体不可读：raw=%s err=%v", raw, err)
	}
	output := trace.String()
	if strings.Contains(output, key) || strings.Contains(output, user) {
		t.Fatalf("协议追踪泄露认证信息：%s", output)
	}
	if !strings.Contains(output, "[REDACTED]") || !strings.Contains(output, "[SET]") {
		t.Fatalf("协议追踪缺少脱敏提示：%s", output)
	}
}

// TestStrictTransportInjectsBearerToken 验证 JWT 使用标准 Bearer Header。
func TestStrictTransportInjectsBearerToken(t *testing.T) {
	t.Parallel()

	const token = "secret-jwt"
	transport := &strictTransport{
		config: clientConfig{authMode: "jwt", token: token},
		base: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			if request.Header.Get("Authorization") != "Bearer "+token {
				t.Fatal("Bearer Token 没有正确注入")
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{}`)),
				Request:    request,
			}, nil
		}),
	}
	if _, err := transport.RoundTrip(newProtocolRequest(t, "tools/list")); err != nil {
		t.Fatalf("请求失败：%v", err)
	}
}

// TestStrictTransportSupportsAPIKeyWithoutUser 验证纯服务模式不会发送用户 Header。
func TestStrictTransportSupportsAPIKeyWithoutUser(t *testing.T) {
	t.Parallel()

	transport := &strictTransport{
		config: clientConfig{
			authMode:         "api_key",
			apiKey:           "service-key",
			credentialHeader: "X-EACG-API-Key",
			subjectHeader:    "X-EACG-Requester-UserID",
		},
		base: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			if request.Header.Get("X-EACG-API-Key") != "service-key" {
				t.Fatal("API Key 没有正确注入")
			}
			if len(request.Header.Values("X-EACG-Requester-UserID")) != 0 {
				t.Fatal("纯服务模式不应发送用户 Header")
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{}`)),
				Request:    request,
			}, nil
		}),
	}
	if _, err := transport.RoundTrip(newProtocolRequest(t, "tools/list")); err != nil {
		t.Fatalf("请求失败：%v", err)
	}
}

// TestStrictTransportRejectsOldProtocol 验证旧协议和旧状态不会发送到底层网络。
func TestStrictTransportRejectsOldProtocol(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		setup func(*http.Request)
	}{
		{
			name: "非 POST",
			setup: func(request *http.Request) {
				request.Method = http.MethodGet
			},
		},
		{
			name: "旧版本",
			setup: func(request *http.Request) {
				request.Header.Set("Mcp-Protocol-Version", "2025-11-25")
			},
		},
		{
			name: "缺少方法",
			setup: func(request *http.Request) {
				request.Header.Del("Mcp-Method")
			},
		},
		{
			name: "旧方法",
			setup: func(request *http.Request) {
				request.Header.Set("Mcp-Method", "initialize")
			},
		},
		{
			name: "旧状态 Header",
			setup: func(request *http.Request) {
				request.Header.Set("Mcp-Session-Id", "legacy")
			},
		},
		{
			name: "Tool 缺少名称",
			setup: func(request *http.Request) {
				request.Header.Set("Mcp-Method", "tools/call")
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			called := false
			transport := &strictTransport{
				config: clientConfig{authMode: "jwt", token: "token"},
				base: roundTripFunc(func(*http.Request) (*http.Response, error) {
					called = true
					return nil, nil
				}),
			}
			request := newProtocolRequest(t, "tools/list")
			test.setup(request)
			if _, err := transport.RoundTrip(request); err == nil {
				t.Fatal("非法协议请求不应成功")
			}
			if called {
				t.Fatal("非法协议请求不应进入底层网络")
			}
		})
	}
}

// TestReadPreviewRestoresTruncatedBody 验证截断预览不会丢失原始正文。
func TestReadPreviewRestoresTruncatedBody(t *testing.T) {
	t.Parallel()

	original := strings.Repeat("a", maxTraceBodyBytes+100)
	preview, truncated, restored, err := readPreview(io.NopCloser(strings.NewReader(original)))
	if err != nil {
		t.Fatalf("读取预览失败：%v", err)
	}
	if !truncated || len(preview) != maxTraceBodyBytes {
		t.Fatalf("正文应被安全截断：truncated=%v size=%d", truncated, len(preview))
	}
	raw, err := io.ReadAll(restored)
	if err != nil || string(raw) != original {
		t.Fatalf("恢复后的正文不完整：size=%d err=%v", len(raw), err)
	}
}
