"use client"

import { useCallback, useEffect, useMemo, useState } from "react"
import Link from "next/link"
import { useRouter, useSearchParams } from "next/navigation"
import { Tabs as TabsPrimitive } from "radix-ui"
import {
  ArrowRight,
  ArrowUp,
  Bell,
  Box,
  ChartActivity,
  CheckCircle,
  Globe,
  Logs,
  RefreshClockwise,
  Stopwatch,
  Warning,
} from "@/components/icons"
import { get } from "@/lib/api"
import { bytes, plural, relativeTime } from "@/lib/format"
import { LANES } from "@/lib/hue"
import { latencyTerm, latencyWords } from "@/lib/latency-words"
import { latency, latencyTone, perMinute, REQUEST_RANGES } from "@/lib/requests"
import type { SiteTrafficReading, SiteTrafficSummary, VHost } from "@/lib/types"
import { cn } from "@/lib/utils"
import { useSessionState } from "@/lib/view-state"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { Page, PageContext } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { FactDot, HostFact, HostIdentity } from "@/components/metrics/host-identity"
import { ChartPanel } from "@/components/metrics/chart-panel"
import type { ChartRowLike, Series } from "@/components/metrics/metric-chart"
import { TileTrend } from "@/components/metrics/sparkline"
import { HUE } from "@/components/overview/readings"
import { NoReading, ReadingLine, ReadingLines } from "@/components/reading-line"
import { RankList, RankSkeleton, SplitBar } from "@/components/rank-list"
import { ErrorState, LoadingPanel } from "@/components/state"
import { Status, type DotTone } from "@/components/status-dot"
import { ChipCount, tabClasses } from "@/components/tabs"
import { Tabs, TabsContent } from "@/components/ui/tabs"
import { Button } from "@/components/ui/button"
import { TextShimmer } from "@/components/ui/text-shimmer"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { NumberTicker } from "@/components/ui/number-ticker"
import { ProductGlyph, hasProductLogo } from "@/components/product-logo"
import { VerbMenu, type Verb } from "@/components/verbs"
import { WireMark } from "@/components/deploy/wire"
import { ProjectFavicon } from "@/components/deploy/project-mark"
import { Segments } from "@/components/deploy/settings/segments"
import { EngineSetup } from "@/components/proxy/engine-setup"
import { EmptyMark, OverviewEmpty } from "@/components/proxy/overview-empty"
import { engineProduct } from "@/components/proxy/marks"
import { useProxy, useProxyRead } from "@/components/proxy/proxy-context"
import { siteBackend } from "@/components/proxy/route-map"
import { ErrorLog, actionableCount, useErrorReport } from "@/components/proxy/site-errors"
import {
  busiestSites,
  edgeHour,
  errorRateTone,
  failingWords,
  legacySiteTarget,
  trafficTab,
  type EdgeHour,
  type TrafficTab,
} from "@/components/proxy/site-traffic"

/** The windows the error log is read over: the request record's own presets. */
type ErrorWindow = (typeof REQUEST_RANGES)[number]["id"]
const ERROR_WINDOWS = REQUEST_RANGES.map((r) => ({ value: r.id, label: windowWord(r.id) }))

function windowWord(id: ErrorWindow) {
  return id === "15m" ? "15 min" : id
}

/**
 * The deployment Logs page's request series, for the edge's hour: volume as an
 * area, the two families that mean trouble as lines over it. Module constants,
 * because `ChartPanel` is memoised on its props.
 */
const SERIES: Series[] = [
  { key: "total", label: "Requests", color: "var(--chart-1)", kind: "area" },
  { key: "refused", label: "4xx client error", color: "var(--warning)", kind: "line" },
  { key: "failed", label: "5xx server error", color: "var(--destructive)", kind: "line" },
]
const COUNT = (value: number) => Math.round(value).toLocaleString()
const CHART_HEIGHT = 132

/**
 * The last hour at the edge, every site at once, read like a project's Logs
 * page reads one deployment: what the page is about on one line, then the
 * hour as readings in words (requests a minute, the share that failed, how
 * long the slowest site took, what was sent), the hour drawn minute by
 * minute, and two views in the address — the sites that carried it, busiest
 * first, and what nginx wrote in its error log.
 *
 * It opened on four bare tiles and a window select that moved only the error
 * log. One site's requests were a second copy of what that site's own page
 * draws in the deployment's workspace; `?site=` lands there now, and a site's
 * error log, which only this page groups and explains, is the Errors view
 * narrowed to it.
 */
