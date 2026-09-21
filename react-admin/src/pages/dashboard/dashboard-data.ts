import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  getAcquisitionChannelStates,
  getAcquisitionChannels,
  getAcquisitionDevices,
  getAcquisitionScriptStates,
  getAcquisitionStates,
} from "@/api/acquisition";
import { getMqttOutboxStats, getMqttState } from "@/api/mqtt";
import { getErrorMessage } from "@/lib/api-error";
import { hasPermission } from "@/lib/permission";
import { http } from "@/lib/http";
import type {
  AcquisitionChannel,
  AcquisitionChannelRuntimeState,
  AcquisitionCurrentState,
  AcquisitionDevice,
  AcquisitionScriptRuntimeState,
} from "@/types";
import type { MqttOutboxStats, MqttRuntimeState } from "@/types/mqtt";
import type { DashboardSource, DashboardSources } from "./types";

const MQTT_PERMISSION = "mqtt:overview:list";

function source<T>(): DashboardSource<T> {
  return { data: null, loading: true, error: "", stale: false, lastSuccessAt: null };
}

function initialSources(mqttVisible: boolean): DashboardSources {
  return {
    channels: source<AcquisitionChannel[]>(),
    devices: source<AcquisitionDevice[]>(),
    channelStates: source<AcquisitionChannelRuntimeState[]>(),
    states: source<AcquisitionCurrentState[]>(),
    scriptStates: source<AcquisitionScriptRuntimeState[]>(),
    mqtt: { ...source<MqttRuntimeState>(), loading: mqttVisible },
    mqttOutbox: { ...source<MqttOutboxStats>(), loading: mqttVisible },
    health: source<{ status: string }>(),
    ready: source<{ status: string }>(),
  };
}

export function useDashboardData() {
  const mqttVisible = hasPermission(MQTT_PERMISSION);
  const [sources, setSources] = useState<DashboardSources>(() => initialSources(mqttVisible));
  const refreshing = useRef(false);

  const loadSource = useCallback(
    async <K extends keyof DashboardSources>(
      key: K,
      fetcher: () => Promise<NonNullable<DashboardSources[K]["data"]>>,
    ) => {
      setSources((current) => ({
        ...current,
        [key]: { ...current[key], loading: true, error: "" },
      }));

      try {
        const data = await fetcher();
        setSources((current) => ({
          ...current,
          [key]: {
            data,
            loading: false,
            error: "",
            stale: false,
            lastSuccessAt: Date.now(),
          },
        }));
      } catch (error) {
        setSources((current) => ({
          ...current,
          [key]: {
            ...current[key],
            loading: false,
            error: getErrorMessage(error, "暂时无法获取数据"),
            stale: current[key].data !== null,
          },
        }));
      }
    },
    [],
  );

  const refresh = useCallback(async () => {
    if (refreshing.current) return;
    refreshing.current = true;
    const requests: Promise<void>[] = [
      loadSource("channels", async () => {
        const result = await getAcquisitionChannels({ page: 1, pageSize: 500 });
        return Array.isArray(result.records) ? result.records : [];
      }),
      loadSource("devices", async () => {
        const result = await getAcquisitionDevices({ page: 1, pageSize: 500 });
        return Array.isArray(result.records) ? result.records : [];
      }),
      loadSource("channelStates", async () => {
        const result = await getAcquisitionChannelStates();
        return Array.isArray(result) ? result : [];
      }),
      loadSource("states", async () => {
        const result = await getAcquisitionStates();
        return Array.isArray(result) ? result : [];
      }),
      loadSource("scriptStates", async () => {
        const result = await getAcquisitionScriptStates();
        return Array.isArray(result) ? result : [];
      }),
      loadSource("health", () => http.get<{ status: string }>("/health")),
      loadSource("ready", () => http.get<{ status: string }>("/ready")),
    ];

    if (mqttVisible) {
      requests.push(
        loadSource("mqtt", getMqttState),
        loadSource("mqttOutbox", getMqttOutboxStats),
      );
    }

    await Promise.all(requests);
    refreshing.current = false;
  }, [loadSource, mqttVisible]);

  useEffect(() => {
    void refresh();
    const timer = window.setInterval(() => {
      if (document.visibilityState === "visible") void refresh();
    }, 10000);
    const onVisibilityChange = () => {
      if (document.visibilityState === "visible") void refresh();
    };
    document.addEventListener("visibilitychange", onVisibilityChange);
    return () => {
      window.clearInterval(timer);
      document.removeEventListener("visibilitychange", onVisibilityChange);
    };
  }, [refresh]);

  const lastSuccessfulRefreshAt = useMemo(() => {
    return Object.values(sources).reduce<number | null>((latest, item) => {
      if (!item.lastSuccessAt) return latest;
      return latest === null ? item.lastSuccessAt : Math.max(latest, item.lastSuccessAt);
    }, null);
  }, [sources]);

  return { sources, mqttVisible, refresh, lastSuccessfulRefreshAt };
}
