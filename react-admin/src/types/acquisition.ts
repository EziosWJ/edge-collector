import type { ApiPageRequest, ApiPageResult, ApiStatus } from "./api";

export type AcquisitionProtocol =
  | "MODBUS_RTU"
  | "MODBUS_TCP"
  | "MODBUS_UDP"
  | "MODBUS_RTU_OVER_UDP";

export type AcquisitionSerialConfig = {
  port: string;
  baudRate: number;
  dataBits: number;
  stopBits: number;
  parity: "N" | "E" | "O";
};

export type AcquisitionNetworkEndpoint = {
  host: string;
  port: number;
};

export type AcquisitionChannel = {
  id: number;
  name: string;
  protocol: AcquisitionProtocol;
  serialConfig?: AcquisitionSerialConfig | null;
  timeoutMs: number;
  interRequestDelayMs: number;
  enabled: ApiStatus;
  createTime?: string | null;
  updateTime?: string | null;
};

export type AcquisitionDevice = {
  id: number;
  name: string;
  deviceType: "FEED_PROTECTOR";
  scriptId?: number | null;
  channelId: number;
  unitId: number;
  networkEndpoint?: AcquisitionNetworkEndpoint | null;
  pollIntervalMs: number;
  failureThreshold: number;
  enabled: ApiStatus;
  registerBlocks: AcquisitionRegisterBlock[];
  createTime?: string | null;
  updateTime?: string | null;
};

export type AcquisitionChannelQuery = Partial<ApiPageRequest> & {
  name?: string;
  protocol?: AcquisitionProtocol;
  enabled?: ApiStatus;
};
export type AcquisitionDeviceQuery = Partial<ApiPageRequest> & {
  name?: string;
  channelId?: number;
  enabled?: ApiStatus;
};

export type AcquisitionChannelInput = Omit<
  AcquisitionChannel,
  "id" | "createTime" | "updateTime"
>;
export type AcquisitionRegisterBlock = {
  id: number;
  name: string;
  functionCode: 3 | 4;
  startAddress: number;
  quantity: number;
  sortOrder: number;
  createTime?: string | null;
  updateTime?: string | null;
};

export type AcquisitionRegisterBlockInput = Omit<
  AcquisitionRegisterBlock,
  "id" | "createTime" | "updateTime"
> & { id?: number };

export type AcquisitionDeviceInput = Omit<
  AcquisitionDevice,
  "id" | "createTime" | "updateTime" | "registerBlocks" | "scriptId"
> & {
  scriptId?: number | null;
  registerBlocks: AcquisitionRegisterBlockInput[];
};

export type AcquisitionRegisterBlockState = {
  id: number;
  name: string;
  functionCode: 3 | 4;
  startAddress: number;
  quantity: number;
  sortOrder: number;
  values: Array<number | null>;
  valid: boolean;
  lastAttemptAt?: string | null;
  lastSuccessAt?: string | null;
  lastError?: string;
};

export type AcquisitionCommunicationStatus =
  | "INITIAL"
  | "ONLINE"
  | "DEGRADED"
  | "OFFLINE";

export type AcquisitionStatusTone = "success" | "warning" | "error" | "neutral";
export type AcquisitionStatusMeta = { label: string; tone: AcquisitionStatusTone };

export const acquisitionCommunicationStatusMeta: Record<AcquisitionCommunicationStatus, AcquisitionStatusMeta> = {
  INITIAL: { label: "等待首次采集", tone: "neutral" },
  ONLINE: { label: "在线", tone: "success" },
  DEGRADED: { label: "部分失败", tone: "warning" },
  OFFLINE: { label: "离线", tone: "error" },
};

export type AcquisitionChannelRuntimeStatus =
  | "IDLE"
  | "STARTING"
  | "ONLINE"
  | "DEGRADED"
  | "OFFLINE";

export type AcquisitionChannelRuntimeState = {
  channelId: number;
  channelName: string;
  protocol: AcquisitionProtocol;
  status: AcquisitionChannelRuntimeStatus;
  lastAttemptAt?: string | null;
  lastSuccessAt?: string | null;
  lastError?: string;
};

export const acquisitionChannelRuntimeStatusMeta: Record<AcquisitionChannelRuntimeStatus, AcquisitionStatusMeta> = {
  IDLE: { label: "空闲", tone: "neutral" },
  STARTING: { label: "启动中", tone: "neutral" },
  ONLINE: { label: "在线", tone: "success" },
  DEGRADED: { label: "部分失败", tone: "warning" },
  OFFLINE: { label: "离线", tone: "error" },
};

export type AcquisitionCurrentState = {
  deviceId: number;
  deviceName: string;
  channelId: number;
  unitId: number;
  networkEndpoint?: AcquisitionNetworkEndpoint | null;
  registerBlocks: AcquisitionRegisterBlockState[];
  lastAttemptAt?: string | null;
  lastSuccessAt?: string | null;
  status: AcquisitionCommunicationStatus;
  consecutiveFailures: number;
  lastError?: string;
};

export type AcquisitionChannelPage = ApiPageResult<AcquisitionChannel>;
export type AcquisitionDevicePage = ApiPageResult<AcquisitionDevice>;

export type AcquisitionScriptJsonPrimitive = string | number | boolean | null;
export type AcquisitionScriptJsonValue =
  | AcquisitionScriptJsonPrimitive
  | AcquisitionScriptJsonValue[]
  | { [key: string]: AcquisitionScriptJsonValue };

export type AcquisitionScriptErrorType =
  | "SCRIPT_COMPILE"
  | "SCRIPT_RUNTIME"
  | "SCRIPT_LIMIT"
  | "MODBUS_TRANSPORT"
  | "MODBUS_EXCEPTION"
  | "SCRIPT_OUTPUT"
  | "CANCELED"
  | "HOST_UNAVAILABLE";

export type AcquisitionScriptVersion = {
  id: number;
  scriptId: number;
  versionNo: number;
  source: string;
  checksum: string;
  publishedBy: number;
  publishedAt: string;
};

export type AcquisitionScript = {
  id: number;
  name: string;
  description: string;
  draftSource: string;
  publishedVersionId?: number | null;
  publishedVersion?: AcquisitionScriptVersion | null;
  draftMatchesPublished: boolean;
  boundDeviceCount: number;
  createTime?: string | null;
  updateTime?: string | null;
};

export type AcquisitionScriptInput = Pick<
  AcquisitionScript,
  "name" | "description" | "draftSource"
>;

export type AcquisitionScriptQuery = Partial<ApiPageRequest> & {
  name?: string;
  published?: boolean;
  bound?: boolean;
};

export type AcquisitionScriptPage = ApiPageResult<AcquisitionScript>;

export type AcquisitionScriptValidationError = {
  filename?: string;
  line: number;
  column: number;
  message: string;
};

export type AcquisitionScriptValidationResult = {
  valid: boolean;
  errors: AcquisitionScriptValidationError[];
};

export type AcquisitionScriptRollbackInput = {
  versionId: number;
};

export type AcquisitionScriptRuntimeEvent = {
  kind: string;
  key: string;
  payload: AcquisitionScriptJsonValue;
  occurredAt: string;
};

export type AcquisitionScriptRuntimeState = {
  deviceId: number;
  deviceName?: string;
  scriptId: number;
  scriptVersionId: number;
  versionNo: number;
  lastAttemptAt?: string | null;
  lastSuccessAt?: string | null;
  lastError?: string | null;
  lastErrorType?: AcquisitionScriptErrorType | null;
  state: Record<string, AcquisitionScriptJsonValue>;
  events: AcquisitionScriptRuntimeEvent[];
};
