import { useCallback, useEffect, useRef, useState } from "react";
import { getAcquisitionChannelStates, getAcquisitionStates } from "@/api/acquisition";
import { getErrorMessage } from "@/lib/api-error";
import { toast } from "@/components/common/toast-store";
import type {
  AcquisitionChannelRuntimeState,
  AcquisitionCurrentState,
} from "@/types";

function normalizeCurrentState(state: AcquisitionCurrentState): AcquisitionCurrentState {
  return {
    ...state,
    registerBlocks: (Array.isArray(state.registerBlocks) ? state.registerBlocks : []).map(
      (block) => ({
        ...block,
        values: Array.isArray(block.values) ? block.values : [],
      }),
    ),
  };
}

export function useRealtimeAcquisition(paused: boolean) {
  const [states, setStates] = useState<AcquisitionCurrentState[]>([]);
  const [channelStates, setChannelStates] = useState<AcquisitionChannelRuntimeState[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [channelStateError, setChannelStateError] = useState("");
  const refreshing = useRef(false);

  const loadStates = useCallback(async () => {
    if (refreshing.current) return;
    refreshing.current = true;
    setLoading(true);
    setError("");
    try {
      const payload = await getAcquisitionStates();
      const result = (Array.isArray(payload) ? payload : []).map(normalizeCurrentState);
      setStates(result);

      try {
        const channelPayload = await getAcquisitionChannelStates();
        setChannelStates(Array.isArray(channelPayload) ? channelPayload : []);
        setChannelStateError("");
      } catch (channelError) {
        setChannelStateError(getErrorMessage(channelError, "通信通道状态暂不可用"));
      }
    } catch (loadError) {
      const message = getErrorMessage(loadError, "无法获取实时寄存器状态");
      setError(message);
      toast.error({
        title: "实时寄存器加载失败",
        description: getErrorMessage(loadError, "请检查 API 服务"),
      });
    } finally {
      setLoading(false);
      refreshing.current = false;
    }
  }, []);

  useEffect(() => {
    void loadStates();
    if (paused) return;

    const timer = window.setInterval(() => void loadStates(), 1500);
    return () => window.clearInterval(timer);
  }, [loadStates, paused]);

  return {
    states,
    channelStates,
    loading,
    error,
    channelStateError,
    loadStates,
  };
}
