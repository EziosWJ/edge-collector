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
export type AcquisitionDeviceInput = Omit<
  AcquisitionDevice,
  "id" | "createTime" | "updateTime"
>;

export type FeedProtectorData = {
  voltage: number;
  current: number;
  activePower: number;
  frequency: number;
  powerFactor: number;
  status: number;
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
  data: FeedProtectorData;
  fieldValidity: Record<string, boolean>;
  fieldUpdatedAt: Record<string, string>;
  lastAttemptAt?: string | null;
  lastSuccessAt?: string | null;
  status: AcquisitionCommunicationStatus;
  consecutiveFailures: number;
  lastError?: string;
};

export type AcquisitionChannelPage = ApiPageResult<AcquisitionChannel>;
export type AcquisitionDevicePage = ApiPageResult<AcquisitionDevice>;
