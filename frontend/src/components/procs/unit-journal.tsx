"use client"

import { useMemo, useState } from "react"
import Link from "next/link"
import { ArrowUpRight, Copy, Cpu, Servers } from "@/components/icons"
import { useViewState } from "@/lib/view-state"
import type { ProcessList, ProcessRow, SystemdUnit, SystemdUnitDetail } from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { get } from "@/lib/api"
import { copyText } from "@/lib/clipboard"
import { bytes, duration, percent, plural, timestamp } from "@/lib/format"
import { journalSource } from "@/lib/log-sources"
import { cn } from "@/lib/utils"
import { useConfirm } from "@/components/confirm-dialog"
import { ShellWords } from "@/components/deploy/run-evidence"
import { useNow } from "@/components/deploy/vocabulary"
import { IconAction } from "@/components/icon-action"
import { ServiceLogs, type ServiceLogSource } from "@/components/logs/service-logs"
import { TileTrend } from "@/components/metrics/sparkline"
import { HUE, LiveBytes, LiveFigure } from "@/components/overview/readings"
import { Detail, DetailList } from "@/components/page"
import { Panel, PanelBody, PanelHeader, Well } from "@/components/panel"
import { ProductLogo, processProduct, unitProduct } from "@/components/product-logo"
import { Row, RowList } from "@/components/row-list"
import { SidePanel } from "@/components/side-panel"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { ErrorState, LoadingRows, Notice } from "@/components/state"
import { Status, StatusDot } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { VerbBar } from "@/components/verbs"
import { Button } from "@/components/ui/button"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { Counter } from "@/components/procs/process-detail"
import { useUnitControl, useUnitVerbs } from "@/components/procs/unit-actions"
import { unitRunsView } from "@/components/procs/unit-runs"
import { authUnit, cpuTone, processStateTone } from "@/components/procs/shared"
import {
  RESULT_WORDS,
  ago,
  execCommands,
  exitSentence,
  exitWords,
  failureWords,
  unitBucket,
  unitStateWord,
  unitTone,
} from "@/components/procs/units"

/**
 * An open sheet reads its unit this often. The read also measures it, so the
 * figures move at the sheet's pace and the trend under them fills in.
 */
const DETAIL_POLL = 2000

/** The unit's processes drawn before the rest fold behind a count. */
const PROCESSES_SHOWN = 8

/**
 * One unit, opened: what it is using now, how its last run ended, the
 * command it runs and its processes, how it is set up, and its journal.
 * The verbs sit in the header, and here — where `systemctl show` has said
 * whether the unit takes a reload — the reload verb is offered too.
 */
export function UnitJournalSheet(props: {
  unit: string | null
  initialTab?: string
  onOpenChange: (open: boolean) => void
  onChanged?: () => void
}) {
  // Remounted per unit and per requested tab, so "Journal" from a row lands
  // on the journal even when the sheet was already open on the overview.
  return <UnitSheet key={`${props.unit ?? "none"}:${props.initialTab ?? ""}`} {...props} />
}

