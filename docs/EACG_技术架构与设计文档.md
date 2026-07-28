# EACG 技术架构与设计文档

> Enterprise Agent Capability Gateway
> 企业 Agent 能力网关

- 文档版本：V1.0
- 文档状态：架构设计稿
- 开发语言：Go
- HTTP 框架：CloudWeGo Hertz
- MCP SDK：`github.com/modelcontextprotocol/go-sdk/mcp`
- 项目形态：独立 Git 仓库、独立 Go Module、可开源复用

---

## 1. 项目定位

EACG 是一套以 MCP Server 为核心的企业 Agent 能力网关。

EACG 以独立 Git 仓库和 Go Module 的形式发布，为企业构建面向具体业务域的 Agent 能力网关提供统一的协议接入、能力模型、执行管线、安全治理、连接器与可观测性能力。

EACG 本身不包含商城、ERP、CRM 等具体企业业务实现。企业内部项目通过 Go Module 依赖 EACG，在独立代码仓库中实现具体业务能力，并最终编译、部署为独立的领域 Agent 网关应用。

```text
github.com/cymomaker/eacg
        │
        │ Go Module 依赖
        ├──────────────────────────┐
        ▼                          ▼
领域 Agent 网关 A              领域 Agent 网关 B
独立 Git 仓库                  独立 Git 仓库
独立业务能力                    独立业务能力
独立配置                        独立配置
独立编译与部署                  独立编译与部署
```

EACG 的核心价值不是简单地将 HTTP API 转换为 MCP Tool，而是：

> 帮助企业安全、可控、可观测地将现有业务能力开放给 Agent。

---

## 2. 背景与问题

企业通常已经拥有多个业务系统或微服务集群，例如订单、商品、库存、供应链、财务、客户、知识库和基础设施系统。

这些系统已经通过 HTTP、RPC、消息队列或内部 SDK 提供能力，但现有接口通常面向 Web、App、后台管理系统或微服务间调用，并不适合直接暴露给 Agent。

直接将现有 API 一对一转换成 MCP Tool，容易产生以下问题：

1. Tool 粒度过细，Agent 被迫理解企业内部微服务结构。
2. LLM 需要完成大量底层接口编排，调用次数多且稳定性差。
3. 权限、数据范围、脱敏、审计和风险控制被分散实现。
4. LLM 可能错误判断退款资格、库存状态、金额和业务状态。
5. 不同业务团队重复开发 MCP Server 的通用功能。
6. MCP SDK 和协议类型直接侵入业务代码。
7. 不同领域网关难以保持一致的安全和治理标准。
8. MCP 协议升级可能直接影响企业业务代码。

EACG 通过公共框架能力解决以上问题。

---

## 3. 项目目标

### 3.1 核心目标

1. **MCP 标准接入**：基于官方 Go SDK 实现 MCP Server，兼容主流 MCP Host。
2. **公共能力复用**：不同企业业务项目通过依赖同一 Go Module 复用网关能力。
3. **领域业务隔离**：EACG 不包含具体企业业务代码，业务能力由依赖项目实现。
4. **协议与业务解耦**：企业业务代码不直接依赖官方 MCP SDK 类型。
5. **能力优先设计**：面向 Agent 的业务任务设计能力，而不是机械映射已有 API。
6. **确定性边界**：程序负责事实、规则、权限和执行边界，LLM 负责理解、规划和表达。
7. **安全治理**：提供认证、授权、数据权限、脱敏、风险和人工确认机制。
8. **插件化扩展**：认证、限流、审计和脱敏等公共功能通过插件扩展。
9. **连接器抽象**：统一封装 HTTP、gRPC、Kitex、MCP 等下游调用方式。
10. **低资源部署**：使用 Go 和 Hertz，支持单二进制和容器化部署。
11. **可演进性**：第一阶段作为 Go Module 使用，后续可演进统一控制面和插件生态。

### 3.2 非目标

EACG 不负责：

