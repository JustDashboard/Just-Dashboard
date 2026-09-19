"use client"

import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { MagnifyingGlass } from "@/components/icons"
import { cn } from "@/lib/utils"
import { get } from "@/lib/api"
import { relativeTime, timestamp } from "@/lib/format"
import type { DeploymentRequests, RequestEntry } from "@/lib/types"
import {
  CLASS_DOT,
  CLASS_HINT,
  REQUEST_RANGES,
  STATUS_CLASSES,
  latency,
  resolveRequestRange,
  type RequestRange,
  type StatusClass,
} from "@/lib/requests"
import { useSocket, type Envelope } from "@/hooks/use-socket"
import { usePoll } from "@/hooks/use-poll"
import { EmptyState, ErrorState } from "@/components/state"
import { Status } from "@/components/status-dot"
import { FilterChip, ChipCount } from "@/components/tabs"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { RequestChart } from "@/components/deploy/request-chart"
import { RequestConsole } from "@/components/deploy/request-console"

/** How many live rows the pane holds before the oldest fall off the bottom. */
const LIVE_BUFFER = 2000

export type RequestQuery = {
  range: RequestRange
  since?: string
  until?: string
  path: string
  classes: StatusClass[]
  methods: string[]
}

export const EMPTY_REQUEST_QUERY: RequestQuery = {
  range: "1h",
  path: "",
  classes: [],
  methods: [],
}

/**
 * The traffic a deployment served, as one pane.
 *
 * A window and a live tail rather than two tabs. The window is what the chart,
 * the chips and every reading are computed over; "Live" is a toggle that
 * prepends what arrives on top of it, because a request log is read newest
 * first and switching views to watch a deploy take its first traffic is a
 * worse answer than a switch that keeps the rows you were already reading.
 */
export function RequestsWorkspace({
  projectId,
  query,
  onQueryChange,
}: {
  projectId: number
  query: RequestQuery
  onQueryChange: (next: RequestQuery) => void
}) {
  const [live, setLive] = useState(false)
  const params = useMemo(() => requestParams(query), [query])

  // The window reloads on its own while nothing is streaming, so the readings
  // stay true without the reader pressing anything. With the live tail open it
  // stops: the socket is already the fresher answer, and a poll landing under
  // it would swap the rows out from beneath the one being read. Ten seconds
  // is cheap now — the server answers from what it holds and reads only what
  // the proxy appended since it last looked.
  const window = usePoll<DeploymentRequests>(
    (signal) => get<DeploymentRequests>(`/deploy/${projectId}/requests`, params, signal),
    live ? 0 : 10000,
    [projectId, JSON.stringify(params)],
  )
  const data = window.data

  // The tail picks up exactly where the window's rows end. A cursor rather
  // than a timestamp, because two requests can share a second and the window
  // may hold one of them; a socket that started "after the newest time" would
  // send the other again, or never.
  const tail = useLiveRequests(projectId, live ? params : null, data?.coverage.cursor)

  if (window.error && !data) {
    return (
      <div className="flex min-h-0 flex-1 items-center justify-center p-6">
        <ErrorState error={window.error} onRetry={window.refresh} />
      </div>
    )
  }
  if (!data) {
    return (
      <div className="flex min-h-0 flex-1 items-center justify-center p-6 text-hint text-muted-foreground">
        Reading the request record…
      </div>
    )
  }
  if (data.status !== "available") {
    return (
      <div className="flex min-h-0 flex-1 items-center justify-center p-6">
        <EmptyState
          title="No request record for this deployment"
          description={
            data.reason ??
            "Nothing records the requests this deployment serves. A deployment gets one once it has a public route."
          }
        />
      </div>
    )
  }

  const rows = live ? mergeLive(tail.entries, data.entries, data.coverage.cursor) : data.entries
  const counts = data.summary.classes

  return (
    <>
      <RequestFilterBar
        query={query}
        onQueryChange={onQueryChange}
        summary={data}
        live={live}
        onLiveChange={setLive}
      />

      {data.summary.buckets.length > 0 && (
        <RequestChart
          key={`${params.since}:${data.observedAt}`}
          className="animate-rise"
          buckets={data.summary.buckets}
          bucketSeconds={data.summary.bucketSeconds}
          latencyKnown={data.latency}
          onZoom={(from, to) =>
            onQueryChange({
              ...query,
              range: "custom" as RequestRange,
              since: from.toISOString(),
              until: to.toISOString(),
            })
          }
        />
      )}

      <RequestConsole
        entries={rows}
        summary={data.summary}
        latencyKnown={data.latency}
        paused={live ? tail.paused : undefined}
        onPausedChange={live ? tail.setPaused : undefined}
        held={tail.held}
        onFilterPath={(path) => onQueryChange({ ...query, path })}
        leading={
          <ClassChips
            selected={query.classes}
            counts={counts}
            onChange={(classes) => onQueryChange({ ...query, classes })}
          />
        }
        status={
          live ? (
            <Status
              state={
                tail.state === "open"
                  ? "running"
                  : tail.state === "connecting"
                    ? "restarting"
                    : "stopped"
              }
              label={
                tail.state === "open"
                  ? "Live"
                  : tail.state === "connecting"
                    ? "Connecting"
                    : "Disconnected"
              }
              live={tail.state === "open"}
              className="text-hint"
            />
          ) : (
            <span className="numeric whitespace-nowrap">
              {data.summary.total.toLocaleString()} in this window
            </span>
          )
        }
        footer={<WindowNotes data={data} />}
        empty={
          <EmptyState
            title="No requests in this window"
            description={
              query.path || query.classes.length > 0 || query.methods.length > 0
                ? "Nothing matched. Widen the window or clear the filters."
                : "Nothing has asked for this deployment in the window you chose. Widen it, or turn Live on and watch for the first request."
            }
          />
        }
      />
    </>
  )
}

