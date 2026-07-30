// Command eacg-client 使用官方 SDK 演示 MCP 2026-07-28 调用流程。
package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"syscall"
)

// main 加载参数并以正确退出码运行教学客户端。
func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(execute(ctx, os.Args[1:], os.Getenv, os.Stdout, os.Stderr, nil))
}

// execute 组装配置和客户端，方便命令测试复用。
func execute(
	ctx context.Context,
	args []string,
	lookup func(string) string,
	stdout io.Writer,
	stderr io.Writer,
	base http.RoundTripper,
) int {
	config, err := loadConfig(args, lookup)
	if err == nil {
		err = runClient(ctx, config, stdout, stderr, base)
	}
	if err != nil {
		fmt.Fprintf(stderr, "eacg-client：%v\n", err)
		return 1
	}
	return 0
}
