"use client"

import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { MagnifyingGlass } from "@/components/icons"
import { cn } from "@/lib/utils"
import { API_BASE, get } from "@/lib/api"
import { relativeTime, timestamp } from "@/lib/format"
import { notify } from "@/lib/toast"
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
import { useAuth } from "@/hooks/use-auth"
import { EmptyState, ErrorState } from "@/components/state"
import { Status } from "@/components/status-dot"
import { FilterChip, ChipCount } from "@/components/tabs"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { blockAddress } from "@/components/security/address-verbs"
import { RequestChart, type ChartMarker } from "@/components/deploy/request-chart"
import { RequestConsole } from "@/components/deploy/request-console"
import { TrafficFacets } from "@/components/deploy/traffic-facets"

/** How many live rows the pane holds before the oldest fall off the bottom. */
const LIVE_BUFFER = 2000

export type RequestQuery = {
  range: RequestRange
  since?: string
  until?: string
  path: string
  client: string
  classes: StatusClass[]
  methods: string[]
  /** Page views only: no prefetches, scripts, icons, probes. */
  pages: boolean
}

export const EMPTY_REQUEST_QUERY: RequestQuery = {
  range: "1h",
  path: "",
  client: "",
  classes: [],
  methods: [],
  pages: false,
}

export type RequestsView = "requests" | "insights"

/**
 * The traffic a deployment served, as one pane with two readings of it.
 *
 * Requests is the rows; Insights is what they add up to — the same window,
 * the same filter, the same chart, and under it the pages, clients, agents,
 * sources and slowest routes. A window and a live tail rather than two tabs:
 * the window is what every reading is computed over; "Live" is a toggle that
 * prepends what arrives on top of it, because a request log is read newest
 * first and switching views to watch a deploy take its first traffic is a
 * worse answer than a switch that keeps the rows you were already reading.
 */
export function RequestsWorkspace({
  projectId,
  view,
  query,
  onQueryChange,
  markers,
  outputHref,
  onEventsAround,
}: {
  projectId: number
  view: RequestsView
  query: RequestQuery
  onQueryChange: (next: RequestQuery) => void
  /** Releases going live and the container's exits, for the chart. */
  markers: ChartMarker[]
  outputHref?: (entry: RequestEntry) => string | undefined
  onEventsAround?: (entry: RequestEntry) => void
}) {
  const { can } = useAuth()
  const [live, setLive] = useState(false)
  const [blocking, setBlocking] = useState<string | null>(null)
  const params = useMemo(() => requestParams(query), [query])

  // The window reloads on its own while nothing is streaming, so the readings
  // stay true without the reader pressing anything. With the live tail open it
  // stops: the socket is already the fresher answer, and a poll landing under
  // it would swap the rows out from beneath the one being read. Ten seconds
  // is cheap — the server answers from what it holds and reads only what the
  // proxy appended since it last looked.
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

  const block = async (ip: string) => {
    setBlocking(ip)
    try {
      await blockAddress(ip, `blocked from deployment ${projectId} requests`)
      notify.success(`${ip} blocked`, {
        description: "A deny rule now sits in front of every allow. Unlike a ban, it does not expire.",
      })
    } catch (error) {
      notify.error("Could not block the address", { description: String(error) })
    } finally {
      setBlocking(null)
    }
  }

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
  const exportHref = `${API_BASE}/deploy/${projectId}/requests/export?${new URLSearchParams(
    Object.entries(params)
      .filter(([, v]) => v !== undefined)
      .map(([k, v]) => [k, String(v)]),
  ).toString()}`

  return (
    <>
      <RequestFilterBar
        query={query}
        onQueryChange={onQueryChange}
        summary={data}
        live={live}
        onLiveChange={setLive}
        exportHref={exportHref}
      />

      {data.summary.buckets.length > 0 && (
        <RequestChart
          key={`${params.since}:${data.observedAt}`}
          className="animate-rise"
          buckets={data.summary.buckets}
          bucketSeconds={data.summary.bucketSeconds}
          latencyKnown={data.latency}
          markers={markers}
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

      {view === "insights" ? (
        <div key={`${params.since}:${data.observedAt}`} className="min-h-0 flex-1 animate-rise overflow-auto">
          <TrafficFacets
            summary={data.summary}
            slowest={data.slowest}
            latencyKnown={data.latency}
            onFilterPath={(path) => onQueryChange({ ...query, path })}
            onFilterClient={(client) => onQueryChange({ ...query, client })}
            onBlock={can("system.admin") ? (ip) => void block(ip) : undefined}
            blocking={blocking}
          />
          <div className="border-t border-hairline px-3 py-2 text-hint text-muted-foreground">
            <WindowNotes data={data} />
          </div>
        </div>
      ) : (
        <RequestConsole
          entries={rows}
          summary={data.summary}
          latencyKnown={data.latency}
          paused={live ? tail.paused : undefined}
          onPausedChange={live ? tail.setPaused : undefined}
          held={tail.held}
          onFilterPath={(path) => onQueryChange({ ...query, path })}
          outputHref={outputHref}
          onEventsAround={onEventsAround}
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
                {query.pages ? "" : ` · ${data.summary.pages.toLocaleString()} page views`}
              </span>
            )
          }
          footer={<WindowNotes data={data} />}
          empty={
            <EmptyState
              title="No requests in this window"
              description={
                query.path || query.client || query.classes.length > 0 || query.methods.length > 0 || query.pages
                  ? "Nothing matched. Widen the window or clear the filters."
                  : "Nothing has asked for this deployment in the window you chose. Widen it, or turn Live on and watch for the first request."
              }
            />
          }
        />
      )}
    </>
  )
}

/** The query the API takes, built once so the poll, the socket and the export agree. */
function requestParams(query: RequestQuery) {
  const since = query.range === "custom" ? query.since : resolveRequestRange(query.range)
  return {
    since,
    until: query.range === "custom" ? query.until : undefined,
    path: query.path || undefined,
    client: query.client || undefined,
    classes: query.classes.length ? query.classes.join(",") : undefined,
    methods: query.methods.length ? query.methods.join(",") : undefined,
    pages: query.pages ? "true" : undefined,
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
 * What arrived live, over what the window already had. The socket continues
 * from the window's cursor, so nothing should overlap; the guard is for a
 * socket that reconnected with an older cursor than the window it now sits over.
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
  exportHref,
}: {
  query: RequestQuery
  onQueryChange: (next: RequestQuery) => void
  summary: DeploymentRequests
  live: boolean
  onLiveChange: (live: boolean) => void
  exportHref: string
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

      {query.client && (
        <FilterChip selected onClick={() => onQueryChange({ ...query, client: "" })} title="Clear the client filter">
          from {query.client} ×
        </FilterChip>
      )}

      {/* Page views: the number a person means by "visits". Off by default,
          because the honest record is everything the proxy answered. */}
      <FilterChip
        selected={query.pages}
        onClick={() => onQueryChange({ ...query, pages: !query.pages })}
        title="Only page views — no prefetches, scripts, icons or scanner probes"
      >
        Pages only
      </FilterChip>

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

      <Button size="sm" variant="ghost" className="h-7 shrink-0 px-2 text-xs" asChild>
        <a href={exportHref} download title="Download this window as CSV — every matching request, not only the rows shown">
          Export
        </a>
      </Button>
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
