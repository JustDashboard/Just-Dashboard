"use client"

import { Children, useMemo } from "react"
import Link from "next/link"
import { Plus } from "@/components/icons"
import { get } from "@/lib/api"
import { bytes, duration, percent, rate } from "@/lib/format"
import type { BoardSummary } from "@/lib/boards"
import type { RdOverview } from "@/lib/remote-desktop"
import type {
  BackupJob,
  Certificate,
  Container,
  DbFleet,
  DeploymentFleet,
  Exposure,
  GitRepo,
  SystemdList,
  TerminalList,
  TrafficPulse,
  UpdateReport,
} from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import { useHiddenSections } from "@/hooks/use-hidden-sections"
import { useMediaQuery } from "@/hooks/use-mobile"
import type { SectionKey } from "@/lib/sidebar-sections"
import { type PollState, usePoll } from "@/hooks/use-poll"
import { useMetrics } from "@/hooks/use-metrics"
import { useMetricEvents, useMetricsHistory } from "@/hooks/use-metrics-history"
import type { MetricsWindow } from "@/lib/metrics-range"
import { Page, PageContext, PageState, Section } from "@/components/page"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { utilisationTone } from "@/components/meter"
import { EmptyState } from "@/components/state"
import { useConfirm } from "@/components/confirm-dialog"
import { TopProcesses } from "@/components/metrics/top-processes"
import { TileTrend } from "@/components/metrics/sparkline"
import { lastFleet, readFleet } from "@/components/database/fleet/read"
import { FactDot, HostFact, HostIdentity, platformName } from "@/components/metrics/host-identity"
import { ProjectCard } from "@/components/deploy/fleet-card"
import { sortFleet } from "@/components/deploy/fleet"
import { ActivityPanel } from "@/components/overview/activity"
import { BackupsPanel, DatabasesPanel } from "@/components/overview/data"
import {
  CoreBars,
  HUE,
  LiveBytes,
  LiveFigure,
  SeriesKey,
  StreamState,
} from "@/components/overview/readings"
import { SectionLink } from "@/components/overview/section-link"
import {
  DockerTile,
  GitTile,
  PackagesTile,
  ProxyTile,
  RemoteDesktopTile,
  SecurityTile,
  SettingsTile,
  UnitsTile,
} from "@/components/overview/services"
import { SHORTCUT_GLYPH, Shortcuts, counted, type Shortcut } from "@/components/overview/shortcuts"
import { StorageBand } from "@/components/overview/storage"
import { RecentWorkPanel, ShellsPanel, newShellHref } from "@/components/overview/workspace"
import {
  ProductLogos,
  cpuProduct,
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

/**
 * Two rows of three: the projects that need the reader most, then the way to
 * the rest. One column on a phone, where six cards were a screen and a half
 * before anything else on the page, so it keeps the worst three.
 */
const SHOWN_PROJECTS = 6
const SHOWN_PROJECTS_NARROW = 3

/** Each reading's line, and the key before its name that says which line is whose. */
const SERIES = HUE

export default function OverviewPage() {
  const { host, snapshot, error, connection } = useMetrics()
  const recorded = useMetricsHistory(HOUR)
  const events = useMetricEvents(DAY)
  // A section the account keeps out of its sidebar is kept off its Overview
  // too, and so is the read behind its tile: the page asks the server only
  // about what it is going to draw.
  const { hidden } = useHiddenSections()
  const shows = (key: SectionKey) => !hidden.has(key)
  const { can } = useAuth()
  const reads = useModuleReads(shows, can)
  const { confirm, dialog } = useConfirm()

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
      />

      <Shortcuts shortcuts={shortcutsFor(reads, shows, can, host.hostname)} />

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
          <StatGrid columns={4} dense>
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

      {shows("deploy") && (
        <DeploymentsSection fleet={reads.fleet} traffic={reads.traffic.data} confirm={confirm} />
      )}

      {/* What was being worked on — the shells left open, the last things
          drawn — so a visit can go straight back to it. */}
      <Pair>
        {reads.terminal.enabled && <ShellsPanel read={reads.terminal} />}
        {reads.drawings.enabled && <RecentWorkPanel drawings={reads.drawings} />}
      </Pair>

      {/* The data on the machine and the copies of it, side by side: a
          database's row says when it answered, a job's when it last ran. */}
      <Pair>
        {shows("databases") && <DatabasesPanel read={reads.databases} />}
        {shows("backups") && <BackupsPanel read={reads.backups} />}
      </Pair>

      {/* Who is spending the machine and what changed on it — the two
          questions the tiles raise — side by side, as /metrics pairs its
          process list with its notable moments. */}
      <Pair>
        <TopProcesses />
        <ActivityPanel events={events} />
      </Pair>

      {/* The same run of readings as the tiles at the top, one per module,
          because a module's headline figure *is* a reading. Each says what
          it counts with the products themselves — the images running, the
          forges pushed to — after its words. Deployments, Databases and
          Backups have blocks of their own above, so their tiles went to the
          services systemd runs and the remote computers. Two to a row on a
          phone, where eight full-width tiles were a screen of their own. */}
      <Section title="Services">
        <StatGrid columns={4} dense>
          {shows("docker") && <DockerTile read={reads.docker} />}
          {shows("proxy") && <ProxyTile read={reads.certificates} />}
          {shows("security") && <SecurityTile read={reads.exposure} />}
          {shows("packages") && <PackagesTile read={reads.packages} platform={host.platform} />}
          <UnitsTile read={reads.units} />
          {shows("git") && <GitTile read={reads.git} />}
          {reads.remote.enabled && <RemoteDesktopTile read={reads.remote} />}
          <SettingsTile />
        </StatGrid>
      </Section>
      {dialog}
    </Page>
  )
}

