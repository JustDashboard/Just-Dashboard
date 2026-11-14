"use client"

import { useMemo, useState } from "react"
import { ChartActivity, FileText } from "@/components/icons"
import { bytes, duration, percent, plural } from "@/lib/format"
import { pm2Source } from "@/lib/log-sources"
import type { PM2Process, ProcessRow } from "@/lib/types"
import { cn } from "@/lib/utils"
import { useAuth } from "@/hooks/use-auth"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import { ServiceLogs, type LogWindow, type ServiceLogSource } from "@/components/logs/service-logs"
import { Section } from "@/components/page"
import { Panel, PanelHeader } from "@/components/panel"
import { ProductLogo, pm2Product } from "@/components/product-logo"
import { EmptyNote, EmptyState, Notice } from "@/components/state"
import { Status, StatusDot } from "@/components/status-dot"
import { FilterChip } from "@/components/tabs"
import { VerbMenu, type Verb } from "@/components/verbs"
import { Button } from "@/components/ui/button"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { usePM2Verbs, type ConfirmFn, type PendingMap } from "@/components/procs/pm2-actions"
import { pm2Lead, pm2RestartWords, pm2VerbWords } from "@/components/procs/pm2-facts"
import {
  isCluster,
  memoryLimitShare,
  pm2Failing,
  pm2Key,
  type PM2App,
} from "@/components/procs/pm2-shared"
import { useProcessInspect } from "@/components/procs/process-detail"
import { processHref } from "@/components/procs/process-facts"
import { ProcessEnvironment } from "@/components/procs/process-views"
import { cpuTone } from "@/components/procs/shared"

/**
 * The stretch "around the last start" covers: the crash before it — PM2's
 * backoff waits up to fifteen seconds before trying again — and the start.
 */
const BEFORE_START = 2 * 60_000
const AFTER_START = 60_000

type WorkerAct = Parameters<typeof usePM2Verbs>[0]["act"]

/**
 * A cluster's workers, each a card that opens its process's own page — where
 * its threads, files, sockets and limits are — with what it is doing on the
 * card: its PID and uptime under its number, then its processor, its memory
 * (in the limit's tone when it nears the one PM2 restarts it at), its
 * restarts and its state in columns that line up down the list.
 *
 * A worker's own verbs are in its menu. They reach that worker alone, which
 * is what the page's own verbs do not: restarting one stuck worker of four is
 * a different act from restarting the application.
 */
export function PM2AppInstances({
  app,
  rows,
  now,
  pending,
  confirm,
  act,
  onLogs,
  onScale,
  onChanged,
}: {
  app: PM2App
  rows: (ProcessRow | null)[]
  now: number
  pending: PendingMap
  confirm: ConfirmFn
  act: WorkerAct
  /** Opens the log on one worker's own stream. */
  onLogs: (worker: PM2Process) => void
  onScale: () => void
  onChanged: () => void
}) {
  const { can } = useAuth()
  const workers = useMemo(() => [...app.instances].sort((a, b) => a.id - b.id), [app.instances])
  return (
    <Panel plain className="gap-4 pb-6">
      <PanelHeader
        title={
          <>
            Workers
            <span className="numeric ml-2 text-body font-normal text-muted-foreground">
              {workers.length}
            </span>
          </>
        }
        actions={
          <>
            <span
              className={cn(
                "numeric text-hint",
                app.online < workers.length ? "text-warning" : "text-muted-foreground",
              )}
            >
              {app.online} of {workers.length} online
            </span>
            {can("service.control") && isCluster(workers[0]) && (
              <Button size="xs" variant="outline" onClick={onScale}>
                Scale…
              </Button>
            )}
          </>
        }
      />
      <ChoiceList aria-label="Workers">
        {workers.map((worker, index) => (
          <WorkerRow
            key={pm2Key(worker)}
            worker={worker}
            position={index + 1}
            row={rows.find((r) => r?.pid === worker.pid && worker.pid > 0) ?? undefined}
            now={now}
            busy={pending[pm2Key(worker)]}
            confirm={confirm}
            act={act}
            onLogs={() => onLogs(worker)}
            onScale={onScale}
            onChanged={onChanged}
          />
        ))}
      </ChoiceList>
    </Panel>
  )
}

