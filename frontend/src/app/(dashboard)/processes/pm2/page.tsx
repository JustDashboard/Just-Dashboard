"use client"

import { Suspense, useMemo, useState } from "react"
import { forgetSessionState, useSessionState } from "@/lib/view-state"
import { ChartActivity, Cross, Plus } from "@/components/icons"
import { get } from "@/lib/api"
import { bytes, duration, percent, plural, relativeTime } from "@/lib/format"
import type { PM2Daemon, PM2Inventory, PM2Process } from "@/lib/types"
import { cn } from "@/lib/utils"
import { useAuth } from "@/hooks/use-auth"
import { useArrivals } from "@/hooks/use-arrivals"
import { useMetrics } from "@/hooks/use-metrics"
import { usePoll } from "@/hooks/use-poll"
import { useQuerySelection } from "@/hooks/use-query-selection"
import { useConfirm } from "@/components/confirm-dialog"
import { useNow } from "@/components/deploy/vocabulary"
import { FactDot, HostFact, HostIdentity } from "@/components/metrics/host-identity"
import { HUE } from "@/components/overview/readings"
import { Page, PageContext, RowLink, SearchInput } from "@/components/page"
import { Panel, PanelBody, PanelFooter, PanelHeader, PanelToolbar } from "@/components/panel"
import { ProductLogo, pm2Product } from "@/components/product-logo"
import { Row, RowList, ROW_BLEED } from "@/components/row-list"
import { ChipCount, ChipStrip, FilterChip } from "@/components/tabs"
import { EmptyState, ErrorState, LoadingPanel } from "@/components/state"
import { Status, StatusDot } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { VerbActions, VerbMenu } from "@/components/verbs"
import { Button } from "@/components/ui/button"
import { TextShimmer } from "@/components/ui/text-shimmer"
import {
  stickyTableHeader,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { Workspace, WorkspaceHelp } from "@/components/workspace/workspace"
import { PM2Band } from "@/components/procs/pm2-band"
import { PM2DetailSheet } from "@/components/procs/pm2-detail"
import { PM2StartDialog } from "@/components/procs/pm2-start-dialog"
import {
  useDaemonVerbs,
  usePM2Control,
  usePM2Verbs,
  type ConfirmFn,
  type PendingMap,
} from "@/components/procs/pm2-actions"
import {
  isCluster,
  memoryLimitShare,
  pm2AppKey,
  pm2Apps,
  pm2Failing,
  pm2Key,
  pm2Order,
  pm2Select,
  savedDrift,
} from "@/components/procs/pm2-shared"
import { MiniBar } from "@/components/procs/process-table"
import { cpuTone } from "@/components/procs/shared"

/** PM2 reads its list every five seconds, and so does this page. */
const POLL = 5000

type StateFilter = "" | "online" | "stopped" | "errored"

const STATES: { value: Exclude<StateFilter, "">; label: string }[] = [
  { value: "online", label: "Online" },
  { value: "stopped", label: "Stopped" },
  { value: "errored", label: "Errored" },
]

export default function PM2Page() {
  return (
    <Suspense>
      <PM2Applications />
    </Suspense>
  )
}

/**
 * What PM2 runs, what it takes of the machine, and whether it would come back.
 *
 * The page opens on PM2 as the thing it is about — its own mark on the
 * identity line the Overview gives the machine — and the daemon facts are
 * that line's facts: the account, the Node it runs, the boot hook, and when
 * the list was last saved, with the verdict at the right end. The verdict
 * reads the saved list itself: a list saved before the last application was
 * started restores everything but that one, and the line says so beside the
 * press that saves it again. With several accounts each daemon is a row of
 * its own under the line, and the line carries the worst verdict.
 *
 * Then what it takes: `PM2Band`, the applications as spans of one bar the
 * size of the machine, by processor and by memory. It replaced four tiles
 * (§15 pass 2 names the exit): Online and Not running are the state chips in
 * the table's head, which count *and* narrow; Restarts summed every
 * application's counter, so the one crash-looping worker hid in a number —
 * now it is the table's first row, its unstable restarts in red; and Memory
 * is the band's figure, said with who holds it.
 *
 * Then the table, each application drawn as what runs it (§14) — Node, Bun or
 * Python by the interpreter PM2 reports, a glyph for a binary — its CPU and
 * memory as a figure beside a short bar (memory against the limit PM2
 * restarts it at, when it has one), its uptime ticking, its restart count
 * rising when it moves, and an application that just arrived rising into
 * place.
 */
function PM2Applications() {
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()
  const { snapshot } = useMetrics()
  const [query, setQuery] = useSessionState("processes.pm2.query", "")
  const [state, setState] = useSessionState<StateFilter>("processes.pm2.state.v2", "")
  const [appFilter, setAppFilter] = useSessionState("processes.pm2.app", "")
  const [selectedKey, selectKey] = useQuerySelection("app")
  const [focusTab, setFocusTab] = useState<string>()
  const [starting, setStarting] = useSessionState("processes.pm2.starting", false)
  const inventory = usePoll((signal) => get<PM2Inventory>("/pm2/", undefined, signal), POLL)
  const { pending, act } = usePM2Control(inventory.refresh)

  const processes = useMemo(() => pm2Order(inventory.data?.processes ?? []), [inventory.data])
  const daemons = useMemo(() => inventory.data?.daemons ?? [], [inventory.data])
  const apps = useMemo(() => pm2Apps(processes), [processes])
  const daemonVerbs = useDaemonVerbs({ daemons, confirm, onChanged: inventory.refresh })
  const save = daemonVerbs.find((verb) => verb.key === "save")
  // The Node every application runs, as one fact when they agree.
  const nodeVersions = useMemo(
    () => [...new Set(processes.map((p) => p.nodeVersion).filter(Boolean))],
    [processes],
  )
  const unsaved = useMemo(
    () =>
      new Set(
        daemons.flatMap((d) =>
          (savedDrift(d, processes)?.unsaved ?? []).map((name) => `${d.account}/${name}`),
        ),
      ),
    [daemons, processes],
  )

  const counts = useMemo(
    () => ({
      online: processes.filter((p) => p.status === "online").length,
      stopped: processes.filter((p) => p.status === "stopped").length,
      errored: processes.filter(pm2Failing).length,
    }),
    [processes],
  )
  const chosenApp = apps.find((a) => a.key === appFilter)
  const visible = useMemo(() => {
    const needle = query.trim().toLowerCase()
    return processes.filter((p) => {
      if (state === "online" && p.status !== "online") return false
      if (state === "stopped" && p.status !== "stopped") return false
      if (state === "errored" && !pm2Failing(p)) return false
      if (appFilter && pm2AppKey(p) !== appFilter) return false
      if (!needle) return true
      return (
        p.name.toLowerCase().includes(needle) ||
        p.scriptPath.toLowerCase().includes(needle) ||
        p.daemonId.toLowerCase().includes(needle)
      )
    })
  }, [processes, query, state, appFilter])

  const selected = useMemo(() => pm2Select(processes, selectedKey), [processes, selectedKey])

  const open = (process: PM2Process, tab?: string) => {
    setFocusTab(tab)
    selectKey(pm2Key(process))
  }

  const header = <PageContext eyebrow="Processes" title="PM2" />

  if (inventory.loading && !inventory.data) {
    return (
      <Page>
        {header}
        <LoadingPanel />
      </Page>
    )
  }
  if (inventory.error && !inventory.data) {
    return (
      <Page>
        {header}
        <ErrorState error={inventory.error} />
      </Page>
    )
  }
  if (!inventory.data?.available) {
    return (
      <Page>
        {header}
        <EmptyState
          icon={ChartActivity}
          title="PM2 is not installed"
          description="Install PM2 for a host account (npm install -g pm2) to manage Node applications from here. The dashboard finds every account's daemon on its own."
        />
      </Page>
    )
  }

  return (
    <Workspace
      name="PM2"
      refresh={inventory.refresh}
      escape={() => {
        if (query) {
          setQuery("")
          return true
        }
        if (appFilter) {
          setAppFilter("")
          return true
        }
        if (state) {
          setState("")
          return true
        }
        return false
      }}
    >
      <Page className="animate-rise">
        {header}

        {daemons.length > 0 && (
          <HostIdentity
            mark="pm2"
            title="PM2"
            facts={
              <>
                {daemons.length === 1 ? (
                  <DaemonFacts daemon={daemons[0]} nodeVersions={nodeVersions} />
                ) : (
                  <>
                    <span className="numeric font-medium text-foreground">
                      {daemons.length} daemons
                    </span>
                    <FactDot />
                    <span className="truncate">{daemons.map((d) => d.account).join(", ")}</span>
                    {nodeVersions.length > 0 && (
                      <>
                        <FactDot />
                        <HostFact product="nodejs">Node {nodeVersions.join(", ")}</HostFact>
                      </>
                    )}
                  </>
                )}
                <FactDot />
                <span className="numeric">{plural(apps.length, "application")}</span>
              </>
            }
            aside={
              <span className="flex items-center gap-3">
                <ResurrectVerdict daemons={daemons} processes={processes} />
                {save && daemonsDrift(daemons, processes) && (
                  <Button size="xs" variant="outline" onClick={save.run}>
                    Save list
                  </Button>
                )}
              </span>
            }
          />
        )}

        {daemons.length > 1 && (
          <Panel plain>
            <PanelHeader title="Daemons" />
            <PanelBody flush>
              <RowList>
                {daemons.map((daemon) => (
                  <Row
                    key={daemon.account}
                    title={daemon.account}
                    subtitle={
                      <span className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
                        <DaemonFacts daemon={daemon} nodeVersions={[]} />
                      </span>
                    }
                    trailing={<ResurrectVerdict daemons={[daemon]} processes={processes} />}
                    className="py-2.5"
                  />
                ))}
              </RowList>
            </PanelBody>
          </Panel>
        )}

        {apps.length > 0 && (
          <PM2Band apps={apps} snapshot={snapshot} selected={appFilter} onSelect={setAppFilter} />
        )}

        {/* Framed, because it is a table: the grid owns a scroll region and the
          edge is what says so (§2). Everything above it stays plain. */}
        <Panel>
          <PanelHeader
            title={
              <>
                Applications
                <span className="numeric ml-2 text-body font-normal text-muted-foreground">
                  {apps.length}
                </span>
              </>
            }
            actions={
              <>
                {can("system.admin") && daemons.length > 0 && (
                  <Button size="sm" onClick={() => setStarting(true)}>
                    <Plus className="size-3.5" />
                    Start application
                  </Button>
                )}
                {daemonVerbs.length > 0 && (
                  <VerbMenu
                    verbs={daemonVerbs}
                    trigger={
                      <Button size="sm" variant="outline">
                        Startup and bulk
                      </Button>
                    }
                  />
                )}
                <WorkspaceHelp compact />
              </>
            }
          >
            {/* The states sit beside the title as what it counts and as
              filters: Online and Not running were two of the four tiles, and
              a chip says the number and narrows the table to it. */}
            {processes.length > 0 && (
              <ChipStrip aria-label="State" className="mr-auto">
                {STATES.map(({ value, label }) => {
                  const count = counts[value]
                  if (count === 0 && state !== value && value !== "online") return null
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
                          value === "online"
                            ? "bg-success"
                            : value === "errored"
                              ? "bg-destructive"
                              : "bg-muted-foreground/50",
                        )}
                      />
                      {label}
                      <ChipCount
                        className={cn(value === "errored" && "text-destructive opacity-100")}
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
              onChange={(e) => setQuery(e.target.value)}
              placeholder="Name, script or account"
              containerClassName="sm:w-64"
            />
            {appFilter && (
              <ChipStrip aria-label="Application">
                <FilterChip
                  selected
                  aria-label={`Showing ${chosenApp?.name ?? appFilter.split("/").slice(1).join("/")} only; press to show every application`}
                  onClick={() => setAppFilter("")}
                >
                  {chosenApp?.name ?? appFilter.split("/").slice(1).join("/")}
                  <Cross aria-hidden className="size-3" />
                </FilterChip>
              </ChipStrip>
            )}
          </PanelToolbar>
          <PanelBody flush>
            {processes.length === 0 ? (
              <EmptyState
                icon={ChartActivity}
                title="PM2 is running but manages no applications"
                description={
                  can("system.admin")
                    ? "Start one from a script or an ecosystem file, and save the startup list once it runs."
                    : "Nothing has been started under PM2 yet."
                }
                className="mt-4"
              />
            ) : visible.length === 0 ? (
              <EmptyState
                icon={ChartActivity}
                title="No applications match"
                description="Clear a filter or search for a different name, script or account."
                className="mt-4"
              />
            ) : (
              <PM2Rows
                rows={visible}
                several={daemons.length > 1}
                unsaved={unsaved}
                pending={pending}
                confirm={confirm}
                act={act}
                onOpen={open}
                onChanged={inventory.refresh}
              />
            )}
          </PanelBody>
          {processes.length > 0 && (
            <PanelFooter className="text-hint text-muted-foreground">
              <span className="numeric">
                {plural(processes.length, "process", "processes")} · {counts.online} online ·{" "}
                {bytes(apps.reduce((s, a) => s + a.memory, 0))}
              </span>
              {counts.errored > 0 && (
                <>
                  <span className="text-muted-foreground/40">·</span>
                  <span>crashing applications first</span>
                </>
              )}
              <span className="text-muted-foreground/40">·</span>
              <span>read from PM2 every {POLL / 1000}s</span>
            </PanelFooter>
          )}
        </Panel>

        <PM2DetailSheet
          process={selected}
          processes={processes}
          initialTab={focusTab}
          onOpenChange={(o) => !o && selectKey(null)}
          onSelect={(process) => selectKey(pm2Key(process))}
          onChanged={inventory.refresh}
        />
        <PM2StartDialog
          open={starting}
          daemons={daemons}
          onOpenChange={(open) => {
            setStarting(open)
            // Closed by hand, whether cancelled or started: the next one starts blank.
            if (!open) forgetSessionState("processes.pm2.start.")
          }}
          onStarted={inventory.refresh}
        />
        {dialog}
      </Page>
    </Workspace>
  )
}

/**
 * The facts about one account's daemon: whose it is, the Node it runs, the
 * boot hook, and when the list was last saved. The hook and the save are the
 * day-after-the-reboot surprise this page exists to prevent, so each is
 * said plainly rather than hidden in a tooltip on the Save button.
 */
function DaemonFacts({ daemon, nodeVersions }: { daemon: PM2Daemon; nodeVersions: string[] }) {
  const hook = Boolean(daemon.startupUnit)
  const saved = Boolean(daemon.dumpSavedAt)
  return (
    <>
      <span className="font-medium text-foreground">{daemon.account}</span>
      {nodeVersions.length > 0 && (
        <>
          <FactDot />
          <HostFact product="nodejs">Node {nodeVersions.join(", ")}</HostFact>
        </>
      )}
      <FactDot />
      <span className="truncate">
        {hook ? (
          <>
            hook <span className="font-mono">{daemon.startupUnit}</span>
          </>
        ) : (
          <>
            run <span className="font-mono">pm2 startup</span> as {daemon.account} to install one
          </>
        )}
      </span>
      <FactDot />
      <span>{saved ? `list saved ${relativeTime(daemon.dumpSavedAt)}` : "list not yet saved"}</span>
    </>
  )
}

function daemonsDrift(daemons: PM2Daemon[], processes: PM2Process[]): boolean {
  return daemons.some((d) => {
    const drift = savedDrift(d, processes)
    return Boolean(d.startupUnit && drift && (drift.unsaved.length || drift.removed.length))
  })
}

/**
 * Whether what PM2 runs survives a reboot, as the verdict at the line's end:
 * red where a hook or a saved list is missing on any account, because a
 * daemon with three online applications and no saved list restores nothing;
 * amber where the list was saved and has since gone out of date — what was
 * started after the save does not come back, and what was deleted after it
 * does.
 */
function ResurrectVerdict({
  daemons,
  processes,
}: {
  daemons: PM2Daemon[]
  processes: PM2Process[]
}) {
  const noHook = daemons.filter((d) => !d.startupUnit)
  const unsavedList = daemons.filter((d) => d.startupUnit && !d.dumpSavedAt)
  const several = daemons.length > 1
  if (noHook.length > 0 || unsavedList.length > 0) {
    const label = !several
      ? noHook.length > 0
        ? "No boot hook"
        : "Startup list never saved"
      : noHook.length > 0
        ? `No boot hook for ${noHook.map((d) => d.account).join(", ")}`
        : `List never saved for ${unsavedList.map((d) => d.account).join(", ")}`
    return <Status state="failed" label={label} className="text-body" />
  }
  const drifts = daemons.map((d) => savedDrift(d, processes))
  const lost = drifts.flatMap((d) => d?.unsaved ?? [])
  const back = drifts.flatMap((d) => d?.removed ?? [])
  if (lost.length > 0) {
    return (
      <Status
        tone="warning"
        label={
          lost.length === 1
            ? `${lost[0]} is not in the saved list`
            : `${lost.length} applications are not in the saved list`
        }
        className="text-body"
      />
    )
  }
  if (back.length > 0) {
    return (
      <span
        title={`Deleted since the list was saved, and restored by a reboot: ${back.join(", ")}`}
      >
        <Status tone="warning" label="Saved list out of date" className="text-body" />
      </span>
    )
  }
  return <Status state="active" label="Resurrects on boot" className="text-body" />
}

type RowProps = {
  process: PM2Process
  several: boolean
  unsaved: boolean
  arrived: boolean
  now: number
  heaviest: number
  started?: number
  /** Which of its cluster's instances this is, counted from one, and how many there are. */
  position?: [number, number]
  pending: PendingMap
  confirm: ConfirmFn
  act: Parameters<typeof usePM2Verbs>[0]["act"]
  onOpen: (process: PM2Process, tab?: string) => void
  onChanged: () => void
}

/**
 * The rows, wide and narrow. Wide from `xl`, with fixed columns and the
 * application taking what is left, so a long script path ellipses rather than
 * pushing every row's verbs behind a sideways scroll; below it each row is
 * drawn down rather than across, nothing dropped.
 *
 * Each application's start is held across polls, so its uptime ticks every
 * second between them instead of jumping by five, and a restart — a start
 * that moved — resets it.
 */
function PM2Rows({
  rows,
  several,
  unsaved,
  ...rest
}: {
  rows: PM2Process[]
  several: boolean
  unsaved: Set<string>
} & Pick<RowProps, "pending" | "confirm" | "act" | "onOpen" | "onChanged">) {
  const now = useNow(1000)
  const arrived = useArrivals(rows.map(pm2Key))
  const starts = useStarts(rows, now)
  // Memory without a limit is drawn against the heaviest listed instance:
  // against the host's total, a column of 3% applications is a column of
  // empty tracks.
  const heaviest = rows.reduce((top, p) => Math.max(top, p.memory), 0)
  const positions = useMemo(() => clusterPositions(rows), [rows])
  const props = (process: PM2Process): RowProps => ({
    process,
    several,
    unsaved: unsaved.has(pm2AppKey(process)),
    arrived: arrived.has(pm2Key(process)),
    now,
    heaviest,
    started: starts.get(pm2Key(process)),
    position: positions.get(pm2Key(process)),
    ...rest,
  })
  return (
    <>
      <div className="hidden min-w-0 xl:block">
        <Table className="table-fixed" containerClassName="max-h-[calc(100svh-13rem)]">
          <TableHeader className={stickyTableHeader}>
            <TableRow>
              <TableHead>Application</TableHead>
              <TableHead className="w-36">Status</TableHead>
              <TableHead className="hidden w-28 2xl:table-cell">Mode</TableHead>
              <TableHead className="w-32 text-right">CPU</TableHead>
              <TableHead className="w-40 text-right">Memory</TableHead>
              <TableHead className="w-28 text-right">Restarts</TableHead>
              <TableHead className="w-36">
                <span className="sr-only">Actions</span>
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {rows.map((process) => (
              <PM2Row key={pm2Key(process)} {...props(process)} />
            ))}
          </TableBody>
        </Table>
      </div>
      <ul className="divide-y divide-hairline px-4 xl:hidden">
        {rows.map((process) => (
          <PM2NarrowRow key={pm2Key(process)} {...props(process)} />
        ))}
      </ul>
    </>
  )
}

/**
 * When each listed application started, held across polls. PM2's uptime is
 * read every five seconds from a list cached for three, so recomputing the
 * start from each read moves it by a few seconds either way and a ticking
 * uptime would step backwards; a start that moved by more than that is a
 * restart, and is taken.
 */
function useStarts(rows: PM2Process[], now: number): Map<string, number> {
  const [held, setHeld] = useState<{ rows: PM2Process[]; starts: Map<string, number> }>({
    rows: [],
    starts: new Map(),
  })
  if (held.rows === rows) return held.starts
  const starts = new Map<string, number>()
  for (const process of rows) {
    if (process.status !== "online" || process.uptimeMs <= 0) continue
    const key = pm2Key(process)
    const fresh = now - process.uptimeMs
    const known = held.starts.get(key)
    starts.set(key, known !== undefined && Math.abs(known - fresh) < 10_000 ? known : fresh)
  }
  setHeld({ rows, starts })
  return starts
}

function clusterPositions(rows: PM2Process[]): Map<string, [number, number]> {
  const out = new Map<string, [number, number]>()
  for (const app of pm2Apps(rows)) {
    if (!isCluster(app.instances[0])) continue
    const ordered = [...app.instances].sort((a, b) => a.id - b.id)
    ordered.forEach((p, index) => out.set(pm2Key(p), [index + 1, ordered.length]))
  }
  return out
}

/** The application as what runs it — Node, Bun, Python — or a glyph for a binary. */
function ApplicationMark({ process }: { process: PM2Process }) {
  return <ProductLogo id={pm2Product(process.interpreter)} size="sm" fallback={ChartActivity} />
}

function useRowVerbs({ process, confirm, act, onOpen, onChanged }: RowProps) {
  return usePM2Verbs({
    process,
    confirm,
    act,
    onOpenTab: (tab) => onOpen(process, tab),
    onScale: () => onOpen(process, "scale"),
    onChanged,
  })
}

function PM2Row(props: RowProps) {
  const { process, several, unsaved, arrived, pending, onOpen } = props
  const verbs = useRowVerbs(props)
  return (
    <TableRow
      data-workspace-item={pm2Key(process)}
      data-workspace-name={process.name}
      className={cn("group", arrived && "animate-rise")}
      onActivate={() => onOpen(process)}
    >
      <TableCell className="py-2">
        <div className="flex w-full min-w-0 items-center gap-3">
          <ApplicationMark process={process} />
          <div className="min-w-0">
            <div className="flex min-w-0 items-center gap-2">
              <RowLink title={process.name} onClick={() => onOpen(process)}>
                {process.name}
              </RowLink>
              <span className="numeric shrink-0 font-mono text-hint text-muted-foreground">
                #{process.id}
              </span>
              {/* Mode has a column of its own only where the table has room
                  for one; narrower, a cluster's instance says which it is here. */}
              {props.position && (
                <span className="numeric shrink-0 text-hint text-muted-foreground 2xl:hidden">
                  {props.position[0]} of {props.position[1]}
                </span>
              )}
              {several && (
                <span className="truncate text-hint text-muted-foreground">{process.daemonId}</span>
              )}
              {process.watching && <Tag tone="warning">watch</Tag>}
              {unsaved && (
                <span title="Started since the startup list was saved: a reboot would not bring it back">
                  <Tag tone="warning">not saved</Tag>
                </span>
              )}
            </div>
            <p
              className="truncate font-mono text-hint text-muted-foreground"
              title={process.scriptPath}
            >
              {process.scriptPath}
            </p>
          </div>
        </div>
      </TableCell>
      <TableCell className="py-2">
        <AppState process={process} busy={pending[pm2Key(process)]} />
        <Uptime {...props} className="mt-0.5 block" />
      </TableCell>
      <TableCell className="hidden py-2 2xl:table-cell">
        <ModeCell {...props} />
      </TableCell>
      <TableCell className="py-2">
        <CpuReading process={process} />
      </TableCell>
      <TableCell className="py-2">
        <MemoryReading process={process} heaviest={props.heaviest} />
      </TableCell>
      <TableCell className="py-2 text-right">
        <Restarts process={process} />
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
 * A row drawn down rather than across. Still a row, not a card: no frame, a
 * hairline to the next, a wash under the pointer.
 */
function PM2NarrowRow(props: RowProps) {
  const { process, several, unsaved, arrived, pending, onOpen } = props
  const verbs = useRowVerbs(props)
  const limit = memoryLimitShare(process)
  return (
    <li
      data-workspace-item={pm2Key(process)}
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
      <ApplicationMark process={process} />
      <div className="min-w-0 flex-1">
        <div className="flex min-w-0 items-baseline gap-2">
          <RowLink data-workspace-primary onClick={() => onOpen(process)}>
            {process.name}
          </RowLink>
          <span className="numeric font-mono text-hint text-muted-foreground">#{process.id}</span>
          {several && <span className="text-hint text-muted-foreground">{process.daemonId}</span>}
          {unsaved && <Tag tone="warning">not saved</Tag>}
        </div>
        <p className="truncate font-mono text-hint text-muted-foreground">{process.scriptPath}</p>
        <div className="mt-1.5 flex flex-wrap items-center gap-x-3 gap-y-1 text-hint">
          <AppState process={process} busy={pending[pm2Key(process)]} />
          <Uptime {...props} />
          {process.status === "online" && (
            <>
              <span
                className={cn(
                  "numeric font-mono",
                  cpuTone(process.cpu) === "default" ? "text-muted-foreground" : "text-warning",
                )}
              >
                {percent(process.cpu)} CPU
              </span>
              <span
                className={cn(
                  "numeric font-mono",
                  limit !== undefined && limit >= 80 ? "text-warning" : "text-muted-foreground",
                )}
              >
                {bytes(process.memory)}
                {process.maxMemoryRestart ? ` of ${bytes(process.maxMemoryRestart, 0)}` : ""}
              </span>
            </>
          )}
          <span className="numeric font-mono text-muted-foreground">
            {plural(process.restarts, "restart")}
            {process.unstableRestarts > 0 && (
              <span className="ml-1 text-destructive">({process.unstableRestarts} unstable)</span>
            )}
          </span>
        </div>
      </div>
      <VerbActions verbs={verbs} className="shrink-0" />
    </li>
  )
}

/** The state, or the verb in flight while one is out — lit, because it is happening. */
function AppState({ process, busy }: { process: PM2Process; busy?: string }) {
  if (busy) {
    return (
      <span className="inline-flex items-center gap-1.5 text-xs font-medium">
        <StatusDot tone="notice" />
        <TextShimmer>{`${busy}…`}</TextShimmer>
      </span>
    )
  }
  return <Status state={process.status} />
}

/**
 * How long it has been up, ticking; or, for an application PM2 gave up on,
 * that it gave up — an errored application is not restarted again until a
 * person starts it.
 */
function Uptime({
  process,
  now,
  started,
  className,
}: Pick<RowProps, "process" | "now" | "started"> & { className?: string }) {
  if (process.status === "online" && started !== undefined) {
    const seconds = Math.max(0, (now - started) / 1000)
    return (
      <span
        className={cn(
          "numeric text-hint",
          // A start in the last five minutes is the restart the counter just
          // counted; the column says it while it is news.
          seconds < 300 && process.restarts > 0 ? "text-warning" : "text-muted-foreground",
          className,
        )}
      >
        up {duration(seconds)}
      </span>
    )
  }
  if (pm2Failing(process) && process.unstableRestarts > 0) {
    return (
      <span className={cn("text-hint text-muted-foreground", className)}>PM2 stopped retrying</span>
    )
  }
  return null
}

function ModeCell({ process, position }: Pick<RowProps, "process" | "position">) {
  if (!isCluster(process)) {
    return (
      <div className="flex items-center gap-1.5">
        <span className="text-body">Fork</span>
        {process.watching && <span className="text-hint text-warning">· watch</span>}
      </div>
    )
  }
  return (
    <div className="min-w-0">
      <p className="text-body">Cluster</p>
      <p className="numeric text-hint text-muted-foreground">
        {position ? `${position[0]} of ${position[1]}` : `${process.instances || 1} workers`}
      </p>
    </div>
  )
}

/** A share of one core, as a figure and a short bar that fills at a whole core. */
function CpuReading({ process }: { process: PM2Process }) {
  if (process.status !== "online") {
    return <p className="text-right font-mono text-muted-foreground">—</p>
  }
  const tone = cpuTone(process.cpu)
  return (
    <div className="flex items-center justify-end gap-2">
      <MiniBar
        value={Math.min(process.cpu, 100)}
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
        {percent(process.cpu)}
      </span>
    </div>
  )
}

/**
 * Resident memory, against the limit PM2 restarts the application at when it
 * has one — that is the ceiling it lives under — and otherwise against the
 * heaviest listed instance.
 */
function MemoryReading({ process, heaviest }: { process: PM2Process; heaviest: number }) {
  if (process.status !== "online") {
    return <p className="text-right font-mono text-muted-foreground">—</p>
  }
  const limit = memoryLimitShare(process)
  const tone =
    limit === undefined ? "default" : limit >= 95 ? "danger" : limit >= 80 ? "warning" : "default"
  return (
    <div
      className="flex items-center justify-end gap-2"
      title={
        process.maxMemoryRestart
          ? `${percent(limit)} of the ${bytes(process.maxMemoryRestart, 0)} PM2 restarts it at`
          : undefined
      }
    >
      <MiniBar
        value={limit ?? (heaviest > 0 ? (process.memory / heaviest) * 100 : 0)}
        color={
          tone === "danger" ? "var(--destructive)" : tone === "warning" ? "var(--warning)" : HUE.mem
        }
      />
      <span className="min-w-0 text-right">
        <span
          className={cn(
            "numeric block font-mono",
            tone === "warning" && "text-warning",
            tone === "danger" && "text-destructive",
          )}
        >
          {bytes(process.memory)}
        </span>
        {process.maxMemoryRestart ? (
          <span className="numeric block text-hint text-muted-foreground">
            of {bytes(process.maxMemoryRestart, 0)}
          </span>
        ) : null}
      </span>
    </div>
  )
}

/**
 * The restart counter, rising into place when it moves — a restart between
 * two polls is the one thing on the row that happened rather than changed —
 * with the unstable ones, PM2's count of starts that died within a second,
 * in red under it.
 */
function Restarts({ process }: { process: PM2Process }) {
  return (
    <div className="numeric font-mono">
      <span key={process.restarts} className="inline-block animate-rise">
        {process.restarts}
      </span>
      {process.unstableRestarts > 0 && (
        <span className="block text-hint text-destructive">
          {process.unstableRestarts} unstable
        </span>
      )}
    </div>
  )
}