function UnitSheet({
  unit,
  initialTab,
  onOpenChange,
  onChanged,
}: {
  unit: string | null
  initialTab?: string
  onOpenChange: (open: boolean) => void
  onChanged?: () => void
}) {
  const { confirm, dialog } = useConfirm()
  // Which tab a unit opens on, remembered the way a container's is; a caller
  // asking for a specific tab ("open its journal") is answered first.
  const [remembered, remember] = useViewState("processes.services.detail.tab", "overview")
  const [tab, setTabState] = useState(initialTab ?? remembered)
  const setTab = (next: string) => {
    setTabState(next)
    remember(next)
  }
  const detail = usePoll(
    (signal) =>
      get<SystemdUnitDetail>(`/systemd/${encodeURIComponent(unit ?? "")}`, undefined, signal),
    DETAIL_POLL,
    [unit],
    { enabled: unit !== null },
  )
  const { pending, act } = useUnitControl(() => {
    detail.refresh()
    onChanged?.()
  })
  const service = detail.data?.unit
  const busy = service ? pending[service.name] : undefined

  return (
    <SidePanel
      open={unit !== null}
      onOpenChange={onOpenChange}
      // The sheet opens on the unit as the product it runs, then its name.
      title={
        <>
          <ProductLogo id={unit ? unitProduct(unit) : undefined} size="sm" fallback={Servers} />
          <span className="min-w-0 truncate">{unit ?? "Unit"}</span>
        </>
      }
      description={service?.description || "Service state, configuration, journal and runs"}
      width="lg"
      actions={
        service && (
          <UnitSheetActions
            unit={service}
            canReload={detail.data?.properties.CanReload === "yes"}
            busy={busy}
            confirm={confirm}
            act={act}
          />
        )
      }
      bodyClassName="flex min-h-0 flex-1 flex-col"
    >
      {unit && (
        <Tabs value={tab} onValueChange={setTab} className="flex min-h-0 flex-1 flex-col gap-0">
          <div className="shrink-0 px-5 pt-4">
            <TabsList className="w-fit">
              <TabsTrigger value="overview">Overview</TabsTrigger>
              <TabsTrigger value="journal">Journal</TabsTrigger>
            </TabsList>
          </div>
          <TabsContent value="overview" className="min-h-0 flex-1 overflow-y-auto">
            {detail.loading && !detail.data && <LoadingRows rows={6} className="m-5" />}
            {detail.error && !detail.data && (
              <div className="p-5">
                <ErrorState error={detail.error} />
              </div>
            )}
            {detail.data && (
              <UnitOverview detail={detail.data} onJournal={() => setTab("journal")} />
            )}
          </TabsContent>
          <TabsContent value="journal" className="flex min-h-0 flex-1 flex-col p-4 pt-3">
            {/* Keyed on the unit so switching units starts a clean buffer. */}
            <UnitLogs key={unit} unit={unit} />
          </TabsContent>
        </Tabs>
      )}
      {dialog}
    </SidePanel>
  )
}

function UnitSheetActions({
  unit,
  canReload,
  busy,
  confirm,
  act,
}: {
  unit: SystemdUnit
  canReload: boolean
  busy?: string
  confirm: Parameters<typeof useUnitVerbs>[0]["confirm"]
  act: Parameters<typeof useUnitVerbs>[0]["act"]
}) {
  const verbs = useUnitVerbs({ unit, confirm, act, canReload })
  const word = unitStateWord(unit)
  return (
    <>
      {busy ? (
        <span className="inline-flex items-center gap-1.5 text-xs font-medium">
          <StatusDot tone="notice" />
          <TextShimmer>{`${busy}…`}</TextShimmer>
        </span>
      ) : (
        <span title={`${unit.activeState} (${unit.subState})`}>
          <Status
            tone={unitTone(unit)}
            label={unitBucket(unit) === "changing" ? <TextShimmer>{word}</TextShimmer> : word}
          />
        </span>
      )}
      <VerbBar verbs={verbs} />
    </>
  )
}

/** A limit property as a number, or nothing when it is unset or infinite. */
function limit(value: string | undefined): number | undefined {
  if (!value || value === "infinity") return undefined
  const n = Number(value)
  return Number.isFinite(n) && n > 0 && n < 2 ** 63 ? n : undefined
}

/**
 * The sheet's overview, one scroll: what the unit is, then — while it runs —
 * four live readings over its recent windows, or how its last run ended
 * when it does not; the command it runs, coloured as a command is; the
 * processes it holds now; and how it is set up. A failure or a restart loop
 * is said first, in words, with the way to its journal.
 */
