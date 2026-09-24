"use client"

import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import Link from "next/link"
import { Cross, Download, FileText, Globe, MagnifyingGlass, Stopwatch } from "@/components/icons"
import { cn } from "@/lib/utils"
import { API_BASE, get } from "@/lib/api"
import { minuteSpan, relativeTime, timestamp } from "@/lib/format"
import { notify } from "@/lib/toast"
import { networkOf } from "@/lib/clients"
import type { DeploymentRequests, RequestEntry, TrafficAlert } from "@/lib/types"
import {
  CLASS_DOT,
  CLASS_HINT,
  CLASS_TEXT,
  REQUEST_RANGES,
  STATUS_CLASSES,
  latency,
  latencyTone,
  resolveRequestRange,
  statusClass,
  type RequestRange,
  type StatusClass,
} from "@/lib/requests"
import { useSocket, type Envelope, type SocketState } from "@/hooks/use-socket"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { NETWORK_GLYPH } from "@/components/client-mark"
import { FactDot } from "@/components/metrics/host-identity"
import { ProductGlyph } from "@/components/product-logo"
import { EmptyState, ErrorState, LoadingRows } from "@/components/state"
import { Status } from "@/components/status-dot"
import { ChipCount, ChipStrip, FilterChip } from "@/components/tabs"
import { Button } from "@/components/ui/button"
import {
  InputGroup,
  InputGroupAddon,
  InputGroupInput,
  InputGroupToggle,
} from "@/components/ui/input-group"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Skeleton } from "@/components/ui/skeleton"
import { blockAddress } from "@/components/security/address-verbs"
import { RequestChart, type ChartMarker } from "@/components/deploy/request-chart"
import { RequestConsole } from "@/components/deploy/request-console"
import { Address, LiveDot, socketReading } from "@/components/deploy/request-marks"
import { TrafficFacets } from "@/components/deploy/traffic-facets"

/** How many live rows the pane holds before the oldest fall off the bottom. */
const LIVE_BUFFER = 2000

export type RequestQuery = {
  range: RequestRange
  since?: string
  until?: string
  path: string
  client: string
  /** One of the deployment's hostnames, from the Domains list. */
  host: string
  /** Exact codes, from the Status codes list — "every 404", not "every 4xx". */
  statuses: number[]
  /** At least this slow, from a mark on the response-time ladder. */
  minMs?: number
  classes: StatusClass[]
  methods: string[]
  /** Page views only: no prefetches, scripts, icons, probes. */
  pages: boolean
}

export const EMPTY_REQUEST_QUERY: RequestQuery = {
  range: "1h",
  path: "",
  client: "",
  host: "",
  statuses: [],
  classes: [],
  methods: [],
  pages: false,
}

export type RequestsView = "requests" | "insights"

/**
 * The traffic a deployment served, as one pane with two readings of it.
 *
 * Requests is the rows; Insights is what they add up to — the same window,
 * the same filter, the same chart, and under it the response times, pages,
 * clients, agents, sources and slowest routes. A window and a live tail rather
 * than two tabs: the window is what every reading is computed over; "Live" is
 * a toggle that prepends what arrives on top of it, because a request log is
 * read newest first and switching views to watch a deploy take its first
 * traffic is a worse answer than a switch that keeps the rows you were
 * already reading.
 *
 * Every narrowing is one query — the path, the address, the domain, the
 * exact code, the latency floor, the families, the methods, page views — so
 * the poll, the live socket and the CSV export can never disagree about what
 * is being shown. What is narrowed is drawn as chips beside the path field,
 * each with its own mark and its own way back.
 */
