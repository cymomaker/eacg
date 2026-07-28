# EACG 产品实现路线图

> Enterprise Agent Capability Gateway
> 企业 Agent 能力网关

- 文档版本：V1.0
- 文档状态：实施规划稿
- 规划基线：`EACG_技术架构与设计文档.md` V1.0
- 技术方向：Go、官方 MCP Go SDK、HTTP
- 规划原则：先验证协议主链路和核心抽象，再补齐企业级治理与扩展能力

---

## 1. 路线图结论

EACG 采用“小内核、分阶段增强”的实施路线。

MVP 只实现以下主链路：

```text
MCP Host
   ↓ Streamable HTTP
EACG HTTP Transport
   ↓
Authentication / Authorization / Validation
   ↓
Capability Registry and Executor
   ↓
HTTP Connector
   ↓
Enterprise HTTP Service
```

MVP 不实现 gRPC、Kitex、下游 MCP、Query Connector，也不实现完整可观测性平台、分布式状态、高风险人工确认和统一控制面。

完整可观测性放到 MVP 之后是合理的，但 MVP 仍必须具备最小可运维能力：

- 结构化运行日志；
- Request ID 和 Trace ID 生成、透传；
- 健康检查和就绪检查；
- Capability 调用审计；
- 错误分类和基础耗时记录。

Prometheus、OpenTelemetry、分布式 Trace、Dashboard、告警规则和 SLO 在 MVP 后实现。

---

## 2. 产品实施原则

### 2.1 先完成协议闭环

优先确保以下流程真实可运行并通过兼容性测试：

```text
initialize
→ tools/list
→ tools/call
→ HTTP downstream call
→ result validation
→ MCP result
```

在主链路稳定前，不扩展 RPC、复杂策略引擎、控制面和插件生态。

### 2.2 MVP 只支持 HTTP

MVP 的“HTTP-only”包括：

- 上游只支持 MCP Streamable HTTP；
- 下游只支持 HTTP/HTTPS 业务服务；
- 管理与健康检查使用 HTTP；
- 不支持 gRPC、Kitex、下游 MCP、Query Connector；
- 不支持 STDIO 传输。

### 2.3 MVP 优先支持低风险能力

MVP 正式支持：

- R0：公共只读；
- R1：敏感只读。

MVP 可以保留 R2、R3 枚举和扩展接口，但默认拒绝注册或执行 R2、R3 Capability。可逆写入、高风险写入、人工确认和审批在后续版本实现。

### 2.4 默认安全，但不追求首版安全能力全集

MVP 必须实现：

- 默认拒绝；
- Bearer Token/JWT 验证；
- Principal 构造；
- Capability 级 RBAC；
- Tenant 隔离；
- 输入输出 Schema 校验；
- 输出字段白名单；
- Secret/Token 固定规则过滤；
- 基础审计。

MVP 后实现：

- 完整 MCP OAuth 2.1 授权发现；
- ABAC 和外部策略引擎；
- 完整数据权限表达式；
- PII 自动识别；
- 高风险确认和审批；
- 审计防篡改与集中归档。

### 2.5 避免过早稳定公开 API

在 `v1.0.0` 前保持 `v0.x` 版本，允许根据试点反馈调整公开接口。首期只公开领域项目真正需要使用的最小 API。

---

## 3. 版本路线图总览

| 阶段 | 建议版本 | 核心目标 | 建议周期 |
| --- | --- | --- | --- |
| 架构验证 | Pre-MVP | 消除传输、类型模型和执行模型风险 | 2 周 |
| 最小可用版本 | v0.1.0 | 打通 HTTP-only MCP 能力调用闭环 | 8～10 周 |
| 企业试点增强 | v0.2.0 | 增加写操作、确认、OAuth 和多实例能力 | 6～8 周 |
| 可观测性与运维 | v0.3.0 | 建立 Metrics、Tracing、告警和运行治理 | 4～6 周 |
| 连接器与生态扩展 | v0.4.0 | 增加 RPC、MCP、Query 等扩展能力 | 按需求迭代 |
| 稳定版本 | v1.0.0 | 稳定公开 API 和生产支持基线 | 试点验证后 |

周期以 3 名熟悉 Go 的工程师为参考，不包含企业身份平台、业务接口改造和安全审批等待时间。

