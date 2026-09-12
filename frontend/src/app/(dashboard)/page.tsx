"use client"

import { useMemo } from "react"
import Link from "next/link"
import {
  ArrowRight,
  Box,
  ChartActivity,
  CloudUpload,
  Cpu,
  Database,
  Gauge,
  Globe,
  GridSquare,
  Puzzle,
} from "@/components/icons"
import { ApiError, get } from "@/lib/api"
import { bytes, clock, duration, percent, rate, relativeTime } from "@/lib/format"
import type { Certificate, Container, DbConnection, MetricEvent } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useMetrics } from "@/hooks/use-metrics"
import { useHealth, useMetricEvents, useMetricsHistory } from "@/hooks/use-metrics-history"
import { useSelfUpdate } from "@/hooks/use-self-update"
import type { MetricsWindow } from "@/lib/metrics-range"
import { Page, PageHeader, PageState, Metric, MetricStrip, Section } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { utilisationTone } from "@/components/meter"
import { HealthPanel, HealthVerdict } from "@/components/metrics/health-panel"
import { Sparkline } from "@/components/metrics/sparkline"
import { eventColor } from "@/components/metrics/metric-chart"

import { Tag } from "@/components/tag"
import { Skeleton } from "@/components/ui/skeleton"

// A fixed hour, not the metrics page's draggable window: the landing page is a
// glance, and there is exactly one range control in the product — on /metrics.
const HOUR: MetricsWindow = { key: "1h" }