/**
 * Every module's own read, once, at the page: the blocks and tiles draw them,
 * and the shortcuts count what each would add to. A read whose section is
 * hidden is never made, and neither is one the account may not make — the
 * terminal's listing needs the terminal, and the remote computers an
 * administrator — so `enabled` says whether there is a block to draw at all.
 */
function useModuleReads(
  shows: (key: SectionKey) => boolean,
  can: ReturnType<typeof useAuth>["can"],
) {
  const terminal = shows("terminal") && can("terminal")
  const remote = shows("remote-desktop") && can("system.admin")
  return {
    docker: usePoll<Container[]>(
      (signal) => get<Container[]>("/docker/containers/", undefined, signal),
      60_000,
      [],
      { enabled: shows("docker") },
    ),
    // The control center's own read, and its last answer drawn at once
    // while this one is asked: a database's state, size and version are
    // what a row says, which the saved connections alone could not.
    databases: usePoll<DbFleet>(readFleet, 60_000, [], {
      enabled: shows("databases"),
      seed: lastFleet(),
    }),
    certificates: usePoll<Certificate[]>(
      (signal) => get<Certificate[]>("/certificates/", undefined, signal),
      300_000,
      [],
      { enabled: shows("proxy") },
    ),
    exposure: usePoll<Exposure>(
      (signal) => get<Exposure>("/exposure", undefined, signal),
      60_000,
      [],
      { enabled: shows("security") },
    ),
    packages: usePoll<UpdateReport>(
      (signal) => get<UpdateReport>("/packages/updates", undefined, signal),
      300_000,
      [],
      { enabled: shows("packages") },
    ),
    // The fleet's own cadence is five seconds; a glance can take ten, which
    // still moves a run's release path while the reader watches.
    fleet: usePoll<DeploymentFleet>(
      (signal) => get<DeploymentFleet>("/deploy/", { view: "fleet" }, signal),
      10_000,
      [],
      { enabled: shows("deploy") },
    ),
    traffic: usePoll<Record<string, TrafficPulse>>(
      (signal) => get<Record<string, TrafficPulse>>("/deploy/traffic", undefined, signal),
      60_000,
      [],
      { enabled: shows("deploy") },
    ),
    backups: usePoll<BackupJob[]>(
      (signal) => get<BackupJob[]>("/backups/", undefined, signal),
      120_000,
      [],
      { enabled: shows("backups") },
    ),
    git: usePoll<{ available: boolean; repos: GitRepo[] }>(
      (signal) => get<{ available: boolean; repos: GitRepo[] }>("/git/", undefined, signal),
      120_000,
      [],
      { enabled: shows("git") },
    ),
    units: usePoll<SystemdList>(
      (signal) => get<SystemdList>("/systemd/", undefined, signal),
      60_000,
    ),
    // Fifteen seconds, the terminal page's own cadence tripled: whether a
    // shell is working is the one thing in its row that changes by itself.
    terminal: {
      ...usePoll<TerminalList>(
        (signal) => get<TerminalList>("/terminal/", undefined, signal),
        15_000,
        [],
        { enabled: terminal },
      ),
      enabled: terminal,
    },
    drawings: {
      ...usePoll<BoardSummary[]>(
        (signal) => get<BoardSummary[]>("/boards/", undefined, signal),
        120_000,
        [],
        { enabled: shows("excalidraw") },
      ),
      enabled: shows("excalidraw"),
    },
    remote: {
      ...usePoll<RdOverview>(
        (signal) => get<RdOverview>("/remote-desktop/", undefined, signal),
        120_000,
        [],
        { enabled: remote },
      ),
      enabled: remote,
    },
  }
}