function UnitOverview({ detail, onJournal }: { detail: SystemdUnitDetail; onJournal: () => void }) {
  const { unit: service, properties: p } = detail
  const bucket = unitBucket(service)
  const running =
    bucket === "active" && (service.mainPid || service.cpuReady || service.memoryBytes)
  // Uptime is a fact that changes while the sheet is open, so it ticks; a
  // stopped unit's "12m ago" only needs the minute.
  const now = useNow(running ? 1000 : 30_000)
  const history = service.history ?? []
  const startup = startupSummary(service.unitFileState)
  const restart = restartSummary(p.Restart)
  const restarts = service.restarts ?? 0
  const memoryMax = limit(p.MemoryMax)
  const tasksMax = limit(p.TasksMax)
  const peak = limit(p.MemoryPeak)
  const memory = service.memoryBytes ?? 0
  const memShare = memoryMax ? (memory / memoryMax) * 100 : undefined
  const tasks = service.tasks ?? 0
  const taskShare = tasksMax ? (tasks / tasksMax) * 100 : undefined
  const commands = execCommands(p.ExecStart)
  const reload = execCommands(p.ExecReload)
  const stop = execCommands(p.ExecStop)
  const dropIns = (p.DropInPaths ?? "").split(/\s+/).filter(Boolean)
  const triggers = (p.TriggeredBy ?? "").split(/\s+/).filter(Boolean)
  const cpuUsed = limit(p.CPUUsageNSec)

  return (
    <div className="flex animate-rise flex-col gap-6 px-5 pt-4 pb-6">
      {/* What this unit is, as the facts after a name — the line every
          identity in the product draws. */}
      <div className="space-y-1">
        {service.description && <p className="text-body">{service.description}</p>}
        <p className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-xs text-muted-foreground">
          <span>runs as {p.User || "root"}</span>
          <Dot />
          <span title={startup.hint}>{startup.fact}</span>
          {running && service.activeSince ? (
            <>
              <Dot />
              <span
                className="numeric"
                title={timestamp(new Date(service.activeSince * 1000).toISOString())}
              >
                up {duration(Math.max(0, now / 1000 - service.activeSince))}
              </span>
            </>
          ) : null}
          {service.mainPid ? (
            <>
              <Dot />
              <Link
                href={`/processes?pid=${service.mainPid}`}
                className="numeric inline-flex items-center gap-1 text-foreground hover:underline"
              >
                PID {service.mainPid}
                <ArrowUpRight aria-hidden className="size-3" />
              </Link>
            </>
          ) : null}
          {triggers.length > 0 && (
            <>
              <Dot />
              <span>started by {triggers.join(", ")}</span>
            </>
          )}
        </p>
      </div>

      {bucket === "failed" && (
        <Notice tone="danger" title={`Failed: ${failureWords(service)}`}>
          <FailureStory unit={service} now={now} onJournal={onJournal} />
        </Notice>
      )}
      {service.subState === "auto-restart" && (
        <Notice tone="warning" title="Restarting after it exited">
          <p>
            Its main process {exitSentence(service) ?? "ended"}
            {service.changedAt ? `, ${ago(service.changedAt, now)}` : ""}, and systemd starts it
            again{p.RestartUSec ? ` after ${p.RestartUSec}` : ""}.{" "}
            {restarts > 0 && `${plural(restarts, "automatic restart")} so far.`}
          </p>
          <JournalButton onJournal={onJournal} />
        </Notice>
      )}
      {service.unitFileState === "masked" && (
        <Notice title="Masked">
          It cannot be started — by hand, by another unit or on boot — until it is unmasked with{" "}
          <code className="font-mono">systemctl unmask {service.name}</code>.
        </Notice>
      )}

      {running ? (
        <StatGrid columns={4} dense className="-mx-5 border-y border-hairline">
          <StatTile
            className="px-5"
            label="CPU"
            tone={service.cpuReady ? cpuTone(service.cpuPercent ?? 0) : "default"}
            value={
              service.cpuReady ? (
                <LiveFigure value={service.cpuPercent ?? 0} decimals={1} unit="%" />
              ) : (
                <span className="text-muted-foreground">—</span>
              )
            }
            trailing={service.cpuReady ? "of a core" : "measuring"}
            trend={
              <TileTrend
                values={history.map((point) => point.cpu)}
                color={HUE.cpu}
                label="CPU over the last minutes"
              />
            }
            hint={
              cpuUsed ? `${duration(cpuUsed / 1e9)} of CPU this run` : "every process it started"
            }
          />
          <StatTile
            className="px-5"
            label="Memory"
            tone={
              memShare === undefined
                ? "default"
                : memShare >= 90
                  ? "danger"
                  : memShare >= 75
                    ? "warning"
                    : "default"
            }
            value={<LiveBytes value={memory} />}
            trailing={memoryMax ? `of ${bytes(memoryMax)}` : undefined}
            meter={memShare}
            meterLabel="Memory against its limit"
            trend={
              memShare === undefined ? (
                <TileTrend
                  values={history.map((point) => point.memory)}
                  color={HUE.mem}
                  label="Memory over the last minutes"
                />
              ) : undefined
            }
            hint={peak ? `peak ${bytes(peak)}` : "page cache included"}
          />
          <StatTile
            className="px-5"
            label="Tasks"
            tone={taskShare !== undefined && taskShare >= 90 ? "warning" : "default"}
            value={<LiveFigure value={tasks} />}
            trailing={tasksMax ? `of ${tasksMax}` : undefined}
            meter={taskShare}
            meterLabel="Tasks against their limit"
            hint="processes and threads"
          />
          <StatTile
            className="px-5"
            label="Restarts"
            tone={restarts >= 3 ? "warning" : "default"}
            value={<LiveFigure value={restarts} />}
            trailing="automatic"
            hint={
              restart.policy === "no"
                ? "stays down if it exits"
                : `${restart.label}${p.RestartUSec ? `, after ${p.RestartUSec}` : ""}`
            }
          />
        </StatGrid>
      ) : (
        <LastRun unit={service} properties={p} now={now} />
      )}

      {commands.length > 0 && (
        <Panel plain>
          <PanelHeader
            title="Command"
            actions={
              <IconAction
                label="Copy command line"
                onClick={() => void copyText(commands.join("\n"), "Command line copied")}
              >
                <Copy />
              </IconAction>
            }
          />
          <PanelBody className="space-y-3 pt-3">
            <Well className="max-h-40 space-y-1 text-xs leading-relaxed break-all whitespace-pre-wrap">
              {commands.map((command, i) => (
                <div key={i}>
                  <ShellWords command={command} />
                </div>
              ))}
            </Well>
            {(reload.length > 0 || stop.length > 0) && (
              <DetailList>
                {reload.length > 0 && (
                  <Detail label="Reload" className="font-mono text-xs break-all">
                    <ShellWords command={reload.join("; ")} />
                  </Detail>
                )}
                {stop.length > 0 && (
                  <Detail label="Stop" className="font-mono text-xs break-all">
                    <ShellWords command={stop.join("; ")} />
                  </Detail>
                )}
              </DetailList>
            )}
          </PanelBody>
        </Panel>
      )}

      {running && <UnitProcesses unit={service.name} />}

      <Panel plain>
        <PanelHeader title="How it runs" />
        <PanelBody className="pt-3">
          <DetailList>
            <Detail label="Runs as">
              <span className="font-mono">{p.User || "root"}</span>
              <span className="text-muted-foreground"> : {p.Group || "its default group"}</span>
            </Detail>
            <Detail label="Startup">
              <Tag tone={service.unitFileState === "masked" ? "warning" : "default"}>
                {startup.label}
              </Tag>
              <span className="ml-2 text-hint text-muted-foreground">{startup.hint}</span>
              {p.WantedBy && (
                <span className="block text-hint text-muted-foreground">
                  wanted by {p.WantedBy.split(/\s+/).join(", ")}
                </span>
              )}
            </Detail>
            {service.type && (
              <Detail label="Type">
                {service.type}
                <span className="block text-hint text-muted-foreground">
                  {TYPE_WORDS[service.type] ?? "How systemd knows it has started"}
                </span>
              </Detail>
            )}
            <Detail label="Restart policy">
              {restart.label}
              <span className="block text-hint text-muted-foreground">{restart.hint}</span>
            </Detail>
            <Detail label="Reload">
              {p.CanReload === "yes"
                ? "Re-reads its configuration without stopping"
                : "Not supported — a restart is the only way to apply changes"}
            </Detail>
            <Detail label="Working directory" className="font-mono break-all">
              {p.WorkingDirectory ? (
                <Link
                  href={`/files?path=${encodeURIComponent(p.WorkingDirectory)}`}
                  className="hover:underline"
                >
                  {p.WorkingDirectory}
                </Link>
              ) : (
                <span className="font-sans text-muted-foreground">Not set</span>
              )}
            </Detail>
            <Detail label="Limits">
              {memoryMax ? `${bytes(memoryMax)} of memory` : "No memory limit"}
              <span className="text-muted-foreground"> · </span>
              {tasksMax ? `${tasksMax} tasks` : "no task limit"}
            </Detail>
            <Detail label="Unit file" className="font-mono break-all">
              {service.fragmentPath ? (
                <FileLink path={service.fragmentPath} />
              ) : (
                <span className="font-sans text-muted-foreground">—</span>
              )}
            </Detail>
            {dropIns.length > 0 && (
              <Detail
                label={dropIns.length > 1 ? "Overrides" : "Override"}
                className="font-mono break-all"
              >
                {dropIns.map((path) => (
                  <span key={path} className="block">
                    <FileLink path={path} />
                  </span>
                ))}
              </Detail>
            )}
          </DetailList>
        </PanelBody>
      </Panel>
    </div>
  )
}

