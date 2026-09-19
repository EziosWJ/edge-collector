import {
  Clipboard,
  Link2Off,
  Pause,
  Play,
  Trash2,
  Wifi,
  WifiOff,
} from "lucide-react";
import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type FormEvent,
} from "react";
import { PermissionGuard } from "@/components/auth/permission-guard";
import { ContentCard } from "@/components/common/content-card";
import { DetailDialog } from "@/components/common/detail-dialog";
import { EmptyState } from "@/components/common/empty-state";
import { Field } from "@/components/common/field";
import { StatusTag } from "@/components/common/status-tag";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { formatDateTime } from "@/lib/datetime";
import {
  appendMonitorMessage,
  buildMonitorSubscriptions,
  createEmptyMonitorBuffer,
  formatMonitorBytes,
  getStoredMonitorSettings,
  isValidMonitorTopicPrefix,
  isValidMonitorTopicSegment,
  MQTT_MONITOR_MAX_BUFFER_BYTES,
  MQTT_MONITOR_MAX_MESSAGES,
  MQTT_MONITOR_MESSAGE_TYPES,
  parseMonitorMessage,
  storeMonitorSettings,
  type MqttMonitorClientFactory,
  type MqttMonitorMessage,
  type MqttMonitorMessageType,
  type MqttMonitorProtocol,
  type MqttMonitorSettings,
} from "@/lib/mqtt-monitor";
import { createMqttMonitorClient } from "@/lib/mqtt-monitor-client";
import { cn } from "@/lib/utils";
import { MqttPageLayout } from "./shared";

type ConnectionState = "disconnected" | "connecting" | "connected" | "reconnecting" | "error";

type ViteMonitorImportMeta = ImportMeta & {
  env?: {
    VITE_MQTT_WS_URL?: string;
    VITE_MQTT_TOPIC_PREFIX?: string;
    VITE_MQTT_EDGE_ID?: string;
  };
};

type MonitorFilters = {
  type: "all" | MqttMonitorMessageType;
  deviceId: string;
  topic: string;
  schema: string;
  diagnosticsOnly: boolean;
};

const defaultSettings: MqttMonitorSettings = {
  wsUrl: (import.meta as ViteMonitorImportMeta).env?.VITE_MQTT_WS_URL || "ws://localhost:9001",
  topicPrefix: (import.meta as ViteMonitorImportMeta).env?.VITE_MQTT_TOPIC_PREFIX || "edge",
  edgeId: (import.meta as ViteMonitorImportMeta).env?.VITE_MQTT_EDGE_ID || "edge-01",
  protocol: "MQTT_5",
  username: "",
  password: "",
  selectedTypes: MQTT_MONITOR_MESSAGE_TYPES.map((item) => item.value),
};

const defaultFilters: MonitorFilters = {
  type: "all",
  deviceId: "",
  topic: "",
  schema: "",
  diagnosticsOnly: false,
};

const statusMeta: Record<ConnectionState, { label: string; tone: "success" | "warning" | "error" | "neutral" }> = {
  disconnected: { label: "未连接", tone: "neutral" },
  connecting: { label: "连接中", tone: "warning" },
  connected: { label: "已连接", tone: "success" },
  reconnecting: { label: "重连中", tone: "warning" },
  error: { label: "连接错误", tone: "error" },
};

function makeClientId() {
  return `edge-monitor-${Math.random().toString(36).slice(2, 10)}`;
}

function isTerminalConnectionError(error: Error) {
  return /not authorized|unauthorized|bad user|identifier rejected|protocol error|bad protocol|client id/i.test(error.message);
}

function safeConnectionError(error: Error) {
  return error.message
    .replace(/(password|passwd|secret|token)=?[^\s&]*/gi, "$1=[已隐藏]")
    .slice(0, 240);
}

function isAtTop(element: HTMLElement | null) {
  return !element || element.scrollTop <= 8;
}

