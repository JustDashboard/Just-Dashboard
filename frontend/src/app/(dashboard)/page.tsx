"use client"

import { useMemo } from "react"
import Link from "next/link"
import {
  Archive,
  ArrowRight,
  Box,
  CloudUpload,
  Database,
  Globe,
  Puzzle,
  SettingsGear,
  Shield,
} from "@/components/icons"
import { ApiError, get } from "@/lib/api"
import { bytes, clock, duration, percent, rate, relativeTime } from "@/lib/format"
import type {
  BackupJob,
  BackupRun,
  Certificate,
  Container,
  DbConnection,
  DeploymentFleet,
  DockerDiagnosis,
  Exposure,
  Health,
  HealthFinding,
  MetricEvent,
  MountStats,
  SystemdUnit,
  UpdateReport,
} from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useMetrics } from "@/hooks/use-metrics"
import { useHealth, useMetricEvents, useMetricsHistory } from "@/hooks/use-metrics-history"
import { useSelfUpdate } from "@/hooks/use-self-update"
import type { MetricsWindow } from "@/lib/metrics-range"
import { Page, PageHeader, PageState, Metric, MetricStrip, Section } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Row, RowList } from "@/components/row-list"
import { StatGrid, StatLink, StatTile } from "@/components/stat-tile"
import { utilisationTone } from "@/components/meter"
import type { Tone } from "@/components/tone"
import { HealthPanel, HealthVerdict } from "@/components/metrics/health-panel"
import { EXPOSURE_GRADE } from "@/components/security/exposure-panel"
import { Sparkline } from "@/components/metrics/sparkline"
import { eventColor } from "@/components/metrics/metric-chart"

import { Tag } from "@/components/tag"
import { cn } from "@/lib/utils"
import { Skeleton } from "@/components/ui/skeleton"

// A fixed hour, not the metrics page's draggable window: the landing page is a
// glance, and there is exactly one range control in the product — on /metrics.
const HOUR: MetricsWindow = { key: "1h" }

export default function OverviewPage() {
  const { host, snapshot, error } = useMetrics()
  const recorded = useMetricsHistory(HOUR)
  const events = useMetricEvents(HOUR)
  const { health: recordedHealth, loading: healthLoading } = useHealth()
  // Two verdicts the metrics recorder never sees, folded into the same list:
  // a systemd unit that has failed and a container failing its own health
  // check are exactly the "is anything wrong" a landing page exists to answer.
  const failedUnits = usePoll<SystemdUnit[]>(
    (signal) => get("/systemd/", { state: "failed" }, signal),
    60_000,
  )
  const docker = usePoll<DockerDiagnosis>(
    (signal) => get("/docker/health", undefined, signal),
    120_000,
  )
  const health = useMemo(
    () => foldHealth(recordedHealth, failedUnits.data, docker.data),
    [recordedHealth, failedUnits.data, docker.data],
  )

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
  const fullest = snapshot.mounts.reduce<MountStats | undefined>(
    (worst, m) => (!worst || m.usedPercent > worst.usedPercent ? m : worst),
    undefined,
  )

  return (
    <Page className="animate-rise">
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

      <StatGrid columns={5}>
        <StatTile
          label="CPU"
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
          value={bytes(snapshot.memory.available)}
          meter={100 - availPercent}
          tone={availPercent <= 5 ? "danger" : availPercent <= 10 ? "warning" : "default"}
          hint={`${percent(snapshot.memory.usedPercent, 0)} used · ${bytes(snapshot.memory.cached)} cached`}
          trailing={<span className="text-hint text-muted-foreground">available</span>}
        />
        <StatTile
          label="Load"
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
          value={rate(throughput.rx)}
          hint={`${rate(throughput.tx)} out · ${snapshot.sockets?.tcpInUse ?? 0} TCP sockets`}
          trailing={<span className="text-hint text-muted-foreground">in</span>}
        />
        {/* The fullest real filesystem, because that is the one that stops the
            machine — the recorder has no disk rule, so this tile is the only
            place a root partition at 96% is said out loud before it fails. */}
        <StatTile
          label="Storage"
          value={fullest ? bytes(fullest.free) : "—"}
          meter={fullest?.usedPercent}
          tone={fullest ? utilisationTone(fullest.usedPercent) : "default"}
          hint={
            fullest
              ? `${percent(fullest.usedPercent, 0)} used of ${bytes(fullest.total)}`
              : "No filesystems reported"
          }
          trailing={
            fullest && (
              <span className="truncate text-hint text-muted-foreground">
                free on {fullest.mountpoint}
              </span>
            )
          }
        />
      </StatGrid>

      {/* A titled list on the page, not a box: the verdict is already in the
          host row above, and the findings are the first thing to read after
          the numbers — a frame around them made the page open with a stack of
          two containers before anything else. */}
      <HealthPanel
        plain
        health={health}
        loading={healthLoading}
        emptyLabel={
          health?.recorded
            ? "Capacity, memory, CPU steal, pressure, sockets, services and containers all within limits"
            : "Every check passed on the current reading"
        }
      />

      <div className="grid items-start gap-6 lg:grid-cols-3 [&>*]:min-w-0">
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

      {/* The same run of readings as the tiles at the top, one per module,
          because a module's headline figure *is* a reading. Eight framed cards
          were eight boxes under a page that had just stopped drawing any. */}
      <Section title="Services">
        <StatGrid columns={4}>
          <DockerCard />
          <DatabasesCard />
          <ProxyCard />
          <SecurityCard />
          <PackagesCard />
          <DeploymentsCard />
          <BackupsCard />
          <UpdatesCard />
        </StatGrid>
      </Section>
    </Page>
  )
}

