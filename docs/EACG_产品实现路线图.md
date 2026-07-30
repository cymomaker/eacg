# EACG 产品实现路线图

## v0.2.0：无状态 Tool 网关

- MCP `2026-07-28` Streamable HTTP；
- Server 发现、Tool 列表和 Tool 调用；
- JWT 用户认证和 API Key 服务身份认证；
- Principal、RBAC、输入输出保护和审计；
- 多实例无状态部署；
- Makefile、Dockerfile、示例和培训文档。

验收重点是基本架构、认证、授权和业务调用正确，不引入 RPC、Kubernetes 或完整可观测性平台。

## v0.3.0：企业接入增强

- 同一服务组合多种 Authenticator；
- 数据库 APIKeyStore 参考实现；
- JWKS/OIDC 验证适配器；
- Tool 级 Scope 策略；
- 可配置缓存失效策略；
- 标准 Trace Context 接入。

## v0.4.0：动态能力

- 运行期安全更新 Tool；
- `subscriptions/listen` 变化通知；
- 动态配置版本与灰度；
- 多租户能力目录。

## 后续版本

根据真实业务需求评估：

- 官方 Tasks 扩展；
- MRTR 多轮输入；
- R2/R3 写能力审批；
- 指标、链路追踪和告警；
- 企业密钥管理控制面。

不因为协议提供某个可选能力就默认实现，新增能力必须有明确业务场景和安全模型。
