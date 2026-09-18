import { zodResolver } from "@hookform/resolvers/zod";
import { ArrowDown, ArrowUp, Pencil, Plus, RefreshCw, RotateCcw, Search, Trash2 } from "lucide-react";
import { useEffect, useState } from "react";
import { useFieldArray, useForm, useWatch } from "react-hook-form";
import { z } from "zod";
import {
  createAcquisitionDevice,
  deleteAcquisitionDevice,
  getAcquisitionChannels,
  getAcquisitionDevices,
  getAcquisitionScripts,
  updateAcquisitionDevice,
} from "@/api/acquisition";
import { ConfirmDialog } from "@/components/common/confirm-dialog";
import { DataTable } from "@/components/common/data-table";
import { DataTableCard } from "@/components/common/data-table-card";
import { Field } from "@/components/common/field";
import { FormDialog } from "@/components/common/form-dialog";
import { PageHeader } from "@/components/common/page-header";
import { Pagination } from "@/components/common/pagination";
import { SearchFilterBar } from "@/components/common/search-filter-bar";
import { StatusTag } from "@/components/common/status-tag";
import { toast } from "@/components/common/toast-store";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { useListPage } from "@/hooks/use-list-page";
import { getErrorMessage } from "@/lib/api-error";
import { getUnitIDRange } from "@/lib/acquisition-validation";
import type {
  AcquisitionChannel,
  AcquisitionDevice,
  AcquisitionDeviceInput,
  AcquisitionScript,
  ApiStatus,
  DataTableColumn,
} from "@/types";

const networkEndpointSchema = z.object({
  host: z.string().trim().max(255, "主机地址不能超过 255 个字符"),
  port: z.coerce.number().int().min(1, "端口范围为 1～65535").max(65535, "端口范围为 1～65535"),
});

const deviceSchema = z.object({
  name: z.string().trim().min(1, "设备名称不能为空").max(100, "设备名称不能超过 100 个字符"),
  deviceType: z.literal("FEED_PROTECTOR"),
  channelId: z.coerce.number().int().positive("请选择通信通道"),
  scriptId: z.preprocess(
    (value) => (value === "" || value === undefined ? null : value),
    z.coerce.number().int().positive("请选择已发布的协议脚本").nullable(),
  ),
  unitId: z.coerce.number().int().min(0, "Unit ID 范围为 0～255").max(255, "Unit ID 范围为 0～255"),
  networkEndpoint: networkEndpointSchema.optional(),
  pollIntervalMs: z.coerce.number().int().positive("采集周期必须大于 0"),
  failureThreshold: z.coerce.number().int().positive("离线阈值必须大于 0"),
  enabled: z.coerce.number().pipe(z.union([z.literal(0), z.literal(1)])),
  registerBlocks: z.array(z.object({
    id: z.number().optional(),
    name: z.string().trim().min(1, "读取块名称不能为空").max(100, "读取块名称不能超过 100 个字符"),
    functionCode: z.coerce.number().int().refine((value) => value === 3 || value === 4, "功能码仅支持 FC03 或 FC04"),
    startAddress: z.coerce.number().int().min(0, "起始地址不能小于 0").max(65535, "起始地址不能超过 65535"),
    quantity: z.coerce.number().int().min(1, "数量范围为 1～125").max(125, "数量范围为 1～125"),
    sortOrder: z.coerce.number().int().min(0, "顺序不能小于 0"),
  })).superRefine((blocks, context) => {
    const names = new Set<string>();
    const intervals = new Map<number, Array<{ start: number; end: number }>>();
    blocks.forEach((block, index) => {
      if (names.has(block.name.trim())) {
        context.addIssue({ code: z.ZodIssueCode.custom, path: [index, "name"], message: "读取块名称必须唯一" });
      }
      names.add(block.name.trim());
      if (block.startAddress + block.quantity - 1 > 65535) {
        context.addIssue({ code: z.ZodIssueCode.custom, path: [index, "quantity"], message: "起始地址与数量超出寄存器范围" });
      }
      const ranges = intervals.get(block.functionCode) ?? [];
      if (ranges.some((range) => block.startAddress <= range.end && range.start <= block.startAddress + block.quantity - 1)) {
        context.addIssue({ code: z.ZodIssueCode.custom, path: [index, "startAddress"], message: "同一功能码的地址区间不能重叠" });
      }
      ranges.push({ start: block.startAddress, end: block.startAddress + block.quantity - 1 });
      intervals.set(block.functionCode, ranges);
    });
  }),
});

