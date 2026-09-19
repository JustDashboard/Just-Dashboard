"use client"

import { useCallback, useState } from "react"
import { useViewState } from "@/lib/view-state"
import Link from "next/link"
import type { JournalEntry, LogLine, SystemdUnit, SystemdUnitDetail } from "@/lib/types"
import { useSocket, type Envelope } from "@/hooks/use-socket"
import { usePoll } from "@/hooks/use-poll"
import { get } from "@/lib/api"
import { bytes, relativeTime, timestamp } from "@/lib/format"
import { useConfirm } from "@/components/confirm-dialog"
import { LogViewer } from "@/components/log-viewer"
import { Detail, DetailList } from "@/components/page"
import { Panel, PanelBody, PanelHeader, Well } from "@/components/panel"
import { SidePanel } from "@/components/side-panel"
import { ErrorState, LoadingRows } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { VerbBar } from "@/components/verbs"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { useUnitControl, useUnitVerbs } from "@/components/procs/unit-actions"

const LOG_LIMIT = 5000

/** syslog priorities, mapped onto the viewer's level vocabulary. */
function levelFor(priority: number): string {
  if (priority <= 2) return "critical"
  if (priority === 3) return "error"
  if (priority === 4) return "warn"
  if (priority <= 6) return "info"
  return "debug"
}

/**
 * One unit, opened: its state, how it runs, and its journal. The verbs sit in
 * the header, and here — where `systemctl show` has said whether the unit
 * takes a reload — the reload verb is offered too.
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
    5000,
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
      title={unit ?? "Unit"}
      description="Service state, configuration and live journal"
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
      bodyClassName="flex min-h-0 flex-1 flex-col p-4"
    >
      {unit && (
        <Tabs value={tab} onValueChange={setTab} className="flex min-h-0 flex-1 flex-col gap-4">
          <TabsList className="w-fit shrink-0">
            <TabsTrigger value="overview">Overview</TabsTrigger>
            <TabsTrigger value="journal">Journal</TabsTrigger>
          </TabsList>
          <TabsContent value="overview" className="min-h-0 flex-1 overflow-y-auto">
            {detail.loading && !detail.data && <LoadingRows rows={6} />}
            {detail.error && !detail.data && <ErrorState error={detail.error} />}
            {detail.data && <UnitOverview detail={detail.data} />}
          </TabsContent>
          <TabsContent value="journal" className="flex min-h-0 flex-1 flex-col">
            {/* Keyed on the unit so switching units starts a clean buffer. */}
            <JournalStream key={unit} unit={unit} />
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
  return (
    <>
      <Status
        state={busy ? "activating" : unit.activeState}
        label={busy ? `${busy}…` : `${unit.activeState} (${unit.subState})`}
      />
      <VerbBar verbs={verbs} />
    </>
  )
}

function UnitOverview({ detail }: { detail: SystemdUnitDetail }) {
  const { unit: service, properties } = detail
  const started = service.activeSince
    ? new Date(service.activeSince * 1000).toISOString()
    : undefined
  const startup = startupSummary(service.unitFileState)
  const restart = restartSummary(properties.Restart)
  return (
    <div className="flex animate-rise flex-col gap-6">
      {service.description && (
        <p className="text-body text-muted-foreground">{service.description}</p>
      )}

      <Panel plain>
        <PanelHeader title="State" />
        <PanelBody>
          <DetailList>
            <Detail label="Startup">
              <Tag>{startup.label}</Tag>
              <span className="block text-hint text-muted-foreground">{startup.hint}</span>
            </Detail>
            <Detail label="Active since">
              {started ? (
                <>
                  {relativeTime(started)}
                  <span className="block text-hint text-muted-foreground">
                    {timestamp(started)}
                  </span>
                </>
              ) : (
                "—"
              )}
            </Detail>
            <Detail label="Last result">{service.result || "—"}</Detail>
            <Detail label="Restarts" className="numeric">
              {service.restarts ?? 0}
            </Detail>
            <Detail label="Main PID" className="font-mono">
              {service.mainPid ? (
                <Link href={`/processes?pid=${service.mainPid}`} className="hover:underline">
                  {service.mainPid}
                </Link>
              ) : (
                "—"
              )}
            </Detail>
            <Detail label="Memory">{bytes(service.memoryBytes)}</Detail>
            <Detail label="Tasks" className="numeric">
              {service.tasks ?? "—"}
            </Detail>
          </DetailList>
        </PanelBody>
      </Panel>

      <Panel plain>
        <PanelHeader title="How it runs" />
        <PanelBody className="space-y-4">
          <DetailList>
            <Detail label="Runs as">
              <span className="font-mono">{properties.User || "root"}</span>
              <span className="text-muted-foreground"> : {properties.Group || "default"}</span>
            </Detail>
            <Detail label="Working directory" className="font-mono break-all">
              {properties.WorkingDirectory || "Not set"}
            </Detail>
            <Detail label="Restart policy">
              {restart.label}
              <span className="block text-hint text-muted-foreground">{restart.hint}</span>
            </Detail>
            <Detail label="Reload">
              {properties.CanReload === "yes"
                ? "Re-reads its configuration without stopping"
                : "Not supported — a restart is the only way to apply changes"}
            </Detail>
            <Detail label="Memory limit">
              {properties.MemoryMax && properties.MemoryMax !== "infinity"
                ? bytes(Number(properties.MemoryMax))
                : "No limit"}
            </Detail>
            <Detail label="Task limit">{properties.TasksMax || "No limit"}</Detail>
            <Detail label="Unit file" className="font-mono break-all">
              {service.fragmentPath ? (
                <Link
                  href={`/files?path=${encodeURIComponent(service.fragmentPath)}`}
                  className="hover:underline"
                >
                  {service.fragmentPath}
                </Link>
              ) : (
                "—"
              )}
            </Detail>
          </DetailList>
          {properties.ExecStart && (
            <div className="space-y-1.5">
              <p className="eyebrow">Command</p>
              <Well className="max-h-36 whitespace-pre-wrap">{properties.ExecStart}</Well>
            </div>
          )}
        </PanelBody>
      </Panel>
    </div>
  )
}

