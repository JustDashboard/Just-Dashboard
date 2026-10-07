"use client"

import { useMemo, useState } from "react"
import { useViewState } from "@/lib/view-state"
import Link from "next/link"
import type { PM2Process, ProcessRow } from "@/lib/types"
import { get, post } from "@/lib/api"
import { bytes, duration, percent, plural, relativeTime, timestamp } from "@/lib/format"
import { copyText } from "@/lib/clipboard"
import { pm2Source } from "@/lib/log-sources"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"
import { useAuth } from "@/hooks/use-auth"
import { useMetrics } from "@/hooks/use-metrics"
import { usePoll } from "@/hooks/use-poll"
import { useConfirm } from "@/components/confirm-dialog"
import { ShellWords } from "@/components/deploy/run-evidence"
import { useNow } from "@/components/deploy/vocabulary"
import { IconAction } from "@/components/icon-action"
import { ArrowUpRight, ChartActivity, Copy, Minus, Plus } from "@/components/icons"
import { ServiceLogs, type LogWindow, type ServiceLogSource } from "@/components/logs/service-logs"
import { TileTrend } from "@/components/metrics/sparkline"
import { Modal } from "@/components/modal"
import { HUE, LiveBytes, LiveFigure } from "@/components/overview/readings"
import { Detail, DetailList } from "@/components/page"
import { Panel, PanelBody, PanelHeader, Well } from "@/components/panel"
import { ProductLogo, pm2Product } from "@/components/product-logo"
import { SidePanel } from "@/components/side-panel"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { Notice } from "@/components/state"
import { Status, StatusDot } from "@/components/status-dot"
import { FilterChip } from "@/components/tabs"
import { Tag } from "@/components/tag"
import { VerbBar } from "@/components/verbs"
import { Button } from "@/components/ui/button"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { usePM2Control, usePM2Verbs } from "@/components/procs/pm2-actions"
import {
  isCluster,
  memoryLimitShare,
  pm2AppKey,
  pm2Failing,
  pm2Key,
} from "@/components/procs/pm2-shared"
import { MiniBar } from "@/components/procs/process-table"
import { cpuTone, reach } from "@/components/procs/shared"

/**
 * The stretch "around the last start" covers: the crash before it — PM2's
 * backoff waits up to fifteen seconds before trying again — and the start.
 */
const BEFORE_START = 2 * 60_000
const AFTER_START = 60_000

/** An open sheet reads its process this often: the figures are the point of opening it. */
const DETAIL_POLL = 2000

/** A start this recent is the restart the counter just counted, and the sheet says so. */
const RECENT_START = 5 * 60

/**
 * One PM2 application, opened: what it is doing now, how it is run, and what
 * it has printed.
 *
 * PM2 says what it was given — `pm2 jlist`, which the list already holds;
 * its "describe" says no more without also handing over the environment,
 * which is where the secrets are. What the process is doing is the process
 * table's to say, so the sheet reads the application's PID from it every
 * two seconds: the figures glide to each read over the minutes the sampler
 * keeps, and the ports it listens on are named with who can reach them.
 */
export function PM2DetailSheet(props: {
  process: PM2Process | null
  /** Every listed process, for a cluster's other instances. */
  processes: PM2Process[]
  /** "logs" opens on the log; "scale" opens the scale dialog over the overview. */
  initialTab?: string
  onOpenChange: (open: boolean) => void
  /** Opens another instance of the same application in this sheet. */
  onSelect: (process: PM2Process) => void
  onChanged: () => void
}) {
  // Remounted per application and per requested tab, so opening "logs" from
  // a row lands on logs even when the sheet was already open on overview.
  return (
    <PM2Sheet
      key={`${props.process ? pm2Key(props.process) : "none"}:${props.initialTab ?? ""}`}
      {...props}
    />
  )
}

