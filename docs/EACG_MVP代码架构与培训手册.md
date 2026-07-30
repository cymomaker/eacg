# EACG v0.2.0 代码架构与培训手册

## 1. 总体结构

```text
HTTP 请求
  → 跨域和 Header 校验
  → Authenticator
  → Principal
  → MCP Server
  → Registry
  → Execution Engine
  → Capability
  → 审计
```

| 包 | 职责 |
| --- | --- |
| `identity` | 把凭据转换成 Principal |
| `protocol/mcphttp` | 处理 MCP 2026-07-28 Streamable HTTP |
| `capability` | 定义有类型的 Tool |
| `registry` | 注册、排序和筛选 Tool |
| `execution` | 二次授权、超时、输出保护和审计 |
| `audit` | 写入安全审计事件 |
| `connector/httpconnector` | 安全访问下游 HTTP 服务 |

## 2. 为什么使用分层

- 协议层不写业务规则；
- 认证层不直接调用业务服务；
- Capability 不读取 HTTP Header；
- 执行层统一处理授权和安全规则；
- Connector 统一限制目标地址、超时和响应大小。

每一层都只依赖接口，方便单元测试和替换实现。

## 3. 注册一个 Tool

```go
item, err := capability.New(
    capability.Descriptor{
        ID:            "get_order_summary.v1",
        Name:          "get_order_summary",
        Version:       "v1",
        Description:   "查询订单汇总",
        RiskLevel:     capability.RiskR1,
        ReadOnly:      true,
        Idempotent:    true,
        RequiredRoles: []string{"order_reader"},
    },
    func(
        ctx context.Context,
        request capability.RequestContext,
        input SummaryInput,
    ) (SummaryOutput, error) {
        tenantID := request.Principal.TenantID
        return service.GetSummary(ctx, tenantID, input)
    },
)
```

输入输出必须使用结构体。EACG 自动生成和校验 JSON Schema。

## 4. 一次 Tool 调用

1. HTTP 中间件校验 Header 和请求大小；
2. Authenticator 生成 Principal；
3. Registry 根据角色生成 Tool 列表；
4. SDK 解析 `tools/call`；
5. Execution Engine 再次检查角色；
6. Capability 校验输入并调用业务代码；
7. 输出执行字段白名单和敏感信息遮盖；
8. 写入审计事件；
9. SDK 返回带 `resultType` 的 JSON 结果。

Tool 列表不是安全边界。即使客户端伪造 Tool 名称，执行层仍会拒绝。

## 5. 错误处理

- 使用 `%w` 保留错误链；
- 业务错误不能直接返回数据库、DSN 或下游正文；
- 输入错误统一映射为安全 Tool Error；
- 请求取消和执行超时必须传递到下游；
- 审计记录稳定错误码，不记录敏感正文。

## 6. 测试建议

- Capability：输入、输出和业务规则；
- Registry：重复注册、冻结和确定性排序；
- Execution：角色、超时、输出过滤和审计；
- identity：凭据、身份类型、过期、停用和服务权限；
- protocol：版本、Header、JSON-RPC 和 HTTP 状态码；
- App：完整发现、列表和调用流程；
- 分布式：相邻请求由不同 Handler 处理。

## 7. 本地验收

```bash
make test
make test-race
make vet
make build
make docker-build
```

所有手写函数和方法使用简短中文注释，注释重点说明“做什么”，复杂安全规则再说明“为什么”。

## 8. example 学习顺序

建议初级工程师按下面顺序阅读：

1. `cmd/eacg-example/main.go`：理解依赖如何组装；
2. `identity/authentication.go`：理解统一认证接口；
3. `identity/apikey.go`：理解 API Key 服务身份；
4. `capability/capability.go`：理解 Tool 输入输出和身份要求；
5. `protocol/mcphttp/handler.go`：理解 HTTP 如何进入执行层；
6. `execution/engine.go`：理解为什么还要二次授权和审计。

example 已使用 `v0.2.0` 无状态协议。普通请求不需要保存服务器返回的协议标识，每一次调用都重新携带认证信息和完整协议元数据。

## 9. Client 学习入口

学习 Server 后，可以阅读 `cmd/eacg-client`：

1. 配置层读取 Endpoint、认证方式和 Tool 参数；
2. Transport 检查新协议并注入认证 Header；
3. SDK 自动发现 Server；
4. Client 查询 Tool；
5. Client 调用 `get_profile`；
6. trace 展示实际 HTTP/JSON-RPC 往返。

Client 与 Server 只通过 HTTP 通信，证明业务项目不需要导入 EACG 内部执行包。
