import {
  AlertTriangle,
  CheckCircle2,
  ChevronRight,
  CircleHelp,
  Database,
  FileWarning,
  Inbox,
  RefreshCw,
  Server,
  WifiOff,
  XCircle,
} from "lucide-react";
import type { ReactNode } from "react";
import { useMemo } from "react";
import { Link } from "react-router-dom";
import { ContentCard } from "@/components/common/content-card";
import { PageHeader } from "@/components/common/page-header";
import { StatusTag } from "@/components/common/status-tag";
import { Button } from "@/components/ui/button";
import { formatDateTime } from "@/lib/datetime";
import { cn } from "@/lib/utils";
import { mqttRuntimeStatusMeta } from "@/types";
import { useDashboardData } from "./dashboard/dashboard-data";
import {
  buildAttentionItems,
  buildDomainSummaries,
  getChannelCounts,
  getChannelRuntime,
  getChannelStatusMeta,
  getDeviceCounts,
  getEnabledChannels,
  getEnabledDevices,
  protocolLabels,
} from "./dashboard/dashboard-utils";
import type {
  DashboardAttentionItem,
  DashboardDomainStatus,
  DashboardSources,
} from "./dashboard/types";

const domainStatusTone: Record<DashboardDomainStatus, "success" | "warning" | "error" | "neutral"> = {
  success: "success",
  warning: "warning",
  error: "error",
  neutral: "neutral",
};

const severityTone: Record<DashboardAttentionItem["severity"], "warning" | "error" | "info"> = {
  critical: "error",
  warning: "warning",
  info: "info",
};

export function DashboardPage() {
  const { sources, mqttVisible, refresh, lastSuccessfulRefreshAt } = useDashboardData();
  const domains = useMemo(() => buildDomainSummaries(sources, mqttVisible), [mqttVisible, sources]);
  const attention = useMemo(() => buildAttentionItems(sources, mqttVisible), [mqttVisible, sources]);
  const enabledChannels = getEnabledChannels(sources.channels.data);
  const isLoading = Object.values(sources).some((source) => source.loading);
  const hasStaleData = Object.values(sources).some((source) => source.stale);

  return (
    <div data-testid="dashboard-page" className="min-w-0">
      <PageHeader
        title="运维概览"
        description="单个 Edge Collector 部署实例的只读运行状态。"
        actions={
          <div className="flex flex-wrap items-center justify-end gap-space-2">
            <span className="text-xs text-text-tertiary" aria-live="polite">
              {lastSuccessfulRefreshAt
                ? `最近成功刷新：${formatDateTime(new Date(lastSuccessfulRefreshAt).toISOString())}`
                : "尚未成功刷新"}
            </span>
            <Button
              data-testid="dashboard-refresh"
              variant="secondary"
              onClick={() => void refresh()}
              disabled={isLoading}
            >
              <RefreshCw className="h-4 w-4" aria-hidden />
              刷新
            </Button>
          </div>
        }
      />

      {hasStaleData && (
        <div className="mb-space-4 flex flex-wrap items-center gap-space-2 rounded-control border border-warning-border bg-warning-background px-space-3 py-space-2 text-body-secondary text-warning" role="status">
          <AlertTriangle className="h-4 w-4" aria-hidden />
          部分数据可能已过期；页面会在可见时继续尝试刷新。
        </div>
      )}

      <div className="grid gap-space-4 xl:grid-cols-[minmax(0,8fr)_minmax(300px,4fr)] xl:items-start">
        <AttentionPanel result={attention} />
        <HealthSummary domains={domains} />
      </div>

      <ChannelBoard sources={sources} channels={enabledChannels} onRefresh={refresh} />
      <RuntimeDetails sources={sources} mqttVisible={mqttVisible} onRefresh={refresh} />
    </div>
  );
}

function HealthSummary({ domains }: { domains: ReturnType<typeof buildDomainSummaries> }) {
  return (
    <ContentCard data-testid="dashboard-health-summary" className="order-first xl:order-last" title="健康分域" description="只统计当前状态，不计算可用率或历史趋势。">
      <div className="grid gap-space-3 sm:grid-cols-2">
        {domains.map((domain) => (
          <div
            key={domain.key}
            data-testid={`dashboard-domain-${domain.key}`}
            className={cn(
              "min-w-0 rounded-control border border-border bg-neutral-background px-space-3 py-space-3",
              domain.status === "error" && "border-l-4 border-l-error",
              domain.status === "warning" && "border-l-4 border-l-warning",
              domain.status === "success" && "border-l-4 border-l-success",
            )}
          >
            <div className="flex items-center justify-between gap-space-2">
              <span className="text-sm font-medium text-text-primary">{domain.label}</span>
              {domain.loading && !domain.error ? <span className="text-xs text-text-tertiary">加载中</span> : <StatusTag tone={domainStatusTone[domain.status]}>{domain.statusLabel}</StatusTag>}
            </div>
            <p className="mt-space-2 break-words text-xs leading-5 text-text-tertiary">{domain.error || domain.detail}</p>
            {domain.stale && <p className="mt-space-1 text-xs text-warning">数据可能已过期</p>}
          </div>
        ))}
      </div>
    </ContentCard>
  );
}

