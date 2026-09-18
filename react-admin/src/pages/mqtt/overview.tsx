import { useCallback, useEffect, useState } from "react";
import { Activity, Settings2 } from "lucide-react";
import { Link } from "react-router-dom";
import { getMqttOutboxStats, getMqttState } from "@/api/mqtt";
import { PermissionGuard } from "@/components/auth/permission-guard";
import { EmptyState } from "@/components/common/empty-state";
import { getMqttErrorMessage } from "@/lib/mqtt";
import type { MqttOutboxStats, MqttRuntimeState } from "@/types";
import {
  getRuntimeStatusMeta,
  MqttPageLayout,
  RuntimeOverview,
} from "./shared";

export function MqttOverviewPage() {
  return (
    <PermissionGuard
      permissionCode="mqtt:overview:list"
      fallback={<EmptyState title="无权查看运行总览" description="当前账号没有 MQTT 运行总览权限。" />}
    >
      <MqttOverviewContent />
    </PermissionGuard>
  );
}

function MqttOverviewContent() {
  const [runtime, setRuntime] = useState<MqttRuntimeState | null>(null);
  const [runtimeLoading, setRuntimeLoading] = useState(true);
  const [runtimeError, setRuntimeError] = useState("");
  const [outbox, setOutbox] = useState<MqttOutboxStats | null>(null);
  const [outboxLoading, setOutboxLoading] = useState(true);
  const [outboxError, setOutboxError] = useState("");
  const [lastSuccessfulRefreshAt, setLastSuccessfulRefreshAt] = useState<number | null>(null);

  const loadRuntime = useCallback(async () => {
    setRuntimeLoading(true);
    setRuntimeError("");
    try {
      setRuntime(await getMqttState());
      setLastSuccessfulRefreshAt(Date.now());
    } catch (error) {
      setRuntimeError(getMqttErrorMessage(error, "无法获取 MQTT 运行状态"));
    } finally {
      setRuntimeLoading(false);
    }
  }, []);

  const loadOutbox = useCallback(async () => {
    setOutboxLoading(true);
    setOutboxError("");
    try {
      setOutbox(await getMqttOutboxStats());
      setLastSuccessfulRefreshAt(Date.now());
    } catch (error) {
      setOutboxError(getMqttErrorMessage(error, "无法获取可靠 Outbox 状态"));
    } finally {
      setOutboxLoading(false);
    }
  }, []);

  const refresh = useCallback(() => {
    void Promise.all([loadRuntime(), loadOutbox()]);
  }, [loadOutbox, loadRuntime]);

  useEffect(() => {
    refresh();
    const interval = window.setInterval(() => {
      if (document.visibilityState === "visible") refresh();
    }, 10000);
    const handleVisibility = () => {
      if (document.visibilityState === "visible") refresh();
    };
    document.addEventListener("visibilitychange", handleVisibility);
    return () => {
      window.clearInterval(interval);
      document.removeEventListener("visibilitychange", handleVisibility);
    };
  }, [refresh]);

  return (
    <MqttPageLayout
      title="运行总览"
      description="快速判断 MQTT 连接、可靠消息容量与 Raw pending 健康度。"
      actions={
        <div className="flex flex-wrap gap-space-2">
          <Link to="/mqtt/config#broker" className="inline-flex h-control-md items-center gap-space-2 rounded-control border border-border bg-surface px-space-4 text-sm font-medium text-text-primary hover:bg-neutral-background">
            <Settings2 className="h-4 w-4" aria-hidden />连接配置
          </Link>
          <Link to="/mqtt/commands" className="inline-flex h-control-md items-center gap-space-2 rounded-control border border-border bg-surface px-space-4 text-sm font-medium text-text-primary hover:bg-neutral-background">
            <Activity className="h-4 w-4" aria-hidden />Command Journal
          </Link>
        </div>
      }
    >
      <RuntimeOverview
        runtime={runtime}
        runtimeStatus={getRuntimeStatusMeta(runtime?.state)}
        runtimeLoading={runtimeLoading}
        runtimeError={runtimeError}
        outbox={outbox}
        outboxLoading={outboxLoading}
        outboxError={outboxError}
        rawPendingCount={runtime?.pendingLatestCount ?? 0}
        lastSuccessfulRefreshAt={lastSuccessfulRefreshAt}
        onRefresh={refresh}
        onRefreshOutbox={loadOutbox}
      />
    </MqttPageLayout>
  );
}