---

## 4. Pre-MVP：架构验证

### 4.1 目标

通过可执行 PoC 和架构决策记录，提前解决对后续实现影响最大的技术不确定性。

### 4.2 必须完成的架构决策

形成以下 ADR：

1. MCP 规范版本和官方 Go SDK 锁定策略；
2. MCP Streamable HTTP 的 Server 实现方式；
3. Hertz 与官方 MCP SDK 的集成方式；
4. Typed Capability 与运行时类型擦除方式；
5. Capability、Handler、Binding、Provider、Connector 的职责边界；
6. MVP 的会话状态、单实例和负载均衡约束；
7. Authentication、Principal、Authorization 的分层；
8. 错误模型和 MCP Error/Tool Error 的映射规则。

### 4.3 Transport PoC

PoC 必须验证：

- 同一 `/mcp` 端点支持 `POST` 和 `GET`；
- `initialize`、`tools/list`、`tools/call` 正常；
- JSON 响应和 SSE 响应正常；
- Header、Context、取消信号正确透传；
- 客户端断开后，正在执行的 Capability 能收到取消信号；
- 长连接能够定期发送事件或保持存活；
- 服务优雅停机不会接受新请求；
- 反向代理关闭缓冲后，SSE 能及时到达客户端。

如果 Hertz 官方 `net/http.Handler` 适配器不能完整支持 Flush、取消和长连接，MVP 应允许 `/mcp` 使用标准库 `net/http` Server，Hertz 仅用于非 MCP HTTP 接口。Transport 必须保持可替换，不应让 Hertz 成为核心领域层依赖。

### 4.4 Typed Capability PoC

建议采用“注册时泛型、运行时类型擦除”的模型：

```go
type Handler[I, O any] func(
    context.Context,
    ExecutionContext,
    I,
) (O, error)

func NewCapability[I, O any](
    descriptor Descriptor,
    handler Handler[I, O],
) Capability
```

`NewCapability` 在注册时完成：

- 输入输出类型绑定；
- JSON Schema 生成或校验；
- Handler 包装；
- 运行时统一执行接口转换。

领域项目不直接依赖 MCP SDK 类型。

### 4.5 退出标准

满足以下条件后才进入 MVP 开发：

- 至少一个真实 MCP Host 可完成端到端调用；
- Transport PoC 连续运行和取消测试通过；
- Capability 类型模型完成代码验证；
- 核心 ADR 完成评审；
- 没有必须通过大规模重构才能解决的已知阻断项。

---

## 5. v0.1.0：HTTP-only MVP

### 5.1 MVP 目标

为一个企业领域项目提供可依赖的 Go Module，使其能够：

1. 注册 R0/R1 Capability；
2. 通过 MCP Streamable HTTP 向 Agent 暴露能力；
3. 验证用户身份与 Capability 权限；
4. 调用企业 HTTP 服务；
5. 校验、裁剪并返回结构化结果；
6. 记录最小审计信息；
7. 以单二进制或容器方式部署。

### 5.2 MVP 功能范围

#### A. 应用与生命周期

- `New`、`Start`、`Stop`、`Run`；
- 配置加载与启动校验；
- Capability 注册冻结；
- 优雅停机；
- `/health`；
- `/ready`。

MVP 不支持运行时热加载和动态注册。

#### B. MCP Streamable HTTP

- `initialize`；
- `tools/list`；
- `tools/call`；
- `POST /mcp`；
- `GET /mcp`；
- 会话 ID 管理；
- Context Cancel；
- 基础 SSE；
- 协议错误映射；
- Tool 执行错误映射。

MVP 不承诺旧版 HTTP+SSE Transport 兼容，也不支持 STDIO。

#### C. Capability Core

- `Descriptor`；
- Typed Handler；
- `ExecutionRequest`；
- `ExecutionResult`；
- Registry；
- Capability 唯一性校验；
- Tool Name 规范化和冲突检测；
- Capability 版本字段；
- 输入输出 JSON Schema；
- R0/R1 风险声明；
- ReadOnly 和 Idempotent 元数据。

MVP 的 `CapabilityBinding` 作为内部对象，不作为稳定公开 API。

#### D. 最小执行管线

MVP 固定管线：

