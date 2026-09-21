import {
  AlertTriangle,
  Activity,
  Clock3,
  Inbox,
  KeyRound,
  RadioTower,
  RefreshCw,
  Server,
  WifiOff,
  XCircle,
} from "lucide-react";
import { useEffect, useState, type ReactNode } from "react";
import { z } from "zod";
import type { UseFormReturn } from "react-hook-form";
import { PermissionGuard } from "@/components/auth/permission-guard";
import { ContentCard } from "@/components/common/content-card";
import { DetailDialog } from "@/components/common/detail-dialog";
import { EmptyState } from "@/components/common/empty-state";
import { Field } from "@/components/common/field";
import { FormSection } from "@/components/common/form-section";
import { PageHeader } from "@/components/common/page-header";
import { StatusTag } from "@/components/common/status-tag";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import { formatDateTime } from "@/lib/datetime";
import { formatMqttBytes, getMqttErrorMessage } from "@/lib/mqtt";
import type { MqttConfigFormValues } from "@/lib/mqtt";
import type {
  DataTableColumn,
  MqttCommandJournal,
  MqttCommandListQuery,
  MqttCommandStatus,
  MqttConfig,
  MqttOutboxStats,
  MqttRuntimeState,
  MqttRuntimeStatus,
} from "@/types";
import { mqttCommandStatusMeta, mqttRuntimeStatusMeta } from "@/types";

export const commandStatuses: Array<{ value: MqttCommandStatus; label: string }> = [
  { value: "ACCEPTED", label: "已接收" },
  { value: "RUNNING", label: "执行中" },
  { value: "SUCCEEDED", label: "执行成功" },
  { value: "FAILED", label: "执行失败" },
  { value: "REJECTED", label: "已拒绝" },
  { value: "EXPIRED", label: "已过期" },
];

export type CommandFilterState = {
  status: "all" | MqttCommandStatus;
  deviceId: string;
  commandId: string;
  name: string;
};

export const DEFAULT_COMMAND_FILTERS: CommandFilterState = {
  status: "all",
  deviceId: "",
  commandId: "",
  name: "",
};

export const DEFAULT_CONFIG: MqttConfig = {
  enabled: false,
  edgeId: "edge-01",
  brokerUrl: "mqtt://127.0.0.1:1883",
  protocolVersion: "MQTT_5",
  clientId: "edge-collector",
  username: "",
  passwordConfigured: false,
  tlsEnabled: false,
  caCertificate: "",
  clientCertificate: "",
  clientPrivateKeyConfigured: false,
  keepAliveSeconds: 30,
  connectTimeoutMs: 10000,
  reconnectMinMs: 1000,
  reconnectMaxMs: 60000,
  topicPrefix: "edge",
  rawPublishIntervalMs: 1000,
  outboxMaxRows: 10000,
  outboxMaxBytes: 64 * 1024 * 1024,
  outboxRetentionDays: 7,
  commandJournalRetentionDays: 7,
  commandJournalMaxRows: 10000,
  commandQueueCapacity: 32,
  commandPollFairness: 1,
};

function isValidTopicSegment(value: string) {
  if (!value || value.includes("/") || value.includes("+") || value.includes("#")) {
    return false;
  }
  return [...value].every((character) => {
    const code = character.charCodeAt(0);
    return code >= 0x20 && code !== 0x7f;
  });
}

