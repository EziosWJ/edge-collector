import {
  Activity,
  CircleCheck,
  RefreshCw,
  Settings2,
  TriangleAlert,
  WifiOff,
} from "lucide-react";
import { useCallback, useEffect, useMemo, useState } from "react";
import type { ReactNode } from "react";
import { getAcquisitionStates } from "@/api/acquisition";
import { ContentCard } from "@/components/common/content-card";
import { DataTable } from "@/components/common/data-table";
import { EmptyState } from "@/components/common/empty-state";
import { PageHeader } from "@/components/common/page-header";
import { StatusTag } from "@/components/common/status-tag";
import { toast } from "@/components/common/toast-store";
import { Button } from "@/components/ui/button";
import { getErrorMessage } from "@/lib/api-error";
import { formatDateTime } from "@/lib/datetime";
import { formatRegisterValue, type RegisterDisplayMode } from "@/lib/register-format";
import { cn } from "@/lib/utils";
import type {
  AcquisitionCommunicationStatus,
  AcquisitionCurrentState,
  AcquisitionRegisterBlockState,
  DataTableColumn,
} from "@/types";

type DisplayMode = RegisterDisplayMode;
type StatusTone = "success" | "warning" | "error" | "neutral";
type StatusMeta = { label: string; tone: StatusTone };

const statusMeta: Record<AcquisitionCommunicationStatus, StatusMeta> = {
  INITIAL: { label: "等待首次采集", tone: "neutral" },
  ONLINE: { label: "在线", tone: "success" },
  DEGRADED: { label: "部分失败", tone: "warning" },
  OFFLINE: { label: "离线", tone: "error" },
};

function getStateStatusMeta(state: AcquisitionCurrentState): StatusMeta {
  if ((state.registerBlocks ?? []).length === 0) {
    return { label: "未配置读取块", tone: "warning" };
  }
  return statusMeta[state.status];
}

function formatOptionalTime(value?: string | null) {
  const formatted = formatDateTime(value);
  return formatted === "-" ? "暂无" : formatted;
}