function PM2Sheet({
  process,
  processes,
  initialTab,
  onOpenChange,
  onSelect,
  onChanged,
}: React.ComponentProps<typeof PM2DetailSheet>) {
  const { confirm, dialog } = useConfirm()
  const { pending, act } = usePM2Control(onChanged)
  // Which tab an application opens on, remembered the way a container's is;
  // a caller asking for a specific tab ("open its logs") is answered first.
  const [remembered, remember] = useViewState("processes.pm2.detail.tab", "overview")
  const [tab, setTabState] = useState(
    initialTab === "logs" ? "logs" : initialTab === "scale" ? "overview" : remembered,
  )
  const setTab = (next: string) => {
    setTabState(next)
    remember(next)
  }
  const [scaling, setScaling] = useState(initialTab === "scale")
  const busy = process ? pending[pm2Key(process)] : undefined
  const siblings = useMemo(
    () =>
      process
        ? processes.filter((p) => pm2AppKey(p) === pm2AppKey(process)).sort((a, b) => a.id - b.id)
        : [],
    [process, processes],
  )

  return (
    <SidePanel
      open={process !== null}
      onOpenChange={onOpenChange}
      // The sheet opens on the application as what runs it, then its name.
      title={
        <>
          <ProductLogo
            id={process ? pm2Product(process.interpreter) : "pm2"}
            size="sm"
            fallback={ChartActivity}
          />
          <span className="min-w-0 truncate">{process?.name ?? "PM2"}</span>
          {process && (
            <span className="numeric font-mono text-body font-normal text-muted-foreground">
              #{process.id}
            </span>
          )}
        </>
      }
      description={process ? `${process.daemonId} #${process.id}` : "PM2 application"}
      actions={
        process && (
          <PM2SheetActions
            process={process}
            busy={busy}
            confirm={confirm}
            act={act}
            onScale={() => setScaling(true)}
            onChanged={() => {
              onChanged()
              onOpenChange(false)
            }}
          />
        )
      }
      bodyClassName="flex min-h-0 flex-1 flex-col"
    >
      {process && (
        <Tabs value={tab} onValueChange={setTab} className="flex min-h-0 flex-1 flex-col gap-0">
          <div className="shrink-0 px-5 pt-4">
            <TabsList className="w-fit">
              <TabsTrigger value="overview">Overview</TabsTrigger>
              <TabsTrigger value="logs">Logs</TabsTrigger>
            </TabsList>
          </div>
          <TabsContent value="overview" className="min-h-0 flex-1 overflow-y-auto">
            <PM2Overview
              process={process}
              siblings={siblings}
              onSelect={onSelect}
              onLogs={() => setTab("logs")}
              onScale={() => setScaling(true)}
            />
          </TabsContent>
          <TabsContent value="logs" className="flex min-h-0 flex-1 flex-col gap-3 px-5 pt-4 pb-5">
            {/* Keyed on the process, so switching to another one starts with a
                clean buffer instead of appending to the previous process's.
                A daemon whose files are outside the log roots opens nothing:
                the server would refuse the read, and a socket retrying a
                refusal once a second is noise under a sentence that already
                says why. */}
            {process.logsAvailable === false ? (
              <Notice title="Logs unavailable">
                {process.logsUnavailableReason ||
                  "The daemon's log files are outside the configured log roots."}
              </Notice>
            ) : (
              <PM2Logs key={pm2Key(process)} process={process} />
            )}
          </TabsContent>
        </Tabs>
      )}
      {process && scaling && (
        <ScaleDialog process={process} onClose={() => setScaling(false)} onChanged={onChanged} />
      )}
      {dialog}
    </SidePanel>
  )
}

function PM2SheetActions({
  process,
  busy,
  confirm,
  act,
  onScale,
  onChanged,
}: {
  process: PM2Process
  busy?: string
  confirm: Parameters<typeof usePM2Verbs>[0]["confirm"]
  act: Parameters<typeof usePM2Verbs>[0]["act"]
  onScale: () => void
  onChanged: () => void
}) {
  const verbs = usePM2Verbs({ process, confirm, act, onScale, onChanged })
  return (
    <>
      {busy ? (
        <span className="inline-flex items-center gap-1.5 text-xs font-medium">
          <StatusDot tone="notice" />
          <TextShimmer>{`${busy}…`}</TextShimmer>
        </span>
      ) : (
        <Status state={process.status} />
      )}
      <VerbBar verbs={verbs} />
    </>
  )
}