export const mqttConfigSchema = z
  .object({
    enabled: z.boolean(),
    edgeId: z.string().trim().min(1, "Edge ID 不能为空").max(128, "Edge ID 不能超过 128 个字符").refine(isValidTopicSegment, "Edge ID 不能包含 MQTT 通配符、斜线或控制字符"),
    brokerUrl: z.string().trim().min(1, "Broker 地址不能为空").max(2048, "Broker 地址不能超过 2048 个字符").refine((value) => {
      try {
        const url = new URL(value);
        return ["mqtt:", "mqtts:", "tcp:", "tls:", "ssl:", "ws:", "wss:"].includes(url.protocol) && Boolean(url.hostname) && !url.username && !url.password;
      } catch {
        return false;
      }
    }, "请输入带协议和主机的 Broker 地址"),
    protocolVersion: z.enum(["MQTT_5", "MQTT_3_1_1"], { errorMap: () => ({ message: "请选择 MQTT 协议版本" }) }),
    clientId: z.string().trim().min(1, "Client ID 不能为空").max(256, "Client ID 不能超过 256 个字符"),
    username: z.string().max(256, "用户名不能超过 256 个字符"),
    passwordConfigured: z.boolean(),
    passwordAction: z.enum(["keep", "set", "clear"]),
    password: z.string().max(4096, "密码不能超过 4096 个字符"),
    tlsEnabled: z.boolean(),
    caCertificate: z.string().max(1024 * 1024, "CA 证书不能超过 1 MiB"),
    clientCertificate: z.string().max(1024 * 1024, "客户端证书不能超过 1 MiB"),
    clientPrivateKeyConfigured: z.boolean(),
    clientPrivateKeyAction: z.enum(["keep", "set", "clear"]),
    clientPrivateKey: z.string().max(1024 * 1024, "客户端私钥不能超过 1 MiB"),
    keepAliveSeconds: z.coerce.number().int("Keep Alive 必须是整数").min(5, "Keep Alive 范围为 5～3600 秒").max(3600, "Keep Alive 范围为 5～3600 秒"),
    connectTimeoutMs: z.coerce.number().int("连接超时必须是整数").min(100, "连接超时范围为 100～120000 毫秒").max(120000, "连接超时范围为 100～120000 毫秒"),
    reconnectMinMs: z.coerce.number().int("最小重连间隔必须是整数").min(100, "最小重连间隔范围为 100～60000 毫秒").max(60000, "最小重连间隔范围为 100～60000 毫秒"),
    reconnectMaxMs: z.coerce.number().int("最大重连间隔必须是整数").min(100, "最大重连间隔范围为 100～600000 毫秒").max(600000, "最大重连间隔范围为 100～600000 毫秒"),
    topicPrefix: z.string().trim().min(1, "Topic 前缀不能为空").max(256, "Topic 前缀不能超过 256 个字符").refine((value) => value.split("/").every(isValidTopicSegment), "Topic 前缀不能包含空段、通配符或控制字符"),
    rawPublishIntervalMs: z.coerce.number().int("Raw 上报间隔必须是整数").min(50, "Raw 上报间隔范围为 50～3600000 毫秒").max(3600000, "Raw 上报间隔范围为 50～3600000 毫秒"),
    outboxMaxRows: z.coerce.number().int("Outbox 行数上限必须是整数").min(1, "Outbox 行数上限至少为 1").max(1000000, "Outbox 行数上限不能超过 1000000"),
    outboxMaxBytes: z.coerce.number().int("Outbox 字节上限必须是整数").min(1024, "Outbox 字节上限至少为 1024").max(1024 * 1024 * 1024, "Outbox 字节上限不能超过 1 GiB"),
    outboxRetentionDays: z.coerce.number().int("Outbox 保留天数必须是整数").min(1, "Outbox 保留天数范围为 1～3650 天").max(3650, "Outbox 保留天数范围为 1～3650 天"),
    commandJournalRetentionDays: z.coerce.number().int("Command Journal 保留天数必须是整数").min(1, "Command Journal 保留天数范围为 1～3650 天").max(3650, "Command Journal 保留天数范围为 1～3650 天"),
    commandJournalMaxRows: z.coerce.number().int("Command Journal 行数上限必须是整数").min(1, "Command Journal 行数上限至少为 1").max(1000000, "Command Journal 行数上限不能超过 1000000"),
    commandQueueCapacity: z.coerce.number().int("Command 队列容量必须是整数").min(1, "Command 队列容量至少为 1").max(100000, "Command 队列容量不能超过 100000"),
    commandPollFairness: z.coerce.number().int("Poll fairness 必须是整数").min(1, "Poll fairness 范围为 1～100").max(100, "Poll fairness 范围为 1～100"),
  })
  .superRefine((values, context) => {
    if (values.reconnectMinMs > values.reconnectMaxMs) {
      context.addIssue({ code: z.ZodIssueCode.custom, path: ["reconnectMaxMs"], message: "最大重连间隔不能小于最小重连间隔" });
    }
    if (values.passwordAction === "set" && values.password.length === 0) {
      context.addIssue({ code: z.ZodIssueCode.custom, path: ["password"], message: "请输入新的 Broker 密码" });
    }
    if (values.clientPrivateKeyAction === "set" && values.clientPrivateKey.length === 0) {
      context.addIssue({ code: z.ZodIssueCode.custom, path: ["clientPrivateKey"], message: "请输入新的客户端私钥" });
    }
  });

