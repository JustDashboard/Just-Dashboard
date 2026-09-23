"use client"

import { useMemo } from "react"
import Link from "next/link"
import {
  Archive,
  Box,
  ChartActivity,
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
import { Page, PageHeader, PageState, Section } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Row, RowList } from "@/components/row-list"
import { StatGrid, StatLink, StatTile } from "@/components/stat-tile"
import { utilisationTone } from "@/components/meter"
import type { Tone } from "@/components/tone"
import { HealthPanel, HealthVerdict } from "@/components/metrics/health-panel"
import { EXPOSURE_GRADE } from "@/components/security/exposure-panel"
import { Sparkline } from "@/components/metrics/sparkline"
import { eventColor } from "@/components/metrics/metric-chart"
import { FactDot, HostFact, HostIdentity, platformName } from "@/components/metrics/host-identity"
import {
  ProductGlyphs,
  cpuProduct,
  imageProducts,
  platformProduct,
  virtualizationProduct,
} from "@/components/product-logo"
import { Button } from "@/components/ui/button"
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
      load: points.map((p) => p.load1),
      net: points.map((p) => p.rx + p.tx),
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

  const trend = (values: number[], label: string, color: string, max?: number) =>
    recorded.disabled || values.length < 2 ? undefined : (
      <div className="h-full animate-rise">
        <Sparkline
          values={values}
          max={max}
          color={color}
          width={240}
          height={36}
          className="h-9 w-full"
          label={`${label} over the last hour`}
        />
      </div>
    )

  return (
    <Page className="animate-rise">
      <PageHeader
        eyebrow="Server"
        title={host.hostname}
        actions={
          <Button variant="outline" asChild>
            <Link href="/metrics">
              <ChartActivity />
              Metrics
            </Link>
          </Button>
        }
      />

      {/* What this machine is, in one line: the distribution drawn as
          itself, then what it runs on. The uptime, process count and cores
          that stood in the header's corner are facts about the machine and
          sit with the rest of them; the cores were said again on the CPU
          tile. */}
      <HostIdentity
        mark={platformProduct(host.platform)}
        title={
          <>
            {platformName(host)}{" "}
            <span className="font-mono text-body font-normal text-muted-foreground">
              {host.kernelVersion}
            </span>
          </>
        }
        facts={
          <>
            <HostFact product={cpuProduct(host.cpuModel, host.kernelArch)}>
              {host.cpuModel || host.kernelArch}
            </HostFact>
            <FactDot />
            <span className="numeric">{cores} cores</span>
            {host.virtualization && (
              <>
                <FactDot />
                <HostFact product={virtualizationProduct(host.virtualization)}>
                  {host.virtualization}
                </HostFact>
              </>
            )}
            <FactDot />
            <span className="numeric">up {duration(snapshot.uptimeSeconds)}</span>
            <FactDot />
            <span className="numeric">{snapshot.procs?.total || host.processes} processes</span>
          </>
        }
        aside={health && <HealthVerdict status={health.status} className="text-body" />}
      />

      {/* Each reading carries its last hour where a meter would be, for the
          four that move. They were a panel of four sparklines further down,
          each under a figure that repeated the tile above it; the fullest
          filesystem fills rather than moves, and keeps its meter. */}
      <StatGrid columns={5}>
        <StatTile
          label="CPU"
          value={percent(snapshot.cpu.totalPercent)}
          tone={utilisationTone(snapshot.cpu.totalPercent)}
          trend={trend(trends.cpu, "CPU", "var(--chart-1)", 100)}
          meter={recorded.disabled ? snapshot.cpu.totalPercent : undefined}
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
            ) : undefined
          }
        />
        <StatTile
          label="Memory"
          value={bytes(snapshot.memory.available)}
          tone={availPercent <= 5 ? "danger" : availPercent <= 10 ? "warning" : "default"}
          trend={trend(trends.mem, "Memory", "var(--chart-2)", 100)}
          meter={recorded.disabled ? 100 - availPercent : undefined}
          hint={`${percent(snapshot.memory.usedPercent, 0)} used · ${bytes(snapshot.memory.cached)} cached`}
          trailing="free"
        />
        <StatTile
          label="Load"
          value={snapshot.cpu.loadAvg1.toFixed(2)}
          tone={utilisationTone((snapshot.cpu.loadAvg5 / cores) * 100)}
          trend={trend(trends.load, "Load", "var(--chart-3)", cores)}
          meter={recorded.disabled ? (snapshot.cpu.loadAvg1 / cores) * 100 : undefined}
          hint={`${snapshot.cpu.loadAvg5.toFixed(2)} · ${snapshot.cpu.loadAvg15.toFixed(2)} over 5 and 15 min`}
          trailing={
            <span className="numeric">{(snapshot.cpu.loadAvg1 / cores).toFixed(2)}/core</span>
          }
        />
        <StatTile
          label="Network"
          value={rate(throughput.rx)}
          trend={trend(trends.net, "Network", "var(--chart-5)")}
          hint={`${rate(throughput.tx)} out · ${snapshot.sockets?.tcpInUse ?? 0} TCP sockets`}
          trailing="in"
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
              ? `${percent(fullest.usedPercent, 0)} of ${bytes(fullest.total)} · ${rate(diskRate)} I/O`
              : "No filesystems reported"
          }
          trailing={fullest && <span className="truncate">free on {fullest.mountpoint}</span>}
        />
      </StatGrid>

      {/* The findings and what happened, side by side: the first two things
          to read after the numbers, and neither needs the full width. */}
      <div className="grid items-start gap-8 lg:grid-cols-3 [&>*]:min-w-0">
        <HealthPanel
          plain
          className="lg:col-span-2"
          health={health}
          loading={healthLoading}
          emptyLabel={
            health?.recorded
              ? "Capacity, memory, CPU steal, pressure, sockets, services and containers all within limits"
              : "Every check passed on the current reading"
          }
        />
        <ActivityPanel events={events} />
      </div>

      {/* The same run of readings as the tiles at the top, one per module,
          because a module's headline figure *is* a reading. Each says what
          it counts with the products themselves — the images running, the
          engines connected — after its words. */}
      <Section title="Services">
        <StatGrid columns={4}>
          <DockerCard />
          <DatabasesCard />
          <ProxyCard />
          <SecurityCard />
          <PackagesCard platform={host.platform} />
          <DeploymentsCard />
          <BackupsCard />
          <UpdatesCard />
        </StatGrid>
      </Section>
    </Page>
  )
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
  products = [],
}: {
  icon: React.ComponentType<{ className?: string }>
  title: string
  href: string
  value?: React.ReactNode
  hint?: React.ReactNode
  /** What the figure counts, as the products themselves — `product-logo` ids. */
  products?: string[]
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
        hint={
          unavailable ? (
            "on this host"
          ) : (
            <span className="inline-flex max-w-full min-w-0 items-center gap-2">
              {hint && <span className="truncate">{hint}</span>}
              {settled && <ProductGlyphs ids={products} />}
            </span>
          )
        }
      />
    </StatLink>
  )
}

