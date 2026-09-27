"use client"

import { useEffect, useMemo, useRef, useState } from "react"
import { usePathname, useRouter, useSearchParams } from "next/navigation"
import { Warning } from "@/components/icons"
import { cn } from "@/lib/utils"
import { get } from "@/lib/api"
import { EMPTY_FILTER, fieldsEqual, fieldsOf, type LogLevel } from "@/lib/log-filter"
import { READINGS_MINUTES, readingFilter } from "@/lib/log-insights"
import { lensFor, type LogLens } from "@/lib/log-lenses"
import { useSessionState } from "@/lib/view-state"
import type { DbConnection, DbLogSource, DbLogSources } from "@/lib/types"
import type { LogFields, LogFilterState } from "@/components/logs/types"
import { usePoll } from "@/hooks/use-poll"
import {
  ServiceLogs,
  type ServiceLogsContext,
  type ServiceLogsView,
} from "@/components/logs/service-logs"
import { useLensReadings, type LensReadingTile } from "@/components/logs/lens-readings"
import { Pane } from "@/components/panel"
import { ProductGlyph } from "@/components/product-logo"
import { ErrorState, LoadingRows, Notice } from "@/components/state"
import { ChipCount, ChipStrip, FilterChip } from "@/components/tabs"
import { DatabaseQueries } from "@/components/database/queries-view"
import { queryNoun } from "@/components/database/queries"

const WINDOW_WORDS: Record<keyof typeof READINGS_MINUTES, string> = {
  "1h": "Last hour",
  "24h": "Last 24 hours",
  "7d": "Last 7 days",
}

/** The readings of Live, History and Insights; a page view reads no filter. */
const FILTERED_MODES = new Set(["live", "search", "insights"])

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
 * no tiles (§15's `/git` exit), so they are chips over the pane, each a
 * press from the lines it counts.
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
  const storageKey = `databases.${conn.id}.logs`
  const sources = found.sources

  // What the pane is on when the address names nothing: ServiceLogs keeps its
  // source and its reading under the storage key, and the chips over it must
  // count, and press for, the same source it shows.
  const [storedSource, setStoredSource] = useSessionState(`${storageKey}.source`, "")
  const [storedMode] = useSessionState(`${storageKey}.mode`, "live")
  const sourceParam = params.get("source")
  const mode = params.get("view") ?? storedMode
  const gone = sourceParam !== null && !sources.some((s) => s.id === sourceParam)
  const picked = sources.find((s) => s.id === (sourceParam ?? storedSource)) ?? sources[0]
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

  const archivesFor = useArchivesWhenEmpty(storageKey, picked)

  // A statement's rows came from the server's own log, so the log around one
  // is that log — not the journal beside it, which holds systemd's starts and
  // stops. The source is chosen in the kept state as well as the address, so
  // the pane opens History once, on the right log, rather than first on the
  // one it was showing.
  const onServerLog = (ctx: ServiceLogsContext): ServiceLogsContext =>
    !primary || ctx.sourceId === primary.id
      ? ctx
      : {
          ...ctx,
          openHistory: (at) => {
            setStoredSource(primary.id)
            write("source", primary.id)
            archivesFor(primary)
            ctx.openHistory(at)
          },
        }

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
      <ReadingChips
        sourceId={gone ? "" : picked.id}
        lens={lensFor(picked.lens)}
        forcedLens={picked.lens}
        storageKey={storageKey}
        onLive={FILTERED_MODES.has(mode) ? undefined : () => write("view", "live")}
        refused={found.refused}
      />
      <ServiceLogs
        sources={sources}
        source={sourceParam ?? picked.id}
        onSourceChange={(id) => write("source", id)}
        view={params.get("view")}
        onViewChange={(id) => write("view", id)}
        storageKey={storageKey}
        views={views}
        pickerLabel="Server log"
        className="min-h-0 flex-1"
      />
    </div>
  )
}

/**
 * A file logrotate emptied opens its History on the rotated files as well:
 * the operator's own server wrote its last lines to yesterday's file, and a
 * History of the empty live one answered "no matches" about a busy night.
 * Once per source per visit, so turning the archives off again sticks. The
 * function it returns does the same for a source a press is about to open
 * History on, before the pane has shown it.
 */
function useArchivesWhenEmpty(storageKey: string, source: DbLogSource) {
  // ServiceLogs keeps the switch under its storage key; it has no prop for
  // a History that should start with the archives in.
  const [, setArchives] = useSessionState(`${storageKey}.archives`, false)
  const seen = useRef<string | null>(null)
  const emptied = isEmptied(source)
  useEffect(() => {
    if (seen.current === source.id) return
    seen.current = source.id
    if (emptied) setArchives(true)
  }, [source.id, emptied, setArchives])
  return (next: DbLogSource) => {
    if (seen.current === next.id) return
    seen.current = next.id
    if (isEmptied(next)) setArchives(true)
  }
}

