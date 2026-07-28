# EACG MVP 代码架构与培训手册

> 面向初级 Go 后端工程师

- 对应版本：`v0.1.0`
- 技术范围：Go、MCP Streamable HTTP、HTTP Connector
- 阅读目标：理解一次 Tool 调用怎样安全地到达企业 HTTP 服务

---

## 1. MVP 实现了什么

EACG MVP 把企业提供的 Go 处理函数注册成 MCP Tool。

一次请求的完整路径是：

```text
MCP Host
   ↓ Authorization: Bearer JWT
net/http
   ↓
MCP Streamable HTTP
   ↓
按身份生成 Tool 列表
   ↓
固定执行管线
   ↓
Typed Capability
   ↓
HTTP Connector
   ↓
企业 HTTP 服务
```

MVP 只允许 R0、R1 只读能力。R2、R3 写操作在注册阶段就会被拒绝。

---

## 2. 为什么首版使用 net/http

官方 MCP Go SDK 原生提供 `net/http.Handler`。首版直接使用标准库可以减少以下风险：

- SSE Flush 不及时；
- Context Cancel 丢失；
- 长连接被框架缓存；
- Header 或 Session ID 适配错误。

MCP Transport 位于独立 package。未来增加 Hertz 时，不需要修改 Capability、Registry 和 Execution Engine。

---

## 3. 目录说明

```text
eacg/
├── app.go                          应用组装和生命周期
├── audit/                          审计接口和默认实现
├── capability/                     Capability 核心模型
├── connector/httpconnector/        下游 HTTP 调用
├── execution/                      固定执行管线
├── identity/                       Principal 和 JWT
├── protocol/mcphttp/               MCP 协议适配
├── registry/                       能力注册表
├── cmd/eacg-example/               可运行示例
├── cmd/eacg-token/                 本地 JWT 工具
├── docs/                           设计和培训文档
├── Makefile                        常用开发命令
└── Dockerfile                      容器构建
```

依赖方向为：

```text
app
 ├── protocol/mcphttp
 ├── execution
 ├── registry
 ├── identity
 └── audit

protocol/mcphttp
 ├── official MCP SDK
 ├── execution
 └── registry

execution
 ├── capability
 ├── registry
 └── audit
```

业务项目只需要使用根 package、`capability` 和所需 Connector，不需要依赖 MCP SDK。

---

## 4. Capability

Capability 是 EACG 最重要的对象。

```go
type Handler[I, O any] func(
    context.Context,
    RequestContext,
    I,
) (O, error)
```

`I` 是输入结构体，`O` 是输出结构体。创建 Capability 时，框架会：

1. 根据 `I` 生成 Input Schema；
2. 根据 `O` 生成 Output Schema；
3. 检查名称、版本和风险等级；
4. 把泛型 Handler 包装成统一运行时接口。

调用时，框架会：

1. 把 MCP JSON 参数解析为 `I`；
2. 按 Input Schema 校验；
3. 调用业务 Handler；
4. 按 Output Schema 校验 `O`；
5. 返回结构化结果。

这样可以同时获得 Go 编译期类型检查和运行时 JSON 校验。

### Descriptor 常用字段

| 字段 | 作用 |
| --- | --- |
| `ID` | 框架内部唯一标识，如 `get_user.v1` |
| `Name` | 暴露给 MCP Host 的 Tool 名称 |
| `Version` | 能力版本 |
| `Description` | 帮助 Agent 理解 Tool |
| `RiskLevel` | MVP 只允许 R0、R1 |
| `ReadOnly` | MVP 必须是 `true` |
| `RequiredRoles` | 调用所需角色 |
| `AllowedOutputFields` | 允许出站的顶层字段 |

---

## 5. Registry

Registry 保存所有 Capability，并提供三种主要操作：

- `Register`：注册并检查重复 ID、名称；
- `Freeze`：应用启动后禁止修改；
- `Visible`：只返回当前 Principal 有权查看的能力。

Registry 使用 `sync.RWMutex`，可以安全处理并发读取。

为什么启动后冻结：

- 避免请求处理中 Tool 列表突然变化；
- 降低并发复杂度；
- 让配置错误在启动阶段暴露；
- 为以后统一配置发布保留清晰边界。

---

## 6. 身份和权限

### Principal

`Principal` 是 EACG 内部统一身份：

```text
TenantID
UserID
AgentID
ClientID
AuthMethod
CredentialID
SubjectProvider
Roles
Scopes
Attributes
```