function Dot() {
  return <span className="text-muted-foreground/40">·</span>
}

function FileLink({ path }: { path: string }) {
  return (
    <Link href={`/files?path=${encodeURIComponent(path)}`} className="hover:underline">
      {path}
    </Link>
  )
}

function JournalButton({ onJournal }: { onJournal: () => void }) {
  return (
    <Button size="xs" variant="outline" className="mt-2" onClick={onJournal}>
      Read its journal
    </Button>
  )
}

/**
 * What a failure means, in the order it is read: how the process ended and
 * when, what systemd did about it, and what will get it going again.
 */
function FailureStory({
  unit,
  now,
  onJournal,
}: {
  unit: SystemdUnit
  now: number
  onJournal: () => void
}) {
  const exit = exitSentence(unit)
  const restarts = unit.restarts ?? 0
  const when = unit.changedAt ? `, ${ago(unit.changedAt, now)}` : ""
  let next: string
  if (unit.result === "start-limit-hit") {
    next = `It was restarted ${plural(restarts, "time")} in quick succession, so systemd stopped trying. Fix the cause, then clear the failed state or restart it.`
  } else if (unit.result === "oom-kill") {
    next =
      "The kernel ended it for memory. Raise its limit or the host's, or find what it is holding."
  } else if (restarts > 0) {
    next = `systemd had restarted it ${plural(restarts, "time")} before this.`
  } else {
    next = "It stays down until it is started again."
  }
  return (
    <>
      <p>
        {exit ? `Its main process ${exit}` : "It failed"}
        {when}. {next}
      </p>
      <JournalButton onJournal={onJournal} />
    </>
  )
}

