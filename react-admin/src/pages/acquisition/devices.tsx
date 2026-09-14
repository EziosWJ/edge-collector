import { zodResolver } from "@hookform/resolvers/zod";
import { Pencil, Plus, RefreshCw, RotateCcw, Search, Trash2 } from "lucide-react";
import { useEffect, useState } from "react";
import { useForm } from "react-hook-form";
import { z } from "zod";
import {
  createAcquisitionDevice,
  deleteAcquisitionDevice,
  getAcquisitionChannels,
  getAcquisitionDevices,
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
import type {
  AcquisitionChannel,
  AcquisitionDevice,
  AcquisitionDeviceInput,
  DataTableColumn,
} from "@/types";

const deviceSchema = z.object({
  name: z.string().trim().min(1, "设备名称不能为空").max(100, "设备名称不能超过 100 个字符"),
  deviceType: z.literal("FEED_PROTECTOR"),
  channelId: z.coerce.number().int().positive("请选择通信通道"),
  slaveId: z.coerce.number().int().min(1, "地址范围为 1～247").max(247, "地址范围为 1～247"),
  pollIntervalMs: z.coerce.number().int().positive("采集周期必须大于 0"),
  failureThreshold: z.coerce.number().int().positive("离线阈值必须大于 0"),
  enabled: z.coerce.number().pipe(z.union([z.literal(0), z.literal(1)])),
});

type DeviceFormValues = z.infer<typeof deviceSchema>;
type ConfirmState = AcquisitionDevice | null;
type DeviceFilterState = {
  channelId: string;
};

const DEFAULT_FILTERS: DeviceFilterState = {
  channelId: "",
};

const emptyValues: DeviceFormValues = {
  name: "",
  deviceType: "FEED_PROTECTOR",
  channelId: 0,
  slaveId: 1,
  pollIntervalMs: 1000,
  failureThreshold: 3,
  enabled: 1,
};

function toFormValues(device?: AcquisitionDevice): DeviceFormValues {
  return device
    ? {
        name: device.name,
        deviceType: device.deviceType,
        channelId: device.channelId,
        slaveId: device.slaveId,
        pollIntervalMs: device.pollIntervalMs,
        failureThreshold: device.failureThreshold,
        enabled: device.enabled,
      }
    : emptyValues;
}

function toPayload(values: DeviceFormValues): AcquisitionDeviceInput {
  return values;
}

export function AcquisitionDevicesPage() {
  const list = useListPage<DeviceFilterState, AcquisitionDevice>({
    fetch: getAcquisitionDevices,
    defaultFilters: DEFAULT_FILTERS,
    toQuery: (filters, page, pageSize) => ({
      page,
      pageSize,
      ...(filters.channelId ? { channelId: Number(filters.channelId) } : {}),
    }),
    defaultPageSize: 10,
    onError: (error) =>
      toast.error({ title: "设备加载失败", description: getErrorMessage(error, "无法获取采集设备") }),
  });
  const [channels, setChannels] = useState<AcquisitionChannel[]>([]);
  const [channelsLoading, setChannelsLoading] = useState(true);
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

  useEffect(() => {
    void loadChannels();
  }, []);

  const openForm = (device?: AcquisitionDevice) => {
    setEditing(device ?? null);
    form.reset(toFormValues(device));
    setFormOpen(true);
    void loadChannels();
  };

  const submit = async (values: DeviceFormValues) => {
    setSubmitting(true);
    try {
      if (editing) {
        await updateAcquisitionDevice(editing.id, toPayload(values));
        toast.success("设备已更新，重启服务后生效");
      } else {
        await createAcquisitionDevice(toPayload(values));
        toast.success("设备已创建，重启服务后生效");
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

  const channelName = (id: number) => channels.find((channel) => channel.id === id)?.name ?? `通道 ${id}`;
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
    { title: "Slave 地址", dataIndex: "slaveId", width: 120, render: (value) => `#${value}` },
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
        description="配置馈电保护器及其 Modbus 地址、采集周期和离线判定阈值。"
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
          <Select
            value={list.filters.channelId}
            onChange={(event) => list.setFilter("channelId", event.target.value)}
            disabled={channelsLoading}
            aria-label="筛选通信通道"
          >
            <option value="">全部通道</option>
            {channels.map((channel) => (
              <option key={channel.id} value={channel.id}>
                {channel.name}（{channel.port}）
              </option>
            ))}
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
        description="寄存器地址和解析规则由馈电保护器协议适配内置。"
        loading={submitting}
        onCancel={() => setFormOpen(false)}
        onSubmit={() => void form.handleSubmit(submit)()}
      >
        <DeviceForm form={form} channels={channels} channelsLoading={channelsLoading} loading={submitting} />
      </FormDialog>
      <ConfirmDialog
        open={Boolean(confirm)}
        title="删除采集设备"
        description={`确认删除「${confirm?.name ?? ""}」吗？保存后需要重启服务。`}
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
  loading,
}: {
  form: ReturnType<typeof useForm<DeviceFormValues>>;
  channels: AcquisitionChannel[];
  channelsLoading: boolean;
  loading: boolean;
}) {
  const { register, formState: { errors } } = form;
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
          {channels.map((channel) => <option key={channel.id} value={channel.id}>{channel.name}（{channel.port}）</option>)}
        </Select>
      </Field>
      <Field label="Modbus Slave 地址" required error={errors.slaveId?.message}>
        <Input {...register("slaveId", { valueAsNumber: true })} type="number" min={1} max={247} disabled={loading} />
      </Field>
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
    </div>
  );
}