```text
Request Context
→ Authenticate
→ Resolve Tenant and Principal
→ Tool Visibility
→ Authorize
→ Validate Input
→ Execute Capability
→ Validate Output
→ Filter Output Fields
→ Audit
→ Map Result
```

插件机制只需要支持固定阶段、优先级和 `before/after` 包装。必须保证 Audit 和错误映射在拒绝、超时、取消和 panic 场景下执行。

MVP 不实现通用工作流编排引擎。

#### E. 身份与权限

- 静态开发身份；
- Bearer Token/JWT 验证；
- Issuer、Audience、有效期和签名校验；
- Tenant、User、Agent、Client 映射；
- Capability 级 RBAC；
- `tools/list` 按 Principal 裁剪；
- 下游调用不使用绕过业务权限的超级账号。

完整 MCP OAuth 授权发现和企业身份联邦放到 `v0.2.0`。

#### F. HTTP Connector

MVP 支持：

- HTTP/HTTPS；
- Method、Path、Query、Header、JSON Body；
- Provider Base URL；
- 静态 API Key；
- Bearer Token 透传或 Token Provider；
- TLS；
- 连接超时和总超时；
- Request ID 透传；
- 响应状态映射；
- 响应大小限制；
- Host 白名单和基础 SSRF 防护。

MVP 不支持：

- 服务发现；
- 自动重试；
- 熔断；
- 复杂负载均衡；
- HTTP 工作流 DSL；
- 任意目标 URL。

复杂能力可以在领域 Handler 中通过多个 HTTP Connector 调用完成，但框架不提供可视化或配置化编排。

#### G. 数据出站安全

- Output Schema 校验；
- 明确的字段白名单；
- 最大结果大小；
- 固定敏感字段名过滤；
- Secret、Token 常见格式检测；
- URL 和文件返回默认关闭；
- 对来自下游的文本增加“非可信内容”元数据。

MVP 不实现基于模型的 PII 识别和 Prompt Injection 自动判断。

#### H. 最小审计与运维能力

- JSON 结构化日志；
- Request ID、Trace ID；
- 启动、停止、配置错误日志；
- Capability 调用结果和耗时；
- 认证、授权和 Schema 拒绝记录；
- 可替换的 `AuditSink`；
- 默认本地标准输出 Audit Sink；
- 审计字段脱敏；
- `/health` 和 `/ready`。

这里的 Trace ID 只用于关联日志，不代表 MVP 实现分布式追踪。

### 5.3 MVP 明确不做

- gRPC Connector；
- Kitex Connector；
- MCP Connector；
- Query Connector；
- STDIO Transport；
- Prometheus；
- OpenTelemetry；
- 分布式 Trace；
- Dashboard 和告警；
- R2/R3 写操作；
- Prepare/Execute 人工确认；
- 外部审批系统；
- ABAC 和外部 Policy Engine；
- PII 智能识别；
- Retry、Circuit Breaker；
- 多实例共享 Session；
- 配置热加载；
- Admin API；
- 控制面；
- CLI 和插件市场。

### 5.4 MVP 推荐包结构

首版避免创建大量只有接口的空 package：

```text
eacg/
├── app.go
├── option.go
├── capability/
├── registry/
├── execution/
├── pipeline/
├── identity/
├── auth/
├── audit/
├── connector/
│   └── http/
├── protocol/
│   └── mcp/
├── transport/
│   └── http/
├── config/
├── internal/
├── examples/
│   ├── basic/
│   ├── jwt-auth/
│   └── http-capability/
├── docs/
└── go.mod
```

只有在出现第二个实现或明确替换需求后，再拆分更细的 Provider、Binding、Policy、Confirmation 和 Observability package。

### 5.5 MVP 实施顺序

#### 第 1～2 周：工程骨架和核心模型

- 建立 Go Module、CI、代码规范；
- 实现 App 生命周期；
- 实现 Descriptor、Typed Capability、Registry；
- 完成配置模型和启动校验；
- 建立单元测试框架。

#### 第 3～4 周：MCP HTTP 主链路

- 集成官方 MCP Go SDK；
- 实现 Streamable HTTP；
- 实现 Capability 到 Tool 的适配；
- 完成 Session、取消和错误映射；
- 接入至少两个真实 MCP Host 做兼容性测试。

