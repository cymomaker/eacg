# EACG 企业认证兼容方案

## 1. 这套认证解决什么问题

企业微信智能机器人配置 MCP 插件时，可以填写固定的 Service Token 或 API Key。这个固定值只能说明“请求来自哪个机器人或应用”，不能说明“当前是哪位员工在提问”。

EACG 因此把身份分成两层：

```text
API Key                 → 调用应用身份
requester userid Header → 实际用户身份
```

只有两层都验证成功，EACG 才创建完整的 `Principal` 并处理 MCP 请求。

## 2. 各组件负责什么

### EACG 核心

EACG 提供通用组件：

- `Authenticator`：统一认证入口；
- `APIKeyStore`：按摘要查询 API Key；
- `SubjectResolver`：把外部 userid 映射为企业内部用户；
- API Key 最大权限和用户实际权限取交集；
- MCP Session 与应用、用户和权限版本绑定；
- JWT 兼容适配器。

EACG 不提供 API Key 的生成、发放后台，也不理解企业微信组织架构。

### 企业业务系统

业务系统负责实现生产版本的 `APIKeyStore` 和 `SubjectResolver`：

- 从数据库或密钥服务读取 API Key 状态；
- 把企业微信 userid 映射为内部 User ID；
- 查询用户角色、Scope、数据权限和停用状态；
- 返回权限版本，保证权限变化后旧 Session 失效。

### 企业微信

企业微信在每个 MCP 请求中发送：

```http
X-EACG-API-Key: <固定服务密钥>
X-EACG-Requester-UserID: <当前提问者>
```

Header 名称只是本地示例值。生产环境通过环境变量配置实际名称，不需要修改 EACG 源码。

## 3. 一次请求怎样完成认证

```text
1. MCP HTTP 请求到达 /mcp
2. 读取自定义 API Key Header
3. 拒绝同时出现的 Authorization Header
4. 将 API Key 转成 MCP SDK 内部使用的 Bearer 形式
5. 读取 requester userid Header
6. 对 API Key 计算 SHA-256 摘要
7. APIKeyStore 查询服务身份、租户和最大权限
8. SubjectResolver 查询实际用户和用户权限
9. Roles、Scopes 分别取交集
10. 生成 Principal 和 SessionBindingID
11. MCP SDK 处理 initialize、tools/list 或 tools/call
```

`TenantID`、`ClientID` 和 `AgentID` 只能来自 API Key 记录。请求方不能通过 Header 自行指定租户或应用身份。

## 4. 为什么权限要取交集

假设机器人最多允许读取资料：

```text
API Key Roles = [reader]
```

某位员工拥有：

```text
User Roles = [reader, admin]
```

最终权限是：

```text
Effective Roles = [reader]
```

机器人不能借用管理员的全部权限，用户也不能借用机器人拥有但自己没有的权限。任何一侧权限列表为空，最终都没有权限。

## 5. Session 怎样绑定身份

EACG 使用以下信息生成 SHA-256 会话绑定标识：

- Tenant ID；
- Client ID；
- Agent ID；
- User ID；
- Credential ID；
- API Key 版本；
- 用户权限版本；
- 最终 Roles 和 Scopes。

MCP SDK 在 Session 创建时保存这个标识。后续请求如果更换用户、Key 或权限版本，将返回 `403 session user mismatch`，客户端需要重新执行初始化。

API Key 和 `Mcp-Session-Id` 不能互相替代：

- API Key 证明调用应用身份；
- requester userid 表示实际用户；
- Session ID 表示一段 MCP 协议会话。

## 6. 示例配置

启动本地 API Key 示例：

```bash
make run-api-key
```

等价环境变量如下：

```bash
export EACG_AUTH_MODE=api_key
export EACG_API_KEY=0123456789abcdef0123456789abcdef
export EACG_API_KEY_ID=wecom-demo-key
export EACG_TENANT_ID=tenant-a
export EACG_CLIENT_ID=wecom-bot
export EACG_CREDENTIAL_HEADER=X-EACG-API-Key
export EACG_REQUESTER_USER_HEADER=X-EACG-Requester-UserID
export EACG_SUBJECT_PROVIDER=wecom
go run ./cmd/eacg-example
```

`EACG_API_KEY_EXPIRES_AT` 可以填写 RFC3339 时间，例如：

```text
2026-12-31T23:59:59+08:00
```

未配置 Key 过期时间时，每次认证结果使用 5 分钟租约，但每个 HTTP 请求仍会重新查询 Key 和用户状态。

完整 curl 时序见项目 `README.md`。

## 7. 生产环境接入要求

### Key Store

生产实现只保存高强度随机 API Key 的 SHA-256 摘要，不保存明文。每条记录至少包括：

- 凭据 ID；
- 租户和应用身份；
- 允许的用户来源；
- 最大 Roles 和 Scopes；
- 启用状态；
- 版本；
- 可选过期时间。

轮换时可以同时保留新旧两个 Key，客户端切换完成后停用旧 Key。停用后，旧 Key 的下一次请求立即认证失败。

### 用户目录

生产 `SubjectResolver` 应查询企业 IAM、组织目录或 RBAC 服务。不要使用示例中“外部 userid 直接作为内部 User ID”的实现。

### Header 信任

requester userid Header 本身不是密码，也不是用户签名。生产入口必须：

- 使用 HTTPS；
- 删除公网客户端自行传入的同名 Header；
- 只允许可信企业微信链路或网关注入该 Header；
- 条件允许时增加 mTLS、来源网络限制或请求签名。

如果攻击者同时获得 API Key 并能伪造 userid Header，这种认证方式无法证明真实用户身份。

## 8. 常见错误

| HTTP 状态 | 含义 |
| --- | --- |
| `401` | API Key、userid 或身份映射无效 |
| `403` | 当前请求身份与已有 MCP Session 不一致 |
| `404` | Session 不存在、过期或已关闭 |
| `500` | Key Store 或用户目录暂时不可用 |

认证失败日志只包含 Request ID 和稳定错误码，不记录 API Key、完整 userid 或请求正文。