export function toFormValues(config: MqttConfig): MqttConfigFormValues {
  return {
    ...config,
    passwordAction: config.passwordConfigured ? "keep" : "clear",
    password: "",
    clientPrivateKeyAction: config.clientPrivateKeyConfigured ? "keep" : "clear",
    clientPrivateKey: "",
  };
}

export function buildCommandQuery(filters: CommandFilterState, page: number, pageSize: number): MqttCommandListQuery {
  return {
    page,
    pageSize,
    status: filters.status === "all" ? undefined : filters.status,
    deviceId: filters.deviceId.trim() || undefined,
    commandId: filters.commandId.trim() || undefined,
    name: filters.name.trim() || undefined,
  };
}

export function getRuntimeStatusMeta(status?: string) {
  if (status && status in mqttRuntimeStatusMeta) return mqttRuntimeStatusMeta[status as MqttRuntimeStatus];
  return { label: status || "未知", tone: "neutral" as const };
}

export function getCommandStatusMeta(status?: string) {
  if (status && status in mqttCommandStatusMeta) return mqttCommandStatusMeta[status as MqttCommandStatus];
  return { label: status || "未知", tone: "neutral" as const };
}

export function formatOptionalTime(value?: string | null) {
  return value ? formatDateTime(value) : "-";
}

export function MqttPageLayout({ title, description, actions, children }: { title: string; description: string; actions?: ReactNode; children: ReactNode }) {
  return (
    <>
      <PageHeader title={title} description={description} actions={actions} />
      {children}
    </>
  );
}

export function ErrorPanel({ title, description, onRetry }: { title: string; description: string; onRetry: () => void }) {
  return (
    <div className="flex flex-col items-start gap-space-3 rounded-control border border-error-border bg-error-background px-space-4 py-space-4 sm:flex-row sm:items-center" role="alert">
      <XCircle className="h-5 w-5 shrink-0 text-error" aria-hidden />
      <div className="min-w-0 flex-1">
        <div className="font-medium text-error">{title}</div>
        <div className="mt-1 break-words text-body-secondary text-text-secondary">{description}</div>
      </div>
      <Button size="sm" variant="secondary" onClick={onRetry}>重试</Button>
    </div>
  );
}

export function ConfigLoading() {
  return <div className="space-y-space-4" aria-busy="true" aria-live="polite">{Array.from({ length: 4 }).map((_, index) => <div key={index} className="h-20 animate-pulse rounded-control bg-neutral-background" />)}</div>;
}

export function RuntimeLoading() {
  return <div className="grid gap-space-3 lg:grid-cols-4" aria-busy="true" aria-live="polite">{Array.from({ length: 4 }).map((_, index) => <div key={index} className="h-32 animate-pulse rounded-control bg-neutral-background" />)}</div>;
}

