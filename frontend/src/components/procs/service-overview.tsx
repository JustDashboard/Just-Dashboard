"use client"

import Link from "next/link"
import {
  Clock,
  Connection,
  Copy,
  Cpu,
  Crosshair,
  FolderOpen,
  Globe,
  GridSquare,
  Layers,
  Logs,
  Servers,
  Warning,
  type Icon,
} from "@/components/icons"
import { copyText } from "@/lib/clipboard"
import { bytes, duration, percent, plural, timestamp } from "@/lib/format"
import type {
  ProcessList,
  ProcessRow,
  SystemdRelated,
  SystemdRelatedUnit,
  SystemdUnit,
} from "@/lib/types"
import { cn } from "@/lib/utils"
import { ShellWords } from "@/components/deploy/run-evidence"
import { useNow } from "@/components/deploy/vocabulary"
import { WireMark, WireNode, WirePlaceholder } from "@/components/deploy/wire"
import { IconAction } from "@/components/icon-action"
import { TileTrend } from "@/components/metrics/sparkline"
import { HUE, LiveBytes, LiveFigure, SeriesKey } from "@/components/overview/readings"
import { Detail, DetailList, Section } from "@/components/page"
import { Well } from "@/components/panel"
import {
  ProductGlyph,
  hasProductLogo,
  processProduct,
  unitProduct,
} from "@/components/product-logo"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { Notice } from "@/components/state"
import { StatusDot, type DotTone } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { Counter } from "@/components/procs/process-detail"
import {
  counter,
  limit,
  relatedHref,
  restartSummary,
  serviceWarnings,
  startupSummary,
  TYPE_WORDS,
  unitKind,
  words,
} from "@/components/procs/service-facts"
import { cpuTone, processStateTone, reach } from "@/components/procs/shared"
import {
  RESULT_WORDS,
  ago,
  execCommands,
  exitSentence,
  exitWords,
  failureWords,
  signalName,
  unitBucket,
  unitStateWord,
  unitTone,
} from "@/components/procs/units"
import { WiringPicture, type PictureNode } from "@/components/procs/wiring-picture"

/** Units drawn on the picture's left before the rest fold into a count. */
const BEFORE_DRAWN = 4

/** Below this a process is idle and its line is still. */
const BUSY = 1

export type ServiceOverviewProps = {
  unit: SystemdUnit
  properties: Record<string, string>
  processes?: ProcessList
  /** The main process's own reading, with its sockets. */
  main?: ProcessRow
  related?: SystemdRelated
  onTab: (tab: string) => void
  onDaemonReload?: () => void
}

/**
 * The unit's Overview, one scroll in the order it is asked about: what is
 * wrong, what it is using now (or how its last run ended), where it sits —
 * what starts it and what it needs on the left, what it runs and listens on
 * on the right, as one wired picture — the commands it runs, how it is run,
 * and what it has used since it started.
 */
export function ServiceOverview(props: ServiceOverviewProps) {
  const { unit, properties: p } = props
  const bucket = unitBucket(unit)
  const running = bucket === "active" && Boolean(unit.mainPid || unit.cpuReady || unit.memoryBytes)
  return (
    <div className="flex min-w-0 flex-col gap-8 pb-6">
      <ServiceNotices {...props} />
      {running ? (
        <ServiceReadings unit={unit} properties={p} />
      ) : (
        <LastRun unit={unit} properties={p} />
      )}
      <ServicePicture {...props} />
      <CommandBlock properties={p} />
      {/* A unit that is not running has its last run above, with what it used. */}
      <div className={cn("grid min-w-0 gap-8 [&>*]:min-w-0", running && "xl:grid-cols-2")}>
        <HowItRuns unit={unit} properties={p} onTab={props.onTab} />
        {running && <WhatItUsed unit={unit} properties={p} />}
      </div>
    </div>
  )
}