function AttentionPanel({ result }: { result: ReturnType<typeof buildAttentionItems> }) {
  return (
    <ContentCard data-testid="dashboard-attention" className="order-last xl:order-first" title="运维关注项" description="按严重程度排序，同级问题优先显示持续时间更长的项目。" extra={<StatusTag tone={result.total > 0 ? "warning" : "success"}>{`${result.total} 项`}</StatusTag>}>
      {result.total === 0 ? (
        <div className="flex items-center gap-space-3 rounded-control border border-success-border bg-success-background px-space-4 py-space-4 text-sm text-success">
          <CheckCircle2 className="h-5 w-5 shrink-0" aria-hidden />
          <span>当前没有需要关注的运行问题。</span>
        </div>
      ) : (
        <>
          <div className="space-y-space-2" aria-live="polite">
            {result.items.map((item) => <AttentionRow key={item.id} item={item} />)}
          </div>
          <div className="mt-space-4 flex flex-wrap items-center gap-x-space-3 gap-y-space-2 border-t border-border pt-space-3 text-xs text-text-tertiary">
            {Object.entries(result.categoryCounts).map(([category, count]) => {
              const href = attentionHref(category);
              return href ? <Link key={category} className="hover:text-primary" to={href}>{category} {count}</Link> : <span key={category}>{category} {count}</span>;
            })}
            {result.total > result.items.length && <span>首屏显示前 {result.items.length} 项</span>}
            <Link className="ml-auto inline-flex items-center gap-1 font-medium text-primary hover:text-primary-hover" to="/acquisition/realtime">查看全部 <ChevronRight className="h-3.5 w-3.5" aria-hidden /></Link>
          </div>
        </>
      )}
    </ContentCard>
  );
}

function AttentionRow({ item }: { item: DashboardAttentionItem }) {
  const Icon = item.severity === "critical" ? XCircle : item.severity === "warning" ? AlertTriangle : CircleHelp;
  const iconClass = item.severity === "critical" ? "text-error" : item.severity === "warning" ? "text-warning" : "text-info";
  const content = (
    <div data-testid="dashboard-attention-item" className="flex min-w-0 items-start gap-space-3 rounded-control border border-border px-space-3 py-space-3 transition-colors hover:bg-neutral-background">
      <Icon className={cn("mt-0.5 h-4 w-4 shrink-0", iconClass)} aria-hidden />
      <div className="min-w-0 flex-1">
        <div className="flex flex-wrap items-center gap-space-2"><span className="truncate text-sm font-medium text-text-primary">{item.title}</span><StatusTag tone={severityTone[item.severity]}>{item.category}</StatusTag></div>
        <p className="mt-space-1 break-words text-xs leading-5 text-text-tertiary">{item.description}</p>
        {item.occurredAt && <p className="mt-space-1 text-xs text-text-tertiary">最近发生：{formatDateTime(item.occurredAt)}</p>}
      </div>
      {item.href && <ChevronRight className="mt-0.5 h-4 w-4 shrink-0 text-text-tertiary" aria-hidden />}
    </div>
  );
  return item.href ? <Link to={item.href}>{content}</Link> : content;
}

function ChannelBoard({ sources, channels, onRefresh }: { sources: DashboardSources; channels: ReturnType<typeof getEnabledChannels>; onRefresh: () => void }) {
  const states = sources.channelStates.data;
  const devices = getEnabledDevices(sources.devices.data);
  const channelCounts = getChannelCounts(channels, states);
  const orderedChannels = [...channels].sort((left, right) => {
    const leftStatus = getChannelRuntime(left, states).status;
    const rightStatus = getChannelRuntime(right, states).status;
    const rank = (status: string) => status === "OFFLINE" ? 0 : status === "DEGRADED" ? 1 : 2;
    return rank(leftStatus) - rank(rightStatus) || left.name.localeCompare(right.name, "zh-CN");
  });

  return (
    <ContentCard data-testid="dashboard-channel-board" className="mt-space-4" title="通信通道状态" description="关注项置顶；只展示已启用通道及其设备通信摘要。" extra={<div className="flex flex-wrap items-center gap-space-2 text-xs text-text-tertiary"><span>{`共 ${channels.length} 个`}</span><StatusTag tone="success">{`在线 ${channelCounts.ONLINE}`}</StatusTag><StatusTag tone="warning">{`部分失败 ${channelCounts.DEGRADED}`}</StatusTag><StatusTag tone="error">{`离线 ${channelCounts.OFFLINE}`}</StatusTag></div>}>
      {sources.channels.loading && !sources.channels.data ? <LoadingRows count={3} /> : sources.channels.error && !sources.channels.data ? <InlineError message={sources.channels.error} onRetry={onRefresh} /> : channels.length === 0 ? <EmptyPanel icon={<Server className="h-5 w-5" aria-hidden />} title="尚未配置启用的通信通道" description="启用通道后，运行状态会显示在这里。" /> : (
        <div className="overflow-x-auto"><div className="min-w-[720px]"><div className="grid grid-cols-[minmax(220px,2fr)_minmax(130px,1fr)_minmax(180px,1.5fr)_minmax(190px,1.5fr)] gap-space-3 border-b border-border px-space-3 pb-space-2 text-xs text-text-tertiary"><span>通道</span><span>运行状态</span><span>设备摘要</span><span>最近采集</span></div><div className="divide-y divide-border">{orderedChannels.map((channel) => <ChannelRow key={channel.id} channel={channel} sources={sources} devices={devices} />)}</div></div></div>
      )}
    </ContentCard>
  );
}