export function RuntimeOverview({ runtime, runtimeStatus, runtimeLoading, runtimeError, outbox, outboxLoading, outboxError, rawPendingCount, lastSuccessfulRefreshAt, onRefresh, onRefreshOutbox }: {
  runtime: MqttRuntimeState | null;
  runtimeStatus: ReturnType<typeof getRuntimeStatusMeta>;
  runtimeLoading: boolean;
  runtimeError: string;
  outbox: MqttOutboxStats | null;
  outboxLoading: boolean;
  outboxError: string;
  rawPendingCount: number;
  lastSuccessfulRefreshAt: number | null;
  onRefresh: () => void;
  onRefreshOutbox: () => void;
}) {
  const safeRuntimeError = runtime?.lastError ? getMqttErrorMessage(new Error(runtime.lastError), "最近一次连接失败") : "";
  const safeOutboxError = outbox?.lastError ? getMqttErrorMessage(new Error(outbox.lastError), "最近一次投递失败") : "";
  const hasStaleData = Boolean(runtimeError || outboxError) && Boolean(lastSuccessfulRefreshAt);
  return (
    <ContentCard
      title="运行总览"
      description="MQTT runtime 与 acquisition 状态独立；Broker 断线不应阻塞 Modbus 采集。"
      extra={<Button size="sm" variant="secondary" onClick={onRefresh} disabled={runtimeLoading && outboxLoading}><RefreshCw className="h-4 w-4" aria-hidden />刷新</Button>}
    >
      {hasStaleData && <div className="mb-space-4 flex flex-wrap items-center gap-space-2 rounded-control border border-warning-border bg-warning-background px-space-3 py-space-2 text-body-secondary text-warning" role="status"><AlertTriangle className="h-4 w-4" aria-hidden />数据可能已过期；最近成功刷新：{formatDateTime(new Date(lastSuccessfulRefreshAt as number).toISOString())}</div>}
      {runtimeError && !runtime ? <ErrorPanel title="运行状态加载失败" description={runtimeError} onRetry={onRefresh} /> : runtimeLoading && !runtime ? <RuntimeLoading /> : (
        <div className="grid gap-space-3 lg:grid-cols-[minmax(0,1.45fr)_repeat(3,minmax(0,1fr))]">
          <div className="rounded-control border border-border bg-neutral-background p-space-4">
            <div className="flex items-start justify-between gap-space-3"><div className="flex min-w-0 items-start gap-space-3"><div className="flex h-10 w-10 shrink-0 items-center justify-center rounded-control bg-surface text-primary shadow-subtle">{runtime?.connected ? <RadioTower className="h-5 w-5" aria-hidden /> : <WifiOff className="h-5 w-5" aria-hidden />}</div><div className="min-w-0"><div className="text-body-secondary text-text-tertiary">MQTT runtime</div><div className="mt-1 flex flex-wrap items-center gap-space-2"><StatusTag tone={runtimeStatus.tone}>{runtimeStatus.label}</StatusTag><span className="text-sm text-text-secondary">{runtime?.connected ? "Broker 已连接" : "Broker 未连接"}</span></div></div></div><Activity className="h-5 w-5 text-info" aria-hidden /></div>
            {runtimeError && <div className="mt-space-3 rounded-control border border-warning-border bg-warning-background px-space-3 py-space-2 text-body-secondary text-warning" role="alert">{runtimeError}</div>}
            {safeRuntimeError && <div className="mt-space-3 rounded-control border border-error-border bg-error-background px-space-3 py-space-2 text-body-secondary text-error" role="alert">{safeRuntimeError}</div>}
            <div className="mt-space-4 grid gap-space-2 text-body-secondary text-text-tertiary sm:grid-cols-2"><div>订阅：<code className="break-all text-text-secondary">{runtime?.subscriptionFilter || "-"}</code></div><div>重连次数：<span className="text-text-secondary">{runtime?.reconnectAttempts ?? 0}</span></div><div>最近连接：<span className="text-text-secondary">{formatOptionalTime(runtime?.lastConnectedAt)}</span></div><div>最近断开：<span className="text-text-secondary">{formatOptionalTime(runtime?.lastDisconnectedAt)}</span></div><div>下次重试：<span className="text-text-secondary">{formatOptionalTime(runtime?.nextRetryAt)}</span></div></div>
          </div>
          <OverviewMetric icon={<Inbox className="h-4 w-4" aria-hidden />} label="Reliable Outbox" value={outboxLoading && !outbox ? "加载中" : outboxError && !outbox ? "加载失败" : formatOutboxRows(outbox)} tone={outboxError ? "error" : "neutral"} />
          <OverviewMetric icon={<Clock3 className="h-4 w-4" aria-hidden />} label="最早可靠消息" value={outboxError && !outbox ? "加载失败" : formatOutboxAge(outbox)} tone={outboxError ? "error" : "neutral"} />
          <OverviewMetric icon={<Server className="h-4 w-4" aria-hidden />} label="Raw pending latest" value={String(rawPendingCount)} tone={rawPendingCount > 0 ? "info" : "neutral"} />
        </div>
      )}
      {outbox && !outboxError && <OutboxHealthSummary outbox={outbox} />}
      {outboxError && <div className="mt-space-3 flex flex-col items-start gap-space-2 rounded-control border border-warning-border bg-warning-background px-space-3 py-space-3 text-body-secondary text-warning sm:flex-row sm:items-center" role="alert"><AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" aria-hidden /><span className="flex-1">Reliable Outbox 区块加载失败：{outboxError}</span><Button size="sm" variant="secondary" onClick={onRefreshOutbox} disabled={outboxLoading}>重试</Button></div>}
      {safeOutboxError && <div className="mt-space-3 rounded-control border border-warning-border bg-warning-background px-space-3 py-space-2 text-body-secondary text-warning" role="alert">最近一次 Outbox 投递失败：{safeOutboxError}</div>}
    </ContentCard>
  );
}

function formatOutboxRows(outbox: MqttOutboxStats | null) {
  if (!outbox) return "-";
  return `${outbox.rows} 条 · ${formatMqttBytes(outbox.bytes)}`;
}

function formatOutboxAge(outbox: MqttOutboxStats | null) {
  if (!outbox || outbox.oldestAge <= 0) return "无积压";
  return `${outbox.oldestAge} 秒前`;
}

function OutboxHealthSummary({ outbox }: { outbox: MqttOutboxStats }) {
  return <div id="reliability" className="mt-space-4 rounded-control border border-border bg-neutral-background px-space-4 py-space-3"><div className="flex flex-wrap items-center justify-between gap-space-2"><div><div className="font-medium text-text-primary">Reliable Outbox 容量</div><div className="mt-1 text-body-secondary text-text-tertiary">FINAL result 不会因低优先级消息静默淘汰；容量由服务端准入控制。</div></div><StatusTag tone={outbox.rowUtilization >= 1 || outbox.byteUtilization >= 1 ? "error" : outbox.rowUtilization >= 0.8 || outbox.byteUtilization >= 0.8 ? "warning" : "success"}>{formatUtilization(Math.max(outbox.rowUtilization, outbox.byteUtilization))}</StatusTag></div><div className="mt-space-3 grid gap-space-3 md:grid-cols-2"><CapacityBar label="行容量" used={`${outbox.rows} / ${outbox.maxRows}`} utilization={outbox.rowUtilization} /><CapacityBar label="字节容量" used={`${formatMqttBytes(outbox.bytes)} / ${formatMqttBytes(outbox.maxBytes)}`} utilization={outbox.byteUtilization} /></div></div>;
}

function CapacityBar({ label, used, utilization }: { label: string; used: string; utilization: number }) {
  const safeUtilization = Number.isFinite(utilization) ? Math.max(utilization, 0) : 0;
  const width = Math.min(safeUtilization * 100, 100);
  const barClass = safeUtilization >= 1 ? "bg-error" : safeUtilization >= 0.8 ? "bg-warning" : "bg-primary";
  return <div><div className="flex items-center justify-between gap-space-2 text-body-secondary"><span className="text-text-secondary">{label}</span><span className="tabular-nums text-text-tertiary">{used} · {formatUtilization(safeUtilization)}</span></div><div className="mt-space-2 h-2 overflow-hidden rounded-pill bg-neutral-border" aria-hidden="true"><div className={`h-full rounded-pill ${barClass}`} style={{ width: `${width}%` }} /></div></div>;
}

function formatUtilization(value: number) {
  if (!Number.isFinite(value)) return "-";
  return `${Math.round(Math.max(value, 0) * 100)}%`;
}

function OverviewMetric({ icon, label, value, tone }: { icon: ReactNode; label: string; value: string; tone: "info" | "error" | "neutral" }) {
  const toneClass = tone === "info" ? "text-info" : tone === "error" ? "text-error" : "text-text-primary";
  return <div className="rounded-control border border-border bg-surface p-space-4"><div className="flex items-center justify-between gap-space-2 text-body-secondary text-text-tertiary"><span>{label}</span><span className={toneClass}>{icon}</span></div><div className={`mt-space-3 break-words text-lg font-semibold ${toneClass}`}>{value}</div></div>;
}

export function NumberField<T extends keyof MqttConfigFormValues>({ label, name, register, error, help, min, max, disabled }: { label: string; name: T; register: UseFormReturn<MqttConfigFormValues>["register"]; error?: string; help?: string; min: number; max: number; disabled: boolean }) {
  return <Field label={label} htmlFor={`mqtt-${String(name)}`} required error={error} help={help}><Input id={`mqtt-${String(name)}`} type="number" inputMode="numeric" min={min} max={max} step={1} disabled={disabled} {...register(name)} /></Field>;
}

export function SecretField({ label, actionName, valueName, configured, action, form, error, disabled, placeholder, multiline = false }: { label: string; actionName: "passwordAction" | "clientPrivateKeyAction"; valueName: "password" | "clientPrivateKey"; configured: boolean; action: string; form: UseFormReturn<MqttConfigFormValues>; error?: string; disabled: boolean; placeholder: string; multiline?: boolean }) {
  const { register } = form;
  return <Field label={label} htmlFor={`mqtt-${valueName}`} error={error} help="已配置值不会回读；保持或清除时请求体不携带 secret。"><div className="space-y-space-2"><div className="flex flex-wrap items-center gap-space-2"><StatusTag tone={configured ? "success" : "neutral"}>{configured ? "•••••••• · 已配置" : "未配置"}</StatusTag><Select aria-label={`${label}操作`} className="min-w-[160px] flex-1" disabled={disabled} {...register(actionName)}><option value="keep">保持已配置值</option><option value="set">设置新值</option><option value="clear">清除配置值</option></Select></div>{action === "set" && (multiline ? <Textarea id={`mqtt-${valueName}`} rows={6} spellCheck={false} placeholder={placeholder} disabled={disabled} className="font-mono text-xs leading-5" {...register(valueName)} /> : <Input id={`mqtt-${valueName}`} type="password" autoComplete="new-password" placeholder={placeholder} disabled={disabled} {...register(valueName)} />)}</div></Field>;
}

export function ConfigDisclosure({ storageKey, title, description, children }: { storageKey: string; title: string; description: string; children: ReactNode }) {
  const [open, setOpen] = useState(() => typeof window !== "undefined" && window.sessionStorage.getItem(storageKey) === "true");
  useEffect(() => {
    if (typeof window !== "undefined") window.sessionStorage.setItem(storageKey, String(open));
  }, [open, storageKey]);
  return <div><button type="button" className="flex w-full items-center justify-between rounded-admin border border-border bg-surface px-card py-space-4 text-left shadow-admin hover:bg-neutral-background" aria-expanded={open} onClick={() => setOpen((value) => !value)}><span><span className="block text-card-title font-semibold text-text-primary">{title}</span><span className="mt-1 block text-body-secondary text-text-tertiary">{description}</span></span><span className={`text-xl text-text-tertiary transition-transform ${open ? "rotate-180" : ""}`} aria-hidden>⌄</span></button>{open && <div className="mt-space-3"><FormSection title={title} description={description}>{children}</FormSection></div>}</div>;
}

export function CommandDetailDialog({ open, detail, loading, errorMessage, onCancel }: { open: boolean; detail: MqttCommandJournal | null; loading: boolean; errorMessage: string; onCancel: () => void }) {
  const status = getCommandStatusMeta(detail?.status);
  return <DetailDialog open={open} title="Command Journal 详情" description={loading ? "详情加载中" : `Command ID：${detail?.commandId ?? "-"}`} loading={loading} contentClassName="max-w-[860px]" closeOnEscape={false} closeOnOverlayClick={false} onCancel={onCancel}><div className="space-y-space-5"><div className="flex items-start gap-space-3 rounded-control border border-info-border bg-info-background px-space-4 py-space-3 text-sm text-info"><KeyRound className="mt-0.5 h-4 w-4 shrink-0" aria-hidden /><div><div className="font-medium">幂等事实源</div><div className="mt-1 text-body-secondary text-text-secondary">相同 commandId 不会创建第二个真实执行；详情只展示安全的状态元数据。</div></div></div><div className="grid gap-space-4 md:grid-cols-3"><DetailValue label="设备" value={detail?.deviceId} /><DetailValue label="命令名称" value={detail?.name} /><DetailValue label="状态" value={detail && <StatusTag tone={status.tone}>{status.label}</StatusTag>} /><DetailValue label="接收时间" value={formatOptionalTime(detail?.receivedAt)} /><DetailValue label="签发时间" value={formatOptionalTime(detail?.issuedAt)} /><DetailValue label="过期时间" value={formatOptionalTime(detail?.expiresAt)} /><DetailValue label="开始时间" value={formatOptionalTime(detail?.startedAt)} /><DetailValue label="完成时间" value={formatOptionalTime(detail?.completedAt)} /><DetailValue label="错误类型" value={detail?.errorType} />{errorMessage && <DetailValue label="错误信息" value={errorMessage} className="md:col-span-3" />}</div></div></DetailDialog>;
}

function DetailValue({ label, value, className }: { label: string; value?: ReactNode; className?: string }) {
  return <div className={className}><div className="text-body-secondary text-text-tertiary">{label}</div><div className="mt-space-1 break-words text-sm text-text-primary">{value ?? "-"}</div></div>;
}

export function CommandStatusGuard({ children, fallback }: { children: ReactNode; fallback?: ReactNode }) {
  return <PermissionGuard permissionCode="mqtt:command:detail" fallback={fallback ?? <EmptyState title="无权查看 Command 详情" description="当前账号没有 Command Journal 详情权限。" />}>{children}</PermissionGuard>;
}

export type MqttCommandColumns = DataTableColumn<MqttCommandJournal>[];