function Dot() {
  return <span className="text-muted-foreground/40">·</span>
}

const VERDICT_RANK: Record<Health["status"], number> = { ok: 0, notice: 1, warning: 2, critical: 3 }

/**
 * The recorder's verdict plus the two the host reports about itself. A poll
 * that failed contributes nothing rather than a finding about the poll: the
 * list is what is wrong with the server, not with this page's fetches.
 */
function foldHealth(
  health: Health | undefined,
  failedUnits: SystemdUnit[] | undefined,
  docker: DockerDiagnosis | undefined,
): Health | undefined {
  if (!health) return health
  const extra: HealthFinding[] = []
  if (failedUnits && failedUnits.length > 0) {
    const n = failedUnits.length
    const names = failedUnits.map((u) => u.name)
    extra.push({
      id: "systemd.failed",
      level: "warning",
      title: n === 1 ? `${names[0]} has failed` : `${n} services have failed`,
      detail: n === 1 ? failedUnits[0].description || names[0] : names.join(", "),
      advice:
        "Read the unit's journal under Processes → Services, then restart it — or disable it if nothing needs it any more.",
      value: n,
      threshold: 0,
    })
  }
  const runtime = docker?.runtime
  if (runtime && runtime.unhealthy > 0) {
    const n = runtime.unhealthy
    extra.push({
      id: "docker.unhealthy",
      level: runtime.status === "critical" ? "critical" : "warning",
      title: n === 1 ? "1 container is unhealthy" : `${n} containers are unhealthy`,
      detail:
        n === 1
          ? `1 of ${runtime.running} running containers fails its own health check`
          : `${n} of ${runtime.running} running containers fail their own health checks`,
      advice:
        "Open Docker → Containers: the failure diagnosis on each one says what the check saw.",
      value: n,
      threshold: 0,
    })
  }
  if (runtime && runtime.restarting > 0) {
    const n = runtime.restarting
    extra.push({
      id: "docker.restarting",
      level: "warning",
      title: n === 1 ? "1 container is restarting" : `${n} containers are restarting`,
      detail: "Docker keeps restarting it, which means it keeps exiting",
      advice: "Its logs and failure diagnosis under Docker → Containers say why it exits.",
      value: n,
      threshold: 0,
    })
  }
  if (extra.length === 0) return health
  const status = extra.reduce<Health["status"]>(
    (worst, f) => (VERDICT_RANK[f.level] > VERDICT_RANK[worst] ? f.level : worst),
    health.status,
  )
  return { ...health, status, findings: [...health.findings, ...extra] }
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
 * and the answer to "what exactly" is one click into /metrics. Plain, like the
 * health list beside it: it was the one box left on the top half of the page,
 * and a frame around four lines separated them from nothing.
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
    <Panel plain className={className}>
      <PanelHeader
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
              <div className="animate-rise">
                <Sparkline
                  values={item.data}
                  max={item.max}
                  color={item.color}
                  width={320}
                  height={32}
                  className="h-8 w-full"
                  label={`${item.label} over the last hour`}
                />
              </div>
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
    <Panel plain>
      <PanelHeader title="Recent activity" />
      <PanelBody
        flush
        className={
          newestFirst.length === 0 ? "py-4" : "-mx-3 max-h-[17rem] overflow-y-auto px-3 py-1"
        }
      >
        {newestFirst.length === 0 ? (
          <p className="text-body text-muted-foreground">Nothing in the last hour.</p>
        ) : (
          <RowList className="animate-rise">
            {newestFirst.map((event, i) => (
              <Row
                key={`${event.ts}-${i}`}
                leading={
                  <span
                    aria-hidden
                    className="size-1.5 rounded-full"
                    style={{ background: eventColor(event) }}
                  />
                }
                title={event.title}
                subtitle={`${relativeTime(event.ts)}${event.detail ? ` · ${event.detail}` : ""}`}
                trailing={
                  <span className="numeric text-hint text-muted-foreground">{clock(event.ts)}</span>
                }
                className="py-2.5"
              />
            ))}
          </RowList>
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
 * One module, its headline figure, and the way to its page — a `StatTile`
 * behind a `StatLink`, exactly as the Docker overview draws its own run, so
 * the Services row reads as the top row does: figures on the page, hairlines
 * between them, and an arrow that says the tile goes somewhere.
 *
 * The glyph before the name is wayfinding, not decoration: it is the same
 * mark the sidebar entry carries, so the eye finds "Docker" without reading.
 * The figure rises once when its poll lands, so a page of eight tiles fills in
 * rather than flickering from bone to number.
 */
function ServiceTile({
  icon: Icon,
  title,
  href,
  value,
  hint,
  tone = "default",
  loading,
  unavailable,
}: {
  icon: React.ComponentType<{ className?: string }>
  title: string
  href: string
  value?: React.ReactNode
  hint?: React.ReactNode
  /** Colours the figure as a reading of state, never as decoration. */
  tone?: Tone
  loading?: boolean
  unavailable?: boolean
}) {
  const settled = !loading
  const figure = loading ? (
    <Skeleton className="my-1.5 h-5 w-24" />
  ) : unavailable ? (
    "Not available"
  ) : value == null ? (
    "Unreachable"
  ) : (
    value
  )
  return (
    <StatLink href={href} label={title}>
      <StatTile
        className="h-full transition-colors group-hover:bg-row-hover"
        label={
          <>
            <Icon aria-hidden className="mr-1.5 inline-block size-3 align-[-1.5px] text-brand" />
            {title}
          </>
        }
        value={
          <span
            key={settled ? "figure" : "skeleton"}
            className={cn(
              "inline-block max-w-full truncate align-bottom",
              settled && "animate-rise",
            )}
          >
            {figure}
          </span>
        }
        tone={unavailable || value == null ? "default" : tone}
        hint={unavailable ? "on this host" : hint}
      />
    </StatLink>
  )
}

function DockerCard() {
  const { data, error, loading } = usePoll<Container[]>(
    (signal) => get<Container[]>("/docker/containers/", undefined, signal),
    60_000,
  )
  const running = data?.filter((c) => c.state === "running").length
  return (
    <ServiceTile
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
    <ServiceTile
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
    <ServiceTile
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

function SecurityCard() {
  const { data, error, loading } = usePoll<Exposure>(
    (signal) => get<Exposure>("/exposure", undefined, signal),
    60_000,
  )
  const grade = data ? EXPOSURE_GRADE[data.grade] : undefined
  return (
    <ServiceTile
      icon={Shield}
      title="Security"
      href="/security"
      loading={loading && !data}
      unavailable={moduleGone(error)}
      value={grade?.label}
      tone={
        grade?.verdict === "critical"
          ? "danger"
          : grade?.verdict === "warning"
            ? "warning"
            : "default"
      }
      hint={
        data
          ? `${data.allowlist.length} allowed range${data.allowlist.length === 1 ? "" : "s"} · ${data.interfaces.length} interface${data.interfaces.length === 1 ? "" : "s"}`
          : undefined
      }
    />
  )
}

function PackagesCard() {
  const { data, error, loading } = usePoll<UpdateReport>(
    (signal) => get<UpdateReport>("/packages/updates", undefined, signal),
    300_000,
  )
  const pending = data?.packages.length ?? 0
  return (
    <ServiceTile
      icon={Puzzle}
      title="Packages"
      href="/packages"
      loading={loading && !data}
      unavailable={moduleGone(error) || data?.available === false}
      value={
        data
          ? pending === 0
            ? "Up to date"
            : `${pending} update${pending === 1 ? "" : "s"}`
          : undefined
      }
      tone={data?.rebootRequired || (data?.securityCount ?? 0) > 0 ? "warning" : "default"}
      hint={
        data?.rebootRequired
          ? "Reboot required"
          : data && data.securityCount > 0
            ? `${data.securityCount} security`
            : data && pending > 0 && data.securityFiltering
              ? "None are security updates"
              : undefined
      }
    />
  )
}

/** A deployment's last run, read as a verdict rather than as a state machine. */
function runFailed(state: string | undefined): boolean {
  return state === "failed" || state === "failed_activation" || state === "rolled_back"
}

function DeploymentsCard() {
  const { data, error, loading } = usePoll<DeploymentFleet>(
    (signal) => get<DeploymentFleet>("/deploy/", { view: "fleet" }, signal),
    60_000,
  )
  const deployments = data?.deployments ?? []
  const active = data?.activeWork.length ?? 0
  const failed = deployments.filter((d) => runFailed(d.lastRun?.state)).length
  const unhealthy = deployments.filter((d) => d.health === "unhealthy").length
  const count = `${deployments.length} deployment${deployments.length === 1 ? "" : "s"}`
  return (
    <ServiceTile
      icon={CloudUpload}
      title="Deployments"
      href="/deploy"
      loading={loading && !data}
      unavailable={moduleGone(error)}
      value={
        !data
          ? undefined
          : deployments.length === 0
            ? "None yet"
            : active > 0
              ? `${active} deploying`
              : failed > 0
                ? `${failed} failed`
                : unhealthy > 0
                  ? `${unhealthy} unhealthy`
                  : `${deployments.length} live`
      }
      tone={active === 0 && (failed > 0 || unhealthy > 0) ? "danger" : "default"}
      hint={
        !data || deployments.length === 0
          ? undefined
          : active > 0 && failed > 0
            ? `${count} · ${failed} failed`
            : count
      }
    />
  )
}

function BackupsCard() {
  const { data, error, loading } = usePoll<BackupJob[]>(
    (signal) => get<BackupJob[]>("/backups/", undefined, signal),
    120_000,
  )
  const latest = useMemo(() => {
    if (!data) return undefined
    return data
      .map((j) => j.lastRun)
      .filter((r): r is BackupRun => Boolean(r))
      .sort((a, b) => b.startedAt.localeCompare(a.startedAt))[0]
  }, [data])
  const next = useMemo(() => {
    if (!data) return undefined
    return data
      .filter((j) => j.enabled && j.nextRun)
      .map((j) => j.nextRun as string)
      .sort()[0]
  }, [data])
  return (
    <ServiceTile
      icon={Archive}
      title="Backups"
      href="/backups"
      loading={loading && !data}
      unavailable={moduleGone(error)}
      value={
        !data
          ? undefined
          : data.length === 0
            ? "None yet"
            : !latest
              ? "No runs yet"
              : latest.status === "failed"
                ? "Last run failed"
                : latest.status === "running"
                  ? "Running"
                  : "Last run OK"
      }
      tone={latest?.status === "failed" ? "danger" : "default"}
      hint={
        latest
          ? `${relativeTime(latest.startedAt)}${next ? ` · next ${relativeTime(next)}` : ""}`
          : data && data.length > 0
            ? `${data.length} job${data.length === 1 ? "" : "s"} scheduled`
            : undefined
      }
    />
  )
}

function UpdatesCard() {
  const { report, loading } = useSelfUpdate()
  const behind = report?.releases.length ?? 0
  return (
    <ServiceTile
      icon={SettingsGear}
      title="Dashboard"
      href="/dashboard"
      loading={loading && !report}
      value={report ? (behind === 0 ? "Up to date" : `${behind} behind`) : undefined}
      hint={report ? `v${report.version}${report.breaking ? " · breaking change" : ""}` : undefined}
    />
  )
}
