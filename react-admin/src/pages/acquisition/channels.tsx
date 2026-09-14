import { zodResolver } from "@hookform/resolvers/zod";
import { Pencil, Plus, RefreshCw, Trash2 } from "lucide-react";
import { useState } from "react";
import { useForm } from "react-hook-form";
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
} from "@/types";

const channelSchema = z.object({
  name: z.string().trim().min(1, "通道名称不能为空").max(100, "通道名称不能超过 100 个字符"),
  port: z.string().trim().min(1, "串口设备不能为空").max(255, "串口设备不能超过 255 个字符"),
  baudRate: z.coerce.number().int().positive("波特率必须大于 0"),
  dataBits: z.coerce.number().pipe(z.union([z.literal(7), z.literal(8)])),
  stopBits: z.coerce.number().pipe(z.union([z.literal(1), z.literal(2)])),
  parity: z.enum(["N", "E", "O"]),
  timeoutMs: z.coerce.number().int().positive("通信超时必须大于 0"),
  enabled: z.coerce.number().pipe(z.union([z.literal(0), z.literal(1)])),
});

type ChannelFormValues = z.infer<typeof channelSchema>;
type ConfirmState = AcquisitionChannel | null;

const emptyValues: ChannelFormValues = {
  name: "",
  port: "",
  baudRate: 19200,
  dataBits: 8,
  stopBits: 2,
  parity: "N",
  timeoutMs: 300,
  enabled: 1,
};

function toFormValues(channel?: AcquisitionChannel): ChannelFormValues {
  return channel
    ? {
        name: channel.name,
        port: channel.port,
        baudRate: channel.baudRate,
        dataBits: channel.dataBits as 7 | 8,
        stopBits: channel.stopBits as 1 | 2,
        parity: channel.parity,
        timeoutMs: channel.timeoutMs,
        enabled: channel.enabled,
      }
    : emptyValues;
}

function toPayload(values: ChannelFormValues): AcquisitionChannelInput {
  return values;
}

export function AcquisitionChannelsPage() {
  const list = useListPage<Record<string, never>, AcquisitionChannel>({
    fetch: getAcquisitionChannels,
    defaultFilters: {},
    toQuery: (_filters, page, pageSize) => ({ page, pageSize }),
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
        toast.success("通信通道已更新，重启服务后生效");
      } else {
        await createAcquisitionChannel(toPayload(values));
        toast.success("通信通道已创建，重启服务后生效");
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
    { title: "串口设备", dataIndex: "port", width: 220 },
    {
      title: "串口参数",
      key: "serial",
      width: 260,
      render: (_, channel) => `${channel.baudRate} · ${channel.dataBits}${channel.parity}${channel.stopBits}`,
    },
    {
      title: "超时",
      dataIndex: "timeoutMs",
      width: 100,
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
        description="维护 RS485 串口参数。配置保存后需要重启服务才能开始采集。"
        actions={
          <Button variant="primary" onClick={() => openForm()}>
            <Plus className="h-4 w-4" aria-hidden />
            新建通道
          </Button>
        }
      />
      <DataTableCard
        toolbar={
          <div className="flex items-center justify-between border-b border-border px-card py-space-3">
            <span className="text-sm text-text-secondary">RS485 / Modbus RTU</span>
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
        description="RS485 通道使用 8 个数据位和常见串口校验配置。"
        loading={submitting}
        onCancel={() => setFormOpen(false)}
        onSubmit={() => void form.handleSubmit(submit)()}
      >
        <ChannelForm form={form} loading={submitting} />
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

function ChannelForm({ form, loading }: { form: ReturnType<typeof useForm<ChannelFormValues>>; loading: boolean }) {
  const { register, formState: { errors } } = form;
  return (
    <div className="grid gap-4 md:grid-cols-2">
      <Field label="通道名称" required error={errors.name?.message}>
        <Input {...register("name")} placeholder="例如：一号馈电柜" disabled={loading} />
      </Field>
      <Field label="串口设备" required error={errors.port?.message} help="开发机可填写虚拟串口或 PTY 路径。">
        <Input {...register("port")} placeholder="例如：/dev/ttyUSB0" disabled={loading} />
      </Field>
      <Field label="波特率" required error={errors.baudRate?.message}>
        <Input {...register("baudRate", { valueAsNumber: true })} type="number" min={1} disabled={loading} />
      </Field>
      <Field label="数据位" required error={errors.dataBits?.message}>
        <Select {...register("dataBits", { valueAsNumber: true })} disabled={loading}>
          <option value="7">7</option><option value="8">8</option>
        </Select>
      </Field>
      <Field label="停止位" required error={errors.stopBits?.message}>
        <Select {...register("stopBits", { valueAsNumber: true })} disabled={loading}>
          <option value="1">1</option><option value="2">2</option>
        </Select>
      </Field>
      <Field label="校验方式" required error={errors.parity?.message}>
        <Select {...register("parity")} disabled={loading}>
          <option value="N">无校验</option><option value="E">偶校验</option><option value="O">奇校验</option>
        </Select>
      </Field>
      <Field label="通信超时（毫秒）" required error={errors.timeoutMs?.message}>
        <Input {...register("timeoutMs", { valueAsNumber: true })} type="number" min={1} disabled={loading} />
      </Field>
      <Field label="状态" required error={errors.enabled?.message}>
        <Select {...register("enabled", { valueAsNumber: true })} disabled={loading}>
          <option value="1">启用</option><option value="0">禁用</option>
        </Select>
      </Field>
    </div>
  );
}
