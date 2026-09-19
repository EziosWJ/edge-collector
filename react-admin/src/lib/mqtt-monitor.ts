import type { QoS } from "mqtt-packet";

export const MQTT_MONITOR_STORAGE_KEY = "mqtt-monitor-settings";
export const MQTT_MONITOR_MAX_MESSAGES = 500;
export const MQTT_MONITOR_MAX_PAYLOAD_BYTES = 1024 * 1024;
export const MQTT_MONITOR_MAX_BUFFER_BYTES = 10 * 1024 * 1024;

export type MqttMonitorProtocol = "MQTT_5" | "MQTT_3_1_1";

export type MqttMonitorMessageType =
  | "edge-status"
  | "device-status"
  | "raw"
  | "event"
  | "command-result"
  | "unknown";

export const MQTT_MONITOR_MESSAGE_TYPES: Array<{
  value: Exclude<MqttMonitorMessageType, "unknown">;
  label: string;
  leaf: string;
}> = [
  { value: "edge-status", label: "Edge 状态", leaf: "status" },
  { value: "device-status", label: "设备状态", leaf: "status" },
  { value: "raw", label: "Raw Snapshot", leaf: "raw" },
  { value: "event", label: "设备事件", leaf: "event" },
  { value: "command-result", label: "Command Result", leaf: "command-result" },
];

export type MqttMonitorSettings = {
  wsUrl: string;
  topicPrefix: string;
  edgeId: string;
  protocol: MqttMonitorProtocol;
  username: string;
  password: string;
  selectedTypes: Array<Exclude<MqttMonitorMessageType, "unknown">>;
};

export type MqttMonitorStoredSettings = Omit<MqttMonitorSettings, "password">;

export type MqttMonitorPacket = {
  qos?: QoS;
  retain?: boolean;
  dup?: boolean;
};

export type MqttMonitorClientEventMap = {
  connect: () => void;
  reconnect: () => void;
  offline: () => void;
  close: () => void;
  end: () => void;
  error: (error: Error) => void;
  message: (topic: string, payload: Uint8Array, packet: MqttMonitorPacket) => void;
};

export type MqttMonitorClient = {
  on: <K extends keyof MqttMonitorClientEventMap>(
    event: K,
    listener: MqttMonitorClientEventMap[K],
  ) => void;
  off: <K extends keyof MqttMonitorClientEventMap>(
    event: K,
    listener: MqttMonitorClientEventMap[K],
  ) => void;
  subscribe: (topic: string, callback: (error?: Error | null) => void) => void;
  unsubscribe: (topic: string) => void;
  end: (force?: boolean) => void;
};

export type MqttMonitorClientFactory = (
  settings: MqttMonitorSettings & { clientId: string },
) => MqttMonitorClient;

export type MqttMonitorDiagnostic =
  | "ok"
  | "json_parse_error"
  | "unknown_schema"
  | "missing_envelope"
  | "identity_mismatch"
  | "topic_out_of_scope"
  | "invalid_timestamp"
  | "oversized";

export type MqttMonitorMessage = {
  id: string;
  receivedAt: string;
  payloadTimestamp?: string;
  topic: string;
  type: MqttMonitorMessageType;
  label: string;
  schema?: string;
  edgeId?: string;
  deviceId?: string;
  qos: QoS;
  retain: boolean;
  dup: boolean;
  byteLength: number;
  rawPayload: string;
  parsedPayload?: unknown;
  diagnostic: MqttMonitorDiagnostic;
};

export type MqttMonitorBufferStats = {
  received: number;
  evicted: number;
  skipped: number;
  oversized: number;
  bytes: number;
};

export type MqttMonitorBuffer = {
  messages: MqttMonitorMessage[];
  stats: MqttMonitorBufferStats;
};

const schemaTypeMap: Record<string, MqttMonitorMessageType> = {
  "edge-status/v1": "edge-status",
  "device-status/v1": "device-status",
  "raw-register-snapshot/v1": "raw",
  "device-event/v1": "event",
  "device-command-result/v1": "command-result",
};

const typeLabels: Record<MqttMonitorMessageType, string> = {
  "edge-status": "Edge 状态",
  "device-status": "设备状态",
  raw: "Raw Snapshot",
  event: "设备事件",
  "command-result": "Command Result",
  unknown: "未知消息",
};

