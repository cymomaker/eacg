# EACG

EACG 是面向企业业务的 MCP Tool 网关。`v0.3.0` 默认同时支持 MCP `2026-07-28` 和 `2025-06-18`，使用无状态 Streamable HTTP。

## 核心能力

- Typed Capability 和自动 JSON Schema；
- JWT、Service Token/API Key 认证；
- 用户身份与服务身份明确区分；
- 基于身份类型和角色的 Tool 可见性与执行时二次授权；
- 输入输出校验、字段白名单和敏感字段遮盖；
- HTTP Connector、结构化审计、健康检查和优雅停机；
- 无状态部署，不需要会话存储或负载均衡粘滞。

HTTP Connector 以配置中的 `BaseURL` 作为固定目标，只接受相对请求路径，并拒绝跨协议、主机或端口的重定向。目标服务仍需通过 API Key、OAuth2、mTLS 等机制独立鉴权。

普通 MCP 请求返回 `application/json`。只有将来启用 `subscriptions/listen` 时才使用 `text/event-stream` 长连接。

## 快速启动

要求 Go `1.25`。

```bash
make test
make run
```

默认地址：

```text
MCP:    http://127.0.0.1:8080/mcp
Health: http://127.0.0.1:8080/health
Ready:  http://127.0.0.1:8080/ready
```

生成演示 JWT：

```bash
TOKEN=$(make token | tail -n 1)
```

## JWT 完整调用示例

### 1. 查询 Server 信息

```bash
curl -sS http://127.0.0.1:8080/mcp \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -H "Accept: application/json, text/event-stream" \
  -H "Mcp-Protocol-Version: 2026-07-28" \
  -H "Mcp-Method: server/discover" \
  -d '{
    "jsonrpc": "2.0",
    "id": 1,
    "method": "server/discover",
    "params": {
      "_meta": {
        "io.modelcontextprotocol/protocolVersion": "2026-07-28",
        "io.modelcontextprotocol/clientCapabilities": {},
        "io.modelcontextprotocol/clientInfo": {
          "name": "curl-client",
          "version": "1.0.0"
        }
      }
    }
  }'
```

### 2. 查询 Tool

```bash
curl -sS http://127.0.0.1:8080/mcp \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -H "Accept: application/json, text/event-stream" \
  -H "Mcp-Protocol-Version: 2026-07-28" \
  -H "Mcp-Method: tools/list" \
  -d '{
    "jsonrpc": "2.0",
    "id": 2,
    "method": "tools/list",
    "params": {
      "_meta": {
        "io.modelcontextprotocol/protocolVersion": "2026-07-28",
        "io.modelcontextprotocol/clientCapabilities": {},
        "io.modelcontextprotocol/clientInfo": {
          "name": "curl-client",
          "version": "1.0.0"
        }
      }
    }
  }'
```

### 3. 调用 Tool

```bash
curl -sS http://127.0.0.1:8080/mcp \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -H "Accept: application/json, text/event-stream" \
  -H "Mcp-Protocol-Version: 2026-07-28" \
  -H "Mcp-Method: tools/call" \
  -H "Mcp-Name: get_profile" \
  -d '{
    "jsonrpc": "2.0",
    "id": 3,
    "method": "tools/call",
    "params": {
      "_meta": {
        "io.modelcontextprotocol/protocolVersion": "2026-07-28",
        "io.modelcontextprotocol/clientCapabilities": {},
        "io.modelcontextprotocol/clientInfo": {
          "name": "curl-client",
          "version": "1.0.0"
        }
      },
      "name": "get_profile",
      "arguments": {
        "user_id": "user-1001"
      }
    }
  }'
```

每一次请求都必须重新携带认证信息、协议版本、客户端能力和客户端信息。

## MCP 2025-06-18（企业微信）

旧版客户端的首次 `initialize` 可以不带 MCP 协议 Header；服务端从请求体协商版本。后续请求必须携带 `Mcp-Protocol-Version: 2025-06-18`，但不需要新版的 `Mcp-Method` 和 `Mcp-Name` Header：

```bash
curl -sS http://127.0.0.1:8080/mcp \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -H "Accept: application/json, text/event-stream" \
  -d '{
    "jsonrpc":"2.0",
    "id":0,
    "method":"initialize",
    "params":{
      "protocolVersion":"2025-06-18",
      "capabilities":{},
      "clientInfo":{"name":"wework-mcp-client","version":"1.0.0"}
    }
  }'
```

两种协议共用 `/mcp`，均按请求认证且不创建 `Mcp-Session-Id`。可通过 `Config.MCPProtocolVersions` 显式启用单一版本；未配置时按新到旧启用两个版本。

## API Key 模式

启动：

```bash
make run-api-key
```

调用时把 JWT Header 替换为：

```bash
-H "X-EACG-API-Key: 0123456789abcdef0123456789abcdef"
```

企业微信插件建议配置：

```text
授权方式：Service token / API key
位置：Header
Parameter name：X-EACG-API-Key
传输协议：Streamable HTTP
```

内置 API Key 认证器把 Key 映射为服务身份，不要求 userid。企业微信等代理用户场景应由业务项目实现自定义 `Authenticator`，并通过可选 `SubjectHeader` 接收可信 requester userid。

## Go 教学客户端

`cmd/eacg-client` 使用官方 Go SDK 展示 `2026-07-28` 严格协议流程，并对认证信息做脱敏。服务端兼容旧协议不改变该教学客户端的回归目标。

JWT 模式需要两个终端：

```bash
# 终端一
make run

# 终端二
make client
```

API Key 模式：

```bash
# 终端一
make run-api-key

# 终端二
make client-api-key
```

默认依次执行自动发现、`tools/list` 和 `tools/call get_profile`。也可以分步运行：

```bash
EACG_CLIENT_TOKEN="$TOKEN" go run ./cmd/eacg-client --action discover
EACG_CLIENT_TOKEN="$TOKEN" go run ./cmd/eacg-client --action list
EACG_CLIENT_TOKEN="$TOKEN" go run ./cmd/eacg-client --action call \
  --tool get_profile \
  --arguments '{"user_id":"user-1001"}'
```

协议追踪写入 stderr，业务结果写入 stdout。生产数据调试时可以使用 `--trace=false`。推荐通过环境变量传递 Token 和 API Key，避免密钥出现在 shell 历史中。

## 业务接入

```go
app, err := eacg.New(
    eacg.Config{
        Name:                "business-mcp-server",
        Version:             "v1.0.0",
        Address:             "127.0.0.1:8080",
        ExecutionTimeout:    5 * time.Second,
        MaxRequestBodyBytes: 4 << 20,
    },
    eacg.HTTPAuthenticationConfig{
        Authenticator: authenticator,
    },
    auditSink,
)
```

业务系统实现 `identity.Authenticator`，或直接使用 EACG 提供的 JWT/API Key 认证器。JWT 默认生成用户身份，API Key 默认生成服务身份。Capability 不直接读取 HTTP Header，租户、调用者和权限统一从 `identity.Principal` 获取；需要真实用户的 Tool 应声明 `IdentityUser`。

## 工程命令

```bash
make test
make test-race
make vet
make cover
make build
make docker-build
```

详细说明：

- [MCP 协议工作流程](docs/MCP协议工作流程入门.md)
- [代码架构与培训手册](docs/EACG_MVP代码架构与培训手册.md)
- [identity 认证体系](docs/EACG_identity包架构与认证体系说明.md)
- [企业认证兼容方案](docs/EACG_企业认证兼容方案.md)
- [MCP Client 示例说明](docs/EACG_MCP客户端示例说明.md)
