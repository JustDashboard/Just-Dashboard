"use client"

import { useMemo, useRef, useState } from "react"
import { Warning } from "@/components/icons"
import { ApiError, errorMessage, get, put } from "@/lib/api"
import { bytes, plural } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { ProxyMetrics } from "@/lib/proxy/types-metrics"
import type { SiteTrafficReading } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { TileTrend } from "@/components/metrics/sparkline"
import { ChartPanel } from "@/components/metrics/chart-panel"
import type { ChartRowLike, Series } from "@/components/metrics/metric-chart"
import { Status } from "@/components/status-dot"
import { ErrorState, Notice } from "@/components/state"
import { Skeleton } from "@/components/ui/skeleton"
import { Switch } from "@/components/ui/switch"
import {
  connectionStates,
  mergeSeries,
  metricsCursor,
  perSecond,
  statusAddress,
  readingsStopped,
  type MetricsSeries,
} from "@/components/proxy/live-metrics-series"
import { compactCount, edgeTraffic, errorRateTone, share } from "@/components/proxy/site-traffic"

/**
 * Module constants: `ChartPanel` is memoised on its props, and a series array
 * rebuilt per render is a chart rebuilt per render.
 */
const EDGE_SERIES: Series[] = [
  { key: "total", label: "Requests", color: "var(--chart-1)", kind: "area" },
  { key: "refused", label: "4xx client error", color: "var(--warning)", kind: "line" },
  { key: "failed", label: "5xx server error", color: "var(--destructive)", kind: "line" },
]

const count = (value: number) => Math.round(value).toLocaleString()

/**
 * What came through the edge this last hour, minute by minute: every site's
 * access record added up — nginx's files and the Docker Caddy ingress's
 * routes alike — so the reading is the host's traffic whichever engine holds
 * ports 80 and 443. It used to be nginx's stub_status alone, which on a host
 * where the ingress answers counted nothing and drew two zeros forever; those
 * counters are still here, under the chart, for the nginx they describe.
 *
 * The records are polled, so nothing here is drawn as live (§11).
 */
export function LiveTraffic({
  admin,
  nginx,
  sites,
  error,
}: {
  admin: boolean
  /** nginx is on this host, so its own counters are offered under the chart. */
  nginx: boolean
  sites?: SiteTrafficReading[]
  error?: Error
}) {
  const read = useMemo(() => (sites ?? []).filter((s) => s.status === "available"), [sites])
  const points = useMemo(() => edgeTraffic(read), [read])
  const rows = useMemo<ChartRowLike[]>(
    () =>
      points.map((p) => ({
        ts: Date.parse(p.start),
        total: p.total,
        refused: p.refused,
        failed: p.failed,
      })),
    [points],
  )
  const total = read.reduce((sum, s) => sum + s.requests, 0)
  const failed = points.reduce((sum, p) => sum + p.failed, 0)
  const served = read.reduce((sum, s) => sum + s.bytes, 0)
  // The minute happening now is still filling; the one before it is whole.
  const lastWhole = points.length > 1 ? points[points.length - 2] : points[0]
  const failRate = total > 0 ? failed / total : 0

  return (
    <Panel plain aria-label="Live traffic">
      <PanelHeader
        title="Live traffic"
        actions={
          sites &&
          read.length > 0 && (
            <span className="numeric text-hint text-muted-foreground">
              {`Last hour across ${plural(read.length, "site")}, by the minute`}
            </span>
          )
        }
      />
      <PanelBody className="space-y-4 pt-2">
        {error ? (
          <p className="text-hint text-muted-foreground" title={errorMessage(error)}>
            {"Couldn't read the sites' access logs."}
          </p>
        ) : !sites ? (
          <div className="space-y-3">
            <Skeleton className="h-20 w-full" />
            <Skeleton className="h-40 w-full" />
          </div>
        ) : read.length === 0 ? (
          <p className="py-2 text-body text-muted-foreground">
            No site keeps an access log the dashboard can read, so there is no traffic to count.
          </p>
        ) : (
          <div className="animate-rise space-y-4">
            <StatGrid columns={4} framed>
              <StatTile
                label="Requests"
                value={lastWhole ? compactCount(lastWhole.total) : "—"}
                trailing="last minute"
                trend={
                  <TileTrend
                    values={points.map((p) => p.total)}
                    label="Requests a minute over the last hour"
                  />
                }
              />
              <StatTile
                label="This hour"
                value={compactCount(total)}
                trailing="requests"
                hint={`${(total / 60).toFixed(total >= 600 ? 0 : 1)} a minute on average`}
              />
              <StatTile
                label="Server errors"
                value={share(failRate)}
                trailing="5xx"
                tone={failed > 0 ? errorRateTone(failRate) : "default"}
                trend={
                  <TileTrend
                    values={points.map((p) => p.failed)}
                    color="var(--chart-3)"
                    label="5xx responses a minute over the last hour"
                  />
                }
                hint={failed > 0 ? `${failed.toLocaleString()} this hour` : "None this hour"}
              />
              <StatTile
                label="Served"
                value={bytes(served)}
                trailing="this hour"
                trend={
                  <TileTrend
                    values={points.map((p) => p.bytes)}
                    color="var(--chart-5)"
                    label="Bytes served a minute over the last hour"
                  />
                }
              />
            </StatGrid>
            <ChartPanel
              plain
              title="Requests a minute"
              rows={rows}
              series={EDGE_SERIES}
              height={180}
              format={count}
              showPeaks={false}
              note="No requests in the last hour."
            />
          </div>
        )}
        {nginx && <NginxCounters admin={admin} />}
      </PanelBody>
    </Panel>
  )
}

