// 本文件验证 HTTP Connector 的调用和安全限制。
package httpconnector

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
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
		BaseURL: server.URL + "/api",
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

// TestConnectorAllowsFixedPrivateBaseURL 验证固定 BaseURL 可直接使用私有地址或本地主机名。
func TestConnectorAllowsFixedPrivateBaseURL(t *testing.T) {
	t.Parallel()

	for _, baseURL := range []string{
		"http://127.0.0.1:8090",
		"http://localhost:8090",
		"https://service.internal.example.com",
	} {
		if _, err := New(Config{BaseURL: baseURL}); err != nil {
			t.Errorf("BaseURL %q 不应被拒绝：%v", baseURL, err)
		}
	}
}

// TestConnectorRedirectPolicy 验证只允许固定 Origin 内的重定向。
func TestConnectorRedirectPolicy(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/same-origin":
			http.Redirect(writer, request, server.URL+"/final", http.StatusFound)
		case "/final":
			_, _ = writer.Write([]byte(`{"ok":true}`))
		case "/cross-host":
			http.Redirect(writer, request, "http://example.com/final", http.StatusFound)
		case "/cross-scheme":
			http.Redirect(writer, request, "https://"+request.Host+"/final", http.StatusFound)
		case "/cross-port":
			http.Redirect(writer, request, "http://127.0.0.1:1/final", http.StatusFound)
		}
	}))
	defer server.Close()

	connector, err := New(Config{BaseURL: server.URL})
	if err != nil {
		t.Fatalf("创建 Connector 失败：%v", err)
	}
	if _, err := connector.Invoke(context.Background(), Request{Path: "same-origin"}); err != nil {
		t.Fatalf("同 Origin 重定向失败：%v", err)
	}
	for _, path := range []string{"cross-host", "cross-scheme", "cross-port"} {
		_, err := connector.Invoke(context.Background(), Request{Path: path})
		if !errors.Is(err, ErrUnsafeRedirect) {
			t.Errorf("%s 应返回 ErrUnsafeRedirect，实际为：%v", path, err)
		}
	}
}

// TestConnectorLimitsRedirects 验证默认最多跟随十次重定向。
func TestConnectorLimitsRedirects(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, "/loop", http.StatusFound)
	}))
	defer server.Close()
	connector, err := New(Config{BaseURL: server.URL})
	if err != nil {
		t.Fatalf("创建 Connector 失败：%v", err)
	}
	_, err = connector.Invoke(context.Background(), Request{Path: "loop"})
	if err == nil || !strings.Contains(err.Error(), "重定向次数超过 10 次") {
		t.Fatalf("预期重定向次数错误，实际为：%v", err)
	}
}

// TestConnectorPreservesCustomRedirectPolicy 验证 Connector 不修改共享 Client 并保留自定义策略。
func TestConnectorPreservesCustomRedirectPolicy(t *testing.T) {
	t.Parallel()

	customError := errors.New("自定义重定向拒绝")
	customPolicy := func(*http.Request, []*http.Request) error { return customError }
	client := &http.Client{CheckRedirect: customPolicy}
	connector, err := New(Config{
		BaseURL: "http://127.0.0.1:8090",
		Client:  client,
	})
	if err != nil {
		t.Fatalf("创建 Connector 失败：%v", err)
	}
	if connector.client == client {
		t.Fatal("Connector 不应直接修改调用方 Client")
	}
	if reflect.ValueOf(client.CheckRedirect).Pointer() != reflect.ValueOf(customPolicy).Pointer() {
		t.Fatal("调用方 Client 的重定向策略被修改")
	}
	request, err := http.NewRequest(http.MethodGet, "http://127.0.0.1:8090/next", nil)
	if err != nil {
		t.Fatalf("创建请求失败：%v", err)
	}
	if err := connector.client.CheckRedirect(request, nil); !errors.Is(err, customError) {
		t.Fatalf("自定义重定向策略未执行：%v", err)
	}
}