function isEmptied(source: DbLogSource) {
  return Boolean(source.path) && !source.size && (source.archives ?? 0) > 0
}

/** A question as a quick view, a reading and a filter each put it: fields and levels. */
type Question = { fields?: LogFields; levels?: readonly LogLevel[] }

function sameQuestion(a: Question, b: Question) {
  const levels = a.levels ?? []
  const others = b.levels ?? []
  return (
    fieldsEqual(a.fields ?? {}, b.fields ?? {}) &&
    levels.length === others.length &&
    levels.every((level) => others.includes(level))
  )
}

/**
 * The lens's readings as chips over the pane: the figure for its window, in
 * its tone once it is above zero, and a press that narrows the pane to the
 * lines it counts — or lets them go again. On the Queries view the filter is
 * out of sight, so a press goes to the live lines as well.
 *
 * A reading whose question one of the lens's quick views already asks is
 * left to the quick view, whose chip carries its own count: the same chip
 * twice, counted over two windows, read as two answers. What is left here
 * are the readings nothing else on screen counts — restarts, persistence
 * failures, memory limits. A phone has no room for the row; its quick views
 * are the same questions.
 */
function ReadingChips({
  sourceId,
  lens,
  forcedLens,
  storageKey,
  onLive,
  refused,
}: {
  sourceId: string
  lens: LogLens | undefined
  /** The lens the pane asks for by name, so the figures count what it shows. */
  forcedLens?: string
  storageKey: string
  /** Set while a page view is on screen, where the filter a chip sets is not. */
  onLive?: () => void
  refused?: DbLogSources["refused"]
}) {
  const unasked = useMemo(
    () =>
      lens && {
        ...lens,
        readings: lens.readings?.filter(
          (reading) =>
            !lens.views.some((view) => view.q === undefined && sameQuestion(view, reading)),
        ),
      },
    [lens],
  )
  const readings = useLensReadings(sourceId, unasked, { forcedLens })
  // The pane's own filter, which ServiceLogs keeps under its storage key and
  // offers no prop for: the chip and the lens's quick views are one question.
  const [filter, setFilter] = useSessionState<LogFilterState>(`${storageKey}.filter`, EMPTY_FILTER)
  const tiles = sourceId ? readings.tiles : []
  if (tiles.length === 0 && !refused?.length) return null

  return (
    <div
      className={cn(
        "flex min-w-0 shrink-0 flex-wrap items-center gap-x-4 gap-y-1",
        !refused?.length && "max-sm:hidden",
      )}
    >
      {tiles.length > 0 && (
        <ChipStrip
          aria-label="Readings"
          className="min-w-0 max-sm:hidden sm:-my-1 sm:flex-nowrap sm:overflow-x-auto sm:py-1"
        >
          <span className="shrink-0 pr-1 text-hint text-muted-foreground">
            {WINDOW_WORDS[readings.window]}
          </span>
          {tiles.map((tile) => {
            const pressed = sameQuestion(
              { fields: fieldsOf(filter), levels: filter.levels },
              tile.reading,
            )
            return (
              <FilterChip
                key={tile.reading.id}
                selected={pressed}
                title={titleOf(tile)}
                onClick={() => {
                  setFilter(
                    pressed
                      ? { ...filter, fields: {}, levels: [] }
                      : readingFilter(filter, tile.reading),
                  )
                  if (!pressed) onLive?.()
                }}
              >
                {tile.reading.label}
                <ReadingCount tile={tile} />
              </FilterChip>
            )
          })}
        </ChipStrip>
      )}
      {refused?.map((r) => (
        <p
          key={r.path}
          className="min-w-0 flex-1 basis-80 text-hint text-pretty text-muted-foreground sm:text-right"
        >
          Also writes <span className="font-mono wrap-anywhere">{r.path}</span>, which is {r.reason}
        </p>
      ))}
    </div>
  )
}

function ReadingCount({ tile }: { tile: LensReadingTile }) {
  const figure = tile.figure
  if (!figure) return null
  const value = figure.value
  return (
    <ChipCount
      className={cn(
        value > 0 && tile.reading.tone === "danger" && "text-destructive opacity-100",
        value > 0 && tile.reading.tone === "warning" && "text-warning opacity-100",
      )}
    >
      {value.toLocaleString()}
      {figure.capped ? "+" : ""}
    </ChipCount>
  )
}

function titleOf(tile: LensReadingTile) {
  const { reading, figure } = tile
  if (figure && figure.value === 0 && reading.requires) {
    return `Logged only with ${reading.requires} set`
  }
  return reading.hint
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
