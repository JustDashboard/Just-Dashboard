"use client"

import { useCallback, useMemo, useState } from "react"
import { Download, Logs } from "@/components/icons"
import { get } from "@/lib/api"
import type { DbAccess, DbConnection, LogLine, LogSourceIndex } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useSocket, type Envelope } from "@/hooks/use-socket"
import { useAuth } from "@/hooks/use-auth"
import { Pane } from "@/components/panel"
import { Row, RowList } from "@/components/row-list"
import { EmptyState, LoadingPanel } from "@/components/state"
import { Button } from "@/components/ui/button"
import { LogViewer } from "@/components/log-viewer"
import { ProductLogo } from "@/components/product-logo"

const LOG_LIMIT = 5000

/**
 * The server's own output — the thing to read when a connection fails and
 * the engine's refusal says less than its log does.
 *
 * A database in a container is its container's output, streamed here the
 * way the Docker page streams it. One installed on the machine writes to
 * the journal, so the page lists the units that look like this engine's and
 * hands each to the Logs page, which already knows how to follow a unit.
 */
export function LogsTab({ conn }: { conn: DbConnection }) {
  const { can } = useAuth()
  const admin = can("system.admin")
  const access = usePoll(
    (signal) => get<DbAccess>(`/databases/${conn.id}/access`, undefined, signal),
    0,
    [conn.id],
    { enabled: admin },
  )
  const sources = usePoll(
    (signal) => get<LogSourceIndex>("/logs/sources", undefined, signal),
    0,
    [],
    { enabled: admin ? access.data !== undefined && !access.data.container : true },
  )

  if (!admin) {
    return (
      <EmptyState
        icon={Logs}
        title="Logs are read by an administrator"
        description="Reading a server's output means reading its container or the system journal, which this account cannot."
      />
    )
  }
  if (access.loading && !access.data) return <LoadingPanel />
  if (access.data?.container) return <ContainerLogs container={access.data.container} />

  const wanted = journalNeedles(conn.driver)
  const units = (sources.data?.units ?? []).filter((u) =>
    wanted.some((w) => u.name.toLowerCase().includes(w)),
  )
  if (sources.loading && !sources.data) return <LoadingPanel />
  if (units.length === 0) {
    return (
      <EmptyState
        icon={Logs}
        title="No log for this server here"
        description={
          conn.driver === "sqlite"
            ? "A SQLite database is a file and writes no log of its own."
            : access.data?.exposure === "remote"
              ? "The server is on another machine; its log is there."
              : "No container and no systemd unit on this machine looks like this engine's. The Logs page lists every source it can read."
        }
      />
    )
  }
  return (
    <RowList>
      {units.map((u) => (
        <Row
          key={u.name}
          href={`/logs?source=journal:${encodeURIComponent(u.name)}`}
          leading={<ProductLogo id={conn.driver} size="sm" />}
          title={u.name}
          subtitle={u.description || u.active}
        />
      ))}
    </RowList>
  )
}

function journalNeedles(driver: string): string[] {
  switch (driver) {
    case "postgres":
      return ["postgres"]
    case "mysql":
      return ["mysql", "mariadb"]
    case "redis":
      return ["redis", "valkey", "keydb"]
    case "mongodb":
      return ["mongod"]
    case "clickhouse":
      return ["clickhouse"]
    case "sqlserver":
      return ["mssql"]
    case "oracle":
      return ["oracle"]
  }
  return []
}

function ContainerLogs({ container }: { container: string }) {
  const [lines, setLines] = useState<LogLine[]>([])
  const [timestamps, setTimestamps] = useState(true)
  const onMessage = useCallback((envelope: Envelope) => {
    if (envelope.type !== "logs") return
    const batch = envelope.data as { stream: string; text: string }[]
    setLines((prev) => {
      const next = [...prev, ...batch.map((l) => ({ text: l.text }))]
      return next.length > LOG_LIMIT ? next.slice(next.length - LOG_LIMIT) : next
    })
  }, [])
  const query = useMemo(
    () => ({ tail: 500, timestamps: timestamps ? "true" : "false" }),
    [timestamps],
  )
  const { state } = useSocket(`/docker/containers/${encodeURIComponent(container)}/logs/stream`, {
    onMessage,
    query,
  })
  const save = () => {
    const blob = new Blob([lines.map((l) => l.text).join("\n")], { type: "text/plain" })
    const a = document.createElement("a")
    a.href = URL.createObjectURL(blob)
    a.download = `${container}.log`
    a.click()
    URL.revokeObjectURL(a.href)
  }
  return (
    <Pane className="h-full min-h-[24rem]">
      <LogViewer
        className="h-full"
        lines={lines}
        showTimestamps={false}
        onClear={() => setLines([])}
        emptyMessage={state === "open" ? "No output yet." : "Connecting…"}
        toolbar={
          <>
            <Button
              size="xs"
              variant="ghost"
              onClick={() => {
                setLines([])
                setTimestamps((t) => !t)
              }}
            >
              {timestamps ? "Hide times" : "Show times"}
            </Button>
            <Button size="xs" variant="ghost" onClick={save} disabled={lines.length === 0}>
              <Download className="size-3" />
              Save
            </Button>
          </>
        }
      />
    </Pane>
  )
}
