---
status: accepted
date: 2026-09-18
---

# 管理后台通过浏览器直连 Broker 进行 MQTT 只读监控

管理后台新增独立的“MQTT 消息监控”运维能力，使用 MQTT.js 经 WebSocket/WSS 直接连接 Broker，而不由 Go 后端代理消息。监控范围仅包含 Edge Collector 发布的 edge status、device status、raw snapshot、device event 和 command result；它不是通用 MQTT 客户端，不订阅或发布 command。这个选择让运维人员能够直接验证 Broker 实际投递行为，并保持本次能力与采集及后端运行链路解耦。

## Considered Options

- **浏览器直连 Broker（采用）**：能观察真实的 QoS、retain、duplicate 和重连投递行为，无需改变后端；代价是 Broker 必须提供受控的 WebSocket/WSS 入口。
- **由 Go 后端代理消息（拒绝）**：可以隐藏 Broker 凭据和网络边界，但会引入新的后端消息转发协议，并且监控到的是代理后的数据，不再是浏览器对 Broker 投递链路的直接验证。

## Consequences

- 生产环境必须使用浏览器信任证书的 WSS，并为监控页面提供独立的只读 Broker 账号；ACL 只允许订阅当前 Edge 的五类上行 Topic，禁止 publish。
- 浏览器监控客户端使用独立且按标签页生成的 client ID，不能复用 Edge Collector 的 client ID。会话是临时会话，离开页面后不保留订阅或离线消息。
- Broker 密码只驻留当前页面内存，不进入 URL、浏览器存储、日志或错误文本。非敏感连接参数可以保存在当前标签页的 sessionStorage。
- 页面保留 Broker 实际交付的 retained 和 duplicate 消息，不做业务去重；消息缓冲必须有数量、总字节数和单条 Payload 大小上限，避免高频 raw 数据造成浏览器内存无界增长。
- HTTPS 页面不得连接 `ws://`；浏览器端不支持录入自定义 CA、客户端证书或私钥，WSS 信任由浏览器和部署环境负责。

## 关联

- ADR-0017：MQTT 上下行、可靠消息与 Starlark 远程控制
- `CONTEXT.md`：MQTT 消息监控