export default function OverviewPage() {
  const { host, snapshot, error } = useMetrics()
  const recorded = useMetricsHistory(HOUR)
  const events = useMetricEvents(HOUR)
  const { health, loading: healthLoading } = useHealth()

  const trends = useMemo(() => {
    const points = recorded.history?.points ?? []
    return {
      cpu: points.map((p) => p.cpu),
      mem: points.map((p) => p.mem),
      net: points.map((p) => p.rx + p.tx),
      disk: points.map((p) => p.diskRead + p.diskWrite),
    }
  }, [recorded.history])

  // The hostname is the page's title once it is known and "Overview" until
  // then, so the heading does not change under the reader — see `PageState`.
  if (!snapshot || !host || (error && !snapshot)) {
    return (
      <PageState
        eyebrow="Server"
        title={host?.hostname ?? "Overview"}
        error={error && !snapshot ? new Error(error) : undefined}
        skeleton={
          <>
            <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4 [&>*]:min-w-0">
              {Array.from({ length: 4 }).map((_, i) => (
                <Skeleton key={i} className="h-30 rounded-xl" />
              ))}
            </div>
            <Skeleton className="h-48 rounded-xl" />
          </>
        }
      />
    )
  }

  const modes = snapshot.cpu.modes
  const throughput = {
    rx: snapshot.net.reduce((sum, n) => sum + n.recvRate, 0),
    tx: snapshot.net.reduce((sum, n) => sum + n.sendRate, 0),
  }
  const diskRate = snapshot.mounts.reduce((s, m) => s + m.readRate + m.writeRate, 0)
  const availPercent =
    snapshot.memory.total > 0 ? (snapshot.memory.available / snapshot.memory.total) * 100 : 0
  const cores = snapshot.cpu.cores || 1

  return (
    <Page>
      <PageHeader
        eyebrow="Server"
        title={host.hostname}
        actions={
          <MetricStrip>
            <Metric label="Uptime" value={duration(snapshot.uptimeSeconds)} />
            <Metric label="Processes" value={snapshot.procs?.total || host.processes} />
            <Metric label="Cores" value={cores} />
          </MetricStrip>
        }
      />

      {/* What this machine is. This was the page header's description, and it
          is the one place where that slot held data rather than a caption —
          the platform, the kernel and the architecture are the subject of the
          page, not an explanation of it. So it stays, as its own row. */}
      <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-body text-muted-foreground">
        <span>
          {host.platform} {host.platformVersion}
        </span>
        <Dot />
        <span>kernel {host.kernelVersion}</span>
        <Dot />
        <span>{host.kernelArch}</span>
        {host.virtualization && <Tag>{host.virtualization}</Tag>}
        {health && <HealthVerdict status={health.status} />}
      </div>

      <StatGrid columns={4}>
        <StatTile
          label="CPU"
          icon={Cpu}
          value={percent(snapshot.cpu.totalPercent)}
          meter={snapshot.cpu.totalPercent}
          tone={utilisationTone(snapshot.cpu.totalPercent)}
          hint={
            modes
              ? `${modes.user.toFixed(0)}% user · ${modes.system.toFixed(0)}% sys · ${modes.iowait.toFixed(0)}% wait`
              : `load ${snapshot.cpu.loadAvg1.toFixed(2)}`
          }
          trailing={
            modes && modes.steal >= 1 ? (
              <span className="numeric text-hint font-medium text-destructive">
                {percent(modes.steal, 0)} steal
              </span>
            ) : (
              <span className="text-hint text-muted-foreground">{cores} cores</span>
            )
          }
        />
        <StatTile
          label="Memory"
          icon={GridSquare}
          value={bytes(snapshot.memory.available)}
          meter={100 - availPercent}
          tone={availPercent <= 5 ? "danger" : availPercent <= 10 ? "warning" : "default"}
          hint={`${percent(snapshot.memory.usedPercent, 0)} used · ${bytes(snapshot.memory.cached)} cached`}
          trailing={<span className="text-hint text-muted-foreground">available</span>}
        />
        <StatTile
          label="Load"
          icon={Gauge}
          value={snapshot.cpu.loadAvg1.toFixed(2)}
          meter={(snapshot.cpu.loadAvg1 / cores) * 100}
          tone={utilisationTone((snapshot.cpu.loadAvg5 / cores) * 100)}
          hint={`${snapshot.cpu.loadAvg5.toFixed(2)} · ${snapshot.cpu.loadAvg15.toFixed(2)} over 5 and 15 min`}
          trailing={
            <span className="numeric text-hint text-muted-foreground">
              {(snapshot.cpu.loadAvg1 / cores).toFixed(2)}/core
            </span>
          }
        />
        <StatTile
          label="Network"
          icon={ChartActivity}
          value={rate(throughput.rx)}
          hint={`${rate(throughput.tx)} out · ${snapshot.sockets?.tcpInUse ?? 0} TCP sockets`}
          trailing={<span className="text-hint text-muted-foreground">in</span>}
        />
      </StatGrid>

      <HealthPanel health={health} loading={healthLoading} />

      <div className="grid items-start gap-4 lg:grid-cols-3 [&>*]:min-w-0">
        <TrendsPanel
          className="lg:col-span-2"
          disabled={recorded.disabled}
          items={[
            {
              label: "CPU",
              value: percent(snapshot.cpu.totalPercent, 0),
              data: trends.cpu,
              max: 100,
              color: "var(--chart-1)",
            },
            {
              label: "Memory",
              value: percent(snapshot.memory.usedPercent, 0),
              data: trends.mem,
              max: 100,
              color: "var(--chart-2)",
            },
            {
              label: "Network",
              value: rate(throughput.rx + throughput.tx),
              data: trends.net,
              color: "var(--chart-5)",
            },
            {
              label: "Disk I/O",
              value: rate(diskRate),
              data: trends.disk,
              color: "var(--chart-3)",
            },
          ]}
        />
        <ActivityPanel events={events} />
      </div>

      <Section title="Services">
        <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4 [&>*]:min-w-0">
          <DockerCard />
          <DatabasesCard />
          <ProxyCard />
          <UpdatesCard />
        </div>
      </Section>
    </Page>
  )
}

function Dot() {
  return <span className="text-muted-foreground/40">·</span>
}

type TrendItem = {
  label: string
  value: string
  data: number[]
  max?: number
  color: string
}