function WorkerRow({
  worker,
  position,
  row,
  now,
  busy,
  confirm,
  act,
  onLogs,
  onScale,
  onChanged,
}: {
  worker: PM2Process
  position: number
  row?: ProcessRow
  now: number
  busy?: string
  confirm: ConfirmFn
  act: WorkerAct
  onLogs: () => void
  onScale: () => void
  onChanged: () => void
}) {
  const verbs = usePM2Verbs({
    process: worker,
    confirm,
    act,
    onOpenTab: onLogs,
    onScale,
    onChanged,
  })
  const online = worker.status === "online"
  const cpu = row && row.cpuReady !== false ? row.cpuPercent : worker.cpu
  const rss = row?.rss ?? worker.memory
  const share = memoryLimitShare({ memory: rss, maxMemoryRestart: worker.maxMemoryRestart })
  const started = row ? Date.parse(row.createTime) : now - worker.uptimeMs
  const up = online && worker.uptimeMs > 0 ? Math.max(0, (now - started) / 1000) : undefined
  return (
    <ChoiceRow
      workspaceItem={{ id: pm2Key(worker), name: `worker ${position}` }}
      href={worker.pid > 0 ? processHref(worker.pid) : undefined}
      disabled={worker.pid <= 0}
      verb={`Open worker ${position}, PID ${worker.pid}`}
      leading={
        <span className="relative flex shrink-0">
          <ProductLogo id={pm2Product(worker.interpreter)} size="sm" fallback={ChartActivity} />
          <span
            aria-hidden
            className="absolute -right-0.5 -bottom-0.5 flex size-3 items-center justify-center rounded-full bg-choice-surface"
          >
            <StatusDot state={worker.status} className="size-1.5" />
          </span>
        </span>
      }
      title={
        <>
          Worker {position}{" "}
          <span className="numeric font-mono text-hint font-normal text-muted-foreground">
            #{worker.id}
          </span>
        </>
      }
      description={
        <span className="numeric font-mono">
          {worker.pid > 0 ? `PID ${worker.pid}` : "no process"}
          {up !== undefined && <span className="font-sans"> · up {duration(up)}</span>}
          {!online && pm2Failing(worker) && worker.unstableRestarts > 0 && (
            <span className="font-sans"> · PM2 stopped retrying</span>
          )}
        </span>
      }
      trailing={
        <span className="flex items-center gap-4 text-hint">
          <span
            className={cn(
              "numeric w-14 text-right font-mono max-sm:hidden",
              !online || cpuTone(cpu) === "default"
                ? "text-muted-foreground"
                : cpuTone(cpu) === "danger"
                  ? "font-medium text-destructive"
                  : "text-warning",
            )}
          >
            {online ? percent(cpu) : "—"}
          </span>
          <span
            title={
              online && share !== undefined
                ? `${percent(share, 0)} of the ${bytes(worker.maxMemoryRestart, 0)} PM2 restarts it at`
                : undefined
            }
            className={cn(
              "numeric w-20 text-right font-mono max-sm:hidden",
              !online || share === undefined || share < 80
                ? "text-muted-foreground"
                : share >= 95
                  ? "text-destructive"
                  : "text-warning",
            )}
          >
            {online ? bytes(rss) : "—"}
          </span>
          <span
            className={cn(
              "numeric w-24 text-right max-sm:hidden",
              worker.unstableRestarts > 0 ? "text-destructive" : "text-muted-foreground",
            )}
          >
            {worker.unstableRestarts > 0
              ? `${worker.unstableRestarts} unstable`
              : plural(worker.restarts, "restart")}
          </span>
          <span className="flex w-20 items-center">
            {busy ? (
              <span className="inline-flex items-center gap-1.5 text-xs font-medium">
                <StatusDot tone="notice" />
                <TextShimmer>{`${busy}…`}</TextShimmer>
              </span>
            ) : (
              <Status state={worker.status} />
            )}
          </span>
        </span>
      }
      actions={<VerbMenu verbs={verbs} label={`More for worker ${position}`} />}
    />
  )
}