#### 第 5～6 周：HTTP Connector

- 实现 HTTP Provider 和 Connector；
- 完成超时、认证 Header、TLS、SSRF 防护；
- 实现响应大小限制和错误映射；
- 完成一个真实业务服务示例。

#### 第 7～8 周：安全和执行管线

- 实现认证、Principal、Tenant、RBAC；
- 实现输入输出 Schema；
- 实现 Tool Visibility；
- 实现字段白名单和最小敏感数据过滤；
- 实现 Audit Sink。

#### 第 9～10 周：稳定性、测试和文档

- MCP 契约测试；
- Hertz/HTTP 适配测试；
- 安全测试；
- 并发、取消、超时和优雅停机测试；
- 容器化示例；
- Quick Start、Capability 开发指南；
- MVP 发布检查和缺陷修复。

### 5.6 MVP 验收标准

#### 功能验收

- MCP Host 可以初始化、发现和调用 Capability；
- 未授权用户在 `tools/list` 中看不到受限 Tool；
- 越权调用被拒绝且产生审计记录；
- 输入输出不符合 Schema 时调用失败；
- HTTP 下游调用支持超时、取消和错误映射；
- 敏感字段不会因默认配置直接出站；
- 服务可以优雅停机。

#### 兼容性验收

- 至少验证两个主流 MCP Host；
- 通过官方 SDK 适用的协议和 conformance 测试；
- 在直接连接和至少一种反向代理部署下验证；
- SSE 不被错误缓冲；
- Header、Session ID 和取消信号正确传递。

#### 质量验收

- 核心 package 单元测试覆盖关键分支；
- 通过 `go test -race ./...`；
- 通过关键 Schema、Header 和 URL 输入的 fuzz 测试；
- 无已知高危越权、SSRF、Token 泄露问题；
- 示例项目可从零构建和运行。

#### MVP 部署约束

- 正式支持单实例；
- 多副本部署必须使用会话粘滞，且不承诺重启后恢复 Session；
- R2/R3 默认拒绝；
- 配置变更通过重启发布；
- 管理操作通过部署平台完成，不提供 Admin API。

---

## 6. v0.2.0：企业试点增强

### 6.1 目标

使 EACG 能够承载有限写操作、高风险确认和多实例企业试点。

### 6.2 功能范围

- MCP OAuth 2.1 授权发现；
- Protected Resource Metadata；
- 企业 OIDC 和 Token Exchange；
- R2 可逆写入；
- R3 Prepare/Execute；
- Confirmation Token；
- `ConfirmationStore`；
- `IdempotencyStore`；
- 写操作状态查询；
- 参数摘要与一次性原子消费；
- 防 TOCTOU 再校验；
- Retry Policy；
- Circuit Breaker；
- 多实例 Session/Event Store；
- 配置版本和灰度发布；
- 受保护的 Admin API；
- 独立管理监听端口或 mTLS；
- 更完整的数据权限和审计存储。

### 6.3 验收重点

- 同一确认令牌只能成功消费一次；
- 参数或业务状态变化后旧令牌失效；
- 写操作重试不会产生重复业务动作；
- 多副本切换不造成越权或状态错乱；
- 权限撤销后 Tool 可见性和调用权限及时更新；
- Admin API 默认关闭且不能从业务网络匿名访问。

---

## 7. v0.3.0：可观测性与运维

### 7.1 为什么放在 MVP 后

完整可观测性需要稳定的执行阶段、错误模型和标签体系。如果在核心调用链尚未稳定时过早建设，后续会因模型变化反复重构。

因此 MVP 先提供日志、审计、健康检查和耗时记录，`v0.3.0` 再建设标准化可观测性。

### 7.2 功能范围

- Prometheus Metrics；
- OpenTelemetry Trace；
- Trace Context 下游透传；
- MCP、Capability、Connector 标准 Span；
- P50/P95/P99；
- 成功率和错误分类；
- 认证和授权拒绝指标；
- Session、超时、取消、重试、熔断指标；
- 返回数据量指标；
- Grafana Dashboard 模板；
- 告警规则模板；
- SLI/SLO 建议；
- 日志、指标、Trace、Audit 关联；
- 性能和容量基线。

### 7.3 标签控制

