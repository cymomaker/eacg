# EACG

EACG（Enterprise Agent Capability Gateway）是一个面向企业 Agent 的能力网关 Go Module。

当前仓库实现 `v0.1.0` HTTP-only MVP：

- MCP Streamable HTTP；
- Typed Capability；
- Bearer Token/JWT；
- Service Token/API Key 与实际用户复合认证；
- Tenant、Principal 和 Capability 级 RBAC；
- 动态 Tool 可见性；
- 输入输出 JSON Schema；
- R0/R1 只读能力；
- HTTP Connector；
- 输出字段白名单与敏感字段遮盖；
- 结构化审计；
- Health、Readiness 和优雅停机。

MVP 不包含 gRPC、Kitex、下游 MCP、STDIO、R2/R3 写操作、Prometheus 和 OpenTelemetry。

## 环境要求

- Go 1.25 或更高版本；
- Make；
- Docker，可选。

## 运行测试

```bash
make test
make test-race
make vet
```

## 启动示例

示例程序会同时启动：

- EACG：`http://127.0.0.1:8080/mcp`
- 演示下游 HTTP 服务：`http://127.0.0.1:8090`

```bash
make run
```

默认使用 JWT。若要演示企业微信风格的固定 API Key 加用户 Header：

```bash
make run-api-key
```

另开一个终端生成测试 JWT：

```bash
make token
```

默认令牌包含：

- Tenant：`tenant-a`
- User：`user-1`
- Role：`reader`
- 有效期：1 小时

把令牌配置到 MCP Host 的 `Authorization: Bearer <token>` 请求头，然后连接：

```text
http://127.0.0.1:8080/mcp
```

可调用 Tool：

```json
{
  "name": "get_profile",
  "arguments": {
    "user_id": "42"
  }
}
```

## 使用 curl 调用 MCP

以下命令已经在本地示例服务上验证。整个会话必须使用同一个 `TOKEN` 和 `SESSION_ID`。

先启动服务并生成 JWT：

```bash
make run
```

另开一个终端执行：

```bash
TOKEN=$(make -s token)
MCP_URL=http://127.0.0.1:8080/mcp
HEADER_FILE=/tmp/eacg-mcp-headers
```

### 1. 初始化 MCP Session

```bash
curl -sS -N \
  -D "$HEADER_FILE" \
  -X POST "$MCP_URL" \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  --data '{
    "jsonrpc": "2.0",
    "id": 1,
    "method": "initialize",
    "params": {
      "protocolVersion": "2025-11-25",
      "capabilities": {},
      "clientInfo": {
        "name": "curl-client",
        "version": "1.0.0"
      }
    }
  }'
```

响应使用 SSE 格式：

```text
event: message
data: {"jsonrpc":"2.0","id":1,"result":{...}}
```

从响应头提取 Session ID：

```bash
SESSION_ID=$(
  awk 'BEGIN{IGNORECASE=1} /^Mcp-Session-Id:/ {
    gsub("\r", "", $2)
    print $2
  }' "$HEADER_FILE"
)
echo "$SESSION_ID"
```

### 2. 发送初始化完成通知

```bash
curl -sS -i \
  -X POST "$MCP_URL" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Mcp-Session-Id: $SESSION_ID" \
  -H 'Mcp-Protocol-Version: 2025-11-25' \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  --data '{
    "jsonrpc": "2.0",
    "method": "notifications/initialized"
  }'
```

成功时返回 `HTTP 202 Accepted`。

### 3. 查询可用 Tool

```bash
curl -sS -N \
  -X POST "$MCP_URL" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Mcp-Session-Id: $SESSION_ID" \
  -H 'Mcp-Protocol-Version: 2025-11-25' \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  --data '{
    "jsonrpc": "2.0",
    "id": 2,
    "method": "tools/list",
    "params": {}
  }'
```

返回列表中应包含 `get_profile`。

### 4. 调用 get_profile

```bash
curl -sS -N \
  -X POST "$MCP_URL" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Mcp-Session-Id: $SESSION_ID" \
  -H 'Mcp-Protocol-Version: 2025-11-25' \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  --data '{
    "jsonrpc": "2.0",
    "id": 3,
    "method": "tools/call",
    "params": {
      "name": "get_profile",
      "arguments": {
        "user_id": "42"
      }
    }
  }'
```

结构化结果示例：

```json
{
  "email": "42@example.com",
  "name": "示例用户",
  "user_id": "42"
}
```

### 5. 关闭 MCP Session

```bash
curl -sS -i \
  -X DELETE "$MCP_URL" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Mcp-Session-Id: $SESSION_ID" \
  -H 'Mcp-Protocol-Version: 2025-11-25'
```

成功关闭时返回 `HTTP 204 No Content`；如果 Session 已关闭或已过期，则可能返回 `404`。

如果返回内容以 `event: message` 和 `data:` 开头，表示服务正在按 MCP Streamable HTTP 规范返回 SSE，而不是普通 JSON。

## 使用 API Key 和 requester userid 调用

API Key 模式使用两个 Header：

- `X-EACG-API-Key`：认证企业微信机器人或其他调用应用；
- `X-EACG-Requester-UserID`：标识当前实际提问用户。

先执行 `make run-api-key`，然后准备调用参数：