/**
 * What the application printed, read the way the logs page reads it: each
 * worker's files as one stream through the PM2 lens, with its history and
 * what it adds up to; a cluster picks the worker. The restarts are PM2's own
 * count from the process record — its daemon log is not read — and while a
 * worker is up, the minutes around its last start are one press away, which
 * is where the crash that caused a restart is.
 *
 * That press is offered only when PM2 stamps the lines (`--time`,
 * `log_date_format`). Without a stamp a line has no time to fall in a
 * window by, History keeps every such line, and "the three minutes around
 * the last start" would be the file's last lines under that name.
 *
 * A daemon whose files are outside the log roots opens nothing: the server
 * would refuse the read, and a socket retrying a refusal once a second is
 * noise under a sentence that already says why.
 */
export function PM2AppLogs({ app, worker }: { app: PM2App; worker: number | null }) {
  const lead = pm2Lead(app)
  const several = app.instances.length > 1
  const product = pm2Product(app.interpreter)
  const sources = useMemo<ServiceLogSource[]>(
    () =>
      [...app.instances]
        .sort((a, b) => a.id - b.id)
        .map((p) => ({
          id: pm2Source(p.daemonId, p.id, p.name),
          label: several ? `${p.name} #${p.id}` : p.name,
          kind: "pm2",
          status: p.status,
          product,
        })),
    [app.instances, several, product],
  )
  const asked = app.instances.find((p) => p.id === worker)
  const arrival = asked ? pm2Source(asked.daemonId, asked.id, asked.name) : null
  const [picked, setPicked] = useState<string | null>(arrival)
  const reading =
    app.instances.find((p) => pm2Source(p.daemonId, p.id, p.name) === picked) ?? asked ?? lead
  // Fixed when pressed: the record's uptime is re-read every few seconds,
  // and a window that moved with it would run History again each time.
  const [around, setAround] = useState<LogWindow>()
  const aroundLastStart = () => {
    if (around) {
      setAround(undefined)
      return
    }
    // On whole seconds, which is what the window's fields show.
    const started = Math.round((Date.now() - reading.uptimeMs) / 1000) * 1000
    setAround({
      since: new Date(started - BEFORE_START).toISOString(),
      until: new Date(started + AFTER_START).toISOString(),
    })
  }

  if (lead.logsAvailable === false) {
    return (
      <Notice title="Logs unavailable">
        {lead.logsUnavailableReason ||
          "The daemon's log files are outside the configured log roots."}
      </Notice>
    )
  }
  return (
    <div className="flex h-full min-h-0 flex-col gap-3">
      <div className="flex min-w-0 shrink-0 flex-wrap items-center gap-x-3 gap-y-1.5 text-hint text-muted-foreground">
        <span className="numeric">{pm2RestartWords(reading.restarts)}</span>
        {reading.unstableRestarts > 0 && (
          <span className="numeric font-medium text-destructive">
            {reading.unstableRestarts} unstable
          </span>
        )}
        {reading.uptimeMs > 0 && (
          <span className="numeric">up {duration(reading.uptimeMs / 1000)}</span>
        )}
        {reading.logTimes && (reading.uptimeMs > 0 || around) && (
          <FilterChip selected={Boolean(around)} onClick={aroundLastStart} className="ml-auto">
            Around the last start
          </FilterChip>
        )}
      </div>
      <ServiceLogs
        sources={sources}
        source={arrival}
        onSourceChange={(id) => {
          setPicked(id)
          // Another worker started at another moment.
          setAround(undefined)
        }}
        pickerLabel="Worker"
        storageKey={`processes.pm2.app.logs.${app.key}`}
        window={around}
        // Live pressed while the window is open lets the window go.
        onLeaveWindow={() => setAround(undefined)}
        className="min-h-0 flex-1"
        paneClassName="min-h-[30rem]"
      />
    </div>
  )
}

/**
 * The environment the application runs with, read from its running process:
 * PM2's own record of it is where the secrets are, and the process table
 * already masks credential-shaped values for an account that is not an
 * administrator. A cluster's workers are given the same environment apart
 * from their instance number, so the first running one is read.
 */