/**
 * The shortcuts this account can take, in the sidebar's order, each counting
 * what it would add to. Only what the account may do and keeps in its
 * sidebar: a shortcut that opens a refusal is a dead button.
 */
function shortcutsFor(
  reads: ReturnType<typeof useModuleReads>,
  shows: (key: SectionKey) => boolean,
  can: ReturnType<typeof useAuth>["can"],
  hostname: string,
): Shortcut[] {
  const admin = can("system.admin")
  const write = can("service.control")
  const list: (Shortcut | false)[] = [
    shows("deploy") &&
      admin && {
        key: "deploy",
        label: "New project",
        detail: counted(reads.fleet.data?.deployments.length, "project"),
        fallback: SHORTCUT_GLYPH.deploy,
        href: "/deploy/new",
      },
    shows("databases") &&
      admin && {
        key: "database",
        label: "Add a database",
        detail: counted(reads.databases.data?.connections.length, "database"),
        fallback: SHORTCUT_GLYPH.database,
        href: "/databases/new",
      },
    reads.terminal.enabled && {
      key: "shell",
      label: "New shell",
      // The count first: the account it signs in as is the part a narrow
      // card can lose.
      detail: reads.terminal.data
        ? reads.terminal.data.sessions.length > 0
          ? `${reads.terminal.data.sessions.length} open · ${reads.terminal.data.login.user}`
          : `as ${reads.terminal.data.login.user}`
        : undefined,
      product: "terminal",
      href: newShellHref(reads.terminal.data),
    },
    reads.drawings.enabled &&
      write && {
        key: "drawing",
        label: "New drawing",
        detail: counted(reads.drawings.data?.length, "drawing"),
        product: "excalidraw",
        create: "drawing",
      },
    shows("files") && {
      key: "files",
      label: "Browse files",
      detail: hostname,
      fallback: SHORTCUT_GLYPH.files,
      href: "/files",
    },
    reads.remote.enabled && {
      key: "computer",
      label: "Add a computer",
      detail: counted(reads.remote.data?.computers.length, "computer"),
      fallback: SHORTCUT_GLYPH.computer,
      href: "/remote-desktop/new",
    },
    shows("backups") &&
      admin && {
        key: "backup",
        label: "Back up",
        detail: counted(reads.backups.data?.length, "job"),
        fallback: SHORTCUT_GLYPH.backup,
        href: "/backups",
      },
  ]
  return list.filter((shortcut): shortcut is Shortcut => Boolean(shortcut))
}

/**
 * Two plain blocks side by side from `lg`, their heads on one hairline; one
 * alone takes the row, where half of it beside nothing read as a missing twin.
 */
function Pair({ children }: { children: React.ReactNode }) {
  const blocks = Children.toArray(children)
  if (blocks.length === 0) return null
  return (
    <div
      className={cn("grid items-start gap-8 [&>*]:min-w-0", blocks.length > 1 && "lg:grid-cols-2")}
    >
      {blocks}
    </div>
  )
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
  const wide = useMediaQuery("(min-width: 1024px)")
  const shown = deployments.slice(0, wide ? SHOWN_PROJECTS : SHOWN_PROJECTS_NARROW)

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

/** The fleet's own grid, one column short of it at the widest: two rows of three. */
const PROJECT_GRID = "grid gap-3 lg:grid-cols-2 xl:grid-cols-3"
