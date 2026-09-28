"use client"

import { useRef, useState } from "react"
import { usePathname, useRouter, useSearchParams } from "next/navigation"
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
import { ProductGlyph } from "@/components/product-logo"
import { ErrorState, LoadingRows, Notice } from "@/components/state"
import { DatabaseQueries } from "@/components/database/queries-view"
import { queryNoun } from "@/components/database/queries"

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
 * failures, memory limits in the last hour — and the Databases section draws
 * no tiles (§15's `/git` exit), so they are the counts on the lens row's
 * chips, each a press from the lines it counts.
 */
export function LogsTab({
  conn,
  onQuery,
}: {
  conn: DbConnection
  onQuery?: (sql: string) => void
}) {
  const found = usePoll(
    (signal) => get<DbLogSources>(`/databases/${conn.id}/logs/sources`, undefined, signal),
    60_000,
    [conn.id],
  )
  // A server that stops takes its listener with it, and the next answer may
  // find nothing left to follow. The log already open stays, with why,
  // rather than going from under the reader just as it explains the most.
  const [held, setHeld] = useState<{ id: number; found: DbLogSources } | null>(null)
  const latest = found.data
  if (latest && latest.sources.length > 0 && (held?.found !== latest || held.id !== conn.id)) {
    setHeld({ id: conn.id, found: latest })
  }
  const kept = held?.id === conn.id ? held.found : undefined

  if (found.error && !latest) return <ErrorState error={found.error} />
  if (!latest) {
    return (
      <Pane className="h-full">
        <LoadingRows rows={8} className="p-3" />
      </Pane>
    )
  }
  if (latest.sources.length > 0) {
    return <ServerLogs conn={conn} found={latest} onQuery={onQuery} />
  }
  if (kept) {
    const note =
      "Nothing answers for this connection any more, so the server may have stopped. This is the log it was writing."
    return <ServerLogs conn={conn} found={{ ...kept, note }} onQuery={onQuery} />
  }
  return (
    <QueriesAlone conn={conn} reason={latest.reason} refused={latest.refused} onQuery={onQuery} />
  )
}

function ServerLogs({
  conn,
  found,
  onQuery,
}: {
  conn: DbConnection
  found: DbLogSources
  onQuery?: (sql: string) => void
}) {
  const router = useRouter()
  const pathname = usePathname()
  const params = useSearchParams()
  const noun = queryNoun(conn.driver)
  const sources = found.sources
  const primary = sources.find((s) => s.primary)

  // The page owns its address: the view and the source are in it, so a link
  // opens on the same reading. One press can change both — "Server log
  // around this" opens History on the server's own log — and two replaces
  // built from the same address would each undo the other, so the changes
  // of one press are gathered and written once.
  const pending = useRef<URLSearchParams | null>(null)
  const write = (key: string, value: string) => {
    if (!pending.current) {
      const next = new URLSearchParams(params.toString())
      pending.current = next
      queueMicrotask(() => {
        pending.current = null
        router.replace(`${pathname}?${next.toString()}`, { scroll: false })
      })
    }
    pending.current.set(key, value)
  }

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
      render: (ctx) => <DatabaseQueries conn={conn} ctx={onServerLog(ctx)} onQuery={onQuery} />,
    },
  ]

  return (
    <div className="flex h-full min-h-0 flex-col gap-3">
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
        source={params.get("source")}
        onSourceChange={(id) => write("source", id)}
        view={params.get("view")}
        onViewChange={(id) => write("view", id)}
        storageKey={`databases.${conn.id}.logs`}
        views={views}
        // The Databases section draws no tiles (§15's `/git` exit): the
        // lens's figures are the counts on its chips, each a press from the
        // lines it counts.
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
  reason,
  refused,
  onQuery,
}: {
  conn: DbConnection
  reason?: string
  refused?: DbLogSources["refused"]
  onQuery?: (sql: string) => void
}) {
  const noun = queryNoun(conn.driver)
  const why = reason ?? (refused?.length ? `${refused[0].path} is ${refused[0].reason}.` : "")
  return (
    <Pane className="h-full min-h-[24rem]">
      <div className="flex shrink-0 items-start gap-x-2 border-b border-hairline px-2.5 py-2.5">
        <ProductGlyph id={conn.driver} className="mt-px" />
        <div className="flex min-w-0 flex-1 flex-wrap items-baseline gap-x-3 gap-y-0.5">
          <span className="shrink-0 text-body font-medium">
            {conn.driver === "sqlite" ? "Statements run from here" : noun.title}
          </span>
          {why && (
            <span className="min-w-0 basis-64 text-hint text-pretty text-muted-foreground max-sm:basis-full sm:flex-1">
              {why}
            </span>
          )}
        </div>
      </div>
      <DatabaseQueries conn={conn} onQuery={onQuery} />
    </Pane>
  )
}