export function MqttMonitorPage({ clientFactory = createMqttMonitorClient }: { clientFactory?: MqttMonitorClientFactory }) {
  const [settings, setSettings] = useState<MqttMonitorSettings>(() => {
    const stored = getStoredMonitorSettings({
      wsUrl: defaultSettings.wsUrl,
      topicPrefix: defaultSettings.topicPrefix,
      edgeId: defaultSettings.edgeId,
      protocol: defaultSettings.protocol,
      username: defaultSettings.username,
      selectedTypes: defaultSettings.selectedTypes,
    });
    return { ...defaultSettings, ...stored, password: "" };
  });
  const [connectionState, setConnectionState] = useState<ConnectionState>("disconnected");
  const [connectionError, setConnectionError] = useState("");
  const [buffer, setBuffer] = useState(createEmptyMonitorBuffer);
  const [paused, setPaused] = useState(false);
  const [filters, setFilters] = useState<MonitorFilters>(defaultFilters);
  const [selectedMessage, setSelectedMessage] = useState<MqttMonitorMessage | null>(null);
  const [newMessageCount, setNewMessageCount] = useState(0);
  const [copiedField, setCopiedField] = useState<"topic" | "payload" | null>(null);
  const clientRef = useRef<ReturnType<MqttMonitorClientFactory> | null>(null);
  const listenersRef = useRef<Array<() => void>>([]);
  const currentTopicsRef = useRef<string[]>([]);
  const manualDisconnectRef = useRef(false);
  const terminalErrorRef = useRef(false);
  const settingsRef = useRef(settings);
  const pausedRef = useRef(paused);
  const messageListRef = useRef<HTMLDivElement>(null);
  const atTopRef = useRef(true);

  useEffect(() => {
    settingsRef.current = settings;
    pausedRef.current = paused;
    storeMonitorSettings({
      wsUrl: settings.wsUrl,
      topicPrefix: settings.topicPrefix,
      edgeId: settings.edgeId,
      protocol: settings.protocol,
      username: settings.username,
      selectedTypes: settings.selectedTypes,
    });
  }, [settings, paused]);

  const removeClientListeners = useCallback(() => {
    listenersRef.current.forEach((remove) => remove());
    listenersRef.current = [];
  }, []);

  const destroyClient = useCallback((force = true) => {
    removeClientListeners();
    currentTopicsRef.current = [];
    const client = clientRef.current;
    clientRef.current = null;
    client?.end(force);
  }, [removeClientListeners]);

  const syncSubscriptions = useCallback(() => {
    const client = clientRef.current;
    if (!client || connectionState !== "connected") return;
    const desired = buildMonitorSubscriptions(settingsRef.current).map((item) => item.topic);
    const current = currentTopicsRef.current;
    current.filter((topic) => !desired.includes(topic)).forEach((topic) => client.unsubscribe(topic));
    desired.filter((topic) => !current.includes(topic)).forEach((topic) => {
      client.subscribe(topic, (error) => {
        if (error) setConnectionError(`订阅失败：${safeConnectionError(error)}`);
      });
    });
    currentTopicsRef.current = desired;
  }, [connectionState]);

  useEffect(() => {
    syncSubscriptions();
  }, [settings.edgeId, settings.topicPrefix, settings.selectedTypes, syncSubscriptions]);

  const disconnect = useCallback(() => {
    manualDisconnectRef.current = true;
    terminalErrorRef.current = false;
    destroyClient(true);
    setConnectionState("disconnected");
  }, [destroyClient]);

  const connect = useCallback(() => {
    const current = settingsRef.current;
    if (!current.wsUrl.trim()) {
      setConnectionError("请输入 WebSocket Broker 地址。");
      setConnectionState("error");
      return;
    }
    if (!isValidMonitorTopicPrefix(current.topicPrefix) || !isValidMonitorTopicSegment(current.edgeId.trim())) {
      setConnectionError("Topic 前缀和 Edge ID 不能包含空段、斜线、MQTT 通配符或控制字符。");
      setConnectionState("error");
      return;
    }
    if (window.location.protocol === "https:" && current.wsUrl.trim().startsWith("ws://")) {
      setConnectionError("HTTPS 页面不能连接 ws://，请使用 wss://。");
      setConnectionState("error");
      return;
    }

    manualDisconnectRef.current = false;
    terminalErrorRef.current = false;
    destroyClient(true);
    setConnectionError("");
    setConnectionState("connecting");
    const client = clientFactory({ ...current, clientId: makeClientId() });
    clientRef.current = client;

    const addListener = <K extends keyof import("@/lib/mqtt-monitor").MqttMonitorClientEventMap>(
      event: K,
      listener: import("@/lib/mqtt-monitor").MqttMonitorClientEventMap[K],
    ) => {
      client.on(event, listener);
      listenersRef.current.push(() => client.off(event, listener));
    };

    addListener("connect", () => {
      setConnectionState("connected");
      setConnectionError("");
    });
    addListener("reconnect", () => setConnectionState("reconnecting"));
    addListener("offline", () => setConnectionState("reconnecting"));
    addListener("error", (error) => {
      const message = safeConnectionError(error);
      setConnectionError(message || "MQTT 连接失败。");
      if (isTerminalConnectionError(error)) {
        terminalErrorRef.current = true;
        client.end(true);
        setConnectionState("error");
      }
    });
    addListener("close", () => {
      if (!manualDisconnectRef.current && !terminalErrorRef.current) setConnectionState("reconnecting");
    });
    addListener("end", () => {
      if (manualDisconnectRef.current) setConnectionState("disconnected");
    });
    addListener("message", (topic, payload, packet) => {
      const message = parseMonitorMessage({ topic, payload, packet, settings: settingsRef.current });
      setBuffer((currentBuffer) => appendMonitorMessage(currentBuffer, message, { paused: pausedRef.current }));
      if (!pausedRef.current && !atTopRef.current) setNewMessageCount((count) => count + 1);
    });
  }, [clientFactory, destroyClient]);

  useEffect(() => () => {
    manualDisconnectRef.current = true;
    destroyClient(true);
  }, [destroyClient]);

  const visibleMessages = useMemo(() => {
    const query = filters.topic.trim().toLocaleLowerCase("zh-CN");
    const deviceQuery = filters.deviceId.trim().toLocaleLowerCase("zh-CN");
    const schemaQuery = filters.schema.trim().toLocaleLowerCase("zh-CN");
    return buffer.messages.filter((message) => {
      if (filters.type !== "all" && message.type !== filters.type) return false;
      if (deviceQuery && !(message.deviceId ?? "").toLocaleLowerCase("zh-CN").includes(deviceQuery)) return false;
      if (query && !message.topic.toLocaleLowerCase("zh-CN").includes(query)) return false;
      if (schemaQuery && !(message.schema ?? "").toLocaleLowerCase("zh-CN").includes(schemaQuery)) return false;
      if (filters.diagnosticsOnly && message.diagnostic === "ok") return false;
      return true;
    });
  }, [buffer.messages, filters]);

  const handleConnect = (event: FormEvent) => {
    event.preventDefault();
    connect();
  };

  const updateSettings = <K extends keyof MqttMonitorSettings>(key: K, value: MqttMonitorSettings[K]) => {
    setSettings((current) => ({ ...current, [key]: value }));
  };

  const toggleType = (type: Exclude<MqttMonitorMessageType, "unknown">) => {
    setSettings((current) => {
      const selected = current.selectedTypes.includes(type)
        ? current.selectedTypes.filter((item) => item !== type)
        : [...current.selectedTypes, type];
      return { ...current, selectedTypes: selected };
    });
  };

  const clearBuffer = () => {
    setBuffer(createEmptyMonitorBuffer());
    setSelectedMessage(null);
    setNewMessageCount(0);
  };

  const copyValue = async (field: "topic" | "payload", value: string) => {
    try {
      await navigator.clipboard.writeText(value);
      setCopiedField(field);
      window.setTimeout(() => setCopiedField(null), 1500);
    } catch {
      setConnectionError("复制失败，请检查浏览器剪贴板权限。");
    }
  };

  const status = statusMeta[connectionState];

  return (
    <PermissionGuard
      permissionCode="mqtt:monitor:list"
      fallback={<EmptyState title="无权查看 MQTT 消息监控" description="当前账号没有 MQTT 消息监控权限。" />}
    >
      <div data-testid="mqtt-monitor-page" className="min-w-0 space-y-space-4">
        <MqttPageLayout
          title="MQTT 消息监控"
          description="只读观察 Edge Collector 发布的 MQTT 上报；不会订阅或发送控制指令。"
          actions={
            <div className="flex flex-wrap items-center gap-space-2">
              <div className="flex h-control-sm items-center gap-1.5 rounded-control bg-neutral-background px-space-2 text-xs" role="status" aria-label={`MQTT 连接状态：${status.label}`}>
                <span className={cn("h-2 w-2 rounded-full", status.tone === "success" ? "bg-success" : status.tone === "warning" ? "bg-warning" : status.tone === "error" ? "bg-error" : "bg-text-tertiary")} aria-hidden />
                {status.label}
              </div>
            </div>
          }
        >

        <form onSubmit={handleConnect}>
          <ContentCard title="只读连接" description="使用专门的 Broker 只读账号；密码只保存在当前页面内存。" extra={<StatusTag tone={status.tone}>{status.label}</StatusTag>}>
            <div className="grid gap-space-4 md:grid-cols-2 xl:grid-cols-4">
              <div className="md:col-span-2 xl:col-span-2">
                <Field label="WebSocket Broker 地址" htmlFor="mqtt-monitor-url" required help="例如 ws://localhost:9001 或 wss://broker.example.com/mqtt">
                  <Input id="mqtt-monitor-url" value={settings.wsUrl} onChange={(event) => updateSettings("wsUrl", event.target.value)} placeholder="ws://localhost:9001" disabled={connectionState === "connected" || connectionState === "connecting" || connectionState === "reconnecting"} />
                </Field>
              </div>
              <Field label="Topic 前缀" htmlFor="mqtt-monitor-prefix" required>
                <Input id="mqtt-monitor-prefix" value={settings.topicPrefix} onChange={(event) => updateSettings("topicPrefix", event.target.value)} disabled={connectionState !== "disconnected" && connectionState !== "error"} />
              </Field>
              <Field label="Edge ID" htmlFor="mqtt-monitor-edge-id" required>
                <Input id="mqtt-monitor-edge-id" value={settings.edgeId} onChange={(event) => updateSettings("edgeId", event.target.value)} disabled={connectionState !== "disconnected" && connectionState !== "error"} />
              </Field>
              <Field label="协议版本" htmlFor="mqtt-monitor-protocol" required>
                <Select id="mqtt-monitor-protocol" value={settings.protocol} onChange={(event) => updateSettings("protocol", event.target.value as MqttMonitorProtocol)} disabled={connectionState !== "disconnected" && connectionState !== "error"}>
                  <option value="MQTT_5">MQTT 5（默认）</option>
                  <option value="MQTT_3_1_1">MQTT 3.1.1</option>
                </Select>
              </Field>
              <Field label="用户名" htmlFor="mqtt-monitor-username" help="建议使用 Broker 只读监控账号。">
                <Input id="mqtt-monitor-username" autoComplete="username" value={settings.username} onChange={(event) => updateSettings("username", event.target.value)} disabled={connectionState !== "disconnected" && connectionState !== "error"} />
              </Field>
              <Field label="密码" htmlFor="mqtt-monitor-password" help="不会写入 URL、sessionStorage 或日志。">
                <Input id="mqtt-monitor-password" type="password" autoComplete="new-password" value={settings.password} onChange={(event) => updateSettings("password", event.target.value)} disabled={connectionState !== "disconnected" && connectionState !== "error"} />
              </Field>
            </div>
            <fieldset className="mt-space-2">
              <legend className="text-form-label font-medium text-text-primary">订阅类型</legend>
              <div className="mt-space-2 flex flex-wrap gap-space-2">
                {MQTT_MONITOR_MESSAGE_TYPES.map((item) => (
                  <label key={item.value} className="inline-flex cursor-pointer items-center gap-2 rounded-control border border-border px-space-3 py-space-2 text-sm hover:bg-neutral-background">
                    <input type="checkbox" checked={settings.selectedTypes.includes(item.value)} onChange={() => toggleType(item.value)} disabled={connectionState === "connecting" || connectionState === "reconnecting"} />
                    {item.label}
                  </label>
                ))}
              </div>
            </fieldset>
            <div className="mt-space-3 flex flex-wrap items-center gap-space-2 border-t border-border pt-space-4">
              {connectionState === "connected" || connectionState === "connecting" || connectionState === "reconnecting" ? (
                <Button key="disconnect" type="button" variant="secondary" onClick={disconnect}><WifiOff className="h-4 w-4" aria-hidden />断开</Button>
              ) : (
                <Button key="connect" type="submit" variant="primary"><Wifi className="h-4 w-4" aria-hidden />连接 Broker</Button>
              )}
              <span className="text-helper text-text-tertiary">当前会话使用独立 client ID，连接关闭后不保留离线消息。</span>
            </div>
            {connectionError && <div className="mt-space-3 flex items-start gap-space-2 rounded-control border border-error-border bg-error-background px-space-3 py-space-2 text-sm text-error" role="alert"><Link2Off className="mt-0.5 h-4 w-4 shrink-0" aria-hidden /><span className="break-words">{connectionError}</span></div>}
          </ContentCard>
        </form>

        <ContentCard
          title="消息流"
          description="消息保存在当前页面内存，筛选不会改变 Broker 订阅。"
          extra={
            <div className="flex max-w-full flex-wrap items-center justify-end gap-space-2">
              <div className="flex flex-wrap items-center gap-space-2">
                <Button variant="secondary" onClick={() => setPaused((value) => !value)} disabled={connectionState === "disconnected"}>
                  {paused ? <Play className="h-4 w-4" aria-hidden /> : <Pause className="h-4 w-4" aria-hidden />}
                  {paused ? "继续接收" : "暂停显示"}
                </Button>
                <Button variant="secondary" onClick={clearBuffer} disabled={buffer.messages.length === 0}>
                  <Trash2 className="h-4 w-4" aria-hidden />清空消息
                </Button>
              </div>
              <div className="flex flex-wrap items-center gap-x-space-2 gap-y-1 text-helper text-text-tertiary">
                <span>接收 {buffer.stats.received}</span>
                <span>保留 {buffer.messages.length}/{MQTT_MONITOR_MAX_MESSAGES}</span>
                <span>占用 {formatMonitorBytes(buffer.stats.bytes)}/{formatMonitorBytes(MQTT_MONITOR_MAX_BUFFER_BYTES)}</span>
                <span>淘汰 {buffer.stats.evicted}</span>
                <span>跳过 {buffer.stats.skipped}</span>
                <span>超限 {buffer.stats.oversized}</span>
              </div>
            </div>
          }
        >
          <div className="grid gap-space-3 md:grid-cols-2 xl:grid-cols-[180px_180px_minmax(200px,1fr)_minmax(180px,1fr)_auto]">
            <Select aria-label="消息类型筛选" value={filters.type} onChange={(event) => setFilters((current) => ({ ...current, type: event.target.value as MonitorFilters["type"] }))}>
              <option value="all">全部类型</option>
              {MQTT_MONITOR_MESSAGE_TYPES.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)}
            </Select>
            <Input aria-label="Device ID 筛选" placeholder="Device ID" value={filters.deviceId} onChange={(event) => setFilters((current) => ({ ...current, deviceId: event.target.value }))} />
            <Input aria-label="Topic 筛选" placeholder="Topic 关键词" value={filters.topic} onChange={(event) => setFilters((current) => ({ ...current, topic: event.target.value }))} />
            <Input aria-label="Schema 筛选" placeholder="Schema" value={filters.schema} onChange={(event) => setFilters((current) => ({ ...current, schema: event.target.value }))} />
            <label className="inline-flex h-control-md items-center gap-2 whitespace-nowrap text-sm text-text-secondary"><input type="checkbox" checked={filters.diagnosticsOnly} onChange={(event) => setFilters((current) => ({ ...current, diagnosticsOnly: event.target.checked }))} />仅异常</label>
          </div>
          {newMessageCount > 0 && <div className="mt-space-3 flex items-center justify-between rounded-control border border-info-border bg-info-background px-space-3 py-space-2 text-sm text-info" role="status"><span>收到 {newMessageCount} 条新消息</span><Button size="sm" variant="ghost" onClick={() => { messageListRef.current?.scrollTo({ top: 0, behavior: "smooth" }); setNewMessageCount(0); }}>回到最新</Button></div>}
          <div ref={messageListRef} onScroll={(event) => { const top = isAtTop(event.currentTarget); atTopRef.current = top; if (top) setNewMessageCount(0); }} className="mt-space-4 max-h-[60vh] overflow-auto rounded-control border border-border" data-testid="mqtt-monitor-message-list">
            {visibleMessages.length === 0 ? (
              <EmptyState title={connectionState === "disconnected" ? "尚未连接 Broker" : "暂无匹配消息"} description={connectionState === "disconnected" ? "填写连接信息后建立只读 MQTT 会话。" : "等待上行消息，或调整当前筛选条件。"} />
            ) : (
              <table className="w-full min-w-[1100px] border-separate border-spacing-0 text-left text-sm">
                <thead className="sticky top-0 z-10 bg-neutral-background text-text-secondary">
                  <tr>
                    <th className="border-b border-border px-space-3 py-space-3 font-medium">接收时间</th>
                    <th className="border-b border-border px-space-3 py-space-3 font-medium">类型 / 状态</th>
                    <th className="border-b border-border px-space-3 py-space-3 font-medium">Device ID</th>
                    <th className="border-b border-border px-space-3 py-space-3 font-medium">Topic</th>
                    <th className="border-b border-border px-space-3 py-space-3 font-medium">投递</th>
                    <th className="border-b border-border px-space-3 py-space-3 font-medium">Payload 时间</th>
                    <th className="border-b border-border px-space-3 py-space-3 text-right font-medium">详情</th>
                  </tr>
                </thead>
                <tbody>
                  {visibleMessages.map((message) => (
                    <tr key={message.id} className="hover:bg-neutral-background">
                      <td className="border-b border-border px-space-3 py-space-3 align-top whitespace-nowrap text-text-secondary">{formatDateTime(message.receivedAt)}</td>
                      <td className="border-b border-border px-space-3 py-space-3 align-top"><div className="font-medium text-text-primary">{message.label}</div><StatusTag tone={message.diagnostic === "ok" ? "success" : message.diagnostic === "oversized" ? "warning" : "error"}>{diagnosticLabel(message.diagnostic)}</StatusTag></td>
                      <td className="max-w-[160px] border-b border-border px-space-3 py-space-3 align-top text-text-secondary">{message.deviceId || "—"}</td>
                      <td className="max-w-[360px] border-b border-border px-space-3 py-space-3 align-top"><code className="break-all text-xs text-text-secondary">{message.topic}</code></td>
                      <td className="border-b border-border px-space-3 py-space-3 align-top whitespace-nowrap text-xs text-text-tertiary">QoS {message.qos} · {message.retain ? "Retain" : "非 Retain"}{message.dup ? " · Duplicate" : ""}<br />{formatMonitorBytes(message.byteLength)}</td>
                      <td className="border-b border-border px-space-3 py-space-3 align-top whitespace-nowrap text-text-secondary">{formatDateTime(message.payloadTimestamp)}</td>
                      <td className="border-b border-border px-space-3 py-space-3 text-right align-top"><Button size="sm" variant="ghost" onClick={() => setSelectedMessage(message)}>查看</Button></td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </div>
        </ContentCard>

        <DetailDialog open={Boolean(selectedMessage)} title={selectedMessage ? `${selectedMessage.label} · 消息详情` : "消息详情"} description={selectedMessage ? `${formatDateTime(selectedMessage.receivedAt)} · ${selectedMessage.topic}` : undefined} onCancel={() => setSelectedMessage(null)} contentClassName="max-w-[960px]" bodyClassName="space-y-space-4">
          {selectedMessage && <MessageDetail message={selectedMessage} copiedField={copiedField} onCopy={copyValue} />}
        </DetailDialog>
        </MqttPageLayout>
      </div>
    </PermissionGuard>
  );
}

function diagnosticLabel(diagnostic: MqttMonitorMessage["diagnostic"]) {
  const labels: Record<MqttMonitorMessage["diagnostic"], string> = {
    ok: "正常",
    json_parse_error: "JSON 解析失败",
    unknown_schema: "未知 Schema",
    missing_envelope: "Envelope 不完整",
    identity_mismatch: "标识不一致",
    topic_out_of_scope: "Topic 不在监控范围",
    invalid_timestamp: "时间非法",
    oversized: "Payload 过大",
  };
  return labels[diagnostic];
}

function MessageDetail({ message, copiedField, onCopy }: { message: MqttMonitorMessage; copiedField: "topic" | "payload" | null; onCopy: (field: "topic" | "payload", value: string) => void }) {
  const prettyPayload = typeof message.parsedPayload === "undefined" ? message.rawPayload : JSON.stringify(message.parsedPayload, null, 2);
  return (
    <>
      <div className="grid gap-space-3 text-sm sm:grid-cols-2">
        <DetailValue label="Schema" value={message.schema || "—"} />
        <DetailValue label="Device ID" value={message.deviceId || "—"} />
        <DetailValue label="接收时间" value={formatDateTime(message.receivedAt)} />
        <DetailValue label="Payload 时间" value={formatDateTime(message.payloadTimestamp)} />
        <DetailValue label="投递" value={`QoS ${message.qos} · ${message.retain ? "Retain" : "非 Retain"}${message.dup ? " · Duplicate" : ""}`} />
        <DetailValue label="解析状态" value={diagnosticLabel(message.diagnostic)} />
      </div>
      <div>
        <div className="mb-space-2 flex items-center justify-between gap-space-3"><span className="text-form-label font-medium">Topic</span><Button size="sm" variant="ghost" onClick={() => onCopy("topic", message.topic)}><Clipboard className="h-4 w-4" aria-hidden />{copiedField === "topic" ? "已复制" : "复制"}</Button></div>
        <code className="block break-all rounded-control bg-neutral-background p-space-3 text-xs text-text-secondary">{message.topic}</code>
      </div>
      <div>
        <div className="mb-space-2 flex items-center justify-between gap-space-3"><span className="text-form-label font-medium">Payload</span><Button size="sm" variant="ghost" onClick={() => onCopy("payload", message.rawPayload)}><Clipboard className="h-4 w-4" aria-hidden />{copiedField === "payload" ? "已复制" : "复制"}</Button></div>
        <pre className="max-h-[42vh] overflow-auto whitespace-pre-wrap break-words rounded-control bg-neutral-background p-space-3 text-xs leading-5 text-text-secondary">{prettyPayload}</pre>
      </div>
    </>
  );
}

function DetailValue({ label, value }: { label: string; value: string }) {
  return <div><div className="text-helper text-text-tertiary">{label}</div><div className="mt-1 break-words text-text-secondary">{value}</div></div>;
}
