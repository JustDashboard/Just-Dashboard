"use client"

import { useMemo, useRef, useState } from "react"
import Link from "next/link"
import { useRouter, useSearchParams } from "next/navigation"
import { ArrowLeft, Cross, Download, Globe } from "@/components/icons"
import { downloadUrl, get } from "@/lib/api"
import { bytes, minuteSpan } from "@/lib/format"
import { latency, perMinute, REQUEST_RANGES, type RequestRange } from "@/lib/requests"
import type { RequestEntry, SiteTraffic, SiteTrafficSummary, SiteTrafficTail } from "@/lib/types"
import { useSessionState } from "@/lib/view-state"
import { usePoll } from "@/hooks/use-poll"
import { Page, PageContext } from "@/components/page"
import { Panel, PanelBody, PanelHeader, Pane } from "@/components/panel"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { BarList } from "@/components/bar-list"
import { EmptyState, ErrorState, LoadingPanel } from "@/components/state"
import { FilterChip, tabClasses } from "@/components/tabs"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { RequestChart } from "@/components/deploy/request-chart"
import { RequestConsole } from "@/components/deploy/request-console"
import { TrafficFacets } from "@/components/deploy/traffic-facets"
import { SiteErrors } from "@/components/proxy/site-errors"
import {
  busiestItems,
  compactCount,
  errorRateTone,
  share,
  trafficHref,
  type TrafficView,
} from "@/components/proxy/site-traffic"

/** What the reader narrowed the window to, from the facets and the rows. */
type Narrowing = {
  path: string
  client: string
  host: string
  status?: number
  minMs?: number
  since?: string
  until?: string
}

const NONE: Narrowing = { path: "", client: "", host: "" }

/** The query the window, the tail and the export share, so the three agree. */
function trafficQuery(range: RequestRange, n: Narrowing) {
  return {
    window: range === "custom" ? undefined : range,
    since: n.since,
    until: n.until,
    path: n.path || undefined,
    client: n.client || undefined,
    host: n.host || undefined,
    status: n.status,
    minMs: n.minMs,
  }
}

/**
 * One nginx site's traffic and errors, read from the logs its file names; with
 * no ?site= it is every site's last hour and nginx's own error log. The chart,
 * facets and console are the deployment Logs page's, answered in its shape.
 */
export function SiteTrafficView() {
  const params = useSearchParams()
  const router = useRouter()
  const site = params.get("site") ?? undefined
  const [view, setView] = useState<TrafficView>(
    params.get("view") === "errors" ? "errors" : "requests",
  )
  const [range, setRange] = useSessionState<RequestRange>("proxy.traffic.range", "1h")
  const [narrowing, setNarrowing] = useState<Narrowing>(NONE)
  // Each site starts from its whole window; a narrowing made on one site is
  // not a question about the next.
  const [narrowedFor, setNarrowedFor] = useState(site)
  if (narrowedFor !== site) {
    setNarrowedFor(site)
    setNarrowing(NONE)
  }
  const shownRange: RequestRange = range === "custom" && !narrowing.since ? "1h" : range
  const query = trafficQuery(shownRange, narrowing)
  // The error log is read by preset; a span dragged out of the chart reads
  // the day around it.
  const span = shownRange === "custom" ? "24h" : shownRange

  const summary = usePoll(
    (signal) => get<SiteTrafficSummary>("/proxy/traffic", undefined, signal),
    60_000,
    [],
    { enabled: !site },
  )
  const traffic = usePoll(
    (signal) => get<SiteTraffic>(`/proxy/traffic/${encodeURIComponent(site ?? "")}`, query, signal),
    30_000,
    [site ?? "", JSON.stringify(query)],
    { enabled: Boolean(site) },
  )
  const data = traffic.error ? undefined : traffic.data

  const header = (
    <PageContext
      eyebrow={
        <span className="flex min-w-0 items-center gap-1.5">
          <Link
            href={site ? "/proxy/traffic" : "/proxy"}
            className="flex items-center gap-1 font-medium hover:text-foreground"
          >
            <ArrowLeft className="size-3" /> {site ? "Traffic" : "Proxy"}
          </Link>
          {site && <span className="truncate font-medium text-foreground">/ {site}</span>}
        </span>
      }
      title={site ? `${site} traffic` : "Traffic"}
      actions={
        <div className="flex items-center gap-2">
          <Select value={shownRange} onValueChange={(value) => setRange(value as RequestRange)}>
            <SelectTrigger size="sm" className="w-40 shrink-0" aria-label="Traffic window">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {REQUEST_RANGES.map((r) => (
                <SelectItem key={r.id} value={r.id}>
                  {r.label}
                </SelectItem>
              ))}
              {shownRange === "custom" && narrowing.since && (
                <SelectItem value="custom">
                  {minuteSpan(narrowing.since, narrowing.until)}
                </SelectItem>
              )}
            </SelectContent>
          </Select>
          {site && data?.status === "available" && (
            <Button asChild size="sm" variant="outline">
              <a
                href={downloadUrl(`/proxy/traffic/${encodeURIComponent(site)}/export`, query)}
                download
                title="Download this window as CSV — every matching request, not only the rows shown"
              >
                <Download className="size-3.5" />
                Export
              </a>
            </Button>
          )}
        </div>
      }
    />
  )

  const tabs = (
    <nav aria-label="Traffic views" className="flex gap-1 border-b border-hairline">
      {(
        [
          ["requests", site ? "Requests" : "Sites"],
          ["errors", "Errors"],
        ] as const
      ).map(([key, label]) => (
        <button
          key={key}
          type="button"
          aria-pressed={view === key}
          onClick={() => setView(key)}
          className={tabClasses(view === key, "h-10")}
        >
          {label}
        </button>
      ))}
    </nav>
  )

  if (!site) {
    const sites = summary.error ? undefined : summary.data?.sites
    return (
      <Page className="animate-rise">
        {header}
        <FleetReadings sites={sites} loading={summary.loading} />
        {tabs}
        {view === "errors" ? (
          <SiteErrors range={span} />
        ) : summary.error ? (
          <ErrorState error={summary.error} onRetry={summary.refresh} />
        ) : !sites ? (
          <LoadingPanel />
        ) : (
          <BusiestSites sites={sites} onOpen={(name) => router.push(trafficHref(name))} />
        )}
      </Page>
    )
  }

  return (
    <Page className="animate-rise">
      {header}
      <SiteReadings data={data} loading={traffic.loading} />
      {tabs}
      {view === "errors" ? (
        <SiteErrors site={site} range={span} />
      ) : traffic.error ? (
        <ErrorState error={traffic.error} onRetry={traffic.refresh} />
      ) : !data ? (
        <LoadingPanel />
      ) : data.status !== "available" ? (
        <EmptyState
          icon={Globe}
          title="No requests to show"
          description={data.reason ?? "This site's access log could not be read."}
        />
      ) : (
        <Requests
          site={site}
          data={data}
          query={query}
          narrowing={narrowing}
          custom={shownRange === "custom"}
          onNarrow={setNarrowing}
          onZoom={(since, until) => {
            setNarrowing({ ...narrowing, since, until })
            setRange("custom")
          }}
          onReset={() => {
            setNarrowing({ ...narrowing, since: undefined, until: undefined })
            setRange("1h")
          }}
        />
      )}
    </Page>
  )
}