- 实现完整 Agent Runtime；
- 训练、托管或代理大语言模型；
- 替代企业现有 API Gateway；
- 替代企业业务微服务；
- 保存企业核心业务主数据；
- 实现领域核心业务规则；
- 直接操作订单、库存、支付等核心业务数据；
- 向 Agent 开放任意 SQL；
- 强制企业使用固定的权限中心、数据库或配置中心。

---

## 4. 使用方式

EACG 是一个可被依赖的 Go Module，而不是企业需要远程调用的公共服务。

企业领域项目通过 `go.mod` 引入：

```go
require github.com/cymomaker/eacg v0.1.0
```

领域项目负责：

- 创建 EACG 应用；
- 注册具体 Capability；
- 注册企业自定义 Plugin；
- 配置 Provider 和 Connector；
- 接入企业身份与权限系统；
- 实现具体业务接口编排；
- 编译并部署最终应用。

示例入口：

```go
func main() {
    ctx := context.Background()

    authenticator, err := identity.NewJWTAuthenticator(identity.JWTConfig{
        Secret:   []byte(os.Getenv("EACG_JWT_SECRET")),
        Issuer:   "domain-eacg",
        Audience: "eacg",
    })
    if err != nil {
        log.Fatal(err)
    }
    app, err := eacg.New(
        eacg.Config{Name: "domain-eacg", Version: "v0.1.0"},
        eacg.HTTPAuthenticationConfig{Authenticator: authenticator},
        nil,
    )
    if err != nil {
        log.Fatal(err)
    }
    if err := app.RegisterCapability(domain.NewCapabilities(dependencies)...); err != nil {
        log.Fatal(err)
    }
    if err := app.Run(ctx); err != nil {
        log.Fatal(err)
    }
}
```

最终部署的是依赖 EACG 的领域应用二进制，而不是单独部署一个 EACG 公共服务。

```text
领域项目源码
   +
EACG Go Module
   ↓
Go Build
   ↓
领域 Agent 网关二进制
```

---

## 5. 总体架构

```text
┌──────────────────────────────────────────────────────────────┐
│                      Agent / MCP Host                        │
│  Codex / Claude Code / 企业 Agent / Workflow / 其他 Host    │
└──────────────────────────────┬───────────────────────────────┘
                               │
                     MCP Streamable HTTP
                               │
                               ▼
┌──────────────────────────────────────────────────────────────┐
│                       Hertz HTTP 层                          │
│ TLS / Recovery / RequestID / AccessLog / Health / Metrics   │
└──────────────────────────────┬───────────────────────────────┘
                               │
                               ▼
┌──────────────────────────────────────────────────────────────┐
│                     MCP 协议适配层                           │
│ github.com/modelcontextprotocol/go-sdk/mcp                  │
└──────────────────────────────┬───────────────────────────────┘
                               │
                               ▼
┌──────────────────────────────────────────────────────────────┐
│                         EACG 核心                            │
│ Capability Registry / Binding / Plugin Pipeline / Policy     │
│ Risk / Confirmation / Execution / Result Processing         │
└──────────────────────────────┬───────────────────────────────┘
                               │
                               ▼
┌──────────────────────────────────────────────────────────────┐
│                       Capability Executor                   │
│ Local / HTTP / gRPC / Kitex / MCP / Query                  │
└──────────────────────────────┬───────────────────────────────┘
                               │
                               ▼
┌──────────────────────────────────────────────────────────────┐
│                      企业现有业务服务                        │
└──────────────────────────────────────────────────────────────┘
```

---

## 6. 核心设计原则

### 6.1 Capability 是 EACG 的核心领域对象

MCP SDK 的核心对象是 Tool，但 EACG 的核心领域对象应是 Capability。

```text
Enterprise Capability
       ↓
MCP Adapter
       ↓
MCP Tool
```

这样设计可以隔离 MCP SDK、保持业务模型稳定、支持版本管理，也为未来扩展其他协议保留空间。

### 6.2 EACG 管理 Agent 能力，不只是 HTTP 流量

传统 API Gateway 管理 Path、Method、Upstream 和流量；EACG 管理 Capability、Tool、Principal、Policy、Risk、Confirmation、Context Data、Provider 和 Connector。

