# MQTT 管理

## 页面与路由

MQTT 管理由三个独立页面组成：

- `/mqtt/overview`：运行总览。
- `/mqtt/config`：连接配置。
- `/mqtt/commands`：Command Journal。

`/mqtt` 仅作为兼容入口，跳转到当前用户可访问的默认页面，优先进入运行总览。三个页面分别加载、分别处理 loading/error/empty 状态，不再渲染纵向堆叠的复合页面。

左侧导航将 MQTT 管理显示为目录，并按权限显示可访问的子页面。页面名称固定为“运行总览”“连接配置”和“Command Journal”。

## 权限

页面和操作使用独立权限：

- `mqtt:overview:list`：运行总览。
- `mqtt:config:list`：查看连接配置。
- `mqtt:config:edit`：保存连接配置。
- `mqtt:config:test`：测试连接。
- `mqtt:command:list`：查看 Command Journal 列表。
- `mqtt:command:detail`：查看 Journal 详情。

拥有任一 MQTT 子权限时显示 MQTT 管理目录；没有对应权限的页面或操作不显示，直接访问仍必须受到页面级权限守卫保护。拥有 Journal 权限不要求同时拥有连接配置权限。

## 运行总览

运行总览只负责观察 MQTT，不重复展示通信通道、设备或 acquisition 健康状态。页面依次突出：

- MQTT runtime 主状态、Broker 连接状态、订阅、重连次数，以及最近连接、断开和重试时间。
- Reliable Outbox 行数、字节容量、最早可靠消息、容量利用率和最近投递错误。
- Raw pending latest，作为待发送的 latest-state 信息指标，不直接判定为故障。

状态语义保持一致：停用为中性；连接中或重连中为警告；已启用但未连接或处于错误状态为错误；Reliable Outbox 达到 80% 为警告，达到 100% 为错误。

运行状态和 Outbox 数据独立加载、独立显示错误。一方刷新失败时保留另一方的可用数据，并保留上一次成功数据，同时提示数据可能已过期和最后成功刷新时间。页面可见时每 10 秒自动刷新，也支持手动刷新；不覆盖连接配置页的草稿。

异常指标提供上下文跳转：连接问题跳转到连接配置的 Broker/启用区域，Outbox 问题跳转到可靠性区域，Command 失败或积压跳转到带状态筛选的 Command Journal。Reliable Outbox 不单独成页。

## 连接配置

连接配置保留现有 MQTT 配置字段和请求语义，按以下区域组织：

- 基础连接：启用、Edge ID、Broker、协议、Client ID 和认证信息。
- TLS：TLS、CA、客户端证书和私钥。
- 高级连接：Keep Alive、超时、重连、Topic 和 Raw 上报。
- 可靠性：Outbox、Journal、Command 队列和公平调度参数。

基础连接和 TLS 首次可见；高级连接和可靠性默认折叠。MQTT 停用时仍可编辑配置；关闭 TLS 时保留已有证书值，只有用户明确清除并保存才删除。

表单有未保存变更时，离开页面、刷新或关闭浏览器会提示；“放弃变更”恢复最近一次已保存配置。表单有草稿变更时，“测试连接”使用当前草稿且不保存；没有草稿变更时测试已保存配置。保存成功后立即反馈配置已保存，runtime 异步重连，不等待 Broker 连接成功。

Secret 仅写入，不回显。读取配置只展示 `passwordConfigured` 和 `clientPrivateKeyConfigured` 等配置状态；保持现有 `keep/set/clear` 语义，`keep` 和 `clear` 不提交 Secret，只有 `set` 携带新值。清除 Broker 密码或客户端私钥必须显式确认。错误、日志、审计和页面均不得泄露密码、私钥或 ciphertext。

没有 `mqtt:config:edit` 时可以查看配置但不能保存；没有 `mqtt:config:test` 时隐藏测试操作。

## Command Journal

Command Journal 是 MQTT 控制指令的只读持久化事实视图，负责查询命令生命周期，不提供任何控制操作。页面默认按 `receivedAt` 倒序，并以 Command ID 稳定打散；使用服务端分页，默认每页 20 条，提供 10、20、50、100 条选项，服务端继续限制最大页大小。

支持以下服务端筛选：

- 状态。
- 设备 ID 精确匹配。
- Command ID 精确或前缀匹配。
- 命令名称包含匹配。

查询接口保留 `status`、`deviceId`、`page` 和 `pageSize`，并增加 `commandId` 和 `name`。列表存在 `ACCEPTED` 或 `RUNNING` 记录时每 5 秒刷新；全部记录进入终态后停止自动刷新，始终支持手动刷新。

详情使用抽屉或宽弹窗保留列表上下文，只展示安全元数据和脱敏后的稳定错误信息。Command payload、result payload、payload hash、ciphertext 和 credential 不进入 API 响应或页面。

列表和详情分别受 `mqtt:command:list`、`mqtt:command:detail` 控制。Command Journal 不新增重试、取消、重放、导出或手动下发命令。

## 时间与响应式约束

API 时间保持带时区的 RFC3339 UTC instant。页面所有时间统一经过 `src/lib/datetime.ts` 的格式化函数，按 `Asia/Shanghai` 展示；非法时间回退为 `-`，不得通过字符串截取或替换模拟时区转换。

窄屏下运行总览指标单列堆叠，连接配置表单单列布局，Command Journal 保留横向滚动以保证 Command ID 和时间列可读，页面头部操作允许换行。页面不引入独立视觉主题、渐变或非必要动画。

## 明确不包含

- Command 重试、取消、重放、导出或手动创建。
- SSE、WebSocket 或新的实时传输机制。
- 独立 Reliable Outbox 页面。
- MQTT Topic/Payload、runtime 状态机、控制安全边界、可靠结果容量准入或 Secret 存储语义的改变。
- acquisition、通信通道、设备、脚本或设备健康页面的改动。

本页设计继续遵守 ADR-0017；MQTT runtime、可靠 Outbox、Command Journal、控制安全边界和 Secret 语义以该 ADR 为准。
