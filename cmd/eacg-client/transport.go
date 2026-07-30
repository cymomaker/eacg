// 本文件实现认证注入、严格协议检查和安全报文追踪。
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const (
	requiredProtocolVersion = "2026-07-28"
	maxTraceBodyBytes       = 64 << 10
)

// strictTransport 为 SDK 请求增加认证并阻止旧协议回退。
type strictTransport struct {
	base   http.RoundTripper
	config clientConfig
	trace  io.Writer
}

// replayReadCloser 在重放已读取数据后继续读取并关闭原始 Body。
type replayReadCloser struct {
	io.Reader
	closer io.Closer
}

// Close 关闭原始 HTTP Body。
func (r *replayReadCloser) Close() error {
	return r.closer.Close()
}

// RoundTrip 校验新版协议、注入认证并调用底层 Transport。
func (t *strictTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if err := validateProtocolRequest(request); err != nil {
		return nil, err
	}
	cloned := request.Clone(request.Context())
	cloned.Header = request.Header.Clone()
	switch t.config.authMode {
	case "jwt":
		cloned.Header.Set("Authorization", "Bearer "+t.config.token)
	case "api_key":
		cloned.Header.Set(t.config.credentialHeader, t.config.apiKey)
		if t.config.requesterUser != "" {
			cloned.Header.Set(t.config.subjectHeader, t.config.requesterUser)
		}
	}
	if t.trace != nil {
		if err := traceRequest(t.trace, cloned, t.config); err != nil {
			return nil, fmt.Errorf("读取请求追踪：%w", err)
		}
	}
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	response, err := base.RoundTrip(cloned)
	if err != nil {
		return nil, err
	}
	if t.trace != nil {
		if err := traceResponse(t.trace, response, t.config); err != nil {
			_ = response.Body.Close()
			return nil, fmt.Errorf("读取响应追踪：%w", err)
		}
	}
	return response, nil
}

// validateProtocolRequest 保证客户端只发送 MCP 2026-07-28 请求。
func validateProtocolRequest(request *http.Request) error {
	if request.Method != http.MethodPost {
		return fmt.Errorf("MCP Client 只允许 POST")
	}
	versions := request.Header.Values("Mcp-Protocol-Version")
	if len(versions) != 1 || versions[0] != requiredProtocolVersion {
		return fmt.Errorf("拒绝发送非 MCP 2026-07-28 请求")
	}
	methods := request.Header.Values("Mcp-Method")
	if len(methods) != 1 || strings.TrimSpace(methods[0]) == "" {
		return fmt.Errorf("Mcp-Method Header 缺失")
	}
	if removedClientMethod(methods[0]) {
		return fmt.Errorf("拒绝发送已删除的 MCP 方法")
	}
	if methods[0] == "tools/call" {
		names := request.Header.Values("Mcp-Name")
		if len(names) != 1 || strings.TrimSpace(names[0]) == "" {
			return fmt.Errorf("Tool 调用缺少 Mcp-Name Header")
		}
	}
	if len(request.Header.Values("Mcp-Session-Id")) > 0 ||
		len(request.Header.Values("Last-Event-ID")) > 0 {
		return fmt.Errorf("拒绝发送旧状态 Header")
	}
	return nil
}

// removedClientMethod 判断方法是否已经从新版协议删除。
func removedClientMethod(method string) bool {
	switch method {
	case "initialize",
		"notifications/initialized",
		"ping",
		"logging/setLevel",
		"resources/subscribe",
		"resources/unsubscribe":
		return true
	default:
		return false
	}
}

// traceRequest 输出脱敏后的 HTTP 请求。
func traceRequest(writer io.Writer, request *http.Request, config clientConfig) error {
	fmt.Fprintf(writer, "\n>>> %s %s\n", request.Method, request.URL.String())
	traceHeader(writer, request.Header, "Mcp-Protocol-Version", "")
	traceHeader(writer, request.Header, "Mcp-Method", "")
	traceHeader(writer, request.Header, "Mcp-Name", "")
	traceHeader(writer, request.Header, "Content-Type", "")
	traceHeader(writer, request.Header, "Accept", "")
	if config.authMode == "jwt" {
		traceHeader(writer, request.Header, "Authorization", "[REDACTED]")
	} else {
		traceHeader(writer, request.Header, config.credentialHeader, "[REDACTED]")
		if config.requesterUser != "" {
			traceHeader(writer, request.Header, config.subjectHeader, "[SET]")
		}
	}
	if request.Body == nil {
		return nil
	}
	preview, truncated, restored, err := readPreview(request.Body)
	if err != nil {
		return err
	}
	request.Body = restored
	fmt.Fprintf(writer, "\n%s", redactTraceText(prettyJSON(preview), config))
	if truncated {
		fmt.Fprint(writer, "\n...[已截断]")
	}
	fmt.Fprintln(writer)
	return nil
}

// traceResponse 输出 HTTP 状态、响应类型和脱敏后的正文预览。
func traceResponse(writer io.Writer, response *http.Response, config clientConfig) error {
	fmt.Fprintf(writer, "\n<<< HTTP %d\n", response.StatusCode)
	traceHeader(writer, response.Header, "Content-Type", "")
	traceHeader(writer, response.Header, "X-Request-ID", "")
	if response.Body == nil {
		return nil
	}
	preview, truncated, restored, err := readPreview(response.Body)
	if err != nil {
		return err
	}
	response.Body = restored
	fmt.Fprintf(writer, "\n%s", redactTraceText(prettyJSON(preview), config))
	if truncated {
		fmt.Fprint(writer, "\n...[已截断]")
	}
	fmt.Fprintln(writer)
	return nil
}

// redactTraceText 隐藏追踪正文中可能出现的认证信息。
func redactTraceText(value string, config clientConfig) string {
	if config.token != "" {
		value = strings.ReplaceAll(value, config.token, "[REDACTED]")
	}
	if config.apiKey != "" {
		value = strings.ReplaceAll(value, config.apiKey, "[REDACTED]")
	}
	if config.requesterUser != "" {
		value = strings.ReplaceAll(value, config.requesterUser, "[SET]")
	}
	return value
}

// traceHeader 输出存在的 Header，并允许使用安全替代值。
func traceHeader(writer io.Writer, header http.Header, name, replacement string) {
	if len(header.Values(name)) == 0 {
		return
	}
	value := strings.Join(header.Values(name), ", ")
	if replacement != "" {
		value = replacement
	}
	fmt.Fprintf(writer, "%s: %s\n", name, value)
}

// readPreview 读取有限正文并返回不会丢失数据的新 Body。
func readPreview(body io.ReadCloser) ([]byte, bool, io.ReadCloser, error) {
	data, err := io.ReadAll(io.LimitReader(body, maxTraceBodyBytes+1))
	if err != nil {
		return nil, false, nil, err
	}
	truncated := len(data) > maxTraceBodyBytes
	preview := data
	if truncated {
		preview = data[:maxTraceBodyBytes]
	}
	restored := &replayReadCloser{
		Reader: io.MultiReader(bytes.NewReader(data), body),
		closer: body,
	}
	return preview, truncated, restored, nil
}

// prettyJSON 美化合法 JSON，其他内容保持原样。
func prettyJSON(raw []byte) string {
	var output bytes.Buffer
	if json.Indent(&output, raw, "", "  ") == nil {
		return output.String()
	}
	return string(raw)
}