> API Gateway 主要治理接口流量，EACG 主要治理 Agent 能力及其调用边界。

### 6.3 EACG 可以处理能力编排，不处理核心业务规则

EACG 或依赖项目中的 Capability 可以处理：

- 调用一个或多个业务服务；
- 并行和串行编排；
- 聚合数据；
- 字段裁剪；
- 状态码转换；
- 结果结构化；
- 风险识别；
- Agent 友好的下一步动作提示。

原业务系统继续处理：

- 是否可以退款；
- 应退款多少；
- 库存如何扣减；
- 状态如何流转；
- 支付事务；
- 业务幂等；
- 数据一致性。

### 6.4 默认通过业务 API 获取能力

推荐优先级：

1. 企业业务 HTTP/RPC API；
2. 企业 Application Service 或内部 SDK；
3. 只读数仓、查询库或搜索索引；
4. 谨慎读取生产只读库；
5. 禁止直接写核心业务数据库；
6. 禁止开放任意 SQL。

---

## 7. EACG 公开 API 设计

### 7.1 应公开的稳定对象

- `App`
- `Option`
- `Capability`
- `Descriptor`
- `ExecutionRequest`
- `ExecutionResult`
- `CapabilityGroup`
- `Provider`
- `Connector`
- `Plugin`
- `Policy`
- `Principal`
- `Registry`
- `AuditSink`
- `AuthProvider`

### 7.2 不应暴露的内部对象

- MCP SDK Request/Result；
- MCP Session 内部结构；
- Hertz RequestContext；
- 内部 Registry 实现；
- 内部 Pipeline 节点；
- 协议适配细节；
- 配置缓存模型；
- 数据库表结构。

官方 MCP SDK 类型只能在 EACG 的协议适配包中使用。

---

## 8. 核心领域模型

### 8.1 Capability

```go
type Capability interface {
    Descriptor() Descriptor
    Execute(ctx context.Context, request ExecutionRequest) (ExecutionResult, error)
}
```

```go
type Descriptor struct {
    ID          string
    Name        string
    Version     string
    Description string

    InputSchema  json.RawMessage
    OutputSchema json.RawMessage

    Group string
    Tags  []string

    RiskLevel   RiskLevel
    ReadOnly    bool
    Idempotent  bool
    Destructive bool

    RequiredPermissions []string
    PolicyRefs          []string
}
```

Capability 应业务语义明确、输入输出稳定、可授权、可审计、可测试、可版本化，并与下游接口解耦。

### 8.2 CapabilityGroup

```go
type CapabilityGroup struct {
    ID           string
    Name         string
    Description  string
    ProviderRefs []string
    PolicyRefs   []string
    Tags         []string
}
```

用于复用认证、授权、数据权限、限流、脱敏、审计、超时和 Provider 配置。

### 8.3 Provider

```go
type Provider struct {
    ID       string
    Type     ProviderType
    Endpoint string
    AuthRef  string
    Config   map[string]any
}
```

Provider 类型：`Local`、`HTTP`、`gRPC`、`Kitex`、`MCP`、`Query`。

### 8.4 CapabilityBinding

```go
type CapabilityBinding struct {
    CapabilityRef string
    ExecutorRef   string
    ProviderRefs  []string
    PolicyRefs    []string
}
```

### 8.5 Principal

```go
type Principal struct {
    TenantID string
    UserID   string
    AgentID  string
    ClientID string

    Roles      []string
    Scopes     []string
    DataScopes []string

    Attributes map[string]string
}
```

### 8.6 Exchange

```go
type Exchange struct {
    RequestID  string
    TraceID    string
    StartedAt  time.Time
    Principal  Principal
    Capability Descriptor
    Arguments  json.RawMessage
    Result     *ExecutionResult
    Metadata   map[string]any
}
```

Plugin 不直接依赖 Hertz 或 MCP SDK。

---

## 9. 应用生命周期

