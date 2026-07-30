// 本文件验证 HTTP Connector 的调用和安全限制。
package httpconnector

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// TestConnectorInvoke 验证 HTTP Connector 的请求和响应处理。
func TestConnectorInvoke(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/accounts/42" || r.URL.Query().Get("tenant") != "a" {
			t.Errorf("请求地址不正确：%s", r.URL.String())
		}
		if r.Header.Get("X-Request-ID") != "request-1" {
			t.Errorf("请求头没有透传：%v", r.Header)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"42"}`))
	}))
	defer server.Close()

	connector, err := New(Config{
		BaseURL:      server.URL + "/api",
		AllowedHosts: []string{"127.0.0.1"},
	})
	if err != nil {
		t.Fatalf("创建 Connector 失败：%v", err)
	}
	response, err := connector.Invoke(context.Background(), Request{
		Path:  "accounts/42",
		Query: url.Values{"tenant": []string{"a"}},
		Headers: http.Header{
			"X-Request-ID": []string{"request-1"},
		},
	})
	if err != nil {
		t.Fatalf("调用下游失败：%v", err)
	}
	if string(response.Body) != `{"id":"42"}` {
		t.Fatalf("响应不正确：%s", response.Body)
	}
}

// TestConnectorRejectsUnsafePathAndLargeResponse 验证 SSRF 路径和大响应保护。
func TestConnectorRejectsUnsafePathAndLargeResponse(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`"` + strings.Repeat("x", 32) + `"`))
	}))
	defer server.Close()
	connector, err := New(Config{
		BaseURL:          server.URL,
		AllowedHosts:     []string{"127.0.0.1"},
		MaxResponseBytes: 8,
	})
	if err != nil {
		t.Fatalf("创建 Connector 失败：%v", err)
	}

	if _, err := connector.Invoke(context.Background(), Request{Path: "http://evil.test"}); err == nil {
		t.Fatal("绝对 URL 不应被接受")
	}
	_, err = connector.Invoke(context.Background(), Request{Path: "large"})
	if !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("大响应应返回 ErrResponseTooLarge，实际为：%v", err)
	}
}
