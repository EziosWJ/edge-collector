# Requirements

- [完整产品需求基线](edge-collector-requirements.md)
- [分阶段交付计划](delivery-phases.md)
- [Modbus 模拟器 Spec](../specs/modbus-simulator.md)
- [RS485 采集报文节流与配置运行时刷新 Spec](../specs/rs485-acquisition-pacing-and-runtime-reconfiguration.md)
- [Starlark Modbus 动态事务 Implementation Spec](../specs/starlark-modbus-dynamic-transactions.md)
- [ADR-0016 实际验收记录](../acceptance/adr-0016-starlark-dynamic-transactions.md)
- [ADR-0017 MQTT 上下行、可靠消息与 Starlark 远程控制](../adr/0017-mqtt-uplink-downlink-reliable-control.md)
- [MQTT 上下行、可靠消息与 Starlark 远程控制 Implementation Spec](../specs/mqtt-uplink-downlink-reliable-control.md)

第一阶段正式 Spec 见 GitHub Issue #1；ADR-0016 已完成实现与验收。MQTT 第三阶段的 Topic/Payload/QoS、可靠 outbox、command journal、Starlark `command(ctx,name,args)` 与管理面已按 ADR-0017 / Issue #39 及 #40～#47 实现；真实 Docker Broker/PostgreSQL 矩阵的执行记录见 [`docs/acceptance/adr-0017-mqtt-reliable-control.md`](../acceptance/adr-0017-mqtt-reliable-control.md)。关键架构决策见 ADR-0011、ADR-0013、ADR-0014、ADR-0015、ADR-0016 与 ADR-0017；技术方案见 `docs/design/phase-1-acquisition-technical-design.md`。
