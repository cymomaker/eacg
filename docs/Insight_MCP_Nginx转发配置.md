# Insight MCP Server Nginx 转发配置

本文说明如何通过 Nginx 将 Insight MCP Server 暴露为统一的企业 MCP 地址：

```text
https://mcp.wsyxmall.com/insight/v1/mcp
```

请求链路如下：

```text
MCP Client
    -> https://mcp.wsyxmall.com/insight/v1/mcp
    -> Nginx
    -> http://127.0.0.1:9801/mcp
```

## 前提条件

- Insight MCP Server 已启动并监听 `9801` 端口。
- 服务内部的 MCP 路径为 `/mcp`。
- 域名 `mcp.wsyxmall.com` 已解析到 Nginx 所在服务器。
- 证书文件已放置在以下位置：

```text
/etc/nginx/ssl/wsyxmall.net.pem
/etc/nginx/ssl/wsyxmall.net.key
```

可以先在 Nginx 所在服务器上检查上游端口：

```bash
curl -i http://127.0.0.1:9801/mcp
```

这里即使返回 `400`、`401`、`405` 等 HTTP 响应，也说明端口和 HTTP 服务已经连通；MCP 请求通常还需要正确的方法、协议头和认证信息。

## Nginx 配置

建议新建：

```text
/etc/nginx/conf.d/mcp.wsyxmall.com.conf
```

完整配置如下：

```nginx
upstream insight_mcp_server {
    server 127.0.0.1:9801;
    keepalive 32;
}

server {
    listen 80;
    server_name mcp.wsyxmall.com;

    return 301 https://$host$request_uri;
}

server {
    listen 443 ssl http2;
    server_name mcp.wsyxmall.com;

    ssl_certificate     /etc/nginx/ssl/wsyxmall.net.pem;
    ssl_certificate_key /etc/nginx/ssl/wsyxmall.net.key;

    ssl_protocols TLSv1.2 TLSv1.3;
    client_max_body_size 10m;

    # 对外地址：/insight/v1/mcp
    # 上游地址：http://127.0.0.1:9801/mcp
    location = /insight/v1/mcp {
        proxy_pass http://insight_mcp_server/mcp;
        proxy_http_version 1.1;

        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header X-Forwarded-Host $host;
        proxy_set_header X-Forwarded-Port $server_port;

        # MCP Streamable HTTP 可能返回流式响应，不能让 Nginx 缓冲。
        proxy_buffering off;
        proxy_request_buffering off;
        proxy_cache off;
        gzip off;

        # 允许长时间运行的 MCP 调用和流式连接。
        proxy_connect_timeout 10s;
        proxy_send_timeout 3600s;
        proxy_read_timeout 3600s;

        # 不向上游发送 hop-by-hop Connection 头。
        proxy_set_header Connection "";

        # 同时通知可能存在的上层 Nginx 不要缓冲响应。
        add_header X-Accel-Buffering no always;
    }

    # 避免把其他未知路径意外转发给内部服务。
    location / {
        return 404;
    }
}
```

`Authorization`、`Mcp-Protocol-Version`、`Accept` 和 `Content-Type` 等客户端请求头，Nginx 默认会继续转发，无须逐个重复配置。生产访问日志不得包含 `$request_body`，避免记录协议参数或潜在业务数据。

MCP Streamable HTTP 不是 WebSocket，因此不需要配置 `Upgrade` 和 `Connection: upgrade`。

## 加载配置

先检查配置语法：

```bash
sudo nginx -t
```

确认无误后平滑加载：

```bash
sudo nginx -s reload
```

如果 Nginx 由 systemd 管理，也可以使用：

```bash
sudo systemctl reload nginx
```

## 验证转发

先验证域名、TLS 和路由是否可达：

```bash
curl -i https://mcp.wsyxmall.com/insight/v1/mcp
```

随后使用实际 MCP 客户端，将服务地址配置为：

```text
https://mcp.wsyxmall.com/insight/v1/mcp
```

如果服务使用 Bearer Token，可通过下面的请求检查认证头是否成功转发：

```bash
curl -i https://mcp.wsyxmall.com/insight/v1/mcp \
  -H 'Authorization: Bearer REPLACE_WITH_TOKEN'
```

不要把真实 Token 写入 Nginx 配置、Shell 历史或项目仓库。

## 常见问题

### 返回 502 Bad Gateway

在 Nginx 服务器上检查：

```bash
ss -lntp | grep 9801
curl -i http://127.0.0.1:9801/mcp
```

如果 Insight MCP Server 只监听了其他 IP，需修改其监听地址，或者将 `upstream` 中的地址改为服务实际可达的地址。

### Nginx 运行在 Docker 中

此时 `127.0.0.1:9801` 指向 Nginx 容器自身，而不是宿主机。应将上游改成同一 Docker 网络中的服务名，例如：

```nginx
upstream insight_mcp_server {
    server insight-mcp-server:9801;
    keepalive 32;
}
```

### 流式响应延迟或一次性返回

确认对应 `location` 中存在：

```nginx
proxy_buffering off;
proxy_cache off;
gzip off;
```

还需检查 Nginx 前面是否存在 CDN、负载均衡器或其他反向代理，并在这些代理上关闭响应缓冲、延长空闲连接超时。

### 路径返回 404

客户端应使用不带结尾斜杠的精确地址：

```text
https://mcp.wsyxmall.com/insight/v1/mcp
```

该配置不会匹配 `/insight/v1/mcp/`。使用精确路径可以避免错误路径被静默转发。

## 后续扩展 Mall MCP Server

以后增加 Mall MCP Server 时，可继续使用相同域名和项目级版本结构：

```text
https://mcp.wsyxmall.com/mall/v1/mcp
```

为 Mall 创建独立的 `upstream` 和精确 `location` 即可，Insight 与 Mall 可以分别升级为 `v2`，互不影响。