/**
 * The overview, one scroll: what it is as a line of facts, what is wrong with
 * it if anything is, four readings that move, its other instances, the ports
 * it answers on, the command it runs, and how PM2 runs it.
 */
function PM2Overview({
  process,
  siblings,
  onSelect,
  onLogs,
  onScale,
}: {
  process: PM2Process
  siblings: PM2Process[]
  onSelect: (process: PM2Process) => void
  onLogs: () => void
  onScale: () => void
}) {
  const { can } = useAuth()
  const online = process.status === "online" && process.pid > 0
  // The process table's reading of this PID, every two seconds while it
  // runs. Its history is the sampler's, so a PID the live table has been
  // watching opens on those minutes, and one it has not fills in here.
  const detail = usePoll(
    (signal) => get<ProcessRow>(`/processes/${process.pid}`, undefined, signal),
    DETAIL_POLL,
    [process.pid],
    { enabled: online },
  )
  const row = online && detail.data?.pid === process.pid ? detail.data : undefined
  // Uptime is a fact that changes while the sheet is open, so it ticks: from
  // the process's own start time once the table has read it, and until then
  // from PM2's uptime, held across its five-second reads.
  const now = useNow(1000, online)
  const pm2Start = useHeldStart(process, now)
  const started = row ? Date.parse(row.createTime) : pm2Start
  const uptime = online && started !== undefined ? Math.max(0, (now - started) / 1000) : 0
  const cluster = isCluster(process)
  const position = siblings.findIndex((p) => pm2Key(p) === pm2Key(process)) + 1
  const command = row?.cmdline || [process.interpreter || "node", process.scriptPath].join(" ")

  return (
    <div className="flex animate-rise flex-col gap-6 px-5 pt-4 pb-6">
      {/* What this application is, as the facts after a name — the line every
          identity in the product draws. */}
      <p className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-xs text-muted-foreground">
        <span className="text-foreground">{process.daemonId}</span>
        <Dot />
        <span>
          {cluster
            ? `cluster instance ${position || "?"} of ${siblings.length || process.instances || 1}`
            : "fork, one process"}
        </span>
        {online && (
          <>
            <Dot />
            <span
              className={cn(
                "numeric",
                uptime < RECENT_START && process.restarts > 0 && "text-warning",
              )}
            >
              up {duration(uptime)}
            </span>
            <Dot />
            <Link
              href={`/processes?pid=${process.pid}`}
              className="numeric inline-flex items-center gap-1 text-foreground hover:underline"
            >
              PID {process.pid}
              <ArrowUpRight aria-hidden className="size-3" />
            </Link>
          </>
        )}
        {process.version && (
          <>
            <Dot />
            <span className="numeric font-mono">v{process.version}</span>
          </>
        )}
        {process.watching && <Tag tone="warning">watching</Tag>}
      </p>

      <Attention process={process} uptime={uptime} onLogs={onLogs} />

      {online && (
        <StatGrid columns={4} dense className="-mx-5 border-y border-hairline">
          <Readings process={process} row={row} uptime={uptime} />
        </StatGrid>
      )}

      {cluster && siblings.length > 0 && (
        <Panel plain>
          <PanelHeader
            title="Instances"
            actions={
              <span className="flex items-center gap-2">
                <span className="numeric text-hint text-muted-foreground">
                  {siblings.filter((p) => p.status === "online").length} of{" "}
                  {plural(siblings.length, "worker")} online
                </span>
                {can("service.control") && (
                  <Button size="xs" variant="outline" onClick={onScale}>
                    Scale…
                  </Button>
                )}
              </span>
            }
          />
          <ul className="-mx-2">
            {siblings.map((sibling) => (
              <InstanceRow
                key={pm2Key(sibling)}
                instance={sibling}
                current={pm2Key(sibling) === pm2Key(process)}
                heaviest={Math.max(...siblings.map((p) => p.memory), 1)}
                onSelect={() => onSelect(sibling)}
              />
            ))}
          </ul>
        </Panel>
      )}

      {row?.listening && row.listening.length > 0 && (
        <Panel plain>
          <PanelHeader
            title="Listening"
            actions={
              <span className="numeric text-hint text-muted-foreground">
                {plural(row.connections ?? 0, "open connection")}
              </span>
            }
          />
          <ul className="divide-y divide-hairline">
            {row.listening.map((port) => (
              <li
                key={`${port.proto}/${port.address}/${port.port}`}
                className="flex min-w-0 items-baseline gap-3 py-2 text-body"
              >
                <span className="numeric w-16 shrink-0 font-mono font-medium">:{port.port}</span>
                <span className="w-12 shrink-0 font-mono text-hint text-muted-foreground uppercase">
                  {port.proto}
                </span>
                <span className="min-w-0 truncate text-muted-foreground">
                  {reach(port.address)}
                </span>
                <span className="ml-auto truncate font-mono text-hint text-muted-foreground">
                  {port.address || "*"}
                </span>
              </li>
            ))}
          </ul>
        </Panel>
      )}

      <Panel plain>
        <PanelHeader
          title="Command"
          actions={
            <IconAction
              label="Copy command line"
              onClick={() => void copyText(command, "Command line copied")}
            >
              <Copy />
            </IconAction>
          }
        />
        <PanelBody className="space-y-3 pt-3">
          <Well className="max-h-40 text-xs leading-relaxed break-all whitespace-pre-wrap">
            <ShellWords command={command} />
          </Well>
          <DetailList>
            <Detail label="Script" className="font-mono break-all">
              {process.scriptPath || "—"}
            </Detail>
            <Detail label="Working directory" className="font-mono break-all">
              {process.cwd ? (
                <Link
                  href={`/files?path=${encodeURIComponent(process.cwd)}`}
                  className="hover:underline"
                >
                  {process.cwd}
                </Link>
              ) : (
                "—"
              )}
            </Detail>
            <Detail label="Interpreter" className="font-mono">
              {process.interpreter || "node"}
              {process.nodeVersion && (process.interpreter ?? "node") === "node" && (
                <span className="ml-1 text-muted-foreground">v{process.nodeVersion}</span>
              )}
            </Detail>
          </DetailList>
        </PanelBody>
      </Panel>

      <Panel plain>
        <PanelHeader title="How PM2 runs it" />
        <PanelBody className="pt-3">
          <dl className="grid grid-cols-1 gap-x-6 gap-y-1.5 sm:grid-cols-2">
            <Fact label="Mode">
              {cluster
                ? `Cluster of ${plural(process.instances || siblings.length || 1, "worker")}`
                : "Fork, one process"}
            </Fact>
            <Fact label="On crash">
              {process.autorestart ? "Restarts it" : "Leaves it stopped"}
            </Fact>
            <Fact label="Memory limit">
              {process.maxMemoryRestart
                ? `Restarts above ${bytes(process.maxMemoryRestart, 0)}`
                : "None"}
            </Fact>
            <Fact label="On file change">{process.watching ? "Restarts it" : "Nothing"}</Fact>
            <Fact label="Runs as">{process.user}</Fact>
            <Fact label="Registered">
              {process.createdAtMs ? (
                <span title={timestamp(new Date(process.createdAtMs).toISOString())}>
                  {relativeTime(new Date(process.createdAtMs).toISOString())}
                </span>
              ) : (
                "—"
              )}
            </Fact>
          </dl>
        </PanelBody>
      </Panel>

      <Panel plain>
        <PanelHeader
          title="Log files"
          actions={
            <Button size="xs" variant="ghost" onClick={onLogs}>
              Read the log
            </Button>
          }
        />
        <PanelBody className="pt-3">
          <DetailList>
            <Detail label="stdout" className="font-mono break-all">
              {process.outLogPath || "—"}
            </Detail>
            <Detail label="stderr" className="font-mono break-all">
              {process.errLogPath || "—"}
            </Detail>
          </DetailList>
        </PanelBody>
      </Panel>
    </div>
  )
}