export function SiteTrafficView() {
  const params = useSearchParams()
  const router = useRouter()
  const legacy = legacySiteTarget(params)
  useEffect(() => {
    if (legacy) router.replace(legacy)
  }, [legacy, router])
  if (legacy) {
    return (
      <Page>
        <PageContext title="Traffic" />
      </Page>
    )
  }
  return <TrafficPage />
}

function TrafficPage() {
  const params = useSearchParams()
  const router = useRouter()
  const { can } = useAuth()
  const admin = can("system.admin")
  const { status, refresh: refreshStatus } = useProxy()
  const vhosts = useProxyRead("vhosts")
  const ports = useProxyRead("ports")

  const [tab, setTabState] = useState<TrafficTab>(() => trafficTab(params))
  const [site, setSiteState] = useState(() => params.get("site") ?? "")
  // The view and the site it is narrowed to are in the address, so a link to
  // a site's error log opens on it; replaced, not pushed: a view is not a
  // place Back should step through.
  const write = useCallback((next: { tab: TrafficTab; site: string }) => {
    const url = new URL(window.location.href)
    url.searchParams.delete("view")
    if (next.tab === "errors") url.searchParams.set("tab", "errors")
    else url.searchParams.delete("tab")
    if (next.site) url.searchParams.set("site", next.site)
    else url.searchParams.delete("site")
    window.history.replaceState(null, "", `${url.pathname}${url.search}`)
  }, [])
  const setTab = (next: string) => {
    const chosen: TrafficTab = next === "errors" ? "errors" : "sites"
    setTabState(chosen)
    write({ tab: chosen, site })
  }
  const setSite = (next: string) => {
    setSiteState(next)
    write({ tab, site: next })
  }

  const [stored, setRange] = useSessionState<string>("proxy.traffic.range", "1h")
  const range: ErrorWindow = ERROR_WINDOWS.some((w) => w.value === stored)
    ? (stored as ErrorWindow)
    : "1h"

  const summary = usePoll(
    (signal) => get<SiteTrafficSummary>("/proxy/traffic", undefined, signal),
    60_000,
  )
  // nginx's error log is nginx's: a host whose only engine is the Docker
  // Caddy ingress has none to read.
  const nginx = status?.nginx ?? false
  const errors = useErrorReport(site || undefined, range, nginx)
  const refresh = () => {
    summary.refresh()
    errors.refresh()
  }

  const sites = summary.error ? undefined : summary.data?.sites
  const hour = useMemo(() => (sites ? edgeHour(sites) : undefined), [sites])
  const engineMissing = status && !status.nginx && !status.caddy && !status.ingressContainer

  const verbs: Verb[] = [
    { key: "refresh", label: "Refresh", icon: RefreshClockwise, run: refresh },
    { key: "sites", label: "Open Sites", icon: Globe, run: () => router.push("/proxy/sites") },
    ...(admin && nginx
      ? [
          {
            key: "alerts",
            label: "Alert rules",
            icon: Bell,
            run: () => router.push("/proxy/alerts"),
          },
        ]
      : []),
  ]

  if (engineMissing) {
    return (
      <Page className="animate-rise">
        <PageContext title="Traffic" />
        <HostIdentity
          fallback={ChartActivity}
          title="Traffic"
          state={<Status tone="stopped" label="Not installed" />}
          facts={<span>No reverse proxy on this host</span>}
        />
        <EngineSetup status={status} admin={admin} onInstalled={refreshStatus} />
      </Page>
    )
  }

  const empty = sites && (sites.length === 0 || hour?.logging === 0)
  const errorCount = actionableCount(errors.error ? undefined : errors.data)
  const nginxSites = (vhosts.data ?? []).filter((v) => v.kind === "nginx")

  return (
    <Page className="animate-rise">
      <PageContext title="Traffic" />
      <HostIdentity
        mark={engineProduct(status)}
        fallback={ChartActivity}
        title="Traffic"
        facts={
          <IdentityFacts
            sites={sites}
            hour={hour}
            error={summary.error}
            observedAt={summary.data?.observedAt}
          />
        }
        // Laid out as a project's header: how the hour is answering beside
        // the name, the page's verbs behind `⋯`. Always a state, so the line
        // is one height on a phone before the hour is read and after.
        state={
          summary.error ? (
            <Status tone="danger" label="Not read" />
          ) : !hour || !sites ? (
            <Status tone="unknown" label="Reading" />
          ) : empty ? (
            <Status tone="stopped" label="Nothing to count" />
          ) : (
            <HourVerdict hour={hour} />
          )
        }
        menu={<VerbMenu verbs={verbs} label="More actions for traffic" />}
      />

      {summary.error ? (
        <ErrorState error={summary.error} onRetry={summary.refresh} />
      ) : empty ? (
        <NothingToCount sites={sites} admin={admin} />
      ) : (
        <div data-slot="traffic-hour" className="space-y-6">
          <HourReadings hour={hour} />
          <HourChart hour={hour} />
        </div>
      )}

      {/* The strip is drawn while the status is read, so it does not arrive
          under the reader; a host with no nginx has no error log to tab to. */}
      {sites?.length === 0 && nginx ? (
        // No site has nothing to rank: nginx's own log is all there is.
        <Panel plain>
          <PanelHeader title="Error log" />
          <PanelBody>
            <ErrorsView
              poll={errors}
              site={site}
              onSite={setSite}
              sites={nginxSites}
              range={range}
              onRange={setRange}
            />
          </PanelBody>
        </Panel>
      ) : !status || nginx ? (
        <Tabs value={tab} onValueChange={setTab} className="gap-0">
          <TabsPrimitive.List
            aria-label="Traffic views"
            className="flex shrink-0 [scrollbar-width:none] gap-1 overflow-x-auto border-b border-hairline [&::-webkit-scrollbar]:hidden"
          >
            <TabsPrimitive.Trigger value="sites" className={tabClasses(tab === "sites", "h-10")}>
              Sites
              {sites && sites.length > 0 && <ChipCount>{sites.length}</ChipCount>}
            </TabsPrimitive.Trigger>
            <TabsPrimitive.Trigger value="errors" className={tabClasses(tab === "errors", "h-10")}>
              Errors
              {errorCount !== undefined && errorCount > 0 && (
                <ChipCount>{errorCount.toLocaleString()}</ChipCount>
              )}
            </TabsPrimitive.Trigger>
          </TabsPrimitive.List>
          <TabsContent value="sites" className="pt-4">
            <SitesView
              sites={sites}
              error={summary.error}
              vhosts={vhosts.data}
              listeners={ports.data}
            />
          </TabsContent>
          <TabsContent value="errors" className="pt-4">
            <ErrorsView
              poll={errors}
              site={site}
              onSite={setSite}
              sites={nginxSites}
              range={range}
              onRange={setRange}
            />
          </TabsContent>
        </Tabs>
      ) : (
        <SitesView
          sites={sites}
          error={summary.error}
          vhosts={vhosts.data}
          listeners={ports.data}
        />
      )}
    </Page>
  )
}

