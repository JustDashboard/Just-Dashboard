"use client"

import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import {
  ChartActivity,
  ClockRewind,
  MagnifyingGlass,
  MagnifyingGlassMinus,
} from "@/components/icons"
import { errorMessage, get } from "@/lib/api"
import { plural } from "@/lib/format"
import type {
  LogLine,
  LogRetention,
  LogSearchResult,
  LogSource,
  LogSourceIndex,
  LogStreamMeta,
} from "@/lib/types"
import { EMPTY_FILTER, filterQuery, isFilterActive, resolveRange } from "@/lib/log-filter"
import type { LogFilterState, LogMode, LogTimeRange } from "@/components/logs/types"
import { useSocket, type Envelope } from "@/hooks/use-socket"
import { usePoll } from "@/hooks/use-poll"
import { EmptyState, ErrorState } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { FilterBar } from "@/components/logs/filter-bar"
import { Histogram } from "@/components/logs/histogram"
import { LogConsole } from "@/components/logs/log-console"
import { RetentionNote } from "@/components/logs/retention-note"
import { Tag } from "@/components/tag"
import { PaneFooter } from "@/components/panel"

/** How many lines the live pane holds before the oldest fall off the top. */
const LIVE_BUFFER = 4000

/** The opening window a live tail asks for. */
const TAIL_LINES = 500

/** datetime-local wants the browser's own clock, not an ISO string in UTC. */
function toLocalInput(date: Date) {
  const offset = date.getTimezoneOffset() * 60_000
  return new Date(date.getTime() - offset).toISOString().slice(0, 23)
}

type WorkspaceProps = {
  source: LogSource
  sourceId: string
  units: LogSourceIndex["units"]
  mode: LogMode
  onModeChange: (mode: LogMode) => void
  filter: LogFilterState
  onFilterChange: (filter: LogFilterState) => void
  unit: string
  onUnitChange: (unit: string) => void
  range: LogTimeRange
  onRangeChange: (range: LogTimeRange) => void
  since: string
  until: string
  onSinceChange: (value: string) => void
  onUntilChange: (value: string) => void
  onCustomRange: (since: Date, until: Date) => void
  context: number
  onContextChange: (value: number) => void
  archives: boolean
  onArchivesChange: (value: boolean) => void
  boot: boolean
  onBootChange: (value: boolean) => void
}

