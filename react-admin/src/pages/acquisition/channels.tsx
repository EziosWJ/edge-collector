import { zodResolver } from "@hookform/resolvers/zod";
import { Pencil, Plus, RefreshCw, RotateCcw, Search, Trash2 } from "lucide-react";
import { useState } from "react";
import { useForm, useWatch } from "react-hook-form";
import { z } from "zod";
import {
  createAcquisitionChannel,
  deleteAcquisitionChannel,
  getAcquisitionChannels,
  updateAcquisitionChannel,
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
  AcquisitionChannelInput,
  DataTableColumn,
  AcquisitionProtocol,
  ApiStatus,
} from "@/types";

const protocolOptions: Array<{ value: AcquisitionProtocol; label: string }> = [
  { value: "MODBUS_RTU", label: "Modbus RTU" },
  { value: "MODBUS_TCP", label: "Modbus TCP" },
  { value: "MODBUS_UDP", label: "Modbus UDP（MBAP）" },
  { value: "MODBUS_RTU_OVER_UDP", label: "Modbus RTU over UDP" },
];

const serialConfigSchema = z.object({
  port: z.string().trim().min(1, "串口设备不能为空").max(255, "串口设备不能超过 255 个字符"),
  baudRate: z.coerce.number().int().positive("波特率必须大于 0"),
  dataBits: z.coerce.number().pipe(z.union([z.literal(7), z.literal(8)])),
  stopBits: z.coerce.number().pipe(z.union([z.literal(1), z.literal(2)])),
  parity: z.enum(["N", "E", "O"]),
});

const channelSchema = z.object({
  name: z.string().trim().min(1, "通道名称不能为空").max(100, "通道名称不能超过 100 个字符"),
  protocol: z.enum(["MODBUS_RTU", "MODBUS_TCP", "MODBUS_UDP", "MODBUS_RTU_OVER_UDP"]),
  serialConfig: serialConfigSchema.optional(),
  timeoutMs: z.coerce.number().int().positive("通信超时必须大于 0"),
  interRequestDelayMs: z.coerce.number().int().min(0, "报文间隔延迟不能小于 0").max(60000, "报文间隔延迟不能超过 60000 毫秒"),
  enabled: z.coerce.number().pipe(z.union([z.literal(0), z.literal(1)])),
}).superRefine((value, context) => {
  if (value.protocol === "MODBUS_RTU" && !value.serialConfig) {
    context.addIssue({ code: z.ZodIssueCode.custom, path: ["serialConfig"], message: "Modbus RTU 必须配置串口参数" });
  }
});

type ChannelFormValues = z.infer<typeof channelSchema>;
type ConfirmState = AcquisitionChannel | null;
type ChannelFilterState = {
  name: string;
  protocol: "" | AcquisitionProtocol;
  enabled: "" | "0" | "1";
};

const DEFAULT_FILTERS: ChannelFilterState = {
  name: "",
  protocol: "",
  enabled: "",
};

const emptyValues: ChannelFormValues = {
  name: "",
  protocol: "MODBUS_RTU",
  serialConfig: { port: "", baudRate: 19200, dataBits: 8, stopBits: 2, parity: "N" },
  timeoutMs: 300,
  interRequestDelayMs: 0,
  enabled: 1,
};

function toFormValues(channel?: AcquisitionChannel): ChannelFormValues {
  return channel
      ? {
        name: channel.name,
        protocol: channel.protocol,
        serialConfig: channel.serialConfig
          ? {
              port: channel.serialConfig.port,
              baudRate: channel.serialConfig.baudRate,
              dataBits: channel.serialConfig.dataBits as 7 | 8,
              stopBits: channel.serialConfig.stopBits as 1 | 2,
              parity: channel.serialConfig.parity,
            }
          : emptyValues.serialConfig,
        timeoutMs: channel.timeoutMs,
        interRequestDelayMs: channel.interRequestDelayMs,
        enabled: channel.enabled,
      }
    : emptyValues;
}

function toPayload(values: ChannelFormValues): AcquisitionChannelInput {
  return values.protocol === "MODBUS_RTU"
    ? values
    : { ...values, serialConfig: undefined };
}

function protocolLabel(protocol: AcquisitionProtocol) {
  return protocolOptions.find((option) => option.value === protocol)?.label ?? protocol;
}