/** What the page covers, on the identity line: the hour, the sites, and how old the read is. */
function IdentityFacts({
  sites,
  hour,
  error,
  observedAt,
}: {
  sites: SiteTrafficReading[] | undefined
  hour: EdgeHour | undefined
  error: Error | undefined
  observedAt: string | undefined
}) {
  if (error) return <span className="text-warning">{"couldn't read the sites' access logs"}</span>
  if (!sites || !hour) return <TextShimmer>{"Reading the sites' access logs…"}</TextShimmer>
  const unlogged = hour.unlogged
  return (
    <>
      <span>last hour</span>
      <FactDot />
      <HostFact>
        <Link href="/proxy/sites" className="rounded-sm hover:text-foreground hover:underline">
          {plural(hour.sites, "site")}
        </Link>
      </HostFact>
      {unlogged.length > 0 && hour.sites > 0 && (
        <>
          <FactDot />
          <span
            className="text-warning"
            title={unlogged.map((s) => `${s.site}: ${s.reason ?? "no log"}`).join("\n")}
          >
            {unlogged.length === hour.sites
              ? "none keeps an access log"
              : `${unlogged.length} with no access log`}
          </span>
        </>
      )}
      {!hour.complete && (
        <>
          <FactDot />
          <span title="A log is longer than the part the dashboard holds, so its counts are floors">
            counts are floors
          </span>
        </>
      )}
      {observedAt && (
        <>
          <FactDot />
          <span>read {relativeTime(observedAt)}</span>
        </>
      )}
    </>
  )
}

