# WhatsApp Forwarder

专用 Android 通知转发器，只监听 WhatsApp / WhatsApp Business，并把包含 OTP 的通知 POST 到本项目的 GoPay OTP webhook。

默认 webhook payload：

```json
{
  "otp": "123456",
  "source": "whatsapp"
}
```

服务端地址填：

```text
http://192.168.0.115:8081/webhook/otp
```

## 构建

```bash
cd whatsapp-forwarder
./gradlew assembleDebug
```

也可以在构建时写入默认 webhook：

```bash
./gradlew assembleDebug -PdefaultWebhookUrl=http://192.168.0.115:8081/webhook/otp
```

APK 输出：

```text
whatsapp-forwarder/app/build/outputs/apk/debug/app-debug.apk
```

## 手机端设置

1. 安装 APK。
2. 打开应用，填写 webhook URL，保存。
3. 点击 `Open`，在系统通知访问设置里启用 `WhatsApp Forwarder`。
4. 允许通知权限；应用会显示一条低优先级常驻通知，用于提高后台存活率。
5. 点击 `Battery settings`，允许忽略电池优化；部分 ROM 还需要允许自启动、后台运行并锁定后台。
6. 点击 `Test`，服务端 `gopay-payment` 日志应出现 OTP accepted。

说明：保活服务使用 `specialUse` 前台服务类型，避免 Android 15+ 对 `dataSync` 前台服务的 6 小时后台限额。开机广播会尝试重新绑定通知监听器并启动保活服务；如果厂商 ROM 拦截自启动，重启后手动打开一次应用即可恢复。

## 为什么不会只发第一条

参考项目使用 `notificationKey` 做唯一索引，WhatsApp 同一会话可能复用同一个通知 key，第二条以后会被数据库 `IGNORE` 掉。这个专用版本：

- 不使用本地持久队列，也不把 `notificationKey` 当唯一键；
- 每条候选 OTP 通知都会直接 POST 到 webhook；
- 会解析 `EXTRA_TEXT`、`EXTRA_BIG_TEXT`、`EXTRA_TEXT_LINES` 和 MessagingStyle messages，适配 WhatsApp 聚合通知。

实现参考了 ItsAzni/NotificationForwarder 的通知监听思路：https://github.com/ItsAzni/NotificationForwarder