function ServiceNotices({ unit, properties: p, onTab, onDaemonReload }: ServiceOverviewProps) {
  const now = useNow(30_000)
  const restarts = unit.restarts ?? 0
  const notices: React.ReactNode[] = []
  if (unitBucket(unit) === "failed") {
    notices.push(
      <Notice key="failed" tone="danger" title={`Failed: ${failureWords(unit)}`}>
        <FailureStory unit={unit} now={now} />
        <Button size="xs" variant="outline" className="mt-2" onClick={() => onTab("journal")}>
          Read its journal
        </Button>
      </Notice>,
    )
  }
  if (unit.subState === "auto-restart") {
    notices.push(
      <Notice key="restarting" tone="warning" title="Restarting after it exited">
        <p>
          Its main process {exitSentence(unit) ?? "ended"}
          {unit.changedAt ? `, ${ago(unit.changedAt, now)}` : ""}, and systemd starts it again
          {p.RestartUSec ? ` after ${p.RestartUSec}` : ""}.{" "}
          {restarts > 0 && `${plural(restarts, "automatic restart")} so far.`}
        </p>
        <Button size="xs" variant="outline" className="mt-2" onClick={() => onTab("journal")}>
          Read its journal
        </Button>
      </Notice>,
    )
  }
  if (unit.unitFileState === "masked") {
    notices.push(
      <Notice key="masked" title="Masked">
        It cannot be started — by hand, by another unit or on boot — until it is unmasked, from
        Control or with <code className="font-mono">systemctl unmask {unit.name}</code>.
      </Notice>,
    )
  }
  for (const warning of serviceWarnings(unit, p)) {
    notices.push(
      <Notice key={warning.key} tone={warning.tone} title={warning.title}>
        <p>{warning.body}</p>
        {warning.key === "reload" && onDaemonReload && (
          <Button size="xs" variant="outline" className="mt-2" onClick={onDaemonReload}>
            Reload unit files
          </Button>
        )}
        {(warning.key === "memory" || warning.key === "frozen") && (
          <Button size="xs" variant="outline" className="mt-2" onClick={() => onTab("control")}>
            Open Control
          </Button>
        )}
      </Notice>,
    )
  }
  if (notices.length === 0) return null
  return <div className="grid min-w-0 gap-3 lg:grid-cols-2">{notices}</div>
}

/**
 * What a failure means, in the order it is read: how the process ended and
 * when, what systemd did about it, and what will get it going again.
 */
function FailureStory({ unit, now }: { unit: SystemdUnit; now: number }) {
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
    <p>
      {exit ? `Its main process ${exit}` : "It failed"}
      {when}. {next}
    </p>
  )
}

/** Four live readings over the unit's recent windows, from its cgroup. */
function ServiceReadings({
  unit,
  properties: p,
}: {
  unit: SystemdUnit
  properties: Record<string, string>
}) {
  const history = unit.history ?? []
  const restart = restartSummary(p.Restart)
  const restarts = unit.restarts ?? 0
  const memoryMax = limit(p.MemoryMax)
  const tasksMax = limit(p.TasksMax)
  const peak = limit(p.MemoryPeak)
  const memory = unit.memoryBytes ?? 0
  const memShare = memoryMax ? (memory / memoryMax) * 100 : undefined
  const tasks = unit.tasks ?? 0
  const taskShare = tasksMax ? (tasks / tasksMax) * 100 : undefined
  const cpuUsed = limit(p.CPUUsageNSec)
  return (
    <StatGrid columns={4} dense data-testid="service-readings">
      <StatTile
        label={
          <>
            <SeriesKey color={HUE.cpu} />
            CPU
          </>
        }
        tone={unit.cpuReady ? cpuTone(unit.cpuPercent ?? 0) : "default"}
        value={
          unit.cpuReady ? (
            <LiveFigure value={unit.cpuPercent ?? 0} decimals={1} unit="%" />
          ) : (
            <span className="text-muted-foreground">—</span>
          )
        }
        trailing={unit.cpuReady ? "of a core" : "measuring"}
        trend={
          <TileTrend
            values={history.map((point) => point.cpu)}
            color={HUE.cpu}
            label="CPU over the last minutes"
          />
        }
        hint={cpuUsed ? `${duration(cpuUsed / 1e9)} of CPU this run` : "every process it started"}
      />
      <StatTile
        label={
          <>
            <SeriesKey color={HUE.mem} />
            Memory
          </>
        }
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
        label="Tasks"
        tone={taskShare !== undefined && taskShare >= 90 ? "warning" : "default"}
        value={<LiveFigure value={tasks} />}
        trailing={tasksMax ? `of ${tasksMax}` : undefined}
        meter={taskShare}
        meterLabel="Tasks against their limit"
        hint="processes and threads"
      />
      <StatTile
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
  )
}

