import type { ApiPageRequest, ApiPageResult } from "./api";

export type MqttProtocolVersion = "MQTT_5" | "MQTT_3_1_1";

export type MqttSecretAction = "keep" | "set" | "clear";

export type MqttConfig = {
  enabled: boolean;
  edgeId: string;
  brokerUrl: string;
  protocolVersion: MqttProtocolVersion;
  clientId: string;
  username: string;
  passwordConfigured: boolean;
  tlsEnabled: boolean;
  caCertificate: string;
  clientCertificate: string;
  clientPrivateKeyConfigured: boolean;
  keepAliveSeconds: number;
  connectTimeoutMs: number;
  reconnectMinMs: number;
  reconnectMaxMs: number;
  topicPrefix: string;
  rawPublishIntervalMs: number;
  outboxMaxRows: number;
  outboxMaxBytes: number;
  outboxRetentionDays: number;
  commandJournalRetentionDays: number;
  commandJournalMaxRows: number;
  commandQueueCapacity: number;
  commandPollFairness: number;
};

export type MqttConfigUpdateRequest = Omit<
  MqttConfig,
  "passwordConfigured" | "clientPrivateKeyConfigured"
> & {
  passwordAction: MqttSecretAction;
  password?: string;
  clientPrivateKeyAction: MqttSecretAction;
  clientPrivateKey?: string;
};

export type MqttRuntimeStatus =
  | "DISABLED"
  | "CONNECTING"
  | "CONNECTED"
  | "RECONNECTING"
  | "ERROR";

export type MqttStatusTone =
  | "success"
  | "warning"
  | "error"
  | "info"
  | "neutral";

export type MqttStatusMeta = {
  label: string;
  tone: MqttStatusTone;
};

export const mqttRuntimeStatusMeta: Record<
  MqttRuntimeStatus,
  MqttStatusMeta
> = {
  DISABLED: { label: "已停用", tone: "neutral" },
  CONNECTING: { label: "连接中", tone: "warning" },
  CONNECTED: { label: "已连接", tone: "success" },
  RECONNECTING: { label: "重连中", tone: "warning" },
  ERROR: { label: "连接错误", tone: "error" },
};

export type MqttOutboxStats = {
  rows: number;
  bytes: number;
  oldestAge: number;
  lastError?: string | null;
  maxRows: number;
  maxBytes: number;
  rowUtilization: number;
  byteUtilization: number;
};

export type MqttRuntimeState = {
  state: MqttRuntimeStatus;
  connected: boolean;
  lastError?: string | null;
  reconnectAttempts: number;
  nextRetryAt?: string | null;
  lastConnectedAt?: string | null;
  lastDisconnectedAt?: string | null;
  subscriptionFilter: string;
  pendingLatestCount: number;
  outbox: MqttOutboxStats;
};

export type MqttTestConnectionResult = {
  success: boolean;
  runtimeConnected: boolean;
};

export type MqttCommandStatus =
  | "ACCEPTED"
  | "REJECTED"
  | "EXPIRED"
  | "RUNNING"
  | "SUCCEEDED"
  | "FAILED";

export const mqttCommandStatusMeta: Record<
  MqttCommandStatus,
  MqttStatusMeta
> = {
  ACCEPTED: { label: "已接收", tone: "info" },
  REJECTED: { label: "已拒绝", tone: "error" },
  EXPIRED: { label: "已过期", tone: "warning" },
  RUNNING: { label: "执行中", tone: "warning" },
  SUCCEEDED: { label: "执行成功", tone: "success" },
  FAILED: { label: "执行失败", tone: "error" },
};

export type MqttCommandJournal = {
  commandId: string;
  deviceId: string;
  name: string;
  status: MqttCommandStatus;
  receivedAt: string;
  issuedAt: string;
  expiresAt: string;
  startedAt?: string | null;
  completedAt?: string | null;
  errorType?: string | null;
  errorMessage?: string | null;
};

export type MqttCommandListQuery = Partial<ApiPageRequest> & {
  status?: MqttCommandStatus;
  deviceId?: string;
  commandId?: string;
  name?: string;
};

export type MqttCommandPage = ApiPageResult<MqttCommandJournal>;