/** What the startup state means for the next boot, in words. */
function startupSummary(state: string | undefined): { label: string; hint: string } {
  switch (state) {
    case "enabled":
    case "enabled-runtime":
      return { label: state, hint: "Starts automatically on boot" }
    case "disabled":
      return { label: state, hint: "Only runs when started by hand" }
    case "static":
      return { label: state, hint: "Started by another unit, not on its own" }
    case "masked":
      return { label: state, hint: "Blocked from starting at all" }
    default:
      return { label: state || "unknown", hint: "Startup state was not reported" }
  }
}

/** What the restart policy does the next time the process exits. */
function restartSummary(policy: string | undefined): { label: string; hint: string } {
  switch ((policy || "no").toLowerCase()) {
    case "always":
      return { label: policy!, hint: "Restarts after every exit" }
    case "on-failure":
      return { label: policy!, hint: "Restarts after crashes, not clean exits" }
    case "on-abnormal":
    case "on-abort":
    case "on-watchdog":
      return { label: policy!, hint: "Restarts after abnormal exits only" }
    default:
      return { label: policy || "no", hint: "Stays stopped until started again" }
  }
}

function JournalStream({ unit }: { unit: string }) {
  const [lines, setLines] = useState<LogLine[]>([])
  const [failed, setFailed] = useState<string | null>(null)

  const onMessage = useCallback((envelope: Envelope) => {
    // The stream was empty on every unit because the server read the journal
    // from inside its own container, where only the previous boot's flushed
    // entries are visible. It now reads the host's live journal, so anything
    // still empty here is genuinely quiet — the message below says which.
    if (envelope.type === "error" || envelope.error) {
      setFailed(envelope.error || "The journal stream closed with an error.")
      return
    }
    if (envelope.type !== "journal") return
    const batch = envelope.data as JournalEntry[]
    setLines((prev) => {
      const next = [
        ...prev,
        ...batch.map((e) => ({
          text: e.message,
          level: levelFor(e.priority),
          timestamp: e.timestamp,
          source: e.syslogIdentifier,
        })),
      ]
      return next.length > LOG_LIMIT ? next.slice(next.length - LOG_LIMIT) : next
    })
  }, [])

  const { state } = useSocket(`/systemd/${encodeURIComponent(unit)}/journal/stream`, {
    onMessage,
    query: { lines: 300 },
  })

  if (failed) return <ErrorState error={new Error(failed)} />
  return (
    <LogViewer
      className="h-full min-h-80"
      lines={lines}
      onClear={() => setLines([])}
      toolbar={
        <Status
          state={state}
          live={state === "open"}
          label={state === "open" ? "Live" : state === "connecting" ? "Connecting" : "Reconnecting"}
        />
      }
      emptyMessage={
        state === "open"
          ? `No journal entries for ${unit} yet — a quiet unit logs nothing.`
          : "Connecting to the journal…"
      }
    />
  )
}