/**
 * The last run of a unit that is not running: when it ended and how, what
 * it cost. Nothing for a unit that has not run since boot.
 */
function LastRun({
  unit,
  properties: p,
}: {
  unit: SystemdUnit
  properties: Record<string, string>
}) {
  const now = useNow(30_000)
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
    <Section
      title="Last run"
      actions={
        unit.changedAt ? (
          <span className="numeric text-hint text-muted-foreground">
            {outcome} {ago(unit.changedAt, now)}
          </span>
        ) : undefined
      }
    >
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
    </Section>
  )
}

/** A unit kind's glyph where it runs no product of its own. */
const KIND_ICONS: Partial<Record<ReturnType<typeof unitKind>, Icon>> = {
  timer: Clock,
  socket: Connection,
  path: FolderOpen,
  target: Crosshair,
  slice: GridSquare,
  mount: Layers,
  automount: Layers,
}

/** A related unit's tone from its state, as its row in the Services table would draw it. */
export function relatedTone(unit?: SystemdRelatedUnit): DotTone {
  if (!unit) return "stopped"
  if (unit.loadState === "not-found") return "warning"
  return unitTone({ activeState: unit.activeState })
}

/** The words a related unit's state is read in: a timer waits, a socket listens. */
export function relatedState(unit?: SystemdRelatedUnit, read = false): string {
  if (!unit) return read ? "" : "reading…"
  if (unit.loadState === "not-found") return "not installed"
  if (unit.activeState === "active" && unit.subState && unit.subState !== "running") {
    return unit.subState === "exited" ? "done" : unit.subState
  }
  return unitStateWord(unit)
}

/** A unit as a mark: the product it runs, or its kind's glyph, with its state in the corner. */
export function UnitMark({
  name,
  state,
  size = "md",
}: {
  name: string
  state?: SystemdRelatedUnit
  size?: "sm" | "md" | "lg"
}) {
  const product = unitKind(name) === "service" ? unitProduct(name) : undefined
  const Glyph = KIND_ICONS[unitKind(name)] ?? Servers
  return (
    <span className="relative flex">
      <WireMark tone="logo" size={size}>
        {hasProductLogo(product) ? <ProductGlyph id={product} /> : <Glyph aria-hidden />}
      </WireMark>
      <span
        aria-hidden
        className="absolute -right-0.5 -bottom-0.5 flex size-3.5 items-center justify-center rounded-full bg-background"
      >
        <StatusDot tone={relatedTone(state)} />
      </span>
    </span>
  )
}

/** A process's programs, the copies of one program as one group, busiest first. */
function programs(rows: ProcessRow[]): ProcessRow[][] {
  const groups = new Map<string, ProcessRow[]>()
  for (const row of rows) {
    const group = groups.get(row.name)
    if (group) group.push(row)
    else groups.set(row.name, [row])
  }
  return [...groups.values()]
}

