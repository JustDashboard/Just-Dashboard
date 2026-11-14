"use client"

import { useMemo } from "react"
import Link from "next/link"
import {
  ChartActivity,
  Copy,
  Cpu,
  Database,
  FileText,
  FolderOpen,
  Globe,
  Layers,
  RefreshClockwise,
  Warning,
} from "@/components/icons"
import { copyText } from "@/lib/clipboard"
import { bytes, duration, percent, plural, relativeTime, timestamp } from "@/lib/format"
import type { PM2Daemon, PM2Process, ProcessRow } from "@/lib/types"
import { cn } from "@/lib/utils"
import { ShellWords } from "@/components/deploy/run-evidence"
import { WireMark, WireNode, WirePlaceholder } from "@/components/deploy/wire"
import { IconAction } from "@/components/icon-action"
import { TileTrend } from "@/components/metrics/sparkline"
import { HUE, LiveBytes, LiveFigure } from "@/components/overview/readings"
import { Detail, DetailList, Section } from "@/components/page"
import { Well } from "@/components/panel"
import { ProductGlyph, hasProductLogo, pm2Product } from "@/components/product-logo"
import { NoReading, ReadingLine, ReadingLines } from "@/components/reading-line"
import { Notice } from "@/components/state"
import { StatusDot } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import {
  pm2AppState,
  pm2ModeWords,
  pm2RestartWords,
  type PM2Reading,
} from "@/components/procs/pm2-facts"
import { isCluster, pm2Failing, pm2Key, type PM2App } from "@/components/procs/pm2-shared"
import { processHref } from "@/components/procs/process-facts"
import { cores, cpuTone, reach } from "@/components/procs/shared"
import { WiringPicture, type PictureNode } from "@/components/procs/wiring-picture"

/** Workers drawn in the picture before the rest fold into a count. */
const WORKERS_DRAWN = 3

/** Below this a worker is idle and its line is still. */
const BUSY = 1

/** A start this recent is the restart the counter just counted. */
const RECENT_START = 5 * 60

export type PM2OverviewProps = {
  app: PM2App
  /** The worker the application is read through: the first that is running. */
  lead: PM2Process
  /** The process table's reading of each running worker's PID, as far as it has them. */
  rows: (ProcessRow | null)[]
  reading: PM2Reading
  daemon?: PM2Daemon
  /** Started since the startup list was last saved. */
  unsaved: boolean
  /** Seconds since the lead worker last started; zero while it is not running. */
  uptime: number
  onTab: (tab: string) => void
  /** Saves the startup list, for an account that may. */
  onSave?: () => void
}

/**
 * The application's Overview, one scroll in the order it is asked about: what
 * is wrong, where it sits — the daemon that restarts it and the hook that
 * starts the daemon above, its workers, the port they answer on and the files
 * they write below, as one wired picture — what it is taking now, the command
 * PM2 runs, and how PM2 runs it and whether a reboot brings it back.
 *
 * The sheet this replaced opened on four tiles for one worker. The figures
 * are sentences here (`ReadingLine`), and they are the application's: a
 * cluster's workers summed, because "api is using 0.6 cores" is the answer
 * and four tiles of 0.15 were the arithmetic.
 */
export function PM2AppOverview(props: PM2OverviewProps) {
  return (
    <div className="flex min-w-0 flex-col gap-8 pb-6">
      <AppNotices {...props} />
      <AppPicture {...props} />
      <AppReadings {...props} />
      <CommandBlock {...props} />
      <div className="grid min-w-0 gap-8 xl:grid-cols-2 [&>*]:min-w-0">
        <HowPM2RunsIt {...props} />
        <AfterAReboot {...props} />
      </div>
    </div>
  )
}

/** Why a reboot would not bring the application back, if it would not. */
function rebootGap(daemon: PM2Daemon | undefined, unsaved: boolean) {
  if (!daemon) return undefined
  if (!daemon.startupUnit) return "hook" as const
  if (!daemon.dumpSavedAt) return "never" as const
  if (unsaved) return "unsaved" as const
  return undefined
}