/**
 * The last run of a unit that is not running: when it ended and how, what
 * it cost. Nothing for a unit that has not run since boot.
 */
function LastRun({
  unit,
  properties: p,
  now,
}: {
  unit: SystemdUnit
  properties: Record<string, string>
  now: number
}) {
  const exit = exitWords(unit)
  const cpu = limit(p.CPUUsageNSec)
  const peak = limit(p.MemoryPeak)
  if (!unit.changedAt && !exit && !cpu) return null
  const outcome =
    unitBucket(unit) === "failed"
      ? failureWords(unit)
      : unit.result && unit.result !== "success"
        ? (RESULT_WORDS[unit.result] ?? unit.result)
        : unit.type === "oneshot"
          ? "finished"
          : "stopped"
  return (
    <Panel plain>
      <PanelHeader
        title="Last run"
        actions={
          unit.changedAt ? (
            <span className="numeric text-hint text-muted-foreground">
              {outcome} {ago(unit.changedAt, now)}
            </span>
          ) : undefined
        }
      />
      <PanelBody className="pt-3">
        <dl className="grid grid-cols-2 gap-x-6 gap-y-1.5 sm:grid-cols-3">
          {unit.changedAt ? (
            <Counter label="Ended">
              {timestamp(new Date(unit.changedAt * 1000).toISOString())}
            </Counter>
          ) : null}
          <Counter label="Exit">{exit ?? "—"}</Counter>
          <Counter label="Result">{unit.result || "—"}</Counter>
          <Counter label="CPU used">{cpu ? duration(cpu / 1e9) : "—"}</Counter>
          <Counter label="Peak memory">{peak ? bytes(peak) : "—"}</Counter>
          <Counter label="Automatic restarts">{unit.restarts ?? 0}</Counter>
        </dl>
      </PanelBody>
    </Panel>
  )
}