export function LogWorkspace(props: WorkspaceProps) {
  const { source, sourceId, mode, filter, onFilterChange, onModeChange } = props

  // The live filter trails what is being typed. Reconnecting the socket per
  // keystroke would restart the tail four times a word; waiting for a button
  // is the friction that made the old page's filter go unused. The pause is
  // short enough to read as instant and long enough to type through.
  const applied = useDebounced(filter, 400)
  const live = useLiveTail(sourceId, mode === "live" ? applied : null, props.boot)
  const search = useHistorySearch(props)

  // Arriving on a shared link that already says "history, this term" should
  // show the answer, not a form with the question typed into it. The workspace
  // is keyed on the source, so this fires once per source rather than on every
  // change to the filter.
  const started = useRef(false)
  const runSearch = search.run
  useEffect(() => {
    if (mode !== "search" || started.current) return
    started.current = true
    runSearch()
  }, [mode, runSearch])

  const lines = mode === "live" ? live.lines : (search.result?.lines ?? [])
  const counts = useMemo(() => {
    if (mode === "search") {
      const totals: Record<string, number> = {}
      for (const bucket of search.result?.histogram ?? []) {
        for (const [level, n] of Object.entries(bucket.counts)) {
          totals[level] = (totals[level] ?? 0) + n
        }
      }
      return totals
    }
    const totals: Record<string, number> = {}
    for (const line of live.lines) {
      const level = line.level || "unknown"
      totals[level] = (totals[level] ?? 0) + 1
    }
    return totals
  }, [mode, live.lines, search.result])

  const retention = usePoll(
    (signal) => get<LogRetention>("/logs/retention", { source: sourceId }, signal),
    0,
    [sourceId],
    { enabled: Boolean(source.path) || source.kind === "pm2" },
  )

  return (
    <div className="flex min-h-0 min-w-0 flex-1 flex-col gap-3">
      <FilterBar
        mode={mode}
        onModeChange={(next) => {
          onModeChange(next)
          if (next === "search") search.run()
        }}
        filter={filter}
        onFilterChange={onFilterChange}
        onSubmit={() => {
          if (mode === "search") search.run()
          else onModeChange("search")
        }}
        searching={search.loading}
        counts={counts}
        source={source}
        units={props.units}
        unit={props.unit}
        onUnitChange={props.onUnitChange}
        range={props.range}
        onRangeChange={(next) => {
          props.onRangeChange(next)
          if (mode === "search" && next !== "custom") search.run({ range: next })
        }}
        since={props.since}
        until={props.until}
        onSinceChange={props.onSinceChange}
        onUntilChange={props.onUntilChange}
        context={props.context}
        onContextChange={(next) => {
          props.onContextChange(next)
          if (mode === "search") search.run({ context: next })
        }}
        archives={props.archives}
        onArchivesChange={(next) => {
          props.onArchivesChange(next)
          if (mode === "search") search.run({ archives: next })
        }}
        boot={props.boot}
        onBootChange={(next) => {
          props.onBootChange(next)
          if (mode === "search") search.run({ boot: next })
        }}
      />

      {mode === "search" && (search.result?.histogram.length ?? 0) > 0 && (
        <Histogram
          buckets={search.result!.histogram}
          bucketSeconds={search.result!.bucketSeconds ?? 60}
          onZoom={(from, to) => {
            props.onCustomRange(from, to)
            search.run({ range: "custom", since: toLocalInput(from), until: toLocalInput(to) })
          }}
        />
      )}

      <LogConsole
        className="min-h-0 flex-1"
        lines={lines}
        filter={applied}
        showLineNumbers={mode === "search"}
        // A file's "source" is the file already chosen in the rail, repeated
        // on every line. The journal's is which unit spoke, which is the point.
        showSource={source.kind === "journal"}
        showFile={mode === "search" && (search.result?.files.length ?? 0) > 1}
        paused={mode === "live" ? live.paused : undefined}
        onPausedChange={mode === "live" ? live.setPaused : undefined}
        held={live.held}
        onClear={mode === "live" ? live.clear : undefined}
        status={
          mode === "live" ? (
            <Status
              state={
                live.state === "open"
                  ? "running"
                  : live.state === "connecting"
                    ? "restarting"
                    : "stopped"
              }
              label={
                live.state === "open"
                  ? "Live"
                  : live.state === "connecting"
                    ? "Connecting"
                    : "Disconnected"
              }
              className="text-hint"
            />
          ) : (
            <SearchSummary result={search.result} loading={search.loading} />
          )
        }
        actions={
          mode === "live" && live.meta?.prefill && !live.meta.prefill.complete ? (
            <Tag title={live.meta.note}>partial history</Tag>
          ) : undefined
        }
        footer={
          <>
            {mode === "search" && search.result && <SearchFooter result={search.result} />}
            {retention.data && <RetentionNote retention={retention.data} />}
          </>
        }
        empty={
          mode === "live" ? (
            <LiveEmpty
              state={live.state}
              error={live.error}
              filter={applied}
              onSearchHistory={() => {
                onModeChange("search")
                search.run()
              }}
              onClearFilter={() => onFilterChange(EMPTY_FILTER)}
            />
          ) : (
            <SearchEmpty
              result={search.result}
              loading={search.loading}
              error={search.error}
              source={source}
              archives={props.archives}
              onIncludeArchives={() => {
                props.onArchivesChange(true)
                search.run({ archives: true })
              }}
              onRun={() => search.run()}
            />
          )
        }
      />
    </div>
  )
}

