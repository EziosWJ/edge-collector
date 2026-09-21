import type {
  AcquisitionChannel,
  AcquisitionChannelRuntimeState,
  AcquisitionChannelRuntimeStatus,
  AcquisitionCommunicationStatus,
  AcquisitionCurrentState,
  AcquisitionDevice,
  AcquisitionProtocol,
} from "@/types";
import { acquisitionChannelRuntimeStatusMeta, acquisitionCommunicationStatusMeta } from "@/types";
import type {
  DashboardAttentionItem,
  DashboardAttentionResult,
  DashboardDomainSummary,
  DashboardSources,
} from "./types";

export const protocolLabels: Record<AcquisitionProtocol, string> = {
  MODBUS_RTU: "Modbus RTU",
  MODBUS_TCP: "Modbus TCP",
  MODBUS_UDP: "Modbus UDP",
  MODBUS_RTU_OVER_UDP: "RTU over UDP",
};

export function getEnabledChannels(channels: AcquisitionChannel[] | null) {
  return (channels ?? []).filter((channel) => channel.enabled === 1);
}

export function getEnabledDevices(devices: AcquisitionDevice[] | null) {
  return (devices ?? []).filter((device) => device.enabled === 1);
}

export function getChannelRuntime(
  channel: AcquisitionChannel,
  states: AcquisitionChannelRuntimeState[] | null,
) {
  return (
    states?.find((state) => state.channelId === channel.id) ?? {
      channelId: channel.id,
      channelName: channel.name,
      protocol: channel.protocol,
      status: "IDLE" as const,
      lastAttemptAt: null,
      lastSuccessAt: null,
      lastError: undefined,
    }
  );
}

export function getChannelStatusMeta(status: AcquisitionChannelRuntimeStatus) {
  return acquisitionChannelRuntimeStatusMeta[status];
}

export function getDeviceStatusMeta(status: AcquisitionCommunicationStatus) {
  return acquisitionCommunicationStatusMeta[status];
}

export function getDeviceState(
  device: AcquisitionDevice,
  states: AcquisitionCurrentState[] | null,
) {
  return (
    states?.find((state) => state.deviceId === device.id) ?? {
      deviceId: device.id,
      deviceName: device.name,
      channelId: device.channelId,
      unitId: device.unitId,
      registerBlocks: [],
      lastAttemptAt: null,
      lastSuccessAt: null,
      status: "INITIAL" as const,
      consecutiveFailures: 0,
      lastError: undefined,
    }
  );
}

export function getChannelCounts(channels: AcquisitionChannel[], states: AcquisitionChannelRuntimeState[] | null) {
  const counts: Record<AcquisitionChannelRuntimeStatus, number> = {
    IDLE: 0,
    STARTING: 0,
    ONLINE: 0,
    DEGRADED: 0,
    OFFLINE: 0,
  };
  channels.forEach((channel) => {
    counts[getChannelRuntime(channel, states).status] += 1;
  });
  return counts;
}

export function getDeviceCounts(devices: AcquisitionDevice[], states: AcquisitionCurrentState[] | null) {
  const counts: Record<AcquisitionCommunicationStatus, number> = {
    INITIAL: 0,
    ONLINE: 0,
    DEGRADED: 0,
    OFFLINE: 0,
  };
  devices.forEach((device) => {
    counts[getDeviceState(device, states).status] += 1;
  });
  return counts;
}

function sourceError(sources: DashboardSources, keys: Array<keyof DashboardSources>) {
  return keys.map((key) => sources[key].error).find(Boolean) ?? "";
}

function sourceLoading(sources: DashboardSources, keys: Array<keyof DashboardSources>) {
  return keys.some((key) => sources[key].loading);
}

function sourceStale(sources: DashboardSources, keys: Array<keyof DashboardSources>) {
  return keys.some((key) => sources[key].stale);
}