export function AcquisitionRealtimePage() {
  const [states, setStates] = useState<AcquisitionCurrentState[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [selectedID, setSelectedID] = useState<number | null>(null);
  const [displayMode, setDisplayMode] = useState<DisplayMode>("hex");

  const loadStates = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      const payload = await getAcquisitionStates();
      const result = (Array.isArray(payload) ? payload : []).map(normalizeCurrentState);
      setStates(result);
      setSelectedID((current) => {
        if (current !== null && result.some((state) => state.deviceId === current)) return current;
        return result[0]?.deviceId ?? null;
      });
    } catch (loadError) {
      setStates([]);
      setError(getErrorMessage(loadError, "无法获取实时寄存器状态"));
      toast.error({
        title: "实时寄存器加载失败",
        description: getErrorMessage(loadError, "请检查 API 服务"),
      });
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
  const unconfiguredCount = states.filter(
    (state) => (state.registerBlocks ?? []).length === 0,
  ).length;
  const configuredStates = states.filter((state) => (state.registerBlocks ?? []).length > 0);
  const onlineCount = configuredStates.filter((state) => state.status === "ONLINE").length;
  const degradedCount = configuredStates.filter((state) => state.status === "DEGRADED").length;
  const offlineCount = configuredStates.filter((state) => state.status === "OFFLINE").length;

  return (
    <div data-testid="realtime-page" className="min-w-0">
      <PageHeader
        title="实时寄存器"
        description="持续观察启用设备的原始寄存器；不进行电压、电流等业务协议解析。"
        actions={
          <div className="flex flex-wrap items-center justify-end gap-space-2">
            <div className="flex h-control-sm items-center gap-1.5 rounded-control bg-neutral-background px-space-2 text-xs text-text-tertiary">
              <span className="h-2 w-2 animate-pulse rounded-full bg-success" aria-hidden />
              自动刷新 · 1.5 秒
            </div>
            <DisplayModeToolbar mode={displayMode} onChange={setDisplayMode} />
            <Button variant="secondary" onClick={() => void loadStates()} disabled={loading}>
              <RefreshCw className="h-4 w-4" aria-hidden />
              立即刷新
            </Button>
          </div>
        }
      />

      <div
        className="mb-space-6 grid min-w-0 grid-cols-2 gap-space-3 sm:grid-cols-3 lg:grid-cols-5"
        aria-label="设备状态总览"
      >
        <SummaryCard
          testId="summary-total"
          icon={<Activity className="h-4 w-4" aria-hidden />}
          label="设备总数"
          value={states.length}
        />
        <SummaryCard
          testId="summary-online"
          icon={<CircleCheck className="h-4 w-4" aria-hidden />}
          label="当前在线"
          value={onlineCount}
          tone="success"
        />
        <SummaryCard
          testId="summary-degraded"
          icon={<TriangleAlert className="h-4 w-4" aria-hidden />}
          label="部分失败"
          value={degradedCount}
          tone="warning"
        />
        <SummaryCard
          testId="summary-unconfigured"
          icon={<Settings2 className="h-4 w-4" aria-hidden />}
          label="未配置读取块"
          value={unconfiguredCount}
          tone="warning"
        />
        <SummaryCard
          testId="summary-offline"
          icon={<WifiOff className="h-4 w-4" aria-hidden />}
          label="离线"
          value={offlineCount}
          tone="error"
        />
      </div>

      <div className="grid min-w-0 gap-space-6 lg:grid-cols-[300px_minmax(0,1fr)] lg:items-start">
        <DeviceNavigation
          states={states}
          selectedID={selectedID}
          loading={loading}
          error={error}
          onSelect={setSelectedID}
          onRetry={() => void loadStates()}
        />
        <StateDetail state={selected} displayMode={displayMode} />
      </div>
    </div>
  );
}

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

function SummaryCard({
  testId,
  icon,
  label,
  value,
  tone = "neutral",
}: {
  testId: string;
  icon: ReactNode;
  label: string;
  value: number;
  tone?: StatusTone;
}) {
  const toneClass =
    tone === "success"
      ? "text-success"
      : tone === "warning"
        ? "text-warning"
        : tone === "error"
          ? "text-error"
          : "text-text-primary";

  return (
    <ContentCard className="min-w-0" bodyClassName="px-space-4 py-space-3">
      <div className="flex min-w-0 items-center justify-between gap-space-2">
        <div className="min-w-0 truncate text-body-secondary text-text-tertiary">{label}</div>
        <div className={cn("shrink-0", toneClass)}>{icon}</div>
      </div>
      <div data-testid={testId} className={cn("mt-space-2 text-2xl font-semibold leading-none tabular-nums", toneClass)}>
        {value}
      </div>
    </ContentCard>
  );
}

function DisplayModeToolbar({
  mode,
  onChange,
}: {
  mode: DisplayMode;
  onChange: (mode: DisplayMode) => void;
}) {
  return (
    <div
      data-testid="register-display-toolbar"
      className="inline-flex h-control-sm items-center rounded-control border border-border bg-surface p-0.5"
      role="group"
      aria-label="寄存器值显示进制"
    >
      {(["hex", "dec", "bin"] as DisplayMode[]).map((item) => (
        <Button
          key={item}
          size="sm"
          variant={mode === item ? "primary" : "ghost"}
          aria-pressed={mode === item}
          onClick={() => onChange(item)}
        >
          {item.toUpperCase()}
        </Button>
      ))}
    </div>
  );
}

function DeviceNavigation({
  states,
  selectedID,
  loading,
  error,
  onSelect,
  onRetry,
}: {
  states: AcquisitionCurrentState[];
  selectedID: number | null;
  loading: boolean;
  error: string;
  onSelect: (deviceID: number) => void;
  onRetry: () => void;
}) {
  return (
    <div data-testid="realtime-device-nav" className="min-w-0" aria-label="实时设备导航">
      <ContentCard
        title="设备"
        description="选择设备查看读取块"
        extra={<span className="shrink-0 text-body-secondary text-text-tertiary">{states.length} 台</span>}
        className="min-w-0 overflow-hidden"
        bodyClassName="p-0"
      >
        {loading && states.length === 0 ? (
          <div data-testid="device-nav-loading" className="space-y-space-2 p-space-3" aria-busy="true" aria-live="polite">
            {Array.from({ length: 4 }).map((_, index) => (
              <div key={index} className="h-16 animate-pulse rounded-control bg-neutral-background" />
            ))}
          </div>
        ) : error && states.length === 0 ? (
          <div data-testid="device-nav-error" className="p-space-3" role="alert">
            <div className="rounded-control border border-error-border bg-error-background p-space-4">
              <div className="font-medium text-error">加载失败</div>
              <div className="mt-1 break-words text-body-secondary text-text-secondary">{error}</div>
              <Button className="mt-space-3" size="sm" variant="secondary" onClick={onRetry}>
                重试
              </Button>
            </div>
          </div>
        ) : states.length === 0 ? (
          <div className="p-space-3">
            <DeviceNavigationMessage title="暂无运行中设备" description="启用设备并配置读取块后，设备会出现在这里。" />
          </div>
        ) : (
          <nav className="min-w-0 p-space-2" aria-label="实时设备列表">
            {error && (
              <div className="mb-space-2 rounded-control bg-error-background px-space-3 py-space-2 text-xs text-error" role="status">
                本次刷新失败，当前显示上次状态
              </div>
            )}
            <ul className="m-0 min-w-0 list-none space-y-1 p-0">
              {states.map((state) => {
                const status = getStateStatusMeta(state);
                const selected = state.deviceId === selectedID;
                return (
                  <li key={state.deviceId}>
                    <button
                      type="button"
                      data-testid={`device-nav-${state.deviceId}`}
                      aria-current={selected ? "true" : undefined}
                      className={cn(
                        "group flex w-full min-w-0 items-start gap-space-3 rounded-control border border-transparent border-l-4 px-space-3 py-space-3 text-left transition-colors focus-visible:relative",
                        selected
                          ? "border-info-border border-l-primary bg-info-background"
                          : "border-l-transparent hover:border-border hover:bg-neutral-background",
                      )}
                      onClick={() => onSelect(state.deviceId)}
                    >
                      <span className={cn("mt-1.5 h-2 w-2 shrink-0 rounded-full", getStatusDotClass(status.tone))} aria-hidden />
                      <span className="min-w-0 flex-1">
                        <span className="block truncate font-medium text-text-primary">{state.deviceName}</span>
                        <span className="mt-0.5 block truncate text-xs text-text-tertiary">
                          通道 {state.channelId} · Slave #{state.slaveId}
                        </span>
                        <span className="mt-space-2 flex min-w-0 flex-wrap items-center gap-x-space-2 gap-y-1">
                          <StatusTag tone={status.tone}>{status.label}</StatusTag>
                          <span className="truncate text-xs text-text-tertiary">
                            最近成功 {formatOptionalTime(state.lastSuccessAt)}
                          </span>
                        </span>
                      </span>
                    </button>
                  </li>
                );
              })}
            </ul>
          </nav>
        )}
      </ContentCard>
    </div>
  );
}

function DeviceNavigationMessage({ title, description }: { title: string; description: string }) {
  return (
    <div className="rounded-control border border-dashed border-border bg-neutral-background px-space-4 py-space-6 text-center">
      <div className="font-medium text-text-primary">{title}</div>
      <p className="mt-1 text-body-secondary text-text-tertiary">{description}</p>
    </div>
  );
}

function getStatusDotClass(tone: StatusTone) {
  if (tone === "success") return "bg-success";
  if (tone === "warning") return "bg-warning";
  if (tone === "error") return "bg-error";
  return "bg-text-tertiary";
}

export function StateDetail({
  state,
  displayMode,
}: {
  state: AcquisitionCurrentState | null;
  displayMode: DisplayMode;
}) {
  if (!state) {
    return (
      <div data-testid="state-detail" className="min-w-0">
        <ContentCard bodyClassName="p-0">
          <EmptyState title="选择一个设备" description="从左侧设备导航选择设备，查看当前读取块和原始寄存器值。" />
        </ContentCard>
      </div>
    );
  }

  const registerBlocks = state.registerBlocks ?? [];
  const status = getStateStatusMeta(state);
  const validBlockCount = registerBlocks.filter((block) => block.valid).length;

  return (
    <ContentCard
      title={<span data-testid="state-detail-title">{state.deviceName}</span>}
      description={`通道 ${state.channelId} · Modbus Slave #${state.slaveId}`}
      extra={<StatusTag tone={status.tone}>{status.label}</StatusTag>}
      className="min-w-0 overflow-hidden"
      bodyClassName="p-0"
    >
      <div className="flex min-w-0 flex-wrap items-center gap-x-space-5 gap-y-2 border-b border-border bg-neutral-background px-card py-space-3 text-body-secondary text-text-secondary">
        <span>最近尝试：{formatOptionalTime(state.lastAttemptAt)}</span>
        <span>最近成功：{formatOptionalTime(state.lastSuccessAt)}</span>
        {state.lastError && <span className="max-w-full break-words text-error">最近错误：{state.lastError}</span>}
      </div>

      <div className="flex min-w-0 flex-wrap items-end justify-between gap-space-3 px-card py-space-4">
        <div>
          <h3 className="text-section-title font-semibold text-text-primary">读取块</h3>
          <p className="mt-1 text-body-secondary text-text-tertiary">
            {registerBlocks.length === 0
              ? "当前设备尚未配置寄存器读取范围。"
              : `${validBlockCount}/${registerBlocks.length} 个读取块本轮完整有效`}
          </p>
        </div>
        {registerBlocks.length > 0 && (
          <span className="text-xs text-text-tertiary">值为原始 16-bit 无符号寄存器</span>
        )}
      </div>

      {registerBlocks.length === 0 ? (
        <div className="px-card pb-card">
          <DeviceNavigationMessage title="未配置读取块" description="请在设备管理中添加读取块，保存后开始采集。" />
        </div>
      ) : (
        <div className="min-w-0 space-y-space-4 px-card pb-card">
          {registerBlocks.map((block) => (
            <BlockDetail key={block.id || `${block.name}-${block.sortOrder}`} block={block} displayMode={displayMode} />
          ))}
        </div>
      )}
    </ContentCard>
  );
}

function BlockDetail({ block, displayMode }: { block: AcquisitionRegisterBlockState; displayMode: DisplayMode }) {
  const values = Array.isArray(block.values) ? block.values : [];
  const validValueCount = block.valid ? values.filter((value) => value !== null).length : 0;
  const columns: DataTableColumn<{ address: number; value: number | null }>[] = [
    {
      title: "地址（十进制）",
      dataIndex: "address",
      width: 150,
      render: (value) => <span className="tabular-nums">{String(value)}</span>,
    },
    {
      title: `值（${displayMode.toUpperCase()}）`,
      dataIndex: "value",
      width: 220,
      render: (value, row) => (
        <span data-testid={row.address === block.startAddress ? "register-value-0" : undefined} className="font-mono tabular-nums">
          {formatRegisterValue(value as number | null, displayMode)}
        </span>
      ),
    },
    {
      title: "状态",
      key: "valid",
      width: 120,
      render: () => <StatusTag tone={block.valid ? "success" : "warning"}>{block.valid ? "有效" : "无效"}</StatusTag>,
    },
  ];
  const rows = values.map((value, index) => ({ address: block.startAddress + index, value }));

  return (
    <ContentCard
      title={block.name}
      description={`FC${String(block.functionCode).padStart(2, "0")} · 地址 ${block.startAddress}–${block.startAddress + block.quantity - 1} · ${block.quantity} 个寄存器 · ${validValueCount}/${block.quantity} 有效`}
      extra={
        <div className="shrink-0 text-right text-xs text-text-tertiary">
          <div>最近尝试：{formatOptionalTime(block.lastAttemptAt)}</div>
          <div>最近成功：{formatOptionalTime(block.lastSuccessAt)}</div>
          {block.lastError && <div className="mt-1 max-w-64 break-words text-error">{block.lastError}</div>}
        </div>
      }
      className="min-w-0 overflow-hidden rounded-control shadow-none"
      bodyClassName="p-0"
    >
      <DataTable
        className="realtime-register-table min-w-0"
        columns={columns}
        dataSource={rows}
        rowKey={(row) => `${block.id}-${row.address}`}
        minWidth={460}
      />
    </ContentCard>
  );
}
