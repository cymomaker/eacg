# Changelog

本项目的重要变更记录在此文件中。

格式参考 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)，版本号遵循语义化版本。

## [0.1.0] - 2026-07-28

### Added

- 基于官方 Go SDK 的 MCP Streamable HTTP Server。
- Typed Capability、输入输出 JSON Schema 和运行时校验。
- R0/R1 只读能力及 Capability 级 RBAC。
- JWT 和 Service Token/API Key 两种可选认证模式。
- API Key 应用身份与外部用户身份的复合认证。
- MCP Session 与用户、凭据和权限版本绑定。
- 动态 Tool 可见性与调用时二次授权。
- HTTP Connector、Host 白名单和基础 SSRF 防护。
- 输出字段白名单、敏感字段遮盖和 1 MiB 大小限制。
- 结构化审计、健康检查、就绪检查和优雅停机。
- Makefile、Dockerfile、curl 示例和内部培训文档。

### Security

- API Key 只按 SHA-256 摘要查询，不保存或记录明文。
- 认证错误对外隐藏 Key、userid 和后端内部信息。
- 拒绝重复、冲突和 Query 形式的认证凭据。
- MCP Session 防止跨用户和跨权限版本复用。

### Known Limitations

- 一个服务实例暂时只能选择一种认证模式。
- MCP Session 仅保存在单实例内存中。
- API Key 示例使用内存 Store，生产数据库实现由业务系统提供。
- 只支持 HTTP 下游和 R0/R1 只读能力。
- 不包含 OAuth/OIDC、R2/R3、Kubernetes、Prometheus 和 OpenTelemetry。

[0.1.0]: https://github.com/cymomaker/eacg/releases/tag/v0.1.0