type DeviceFormValues = z.infer<typeof deviceSchema>;
type ConfirmState = AcquisitionDevice | null;
type DeviceFilterState = {
  name: string;
  channelId: string;
  enabled: "" | "0" | "1";
};

const DEFAULT_FILTERS: DeviceFilterState = {
  name: "",
  channelId: "",
  enabled: "",
};

const emptyValues: DeviceFormValues = {
  name: "",
  deviceType: "FEED_PROTECTOR",
  channelId: 0,
  scriptId: null,
  unitId: 1,
  networkEndpoint: { host: "", port: 502 },
  pollIntervalMs: 1000,
  failureThreshold: 3,
  enabled: 1,
  registerBlocks: [{ id: undefined, name: "", functionCode: 3, startAddress: 0, quantity: 1, sortOrder: 0 }],
};

function toFormValues(device?: AcquisitionDevice): DeviceFormValues {
  return device
    ? {
        name: device.name,
        deviceType: device.deviceType,
        channelId: device.channelId,
        scriptId: device.scriptId ?? null,
        unitId: device.unitId,
        networkEndpoint: device.networkEndpoint ?? emptyValues.networkEndpoint,
        pollIntervalMs: device.pollIntervalMs,
        failureThreshold: device.failureThreshold,
        enabled: device.enabled,
        registerBlocks: (device.registerBlocks ?? []).map((block, index) => ({
          id: block.id,
          name: block.name,
          functionCode: block.functionCode,
          startAddress: block.startAddress,
          quantity: block.quantity,
          sortOrder: block.sortOrder ?? index,
        })),
      }
    : emptyValues;
}

function toPayload(values: DeviceFormValues, network: boolean): AcquisitionDeviceInput {
  return {
    ...values,
    networkEndpoint: network ? {
      host: values.networkEndpoint?.host.trim() ?? "",
      port: values.networkEndpoint?.port ?? 0,
    } : undefined,
    registerBlocks: values.registerBlocks.map((block, index) => ({
      ...block,
      functionCode: block.functionCode as 3 | 4,
      sortOrder: index,
    })),
  };
}