export function AcquisitionChannelsPage() {
  const list = useListPage<ChannelFilterState, AcquisitionChannel>({
    fetch: getAcquisitionChannels,
    defaultFilters: DEFAULT_FILTERS,
    toQuery: (filters, page, pageSize) => ({
      page,
      pageSize,
      name: filters.name.trim() || undefined,
      protocol: filters.protocol || undefined,
      enabled: filters.enabled === "" ? undefined : (Number(filters.enabled) as ApiStatus),
    }),
    defaultPageSize: 10,
    onError: (error) =>
      toast.error({
        title: "通道加载失败",
        description: getErrorMessage(error, "无法获取通信通道"),
      }),
  });
  const [formOpen, setFormOpen] = useState(false);
  const [editing, setEditing] = useState<AcquisitionChannel | null>(null);
  const [confirm, setConfirm] = useState<ConfirmState>(null);
  const [submitting, setSubmitting] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const form = useForm<ChannelFormValues>({
    resolver: zodResolver(channelSchema),
    defaultValues: emptyValues,
  });

  const openForm = (channel?: AcquisitionChannel) => {
    setEditing(channel ?? null);
    form.reset(toFormValues(channel));
    setFormOpen(true);
  };

  const submit = async (values: ChannelFormValues) => {
    setSubmitting(true);
    try {
      if (editing) {
        await updateAcquisitionChannel(editing.id, toPayload(values));
        toast.success("通信通道已更新，后续采集请求生效");
      } else {
        await createAcquisitionChannel(toPayload(values));
        toast.success("通信通道已创建，后续采集请求生效");
      }
      setFormOpen(false);
      list.reload();
    } catch (error) {
      toast.error({ title: "保存失败", description: getErrorMessage(error, "请检查配置后重试") });
    } finally {
      setSubmitting(false);
    }
  };

  const remove = async () => {
    if (!confirm) return;
    setDeleting(true);
    try {
      await deleteAcquisitionChannel(confirm.id);
      toast.success("通信通道已删除");
      setConfirm(null);
      list.reload();
    } catch (error) {
      toast.error({ title: "删除失败", description: getErrorMessage(error, "通道可能仍关联设备") });
    } finally {
      setDeleting(false);
    }
  };

  const columns: DataTableColumn<AcquisitionChannel>[] = [
    {
      title: "通道",
      key: "channel",
      width: 240,
      render: (_, channel) => (
        <div>
          <div className="font-medium text-text-primary">{channel.name}</div>
          <div className="text-xs text-text-tertiary">ID {channel.id}</div>
        </div>
      ),
    },
    {
      title: "协议",
      dataIndex: "protocol",
      width: 180,
      render: (value) => protocolLabel(value as AcquisitionProtocol),
    },
    {
      title: "端点/串口",
      key: "endpoint",
      width: 260,
      render: (_, channel) => channel.serialConfig
        ? `${channel.serialConfig.port} · ${channel.serialConfig.baudRate} ${channel.serialConfig.dataBits}${channel.serialConfig.parity}${channel.serialConfig.stopBits}`
        : "设备级网络端点",
    },
    {
      title: "超时",
      dataIndex: "timeoutMs",
      width: 100,
      render: (value) => `${value} ms`,
    },
    {
      title: "报文间隔",
      dataIndex: "interRequestDelayMs",
      width: 120,
      render: (value) => `${value} ms`,
    },
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
      render: (_, channel) => (
        <div className="inline-flex gap-1">
          <Button size="sm" variant="ghost" onClick={() => openForm(channel)}>
            <Pencil className="h-4 w-4" aria-hidden />
            编辑
          </Button>
          <Button size="sm" variant="ghost" className="text-error hover:text-error" onClick={() => setConfirm(channel)}>
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
        title="通信通道"
        description="维护 Modbus 传输协议与公共调度参数。配置保存后由运行中的采集器刷新。"
        actions={
          <Button variant="primary" onClick={() => openForm()}>
            <Plus className="h-4 w-4" aria-hidden />
            新建通道
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
            placeholder="通道名称"
            aria-label="筛选通道名称"
          />
          <Select
            value={list.filters.protocol}
            onChange={(event) => list.setFilter("protocol", event.target.value as ChannelFilterState["protocol"])}
            aria-label="筛选协议"
          >
            <option value="">全部协议</option>
            {protocolOptions.map((option) => (
              <option key={option.value} value={option.value}>{option.label}</option>
            ))}
          </Select>
          <Select
            value={list.filters.enabled}
            onChange={(event) => list.setFilter("enabled", event.target.value as ChannelFilterState["enabled"])}
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
            <span className="text-sm text-text-secondary">Modbus RTU / TCP / UDP</span>
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
        <DataTable columns={columns} dataSource={list.data} rowKey="id" loading={list.loading} error={list.error} minWidth={980} />
      </DataTableCard>
      <FormDialog
        open={formOpen}
        title={editing ? "编辑通信通道" : "新建通信通道"}
        description="协议创建后不可修改；网络协议的 endpoint 配置在设备上。"
        loading={submitting}
        onCancel={() => setFormOpen(false)}
        onSubmit={() => void form.handleSubmit(submit)()}
      >
        <ChannelForm form={form} loading={submitting} editing={Boolean(editing)} />
      </FormDialog>
      <ConfirmDialog
        open={Boolean(confirm)}
        title="删除通信通道"
        description={`确认删除「${confirm?.name ?? ""}」吗？只有未关联设备的通道可以删除。`}
        danger
        loading={deleting}
        onCancel={() => setConfirm(null)}
        onConfirm={() => void remove()}
        confirmText="删除"
      />
    </>
  );
}

function ChannelForm({ form, loading, editing }: { form: ReturnType<typeof useForm<ChannelFormValues>>; loading: boolean; editing: boolean }) {
  const { register, control, formState: { errors } } = form;
  const protocol = useWatch({ control, name: "protocol" });
  const isRTU = protocol === "MODBUS_RTU";
  return (
    <div className="grid gap-4 md:grid-cols-2">
      <Field label="通道名称" required error={errors.name?.message}>
        <Input {...register("name")} placeholder="例如：一号馈电柜" disabled={loading} />
      </Field>
      <Field label="协议" required error={errors.protocol?.message} help="协议创建后不可修改。">
        <Select {...register("protocol")} disabled={loading || editing}>
          {protocolOptions.map((option) => <option key={option.value} value={option.value}>{option.label}</option>)}
        </Select>
      </Field>
      {isRTU && <>
        <Field label="串口设备" required error={errors.serialConfig?.port?.message} help="开发机可填写虚拟串口或 PTY 路径。">
          <Input {...register("serialConfig.port")} placeholder="例如：/dev/ttyUSB0" disabled={loading} />
        </Field>
        <Field label="波特率" required error={errors.serialConfig?.baudRate?.message}>
          <Input {...register("serialConfig.baudRate", { valueAsNumber: true })} type="number" min={1} disabled={loading} />
        </Field>
        <Field label="数据位" required error={errors.serialConfig?.dataBits?.message}>
          <Select {...register("serialConfig.dataBits", { valueAsNumber: true })} disabled={loading}>
            <option value="7">7</option><option value="8">8</option>
          </Select>
        </Field>
        <Field label="停止位" required error={errors.serialConfig?.stopBits?.message}>
          <Select {...register("serialConfig.stopBits", { valueAsNumber: true })} disabled={loading}>
            <option value="1">1</option><option value="2">2</option>
          </Select>
        </Field>
        <Field label="校验方式" required error={errors.serialConfig?.parity?.message}>
          <Select {...register("serialConfig.parity")} disabled={loading}>
            <option value="N">无校验</option><option value="E">偶校验</option><option value="O">奇校验</option>
          </Select>
        </Field>
      </>}
      <Field label="通信超时（毫秒）" required error={errors.timeoutMs?.message}>
        <Input {...register("timeoutMs", { valueAsNumber: true })} type="number" min={1} disabled={loading} />
      </Field>
      <Field label="报文间隔延迟（毫秒）" required error={errors.interRequestDelayMs?.message} help="同一通道连续 Modbus 请求之间的额外等待；0 表示仅使用 RTU 协议自身间隔。">
        <Input {...register("interRequestDelayMs", { valueAsNumber: true })} type="number" min={0} max={60000} disabled={loading} />
      </Field>
      <Field label="状态" required error={errors.enabled?.message}>
        <Select {...register("enabled", { valueAsNumber: true })} disabled={loading}>
          <option value="1">启用</option><option value="0">禁用</option>
        </Select>
      </Field>
    </div>
  );
}