function SiteReadings({ data, loading }: { data: SiteTraffic | undefined; loading: boolean }) {
  const s = data?.status === "available" ? data.summary : undefined
  const figure = (value: React.ReactNode) =>
    loading && !data ? <Skeleton className="my-1.5 h-5 w-12" /> : (value ?? "—")
  return (
    <StatGrid columns={4} dense>
      <StatTile
        label="Requests"
        value={figure(s && s.total.toLocaleString())}
        hint={s && `${perMinute(s.perMinute)}/min · ${s.pages.toLocaleString()} pages`}
      />
      <StatTile
        label="5xx rate"
        value={figure(s && share(s.errorRate))}
        hint={s && `${(s.classes["5xx"] ?? 0).toLocaleString()} failed`}
        tone={s ? errorRateTone(s.errorRate) : "default"}
      />
      <StatTile
        label="p95"
        value={figure(s && (s.latency ? latency(s.latency.p95) : undefined))}
        hint={
          s && (s.latency ? `p50 ${latency(s.latency.p50)}` : "nginx's log format has no timing")
        }
      />
      <StatTile
        label="Bandwidth"
        value={figure(s && bytes(s.bytes))}
        hint={s && !data?.complete ? "a floor: only the log's tail is held" : undefined}
      />
    </StatGrid>
  )
}

function FleetReadings({
  sites,
  loading,
}: {
  sites: SiteTrafficSummary["sites"] | undefined
  loading: boolean
}) {
  const logging = sites?.filter((s) => s.status === "available") ?? []
  const total = logging.reduce((n, s) => n + s.requests, 0)
  const failed = logging.reduce((n, s) => n + s.requests * s.errorRate, 0)
  const sent = logging.reduce((n, s) => n + s.bytes, 0)
  const rate = total > 0 ? failed / total : 0
  const figure = (value: React.ReactNode) =>
    loading && !sites ? <Skeleton className="my-1.5 h-5 w-12" /> : sites ? value : "—"
  return (
    <StatGrid columns={4} dense>
      <StatTile label="Requests" value={figure(compactCount(total))} hint="last hour" />
      <StatTile
        label="5xx rate"
        value={figure(share(rate))}
        tone={sites ? errorRateTone(rate) : "default"}
      />
      <StatTile
        label="Sites logging"
        value={figure(logging.length)}
        hint={sites && `of ${sites.length}`}
      />
      <StatTile label="Bandwidth" value={figure(bytes(sent))} hint="last hour" />
    </StatGrid>
  )
}

