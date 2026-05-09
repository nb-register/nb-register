# NB Register

本项目用于本地编排账号注册、Outlook 邮件 OTP、GoPay 支付和工作流看板。

部分模块仅限授权研究、内部实验和非商用用途。支付和浏览器自动化相关实现参考并感谢 [DanOps-1/Gpt-Agreement-Payment](https://github.com/DanOps-1/Gpt-Agreement-Payment)。

根仓只保存 compose、文档和共享 proto。`account-db`、`browser-reg`、`dashboard`、`gopay-payment`、`orchestrator`、`outlook-imap-service` 等服务目录由各自仓库独立管理。

## 快速启动

```bash
cp compose.example.env compose.env
```

编辑 `compose.env` 顶部的用户配置项。通常只需要改这些：

```env
OUTLOOK_EMAIL=
REGISTER_PROXY_URL=socks5://host.docker.internal:10813

GOPAY_COUNTRY_CODE=62
GOPAY_PHONE_NUMBER=
GOPAY_PIN=
GOPAY_PROXY_URL=socks5://host.docker.internal:10813
GOPAY_OTP_WEBHOOK_TOKEN=
```

启动：

```bash
docker compose --env-file compose.env up -d --build
```

打开看板：

```text
http://127.0.0.1:8080
```

健康检查：

```bash
curl -fsS http://127.0.0.1:8080/api/health
```

## 配置说明

`compose.example.env` 已按使用频率分层：

- `User settings`：首次运行必须确认，包含 Outlook 主邮箱、注册代理、GoPay 手机号/PIN/代理、OTP webhook token。
- `Optional host ports`：默认即可，只有本机端口冲突时再改。
- `Stable defaults`：内部服务地址、数据库、Temporal、OTP 等待时间等，正常不要改。

真实值只写入 `compose.env`。`compose.env`、token、日志、抓包、浏览器状态和数据库数据都不会入库。

## Outlook 授权

`outlook-imap-service` 不需要手动配置 refresh token。服务启动后会读取：

```text
outlook-imap-service/tokens/outlook_refresh_token
```

如果 token 不存在、为空、过期或被撤销，服务会自动进入 Microsoft device flow。查看登录码：

```bash
docker compose --env-file compose.env logs -f outlook-imap-service
```

按日志提示打开 Microsoft device 页面并输入 code。授权成功后 token 会写回 `outlook-imap-service/tokens/`，后续自动刷新。

强制重新授权：

```bash
rm -f outlook-imap-service/tokens/outlook_refresh_token
docker compose --env-file compose.env restart outlook-imap-service
docker compose --env-file compose.env logs -f outlook-imap-service
```

## GoPay OTP

GoPay payment 内置 OTP webhook。手机端通知转发工具把收到的 GoPay OTP POST 到：

```text
http://<本机局域网 IP>:8081/webhook/otp
```

本机测试：

```bash
curl -X POST http://127.0.0.1:8081/webhook/otp \
  -H 'Content-Type: application/json' \
  -d '{"otp":"123456","source":"phone"}'
```

也支持纯文本 payload。配置了 `GOPAY_OTP_WEBHOOK_TOKEN` 时，请求需要带：

```bash
-H "Authorization: Bearer $GOPAY_OTP_WEBHOOK_TOKEN"
```

GoPay 支付参数来自容器环境变量，不从 gRPC 请求传入：

```env
GOPAY_COUNTRY_CODE=62
GOPAY_PHONE_NUMBER=
GOPAY_PIN=
GOPAY_PROXY_URL=socks5://host.docker.internal:10813
```

## 看板操作

在 `http://127.0.0.1:8080` 可以执行：

- 创建账号：可不填邮箱/密码，服务会生成 Outlook plus alias 和随机密码。
- 注册账号：触发 `browser-reg`，等待 Outlook 邮件 OTP。
- 激活账号：使用账号 session token / access token 触发 GoPay 支付，等待 GoPay OTP webhook 回传。
- 注册并激活：按顺序执行注册和支付。
- 账号详情：查看/隐藏账号密码，修改 session token。
- 工作流详情：查看 job 状态、步骤、错误和结果摘要。

账号有运行中的 job 时，行内操作会显示“进行中”并禁止重复触发。

## 常用命令

查看服务：

```bash
docker compose --env-file compose.env ps
```

查看日志：

```bash
docker compose --env-file compose.env logs -f orchestrator
docker compose --env-file compose.env logs -f browser-reg
docker compose --env-file compose.env logs -f gopay-payment
docker compose --env-file compose.env logs -f outlook-imap-service
```

重启单个服务：

```bash
docker compose --env-file compose.env restart dashboard
```

重建单个服务：

```bash
docker compose --env-file compose.env up -d --build dashboard
```

停止：

```bash
docker compose --env-file compose.env down
```

## 开发检查

```bash
(cd account-db && go test ./...)
(cd orchestrator && go test ./...)
(cd dashboard && go test ./...)
(cd outlook-imap-service && go test ./...)
(cd dashboard/web && npm run build)
docker compose --env-file compose.example.env config --quiet
```

## 更多文档

- [account-db](account-db/README.md)
- [browser-reg](browser-reg/README.md)
- [dashboard](dashboard/README.md)
- [orchestrator](orchestrator/README.md)
- [outlook-imap-service](outlook-imap-service/README.md)
- [gopay-payment](gopay-payment/README.md)
