"use client"

import { Suspense, useEffect, useMemo, useState } from "react"
import { useRouter, useSearchParams } from "next/navigation"
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
import { useConfirm } from "@/components/confirm-dialog"
import { useNow } from "@/components/deploy/vocabulary"
import { FactDot, HostFact, HostIdentity } from "@/components/metrics/host-identity"
import { Page, PageContext, RowLink, SearchInput } from "@/components/page"
import { Panel, PanelBody, PanelFooter, PanelHeader, PanelToolbar } from "@/components/panel"
import { ProductLogo, ProductLogos, hasProductLogo, pm2Product } from "@/components/product-logo"
import { Row, RowList, ROW_BLEED } from "@/components/row-list"
import { ChipCount, ChipStrip, FilterChip } from "@/components/tabs"
import { EmptyState, LoadingPanel } from "@/components/state"
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
import { PM2FactsLine, PM2Idle, PM2Missing, PM2Unanswered } from "@/components/procs/pm2-missing"
import { PM2ScaleDialog } from "@/components/procs/pm2-scale-dialog"
import { PM2StartDialog } from "@/components/procs/pm2-start-dialog"
import { useInspectionOrder } from "@/components/procs/inspection-order"
import {
  useDaemonVerbs,
  usePM2Control,
  usePM2Verbs,
  type ConfirmFn,
  type PendingMap,
} from "@/components/procs/pm2-actions"
import {
  PM2_FILTER_KEYS,
  pm2AppHref,
  pm2LegacyHref,
  pm2Matches,
  pm2Positions,
  type PM2StateFilter,
} from "@/components/procs/pm2-facts"
import {
  isCluster,
  memoryLimitShare,
  pm2AppKey,
  pm2Apps,
  pm2Failing,
  pm2Key,
  pm2Order,
  savedDrift,
} from "@/components/procs/pm2-shared"
import { cpuTone } from "@/components/procs/shared"

/** PM2 reads its list every five seconds, and so does this page. */
const POLL = 5000