/**
 * An hour of shape for the four figures the stat tiles show as one instant.
 *
 * Sparklines, not charts: this answers "did anything happen while I was away",
 * and the answer to "what exactly" is one click into /metrics.
 */
function TrendsPanel({
  items,
  disabled,
  className,
}: {
  items: TrendItem[]
  disabled: boolean
  className?: string
}) {
  return (
    <Panel className={className}>
      <PanelHeader
        icon={ChartActivity}
        title="Last hour"
        actions={
          <Link
            href="/metrics"
            className="flex items-center gap-1 text-hint font-medium text-muted-foreground hover:text-foreground"
          >
            Metrics <ArrowRight className="size-3" />
          </Link>
        }
      />
      <PanelBody className="grid gap-x-6 gap-y-4 sm:grid-cols-2">
        {items.map((item) => (
          <div key={item.label} className="min-w-0 space-y-1.5">
            <div className="flex min-w-0 items-baseline justify-between gap-2">
              <span className="eyebrow truncate">{item.label}</span>
              <span className="numeric shrink-0 text-body font-medium">{item.value}</span>
            </div>
            {disabled || item.data.length < 2 ? (
              <div className="flex h-8 items-center text-hint text-muted-foreground">
                {disabled ? "History off" : "Collecting…"}
              </div>
            ) : (
              <Sparkline
                values={item.data}
                max={item.max}
                color={item.color}
                width={320}
                height={32}
                className="h-8 w-full"
                label={`${item.label} over the last hour`}
              />
            )}
          </div>
        ))}
      </PanelBody>
    </Panel>
  )
}

/**
 * Deploys, backups, restarts and the actions that change things — the list
 * form of the marks on the metric charts.
 */
function ActivityPanel({ events }: { events: MetricEvent[] }) {
  const newestFirst = useMemo(() => [...events].reverse(), [events])

  return (
    <Panel>
      <PanelHeader icon={CloudUpload} title="Recent activity" />
      <PanelBody className={newestFirst.length === 0 ? undefined : "max-h-[15rem] overflow-y-auto"}>
        {newestFirst.length === 0 ? (
          <p className="text-body text-muted-foreground">Nothing in the last hour.</p>
        ) : (
          <ol className="space-y-2.5">
            {newestFirst.map((event, i) => (
              <li key={`${event.ts}-${i}`} className="flex min-w-0 gap-2.5">
                <span
                  aria-hidden
                  className="mt-1.5 size-1.5 shrink-0 rounded-full"
                  style={{ background: eventColor(event) }}
                />
                <div className="min-w-0 flex-1">
                  <div className="flex min-w-0 items-baseline justify-between gap-2">
                    <span className="truncate text-body">{event.title}</span>
                    <span className="numeric shrink-0 text-hint text-muted-foreground">
                      {clock(event.ts)}
                    </span>
                  </div>
                  <p className="truncate text-hint text-muted-foreground">
                    {relativeTime(event.ts)}
                    {event.detail ? ` · ${event.detail}` : ""}
                  </p>
                </div>
              </li>
            ))}
          </ol>
        )}
      </PanelBody>
    </Panel>
  )
}

/**
 * A module that is genuinely absent on this host, as opposed to a poll that
 * failed once. The dashboard returns a precise code for the former; a dropped
 * VPN tunnel — which is a normal Tuesday here — is anything else, and must not
 * turn a card that read "12 running" into a false claim about the host.
 */
function moduleGone(error: Error | undefined): boolean {
  return (
    error instanceof ApiError &&
    (error.code === "docker_unavailable" ||
      error.code === "not_installed" ||
      error.code === "no_proxy")
  )
}

/**
 * One service, its headline figure, and the way to its page. Composed from
 * `Panel` rather than a hand-rolled card so it re-themes with everything else.
 */