/**
 * The processes the unit holds now, from the process table narrowed to its
 * cgroup, heaviest first: a worker pool's spread, the child a service forked
 * that is the one spinning. Each opens in the Live page's sheet, where its
 * signals and priority are.
 */
function UnitProcesses({ unit }: { unit: string }) {
  const [all, setAll] = useState(false)
  const list = usePoll(
    (signal) =>
      get<ProcessList>(
        "/processes/inventory",
        { group: `systemd:${unit}`, sort: "cpu", limit: 50 },
        signal,
      ),
    4000,
    [unit],
  )
  const rows = useMemo(() => list.data?.processes ?? [], [list.data])
  const shown = all ? rows : rows.slice(0, PROCESSES_SHOWN)
  if (list.data && rows.length === 0) return null
  return (
    <Panel plain>
      <PanelHeader
        title="Processes"
        actions={
          list.data && (
            <span className="numeric text-hint text-muted-foreground">
              {plural(list.data.total, "process", "processes")}
            </span>
          )
        }
      />
      <PanelBody className="space-y-2 pt-3">
        {list.loading && !list.data && <LoadingRows rows={2} />}
        {list.error && !list.data && <ErrorState error={list.error} />}
        {rows.length > 0 && (
          <RowList>
            {shown.map((process) => (
              <UnitProcessRow
                key={process.pid}
                process={process}
                ratesReady={list.data?.ratesReady ?? false}
              />
            ))}
          </RowList>
        )}
        {rows.length > PROCESSES_SHOWN && (
          <Button size="xs" variant="ghost" onClick={() => setAll((v) => !v)}>
            {all ? "Show fewer" : `Show all ${rows.length}`}
          </Button>
        )}
      </PanelBody>
    </Panel>
  )
}

