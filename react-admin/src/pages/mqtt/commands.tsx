import { useCallback, useEffect, useMemo, useState } from "react";
import { Eye, RefreshCw, RotateCcw, Search } from "lucide-react";
import { useSearchParams } from "react-router-dom";
import { getMqttCommand, getMqttCommands } from "@/api/mqtt";
import { PermissionGuard } from "@/components/auth/permission-guard";
import { DataTable } from "@/components/common/data-table";
import { DataTableCard } from "@/components/common/data-table-card";
import { EmptyState } from "@/components/common/empty-state";
import { Pagination } from "@/components/common/pagination";
import { SearchFilterBar } from "@/components/common/search-filter-bar";
import { StatusTag } from "@/components/common/status-tag";
import { TableToolbar } from "@/components/common/table-toolbar";
import { toast } from "@/components/common/toast-store";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { useListPage } from "@/hooks/use-list-page";
import { hasPermission } from "@/lib/permission";
import { getMqttErrorMessage } from "@/lib/mqtt";
import type { DataTableColumn, MqttCommandJournal, MqttCommandListQuery, MqttCommandStatus } from "@/types";
import {
  buildCommandQuery,
  CommandDetailDialog,
  commandStatuses,
  type CommandFilterState,
  DEFAULT_COMMAND_FILTERS,
  formatOptionalTime,
  getCommandStatusMeta,
  MqttPageLayout,
} from "./shared";

function getInitialFilters(searchParams: URLSearchParams): CommandFilterState {
  const status = searchParams.get("status");
  const validStatus: CommandFilterState["status"] = commandStatuses.some((item) => item.value === status) ? status as MqttCommandStatus : "all";
  return {
    ...DEFAULT_COMMAND_FILTERS,
    status: validStatus,
  };
}

export function MqttCommandsPage() {
  return (
    <PermissionGuard
      permissionCode="mqtt:command:list"
      fallback={<EmptyState title="无权查看 Command Journal" description="当前账号没有 Command Journal 查看权限。" />}
    >
      <MqttCommandsContent />
    </PermissionGuard>
  );
}