const STATES: { value: Exclude<PM2StateFilter, "">; label: string }[] = [
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
 * The page opens on PM2 as the thing it is about, laid out as a project's
 * header is: its own mark, the verdict beside its name, and the daemon facts
 * under it — the account, the Node it runs, the boot hook, and when the list
 * was last saved. The verdict reads the saved list itself: a list saved
 * before the last application was started restores everything but that one,
 * and the line says so beside the press that saves it again. At the line's
 * end the one command, Start application, and the daemon's own verbs behind
 * the menu, where they were a second grey button on the table's title. With
 * several accounts each daemon is a row of its own under the line, and the
 * line carries the worst verdict.
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
 * place. A row opens the application's own page (`pm2-app-page`).
 *
 * The line and the band are drawn while the list is being read, so the page
 * has its shape before it has its data.
 */
function PM2Applications() {
  const { can } = useAuth()
  const router = useRouter()
  const { confirm, dialog } = useConfirm()
  const { snapshot } = useMetrics()
  const [query, setQuery] = useSessionState(PM2_FILTER_KEYS.query, "")
  const [state, setState] = useSessionState<PM2StateFilter>(PM2_FILTER_KEYS.state, "")
  const [appFilter, setAppFilter] = useSessionState(PM2_FILTER_KEYS.app, "")
  const [scaling, setScaling] = useState<PM2Process | null>(null)
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
  const matching = useMemo(
    () => processes.filter((p) => pm2Matches(p, { query, state, app: appFilter })),
    [processes, query, state, appFilter],
  )
  const inspection = useInspectionOrder(matching, pm2Key, JSON.stringify([query, state, appFilter]))
  const visible = inspection.rows

  // `?app=` is where an application opened before it had a page of its own:
  // the process page, the server map, the search palette and a port's owner
  // linked there, and so did every shared link. It needs the list to turn an
  // `account:id` into a name, so it waits for the first read.
  const legacy = useSearchParams().get("app")
  const listed = inventory.data?.available ? inventory.data.processes : undefined
  useEffect(() => {
    if (legacy && listed) router.replace(pm2LegacyHref(listed, legacy))
  }, [legacy, listed, router])

  const open = (process: PM2Process, tab?: string) => {
    // The scale dialog opens where it was asked for; everything else is the
    // application's page, on the log of the worker whose row was pressed.
    if (tab === "scale") {
      setScaling(process)
      return
    }
    const shared = processes.some((p) => p.name === process.name && p.daemonId !== process.daemonId)
    router.push(
      pm2AppHref(process, {
        tab,
        account: shared,
        worker: tab === "logs" && isCluster(process) ? process.id : undefined,
      }),
    )
  }

  const header = <PageContext eyebrow="Processes" title="PM2" />
  const unread = !inventory.data ? (inventory.error ? "failed" : "loading") : undefined

  if (unread) {
    return (
      <Page>
        {header}
        <HostIdentity
          mark="pm2"
          title="PM2"
          state={
            unread === "loading" ? (
              <span className="text-xs text-muted-foreground">
                <TextShimmer>Reading its list…</TextShimmer>
              </span>
            ) : (
              <Status state="failed" label="Not answering" />
            )
          }
          facts={<PM2FactsLine />}
        />
        {inventory.error ? (
          <PM2Unanswered error={inventory.error} onRetry={inventory.refresh} />
        ) : (
          <>
            <PM2Band apps={[]} snapshot={snapshot} selected="" onSelect={() => {}} loading />
            <LoadingPanel />
          </>
        )}
      </Page>
    )
  }
  if (!inventory.data?.available) {
    return (
      <Page>
        {header}
        <PM2Missing />
      </Page>
    )
  }
  if (daemons.length === 0 && processes.length === 0) {
    return (
      <Page>
        {header}
        <PM2Idle />
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
      <Page className="animate-rise" {...inspection.bindings}>
        {header}

        <HostIdentity
          mark="pm2"
          title="PM2"
          state={
            daemons.length > 0 ? (
              <ResurrectVerdict daemons={daemons} processes={processes} />
            ) : undefined
          }
          command={
            <>
              {/* On a phone the line's first row is the name's and the
                  command's; saving is in the menu beside them. */}
              {save && daemonsDrift(daemons, processes) && (
                <Button size="sm" variant="outline" className="max-sm:hidden" onClick={save.run}>
                  Save list
                </Button>
              )}
              {can("system.admin") && daemons.length > 0 && (
                <Button size="sm" aria-label="Start application" onClick={() => setStarting(true)}>
                  <Plus className="size-3.5" />
                  Start<span className="max-sm:hidden"> application</span>
                </Button>
              )}
            </>
          }
          menu={
            daemonVerbs.length > 0 ? (
              <VerbMenu verbs={daemonVerbs} label="Startup and bulk" />
            ) : undefined
          }
          facts={
            <PM2FactsLine>
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
            </PM2FactsLine>
          }
        />

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

        <PM2Band apps={apps} snapshot={snapshot} selected={appFilter} onSelect={setAppFilter} />

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
            actions={<WorkspaceHelp compact />}
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
          {/* Nothing to search or narrow while PM2 runs nothing. */}
          <PanelToolbar className={cn(processes.length === 0 && "hidden")}>
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
                mark={hasProductLogo("pm2") ? <ProductLogos ids={["pm2"]} /> : undefined}
                title="PM2 runs nothing yet"
                description={
                  can("system.admin")
                    ? "Start an application from a script or an ecosystem file, then save the startup list."
                    : "Nothing has been started under PM2."
                }
                action={
                  can("system.admin") && daemons.length > 0 ? (
                    <Button size="sm" variant="outline" onClick={() => setStarting(true)}>
                      <Plus className="size-3.5" />
                      Start application
                    </Button>
                  ) : undefined
                }
                className="m-4 border-0"
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
                all={processes}
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

        {scaling && (
          <PM2ScaleDialog
            process={scaling}
            onClose={() => setScaling(null)}
            onChanged={inventory.refresh}
          />
        )}
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
 * Whether what PM2 runs survives a reboot, as the verdict beside PM2's name:
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
    return <Status state="failed" label={label} />
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
      />
    )
  }
  if (back.length > 0) {
    return (
      <span
        title={`Deleted since the list was saved, and restored by a reboot: ${back.join(", ")}`}
      >
        <Status tone="warning" label="Saved list out of date" />
      </span>
    )
  }
  return <Status state="active" label="Resurrects on boot" />
}

type RowProps = {
  process: PM2Process
  several: boolean
  unsaved: boolean
  arrived: boolean
  now: number
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
  all,
  several,
  unsaved,
  ...rest
}: {
  rows: PM2Process[]
  /** Every process PM2 lists, so a worker's number does not change with the filter. */
  all: PM2Process[]
  several: boolean
  unsaved: Set<string>
} & Pick<RowProps, "pending" | "confirm" | "act" | "onOpen" | "onChanged">) {
  const now = useNow(1000)
  const arrived = useArrivals(rows.map(pm2Key))
  const starts = useStarts(rows, now)
  const positions = useMemo(() => pm2Positions(all), [all])
  const props = (process: PM2Process): RowProps => ({
    process,
    several,
    unsaved: unsaved.has(pm2AppKey(process)),
    arrived: arrived.has(pm2Key(process)),
    now,
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
              <TableHead className="w-24 text-right">CPU</TableHead>
              <TableHead className="w-32 text-right">Memory</TableHead>
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
      // One height: a cell that stacks a figure over a hint is half a pixel
      // taller than one that stacks a name over a path, and a row without
      // either was shorter than its neighbours by that much.
      className={cn("group h-13", arrived && "animate-rise")}
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
        <MemoryReading process={process} />
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
    // eslint-disable-next-line jsx-a11y/click-events-have-key-events, jsx-a11y/no-noninteractive-element-interactions -- the pointer's larger target: the row's name is the link or button a keyboard opens it by
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

/**
 * A share of one core, as a figure in its column, toned where it is high. It
 * had a short meter beside it in every row, and a meter a row is a bar a row:
 * the band over the list is where the applications are drawn against each
 * other, and a column of figures is read down.
 */
function CpuReading({ process }: { process: PM2Process }) {
  if (process.status !== "online") {
    return <p className="text-right font-mono text-muted-foreground">—</p>
  }
  const tone = cpuTone(process.cpu)
  return (
    <p
      className={cn(
        "numeric text-right font-mono",
        tone === "danger"
          ? "font-medium text-destructive"
          : tone === "warning"
            ? "text-warning"
            : "text-muted-foreground",
      )}
    >
      {percent(process.cpu)}
    </p>
  )
}

/**
 * Resident memory as a figure, and under it the limit PM2 restarts the
 * application at when it has one: that is the ceiling it lives under, and the
 * figure is toned as it nears it.
 */
function MemoryReading({ process }: { process: PM2Process }) {
  if (process.status !== "online") {
    return <p className="text-right font-mono text-muted-foreground">—</p>
  }
  const limit = memoryLimitShare(process)
  const tone =
    limit === undefined ? "default" : limit >= 95 ? "danger" : limit >= 80 ? "warning" : "default"
  return (
    <div
      className="min-w-0 text-right"
      title={
        process.maxMemoryRestart
          ? `${percent(limit)} of the ${bytes(process.maxMemoryRestart, 0)} PM2 restarts it at`
          : undefined
      }
    >
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
