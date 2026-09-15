"use client"

import { useCallback, useState } from "react"
import type { LogLine, PM2Process } from "@/lib/types"
import { useSocket, type Envelope } from "@/hooks/use-socket"
import { LogViewer } from "@/components/log-viewer"
import { SidePanel } from "@/components/side-panel"
import { Status } from "@/components/status-dot"
import { Notice } from "@/components/state"

const LOG_LIMIT = 5000

export function PM2LogSheet({
  process,
  onOpenChange,
}: {
  process: PM2Process | null
  onOpenChange: (open: boolean) => void
}) {
  return (
    <SidePanel
      open={process !== null}
      onOpenChange={onOpenChange}
      title={process?.name ?? "PM2"}
      description={
        process ? `${process.daemonId} #${process.id} · stdout and stderr, merged live` : ""
      }
      bodyClassName="flex min-h-0 flex-1 flex-col p-4"
    >
      {/* Keyed on the process, so switching to another one starts with a
          clean buffer instead of appending to the previous process's. */}
      {process?.logsAvailable === false ? (
        <Notice title="Logs unavailable">
          {process.logsUnavailableReason ||
            "The daemon's log files are outside the configured log roots."}
        </Notice>
      ) : process ? (
        <PM2LogStream key={`${process.daemonId}:${process.id}`} process={process} />
      ) : null}
    </SidePanel>
  )
}

function PM2LogStream({ process }: { process: PM2Process }) {
  const [lines, setLines] = useState<LogLine[]>([])

  const onMessage = useCallback((envelope: Envelope) => {
    if (envelope.type !== "logs") return
    const batch = envelope.data as { stream: string; text: string }[]
    setLines((prev) => {
      // stdout and stderr arrive interleaved on one socket; the stream tag is
      // what lets stderr be coloured without re-parsing the text.
      const next = [
        ...prev,
        ...batch.map((l) => ({
          text: l.text,
          level: l.stream === "stderr" ? "error" : undefined,
        })),
      ]
      return next.length > LOG_LIMIT ? next.slice(next.length - LOG_LIMIT) : next
    })
  }, [])

  const { state } = useSocket(`/pm2/${encodeURIComponent(process.name)}/logs/stream`, {
    onMessage,
    query: { lines: 300, user: process.daemonId, id: process.id },
  })

  return (
    <LogViewer
      className="h-full"
      lines={lines}
      showTimestamps={false}
      toolbar={
        <Status
          state={state}
          label={
            state === "open"
              ? "Live"
              : state === "connecting"
                ? "Connecting"
                : "Disconnected — retrying"
          }
        />
      }
      onClear={() => setLines([])}
    />
  )
}