业务代码不直接解析 JWT，也不直接依赖 MCP SDK 的 Token 类型。

### JWT

MVP 提供 HS256 JWT 校验器，检查：

- 签名算法；
- 签名；
- Issuer；
- Audience；
- 过期时间；
- Tenant ID；
- User ID。

JWT、API Key、OIDC 或内部 Token 服务都应实现统一的 `identity.Authenticator` 接口，再通过 `eacg.New` 注入应用。

### Service Token / API Key

固定 API Key 只认证调用应用，不能单独证明当前操作用户。EACG 的 API Key 模式还要求可信请求头提供 requester userid：

```text
API Key → APIKeyStore → Tenant / Client / 最大权限
userid  → SubjectResolver → User / 用户权限
                       ↓
                  最终 Principal
```

`Roles` 和 `Scopes` 分别取应用最大权限与用户实际权限的交集。业务系统需要提供生产版本的 `APIKeyStore` 和 `SubjectResolver`，EACG 不负责生成或派发密钥。

每个 Session 同时绑定应用、用户、Key 版本和权限版本。后续请求更换任何一项，必须重新初始化 MCP Session。

### Tool 可见性

认证成功后，MCP 适配层会为当前 Principal 创建 MCP Server，只注册其角色允许查看的 Tool。

这意味着：

- 无权限 Tool 不出现在 `tools/list`；
- 即使客户端猜出 Tool 名称，Execution Engine 仍会再次检查角色；
- Tool 可见性检查不能替代调用时授权。

---

## 7. Execution Engine

MVP 使用固定执行顺序：

```text
检查 Principal
→ 查找 Capability
→ 检查 RequiredRoles
→ 创建超时 Context
→ 执行 Capability
→ 字段白名单
→ 敏感字段遮盖
→ 写审计
→ 返回结果
```

审计通过 `defer` 写入，所以成功、失败、越权、超时和取消都会留下记录。

### 错误类型

内部错误会被分成稳定类型：

- `not_found`；
- `forbidden`；
- `invalid_input`；
- `invalid_output`；
- `timeout`；
- `canceled`；
- `execution_error`。

返回给 MCP Host 的错误信息会隐藏内部实现细节，详细错误只应进入受控日志。

---

## 8. HTTP Connector

HTTP Connector 只负责一次下游协议调用，不负责业务编排。

它提供：

- 固定 Base URL；
- Host 白名单；
- HTTP/HTTPS 检查；
- 相对路径限制；
- 基础 SSRF 防护；
- Header、Query、JSON Body；
- Context Cancel；
- 超时；
- 非 2xx 错误映射；
- 最大响应大小；
- JSON 响应检查。

复杂 Capability 可以调用多个 Connector，但串并行顺序和业务判断应写在领域 Handler 中。

### 为什么不自动重试

自动重试可能让写操作重复执行。虽然 MVP 只允许只读能力，仍先不提供自动重试，以保持调用语义简单。后续版本会在幂等策略明确后增加。

---

## 9. MCP 协议适配

`protocol/mcphttp` 是唯一直接依赖官方 MCP SDK 的 package。

它负责：

- 创建 Streamable HTTP Handler；
- 调用令牌校验器；
- 绑定 MCP Session 和 User ID；
- 根据 Principal 构建 Tool；
- 把 MCP 参数转换成 Execution Request；
- 把执行结果转换成 MCP Structured Content；
- 把内部错误转换成安全 Tool Error；
- 检查 Origin；
- 生成 Request ID。

领域 Handler 不接触以下类型：

- `mcp.CallToolRequest`；
- `mcp.CallToolResult`；
- MCP Session；
- MCP Transport。

这样升级 MCP SDK 时，修改范围主要集中在协议适配 package。

---

## 10. App 生命周期

`App` 负责组装各模块：

```text
New
→ RegisterCapability
→ Handler
→ Freeze Registry
→ Build Engine
→ Build MCP Handler
→ Ready
→ Run
→ Stop
```

### Health 和 Readiness

- `/health`：进程可以响应；
- `/ready`：应用已经完成能力注册和 Handler 构建。

`Stop` 使用 `http.Server.Shutdown`，会停止接收新请求，并给正在执行的请求留出结束时间。

---

## 11. 审计与最低可运维能力

MVP 没有 Prometheus 和 OpenTelemetry，但仍保留：

- JSON 结构化日志；
- Request ID；
- Trace ID；
- Health；
- Readiness；
- Capability 审计；
- 调用耗时；
- 错误分类。

