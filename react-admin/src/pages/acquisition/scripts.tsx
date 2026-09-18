import { zodResolver } from "@hookform/resolvers/zod";
import {
  AlertTriangle,
  CheckCircle2,
  Code2,
  Eye,
  History,
  Pencil,
  Plus,
  RefreshCw,
  RotateCcw,
  Search,
  Trash2,
  Upload,
} from "lucide-react";
import { useState } from "react";
import { useForm, type UseFormReturn } from "react-hook-form";
import { z } from "zod";
import {
  createAcquisitionScript,
  deleteAcquisitionScript,
  getAcquisitionScript,
  getAcquisitionScriptStates,
  getAcquisitionScriptVersions,
  getAcquisitionScripts,
  publishAcquisitionScript,
  rollbackAcquisitionScript,
  updateAcquisitionScript,
  validateAcquisitionScript,
} from "@/api/acquisition";
import { ConfirmDialog } from "@/components/common/confirm-dialog";
import { DataTable } from "@/components/common/data-table";
import { DataTableCard } from "@/components/common/data-table-card";
import { DetailDialog } from "@/components/common/detail-dialog";
import { EmptyState } from "@/components/common/empty-state";
import { Field } from "@/components/common/field";
import { FormDialog } from "@/components/common/form-dialog";
import { PageHeader } from "@/components/common/page-header";
import { Pagination } from "@/components/common/pagination";
import { SearchFilterBar } from "@/components/common/search-filter-bar";
import { StatusTag } from "@/components/common/status-tag";
import { TableToolbar } from "@/components/common/table-toolbar";
import { toast } from "@/components/common/toast-store";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import { useListPage } from "@/hooks/use-list-page";
import { getErrorMessage } from "@/lib/api-error";
import { formatDateTime } from "@/lib/datetime";
import type {
  AcquisitionScript,
  AcquisitionScriptErrorType,
  AcquisitionScriptRuntimeState,
  AcquisitionScriptValidationResult,
  AcquisitionScriptVersion,
  DataTableColumn,
} from "@/types";

const DEFAULT_DRAFT_SOURCE = `def after_poll(ctx):
    pass
`;

const scriptSchema = z.object({
  name: z
    .string()
    .trim()
    .min(1, "脚本名称不能为空")
    .max(100, "脚本名称不能超过 100 个字符"),
  description: z
    .string()
    .trim()
    .max(500, "脚本说明不能超过 500 个字符"),
  draftSource: z
    .string()
    .min(1, "Draft 源码不能为空")
    .max(262144, "Draft 源码不能超过 256 KiB"),
});

type ScriptFormValues = z.infer<typeof scriptSchema>;
type ScriptFilterState = {
  name: string;
  published: "" | "0" | "1";
  bound: "" | "0" | "1";
};
type ConfirmAction =
  | { type: "delete"; script: AcquisitionScript }
  | { type: "rollback"; script: AcquisitionScript; version: AcquisitionScriptVersion };

const DEFAULT_FILTERS: ScriptFilterState = {
  name: "",
  published: "",
  bound: "",
};

const errorTypeLabels: Record<AcquisitionScriptErrorType, string> = {
  SCRIPT_COMPILE: "脚本编译",
  SCRIPT_RUNTIME: "脚本运行",
  SCRIPT_LIMIT: "资源限制",
  MODBUS_TRANSPORT: "Modbus 传输",
  MODBUS_EXCEPTION: "Modbus 异常",
  SCRIPT_OUTPUT: "脚本输出",
  CANCELED: "已取消",
  HOST_UNAVAILABLE: "Host 不可用",
};

function toFormValues(script?: AcquisitionScript): ScriptFormValues {
  return {
    name: script?.name ?? "",
    description: script?.description ?? "",
    draftSource: script?.draftSource ?? DEFAULT_DRAFT_SOURCE,
  };
}