/**
 * Where the unit sits, drawn as the process page draws a process: on the
 * left what starts it — a timer, a socket, the target that wants it on boot
 * — and the units it needs; the unit in the middle as the product it runs,
 * with its state in the mark's corner; on the right its main process, the
 * rest of its processes by program, what it listens on and where it writes.
 *
 * A needed unit that is down is drawn in the danger hue, because that is
 * where a unit that will not start is usually fixed. A process's line pulses
 * while it is busy, a port's while connections are open on it. Every node
 * opens what it is: a unit its page or its timer, a process its page, the
 * rest the view on this page that holds them.
 */
function ServicePicture({
  unit,
  properties: p,
  processes,
  main,
  related,
  onTab,
}: ServiceOverviewProps) {
  const states = new Map((related?.units ?? []).map((u) => [u.name, u]))
  const live = unitBucket(unit) === "active" || unitBucket(unit) === "changing"
  const failed = unitBucket(unit) === "failed"

  const before: PictureNode[] = []
  const add = (name: string, eyebrow: string, needed: boolean) => {
    if (before.some((node) => node.id === name)) return
    const state = states.get(name)
    const down =
      needed && state && state.activeState !== "active" && state.activeState !== "activating"
    before.push({
      id: name,
      mark: <UnitMark name={name} state={state} />,
      eyebrow,
      title: <span className="block truncate">{name}</span>,
      hint: (
        <span className={cn(down && "text-destructive")}>
          {[
            relatedState(state, Boolean(related)),
            state?.description !== name && state?.description,
          ]
            .filter(Boolean)
            .join(" · ")}
        </span>
      ),
      href: relatedHref(name),
      label: `Open ${name}`,
      wire: {
        tone: down ? "danger" : "default",
        dashed: !live || Boolean(down),
        carries: false,
      },
    })
  }
  for (const name of words(p.TriggeredBy)) {
    const kind = unitKind(name)
    add(
      name,
      kind === "timer"
        ? "Started by a timer"
        : kind === "socket"
          ? "Started by a socket"
          : "Started by",
      false,
    )
  }
  // A needed unit that is down first: it is the likeliest reason this one is.
  const needs = [...words(p.Requires), ...words(p.BindsTo), ...words(p.Requisite)].filter(
    (name) => !name.endsWith(".slice") && !name.endsWith(".mount"),
  )
  const down = (name: string) => {
    const state = states.get(name)
    return state ? state.activeState !== "active" : false
  }
  for (const name of [...needs.filter(down), ...needs.filter((n) => !down(n))])
    add(name, "Needs", true)
  for (const name of words(p.WantedBy)) add(name, "Wanted by", false)
  for (const name of words(p.RequiredBy)) add(name, "Required by", false)
  const left = before.slice(0, BEFORE_DRAWN)
  if (before.length > BEFORE_DRAWN) {
    left.push({
      id: "more-before",
      mark: (
        <WirePlaceholder size="md">
          <Servers />
        </WirePlaceholder>
      ),
      eyebrow: "And",
      title: plural(before.length - BEFORE_DRAWN, "more unit"),
      hint: "Listed under Dependencies",
      open: () => onTab("dependencies"),
      label: "Show every dependency",
      wire: { tone: "default", dashed: true, carries: false },
    })
  }

  const right: PictureNode[] = []
  const rows = processes?.processes ?? []
  const mainRow = rows.find((row) => row.pid === unit.mainPid) ?? main
  if (unit.mainPid && mainRow) {
    const product = processProduct(mainRow.name)
    right.push({
      id: `pid:${mainRow.pid}`,
      mark: (
        <span className="relative flex">
          <WireMark tone="logo" size="md">
            {hasProductLogo(product) ? <ProductGlyph id={product} /> : <Cpu aria-hidden />}
          </WireMark>
          <span
            aria-hidden
            className="absolute -right-0.5 -bottom-0.5 flex size-3.5 items-center justify-center rounded-full bg-background"
          >
            <StatusDot state={processStateTone(mainRow.state)} />
          </span>
        </span>
      ),
      eyebrow: "Main process",
      title: <span className="block truncate">{mainRow.name}</span>,
      hint: (
        <span className="numeric">
          PID {mainRow.pid} ·{" "}
          <span className={cn(cpuTone(mainRow.cpuPercent) !== "default" && "text-warning")}>
            {mainRow.cpuReady === false ? "measuring" : `${percent(mainRow.cpuPercent)} CPU`}
          </span>{" "}
          · {bytes(mainRow.rss)}
        </span>
      ),
      href: `/processes/${mainRow.pid}`,
      label: `Open its main process, ${mainRow.name} (${mainRow.pid})`,
      wire: { tone: "default", dashed: !live, carries: live && mainRow.cpuPercent >= BUSY },
    })
  }
  const rest = rows.filter((row) => row.pid !== unit.mainPid)
  for (const group of programs(rest).slice(0, 2)) {
    const first = group[0]
    const cpu = group.reduce((n, row) => n + (row.cpuReady === false ? 0 : row.cpuPercent), 0)
    const rss = group.reduce((n, row) => n + row.rss, 0)
    const product = processProduct(first.name)
    right.push({
      id: `group:${first.name}`,
      mark: (
        <WireMark tone="logo" size="md">
          {hasProductLogo(product) ? <ProductGlyph id={product} /> : <Cpu aria-hidden />}
        </WireMark>
      ),
      eyebrow: group.length > 1 ? "Its processes" : "Its process",
      title: (
        <span className="block truncate">
          {first.name}
          {group.length > 1 && (
            <span className="numeric font-normal text-muted-foreground"> × {group.length}</span>
          )}
        </span>
      ),
      hint: (
        <span className="numeric">
          {group.length === 1 ? `PID ${first.pid} · ` : ""}
          <span className={cn(cpuTone(cpu) !== "default" && "text-warning")}>
            {percent(cpu)} CPU
          </span>{" "}
          · {bytes(rss)}
        </span>
      ),
      ...(group.length === 1
        ? { href: `/processes/${first.pid}`, label: `Open ${first.name} (${first.pid})` }
        : {
            open: () => onTab("processes"),
            label: `Show its ${group.length} ${first.name} processes`,
          }),
      wire: { tone: "default", dashed: !live, carries: live && cpu >= BUSY },
    })
  }
  const others = programs(rest)
    .slice(2)
    .reduce((n, group) => n + group.length, 0)
  if (others > 0) {
    right.push({
      id: "more-processes",
      mark: (
        <WirePlaceholder size="md">
          <Cpu />
        </WirePlaceholder>
      ),
      eyebrow: "And",
      title: plural(others, "more process", "more processes"),
      hint: "Listed under Processes",
      open: () => onTab("processes"),
      label: "Show every process it runs",
      wire: { tone: "default", dashed: true, carries: false },
    })
  }
  const listening = main?.listening ?? []
  const connections = main?.connections ?? 0
  if (live && listening.length > 0 && main) {
    const ports = [...new Set(listening.map((port) => port.port))].sort((a, b) => a - b)
    const protos = [...new Set(listening.map((port) => port.proto.replace(/6$/, "").toUpperCase()))]
    const open = listening.some((port) => reach(port.address) === "every interface")
    const local = listening.every((port) => reach(port.address) === "this machine only")
    right.push({
      id: "ports",
      mark: (
        <WireMark tone={open ? "warning" : "logo"} size="md">
          {open ? <Warning aria-hidden /> : <Globe aria-hidden />}
        </WireMark>
      ),
      eyebrow: `Listens · ${protos.join(" and ")}`,
      title: (
        <span className="block truncate font-mono">
          {ports
            .slice(0, 4)
            .map((port) => `:${port}`)
            .join(" ")}
          {ports.length > 4 && (
            <span className="font-sans text-muted-foreground"> +{ports.length - 4}</span>
          )}
        </span>
      ),
      hint: `${open ? "every interface" : local ? "this machine only" : "one address"} · ${plural(connections, "open connection")}`,
      href: `/processes/${main.pid}?tab=network`,
      label: `Show what it listens on and its ${plural(connections, "connection")}`,
      wire: { tone: open ? "warning" : "default", dashed: !live, carries: live && connections > 0 },
    })
  }
  const output = p.StandardOutput || "journal"
  const toFile = /^(append|file|truncate):/.exec(output)
  right.push({
    id: "output",
    mark: (
      <WireMark tone="logo" size="md">
        <Logs aria-hidden />
      </WireMark>
    ),
    eyebrow: "Writes to",
    title: (
      <span className="block truncate">
        {toFile ? (
          <span className="font-mono">{output.slice(toFile[0].length)}</span>
        ) : output.startsWith("journal") || output === "inherit" ? (
          "The journal"
        ) : (
          output
        )}
      </span>
    ),
    hint: p.SyslogIdentifier
      ? `as ${p.SyslogIdentifier}`
      : "its output and systemd's lines about it",
    open: () => onTab("journal"),
    label: "Read its journal",
    wire: { tone: failed ? "danger" : "default", dashed: !live, carries: false },
  })

  const cpu = unit.cpuReady ? `${percent(unit.cpuPercent ?? 0)} CPU` : null
  return (
    <WiringPicture
      title="Where it runs"
      left={{
        title: "Before it",
        nodes: left,
        empty: (
          <WireNode
            align="end"
            mark={
              <WirePlaceholder size="md">
                <Servers />
              </WirePlaceholder>
            }
            title={<span className="text-muted-foreground">Started by hand</span>}
            hint="No timer, socket or target starts it"
          />
        ),
      }}
      right={{
        title: "What it runs",
        nodes: right,
        empty: null,
      }}
      center={{
        mark: <UnitMark name={unit.name} state={unit} size="lg" />,
        eyebrow: `${unit.type || p.Type || "a"} service`,
        title: <span className="block truncate">{unit.name}</span>,
        hint: (
          <span className="numeric">
            {live
              ? [cpu, (unit.memoryBytes ?? 0) > 0 ? bytes(unit.memoryBytes) : null]
                  .filter(Boolean)
                  .join(" · ") || unitStateWord(unit)
              : unitStateWord(unit)}
          </span>
        ),
      }}
    />
  )
}