function useDebounced<T>(value: T, delay: number): T {
  const [settled, setSettled] = useState(value)
  const key = JSON.stringify(value)
  useEffect(() => {
    const timer = setTimeout(() => setSettled(value), delay)
    return () => clearTimeout(timer)
    // The value is an object rebuilt on every keystroke, so the identity is
    // useless as a dependency and its content is what actually changed.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key, delay])
  return settled
}

/**
 * The live tail.
 *
 * Pausing holds what arrives rather than dropping it, which is the difference
 * between reading a busy log and choosing between reading and keeping. The
 * held lines are counted and appended on resume.
 */
function useLiveTail(sourceId: string, filter: LogFilterState | null, boot: boolean) {
  const [lines, setLines] = useState<LogLine[]>([])
  const [meta, setMeta] = useState<LogStreamMeta | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [paused, setPaused] = useState(false)
  // Held lines are state rather than a ref because the reset happens during
  // render, and a ref written there is a value React never sees.
  const [heldLines, setHeldLines] = useState<LogLine[]>([])
  const pausedRef = useRef(false)

  useEffect(() => {
    pausedRef.current = paused
  }, [paused])

  const query = useMemo(
    () =>
      filter
        ? {
            source: sourceId,
            lines: TAIL_LINES,
            boot: boot ? "true" : undefined,
            ...filterQuery(filter),
          }
        : { source: "" },
    [sourceId, filter, boot],
  )
  const queryKey = JSON.stringify(query)

  // A new filter is a new question: the socket restarts with a window that
  // matches it, so mixing the answers would put lines the filter rejects above
  // the ones it keeps. Reset during render rather than in an effect — the two
  // differ by one frame, and that frame is the old log's tail drawn under the
  // new question.
  const [lastQuery, setLastQuery] = useState(queryKey)
  if (lastQuery !== queryKey) {
    setLastQuery(queryKey)
    setLines([])
    setMeta(null)
    setError(null)
    setHeldLines([])
  }

  const onMessage = useCallback((envelope: Envelope) => {
    if (envelope.error) {
      setError(envelope.error)
      return
    }
    if (envelope.type === "meta") {
      setMeta(envelope.data as LogStreamMeta)
      return
    }
    if (envelope.type !== "logs") return
    const batch = envelope.data as LogLine[]
    if (pausedRef.current) {
      setHeldLines((prev) => capLines([...prev, ...batch]))
      return
    }
    setLines((prev) => capLines([...prev, ...batch]))
  }, [])

  const { state } = useSocket("/logs/stream", {
    onMessage,
    query,
    enabled: Boolean(filter && sourceId),
  })

  const setPausedAndFlush = useCallback(
    (next: boolean) => {
      setPaused(next)
      if (next) return
      if (heldLines.length) setLines((prev) => capLines([...prev, ...heldLines]))
      setHeldLines([])
    },
    [heldLines],
  )

  const clear = useCallback(() => {
    setLines([])
    setHeldLines([])
  }, [])

  return {
    lines,
    meta,
    error,
    state,
    paused,
    setPaused: setPausedAndFlush,
    held: heldLines.length,
    clear,
  }
}

function capLines(lines: LogLine[]) {
  return lines.length > LIVE_BUFFER ? lines.slice(lines.length - LIVE_BUFFER) : lines
}

/**
 * The history search.
 *
 * It runs on Enter rather than as you type, and that is deliberate: this scans
 * the file — and its rotated archives when asked — so a keystroke-triggered
 * version would queue a full pass over gigabytes per character. The live mode
 * next to it is the one that answers instantly.
 */
function useHistorySearch(props: WorkspaceProps) {
  const [result, setResult] = useState<LogSearchResult | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const abort = useRef<AbortController | null>(null)
  // Which answer is still wanted. A sequence number rather than the abort
  // signal, because "this request was superseded" and "this request was
  // cancelled" are different questions and only the first should decide
  // whether to render — reading it off the signal left the pane stuck on
  // "Searching…" forever the first time anything cancelled a live request.
  const seq = useRef(0)
  // The runner is called from event handlers that already know what changed,
  // so the current values are read from a ref rather than from a closure that
  // was built before the change landed.
  const latest = useRef(props)
  useEffect(() => {
    latest.current = props
  })

  const run = useCallback(
    async (
      overrides: Partial<{
        range: LogTimeRange
        since: string
        until: string
        context: number
        archives: boolean
        boot: boolean
      }> = {},
    ) => {
      const p = latest.current
      const range = overrides.range ?? p.range
      const id = ++seq.current
      abort.current?.abort()
      const controller = new AbortController()
      abort.current = controller
      setLoading(true)
      setError(null)
      try {
        const window = resolveRange(range, overrides.since ?? p.since, overrides.until ?? p.until)
        if (window.since && window.until && Date.parse(window.since) >= Date.parse(window.until)) {
          throw new Error("The start of the log window must be before its end.")
        }
        const res = await get<LogSearchResult>(
          "/logs/search",
          {
            source: p.sourceId,
            ...filterQuery(p.filter),
            ...window,
            before: (overrides.context ?? p.context) || undefined,
            after: (overrides.context ?? p.context) || undefined,
            archives: (overrides.archives ?? p.archives) ? "true" : undefined,
            boot: (overrides.boot ?? p.boot) ? "true" : undefined,
            limit: 3000,
          },
          controller.signal,
        )
        if (id !== seq.current) return
        setResult(res)
      } catch (err) {
        if (id !== seq.current) return
        setError(errorMessage(err))
        setResult(null)
      } finally {
        if (id === seq.current) setLoading(false)
      }
    },
    [],
  )

  return { result, loading, error, run }
}

function SearchSummary({ result, loading }: { result: LogSearchResult | null; loading: boolean }) {
  if (loading) {
    return <span className="text-hint text-muted-foreground">Searching…</span>
  }
  if (!result) return <span className="text-hint text-muted-foreground">History</span>
  return (
    <span className="numeric text-hint text-muted-foreground">
      {plural(result.matched, "match", "matches")} in {result.scanned.toLocaleString()} lines ·{" "}
      {result.tookMillis}ms
    </span>
  )
}

/**
 * What was actually read. A search that spans a rotated set and finds
 * everything in yesterday's file is saying something a merged total hides, and
 * a truncated answer that does not admit it is worse than no answer.
 */
function SearchFooter({ result }: { result: LogSearchResult }) {
  const notes: string[] = []
  if (result.truncated) {
    notes.push(
      `Showing the most recent ${result.lines.length.toLocaleString()} of ${result.matched.toLocaleString()} matches — narrow the window to see the rest.`,
    )
  }
  if (!result.complete) {
    notes.push("The scan hit its time limit before reaching the end.")
  }
  const files = result.files.filter((f) => f.matched > 0 || f.error)
  if (notes.length === 0 && files.length <= 1) return null

  return (
    <PaneFooter className="gap-x-3 gap-y-1 px-3 text-hint text-muted-foreground">
      {notes.map((note) => (
        <span key={note}>{note}</span>
      ))}
      {files.length > 1 &&
        files.map((file) => (
          <span key={file.path} className="numeric" title={file.path}>
            {file.name}
            {file.archive && " (archive)"}:{" "}
            {file.error ?? `${file.matched.toLocaleString()} matched`}
          </span>
        ))}
    </PaneFooter>
  )
}

function LiveEmpty({
  state,
  error,
  filter,
  onSearchHistory,
  onClearFilter,
}: {
  state: string
  error: string | null
  filter: LogFilterState
  onSearchHistory: () => void
  onClearFilter: () => void
}) {
  if (error) {
    return <ErrorState error={new Error(error)} className="max-w-lg" />
  }
  if (state !== "open") {
    return (
      <EmptyState
        icon={ChartActivity}
        title={state === "connecting" ? "Connecting…" : "Not connected"}
        description={
          state === "connecting"
            ? "Opening the stream."
            : "The stream closed. It reconnects on its own; this page rides a tunnel that drops routinely."
        }
      />
    )
  }
  if (isFilterActive(filter)) {
    return (
      <EmptyState
        icon={MagnifyingGlassMinus}
        title="Nothing in the recent window matches"
        description="The filter was applied on the server, so this really is every matching line in the opening window — not a slice of it. History goes further back than a live tail can."
        action={
          <div className="flex flex-wrap justify-center gap-2">
            <Button size="sm" onClick={onSearchHistory}>
              <ClockRewind className="size-3.5" />
              Search this log&apos;s history
            </Button>
            <Button size="sm" variant="outline" onClick={onClearFilter}>
              Clear the filter
            </Button>
          </div>
        }
      />
    )
  }
  return (
    <EmptyState
      icon={ChartActivity}
      title="Nothing new yet"
      description="This log is quiet. New lines appear here the moment they are written."
    />
  )
}

function SearchEmpty({
  result,
  loading,
  error,
  source,
  archives,
  onIncludeArchives,
  onRun,
}: {
  result: LogSearchResult | null
  loading: boolean
  error: string | null
  source: LogSource
  archives: boolean
  onIncludeArchives: () => void
  onRun: () => void
}) {
  if (loading) {
    return (
      <EmptyState
        icon={MagnifyingGlass}
        title="Searching…"
        description="Reading the file server-side."
      />
    )
  }
  if (error) {
    return <ErrorState error={new Error(error)} className="max-w-lg" />
  }
  if (!result) {
    return (
      <EmptyState
        icon={ClockRewind}
        title="Search the whole log, not just the tail"
        description="Type a term and press Enter. The scan happens on the server, so this works on a file far too large to send to a browser — and it reads the rotated archives too, which is where last night's answer usually is."
        action={
          <Button size="sm" onClick={onRun}>
            <MagnifyingGlass className="size-3.5" />
            Search
          </Button>
        }
      />
    )
  }
  const hasArchives = (source.archives ?? 0) > 0
  return (
    <EmptyState
      icon={MagnifyingGlassMinus}
      title="No matches in that window"
      description={`Scanned ${result.scanned.toLocaleString()} lines in ${result.tookMillis}ms. Widen the window, relax the level filter, or look further back.`}
      action={
        hasArchives && !archives ? (
          <Button size="sm" onClick={onIncludeArchives}>
            Include the {source.archives} rotated {source.archives === 1 ? "archive" : "archives"}
          </Button>
        ) : undefined
      }
    />
  )
}
