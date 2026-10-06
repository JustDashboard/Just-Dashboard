"use client"

import { useMemo } from "react"
import Link from "next/link"
import {
  Archive,
  ArrowRight,
  Box,
  Database,
  GitHubMark,
  Globe,
  Plus,
  Puzzle,
  SettingsGear,
  Shield,
} from "@/components/icons"
import { ApiError, get } from "@/lib/api"
import { unusableCount } from "@/lib/db-connections"
import { bytes, duration, percent, plural, rate, relativeTime } from "@/lib/format"
import type {
  BackupJob,
  BackupRun,
  Certificate,
  Container,
  DbConnection,
  DeploymentFleet,
  Exposure,
  GitRepo,
  TrafficPulse,
  UpdateReport,
} from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import { type PollState, usePoll } from "@/hooks/use-poll"
import { useMetrics } from "@/hooks/use-metrics"
import { useHealth, useMetricEvents, useMetricsHistory } from "@/hooks/use-metrics-history"
import { useSelfUpdate } from "@/hooks/use-self-update"
import type { MetricsWindow } from "@/lib/metrics-range"
import type { ConnectionState } from "@/lib/metrics-store"
import { Page, PageContext, PageState, Section } from "@/components/page"
import { StatGrid, StatLink, StatTile } from "@/components/stat-tile"
import { utilisationTone } from "@/components/meter"
import { Status } from "@/components/status-dot"
import type { Tone } from "@/components/tone"
import { EmptyState } from "@/components/state"
import { useConfirm } from "@/components/confirm-dialog"
import { HealthPanel, HealthVerdict } from "@/components/metrics/health-panel"
import { TopProcesses } from "@/components/metrics/top-processes"
import { EXPOSURE_GRADE } from "@/components/security/exposure-panel"
import { TileTrend } from "@/components/metrics/sparkline"
import { engineFor } from "@/components/database/engine"
import { FactDot, HostFact, HostIdentity, platformName } from "@/components/metrics/host-identity"
import { ProjectCard } from "@/components/deploy/fleet-card"
import { sortFleet } from "@/components/deploy/fleet"
import { serverAttention, verdictWith } from "@/components/overview/attention"
import { ActivityPanel } from "@/components/overview/activity"
import { CoreBars, LiveBytes, LiveFigure, SeriesKey } from "@/components/overview/readings"
import { StorageBand } from "@/components/overview/storage"
import {
  ProductGlyphs,
  ProductLogos,
  containerProducts,
  cpuProduct,
  hostProduct,
  platformProduct,
  virtualizationProduct,
} from "@/components/product-logo"
import { Button } from "@/components/ui/button"
import { cn } from "@/lib/utils"
import { Skeleton } from "@/components/ui/skeleton"

// A fixed hour, not the metrics page's draggable window: the landing page is a
// glance, and there is exactly one range control in the product — on /metrics.
const HOUR: MetricsWindow = { key: "1h" }
// Activity reads a day rather than the tiles' hour: a nightly backup and the
// deploy before lunch are what "recent" means to somebody opening the page,
// and an hour was "Nothing in the last hour" on most visits.
const DAY: MetricsWindow = { key: "24h" }

/** Two rows of three: the projects that need the reader most, then the way to the rest. */
const SHOWN_PROJECTS = 6

/** Each reading's line, and the key before its name that says which line is whose. */
const SERIES = {
  cpu: "var(--chart-1)",
  mem: "var(--chart-2)",
  load: "var(--chart-3)",
  net: "var(--chart-5)",
  disk: "var(--chart-4)",
}

