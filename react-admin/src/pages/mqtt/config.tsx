import { zodResolver } from "@hookform/resolvers/zod";
import { Activity, RotateCcw, Save, ShieldCheck } from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import { Controller, useForm, useWatch } from "react-hook-form";
import { useBeforeUnload, useBlocker } from "react-router-dom";
import { getMqttConfig, testMqttConnection, updateMqttConfig } from "@/api/mqtt";
import { PermissionGuard } from "@/components/auth/permission-guard";
import { EmptyState } from "@/components/common/empty-state";
import { Field } from "@/components/common/field";
import { FormSection } from "@/components/common/form-section";
import { toast } from "@/components/common/toast-store";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import { hasPermission } from "@/lib/permission";
import { buildMqttConfigUpdate, getMqttErrorMessage, type MqttConfigFormValues } from "@/lib/mqtt";
import type { MqttConfig } from "@/types";
import {
  ConfigDisclosure,
  ConfigLoading,
  DEFAULT_CONFIG,
  ErrorPanel,
  MqttPageLayout,
  mqttConfigSchema,
  NumberField,
  SecretField,
  toFormValues,
} from "./shared";

export function MqttConfigPage() {
  return (
    <PermissionGuard
      permissionCode="mqtt:config:list"
      fallback={<EmptyState title="无权查看连接配置" description="当前账号没有 MQTT 配置查看权限。" />}
    >
      <MqttConfigContent />
    </PermissionGuard>
  );
}