function AppNotices({ app, daemon, unsaved, onTab, onSave }: PM2OverviewProps) {
  const state = pm2AppState(app)
  const unstable = app.instances.reduce((n, p) => n + p.unstableRestarts, 0)
  const gap = rebootGap(daemon, unsaved)
  const notices: React.ReactNode[] = []
  if (app.online === 0 && app.instances.some(pm2Failing) && unstable > 0) {
    notices.push(
      <Notice key="gave-up" tone="danger" title="PM2 stopped restarting it">
        <p>
          It died within a second of starting {unstable === 1 ? "once" : `${unstable} times`}, so
          PM2 gave up. Its log says why, and Start tries again.
        </p>
        <Button size="xs" variant="outline" className="mt-2" onClick={() => onTab("logs")}>
          Read the log
        </Button>
      </Notice>,
    )
  }
  if (state.partial) {
    const down = app.instances.length - app.online
    notices.push(
      <Notice
        key="partial"
        tone="warning"
        title={`${down} of ${plural(app.instances.length, "worker")} ${down === 1 ? "is" : "are"} not running`}
      >
        <p>The rest keep serving. Start brings {down === 1 ? "it" : "them"} back.</p>
        <Button size="xs" variant="outline" className="mt-2" onClick={() => onTab("instances")}>
          Show the workers
        </Button>
      </Notice>,
    )
  }
  if (gap) {
    notices.push(
      <Notice key="reboot" tone="warning" title="A reboot would not bring it back">
        <p>
          {gap === "hook" ? (
            <>
              Nothing starts {daemon?.account}&apos;s PM2 on boot. Run{" "}
              <span className="font-mono">pm2 startup</span> as {daemon?.account} to install the
              hook.
            </>
          ) : gap === "never" ? (
            "The startup list has never been saved, so PM2 would come back with nothing to run."
          ) : (
            "It was started after the startup list was last saved."
          )}
        </p>
        {gap !== "hook" && onSave && (
          <Button size="xs" variant="outline" className="mt-2" onClick={onSave}>
            Save list
          </Button>
        )}
      </Notice>,
    )
  }
  if (notices.length === 0) return null
  return <div className="grid min-w-0 gap-3 lg:grid-cols-2">{notices}</div>
}

/** The mark an application or one of its workers is drawn as: what runs it. */
function AppGlyph({ interpreter }: { interpreter?: string }) {
  const product = pm2Product(interpreter)
  return hasProductLogo(product) ? <ProductGlyph id={product} /> : <ChartActivity aria-hidden />
}

/**
 * Where the application sits, drawn the way the process and service pages
 * draw theirs (`WiringPicture`): on the left what keeps it running — the
 * account's PM2 daemon, which starts it again when it exits, and the systemd
 * unit that starts that daemon on boot; the application in the middle as what
 * runs it; on the right its workers, each a process with a page of its own,
 * the port they answer on, the log they write and the directory they run
 * from.
 *
 * A worker's line pulses while it is busy and the port's while connections
 * are open on it; every line is dashed while the application is not running.
 * A missing boot hook is drawn, in amber, because it is what is wrong.
 */
