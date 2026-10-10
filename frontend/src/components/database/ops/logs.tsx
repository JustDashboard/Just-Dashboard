"use client"

import { useState } from "react"
import { Warning } from "@/components/icons"
import { get } from "@/lib/api"
import type { DbConnection, DbLogSources } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import {
  ServiceLogs,
  type ServiceLogsContext,
  type ServiceLogsView,
} from "@/components/logs/service-logs"
import { Pane } from "@/components/panel"
import { LoadingRows, Notice } from "@/components/state"
import type { Engine } from "@/components/database/engine"
import { CouldNotRead } from "@/components/database/home/blocks"
import { SectionFrame } from "@/components/database/kit"
import { DatabaseQueries } from "@/components/database/ops/queries-view"
import { queryNoun } from "@/components/database/ops/queries"
import { useDatabase } from "@/components/database/shell/database-context"

/**
 * The server's own log, read on the database's page.
 *
 * The server is found from the connection rather than guessed at
 * (`GET /databases/{id}/logs/sources`): a database in a container is its
 * container's output; one installed on the machine is the file its process
 * writes — even when last night's rotation left it empty, since its history
 * is in the rotated files the History reading opens — and its unit's journal
 * beside it. A server nothing answers for is found by name instead — the
 * container the connection is named after, or the engine's units — since
 * a stopped server's log is read for the lines that say why. Either is read
 * through the engine's own lens, so a Postgres log reads as its slow
 * statements, auth failures, locks and checkpoints, with the quick views and
 * Insights that go with them, and a line opens in place. Nothing sends the
 * reader to the host's Logs page.
 *
 * The page adds its own reading of the same server: its queries, as the
 * server recorded them. A server on another machine and a SQLite file have no
 * log here, so they are that reading alone, with the reason said above it.
 *
 * The lens's readings are figures over its window — restarts, persistence
 * failures, memory limits in the last hour — drawn as the counts on the lens
 * row's chips, each a press from the lines it counts, since a workbench has
 * no room above it for a row of tiles.
 */
export function Logs() {
  const { conn, engine, goto } = useDatabase()
  const found = usePoll(
    (signal) => get<DbLogSources>(`/databases/${conn.id}/logs/sources`, undefined, signal),
    60_000,
    [conn.id],
  )
  // A server that stops takes its listener with it, and the next answer may
  // find nothing left to follow. The log already open stays, with why,
  // rather than going from under the reader just as it explains the most.
  const [held, setHeld] = useState<DbLogSources | null>(null)
  const latest = found.data
  if (latest && latest.sources.length > 0 && held !== latest) setHeld(latest)

  // A statement from the log opens in the SQL editor, on an engine whose log
  // holds SQL. Every engine has a console of its own kind; what is handed
  // over here is a statement.
  const onQuery = engine.can("sql") ? (sql: string) => goto("query", { sql }) : undefined

  return (
    <SectionFrame section="logs">
      {found.error && !latest ? (
        // Where the server's log is could not be found out: said, with the
        // way to ask again, rather than left to the next poll.
        <CouldNotRead
          what="where this server writes its log"
          error={found.error}
          onRetry={found.refresh}
        />
      ) : !latest ? (
        <Pane className="min-h-0 flex-1">
          <LoadingRows rows={8} className="p-3" />
        </Pane>
      ) : latest.sources.length > 0 ? (
        <ServerLogs conn={conn} engine={engine} found={latest} onQuery={onQuery} />
      ) : held ? (
        <ServerLogs
          conn={conn}
          engine={engine}
          found={{
            ...held,
            note: "Nothing answers for this connection any more, so the server may have stopped. This is the log it was writing.",
          }}
          onQuery={onQuery}
        />
      ) : (
        <QueriesAlone
          conn={conn}
          engine={engine}
          reason={latest.reason}
          refused={latest.refused}
          onQuery={onQuery}
        />
      )}
    </SectionFrame>
  )
}

function ServerLogs({
  conn,
  engine,
  found,
  onQuery,
}: {
  conn: DbConnection
  engine: Engine
  found: DbLogSources
  onQuery?: (sql: string) => void
}) {
  const { select, param } = useDatabase()
  const noun = queryNoun(engine)
  const sources = found.sources
  const primary = sources.find((s) => s.primary)

  // A statement's rows came from the server's own log, so the log around one
  // is that log — not the journal beside it, which holds systemd's starts and
  // stops.
  const onServerLog = (ctx: ServiceLogsContext): ServiceLogsContext =>
    !primary || ctx.sourceId === primary.id
      ? ctx
      : { ...ctx, openHistory: (at) => ctx.openHistory({ ...at, source: primary.id }) }

  const views: ServiceLogsView[] = [
    {
      id: "queries",
      label: noun.view,
      render: (ctx) => (
        <DatabaseQueries conn={conn} engine={engine} ctx={onServerLog(ctx)} onQuery={onQuery} />
      ),
    },
  ]

  return (
    <div className="flex min-h-0 flex-1 flex-col gap-3">
      {found.note && (
        <Notice tone="warning" icon={Warning} title="The server is not answering">
          <p className="max-w-3xl text-pretty">{found.note}</p>
        </Notice>
      )}
      {found.refused?.map((r) => (
        <p key={r.path} className="shrink-0 text-hint text-pretty text-muted-foreground">
          Also writes <span className="font-mono wrap-anywhere">{r.path}</span>, which is {r.reason}
        </p>
      ))}
      <ServiceLogs
        sources={sources}
        // The page owns its address: the view and the source are in it, so
        // a link opens on the same reading. One press can change both —
        // "Server log around this" opens History on the server's own log —
        // and the context's writer makes one change of the two.
        source={param("source") || null}
        onSourceChange={(id) => select({ source: id })}
        view={param("view") || null}
        onViewChange={(id) => select({ view: id })}
        storageKey={`databases.${conn.id}.logs`}
        views={views}
        readings="chips"
        pickerLabel="Server log"
        className="min-h-0 flex-1"
      />
    </div>
  )
}

/**
 * A database with no log on this machine — on another machine, or a SQLite
 * file — is its queries alone, in a pane of the same shape, with why there
 * is no log said beside the name, wrapping rather than cut: it is the one
 * sentence the reader came for.
 */
function QueriesAlone({
  conn,
  engine,
  reason,
  refused,
  onQuery,
}: {
  conn: DbConnection
  engine: Engine
  reason?: string
  refused?: DbLogSources["refused"]
  onQuery?: (sql: string) => void
}) {
  const why = reason ?? (refused?.length ? `${refused[0].path} is ${refused[0].reason}.` : "")
  return (
    <Pane className="min-h-[24rem] flex-1">
      <DatabaseQueries conn={conn} engine={engine} onQuery={onQuery} heading={{ why }} />
    </Pane>
  )
}
