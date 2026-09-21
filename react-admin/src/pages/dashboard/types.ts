import type {
  AcquisitionChannel,
  AcquisitionChannelRuntimeState,
  AcquisitionCurrentState,
  AcquisitionDevice,
  AcquisitionScriptRuntimeState,
} from "@/types";
import type { MqttOutboxStats, MqttRuntimeState } from "@/types/mqtt";

export type DashboardSource<T> = {
  data: T | null;
  loading: boolean;
  error: string;
  stale: boolean;
  lastSuccessAt: number | null;
};

export type DashboardSources = {
  channels: DashboardSource<AcquisitionChannel[]>;
  devices: DashboardSource<AcquisitionDevice[]>;
  channelStates: DashboardSource<AcquisitionChannelRuntimeState[]>;
  states: DashboardSource<AcquisitionCurrentState[]>;
  scriptStates: DashboardSource<AcquisitionScriptRuntimeState[]>;
  mqtt: DashboardSource<MqttRuntimeState>;
  mqttOutbox: DashboardSource<MqttOutboxStats>;
  health: DashboardSource<{ status: string }>;
  ready: DashboardSource<{ status: string }>;
};

export type DashboardDomainStatus = "success" | "warning" | "error" | "neutral";

export type DashboardDomainSummary = {
  key: "acquisition" | "device" | "mqtt" | "service";
  label: string;
  status: DashboardDomainStatus;
  statusLabel: string;
  detail: string;
  loading: boolean;
  error: string;
  stale: boolean;
};

export type DashboardAttentionSeverity = "critical" | "warning" | "info";

export type DashboardAttentionItem = {
  id: string;
  severity: DashboardAttentionSeverity;
  title: string;
  description: string;
  occurredAt?: string | null;
  href?: string;
  category: string;
};

export type DashboardAttentionResult = {
  items: DashboardAttentionItem[];
  total: number;
  categoryCounts: Record<string, number>;
};
