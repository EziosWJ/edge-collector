import { zodResolver } from "@hookform/resolvers/zod";
import {
  Activity,
  AlertTriangle,
  Clock3,
  Eye,
  Inbox,
  KeyRound,
  RadioTower,
  RefreshCw,
  RotateCcw,
  Save,
  Search,
  Server,
  ShieldCheck,
  WifiOff,
  XCircle,
} from "lucide-react";
import { useCallback, useEffect, useMemo, useState } from "react";
import {
  Controller,
  useForm,
  useWatch,
  type UseFormReturn,
} from "react-hook-form";
import { z } from "zod";
import {
  getMqttCommand,
  getMqttCommands,
  getMqttConfig,
  getMqttOutboxStats,
  getMqttState,
  testMqttConnection,
  updateMqttConfig,
} from "@/api/mqtt";
import { PermissionGuard } from "@/components/auth/permission-guard";
import { ContentCard } from "@/components/common/content-card";
import { DataTable } from "@/components/common/data-table";
import { DataTableCard } from "@/components/common/data-table-card";
import { DetailDialog } from "@/components/common/detail-dialog";
import { EmptyState } from "@/components/common/empty-state";
import { Field } from "@/components/common/field";
import { FormSection } from "@/components/common/form-section";
import { PageHeader } from "@/components/common/page-header";
import { Pagination } from "@/components/common/pagination";
import { SearchFilterBar } from "@/components/common/search-filter-bar";
import { StatusTag } from "@/components/common/status-tag";
import { TableToolbar } from "@/components/common/table-toolbar";
import { toast } from "@/components/common/toast-store";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import { useListPage } from "@/hooks/use-list-page";
import { getMqttErrorMessage, buildMqttConfigUpdate, formatMqttBytes } from "@/lib/mqtt";
import { formatDateTime } from "@/lib/datetime";
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
import type { MqttConfigFormValues } from "@/lib/mqtt";
import {
  mqttCommandStatusMeta,
  mqttRuntimeStatusMeta,
} from "@/types";

const commandStatuses: Array<{ value: MqttCommandStatus; label: string }> = [
  { value: "ACCEPTED", label: "已接收" },
  { value: "RUNNING", label: "执行中" },
  { value: "SUCCEEDED", label: "执行成功" },
  { value: "FAILED", label: "执行失败" },
  { value: "REJECTED", label: "已拒绝" },
  { value: "EXPIRED", label: "已过期" },
];

