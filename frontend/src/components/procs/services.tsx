"use client"

import { useMemo, useState } from "react"
import { useSearchParams } from "next/navigation"
import { Cross, RefreshClockwise, Servers } from "@/components/icons"
import { useAuth } from "@/hooks/use-auth"
import { useArrivals } from "@/hooks/use-arrivals"
import { useMetrics } from "@/hooks/use-metrics"
import { usePoll } from "@/hooks/use-poll"
import { useQuerySelection } from "@/hooks/use-query-selection"
import { useConfirm } from "@/components/confirm-dialog"
import { useNow } from "@/components/deploy/vocabulary"
import { FactDot, HostFact, HostIdentity, platformName } from "@/components/metrics/host-identity"
import { HUE } from "@/components/overview/readings"
import { Page, PageContext, RowLink, SearchInput } from "@/components/page"
import { Panel, PanelBody, PanelFooter, PanelHeader, PanelToolbar } from "@/components/panel"
import { ProductLogo, platformProduct, unitProduct } from "@/components/product-logo"
import { ROW_BLEED } from "@/components/row-list"
import { ChipCount, ChipStrip, FilterChip } from "@/components/tabs"
import { EmptyState, ErrorState, LoadingPanel } from "@/components/state"
import { Status, StatusDot } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { VerbActions } from "@/components/verbs"
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
import { get, post } from "@/lib/api"
import { bytes, duration, percent, plural } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { SystemdList, SystemdUnit } from "@/lib/types"
import { cn } from "@/lib/utils"
import { useSessionState } from "@/lib/view-state"
import { MiniBar } from "@/components/procs/process-table"
import { ServiceBand } from "@/components/procs/service-band"
import { useInspectionOrder } from "@/components/procs/inspection-order"
import { cpuTone } from "@/components/procs/shared"
import { UnitJournalSheet } from "@/components/procs/unit-journal"
import {
  useUnitControl,
  useUnitVerbs,
  type ConfirmFn,
  type PendingMap,
} from "@/components/procs/unit-actions"
import {
  ago,
  failureWords,
  unitBucket,
  unitStateWord,
  unitTone,
  type UnitBucket,
} from "@/components/procs/units"

/**
 * How often the list is read. Each read is one `systemctl show` over the
 * loaded units, a few milliseconds of PID 1's time a unit, and the figures
 * glide between reads.
 */
const POLL = 5000

/** The table's rows; a host with more services than this is narrowed by search. */
const ROWS = 400

type StartupFilter = "" | "enabled" | "disabled" | "static" | "masked"

const STARTUP: { value: Exclude<StartupFilter, "">; label: string; title: string }[] = [
  { value: "enabled", label: "Enabled", title: "Starts on boot" },
  { value: "disabled", label: "Disabled", title: "Runs only when started by hand" },
  { value: "static", label: "Static", title: "Started by another unit, not on its own" },
  { value: "masked", label: "Masked", title: "Blocked from starting at all" },
]

/** The state chips, in the order they are asked about; the toned two only while there is one. */
const STATES: { value: UnitBucket; label: string; tone?: "warning" | "danger" }[] = [
  { value: "active", label: "Active" },
  { value: "failed", label: "Failed", tone: "danger" },
  { value: "changing", label: "Starting", tone: "warning" },
  { value: "inactive", label: "Inactive" },
]

const BUCKETS = new Set<string>(STATES.map((s) => s.value))
const STARTUPS = new Set<string>(STARTUP.map((s) => s.value))

/** An enabled-runtime unit starts on this boot's next start as an enabled one does. */
function startupOf(unit: SystemdUnit): string {
  const state = unit.unitFileState || "unknown"
  return state === "enabled-runtime" ? "enabled" : state
}