function DockerCard() {
  const { data, error, loading } = usePoll<Container[]>(
    (signal) => get<Container[]>("/docker/containers/", undefined, signal),
    60_000,
  )
  const running = data?.filter((c) => c.state === "running")
  const products = imageProducts((running ?? []).map((c) => c.image))
  return (
    <ServiceTile
      icon={Box}
      title="Docker"
      href="/docker"
      products={products}
      loading={loading && !data}
      unavailable={moduleGone(error)}
      value={running === undefined ? undefined : `${running.length} running`}
      hint={data ? `${data.length} container${data.length === 1 ? "" : "s"}` : undefined}
    />
  )
}

function DatabasesCard() {
  const { data, error, loading } = usePoll<DbConnection[]>(
    (signal) => get<DbConnection[]>("/databases/", undefined, signal),
    60_000,
  )
  const engines = [
    ...new Set((data ?? []).map((c) => (c.driver === "postgres" ? "postgresql" : c.driver))),
  ]
  return (
    <ServiceTile
      icon={Database}
      title="Databases"
      href="/databases"
      products={engines}
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
      products={data && data.length > 0 ? ["nginx-static", "lets-encrypt"] : ["nginx-static"]}
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
      products={data?.grade === "tailscale" ? ["tailscale"] : []}
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

function PackagesCard({ platform }: { platform: string }) {
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
      products={[platformProduct(platform)].filter((id): id is string => Boolean(id))}
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
  // A job that has gone quiet is as much a finding as one that failed: the
  // schedule stopped delivering, and nothing else on the page would say so.
  const overdue = data?.filter((j) => j.overdue).length ?? 0
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
                  : overdue > 0
                    ? `${overdue} overdue`
                    : "Last run OK"
      }
      tone={latest?.status === "failed" ? "danger" : overdue > 0 ? "warning" : "default"}
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
      title="Settings"
      href="/dashboard"
      loading={loading && !report}
      value={report ? (behind === 0 ? "Up to date" : `${behind} behind`) : undefined}
      hint={report ? `v${report.version}${report.breaking ? " · breaking change" : ""}` : undefined}
    />
  )
}
