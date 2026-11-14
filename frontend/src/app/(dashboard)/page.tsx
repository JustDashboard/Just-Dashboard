"use client"

import { Children, useMemo } from "react"
import Link from "next/link"
import { useRouter } from "next/navigation"
import { ArrowCircleUp, Plus, Puzzle, RotateClockwise, Terminal } from "@/components/icons"
import { get } from "@/lib/api"
import { duration } from "@/lib/format"
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
import { useSelfUpdate } from "@/hooks/use-self-update"
import type { MetricsWindow } from "@/lib/metrics-range"
import type { ConnectionState } from "@/lib/metrics-store"
import { Page, PageContext, Section } from "@/components/page"
import { EmptyState, ErrorState } from "@/components/state"
import { Status } from "@/components/status-dot"
import { useConfirm } from "@/components/confirm-dialog"
import { TopProcesses } from "@/components/metrics/top-processes"
import { lastFleet, readFleet } from "@/components/database/fleet/read"
import { FactDot, HostFact, HostIdentity, platformName } from "@/components/metrics/host-identity"
import { ProjectCard } from "@/components/deploy/fleet-card"
import { sortFleet } from "@/components/deploy/fleet"
import { DashboardHealthNotice } from "@/components/overview/health-notice"
import { UsagePingQuestion } from "@/components/overview/usage-ping-question"
import { ActivityPanel } from "@/components/overview/activity"
import { BackupsPanel, DatabasesPanel } from "@/components/overview/data"
import { ResourceReadings, ResourceReadingsSkeleton } from "@/components/overview/resources"
import { SectionLink } from "@/components/overview/section-link"
import {
  DockerRow,
  GitRow,
  PackagesRow,
  ProxyRow,
  RemoteDesktopRow,
  SecurityRow,
  ServiceList,
  SettingsRow,
  UnitsRow,
} from "@/components/overview/services"
import { SHORTCUT_GLYPH, Shortcuts, counted, type Shortcut } from "@/components/overview/shortcuts"
import { RecentWorkPanel, ShellsPanel, newShellHref } from "@/components/overview/workspace"
import { useUpdateDialog } from "@/components/update/update-dialog"
import { VerbMenu, type Verb } from "@/components/verbs"
import {
  ProductLogos,
  cpuProduct,
  platformProduct,
  virtualizationProduct,
} from "@/components/product-logo"
import { Bone } from "@/components/bone"
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

export default function OverviewPage() {
  const { host, snapshot, error, connection } = useMetrics()
  const recorded = useMetricsHistory(HOUR)
  const events = useMetricEvents(DAY)
  // A section the account keeps out of its sidebar is kept off its Overview
  // too, and so is the read behind its row: the page asks the server only
  // about what it is going to draw.
  const { hidden } = useHiddenSections()
  const shows = (key: SectionKey) => !hidden.has(key)
  const { can } = useAuth()
  const reads = useModuleReads(shows, can)
  const { confirm, dialog } = useConfirm()
  const verbs = useServerVerbs(reads, shows)

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
  // then, so the heading does not change under the reader.
  if (error && !snapshot) {
    return (
      <Page>
        <PageContext title={host?.hostname ?? "Overview"} />
        <ErrorState error={new Error(error)} />
      </Page>
    )
  }

  // Until the socket's first frame the page stands in its own shape: the
  // machine's line and its readings as bones at their heights, and every
  // block under them already drawn and reading for itself, so nothing on the
  // page moves when the frame lands.
  const ready = snapshot && host ? { snapshot, host } : undefined
  const cores = ready ? ready.snapshot.cpu.cores || 1 : 0

  return (
    <Page className="animate-rise">
      <PageContext title={host?.hostname ?? "Overview"} />

      <DashboardHealthNotice />
      <UsagePingQuestion />

      {/* What this machine is, laid out as a project's header: the
          distribution drawn as itself, whether it is answering, then what it
          runs on. Its command is the one thing done to a server from
          anywhere, a shell on it; what the dashboard can do to itself is
          behind the menu. */}
      {ready ? (
        <HostIdentity
          mark={platformProduct(ready.host.platform)}
          title={<span className="min-w-0 truncate">{platformName(ready.host)}</span>}
          state={
            <>
              <ServerState connection={connection} />
              {reads.packages.data?.rebootRequired && (
                <Status tone="warning" label="Restart needed" />
              )}
            </>
          }
          command={
            reads.terminal.enabled && (
              <Button size="sm" asChild>
                <Link href={newShellHref(reads.terminal.data)}>
                  <Terminal className="size-3.5" />
                  Open terminal
                </Link>
              </Button>
            )
          }
          menu={verbs.menu.length > 0 && <VerbMenu verbs={verbs.menu} label="Server actions" />}
          facts={
            <>
              <span className="font-medium text-foreground">{ready.host.hostname}</span>
              <FactDot />
              <span className="font-mono">{ready.host.kernelVersion}</span>
              <FactDot />
              <HostFact product={cpuProduct(ready.host.cpuModel, ready.host.kernelArch)}>
                {ready.host.cpuModel || ready.host.kernelArch}
              </HostFact>
              <FactDot />
              <span className="numeric">{cores} cores</span>
              {ready.host.virtualization && (
                <>
                  <FactDot />
                  <HostFact product={virtualizationProduct(ready.host.virtualization)}>
                    {ready.host.virtualization}
                  </HostFact>
                </>
              )}
              <FactDot />
              <span className="numeric">up {duration(ready.snapshot.uptimeSeconds)}</span>
              <FactDot />
              <span className="numeric">
                {ready.snapshot.procs?.total || ready.host.processes} processes
              </span>
            </>
          }
        />
      ) : (
        <HostIdentity
          logo={<Skeleton className="size-10 rounded-lg" />}
          title={<Bone className="w-56" />}
          state={<Bone className="w-14 text-xs" />}
          // Two lines of facts on a phone, where the machine's own wrap.
          facts={
            <>
              <Bone className="w-64" />
              <Bone className="w-56 sm:w-32" />
            </>
          }
        />
      )}

      <Shortcuts shortcuts={shortcutsFor(reads, shows, can, host?.hostname)} />

      {/* The machine's readings, each a sentence with its last hour, under a
          head that leads to the page that records them. They were tiles; the
          operator asked for none on this page. Whether they are arriving is
          the state on the machine's own line. */}
      <Section title="Resources" actions={<SectionLink href="/metrics">All metrics</SectionLink>}>
        {ready ? (
          <ResourceReadings
            snapshot={ready.snapshot}
            trends={trends}
            recorded={!recorded.disabled}
          />
        ) : (
          <ResourceReadingsSkeleton />
        )}
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
          questions the readings raise — side by side, as /metrics pairs its
          process list with its notable moments. */}
      <Pair>
        <TopProcesses />
        <ActivityPanel events={events} />
      </Pair>

      {/* A row per module, each a sentence about it and the way in.
          Deployments, Databases and Backups have blocks of their own above,
          so the rows are the rest: what Docker and systemd run, the proxy's
          certificates, how the panel is reached, what waits to be updated,
          the checkouts, the remote computers and the dashboard itself. */}
      <Section title="Services">
        <ServiceList>
          {shows("docker") && <DockerRow read={reads.docker} />}
          {shows("proxy") && <ProxyRow read={reads.certificates} />}
          {shows("security") && <SecurityRow read={reads.exposure} />}
          {shows("packages") && (
            <PackagesRow read={reads.packages} platform={host?.platform ?? ""} />
          )}
          <UnitsRow read={reads.units} />
          {shows("git") && <GitRow read={reads.git} />}
          {reads.remote.enabled && <RemoteDesktopRow read={reads.remote} />}
          <SettingsRow />
        </ServiceList>
      </Section>
      {dialog}
      {verbs.dialog}
    </Page>
  )
}