export function RequestsWorkspace({
  projectId,
  view,
  query,
  onQueryChange,
  markers,
  alerts,
  routed,
  outputHref,
  onEventsAround,
}: {
  projectId: number
  view: RequestsView
  query: RequestQuery
  onQueryChange: (next: RequestQuery) => void
  /** Releases going live and the container's exits, for the chart. */
  markers: ChartMarker[]
  /** The deployment's traffic alerts: a latency rule is a line on the p95 chart. */
  alerts?: TrafficAlert[]
  /** Whether any domain routes here; undefined while that is not yet known. */
  routed?: boolean
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

  const onReset = useCallback(
    () => onQueryChange({ ...query, range: "1h", since: undefined, until: undefined }),
    [query, onQueryChange],
  )

  const block = async (ip: string) => {
    setBlocking(ip)
    try {
      await blockAddress(ip, `blocked from deployment ${projectId} requests`)
      notify.success(`${ip} blocked`, {
        description:
          "A deny rule now sits in front of every allow. Unlike a ban, it does not expire.",
      })
    } catch (error) {
      notify.error("Could not block the address", { description: String(error) })
    } finally {
      setBlocking(null)
    }
  }
  const onBlock = can("system.admin") ? (ip: string) => void block(ip) : undefined

  const exportHref = `${API_BASE}/deploy/${projectId}/requests/export?${new URLSearchParams(
    Object.entries(params)
      .filter(([, v]) => v !== undefined)
      .map(([k, v]) => [k, String(v)]),
  ).toString()}`

  if (window.error && !data) {
    return (
      <div className="flex min-h-0 flex-1 items-center justify-center p-6">
        <ErrorState error={window.error} onRetry={window.refresh} />
      </div>
    )
  }
  if (data && data.status !== "available") {
    // The next move depends on which nothing this is. With no domain there
    // is no route to record, and adding one is the answer. With a route and
    // no reading of the ingress at all — no driver came back — the proxy is
    // what to look at. With both, the record exists and simply has no request
    // in it yet, and there is nothing to press.
    const next =
      routed === false ? (
        <Button size="sm" variant="outline" asChild>
          <Link href={`/deploy/${projectId}/settings/domains`}>Add a domain</Link>
        </Button>
      ) : !data.driver ? (
        <Button size="sm" variant="outline" asChild>
          <Link href="/proxy">Open Proxy</Link>
        </Button>
      ) : undefined
    return (
      <div className="flex min-h-0 flex-1 items-center justify-center p-6">
        <EmptyState
          icon={Globe}
          title="No request record for this deployment"
          description={
            data.reason ??
            "Nothing records the requests this deployment serves. A deployment gets one once it has a public route."
          }
          action={next}
        />
      </div>
    )
  }

  const bar = (
    <RequestFilterBar
      query={query}
      onQueryChange={onQueryChange}
      methods={data?.summary.methods.map((facet) => facet.value) ?? []}
      live={live}
      liveState={tail.state}
      onLiveChange={setLive}
      exportHref={exportHref}
    />
  )

  // The shape that is coming — the chart's band and the rows under it — so
  // the pane does not jump when the first read lands.
  if (!data) {
    return (
      <>
        {bar}
        <div className="shrink-0 border-b border-hairline px-3 py-3">
          <Skeleton className="h-36 w-full" />
        </div>
        <LoadingRows rows={8} className="min-h-0 flex-1 overflow-hidden px-3 py-3" />
      </>
    )
  }

  const rows = live ? mergeLive(tail.entries, data.entries, data.coverage.cursor) : data.entries
  const counts = data.summary.classes
  const threshold = alertLine(alerts)
  const narrowed =
    query.path !== "" ||
    query.client !== "" ||
    query.host !== "" ||
    query.statuses.length > 0 ||
    query.minMs !== undefined ||
    query.classes.length > 0 ||
    query.methods.length > 0 ||
    query.pages

  return (
    <>
      {bar}

      {data.summary.buckets.length > 0 && (
        <RequestChart
          key={`chart:${params.since}`}
          buckets={data.summary.buckets}
          bucketSeconds={data.summary.bucketSeconds}
          latencyKnown={data.latency}
          showLatency={view === "insights"}
          markers={markers}
          alerts={alerts}
          total={data.summary.total}
          failed={counts["5xx"] ?? 0}
          custom={query.range === "custom"}
          onReset={onReset}
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
        <div key={`insights:${params.since}`} className="min-h-0 flex-1 animate-rise overflow-auto">
          <TrafficFacets
            summary={data.summary}
            slowest={data.slowest}
            latencyKnown={data.latency}
            threshold={threshold}
            onFilterPath={(path) => onQueryChange({ ...query, path })}
            onFilterClient={(client) => onQueryChange({ ...query, client })}
            onFilterHost={(host) => onQueryChange({ ...query, host })}
            onFilterStatus={(code) => onQueryChange({ ...query, statuses: [code] })}
            onFilterSlow={(ms) => onQueryChange({ ...query, minMs: Math.floor(ms) })}
            onBlock={onBlock}
            blocking={blocking}
          />
          <WindowNotes data={data} className="border-t border-hairline px-3 py-2" />
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
          onFilterClient={(client) => onQueryChange({ ...query, client })}
          onBlock={onBlock}
          blocking={blocking}
          outputHref={outputHref}
          onEventsAround={onEventsAround}
          leading={
            <ClassChips
              selected={query.classes}
              counts={counts}
              onChange={(classes) => onQueryChange({ ...query, classes })}
            />
          }
          // The window's total is the chart's head and the chips' sum, so
          // the footer starts at what neither says: the stream's state, or
          // how many of the window were page views.
          status={
            live ? (
              <Status {...socketReading(tail.state)} className="text-hint" />
            ) : !query.pages ? (
              <span className="numeric whitespace-nowrap">
                {data.summary.pages.toLocaleString()} page views
              </span>
            ) : undefined
          }
          footer={<WindowNotes data={data} percentiles />}
          empty={
            <EmptyState
              title="No requests in this window"
              description={
                narrowed
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

/** The line the tightest enabled latency alert watches, if any rule does. */
function alertLine(alerts: TrafficAlert[] | undefined) {
  const lines = (alerts ?? [])
    .filter((rule) => rule.enabled && rule.kind === "latency")
    .map((rule) => rule.threshold)
  return lines.length > 0 ? Math.min(...lines) : undefined
}

/** The query the API takes, built once so the poll, the socket and the export agree. */
function requestParams(query: RequestQuery) {
  const since = query.range === "custom" ? query.since : resolveRequestRange(query.range)
  return {
    since,
    until: query.range === "custom" ? query.until : undefined,
    path: query.path || undefined,
    client: query.client || undefined,
    host: query.host || undefined,
    status: query.statuses.length ? query.statuses.join(",") : undefined,
    minMs: query.minMs,
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
        ? {
            ...params,
            since: undefined,
            until: undefined,
            after: after === undefined ? undefined : String(after),
          }
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

/**
 * The status families as chips. Each dot is its family's colour in the rows
 * below — the chip is the legend for the codes it filters — and a chosen
 * chip's count takes that colour too, so "5xx 13" says which 13.
 */
function ClassChips({
  selected,
  counts,
  onChange,
}: {
  selected: StatusClass[]
  counts: Record<string, number>
  onChange: (classes: StatusClass[]) => void
}) {
  const present = STATUS_CLASSES.filter(
    (klass) => (counts[klass] ?? 0) > 0 || selected.includes(klass),
  )
  if (present.length === 0) return null
  return (
    <>
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
            {counts[klass] ? (
              <ChipCount className={cn(on && CLASS_TEXT[klass], on && "opacity-100")}>
                {counts[klass].toLocaleString()}
              </ChipCount>
            ) : null}
          </FilterChip>
        )
      })}
    </>
  )
}

/**
 * The window's controls: the path field with its page-views switch inside
 * its edge (§7: a binary that belongs to a field is a toggle in the field's
 * group), the narrowings in force as chips with their marks, the methods, and
 * the window, Live and Export.
 *
 * On a phone it is three deliberate rows — the field, the chips (scrolling
 * sideways), the window with Live and Export — where it used to wrap into
 * three accidental ones with Export alone at the left of the last. From `lg`
 * it is one row, the controls at the right end.
 */
function RequestFilterBar({
  query,
  onQueryChange,
  methods,
  live,
  liveState,
  onLiveChange,
  exportHref,
}: {
  query: RequestQuery
  onQueryChange: (next: RequestQuery) => void
  methods: string[]
  live: boolean
  liveState: SocketState
  onLiveChange: (live: boolean) => void
  exportHref: string
}) {
  const network = query.client ? networkOf(query.client) : undefined
  const Place = network ? NETWORK_GLYPH[network.kind] : undefined
  const chips = [
    query.client && (
      <FilterChip
        key="client"
        selected
        aria-label="Clear the client filter"
        title={`Only requests from ${query.client} — press to clear`}
        onClick={() => onQueryChange({ ...query, client: "" })}
      >
        {network?.product ? (
          <ProductGlyph id={network.product} className="size-3" />
        ) : (
          Place && <Place aria-hidden className="size-3 text-muted-foreground" />
        )}
        <Address ip={query.client} />
        <Cross aria-hidden className="size-3 text-muted-foreground" />
      </FilterChip>
    ),
    query.host && (
      <FilterChip
        key="host"
        selected
        aria-label="Clear the domain filter"
        title={`Only requests to ${query.host} — press to clear`}
        onClick={() => onQueryChange({ ...query, host: "" })}
      >
        <Globe aria-hidden className="size-3 text-muted-foreground" />
        <span className="font-mono">{query.host}</span>
        <Cross aria-hidden className="size-3 text-muted-foreground" />
      </FilterChip>
    ),
    ...query.statuses.map((code) => (
      <FilterChip
        key={`status:${code}`}
        selected
        aria-label={`Clear the ${code} filter`}
        title={`Only requests answered ${code} — press to clear`}
        onClick={() =>
          onQueryChange({ ...query, statuses: query.statuses.filter((c) => c !== code) })
        }
      >
        <span className={cn("numeric font-mono", CLASS_TEXT[statusClass(code)])}>{code}</span>
        <Cross aria-hidden className="size-3 text-muted-foreground" />
      </FilterChip>
    )),
    query.minMs !== undefined && (
      <FilterChip
        key="slow"
        selected
        aria-label="Clear the slow filter"
        title="Only requests at least this slow — press to clear"
        onClick={() => onQueryChange({ ...query, minMs: undefined })}
      >
        <Stopwatch aria-hidden className="size-3 text-muted-foreground" />
        <span className={cn("numeric", latencyTone(query.minMs) === "warning" && "text-warning")}>
          slower than {latency(query.minMs)}
        </span>
        <Cross aria-hidden className="size-3 text-muted-foreground" />
      </FilterChip>
    ),
  ].filter(Boolean)

  // The window's methods are counted after the filter, so choosing POST
  // leaves only POST — and the chosen chip is the one way back. The chosen
  // ones stay drawn whatever the window now holds, as the class chips do.
  // The label is plain: in a row of chips a blue POST read as the chosen one
  // (§3 — selection is the chip's fill, never a hue), and the method's hue
  // belongs to the rows it describes.
  const methodList = [...new Set([...query.methods, ...methods])]
  const methodChips =
    methodList.length > 1 || query.methods.length > 0
      ? methodList.slice(0, 5).map((method) => {
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
              <span className="font-mono text-hint">{method}</span>
            </FilterChip>
          )
        })
      : []

  return (
    <div className="flex shrink-0 flex-wrap items-center gap-x-2 gap-y-1.5 border-b border-hairline px-2 py-1.5">
      <InputGroup className="h-10 basis-full sm:h-8 lg:max-w-md lg:min-w-64 lg:flex-1 lg:basis-0">
        <InputGroupAddon className="border-r-0 pr-0">
          <MagnifyingGlass />
        </InputGroupAddon>
        <InputGroupInput
          value={query.path}
          onChange={(event) => onQueryChange({ ...query, path: event.target.value })}
          placeholder="Filter by path — /api, /assets"
          aria-label="Filter requests by path"
          className="font-mono placeholder:font-sans sm:text-xs"
        />
        {/* Page views: the number a person means by "visits". Off by default,
            because the honest record is everything the proxy answered. */}
        <InputGroupAddon align="inline-end" className="gap-0 p-0">
          <InputGroupToggle
            icon={FileText}
            label="Pages only"
            aria-label="Pages only"
            pressed={query.pages}
            onPressedChange={(pages) => onQueryChange({ ...query, pages })}
          />
        </InputGroupAddon>
      </InputGroup>

      {/* One line from `sm` up, scrolling sideways under its own edge: three
          narrowings wrapped the strip into a ragged block in the middle of
          the bar, with the field and the window centred against it. Beside
          the field it takes two shares to the field's one — the field is
          usable at its floor, and the chips are what the reader is reading. */}
      {chips.length + methodChips.length > 0 && (
        <ChipStrip className="scroll-affordance flex-2 max-sm:-mx-2 max-sm:basis-full max-sm:px-2 sm:flex-nowrap sm:overflow-x-auto">
          {chips}
          {methodChips}
        </ChipStrip>
      )}

      <div className="ml-auto flex items-center gap-1.5 max-sm:basis-full">
        <Select
          value={query.range}
          onValueChange={(value) => onQueryChange({ ...query, range: value as RequestRange })}
        >
          <SelectTrigger
            size="sm"
            className="w-40 shrink-0 max-sm:w-auto max-sm:flex-1"
            aria-label="Request window"
          >
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
                {query.since ? minuteSpan(query.since, query.until) : "Custom range"}
              </SelectItem>
            )}
          </SelectContent>
        </Select>

        {/* The dot is a claim about the socket (§11): it breathes only once the
            tail is open, sits still in amber while it connects, goes grey when
            the socket drops — the footer's words, in the same tones — and is
            not drawn at all while Live is off. */}
        <FilterChip
          selected={live}
          onClick={() => onLiveChange(!live)}
          title="Follow new requests as they arrive, on top of this window"
          className="shrink-0 max-sm:h-10"
        >
          {live && <LiveDot state={liveState} />}
          Live
        </FilterChip>

        <Button
          size="sm"
          variant="ghost"
          className="h-7 shrink-0 px-2 text-xs max-sm:h-10 max-sm:px-3"
          asChild
        >
          <a
            href={exportHref}
            download
            aria-label="Export"
            title="Download this window as CSV — every matching request, not only the rows shown"
          >
            <Download className="size-3.5" />
            <span className="max-sm:hidden">Export</span>
          </a>
        </Button>
      </div>
    </div>
  )
}

/**
 * What the window is: where it came from and how far it goes. On Requests it
 * ends on the distribution in three figures, each amber past a second; on
 * Insights the ladder draws the same numbers, so they are not said twice.
 */
function WindowNotes({
  data,
  percentiles,
  className,
}: {
  data: DeploymentRequests
  percentiles?: boolean
  className?: string
}) {
  const notes: React.ReactNode[] = []
  if (!data.latency) {
    notes.push(
      <span key="latency">
        {data.driver === "nginx" ? "nginx" : "This ingress"} records no request duration, so there
        is no timing column.
      </span>,
    )
  }
  if (!data.complete && data.coverage.from) {
    notes.push(
      <span key="from">
        What is held begins {timestamp(data.coverage.from)}; earlier requests have rolled off, so
        the figures are a floor.
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
  const spread = data.summary.latency
  if (percentiles && spread) {
    notes.push(
      <span key="p" className="numeric flex items-center gap-2 whitespace-nowrap max-sm:basis-full">
        {(["p50", "p95", "p99"] as const).map((name, i) => (
          <span key={name} className="flex items-center gap-2">
            {i > 0 && <FactDot />}
            <span>
              {name}{" "}
              <span
                className={cn(
                  latencyTone(spread[name]) === "warning" && "font-medium text-warning",
                )}
              >
                {latency(spread[name])}
              </span>
            </span>
          </span>
        ))}
      </span>,
    )
  }
  if (notes.length === 0) return null
  return (
    <span
      className={cn(
        "ml-auto flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 text-hint text-muted-foreground",
        className,
      )}
    >
      {notes}
    </span>
  )
}