function ChannelRow({ channel, sources, devices }: { channel: ReturnType<typeof getEnabledChannels>[number]; sources: DashboardSources; devices: ReturnType<typeof getEnabledDevices> }) {
  const runtime = getChannelRuntime(channel, sources.channelStates.data);
  const channelDevices = devices.filter((device) => device.channelId === channel.id);
  const deviceCounts = getDeviceCounts(channelDevices, sources.states.data);
  const status = getChannelStatusMeta(runtime.status);
  const isAttention = runtime.status === "OFFLINE" || runtime.status === "DEGRADED";
  return (
    <Link data-testid="dashboard-channel-row" to={`/acquisition/realtime?channelId=${channel.id}`} className={cn("grid grid-cols-[minmax(220px,2fr)_minmax(130px,1fr)_minmax(180px,1.5fr)_minmax(190px,1.5fr)] gap-space-3 px-space-3 py-space-3 transition-colors hover:bg-neutral-background", isAttention && "bg-warning-background/30")}>
      <div className="min-w-0"><div className="flex items-center gap-space-2"><span className="truncate text-sm font-medium text-text-primary">{channel.name}</span>{isAttention && <AlertTriangle className="h-3.5 w-3.5 shrink-0 text-warning" aria-label="需要关注" />}</div><div className="mt-space-1 text-xs text-text-tertiary">{protocolLabels[channel.protocol]} · ID {channel.id}</div></div>
      <div><StatusTag tone={status.tone}>{status.label}</StatusTag>{runtime.lastError && <div className="mt-space-1 max-w-[180px] truncate text-xs text-error">{runtime.lastError}</div>}</div>
      <div className="text-sm text-text-secondary"><div>{channelDevices.length} 个设备</div><div className="mt-space-1 text-xs text-text-tertiary">在线 {deviceCounts.ONLINE} · 部分失败 {deviceCounts.DEGRADED} · 离线 {deviceCounts.OFFLINE}</div></div>
      <div className="flex items-start justify-between gap-space-2 text-sm text-text-secondary"><div><div>最近成功：{formatDateTime(runtime.lastSuccessAt)}</div><div className="mt-space-1 text-xs text-text-tertiary">最近尝试：{formatDateTime(runtime.lastAttemptAt)}</div></div><ChevronRight className="mt-0.5 h-4 w-4 shrink-0 text-text-tertiary" aria-hidden /></div>
    </Link>
  );
}