/** Whether the hour went well, in the colour the section gives a failing share. */
function HourVerdict({ hour }: { hour: EdgeHour }) {
  const tone = errorRateTone(hour.rate)
  const dot: DotTone = tone === "danger" ? "danger" : tone === "warning" ? "warning" : "running"
  const label =
    hour.total === 0
      ? "No requests this hour"
      : tone === "default"
        ? "Answering"
        : `${(hour.rate * 100).toFixed(1)}% failing`
  return <Status tone={hour.total === 0 ? "stopped" : dot} label={label} className="text-xs" />
}

/**
 * The hour in four readings, said as a project's Logs page says them: a rate,
 * the share that failed, how long the slowest site took in words, and what
 * was sent. Each line keeps its height before the read lands.
 */
function HourReadings({ hour }: { hour: EdgeHour | undefined }) {
  const waiting = <NoReading>No reading yet</NoReading>
  const totals = hour?.points.map((p) => p.total) ?? []
  const tone = hour ? errorRateTone(hour.rate) : "default"
  const slow = hour?.slowest
  return (
    <ReadingLines>
      <ReadingLine
        mark={
          <WireMark size="md" hue={HUE.net}>
            <Globe aria-hidden />
          </WireMark>
        }
        name="Requests"
        hint={
          hour
            ? `${hour.total.toLocaleString()} in the last hour across ${plural(hour.logging, "site")}`
            : "Reading the access logs…"
        }
        trend={
          hour && <TileTrend values={totals} label="Requests a minute, last hour" color={HUE.net} />
        }
      >
        {hour ? (
          <>
            <NumberTicker
              value={Number(perMinute(hour.perMinute))}
              decimalPlaces={hour.perMinute < 10 ? 2 : hour.perMinute < 100 ? 1 : 0}
            />{" "}
            {hour.perMinute === 1 ? "request" : "requests"} a minute
          </>
        ) : (
          waiting
        )}
      </ReadingLine>
      <ReadingLine
        mark={
          hour ? (
            <WireMark
              size="md"
              tone={tone === "default" ? "success" : tone === "danger" ? "danger" : "warning"}
            >
              {tone === "default" ? <CheckCircle aria-hidden /> : <Warning aria-hidden />}
            </WireMark>
          ) : (
            <WireMark size="md">
              <CheckCircle aria-hidden className="text-muted-foreground" />
            </WireMark>
          )
        }
        name="Failing"
        hint={
          hour
            ? `${hour.failed.toLocaleString()} of ${hour.total.toLocaleString()} answered 5xx · ${hour.refused.toLocaleString()} refused with a 4xx`
            : undefined
        }
        trend={
          hour &&
          hour.failed > 0 && (
            <TileTrend
              values={hour.points.map((p) => p.failed)}
              label="Requests answered 5xx a minute, last hour"
              color="var(--destructive)"
            />
          )
        }
      >
        {hour ? (
          <span
            className={cn(
              tone === "danger" && "text-destructive",
              tone === "warning" && "text-warning",
            )}
          >
            {failingWords(hour.rate)}
          </span>
        ) : (
          waiting
        )}
      </ReadingLine>
      <ReadingLine
        mark={
          <WireMark
            size="md"
            {...(latencyTone(slow?.p95) === "warning"
              ? { tone: "warning" as const }
              : { hue: "var(--chart-3)" })}
          >
            <Stopwatch aria-hidden />
          </WireMark>
        }
        name="Response time"
        hint={
          !hour
            ? undefined
            : slow
              ? hour.timed > 1
                ? `the slowest of ${hour.timed} timed sites, ${slow.site}`
                : slow.site
              : "nginx's log format records no timing"
        }
      >
        {!hour ? (
          waiting
        ) : slow ? (
          <span title={latencyTerm("p95")}>
            {latencyWords("p95", latency(slow.p95)).replace(/^./, (c) => c.toUpperCase())}
          </span>
        ) : (
          <NoReading>No timing recorded</NoReading>
        )}
      </ReadingLine>
      <ReadingLine
        mark={
          <WireMark size="md" hue="var(--chart-2)">
            <ArrowUp aria-hidden />
          </WireMark>
        }
        name="Served"
        hint={
          !hour
            ? undefined
            : hour.complete
              ? "sent in the last hour"
              : "sent in the last hour, at least: a log's tail is all that is held"
        }
        trend={
          hour && (
            <TileTrend
              values={hour.points.map((p) => p.bytes)}
              label="Bytes sent a minute, last hour"
              color="var(--chart-2)"
            />
          )
        }
      >
        {hour ? `${bytes(hour.bytes)} sent` : waiting}
      </ReadingLine>
    </ReadingLines>
  )
}