function BusiestSites({
  sites,
  onOpen,
}: {
  sites: SiteTrafficSummary["sites"]
  onOpen: (site: string) => void
}) {
  return (
    <Panel plain>
      <PanelHeader title="Busiest sites" />
      <PanelBody flush>
        <BarList
          className="animate-rise"
          items={busiestItems(sites, onOpen)}
          emptyLabel="No nginx sites on this host."
        />
      </PanelBody>
    </Panel>
  )
}

/**
 * The Requests tab: the chart over the window, what it was made of, and the
 * rows — followed live on request, by polling the tail from the window's
 * cursor so nothing between the two reads is missed or shown twice.
 */
function Requests({
  site,
  data,
  query,
  narrowing,
  custom,
  onNarrow,
  onZoom,
  onReset,
}: {
  site: string
  data: SiteTraffic
  query: ReturnType<typeof trafficQuery>
  narrowing: Narrowing
  custom: boolean
  onNarrow: (n: Narrowing) => void
  onZoom: (since: string, until: string) => void
  onReset: () => void
}) {
  const [live, setLive] = useState(false)
  const [tail, setTail] = useState<RequestEntry[]>([])
  const cursor = useRef<number | undefined>(undefined)
  usePoll(
    async (signal) => {
      const res = await get<SiteTrafficTail>(
        `/proxy/traffic/${encodeURIComponent(site)}/tail`,
        { ...query, after: cursor.current ?? data.coverage.cursor },
        signal,
      )
      cursor.current = res.cursor
      if (res.entries.length > 0) {
        setTail((held) => [...[...res.entries].reverse(), ...held].slice(0, 2000))
      }
      return res.cursor
    },
    2000,
    [site, JSON.stringify(query)],
    { enabled: live },
  )
  const toggleLive = () => {
    cursor.current = undefined
    setTail([])
    setLive(!live)
  }
  const rows = useMemo(() => {
    if (!live || tail.length === 0) return data.entries
    const fresh = tail.filter((e) => !e.seq || e.seq > data.coverage.cursor)
    return [...fresh, ...data.entries]
  }, [live, tail, data])

  const chips = (
    [
      ["path", narrowing.path && `Path ${narrowing.path}`],
      ["client", narrowing.client && `Client ${narrowing.client}`],
      ["host", narrowing.host && `Host ${narrowing.host}`],
      ["status", narrowing.status && `Status ${narrowing.status}`],
      ["minMs", narrowing.minMs !== undefined && `Slower than ${latency(narrowing.minMs)}`],
    ] as const
  ).filter(([, label]) => label)

  return (
    <div className="space-y-6">
      {data.summary.buckets.length > 0 && (
        <RequestChart
          buckets={data.summary.buckets}
          bucketSeconds={data.summary.bucketSeconds}
          latencyKnown={data.latency}
          total={data.summary.total}
          failed={data.summary.classes["5xx"] ?? 0}
          custom={custom}
          onReset={onReset}
          onZoom={(from, to) => onZoom(from.toISOString(), to.toISOString())}
        />
      )}

      {chips.length > 0 && (
        <div className="flex flex-wrap items-center gap-2">
          {chips.map(([key, label]) => (
            <Button
              key={key}
              size="xs"
              variant="outline"
              onClick={() =>
                onNarrow({
                  ...narrowing,
                  [key]: key === "status" || key === "minMs" ? undefined : "",
                })
              }
              aria-label={`Clear ${String(label)}`}
            >
              {label}
              <Cross className="size-3" />
            </Button>
          ))}
        </div>
      )}

      <TrafficFacets
        summary={data.summary}
        slowest={data.slowest}
        latencyKnown={data.latency}
        onFilterPath={(path) => onNarrow({ ...narrowing, path })}
        onFilterClient={(client) => onNarrow({ ...narrowing, client })}
        onFilterHost={(host) => onNarrow({ ...narrowing, host })}
        onFilterStatus={(status) => onNarrow({ ...narrowing, status })}
        onFilterSlow={(ms) => onNarrow({ ...narrowing, minMs: Math.floor(ms) })}
      />

      {/* A working region with its own scroll: the console follows the newest
          row, and the page around it must not scroll with it. */}
      <Pane className="h-[32rem]">
        <RequestConsole
          entries={rows}
          summary={data.summary}
          latencyKnown={data.latency}
          onFilterPath={(path) => onNarrow({ ...narrowing, path })}
          onFilterClient={(client) => onNarrow({ ...narrowing, client })}
          leading={
            <FilterChip
              selected={live}
              onClick={toggleLive}
              title="Add new requests as they arrive, checked every two seconds"
            >
              Live
            </FilterChip>
          }
          status={
            <span className="numeric whitespace-nowrap">
              {live ? "Following every 2s" : `${data.summary.pages.toLocaleString()} page views`}
            </span>
          }
          footer={
            <span className="truncate font-mono" title={data.logs.access}>
              {data.logs.access}
            </span>
          }
          empty={
            <EmptyState
              title="No requests in this window"
              description="Widen the window or clear a filter."
            />
          }
        />
      </Pane>
    </div>
  )
}
