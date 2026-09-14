import { RefreshCw, Radio, ShieldAlert } from "lucide-react";
import { useCallback, useEffect, useMemo, useState } from "react";
import type { ReactNode } from "react";
import { getAcquisitionStates } from "@/api/acquisition";
import { ContentCard } from "@/components/common/content-card";
import { DataTable } from "@/components/common/data-table";
import { DataTableCard } from "@/components/common/data-table-card";
import { EmptyState } from "@/components/common/empty-state";
import { PageHeader } from "@/components/common/page-header";
import { StatusTag } from "@/components/common/status-tag";
import { toast } from "@/components/common/toast-store";
import { Button } from "@/components/ui/button";
import { getErrorMessage } from "@/lib/api-error";
import { formatDateTime } from "@/lib/datetime";
import type {
  AcquisitionCommunicationStatus,
  AcquisitionCurrentState,
  DataTableColumn,
} from "@/types";

const statusMeta: Record<
  AcquisitionCommunicationStatus,
  { label: string; tone: "success" | "warning" | "error" | "neutral" }
> = {
  INITIAL: { label: "等待首次采集", tone: "neutral" },
  ONLINE: { label: "在线", tone: "success" },
  DEGRADED: { label: "近期失败", tone: "warning" },
  OFFLINE: { label: "离线", tone: "error" },
};

const fieldLabels: Record<string, string> = {
  voltage: "电压",
  current: "电流",
  activePower: "有功功率",
  frequency: "频率",
  powerFactor: "功率因数",
  status: "设备状态码",
};

export function AcquisitionRealtimePage() {
  const [states, setStates] = useState<AcquisitionCurrentState[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [selectedID, setSelectedID] = useState<number | null>(null);

  const loadStates = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      const result = await getAcquisitionStates();
      setStates(result);
      setSelectedID((current) => current ?? result[0]?.deviceId ?? null);
    } catch (loadError) {
      setStates([]);
      setError(getErrorMessage(loadError, "无法获取实时采集状态"));
      toast.error({ title: "实时数据加载失败", description: getErrorMessage(loadError, "请检查 API 服务") });
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void loadStates();
    const timer = window.setInterval(() => void loadStates(), 1500);
    return () => window.clearInterval(timer);
  }, [loadStates]);

  const selected = useMemo(
    () => states.find((state) => state.deviceId === selectedID) ?? null,
    [selectedID, states],
  );
  const onlineCount = states.filter((state) => state.status === "ONLINE").length;
  const offlineCount = states.filter((state) => state.status === "OFFLINE").length;
  const columns: DataTableColumn<AcquisitionCurrentState>[] = [
    {
      title: "设备",
      key: "device",
      width: 240,
      render: (_, state) => (
        <div>
          <div className="font-medium text-text-primary">{state.deviceName}</div>
          <div className="text-xs text-text-tertiary">通道 {state.channelId} · Slave #{state.slaveId}</div>
        </div>
      ),
    },
    {
      title: "通信状态",
      dataIndex: "status",
      width: 150,
      render: (value) => {
        const meta = statusMeta[value as AcquisitionCommunicationStatus];
        return <StatusTag tone={meta.tone}>{meta.label}</StatusTag>;
      },
    },
    { title: "最近尝试", dataIndex: "lastAttemptAt", width: 190, render: (value) => formatDateTime(value as string) },
    { title: "最近成功", dataIndex: "lastSuccessAt", width: 190, render: (value) => formatDateTime(value as string) },
    {
      title: "失败次数",
      dataIndex: "consecutiveFailures",
      width: 100,
      render: (value) => <span className="tabular-nums">{String(value ?? 0)}</span>,
    },
  ];

  return (
    <>
      <PageHeader
        title="实时数据"
        description="页面每 1.5 秒轮询进程内当前状态，不读取实时历史数据。"
        actions={
          <Button variant="secondary" onClick={() => void loadStates()} disabled={loading}>
            <RefreshCw className="h-4 w-4" aria-hidden />
            立即刷新
          </Button>
        }
      />
      <div className="mb-space-6 grid gap-space-4 md:grid-cols-3">
        <SummaryCard icon={<Radio className="h-5 w-5" aria-hidden />} label="已配置设备" value={states.length} />
        <SummaryCard icon={<span className="block h-3 w-3 rounded-full bg-success" />} label="当前在线" value={onlineCount} tone="success" />
        <SummaryCard icon={<ShieldAlert className="h-5 w-5" aria-hidden />} label="离线设备" value={offlineCount} tone={offlineCount > 0 ? "error" : "neutral"} />
      </div>
      <div className="grid gap-space-6 xl:grid-cols-[minmax(0,1.5fr)_minmax(340px,1fr)]">
        <DataTableCard>
          <DataTable
            columns={columns}
            dataSource={states}
            rowKey="deviceId"
            loading={loading && states.length === 0}
            error={error && states.length === 0 ? error : undefined}
            empty={<EmptyState title="暂无采集状态" description="启用设备并重启服务后，当前状态会显示在这里。" />}
            minWidth={820}
            onRowClick={(state) => setSelectedID(state.deviceId)}
            rowClassName={(state) => state.deviceId === selectedID ? "bg-info-background" : undefined}
          />
        </DataTableCard>
        <StateDetail state={selected} />
      </div>
    </>
  );
}

