"use client"

import { useEffect, useMemo, useState } from "react"
import { Cpu, Cross, Pause, Play, SettingsSliders } from "@/components/icons"
import { useArrivals } from "@/hooks/use-arrivals"
import { useMetrics } from "@/hooks/use-metrics"
import { usePoll } from "@/hooks/use-poll"
import { useQuerySelection } from "@/hooks/use-query-selection"
import { useConfirm } from "@/components/confirm-dialog"
import { FactDot, HostFact, HostIdentity, platformName } from "@/components/metrics/host-identity"
import { HUE, LiveBytes, LiveFigure } from "@/components/overview/readings"
import { Page, PageContext, RowLink, SearchInput } from "@/components/page"
import { Panel, PanelBody, PanelFooter, PanelHeader, PanelToolbar } from "@/components/panel"
import {
  ProductGlyph,
  ProductLogo,
  cpuProduct,
  platformProduct,
  processProduct,
} from "@/components/product-logo"
import { ROW_BLEED } from "@/components/row-list"
import { ChipCount, ChipStrip, FilterChip } from "@/components/tabs"
import { EmptyState, ErrorState, LoadingPanel } from "@/components/state"
import { Status, StatusDot } from "@/components/status-dot"
import { VerbActions } from "@/components/verbs"
import { Button } from "@/components/ui/button"
import { TextShimmer } from "@/components/ui/text-shimmer"
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
import { Workspace, WorkspaceHelp } from "@/components/workspace/workspace"
import {
  useProcessControl,
  useProcessVerbs,
  type ConfirmFn,
  type PendingMap,
} from "@/components/procs/process-actions"
import {
  cpuTone,
  groupName,
  managerName,
  ownerName,
  processCaption,
  processKey,
  processStateTone,
} from "@/components/procs/shared"
import { WorkloadBand } from "@/components/procs/workloads"

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
 * Everything running on the host, who is using it, and which of it is heavy.
 *
 * The machine first, as the identity line the Overview and Metrics open on —
 * its distribution drawn as itself, and its processor, load, memory and
 * uptime as facts that glide to each frame of the metrics socket — with the
 * table's own controls at its right end, where a line of two buttons used to
 * stand alone above it.
 *
 * Then who: `WorkloadBand`, the five heaviest workloads by processor and by
 * memory as spans of one bar the size of the machine. It replaced four tiles
 * (§15 pass 2 names the exit): the process count is a fact in the identity
 * line and the table's head; running, sleeping, blocked and zombie are the
 * state chips in that head, which count *and* narrow, the two that mean
 * trouble in their tone; unmanaged was already an owner chip; and the
 * supervised breakdown is the owner chips' counts.
 *
 * Then the table, whose question is "which rows" — the focus control answers
 * it by what the host is short of rather than by a fixed column. Every row is
 * drawn as the product it is where it is one (§14), its CPU and memory as a
 * short bar beside the figure so a column of them is seen before it is read,
 * and a process that started since the last poll rises into place.
 */