/** The query the API takes, built once so the poll and the socket agree. */
function requestParams(query: RequestQuery) {
  const since = query.range === "custom" ? query.since : resolveRequestRange(query.range)
  return {
    since,
    until: query.range === "custom" ? query.until : undefined,
    path: query.path || undefined,
    classes: query.classes.length ? query.classes.join(",") : undefined,
    methods: query.methods.length ? query.methods.join(",") : undefined,
    limit: 500,
  }
}

/**
 * The live tail.
 *
 * Pausing holds what arrives rather than dropping it, which is the difference
 * between reading a busy deployment and choosing between reading and keeping.
 */
function useLiveRequests(
  projectId: number,
  params: Record<string, unknown> | null,
  after: number | undefined,
) {
  const [entries, setEntries] = useState<RequestEntry[]>([])
  const [heldEntries, setHeld] = useState<RequestEntry[]>([])
  const [paused, setPaused] = useState(false)
  const pausedRef = useRef(false)
  useEffect(() => {
    pausedRef.current = paused
  }, [paused])

  const query = useMemo(
    () =>
      params
        ? { ...params, since: undefined, until: undefined, after: after === undefined ? undefined : String(after) }
        : {},
    [params, after],
  )
  const queryKey = JSON.stringify(query)
  const [lastQuery, setLastQuery] = useState(queryKey)
  if (lastQuery !== queryKey) {
    setLastQuery(queryKey)
    setEntries([])
    setHeld([])
  }

  const onMessage = useCallback((envelope: Envelope) => {
    if (envelope.type !== "requests") return
    const batch = envelope.data as RequestEntry[]
    // Newest first, so a batch arrives on top and in reverse of the order the
    // proxy wrote it.
    const incoming = [...batch].reverse()
    if (pausedRef.current) {
      setHeld((prev) => cap([...incoming, ...prev]))
      return
    }
    setEntries((prev) => cap([...incoming, ...prev]))
  }, [])

  const { state } = useSocket(`/deploy/${projectId}/requests/stream`, {
    onMessage,
    query,
    enabled: Boolean(params),
  })

  const setPausedAndFlush = useCallback(
    (next: boolean) => {
      setPaused(next)
      if (next) return
      if (heldEntries.length) setEntries((prev) => cap([...heldEntries, ...prev]))
      setHeld([])
    },
    [heldEntries],
  )

  return { entries, state, paused, setPaused: setPausedAndFlush, held: heldEntries.length }
}

function cap(entries: RequestEntry[]) {
  return entries.length > LIVE_BUFFER ? entries.slice(0, LIVE_BUFFER) : entries
}

/**
 * What arrived live, over what the window already had.
 *
 * The socket continues from the window's cursor, so nothing should overlap;
 * the guard is for a socket that reconnected with an older cursor than the
 * window it now sits over.
 */
function mergeLive(live: RequestEntry[], windowRows: RequestEntry[], cursor: number) {
  if (live.length === 0) return windowRows
  const fresh = live.filter((entry) => !entry.seq || entry.seq > cursor)
  return [...fresh, ...windowRows]
}