export function AcquisitionDevicesPage() {
  const list = useListPage<DeviceFilterState, AcquisitionDevice>({
    fetch: getAcquisitionDevices,
    defaultFilters: DEFAULT_FILTERS,
    toQuery: (filters, page, pageSize) => ({
      page,
      pageSize,
      name: filters.name.trim() || undefined,
      ...(filters.channelId ? { channelId: Number(filters.channelId) } : {}),
      enabled: filters.enabled === "" ? undefined : (Number(filters.enabled) as ApiStatus),
    }),
    defaultPageSize: 10,
    onError: (error) =>
      toast.error({ title: "设备加载失败", description: getErrorMessage(error, "无法获取采集设备") }),
  });
  const [channels, setChannels] = useState<AcquisitionChannel[]>([]);
  const [channelsLoading, setChannelsLoading] = useState(true);
  const [scripts, setScripts] = useState<AcquisitionScript[]>([]);
  const [scriptsLoading, setScriptsLoading] = useState(true);
  const [formOpen, setFormOpen] = useState(false);
  const [editing, setEditing] = useState<AcquisitionDevice | null>(null);
  const [confirm, setConfirm] = useState<ConfirmState>(null);
  const [submitting, setSubmitting] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const form = useForm<DeviceFormValues>({
    resolver: zodResolver(deviceSchema),
    defaultValues: emptyValues,
  });

  const loadChannels = async () => {
    setChannelsLoading(true);
    try {
      const result = await getAcquisitionChannels({ page: 1, pageSize: 500 });
      setChannels(result.records);
    } catch (error) {
      setChannels([]);
      toast.error({ title: "通道加载失败", description: getErrorMessage(error, "无法选择通信通道") });
    } finally {
      setChannelsLoading(false);
    }
  };

  const loadScripts = async () => {
    setScriptsLoading(true);
    try {
      const result = await getAcquisitionScripts({ page: 1, pageSize: 500 });
      setScripts(result.records.filter((script) => script.publishedVersion != null));
    } catch (error) {
      setScripts([]);
      toast.error({ title: "脚本加载失败", description: getErrorMessage(error, "无法选择动态协议脚本") });
    } finally {
      setScriptsLoading(false);
    }
  };

  useEffect(() => {
    void loadChannels();
    void loadScripts();
  }, []);

  const openForm = (device?: AcquisitionDevice) => {
    setEditing(device ?? null);
    form.reset(toFormValues(device));
    setFormOpen(true);
    void loadChannels();
    void loadScripts();
  };

  const submit = async (values: DeviceFormValues) => {
    const channel = channels.find((item) => item.id === values.channelId);
    const network = channel?.protocol !== "MODBUS_RTU";
    if (!channel) {
      form.setError("channelId", { type: "validate", message: "请选择通信通道" });
      return;
    }
    if (network && (!values.networkEndpoint?.host.trim() || !values.networkEndpoint.port)) {
      form.setError("networkEndpoint.host", { type: "validate", message: "网络设备必须配置 host" });
      return;
    }
    const unitIDRange = getUnitIDRange(channel.protocol);
    if (values.unitId < unitIDRange.min || values.unitId > unitIDRange.max) {
      form.setError("unitId", { type: "validate", message: unitIDRange.message });
      return;
    }
    setSubmitting(true);
    try {
      if (editing) {
        await updateAcquisitionDevice(editing.id, toPayload(values, network));
        toast.success("设备已更新，后续采集周期生效");
      } else {
        await createAcquisitionDevice(toPayload(values, network));
        toast.success("设备已创建，后续采集周期生效");
      }
      setFormOpen(false);
      list.reload();
    } catch (error) {
      toast.error({ title: "保存失败", description: getErrorMessage(error, "请检查设备配置后重试") });
    } finally {
      setSubmitting(false);
    }
  };

  const remove = async () => {
    if (!confirm) return;
    setDeleting(true);
    try {
      await deleteAcquisitionDevice(confirm.id);
      toast.success("设备已删除");
      setConfirm(null);
      list.reload();
    } catch (error) {
      toast.error({ title: "删除失败", description: getErrorMessage(error, "请稍后重试") });
    } finally {
      setDeleting(false);
    }
  };

  const channelName = (id: number) => {
    const channel = channels.find((item) => item.id === id);
    return channel ? `${channel.name} · ${channel.protocol}` : `通道 ${id}`;
  };
  const columns: DataTableColumn<AcquisitionDevice>[] = [
    {
      title: "设备",
      key: "device",
      width: 260,
      render: (_, device) => (
        <div>
          <div className="font-medium text-text-primary">{device.name}</div>
          <div className="text-xs text-text-tertiary">馈电保护器 · ID {device.id}</div>
        </div>
      ),
    },
    { title: "通信通道", key: "channel", width: 180, render: (_, device) => channelName(device.channelId) },
    { title: "Unit ID", dataIndex: "unitId", width: 100, render: (value) => `#${value}` },
    {
      title: "网络端点",
      key: "networkEndpoint",
      width: 190,
      render: (_, device) => device.networkEndpoint
        ? `${device.networkEndpoint.host}:${device.networkEndpoint.port}`
        : "串口通道",
    },
    { title: "采集周期", dataIndex: "pollIntervalMs", width: 120, render: (value) => `${value} ms` },
    { title: "离线阈值", dataIndex: "failureThreshold", width: 110, render: (value) => `${value} 次` },
    {
      title: "状态",
      dataIndex: "enabled",
      width: 100,
      render: (value) => <StatusTag tone={value === 1 ? "success" : "neutral"}>{value === 1 ? "启用" : "禁用"}</StatusTag>,
    },
    {
      title: "操作",
      key: "actions",
      width: 180,
      nowrap: true,
      render: (_, device) => (
        <div className="inline-flex gap-1">
          <Button size="sm" variant="ghost" onClick={() => openForm(device)}>
            <Pencil className="h-4 w-4" aria-hidden />
            编辑
          </Button>
          <Button size="sm" variant="ghost" className="text-error hover:text-error" onClick={() => setConfirm(device)}>
            <Trash2 className="h-4 w-4" aria-hidden />
            删除
          </Button>
        </div>
      ),
    },
  ];

  return (
    <>
      <PageHeader
        title="设备管理"
        description="配置设备地址、寄存器读取块、采集周期和离线判定阈值。"
        actions={
          <Button variant="primary" onClick={() => openForm()}>
            <Plus className="h-4 w-4" aria-hidden />
            新建设备
          </Button>
        }
      />
      <SearchFilterBar
        actions={
          <>
            <Button variant="secondary" onClick={list.resetFilters}>
              <RotateCcw className="h-4 w-4" aria-hidden />
              重置
            </Button>
            <Button variant="primary" onClick={list.submitFilters}>
              <Search className="h-4 w-4" aria-hidden />
              查询
            </Button>
          </>
        }
      >
        <form className="contents" onSubmit={(event) => { event.preventDefault(); list.submitFilters(); }}>
          <Input
            value={list.filters.name}
            onChange={(event) => list.setFilter("name", event.target.value)}
            placeholder="设备名称"
            aria-label="筛选设备名称"
          />
          <Select
            value={list.filters.channelId}
            onChange={(event) => list.setFilter("channelId", event.target.value)}
            disabled={channelsLoading}
            aria-label="筛选通信通道"
          >
            <option value="">全部通道</option>
            {channels.map((channel) => (
                <option key={channel.id} value={channel.id}>
                {channel.name}（{channel.protocol}）
              </option>
            ))}
          </Select>
          <Select
            value={list.filters.enabled}
            onChange={(event) => list.setFilter("enabled", event.target.value as DeviceFilterState["enabled"])}
            aria-label="筛选状态"
          >
            <option value="">全部状态</option>
            <option value="1">启用</option>
            <option value="0">禁用</option>
          </Select>
        </form>
      </SearchFilterBar>
      <DataTableCard
        toolbar={
          <div className="flex items-center justify-between border-b border-border px-card py-space-3">
            <span className="text-sm text-text-secondary">首阶段设备类型：馈电保护器</span>
            <Button size="sm" variant="secondary" onClick={list.reload} disabled={list.loading}>
              <RefreshCw className="h-4 w-4" aria-hidden />
              刷新
            </Button>
          </div>
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
        <DataTable columns={columns} dataSource={list.data} rowKey="id" loading={list.loading} error={list.error} minWidth={1050} />
      </DataTableCard>
      <FormDialog
        open={formOpen}
        title={editing ? "编辑采集设备" : "新建设备"}
        description="读取块使用零基地址；当前阶段只采集原始 16 位寄存器值。"
        loading={submitting}
        onCancel={() => setFormOpen(false)}
        onSubmit={() => void form.handleSubmit(submit)()}
      >
        <DeviceForm
          form={form}
          channels={channels}
          channelsLoading={channelsLoading}
          scripts={scripts}
          scriptsLoading={scriptsLoading}
          loading={submitting}
        />
      </FormDialog>
      <ConfirmDialog
        open={Boolean(confirm)}
        title="删除采集设备"
        description={`确认删除「${confirm?.name ?? ""}」吗？`}
        danger
        loading={deleting}
        onCancel={() => setConfirm(null)}
        onConfirm={() => void remove()}
        confirmText="删除"
      />
    </>
  );
}

function DeviceForm({
  form,
  channels,
  channelsLoading,
  scripts,
  scriptsLoading,
  loading,
}: {
  form: ReturnType<typeof useForm<DeviceFormValues>>;
  channels: AcquisitionChannel[];
  channelsLoading: boolean;
  scripts: AcquisitionScript[];
  scriptsLoading: boolean;
  loading: boolean;
}) {
  const { register, control, formState: { errors } } = form;
  const channelId = useWatch({ control, name: "channelId" });
  const selectedChannel = channels.find((channel) => channel.id === channelId);
  const isNetwork = selectedChannel != null && selectedChannel.protocol !== "MODBUS_RTU";
  const unitIDRange = getUnitIDRange(selectedChannel?.protocol);
  const blocks = useFieldArray({ control, name: "registerBlocks" });
  return (
    <div className="grid gap-4 md:grid-cols-2">
      <Field label="设备名称" required error={errors.name?.message}>
        <Input {...register("name")} placeholder="例如：馈电保护器 01" disabled={loading} />
      </Field>
      <Field label="设备类型" required error={errors.deviceType?.message}>
        <Select {...register("deviceType")} disabled>
          <option value="FEED_PROTECTOR">馈电保护器</option>
        </Select>
      </Field>
      <Field label="通信通道" required error={errors.channelId?.message} help={channelsLoading ? "通道加载中..." : undefined}>
        <Select {...register("channelId", { valueAsNumber: true })} disabled={loading || channelsLoading}>
          <option value="0">请选择通信通道</option>
          {channels.map((channel) => <option key={channel.id} value={channel.id}>{channel.name}（{channel.protocol}）</option>)}
        </Select>
      </Field>
      <Field
        label="动态协议脚本"
        error={errors.scriptId?.message}
        help={scriptsLoading ? "脚本加载中..." : "仅显示已有 published version 的脚本；不选择则只运行固定读取块。"}
      >
        <Select {...register("scriptId")} disabled={loading || scriptsLoading}>
          <option value="">不绑定（仅固定读取块）</option>
          {scripts.map((script) => (
            <option key={script.id} value={script.id}>
              {script.name}（v{script.publishedVersion?.versionNo}）
            </option>
          ))}
        </Select>
      </Field>
      <Field label="Modbus Unit ID" required error={errors.unitId?.message} help={unitIDRange.message}>
        <Input {...register("unitId", { valueAsNumber: true })} type="number" min={unitIDRange.min} max={unitIDRange.max} disabled={loading} />
      </Field>
      {isNetwork && <>
        <Field label="网络主机" required error={errors.networkEndpoint?.host?.message} help="支持 IPv4、IPv6 和 hostname。">
          <Input {...register("networkEndpoint.host")} placeholder="例如：192.168.1.10" disabled={loading} />
        </Field>
        <Field label="网络端口" required error={errors.networkEndpoint?.port?.message}>
          <Input {...register("networkEndpoint.port", { valueAsNumber: true })} type="number" min={1} max={65535} disabled={loading} />
        </Field>
      </>}
      <Field label="采集周期（毫秒）" required error={errors.pollIntervalMs?.message}>
        <Input {...register("pollIntervalMs", { valueAsNumber: true })} type="number" min={1} disabled={loading} />
      </Field>
      <Field label="连续失败阈值" required error={errors.failureThreshold?.message} help="默认连续 3 次失败判定离线。">
        <Input {...register("failureThreshold", { valueAsNumber: true })} type="number" min={1} disabled={loading} />
      </Field>
      <Field label="状态" required error={errors.enabled?.message}>
        <Select {...register("enabled", { valueAsNumber: true })} disabled={loading}>
          <option value="1">启用</option><option value="0">禁用</option>
        </Select>
      </Field>
      <div className="md:col-span-2 rounded-admin border border-border bg-neutral-background p-space-4">
        <div className="mb-space-3 flex items-center justify-between gap-space-3">
          <div>
            <div className="font-medium text-text-primary">寄存器读取块</div>
            <div className="mt-1 text-helper text-text-tertiary">FC03 保持寄存器、FC04 输入寄存器；地址从 0 开始，值将在实时寄存器页面显示。</div>
          </div>
          <Button size="sm" variant="secondary" onClick={() => blocks.append({ id: undefined, name: "", functionCode: 3, startAddress: 0, quantity: 1, sortOrder: blocks.fields.length })} disabled={loading}>
            <Plus className="h-4 w-4" aria-hidden />
            添加读取块
          </Button>
        </div>
        <div className="space-y-space-3">
          {blocks.fields.map((field, index) => {
            const blockErrors = errors.registerBlocks?.[index];
            return (
              <div key={field.id} className="rounded-control border border-border bg-surface p-space-3">
                <div className="mb-space-3 flex items-center justify-between gap-space-2">
                  <span className="text-sm font-medium text-text-secondary">读取块 {index + 1}</span>
                  <div className="inline-flex gap-1">
                    <Button size="icon" variant="ghost" aria-label="上移读取块" onClick={() => index > 0 && blocks.move(index, index - 1)} disabled={loading || index === 0}>
                      <ArrowUp className="h-4 w-4" aria-hidden />
                    </Button>
                    <Button size="icon" variant="ghost" aria-label="下移读取块" onClick={() => index < blocks.fields.length - 1 && blocks.move(index, index + 1)} disabled={loading || index === blocks.fields.length - 1}>
                      <ArrowDown className="h-4 w-4" aria-hidden />
                    </Button>
                    <Button size="sm" variant="ghost" className="text-error hover:text-error" onClick={() => blocks.remove(index)} disabled={loading}>
                      <Trash2 className="h-4 w-4" aria-hidden />
                      删除
                    </Button>
                  </div>
                </div>
                <div className="grid gap-4 md:grid-cols-2">
                  <Field label="读取块名称" required error={blockErrors?.name?.message}>
                    <Input {...register(`registerBlocks.${index}.name`)} placeholder="例如：测量值" disabled={loading} />
                  </Field>
                  <Field label="功能码" required error={blockErrors?.functionCode?.message}>
                    <Select {...register(`registerBlocks.${index}.functionCode`, { valueAsNumber: true })} disabled={loading}>
                      <option value="3">FC03 · 保持寄存器</option>
                      <option value="4">FC04 · 输入寄存器</option>
                    </Select>
                  </Field>
                  <Field label="零基起始地址" required error={blockErrors?.startAddress?.message}>
                    <Input {...register(`registerBlocks.${index}.startAddress`, { valueAsNumber: true })} type="number" min={0} max={65535} disabled={loading} />
                  </Field>
                  <Field label="寄存器数量" required error={blockErrors?.quantity?.message}>
                    <Input {...register(`registerBlocks.${index}.quantity`, { valueAsNumber: true })} type="number" min={1} max={125} disabled={loading} />
                  </Field>
                </div>
              </div>
            );
          })}
          {blocks.fields.length === 0 && <div className="rounded-control border border-dashed border-border p-space-4 text-sm text-text-tertiary">暂未配置读取块；保存后设备会显示为“未配置读取块”，添加读取块后才会开始采集。</div>}
        </div>
      </div>
    </div>
  );
}