function normalizePrefix(prefix: string) {
  return prefix.trim().replace(/^\/+|\/+$/g, "");
}

export function isValidMonitorTopicSegment(value: string) {
  if (!value || value.includes("/") || value.includes("+") || value.includes("#")) return false;
  return [...value].every((character) => {
    const code = character.charCodeAt(0);
    return code >= 0x20 && code !== 0x7f;
  });
}

export function isValidMonitorTopicPrefix(value: string) {
  const normalized = normalizePrefix(value);
  return Boolean(normalized) && normalized.split("/").every(isValidMonitorTopicSegment);
}

function topicBase(prefix: string, edgeId: string) {
  return `${normalizePrefix(prefix)}/${edgeId.trim()}`;
}

export function buildMonitorSubscriptions(
  settings: Pick<MqttMonitorSettings, "topicPrefix" | "edgeId" | "selectedTypes">,
) {
  const base = topicBase(settings.topicPrefix, settings.edgeId);
  return MQTT_MONITOR_MESSAGE_TYPES.filter((item) =>
    settings.selectedTypes.includes(item.value),
  ).map((item) => ({
    type: item.value,
    topic:
      item.value === "edge-status"
        ? `${base}/status`
        : `${base}/device/+/${item.leaf}`,
    qos: 1 as const,
  }));
}

function inferTopic(topic: string, prefix: string, edgeId: string) {
  const base = topicBase(prefix, edgeId);
  if (topic === `${base}/status`) {
    return { type: "edge-status" as const, topicEdgeId: edgeId };
  }

  const match = topic.match(
    new RegExp(`^${escapeRegExp(base)}/device/([^/]+)/([^/]+)$`),
  );
  if (!match) return null;

  const [, topicDeviceId, leaf] = match;
  const type = MQTT_MONITOR_MESSAGE_TYPES.find((item) => item.leaf === leaf && item.value !== "edge-status")?.value;
  return type ? { type, topicEdgeId: edgeId, topicDeviceId } : null;
}