export default function OverviewPage() {
  const { host, snapshot, error, connection } = useMetrics()
  const recorded = useMetricsHistory(HOUR)
  const events = useMetricEvents(DAY)
  const { health, loading: healthLoading, error: healthError } = useHealth()
  const reads = useModuleReads()
  const { confirm, dialog } = useConfirm()

  const attention = useMemo(
    () =>
      serverAttention({
        deployments: reads.fleet.data?.deployments,
        pulses: reads.traffic.data,
        backups: reads.backups.data,
        certificates: reads.certificates.data,
        packages: reads.packages.data,
        databases: reads.databases.data,
        exposure: reads.exposure.data,
      }),
    [
      reads.fleet.data,
      reads.traffic.data,
      reads.backups.data,
      reads.certificates.data,
      reads.packages.data,
      reads.databases.data,
      reads.exposure.data,
    ],
  )

  const trends = useMemo(() => {
    const points = recorded.history?.points ?? []
    return {
      cpu: points.map((p) => p.cpu),
      mem: points.map((p) => p.mem),
      load: points.map((p) => p.load1),
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
  const availPercent =
    snapshot.memory.total > 0 ? (snapshot.memory.available / snapshot.memory.total) * 100 : 0
  const cores = snapshot.cpu.cores || 1

  // `TileTrend` leaves out a line that never moves on a scale of its own, which
  // would otherwise fill the band and read as a full meter.
  const trend = (values: number[], label: string, color: string, max?: number) =>
    recorded.disabled ? undefined : (
      <TileTrend values={values} max={max} color={color} label={`${label} over the last hour`} />
    )

  return (
    <Page className="animate-rise">
      <PageContext title={host.hostname} />

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
            <span className="font-medium text-foreground">{host.hostname}</span>
            <FactDot />
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
        aside={
          health && (
            <HealthVerdict
              partial={!!health.silences?.length}
              status={verdictWith(health.status, attention)}
              className="text-body"
            />
          )
        }
      />

      {/* The machine's readings, under a head that says they are arriving
          and leads to the page that records them. The way to Metrics stood
          at the identity line's end as a button, a second control beside the
          verdict on a line that otherwise only describes; it is a section's
          way onward now, worded and drawn as Deployments' is. */}
      <Section
        title="Resources"
        actions={
          <div className="flex items-center gap-4">
            <StreamState connection={connection} />
            <SectionLink href="/metrics">All metrics</SectionLink>
          </div>
        }
      >
        <div>
          {/* Each reading carries its last hour where a meter would be, keyed
              by the colour of its line, and its figure glides to each frame
              of the socket rather than jumping. The fullest filesystem was a
              fifth tile here that filled rather than moved; the disks are a
              band of their own under the four now. */}
          <StatGrid columns={4}>
            <StatTile
              label={
                <>
                  <SeriesKey color={SERIES.cpu} />
                  CPU
                </>
              }
              meterLabel="CPU"
              value={<LiveFigure value={snapshot.cpu.totalPercent} decimals={1} unit="%" />}
              tone={utilisationTone(snapshot.cpu.totalPercent)}
              trend={trend(trends.cpu, "CPU", SERIES.cpu, 100)}
              meter={recorded.disabled ? snapshot.cpu.totalPercent : undefined}
              hint={
                modes
                  ? `${modes.user.toFixed(0)}% user · ${modes.system.toFixed(0)}% sys · ${modes.iowait.toFixed(0)}% wait`
                  : `load ${snapshot.cpu.loadAvg1.toFixed(2)}`
              }
              trailing={
                <span className="inline-flex items-center gap-2">
                  <CoreBars cores={snapshot.cpu.perCore} color={SERIES.cpu} />
                  {modes && modes.steal >= 1 && (
                    <span className="numeric font-medium text-destructive">
                      {percent(modes.steal, 0)} steal
                    </span>
                  )}
                </span>
              }
            />
            <StatTile
              label={
                <>
                  <SeriesKey color={SERIES.mem} />
                  Memory
                </>
              }
              meterLabel="Memory"
              value={<LiveBytes value={snapshot.memory.available} />}
              tone={availPercent <= 5 ? "danger" : availPercent <= 10 ? "warning" : "default"}
              trend={trend(trends.mem, "Memory", SERIES.mem, 100)}
              meter={recorded.disabled ? 100 - availPercent : undefined}
              hint={`${percent(snapshot.memory.usedPercent, 0)} used · ${bytes(snapshot.memory.cached)} cached`}
              trailing="free"
            />
            <StatTile
              label={
                <>
                  <SeriesKey color={SERIES.load} />
                  Load
                </>
              }
              meterLabel="Load"
              value={<LiveFigure value={snapshot.cpu.loadAvg1} decimals={2} />}
              tone={utilisationTone((snapshot.cpu.loadAvg5 / cores) * 100)}
              trend={trend(trends.load, "Load", SERIES.load, cores)}
              meter={recorded.disabled ? (snapshot.cpu.loadAvg1 / cores) * 100 : undefined}
              hint={`${snapshot.cpu.loadAvg5.toFixed(2)} · ${snapshot.cpu.loadAvg15.toFixed(2)} over 5 and 15 min`}
              trailing={
                <span className="numeric">{(snapshot.cpu.loadAvg1 / cores).toFixed(2)}/core</span>
              }
            />
            <StatTile
              label={
                <>
                  <SeriesKey color={SERIES.net} />
                  Network
                </>
              }
              value={<LiveBytes value={throughput.rx} suffix="/s" />}
              trend={trend(trends.net, "Network", SERIES.net)}
              hint={`${rate(throughput.tx)} out · ${snapshot.sockets?.tcpInUse ?? 0} TCP sockets`}
              trailing="in"
            />
          </StatGrid>
          <StorageBand
            mounts={snapshot.mounts}
            trend={trend(trends.disk, "Disk I/O", SERIES.disk)}
            color={SERIES.disk}
          />
        </div>
      </Section>

      {/* What needs the reader, from the machine and from every module on
          it, as one list: the recorder's findings and a failed deploy, a
          quiet backup or a certificate past its renewal, worst first. It
          has the width to itself because it is the first thing to read
          after the numbers, and a short list beside a tall one left half
          the row empty. */}
      <HealthPanel
        plain
        health={health}
        also={attention}
        error={healthError}
        loading={healthLoading}
        emptyLabel={
          health?.recorded
            ? "Capacity, memory, CPU steal, pressure, sockets, services, containers, deployments, backups and certificates all within limits"
            : "Every check passed on the current reading"
        }
      />

      <DeploymentsSection fleet={reads.fleet} traffic={reads.traffic.data} confirm={confirm} />

      {/* Who is spending the machine and what changed on it — the two
          questions the tiles raise — side by side, as /metrics pairs its
          process list with its notable moments. */}
      <div className="grid items-start gap-8 lg:grid-cols-2 [&>*]:min-w-0">
        <TopProcesses />
        <ActivityPanel events={events} />
      </div>

      {/* The same run of readings as the tiles at the top, one per module,
          because a module's headline figure *is* a reading. Each says what
          it counts with the products themselves — the images running, the
          engines connected — after its words. Deployments has its own
          section above, so its tile went to Git. */}
      <Section title="Services">
        <StatGrid columns={4}>
          <DockerCard read={reads.docker} />
          <DatabasesCard read={reads.databases} />
          <ProxyCard read={reads.certificates} />
          <SecurityCard read={reads.exposure} />
          <PackagesCard read={reads.packages} platform={host.platform} />
          <BackupsCard read={reads.backups} />
          <GitCard read={reads.git} />
          <UpdatesCard />
        </StatGrid>
      </Section>
      {dialog}
    </Page>
  )
}

/**
 * Every module's own read, once, at the page: the Services tiles draw them
 * and the Health list reads what is wrong out of them, so a failed backup is
 * one request whether it is said as a figure or as a finding.
 */
function useModuleReads() {
  return {
    docker: usePoll<Container[]>(
      (signal) => get<Container[]>("/docker/containers/", undefined, signal),
      60_000,
    ),
    databases: usePoll<DbConnection[]>(
      (signal) => get<DbConnection[]>("/databases/", undefined, signal),
      60_000,
    ),
    certificates: usePoll<Certificate[]>(
      (signal) => get<Certificate[]>("/certificates/", undefined, signal),
      300_000,
    ),
    exposure: usePoll<Exposure>((signal) => get<Exposure>("/exposure", undefined, signal), 60_000),
    packages: usePoll<UpdateReport>(
      (signal) => get<UpdateReport>("/packages/updates", undefined, signal),
      300_000,
    ),
    // The fleet's own cadence is five seconds; a glance can take ten, which
    // still moves a run's release path while the reader watches.
    fleet: usePoll<DeploymentFleet>(
      (signal) => get<DeploymentFleet>("/deploy/", { view: "fleet" }, signal),
      10_000,
    ),
    traffic: usePoll<Record<string, TrafficPulse>>(
      (signal) => get<Record<string, TrafficPulse>>("/deploy/traffic", undefined, signal),
      60_000,
    ),
    backups: usePoll<BackupJob[]>(
      (signal) => get<BackupJob[]>("/backups/", undefined, signal),
      120_000,
    ),
    git: usePoll<{ available: boolean; repos: GitRepo[] }>(
      (signal) => get<{ available: boolean; repos: GitRepo[] }>("/git/", undefined, signal),
      120_000,
    ),
  }
}

/**
 * The projects on this server, as the fleet draws them: each the product it
 * is, its address, its traffic and its last runs, with a light round the
 * edge while one deploys. Worst first, so a failing project is on the first
 * row, and capped at two rows — the fleet is one press away for the rest.
 */
function DeploymentsSection({
  fleet,
  traffic,
  confirm,
}: {
  fleet: PollState<DeploymentFleet>
  traffic: Record<string, TrafficPulse> | undefined
  confirm: ReturnType<typeof useConfirm>["confirm"]
}) {
  const { can } = useAuth()
  const admin = can("system.admin")
  const deployments = useMemo(() => sortFleet(fleet.data?.deployments ?? []), [fleet.data])
  const shown = deployments.slice(0, SHOWN_PROJECTS)

  return (
    <Section
      title="Deployments"
      actions={
        <div className="flex items-center gap-3">
          {deployments.length > shown.length && (
            <span className="numeric text-hint text-muted-foreground">
              Showing {shown.length} of {deployments.length}
            </span>
          )}
          {admin && deployments.length > 0 && (
            <Button variant="outline" size="sm" asChild>
              <Link href="/deploy/new">
                <Plus className="size-3.5" />
                New project
              </Link>
            </Button>
          )}
          <SectionLink href="/deploy">All projects</SectionLink>
        </div>
      }
    >
      {fleet.loading && !fleet.data ? (
        <ul aria-hidden className={PROJECT_GRID}>
          {[0, 1, 2].map((index) => (
            <li key={index}>
              <Skeleton className="h-50.5 rounded-xl" />
            </li>
          ))}
        </ul>
      ) : !fleet.data ? (
        <p className="text-body text-muted-foreground">The deployments could not be read.</p>
      ) : deployments.length === 0 ? (
        <EmptyState
          mark={<ProductLogos ids={["github", "docker", "docker-compose"]} size="md" />}
          title="Deploy your first project"
          description="A repository, an image, a template or a compose file — everything you deploy is watched from here."
          action={
            admin && (
              <Button size="sm" asChild>
                <Link href="/deploy/new">New project</Link>
              </Button>
            )
          }
        />
      ) : (
        <ul aria-label="Deployment projects" className={PROJECT_GRID}>
          {shown.map((deployment, index) => (
            <ProjectCard
              key={deployment.id}
              index={index}
              deployment={deployment}
              pulse={traffic?.[String(deployment.id)]}
              work={fleet.data?.activeWork.find((item) => item.run.id === deployment.activeRun?.id)}
              confirm={confirm}
              refresh={fleet.refresh}
            />
          ))}
        </ul>
      )}
    </Section>
  )
}

/** The way on to the page a section stands for, at the end of its head. */
function SectionLink({ href, children }: { href: string; children: React.ReactNode }) {
  return (
    <Link
      href={href}
      className="flex items-center gap-1 rounded-md text-hint font-medium text-muted-foreground focus-ring hover:text-foreground"
    >
      {children} <ArrowRight className="size-3" />
    </Link>
  )
}

/**
 * Whether the readings are arriving. They are the one block on the page fed by
 * an open socket, so they are the one block that may say Live; while the
 * socket is down the figures are the last frame, and the head says so.
 */
function StreamState({ connection }: { connection: ConnectionState }) {
  if (connection === "open") return <Status live tone="running" label="Live" />
  if (connection === "connecting") return <Status tone="notice" label="Connecting…" />
  return <Status tone="warning" label="Reconnecting…" />
}

/** The fleet's own grid, one column short of it at the widest: two rows of three. */
const PROJECT_GRID = "grid gap-3 lg:grid-cols-2 xl:grid-cols-3"

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

function DockerCard({ read }: { read: PollState<Container[]> }) {
  const { data, error, loading } = read
  const running = data?.filter((c) => c.state === "running")
  const products = containerProducts(running ?? [])
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

function DatabasesCard({ read }: { read: PollState<DbConnection[]> }) {
  const { data, error, loading } = read
  // Drawn as what each one is, in the registry's words: the glyph of its
  // flavour where the server has said one, of its driver otherwise.
  const engines = [...new Set((data ?? []).flatMap((conn) => engineFor(conn).logo ?? []))]
  return (
    <ServiceTile
      icon={Database}
      title="Databases"
      href="/databases"
      products={engines}
      loading={loading && !data}
      unavailable={moduleGone(error)}
      value={data ? (data.length === 0 ? "None yet" : `${data.length} connections`) : undefined}
      hint={
        // A saved connection that no longer opens is counted, and said to be one.
        unusableCount(data) > 0
          ? `${unusableCount(data)} cannot be opened`
          : data && data.length === 1
            ? "1 connection"
            : undefined
      }
    />
  )
}

function ProxyCard({ read }: { read: PollState<Certificate[]> }) {
  const { data, error, loading } = read
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

function SecurityCard({ read }: { read: PollState<Exposure> }) {
  const { data, error, loading } = read
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

function PackagesCard({ read, platform }: { read: PollState<UpdateReport>; platform: string }) {
  const { data, error, loading } = read
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

function BackupsCard({ read }: { read: PollState<BackupJob[]> }) {
  const { data, error, loading } = read
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

/**
 * The working copies on this server, drawn as the forges they push to, and
 * what is waiting in them: uncommitted work first, because it is the one
 * thing here a deploy or a restore cannot bring back.
 */
function GitCard({ read }: { read: PollState<{ available: boolean; repos: GitRepo[] }> }) {
  const { data, error, loading } = read
  const repos = data?.repos ?? []
  const dirty = repos.filter((repo) => repo.dirty).length
  const behind = repos.filter((repo) => repo.behind > 0).length
  const conflicted = repos.filter((repo) => repo.conflicts > 0).length
  const forges = [...new Set(repos.flatMap((repo) => hostProduct(repo.remote) ?? []))]
  return (
    <ServiceTile
      icon={GitHubMark}
      title="Git"
      href="/git"
      products={forges}
      loading={loading && !data}
      unavailable={moduleGone(error) || data?.available === false}
      value={
        !data
          ? undefined
          : repos.length === 0
            ? "None yet"
            : conflicted > 0
              ? `${conflicted} in conflict`
              : plural(repos.length, "repository", "repositories")
      }
      tone={conflicted > 0 ? "warning" : "default"}
      hint={
        !data || repos.length === 0
          ? undefined
          : [dirty > 0 ? `${dirty} uncommitted` : "All committed", behind > 0 && `${behind} behind`]
              .filter(Boolean)
              .join(" · ")
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