function Dot() {
  return <span className="text-muted-foreground/40">·</span>
}

/**
 * When PM2 last started the application, held across reads. Its uptime comes
 * from a list cached for a few seconds, so a start recomputed from each read
 * moves back and forth by that much; a move larger than that is a restart.
 */
function useHeldStart(process: PM2Process, now: number): number | undefined {
  const fresh = process.uptimeMs > 0 ? now - process.uptimeMs : undefined
  const [held, setHeld] = useState<{ uptimeMs: number; start?: number }>({
    uptimeMs: process.uptimeMs,
    start: fresh,
  })
  if (held.uptimeMs !== process.uptimeMs) {
    const keep =
      fresh !== undefined && held.start !== undefined && Math.abs(held.start - fresh) < 10_000
    setHeld({ uptimeMs: process.uptimeMs, start: keep ? held.start : fresh })
  }
  return held.start
}

function Fact({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex min-w-0 items-baseline justify-between gap-3 border-b border-hairline py-1.5">
      <dt className="truncate text-xs text-muted-foreground">{label}</dt>
      <dd className="truncate text-right text-xs">{children}</dd>
    </div>
  )
}

/**
 * What is wrong with it, if anything, said once at the top: an application
 * PM2 gave up on, one stopped on purpose, and one that restarted in the last
 * few minutes — each with the way to its log, which is where the reason is.
 */