/** Every command systemd runs for it, in the order it runs them, coloured as commands are. */
function CommandBlock({ properties: p }: { properties: Record<string, string> }) {
  const steps = [
    { key: "ExecCondition", label: "Checks first" },
    { key: "ExecStartPre", label: "Before it starts" },
    { key: "ExecStart", label: "Starts" },
    { key: "ExecStartPost", label: "After it starts" },
    { key: "ExecReload", label: "Reloads with" },
    { key: "ExecStop", label: "Stops with" },
    { key: "ExecStopPost", label: "After it stops" },
  ]
    .map((step) => ({ ...step, commands: execCommands(p[step.key]) }))
    .filter((step) => step.commands.length > 0)
  const start = steps.find((step) => step.key === "ExecStart")
  if (!start) return null
  return (
    <Section
      title="Command"
      actions={
        <IconAction
          label="Copy command line"
          onClick={() => void copyText(start.commands.join("\n"), "Command line copied")}
        >
          <Copy />
        </IconAction>
      }
    >
      <div className="space-y-3" data-testid="service-command">
        <Well className="max-h-40 space-y-1 text-xs leading-relaxed break-all whitespace-pre-wrap">
          {start.commands.map((command, i) => (
            <div key={i}>
              <ShellWords command={command} />
            </div>
          ))}
        </Well>
        {steps.length > 1 && (
          <DetailList>
            {steps
              .filter((step) => step !== start)
              .map((step) => (
                <Detail key={step.key} label={step.label} className="font-mono text-xs break-all">
                  {step.commands.map((command, i) => (
                    <span key={i} className="block">
                      <ShellWords command={command} />
                    </span>
                  ))}
                </Detail>
              ))}
          </DetailList>
        )}
      </div>
    </Section>
  )
}