```go
type App interface {
    RegisterCapability(...Capability) error
    RegisterPlugin(...Plugin) error
    RegisterProvider(...Provider) error
    RegisterConnector(...Connector) error

    Start(context.Context) error
    Stop(context.Context) error
    Run(context.Context) error
}
```

生命周期：

```text
New → Load Config → Initialize → Register Plugins
→ Register Providers → Register Capabilities
→ Build Pipeline → Build MCP Server → Start Hertz
→ Ready → Graceful Shutdown
```

---

## 10. MCP 协议适配

依赖：

```go
import "github.com/modelcontextprotocol/go-sdk/mcp"
```

适配层负责：

- 创建 `mcp.Server`；
- 将 Capability 转换为 MCP Tool；
- 处理 `initialize`、`tools/list`、`tools/call`；
- 根据 Principal 动态过滤 Tool；
- 构造 `ExecutionRequest`；
- 调用 EACG 执行管线；
- 转换 `ExecutionResult`；
- 管理 Session；
- 处理错误、取消和通知；
- 隔离 SDK 版本变化。

概念代码：

```go
server := mcp.NewServer(
    &mcp.Implementation{Name: appName, Version: appVersion},
    &mcp.ServerOptions{},
)
```

```go
func RegisterCapability[I, O any](
    server *mcp.Server,
    capability Capability,
    executor Executor,
) {
    descriptor := capability.Descriptor()

    tool := &mcp.Tool{
        Name:        descriptor.Name,
        Description: descriptor.Description,
    }

    mcp.AddTool(server, tool,
        func(ctx context.Context, req *mcp.CallToolRequest, input I) (*mcp.CallToolResult, O, error) {
            result, err := executor.Execute(ctx, NewExecutionRequest(req, descriptor, input))
            if err != nil {
                var zero O
                return nil, zero, err
            }
            return ToMCPResult[O](result)
        },
    )
}
```

实际代码以锁定版本的 SDK API 为准。

---

## 11. Hertz HTTP 层

Hertz 负责 HTTP Server、路由、TLS、Recovery、Request ID、Access Log、CORS、Health、Readiness、Metrics、Admin API 和 OAuth Callback。

推荐路由：

```text
POST /mcp
GET  /health
GET  /ready
GET  /metrics
GET  /admin/capabilities
GET  /admin/providers
GET  /admin/plugins
GET  /admin/audits
POST /admin/reload
```

官方 MCP Go SDK 的 Streamable HTTP Handler 通常基于 `net/http.Handler`。EACG 需要封装 Hertz 与标准库 Handler 的适配层，并确保 Header、Context、Flush、取消信号、Body 和 SSE Header 正确传递。

---

## 12. Plugin Pipeline

EACG 借鉴 APISIX 的插件链思想，但插件作用于 Capability 调用，而不是 HTTP Route。

```go
type Plugin interface {
    Name() string
    Phase() Phase
    Priority() int
    Execute(ctx context.Context, exchange *Exchange, next Next) error
}
```

推荐阶段：

```text
Discover
Authenticate
Authorize
Validate
Risk
Confirm
BeforeExecute
AfterExecute
Mask
Audit
Error
```

首批内置插件：

- Request Context；
- Authentication；
- Tenant Resolution；
- Tool Visibility；
- RBAC；
- ABAC；
- Data Scope；
- Input Validation；
- Risk Classification；
- Human Confirmation；
- Rate Limit；
- Timeout；
- Retry；
- Circuit Breaker；
- Idempotency；
- Output Validation；
- Field Filtering；
- PII Masking；
- Audit；
- Metrics；
- Tracing；
- Error Mapping。

配置继承：

```text
Global → CapabilityGroup → Capability → Runtime Override
```

---

## 13. Capability 执行管线

```text
MCP tools/call
      ↓
MCP Adapter
      ↓
Capability Registry
      ↓
Tool Visibility
      ↓
Authentication
      ↓
Tenant Resolution
      ↓
Authorization
      ↓
Input Validation
      ↓
Risk Evaluation
      ↓
Confirmation Check
      ↓
Rate Limit
      ↓
Timeout / Retry / Idempotency
      ↓
Capability Execute
      ↓
Output Validation
      ↓
Field Filtering
      ↓
Sensitive Data Masking
      ↓
Audit
      ↓
Metrics / Trace
      ↓
MCP Result
```