Metrics 不允许使用 User ID、Request ID、订单号等高基数字段作为 Label。Tenant、Capability 和 Error Code 需要提供基数上限和可配置聚合策略。

---

## 8. v0.4.0：连接器与生态扩展

按真实业务需求决定优先级，不要求一次完成。

建议顺序：

1. gRPC 或 Kitex 二选一；
2. MCP Connector；
3. 固定模板 Query Connector；
4. `eacg-contrib`；
5. CLI 和领域项目脚手架；
6. 外部 Policy Engine；
7. 控制面。

每增加一种 Connector，都必须保持：

- Connector 只处理协议调用；
- Capability Handler 负责业务编排；
- 下游业务系统执行最终权限和业务规则；
- 不允许绕过下游幂等和状态机；
- 错误、超时、取消、审计语义与 HTTP Connector 一致。

---

## 9. v1.0.0：稳定版本

进入 `v1.0.0` 前应满足：

- 至少两个不同业务域完成生产试点；
- 公开 API 经历至少两个 `v0.x` 版本验证；
- MCP 规范和 SDK 兼容矩阵明确；
- 升级和迁移策略可执行；
- HTTP 和至少一种扩展 Connector 生产稳定；
- R3 确认闭环通过安全评审；
- 多实例部署和灾难恢复完成验证；
- Metrics、Tracing、Audit 和告警达到生产要求；
- 性能和资源基线有可复现测试；
- `CHANGELOG.md`、`MIGRATION.md`、`UPGRADING.md` 完整。

`v1.0.0` 后对公开 API 遵循语义化版本和兼容性承诺。

---

## 10. 关键依赖与团队安排

### 10.1 建议角色

- 2～3 名 Go 工程师：核心、协议、Connector；
- 1 名安全/身份专家兼职：OAuth、权限、确认、审计；
- 1 名业务域工程师兼职：真实 Capability 和下游接口；
- 1 名测试工程师兼职：兼容性、安全和稳定性测试。

### 10.2 外部依赖

- 官方 MCP Go SDK 的稳定版本；
- 至少两个 MCP Host 测试环境；
- 企业测试 OIDC/JWT 环境；
- 一个可稳定调用的业务 HTTP 测试服务；
- 反向代理或入口网关测试环境；
- 容器构建和 CI 环境。

### 10.3 主要风险

| 风险 | 影响 | 应对 |
| --- | --- | --- |
| Hertz 与 MCP 流式语义不兼容 | 阻断主链路 | Pre-MVP PoC；保留原生 `net/http` 路线 |
| MCP SDK 快速演进 | 适配反复修改 | 锁定版本；维护兼容矩阵；SDK 仅存在于适配层 |
| 首期公开 API 过多 | 后续难以演进 | 最小公开面；Binding、Pipeline 实现留在内部 |
| 企业认证环境差异大 | 试点接入延期 | AuthProvider 接口；MVP 先支持 JWT |
| 写操作和确认提前进入 MVP | 引入分布式状态风险 | MVP 限制 R0/R1；R2/R3 延后 |
| 过早建设完整观测平台 | 分散主链路投入 | MVP 只做最小日志、审计和健康检查 |

---

## 11. 发布门禁

每个版本必须通过以下检查才能发布：

```text
Architecture Decision Review
        ↓
Unit / Contract / Race Tests
        ↓
MCP Host Compatibility
        ↓
Security Review
        ↓
Example Gateway Verification
        ↓
Upgrade and Rollback Check
        ↓
Release
```

任何版本如果未解决协议互操作、越权、数据泄露或重复写入问题，不得以“后续优化”为由进入生产试点。

---

## 12. 最终实施建议

EACG 当前最适合从 HTTP-only、R0/R1、单实例的 MVP 起步。

首期成功标准不是功能数量，而是以下闭环真实可靠：

```text
Agent 能发现正确的 Tool
→ 只有正确的人可以调用
→ 输入经过确定性校验
→ HTTP 业务服务被正确调用
→ 输出经过裁剪和校验
→ 全过程可追责
```

完成这一闭环后，再依次增加高风险写操作、分布式状态、完整可观测性和其他 Connector，可以显著降低首版交付风险，并避免在核心模型尚未稳定时形成过重的框架负担。
