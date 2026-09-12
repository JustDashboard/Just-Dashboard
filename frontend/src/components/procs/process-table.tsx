"use client"

import Link from "next/link"
import { useEffect, useMemo, useState } from "react"
import { Cpu, Inspect, SettingsSliders } from "@/components/icons"
import { useAuth } from "@/hooks/use-auth"
import { useMetrics } from "@/hooks/use-metrics"
import { usePoll } from "@/hooks/use-poll"
import { useConfirm } from "@/components/confirm-dialog"
import { IconAction } from "@/components/icon-action"
import { Detail, DetailList, Metric, MetricStrip, RowLink, SearchInput } from "@/components/page"
import { Panel, PanelBody, PanelHeader, PanelToolbar, Well } from "@/components/panel"
import { SidePanel } from "@/components/side-panel"
import { EmptyState, ErrorState, LoadingPanel, Spinner } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
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
import { get, post, put } from "@/lib/api"
import { bytes, duration, percent, relativeTime } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { ProcessList, ProcessRow, Snapshot } from "@/lib/types"
import { cn } from "@/lib/utils"
import { useViewState } from "@/lib/view-state"

type ProcessSort = "auto" | "cpu" | "memory" | "io" | "uptime"

function automaticFocus(snapshot: Snapshot | undefined): {
  sort: Exclude<ProcessSort, "auto">
  reason: string
} {
  if (!snapshot) return { sort: "cpu", reason: "CPU until host metrics arrive" }
  const pressure = snapshot.pressure
  if (
    snapshot.procs.blocked > 0 ||
    snapshot.cpu.modes.iowait >= 10 ||
    (pressure.supported && pressure.ioSome >= 10)
  ) {
    return { sort: "io", reason: "disk wait is holding work up" }
  }
  const availablePercent =
    snapshot.memory.total > 0 ? (snapshot.memory.available / snapshot.memory.total) * 100 : 100
  if (availablePercent <= 10 || (pressure.supported && pressure.memSome >= 5)) {
    return { sort: "memory", reason: "available memory is tight" }
  }
  return { sort: "cpu", reason: "the host is not memory- or I/O-bound" }
}

