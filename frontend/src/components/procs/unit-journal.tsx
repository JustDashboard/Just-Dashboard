"use client"

import Link from "next/link"
import { useCallback, useState } from "react"
import type { JournalEntry, LogLine, SystemdUnitDetail } from "@/lib/types"
import { useSocket, type Envelope } from "@/hooks/use-socket"
import { usePoll } from "@/hooks/use-poll"
import { get } from "@/lib/api"
import { bytes, relativeTime } from "@/lib/format"
import { ListOrdered } from "@/components/icons"
import { LogViewer } from "@/components/log-viewer"
import { Detail, DetailList } from "@/components/page"
import { PaneHeader, Panel, PanelBody, PanelHeader, Well } from "@/components/panel"
import { SidePanel } from "@/components/side-panel"
import { ErrorState, LoadingPanel } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
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
      icon={ListOrdered}
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
        <PanelBody>
          <DetailList>
            <Detail label="Startup">{service.unitFileState || "unknown"}</Detail>
            <Detail label="Main PID" className="font-mono">
              {service.mainPid || "—"}
            </Detail>
            <Detail label="Active since">{relativeTime(started)}</Detail>
            <Detail label="Memory">{bytes(service.memoryBytes)}</Detail>
            <Detail label="Tasks">{service.tasks ?? "—"}</Detail>
            <Detail label="Restarts">{service.restarts ?? 0}</Detail>
            <Detail label="Last result">{service.result || "—"}</Detail>
          </DetailList>
          {service.fragmentPath && (
            <Button asChild variant="outline" size="xs" className="mt-4">
              <Link href={`/files?path=${encodeURIComponent(service.fragmentPath)}`}>
                Open unit file
              </Link>
            </Button>
          )}
        </PanelBody>
      </Panel>
      <Panel>
        <PanelHeader
          title="Runtime policy"
        />
        <PanelBody>
          <DetailList>
            <Detail label="Account">{properties.User || "root"}</Detail>
            <Detail label="Group">{properties.Group || "default"}</Detail>
            <Detail label="Working directory" className="font-mono break-all">
              {properties.WorkingDirectory || "Not set"}
            </Detail>
            <Detail label="Restart policy">{properties.Restart || "no"}</Detail>
            <Detail label="Memory limit">
              {properties.MemoryMax && properties.MemoryMax !== "infinity"
                ? bytes(Number(properties.MemoryMax))
                : "No limit"}
            </Detail>
            <Detail label="Task limit">{properties.TasksMax || "No limit"}</Detail>
          </DetailList>
          {properties.ExecStart && (
            <Well className="mt-4 max-h-36 whitespace-pre-wrap">{properties.ExecStart}</Well>
          )}
        </PanelBody>
      </Panel>
    </div>
  )
}

function JournalStream({ unit }: { unit: string }) {
  const [lines, setLines] = useState<LogLine[]>([])

  const onMessage = useCallback((envelope: Envelope) => {
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

  useSocket(`/systemd/${encodeURIComponent(unit)}/journal/stream`, {
    onMessage,
    query: { lines: 300 },
  })

  return <LogViewer className="h-full min-h-80" lines={lines} onClear={() => setLines([])} />
}