/**
 * nginx's own requests and connections, as its stub_status counts them — a
 * strip under the edge's chart rather than the panel's headline, because it
 * counts only what nginx answers.
 *
 * The server reads the counters every five seconds from a status server it
 * writes into conf.d on loopback only, and only while an administrator has
 * the switch on: the file is the switch. The page asks for the readings it
 * lacks on the same cadence, and not while the tab is hidden. It is a poll,
 * so nothing here is drawn as live (§11).
 */
function NginxCounters({ admin }: { admin: boolean }) {
  const held = useRef<MetricsSeries | undefined>(undefined)
  const [switching, setSwitching] = useState<"on" | "off">()
  // Not asked while a switch is in flight: the file goes in before nginx
  // reloads, and a reading taken between the two said "No answer" about a
  // server that was about to answer.
  const metrics = usePoll(
    async (signal) => {
      const report = await get<ProxyMetrics>("/proxy/metrics", metricsCursor(held.current), signal)
      held.current = mergeSeries(held.current, report)
      return { report, series: held.current }
    },
    5_000,
    [],
    { enabled: switching === undefined },
  )
  const report = metrics.data?.report

  const flip = async (on: boolean) => {
    setSwitching(on ? "on" : "off")
    try {
      const next = await put<ProxyMetrics>("/proxy/metrics", { enabled: on })
      if (on) {
        notify.success("Live metrics on", {
          description: `nginx answers on ${statusAddress(next)}, read every ${next.interval} seconds.`,
        })
      } else {
        notify.success("Live metrics off", {
          description: "The status server is out of conf.d and nginx reloaded without it.",
        })
      }
    } catch (err) {
      if (!on && err instanceof ApiError && err.code === "reload_failed") {
        // The file is out whatever nginx did: the switch is off, and the
        // message says whether the status server still answers until a reload.
        notify.warning("Live metrics off, nginx not reloaded", { description: err.message })
      } else {
        notify.error(on ? "Live metrics not switched on" : "Live metrics not switched off", err)
      }
    } finally {
      // Whatever happened, the next poll reads the switch as it now is.
      setSwitching(undefined)
    }
  }

  const state = switching ? (
    <Status state="activating" label={switching === "on" ? "Switching on…" : "Switching off…"} />
  ) : metrics.error && report ? (
    <span title={errorMessage(metrics.error)}>
      <Status verdict="warning" label="Not updating" />
    </span>
  ) : report?.enabled ? (
    report.error ? (
      <Status verdict="warning" label="No answer" />
    ) : (
      <Status verdict="ok" label="On" />
    )
  ) : report ? (
    <Status tone="stopped" label="Off" />
  ) : undefined

  return (
    <section
      aria-label="nginx counters"
      className="flex min-w-0 flex-col gap-2 rounded-lg border border-hairline px-4 py-3"
    >
      <div className="flex min-w-0 flex-wrap items-center gap-x-4 gap-y-2">
        <span className="eyebrow">nginx stub_status</span>
        <div className="min-w-0 flex-1 text-hint text-muted-foreground">
          {metrics.loading ? (
            <Skeleton className="h-4 w-48" />
          ) : !report ? (
            metrics.error && <ErrorState error={metrics.error} onRetry={metrics.refresh} />
          ) : report.foreign ? null : !report.enabled ? (
            !report.supported ? (
              report.reason
            ) : admin ? (
              <span
                title={`Switching on adds a loopback-only status server to conf.d, tests the configuration and reloads nginx; its counters are then read every ${report.interval} seconds.`}
              >
                Off — counts only what nginx itself answers.
              </span>
            ) : (
              "Off. An administrator can switch these counters on."
            )
          ) : (
            <Readings report={report} failure={metrics.error} />
          )}
        </div>
        <span className="flex shrink-0 items-center gap-3">
          {state}
          {admin && report && (
            <Switch
              aria-label="Live metrics"
              checked={switching ? switching === "on" : report.enabled}
              disabled={
                switching !== undefined ||
                Boolean(report.foreign) ||
                (!report.supported && !report.enabled)
              }
              onCheckedChange={flip}
            />
          )}
        </span>
      </div>
      {report?.foreign && (
        <Notice tone="warning" icon={Warning} title="The status file is not the dashboard's">
          <span className="font-mono">{report.path}</span> was written by somebody else, so it is
          left alone. Move it aside to switch live metrics on from here.
        </Notice>
      )}
    </section>
  )
}

/**
 * The two readings stub_status has — requests as a rate from its counter, and
 * the connections open now — on one line. Readings that stopped lose their
 * figure: the last one is not the traffic now.
 */
function Readings({
  report,
  failure,
}: {
  report: ProxyMetrics
  /** Why the last poll failed: the report is the one before it, not now. */
  failure?: Error
}) {
  const current = report.error || failure ? undefined : report.current
  const rate = current?.requests
  const stopped = failure ? `Not updating: ${failure.message}` : readingsStopped(report)
  if (stopped) return <span className="text-warning">{stopped}</span>
  return (
    <span
      className="numeric flex min-w-0 flex-wrap items-center gap-x-4 gap-y-1"
      title={`nginx's stub_status on ${statusAddress(report)}, read every ${report.interval} seconds; the dashboard's own reads are left out.`}
    >
      <span>
        <span className="font-medium text-foreground">{rate != null ? perSecond(rate) : "—"}</span>{" "}
        requests a second
      </span>
      <span>
        <span className="font-medium text-foreground">
          {current ? current.active.toLocaleString() : "—"}
        </span>{" "}
        connections open{current && ` (${connectionStates(current)})`}
      </span>
      {report.hourDropped > 0 && (
        <span className="text-warning">
          {report.hourDropped.toLocaleString()} turned away: worker_connections is full
        </span>
      )}
    </span>
  )
}