function ServiceCard({
  icon: Icon,
  title,
  href,
  value,
  hint,
  loading,
  unavailable,
}: {
  icon: React.ComponentType<{ className?: string }>
  title: string
  href: string
  value?: React.ReactNode
  hint?: React.ReactNode
  loading?: boolean
  unavailable?: boolean
}) {
  return (
    <Link href={href} className="group block min-w-0">
      <Panel className="h-full transition-colors group-hover:border-rule-brand group-hover:bg-row-hover">
        <PanelBody className="flex flex-col gap-3">
          <div className="flex min-w-0 items-center justify-between gap-2">
            <div className="flex min-w-0 items-center gap-2">
              <span className="flex size-7 shrink-0 items-center justify-center rounded-md bg-plot-brand text-brand">
                <Icon className="size-3.5" />
              </span>
              <span className="truncate text-body font-medium">{title}</span>
            </div>
            <ArrowRight className="size-3.5 shrink-0 text-muted-foreground transition-transform group-hover:translate-x-0.5" />
          </div>
          {loading ? (
            <Skeleton className="h-6 w-24" />
          ) : unavailable ? (
            <p className="text-body text-muted-foreground">Not available on this host</p>
          ) : value == null ? (
            <p className="text-body text-muted-foreground">Unreachable</p>
          ) : (
            <div className="min-w-0">
              <p className="numeric truncate text-lg leading-tight font-semibold">{value}</p>
              {hint && <p className="truncate text-hint text-muted-foreground">{hint}</p>}
            </div>
          )}
        </PanelBody>
      </Panel>
    </Link>
  )
}

function DockerCard() {
  const { data, error, loading } = usePoll<Container[]>(
    (signal) => get<Container[]>("/docker/containers/", undefined, signal),
    60_000,
  )
  const running = data?.filter((c) => c.state === "running").length
  return (
    <ServiceCard
      icon={Box}
      title="Docker"
      href="/docker"
      loading={loading && !data}
      unavailable={moduleGone(error)}
      value={running === undefined ? undefined : `${running} running`}
      hint={data ? `${data.length} container${data.length === 1 ? "" : "s"}` : undefined}
    />
  )
}

function DatabasesCard() {
  const { data, error, loading } = usePoll<DbConnection[]>(
    (signal) => get<DbConnection[]>("/databases/", undefined, signal),
    60_000,
  )
  return (
    <ServiceCard
      icon={Database}
      title="Databases"
      href="/databases"
      loading={loading && !data}
      unavailable={moduleGone(error)}
      value={data ? (data.length === 0 ? "None yet" : `${data.length} connections`) : undefined}
      hint={data && data.length === 1 ? "1 connection" : undefined}
    />
  )
}

function ProxyCard() {
  const { data, error, loading } = usePoll<Certificate[]>(
    (signal) => get<Certificate[]>("/certificates/", undefined, signal),
    300_000,
  )
  const soonest = useMemo(() => {
    if (!data || data.length === 0) return undefined
    return [...data].sort((a, b) => a.daysLeft - b.daysLeft)[0]
  }, [data])
  return (
    <ServiceCard
      icon={Globe}
      title="Proxy & TLS"
      href="/proxy"
      loading={loading && !data}
      unavailable={moduleGone(error)}
      value={data ? `${data.length} certificate${data.length === 1 ? "" : "s"}` : undefined}
      hint={
        soonest
          ? soonest.expired
            ? "One expired"
            : `Renews in ${soonest.daysLeft}d`
          : data
            ? "None issued"
            : undefined
      }
    />
  )
}

function UpdatesCard() {
  const { report, loading } = useSelfUpdate()
  const behind = report?.releases.length ?? 0
  return (
    <ServiceCard
      icon={Puzzle}
      title="Dashboard"
      href="/dashboard"
      loading={loading && !report}
      value={report ? (behind === 0 ? "Up to date" : `${behind} behind`) : undefined}
      hint={report ? `v${report.version}${report.breaking ? " · breaking change" : ""}` : undefined}
    />
  )
}