/**
 * Every systemd service on the host, what each is using, and what just
 * happened to them.
 *
 * The machine first, as the identity line Live and the Overview open on —
 * the distribution as its mark, systemd's version, how many services are
 * active and start on boot, when it booted — with the verdict at its right
 * end: the failed count, which narrows the table to them, or that nothing
 * failed. Reload unit files and the shortcuts sit beside it, where a button
 * stood alone over the table.
 *
 * Then `ServiceBand`: the services using the most processor and memory as
 * spans of one bar the size of the machine, and the last to start, stop,
 * finish or fail. It replaced four tiles (§15 pass 2 names the exit): active,
 * failed and inactive are the state chips in the table's head, which count
 * *and* narrow, failed in its tone and first in the table as it always was;
 * "enabled on boot" is the identity line's fact and the Enabled startup chip,
 * where the disabled and static it used to hint at are chips of their own.
 *
 * Then the table, every unit as the product it runs (§14), its state with how
 * long it has been in it — up for, failed since and why, the restarts it has
 * taken — and its processor and memory as a figure beside a short bar, read
 * from its cgroup. New units rise into place; an existing unit changes state
 * in place so starting or stopping it does not replay its arrival.
 */
export function Services() {
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()
  const params = useSearchParams()
  const { host, snapshot } = useMetrics()
  const [query, setQuery] = useSessionState("processes.services.query", "")
  const [rememberedState, rememberState] = useSessionState("processes.services.state", "")
  const [requestedState, setRequestedState] = useState(
    params.get("state") === "failed" ? "failed" : rememberedState,
  )
  // A remembered "all" from before the chips were toggles is no filter.
  const state = (BUCKETS.has(requestedState) ? requestedState : "") as UnitBucket | ""
  const setState = (next: UnitBucket | "") => {
    setRequestedState(next)
    rememberState(next)
  }
  const [rememberedStartup, setStartup] = useSessionState("processes.services.startup", "")
  const startup = (STARTUPS.has(rememberedStartup) ? rememberedStartup : "") as StartupFilter
  const [selected, select] = useQuerySelection("unit")
  const [focusTab, setFocusTab] = useState<string>()
  const [reloading, setReloading] = useState(false)
  const list = usePoll((signal) => get<SystemdList>("/systemd/", undefined, signal), POLL)
  const { pending, act } = useUnitControl(list.refresh)
  const now = useNow(30_000)

  const all = useMemo(() => list.data?.units ?? [], [list.data])
  // A unit another one names but no file defines is listed by systemd as
  // inactive; it is not installed, and is counted rather than listed.
  const installed = useMemo(() => all.filter((u) => u.loadState !== "not-found"), [all])
  const missing = all.length - installed.length
  const counts = useMemo(() => {
    const bucket: Record<UnitBucket, number> = { active: 0, failed: 0, changing: 0, inactive: 0 }
    const boot: Record<string, number> = {}
    for (const u of installed) {
      bucket[unitBucket(u)]++
      boot[startupOf(u)] = (boot[startupOf(u)] ?? 0) + 1
    }
    return { bucket, boot }
  }, [installed])
  const changing = installed.filter((u) => unitBucket(u) === "changing")
  const changingLabel = changing.every((u) => u.activeState === "activating")
    ? "Starting"
    : "Changing"
  const matching = useMemo(() => {
    const needle = query.trim().toLowerCase()
    return installed.filter((u) => {
      if (state && unitBucket(u) !== state) return false
      if (startup && startupOf(u) !== startup) return false
      if (!needle) return true
      return u.name.toLowerCase().includes(needle) || u.description.toLowerCase().includes(needle)
    })
  }, [installed, query, state, startup])
  const inspection = useInspectionOrder(
    matching,
    (unit) => unit.name,
    JSON.stringify([query, state, startup]),
  )
  const visible = inspection.rows
  // A filter change is a new list rather than arrivals into this one.
  const listKey = [query, state, startup].join("\u0000")

  const open = (unit: SystemdUnit, tab?: string) => {
    setFocusTab(tab)
    select(unit.name)
  }

  const daemonReload = async () => {
    setReloading(true)
    try {
      await post("/systemd/daemon-reload")
      notify.success("systemd re-read its unit files")
      list.refresh()
    } catch (err) {
      notify.error("Could not reload unit files", err)
    } finally {
      setReloading(false)
    }
  }

  const header = <PageContext eyebrow="Processes" title="Services" />

  if (list.loading && !list.data) {
    return (
      <Page>
        {header}
        <LoadingPanel />
      </Page>
    )
  }
  if (list.error && !list.data) {
    return (
      <Page>
        {header}
        <ErrorState error={list.error} />
      </Page>
    )
  }
  if (!list.data?.available) {
    return (
      <Page>
        {header}
        <EmptyState icon={Servers} title="systemd is not available on this host" />
      </Page>
    )
  }

  const manager = list.data.manager
  const failed = counts.bucket.failed
  const restarting = installed.filter((u) => u.subState === "auto-restart").length
  const version = manager?.version.match(/^\d+/)?.[0]

  return (
    <Workspace
      name="Services"
      refresh={list.refresh}
      escape={() => {
        if (query) {
          setQuery("")
          return true
        }
        // The startup chips narrow within a state, so they let go first.
        if (startup) {
          setStartup("")
          return true
        }
        if (state) {
          setState("")
          return true
        }
        return false
      }}
      commands={[
        {
          id: "failed",
          label: state === "failed" ? "Show every service" : "Show failed services",
          run: () => setState(state === "failed" ? "" : "failed"),
        },
      ]}
    >
      <Page className="animate-rise" {...inspection.bindings}>
        {header}

        <HostIdentity
          mark={platformProduct(host?.platform)}
          fallback={Servers}
          title={host?.hostname ?? "Services"}
          facts={
            <>
              {/* The host's facts arrive on their own read, and an install
                  that cannot say its distribution still lists its services. */}
              {host?.platform && (
                <>
                  <HostFact product={platformProduct(host.platform)}>{platformName(host)}</HostFact>
                  <FactDot />
                </>
              )}
              {version && (
                <>
                  <span>systemd {version}</span>
                  <FactDot />
                </>
              )}
              <span className="numeric">
                {counts.bucket.active} of {plural(installed.length, "service")} active
              </span>
              <FactDot />
              <span className="numeric">{counts.boot.enabled ?? 0} start on boot</span>
              {manager?.bootedAt && (
                <>
                  <FactDot />
                  <span
                    className="numeric"
                    title={new Date(manager.bootedAt * 1000).toLocaleString()}
                  >
                    booted {ago(manager.bootedAt, now)}
                  </span>
                </>
              )}
            </>
          }
          aside={
            <div className="flex flex-wrap items-center gap-3">
              {failed > 0 ? (
                <button
                  type="button"
                  aria-pressed={state === "failed"}
                  onClick={() => setState(state === "failed" ? "" : "failed")}
                  className="rounded-md px-1.5 py-1 focus-ring transition-colors hover:bg-row-hover"
                >
                  <Status tone="danger" label={`${plural(failed, "service")} failed`} />
                </button>
              ) : restarting > 0 ? (
                <Status tone="warning" label={`${plural(restarting, "service")} restarting`} />
              ) : (
                <Status tone="running" label="Nothing failed" />
              )}
              <div className="flex items-center gap-1">
                {can("system.admin") && (
                  <Button
                    size="xs"
                    variant="ghost"
                    className="text-muted-foreground"
                    disabled={reloading}
                    title="systemctl daemon-reload: re-read every unit file after editing one"
                    onClick={() => void daemonReload()}
                  >
                    <RefreshClockwise className="size-3.5" />
                    {reloading ? "Reloading…" : "Reload unit files"}
                  </Button>
                )}
                <WorkspaceHelp compact />
              </div>
            </div>
          }
        />

        <ServiceBand
          units={installed}
          snapshot={snapshot}
          ratesReady={list.data.ratesReady ?? false}
          bootedAt={manager?.bootedAt}
          onOpen={(unit) => open(unit)}
        />

        {/* Framed, because it is a table: the grid owns a scroll region and
            the edge is what says so (§2). Everything above it stays plain. */}
        <Panel>
          <PanelHeader
            title={
              <>
                Services
                <span className="numeric ml-2 text-body font-normal text-muted-foreground">
                  {installed.length}
                </span>
              </>
            }
          >
            <ChipStrip aria-label="State" className="mr-auto">
              {STATES.map(({ value, label, tone }) => {
                const count = counts.bucket[value]
                if (tone && count === 0 && state !== value) return null
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
                            : value === "active"
                              ? "bg-success"
                              : "bg-muted-foreground/50",
                      )}
                    />
                    {value === "changing" ? changingLabel : label}
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
          </PanelHeader>
          <PanelToolbar>
            <SearchInput
              value={query}
              onChange={(event) => setQuery(event.target.value)}
              placeholder="Name or description"
              containerClassName="sm:w-64"
            />
            <ChipStrip aria-label="Startup">
              <FilterChip selected={startup === ""} onClick={() => setStartup("")}>
                Any startup <ChipCount>{installed.length}</ChipCount>
              </FilterChip>
              {STARTUP.map(({ value, label, title }) => {
                const count = counts.boot[value] ?? 0
                if (count === 0 && startup !== value) return null
                return (
                  <FilterChip
                    key={value}
                    selected={startup === value}
                    title={title}
                    onClick={() => setStartup(startup === value ? "" : value)}
                  >
                    {label} <ChipCount>{count}</ChipCount>
                  </FilterChip>
                )
              })}
            </ChipStrip>
            {state && (
              <FilterChip
                selected
                className="ml-auto"
                aria-label="Show services in every state"
                onClick={() => setState("")}
              >
                {state === "changing"
                  ? changingLabel
                  : STATES.find((s) => s.value === state)?.label}
                <Cross aria-hidden className="size-3" />
              </FilterChip>
            )}
          </PanelToolbar>
          <PanelBody flush>
            {visible.length === 0 ? (
              <EmptyState
                icon={Servers}
                title="No services match"
                description="Clear a filter or search for a different name or description."
                className="my-4"
              />
            ) : (
              <UnitRows
                key={listKey}
                rows={visible.slice(0, ROWS)}
                now={now}
                pending={pending}
                confirm={confirm}
                act={act}
                onOpen={open}
              />
            )}
          </PanelBody>
          <PanelFooter className="text-hint text-muted-foreground">
            <span className="numeric">
              {visible.length > ROWS
                ? `Showing ${ROWS} of ${visible.length} matching — search to narrow`
                : `${plural(visible.length, "service")} matching`}
            </span>
            <span className="text-muted-foreground/40">·</span>
            <span>failed first, then by name</span>
            {missing > 0 && (
              <>
                <span className="text-muted-foreground/40">·</span>
                <span
                  className="numeric"
                  title="Named by another unit, with no unit file on this host"
                >
                  {missing} referenced but not installed
                </span>
              </>
            )}
            {!list.data.ratesReady && (
              <>
                <span className="text-muted-foreground/40">·</span>
                <span>processor shares arrive with the second read</span>
              </>
            )}
          </PanelFooter>
        </Panel>

        <UnitJournalSheet
          unit={selected}
          initialTab={focusTab}
          onOpenChange={(o) => !o && select(null)}
          onChanged={list.refresh}
        />
        {dialog}
      </Page>
    </Workspace>
  )
}

type RowsProps = {
  now: number
  pending: PendingMap
  confirm: ConfirmFn
  act: Parameters<typeof useUnitVerbs>[0]["act"]
  onOpen: (unit: SystemdUnit, tab?: string) => void
}

/**
 * The rows, wide and narrow. Wide, the columns are fixed and the service
 * takes what is left, so a long description never pushes the readings or
 * the row's verbs past the panel's edge; below `xl` the same row is drawn
 * down instead of across, with nothing dropped.
 */
function UnitRows({ rows, ...rest }: RowsProps & { rows: SystemdUnit[] }) {
  // A state change updates the existing row; only a new unit arrives.
  const key = (u: SystemdUnit) => u.name
  const arrived = useArrivals(rows.map(key))
  // Against the heaviest listed, as the process table's bars are: against
  // the host, a column of 1% services is a column of empty tracks.
  const heaviest = rows.reduce((top, u) => Math.max(top, u.memoryBytes ?? 0), 0)
  return (
    <>
      <div className="hidden min-w-0 xl:block">
        <Table className="table-fixed" containerClassName="max-h-[calc(100svh-13rem)]">
          <TableHeader className={stickyTableHeader}>
            <TableRow>
              <TableHead>Service</TableHead>
              <TableHead className="w-52">State</TableHead>
              <TableHead className="w-28">Startup</TableHead>
              <TableHead className="w-32 text-right">CPU</TableHead>
              <TableHead className="w-36 text-right">Memory</TableHead>
              <TableHead className="w-32">
                <span className="sr-only">Actions</span>
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {rows.map((unit) => (
              <UnitTableRow
                key={unit.name}
                unit={unit}
                heaviest={heaviest}
                arrived={arrived.has(key(unit))}
                {...rest}
              />
            ))}
          </TableBody>
        </Table>
      </div>
      <ul className="divide-y divide-hairline px-4 xl:hidden">
        {rows.map((unit) => (
          <UnitNarrowRow key={unit.name} unit={unit} arrived={arrived.has(key(unit))} {...rest} />
        ))}
      </ul>
    </>
  )
}

/** The unit as the product it runs; one this cannot name keeps the page's glyph. */
function UnitMark({ unit }: { unit: SystemdUnit }) {
  return <ProductLogo id={unitProduct(unit.name)} size="sm" fallback={Servers} />
}

function UnitTableRow({
  unit,
  heaviest,
  arrived,
  now,
  pending,
  confirm,
  act,
  onOpen,
}: RowsProps & { unit: SystemdUnit; heaviest: number; arrived: boolean }) {
  const verbs = useUnitVerbs({ unit, confirm, act, onOpenTab: (tab) => onOpen(unit, tab) })
  return (
    <TableRow
      data-workspace-item={unit.name}
      data-workspace-name={unit.name}
      className={cn("group", arrived && "animate-rise")}
      onActivate={() => onOpen(unit)}
    >
      <TableCell className="py-2">
        <div className="flex w-full min-w-0 items-center gap-3">
          <UnitMark unit={unit} />
          <div className="min-w-0">
            <RowLink title={unit.name} onClick={() => onOpen(unit)}>
              {unit.name}
            </RowLink>
            <p className="truncate text-hint text-muted-foreground" title={unit.description}>
              {unit.description}
            </p>
          </div>
        </div>
      </TableCell>
      <TableCell className="py-2">
        <UnitState unit={unit} busy={pending[unit.name]} now={now} />
      </TableCell>
      <TableCell className="py-2">
        <StartupTag unit={unit} />
      </TableCell>
      <TableCell className="py-2">
        <CpuReading unit={unit} />
      </TableCell>
      <TableCell className="py-2">
        <MemoryReading unit={unit} heaviest={heaviest} />
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
 * hairline to the next, a wash under the pointer. The title is the real
 * button; the surrounding click is a convenience for the pointer that skips
 * any press landing on a control of its own.
 */
function UnitNarrowRow({
  unit,
  arrived,
  now,
  pending,
  confirm,
  act,
  onOpen,
}: RowsProps & { unit: SystemdUnit; arrived: boolean }) {
  const verbs = useUnitVerbs({ unit, confirm, act, onOpenTab: (tab) => onOpen(unit, tab) })
  const running = unit.cpuReady || (unit.memoryBytes ?? 0) > 0
  return (
    <li
      data-workspace-item={unit.name}
      data-workspace-name={unit.name}
      className={cn(
        "group flex min-w-0 items-start gap-3 py-3 transition-colors hover:bg-row-hover",
        ROW_BLEED,
        arrived && "animate-rise",
      )}
      onClick={(event) => {
        if ((event.target as HTMLElement).closest("a, button, [role='menuitem']")) return
        onOpen(unit)
      }}
    >
      <UnitMark unit={unit} />
      <div className="min-w-0 flex-1">
        <div className="flex min-w-0 items-baseline gap-2">
          <RowLink data-workspace-primary onClick={() => onOpen(unit)}>
            {unit.name}
          </RowLink>
          <StartupTag unit={unit} />
        </div>
        <p className="truncate text-hint text-muted-foreground">{unit.description}</p>
        <div className="mt-1.5 flex flex-wrap items-start gap-x-3 gap-y-1 text-hint">
          <UnitState unit={unit} busy={pending[unit.name]} now={now} inline />
          {running && (
            <span className="numeric font-mono text-muted-foreground">
              {unit.cpuReady ? `${percent(unit.cpuPercent ?? 0)} CPU` : "measuring"}
              {(unit.memoryBytes ?? 0) > 0 && ` · ${bytes(unit.memoryBytes)}`}
            </span>
          )}
        </div>
      </div>
      <VerbActions verbs={verbs} className="shrink-0" />
    </li>
  )
}

function StartupTag({ unit }: { unit: SystemdUnit }) {
  const state = unit.unitFileState || "unknown"
  return (
    <Tag
      tone={state === "masked" ? "warning" : "default"}
      title={STARTUP.find((s) => s.value === startupOf(unit))?.title}
    >
      {state}
    </Tag>
  )
}

/**
 * The state as a word, and under it how long the unit has been in it: up
 * for how long and after how many restarts, failed since when and why,
 * stopped or finished since when. A unit on its way somewhere, or a verb in
 * flight, shimmers, because it is happening.
 */
function UnitState({
  unit,
  busy,
  now,
  inline,
}: {
  unit: SystemdUnit
  busy?: string
  now: number
  inline?: boolean
}) {
  const bucket = unitBucket(unit)
  const tone = unitTone(unit)
  const word = unitStateWord(unit)
  const restarts = unit.restarts ?? 0
  let since: React.ReactNode = null
  if (bucket === "active" && unit.activeSince) {
    since = (
      <>
        up {duration(now / 1000 - unit.activeSince)}
        {restarts > 0 && (
          <span className={cn(restarts >= 3 && "text-warning")}>
            {" "}
            · {plural(restarts, "restart")}
          </span>
        )}
      </>
    )
  } else if (bucket === "failed") {
    since = (
      <span className="text-destructive/80">
        {failureWords(unit)}
        {unit.changedAt ? ` · ${ago(unit.changedAt, now)}` : ""}
      </span>
    )
  } else if (bucket === "changing") {
    since =
      unit.subState === "auto-restart"
        ? `${plural(restarts, "restart")} so far`
        : unit.changedAt
          ? `since ${ago(unit.changedAt, now)}`
          : null
  } else if (unit.changedAt && unit.result) {
    since = `${unit.type === "oneshot" && unit.result === "success" ? "finished" : "stopped"} ${ago(unit.changedAt, now)}`
  }

  const status = busy ? (
    <span className="inline-flex items-center gap-1.5 text-xs font-medium">
      <StatusDot tone="notice" />
      <TextShimmer>{`${busy}…`}</TextShimmer>
    </span>
  ) : (
    <span title={`${unit.activeState} (${unit.subState})`}>
      <Status
        tone={tone}
        label={bucket === "changing" ? <TextShimmer>{word}</TextShimmer> : word}
      />
    </span>
  )
  if (inline) {
    return (
      <span className="inline-flex min-w-0 items-center gap-2">
        {status}
        {since && <span className="truncate text-muted-foreground">{since}</span>}
      </span>
    )
  }
  return (
    <div className="min-w-0">
      {status}
      {since && <p className="numeric truncate text-hint text-muted-foreground">{since}</p>}
    </div>
  )
}

/**
 * A share of one core, as a figure and a short bar that fills at a whole
 * core, as the process table draws it. Nothing for a unit that is not
 * running; a dash for one not yet measured.
 */
function CpuReading({ unit }: { unit: SystemdUnit }) {
  const value = unit.cpuPercent ?? 0
  const tone = cpuTone(value)
  if (!unit.cpuReady) {
    return <p className="text-right font-mono text-muted-foreground/60">—</p>
  }
  return (
    <div className="flex items-center justify-end gap-2">
      <MiniBar
        value={Math.min(value, 100)}
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
        {percent(value)}
      </span>
    </div>
  )
}

function MemoryReading({ unit, heaviest }: { unit: SystemdUnit; heaviest: number }) {
  const memory = unit.memoryBytes ?? 0
  if (memory === 0) {
    return <p className="text-right font-mono text-muted-foreground/60">—</p>
  }
  return (
    <div
      className="flex items-center justify-end gap-2"
      title="Charged to its cgroup, page cache included"
    >
      <MiniBar value={heaviest > 0 ? (memory / heaviest) * 100 : 0} color={HUE.mem} />
      <span className="numeric w-16 text-right font-mono">{bytes(memory)}</span>
    </div>
  )
}