export function LiveProcesses() {
  const [paused, setPaused] = useState(false)
  const [order, setOrder] = useState<string[]>([])
  const { confirm, dialog } = useConfirm()
  const [query, setQuery] = useSessionState("processes.live.query", "")
  const [user, setUser] = useSessionState("processes.live.user", "")
  const [state, setState] = useSessionState("processes.live.state", "")
  const [manager, setManager] = useSessionState("processes.live.manager", "")
  const [group, setGroup] = useSessionState("processes.live.group", "")
  const [selectedPid, selectPid] = useQuerySelection("pid")
  const [sort, setSort] = useViewState<ProcessSort>("processes.table.sort.v2", "auto")
  const [limit, setLimit] = useViewState("processes.table.limit", 200)
  const [refreshSeconds, setRefreshSeconds] = useViewState("processes.table.refresh", 4)
  const appliedQuery = useDebounced(query, 250)
  const { host, snapshot } = useMetrics()
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
          group: group || undefined,
        },
        signal,
      ),
    paused ? 0 : refreshSeconds * 1000,
    [appliedQuery, effectiveSort, user, state, manager, group, limit],
  )
  const { pending, signal } = useProcessControl(processList.refresh)

  const data = processList.data
  const rows = useMemo(() => {
    const rank = new Map(order.map((id, index) => [id, index]))
    return [...(data?.processes ?? [])].sort(
      (a, b) => (rank.get(processKey(a)) ?? Infinity) - (rank.get(processKey(b)) ?? Infinity),
    )
  }, [data?.processes, order])
  const refresh = () => {
    setOrder([])
    processList.refresh()
  }
  const memTotal = snapshot?.memory?.total ?? 0
  const facet = (list: ProcessList["states"] | undefined, value: string) =>
    list?.find((f) => f.value === value)?.count ?? 0
  // A deep link is whatever was typed after `?pid=`; anything but a number
  // addressed `/processes/NaN` and said the process had exited.
  const pid = selectedPid && /^\d+$/.test(selectedPid) ? Number(selectedPid) : null
  const known = !!(host && snapshot?.cpu && snapshot.memory)
  // The workloads are the whole host's, whatever the table is filtered to, so
  // the band holds its last answer while a new filter's first read is out
  // rather than vanishing and counting up from nothing on every press.
  const [held, setHeld] = useState<Pick<ProcessList, "groups" | "ratesReady">>()
  if (data?.groups && data.groups !== held?.groups) {
    setHeld({ groups: data.groups, ratesReady: data.ratesReady })
  }
  const chosenGroup = held?.groups?.find((g) => g.key === group)
  // A filter change is a new list rather than arrivals into this one, so the
  // rows' arrival memory starts over with it.
  const listKey = [appliedQuery, user, state, manager, group].join("\u0000")

  const settings = (
    <ProcessTableSettings
      limit={limit}
      setLimit={setLimit}
      refreshSeconds={refreshSeconds}
      setRefreshSeconds={setRefreshSeconds}
    />
  )
  const controls = (
    <div className="flex flex-wrap items-center gap-1">
      {settings}
      <Button
        size="xs"
        variant="ghost"
        aria-pressed={paused}
        aria-label={paused ? "Resume updates" : "Pause updates"}
        className={cn("text-muted-foreground", paused && "text-foreground")}
        onClick={() => setPaused((value) => !value)}
      >
        {paused ? <Play className="size-3.5" /> : <Pause className="size-3.5" />}
        {paused ? "Resume" : "Pause"}
      </Button>
      <WorkspaceHelp compact />
    </div>
  )

  return (
    <Workspace
      name="Processes"
      refresh={refresh}
      escape={() => {
        if (query) {
          setQuery("")
          return true
        }
        if (group) {
          setGroup("")
          return true
        }
        if (order.length) {
          setOrder([])
          return true
        }
        return false
      }}
      commands={[
        {
          id: "pause",
          label: paused ? "Resume process updates" : "Pause process updates",
          run: () => setPaused((value) => !value),
        },
      ]}
    >
      <Page
        className="animate-rise"
        onFocusCapture={(event) => {
          if ((event.target as HTMLElement).closest("[data-workspace-item]") && !order.length)
            setOrder((data?.processes ?? []).map(processKey))
        }}
        onBlurCapture={(event) => {
          const next = event.relatedTarget as HTMLElement | null
          if (next && !next.closest("[data-workspace-item], [role='dialog'], [role='menu']"))
            setOrder([])
        }}
      >
        <PageContext eyebrow="Processes" title="Live" />

        {known && (
          <HostIdentity
            mark={platformProduct(host.platform)}
            title={host.hostname}
            facts={
              <>
                <HostFact product={platformProduct(host.platform)}>{platformName(host)}</HostFact>
                <FactDot />
                <HostFact product={cpuProduct(host.cpuModel, host.kernelArch)}>
                  <span className="numeric">
                    <LiveFigure value={snapshot.cpu.totalPercent} unit="%" /> CPU
                  </span>
                </HostFact>
                <FactDot />
                <span className="numeric">
                  load <LiveFigure value={snapshot.cpu.loadAvg1} decimals={2} />
                </span>
                <FactDot />
                <span className="numeric">
                  <LiveBytes value={snapshot.memory.available} /> available
                </span>
                <FactDot />
                <span className="numeric">up {duration(snapshot.uptimeSeconds)}</span>
                {data && (
                  <>
                    <FactDot />
                    <span className="numeric">
                      {plural(data.available, "process", "processes")}
                    </span>
                  </>
                )}
              </>
            }
            aside={controls}
          />
        )}

        {held?.groups && (
          <WorkloadBand
            groups={held.groups}
            snapshot={snapshot}
            ratesReady={held.ratesReady}
            selected={group}
            onSelect={setGroup}
          />
        )}

        {/* Framed, because it is a table: the grid owns a scroll region and the
          edge is what says so (§2). Everything above it stays plain. */}
        <Panel>
          <PanelHeader
            actions={
              <>
                <FacetSelect
                  label="All users"
                  value={user}
                  onChange={setUser}
                  options={data?.users ?? []}
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
                {!known && controls}
              </>
            }
            title={
              <>
                Processes
                {data && (
                  <span className="numeric ml-2 text-body font-normal text-muted-foreground">
                    {data.available}
                  </span>
                )}
              </>
            }
          >
            {/* The states sit beside the title as what it counts and as
              filters: Blocked and Zombie were two of the four tiles, and a
              chip says the number and narrows the table to it. */}
            {data && (
              <ChipStrip aria-label="State" className="mr-auto">
                {STATES.map(({ value, label, tone }) => {
                  const count = facet(data.states, value)
                  if (count === 0 && state !== value && value !== "running") return null
                  return (
                    <FilterChip
                      key={value}
                      selected={state === value}
                      onClick={() => setState(state === value ? "" : value)}
                    >
                      <span
                        aria-hidden
                        className={cn(
                          "size-1.5 rounded-full",
                          tone === "danger"
                            ? "bg-destructive"
                            : tone === "warning"
                              ? "bg-warning"
                              : value === "running"
                                ? "bg-success"
                                : "bg-muted-foreground/50",
                        )}
                      />
                      {label}
                      <ChipCount
                        className={cn(
                          tone === "danger" && "text-destructive opacity-100",
                          tone === "warning" && "text-warning opacity-100",
                        )}
                      >
                        {count}
                      </ChipCount>
                    </FilterChip>
                  )
                })}
              </ChipStrip>
            )}
          </PanelHeader>
          <PanelToolbar>
            <SearchInput
              value={query}
              onChange={(event) => setQuery(event.target.value)}
              placeholder="Name, command, PID, user or owner"
              containerClassName="sm:w-64"
            />
            <ChipStrip aria-label="Owner">
              <FilterChip selected={manager === ""} onClick={() => setManager("")}>
                All <ChipCount>{data?.available ?? 0}</ChipCount>
              </FilterChip>
              {data?.managers.map((option) => (
                <FilterChip
                  key={option.value}
                  selected={manager === option.value}
                  title={
                    option.value === "unmanaged"
                      ? "Started by nothing that would start it again: it does not survive a reboot"
                      : undefined
                  }
                  onClick={() => setManager(manager === option.value ? "" : option.value)}
                >
                  {option.label} <ChipCount>{option.count}</ChipCount>
                </FilterChip>
              ))}
              {group && (
                <FilterChip
                  selected
                  aria-label={`Showing ${chosenGroup ? groupName(chosenGroup) : groupLabel(group)}'s processes; press to show every workload`}
                  onClick={() => setGroup("")}
                >
                  {chosenGroup ? groupName(chosenGroup) : groupLabel(group)}
                  <Cross aria-hidden className="size-3" />
                </FilterChip>
              )}
            </ChipStrip>
          </PanelToolbar>
          <PanelBody flush>
            {processList.loading && !data && <LoadingPanel />}
            {processList.error && !data && <ErrorState error={processList.error} />}
            {data && (
              <ProcessRows
                key={listKey}
                rows={rows}
                ratesReady={data.ratesReady}
                memTotal={memTotal}
                pending={pending}
                confirm={confirm}
                signal={signal}
                onOpen={(p) => selectPid(String(p.pid))}
                onOpenPid={(next) => selectPid(String(next))}
              />
            )}
            {data && data.processes.length === 0 && (
              <EmptyState
                icon={Cpu}
                title="No processes match"
                description="Clear a filter or search for a different command, PID, user or owner."
                className="mt-4"
              />
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
              {(paused || order.length > 0) && (
                <span role="status">
                  {paused ? "Updates paused" : "Row order held while inspecting"}
                </span>
              )}
              {!data.ratesReady && (
                <>
                  <span className="text-muted-foreground/40">·</span>
                  <span>rates arrive with the second sample</span>
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
    </Workspace>
  )
}

/**
 * The states as chips in the table's head, in the order they are asked
 * about. Blocked and Zombie carry their tone and are drawn only while there
 * is one, or while they are the filter on screen.
 */
const STATES: { value: ProcessRow["state"]; label: string; tone?: "warning" | "danger" }[] = [
  { value: "running", label: "Running" },
  { value: "sleeping", label: "Sleeping" },
  { value: "blocked", label: "Blocked", tone: "warning" },
  { value: "zombie", label: "Zombie", tone: "danger" },
  { value: "stopped", label: "Stopped", tone: "warning" },
]

/** A workload filter whose group is not among the ones the band lists. */
function groupLabel(key: string) {
  const [kind, ...rest] = key.split(":")
  const name = rest.join(":")
  return kind === "kernel" ? "Kernel threads" : name || key
}

type RowsProps = {
  rows: ProcessRow[]
  memTotal: number
  pending: PendingMap
  confirm: ConfirmFn
  signal: Parameters<typeof useProcessVerbs>[0]["signal"]
  onOpen: (process: ProcessRow) => void
  onOpenPid: (pid: number) => void
}

/**
 * The rows, wide and narrow. Below `xl` the same rows are drawn down the row
 * instead of across it: a ten-column table keeps three on a phone, and what
 * is left is the remains of a table. Nothing is dropped — the PID, the
 * owner, both readings and the state are all part of "is the thing I just
 * deployed alive".
 *
 * Wide, the columns have fixed widths and the process takes what is left
 * (§12: the breakpoint is where the table stops fitting beside the sidebar,
 * not where the window stops being wide). Sized to their content, a long
 * command line pushed Memory, Disk I/O and every row's menu past the panel's
 * edge at 1280 and 1440, behind a sideways scroll nobody knew was there.
 */
function ProcessRows({ rows, ratesReady, ...rest }: RowsProps & { ratesReady: boolean }) {
  const arrived = useArrivals(rows.map(processKey))
  // The memory bars are drawn against the heaviest listed row: against the
  // host's total, a column of 2% processes is a column of empty tracks.
  const heaviest = rows.reduce((top, p) => Math.max(top, p.rss), 0)
  return (
    <>
      {/* The outer columns take the gutter from their own cell padding, so
        the first column starts in the title's column. */}
      <div className="hidden min-w-0 xl:block">
        <Table className="table-fixed" containerClassName="max-h-[calc(100svh-13rem)]">
          <TableHeader className={stickyTableHeader}>
            <TableRow>
              <TableHead className="w-24">PID</TableHead>
              <TableHead>Process</TableHead>
              <TableHead className="w-40">Owner</TableHead>
              <TableHead className="hidden w-28 2xl:table-cell">User</TableHead>
              <TableHead className="w-28">State</TableHead>
              <TableHead className="w-32 text-right">CPU</TableHead>
              <TableHead className="w-36 text-right">Memory</TableHead>
              <TableHead className="hidden w-28 text-right 2xl:table-cell">Disk I/O</TableHead>
              <TableHead className="hidden w-32 min-[1700px]:table-cell">Started</TableHead>
              <TableHead className="w-16">
                <span className="sr-only">Actions</span>
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {rows.map((process) => (
              <ProcessTableRow
                key={processKey(process)}
                process={process}
                ratesReady={ratesReady}
                heaviest={heaviest}
                arrived={arrived.has(processKey(process))}
                {...rest}
              />
            ))}
          </TableBody>
        </Table>
      </div>
      <ul className="divide-y divide-hairline px-4 xl:hidden">
        {rows.map((process) => (
          <ProcessNarrowRow
            key={processKey(process)}
            process={process}
            arrived={arrived.has(processKey(process))}
            {...rest}
          />
        ))}
      </ul>
    </>
  )
}

function ProcessTableRow({
  process,
  ratesReady,
  heaviest,
  arrived,
  memTotal,
  pending,
  confirm,
  signal,
  onOpen,
  onOpenPid,
}: Omit<RowsProps, "rows"> & {
  process: ProcessRow
  ratesReady: boolean
  heaviest: number
  arrived: boolean
}) {
  const verbs = useProcessVerbs({
    process,
    confirm,
    signal,
    onInspect: () => onOpen(process),
    onOpen: onOpenPid,
  })
  const busy = pending[processKey(process)]
  const io = (process.ioReadRate ?? 0) + (process.ioWriteRate ?? 0)
  return (
    <TableRow
      data-workspace-item={processKey(process)}
      data-workspace-name={process.name}
      className={cn("group", arrived && "animate-rise")}
      onActivate={() => onOpen(process)}
    >
      <TableCell className="numeric py-2 font-mono text-muted-foreground">{process.pid}</TableCell>
      <TableCell className="py-2">
        <div className="flex w-full min-w-0 items-center gap-3">
          <ProcessMark process={process} />
          <div className="min-w-0">
            <RowLink title={process.name} onClick={() => onOpen(process)}>
              {process.name}
            </RowLink>
            <p
              className={cn(
                "truncate text-hint text-muted-foreground",
                process.cmdline ? "font-mono" : "italic",
              )}
              title={process.cmdline || undefined}
            >
              {processCaption(process)}
            </p>
          </div>
        </div>
      </TableCell>
      <TableCell className="py-2">
        <Owner process={process} />
      </TableCell>
      <TableCell className="hidden truncate py-2 2xl:table-cell">
        {process.username || "—"}
      </TableCell>
      <TableCell className="py-2">
        <ProcessState process={process} busy={busy} />
      </TableCell>
      <TableCell className="py-2">
        <CpuReading process={process} />
      </TableCell>
      <TableCell className="py-2">
        <MemoryReading process={process} heaviest={heaviest} memTotal={memTotal} />
      </TableCell>
      <TableCell className="numeric hidden py-2 text-right font-mono text-muted-foreground 2xl:table-cell">
        {!ratesReady ? "—" : io > 0 ? `${bytes(io)}/s` : "—"}
      </TableCell>
      <TableCell className="hidden truncate py-2 text-muted-foreground min-[1700px]:table-cell">
        {relativeTime(process.createTime)}
      </TableCell>
      <TableCell className="py-2">
        {/* Always drawn, quiet until the row is hovered: these own their
            column, and a reserved column left empty reads as a layout bug. */}
        <VerbActions dim verbs={verbs} />
      </TableCell>
    </TableRow>
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
  arrived,
  memTotal,
  pending,
  confirm,
  signal,
  onOpen,
  onOpenPid,
}: Omit<RowsProps, "rows"> & { process: ProcessRow; arrived: boolean }) {
  const verbs = useProcessVerbs({
    process,
    confirm,
    signal,
    onInspect: () => onOpen(process),
    onOpen: onOpenPid,
  })
  const busy = pending[processKey(process)]
  return (
    <li
      data-workspace-item={processKey(process)}
      data-workspace-name={process.name}
      className={cn(
        "group flex min-w-0 items-start gap-3 py-3 transition-colors hover:bg-row-hover",
        ROW_BLEED,
        arrived && "animate-rise",
      )}
      onClick={(event) => {
        if ((event.target as HTMLElement).closest("a, button, [role='menuitem']")) return
        onOpen(process)
      }}
    >
      <ProcessMark process={process} />
      <div className="min-w-0 flex-1">
        <div className="flex min-w-0 items-baseline gap-2">
          <RowLink data-workspace-primary onClick={() => onOpen(process)}>
            {process.name}
          </RowLink>
          <span className="numeric font-mono text-hint text-muted-foreground">{process.pid}</span>
        </div>
        <p
          className={cn(
            "truncate text-hint text-muted-foreground",
            process.cmdline ? "font-mono" : "italic",
          )}
        >
          {processCaption(process)}
        </p>
        <div className="mt-1.5 flex flex-wrap items-center gap-x-3 gap-y-1 text-hint">
          <ProcessState process={process} busy={busy} />
          <span
            className={cn(
              "numeric font-mono",
              cpuTone(process.cpuPercent) !== "default" && "text-warning",
            )}
          >
            {process.cpuReady === false ? "Sampling CPU" : `${percent(process.cpuPercent)} CPU`}
          </span>
          <span className="numeric font-mono text-muted-foreground">
            {bytes(process.rss)}
            {memTotal > 0 && ` · ${((process.rss / memTotal) * 100).toFixed(0)}%`}
          </span>
          <span className="min-w-0 truncate text-muted-foreground">
            {ownerName(process) || managerName(process.manager)}
            {process.username && ` · ${process.username}`}
          </span>
        </div>
      </div>
      <VerbActions verbs={verbs} className="shrink-0" />
    </li>
  )
}

/** The state, or the verb in flight while a signal is out — lit, because it is happening. */
function ProcessState({ process, busy }: { process: ProcessRow; busy?: string }) {
  if (busy) {
    return (
      <span className="inline-flex items-center gap-1.5 text-xs font-medium">
        <StatusDot tone="notice" />
        <TextShimmer>{`${busy}…`}</TextShimmer>
      </span>
    )
  }
  return <Status state={processStateTone(process.state)} label={process.state} />
}

/**
 * A share of one core, as a figure and a short bar. The bar fills at a whole
 * core, so a process using three is a full bar and a figure past 100%.
 */
function CpuReading({ process }: { process: ProcessRow }) {
  const tone = cpuTone(process.cpuPercent)
  const ready = process.cpuReady !== false
  return (
    <div className="flex items-center justify-end gap-2">
      <MiniBar
        value={ready ? Math.min(process.cpuPercent, 100) : 0}
        color={
          tone === "danger" ? "var(--destructive)" : tone === "warning" ? "var(--warning)" : HUE.cpu
        }
      />
      <span
        className={cn(
          "numeric w-13 text-right font-mono",
          tone === "danger"
            ? "font-medium text-destructive"
            : tone === "warning"
              ? "text-warning"
              : "text-muted-foreground",
        )}
      >
        {ready ? percent(process.cpuPercent) : "—"}
      </span>
    </div>
  )
}

function MemoryReading({
  process,
  heaviest,
  memTotal,
}: {
  process: ProcessRow
  heaviest: number
  memTotal: number
}) {
  return (
    <div
      className="flex items-center justify-end gap-2"
      title={memTotal > 0 ? `${percent((process.rss / memTotal) * 100)} of the host` : undefined}
    >
      <MiniBar value={heaviest > 0 ? (process.rss / heaviest) * 100 : 0} color={HUE.mem} />
      <span className="numeric w-16 text-right font-mono">{bytes(process.rss)}</span>
    </div>
  )
}

/** A reading's bar inside a cell, easing to each poll's width. PM2's rows draw the same one. */
export function MiniBar({ value, color }: { value: number; color: string }) {
  return (
    <span aria-hidden className="h-1 w-8 shrink-0 overflow-hidden rounded-full bg-meter-track">
      <span
        className="block h-full rounded-full transition-[width] duration-700 ease-out"
        style={{ width: `${Math.max(0, Math.min(value, 100))}%`, background: color }}
      />
    </span>
  )
}

/** The process as the product it is; a name this cannot place keeps a glyph. */
function ProcessMark({ process }: { process: ProcessRow }) {
  return <ProductLogo id={processProduct(process.name)} size="sm" fallback={Cpu} />
}

/**
 * Who supervises the process: its name as its owner knows it — a container
 * by its name rather than the id its cgroup carries — over the kind, with
 * the supervisor's own mark where it has one: PM2 and Docker are products,
 * systemd and the kernel are not.
 */
const SUPERVISOR_PRODUCT: Partial<Record<ProcessRow["manager"], string>> = {
  pm2: "pm2",
  container: "docker",
}

function Owner({ process }: { process: ProcessRow }) {
  const product = SUPERVISOR_PRODUCT[process.manager]
  // A login session's scope is systemd's bookkeeping, not a name anybody
  // gave it; the kind leads and the session's number follows.
  const name =
    process.manager === "kernel" || process.manager === "session" ? "" : ownerName(process)
  const session =
    process.manager === "session" && process.managerName
      ? process.managerName.replace(/^session-/, "session ").replace(/\.scope$/, "")
      : ""
  return (
    <div className="min-w-0">
      <p className="flex min-w-0 items-center gap-1.5" title={name || undefined}>
        {product && <ProductGlyph id={product} className="size-3" />}
        <span className="truncate">{name || managerName(process.manager)}</span>
      </p>
      {(name || session) && (
        <p className="truncate text-hint text-muted-foreground">
          {name ? managerName(process.manager) : session}
        </p>
      )}
    </div>
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