export function buildDomainSummaries(
  sources: DashboardSources,
  mqttVisible: boolean,
): DashboardDomainSummary[] {
  const channels = getEnabledChannels(sources.channels.data);
  const devices = getEnabledDevices(sources.devices.data);
  const channelCounts = getChannelCounts(channels, sources.channelStates.data);
  const deviceCounts = getDeviceCounts(devices, sources.states.data);
  const acquisitionKeys: Array<keyof DashboardSources> = ["channels", "channelStates", "scriptStates"];
  const deviceKeys: Array<keyof DashboardSources> = ["devices", "states"];
  const acquisitionError = sourceError(sources, acquisitionKeys);
  const deviceError = sourceError(sources, deviceKeys);
  const acquisitionLoading = sourceLoading(sources, acquisitionKeys);
  const deviceLoading = sourceLoading(sources, deviceKeys);

  const acquisition: DashboardDomainSummary = {
    key: "acquisition",
    label: "采集运行",
    status: acquisitionError ? "error" : channels.length === 0 ? "neutral" : channelCounts.OFFLINE > 0 ? "error" : channelCounts.DEGRADED > 0 ? "warning" : channelCounts.ONLINE > 0 ? "success" : "neutral",
    statusLabel: acquisitionError ? "数据不可用" : channels.length === 0 ? "未配置" : channelCounts.OFFLINE > 0 ? "有通道离线" : channelCounts.DEGRADED > 0 ? "部分失败" : channelCounts.ONLINE > 0 ? "运行正常" : "运行准备中",
    detail: acquisitionError ? acquisitionError : channels.length === 0 ? "尚未配置启用的通信通道" : `${channels.length} 个启用通道 · ${channelCounts.ONLINE} 在线`,
    loading: acquisitionLoading,
    error: acquisitionError,
    stale: sourceStale(sources, acquisitionKeys),
  };
  const device: DashboardDomainSummary = {
    key: "device",
    label: "设备通信",
    status: deviceError ? "error" : devices.length === 0 ? "neutral" : deviceCounts.OFFLINE > 0 ? "error" : deviceCounts.DEGRADED > 0 ? "warning" : deviceCounts.ONLINE > 0 ? "success" : "neutral",
    statusLabel: deviceError ? "数据不可用" : devices.length === 0 ? "未配置" : deviceCounts.OFFLINE > 0 ? "有设备离线" : deviceCounts.DEGRADED > 0 ? "部分失败" : deviceCounts.ONLINE > 0 ? "通信正常" : "等待首次采集",
    detail: deviceError ? deviceError : devices.length === 0 ? "尚未配置启用的采集设备" : `${devices.length} 个启用设备 · ${deviceCounts.ONLINE} 在线`,
    loading: deviceLoading,
    error: deviceError,
    stale: sourceStale(sources, deviceKeys),
  };

  const domains = [acquisition, device];
  if (mqttVisible) domains.push(buildMqttSummary(sources));
  domains.push(buildServiceSummary(sources));
  return domains;
}

function buildMqttSummary(sources: DashboardSources): DashboardDomainSummary {
  const error = sourceError(sources, ["mqtt", "mqttOutbox"]);
  const loading = sourceLoading(sources, ["mqtt", "mqttOutbox"]);
  const stale = sourceStale(sources, ["mqtt", "mqttOutbox"]);
  const runtime = sources.mqtt.data;
  const outbox = sources.mqttOutbox.data;
  const maxUtilization = outbox ? Math.max(outbox.rowUtilization, outbox.byteUtilization) : 0;
  const hasMqttData = runtime !== null || outbox !== null;
  const status = !hasMqttData && error ? "error" : maxUtilization >= 1 || runtime?.state === "ERROR" ? "error" : error || maxUtilization >= 0.8 || runtime?.state === "CONNECTING" || runtime?.state === "RECONNECTING" ? "warning" : runtime?.state === "DISABLED" ? "neutral" : runtime ? "success" : "neutral";
  return {
    key: "mqtt",
    label: "MQTT 上行",
    status,
    statusLabel: !hasMqttData && error ? "数据不可用" : runtime?.state === "DISABLED" ? "已停用" : maxUtilization >= 1 ? "Outbox 已满" : runtime?.state === "ERROR" ? "连接错误" : runtime?.state === "CONNECTING" || runtime?.state === "RECONNECTING" ? "连接不稳定" : error ? "数据可能已过期" : maxUtilization >= 0.8 ? "Outbox 预警" : runtime ? "运行正常" : "等待数据",
    detail: !hasMqttData && error ? error : outbox ? `Outbox ${Math.round(maxUtilization * 100)}% · Raw pending ${runtime?.pendingLatestCount ?? 0}` : error || "等待 MQTT 运行状态",
    loading,
    error,
    stale,
  };
}