function ClassChips({
  selected,
  counts,
  onChange,
}: {
  selected: StatusClass[]
  counts: Record<string, number>
  onChange: (classes: StatusClass[]) => void
}) {
  const present = STATUS_CLASSES.filter((klass) => (counts[klass] ?? 0) > 0 || selected.includes(klass))
  if (present.length === 0) return null
  return (
    <div className="flex shrink-0 items-center gap-1">
      {present.map((klass) => {
        const on = selected.includes(klass)
        return (
          <FilterChip
            key={klass}
            selected={on}
            title={CLASS_HINT[klass]}
            onClick={() =>
              onChange(on ? selected.filter((k) => k !== klass) : [...selected, klass])
            }
          >
            <span className={cn("size-1.5 rounded-full", CLASS_DOT[klass])} />
            {klass}
            {counts[klass] ? <ChipCount>{counts[klass].toLocaleString()}</ChipCount> : null}
          </FilterChip>
        )
      })}
    </div>
  )
}

function RequestFilterBar({
  query,
  onQueryChange,
  summary,
  live,
  onLiveChange,
}: {
  query: RequestQuery
  onQueryChange: (next: RequestQuery) => void
  summary: DeploymentRequests
  live: boolean
  onLiveChange: (live: boolean) => void
}) {
  const methods = summary.summary.methods.map((facet) => facet.value)
  return (
    <div className="flex min-h-11 shrink-0 flex-wrap items-center gap-2 border-b border-hairline px-2 py-1.5">
      <label className="relative flex min-w-48 flex-1 items-center">
        <MagnifyingGlass className="pointer-events-none absolute left-2.5 size-3.5 text-muted-foreground" />
        <Input
          value={query.path}
          onChange={(event) => onQueryChange({ ...query, path: event.target.value })}
          placeholder="Filter by path — /api, /assets"
          aria-label="Filter requests by path"
          className="h-8 pl-8 font-mono text-xs"
        />
      </label>

      {methods.length > 1 && (
        <div className="flex shrink-0 items-center gap-1">
          {methods.slice(0, 5).map((method) => {
            const on = query.methods.includes(method)
            return (
              <FilterChip
                key={method}
                selected={on}
                onClick={() =>
                  onQueryChange({
                    ...query,
                    methods: on
                      ? query.methods.filter((m) => m !== method)
                      : [...query.methods, method],
                  })
                }
              >
                {method}
              </FilterChip>
            )
          })}
        </div>
      )}

      <Select
        value={query.range}
        onValueChange={(value) => onQueryChange({ ...query, range: value as RequestRange })}
      >
        <SelectTrigger size="sm" className="w-44 shrink-0" aria-label="Request window">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          {REQUEST_RANGES.map((range) => (
            <SelectItem key={range.id} value={range.id}>
              {range.label}
            </SelectItem>
          ))}
          {query.range === "custom" && (
            <SelectItem value="custom">
              {query.since ? `From ${timestamp(query.since)}` : "Custom range"}
            </SelectItem>
          )}
        </SelectContent>
      </Select>

      <FilterChip selected={live} onClick={() => onLiveChange(!live)} className="shrink-0">
        {live && <span className="size-1.5 rounded-full bg-success animate-breathe" />}
        Live
      </FilterChip>
    </div>
  )
}

/** What the window is, under the rows: where it came from and how far it goes. */
function WindowNotes({ data }: { data: DeploymentRequests }) {
  const notes: React.ReactNode[] = []
  if (!data.latency) {
    notes.push(
      <span key="latency">
        {data.driver === "nginx" ? "nginx" : "This ingress"} records no request duration, so there is
        no timing column.
      </span>,
    )
  }
  // "No requests in the last 24 hours" and "the record here only goes back
  // an hour" are different answers, and only the coverage can tell them apart.
  if (!data.complete && data.coverage.from) {
    notes.push(
      <span key="from">
        What is held begins {timestamp(data.coverage.from)}; earlier requests have rolled off, so the
        figures are a floor.
      </span>,
    )
  }
  if (data.coverage.stale) {
    notes.push(
      <span key="stale">
        The ingress did not answer just now — showing the read from{" "}
        {relativeTime(data.coverage.refreshedAt)}.
      </span>,
    )
  }
  if (data.summary.latency) {
    notes.push(
      <span key="p" className="numeric whitespace-nowrap">
        p50 {latency(data.summary.latency.p50)} · p95 {latency(data.summary.latency.p95)} · p99{" "}
        {latency(data.summary.latency.p99)}
      </span>,
    )
  }
  if (notes.length === 0) return null
  return (
    <span className="ml-auto flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1">{notes}</span>
  )
}