function UnitProcessRow({ process, ratesReady }: { process: ProcessRow; ratesReady: boolean }) {
  return (
    <Row
      href={`/processes?pid=${process.pid}`}
      mono
      leading={<ProductLogo id={processProduct(process.name)} size="sm" fallback={Cpu} />}
      title={
        <span className="flex min-w-0 items-center gap-2">
          <span className="truncate">{process.name}</span>
          <span className="numeric font-mono text-hint font-normal text-muted-foreground">
            {process.pid}
          </span>
          {process.username && (
            <span className="truncate text-hint font-normal text-muted-foreground">
              {process.username}
            </span>
          )}
        </span>
      }
      subtitle={process.cmdline || "command line not readable"}
      trailing={
        <>
          <span
            className={cn(
              "numeric font-mono text-hint",
              cpuTone(process.cpuPercent) === "default" ? "text-muted-foreground" : "text-warning",
            )}
          >
            {!ratesReady || process.cpuReady === false ? "—" : percent(process.cpuPercent)}
          </span>
          <span className="numeric font-mono text-hint text-muted-foreground">
            {bytes(process.rss)}
          </span>
          <Status state={processStateTone(process.state)} label={process.state} />
        </>
      }
    />
  )
}

/** When systemd counts each kind of service as started. */
const TYPE_WORDS: Record<string, string> = {
  simple: "Started as soon as its process is",
  exec: "Started once its program has been executed",
  forking: "Started when its process forks and the parent exits",
  notify: "Started when it tells systemd it is ready",
  "notify-reload": "Started when it says it is ready; reloads by signal",
  oneshot: "Runs to completion, and is started once it has",
  dbus: "Started when it takes its name on D-Bus",
  idle: "Started once every other job is done",
}

/** What the startup state means for the next boot, in words. */
function startupSummary(state: string | undefined): { label: string; fact: string; hint: string } {
  switch (state) {
    case "enabled":
    case "enabled-runtime":
      return { label: state, fact: "starts on boot", hint: "Starts automatically on boot" }
    case "disabled":
      return { label: state, fact: "started by hand", hint: "Only runs when started by hand" }
    case "static":
      return {
        label: state,
        fact: "started by another unit",
        hint: "Started by another unit, not on its own",
      }
    case "masked":
      return { label: state, fact: "masked", hint: "Blocked from starting at all" }
    default:
      return {
        label: state || "unknown",
        fact: "startup unknown",
        hint: "Startup state was not reported",
      }
  }
}

/** What the restart policy does the next time the process exits. */
function restartSummary(policy: string | undefined): {
  policy: string
  label: string
  hint: string
} {
  const value = (policy || "no").toLowerCase()
  switch (value) {
    case "always":
      return { policy: value, label: "always", hint: "Restarts after every exit" }
    case "on-failure":
      return { policy: value, label: "on failure", hint: "Restarts after crashes, not clean exits" }
    case "on-abnormal":
    case "on-abort":
    case "on-watchdog":
      return { policy: value, label: value, hint: "Restarts after abnormal exits only" }
    default:
      return { policy: "no", label: "never", hint: "Stays stopped until started again" }
  }
}

/**
 * The unit's journal, read the way the logs page reads it — live, searched,
 * added up through the unit's lens — and its runs beside that: when systemd
 * started it, how long each lasted and how it ended. One unit only; the
 * whole journal is the logs page's.
 *
 * sshd's journal is login records, which only an administrator reads. For
 * anyone else it opens nothing: the server refuses the read before the
 * socket upgrades, and a pane retrying a refusal while it says the tunnel
 * dropped is wrong twice.
 */
function UnitLogs({ unit }: { unit: string }) {
  const { can } = useAuth()
  const sources = useMemo<ServiceLogSource[]>(
    () => [{ id: journalSource(unit), label: unit, kind: "journal", product: unitProduct(unit) }],
    [unit],
  )
  const views = useMemo(() => [unitRunsView(unit)], [unit])
  if (authUnit(unit) && !can("system.admin")) {
    return (
      <Notice title="Login records need an administrator">
        {unit}&apos;s journal holds every sign-in attempt, and a failed one can hold a password
        typed into the username prompt, so only an administrator can read it.
      </Notice>
    )
  }
  return (
    <ServiceLogs
      sources={sources}
      views={views}
      layout="sheet"
      className="min-h-0 flex-1"
      paneClassName="min-h-80"
    />
  )
}
