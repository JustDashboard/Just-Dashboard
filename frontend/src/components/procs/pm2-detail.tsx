"use client"

import { useMemo, useState } from "react"
import { useViewState } from "@/lib/view-state"
import Link from "next/link"
import type { PM2Process } from "@/lib/types"
import { post } from "@/lib/api"
import { bytes, duration, plural, relativeTime, timestamp } from "@/lib/format"
import { pm2Source } from "@/lib/log-sources"
import { notify } from "@/lib/toast"
import { useConfirm } from "@/components/confirm-dialog"
import { Field } from "@/components/form"
import { ServiceLogs, type LogWindow, type ServiceLogSource } from "@/components/logs/service-logs"
import { Modal } from "@/components/modal"
import { Detail, DetailList } from "@/components/page"
import { ChartActivity } from "@/components/icons"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ProductLogo, pm2Product } from "@/components/product-logo"
import { SidePanel } from "@/components/side-panel"
import { Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { FilterChip } from "@/components/tabs"
import { Tag } from "@/components/tag"
import { VerbBar } from "@/components/verbs"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { pm2Key, usePM2Control, usePM2Verbs } from "@/components/procs/pm2-actions"

/**
 * The stretch "around the last start" covers: the crash before it — PM2's
 * backoff waits up to fifteen seconds before trying again — and the start.
 */
const BEFORE_START = 2 * 60_000
const AFTER_START = 60_000

/**
 * One PM2 application, opened: how it is run, and what it has printed.
 *
 * Everything on the overview is from `pm2 jlist`, which the list already
 * holds — PM2 has no separate "describe" that says more without also handing
 * over the environment, and the environment is where the secrets are.
 */
export function PM2DetailSheet(props: {
  process: PM2Process | null
  initialTab?: string
  onOpenChange: (open: boolean) => void
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
  initialTab,
  onOpenChange,
  onChanged,
}: {
  process: PM2Process | null
  initialTab?: string
  onOpenChange: (open: boolean) => void
  onChanged: () => void
}) {
  const { confirm, dialog } = useConfirm()
  const { pending, act } = usePM2Control(onChanged)
  // Which tab an application opens on, remembered the way a container's is;
  // a caller asking for a specific tab ("open its logs") is answered first.
  const [remembered, remember] = useViewState("processes.pm2.detail.tab", "overview")
  const [tab, setTabState] = useState(initialTab ?? remembered)
  const setTab = (next: string) => {
    setTabState(next)
    remember(next)
  }
  const [scaling, setScaling] = useState(false)
  const busy = process ? pending[pm2Key(process)] : undefined

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
      bodyClassName="flex min-h-0 flex-1 flex-col p-4"
    >
      {process && (
        <Tabs value={tab} onValueChange={setTab} className="flex min-h-0 flex-1 flex-col gap-4">
          <TabsList className="w-fit shrink-0">
            <TabsTrigger value="overview">Overview</TabsTrigger>
            <TabsTrigger value="logs">Logs</TabsTrigger>
          </TabsList>
          <TabsContent value="overview" className="min-h-0 flex-1 overflow-y-auto">
            <PM2Overview process={process} />
          </TabsContent>
          <TabsContent value="logs" className="flex min-h-0 flex-1 flex-col gap-3">
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
      <Status
        state={busy ? "activating" : process.status}
        label={busy ? `${busy}…` : process.status}
      />
      <VerbBar verbs={verbs} />
    </>
  )
}

function PM2Overview({ process }: { process: PM2Process }) {
  const cluster = process.execMode === "cluster_mode" || process.execMode === "cluster"
  return (
    <div className="flex animate-rise flex-col gap-6">
      <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-body text-muted-foreground">
        <span>{process.daemonId}</span>
        <Dot />
        <span className="numeric font-mono">#{process.id}</span>
        {process.pid > 0 && (
          <>
            <Dot />
            <Link
              href={`/processes?pid=${process.pid}`}
              className="numeric font-mono hover:underline"
            >
              PID {process.pid}
            </Link>
          </>
        )}
        <Tag>{cluster ? `cluster × ${process.instances || 1}` : "fork"}</Tag>
        {process.watching && <Tag tone="warning">watching</Tag>}
      </div>

      <Panel plain>
        <PanelHeader title="Application" />
        <PanelBody>
          <DetailList>
            <Detail label="Uptime">
              {process.uptimeMs > 0 ? duration(process.uptimeMs / 1000) : "—"}
            </Detail>
            <Detail label="Restarts" className="numeric">
              {process.restarts}
              {process.unstableRestarts > 0 && (
                <span className="ml-1 text-destructive">{process.unstableRestarts} unstable</span>
              )}
            </Detail>
            <Detail label="CPU · memory" className="numeric">
              {process.cpu.toFixed(1)}% · {bytes(process.memory)}
            </Detail>
            <Detail label="Registered">
              {process.createdAtMs ? (
                <>
                  {relativeTime(new Date(process.createdAtMs).toISOString())}
                  <span className="block text-hint text-muted-foreground">
                    {timestamp(new Date(process.createdAtMs).toISOString())}
                  </span>
                </>
              ) : (
                "—"
              )}
            </Detail>
            {process.version && (
              <Detail label="Package version" className="font-mono">
                {process.version}
              </Detail>
            )}
          </DetailList>
        </PanelBody>
      </Panel>

      <Panel plain>
        <PanelHeader title="How it runs" />
        <PanelBody>
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
              {process.nodeVersion && (
                <span className="ml-1 text-muted-foreground">v{process.nodeVersion}</span>
              )}
            </Detail>
            <Detail label="Mode">
              {cluster
                ? `Cluster, ${process.instances || 1} ${process.instances === 1 ? "instance" : "instances"}`
                : "Fork, one process"}
            </Detail>
            <Detail label="On crash">
              {process.autorestart ? "Restarts automatically" : "Stays stopped"}
              {process.maxMemoryRestart ? (
                <span className="block text-hint text-muted-foreground">
                  also restarts above {bytes(process.maxMemoryRestart)}
                </span>
              ) : null}
            </Detail>
            <Detail label="Watch">{process.watching ? "Restarts when files change" : "Off"}</Detail>
            <Detail label="Runs as" className="font-mono">
              {process.user}
            </Detail>
          </DetailList>
        </PanelBody>
      </Panel>

      <Panel plain>
        <PanelHeader title="Log files" />
        <PanelBody>
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

/** How many workers a cluster runs. A number, because that is what it is. */
function ScaleDialog({
  process,
  onClose,
  onChanged,
}: {
  process: PM2Process
  onClose: () => void
  onChanged: () => void
}) {
  // Mounted only while open, so the count starts from the live figure each time.
  const [count, setCount] = useState(String(process.instances || 1))
  const [busy, setBusy] = useState(false)
  const instances = Number(count)
  const valid = Number.isInteger(instances) && instances >= 1 && instances <= 128

  const submit = async () => {
    setBusy(true)
    try {
      await post(
        `/pm2/${encodeURIComponent(process.name)}/scale`,
        { instances },
        { query: { user: process.daemonId, id: process.id } },
      )
      notify.success(`${process.name} scaled to ${instances}`)
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
      title={`Scale ${process.name}`}
      description="Set how many cluster workers run"
      size="sm"
      footer={
        <>
          <Button variant="ghost" size="sm" onClick={onClose}>
            Cancel
          </Button>
          <Button size="sm" disabled={!valid || busy} onClick={() => void submit()}>
            {busy ? "Scaling…" : "Scale"}
          </Button>
        </>
      }
    >
      <Field
        label="Instances"
        htmlFor="pm2-scale"
        hint={`${process.instances || 1} now. Workers are added or removed one at a time; nothing else restarts.`}
        error={valid ? undefined : "A whole number from 1 to 128."}
      >
        <Input
          id="pm2-scale"
          type="number"
          min={1}
          max={128}
          value={count}
          onChange={(event) => setCount(event.target.value)}
          className="w-32 font-mono"
        />
      </Field>
    </Modal>
  )
}

/**
 * What the application printed, read the way the logs page reads it: both
 * files as one stream, through the PM2 lens, with its history and what it
 * adds up to. The restarts are PM2's own count from the process record —
 * its daemon log is not read — and while it is up, the minutes around its
 * last start are one press away, which is where the crash that caused a
 * restart is.
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
        {(process.uptimeMs > 0 || around) && (
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
