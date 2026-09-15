import type { ApiPageRequest, ApiPageResult, ApiStatus } from "./api";

export type AcquisitionChannel = {
  id: number;
  name: string;
  port: string;
  baudRate: number;
  dataBits: number;
  stopBits: number;
  parity: "N" | "E" | "O";
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
  channelId: number;
  slaveId: number;
  pollIntervalMs: number;
  failureThreshold: number;
  enabled: ApiStatus;
  registerBlocks: AcquisitionRegisterBlock[];
  createTime?: string | null;
  updateTime?: string | null;
};

export type AcquisitionChannelQuery = Partial<ApiPageRequest>;
export type AcquisitionDeviceQuery = Partial<ApiPageRequest> & {
  channelId?: number;
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
  "id" | "createTime" | "updateTime" | "registerBlocks"
> & { registerBlocks: AcquisitionRegisterBlockInput[] };

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

export type AcquisitionCurrentState = {
  deviceId: number;
  deviceName: string;
  channelId: number;
  slaveId: number;
  registerBlocks: AcquisitionRegisterBlockState[];
  lastAttemptAt?: string | null;
  lastSuccessAt?: string | null;
  status: AcquisitionCommunicationStatus;
  consecutiveFailures: number;
  lastError?: string;
};

export type AcquisitionChannelPage = ApiPageResult<AcquisitionChannel>;
export type AcquisitionDevicePage = ApiPageResult<AcquisitionDevice>;