function Attention({
  process,
  uptime,
  onLogs,
}: {
  process: PM2Process
  uptime: number
  onLogs: () => void
}) {
  const read = (
    <Button size="xs" variant="outline" className="mt-2" onClick={onLogs}>
      Read the log
    </Button>
  )
  if (pm2Failing(process)) {
    return (
      <Notice tone="danger" title="PM2 stopped restarting it">
        {process.unstableRestarts > 0
          ? `It died within moments of starting ${plural(process.unstableRestarts, "time")} in a row, and PM2 gives up after that. It stays down until it is started again; what it printed before each crash is in its log.`
          : `PM2 reports it as ${process.status}. It stays down until it is started again; what it printed is in its log.`}
        <div>{read}</div>
      </Notice>
    )
  }
  if (process.status === "stopped") {
    return (
      <Notice title="Stopped">
        Registered with PM2 and not running. It serves again once started; nothing about it was
        removed.
      </Notice>
    )
  }
  if (process.status === "online" && process.restarts > 0 && uptime < RECENT_START) {
    return (
      <Notice tone="warning" title={`Restarted ${duration(uptime)} ago`}>
        {`That is its ${ordinal(process.restarts)} restart${process.autorestart ? ", most likely after a crash" : ""}. The minutes around it are in its log.`}
        <div>{read}</div>
      </Notice>
    )
  }
  return null
}

function ordinal(n: number) {
  const rem = n % 100
  if (rem >= 11 && rem <= 13) return `${n}th`
  return `${n}${["th", "st", "nd", "rd"][n % 10] ?? "th"}`
}

/**
 * Four readings that move: CPU and memory over the process's recent windows,
 * disk I/O, and the restart count. Memory reads against the limit PM2
 * restarts it at, when there is one. Where the process table cannot be read
 * the figures are PM2's own, without their history.
 */
