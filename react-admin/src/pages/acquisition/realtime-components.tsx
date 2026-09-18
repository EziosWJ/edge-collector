import {
  ChevronDown,
  ChevronRight,
  CircleCheck,
  Cpu,
  RadioTower,
  Search,
  Settings2,
  TriangleAlert,
  WifiOff,
} from "lucide-react";
import type { ReactNode } from "react";
import { ContentCard } from "@/components/common/content-card";
import { DataTable } from "@/components/common/data-table";
import { EmptyState } from "@/components/common/empty-state";
import { StatusTag } from "@/components/common/status-tag";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { formatRegisterValue, type RegisterDisplayMode } from "@/lib/register-format";
import { cn } from "@/lib/utils";
import { acquisitionChannelRuntimeStatusMeta } from "@/types";
import type {
  AcquisitionCurrentState,
  AcquisitionRegisterBlockState,
  AcquisitionStatusTone,
  DataTableColumn,
} from "@/types";
import {
  formatOptionalTime,
  getStateStatusMeta,
  type ChannelGroup,
  type StatusFilter,
} from "./realtime-utils";

export type DisplayMode = RegisterDisplayMode;

export function SummaryStrip({ states }: { states: AcquisitionCurrentState[] }) {
  const unconfiguredCount = states.filter(
    (state) => (state.registerBlocks ?? []).length === 0,
  ).length;
  const configuredStates = states.filter((state) => (state.registerBlocks ?? []).length > 0);
  const onlineCount = configuredStates.filter((state) => state.status === "ONLINE").length;
  const degradedCount = configuredStates.filter((state) => state.status === "DEGRADED").length;
  const offlineCount = configuredStates.filter((state) => state.status === "OFFLINE").length;

  return (
    <div
      className="mb-space-4 grid min-w-0 grid-cols-2 gap-space-3 sm:grid-cols-3 lg:grid-cols-5"
      aria-label="设备状态总览"
    >
      <SummaryCard
        testId="summary-total"
        label="设备总数"
        value={states.length}
        icon={<span className="text-sm font-semibold">总</span>}
      />
      <SummaryCard
        testId="summary-online"
        label="当前在线"
        value={onlineCount}
        tone="success"
        icon={<CircleCheck className="h-4 w-4" aria-hidden />}
      />
      <SummaryCard
        testId="summary-degraded"
        label="部分失败"
        value={degradedCount}
        tone="warning"
        icon={<TriangleAlert className="h-4 w-4" aria-hidden />}
      />
      <SummaryCard
        testId="summary-unconfigured"
        label="未配置读取块"
        value={unconfiguredCount}
        tone="warning"
        icon={<Settings2 className="h-4 w-4" aria-hidden />}
      />
      <SummaryCard
        testId="summary-offline"
        label="离线"
        value={offlineCount}
        tone="error"
        icon={<WifiOff className="h-4 w-4" aria-hidden />}
      />
    </div>
  );
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
  tone?: AcquisitionStatusTone;
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
      <div
        data-testid={testId}
        className={cn("mt-space-2 text-2xl font-semibold leading-none tabular-nums", toneClass)}
      >
        {value}
      </div>
    </ContentCard>
  );
}