/**
 * The hour minute by minute, every site added up: the deployment Logs page's
 * request chart over the edge, with its legend on one line. Drawn the same
 * height before the read lands, with the sentence where the plot will be.
 */
function HourChart({ hour }: { hour: EdgeHour | undefined }) {
  const rows = useMemo<ChartRowLike[]>(
    () =>
      (hour?.points ?? []).map((p) => ({
        ts: Date.parse(p.start),
        total: p.total,
        refused: p.refused,
        failed: p.failed,
      })),
    [hour],
  )
  return (
    <ChartPanel
      plain
      title="Requests"
      actions={
        // Held while the hour is read, so the title line keeps its height.
        <span className="numeric truncate text-hint text-muted-foreground">
          {hour ? (
            <>
              {hour.total.toLocaleString()}
              <span className="text-muted-foreground/50"> · </span>
              <span className={cn(hour.failed > 0 && "text-destructive")}>
                {hour.failed.toLocaleString()} failed
              </span>
            </>
          ) : (
            "\u00a0"
          )}
        </span>
      }
      rows={rows}
      series={SERIES}
      height={CHART_HEIGHT}
      format={COUNT}
      showPeaks={false}
      legend={false}
      note={hour ? "No requests in the last hour." : "Reading the sites' access logs…"}
      footer={
        <span className="flex flex-wrap items-center gap-x-3 gap-y-1 text-hint text-muted-foreground">
          {SERIES.map((s) => (
            <span key={s.key} className="flex items-center gap-1.5">
              <span aria-hidden className="size-1.5 rounded-full" style={{ background: s.color }} />
              {s.label}
            </span>
          ))}
          <span className="ml-auto">One point is 1 min, every site added up.</span>
        </span>
      }
    />
  )
}

/**
 * A host with nothing to count: one sentence and the press that changes it.
 * No site at all is a site to add; sites that keep no access log the
 * dashboard reads are a log to turn on, in the one site's form when there is
 * one, on the Sites list when there are several.
 */
function NothingToCount({
  sites,
  admin,
}: {
  sites: SiteTrafficReading[] | undefined
  admin: boolean
}) {
  if (!sites || sites.length === 0) {
    return (
      <OverviewEmpty
        mark={<EmptyMark product="nginx-static" fallback={Globe} corner={Box} />}
        title="No sites to count"
        action={
          <Button asChild size="xs" variant="outline">
            <Link href={admin ? "/proxy/sites?new=1" : "/proxy/sites"}>
              {admin ? "New site" : "Open Sites"}
              <ArrowRight />
            </Link>
          </Button>
        }
      >
        Traffic is counted per site, and this host serves none yet.
      </OverviewEmpty>
    )
  }
  const one = sites.length === 1 ? sites[0] : undefined
  return (
    <OverviewEmpty
      tone="warning"
      mark={<EmptyMark product="nginx-static" fallback={Globe} corner={Logs} tone="warning" />}
      title="No access log to read"
      action={
        <Button asChild size="xs" variant="outline">
          <Link href={one ? `/proxy/sites/${encodeURIComponent(one.site)}` : "/proxy/sites"}>
            {one ? (admin ? "Turn its access log on" : `Open ${one.site}`) : "Open Sites"}
            <ArrowRight />
          </Link>
        </Button>
      }
    >
      <span title={sites.map((s) => `${s.site}: ${s.reason ?? "no log"}`).join("\n")}>
        {one
          ? (one.reason ?? `${one.site} keeps no access log the dashboard can read.`)
          : `None of the ${sites.length} sites keeps an access log the dashboard can read.`}
      </span>
    </OverviewEmpty>
  )
}

/** A site's mark: the project it is, else the product it reaches, else its engine. */
function SiteGlyph({
  reading,
  backend,
}: {
  reading: SiteTrafficReading
  backend?: { product?: string; projectId?: number }
}) {
  const engine = reading.engine === "caddy-ingress" ? "caddy" : "nginx-static"
  return (
    <ProjectFavicon
      projectId={backend?.projectId}
      className="size-3.5"
      fallback={<ProductGlyph id={hasProductLogo(backend?.product) ? backend.product : engine} />}
    />
  )
}

