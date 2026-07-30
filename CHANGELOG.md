# Changelog

本项目遵循语义化版本。

## [0.2.0] - 2026-07-29

### Changed

- MCP Go SDK 升级到 `v1.7.0`。
- 协议固定为 MCP `2026-07-28` 无状态 Streamable HTTP。
- 普通请求改为 JSON 响应，并支持请求取消传播。
- Server 发现结果只声明一个协议版本。
- Tool 列表使用私有、立即过期的缓存策略。
- 使用 Go 标准库实现跨域请求保护。
- API Key 认证改为不要求 userid 的服务身份认证。
- Principal 增加用户/服务身份类型，Capability 支持身份类型约束。

### Removed

- 旧版协议生命周期和服务器端协议会话。
- 身份认证中的会话绑定摘要及相关版本字段。
- 内置 API Key 认证器中的 SubjectResolver 和用户权限交集。
- 事件重放、协议保活和已经废弃的 MCP 能力。

### Security

- 每个 HTTP 请求独立认证和授权。
- 严格校验协议版本、方法、Tool 名称及 Header/Body 一致性。
- 拒绝旧会话和事件续传 Header。
- 多实例部署不再需要会话粘滞或共享会话存储。

## [0.1.0] - 2026-07-28

- 首个 MCP Tool 网关 MVP。
- 提供 Typed Capability、JWT/API Key、RBAC、审计和 HTTP Connector。

[0.2.0]: https://github.com/cymomaker/eacg/releases/tag/v0.2.0
[0.1.0]: https://github.com/cymomaker/eacg/releases/tag/v0.1.0
