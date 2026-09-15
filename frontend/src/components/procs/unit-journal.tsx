"use client"

import { useCallback, useState } from "react"
import type { JournalEntry, LogLine, SystemdUnitDetail } from "@/lib/types"
import { useSocket, type Envelope } from "@/hooks/use-socket"
import { usePoll } from "@/hooks/use-poll"
import { get } from "@/lib/api"
import { bytes, relativeTime, timestamp } from "@/lib/format"
import { LogViewer } from "@/components/log-viewer"
import { Detail, DetailList } from "@/components/page"
import { PaneHeader, Panel, PanelBody, PanelHeader, Well } from "@/components/panel"
import { SidePanel } from "@/components/side-panel"
import { ErrorState, LoadingPanel } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"

const LOG_LIMIT = 5000

/** syslog priorities, mapped onto the viewer's level vocabulary. */
function levelFor(priority: number): string {
  if (priority <= 2) return "critical"
  if (priority === 3) return "error"
  if (priority === 4) return "warn"
  if (priority <= 6) return "info"
  return "debug"
}

export function UnitJournalSheet({
  unit,
  onOpenChange,
}: {
  unit: string | null
  onOpenChange: (open: boolean) => void
}) {
  return (
    <SidePanel
      open={unit !== null}
      onOpenChange={onOpenChange}
      title={unit ?? "Unit"}
      description="Service state, configuration and live journal"
      bodyClassName="flex min-h-0 flex-1 flex-col"
    >
      {unit && (
        <Tabs defaultValue="overview" className="min-h-0 flex-1 gap-0">
          <PaneHeader className="px-4">
            <TabsList>
              <TabsTrigger value="overview">Overview</TabsTrigger>
              <TabsTrigger value="journal">Journal</TabsTrigger>
            </TabsList>
          </PaneHeader>
          <TabsContent value="overview" className="min-h-0 overflow-y-auto p-4">
            <UnitOverview unit={unit} />
          </TabsContent>
          <TabsContent value="journal" className="min-h-0 flex-1 p-4">
            {/* Keyed on the unit so switching units starts a clean buffer. */}
            <JournalStream key={unit} unit={unit} />
          </TabsContent>
        </Tabs>
      )}
    </SidePanel>
  )
}

function UnitOverview({ unit }: { unit: string }) {
  const detail = usePoll(
    (signal) => get<SystemdUnitDetail>(`/systemd/${encodeURIComponent(unit)}`, undefined, signal),
    5000,
    [unit],
  )
  if (detail.loading) return <LoadingPanel />
  if (detail.error) return <ErrorState error={detail.error} />
  if (!detail.data) return null

  const { unit: service, properties } = detail.data
  const started = service.activeSince
    ? new Date(service.activeSince * 1000).toISOString()
    : undefined
  return (
    <div className="flex flex-col gap-4">
      <Panel>
        <PanelHeader
          title="Service"
          actions={
            <Status
              state={service.activeState}
              label={`${service.activeState} (${service.subState})`}
            />
          }
        />
        <PanelBody className="space-y-5">
          <section className="min-w-0 space-y-2">
            <p className="eyebrow">State</p>
            <DetailList>
              <Detail label="Startup">
                {startupSummary(service.unitFileState).label}
                <span className="block text-hint text-muted-foreground">
                  {startupSummary(service.unitFileState).hint}
                </span>
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
              <Detail label="Restarts">{service.restarts ?? 0}</Detail>
            </DetailList>
          </section>
          <section className="min-w-0 space-y-2">
            <p className="eyebrow">Footprint</p>
            <DetailList>
              <Detail label="Main PID" className="font-mono">
                {service.mainPid || "—"}
              </Detail>
              <Detail label="Memory">{bytes(service.memoryBytes)}</Detail>
              <Detail label="Tasks">{service.tasks ?? "—"}</Detail>
            </DetailList>
          </section>
        </PanelBody>
      </Panel>
      <Panel>
        <PanelHeader title="How it runs" />
        <PanelBody className="space-y-2">
          <DetailList>
            <Detail label="Runs as">
              <span className="font-mono">{properties.User || "root"}</span>
              <span className="text-muted-foreground"> : {properties.Group || "default"}</span>
            </Detail>
            <Detail label="Working directory" className="font-mono break-all">
              {properties.WorkingDirectory || "Not set"}
            </Detail>
            <Detail label="Restart policy">
              {restartSummary(properties.Restart).label}
              <span className="block text-hint text-muted-foreground">
                {restartSummary(properties.Restart).hint}
              </span>
            </Detail>
            <Detail label="Memory limit">
              {properties.MemoryMax && properties.MemoryMax !== "infinity"
                ? bytes(Number(properties.MemoryMax))
                : "No limit"}
            </Detail>
            <Detail label="Task limit">{properties.TasksMax || "No limit"}</Detail>
          </DetailList>
          {properties.ExecStart && (
            <div className="space-y-1">
              <p className="eyebrow">Command</p>
              <Well className="max-h-36 font-mono text-xs whitespace-pre-wrap">
                {properties.ExecStart}
              </Well>
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
      emptyMessage={
        state === "open"
          ? `No journal entries for ${unit} yet — a quiet unit logs nothing.`
          : "Connecting to the journal…"
      }
    />
  )
}
