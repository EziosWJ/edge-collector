import { acquisitionCommunicationStatusMeta } from "@/types";
import { formatDateTime } from "@/lib/datetime";
import type {
  AcquisitionChannelRuntimeState,
  AcquisitionCurrentState,
  AcquisitionStatusMeta,
} from "@/types";

export type StatusFilter =
  | "ALL"
  | "UNCONFIGURED"
  | AcquisitionCurrentState["status"];

export type ChannelGroup = {
  channelId: number;
  channelName: string;
  protocol?: AcquisitionChannelRuntimeState["protocol"];
  runtimeState?: AcquisitionChannelRuntimeState;
  states: AcquisitionCurrentState[];
};

export function formatOptionalTime(value?: string | null) {
  const formatted = formatDateTime(value);
  return formatted === "-" ? "暂无" : formatted;
}

export function getStateStatusMeta(state: AcquisitionCurrentState): AcquisitionStatusMeta {
  if ((state.registerBlocks ?? []).length === 0) {
    return { label: "未配置读取块", tone: "warning" };
  }
  return acquisitionCommunicationStatusMeta[state.status];
}

export function groupRealtimeStates(
  states: AcquisitionCurrentState[],
  channelStates: AcquisitionChannelRuntimeState[],
): ChannelGroup[] {
  const groups = new Map<number, ChannelGroup>();

  for (const channel of channelStates) {
    groups.set(channel.channelId, {
      channelId: channel.channelId,
      channelName: channel.channelName || `通道 ${channel.channelId}`,
      protocol: channel.protocol,
      runtimeState: channel,
      states: [],
    });
  }

  for (const state of states) {
    const current = groups.get(state.channelId);
    if (current) {
      current.states.push(state);
      continue;
    }
    groups.set(state.channelId, {
      channelId: state.channelId,
      channelName: `通道 ${state.channelId}`,
      states: [state],
    });
  }

  return [...groups.values()].sort((left, right) => {
    const leftName = left.channelName || `通道 ${left.channelId}`;
    const rightName = right.channelName || `通道 ${right.channelId}`;
    return leftName.localeCompare(rightName, "zh-CN") || left.channelId - right.channelId;
  });
}