function AppPicture({ app, lead, rows, reading, daemon, unsaved, onTab }: PM2OverviewProps) {
  const live = app.online > 0
  const state = pm2AppState(app)
  const parent = rows.find((row) => row?.pid === lead.pid)?.ppid

  const above = useMemo(() => {
    const nodes: PictureNode[] = [
      {
        id: "daemon",
        mark: (
          <WireMark tone="logo" size="md">
            <ProductGlyph id="pm2" />
          </WireMark>
        ),
        eyebrow: "PM2 daemon",
        title: <span className="block truncate">{app.daemonId}</span>,
        hint: (
          <span className="numeric">
            {lead.autorestart ? "restarts it" : "leaves it stopped"}
            {parent ? ` · PID ${parent}` : ""}
          </span>
        ),
        href: parent ? processHref(parent) : undefined,
        label: parent ? `Open the PM2 daemon, PID ${parent}` : undefined,
        wire: { tone: "default", dashed: !live, carries: false },
      },
    ]
    if (daemon?.startupUnit) {
      nodes.push({
        id: "hook",
        mark: (
          <WireMark tone="logo" size="md">
            <ProductGlyph id="systemd" />
          </WireMark>
        ),
        eyebrow: "Starts PM2 on boot",
        title: (
          <span className="block truncate font-mono">
            {daemon.startupUnit.replace(/\.service$/, "")}
          </span>
        ),
        hint: !daemon.dumpSavedAt
          ? "list never saved"
          : unsaved
            ? "this application is not in the saved list"
            : `list saved ${relativeTime(daemon.dumpSavedAt)}`,
        href: `/processes/services/${encodeURIComponent(daemon.startupUnit)}`,
        label: `Open ${daemon.startupUnit}`,
        wire: {
          tone: !daemon.dumpSavedAt || unsaved ? "warning" : "default",
          dashed: !live || !daemon.dumpSavedAt || unsaved,
          carries: false,
        },
      })
    } else if (daemon) {
      nodes.push({
        id: "hook",
        mark: (
          <WireMark tone="warning" size="md">
            <Warning aria-hidden />
          </WireMark>
        ),
        eyebrow: "On boot",
        title: "No boot hook",
        hint: (
          <>
            run <span className="font-mono">pm2 startup</span> as {daemon.account}
          </>
        ),
        wire: { tone: "warning", dashed: true, carries: false },
      })
    }
    return nodes
  }, [app.daemonId, lead.autorestart, parent, daemon, unsaved, live])

  const below = useMemo(() => {
    const cluster = isCluster(lead)
    const workers = [...app.instances].sort((a, b) => a.id - b.id)
    const drawn = workers.length > WORKERS_DRAWN + 1 ? workers.slice(0, WORKERS_DRAWN) : workers
    const nodes: PictureNode[] = drawn.map((worker) => {
      const row = rows.find((r) => r?.pid === worker.pid && worker.pid > 0)
      const online = worker.status === "online"
      const cpu = row && row.cpuReady !== false ? row.cpuPercent : worker.cpu
      return {
        id: pm2Key(worker),
        mark: (
          <span className="relative flex">
            <WireMark tone="logo" size="md">
              <AppGlyph interpreter={worker.interpreter} />
            </WireMark>
            <span
              aria-hidden
              className="absolute -right-0.5 -bottom-0.5 flex size-3.5 items-center justify-center rounded-full bg-background"
            >
              <StatusDot state={worker.status} />
            </span>
          </span>
        ),
        eyebrow: cluster
          ? `Worker ${workers.indexOf(worker) + 1} of ${workers.length}`
          : "Its process",
        title: (
          <span className="numeric block truncate">
            {worker.pid > 0 ? `PID ${worker.pid}` : worker.status}
          </span>
        ),
        hint: online ? (
          <span className="numeric">
            <span className={cn(cpuTone(cpu) !== "default" && "text-warning")}>
              {percent(cpu)} CPU
            </span>{" "}
            · {bytes(row?.rss ?? worker.memory)}
          </span>
        ) : pm2Failing(worker) && worker.unstableRestarts > 0 ? (
          "PM2 stopped retrying"
        ) : (
          "not running"
        ),
        href: worker.pid > 0 ? processHref(worker.pid) : undefined,
        label: worker.pid > 0 ? `Open process ${worker.pid}` : undefined,
        wire: {
          tone: pm2Failing(worker) ? "danger" : "default",
          dashed: !online,
          carries: online && cpu >= BUSY,
        },
      }
    })
    if (drawn.length < workers.length) {
      nodes.push({
        id: "more",
        mark: (
          <WirePlaceholder size="md">
            <Cpu />
          </WirePlaceholder>
        ),
        eyebrow: "Workers",
        title: `${workers.length - drawn.length} more`,
        hint: "Listed under Instances",
        open: () => onTab("instances"),
        label: "Show every worker",
        wire: { tone: "default", dashed: true, carries: false },
      })
    }
    if (reading.listening.length > 0) {
      // One node for every socket it listens on, as the process page draws it.
      const ports = [...new Set(reading.listening.map((port) => port.port))].sort((a, b) => a - b)
      const protos = [
        ...new Set(reading.listening.map((port) => port.proto.replace(/6$/, "").toUpperCase())),
      ]
      const open = reading.listening.some((port) => reach(port.address) === "every interface")
      const local = reading.listening.every((port) => reach(port.address) === "this machine only")
      nodes.push({
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
        hint: `${open ? "every interface" : local ? "this machine only" : "one address"} · ${plural(reading.connections, "open connection")}`,
        href: lead.pid > 0 ? processHref(lead.pid, "network") : undefined,
        label: "Show what it listens on and its connections",
        wire: {
          tone: open ? "warning" : "default",
          dashed: !live,
          carries: live && reading.connections > 0,
        },
      })
    }
    if (lead.outLogPath || lead.errLogPath) {
      nodes.push({
        id: "log",
        mark: (
          <WireMark tone="logo" size="md">
            <FileText aria-hidden />
          </WireMark>
        ),
        eyebrow: "Writes",
        title: "Its log",
        hint: (
          <span className="block truncate font-mono">
            {(lead.outLogPath || lead.errLogPath).split("/").pop()}
          </span>
        ),
        open: () => onTab("logs"),
        label: "Read its log",
        wire: { tone: "default", dashed: !live, carries: false },
      })
    }
    if (lead.cwd) {
      nodes.push({
        id: "cwd",
        mark: (
          <WireMark tone="logo" size="md">
            <FolderOpen aria-hidden />
          </WireMark>
        ),
        eyebrow: "Runs from",
        title: <span className="block truncate font-mono">{lead.cwd}</span>,
        href: `/files?path=${encodeURIComponent(lead.cwd)}`,
        label: `Open ${lead.cwd} in Files`,
        wire: { tone: "default", dashed: !live, carries: false },
      })
    }
    return nodes
  }, [app.instances, lead, rows, reading.listening, reading.connections, live, onTab])

  return (
    <WiringPicture
      title="Where it runs"
      left={{
        title: "Above it",
        nodes: above,
        empty: (
          <WireNode
            align="end"
            mark={
              <WirePlaceholder size="md">
                <ChartActivity />
              </WirePlaceholder>
            }
            title={<span className="text-muted-foreground">Reading its daemon…</span>}
          />
        ),
      }}
      right={{
        title: "Below and around it",
        nodes: below,
        empty: (
          <WireNode
            mark={
              <WirePlaceholder size="md">
                <Cpu />
              </WirePlaceholder>
            }
            title={<span className="text-muted-foreground">Nothing under it</span>}
          />
        ),
      }}
      center={{
        mark: (
          <>
            <WireMark tone="logo" size="lg">
              <AppGlyph interpreter={app.interpreter} />
            </WireMark>
            <span
              aria-hidden
              className="absolute -right-0.5 -bottom-0.5 flex size-4 items-center justify-center rounded-full bg-background"
            >
              {state.partial ? (
                <StatusDot tone="warning" />
              ) : (
                <StatusDot state={state.status} live={live} />
              )}
            </span>
          </>
        ),
        eyebrow: pm2ModeWords(app),
        title: <span className="block truncate">{app.name}</span>,
        hint: (
          <span className="numeric">
            {live ? `${cores(reading.cpu)} · ${bytes(reading.rss)}` : state.word}
          </span>
        ),
      }}
    />
  )
}