/**
 * Whether the machine is answering, as the state on its own line: the facts
 * beside it and the readings under it arrive on the socket this names, so the
 * dot breathes only while it is open and says so when it is not.
 */
function ServerState({ connection }: { connection: ConnectionState }) {
  if (connection === "open") return <Status live tone="running" label="Online" />
  if (connection === "connecting") return <Status tone="notice" label="Connecting…" />
  return <Status tone="warning" label="Reconnecting…" />
}

/**
 * What can be done to the server from its own line, behind the menu. There is
 * no route that restarts the machine, so Restart is the dashboard's own, and
 * goes to the page that asks before it and follows it after: the same press
 * here would start a restart with nowhere to watch it. Update is the dialog
 * the sidebar's notice opens, offered while there is a version to take.
 */
function useServerVerbs(
  reads: ReturnType<typeof useModuleReads>,
  shows: (key: SectionKey) => boolean,
) {
  const router = useRouter()
  const { can } = useAuth()
  const { report } = useSelfUpdate()
  const { openUpdate, dialog } = useUpdateDialog()
  const admin = can("system.admin")
  const running = report?.run?.status === "running" || report?.run?.status === "pending"
  const waiting = reads.packages.data?.packages.length ?? 0
  const menu: (Verb | false | undefined)[] = [
    admin &&
      report?.available &&
      report.install.supported &&
      !running && {
        key: "update",
        label: `Update to ${report.latest}`,
        icon: ArrowCircleUp,
        run: openUpdate,
      },
    shows("packages") &&
      waiting > 0 && {
        key: "packages",
        label: `Update ${waiting === 1 ? "1 package" : `${waiting} packages`}`,
        icon: Puzzle,
        run: () => router.push("/packages"),
      },
    admin && {
      key: "restart",
      label: "Restart the dashboard",
      icon: RotateClockwise,
      run: () => router.push("/dashboard/configuration"),
    },
  ]
  return { menu: menu.filter((verb): verb is Verb => Boolean(verb)), dialog }
}

/**
 * Every module's own read, once, at the page: the blocks and rows draw them,
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
      { enabled: shows("deploy"), key: "/deploy/traffic" },
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
  hostname: string | undefined,
): Shortcut[] {
  const list: (Shortcut | false)[] = [
    shows("deploy") &&
      can("system.admin") && {
        key: "deploy",
        label: "New project",
        detail: counted(reads.fleet.data?.deployments.length, "project"),
        fallback: SHORTCUT_GLYPH.deploy,
        verb: "new",
        href: "/deploy/new",
      },
    reads.terminal.enabled && {
      key: "terminal",
      label: "New terminal",
      // The count first: the account it signs in as is the part a narrow
      // card can lose.
      detail: reads.terminal.data
        ? reads.terminal.data.sessions.length > 0
          ? `${reads.terminal.data.sessions.length} open · ${reads.terminal.data.login.user}`
          : `as ${reads.terminal.data.login.user}`
        : undefined,
      product: "terminal",
      verb: "new",
      href: newShellHref(reads.terminal.data),
    },
    reads.drawings.enabled &&
      can("service.control") && {
        key: "drawing",
        label: "New drawing",
        detail: counted(reads.drawings.data?.length, "drawing"),
        product: "excalidraw",
        verb: "new",
        create: "drawing",
      },
    shows("files") && {
      key: "files",
      label: "Browse files",
      detail: hostname,
      fallback: SHORTCUT_GLYPH.files,
      verb: "open",
      href: "/files",
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
