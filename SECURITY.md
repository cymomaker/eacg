# Security Policy

## 支持版本

| Version | Supported |
| --- | --- |
| `0.2.x` | Yes |
| `< 0.2.0` | No |

## 安全边界

生产环境必须满足：

- 外部流量使用 HTTPS；
- API Key、JWT Secret 由企业密钥系统生成、保存和轮换；
- 内置 API Key 服务认证不要求 requester userid；
- 业务启用代理用户认证时，requester userid Header 只能由可信网关注入；
- 每个 MCP 请求都经过认证和授权；
- 业务 `APIKeyStore` 正确处理停用、过期和权限变化；
- 自定义用户 Authenticator 负责用户状态、租户和权限校验；
- 数据库账号遵循最小权限原则；
- 日志和审计不得记录明文凭据、完整请求正文和内部数据库错误。

EACG `v0.2.x` 不保存 MCP 协议会话。多实例部署可以直接负载均衡，不需要会话粘滞。

## 漏洞报告

请通过 GitHub Security 页面提交私密报告：

```text
https://github.com/cymomaker/eacg/security
```

报告应包含受影响版本、影响范围、最小复现步骤和建议修复方式。不要在公开 Issue 中提交真实凭据、企业用户信息或尚未修复漏洞的利用代码。