/** A reading's mark: the measure's own hue, or the tone of what is wrong with it. */
function ReadingMark({
  tone,
  hue,
  children,
}: {
  tone: "default" | "warning" | "danger"
  hue?: string
  children: React.ReactNode
}) {
  if (tone !== "default") {
    return (
      <WireMark size="md" tone={tone}>
        {children}
      </WireMark>
    )
  }
  return (
    <WireMark size="md" hue={hue}>
      {children}
    </WireMark>
  )
}

/**
 * What the application is taking, as four lines that hold their place whether
 * or not it is running: processor and memory summed over its workers, memory
 * against the limit PM2 restarts a worker at, disk rates, and PM2's count of
 * its restarts. Where the process table has not read a worker the figure is
 * PM2's own, and the line says so.
 */
function AppReadings({ app, lead, reading, uptime }: PM2OverviewProps) {
  const live = app.online > 0
  const workers = app.online
  const restarts = app.instances.reduce((n, p) => n + p.restarts, 0)
  const unstable = app.instances.reduce((n, p) => n + p.unstableRestarts, 0)
  const limit = lead.maxMemoryRestart
  const full = reading.limitShare
  const memoryTone =
    full === undefined ? "default" : full >= 95 ? "danger" : full >= 80 ? "warning" : "default"
  // Per worker, as the list colours a row: four workers at 20% are not one at 80%.
  const share = cpuTone(reading.cpu / Math.max(workers, 1))
  const cpu = share === "danger" || share === "warning" ? share : "default"
  const recent = live && uptime < RECENT_START && restarts > 0
  const stopped = <NoReading>Not running</NoReading>
  return (
    <Section title="What it takes">
      {/* One column until the page is wide: beside the rail a half-width line
          has no room left for its sentence. */}
      <ReadingLines data-testid="pm2-readings" className="xl:grid-cols-1 2xl:grid-cols-2">
        <ReadingLine
          mark={
            <ReadingMark tone={live ? cpu : "default"} hue={HUE.cpu}>
              <Cpu aria-hidden />
            </ReadingMark>
          }
          name="CPU"
          hint={
            !live
              ? undefined
              : reading.measured
                ? [workers > 1 ? `${workers} workers` : "", plural(reading.threads, "thread")]
                    .filter(Boolean)
                    .join(" · ")
                : "as PM2 measures it"
          }
          trend={
            live && (
              <TileTrend
                values={reading.history.cpu}
                color={HUE.cpu}
                label="CPU over the last minutes"
              />
            )
          }
        >
          {!live ? (
            stopped
          ) : reading.cpu >= 100 ? (
            <span className="numeric">
              <LiveFigure value={reading.cpu / 100} decimals={2} /> cores busy
            </span>
          ) : (
            <span className="numeric">
              <LiveFigure value={reading.cpu} decimals={1} unit="%" /> of one core
            </span>
          )}
        </ReadingLine>
        <ReadingLine
          mark={
            <ReadingMark tone={live ? memoryTone : "default"} hue={HUE.mem}>
              <Layers aria-hidden />
            </ReadingMark>
          }
          name="Memory"
          hint={
            !live
              ? undefined
              : full !== undefined && limit
                ? `${workers > 1 ? "fullest worker at " : ""}${percent(full, 0)} of the ${bytes(limit, 0)} PM2 restarts it at`
                : "no restart limit"
          }
          trend={
            live && (
              <TileTrend
                values={reading.history.rss}
                color={HUE.mem}
                label="Resident memory over the last minutes"
              />
            )
          }
        >
          {live ? (
            <span className="numeric">
              <LiveBytes value={reading.rss} /> in use
            </span>
          ) : (
            stopped
          )}
        </ReadingLine>
        <ReadingLine
          mark={
            <ReadingMark tone="default" hue={HUE.disk}>
              <Database aria-hidden />
            </ReadingMark>
          }
          name="Disk"
          hint={
            !live
              ? undefined
              : reading.io
                ? `read ${bytes(reading.io.read)}/s · write ${bytes(reading.io.write)}/s`
                : "measuring"
          }
          trend={
            live && (
              <TileTrend
                values={reading.history.io}
                color={HUE.disk}
                label="Disk reads and writes over the last minutes"
              />
            )
          }
        >
          {!live ? (
            stopped
          ) : reading.io ? (
            <span className="numeric">
              <LiveBytes value={reading.io.read + reading.io.write} suffix="/s" />
            </span>
          ) : (
            <NoReading>No reading yet</NoReading>
          )}
        </ReadingLine>
        <ReadingLine
          mark={
            <ReadingMark tone={unstable > 0 ? "danger" : recent ? "warning" : "default"}>
              <RefreshClockwise aria-hidden />
            </ReadingMark>
          }
          name="Restarts"
          hint={
            unstable > 0 ? (
              <span className="text-destructive">{unstable} unstable</span>
            ) : restarts > 0 && live ? (
              `last start ${duration(uptime)} ago`
            ) : app.instances.length > 1 ? (
              `across its ${plural(app.instances.length, "worker")}`
            ) : undefined
          }
        >
          <span className="numeric">{pm2RestartWords(restarts)}</span>
        </ReadingLine>
      </ReadingLines>
    </Section>
  )
}

