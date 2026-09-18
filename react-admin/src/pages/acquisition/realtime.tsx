import { Pause, Play, RefreshCw } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { PageHeader } from "@/components/common/page-header";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import type { AcquisitionCurrentState } from "@/types";
import {
  ChannelDeviceNavigator,
  StateDetail,
  SummaryStrip,
} from "./realtime-components";
import { groupRealtimeStates, type StatusFilter } from "./realtime-utils";
import { useRealtimeAcquisition } from "./use-realtime-acquisition";

export { StateDetail } from "./realtime-components";

export function AcquisitionRealtimePage() {
  const [paused, setPaused] = useState(false);
  const [selectedID, setSelectedID] = useState<number | null>(null);
  const [displayMode, setDisplayMode] = useState<"hex" | "dec" | "bin">("hex");
  const [blockFilter, setBlockFilter] = useState<number | "all">("all");
  const [search, setSearch] = useState("");
  const [statusFilter, setStatusFilter] = useState<StatusFilter>("ALL");
  const [expandedChannels, setExpandedChannels] = useState<Set<number>>(new Set());
  const [expandedInitialized, setExpandedInitialized] = useState(false);
  const {
    states,
    channelStates,
    loading,
    error,
    channelStateError,
    loadStates,
  } = useRealtimeAcquisition(paused);

  const channelGroups = useMemo(
    () => groupRealtimeStates(states, channelStates),
    [channelStates, states],
  );

  useEffect(() => {
    if (expandedInitialized || channelGroups.length === 0 || states.length === 0) return;
    setExpandedChannels(new Set(channelGroups.map((group) => group.channelId)));
    setExpandedInitialized(true);
  }, [channelGroups, expandedInitialized, states.length]);

  const visibleGroups = useMemo(() => {
    const normalizedQuery = search.trim().toLocaleLowerCase("zh-CN");

    return channelGroups
      .map((group) => {
        const channelMatches =
          normalizedQuery.length === 0 ||
          [group.channelName, group.protocol, group.channelId]
            .filter(Boolean)
            .join(" ")
            .toLocaleLowerCase("zh-CN")
            .includes(normalizedQuery);
        const filteredStates = group.states.filter((state) => {
          if (!matchesStatusFilter(state, statusFilter)) return false;
          if (channelMatches) return true;
          return [
            state.deviceName,
            state.channelId,
            state.unitId,
            state.networkEndpoint?.host,
            state.networkEndpoint?.port,
          ]
            .filter(Boolean)
            .join(" ")
            .toLocaleLowerCase("zh-CN")
            .includes(normalizedQuery);
        });
        return { ...group, states: filteredStates };
      })
      .filter(
        (group) =>
          group.states.length > 0 ||
          (Boolean(group.runtimeState) &&
            normalizedQuery.length > 0 &&
            group.channelName.toLocaleLowerCase("zh-CN").includes(normalizedQuery)),
      );
  }, [channelGroups, search, statusFilter]);

  const visibleStates = useMemo(
    () => visibleGroups.flatMap((group) => group.states),
    [visibleGroups],
  );
  const selected = useMemo(
    () => states.find((state) => state.deviceId === selectedID) ?? null,
    [selectedID, states],
  );

  useEffect(() => {
    setSelectedID((current) => {
      if (current !== null && visibleStates.some((state) => state.deviceId === current)) {
        return current;
      }
      return visibleStates[0]?.deviceId ?? null;
    });
  }, [visibleStates]);

  useEffect(() => {
    if (
      blockFilter !== "all" &&
      !selected?.registerBlocks.some((block) => block.id === blockFilter)
    ) {
      setBlockFilter("all");
    }
  }, [blockFilter, selected]);

  return (
    <div data-testid="realtime-page" className="min-w-0">
      <PageHeader
        title="实时寄存器"
        description="持续观察启用设备的原始寄存器；不进行电压、电流等业务协议解析。"
        actions={
          <div className="flex flex-wrap items-center justify-end gap-space-2">
            <div
              className={cn(
                "flex h-control-sm items-center gap-1.5 rounded-control px-space-2 text-xs",
                paused
                  ? "bg-warning-background text-warning"
                  : "bg-neutral-background text-text-tertiary",
              )}
            >
              <span
                className={cn(
                  "h-2 w-2 rounded-full",
                  paused ? "bg-warning" : "animate-pulse bg-success",
                )}
                aria-hidden
              />
              {paused ? "自动刷新已暂停" : "自动刷新 · 1.5 秒"}
            </div>
            <Button variant="secondary" onClick={() => setPaused((current) => !current)}>
              {paused ? <Play className="h-4 w-4" aria-hidden /> : <Pause className="h-4 w-4" aria-hidden />}
              {paused ? "继续刷新" : "暂停刷新"}
            </Button>
            <Button variant="secondary" onClick={() => void loadStates()} disabled={loading}>
              <RefreshCw className="h-4 w-4" aria-hidden />
              立即刷新
            </Button>
          </div>
        }
      />

      <SummaryStrip states={states} />

      <div className="grid min-w-0 gap-space-4 lg:grid-cols-[minmax(280px,3fr)_minmax(0,7fr)] lg:items-start">
        <ChannelDeviceNavigator
          groups={visibleGroups}
          totalDeviceCount={states.length}
          selectedID={selectedID}
          loading={loading}
          error={error}
          channelStateError={channelStateError}
          search={search}
          statusFilter={statusFilter}
          expandedChannels={expandedChannels}
          onSearchChange={setSearch}
          onStatusFilterChange={setStatusFilter}
          onSelect={setSelectedID}
          onToggleChannel={(channelID) => {
            setExpandedChannels((current) => {
              const next = new Set(current);
              if (next.has(channelID)) next.delete(channelID);
              else next.add(channelID);
              return next;
            });
          }}
          onRetry={() => void loadStates()}
        />
        <StateDetail
          state={selected}
          displayMode={displayMode}
          onDisplayModeChange={setDisplayMode}
          blockFilter={blockFilter}
          onBlockFilterChange={setBlockFilter}
        />
      </div>
    </div>
  );
}

function matchesStatusFilter(state: AcquisitionCurrentState, filter: StatusFilter) {
  if (filter === "ALL") return true;
  if (filter === "UNCONFIGURED") return state.registerBlocks.length === 0;
  return state.status === filter;
}