---

## 14. Connector 架构

```go
type Connector interface {
    Type() string
    Invoke(ctx context.Context, request ConnectorRequest) (ConnectorResponse, error)
}
```

### 14.1 HTTP Connector

支持 Path、Query、Header、OAuth、JWT、API Key、TLS/mTLS、超时、重试、熔断、服务发现、Trace 透传和错误映射。

### 14.2 gRPC Connector

支持 Protobuf、Metadata、Deadline、服务发现、负载均衡、Trace 和 Status 映射。

### 14.3 Kitex Connector

支持 Kitex Client、服务发现、熔断、超时、重试、Tracing、Metadata 和统一错误转换。

### 14.4 MCP Connector

EACG 可以作为 MCP Client 调用下游 MCP Server，用于 Tool 聚合、重命名、可见性控制、统一认证、限流、审计和故障隔离。

### 14.5 Query Connector

仅面向数仓、只读副本、搜索索引和固定查询模板，禁止提供通用 `execute_sql` Capability。

---

## 15. 安全架构

### 15.1 默认拒绝

所有 Capability 默认不可见、不可调用、未授权、不返回敏感数据，只有显式配置后才开放。

### 15.2 授权层级

1. 实例级；
2. Capability 级；
3. 参数级；
4. 数据级。

### 15.3 双重权限校验

EACG 负责前置权限校验，下游业务系统必须继续执行最终权限判断。EACG 不应使用超级账号绕过业务权限。

### 15.4 Tool 可见性

`tools/list` 根据 Tenant、User、Agent、Client、Role、Scope、Environment、Capability 状态、风险等级、灰度策略和功能开关动态裁剪。

---

## 16. 风险与人工确认

| 等级 | 类型    | 示例         |
| -- | ----- | ---------- |
| R0 | 公共只读  | 查询公开信息     |
| R1 | 敏感只读  | 查询订单、客户    |
| R2 | 可逆写入  | 添加备注、创建草稿  |
| R3 | 高风险操作 | 退款、取消、库存调整 |

高风险 Capability 建议采用 Prepare / Execute：

```text
prepare_xxx → 返回影响与确认令牌 → 人工确认/审批 → execute_xxx
```

确认令牌绑定 Tenant、User、Agent、Capability、参数摘要、风险结果、过期时间和一次性状态。

---

## 17. 请求与结果模型

```go
type ExecutionRequest struct {
    RequestID  string
    TraceID    string
    TenantID   string
    Principal  Principal
    Capability CapabilityRef
    Arguments  json.RawMessage
    Metadata   map[string]any
}
```

```go
type ExecutionResult struct {
    Success bool

    Facts          any
    Decisions      any
    AllowedActions []AllowedAction
    Warnings       []Warning

    RequiresConfirmation bool
    Confirmation         *ConfirmationInfo

    TraceID  string
    Metadata map[string]any
}
```

`Facts` 表示客观事实，`Decisions` 表示确定性程序结论，`AllowedActions` 表示 Agent 可选择的下一步。

---

## 18. 数据安全与出站治理

结果进入 LLM Context 前必须经过：

1. Output Schema 校验；
2. 字段白名单；
3. 数据权限裁剪；
4. PII 识别；
5. 敏感字段脱敏；
6. Secret 和 Token 扫描；
7. 返回长度限制；
8. 文件与 URL 来源验证；
9. Prompt Injection 可疑内容标记；
10. 审计记录。

EACG 将返回给 Agent 的数据视为一次企业数据出站行为。

---

## 19. 配置模型