/** The command PM2 runs, coloured as a command is, with where it runs from and what it writes. */
function CommandBlock({ lead, rows, onTab }: PM2OverviewProps) {
  const row = rows.find((r) => r?.pid === lead.pid && lead.pid > 0)
  const command = row?.cmdline || [lead.interpreter || "node", lead.scriptPath].join(" ")
  return (
    <Section
      title="Command"
      actions={
        <IconAction
          label="Copy command line"
          onClick={() => void copyText(command, "Command line copied")}
        >
          <Copy />
        </IconAction>
      }
    >
      <Well className="max-h-40 text-xs leading-relaxed break-all whitespace-pre-wrap">
        <ShellWords command={command} />
      </Well>
      <DetailList>
        <Detail label="Script" className="font-mono break-all">
          {lead.scriptPath || "—"}
        </Detail>
        <Detail label="Working directory" className="font-mono break-all">
          {lead.cwd ? (
            <Link href={`/files?path=${encodeURIComponent(lead.cwd)}`} className="hover:underline">
              {lead.cwd}
            </Link>
          ) : (
            "—"
          )}
        </Detail>
        <Detail label="Interpreter" className="font-mono">
          {lead.interpreter || "node"}
          {lead.nodeVersion && (lead.interpreter ?? "node") === "node" && (
            <span className="ml-1 text-muted-foreground">v{lead.nodeVersion}</span>
          )}
        </Detail>
        <Detail label="Output" className="font-mono break-all">
          {lead.outLogPath ? (
            <button
              type="button"
              className="rounded-sm text-left focus-ring hover:underline"
              onClick={() => onTab("logs")}
            >
              {lead.outLogPath}
            </button>
          ) : (
            "—"
          )}
        </Detail>
        <Detail label="Errors" className="font-mono break-all">
          {lead.errLogPath || "—"}
        </Detail>
      </DetailList>
    </Section>
  )
}

