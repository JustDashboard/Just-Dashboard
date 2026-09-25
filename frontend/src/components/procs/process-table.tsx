"use client"

import { useEffect, useState } from "react"
import { Cpu, SettingsSliders } from "@/components/icons"
import { useMetrics } from "@/hooks/use-metrics"
import { usePoll } from "@/hooks/use-poll"
import { useQuerySelection } from "@/hooks/use-query-selection"
import { useConfirm } from "@/components/confirm-dialog"
import { Metric, MetricStrip, Page, PageContext, RowLink, SearchInput } from "@/components/page"
import { Panel, PanelBody, PanelFooter, PanelHeader, PanelToolbar } from "@/components/panel"
import { ROW_BLEED } from "@/components/row-list"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { ChipCount, FilterChip } from "@/components/tabs"
import { EmptyState, ErrorState, LoadingPanel } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { VerbActions } from "@/components/verbs"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import {
  stickyTableHeader,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { get } from "@/lib/api"
import { bytes, duration, percent, plural, relativeTime } from "@/lib/format"
import type { ProcessList, ProcessRow, Snapshot } from "@/lib/types"
import { cn } from "@/lib/utils"
import { useSessionState, useViewState } from "@/lib/view-state"
import { ProcessDetailSheet } from "@/components/procs/process-detail"
import {
  useProcessControl,
  useProcessVerbs,
  type ConfirmFn,
  type PendingMap,
} from "@/components/procs/process-actions"
import { cpuTone, managerName, processKey, processStateTone } from "@/components/procs/shared"

type ProcessSort = "auto" | "cpu" | "memory" | "io" | "uptime"

const SORT_LABEL: Record<Exclude<ProcessSort, "auto">, string> = {
  cpu: "highest CPU",
  memory: "highest memory",
  io: "highest disk I/O",
  uptime: "longest-running",
}

function automaticFocus(snapshot: Snapshot | undefined): {
  sort: Exclude<ProcessSort, "auto">
  reason: string
} {
  // Every field is optional here: a snapshot from an older backend lacks the
  // run queue and the pressure block, and the page must still open.
  if (!snapshot?.cpu || !snapshot.memory) {
    return { sort: "cpu", reason: "CPU until host metrics arrive" }
  }
  const pressure = snapshot.pressure
  if (
    (snapshot.procs?.blocked ?? 0) > 0 ||
    (snapshot.cpu.modes?.iowait ?? 0) >= 10 ||
    (pressure?.supported && pressure.ioSome >= 10)
  ) {
    return { sort: "io", reason: "disk wait is holding work up" }
  }
  const availablePercent =
    snapshot.memory.total > 0 ? (snapshot.memory.available / snapshot.memory.total) * 100 : 100
  if (availablePercent <= 10 || (pressure?.supported && pressure.memSome >= 5)) {
    return { sort: "memory", reason: "available memory is tight" }
  }
  return { sort: "cpu", reason: "the host is not memory- or I/O-bound" }
}

/**
 * Everything running on the host, and which of it is heavy.
 *
 * Readings first, then the table. The four tiles are the answer to "is the
 * process table itself telling me something" — a zombie count and a blocked
 * count are what turns a long list into a diagnosis — and they are computed
 * over the whole snapshot, so a filter narrowing the rows never narrows the
 * figures. The table's own question is "which rows", and the focus control
 * answers it by what the host is short of rather than by a fixed column.
 */
export function LiveProcesses() {
  const { confirm, dialog } = useConfirm()
  const [query, setQuery] = useSessionState("processes.live.query", "")
  const [user, setUser] = useSessionState("processes.live.user", "")
  const [state, setState] = useSessionState("processes.live.state", "")
  const [manager, setManager] = useSessionState("processes.live.manager", "")
  const [selectedPid, selectPid] = useQuerySelection("pid")
  const [sort, setSort] = useViewState<ProcessSort>("processes.table.sort.v2", "auto")
  const [limit, setLimit] = useViewState("processes.table.limit", 200)
  const [refreshSeconds, setRefreshSeconds] = useViewState("processes.table.refresh", 4)
  const appliedQuery = useDebounced(query, 250)
  const { snapshot } = useMetrics()
  const automatic = automaticFocus(snapshot)
  const effectiveSort = sort === "auto" ? automatic.sort : sort
  const processList = usePoll(
    (signal) =>
      get<ProcessList>(
        "/processes/inventory",
        {
          limit,
          q: appliedQuery,
          sort: effectiveSort,
          user: user || undefined,
          state: state || undefined,
          manager: manager || undefined,
        },
        signal,
      ),
    refreshSeconds * 1000,
    [appliedQuery, effectiveSort, user, state, manager, limit],
  )
  const { pending, signal } = useProcessControl(processList.refresh)

  const data = processList.data
  const memTotal = snapshot?.memory?.total ?? 0
  const facet = (list: ProcessList["states"] | undefined, value: string) =>
    list?.find((f) => f.value === value)?.count ?? 0
  const running = facet(data?.states, "running")
  const blocked = facet(data?.states, "blocked")
  const zombies = facet(data?.states, "zombie")
  const unmanaged = facet(data?.managers, "unmanaged")
  const managed = (data?.managers ?? [])
    .filter((f) => f.value !== "unmanaged" && f.value !== "kernel")
    .map((f) => `${f.count} ${f.label.toLowerCase()}`)
    .join(" · ")
  const pid = selectedPid ? Number(selectedPid) : null

  return (
    <Page className="animate-rise">
      <PageContext eyebrow="Processes" title="Live" />

      {snapshot?.cpu && snapshot.memory && (
        <MetricStrip>
          <Metric label="CPU" value={percent(snapshot.cpu.totalPercent, 0)} />
          <Metric label="Available" value={bytes(snapshot.memory.available)} />
          <Metric label="Load" value={snapshot.cpu.loadAvg1.toFixed(2)} />
          <Metric label="Uptime" value={duration(snapshot.uptimeSeconds)} />
        </MetricStrip>
      )}

      {data && (
        <StatGrid columns={4} key="figures" className="animate-rise">
          <StatTile
            label="Processes"
            value={data.available}
            hint={`${running} running · ${facet(data.states, "sleeping")} sleeping`}
            trailing={<span className="text-hint text-muted-foreground">on this host</span>}
          />
          <StatTile
            label="Blocked"
            value={blocked}
            tone={blocked > 0 ? "warning" : "default"}
            hint={blocked > 0 ? "waiting on a disk or a lock" : "nothing waiting on a disk"}
          />
          <StatTile
            label="Zombies"
            value={zombies}
            tone={zombies > 0 ? "danger" : "default"}
            hint={zombies > 0 ? "exited, but the parent has not reaped them" : "none to reap"}
          />
          <StatTile
            label="Unmanaged"
            value={unmanaged}
            hint={managed ? `supervised: ${managed}` : "nothing is supervised"}
            trailing={
              <span className="text-hint text-muted-foreground">would not survive a reboot</span>
            }
          />
        </StatGrid>
      )}

      {/* Framed, because it is a table: the grid owns a scroll region and the
          edge is what says so (§2). The figures above it stay plain. */}
      <Panel>
        <PanelHeader
          title="Process table"
          actions={
            <ProcessTableSettings
              limit={limit}
              setLimit={setLimit}
              refreshSeconds={refreshSeconds}
              setRefreshSeconds={setRefreshSeconds}
            />
          }
        />
        <PanelToolbar>
          <SearchInput
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder="Name, command, PID, user or owner"
            containerClassName="sm:w-72"
          />
          <div className="flex min-w-0 flex-wrap items-center gap-1">
            <FilterChip selected={manager === ""} onClick={() => setManager("")}>
              All <ChipCount>{data?.available ?? 0}</ChipCount>
            </FilterChip>
            {data?.managers.map((option) => (
              <FilterChip
                key={option.value}
                selected={manager === option.value}
                onClick={() => setManager(manager === option.value ? "" : option.value)}
              >
                {option.label} <ChipCount>{option.count}</ChipCount>
              </FilterChip>
            ))}
          </div>
          <div className="ml-auto flex flex-wrap items-center gap-2">
            <FacetSelect
              label="All users"
              value={user}
              onChange={setUser}
              options={data?.users ?? []}
            />
            <FacetSelect
              label="All states"
              value={state}
              onChange={setState}
              options={data?.states ?? []}
            />
            <Select value={sort} onValueChange={(value) => setSort(value as ProcessSort)}>
              <SelectTrigger size="sm" className="w-44" aria-label="Process focus">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="auto">Automatic focus</SelectItem>
                <SelectItem value="cpu">Highest CPU</SelectItem>
                <SelectItem value="memory">Highest memory</SelectItem>
                <SelectItem value="io">Highest disk I/O</SelectItem>
                <SelectItem value="uptime">Longest-running</SelectItem>
              </SelectContent>
            </Select>
          </div>
        </PanelToolbar>
        <PanelBody flush>
          {processList.loading && !data && <LoadingPanel />}
          {processList.error && !data && <ErrorState error={processList.error} />}
          {data && (
            <>
              {/* The outer columns take the gutter from their own cell padding,
                  so the first column starts in the title's column; the `-mx`
                  bleed that does the same on a plain panel is gated to it (§2). */}
              <div className="hidden min-w-0 group-data-[plain]/panel:-mx-4 lg:block">
                <ProcessTableWide
                  rows={data.processes}
                  ratesReady={data.ratesReady}
                  memTotal={memTotal}
                  pending={pending}
                  confirm={confirm}
                  signal={signal}
                  onOpen={(p) => selectPid(String(p.pid))}
                />
              </div>
              {/* Below `lg` the same rows are drawn down the row instead of
                  across it: a ten-column table keeps three on a phone, and
                  what is left is the remains of a table. Nothing is dropped —
                  the PID, the owner, both readings and the state are all
                  part of "is the thing I just deployed alive". */}
              <div className="lg:hidden">
                <ProcessTableNarrow
                  rows={data.processes}
                  memTotal={memTotal}
                  pending={pending}
                  confirm={confirm}
                  signal={signal}
                  onOpen={(p) => selectPid(String(p.pid))}
                />
              </div>
              {data.processes.length === 0 && (
                <EmptyState
                  icon={Cpu}
                  title="No processes match"
                  description="Clear a filter or search for a different command, PID, user or owner."
                  className="mt-4"
                />
              )}
            </>
          )}
        </PanelBody>
        {data && (
          <PanelFooter className="text-hint text-muted-foreground">
            <span className="numeric">
              {data.truncated
                ? `Showing the ${data.processes.length} heaviest of ${data.total} matching`
                : `${plural(data.total, "process", "processes")} matching`}
            </span>
            <span className="text-muted-foreground/40">·</span>
            <span>
              sorted by {SORT_LABEL[effectiveSort]}
              {sort === "auto" && ` because ${automatic.reason}`}
            </span>
            {!data.ratesReady && (
              <>
                <span className="text-muted-foreground/40">·</span>
                <span>disk rates arrive with the second sample</span>
              </>
            )}
          </PanelFooter>
        )}
      </Panel>

      <ProcessDetailSheet
        pid={pid}
        memTotal={memTotal}
        onOpenChange={(open) => !open && selectPid(null)}
        onSelect={(next) => selectPid(String(next))}
        onChanged={processList.refresh}
      />
      {dialog}
    </Page>
  )
}

type RowsProps = {
  rows: ProcessRow[]
  memTotal: number
  pending: PendingMap
  confirm: ConfirmFn
  signal: Parameters<typeof useProcessVerbs>[0]["signal"]
  onOpen: (process: ProcessRow) => void
}

function ProcessTableWide({
  rows,
  ratesReady,
  memTotal,
  ...rest
}: RowsProps & { ratesReady: boolean }) {
  return (
    <Table containerClassName="max-h-[calc(100svh-22rem)]">
      <TableHeader className={stickyTableHeader}>
        <TableRow>
          <TableHead className="w-20">PID</TableHead>
          <TableHead className="w-full">Process</TableHead>
          <TableHead>Owner</TableHead>
          <TableHead className="hidden xl:table-cell">User</TableHead>
          <TableHead>State</TableHead>
          <TableHead className="text-right">CPU</TableHead>
          <TableHead className="text-right">Memory</TableHead>
          <TableHead className="hidden text-right xl:table-cell">Disk I/O</TableHead>
          <TableHead className="hidden 2xl:table-cell">Started</TableHead>
          <TableHead className="w-px" />
        </TableRow>
      </TableHeader>
      <TableBody>
        {rows.map((process) => (
          <ProcessTableRow
            key={processKey(process)}
            process={process}
            ratesReady={ratesReady}
            memTotal={memTotal}
            {...rest}
          />
        ))}
      </TableBody>
    </Table>
  )
}

function ProcessTableRow({
  process,
  ratesReady,
  memTotal,
  pending,
  confirm,
  signal,
  onOpen,
}: Omit<RowsProps, "rows"> & { process: ProcessRow; ratesReady: boolean }) {
  const verbs = useProcessVerbs({ process, confirm, signal, onInspect: () => onOpen(process) })
  const busy = pending[processKey(process)]
  const io = (process.ioReadRate ?? 0) + (process.ioWriteRate ?? 0)
  return (
    <TableRow className="group" onActivate={() => onOpen(process)}>
      <TableCell className="numeric font-mono text-muted-foreground">{process.pid}</TableCell>
      <TableCell>
        <div className="max-w-[28rem] min-w-0">
          <RowLink title={process.name} onClick={() => onOpen(process)}>
            {process.name}
          </RowLink>
          <p className="truncate font-mono text-hint text-muted-foreground" title={process.cmdline}>
            {process.cmdline || "Kernel worker"}
          </p>
        </div>
      </TableCell>
      <TableCell>
        <div className="max-w-40 min-w-0">
          <Tag>{managerName(process.manager)}</Tag>
          {process.managerName && (
            <p className="truncate text-hint text-muted-foreground" title={process.managerName}>
              {process.managerName}
            </p>
          )}
        </div>
      </TableCell>
      <TableCell className="hidden xl:table-cell">{process.username || "—"}</TableCell>
      <TableCell>
        <Status
          state={busy ? "activating" : processStateTone(process.state)}
          label={busy ? `${busy}…` : process.state}
        />
      </TableCell>
      <TableCell
        className={cn(
          "numeric text-right font-mono",
          cpuTone(process.cpuPercent) === "danger"
            ? "font-medium text-destructive"
            : cpuTone(process.cpuPercent) === "warning"
              ? "text-warning"
              : "text-muted-foreground",
        )}
      >
        {percent(process.cpuPercent)}
      </TableCell>
      <TableCell className="numeric text-right font-mono">
        {bytes(process.rss)}
        {memTotal > 0 && (
          <span className="ml-1 text-muted-foreground">
            {((process.rss / memTotal) * 100).toFixed(0)}%
          </span>
        )}
      </TableCell>
      <TableCell className="numeric hidden text-right font-mono text-muted-foreground xl:table-cell">
        {!ratesReady ? "—" : io > 0 ? `${bytes(io)}/s` : "—"}
      </TableCell>
      <TableCell className="hidden text-muted-foreground 2xl:table-cell">
        {relativeTime(process.createTime)}
      </TableCell>
      <TableCell>
        {/* Always drawn, quiet until the row is hovered: these own their
            column, and a reserved column left empty reads as a layout bug. */}
        <VerbActions dim verbs={verbs} />
      </TableCell>
    </TableRow>
  )
}

function ProcessTableNarrow({ rows, memTotal, ...rest }: RowsProps) {
  return (
    <ul className="divide-y divide-hairline">
      {rows.map((process) => (
        <ProcessNarrowRow
          key={processKey(process)}
          process={process}
          memTotal={memTotal}
          {...rest}
        />
      ))}
    </ul>
  )
}

/**
 * A row drawn down rather than across. Still a row, not a card: no frame,
 * a hairline to the next, a wash under the pointer. The title is the real
 * button; the surrounding click is a convenience for the pointer that skips
 * any press landing on a control of its own.
 */
function ProcessNarrowRow({
  process,
  memTotal,
  pending,
  confirm,
  signal,
  onOpen,
}: Omit<RowsProps, "rows"> & { process: ProcessRow }) {
  const verbs = useProcessVerbs({ process, confirm, signal, onInspect: () => onOpen(process) })
  const busy = pending[processKey(process)]
  return (
    <li
      className={cn(
        "group flex min-w-0 items-start gap-3 py-3 transition-colors hover:bg-row-hover",
        ROW_BLEED,
      )}
      onClick={(event) => {
        if ((event.target as HTMLElement).closest("a, button, [role='menuitem']")) return
        onOpen(process)
      }}
    >
      <div className="min-w-0 flex-1">
        <div className="flex min-w-0 items-baseline gap-2">
          <RowLink onClick={() => onOpen(process)}>{process.name}</RowLink>
          <span className="numeric font-mono text-hint text-muted-foreground">{process.pid}</span>
          <Tag>{managerName(process.manager)}</Tag>
        </div>
        <p className="truncate font-mono text-hint text-muted-foreground">
          {process.cmdline || "Kernel worker"}
        </p>
        <div className="mt-1.5 flex flex-wrap items-center gap-x-3 gap-y-1 text-hint">
          <Status
            state={busy ? "activating" : processStateTone(process.state)}
            label={busy ? `${busy}…` : process.state}
          />
          <span
            className={cn(
              "numeric font-mono",
              cpuTone(process.cpuPercent) !== "default" && "text-warning",
            )}
          >
            {percent(process.cpuPercent)} CPU
          </span>
          <span className="numeric font-mono text-muted-foreground">
            {bytes(process.rss)}
            {memTotal > 0 && ` · ${((process.rss / memTotal) * 100).toFixed(0)}%`}
          </span>
          {process.username && <span className="text-muted-foreground">{process.username}</span>}
        </div>
      </div>
      <VerbActions verbs={verbs} className="shrink-0" />
    </li>
  )
}

function FacetSelect({
  label,
  value,
  onChange,
  options,
}: {
  label: string
  value: string
  onChange: (value: string) => void
  options: ProcessList["users"]
}) {
  return (
    <Select value={value || "all"} onValueChange={(next) => onChange(next === "all" ? "" : next)}>
      <SelectTrigger size="sm" className="w-36" aria-label={label}>
        <SelectValue placeholder={label} />
      </SelectTrigger>
      <SelectContent>
        <SelectItem value="all">{label}</SelectItem>
        {options.map((option) => (
          <SelectItem key={option.value} value={option.value}>
            {option.label} ({option.count})
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  )
}

function ProcessTableSettings({
  limit,
  setLimit,
  refreshSeconds,
  setRefreshSeconds,
}: {
  limit: number
  setLimit: (value: number) => void
  refreshSeconds: number
  setRefreshSeconds: (value: number) => void
}) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="ghost" size="xs" className="text-muted-foreground">
          <SettingsSliders className="size-3.5" />
          Every {refreshSeconds}s · {limit} rows
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-52">
        <DropdownMenuLabel>Refresh every</DropdownMenuLabel>
        <DropdownMenuRadioGroup
          value={String(refreshSeconds)}
          onValueChange={(value) => setRefreshSeconds(Number(value))}
        >
          {[2, 4, 10, 30].map((seconds) => (
            <DropdownMenuRadioItem key={seconds} value={String(seconds)}>
              {seconds} seconds
            </DropdownMenuRadioItem>
          ))}
        </DropdownMenuRadioGroup>
        <DropdownMenuSeparator />
        <DropdownMenuLabel>Maximum rows</DropdownMenuLabel>
        <DropdownMenuRadioGroup
          value={String(limit)}
          onValueChange={(value) => setLimit(Number(value))}
        >
          {[100, 200, 500].map((count) => (
            <DropdownMenuRadioItem key={count} value={String(count)}>
              {count} rows
            </DropdownMenuRadioItem>
          ))}
        </DropdownMenuRadioGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

function useDebounced<T>(value: T, delay: number): T {
  const [settled, setSettled] = useState(value)
  useEffect(() => {
    const timer = window.setTimeout(() => setSettled(value), delay)
    return () => window.clearTimeout(timer)
  }, [value, delay])
  return settled
}