export function PM2AppEnvironment({ app, row }: { app: PM2App; row?: ProcessRow }) {
  const lead = pm2Lead(app)
  const live = app.online > 0 && lead.pid > 0
  const inspect = useProcessInspect(row, live)
  if (!live) {
    return (
      <EmptyState
        icon={FileText}
        title="It is not running"
        description="Its environment is read from the running process. Start it to see what it was given."
      />
    )
  }
  if (!row) return <EmptyNote>Reading its environment…</EmptyNote>
  return (
    <div className="flex min-w-0 flex-col gap-3 pb-6">
      {app.instances.length > 1 && (
        <p className="text-hint text-muted-foreground">
          As worker <span className="numeric font-mono">#{lead.id}</span> has it. Every worker is
          given the same, apart from its own instance number.
        </p>
      )}
      <ProcessEnvironment inspect={inspect.data} />
    </div>
  )
}

const NOW_VERBS = ["start", "reload", "restart", "stop"]
const KEEP_VERBS = ["scale", "reset", "flush", "delete"]

/**
 * Every thing that can be done to the application, each said before it is
 * done: running it now, whether a reboot brings it back, and the
 * housekeeping. The verbs are the page's own — they reach every worker.
 */
export function PM2AppControl({
  app,
  verbs,
  pending,
  boot,
  onSave,
}: {
  app: PM2App
  verbs: Verb[]
  pending?: string
  /** Whether a reboot brings it back, in a few words, and in what tone. */
  boot: { word: string; ok: boolean }
  onSave?: () => void
}) {
  const cluster = isCluster(app.instances[0])
  const now = verbs.filter((verb) => NOW_VERBS.includes(verb.key))
  const keep = verbs.filter((verb) => KEEP_VERBS.includes(verb.key))
  return (
    <div className="flex min-w-0 flex-col gap-8 pb-6">
      <div className="grid min-w-0 gap-8 xl:grid-cols-2 [&>*]:min-w-0">
        <Section title="Now">
          <ul aria-label="Run it" className="-mx-2 flex min-w-0 flex-col">
            {now.map((verb) => (
              <VerbRow key={verb.key} verb={verb} cluster={cluster} pending={pending} />
            ))}
          </ul>
        </Section>
        <Section title="On boot">
          <ul aria-label="Startup" className="-mx-2 flex min-w-0 flex-col">
            <li className="flex h-14 min-w-0 items-center gap-3 rounded-md px-2">
              <div className="min-w-0 flex-1">
                <p className="truncate text-body font-medium">Startup list</p>
                <p
                  className={cn(
                    "truncate text-hint",
                    boot.ok ? "text-muted-foreground" : "text-warning",
                  )}
                >
                  {boot.word}
                </p>
              </div>
              {onSave && (
                <Button size="xs" variant="outline" onClick={onSave}>
                  Save
                </Button>
              )}
            </li>
          </ul>
        </Section>
      </div>
      {keep.length > 0 && (
        <Section title="Housekeeping">
          <ul aria-label="Housekeeping" className="-mx-2 flex min-w-0 flex-col">
            {keep.map((verb) => (
              <VerbRow key={verb.key} verb={verb} cluster={cluster} pending={pending} />
            ))}
          </ul>
        </Section>
      )}
    </div>
  )
}

/** A verb as a row with what it does, for a list where each is read before it is pressed. */
function VerbRow({ verb, cluster, pending }: { verb: Verb; cluster: boolean; pending?: string }) {
  const Icon = verb.icon
  const words = pm2VerbWords(verb.key, cluster)
  return (
    <li className="flex h-14 min-w-0 items-center gap-3 rounded-md px-2 transition-colors hover:bg-row-hover">
      <span className="flex size-7 shrink-0 items-center justify-center rounded-md bg-muted text-muted-foreground">
        <Icon className="size-3.5" />
      </span>
      <div className="min-w-0 flex-1">
        <p className="truncate text-body font-medium">{verb.label}</p>
        <p className="truncate text-hint text-muted-foreground" title={words}>
          {words}
        </p>
      </div>
      <Button
        size="xs"
        variant="outline"
        disabled={Boolean(pending)}
        className={cn((verb.key === "stop" || verb.key === "delete") && "text-destructive")}
        onClick={verb.run}
      >
        {verb.label.split(" ")[0].replace("…", "")}
      </Button>
    </li>
  )
}