function SummaryCard({
  icon,
  label,
  value,
  tone = "neutral",
}: {
  icon: ReactNode;
  label: string;
  value: number;
  tone?: "success" | "error" | "neutral";
}) {
  const toneClass = tone === "success" ? "text-success" : tone === "error" ? "text-error" : "text-text-primary";
  return (
    <div className="flex items-center gap-space-4 rounded-admin border border-border bg-surface p-card shadow-admin">
      <div className={`flex h-10 w-10 items-center justify-center rounded-control bg-neutral-background ${toneClass}`}>{icon}</div>
      <div>
        <div className="text-body-secondary text-text-tertiary">{label}</div>
        <div className={`mt-0.5 text-2xl font-semibold tabular-nums ${toneClass}`}>{value}</div>
      </div>
    </div>
  );
}

function StateDetail({ state }: { state: AcquisitionCurrentState | null }) {
  if (!state) {
    return <EmptyState title="选择一个设备" description="从左侧列表选择设备，查看当前实时数据和字段有效性。" />;
  }

  const status = statusMeta[state.status];
  const data = state.data;
  const fields = Object.entries(fieldLabels);
  return (
    <ContentCard
      title={state.deviceName}
      description={`通道 ${state.channelId} · Modbus Slave #${state.slaveId}`}
      extra={<StatusTag tone={status.tone}>{status.label}</StatusTag>}
    >
      <div className="grid grid-cols-2 gap-x-space-5 gap-y-space-4">
        {fields.map(([key, label]) => {
          const valid = state.fieldValidity[key] === true;
          const value = data[key as keyof typeof data];
          const suffix = key === "voltage" ? " V" : key === "current" ? " A" : key === "activePower" ? " kW" : key === "frequency" ? " Hz" : "";
          return (
            <div key={key} className="border-b border-border pb-space-3">
              <div className="text-body-secondary text-text-tertiary">{label}</div>
              <div className={`mt-space-1 text-lg font-semibold tabular-nums ${valid ? "text-text-primary" : "text-text-tertiary"}`}>
                {valid ? `${value}${suffix}` : "暂无本轮有效数据"}
              </div>
              <div className="mt-space-1 text-helper text-text-tertiary">
                {valid ? `更新于 ${formatDateTime(state.fieldUpdatedAt[key])}` : "保留值未在本轮更新"}
              </div>
            </div>
          );
        })}
      </div>
      <div className="mt-space-5 space-y-space-2 text-body-secondary text-text-tertiary">
        <div>最近尝试：{formatDateTime(state.lastAttemptAt)}</div>
        <div>最近成功：{formatDateTime(state.lastSuccessAt)}</div>
        {state.lastError && <div className="text-error">最近错误：{state.lastError}</div>}
      </div>
    </ContentCard>
  );
}