export function ProcessTableTab() {
  const [query, setQuery] = useState("")
  const [user, setUser] = useState("")
  const [state, setState] = useState("")
  const [manager, setManager] = useState("")
  const [selected, setSelected] = useState<ProcessRow | null>(null)
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

  if (processList.loading && !processList.data) return <LoadingPanel />
  if (processList.error && !processList.data) return <ErrorState error={processList.error} />

  const data = processList.data
  const memTotal = snapshot?.memory.total ?? 0

  return (
    <>
      <Panel>
        <PanelHeader
          icon={Cpu}
          title="Live processes"
          actions={
            snapshot && (
              <MetricStrip>
                <Metric label="CPU" value={percent(snapshot.cpu.totalPercent, 0)} />
                <Metric label="Available" value={bytes(snapshot.memory.available)} />
                <Metric
                  label="Run / blocked"
                  value={`${snapshot.procs?.running ?? 0} / ${snapshot.procs?.blocked ?? 0}`}
                />
              </MetricStrip>
            )
          }
        />
        <PanelToolbar>
          <SearchInput
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder="Name, command, PID, user or owner"
            containerClassName="sm:w-80"
          />
          <FacetSelect
            label="All owners"
            value={manager}
            onChange={setManager}
            options={data?.managers ?? []}
          />
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
            <SelectTrigger size="sm" className="w-40" aria-label="Process focus">
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
          <ProcessTableSettings
            limit={limit}
            setLimit={setLimit}
            refreshSeconds={refreshSeconds}
            setRefreshSeconds={setRefreshSeconds}
          />
        </PanelToolbar>
        <PanelBody flush>
          <Table containerClassName="max-h-[calc(100svh-23rem)]">
            <TableHeader className={stickyTableHeader}>
              <TableRow>
                <TableHead className="hidden w-20 sm:table-cell">PID</TableHead>
                <TableHead className="w-full">Process</TableHead>
                <TableHead className="hidden lg:table-cell">Owner</TableHead>
                <TableHead className="hidden xl:table-cell">User</TableHead>
                <TableHead className="hidden md:table-cell">State</TableHead>
                <TableHead className="text-right">CPU</TableHead>
                <TableHead className="hidden text-right sm:table-cell">Memory</TableHead>
                <TableHead className="hidden text-right xl:table-cell">Disk I/O</TableHead>
                <TableHead className="hidden lg:table-cell">Started</TableHead>
                <TableHead className="w-px" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {data?.processes.map((process) => (
                <TableRow
                  key={`${process.pid}-${process.createTime}`}
                  className="group"
                  onActivate={() => setSelected(process)}
                >
                  <TableCell className="numeric hidden font-mono sm:table-cell">
                    {process.pid}
                  </TableCell>
                  <TableCell>
                    <div className="max-w-[28rem] min-w-0">
                      <RowLink onClick={() => setSelected(process)}>{process.name}</RowLink>
                      <p
                        className="truncate font-mono text-hint text-muted-foreground"
                        title={process.cmdline}
                      >
                        {process.cmdline || "Kernel worker"}
                      </p>
                      {/* PID and state have their own columns from sm and md
                          up. Below that they come here rather than being
                          dropped: a process you cannot identify by number is
                          one you cannot act on from a phone. */}
                      <p className="mt-0.5 font-mono text-hint text-muted-foreground md:hidden">
                        <span className="sm:hidden">{process.pid} · </span>
                        {process.state}
                      </p>
                    </div>
                  </TableCell>
                  <TableCell className="hidden lg:table-cell">
                    <div className="max-w-40 min-w-0">
                      <Tag>{managerName(process.manager)}</Tag>
                      {process.managerName && (
                        <p
                          className="mt-0.5 truncate text-hint text-muted-foreground"
                          title={process.managerName}
                        >
                          {process.managerName}
                        </p>
                      )}
                    </div>
                  </TableCell>
                  <TableCell className="hidden xl:table-cell">{process.username || "—"}</TableCell>
                  <TableCell className="hidden md:table-cell">
                    <Status state={processStateTone(process.state)} label={process.state} />
                  </TableCell>
                  <TableCell
                    className={cn(
                      "numeric text-right font-mono text-xs",
                      process.cpuPercent >= 50
                        ? "font-medium text-destructive"
                        : process.cpuPercent >= 10
                          ? "text-warning"
                          : "text-muted-foreground",
                    )}
                  >
                    {percent(process.cpuPercent)}
                  </TableCell>
                  <TableCell className="numeric hidden text-right font-mono sm:table-cell">
                    {bytes(process.rss)}
                    {memTotal > 0 && (
                      <span className="ml-1 text-muted-foreground">
                        {((process.rss / memTotal) * 100).toFixed(0)}%
                      </span>
                    )}
                  </TableCell>
                  <TableCell className="numeric hidden text-right font-mono text-muted-foreground xl:table-cell">
                    {!data.ratesReady
                      ? "Sampling…"
                      : (process.ioReadRate ?? 0) + (process.ioWriteRate ?? 0) > 0
                        ? `${bytes((process.ioReadRate ?? 0) + (process.ioWriteRate ?? 0))}/s`
                        : "—"}
                  </TableCell>
                  <TableCell className="hidden text-muted-foreground lg:table-cell">
                    {relativeTime(process.createTime)}
                  </TableCell>
                  <TableCell>
                    <IconAction label="Inspect process" onClick={() => setSelected(process)}>
                      <Inspect />
                    </IconAction>
                  </TableCell>
                </TableRow>
              ))}
              {data?.processes.length === 0 && (
                <TableRow>
                  <TableCell colSpan={10} className="p-0">
                    <EmptyState
                      icon={Cpu}
                      title="No processes match"
                      description="Clear a filter or search for a different command, PID, user or owner."
                    />
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
        </PanelBody>
      </Panel>
      <ProcessDetailSheet
        process={selected}
        onOpenChange={(open) => !open && setSelected(null)}
        onChanged={processList.refresh}
      />
    </>
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
          Configure
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

function ProcessDetailSheet({
  process,
  onOpenChange,
  onChanged,
}: {
  process: ProcessRow | null
  onOpenChange: (open: boolean) => void
  onChanged: () => void
}) {
  return (
    <SidePanel
      open={process !== null}
      onOpenChange={onOpenChange}
      icon={Cpu}
      title={process?.name ?? "Process"}
      description={
        process ? `PID ${process.pid} · ${process.username || "unknown user"}` : undefined
      }
      width="md"
      bodyClassName="p-4"
    >
      {process && (
        <ProcessDetail
          key={`${process.pid}-${process.createTime}`}
          process={process}
          onClosed={() => onOpenChange(false)}
          onChanged={onChanged}
        />
      )}
    </SidePanel>
  )
}

function ProcessDetail({
  process,
  onClosed,
  onChanged,
}: {
  process: ProcessRow
  onClosed: () => void
  onChanged: () => void
}) {
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()
  const [signal, setSignal] = useState("SIGTERM")
  const [nice, setNice] = useState(process.nice)
  const [openedAt] = useState(() => Date.now())
  const detail = usePoll(
    (abort) => get<ProcessRow>(`/processes/${process.pid}`, undefined, abort),
    4000,
    [process.pid],
  )
  const row = detail.data ?? process
  const uptime = useMemo(() => {
    const started = new Date(row.createTime).getTime()
    return Number.isFinite(started) ? duration(Math.max(0, (openedAt - started) / 1000)) : "—"
  }, [openedAt, row.createTime])

  const sendSignal = () => {
    const label = signalLabel(signal)
    confirm({
      title: `${label} process`,
      confirmLabel: `Send ${signal}`,
      description: (
        <div className="space-y-2">
          <p>
            Sends {signal} to <b>{row.name}</b> (PID {row.pid}). The process may stop or be
            restarted by {managerName(row.manager)}.
          </p>
          <Well className="max-h-24 break-all whitespace-pre-wrap">{row.cmdline || row.name}</Well>
        </div>
      ),
      action: async (confirmation) => {
        await post(
          `/processes/${row.pid}/signal`,
          { signal, startedAt: row.createTime },
          { confirm: confirmation },
        )
        notify.success(`${signal} sent to ${row.name}`)
        onChanged()
        if (["SIGTERM", "SIGKILL", "SIGINT"].includes(signal)) onClosed()
        else detail.refresh()
      },
    })
  }

  const savePriority = async () => {
    await put(`/processes/${row.pid}/priority`, { nice, startedAt: row.createTime })
    notify.success(`Priority updated for ${row.name}`)
    detail.refresh()
    onChanged()
  }

  if (detail.loading && !detail.data) return <Spinner />
  if (detail.error && !detail.data) {
    return (
      <EmptyState
        icon={Cpu}
        title="This process is no longer running"
        description="Short-lived processes can exit between opening the row and reading its details."
      />
    )
  }

  return (
    <div className="flex flex-col gap-4">
      <Panel>
        <PanelHeader
          title="Identity"
          actions={<Status state={processStateTone(row.state)} label={row.state} />}
        />
        <PanelBody>
          <DetailList>
            <Detail label="Managed by">
              {managerName(row.manager)}
              {row.managerName ? ` · ${row.managerName}` : ""}
            </Detail>
            <Detail label="Parent PID" className="font-mono">
              {row.ppid || "—"}
            </Detail>
            <Detail label="Executable" className="font-mono break-all">
              {row.exe || "Not reported"}
            </Detail>
            <Detail label="Working directory" className="font-mono break-all">
              {row.cwd || "Not reported"}
            </Detail>
            <Detail label="Started">{relativeTime(row.createTime)}</Detail>
            <Detail label="Uptime">{uptime}</Detail>
          </DetailList>
          <Well className="mt-4 max-h-36 whitespace-pre-wrap">{row.cmdline || row.name}</Well>
          <div className="mt-3 flex flex-wrap gap-2">
            {row.cwd && (
              <Button asChild size="xs" variant="outline">
                <Link href={`/files?path=${encodeURIComponent(row.cwd)}`}>
                  Open working directory
                </Link>
              </Button>
            )}
            {row.exe && (
              <Button asChild size="xs" variant="outline">
                <Link href={`/files?path=${encodeURIComponent(row.exe)}`}>Open executable</Link>
              </Button>
            )}
          </div>
        </PanelBody>
      </Panel>

      <Panel>
        <PanelHeader title="Resources" />
        <PanelBody>
          <DetailList>
            <Detail label="CPU">{percent(row.cpuPercent)}</Detail>
            <Detail label="Resident memory">{bytes(row.rss)}</Detail>
            <Detail label="Virtual memory">{bytes(row.vms)}</Detail>
            <Detail label="Disk read">{bytes(row.ioReadBytes ?? 0)}</Detail>
            <Detail label="Disk written">{bytes(row.ioWriteBytes ?? 0)}</Detail>
            <Detail label="Threads">{row.threads}</Detail>
            <Detail label="Child processes">{row.children ?? 0}</Detail>
            <Detail label="File descriptors">{row.fileDescriptors ?? "Not reported"}</Detail>
            <Detail label="Nice value">{row.nice}</Detail>
          </DetailList>
        </PanelBody>
      </Panel>

      {(can("system.admin") || can("destructive")) && (
        <Panel>
          <PanelHeader
            title="Control"
          />
          <PanelBody className="space-y-4">
            {can("system.admin") && (
              <div className="flex flex-wrap items-end gap-2">
                <label className="min-w-44 flex-1 space-y-1 text-xs">
                  <span className="font-medium">Scheduling priority</span>
                  <Select value={String(nice)} onValueChange={(value) => setNice(Number(value))}>
                    <SelectTrigger size="sm" aria-label="Scheduling priority">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      {Array.from({ length: 40 }, (_, index) => index - 20).map((value) => (
                        <SelectItem key={value} value={String(value)}>
                          {value}{" "}
                          {value < 0
                            ? "· higher priority"
                            : value > 0
                              ? "· lower priority"
                              : "· normal"}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </label>
                <Button
                  size="sm"
                  variant="outline"
                  disabled={nice === row.nice}
                  onClick={() => savePriority().catch((error) => notify.error(String(error)))}
                >
                  Save priority
                </Button>
              </div>
            )}
            {can("destructive") && (
              <div className="flex flex-wrap items-end gap-2">
                <label className="min-w-44 flex-1 space-y-1 text-xs">
                  <span className="font-medium">Signal</span>
                  <Select value={signal} onValueChange={setSignal}>
                    <SelectTrigger size="sm" aria-label="Signal process">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      {[
                        "SIGTERM",
                        "SIGHUP",
                        "SIGINT",
                        "SIGUSR1",
                        "SIGUSR2",
                        "SIGSTOP",
                        "SIGCONT",
                        "SIGKILL",
                      ].map((value) => (
                        <SelectItem key={value} value={value}>
                          {signalLabel(value)} · {value}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </label>
                <Button
                  size="sm"
                  variant={signal === "SIGKILL" ? "destructive" : "outline"}
                  onClick={sendSignal}
                >
                  Send signal
                </Button>
              </div>
            )}
          </PanelBody>
        </Panel>
      )}
      {dialog}
    </div>
  )
}

function managerName(manager: ProcessRow["manager"]): string {
  switch (manager) {
    case "pm2":
      return "PM2"
    case "systemd":
      return "systemd"
    case "container":
      return "Container"
    case "session":
      return "Login session"
    case "kernel":
      return "Kernel"
    default:
      return "Unmanaged"
  }
}

function processStateTone(state: ProcessRow["state"]): string {
  if (state === "blocked" || state === "zombie") return "failed"
  if (state === "sleeping") return "inactive"
  return state
}

function signalLabel(signal: string): string {
  switch (signal) {
    case "SIGTERM":
      return "Terminate"
    case "SIGKILL":
      return "Force kill"
    case "SIGINT":
      return "Interrupt"
    case "SIGHUP":
      return "Reload"
    case "SIGSTOP":
      return "Pause"
    case "SIGCONT":
      return "Resume"
    case "SIGUSR1":
      return "User signal 1"
    case "SIGUSR2":
      return "User signal 2"
    default:
      return signal
  }
}

function useDebounced<T>(value: T, delay: number): T {
  const [settled, setSettled] = useState(value)
  useEffect(() => {
    const timer = window.setTimeout(() => setSettled(value), delay)
    return () => window.clearTimeout(timer)
  }, [value, delay])
  return settled
}