function buildServiceSummary(sources: DashboardSources): DashboardDomainSummary {
  const error = sourceError(sources, ["health", "ready"]);
  const loading = sourceLoading(sources, ["health", "ready"]);
  const stale = sourceStale(sources, ["health", "ready"]);
  return {
    key: "service",
    label: "服务/数据库",
    status: error ? "error" : stale ? "warning" : sources.health.data && sources.ready.data ? "success" : "neutral",
    statusLabel: error ? "不可用" : stale ? "数据可能已过期" : sources.health.data && sources.ready.data ? "服务就绪" : "等待检查",
    detail: error || (sources.health.data && sources.ready.data ? "进程存活，数据库已就绪" : "等待健康检查"),
    loading,
    error,
    stale,
  };
}

function severityRank(severity: DashboardAttentionItem["severity"]) {
  return severity === "critical" ? 0 : severity === "warning" ? 1 : 2;
}

function durationRank(item: DashboardAttentionItem) {
  if (!item.occurredAt) return 0;
  const time = Date.parse(item.occurredAt);
  return Number.isNaN(time) ? 0 : time;
}

export function buildAttentionItems(
  sources: DashboardSources,
  mqttVisible: boolean,
): DashboardAttentionResult {
  const items: DashboardAttentionItem[] = [];
  const channels = getEnabledChannels(sources.channels.data);
  const devices = getEnabledDevices(sources.devices.data);
  const channelStates = sources.channelStates.data;
  const states = sources.states.data;

  if (sources.health.error || sources.ready.error) {
    items.push({ id: "service-unavailable", severity: "critical", title: "服务或数据库不可用", description: sources.ready.error || sources.health.error, category: "服务/数据库" });
  }

  if (sources.channels.error || sources.channelStates.error || sources.states.error || sources.devices.error) {
    items.push({ id: "acquisition-data-unavailable", severity: "warning", title: "采集状态数据暂不可用", description: "无法完整获取通道或设备当前状态；现有数据可能已过期。", category: "采集运行" });
  }

  channels.forEach((channel) => {
    const runtime = getChannelRuntime(channel, channelStates);
    if (runtime.status !== "DEGRADED" && runtime.status !== "OFFLINE") return;
    const affectedDevices = devices.filter((device) => device.channelId === channel.id);
    items.push({
      id: `channel-${channel.id}`,
      severity: runtime.status === "OFFLINE" ? "critical" : "warning",
      title: `通道“${channel.name}”${runtime.status === "OFFLINE" ? "已离线" : "部分失败"}`,
      description: `${affectedDevices.length} 个设备受影响${runtime.lastError ? ` · ${runtime.lastError}` : ""}`,
      occurredAt: runtime.lastAttemptAt,
      href: `/acquisition/realtime?channelId=${channel.id}`,
      category: "采集通道",
    });
  });

  devices.forEach((device) => {
    const channel = channels.find((item) => item.id === device.channelId);
    const channelRuntime = channel ? getChannelRuntime(channel, channelStates) : null;
    if (channelRuntime?.status === "DEGRADED" || channelRuntime?.status === "OFFLINE") return;
    const state = getDeviceState(device, states);
    if (state.status !== "DEGRADED" && state.status !== "OFFLINE") return;
    items.push({
      id: `device-${device.id}`,
      severity: state.status === "OFFLINE" ? "critical" : "warning",
      title: `设备“${device.name}”${state.status === "OFFLINE" ? "已离线" : "部分失败"}`,
      description: state.lastError || `连续失败 ${state.consecutiveFailures} 次`,
      occurredAt: state.lastAttemptAt,
      href: `/acquisition/realtime?channelId=${device.channelId}&deviceId=${device.id}`,
      category: "设备通信",
    });
  });

  (sources.scriptStates.data ?? []).forEach((state) => {
    if (!state.lastError) return;
    items.push({
      id: `script-${state.deviceId}`,
      severity: "warning",
      title: `设备“${state.deviceName || state.deviceId}”协议脚本失败`,
      description: state.lastErrorType ? `${state.lastErrorType} · ${state.lastError}` : state.lastError,
      occurredAt: state.lastAttemptAt,
      href: `/acquisition/script?deviceId=${state.deviceId}`,
      category: "协议脚本",
    });
  });

  if (mqttVisible) {
    const runtime = sources.mqtt.data;
    const outbox = sources.mqttOutbox.data;
    if (sources.mqtt.error || sources.mqttOutbox.error) {
      items.push({ id: "mqtt-data-unavailable", severity: "warning", title: "MQTT 运行数据暂不可用", description: sources.mqtt.error || sources.mqttOutbox.error, category: "MQTT 上行", href: "/mqtt/overview" });
    } else if (runtime && runtime.state === "ERROR") {
      items.push({ id: "mqtt-error", severity: "critical", title: "MQTT 连接错误", description: runtime.lastError || "Broker 连接失败", occurredAt: runtime.lastDisconnectedAt, category: "MQTT 上行", href: "/mqtt/overview" });
    } else if (runtime && (runtime.state === "CONNECTING" || runtime.state === "RECONNECTING")) {
      items.push({ id: "mqtt-reconnecting", severity: "warning", title: "MQTT 正在连接或重连", description: `已重连尝试 ${runtime.reconnectAttempts} 次`, occurredAt: runtime.lastDisconnectedAt, category: "MQTT 上行", href: "/mqtt/overview" });
    }
    if (outbox) {
      const utilization = Math.max(outbox.rowUtilization, outbox.byteUtilization);
      if (outbox.lastError) {
        items.push({ id: "mqtt-outbox-error", severity: "critical", title: "Reliable Outbox 最近投递失败", description: outbox.lastError, category: "MQTT 上行", href: "/mqtt/overview" });
      } else if (utilization >= 1) {
        items.push({ id: "mqtt-outbox-full", severity: "critical", title: "Reliable Outbox 已满", description: `容量利用率 ${Math.round(utilization * 100)}%`, category: "MQTT 上行", href: "/mqtt/overview" });
      } else if (utilization >= 0.8) {
        items.push({ id: "mqtt-outbox-warning", severity: "warning", title: "Reliable Outbox 接近容量上限", description: `容量利用率 ${Math.round(utilization * 100)}%`, category: "MQTT 上行", href: "/mqtt/overview" });
      }
    }
  }

  const sortedItems = items.sort((left, right) => {
    const severity = severityRank(left.severity) - severityRank(right.severity);
    if (severity !== 0) return severity;
    return durationRank(left) - durationRank(right);
  });
  const categoryCounts = sortedItems.reduce<Record<string, number>>((counts, item) => {
    counts[item.category] = (counts[item.category] ?? 0) + 1;
    return counts;
  }, {});
  return {
    items: sortedItems.slice(0, 6),
    total: sortedItems.length,
    categoryCounts,
  };
}
