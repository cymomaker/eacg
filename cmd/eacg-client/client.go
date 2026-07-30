// 本文件使用官方 SDK 执行发现、列表和 Tool 调用。
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// runClient 执行配置指定的 MCP Client 动作。
func runClient(
	ctx context.Context,
	config clientConfig,
	stdout io.Writer,
	stderr io.Writer,
	base http.RoundTripper,
) error {
	ctx, cancel := context.WithTimeout(ctx, config.timeout)
	defer cancel()

	var traceWriter io.Writer
	if config.trace {
		traceWriter = stderr
	}
	httpClient := &http.Client{
		Transport: &strictTransport{
			base:   base,
			config: config,
			trace:  traceWriter,
		},
	}
	client := mcp.NewClient(
		&mcp.Implementation{Name: "eacg-client", Version: "v0.2.0"},
		&mcp.ClientOptions{
			Capabilities:   &mcp.ClientCapabilities{},
			MultiRoundTrip: &mcp.MultiRoundTripOptions{Disabled: true},
		},
	)

	fmt.Fprintln(stderr, "阶段 1：SDK 自动调用 server/discover")
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:   config.endpoint,
		HTTPClient: httpClient,
	}, nil)
	if err != nil {
		return fmt.Errorf("连接 EACG：%w", err)
	}
	defer session.Close()

	// SDK 仍使用 InitializeResult 作为协商结果类型，但新版线上请求是 server/discover。
	discovery := session.InitializeResult()
	if discovery == nil || discovery.ProtocolVersion != requiredProtocolVersion {
		return fmt.Errorf("服务端没有协商 MCP 2026-07-28")
	}
	if err := printDiscovery(stdout, discovery); err != nil {
		return err
	}
	if config.action == "discover" {
		return nil
	}

	if config.action == "flow" || config.action == "list" {
		fmt.Fprintln(stderr, "阶段 2：调用 tools/list")
		tools, err := session.ListTools(ctx, nil)
		if err != nil {
			return fmt.Errorf("查询 Tool：%w", err)
		}
		if err := printTools(stdout, tools); err != nil {
			return err
		}
		if config.action == "list" {
			return nil
		}
	}

	fmt.Fprintf(stderr, "阶段 3：调用 tools/call %s\n", config.tool)
	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      config.tool,
		Arguments: config.arguments,
	})
	if err != nil {
		return fmt.Errorf("调用 Tool：%w", err)
	}
	if err := printCallResult(stdout, result); err != nil {
		return err
	}
	if result.IsError {
		return fmt.Errorf("Tool 返回业务错误")
	}
	return nil
}

// printDiscovery 输出 SDK 从 server/discover 得到的协商结果。
func printDiscovery(writer io.Writer, result *mcp.InitializeResult) error {
	serverName := ""
	serverVersion := ""
	if result.ServerInfo != nil {
		serverName = result.ServerInfo.Name
		serverVersion = result.ServerInfo.Version
	}
	value := map[string]any{
		"protocolVersion": result.ProtocolVersion,
		"serverName":      serverName,
		"serverVersion":   serverVersion,
		"capabilities":    result.Capabilities,
	}
	return writeJSON(writer, "发现结果", value)
}

// printTools 输出当前身份可见的 Tool。
func printTools(writer io.Writer, result *mcp.ListToolsResult) error {
	type visibleTool struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		ReadOnly    bool   `json:"readOnly"`
	}
	items := make([]visibleTool, 0, len(result.Tools))
	for _, tool := range result.Tools {
		readOnly := false
		if tool.Annotations != nil {
			readOnly = tool.Annotations.ReadOnlyHint
		}
		items = append(items, visibleTool{
			Name:        tool.Name,
			Description: tool.Description,
			ReadOnly:    readOnly,
		})
	}
	return writeJSON(writer, "Tool 列表", items)
}

// printCallResult 输出结构化 Tool 结果或文本内容。
func printCallResult(writer io.Writer, result *mcp.CallToolResult) error {
	if result.StructuredContent != nil {
		return writeJSON(writer, "Tool 结果", result.StructuredContent)
	}
	return writeJSON(writer, "Tool 结果", result.Content)
}

// writeJSON 使用易读格式输出一个阶段的业务结果。
func writeJSON(writer io.Writer, title string, value any) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("编码%s：%w", title, err)
	}
	fmt.Fprintf(writer, "\n=== %s ===\n%s\n", title, raw)
	return nil
}