```yaml
eacg:
  name: domain-eacg
  version: 1.0.0

server:
  framework: hertz
  address: 0.0.0.0:8080

transport:
  type: streamable-http
  endpoint: /mcp

security:
  default_deny: true
  auth_provider: oidc
  issuer: https://sso.example.com
  audience: domain-eacg

providers:
  - id: business-service
    type: http
    endpoint: http://business-service
    timeout: 3s

capability_groups:
  - id: business-group
    policies:
      - employee-auth
      - data-scope
      - audit
      - pii-mask

capabilities:
  - name: example_capability
    version: v1
    group: business-group
    executor:
      type: local
      handler: example-handler
    risk:
      level: R1
```

简单 API 映射可以配置化，复杂能力编排使用 Go 代码实现。

---

## 20. Git 仓库与 Go Module 设计

EACG 使用一个独立 Git 仓库：

```text
github.com/cymomaker/eacg
```

第一阶段建议一个 Git 仓库、一个 Go Module。

推荐目录：

```text
eacg/
├── app/
├── capability/
├── registry/
├── binding/
├── pipeline/
├── plugin/
├── policy/
├── identity/
├── auth/
├── risk/
├── confirmation/
├── connector/
│   ├── http/
│   ├── grpc/
│   ├── kitex/
│   ├── mcp/
│   └── query/
├── protocol/
│   └── mcp/
├── transport/
│   ├── hertz/
│   └── stdio/
├── observability/
├── audit/
├── config/
├── admin/
├── internal/
├── examples/
│   ├── basic/
│   ├── http-capability/
│   ├── multi-service/
│   ├── auth/
│   ├── confirmation/
│   └── domain-gateway/
├── docs/
├── go.mod
├── CHANGELOG.md
├── MIGRATION.md
├── README.md
└── LICENSE
```

框架内部实现放在 `internal`，依赖项目只能使用公开 package。

---

## 21. Examples 与扩展文档

具体领域实现不进入主架构设计。

`examples` 用于展示：

- 最小可运行 MCP Server；
- Capability 注册；
- HTTP Connector；
- 多服务编排；
- JWT/OIDC；
- Tool 级权限；
- 风险确认；
- 下游 MCP 聚合；
- 独立领域网关项目结构。

复杂案例放入：

```text
docs/examples/domain-gateway.md
docs/guides/build-enterprise-gateway.md
docs/guides/capability-design.md
```

企业项目应通过 Go Module 依赖 EACG，而不是长期 Fork examples。

---

## 22. 版本管理

EACG 使用语义化版本：

```text
v0.1.0
v0.2.0
v1.0.0
v1.1.0
```

依赖项目必须锁定版本：

```go
require github.com/cymomaker/eacg v0.3.2
```

需要维护 `CHANGELOG.md`、`MIGRATION.md`、`UPGRADING.md` 和 MCP SDK 兼容矩阵。

---

## 23. 依赖替换与扩展点

EACG 应提供默认实现，但不能强绑定企业基础设施。

```go
eacg.WithTransport(...)
eacg.WithAuthProvider(...)
eacg.WithPolicyEngine(...)
eacg.WithAuditSink(...)
eacg.WithRegistry(...)
eacg.WithConnector(...)
eacg.WithConfigProvider(...)
eacg.WithTracerProvider(...)
eacg.WithMetricsProvider(...)
```

企业可以替换身份认证、策略引擎、审计存储、配置中心、Registry、Trace、Metrics、Connector 和审批系统。

---

## 24. 可观测性

Metrics 包括 MCP 请求量、Tool 调用量、成功率、P95/P99 延迟、下游成功率、超时、重试、熔断、权限拒绝、确认次数、Session 数量和返回数据量。

Trace 建议：

```text
mcp.request
  ├── auth.authenticate
  ├── capability.resolve
  ├── policy.authorize
  ├── risk.evaluate
  ├── capability.execute
  │     ├── connector.service_a
  │     └── connector.service_b
  ├── result.mask
  └── audit.write
```

Audit 至少记录 Tenant、User、Agent、Client、Capability、参数摘要、权限结果、风险结果、确认信息、下游调用、返回数据分类、Trace ID 和总耗时。

---

## 25. 稳定性设计