function MqttConfigContent() {
  const canEdit = hasPermission("mqtt:config:edit");
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
  const [config, setConfig] = useState<MqttConfig | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [testing, setTesting] = useState(false);
  const [testResult, setTestResult] = useState<string | null>(null);
  const tlsEnabled = useWatch({ control, name: "tlsEnabled" });
  const passwordAction = useWatch({ control, name: "passwordAction" });
  const clientPrivateKeyAction = useWatch({ control, name: "clientPrivateKeyAction" });
  const blocker = useBlocker(isDirty);

  useBeforeUnload(useCallback((event) => {
    if (!isDirty) return;
    event.preventDefault();
    event.returnValue = "";
  }, [isDirty]));

  useEffect(() => {
    if (blocker.state !== "blocked") return;
    if (window.confirm("当前配置有未保存变更，确定放弃并离开吗？")) blocker.proceed();
    else blocker.reset();
  }, [blocker]);

  const loadConfig = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      const result = await getMqttConfig();
      setConfig(result);
      reset(toFormValues(result));
    } catch (loadError) {
      setError(getMqttErrorMessage(loadError, "无法获取 MQTT 配置"));
    } finally {
      setLoading(false);
    }
  }, [reset]);

  useEffect(() => {
    void loadConfig();
  }, [loadConfig]);

  const submitConfig = async (values: MqttConfigFormValues) => {
    if (!canEdit) return;
    const clearPassword = values.passwordAction === "clear" && Boolean(config?.passwordConfigured);
    const clearPrivateKey = values.clientPrivateKeyAction === "clear" && Boolean(config?.clientPrivateKeyConfigured);
    if ((clearPassword || clearPrivateKey) && !window.confirm("清除已配置的 secret 后，保存会立即生效，可能导致 runtime 无法重新连接。确定继续吗？")) return;
    const secrets = [values.password, values.clientPrivateKey];
    try {
      const saved = await updateMqttConfig(buildMqttConfigUpdate(values));
      setConfig(saved);
      reset(toFormValues(saved));
      setTestResult(null);
      toast.success("MQTT 配置已保存，runtime 正在按新配置重连");
    } catch (saveError) {
      toast.error({ title: "MQTT 配置保存失败", description: getMqttErrorMessage(saveError, "请检查配置后重试", secrets) });
    }
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
      const result = await testMqttConnection(isDirty ? buildMqttConfigUpdate(parsed.data) : undefined);
      const message = result.success ? result.runtimeConnected ? "Broker 连接测试成功，正式 runtime 已连接" : "Broker 连接测试成功，正式 runtime 当前未连接" : "Broker 连接测试未通过";
      setTestResult(message);
      if (result.success) toast.success(message);
      else toast.warning(message);
    } catch (testError) {
      const message = getMqttErrorMessage(testError, "Broker 连接测试失败", [values.password, values.clientPrivateKey]);
      setTestResult(message);
      toast.error({ title: "连接测试失败", description: message });
    } finally {
      setTesting(false);
    }
  };

  const disabled = isSubmitting || !canEdit;

  return (
    <MqttPageLayout
      title="连接配置"
      description="分组管理 Broker、TLS、重连与可靠性参数；保存后 runtime 异步重建连接。"
      actions={<PermissionGuard permissionCode="mqtt:config:test"><Button variant="secondary" onClick={() => void runConnectionTest()} disabled={testing || loading || !config}><Activity className="h-4 w-4" aria-hidden />{testing ? "测试中..." : "测试连接"}</Button></PermissionGuard>}
    >
      {loading ? <ConfigLoading /> : error ? <ErrorPanel title="MQTT 配置加载失败" description={error} onRetry={() => void loadConfig()} /> : (
        <form className="space-y-space-5" noValidate onSubmit={(event) => { void handleSubmit(submitConfig)(event); }}>
          <div id="broker">
            <FormSection title="基础连接" description="管理 Edge identity、Broker 地址和认证信息。">
              <div className="md:col-span-2"><Controller control={control} name="enabled" render={({ field }) => <div className="flex items-center justify-between rounded-control border border-border px-space-4 py-space-3"><div><div className="font-medium text-text-primary">启用 MQTT runtime</div><div className="mt-1 text-body-secondary text-text-tertiary">停用时不建立 Broker 连接，但不会停止 Modbus 采集。</div></div><Switch checked={field.value} onCheckedChange={field.onChange} disabled={disabled} aria-label="启用 MQTT runtime" /></div>} /></div>
              <Field label="Edge ID" htmlFor="mqtt-edge-id" required error={errors.edgeId?.message}><Input id="mqtt-edge-id" disabled={disabled} {...register("edgeId")} /></Field>
              <Field label="Broker 地址" htmlFor="mqtt-broker-url" required error={errors.brokerUrl?.message} help="支持 mqtt/mqtts、tcp/tls、ws/wss 协议地址。"><Input id="mqtt-broker-url" type="url" autoComplete="url" placeholder="mqtt://broker.example:1883" disabled={disabled} {...register("brokerUrl")} /></Field>
              <Field label="协议版本" htmlFor="mqtt-protocol-version" required error={errors.protocolVersion?.message}><Select id="mqtt-protocol-version" disabled={disabled} {...register("protocolVersion")}><option value="MQTT_5">MQTT 5（默认）</option><option value="MQTT_3_1_1">MQTT 3.1.1</option></Select></Field>
              <Field label="Client ID" htmlFor="mqtt-client-id" required error={errors.clientId?.message}><Input id="mqtt-client-id" disabled={disabled} {...register("clientId")} /></Field>
              <Field label="用户名" htmlFor="mqtt-username" error={errors.username?.message} help="用户名不是 secret，可按 Broker 要求配置。"><Input id="mqtt-username" autoComplete="username" disabled={disabled} {...register("username")} /></Field>
              <SecretField label="Broker 密码" actionName="passwordAction" valueName="password" configured={config?.passwordConfigured ?? false} action={passwordAction} form={form} error={errors.password?.message || errors.passwordAction?.message} disabled={disabled} placeholder="仅在设置新值时输入" />
            </FormSection>
          </div>

          <FormSection title="TLS" description="保留已有证书材料；关闭 TLS 只会暂不生效，不会自动清除证书。">
            <div className="md:col-span-2"><Controller control={control} name="tlsEnabled" render={({ field }) => <div className="flex items-center justify-between rounded-control border border-border px-space-4 py-space-3"><div className="flex items-start gap-space-3"><ShieldCheck className="mt-0.5 h-5 w-5 text-info" aria-hidden /><div><div className="font-medium text-text-primary">启用 TLS</div><div className="mt-1 text-body-secondary text-text-tertiary">连接使用系统根证书，或使用下方 CA/客户端证书材料。</div></div></div><Switch checked={field.value} onCheckedChange={field.onChange} disabled={disabled} aria-label="启用 MQTT TLS" /></div>} /></div>
            <div className="md:col-span-2"><Field label="CA 证书" htmlFor="mqtt-ca-certificate" error={errors.caCertificate?.message} help="可留空以使用部署环境的系统根证书。"><Textarea id="mqtt-ca-certificate" rows={5} spellCheck={false} disabled={disabled || !tlsEnabled} className="font-mono text-xs leading-5" placeholder={tlsEnabled ? "-----BEGIN CERTIFICATE-----" : "启用 TLS 后填写"} {...register("caCertificate")} /></Field></div>
            <div className="md:col-span-2"><Field label="客户端证书" htmlFor="mqtt-client-certificate" error={errors.clientCertificate?.message}><Textarea id="mqtt-client-certificate" rows={5} spellCheck={false} disabled={disabled || !tlsEnabled} className="font-mono text-xs leading-5" placeholder={tlsEnabled ? "-----BEGIN CERTIFICATE-----" : "启用 TLS 后填写"} {...register("clientCertificate")} /></Field></div>
            <SecretField label="客户端私钥" actionName="clientPrivateKeyAction" valueName="clientPrivateKey" configured={config?.clientPrivateKeyConfigured ?? false} action={clientPrivateKeyAction} form={form} error={errors.clientPrivateKey?.message || errors.clientPrivateKeyAction?.message} disabled={disabled || !tlsEnabled} placeholder="仅在设置新值时输入" multiline />
          </FormSection>

          <ConfigDisclosure storageKey="mqtt-config-advanced-open" title="高级连接" description="Keep Alive、连接超时、重连和 Raw 上报参数。">
            <NumberField label="Keep Alive（秒）" name="keepAliveSeconds" register={register} error={errors.keepAliveSeconds?.message} min={5} max={3600} disabled={disabled} />
            <NumberField label="连接超时（毫秒）" name="connectTimeoutMs" register={register} error={errors.connectTimeoutMs?.message} min={100} max={120000} disabled={disabled} />
            <NumberField label="最小重连间隔（毫秒）" name="reconnectMinMs" register={register} error={errors.reconnectMinMs?.message} min={100} max={60000} disabled={disabled} />
            <NumberField label="最大重连间隔（毫秒）" name="reconnectMaxMs" register={register} error={errors.reconnectMaxMs?.message} min={100} max={600000} disabled={disabled} />
            <Field label="Topic 前缀" htmlFor="mqtt-topic-prefix" required error={errors.topicPrefix?.message} help="例如 edge；不会自动添加 MQTT 通配符。"><Input id="mqtt-topic-prefix" disabled={disabled} {...register("topicPrefix")} /></Field>
            <NumberField label="Raw 上报间隔（毫秒）" name="rawPublishIntervalMs" register={register} error={errors.rawPublishIntervalMs?.message} min={50} max={3600000} disabled={disabled} help="只限制 MQTT raw publish 频率，不改变采集周期。" />
          </ConfigDisclosure>

          <div id="reliability">
            <ConfigDisclosure storageKey="mqtt-config-reliability-open" title="可靠性" description="Reliable Outbox、Command Journal 和 bounded command queue 容量。">
              <NumberField label="Outbox 行数上限" name="outboxMaxRows" register={register} error={errors.outboxMaxRows?.message} min={1} max={1000000} disabled={disabled} />
              <NumberField label="Outbox 字节上限" name="outboxMaxBytes" register={register} error={errors.outboxMaxBytes?.message} min={1024} max={1024 * 1024 * 1024} disabled={disabled} help={`当前值：${form.watch("outboxMaxBytes")}`} />
              <NumberField label="Outbox 保留天数" name="outboxRetentionDays" register={register} error={errors.outboxRetentionDays?.message} min={1} max={3650} disabled={disabled} />
              <NumberField label="Command Journal 保留天数" name="commandJournalRetentionDays" register={register} error={errors.commandJournalRetentionDays?.message} min={1} max={3650} disabled={disabled} />
              <NumberField label="Command Journal 行数上限" name="commandJournalMaxRows" register={register} error={errors.commandJournalMaxRows?.message} min={1} max={1000000} disabled={disabled} />
              <NumberField label="Command 队列容量" name="commandQueueCapacity" register={register} error={errors.commandQueueCapacity?.message} min={1} max={100000} disabled={disabled} />
              <NumberField label="Poll fairness" name="commandPollFairness" register={register} error={errors.commandPollFairness?.message} min={1} max={100} disabled={disabled} help="Command burst 下允许普通 poll 继续获得调度机会。" />
            </ConfigDisclosure>
          </div>

          {testResult && <div className="rounded-control border border-info-border bg-info-background px-space-4 py-space-3 text-sm text-info" role="status">{testResult}</div>}
          <div className="flex flex-wrap items-center justify-end gap-space-2 border-t border-border pt-space-4">{isDirty && <span className="mr-auto text-body-secondary text-warning">有未保存的配置变更</span>}<Button type="button" variant="secondary" onClick={() => { if (config) reset(toFormValues(config)); }} disabled={disabled || !isDirty}><RotateCcw className="h-4 w-4" aria-hidden />放弃变更</Button><PermissionGuard permissionCode="mqtt:config:edit" fallback={<span className="text-body-secondary text-text-tertiary">当前账号无配置写入权限</span>}><Button type="submit" variant="primary" disabled={isSubmitting || !config}><Save className="h-4 w-4" aria-hidden />{isSubmitting ? "保存中..." : "保存配置"}</Button></PermissionGuard></div>
        </form>
      )}
    </MqttPageLayout>
  );
}