function escapeRegExp(value: string) {
  return value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

function asRecord(value: unknown): Record<string, unknown> | null {
  return value !== null && typeof value === "object" && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : null;
}

function safeDecode(payload: Uint8Array) {
  try {
    return new TextDecoder().decode(payload);
  } catch {
    return "[Payload 无法解码]";
  }
}

export function parseMonitorMessage({
  topic,
  payload,
  packet,
  settings,
  receivedAt = new Date().toISOString(),
  id = `${receivedAt}-${Math.random().toString(36).slice(2)}`,
}: {
  topic: string;
  payload: Uint8Array;
  packet?: MqttMonitorPacket;
  settings: Pick<MqttMonitorSettings, "topicPrefix" | "edgeId">;
  receivedAt?: string;
  id?: string;
}): MqttMonitorMessage {
  const byteLength = payload.byteLength;
  const topicContext = inferTopic(topic, settings.topicPrefix, settings.edgeId);
  const base = {
    id,
    receivedAt,
    topic,
    type: topicContext?.type ?? ("unknown" as const),
    label: typeLabels[topicContext?.type ?? "unknown"],
    qos: packet?.qos ?? 0,
    retain: Boolean(packet?.retain),
    dup: Boolean(packet?.dup),
    byteLength,
  };

  if (byteLength > MQTT_MONITOR_MAX_PAYLOAD_BYTES) {
    return {
      ...base,
      rawPayload: `[Payload 已省略：${byteLength} bytes]`,
      diagnostic: "oversized",
    };
  }

  const rawPayload = safeDecode(payload);
  let parsedPayload: unknown;
  try {
    parsedPayload = JSON.parse(rawPayload) as unknown;
  } catch {
    return { ...base, rawPayload, diagnostic: "json_parse_error" };
  }

  const envelope = asRecord(parsedPayload);
  if (!envelope) {
    return { ...base, rawPayload, parsedPayload, diagnostic: "missing_envelope" };
  }

  const schema = typeof envelope.schema === "string" ? envelope.schema : undefined;
  const payloadType = schema ? schemaTypeMap[schema] : undefined;
  const edgeId = typeof envelope.edgeId === "string" ? envelope.edgeId : undefined;
  const deviceId = typeof envelope.deviceId === "string" ? envelope.deviceId : undefined;
  const timestamp = typeof envelope.timestamp === "string" ? envelope.timestamp : undefined;
  const timestampValid = timestamp ? !Number.isNaN(new Date(timestamp).getTime()) : false;
  const isDeviceMessage = topicContext?.type && topicContext.type !== "edge-status";

  let diagnostic: MqttMonitorDiagnostic = "ok";
  if (!topicContext) diagnostic = "topic_out_of_scope";
  else if (!schema || !envelope.messageId || !edgeId || (isDeviceMessage && !deviceId)) diagnostic = "missing_envelope";
  else if (!payloadType) diagnostic = "unknown_schema";
  else if (payloadType !== topicContext.type || edgeId !== topicContext.topicEdgeId || (isDeviceMessage && deviceId !== topicContext.topicDeviceId)) diagnostic = "identity_mismatch";
  else if (timestamp && !timestampValid) diagnostic = "invalid_timestamp";

  return {
    ...base,
    type: payloadType ?? topicContext?.type ?? "unknown",
    label: typeLabels[payloadType ?? topicContext?.type ?? "unknown"],
    schema,
    edgeId,
    deviceId,
    payloadTimestamp: timestamp,
    rawPayload,
    parsedPayload,
    diagnostic,
  };
}

export function createEmptyMonitorBuffer(): MqttMonitorBuffer {
  return {
    messages: [],
    stats: { received: 0, evicted: 0, skipped: 0, oversized: 0, bytes: 0 },
  };
}

export function appendMonitorMessage(
  buffer: MqttMonitorBuffer,
  message: MqttMonitorMessage,
  options: { paused?: boolean } = {},
): MqttMonitorBuffer {
  const received = buffer.stats.received + 1;
  if (options.paused) {
    return {
      messages: buffer.messages,
      stats: { ...buffer.stats, received, skipped: buffer.stats.skipped + 1 },
    };
  }

  if (message.diagnostic === "oversized") {
    return {
      messages: buffer.messages,
      stats: { ...buffer.stats, received, oversized: buffer.stats.oversized + 1 },
    };
  }

  const messages = [message, ...buffer.messages];
  let bytes = buffer.stats.bytes + message.byteLength;
  let evicted = buffer.stats.evicted;
  while (messages.length > MQTT_MONITOR_MAX_MESSAGES || bytes > MQTT_MONITOR_MAX_BUFFER_BYTES) {
    const removed = messages.pop();
    if (!removed) break;
    bytes -= removed.byteLength;
    evicted += 1;
  }

  return {
    messages,
    stats: { ...buffer.stats, received, bytes, evicted },
  };
}

export function getStoredMonitorSettings(defaults: MqttMonitorStoredSettings): MqttMonitorStoredSettings {
  if (typeof window === "undefined") return defaults;
  try {
    const raw = window.sessionStorage.getItem(MQTT_MONITOR_STORAGE_KEY);
    if (!raw) return defaults;
    const parsed = JSON.parse(raw) as Partial<MqttMonitorStoredSettings>;
    const stored = Object.fromEntries(
      Object.entries(parsed).filter(([key]) => key !== "password"),
    ) as Partial<MqttMonitorStoredSettings>;
    return {
      ...defaults,
      ...stored,
    };
  } catch {
    return defaults;
  }
}

export function storeMonitorSettings(settings: MqttMonitorStoredSettings) {
  if (typeof window === "undefined") return;
  try {
    window.sessionStorage.setItem(MQTT_MONITOR_STORAGE_KEY, JSON.stringify(settings));
  } catch {
    // Storage may be disabled; monitoring remains usable without persistence.
  }
}

export function formatMonitorBytes(bytes: number) {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KiB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MiB`;
}

export function isMonitorTopicSelected(
  settings: Pick<MqttMonitorSettings, "selectedTypes">,
  type: MqttMonitorMessageType,
) {
  return settings.selectedTypes.includes(type as Exclude<MqttMonitorMessageType, "unknown">);
}
