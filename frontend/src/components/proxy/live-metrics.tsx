"use client"

import { useRef, useState } from "react"
import { Warning } from "@/components/icons"
import { errorMessage, get, put } from "@/lib/api"
import { notify } from "@/lib/toast"
import type { ProxyMetrics } from "@/lib/proxy/types-metrics"
import { usePoll } from "@/hooks/use-poll"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { TileTrend } from "@/components/metrics/sparkline"
import { Status } from "@/components/status-dot"
import { ErrorState, Notice } from "@/components/state"
import { Skeleton } from "@/components/ui/skeleton"
import { Switch } from "@/components/ui/switch"
import {
  connectionStates,
  connectionTrend,
  mergeSeries,
  metricsCursor,
  perSecond,
  requestTrend,
  statusAddress,
  readingsStopped,
  windowLabel,
  type MetricsSeries,
} from "@/components/proxy/live-metrics-series"

/**
 * nginx's requests and connections, as its stub_status counts them, with the
 * last hour of each.
 *
 * The server reads the counters every five seconds from a status server it
 * writes into conf.d on loopback only, and only while an administrator has
 * the switch on: the file is the switch. The page asks for the readings it
 * lacks on the same cadence, and not while the tab is hidden. It is a poll,
 * so nothing here is drawn as live (§11).
 */
export function LiveTraffic({ admin }: { admin: boolean }) {
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
      notify.error(on ? "Live metrics not switched on" : "Live metrics not switched off", err)
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
    <Panel plain aria-label="Live traffic">
      <PanelHeader
        title="Live traffic"
        actions={
          <span className="flex items-center gap-3">
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
        }
      />
      <PanelBody>
        {metrics.loading ? (
          <div className="grid gap-4 sm:grid-cols-2">
            <Skeleton className="h-24" />
            <Skeleton className="h-24" />
          </div>
        ) : !report ? (
          metrics.error && <ErrorState error={metrics.error} onRetry={metrics.refresh} />
        ) : report.foreign ? (
          <Notice tone="warning" icon={Warning} title="The status file is not the dashboard's">
            <span className="font-mono">{report.path}</span> was written by somebody else, so it is
            left alone. Move it aside to switch live metrics on from here.
          </Notice>
        ) : !report.enabled ? (
          <p className="animate-rise text-body text-muted-foreground">
            {!report.supported
              ? report.reason
              : admin
                ? `Off. Switching on adds a loopback-only status server to conf.d, tests the configuration and reloads nginx; its requests and connections are then read here every ${report.interval} seconds.`
                : "Off. An administrator can switch live metrics on here."}
          </p>
        ) : (
          <Readings report={report} series={metrics.data?.series} />
        )}
      </PanelBody>
    </Panel>
  )
}

/**
 * The two readings stub_status has: requests, as a rate from its counter,
 * and the connections open now, each with its hour. Readings that stopped
 * keep their hour and lose their figure — the last one is not the traffic now.
 */
function Readings({ report, series }: { report: ProxyMetrics; series?: MetricsSeries }) {
  const samples = series?.samples ?? []
  const current = report.error ? undefined : report.current
  const rate = current?.requests
  const stopped = readingsStopped(report)
  return (
    <div className="animate-rise space-y-3">
      <StatGrid columns={2}>
        <StatTile
          label="Requests"
          value={rate != null ? perSecond(rate) : "—"}
          trailing={rate != null ? "a second" : undefined}
          trend={
            <TileTrend
              values={requestTrend(samples)}
              label="Requests a second over the last hour"
            />
          }
          hint={
            current && rate == null
              ? "The rate needs a second reading"
              : samples.length > 0
                ? `${report.hourRequests.toLocaleString()} ${windowLabel(samples)}`
                : undefined
          }
        />
        <StatTile
          label="Connections"
          value={current ? current.active.toLocaleString() : "—"}
          trailing={current ? "open" : undefined}
          tone={report.hourDropped > 0 ? "warning" : "default"}
          trend={
            <TileTrend
              values={connectionTrend(samples)}
              color="var(--chart-2)"
              label="Open connections over the last hour"
            />
          }
          hint={
            report.hourDropped > 0
              ? `${report.hourDropped.toLocaleString()} turned away ${windowLabel(samples)}: worker_connections is full`
              : current && connectionStates(current)
          }
        />
      </StatGrid>
      <p className={stopped ? "text-hint text-warning" : "text-hint text-muted-foreground"}>
        {stopped ??
          `nginx's stub_status on ${statusAddress(report)}, read every ${report.interval} seconds; the dashboard's own reads are left out.`}
      </p>
    </div>
  )
}