/** A KillSignal property, a number, as its name. */
function signalOf(value: string | undefined): string | undefined {
  const n = Number(value)
  return Number.isFinite(n) && n > 0 ? signalName(n) : undefined
}

function HowItRuns({
  unit,
  properties: p,
  onTab,
}: {
  unit: SystemdUnit
  properties: Record<string, string>
  onTab: (tab: string) => void
}) {
  const startup = startupSummary(unit.unitFileState)
  const restart = restartSummary(p.Restart)
  const dropIns = words(p.DropInPaths)
  const groups = words(p.SupplementaryGroups)
  const kill = signalOf(p.KillSignal)
  return (
    <Section title="How it runs">
      <DetailList>
        <Detail label="Runs as">
          <span className="font-mono">
            {p.User || (p.DynamicUser === "yes" ? "a dynamic user" : "root")}
          </span>
          <span className="text-muted-foreground"> : {p.Group || "its default group"}</span>
          {groups.length > 0 && (
            <span className="block text-hint text-muted-foreground">
              also in {groups.join(", ")}
            </span>
          )}
        </Detail>
        <Detail label="Startup">
          {startup.label}
          <span className="block text-hint text-muted-foreground">
            {startup.hint}
            {p.WantedBy ? ` · wanted by ${words(p.WantedBy).join(", ")}` : ""}
          </span>
        </Detail>
        {(unit.type || p.Type) && (
          <Detail label="Type">
            {unit.type || p.Type}
            <span className="block text-hint text-muted-foreground">
              {TYPE_WORDS[unit.type || p.Type] ?? "How systemd knows it has started"}
            </span>
          </Detail>
        )}
        <Detail label="Restart policy">
          {restart.label}
          {p.RestartUSec && restart.policy !== "no" ? `, after ${p.RestartUSec}` : ""}
          <span className="block text-hint text-muted-foreground">
            {restart.hint}
            {p.StartLimitBurst && p.StartLimitIntervalUSec
              ? ` · gives up after ${p.StartLimitBurst} starts in ${p.StartLimitIntervalUSec}`
              : ""}
          </span>
        </Detail>
        <Detail label="Reload">
          {p.CanReload === "yes"
            ? "Re-reads its configuration without stopping"
            : "Not supported — a restart is the only way to apply changes"}
        </Detail>
        {(p.TimeoutStartUSec || p.TimeoutStopUSec) && (
          <Detail label="Timeouts">
            {p.TimeoutStartUSec ? `${p.TimeoutStartUSec} to start` : ""}
            {p.TimeoutStartUSec && p.TimeoutStopUSec ? " · " : ""}
            {p.TimeoutStopUSec ? `${p.TimeoutStopUSec} to stop` : ""}
          </Detail>
        )}
        {(p.KillMode || kill) && (
          <Detail label="Stopped with">
            {kill ?? "SIGTERM"}
            {p.KillMode ? (
              <span className="text-muted-foreground">
                {" "}
                to{" "}
                {p.KillMode === "control-group"
                  ? "every process in it"
                  : p.KillMode === "mixed"
                    ? "the main process, then the rest"
                    : p.KillMode === "process"
                      ? "the main process only"
                      : "nothing (KillMode=none)"}
              </span>
            ) : null}
          </Detail>
        )}
        {p.WatchdogUSec && p.WatchdogUSec !== "0" && (
          <Detail label="Watchdog">must check in every {p.WatchdogUSec}</Detail>
        )}
        <Detail label="Working directory" className="font-mono break-all">
          {p.WorkingDirectory ? (
            <Link
              href={`/files?path=${encodeURIComponent(p.WorkingDirectory.replace(/^[-!]/, ""))}`}
              className="hover:underline"
            >
              {p.WorkingDirectory}
            </Link>
          ) : (
            <span className="font-sans text-muted-foreground">Not set</span>
          )}
        </Detail>
        {p.RootDirectory && (
          <Detail label="Root directory" className="font-mono break-all">
            {p.RootDirectory}
          </Detail>
        )}
        {(p.Slice || p.ControlGroup) && (
          <Detail label="Cgroup" className="font-mono text-xs break-all">
            {p.ControlGroup || p.Slice}
          </Detail>
        )}
        <Detail label="Unit file" className="break-all">
          {unit.fragmentPath ? (
            <button
              type="button"
              onClick={() => onTab("file")}
              className="rounded-sm text-left font-mono focus-ring hover:underline"
            >
              {unit.fragmentPath}
            </button>
          ) : (
            <span className="text-muted-foreground">None — made at run time</span>
          )}
          {dropIns.length > 0 && (
            <span className="block text-hint text-muted-foreground">
              with {plural(dropIns.length, "override")}
            </span>
          )}
        </Detail>
      </DetailList>
    </Section>
  )
}

