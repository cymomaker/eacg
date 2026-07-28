# Security Policy

## Supported Versions

当前安全修复支持范围：

| Version | Supported |
| --- | --- |
| `0.1.x` | Yes |
| `< 0.1.0` | No |

## Reporting a Vulnerability

请优先通过 GitHub 仓库 Security 页面提交私密安全报告：

```text
https://github.com/cymomaker/eacg/security
```

安全报告请包含：

- 受影响版本；
- 问题类型和影响范围；
- 最小复现步骤；
- 建议修复方式；
- 是否已经公开披露。

不要在公开 Issue 中提交以下内容：

- API Key、JWT 或其他真实凭据；
- 企业 userid、租户信息或业务数据；
- 可直接利用的完整攻击脚本；
- 尚未修复漏洞的敏感部署信息。

维护者确认问题后，应先完成影响评估和修复，再协调披露时间。

## Security Boundaries

EACG `v0.1.x` 的生产使用方必须自行保证：

- 外部流量使用 HTTPS；
- requester userid Header 只能由可信网关注入；
- JWT Secret 和 API Key 由企业密钥系统管理；
- 示例默认密钥不进入生产环境；
- 多实例部署使用会话粘滞；
- 业务 `APIKeyStore` 和 `SubjectResolver` 正确处理停用、过期和权限版本。