const DEFAULT_CONFIG: MqttConfig = {
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

const DEFAULT_COMMAND_FILTERS: CommandFilterState = {
  status: "all",
  deviceId: "",
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

const mqttConfigSchema = z
  .object({
    enabled: z.boolean(),
    edgeId: z
      .string()
      .trim()
      .min(1, "Edge ID 不能为空")
      .max(128, "Edge ID 不能超过 128 个字符")
      .refine(
        isValidTopicSegment,
        "Edge ID 不能包含 MQTT 通配符、斜线或控制字符",
      ),
    brokerUrl: z
      .string()
      .trim()
      .min(1, "Broker 地址不能为空")
      .max(2048, "Broker 地址不能超过 2048 个字符")
      .refine((value) => {
        try {
          const url = new URL(value);
          return (
            ["mqtt:", "mqtts:", "tcp:", "tls:", "ssl:", "ws:", "wss:"].includes(
              url.protocol,
            ) && Boolean(url.hostname)
            && !url.username
            && !url.password
          );
        } catch {
          return false;
        }
      }, "请输入带协议和主机的 Broker 地址"),
    protocolVersion: z.enum(["MQTT_5", "MQTT_3_1_1"], {
      errorMap: () => ({ message: "请选择 MQTT 协议版本" }),
    }),
    clientId: z
      .string()
      .trim()
      .min(1, "Client ID 不能为空")
      .max(256, "Client ID 不能超过 256 个字符"),
    username: z.string().max(256, "用户名不能超过 256 个字符"),
    passwordConfigured: z.boolean(),
    passwordAction: z.enum(["keep", "set", "clear"]),
    password: z.string().max(4096, "密码不能超过 4096 个字符"),
    tlsEnabled: z.boolean(),
    caCertificate: z.string().max(1024 * 1024, "CA 证书不能超过 1 MiB"),
    clientCertificate: z
      .string()
      .max(1024 * 1024, "客户端证书不能超过 1 MiB"),
    clientPrivateKeyConfigured: z.boolean(),
    clientPrivateKeyAction: z.enum(["keep", "set", "clear"]),
    clientPrivateKey: z
      .string()
      .max(1024 * 1024, "客户端私钥不能超过 1 MiB"),
    keepAliveSeconds: z.coerce
      .number()
      .int("Keep Alive 必须是整数")
      .min(5, "Keep Alive 范围为 5～3600 秒")
      .max(3600, "Keep Alive 范围为 5～3600 秒"),
    connectTimeoutMs: z.coerce
      .number()
      .int("连接超时必须是整数")
      .min(100, "连接超时范围为 100～120000 毫秒")
      .max(120000, "连接超时范围为 100～120000 毫秒"),
    reconnectMinMs: z.coerce
      .number()
      .int("最小重连间隔必须是整数")
      .min(100, "最小重连间隔范围为 100～60000 毫秒")
      .max(60000, "最小重连间隔范围为 100～60000 毫秒"),
    reconnectMaxMs: z.coerce
      .number()
      .int("最大重连间隔必须是整数")
      .min(100, "最大重连间隔范围为 100～600000 毫秒")
      .max(600000, "最大重连间隔范围为 100～600000 毫秒"),
    topicPrefix: z
      .string()
      .trim()
      .min(1, "Topic 前缀不能为空")
      .max(256, "Topic 前缀不能超过 256 个字符")
      .refine(
        (value) => value.split("/").every(isValidTopicSegment),
        "Topic 前缀不能包含空段、通配符或控制字符",
      ),
    rawPublishIntervalMs: z.coerce
      .number()
      .int("Raw 上报间隔必须是整数")
      .min(50, "Raw 上报间隔范围为 50～3600000 毫秒")
      .max(3600000, "Raw 上报间隔范围为 50～3600000 毫秒"),
    outboxMaxRows: z.coerce
      .number()
      .int("Outbox 行数上限必须是整数")
      .min(1, "Outbox 行数上限至少为 1")
      .max(1000000, "Outbox 行数上限不能超过 1000000"),
    outboxMaxBytes: z.coerce
      .number()
      .int("Outbox 字节上限必须是整数")
      .min(1024, "Outbox 字节上限至少为 1024")
      .max(1024 * 1024 * 1024, "Outbox 字节上限不能超过 1 GiB"),
    outboxRetentionDays: z.coerce
      .number()
      .int("Outbox 保留天数必须是整数")
      .min(1, "Outbox 保留天数范围为 1～3650 天")
      .max(3650, "Outbox 保留天数范围为 1～3650 天"),
    commandJournalRetentionDays: z.coerce
      .number()
      .int("Command Journal 保留天数必须是整数")
      .min(1, "Command Journal 保留天数范围为 1～3650 天")
      .max(3650, "Command Journal 保留天数范围为 1～3650 天"),
    commandJournalMaxRows: z.coerce
      .number()
      .int("Command Journal 行数上限必须是整数")
      .min(1, "Command Journal 行数上限至少为 1")
      .max(1000000, "Command Journal 行数上限不能超过 1000000"),
    commandQueueCapacity: z.coerce
      .number()
      .int("Command 队列容量必须是整数")
      .min(1, "Command 队列容量至少为 1")
      .max(100000, "Command 队列容量不能超过 100000"),
    commandPollFairness: z.coerce
      .number()
      .int("Poll fairness 必须是整数")
      .min(1, "Poll fairness 范围为 1～100")
      .max(100, "Poll fairness 范围为 1～100"),
  })
  .superRefine((values, context) => {
    if (values.reconnectMinMs > values.reconnectMaxMs) {
      context.addIssue({
        code: z.ZodIssueCode.custom,
        path: ["reconnectMaxMs"],
        message: "最大重连间隔不能小于最小重连间隔",
      });
    }

    if (values.passwordAction === "set" && values.password.length === 0) {
      context.addIssue({
        code: z.ZodIssueCode.custom,
        path: ["password"],
        message: "请输入新的 Broker 密码",
      });
    }
    if (
      values.clientPrivateKeyAction === "set" &&
      values.clientPrivateKey.length === 0
    ) {
      context.addIssue({
        code: z.ZodIssueCode.custom,
        path: ["clientPrivateKey"],
        message: "请输入新的客户端私钥",
      });
    }
  });

type CommandFilterState = {
  status: "all" | MqttCommandStatus;
  deviceId: string;
};

function toFormValues(config: MqttConfig): MqttConfigFormValues {
  return {
    ...config,
    passwordAction: config.passwordConfigured ? "keep" : "clear",
    password: "",
    clientPrivateKeyAction: config.clientPrivateKeyConfigured ? "keep" : "clear",
    clientPrivateKey: "",
  };
}

function buildCommandQuery(
  filters: CommandFilterState,
  page: number,
  pageSize: number,
): MqttCommandListQuery {
  return {
    page,
    pageSize,
    status: filters.status === "all" ? undefined : filters.status,
    deviceId: filters.deviceId.trim() || undefined,
  };
}

function getRuntimeStatusMeta(status?: string) {
  if (status && status in mqttRuntimeStatusMeta) {
    return mqttRuntimeStatusMeta[status as MqttRuntimeStatus];
  }
  return { label: status || "未知", tone: "neutral" as const };
}

function getCommandStatusMeta(status?: string) {
  if (status && status in mqttCommandStatusMeta) {
    return mqttCommandStatusMeta[status as MqttCommandStatus];
  }
  return { label: status || "未知", tone: "neutral" as const };
}

function formatOptionalTime(value?: string | null) {
  return value ? formatDateTime(value) : "-";
}

export function MqttManagementPage() {
  return (
    <PermissionGuard
      permissionCode="mqtt:config:list"
      fallback={
        <EmptyState
          title="无权查看 MQTT 管理"
          description="当前账号没有 MQTT 配置查看权限。"
        />
      }
    >
      <MqttManagementContent />
    </PermissionGuard>
  );
}

function MqttManagementContent() {
  const [config, setConfig] = useState<MqttConfig | null>(null);
  const [configLoading, setConfigLoading] = useState(true);
  const [configError, setConfigError] = useState("");
  const [runtime, setRuntime] = useState<MqttRuntimeState | null>(null);
  const [runtimeLoading, setRuntimeLoading] = useState(true);
  const [runtimeError, setRuntimeError] = useState("");
  const [outbox, setOutbox] = useState<MqttOutboxStats | null>(null);
  const [outboxLoading, setOutboxLoading] = useState(true);
  const [outboxError, setOutboxError] = useState("");
  const [testing, setTesting] = useState(false);
  const [testResult, setTestResult] = useState<string | null>(null);
  const [commandDetail, setCommandDetail] = useState<MqttCommandJournal | null>(
    null,
  );
  const [commandDetailOpen, setCommandDetailOpen] = useState(false);
  const [commandDetailLoading, setCommandDetailLoading] = useState(false);

  const form = useForm<MqttConfigFormValues>({
    resolver: zodResolver(mqttConfigSchema),
    defaultValues: toFormValues(DEFAULT_CONFIG),
    mode: "onBlur",
  });
  const {
    control,
    register,
    handleSubmit,
    trigger,
    reset,
    getValues,
    formState: { errors, isSubmitting, isDirty },
  } = form;
  const tlsEnabled = useWatch({ control, name: "tlsEnabled" });
  const passwordAction = useWatch({ control, name: "passwordAction" });
  const clientPrivateKeyAction = useWatch({
    control,
    name: "clientPrivateKeyAction",
  });

  const commandList = useListPage<
    CommandFilterState,
    MqttCommandJournal,
    MqttCommandListQuery
  >({
    fetch: getMqttCommands,
    defaultFilters: DEFAULT_COMMAND_FILTERS,
    toQuery: buildCommandQuery,
    defaultPageSize: 10,
    onError: (error) =>
      toast.error({
        title: "Command Journal 加载失败",
        description: getMqttErrorMessage(error, "无法获取 Command Journal"),
      }),
  });

  const loadConfig = useCallback(async () => {
    setConfigLoading(true);
    setConfigError("");
    try {
      const result = await getMqttConfig();
      setConfig(result);
      reset(toFormValues(result));
    } catch (error) {
      setConfigError(getMqttErrorMessage(error, "无法获取 MQTT 配置"));
    } finally {
      setConfigLoading(false);
    }
  }, [reset]);

  const loadRuntime = useCallback(async () => {
    setRuntimeLoading(true);
    setRuntimeError("");
    try {
      setRuntime(await getMqttState());
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
    } catch (error) {
      setOutboxError(getMqttErrorMessage(error, "无法获取可靠 Outbox 状态"));
    } finally {
      setOutboxLoading(false);
    }
  }, []);

  useEffect(() => {
    void loadConfig();
    void loadRuntime();
    void loadOutbox();
  }, [loadConfig, loadOutbox, loadRuntime]);

  const submitConfig = async (values: MqttConfigFormValues) => {
    const secrets = [values.password, values.clientPrivateKey];
    try {
      const saved = await updateMqttConfig(buildMqttConfigUpdate(values));
      setConfig(saved);
      reset(toFormValues(saved));
      setTestResult(null);
      toast.success("MQTT 配置已保存");
      void loadRuntime();
    } catch (error) {
      toast.error({
        title: "MQTT 配置保存失败",
        description: getMqttErrorMessage(error, "请检查配置后重试", secrets),
      });
    }
  };

  const resetConfigForm = () => {
    if (config) reset(toFormValues(config));
  };

  const runConnectionTest = async () => {
    setTesting(true);
    setTestResult(null);
    const values = getValues();
    try {
      const parsed = mqttConfigSchema.safeParse(values);
      if (!parsed.success) {
        await trigger();
        const message = "请先修正配置表单中的错误";
        setTestResult(message);
        toast.warning(message);
        return;
      }
      const result = await testMqttConnection(
        isDirty ? buildMqttConfigUpdate(parsed.data) : undefined,
      );
      const message = result.success
        ? result.runtimeConnected
          ? "Broker 连接测试成功，正式 runtime 已连接"
          : "Broker 连接测试成功，正式 runtime 当前未连接"
        : "Broker 连接测试未通过";
      setTestResult(message);
      if (result.success) {
        toast.success(message);
      } else {
        toast.warning(message);
      }
      void loadRuntime();
    } catch (error) {
      const message = getMqttErrorMessage(
        error,
        "Broker 连接测试失败",
        [values.password, values.clientPrivateKey],
      );
      setTestResult(message);
      toast.error({ title: "连接测试失败", description: message });
    } finally {
      setTesting(false);
    }
  };

  const openCommandDetail = async (command: MqttCommandJournal) => {
    setCommandDetail(command);
    setCommandDetailOpen(true);
    setCommandDetailLoading(true);
    try {
      setCommandDetail(await getMqttCommand(command.commandId));
    } catch (error) {
      toast.error({
        title: "Command 详情加载失败",
        description: getMqttErrorMessage(error, "无法获取 Command 详情"),
      });
    } finally {
      setCommandDetailLoading(false);
    }
  };

  const commandColumns = useMemo<DataTableColumn<MqttCommandJournal>[]>(
    () => [
      {
        title: "Command ID",
        dataIndex: "commandId",
        width: 250,
        render: (value) => (
          <code className="break-all text-xs text-text-secondary">
            {String(value ?? "-")}
          </code>
        ),
      },
      {
        title: "设备",
        dataIndex: "deviceId",
        width: 180,
        render: (value) => (
          <span className="font-medium text-text-primary">{String(value ?? "-")}</span>
        ),
      },
      { title: "命令", dataIndex: "name", width: 140 },
      {
        title: "状态",
        dataIndex: "status",
        width: 120,
        render: (value) => {
          const meta = getCommandStatusMeta(String(value ?? ""));
          return <StatusTag tone={meta.tone}>{meta.label}</StatusTag>;
        },
      },
      {
        title: "接收时间",
        dataIndex: "receivedAt",
        width: 180,
        nowrap: true,
        render: (value) => formatDateTime(String(value ?? "")),
      },
      {
        title: "开始时间",
        dataIndex: "startedAt",
        width: 180,
        nowrap: true,
        render: (value) => formatOptionalTime(value as string | null | undefined),
      },
      {
        title: "完成时间",
        dataIndex: "completedAt",
        width: 180,
        nowrap: true,
        render: (value) => formatOptionalTime(value as string | null | undefined),
      },
      {
        title: "操作",
        key: "actions",
        width: 100,
        nowrap: true,
        render: (_, record) => (
          <Button
            size="sm"
            variant="ghost"
            disabled={commandDetailLoading && commandDetail?.commandId === record.commandId}
            onClick={() => void openCommandDetail(record)}
          >
            <Eye className="h-4 w-4" aria-hidden />
            详情
          </Button>
        ),
      },
    ],
    [commandDetail?.commandId, commandDetailLoading],
  );

  const runtimeStatus = getRuntimeStatusMeta(runtime?.state);
  const rawPendingCount = runtime?.pendingLatestCount ?? 0;
  const commandDetailError = commandDetail?.errorMessage
    ? getMqttErrorMessage(
        new Error(commandDetail.errorMessage),
        "错误信息不可展示",
        [getValues("password"), getValues("clientPrivateKey")],
      )
    : "";

  return (
    <>
      <PageHeader
        title="MQTT 管理"
        description="管理上行连接、可靠消息健康度和设备 Command Journal。"
        actions={
          <PermissionGuard permissionCode="mqtt:config:test">
            <Button
              variant="secondary"
              onClick={() => void runConnectionTest()}
              disabled={testing || configLoading || !config}
            >
              <Activity className="h-4 w-4" aria-hidden />
              {testing ? "测试中..." : "测试连接"}
            </Button>
          </PermissionGuard>
        }
      />

      <div className="space-y-space-6">
        <RuntimeOverview
          runtime={runtime}
          runtimeStatus={runtimeStatus}
          runtimeLoading={runtimeLoading}
          runtimeError={runtimeError}
          outbox={outbox}
          outboxLoading={outboxLoading}
          outboxError={outboxError}
          rawPendingCount={rawPendingCount}
          onRefresh={() => {
            void loadRuntime();
            void loadOutbox();
          }}
        />

        <ContentCard
          title="连接配置"
          description="保存配置后，MQTT runtime 会按最新提交值安全重建连接。"
          extra={
            config && (
              <StatusTag tone={config.enabled ? "success" : "neutral"}>
                {config.enabled ? "配置已启用" : "配置已停用"}
              </StatusTag>
            )
          }
        >
          {configLoading ? (
            <ConfigLoading />
          ) : configError ? (
            <ErrorPanel
              title="MQTT 配置加载失败"
              description={configError}
              onRetry={() => void loadConfig()}
            />
          ) : (
            <form
              className="space-y-space-6"
              noValidate
              onSubmit={(event) => {
                void handleSubmit(submitConfig)(event);
              }}
            >
              <FormSection
                title="Broker 与 Edge identity"
                description="Edge ID 会参与 Topic 生成，保存后作为稳定业务身份使用。"
              >
                <div className="md:col-span-2">
                  <Controller
                    control={control}
                    name="enabled"
                    render={({ field }) => (
                      <div className="flex items-center justify-between rounded-control border border-border px-space-4 py-space-3">
                        <div>
                          <div className="font-medium text-text-primary">启用 MQTT runtime</div>
                          <div className="mt-1 text-body-secondary text-text-tertiary">
                            停用时不建立 Broker 连接，但不会停止 Modbus 采集。
                          </div>
                        </div>
                        <Switch
                          checked={field.value}
                          onCheckedChange={field.onChange}
                          disabled={isSubmitting}
                          aria-label="启用 MQTT runtime"
                        />
                      </div>
                    )}
                  />
                </div>
                <Field label="Edge ID" htmlFor="mqtt-edge-id" required error={errors.edgeId?.message}>
                  <Input id="mqtt-edge-id" disabled={isSubmitting} {...register("edgeId")} />
                </Field>
                <Field
                  label="Broker 地址"
                  htmlFor="mqtt-broker-url"
                  required
                  error={errors.brokerUrl?.message}
                  help="支持 mqtt/mqtts、tcp/tls、ws/wss 协议地址。"
                >
                  <Input
                    id="mqtt-broker-url"
                    type="url"
                    autoComplete="url"
                    placeholder="mqtt://broker.example:1883"
                    disabled={isSubmitting}
                    {...register("brokerUrl")}
                  />
                </Field>
                <Field
                  label="协议版本"
                  htmlFor="mqtt-protocol-version"
                  required
                  error={errors.protocolVersion?.message}
                >
                  <Select id="mqtt-protocol-version" disabled={isSubmitting} {...register("protocolVersion")}>
                    <option value="MQTT_5">MQTT 5（默认）</option>
                    <option value="MQTT_3_1_1">MQTT 3.1.1</option>
                  </Select>
                </Field>
                <Field
                  label="Client ID"
                  htmlFor="mqtt-client-id"
                  required
                  error={errors.clientId?.message}
                >
                  <Input id="mqtt-client-id" disabled={isSubmitting} {...register("clientId")} />
                </Field>
                <Field
                  label="用户名"
                  htmlFor="mqtt-username"
                  error={errors.username?.message}
                  help="用户名不是 secret，可按 Broker 要求配置。"
                >
                  <Input id="mqtt-username" autoComplete="username" disabled={isSubmitting} {...register("username")} />
                </Field>
                <SecretField
                  label="Broker 密码"
                  actionName="passwordAction"
                  valueName="password"
                  configured={config?.passwordConfigured ?? false}
                  action={passwordAction}
                  form={form}
                  error={errors.password?.message || errors.passwordAction?.message}
                  disabled={isSubmitting}
                  placeholder="仅在设置新值时输入"
                />
              </FormSection>

              <FormSection
                title="TLS 与客户端凭据"
                description="证书内容按配置保存；客户端私钥始终只显示配置状态，不提供回读。"
              >
                <div className="md:col-span-2">
                  <Controller
                    control={control}
                    name="tlsEnabled"
                    render={({ field }) => (
                      <div className="flex items-center justify-between rounded-control border border-border px-space-4 py-space-3">
                        <div className="flex items-start gap-space-3">
                          <ShieldCheck className="mt-0.5 h-5 w-5 text-info" aria-hidden />
                          <div>
                            <div className="font-medium text-text-primary">启用 TLS</div>
                            <div className="mt-1 text-body-secondary text-text-tertiary">
                              连接使用系统根证书，或使用下方 CA/客户端证书材料。
                            </div>
                          </div>
                        </div>
                        <Switch
                          checked={field.value}
                          onCheckedChange={field.onChange}
                          disabled={isSubmitting}
                          aria-label="启用 MQTT TLS"
                        />
                      </div>
                    )}
                  />
                </div>
                <div className="md:col-span-2">
                  <Field
                    label="CA 证书"
                    htmlFor="mqtt-ca-certificate"
                    error={errors.caCertificate?.message}
                    help="可留空以使用部署环境的系统根证书。"
                  >
                    <Textarea
                      id="mqtt-ca-certificate"
                      rows={5}
                      spellCheck={false}
                      disabled={isSubmitting || !tlsEnabled}
                      className="font-mono text-xs leading-5"
                      placeholder={tlsEnabled ? "-----BEGIN CERTIFICATE-----" : "启用 TLS 后填写"}
                      {...register("caCertificate")}
                    />
                  </Field>
                </div>
                <div className="md:col-span-2">
                  <Field
                    label="客户端证书"
                    htmlFor="mqtt-client-certificate"
                    error={errors.clientCertificate?.message}
                  >
                    <Textarea
                      id="mqtt-client-certificate"
                      rows={5}
                      spellCheck={false}
                      disabled={isSubmitting || !tlsEnabled}
                      className="font-mono text-xs leading-5"
                      placeholder={tlsEnabled ? "-----BEGIN CERTIFICATE-----" : "启用 TLS 后填写"}
                      {...register("clientCertificate")}
                    />
                  </Field>
                </div>
                <SecretField
                  label="客户端私钥"
                  actionName="clientPrivateKeyAction"
                  valueName="clientPrivateKey"
                  configured={config?.clientPrivateKeyConfigured ?? false}
                  action={clientPrivateKeyAction}
                  form={form}
                  error={errors.clientPrivateKey?.message || errors.clientPrivateKeyAction?.message}
                  disabled={isSubmitting || !tlsEnabled}
                  placeholder="仅在设置新值时输入"
                  multiline
                />
              </FormSection>

              <FormSection
                title="连接与重连"
                description="这些参数只影响 MQTT client，不改变 acquisition 的 pollInterval 或 channel pacing。"
              >
                <NumberField label="Keep Alive（秒）" name="keepAliveSeconds" register={register} error={errors.keepAliveSeconds?.message} min={5} max={3600} disabled={isSubmitting} />
                <NumberField label="连接超时（毫秒）" name="connectTimeoutMs" register={register} error={errors.connectTimeoutMs?.message} min={100} max={120000} disabled={isSubmitting} />
                <NumberField label="最小重连间隔（毫秒）" name="reconnectMinMs" register={register} error={errors.reconnectMinMs?.message} min={100} max={60000} disabled={isSubmitting} />
                <NumberField label="最大重连间隔（毫秒）" name="reconnectMaxMs" register={register} error={errors.reconnectMaxMs?.message} min={100} max={600000} disabled={isSubmitting} />
                <Field label="Topic 前缀" htmlFor="mqtt-topic-prefix" required error={errors.topicPrefix?.message} help="例如 edge；不会自动添加 MQTT 通配符。">
                  <Input id="mqtt-topic-prefix" disabled={isSubmitting} {...register("topicPrefix")} />
                </Field>
                <NumberField label="Raw 上报间隔（毫秒）" name="rawPublishIntervalMs" register={register} error={errors.rawPublishIntervalMs?.message} min={50} max={3600000} disabled={isSubmitting} help="只限制 MQTT raw publish 频率，不改变采集周期。" />
              </FormSection>

              <FormSection
                title="可靠性与 Command 安全边界"
                description="Outbox、journal 和 bounded command queue 的容量必须在服务端再次校验。"
              >
                <NumberField label="Outbox 行数上限" name="outboxMaxRows" register={register} error={errors.outboxMaxRows?.message} min={1} max={1000000} disabled={isSubmitting} />
                <NumberField label="Outbox 字节上限" name="outboxMaxBytes" register={register} error={errors.outboxMaxBytes?.message} min={1024} max={1024 * 1024 * 1024} disabled={isSubmitting} help={`当前值：${formatMqttBytes(form.watch("outboxMaxBytes"))}`} />
                <NumberField label="Outbox 保留天数" name="outboxRetentionDays" register={register} error={errors.outboxRetentionDays?.message} min={1} max={3650} disabled={isSubmitting} />
                <NumberField label="Command Journal 保留天数" name="commandJournalRetentionDays" register={register} error={errors.commandJournalRetentionDays?.message} min={1} max={3650} disabled={isSubmitting} />
                <NumberField label="Command Journal 行数上限" name="commandJournalMaxRows" register={register} error={errors.commandJournalMaxRows?.message} min={1} max={1000000} disabled={isSubmitting} />
                <NumberField label="Command 队列容量" name="commandQueueCapacity" register={register} error={errors.commandQueueCapacity?.message} min={1} max={100000} disabled={isSubmitting} />
                <NumberField label="Poll fairness" name="commandPollFairness" register={register} error={errors.commandPollFairness?.message} min={1} max={100} disabled={isSubmitting} help="Command burst 下允许普通 poll 继续获得调度机会。" />
              </FormSection>

              {testResult && (
                <div className="rounded-control border border-info-border bg-info-background px-space-4 py-space-3 text-sm text-info" role="status">
                  {testResult}
                </div>
              )}

              <div className="flex flex-wrap items-center justify-end gap-space-2 border-t border-border pt-space-4">
                {isDirty && <span className="mr-auto text-body-secondary text-warning">有未保存的配置变更</span>}
                <Button type="button" variant="secondary" onClick={resetConfigForm} disabled={isSubmitting || !isDirty}>
                  <RotateCcw className="h-4 w-4" aria-hidden />
                  放弃变更
                </Button>
                <PermissionGuard permissionCode="mqtt:config:edit" fallback={<span className="text-body-secondary text-text-tertiary">当前账号无配置写入权限</span>}>
                  <Button type="submit" variant="primary" disabled={isSubmitting || !config}>
                    <Save className="h-4 w-4" aria-hidden />
                    {isSubmitting ? "保存中..." : "保存配置"}
                  </Button>
                </PermissionGuard>
              </div>
            </form>
          )}
        </ContentCard>

        <PermissionGuard permissionCode="mqtt:command:list">
          <CommandJournalSection
            list={commandList}
            columns={commandColumns}
            onOpenDetail={(record) => void openCommandDetail(record)}
          />
        </PermissionGuard>
      </div>

      <CommandDetailDialog
        open={commandDetailOpen}
        detail={commandDetail}
        loading={commandDetailLoading}
        errorMessage={commandDetailError}
        onCancel={() => setCommandDetailOpen(false)}
      />
    </>
  );
}

function RuntimeOverview({
  runtime,
  runtimeStatus,
  runtimeLoading,
  runtimeError,
  outbox,
  outboxLoading,
  outboxError,
  rawPendingCount,
  onRefresh,
}: {
  runtime: MqttRuntimeState | null;
  runtimeStatus: ReturnType<typeof getRuntimeStatusMeta>;
  runtimeLoading: boolean;
  runtimeError: string;
  outbox: MqttOutboxStats | null;
  outboxLoading: boolean;
  outboxError: string;
  rawPendingCount: number;
  onRefresh: () => void;
}) {
  const safeRuntimeError = runtime?.lastError
    ? getMqttErrorMessage(new Error(runtime.lastError), "最近一次连接失败")
    : "";
  const safeOutboxError = outbox?.lastError
    ? getMqttErrorMessage(new Error(outbox.lastError), "最近一次投递失败")
    : "";

  return (
    <ContentCard
      title="运行总览"
      description="MQTT runtime 与 acquisition 状态独立；Broker 断线不应阻塞 Modbus 采集。"
      extra={
        <Button size="sm" variant="secondary" onClick={onRefresh} disabled={runtimeLoading || outboxLoading}>
          <RefreshCw className="h-4 w-4" aria-hidden />
          刷新
        </Button>
      }
    >
      {runtimeError ? (
        <ErrorPanel title="运行状态加载失败" description={runtimeError} onRetry={onRefresh} />
      ) : runtimeLoading && !runtime ? (
        <RuntimeLoading />
      ) : (
        <div className="grid gap-space-3 lg:grid-cols-[minmax(0,1.45fr)_repeat(3,minmax(0,1fr))]">
          <div className="rounded-control border border-border bg-neutral-background p-space-4">
            <div className="flex items-start justify-between gap-space-3">
              <div className="flex min-w-0 items-start gap-space-3">
                <div className="flex h-10 w-10 shrink-0 items-center justify-center rounded-control bg-surface text-primary shadow-subtle">
                  {runtime?.connected ? <RadioTower className="h-5 w-5" aria-hidden /> : <WifiOff className="h-5 w-5" aria-hidden />}
                </div>
                <div className="min-w-0">
                  <div className="text-body-secondary text-text-tertiary">MQTT runtime</div>
                  <div className="mt-1 flex flex-wrap items-center gap-space-2">
                    <StatusTag tone={runtimeStatus.tone}>{runtimeStatus.label}</StatusTag>
                    <span className="text-sm text-text-secondary">{runtime?.connected ? "Broker 已连接" : "Broker 未连接"}</span>
                  </div>
                </div>
              </div>
              <Activity className="h-5 w-5 text-info" aria-hidden />
            </div>
            {safeRuntimeError && (
              <div className="mt-space-3 rounded-control border border-error-border bg-error-background px-space-3 py-space-2 text-body-secondary text-error" role="alert">
                {safeRuntimeError}
              </div>
            )}
            <div className="mt-space-4 grid gap-space-2 text-body-secondary text-text-tertiary sm:grid-cols-2">
              <div>订阅：<code className="break-all text-text-secondary">{runtime?.subscriptionFilter || "-"}</code></div>
              <div>重连次数：<span className="text-text-secondary">{runtime?.reconnectAttempts ?? 0}</span></div>
              <div>最近连接：<span className="text-text-secondary">{formatOptionalTime(runtime?.lastConnectedAt)}</span></div>
              <div>最近断开：<span className="text-text-secondary">{formatOptionalTime(runtime?.lastDisconnectedAt)}</span></div>
              <div>下次重试：<span className="text-text-secondary">{formatOptionalTime(runtime?.nextRetryAt)}</span></div>
            </div>
          </div>
          <OverviewMetric icon={<Inbox className="h-4 w-4" aria-hidden />} label="Reliable Outbox" value={outboxLoading ? "加载中" : formatOutboxRows(outbox)} tone={outboxError ? "error" : "neutral"} />
          <OverviewMetric icon={<Clock3 className="h-4 w-4" aria-hidden />} label="最早可靠消息" value={outboxError ? "加载失败" : formatOutboxAge(outbox)} tone={outboxError ? "error" : "neutral"} />
          <OverviewMetric icon={<Server className="h-4 w-4" aria-hidden />} label="Raw pending latest" value={String(rawPendingCount)} tone={rawPendingCount > 0 ? "info" : "neutral"} />
        </div>
      )}
      {outbox && !outboxError && <OutboxHealthSummary outbox={outbox} />}
      {outboxError && !runtimeError && (
        <div className="mt-space-3 text-body-secondary text-error" role="alert">{outboxError}</div>
      )}
      {safeOutboxError && (
        <div className="mt-space-3 flex items-start gap-space-2 rounded-control border border-warning-border bg-warning-background px-space-3 py-space-2 text-body-secondary text-warning" role="alert">
          <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" aria-hidden />
          <span>最近一次 Outbox 投递失败：{safeOutboxError}</span>
        </div>
      )}
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
  return (
    <div className="mt-space-4 rounded-control border border-border bg-neutral-background px-space-4 py-space-3">
      <div className="flex flex-wrap items-center justify-between gap-space-2">
        <div>
          <div className="font-medium text-text-primary">Reliable Outbox 容量</div>
          <div className="mt-1 text-body-secondary text-text-tertiary">
            FINAL result 不会因低优先级消息静默淘汰；容量由服务端准入控制。
          </div>
        </div>
        <StatusTag tone={outbox.rowUtilization >= 1 || outbox.byteUtilization >= 1 ? "error" : outbox.rowUtilization >= 0.8 || outbox.byteUtilization >= 0.8 ? "warning" : "success"}>
          {formatUtilization(Math.max(outbox.rowUtilization, outbox.byteUtilization))}
        </StatusTag>
      </div>
      <div className="mt-space-3 grid gap-space-3 md:grid-cols-2">
        <CapacityBar
          label="行容量"
          used={`${outbox.rows} / ${outbox.maxRows}`}
          utilization={outbox.rowUtilization}
        />
        <CapacityBar
          label="字节容量"
          used={`${formatMqttBytes(outbox.bytes)} / ${formatMqttBytes(outbox.maxBytes)}`}
          utilization={outbox.byteUtilization}
        />
      </div>
    </div>
  );
}

function CapacityBar({
  label,
  used,
  utilization,
}: {
  label: string;
  used: string;
  utilization: number;
}) {
  const safeUtilization = Number.isFinite(utilization) ? Math.max(utilization, 0) : 0;
  const width = Math.min(safeUtilization * 100, 100);
  const barClass = safeUtilization >= 1 ? "bg-error" : safeUtilization >= 0.8 ? "bg-warning" : "bg-primary";
  return (
    <div>
      <div className="flex items-center justify-between gap-space-2 text-body-secondary">
        <span className="text-text-secondary">{label}</span>
        <span className="tabular-nums text-text-tertiary">{used} · {formatUtilization(safeUtilization)}</span>
      </div>
      <div className="mt-space-2 h-2 overflow-hidden rounded-pill bg-neutral-border" aria-hidden="true">
        <div className={`h-full rounded-pill ${barClass}`} style={{ width: `${width}%` }} />
      </div>
    </div>
  );
}

function formatUtilization(value: number) {
  if (!Number.isFinite(value)) return "-";
  return `${Math.round(Math.max(value, 0) * 100)}%`;
}

function OverviewMetric({
  icon,
  label,
  value,
  tone,
}: {
  icon: React.ReactNode;
  label: string;
  value: string;
  tone: "info" | "error" | "neutral";
}) {
  const toneClass = tone === "info" ? "text-info" : tone === "error" ? "text-error" : "text-text-primary";
  return (
    <div className="rounded-control border border-border bg-surface p-space-4">
      <div className="flex items-center justify-between gap-space-2 text-body-secondary text-text-tertiary">
        <span>{label}</span>
        <span className={toneClass}>{icon}</span>
      </div>
      <div className={`mt-space-3 break-words text-lg font-semibold ${toneClass}`}>{value}</div>
    </div>
  );
}

function ConfigLoading() {
  return (
    <div className="space-y-space-4" aria-busy="true" aria-live="polite">
      {Array.from({ length: 4 }).map((_, index) => (
        <div key={index} className="h-20 animate-pulse rounded-control bg-neutral-background" />
      ))}
    </div>
  );
}

function RuntimeLoading() {
  return (
    <div className="grid gap-space-3 lg:grid-cols-4" aria-busy="true" aria-live="polite">
      {Array.from({ length: 4 }).map((_, index) => (
        <div key={index} className="h-32 animate-pulse rounded-control bg-neutral-background" />
      ))}
    </div>
  );
}

function ErrorPanel({
  title,
  description,
  onRetry,
}: {
  title: string;
  description: string;
  onRetry: () => void;
}) {
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

function NumberField<T extends keyof MqttConfigFormValues>({
  label,
  name,
  register,
  error,
  help,
  min,
  max,
  disabled,
}: {
  label: string;
  name: T;
  register: UseFormReturn<MqttConfigFormValues>["register"];
  error?: string;
  help?: string;
  min: number;
  max: number;
  disabled: boolean;
}) {
  return (
    <Field label={label} htmlFor={`mqtt-${String(name)}`} required error={error} help={help}>
      <Input
        id={`mqtt-${String(name)}`}
        type="number"
        inputMode="numeric"
        min={min}
        max={max}
        step={1}
        disabled={disabled}
        {...register(name)}
      />
    </Field>
  );
}

function SecretField({
  label,
  actionName,
  valueName,
  configured,
  action,
  form,
  error,
  disabled,
  placeholder,
  multiline = false,
}: {
  label: string;
  actionName: "passwordAction" | "clientPrivateKeyAction";
  valueName: "password" | "clientPrivateKey";
  configured: boolean;
  action: string;
  form: UseFormReturn<MqttConfigFormValues>;
  error?: string;
  disabled: boolean;
  placeholder: string;
  multiline?: boolean;
}) {
  const { register } = form;
  return (
    <Field
      label={label}
      htmlFor={`mqtt-${valueName}`}
      error={error}
      help="已配置值不会回读；保持或清除时请求体不携带 secret。"
    >
      <div className="space-y-space-2">
        <div className="flex flex-wrap items-center gap-space-2">
          <StatusTag tone={configured ? "success" : "neutral"}>
            {configured ? "•••••••• · 已配置" : "未配置"}
          </StatusTag>
          <Select
            aria-label={`${label}操作`}
            className="min-w-[160px] flex-1"
            disabled={disabled}
            {...register(actionName)}
          >
            <option value="keep">保持已配置值</option>
            <option value="set">设置新值</option>
            <option value="clear">清除配置值</option>
          </Select>
        </div>
        {action === "set" && (
          multiline ? (
            <Textarea
              id={`mqtt-${valueName}`}
              rows={6}
              spellCheck={false}
              placeholder={placeholder}
              disabled={disabled}
              className="font-mono text-xs leading-5"
              {...register(valueName)}
            />
          ) : (
            <Input
              id={`mqtt-${valueName}`}
              type="password"
              autoComplete="new-password"
              placeholder={placeholder}
              disabled={disabled}
              {...register(valueName)}
            />
          )
        )}
      </div>
    </Field>
  );
}

function CommandJournalSection({
  list,
  columns,
  onOpenDetail,
}: {
  list: ReturnType<typeof useListPage<CommandFilterState, MqttCommandJournal, MqttCommandListQuery>>;
  columns: DataTableColumn<MqttCommandJournal>[];
  onOpenDetail: (record: MqttCommandJournal) => void;
}) {
  return (
    <>
      <form
        onSubmit={(event) => {
          event.preventDefault();
          list.submitFilters();
        }}
      >
        <SearchFilterBar
          actions={
            <>
              <Button type="submit" variant="primary">
                <Search className="h-4 w-4" aria-hidden />
                查询
              </Button>
              <Button type="button" variant="secondary" onClick={list.resetFilters}>
                <RotateCcw className="h-4 w-4" aria-hidden />
                重置
              </Button>
            </>
          }
        >
          <Input
            value={list.filters.deviceId}
            onChange={(event) => list.setFilter("deviceId", event.target.value)}
            placeholder="按 deviceId 筛选"
            aria-label="按 deviceId 筛选"
          />
          <Select
            value={list.filters.status}
            onChange={(event) => list.setFilter("status", event.target.value as CommandFilterState["status"])}
            aria-label="按 Command 状态筛选"
          >
            <option value="all">全部状态</option>
            {commandStatuses.map((status) => (
              <option key={status.value} value={status.value}>{status.label}</option>
            ))}
          </Select>
        </SearchFilterBar>
      </form>

      <DataTableCard
        toolbar={
          <TableToolbar
            title="Command Journal"
            description="展示持久化幂等事实源状态；页面不展示 command payload、ciphertext 或 result 原文。"
            actions={
              <>
                <StatusTag tone={list.loading ? "warning" : list.error ? "error" : "info"}>
                  {list.loading ? "加载中" : list.error ? "加载失败" : "已同步"}
                </StatusTag>
                <Button size="sm" variant="secondary" onClick={list.reload} disabled={list.loading}>
                  <RefreshCw className="h-4 w-4" aria-hidden />
                  刷新
                </Button>
              </>
            }
          />
        }
        pagination={
          <Pagination
            page={list.page}
            pageSize={list.pageSize}
            total={list.total}
            disabled={list.loading}
            onPageChange={list.setPage}
            onPageSizeChange={list.setPageSize}
          />
        }
      >
        <DataTable
          columns={columns}
          dataSource={list.data}
          rowKey="commandId"
          loading={list.loading}
          error={list.error ? "Command Journal 加载失败，请重试。" : undefined}
          minWidth={1180}
          onRowClick={onOpenDetail}
          empty={<EmptyState title="暂无 Command Journal 记录" description="收到 MQTT command 后，持久化状态会显示在这里。" />}
        />
      </DataTableCard>
    </>
  );
}

function CommandDetailDialog({
  open,
  detail,
  loading,
  errorMessage,
  onCancel,
}: {
  open: boolean;
  detail: MqttCommandJournal | null;
  loading: boolean;
  errorMessage: string;
  onCancel: () => void;
}) {
  const status = getCommandStatusMeta(detail?.status);
  return (
    <DetailDialog
      open={open}
      title="Command Journal 详情"
      description={loading ? "详情加载中" : `Command ID：${detail?.commandId ?? "-"}`}
      loading={loading}
      contentClassName="max-w-[860px]"
      closeOnEscape={false}
      closeOnOverlayClick={false}
      onCancel={onCancel}
    >
      <div className="space-y-space-5">
        <div className="flex items-start gap-space-3 rounded-control border border-info-border bg-info-background px-space-4 py-space-3 text-sm text-info">
          <KeyRound className="mt-0.5 h-4 w-4 shrink-0" aria-hidden />
          <div>
            <div className="font-medium">幂等事实源</div>
            <div className="mt-1 text-body-secondary text-text-secondary">
              相同 commandId 不会创建第二个真实执行；详情只展示安全的状态元数据。
            </div>
          </div>
        </div>
        <div className="grid gap-space-4 md:grid-cols-3">
          <DetailValue label="设备" value={detail?.deviceId} />
          <DetailValue label="命令名称" value={detail?.name} />
          <DetailValue label="状态" value={detail && <StatusTag tone={status.tone}>{status.label}</StatusTag>} />
          <DetailValue label="接收时间" value={formatOptionalTime(detail?.receivedAt)} />
          <DetailValue label="签发时间" value={formatOptionalTime(detail?.issuedAt)} />
          <DetailValue label="过期时间" value={formatOptionalTime(detail?.expiresAt)} />
          <DetailValue label="开始时间" value={formatOptionalTime(detail?.startedAt)} />
          <DetailValue label="完成时间" value={formatOptionalTime(detail?.completedAt)} />
          <DetailValue label="错误类型" value={detail?.errorType} />
          {errorMessage && (
            <DetailValue label="错误信息" value={errorMessage} className="md:col-span-3" />
          )}
        </div>
      </div>
    </DetailDialog>
  );
}

function DetailValue({
  label,
  value,
  className,
}: {
  label: string;
  value?: React.ReactNode;
  className?: string;
}) {
  return (
    <div className={className}>
      <div className="text-body-secondary text-text-tertiary">{label}</div>
      <div className="mt-space-1 break-words text-sm text-text-primary">{value ?? "-"}</div>
    </div>
  );
}