function getPublishedVersionLabel(script: AcquisitionScript) {
  return script.publishedVersion ? `v${script.publishedVersion.versionNo}` : "未发布";
}

function getRuntimeStatus(state: AcquisitionScriptRuntimeState) {
  if (state.lastError) return { label: "最近失败", tone: "error" as const };
  if (state.lastSuccessAt) return { label: "最近成功", tone: "success" as const };
  return { label: "等待执行", tone: "neutral" as const };
}

function formatEventPayload(payload: AcquisitionScriptRuntimeState["events"][number]["payload"]) {
  try {
    return JSON.stringify(payload);
  } catch {
    return "无法展示事件 payload";
  }
}

export function AcquisitionScriptsPage() {
  const list = useListPage<ScriptFilterState, AcquisitionScript>({
    fetch: getAcquisitionScripts,
    defaultFilters: DEFAULT_FILTERS,
    toQuery: (filters, page, pageSize) => ({
      page,
      pageSize,
      name: filters.name.trim() || undefined,
      published: filters.published === "" ? undefined : filters.published === "1",
      bound: filters.bound === "" ? undefined : filters.bound === "1",
    }),
    defaultPageSize: 10,
    onError: (error) =>
      toast.error({
        title: "脚本列表加载失败",
        description: getErrorMessage(error, "无法获取协议脚本"),
      }),
  });
  const [formOpen, setFormOpen] = useState(false);
  const [editing, setEditing] = useState<AcquisitionScript | null>(null);
  const [detailLoading, setDetailLoading] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [validating, setValidating] = useState(false);
  const [publishing, setPublishing] = useState(false);
  const [validation, setValidation] = useState<AcquisitionScriptValidationResult | null>(null);
  const [versionsOpen, setVersionsOpen] = useState(false);
  const [versionScript, setVersionScript] = useState<AcquisitionScript | null>(null);
  const [versions, setVersions] = useState<AcquisitionScriptVersion[]>([]);
  const [versionsLoading, setVersionsLoading] = useState(false);
  const [versionsError, setVersionsError] = useState<string | null>(null);
  const [selectedVersion, setSelectedVersion] = useState<AcquisitionScriptVersion | null>(null);
  const [runtimeOpen, setRuntimeOpen] = useState(false);
  const [runtimeScript, setRuntimeScript] = useState<AcquisitionScript | null>(null);
  const [runtimeStates, setRuntimeStates] = useState<AcquisitionScriptRuntimeState[]>([]);
  const [runtimeLoading, setRuntimeLoading] = useState(false);
  const [runtimeError, setRuntimeError] = useState<string | null>(null);
  const [confirmAction, setConfirmAction] = useState<ConfirmAction | null>(null);
  const [confirmLoading, setConfirmLoading] = useState(false);
  const form = useForm<ScriptFormValues>({
    resolver: zodResolver(scriptSchema),
    defaultValues: toFormValues(),
  });

  const openCreateForm = () => {
    setEditing(null);
    setValidation(null);
    setDetailLoading(false);
    form.reset(toFormValues());
    setFormOpen(true);
  };

  const openEditForm = (script: AcquisitionScript) => {
    setEditing(script);
    setValidation(null);
    form.reset(toFormValues(script));
    setFormOpen(true);
    setDetailLoading(true);
    void getAcquisitionScript(script.id)
      .then((detail) => {
        setEditing(detail);
        form.reset(toFormValues(detail));
      })
      .catch((error) => {
        toast.error({
          title: "脚本详情加载失败",
          description: getErrorMessage(error, "将使用列表中的脚本内容"),
        });
      })
      .finally(() => setDetailLoading(false));
  };

  const saveDraft = async (values: ScriptFormValues) => {
    if (submitting || validating || publishing) return;
    setSubmitting(true);
    try {
      const payload = {
        name: values.name.trim(),
        description: values.description.trim(),
        draftSource: values.draftSource,
      };
      const saved = editing
        ? await updateAcquisitionScript(editing.id, payload)
        : await createAcquisitionScript(payload);
      setEditing(saved);
      form.reset(toFormValues(saved));
      setValidation(null);
      toast.success(editing ? "草稿已保存，不影响运行设备" : "脚本草稿已创建");
      list.reload();
    } catch (error) {
      toast.error({
        title: "保存草稿失败",
        description: getErrorMessage(error, "请检查脚本信息后重试"),
      });
    } finally {
      setSubmitting(false);
    }
  };

  const validateDraft = async () => {
    if (!editing) {
      toast.info("请先保存脚本草稿，再执行 Validate");
      return;
    }
    if (form.formState.isDirty) {
      toast.warning("请先保存草稿，再校验当前内容");
      return;
    }
    setValidating(true);
    try {
      const result = await validateAcquisitionScript(editing.id);
      setValidation(result);
      if (result.valid) {
        toast.success("Draft 校验通过，尚未发布");
      } else {
        toast.error("Draft 校验未通过，请根据行号和列号修正源码");
      }
    } catch (error) {
      toast.error({
        title: "Validate 失败",
        description: getErrorMessage(error, "无法完成脚本校验"),
      });
    } finally {
      setValidating(false);
    }
  };

  const publishDraft = async () => {
    if (!editing) {
      toast.info("请先保存脚本草稿，再发布版本");
      return;
    }
    if (form.formState.isDirty) {
      toast.warning("请先保存草稿，Publish 只会发布已保存的 Draft");
      return;
    }
    setPublishing(true);
    try {
      const version = await publishAcquisitionScript(editing.id);
      try {
        const detail = await getAcquisitionScript(editing.id);
        setEditing(detail);
        form.reset(toFormValues(detail));
      } catch {
        // Publish 已成功；详情刷新失败不掩盖发布结果。
      }
      setValidation({ valid: true, errors: [] });
      toast.success(`脚本已发布 v${version.versionNo}`);
      list.reload();
    } catch (error) {
      toast.error({
        title: "Publish 失败",
        description: getErrorMessage(error, "请先通过 Validate 后重试"),
      });
    } finally {
      setPublishing(false);
    }
  };

  const loadVersions = async (script: AcquisitionScript) => {
    setVersionsLoading(true);
    setVersionsError(null);
    try {
      setVersions(await getAcquisitionScriptVersions(script.id));
    } catch (error) {
      setVersions([]);
      setVersionsError(getErrorMessage(error, "无法获取版本历史"));
    } finally {
      setVersionsLoading(false);
    }
  };

  const openVersions = (script: AcquisitionScript) => {
    setVersionScript(script);
    setSelectedVersion(null);
    setVersionsOpen(true);
    void loadVersions(script);
  };

  const loadRuntimeStates = async () => {
    setRuntimeLoading(true);
    setRuntimeError(null);
    try {
      const states = await getAcquisitionScriptStates();
      setRuntimeStates(runtimeScript ? states.filter((state) => state.scriptId === runtimeScript.id) : []);
    } catch (error) {
      setRuntimeStates([]);
      setRuntimeError(getErrorMessage(error, "无法获取脚本运行状态"));
    } finally {
      setRuntimeLoading(false);
    }
  };

  const openRuntime = (script: AcquisitionScript) => {
    setRuntimeScript(script);
    setRuntimeOpen(true);
    setRuntimeStates([]);
    void getAcquisitionScriptStates()
      .then((states) => setRuntimeStates(states.filter((state) => state.scriptId === script.id)))
      .catch((error) => setRuntimeError(getErrorMessage(error, "无法获取脚本运行状态")))
      .finally(() => setRuntimeLoading(false));
    setRuntimeLoading(true);
    setRuntimeError(null);
  };

  const runConfirmAction = async () => {
    if (!confirmAction) return;
    setConfirmLoading(true);
    try {
      if (confirmAction.type === "delete") {
        await deleteAcquisitionScript(confirmAction.script.id);
        toast.success("协议脚本已删除");
        if (editing?.id === confirmAction.script.id) setFormOpen(false);
      } else {
        await rollbackAcquisitionScript(confirmAction.script.id, confirmAction.version.id);
        toast.success(`已回滚到 v${confirmAction.version.versionNo}`);
        if (versionScript?.id === confirmAction.script.id) {
          try {
            const [detail, nextVersions] = await Promise.all([
              getAcquisitionScript(confirmAction.script.id),
              getAcquisitionScriptVersions(confirmAction.script.id),
            ]);
            setVersionScript(detail);
            setVersions(nextVersions);
          } catch {
            // Rollback 已成功；版本弹窗下次打开时会重新读取。
          }
        }
      }
      setConfirmAction(null);
      list.reload();
    } catch (error) {
      toast.error({
        title: confirmAction.type === "delete" ? "删除失败" : "Rollback 失败",
        description: getErrorMessage(
          error,
          confirmAction.type === "delete"
            ? "已绑定设备的脚本不能删除"
            : "只能回滚到当前脚本的历史版本",
        ),
      });
    } finally {
      setConfirmLoading(false);
    }
  };

  const columns: DataTableColumn<AcquisitionScript>[] = [
    {
      title: "协议脚本",
      key: "script",
      width: 300,
      render: (_, script) => (
        <div className="min-w-0">
          <div className="font-medium text-text-primary">{script.name}</div>
          <div className="mt-1 truncate text-xs text-text-tertiary" title={script.description}>
            {script.description || "暂无说明"} · ID {script.id}
          </div>
        </div>
      ),
    },
    {
      title: "Published version",
      key: "publishedVersion",
      width: 190,
      render: (_, script) => script.publishedVersion ? (
        <div className="space-y-1">
          <StatusTag tone={script.draftMatchesPublished ? "success" : "warning"}>
            {getPublishedVersionLabel(script)}
          </StatusTag>
          {!script.draftMatchesPublished && <div className="text-xs text-warning">Draft 有未发布变更</div>}
        </div>
      ) : <StatusTag>未发布</StatusTag>,
    },
    {
      title: "更新时间",
      dataIndex: "updateTime",
      width: 180,
      nowrap: true,
      render: (value, script) => formatDateTime(String(value ?? script.createTime ?? "")),
    },
    {
      title: "绑定设备",
      dataIndex: "boundDeviceCount",
      width: 120,
      align: "right",
      render: (value) => `${value ?? 0} 台`,
    },
    {
      title: "操作",
      key: "actions",
      width: 360,
      nowrap: true,
      render: (_, script) => (
        <div className="flex flex-wrap gap-1">
          <Button size="sm" variant="ghost" onClick={() => openEditForm(script)}>
            <Pencil className="h-4 w-4" aria-hidden />
            编辑
          </Button>
          <Button size="sm" variant="ghost" onClick={() => openVersions(script)}>
            <History className="h-4 w-4" aria-hidden />
            版本
          </Button>
          <Button size="sm" variant="ghost" onClick={() => openRuntime(script)}>
            <Eye className="h-4 w-4" aria-hidden />
            运行状态
          </Button>
          <Button size="sm" variant="ghost" className="text-error hover:text-error" onClick={() => setConfirmAction({ type: "delete", script })}>
            <Trash2 className="h-4 w-4" aria-hidden />
            删除
          </Button>
        </div>
      ),
    },
  ];

  const busy = submitting || detailLoading || validating || publishing;

  return (
    <>
      <PageHeader
        title="协议脚本"
        description="维护可版本化的 Starlark 动态协议事务，并将已发布版本绑定到采集设备。"
        actions={
          <Button variant="primary" onClick={openCreateForm}>
            <Plus className="h-4 w-4" aria-hidden />
            新建协议脚本
          </Button>
        }
      />

      <div className="mb-space-4 grid gap-space-3 lg:grid-cols-2">
        <div className="flex gap-space-3 rounded-admin border border-info-border bg-info-background px-card py-space-3 text-sm text-info">
          <CheckCircle2 className="mt-0.5 h-4 w-4 shrink-0" aria-hidden />
          <div>
            <div className="font-medium">保存草稿不会影响运行设备</div>
            <div className="mt-1 text-xs text-text-secondary">只有 Publish 后的不可变版本，才会在设备的安全周期边界生效。</div>
          </div>
        </div>
        <div className="flex gap-space-3 rounded-admin border border-warning-border bg-warning-background px-card py-space-3 text-sm text-warning">
          <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" aria-hidden />
          <div>
            <div className="font-medium">已发布脚本可执行 FC16 / FC05，对真实设备产生写副作用</div>
            <div className="mt-1 text-xs text-text-secondary">请仅将经过验证的脚本绑定到目标设备；页面不提供专用合分闸或参数设置。</div>
          </div>
        </div>
      </div>

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
            placeholder="脚本名称"
            aria-label="筛选脚本名称"
          />
          <Select
            value={list.filters.published}
            onChange={(event) => list.setFilter("published", event.target.value as ScriptFilterState["published"])}
            aria-label="筛选发布状态"
          >
            <option value="">全部发布状态</option>
            <option value="1">已发布</option>
            <option value="0">未发布</option>
          </Select>
          <Select
            value={list.filters.bound}
            onChange={(event) => list.setFilter("bound", event.target.value as ScriptFilterState["bound"])}
            aria-label="筛选绑定状态"
          >
            <option value="">全部绑定状态</option>
            <option value="1">已绑定设备</option>
            <option value="0">未绑定设备</option>
          </Select>
        </form>
      </SearchFilterBar>

      <DataTableCard
        toolbar={
          <TableToolbar
            title="脚本列表"
            description={`共 ${list.total} 条数据，当前显示 ${list.data.length} 条。`}
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
          rowKey="id"
          loading={list.loading}
          error={list.error}
          minWidth={1160}
          empty={<EmptyState title="暂无协议脚本" description="创建一个脚本草稿，完成 Validate 后再发布。" actionText="新建协议脚本" onAction={openCreateForm} />}
        />
      </DataTableCard>

      <FormDialog
        open={formOpen}
        title={editing ? `编辑协议脚本 · ${editing.name}` : "新建协议脚本"}
        description={editing ? "编辑只更新 Draft；保存草稿不会影响当前运行设备。" : "先保存草稿，再 Validate 和 Publish。"}
        loading={submitting}
        submitDisabled={detailLoading || validating || publishing}
        submitText="保存草稿"
        loadingText="保存中..."
        closeOnEscape={!busy}
        closeOnOverlayClick={!busy}
        onCancel={() => setFormOpen(false)}
        onSubmit={() => void form.handleSubmit(saveDraft)()}
        contentClassName="max-w-[900px]"
        bodyClassName="max-h-[calc(100vh-150px)]"
      >
        <ScriptForm
          form={form}
          editing={editing}
          loading={busy}
          validating={validating}
          publishing={publishing}
          validation={validation}
          onValidate={() => void validateDraft()}
          onPublish={() => void publishDraft()}
        />
      </FormDialog>

      <DetailDialog
        open={versionsOpen}
        title={`${versionScript?.name ?? "协议脚本"} · 版本历史`}
        description="历史版本不可修改；Rollback 只切换当前脚本的 published pointer。"
        onCancel={() => setVersionsOpen(false)}
        contentClassName="max-w-[1040px]"
      >
        <div className="mb-space-4 flex items-center justify-between gap-space-3">
          <p className="text-sm text-text-secondary">新版本会在当前设备周期完成后，按安全边界切换。</p>
          <Button size="sm" variant="secondary" onClick={() => versionScript && void loadVersions(versionScript)} disabled={versionsLoading}>
            <RefreshCw className="h-4 w-4" aria-hidden />
            刷新
          </Button>
        </div>
        {versionsError ? (
          <EmptyState title="版本历史加载失败" description={versionsError} actionText="重新加载" onAction={() => versionScript && void loadVersions(versionScript)} />
        ) : (
          <DataTable
            columns={[
              {
                title: "版本",
                key: "version",
                width: 120,
                render: (_, version) => versionScript?.publishedVersionId === version.id
                  ? <StatusTag tone="success">{`v${version.versionNo} · 当前`}</StatusTag>
                  : <span className="font-medium text-text-primary">v{version.versionNo}</span>,
              },
              { title: "发布时间", dataIndex: "publishedAt", width: 180, nowrap: true, render: (value) => formatDateTime(String(value ?? "")) },
              { title: "Checksum", dataIndex: "checksum", width: 320, render: (value) => <code className="break-all text-xs text-text-secondary">{String(value ?? "-")}</code> },
              {
                title: "操作",
                key: "actions",
                width: 220,
                nowrap: true,
                render: (_, version) => (
                  <div className="flex gap-1">
                    <Button size="sm" variant="ghost" onClick={() => setSelectedVersion(version)}>
                      <Code2 className="h-4 w-4" aria-hidden />
                      查看源码
                    </Button>
                    <Button
                      size="sm"
                      variant="ghost"
                      disabled={versionScript?.publishedVersionId === version.id}
                      onClick={() => versionScript && setConfirmAction({ type: "rollback", script: versionScript, version })}
                    >
                      回滚
                    </Button>
                  </div>
                ),
              },
            ] as DataTableColumn<AcquisitionScriptVersion>[]}
            dataSource={versions}
            rowKey="id"
            loading={versionsLoading}
            minWidth={840}
            empty={<EmptyState title="暂无已发布版本" description="Validate 通过并 Publish 后，版本会显示在这里。" />}
          />
        )}
        {selectedVersion && (
          <div className="mt-space-4 rounded-admin border border-border bg-neutral-background p-space-4">
            <div className="mb-space-3 flex items-center justify-between gap-space-3">
              <div>
                <h3 className="text-sm font-semibold text-text-primary">v{selectedVersion.versionNo} 源码</h3>
                <p className="mt-1 text-xs text-text-tertiary">只读快照，Checksum：{selectedVersion.checksum}</p>
              </div>
              <Button size="icon" variant="ghost" aria-label="关闭版本源码" onClick={() => setSelectedVersion(null)}>×</Button>
            </div>
            <Textarea value={selectedVersion.source} readOnly rows={16} className="min-h-[360px] resize-y bg-surface font-mono text-xs leading-6" aria-label={`v${selectedVersion.versionNo} 源码`} />
          </div>
        )}
      </DetailDialog>

      <DetailDialog
        open={runtimeOpen}
        title={`${runtimeScript?.name ?? "协议脚本"} · 运行状态`}
        description="脚本运行状态独立于设备 ONLINE / OFFLINE 通信状态，仅用于观察动态事务执行。"
        onCancel={() => setRuntimeOpen(false)}
        contentClassName="max-w-[1180px]"
      >
        <div className="mb-space-4 flex items-center justify-between gap-space-3">
          <p className="text-sm text-text-secondary">展示绑定该脚本的设备、版本、最近执行结果和 bounded events。</p>
          <Button size="sm" variant="secondary" onClick={() => void loadRuntimeStates()} disabled={runtimeLoading}>
            <RefreshCw className="h-4 w-4" aria-hidden />
            刷新
          </Button>
        </div>
        {runtimeError ? (
          <EmptyState title="运行状态加载失败" description={runtimeError} actionText="重新加载" onAction={() => void loadRuntimeStates()} />
        ) : (
          <DataTable
            columns={[
              {
                title: "设备",
                key: "device",
                width: 190,
                render: (_, state) => <div><div className="font-medium text-text-primary">{state.deviceName || `设备 #${state.deviceId}`}</div><div className="text-xs text-text-tertiary">ID {state.deviceId}</div></div>,
              },
              { title: "执行版本", key: "version", width: 120, render: (_, state) => `v${state.versionNo} · #${state.scriptVersionId}` },
              { title: "最近尝试", dataIndex: "lastAttemptAt", width: 180, nowrap: true, render: (value) => formatDateTime(String(value ?? "")) },
              {
                title: "结果",
                key: "result",
                width: 150,
                render: (_, state) => {
                  const status = getRuntimeStatus(state);
                  return <div className="space-y-1"><StatusTag tone={status.tone}>{status.label}</StatusTag>{state.lastSuccessAt && <div className="text-xs text-text-tertiary">成功：{formatDateTime(state.lastSuccessAt)}</div>}</div>;
                },
              },
              {
                title: "最近错误",
                key: "error",
                width: 240,
                render: (_, state) => state.lastError ? <div className="text-sm text-error"><div>{state.lastErrorType ? errorTypeLabels[state.lastErrorType] : "脚本错误"}</div><div className="mt-1 break-words text-xs">{state.lastError}</div></div> : <span className="text-text-tertiary">-</span>,
              },
              {
                title: "最近事件",
                key: "events",
                width: 360,
                render: (_, state) => state.events.length ? <div className="max-h-28 space-y-2 overflow-y-auto text-xs"><div className="font-medium text-text-secondary">共 {state.events.length} 条</div>{state.events.slice(-3).reverse().map((event) => <div key={`${event.kind}-${event.key}-${event.occurredAt}`} className="rounded-control bg-neutral-background px-space-2 py-space-1.5"><div className="font-medium text-text-primary">{event.kind} / {event.key}</div><code className="mt-1 block break-all text-text-tertiary">{formatEventPayload(event.payload)}</code><div className="mt-1 text-text-tertiary">{formatDateTime(event.occurredAt)}</div></div>)}</div> : <span className="text-text-tertiary">暂无事件</span>,
              },
            ] as DataTableColumn<AcquisitionScriptRuntimeState>[]}
            dataSource={runtimeStates}
            rowKey={(state) => state.deviceId}
            loading={runtimeLoading}
            minWidth={1240}
            empty={<EmptyState title="暂无运行状态" description="当前没有设备产生该脚本的运行观察记录，或脚本尚未绑定设备。" />}
          />
        )}
      </DetailDialog>

      <ConfirmDialog
        open={Boolean(confirmAction)}
        title={confirmAction?.type === "delete" ? "删除协议脚本" : "回滚协议脚本"}
        description={confirmAction?.type === "delete"
          ? `确认删除「${confirmAction.script.name}」吗？已绑定任何设备的脚本会被后端拒绝删除。`
          : `确认将「${confirmAction?.script.name ?? ""}」切换到 v${confirmAction?.type === "rollback" ? confirmAction.version.versionNo : ""} 吗？当前运行周期不会被中断。`}
        danger={confirmAction?.type === "delete"}
        confirmText={confirmAction?.type === "delete" ? "删除" : "回滚"}
        loading={confirmLoading}
        onCancel={() => setConfirmAction(null)}
        onConfirm={() => void runConfirmAction()}
      />
    </>
  );
}

function ScriptForm({
  form,
  editing,
  loading,
  validating,
  publishing,
  validation,
  onValidate,
  onPublish,
}: {
  form: UseFormReturn<ScriptFormValues>;
  editing: AcquisitionScript | null;
  loading: boolean;
  validating: boolean;
  publishing: boolean;
  validation: AcquisitionScriptValidationResult | null;
  onValidate: () => void;
  onPublish: () => void;
}) {
  const {
    register,
    formState: { errors },
  } = form;

  return (
    <div className="space-y-space-4">
      <div className="rounded-admin border border-border bg-neutral-background px-space-4 py-space-3 text-sm text-text-secondary">
        <div className="flex items-start gap-space-2">
          <Code2 className="mt-0.5 h-4 w-4 shrink-0 text-primary" aria-hidden />
          <div>
            <div className="font-medium text-text-primary">Draft → Validate → Publish</div>
            <div className="mt-1">Validate 只校验源码，不访问真实设备、不产生 Modbus I/O；保存 Draft 不会替换设备正在运行的版本。</div>
          </div>
        </div>
      </div>
      <div className="grid gap-4 md:grid-cols-2">
        <Field label="脚本名称" htmlFor="script-name" required error={errors.name?.message}>
          <Input id="script-name" placeholder="例如：ZNCK-I 故障详情查询" disabled={loading} {...register("name")} />
        </Field>
        <Field label="说明" htmlFor="script-description" error={errors.description?.message}>
          <Input id="script-description" placeholder="描述脚本用途和绑定范围" disabled={loading} {...register("description")} />
        </Field>
      </div>
      <Field label="Draft 源码" htmlFor="script-source" required error={errors.draftSource?.message} help="首版入口必须定义 after_poll(ctx)；源码会按完整 UTF-8 内容计算 published version checksum。">
        <Textarea id="script-source" rows={18} spellCheck={false} disabled={loading} className="min-h-[360px] resize-y bg-neutral-background font-mono text-xs leading-6" placeholder={DEFAULT_DRAFT_SOURCE} {...register("draftSource")} />
      </Field>
      <div className="flex flex-wrap items-center gap-space-2 border-t border-border pt-space-4">
        <Button variant="secondary" onClick={onValidate} disabled={!editing || loading || validating || publishing}>
          <CheckCircle2 className="h-4 w-4" aria-hidden />
          {validating ? "Validate 中..." : "Validate Draft"}
        </Button>
        <Button variant="primary" onClick={onPublish} disabled={!editing || loading || validating || publishing}>
          <Upload className="h-4 w-4" aria-hidden />
          {publishing ? "Publish 中..." : "Publish"}
        </Button>
        {!editing && <span className="text-xs text-text-tertiary">保存后才能校验或发布。</span>}
      </div>
      {validation && <ValidationResult result={validation} />}
    </div>
  );
}

function ValidationResult({ result }: { result: AcquisitionScriptValidationResult }) {
  if (result.valid) {
    return (
      <div className="flex items-start gap-space-2 rounded-admin border border-success-border bg-success-background px-space-4 py-space-3 text-sm text-success" role="status">
        <CheckCircle2 className="mt-0.5 h-4 w-4 shrink-0" aria-hidden />
        <div><div className="font-medium">Draft 校验通过</div><div className="mt-1 text-xs text-text-secondary">校验成功不会自动发布，运行设备仍继续使用当前 published version。</div></div>
      </div>
    );
  }

  return (
    <div className="rounded-admin border border-error-border bg-error-background px-space-4 py-space-3 text-sm text-error" role="alert">
      <div className="flex items-start gap-space-2">
        <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" aria-hidden />
        <div className="min-w-0">
          <div className="font-medium">Draft 校验未通过</div>
          <ul className="mt-space-2 space-y-1 text-xs text-text-secondary">
            {result.errors.length === 0 && <li>服务端未返回具体错误，请检查源码后重试。</li>}
            {result.errors.map((issue, index) => (
              <li key={`${issue.line}-${issue.column}-${index}`}>
                <span className="font-medium text-error">第 {issue.line} 行，第 {issue.column} 列</span>：{issue.message}
              </li>
            ))}
          </ul>
        </div>
      </div>
    </div>
  );
}