/**
 * Who carried the hour: one strip shared out by site, each of the busiest a
 * hue of its own, and under it every site busiest first with its figure and
 * its share of the whole; a site whose log cannot be read last, saying so. A
 * row opens the site's page, where its requests are read.
 */
function SitesView({
  sites,
  error,
  vhosts,
  listeners,
}: {
  sites: SiteTrafficReading[] | undefined
  error: Error | undefined
  vhosts: VHost[] | undefined
  listeners: Parameters<typeof siteBackend>[1] | undefined
}) {
  const router = useRouter()
  const byName = useMemo(() => new Map((vhosts ?? []).map((v) => [v.name, v])), [vhosts])
  if (error) return null
  if (!sites) {
    return (
      <div className="space-y-3" data-slot="traffic-sites">
        <SplitBar parts={[]} label="Requests this hour by site" />
        <RankSkeleton rows={4} />
      </div>
    )
  }
  if (sites.length === 0) return null
  const rows = busiestSites(sites)
  const readings = new Map(sites.map((s) => [s.site, s]))
  const lanes = rows.filter((r) => r.share !== undefined && r.share > 0).slice(0, LANES.length)
  const lane = new Map(lanes.map((r, i) => [r.key, LANES[i]]))
  const laned = lanes.reduce((n, r) => n + (readings.get(r.key)?.requests ?? 0), 0)
  const whole = sites.reduce((n, s) => n + (s.status === "available" ? s.requests : 0), 0)
  return (
    <div className="space-y-3" data-slot="traffic-sites">
      {/* Nothing logged is nothing to share out: the card above says why. */}
      {whole > 0 && (
        <SplitBar
          label="Requests this hour by site"
          parts={[
            ...lanes.map((r) => ({
              key: r.key,
              count: readings.get(r.key)?.requests ?? 0,
              color: lane.get(r.key)!,
            })),
            { key: "rest", count: whole - laned, color: "var(--muted-foreground)" },
          ]}
        />
      )}
      <RankList
        items={rows.map((row) => {
          const reading = readings.get(row.key)!
          const vhost = byName.get(row.key)
          const backend = vhost ? siteBackend(vhost, listeners ?? []) : undefined
          const label = vhost?.serverNames?.[0] ?? row.label
          return {
            key: row.key,
            label,
            mono: false,
            swatch: lane.get(row.key) ?? "var(--meter-track)",
            mark: <SiteGlyph reading={reading} backend={backend} />,
            value: row.value,
            share: row.share,
            hint: row.hint && (
              <span
                className={cn(
                  row.tone === "danger" && "text-destructive",
                  row.tone === "warning" && "text-warning",
                )}
              >
                {row.hint}
              </span>
            ),
            title:
              reading.status === "available"
                ? `Open ${label}'s requests`
                : `${label}: ${reading.reason ?? "no access log"}`,
            onClick: () => router.push(row.href),
          }
        })}
      />
    </div>
  )
}

/**
 * nginx's error log over a window, grouped and explained, for every site or
 * narrowed to one: a site's own log, or nginx's narrowed to its names.
 */
function ErrorsView({
  poll,
  site,
  onSite,
  sites,
  range,
  onRange,
}: {
  poll: ReturnType<typeof useErrorReport>
  site: string
  onSite: (site: string) => void
  sites: VHost[]
  range: ErrorWindow
  onRange: (range: ErrorWindow) => void
}) {
  const report = poll.error ? undefined : poll.data
  // A site the address names is offered even before the list of sites lands,
  // so the select never shows a blank for the one it is narrowed to.
  const names = [...new Set([...(site ? [site] : []), ...sites.map((v) => v.name)])].sort()
  return (
    <div className="space-y-4" data-slot="traffic-errors">
      <div className="flex min-w-0 flex-wrap items-center justify-between gap-2">
        <Select value={site || "*"} onValueChange={(value) => onSite(value === "*" ? "" : value)}>
          <SelectTrigger size="sm" className="w-56 max-w-full" aria-label="Site">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="*">Every site</SelectItem>
            {names.map((name) => (
              <SelectItem key={name} value={name}>
                {name}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <Segments
          label="Error log window"
          value={range}
          options={ERROR_WINDOWS}
          onChange={onRange}
        />
      </div>
      {poll.error ? (
        <ErrorState error={poll.error} onRetry={poll.refresh} />
      ) : !report ? (
        <LoadingPanel plain rows={4} />
      ) : (
        <ErrorLog report={report} site={site || undefined} />
      )}
    </div>
  )
}