审计不记录原始参数和完整结果，避免日志成为新的敏感数据泄露点。

完整指标、分布式追踪、Dashboard 和告警将在后续版本实现。

---

## 12. 测试策略

项目使用标准库 `testing`，主要测试层次为：

### 单元测试

- Capability 输入输出；
- Registry 重复和冻结；
- JWT 校验；
- RBAC；
- 输出遮盖；
- 审计；
- HTTP Connector。

### 端到端测试

测试使用官方 MCP Client 连接临时 EACG Server，覆盖：

```text
Bearer Token
→ initialize
→ tools/list
→ tools/call
→ Structured Content
```

### 常用命令

```bash
make test
make test-race
make vet
make cover
```

新增功能时建议遵循：

1. 先写失败测试；
2. 编写最少代码让测试通过；
3. 重构重复代码；
4. 再运行竞态和静态检查。

### 使用 curl 完成一次 MCP 调用

先启动示例：

```bash
make run
```

另开终端，准备 JWT 和请求地址：

```bash
TOKEN=$(make -s token)
MCP_URL=http://127.0.0.1:8080/mcp
HEADER_FILE=/tmp/eacg-mcp-headers
```

初始化并保存响应头：

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

读取 `Mcp-Session-Id`：

```bash
SESSION_ID=$(
  awk 'BEGIN{IGNORECASE=1} /^Mcp-Session-Id:/ {
    gsub("\r", "", $2)
    print $2
  }' "$HEADER_FILE"
)
```

发送初始化完成通知：

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

查询可见 Tool：

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

调用 `get_profile`：

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

关闭 Session：

```bash
curl -sS -i \
  -X DELETE "$MCP_URL" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Mcp-Session-Id: $SESSION_ID" \
  -H 'Mcp-Protocol-Version: 2025-11-25'
```

成功关闭时返回 `HTTP 204 No Content`。已关闭或过期的 Session 可能返回 `404`。

`tools/list` 和 `tools/call` 的返回内容默认使用 SSE，所以外层会出现 `event: message` 和 `data:`。真正的 JSON-RPC 结果位于 `data:` 后面。

---

## 13. 如何新增 Capability

### 第一步：定义输入输出

```go
type GetOrderInput struct {
    OrderID string `json:"order_id"`
}

type GetOrderOutput struct {
    OrderID string `json:"order_id"`
    Status  string `json:"status"`
}
```

### 第二步：创建 Capability

```go
item, err := capability.New(capability.Descriptor{
    ID:            "get_order.v1",
    Name:          "get_order",
    Version:       "v1",
    Description:   "查询订单当前状态",
    RiskLevel:     capability.RiskR1,
    ReadOnly:      true,
    RequiredRoles: []string{"order_reader"},
}, handler)
```

### 第三步：实现 Handler

```go
func handler(
    ctx context.Context,
    request capability.RequestContext,
    input GetOrderInput,
) (GetOrderOutput, error) {
    // 使用 ctx 调用下游，超时和取消会自动传播。
    // 使用 request.Principal 传递租户和用户身份。
    return GetOrderOutput{}, nil
}
```

### 第四步：注册

```go
if err := app.RegisterCapability(item); err != nil {
    return err
}
```

---

## 14. MVP 已知限制

- 只正式支持单实例；
- 多副本必须使用会话粘滞；
- 重启后不恢复 MCP Session；
- 只支持 HTTP 下游；
- 只支持 R0/R1；
- 默认 JWT 实现只提供 HS256；
- API Key 示例只使用内存 Key Store 和演示用户映射；
- 不支持完整 MCP OAuth 发现；
- 不支持配置热加载；
- 不支持 Admin API；
- 不支持 Prometheus 和 OpenTelemetry；
- 字段白名单只处理顶层字段；
- 敏感字段检查只基于固定字段名。

这些限制是有意收敛，不是遗漏。后续版本应按产品路线图逐步解决。

---

## 15. 开发约定

- 方法保持单一职责；
- 接口放在使用方或领域边界；
- 不为了“可能的未来需求”提前抽象；
- 错误使用 `%w` 保留错误链；
- Context 放在方法第一个参数；
- 不把 Token、密码和完整业务数据写入日志；
- 不共享可修改的 map 和 slice；
- 所有新方法增加简短中文注释；
- 所有新功能同时增加测试；
- 提交前运行 `make all` 和 `make test-race`。