function HowPM2RunsIt({ app, lead }: PM2OverviewProps) {
  const cluster = isCluster(lead)
  return (
    <Section title="How PM2 runs it">
      <DetailList>
        <Detail label="Mode">
          {cluster ? `Cluster of ${plural(app.instances.length, "worker")}` : "Fork, one process"}
        </Detail>
        <Detail label="On crash">{lead.autorestart ? "Restarts it" : "Leaves it stopped"}</Detail>
        <Detail label="Memory limit">
          {lead.maxMemoryRestart
            ? `Restarts ${cluster ? "a worker" : "it"} above ${bytes(lead.maxMemoryRestart, 0)}`
            : "None"}
        </Detail>
        <Detail label="On file change">
          {lead.watching ? <span className="text-warning">Restarts it</span> : "Nothing"}
        </Detail>
        <Detail label="Runs as" className="font-mono">
          {lead.user || app.daemonId}
        </Detail>
        {lead.version && (
          <Detail label="Version" className="numeric font-mono">
            {lead.version}
          </Detail>
        )}
        <Detail label="Registered">
          {lead.createdAtMs ? (
            <span title={timestamp(new Date(lead.createdAtMs).toISOString())}>
              {relativeTime(new Date(lead.createdAtMs).toISOString())}
            </span>
          ) : (
            "—"
          )}
        </Detail>
      </DetailList>
    </Section>
  )
}

/** Whether the application comes back by itself: the hook, the saved list, and whether it is in it. */
function AfterAReboot({ daemon, unsaved, onSave }: PM2OverviewProps) {
  const gap = rebootGap(daemon, unsaved)
  return (
    <Section
      title="After a reboot"
      actions={
        daemon?.startupUnit &&
        gap &&
        onSave && (
          <Button size="xs" variant="outline" onClick={onSave}>
            Save list
          </Button>
        )
      }
    >
      <DetailList>
        <Detail label="Boot hook">
          {daemon?.startupUnit ? (
            <Link
              href={`/processes/services/${encodeURIComponent(daemon.startupUnit)}`}
              className="font-mono hover:underline"
            >
              {daemon.startupUnit}
            </Link>
          ) : (
            <span className="text-warning">
              None. Run <span className="font-mono">pm2 startup</span> as {daemon?.account}
            </span>
          )}
        </Detail>
        <Detail label="Startup list">
          {daemon?.dumpSavedAt ? (
            <span title={timestamp(daemon.dumpSavedAt)}>
              Saved {relativeTime(daemon.dumpSavedAt)}
            </span>
          ) : (
            <span className="text-warning">Never saved</span>
          )}
        </Detail>
        <Detail label="This application">
          {gap === undefined ? (
            <span className="text-success">Comes back</span>
          ) : gap === "unsaved" ? (
            <span className="text-warning">Not in the saved list</span>
          ) : (
            <span className="text-warning">Does not come back</span>
          )}
        </Detail>
      </DetailList>
    </Section>
  )
}