function Readings({
  process,
  row,
  uptime,
}: {
  process: PM2Process
  row?: ProcessRow
  uptime: number
}) {
  const history = row?.history ?? []
  const cpu = row && row.cpuReady !== false ? row.cpuPercent : process.cpu
  const rss = row?.rss ?? process.memory
  const limit = memoryLimitShare({ memory: rss, maxMemoryRestart: process.maxMemoryRestart })
  const io = (row?.ioReadRate ?? 0) + (row?.ioWriteRate ?? 0)
  return (
    <>
      <StatTile
        className="px-5"
        label="CPU"
        tone={cpuTone(cpu)}
        value={<LiveFigure value={cpu} decimals={1} unit="%" />}
        trailing="of a core"
        trend={
          <TileTrend
            values={history.map((p) => p.cpu)}
            color={HUE.cpu}
            label="CPU over the last minutes"
          />
        }
        hint={row ? plural(row.threads, "thread") : "as PM2 measures it"}
      />
      <StatTile
        className="px-5"
        label="Memory"
        tone={
          limit === undefined
            ? "default"
            : limit >= 95
              ? "danger"
              : limit >= 80
                ? "warning"
                : "default"
        }
        value={<LiveBytes value={rss} />}
        trailing={process.maxMemoryRestart ? `of ${bytes(process.maxMemoryRestart, 0)}` : undefined}
        trend={
          <TileTrend
            values={history.map((p) => p.rss)}
            color={HUE.mem}
            label="Resident memory over the last minutes"
          />
        }
        hint={
          limit !== undefined
            ? `${percent(limit, 0)} of the restart limit`
            : row
              ? `${bytes(row.vms)} virtual`
              : "no restart limit"
        }
      />
      <StatTile
        className="px-5"
        label="Disk I/O"
        value={
          row?.ioReady ? (
            <LiveBytes value={io} suffix="/s" />
          ) : (
            <span className="text-muted-foreground">—</span>
          )
        }
        trend={
          <TileTrend
            values={history.map((p) => p.read + p.write)}
            color={HUE.disk}
            label="Disk reads and writes over the last minutes"
          />
        }
        hint={
          row?.ioReady
            ? `read ${bytes(row.ioReadRate ?? 0)}/s · write ${bytes(row.ioWriteRate ?? 0)}/s`
            : "measuring"
        }
      />
      <StatTile
        className="px-5"
        label="Restarts"
        tone={
          process.unstableRestarts > 0
            ? "danger"
            : uptime < RECENT_START && process.restarts > 0
              ? "warning"
              : "default"
        }
        value={<LiveFigure value={process.restarts} />}
        hint={
          process.unstableRestarts > 0
            ? `${process.unstableRestarts} unstable`
            : process.restarts > 0
              ? `last one ${duration(uptime)} ago`
              : "never restarted"
        }
      />
    </>
  )
}

/** One worker of a cluster: which, its PID, its readings and state; a press opens it here. */
function InstanceRow({
  instance,
  current,
  heaviest,
  onSelect,
}: {
  instance: PM2Process
  current: boolean
  heaviest: number
  onSelect: () => void
}) {
  const online = instance.status === "online"
  return (
    <li>
      <button
        type="button"
        onClick={onSelect}
        aria-current={current || undefined}
        className={cn(
          "flex w-full min-w-0 items-center gap-3 rounded-md px-2 py-2 text-left text-body focus-ring-inset transition-colors",
          current ? "bg-accent" : "hover:bg-row-hover",
        )}
      >
        <span className="numeric w-10 shrink-0 font-mono font-medium">#{instance.id}</span>
        <span className="numeric w-20 shrink-0 truncate font-mono text-hint text-muted-foreground">
          {instance.pid > 0 ? `PID ${instance.pid}` : "no PID"}
        </span>
        <span className="flex w-24 shrink-0 items-center gap-2">
          <MiniBar value={online ? Math.min(instance.cpu, 100) : 0} color={HUE.cpu} />
          <span className="numeric font-mono text-hint text-muted-foreground">
            {online ? percent(instance.cpu, 0) : "—"}
          </span>
        </span>
        <span className="flex w-28 shrink-0 items-center gap-2 max-sm:hidden">
          <MiniBar value={online ? (instance.memory / heaviest) * 100 : 0} color={HUE.mem} />
          <span className="numeric font-mono text-hint text-muted-foreground">
            {online ? bytes(instance.memory, 0) : "—"}
          </span>
        </span>
        <span className="ml-auto flex shrink-0 items-center gap-3">
          {instance.restarts > 0 && (
            <span className="numeric text-hint text-muted-foreground">
              {plural(instance.restarts, "restart")}
            </span>
          )}
          <Status state={instance.status} />
        </span>
      </button>
    </li>
  )
}

