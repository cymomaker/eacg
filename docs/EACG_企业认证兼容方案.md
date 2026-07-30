# EACG 企业认证兼容方案

## 1. 两种 API Key 使用方式

### 服务身份

```text
API Key → Tenant + Client + Roles/Scopes → Service Principal
```

适用于内部服务、定时任务、机器人后台和其他机器到机器调用。只需要 API Key，不要求 userid。

### 代理用户身份

```text
API Key → 调用应用
requester userid → 实际用户
业务用户目录 → 用户权限
自定义 Authenticator → User Principal
```

适用于企业微信等“应用代表用户操作”的场景。userid 不是 API Key 的组成部分，其真实性和业务映射由接入方负责。

## 2. EACG 与业务职责

EACG 核心负责：

- 通用 `Authenticator` 和 `Principal`；
- API Key 摘要校验及服务身份；
- 可选 Subject Header 安全读取；
- 身份类型、角色授权和审计。

业务系统负责：

- 外部 userid 到企业用户的映射；
- 用户状态、租户和数据权限检查；
- 应用权限与用户权限的合并；
- 企业微信、钉钉等平台专用认证逻辑。

## 3. HTTP 配置

纯服务 API Key：

```go
eacg.HTTPAuthenticationConfig{
    Authenticator:    apiKeyAuthenticator,
    CredentialHeader: "X-EACG-API-Key",
}
```

业务自定义代理用户认证：

```go
eacg.HTTPAuthenticationConfig{
    Authenticator:    weComAuthenticator,
    CredentialHeader: "X-EACG-API-Key",
    SubjectHeader:    "X-EACG-Requester-UserID",
    SubjectProvider:  "wecom",
}
```

未配置 `SubjectHeader` 时，EACG 不要求 userid，并向 Authenticator 传入 `Subject=nil`。配置后，Header 必须存在且只能出现一次。

## 4. API Key Store

```go
type APIKeyStore interface {
    LookupByDigest(context.Context, [32]byte) (APIKeyRecord, error)
}
```

生产 Store 可以查询数据库或使用支持主动失效的安全缓存。EACG 只传入 SHA-256 摘要，不保存和记录明文 Key。

API Key 记录中的 TenantID、ClientID、AgentID、Roles 和 Scopes 是可信服务身份，不能被 HTTP Header 或 Tool 参数覆盖。

## 5. 企业微信接入

插件配置：

```text
授权方式：Service token / API key
位置：Header
Parameter name：X-EACG-API-Key
传输协议：Streamable HTTP
```

如果 Tool 只使用机器人服务权限，不需要 requester userid。

如果 Tool 必须识别实际用户：

- 业务项目实现自定义 Authenticator；
- 入口网关删除外部请求自带的 userid Header；
- 可信企业微信链路重新注入 userid；
- 无法保证 Header 来源时增加 mTLS、来源限制或签名验证；
- 返回 `SubjectUser` Principal，并为 Tool 声明 `IdentityUser`。

## 6. Capability 授权

```text
IdentityAny     → 用户和服务都可调用
IdentityUser    → 必须有真实用户
IdentityService → 必须是系统服务
```

Registry 在 `tools/list` 阶段按身份类型和角色裁剪；Execution Engine 在 `tools/call` 阶段再次校验。

## 7. 生产安全要求

- 外部只使用 HTTPS；
- Key 由企业密钥系统生成、轮换和吊销；
- Key Store 只保存摘要；
- 每个 MCP 请求重新认证；
- 自定义 Key Header 与 Authorization 冲突时拒绝；
- Query 中的凭据不生效；
- 日志只记录 CredentialID，不记录明文；
- 服务身份审计的 UserID 为空，保留 Tenant、Client、Agent 和 SubjectType；
- 代理用户 Header 不可信时，不能把它当作强用户认证。