- 每个 Capability 声明总超时、连接超时、读取超时和重试次数；
- 只重试网络瞬时错误、明确可重试状态和幂等操作；
- 支持 Provider 与 Capability 两个维度的熔断；
- 组合能力允许部分成功，但必须返回 Warning；
- 写操作支持 Request ID、Idempotency Key、重复检测和状态查询。

---

## 26. 测试策略

### 单元测试

Capability、Registry、Binding、Plugin、Policy、Risk、Masking、Connector。

### MCP 契约测试

`initialize`、`tools/list`、`tools/call`、Streamable HTTP、STDIO、Session、Cancel、Error 和 SDK 升级兼容。

### Hertz 适配测试

Header 透传、Stream Flush、Context Cancel、SSE、大响应、Proxy、Timeout 和 Connection Close。

### 安全测试

越权、Tool 隐藏、参数注入、SSRF、Token 泄露、重放、确认绕过、敏感数据出站、任意 SQL 和 Prompt Injection 诱导调用。

---

## 27. MVP 范围

第一阶段实现：

1. `App` 与生命周期；
2. Hertz HTTP Server；
3. 官方 MCP Go SDK 适配；
4. Streamable HTTP；
5. STDIO 开发模式；
6. Capability、Group、Provider、Binding、Registry；
7. Plugin Pipeline；
8. JWT/OIDC；
9. Tool 级 RBAC；
10. Input/Output Schema 校验；
11. R0-R3 风险等级；
12. 基础人工确认；
13. HTTP Connector；
14. gRPC 或 Kitex Connector；
15. MCP Connector；
16. Audit；
17. Prometheus；
18. OpenTelemetry；
19. Health、Ready 和 Admin API；
20. Examples 与文档。

第一阶段暂不实现统一控制面、可视化工作流、插件市场、Go 动态 Plugin、任意 SQL、完整审批引擎、Agent Runtime 和多集群配置发布。

---

## 28. 后续演进

### CLI 与脚手架

```bash
eacg new my-agent-gateway
```

生成独立领域项目，并在 `go.mod` 中依赖 EACG。

### eacg-contrib

成熟后可拆出：

```text
github.com/cymomaker/eacg-contrib
```

用于社区 Connector、第三方 Plugin 和实验性能力。

### 控制面

当企业部署多套网关后，可增加实例管理、Capability 目录、策略管理、版本发布、配置下发和审计查询的控制面。

---

## 29. 关键架构决策

1. 项目、仓库、Go Module 和产品统一命名为 **EACG**。
2. 不使用 EACG Core、EACG Framework、EACG Runtime 作为产品名。
3. EACG 通过编译依赖方式被企业项目使用，不作为远程公共服务被调用。
4. 第一阶段一个 Git 仓库、一个 Go Module。
5. Hertz 负责 HTTP 层，MCP SDK 负责协议，EACG 负责能力治理。
6. 使用 `github.com/modelcontextprotocol/go-sdk/mcp`。
7. 业务代码不直接依赖 MCP SDK。
8. Capability 是核心对象，Tool 是协议对象。
9. 核心业务规则保留在原业务系统。
10. 默认不直连核心生产业务数据库。
11. 具体领域案例放在 examples 或独立文档。

---

## 30. 最终结论

EACG 是一个独立、可开源、可通过 Go Module 复用的企业 Agent 能力网关项目。

技术主链路：

```text
Hertz
   ↓
MCP Protocol Adapter
   ↓
Capability Registry
   ↓
Plugin Pipeline
   ↓
Capability Executor
   ↓
HTTP / gRPC / Kitex / MCP Connector
   ↓
Enterprise Business Services
```

EACG 负责 MCP Server、Capability 模型、能力注册与发现、插件执行管线、认证授权、风险与确认、数据出站治理、Connector、审计、可观测性及应用生命周期。

依赖 EACG 的企业项目负责实现具体 Capability、接入企业业务服务、接入企业特有权限、配置和组装 EACG，并编译部署最终应用。

> EACG 的长期产品价值，是为企业提供一套统一、可复用、安全、可控、可观测的 Agent 能力开放标准和开发底座。
