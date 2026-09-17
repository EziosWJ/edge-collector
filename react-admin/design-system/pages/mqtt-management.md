# MQTT 管理页

## 页面定位

这是 Edge Collector 的运行控制台页面，采用“运行总览 + 标准配置表单 + 标准列表”的复合变体。页面首屏先回答 MQTT runtime 是否连接、可靠 Outbox 是否积压、是否有待发送的 raw latest；配置和 Command Journal 放在其后，保持管理后台的信息密度。

## 交互约束

- 路由为 `/mqtt`，页面查看、配置修改、连接测试和 Command Journal 分别使用 `mqtt:config:list`、`mqtt:config:edit`、`mqtt:config:test`、`mqtt:command:list` 权限点。
- GET 配置只消费 `passwordConfigured` 和 `clientPrivateKeyConfigured`，页面不创建或保存服务端 ciphertext。
- secret 输入框初始为空；`keep` 和 `clear` 请求不包含 secret 字段，只有 `set` 才携带用户刚输入的新值。错误提示过滤当前输入值和敏感字段引用。
- Command Journal 只展示持久化状态与时间元数据，不展示 command payload、result 原文或任何 credential。
- 所有 API 时间字段通过 `src/lib/datetime.ts` 的 `formatDateTime` 展示，按 `Asia/Shanghai` 转换；非法时间回退为 `-`。

## 响应式结构

桌面端运行总览使用左侧连接摘要和右侧三项关键指标；表单保持单列分组，短数值字段在分组内双列；Command Journal 在窄屏下保留横向滚动以保证 commandId 和时间列可读。页面不引入独立视觉主题、渐变或非必要动画。