/**
 * How many workers a cluster runs: a stepper over the workers themselves,
 * the ones that stay, the ones that will start and the ones that will stop,
 * so the change is seen before it is made — and the host's core count one
 * press away, because one worker per core is the answer most of the time.
 */
function ScaleDialog({
  process,
  onClose,
  onChanged,
}: {
  process: PM2Process
  onClose: () => void
  onChanged: () => void
}) {
  const { snapshot } = useMetrics()
  const coreCount = snapshot?.cpu?.cores || snapshot?.cpu?.perCore?.length || 0
  // Mounted only while open, so the count starts from the live figure each time.
  const current = process.instances || 1
  const [count, setCount] = useState(current)
  const [busy, setBusy] = useState(false)
  const set = (value: number) => setCount(Math.max(1, Math.min(128, Math.round(value))))
  const change = count - current

  const submit = async () => {
    setBusy(true)
    try {
      await post(
        `/pm2/${encodeURIComponent(process.name)}/scale`,
        { instances: count },
        { query: { user: process.daemonId, id: process.id } },
      )
      notify.success(`${process.name} scaled to ${plural(count, "worker")}`)
      onChanged()
      onClose()
    } catch (err) {
      notify.error(`Could not scale ${process.name}`, err)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal
      open
      onOpenChange={(open) => !open && onClose()}
      title={
        <span className="flex min-w-0 items-center gap-2">
          <ProductLogo id={pm2Product(process.interpreter)} size="sm" fallback={ChartActivity} />
          <span className="truncate">Scale {process.name}</span>
        </span>
      }
      description="Set how many cluster workers run"
      size="sm"
      footer={
        <>
          <span className="mr-auto text-hint text-muted-foreground">
            {change === 0
              ? "No change"
              : change > 0
                ? `Starts ${plural(change, "worker")}; the running ones keep serving.`
                : `Stops ${plural(-change, "worker")}; the rest keep serving.`}
          </span>
          <Button variant="outline" size="sm" onClick={onClose}>
            Cancel
          </Button>
          <Button size="sm" disabled={change === 0 || busy} onClick={() => void submit()}>
            {busy ? "Scaling…" : `Scale to ${count}`}
          </Button>
        </>
      }
    >
      <div className="space-y-5">
        <div className="flex items-center justify-center gap-4">
          <Button
            size="icon"
            variant="outline"
            aria-label="One worker fewer"
            disabled={count <= 1 || busy}
            onClick={() => set(count - 1)}
          >
            <Minus className="size-4" />
          </Button>
          <label className="flex flex-col items-center">
            <span className="sr-only">Workers</span>
            <input
              type="number"
              min={1}
              max={128}
              value={count}
              onChange={(event) => set(Number(event.target.value) || 1)}
              className="numeric w-24 [appearance:textfield] rounded-md bg-transparent text-center text-2xl font-semibold tracking-tight focus-ring [&::-webkit-inner-spin-button]:appearance-none"
            />
            <span className="text-hint text-muted-foreground">
              {count === 1 ? "worker" : "workers"} · {current} now
            </span>
          </label>
          <Button
            size="icon"
            variant="outline"
            aria-label="One worker more"
            disabled={count >= 128 || busy}
            onClick={() => set(count + 1)}
          >
            <Plus className="size-4" />
          </Button>
        </div>

        <WorkerStrip current={current} target={count} />

        {coreCount > 0 && count !== coreCount && (
          <p className="text-center text-hint text-muted-foreground">
            This host has {plural(coreCount, "core")}.{" "}
            <button
              type="button"
              className="font-medium text-foreground underline-offset-2 hover:underline"
              onClick={() => set(coreCount)}
            >
              One worker per core
            </button>
          </p>
        )}
      </div>
    </Modal>
  )
}

/**
 * The workers as cells: running and staying, about to start (outlined in the
 * command's blue, rising as they are added), about to stop (struck). Past
 * thirty-two the cells would be specks, so the strip says the number instead.
 */
function WorkerStrip({ current, target }: { current: number; target: number }) {
  const cells = Math.max(current, target)
  if (cells > 32) return null
  return (
    <div
      role="img"
      aria-label={`${current} workers now, ${target} after`}
      className="flex flex-wrap justify-center gap-1.5"
    >
      {Array.from({ length: cells }, (_, index) => {
        const kept = index < Math.min(current, target)
        const added = index >= current
        return (
          <span
            key={index}
            className={cn(
              "h-6 w-3 rounded-sm transition-colors",
              kept && "bg-chart-1",
              added && "animate-rise border border-dashed border-brand bg-brand/10",
              !kept && !added && "border border-hairline bg-muted-foreground/20 opacity-50",
            )}
          />
        )
      })}
    </div>
  )
}

/**
 * What the application printed, read the way the logs page reads it: both
 * files as one stream, through the PM2 lens, with its history and what it
 * adds up to. The restarts are PM2's own count from the process record —
 * its daemon log is not read — and while it is up, the minutes around its
 * last start are one press away, which is where the crash that caused a
 * restart is.
 *
 * That press is offered only when PM2 stamps the lines (`--time`,
 * `log_date_format`). Without a stamp a line has no time to fall in a
 * window by, History keeps every such line, and "the three minutes around
 * the last start" would be the file's last lines under that name.
 */
function PM2Logs({ process }: { process: PM2Process }) {
  const id = pm2Source(process.daemonId, process.id, process.name)
  const product = pm2Product(process.interpreter)
  const sources = useMemo<ServiceLogSource[]>(
    () => [{ id, label: process.name, kind: "pm2", product }],
    [id, process.name, product],
  )
  // Fixed when pressed: the record's uptime is re-read every few seconds,
  // and a window that moved with it would run History again each time.
  const [around, setAround] = useState<LogWindow>()
  const aroundLastStart = () => {
    if (around) {
      setAround(undefined)
      return
    }
    // On whole seconds, which is what the window's fields show.
    const started = Math.round((Date.now() - process.uptimeMs) / 1000) * 1000
    setAround({
      since: new Date(started - BEFORE_START).toISOString(),
      until: new Date(started + AFTER_START).toISOString(),
    })
  }
  return (
    <>
      <div className="flex min-w-0 shrink-0 flex-wrap items-center gap-x-3 gap-y-1.5 text-hint text-muted-foreground">
        <span className="numeric">
          {process.restarts > 0
            ? `Restarted ${plural(process.restarts, "time")}`
            : "Never restarted"}
        </span>
        {process.unstableRestarts > 0 && (
          <span className="numeric font-medium text-destructive">
            {process.unstableRestarts} unstable
          </span>
        )}
        {process.uptimeMs > 0 && (
          <span className="numeric">up {duration(process.uptimeMs / 1000)}</span>
        )}
        {process.logTimes && (process.uptimeMs > 0 || around) && (
          <FilterChip selected={Boolean(around)} onClick={aroundLastStart} className="ml-auto">
            Around the last start
          </FilterChip>
        )}
      </div>
      <ServiceLogs
        sources={sources}
        layout="sheet"
        window={around}
        // Live pressed while the window is open lets the window go.
        onLeaveWindow={() => setAround(undefined)}
        className="min-h-0 flex-1"
        paneClassName="min-h-80"
      />
    </>
  )
}