function MqttCommandsContent() {
  const [searchParams] = useSearchParams();
  const [detail, setDetail] = useState<MqttCommandJournal | null>(null);
  const [detailOpen, setDetailOpen] = useState(false);
  const [detailLoading, setDetailLoading] = useState(false);
  const defaultFilters = useMemo(() => getInitialFilters(searchParams), [searchParams]);
  const list = useListPage<
    typeof DEFAULT_COMMAND_FILTERS,
    MqttCommandJournal,
    MqttCommandListQuery
  >({
    fetch: getMqttCommands,
    defaultFilters,
    toQuery: buildCommandQuery,
    defaultPageSize: 20,
    onError: (error) => toast.error({ title: "Command Journal 加载失败", description: getMqttErrorMessage(error, "无法获取 Command Journal") }),
  });

  const openDetail = useCallback(async (record: MqttCommandJournal) => {
    setDetail(record);
    setDetailOpen(true);
    setDetailLoading(true);
    try {
      setDetail(await getMqttCommand(record.commandId));
    } catch (error) {
      toast.error({ title: "Command 详情加载失败", description: getMqttErrorMessage(error, "无法获取 Command 详情") });
    } finally {
      setDetailLoading(false);
    }
  }, []);

  const { data: commandData, reload: reloadCommands } = list;

  useEffect(() => {
    const hasRunningCommands = commandData.some((item) => item.status === "ACCEPTED" || item.status === "RUNNING");
    if (!hasRunningCommands) return;
    const interval = window.setInterval(() => {
      if (document.visibilityState === "visible") reloadCommands();
    }, 5000);
    return () => window.clearInterval(interval);
  }, [commandData, reloadCommands]);

  const columns = useMemo<DataTableColumn<MqttCommandJournal>[]>(() => [
    { title: "Command ID", dataIndex: "commandId", width: 250, render: (value) => <code className="break-all text-xs text-text-secondary">{String(value ?? "-")}</code> },
    { title: "设备", dataIndex: "deviceId", width: 180, render: (value) => <span className="font-medium text-text-primary">{String(value ?? "-")}</span> },
    { title: "命令", dataIndex: "name", width: 140 },
    { title: "状态", dataIndex: "status", width: 120, render: (value) => { const meta = getCommandStatusMeta(String(value ?? "")); return <StatusTag tone={meta.tone}>{meta.label}</StatusTag>; } },
    { title: "接收时间", dataIndex: "receivedAt", width: 180, nowrap: true, render: (value) => formatOptionalTime(String(value ?? "")) },
    { title: "开始时间", dataIndex: "startedAt", width: 180, nowrap: true, render: (value) => formatOptionalTime(value as string | null | undefined) },
    { title: "完成时间", dataIndex: "completedAt", width: 180, nowrap: true, render: (value) => formatOptionalTime(value as string | null | undefined) },
    { title: "操作", key: "actions", width: 100, nowrap: true, render: (_, record) => <PermissionGuard permissionCode="mqtt:command:detail"><Button size="sm" variant="ghost" disabled={detailLoading && detail?.commandId === record.commandId} onClick={() => void openDetail(record)}><Eye className="h-4 w-4" aria-hidden />详情</Button></PermissionGuard> },
  ], [detail?.commandId, detailLoading, openDetail]);

  const detailError = detail?.errorMessage ? getMqttErrorMessage(new Error(detail.errorMessage), "错误信息不可展示") : "";

  return (
    <MqttPageLayout title="Command Journal" description="查询持久化的 Command 状态事实源；详情只展示安全元数据。">
      <form onSubmit={(event) => { event.preventDefault(); list.submitFilters(); }}>
        <SearchFilterBar actions={<><Button type="submit" variant="primary"><Search className="h-4 w-4" aria-hidden />查询</Button><Button type="button" variant="secondary" onClick={list.resetFilters}><RotateCcw className="h-4 w-4" aria-hidden />重置</Button></>}>
          <Input value={list.filters.commandId} onChange={(event) => list.setFilter("commandId", event.target.value)} placeholder="按 Command ID 查询" aria-label="按 Command ID 查询" />
          <Input value={list.filters.name} onChange={(event) => list.setFilter("name", event.target.value)} placeholder="按命令名称查询" aria-label="按命令名称查询" />
          <Input value={list.filters.deviceId} onChange={(event) => list.setFilter("deviceId", event.target.value)} placeholder="按 deviceId 筛选" aria-label="按 deviceId 筛选" />
          <Select value={list.filters.status} onChange={(event) => list.setFilter("status", event.target.value as typeof list.filters.status)} aria-label="按 Command 状态筛选"><option value="all">全部状态</option>{commandStatuses.map((status) => <option key={status.value} value={status.value}>{status.label}</option>)}</Select>
        </SearchFilterBar>
      </form>
      <DataTableCard toolbar={<TableToolbar title="Command Journal" description="页面不展示 command payload、ciphertext 或 result 原文。" actions={<><StatusTag tone={list.loading ? "warning" : list.error ? "error" : "info"}>{list.loading ? "加载中" : list.error ? "加载失败" : "已同步"}</StatusTag><Button size="sm" variant="secondary" onClick={list.reload} disabled={list.loading}><RefreshCw className="h-4 w-4" aria-hidden />刷新</Button></>} />} pagination={<Pagination page={list.page} pageSize={list.pageSize} total={list.total} pageSizeOptions={[10, 20, 50, 100]} disabled={list.loading} onPageChange={list.setPage} onPageSizeChange={list.setPageSize} />}>
        <DataTable columns={columns} dataSource={list.data} rowKey="commandId" loading={list.loading} error={list.error ? "Command Journal 加载失败，请重试。" : undefined} minWidth={1180} onRowClick={hasPermission("mqtt:command:detail") ? (record) => void openDetail(record) : undefined} empty={<EmptyState title="暂无 Command Journal 记录" description="收到 MQTT command 后，持久化状态会显示在这里。" />} />
      </DataTableCard>
      <PermissionGuard permissionCode="mqtt:command:detail"><CommandDetailDialog open={detailOpen} detail={detail} loading={detailLoading} errorMessage={detailError} onCancel={() => setDetailOpen(false)} /></PermissionGuard>
    </MqttPageLayout>
  );
}