function RuntimeDetails({ sources, mqttVisible, onRefresh }: { sources: DashboardSources; mqttVisible: boolean; onRefresh: () => void }) {
  const mqtt = sources.mqtt.data;
  const outbox = sources.mqttOutbox.data;
  const serviceError = sources.health.error || sources.ready.error;
  return (
    <div className="mt-space-4 grid gap-space-4 md:grid-cols-2">
      {mqttVisible && <ContentCard title="MQTT 上行" description="连接状态、Reliable Outbox 和 Raw pending 仅作运行信息。">
        {sources.mqtt.loading && !mqtt ? <LoadingRows count={2} /> : sources.mqtt.error && !mqtt ? <InlineError message={sources.mqtt.error} onRetry={onRefresh} /> : <div className="space-y-space-3"><div className="flex items-center justify-between gap-space-3"><div className="flex items-center gap-space-2"><WifiOff className="h-4 w-4 text-info" aria-hidden /><span className="text-sm text-text-secondary">连接</span></div><StatusTag tone={getMqttStatusMeta(mqtt?.state).tone}>{mqtt ? getMqttStatusMeta(mqtt.state).label : "等待数据"}</StatusTag></div><div className="grid gap-space-3 sm:grid-cols-3"><Metric icon={<Inbox className="h-4 w-4" aria-hidden />} label="Outbox" value={outbox ? `${Math.round(Math.max(outbox.rowUtilization, outbox.byteUtilization) * 100)}%` : "-"} /><Metric icon={<Database className="h-4 w-4" aria-hidden />} label="Raw pending" value={String(mqtt?.pendingLatestCount ?? 0)} /><Metric icon={<RefreshCw className="h-4 w-4" aria-hidden />} label="重连次数" value={String(mqtt?.reconnectAttempts ?? 0)} /></div>{outbox?.lastError && <p className="rounded-control border border-error-border bg-error-background px-space-3 py-space-2 text-xs text-error">最近投递失败：{outbox.lastError}</p>}<Link className="inline-flex items-center gap-1 text-sm font-medium text-primary hover:text-primary-hover" to="/mqtt/overview">进入 MQTT 运行总览 <ChevronRight className="h-4 w-4" aria-hidden /></Link></div>}
      </ContentCard>}
      <ContentCard title="服务与数据库" description="/health 证明进程存活；/ready 证明数据库就绪.">
        {sources.health.loading || sources.ready.loading ? <LoadingRows count={2} /> : <div className="space-y-space-3"><ServiceCheck icon={<Server className="h-4 w-4" aria-hidden />} label="服务存活（/health）" ok={Boolean(sources.health.data)} error={sources.health.error} /><ServiceCheck icon={<Database className="h-4 w-4" aria-hidden />} label="数据库就绪（/ready）" ok={Boolean(sources.ready.data)} error={sources.ready.error} />{serviceError && <div className="flex flex-wrap items-start gap-space-2 rounded-control border border-error-border bg-error-background px-space-3 py-space-2 text-xs text-error"><FileWarning className="mt-0.5 h-4 w-4 shrink-0" aria-hidden /><span className="min-w-0 flex-1">{serviceError}</span><Button size="sm" variant="secondary" onClick={onRefresh}>重试</Button></div>}</div>}
      </ContentCard>
    </div>
  );
}

function ServiceCheck({ icon, label, ok, error }: { icon: ReactNode; label: string; ok: boolean; error: string }) {
  return <div className="flex items-center justify-between gap-space-3 rounded-control border border-border bg-neutral-background px-space-3 py-space-3"><div className="flex min-w-0 items-center gap-space-2 text-sm text-text-secondary">{icon}<span>{label}</span></div><StatusTag tone={error ? "error" : ok ? "success" : "neutral"}>{error ? "不可用" : ok ? "正常" : "等待检查"}</StatusTag></div>;
}

function Metric({ icon, label, value }: { icon: ReactNode; label: string; value: string }) {
  return <div className="rounded-control border border-border bg-neutral-background px-space-3 py-space-3"><div className="flex items-center justify-between gap-space-2 text-xs text-text-tertiary"><span>{label}</span><span className="text-info">{icon}</span></div><div className="mt-space-2 tabular-nums text-lg font-semibold text-text-primary">{value}</div></div>;
}

function LoadingRows({ count }: { count: number }) {
  return <div className="space-y-space-2" aria-busy="true" aria-live="polite">{Array.from({ length: count }).map((_, index) => <div key={index} className="h-12 animate-pulse rounded-control bg-neutral-background" />)}</div>;
}

function InlineError({ message, onRetry }: { message: string; onRetry: () => void }) {
  return <div className="flex flex-col items-start gap-space-3 rounded-control border border-error-border bg-error-background px-space-4 py-space-4 text-sm text-error sm:flex-row sm:items-center"><XCircle className="h-5 w-5 shrink-0" aria-hidden /><span className="min-w-0 flex-1 break-words">{message}</span><Button size="sm" variant="secondary" onClick={onRetry}>重试</Button></div>;
}

function EmptyPanel({ icon, title, description }: { icon: ReactNode; title: string; description: string }) {
  return <div className="flex items-start gap-space-3 rounded-control border border-dashed border-border bg-neutral-background px-space-4 py-space-4"><span className="mt-0.5 text-text-tertiary">{icon}</span><div><div className="text-sm font-medium text-text-primary">{title}</div><p className="mt-space-1 text-xs text-text-tertiary">{description}</p></div></div>;
}

function attentionHref(category: string) {
  if (category === "MQTT 上行") return "/mqtt/overview";
  if (category === "协议脚本") return "/acquisition/script";
  if (category === "服务/数据库") return undefined;
  return "/acquisition/realtime";
}

function getMqttStatusMeta(status?: string) {
  if (status && status in mqttRuntimeStatusMeta) {
    return mqttRuntimeStatusMeta[status as keyof typeof mqttRuntimeStatusMeta];
  }
  return { label: status || "未知", tone: "neutral" as const };
}
