"use client"

import { useEffect, useMemo, useRef } from "react"
import { usePathname, useRouter, useSearchParams } from "next/navigation"
import { cn } from "@/lib/utils"
import { get } from "@/lib/api"
import { EMPTY_FILTER, fieldsEqual, fieldsOf } from "@/lib/log-filter"
import { READINGS_MINUTES, readingFilter } from "@/lib/log-insights"
import { lensFor, type LensReading, type LogLens } from "@/lib/log-lenses"
import { useSessionState } from "@/lib/view-state"
import type { DbConnection, DbLogSource, DbLogSources } from "@/lib/types"
import type { LogFilterState } from "@/components/logs/types"
import { usePoll } from "@/hooks/use-poll"
import { ServiceLogs, type ServiceLogsView } from "@/components/logs/service-logs"
import { useLensReadings, type LensReadingTile } from "@/components/logs/lens-readings"
import { Pane } from "@/components/panel"
import { ProductGlyph } from "@/components/product-logo"
import { ErrorState, LoadingRows } from "@/components/state"
import { ChipCount, ChipStrip, FilterChip } from "@/components/tabs"
import { DatabaseQueries } from "@/components/database/queries-view"
import { queryNoun } from "@/components/database/queries"

const WINDOW_WORDS: Record<keyof typeof READINGS_MINUTES, string> = {
  "1h": "Last hour",
  "24h": "Last 24 hours",
  "7d": "Last 7 days",
}

/**
 * The server's own log, read on the database's page.
 *
 * The server is found from the connection rather than guessed at
 * (`GET /databases/{id}/logs/sources`): a database in a container is its
 * container's output; one installed on the machine is the file its process
 * writes — even when last night's rotation left it empty, since its history
 * is in the rotated files the History reading opens — and its unit's journal
 * beside it. Either is read through the engine's own lens, so a Postgres log
 * reads as its slow statements, auth failures, locks and checkpoints, with
 * the quick views and Insights that go with them, and a line opens in place.
 * Nothing sends the reader to the host's Logs page.
 *
 * The page adds its own reading of the same server: its queries, as the
 * server recorded them. A server on another machine and a SQLite file have no
 * log here, so they are that reading alone, with the reason said above it.
 *
 * The lens's readings are figures over its window — errors, slow statements,
 * auth failures in the last hour — and the Databases section draws no tiles
 * (§15's `/git` exit), so they are the chips over the pane, each a press from
 * the lines it counts.
 */
export function LogsTab({ conn }: { conn: DbConnection }) {
  const found = usePoll(
    (signal) => get<DbLogSources>(`/databases/${conn.id}/logs/sources`, undefined, signal),
    60_000,
    [conn.id],
  )
  if (found.error && !found.data) return <ErrorState error={found.error} />
  if (!found.data) {
    return (
      <Pane className="h-full">
        <LoadingRows rows={8} className="p-3" />
      </Pane>
    )
  }
  if (found.data.sources.length === 0) {
    return <QueriesAlone conn={conn} reason={found.data.reason} refused={found.data.refused} />
  }
  return <ServerLogs conn={conn} found={found.data} />
}