```bash
API_KEY=0123456789abcdef0123456789abcdef
REQUESTER_USER_ID=zhangsan
MCP_URL=http://127.0.0.1:8080/mcp
HEADER_FILE=/tmp/eacg-api-key-mcp-headers
```

初始化 MCP Session：

```bash
curl -sS -N \
  -D "$HEADER_FILE" \
  -X POST "$MCP_URL" \
  -H "X-EACG-API-Key: $API_KEY" \
  -H "X-EACG-Requester-UserID: $REQUESTER_USER_ID" \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  --data '{
    "jsonrpc": "2.0",
    "id": 1,
    "method": "initialize",
    "params": {
      "protocolVersion": "2025-11-25",
      "capabilities": {},
      "clientInfo": {
        "name": "curl-api-key-client",
        "version": "1.0.0"
      }
    }
  }'
```

提取 Session ID：

```bash
SESSION_ID=$(
  awk 'BEGIN{IGNORECASE=1} /^Mcp-Session-Id:/ {
    gsub("\r", "", $2)
    print $2
  }' "$HEADER_FILE"
)
echo "$SESSION_ID"
```

发送初始化完成通知：

```bash
curl -sS -i \
  -X POST "$MCP_URL" \
  -H "X-EACG-API-Key: $API_KEY" \
  -H "X-EACG-Requester-UserID: $REQUESTER_USER_ID" \
  -H "Mcp-Session-Id: $SESSION_ID" \
  -H 'Mcp-Protocol-Version: 2025-11-25' \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  --data '{
    "jsonrpc": "2.0",
    "method": "notifications/initialized"
  }'
```

查询 Tool：

```bash
curl -sS -N \
  -X POST "$MCP_URL" \
  -H "X-EACG-API-Key: $API_KEY" \
  -H "X-EACG-Requester-UserID: $REQUESTER_USER_ID" \
  -H "Mcp-Session-Id: $SESSION_ID" \
  -H 'Mcp-Protocol-Version: 2025-11-25' \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  --data '{
    "jsonrpc": "2.0",
    "id": 2,
    "method": "tools/list",
    "params": {}
  }'
```

调用 `get_profile`：

```bash
curl -sS -N \
  -X POST "$MCP_URL" \
  -H "X-EACG-API-Key: $API_KEY" \
  -H "X-EACG-Requester-UserID: $REQUESTER_USER_ID" \
  -H "Mcp-Session-Id: $SESSION_ID" \
  -H 'Mcp-Protocol-Version: 2025-11-25' \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  --data '{
    "jsonrpc": "2.0",
    "id": 3,
    "method": "tools/call",
    "params": {
      "name": "get_profile",
      "arguments": {
        "user_id": "42"
      }
    }
  }'
```

关闭 Session：

```bash
curl -sS -i \
  -X DELETE "$MCP_URL" \
  -H "X-EACG-API-Key: $API_KEY" \
  -H "X-EACG-Requester-UserID: $REQUESTER_USER_ID" \
  -H "Mcp-Session-Id: $SESSION_ID" \
  -H 'Mcp-Protocol-Version: 2025-11-25'
```

同一 Session 的每个请求都必须携带相同 API Key 和 requester userid。更换其中任意一项都会使 Session 身份校验失败。

企业微信插件中应选择 `Header`，Parameter name 配置为 `X-EACG-API-Key`。requester userid Header 的实际名称由企业微信接入链路决定，再通过 `EACG_REQUESTER_USER_HEADER` 告诉 EACG。

## Docker

```bash
make docker-build
docker run --rm -p 8080:8080 \
  -e EACG_JWT_SECRET=0123456789abcdef0123456789abcdef \
  eacg-example:local
```

示例默认密钥只适合本地学习，生产环境必须使用企业密钥管理系统。

API Key 模式：

```bash
docker run --rm -p 8080:8080 \
  -e EACG_AUTH_MODE=api_key \
  -e EACG_API_KEY=0123456789abcdef0123456789abcdef \
  -e EACG_CREDENTIAL_HEADER=X-EACG-API-Key \
  -e EACG_REQUESTER_USER_HEADER=X-EACG-Requester-UserID \
  eacg-example:local
```

## 最小接入示例

```go
type Input struct {
    ID string `json:"id"`
}

type Output struct {
    ID   string `json:"id"`
    Name string `json:"name"`
}

item, err := capability.New(capability.Descriptor{
    ID:            "get_user.v1",
    Name:          "get_user",
    Version:       "v1",
    Description:   "查询用户基础信息",
    RiskLevel:     capability.RiskR1,
    ReadOnly:      true,
    RequiredRoles: []string{"reader"},
}, func(ctx context.Context, request capability.RequestContext, input Input) (Output, error) {
    // 在这里通过 HTTP Connector 调用企业业务服务。
    return Output{ID: input.ID, Name: "示例用户"}, nil
})
```

## 文档

- [技术架构与设计文档](docs/EACG_技术架构与设计文档.md)
- [产品实现路线图](docs/EACG_产品实现路线图.md)
- [MCP 协议工作流程入门](docs/MCP协议工作流程入门.md)
- [MVP 代码架构与培训手册](docs/EACG_MVP代码架构与培训手册.md)
- [企业认证兼容方案](docs/EACG_企业认证兼容方案.md)
- [identity 包架构与认证体系说明](docs/EACG_identity包架构与认证体系说明.md)
- [版本变更记录](CHANGELOG.md)
- [安全策略](SECURITY.md)