export function DisplayModeToolbar({
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

export function ChannelDeviceNavigator({
  groups,
  totalDeviceCount,
  selectedID,
  loading,
  error,
  channelStateError,
  search,
  statusFilter,
  expandedChannels,
  onSearchChange,
  onStatusFilterChange,
  onSelect,
  onToggleChannel,
  onRetry,
}: {
  groups: ChannelGroup[];
  totalDeviceCount: number;
  selectedID: number | null;
  loading: boolean;
  error: string;
  channelStateError: string;
  search: string;
  statusFilter: StatusFilter;
  expandedChannels: Set<number>;
  onSearchChange: (value: string) => void;
  onStatusFilterChange: (value: StatusFilter) => void;
  onSelect: (deviceID: number) => void;
  onToggleChannel: (channelID: number) => void;
  onRetry: () => void;
}) {
  const visibleDeviceCount = groups.reduce((count, group) => count + group.states.length, 0);

  return (
    <div data-testid="realtime-device-nav" className="min-w-0 lg:sticky lg:top-space-4">
      <ContentCard
        title="通道 / 设备"
        description="按通信通道选择设备"
        extra={<span className="shrink-0 text-body-secondary text-text-tertiary">{totalDeviceCount} 台</span>}
        className="min-w-0 overflow-hidden"
        bodyClassName="p-0"
      >
        <div className="space-y-space-2 border-b border-border p-space-3">
          <label className="relative block">
            <Search
              className="pointer-events-none absolute left-space-3 top-1/2 h-4 w-4 -translate-y-1/2 text-text-tertiary"
              aria-hidden
            />
            <Input
              value={search}
              onChange={(event) => onSearchChange(event.target.value)}
              placeholder="搜索通道或设备"
              aria-label="搜索通道或设备"
              className="pl-9"
            />
          </label>
          <Select
            value={statusFilter}
            onChange={(event) => onStatusFilterChange(event.target.value as StatusFilter)}
            aria-label="设备状态筛选"
          >
            <option value="ALL">全部状态</option>
            <option value="ONLINE">在线</option>
            <option value="DEGRADED">部分失败</option>
            <option value="OFFLINE">离线</option>
            <option value="INITIAL">等待首次采集</option>
            <option value="UNCONFIGURED">未配置读取块</option>
          </Select>
        </div>

        {loading && totalDeviceCount === 0 ? (
          <div
            data-testid="device-nav-loading"
            className="space-y-space-2 p-space-3"
            aria-busy="true"
            aria-live="polite"
          >
            {Array.from({ length: 4 }).map((_, index) => (
              <div key={index} className="h-14 animate-pulse rounded-control bg-neutral-background" />
            ))}
          </div>
        ) : error && totalDeviceCount === 0 ? (
          <div data-testid="device-nav-error" className="p-space-3" role="alert">
            <div className="rounded-control border border-error-border bg-error-background p-space-4">
              <div className="font-medium text-error">加载失败</div>
              <div className="mt-1 break-words text-body-secondary text-text-secondary">{error}</div>
              <Button className="mt-space-3" size="sm" variant="secondary" onClick={onRetry}>
                重试
              </Button>
            </div>
          </div>
        ) : totalDeviceCount === 0 ? (
          <div className="p-space-3">
            <DeviceNavigationMessage
              title="暂无运行中设备"
              description="启用设备并配置读取块后，设备会出现在这里。"
            />
          </div>
        ) : (
          <div className="max-h-[calc(100vh-17rem)] overflow-y-auto p-space-2">
            {error && (
              <div
                className="mb-space-2 rounded-control bg-error-background px-space-3 py-space-2 text-xs text-error"
                role="status"
              >
                本次刷新失败，当前显示上次状态
              </div>
            )}
            {channelStateError && (
              <div
                className="mb-space-2 rounded-control bg-warning-background px-space-3 py-space-2 text-xs text-warning"
                role="status"
              >
                通道运行状态暂不可用，仍按设备所属通道展示
              </div>
            )}
            {visibleDeviceCount === 0 ? (
              <DeviceNavigationMessage title="没有匹配的设备" description="调整搜索关键词或状态筛选后重试。" />
            ) : (
              <nav aria-label="按通信通道浏览实时设备">
                <ul className="m-0 min-w-0 list-none space-y-2 p-0">
                  {groups.map((group) => {
                    const expanded = expandedChannels.has(group.channelId);
                    const runtime = group.runtimeState;
                    const runtimeMeta = runtime
                      ? acquisitionChannelRuntimeStatusMeta[runtime.status]
                      : undefined;
                    const onlineCount = group.states.filter((state) => state.status === "ONLINE").length;
                    return (
                      <li key={group.channelId} data-testid={`channel-group-${group.channelId}`}>
                        <button
                          type="button"
                          className="flex w-full items-center gap-space-2 rounded-control border border-info-border bg-info-background px-space-2 py-space-2 text-left text-info transition-colors hover:border-info hover:bg-info-background focus-visible:relative"
                          aria-expanded={expanded}
                          onClick={() => onToggleChannel(group.channelId)}
                        >
                          {expanded ? (
                            <ChevronDown className="h-4 w-4 shrink-0 text-text-tertiary" aria-hidden />
                          ) : (
                            <ChevronRight className="h-4 w-4 shrink-0 text-text-tertiary" aria-hidden />
                          )}
                          <span className="flex h-7 w-7 shrink-0 items-center justify-center rounded-control border border-info-border bg-surface text-info shadow-sm">
                            <RadioTower className="h-4 w-4" aria-hidden />
                          </span>
                          <span className="min-w-0 flex-1">
                            <span className="block truncate font-medium text-text-primary">
                              {group.channelName}
                            </span>
                            <span className="mt-0.5 block truncate text-xs text-text-tertiary">
                              {group.protocol ?? "协议未返回"} · {onlineCount}/{group.states.length} 台在线
                            </span>
                          </span>
                          {runtimeMeta && <StatusTag tone={runtimeMeta.tone}>{runtimeMeta.label}</StatusTag>}
                        </button>
                        {expanded && group.states.length > 0 && (
                          <ul className="m-0 mt-1 list-none space-y-1 border-l border-border pl-space-2">
                            {group.states.map((state) => {
                              const status = getStateStatusMeta(state);
                              const selected = state.deviceId === selectedID;
                              return (
                                <li key={state.deviceId}>
                                  <button
                                    type="button"
                                    data-testid={`device-nav-${state.deviceId}`}
                                    aria-current={selected ? "true" : undefined}
                                    className={cn(
                                      "group flex w-full min-w-0 items-start gap-space-2 rounded-control border border-transparent border-l-2 px-space-2 py-space-2 text-left transition-colors focus-visible:relative",
                                      selected
                                        ? "border-primary border-l-primary bg-surface shadow-subtle"
                                        : "border-l-transparent hover:border-border hover:bg-neutral-background",
                                    )}
                                    onClick={() => onSelect(state.deviceId)}
                                  >
                                    <span
                                      className={cn(
                                        "mt-0.5 flex h-7 w-7 shrink-0 items-center justify-center rounded-control border",
                                        selected
                                          ? "border-primary bg-primary text-white"
                                          : "border-success-border bg-success-background text-success",
                                      )}
                                    >
                                      <Cpu className="h-4 w-4" aria-hidden />
                                    </span>
                                    <span className="min-w-0 flex-1">
                                      <span className="block truncate font-medium text-text-primary">
                                        {state.deviceName}
                                      </span>
                                      <span className="mt-0.5 block truncate text-xs text-text-tertiary">
                                        Unit ID #{state.unitId}
                                        {state.networkEndpoint &&
                                          ` · ${state.networkEndpoint.host}:${state.networkEndpoint.port}`}
                                      </span>
                                      <span className="mt-1 flex min-w-0 flex-wrap items-center gap-x-space-2 gap-y-1">
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
                        )}
                      </li>
                    );
                  })}
                </ul>
              </nav>
            )}
          </div>
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

export function StateDetail({
  state,
  displayMode,
  onDisplayModeChange,
  blockFilter = "all",
  onBlockFilterChange,
}: {
  state: AcquisitionCurrentState | null;
  displayMode: DisplayMode;
  onDisplayModeChange?: (mode: DisplayMode) => void;
  blockFilter?: number | "all";
  onBlockFilterChange?: (value: number | "all") => void;
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
  const visibleBlocks =
    blockFilter === "all"
      ? registerBlocks
      : registerBlocks.filter((block) => block.id === blockFilter);

  return (
    <ContentCard
      title={<span data-testid="state-detail-title">{state.deviceName}</span>}
      description={`通道 ${state.channelId} · Modbus Unit ID #${state.unitId}${state.networkEndpoint ? ` · ${state.networkEndpoint.host}:${state.networkEndpoint.port}` : ""}`}
      extra={<StatusTag tone={status.tone}>{status.label}</StatusTag>}
      className="min-w-0 overflow-hidden"
      bodyClassName="p-0"
    >
      <div className="flex min-w-0 flex-wrap items-center gap-x-space-5 gap-y-2 border-b border-border bg-neutral-background px-card py-space-3 text-body-secondary text-text-secondary">
        <span>最近尝试：{formatOptionalTime(state.lastAttemptAt)}</span>
        <span>最近成功：{formatOptionalTime(state.lastSuccessAt)}</span>
        {state.lastError && <span className="max-w-full break-words text-error">最近错误：{state.lastError}</span>}
      </div>

      <div className="flex min-w-0 flex-wrap items-end justify-between gap-space-3 border-b border-border px-card py-space-4">
        <div>
          <h3 className="text-section-title font-semibold text-text-primary">读取块</h3>
          <p className="mt-1 text-body-secondary text-text-tertiary">
            {registerBlocks.length === 0
              ? "当前设备尚未配置寄存器读取范围。"
              : `${validBlockCount}/${registerBlocks.length} 个读取块本轮完整有效`}
          </p>
        </div>
        <div className="flex flex-wrap items-center justify-end gap-space-2">
          {registerBlocks.length > 0 && (
            <Select
              className="w-auto min-w-36"
              value={blockFilter}
              aria-label="读取块筛选"
              onChange={(event) => {
                const value = event.target.value;
                onBlockFilterChange?.(value === "all" ? "all" : Number(value));
              }}
            >
              <option value="all">全部读取块</option>
              {registerBlocks.map((block) => (
                <option key={block.id} value={block.id}>
                  {block.name}
                </option>
              ))}
            </Select>
          )}
          <DisplayModeToolbar
            mode={displayMode}
            onChange={onDisplayModeChange ?? (() => undefined)}
          />
        </div>
      </div>

      {registerBlocks.length === 0 ? (
        <div className="px-card py-card">
          <DeviceNavigationMessage title="未配置读取块" description="请在设备管理中添加读取块，保存后开始采集。" />
        </div>
      ) : visibleBlocks.length === 0 ? (
        <div className="px-card py-card">
          <DeviceNavigationMessage title="读取块不存在" description="刷新设备状态后重试。" />
        </div>
      ) : (
        <div className="min-w-0">
          {visibleBlocks.map((block) => (
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
      render: (value, row) => {
        const testId = row.address === block.startAddress ? "register-value-0" : undefined;
        return displayMode === "bin" && value !== null && value !== undefined ? (
          <BinaryRegisterValue value={value as number} testId={testId} />
        ) : (
          <span data-testid={testId} className="font-mono tabular-nums">
            {formatRegisterValue(value as number | null, displayMode)}
          </span>
        );
      },
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
    <section data-testid={`register-block-${block.id}`} className="min-w-0 border-b border-border last:border-b-0">
      <header className="flex min-w-0 flex-wrap items-start justify-between gap-space-3 px-card py-space-4">
        <div className="min-w-0">
          <h4 className="truncate font-semibold text-text-primary">{block.name}</h4>
          <p className="mt-1 text-body-secondary text-text-tertiary">
            FC{String(block.functionCode).padStart(2, "0")} · 地址 {block.startAddress}–{block.startAddress + block.quantity - 1} · {block.quantity} 个寄存器 · {validValueCount}/{block.quantity} 有效
          </p>
        </div>
        <div className="shrink-0 text-right text-xs text-text-tertiary">
          <div>最近尝试：{formatOptionalTime(block.lastAttemptAt)}</div>
          <div>最近成功：{formatOptionalTime(block.lastSuccessAt)}</div>
          {block.lastError && <div className="mt-1 max-w-64 break-words text-error">{block.lastError}</div>}
        </div>
      </header>
      <DataTable
        className="realtime-register-table min-w-0"
        columns={columns}
        dataSource={rows}
        rowKey={(row) => `${block.id}-${row.address}`}
        minWidth={460}
      />
    </section>
  );
}

function BinaryRegisterValue({ value, testId }: { value: number; testId?: string }) {
  const bits = value.toString(2).padStart(16, "0").slice(-16).split("");
  const bitGridClass = "grid grid-cols-[repeat(16,minmax(0,1fr))]";

  return (
    <div
      data-testid={testId ? "register-bit-ruler-0" : undefined}
      className="min-w-[320px] font-mono tabular-nums"
      aria-label="二进制寄存器值，位编号从 bit 15 到 bit 0"
    >
      <div className="grid grid-cols-[2rem_minmax(0,1fr)] items-end text-[10px] leading-4 text-text-tertiary">
        <span aria-hidden />
        <div className={cn(bitGridClass, "text-center")} aria-hidden>
          {bits.map((_, index) => (
            <span key={`bit-label-${15 - index}`}>{15 - index}</span>
          ))}
        </div>
        <span className="text-text-secondary">0b</span>
        <div
          data-testid={testId}
          className={cn(bitGridClass, "text-center text-text-primary")}
        >
          {bits.map((bit, index) => <span key={`bit-value-${index}`}>{bit}</span>)}
        </div>
      </div>
    </div>
  );
}