function ServerLogs({ conn, found }: { conn: DbConnection; found: DbLogSources }) {
  const router = useRouter()
  const pathname = usePathname()
  const params = useSearchParams()
  const noun = queryNoun(conn.driver)
  const storageKey = `databases.${conn.id}.logs`
  const sources = found.sources

  // The page owns its address: the view and the source are in it, so a link
  // opens on the same reading. With none named, the server's own log opens.
  const sourceParam = params.get("source")
  const picked = sources.find((s) => s.id === sourceParam) ?? sources[0]
  const write = (key: string, value: string) => {
    const next = new URLSearchParams(params.toString())
    next.set(key, value)
    router.replace(`${pathname}?${next.toString()}`, { scroll: false })
  }

  useArchivesWhenEmpty(storageKey, picked)

  const views = useMemo<ServiceLogsView[]>(
    () => [
      {
        id: "queries",
        label: noun.view,
        render: (ctx) => <DatabaseQueries conn={conn} ctx={ctx} />,
      },
    ],
    [conn, noun.view],
  )

  return (
    <div className="flex h-full min-h-0 flex-col gap-3">
      <ReadingChips
        sourceId={picked.id}
        lens={lensFor(picked.lens)}
        forcedLens={picked.lens}
        storageKey={storageKey}
        view={params.get("view")}
        onLive={() => write("view", "live")}
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
 * Once per source per visit, so turning the archives off again sticks.
 */
function useArchivesWhenEmpty(storageKey: string, source: DbLogSource) {
  // ServiceLogs keeps the switch under its storage key; it has no prop for
  // a History that should start with the archives in.
  const [, setArchives] = useSessionState(`${storageKey}.archives`, false)
  const seen = useRef<string | null>(null)
  const emptied = Boolean(source.path) && !source.size && (source.archives ?? 0) > 0
  useEffect(() => {
    if (seen.current === source.id) return
    seen.current = source.id
    if (emptied) setArchives(true)
  }, [source.id, emptied, setArchives])
}

/**
 * The lens's readings as chips over the pane: the figure for its window, in
 * its tone once it is above zero, and a press that narrows the pane to the
 * lines it counts — or lets them go again. On the Queries view the filter is
 * out of sight, so a press goes to the live lines as well.
 */
function ReadingChips({
  sourceId,
  lens,
  forcedLens,
  storageKey,
  view,
  onLive,
  refused,
}: {
  sourceId: string
  lens: LogLens | undefined
  /** The lens the pane asks for by name, so the figures count what it shows. */
  forcedLens?: string
  storageKey: string
  view: string | null
  onLive: () => void
  refused?: DbLogSources["refused"]
}) {
  const readings = useLensReadings(sourceId, lens, { forcedLens })
  // The pane's own filter, which ServiceLogs keeps under its storage key and
  // offers no prop for: the chip and the lens's quick views are one question.
  const [filter, setFilter] = useSessionState<LogFilterState>(`${storageKey}.filter`, EMPTY_FILTER)
  if (readings.tiles.length === 0 && !refused?.length) return null

  return (
    <div className="flex min-w-0 shrink-0 items-center gap-x-4 gap-y-1 max-sm:flex-col max-sm:items-stretch">
      {readings.tiles.length > 0 && (
        <ChipStrip
          aria-label="Readings"
          className="min-w-0 sm:-my-1 sm:flex-nowrap sm:overflow-x-auto sm:py-1"
        >
          <span className="shrink-0 pr-1 text-hint text-muted-foreground">
            {WINDOW_WORDS[readings.window]}
          </span>
          {readings.tiles.map((tile) => {
            const pressed = readingPressed(tile.reading, filter)
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
                  if (!pressed && view === "queries") onLive()
                }}
              >
                {tile.reading.label}
                <ReadingCount tile={tile} />
              </FilterChip>
            )
          })}
        </ChipStrip>
      )}
      {refused && refused.length > 0 && (
        <p
          className="min-w-0 flex-1 truncate text-hint text-muted-foreground sm:text-right"
          title={refused.map((r) => `${r.path} is ${r.reason}`).join("\n")}
        >
          Also writes <span className="font-mono">{basename(refused[0].path)}</span>, which is{" "}
          {refused[0].reason}
        </p>
      )}
    </div>
  )
}

function basename(path: string) {
  return path.slice(path.lastIndexOf("/") + 1)
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

function readingPressed(reading: LensReading, filter: LogFilterState) {
  const levels = reading.levels ?? []
  return (
    fieldsEqual(fieldsOf(filter), reading.fields ?? {}) &&
    filter.levels.length === levels.length &&
    levels.every((level) => filter.levels.includes(level))
  )
}

/**
 * A database with no log on this machine — on another machine, or a SQLite
 * file — is its queries alone, in a pane of the same shape, with why there
 * is no log said where the log's name would be.
 */
function QueriesAlone({
  conn,
  reason,
  refused,
}: {
  conn: DbConnection
  reason?: string
  refused?: DbLogSources["refused"]
}) {
  const noun = queryNoun(conn.driver)
  const why = reason ?? (refused?.length ? `${refused[0].path} is ${refused[0].reason}.` : "")
  return (
    <Pane className="h-full min-h-[24rem]">
      <div className="flex min-h-10 shrink-0 items-center gap-2 border-b border-hairline px-2.5 py-1.5">
        <ProductGlyph id={conn.driver} />
        <span className="shrink-0 text-body font-medium">
          {conn.driver === "sqlite" ? "Statements run from here" : noun.title}
        </span>
        {why && (
          <span className="min-w-0 truncate text-hint text-muted-foreground" title={why}>
            {why}
          </span>
        )}
      </div>
      <DatabaseQueries conn={conn} />
    </Pane>
  )
}
