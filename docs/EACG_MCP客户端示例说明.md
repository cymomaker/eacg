# EACG MCP Client 示例说明

## 1. 示例解决什么问题

`cmd/eacg-client` 是教学和联调工具。它使用官方 Go SDK 调用 `eacg-example`，同时打印脱敏后的 HTTP 和 JSON-RPC 报文。服务端默认兼容两个协议版本，但本客户端刻意只验证最新版 `2026-07-28`，用于防止新协议回归。

```text
eacg-client
  → SDK 自动 server/discover
  → tools/list
  → tools/call get_profile
  → eacg-example
```

它不是 EACG Server 的运行时依赖，也不是通用 HTTP 代理。

## 2. 快速运行

JWT：

```bash
# 终端一
make run

# 终端二
make client
```

API Key：

```bash
# 终端一
make run-api-key

# 终端二
make client-api-key
```

`make client` 会调用本地 `eacg-token` 生成短期 JWT。生产客户端应从企业认证系统获得 Token，不能自行使用共享 Secret 签发。

## 3. SDK 自动发现与 curl 的关系

curl 示例需要手动发送 `server/discover`。Go SDK 在 `Client.Connect` 内自动完成同一请求：

```go
session, err := client.Connect(ctx, transport, nil)
```

SDK 返回类型目前仍命名为 `InitializeResult`，这是 SDK 为兼容调用接口保留的 Go 类型名称。线上实际发送的是 `server/discover`，不会创建服务器端协议状态。

代码中的 `ClientSession` 也是客户端侧的逻辑对象，它保存 HTTP Client 和协商结果，方便调用 `ListTools`、`CallTool`。它不对应服务端保存的 Session。

## 4. 为什么显式设置 ClientOptions

示例使用：

```go
&mcp.ClientOptions{
    Capabilities:   &mcp.ClientCapabilities{},
    MultiRoundTrip: &mcp.MultiRoundTripOptions{Disabled: true},
}
```

原因：

- SDK 的 nil Capabilities 会使用历史默认值；
- EACG Client 只消费 Tool，不需要声明其他能力；
- 本示例不实现 MRTR；
- KeepAlive 保持零值；
- 不注册 Sampling、Logging、Elicitation 或订阅 Handler。

客户端连接后还会检查协商版本必须等于 `2026-07-28`。

## 5. strictTransport

SDK 负责生成 MCP 请求，`strictTransport` 负责企业接入边界：

1. 检查请求只能是 POST；
2. 检查协议版本和方法 Header；
3. 阻止旧方法和旧状态 Header；
4. JWT 模式注入 Authorization；
5. API Key 模式注入 Key；只有显式提供 requester userid 时才注入可选 Subject Header；
6. 输出安全协议追踪；
7. 调用真实网络 Transport。

如果 SDK 尝试发送非 `2026-07-28` 请求，Transport 会在本地返回错误，不会把请求发送到网络。

## 6. 如何阅读 trace

请求示例：

```text
>>> POST http://127.0.0.1:8080/mcp
Mcp-Protocol-Version: 2026-07-28
Mcp-Method: tools/call
Mcp-Name: get_profile
Authorization: [REDACTED]
```

下面的 JSON 是 SDK 生成的 JSON-RPC Body。可以对照 Header 中的 Method、Name、Version 是否一致。

响应示例：

```text
<<< HTTP 200
Content-Type: application/json
X-Request-ID: req-...
```

追踪最多显示 64 KiB，读取后会恢复 Body，所以不会影响 SDK 继续解析。Token 和 Key 显示 `[REDACTED]`；可选 requester userid 显示 `[SET]`。

内置 API Key 认证器不需要 requester userid，因此 `make client-api-key` 默认只发送 Key。`--requester-user` 仅用于业务自定义代理用户认证联调。

生产数据可能包含个人信息，应使用：

```bash
--trace=false
```

## 7. Action

| Action | 行为 |
| --- | --- |
| `discover` | 只连接并打印发现结果 |
| `list` | 自动发现后打印可见 Tool |
| `call` | 自动发现后直接调用指定 Tool |
| `flow` | 自动发现、列表和调用，默认值 |

示例：

```bash
EACG_CLIENT_TOKEN="$TOKEN" go run ./cmd/eacg-client \
  --action call \
  --tool get_profile \
  --arguments '{"user_id":"user-1001"}'
```

Tool 参数必须是 JSON Object。

## 8. 建议阅读顺序

1. `config.go`：参数和安全校验；
2. `transport.go`：协议 Header、认证和脱敏；
3. `client.go`：SDK 发现、列表和调用；
4. `main.go`：命令入口和退出码；
5. `transport_test.go`：如何验证密钥不泄露；
6. `main_test.go`：如何连接真实 EACG Handler。