const STARTED = new Intl.DateTimeFormat("en-US", {
  month: "short",
  day: "numeric",
  hour: "2-digit",
  minute: "2-digit",
  hourCycle: "h23",
})

/** What it has used since it started: the cgroup's counters, where they are kept. */
function WhatItUsed({
  unit,
  properties: p,
}: {
  unit: SystemdUnit
  properties: Record<string, string>
}) {
  const cpu = counter(p.CPUUsageNSec)
  const readBytes = counter(p.IOReadBytes)
  const writeBytes = counter(p.IOWriteBytes)
  const ingress = counter(p.IPIngressBytes)
  const egress = counter(p.IPEgressBytes)
  const peak = limit(p.MemoryPeak)
  const swap = counter(p.MemorySwapCurrent)
  return (
    <Section title="Since it started">
      <dl className="grid grid-cols-1 gap-x-6 sm:grid-cols-2">
        <Counter label="Started">
          {unit.activeSince ? (
            <span title={timestamp(new Date(unit.activeSince * 1000).toISOString())}>
              {STARTED.format(new Date(unit.activeSince * 1000))}
            </span>
          ) : (
            "—"
          )}
        </Counter>
        <Counter label="Processor time">{cpu !== undefined ? duration(cpu / 1e9) : "—"}</Counter>
        <Counter label="Peak memory">{peak ? bytes(peak) : "—"}</Counter>
        <Counter label="Swapped out">{swap ? bytes(swap) : "nothing"}</Counter>
        <Counter label="Read from disk">
          {readBytes !== undefined ? bytes(readBytes) : "not counted"}
        </Counter>
        <Counter label="Written to disk">
          {writeBytes !== undefined ? bytes(writeBytes) : "not counted"}
        </Counter>
        <Counter label="Received">{ingress !== undefined ? bytes(ingress) : "not counted"}</Counter>
        <Counter label="Sent">{egress !== undefined ? bytes(egress) : "not counted"}</Counter>
        <Counter label="Automatic restarts">{unit.restarts ?? 0}</Counter>
        <Counter label="Invocation">
          <span title={p.InvocationID}>{p.InvocationID ? p.InvocationID.slice(0, 12) : "—"}</span>
        </Counter>
      </dl>
      {(readBytes === undefined || ingress === undefined) && (
        <p className="mt-2 text-hint text-muted-foreground">
          {readBytes === undefined && ingress === undefined
            ? "Disk and network are counted only for units with IOAccounting= and IPAccounting= on."
            : readBytes === undefined
              ? "Disk is counted only for units with IOAccounting= on."
              : "Network is counted only for units with IPAccounting= on."}
        </p>
      )}
    </Section>
  )
}
