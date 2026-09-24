"use client"

import { useCallback, useMemo, useState } from "react"
import Link from "next/link"
import { useRouter, useSearchParams } from "next/navigation"
import {
  Archive,
  Bell,
  Box,
  ChevronDown,
  CloudUpload,
  Cross,
  Database,
  GitHubMark,
  GridMasonry,
  GridSquare,
  Key,
  Layers,
  ListUnordered,
  MagnifyingGlass,
  Plus,
  StopCircle,
} from "@/components/icons"
import { get, post } from "@/lib/api"
import { percent, plural } from "@/lib/format"
import { perMinute } from "@/lib/requests"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"
import { useAuth } from "@/hooks/use-auth"
import { useMediaQuery } from "@/hooks/use-mobile"
import { usePoll } from "@/hooks/use-poll"
import { useSessionState, useViewState } from "@/lib/view-state"
import type {
  ArchivedDeployment,
  DeploymentActiveWork,
  DeploymentFleet,
  DeploymentSummary,
  TrafficPulse,
} from "@/lib/types"
import { useConfirm } from "@/components/confirm-dialog"
import { ChoiceCard, ChoiceGrid } from "@/components/choice-card"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import { FindingList } from "@/components/finding-list"
import { IconAction } from "@/components/icon-action"
import { TileTrend } from "@/components/metrics/sparkline"
import { Page, PageHeader, SearchInput, Toolbar } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ProductGlyphs, ProductLogo, ProductLogos } from "@/components/product-logo"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { EmptyState, ErrorState } from "@/components/state"
import { ChipCount, ChipStrip, FilterChip } from "@/components/tabs"
import { VerbMenu, type Verb } from "@/components/verbs"
import { Button } from "@/components/ui/button"
import { NumberTicker } from "@/components/ui/number-ticker"
import { Skeleton } from "@/components/ui/skeleton"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { ArchivedProjects } from "@/components/deploy/archived-projects"
import {
  FailingShare,
  ProjectCard,
  ProjectRow,
  runClock,
  stageOf,
} from "@/components/deploy/fleet-card"
import { ProjectMark } from "@/components/deploy/project-mark"
import { MiniReleasePath } from "@/components/deploy/run-pipeline"
import { RunActorMark } from "@/components/deploy/run-marks"
import {
  DATABASE_ENGINE_LABELS,
  isCancellable,
  runCommit,
  runTitle,
  runTriggerLine,
  useNow,
} from "@/components/deploy/vocabulary"
import {
  FAILING_NOTICE,
  FLEET_FILTERS,
  fleetAttention,
  fleetCounts,
  fleetHaystack,
  fleetLive,
  fleetTraffic,
  failingTone,
  matchesFilter,
  sortFleet,
  type FleetFilter,
} from "@/components/deploy/fleet"

/**
 * The fleet: every project, what it is, and what is happening to it now.
 *
 * `?view=archived` switches the same route to the archived list — a query
 * flag rather than a second page, because it is the same destination with the
 * same header actions one click away.
 */
export function ProjectsPage() {
  const archived = useSearchParams().get("view") === "archived"
  if (archived) return <ArchivedProjects />
  return <Fleet />
}

/**
 * The card grid, shared by the fleet and its placeholder so one never jumps
 * into the other. Two columns wait for `lg`: from `md` the sidebar takes 248px
 * of the window, and two cards beside it left each name about 70px.
 */
const GRID = "grid gap-3 lg:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-4"

const READINGS = ["Live", "Requests", "Failing requests", "Build slots"]

/**
 * The fleet before its first answer, in the shape it arrives in: four
 * readings, each a hint line tall and the Requests one carrying its trend,
 * then the toolbar's search, then three cards, or three rows in the list
 * layout. The route's Suspense fallback draws the same thing under the same
 * header.
 */
export function FleetSkeleton({ layout = "grid" }: { layout?: "grid" | "list" }) {
  return (
    <>
      <StatGrid columns={4} dense>
        {READINGS.map((label) => (
          <StatTile
            key={label}
            label={label}
            value={<Skeleton className="h-7 w-12" />}
            trend={label === "Requests" && <Skeleton className="h-9 w-full" />}
            // A line's height and nothing in it: the hint is a paragraph,
            // which a block placeholder may not sit inside.
            hint={"\u00a0"}
          />
        ))}
      </StatGrid>
      <Skeleton aria-hidden className="h-10 w-full rounded-md sm:h-8 sm:w-72" />
      {layout === "grid" ? (
        <ul aria-hidden className={GRID}>
          {[0, 1, 2].map((index) => (
            <li key={index}>
              <Skeleton className="h-48 rounded-xl" />
            </li>
          ))}
        </ul>
      ) : (
        <ChoiceList aria-hidden>
          {[0, 1, 2].map((index) => (
            <li key={index}>
              <Skeleton className="h-14 rounded-xl" />
            </li>
          ))}
        </ChoiceList>
      )}
    </>
  )
}

/**
 * The page reads top to bottom as the question an operator brings to it. The
 * four readings say how the fleet is doing as a whole; the runs in flight are
 * the thing moving; the Attention list names what somebody has to act on, in
 * the engine's own words; and the projects follow, worst first, each drawn as
 * the product it is. It used to open on the cards with the one live figure a
 * 48px line inside a wrapping sentence and the build capacity in 11px beside
 * a heading — the failure §16 describes by name.
 */
function Fleet() {
  const { can } = useAuth()
  const router = useRouter()
  const { confirm, dialog } = useConfirm()
  const admin = can("system.admin")
  // The header's shape, and the list rows', chosen once rather than drawn
  // twice and hidden: the tests and a screen reader find each link by its
  // name, and a hidden twin is a second answer to every query (§12). A run
  // in flight lays its path, stage and clock beside its name only from `lg`,
  // where the sidebar leaves the name room to be read.
  const roomy = useMediaQuery("(min-width: 640px)")
  const wideRuns = useMediaQuery("(min-width: 1024px)")
  const wide = useMediaQuery("(min-width: 1280px)")
  const fleet = usePoll(
    (signal) => get<DeploymentFleet>("/deploy/", { view: "fleet" }, signal),
    5000,
  )
  // Every project's last hour in one answer, cached to the minute per route
  // on the server, so a fleet of forty is one read rather than forty.
  const pulse = usePoll(
    (signal) => get<Record<string, TrafficPulse>>("/deploy/traffic", undefined, signal),
    30000,
  )
  // Read once for the count beside Archived, and again when a card archives
  // its project: the archive does not otherwise change under the reader, and
  // each row costs the server a history read.
  const archive = usePoll(
    (signal) => get<ArchivedDeployment[]>("/deploy/", { view: "archived" }, signal),
    0,
  )
  const { refresh: refreshFleet } = fleet
  const { refresh: refreshArchive } = archive
  const refresh = useCallback(() => {
    refreshFleet()
    refreshArchive()
  }, [refreshFleet, refreshArchive])
  // The question is kept for the tab and the furniture for good, so a
  // filtered fleet does not reset itself on the way back from a project.
  const [query, setQuery] = useSessionState("deploy.fleet.query", "")
  const [filter, setFilter] = useSessionState<FleetFilter>("deploy.fleet.filter", "all")
  const [layout, setLayout] = useViewState<"grid" | "list">("deploy.fleet.layout", "grid")

  const deployments = useMemo(() => fleet.data?.deployments ?? [], [fleet.data])
  const pulses = pulse.data
  const counts = useMemo(() => fleetCounts(deployments, pulses), [deployments, pulses])
  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase()
    return sortFleet(
      deployments.filter(
        (deployment) =>
          (!q || fleetHaystack(deployment).includes(q)) &&
          matchesFilter(deployment, filter, pulses),
      ),
    )
  }, [deployments, filter, pulses, query])
  const findings = useMemo(
    () =>
      fleetAttention(deployments, pulses).map((finding) => ({
        ...finding,
        action: { label: finding.action.label, onClick: () => router.push(finding.action.href) },
      })),
    [deployments, pulses, router],
  )
  const workFor = (deployment: DeploymentSummary) =>
    fleet.data?.activeWork.find((item) => item.run.id === deployment.activeRun?.id)
  const narrowed = query.trim() !== "" || filter !== "all"
  const archivedCount = archive.data?.length ?? 0
  const empty = Boolean(fleet.data) && deployments.length === 0

  // Tracked per run id, not a single flag: more than one deployment can be
  // in progress at once, and a click on one row must not disable another.
  const [cancelling, setCancelling] = useState<Set<number>>(new Set())
  const cancel = async (item: DeploymentActiveWork) => {
    setCancelling((current) => new Set(current).add(item.run.id))
    try {
      await post(`/deploy/${item.run.projectId}/runs/${item.run.id}/cancel`, {})
      notify.success(`Cancelling ${item.projectName}`, {
        description: "The run page will show cleanup progress.",
      })
      fleet.refresh()
    } catch (error) {
      notify.error("Could not cancel deployment", error)
    } finally {
      setCancelling((current) => {
        const next = new Set(current)
        next.delete(item.run.id)
        return next
      })
    }
  }

  // Behind a menu on a phone, where three links and the command broke into
  // two ragged rows; each page gets its word and a line (§13).
  const pages: Verb[] = [
    {
      key: "notifications",
      label: "Notifications",
      detail: "Where run outcomes are sent: Discord, Slack, Telegram, e-mail or a webhook.",
      icon: Bell,
      run: () => router.push("/deploy/notifications"),
    },
    // Reading the list itself needs system.admin (the routes seal every
    // credential route alike), so it is offered only where the destination
    // would not just refuse it.
    ...(admin
      ? [
          {
            key: "credentials",
            label: "Credentials",
            detail: "Git tokens, SSH keys and registry logins the projects use.",
            icon: Key,
            run: () => router.push("/deploy/credentials"),
          },
        ]
      : []),
    {
      key: "archived",
      label: "Archived",
      detail:
        archivedCount > 0
          ? `${plural(archivedCount, "project")} taken out of the fleet. Their history is kept.`
          : "Projects taken out of the fleet, with their history.",
      icon: Archive,
      run: () => router.push("/deploy?view=archived"),
    },
  ]

  const layoutControls = (
    <div className="ml-auto flex shrink-0 items-center gap-2">
      {narrowed && (
        <span className="numeric text-hint text-muted-foreground">
          {filtered.length} of {deployments.length}
        </span>
      )}
      <div className="flex items-center gap-0.5" role="group" aria-label="Deployment layout">
        <Button
          variant="ghost"
          size="icon-sm"
          aria-label="Grid view"
          aria-pressed={layout === "grid"}
          className={cn(layout === "grid" && "bg-accent text-accent-foreground")}
          onClick={() => setLayout("grid")}
        >
          <GridSquare className="size-4" />
        </Button>
        <Button
          variant="ghost"
          size="icon-sm"
          aria-label="List view"
          aria-pressed={layout === "list"}
          className={cn(layout === "list" && "bg-accent text-accent-foreground")}
          onClick={() => setLayout("list")}
        >
          <ListUnordered className="size-4" />
        </Button>
      </div>
    </div>
  )

  return (
    <Page>
      <PageHeader
        eyebrow="Apps"
        title="Deployments"
        actions={
          <>
            {roomy ? (
              <>
                <Button variant="ghost" size="sm" asChild>
                  <Link href="/deploy/notifications">Notifications</Link>
                </Button>
                {admin && (
                  <Button variant="ghost" size="sm" asChild>
                    <Link href="/deploy/credentials">Credentials</Link>
                  </Button>
                )}
                <Button variant="ghost" size="sm" asChild>
                  {/* Named without its count, so the link is "Archived"
                      whatever is in it. */}
                  <Link href="/deploy?view=archived" aria-label="Archived">
                    Archived
                    {archivedCount > 0 && (
                      <span aria-hidden className="numeric text-micro text-muted-foreground">
                        {archivedCount}
                      </span>
                    )}
                  </Link>
                </Button>
              </>
            ) : (
              // Opens from its own left edge: the trigger sits on the page's
              // gutter, and a menu hung from its right end ran off the screen.
              <VerbMenu
                align="start"
                verbs={pages}
                trigger={
                  <Button variant="outline" size="sm">
                    Pages
                    <ChevronDown className="size-3.5" />
                  </Button>
                }
              />
            )}
            {/* An empty fleet's own state offers the same command, and a
                surface has one (§3). */}
            {admin && !empty && (
              <Button size="sm" asChild>
                <Link href="/deploy/new">
                  <Plus className="size-3.5" />
                  New project
                </Link>
              </Button>
            )}
          </>
        }
      />

      {fleet.loading && !fleet.data && <FleetSkeleton layout={layout} />}
      {fleet.error && !fleet.data && <ErrorState error={fleet.error} onRetry={fleet.refresh} />}

      {/* Everything under the header rises once, when the first snapshot
          lands (§11), with the page's own rhythm between its blocks. */}
      {fleet.data && (
        <div className="flex min-w-0 animate-rise flex-col gap-6 md:gap-8">
          {deployments.length > 0 && (
            <FleetReadings
              fleet={fleet.data}
              pulses={pulses}
              settled={!pulse.loading}
              failed={Boolean(pulse.error && !pulse.data)}
            />
          )}

          {fleet.data.activeWork.length > 0 && (
            <InProgressPanel
              work={fleet.data.activeWork}
              deployments={deployments}
              roomy={wideRuns}
              canCancel={can("service.control")}
              cancelling={cancelling}
              onCancel={cancel}
            />
          )}

          {/* Only what somebody has to act on, with the engine's own reason.
              The Attention chip below narrows the cards to the same projects. */}
          {findings.length > 0 && (
            <Panel plain>
              <PanelHeader title="Attention" />
              <PanelBody className="py-0">
                <FindingList findings={findings} emptyLabel="" />
              </PanelBody>
            </Panel>
          )}

          {empty && <FleetEmpty admin={admin} />}

          {deployments.length > 0 && (
            <div className="flex min-w-0 flex-col gap-4">
              {/* Below xl the search and the layout share the first line and
                  the chips take the whole of the second, scrolling on a phone
                  to the screen's edge, where a chip cut in half says there
                  is more; beside a fixed search they were squeezed into a
                  column at 768 and cut off under the toggles at 390. */}
              <Toolbar className="justify-between gap-x-4">
                <div className="flex w-full min-w-0 items-center gap-2 xl:w-auto">
                  <SearchInput
                    value={query}
                    onChange={(event) => setQuery(event.target.value)}
                    placeholder="Search deployments"
                    aria-label="Search deployments"
                    containerClassName="max-sm:flex-1"
                    className={cn(query && "pr-8")}
                    trailing={
                      query && (
                        <IconAction
                          label="Clear search"
                          className="size-6"
                          onClick={() => setQuery("")}
                        >
                          <Cross className="size-3" />
                        </IconAction>
                      )
                    }
                  />
                  {!wide && layoutControls}
                </div>
                {/* A state nothing is in gets no chip — a filter that can only
                    return nothing is furniture — unless it is the one chosen,
                    which must stay visible to be unchosen. The counts are the
                    fleet's, so a chip says what is waiting before it is
                    pressed. */}
                <ChipStrip className="grow basis-full xl:basis-0">
                  {FLEET_FILTERS.map(({ key, label }) =>
                    key === "all" || counts[key] > 0 || filter === key ? (
                      <FilterChip
                        key={key}
                        selected={filter === key}
                        onClick={() => setFilter(key)}
                        className={cn(
                          counts[key] > 0 &&
                            key === "failed" &&
                            "text-destructive hover:text-destructive",
                          counts[key] > 0 &&
                            key === "attention" &&
                            "text-warning hover:text-warning",
                        )}
                      >
                        {label}
                        <ChipCount>{counts[key]}</ChipCount>
                      </FilterChip>
                    ) : null,
                  )}
                </ChipStrip>
                {wide && layoutControls}
              </Toolbar>

              {filtered.length === 0 ? (
                <EmptyState
                  icon={MagnifyingGlass}
                  title="No deployments match"
                  description="Clear the search or pick another chip."
                  action={
                    <Button
                      size="sm"
                      variant="outline"
                      onClick={() => {
                        setQuery("")
                        setFilter("all")
                      }}
                    >
                      Clear filters
                    </Button>
                  }
                />
              ) : layout === "grid" ? (
                <ul aria-label="Deployment projects" className={GRID}>
                  {filtered.map((deployment, index) => (
                    <ProjectCard
                      key={deployment.id}
                      index={index}
                      deployment={deployment}
                      pulse={pulses?.[String(deployment.id)]}
                      work={workFor(deployment)}
                      confirm={confirm}
                      refresh={refresh}
                    />
                  ))}
                </ul>
              ) : (
                <ChoiceList aria-label="Deployment projects" className="animate-rise">
                  {filtered.map((deployment) => (
                    <ProjectRow
                      key={deployment.id}
                      wide={wide}
                      roomy={roomy}
                      deployment={deployment}
                      pulse={pulses?.[String(deployment.id)]}
                      work={workFor(deployment)}
                      confirm={confirm}
                      refresh={refresh}
                    />
                  ))}
                </ChoiceList>
              )}
            </div>
          )}
        </div>
      )}
      {dialog}
    </Page>
  )
}

/**
 * The fleet in four figures, each one a thing the cards cannot say at a
 * glance: how much of it is serving (and as what), the traffic it is taking,
 * what share of that fails, and how busy the builders are. The per-state
 * counts are the chips' — a figure here repeating a chip's count would be the
 * same number twice.
 */
function FleetReadings({
  fleet,
  pulses,
  settled,
  failed,
}: {
  fleet: DeploymentFleet
  pulses: Record<string, TrafficPulse> | undefined
  /** Whether the traffic read has answered, so its figures rise when it does. */
  settled: boolean
  /** The traffic read failed, so its dashes are unknowns rather than a quiet hour. */
  failed: boolean
}) {
  const { deployments, activeWork, slots } = fleet
  const live = fleetLive(deployments)
  const traffic = fleetTraffic(deployments, pulses)
  const queued = activeWork.filter((item) => item.queuePosition).length
  const resting = [
    live.stopped > 0 && `${live.stopped} stopped`,
    live.notDeployed > 0 && `${live.notDeployed} not deployed`,
  ]
    .filter(Boolean)
    .join(" · ")
  const share = traffic?.share ?? 0
  const pending = <Skeleton className="h-7 w-12" />
  // Swapped by key between placeholder and figure, so the figure rises once
  // when the read it waits for lands (§11).
  const arrive = (value: React.ReactNode) =>
    settled ? (
      <span key="figure" className="inline-block animate-rise">
        {value}
      </span>
    ) : (
      <span key="skeleton">{pending}</span>
    )

  return (
    <StatGrid columns={4} dense>
      <StatTile
        label="Live"
        value={<NumberTicker value={live.serving} />}
        trailing={`of ${deployments.length}`}
        hint={
          <span className="inline-flex max-w-full min-w-0 items-center gap-2">
            {resting && <span className="truncate">{resting}</span>}
            <ProductGlyphs ids={live.products} />
          </span>
        }
      />
      <StatTile
        label="Requests"
        value={arrive(traffic ? perMinute(traffic.perMinute) : "—")}
        trailing={traffic && "/min"}
        trend={
          traffic && (
            <TileTrend
              values={traffic.points}
              label="Requests per minute across the fleet, last hour"
            />
          )
        }
        hint={
          traffic
            ? `${traffic.pages.toLocaleString()} views · ${plural(traffic.sites, "site")}`
            : failed
              ? "traffic could not be read"
              : settled
                ? "no routed traffic in the last hour"
                : "reading the last hour"
        }
      />
      <StatTile
        label="Failing requests"
        value={arrive(traffic ? percent(share * 100, 1) : "—")}
        tone={failingTone(share)}
        hint={
          !traffic ? (
            !failed && "last hour"
          ) : traffic.failingSites > 0 ? (
            // The Attention list names each of these, with its share.
            `${plural(traffic.failingSites, "site")} failing · last hour`
          ) : traffic.worst.errorRate >= FAILING_NOTICE ? (
            // The site behind the share, and its own, in the colour of how
            // bad it is: a fleet at 0.6% can still hold one site at 3%, and
            // nothing else on the page names a site under 5%.
            <>
              {traffic.worst.name} <FailingShare rate={traffic.worst.errorRate} />
            </>
          ) : (
            "last hour"
          )
        }
      />
      <StatTile
        label="Build slots"
        value={<NumberTicker value={slots.heavyUsed} />}
        trailing={`of ${slots.heavyCapacity} building`}
        meter={slots.heavyCapacity > 0 ? (slots.heavyUsed / slots.heavyCapacity) * 100 : 0}
        tone={queued > 0 ? "warning" : "default"}
        hint={`${queued} queued · control ${slots.lightUsed}/${slots.lightCapacity}`}
      />
    </StatGrid>
  )
}

/**
 * Runs in flight, under the readings and above everything else: the one thing
 * on the page moving under the reader, so it comes first and takes no frame.
 * Each is a destination — the run's own page — drawn as the project it is
 * releasing, with the release path sweeping and the stage it is at lit, which
 * is how its page draws it too.
 */
function InProgressPanel({
  work,
  deployments,
  roomy,
  canCancel,
  cancelling,
  onCancel,
}: {
  work: DeploymentActiveWork[]
  deployments: DeploymentSummary[]
  roomy: boolean
  canCancel: boolean
  cancelling: Set<number>
  onCancel: (item: DeploymentActiveWork) => void
}) {
  const now = useNow(1000)
  const queued = work.filter((item) => item.queuePosition).length
  return (
    <Panel plain aria-labelledby="in-progress-title">
      <PanelHeader
        title={<span id="in-progress-title">In progress</span>}
        actions={
          <span className="numeric text-hint text-muted-foreground">
            {work.length - queued} running{queued > 0 && ` · ${queued} queued`}
          </span>
        }
      />
      {/* The live region announces the count, not the list: the list's own
          elapsed-time column re-renders every second, and a screen reader
          tied to that read every row back once a second. */}
      <span className="sr-only" aria-live="polite" aria-atomic="true">
        {plural(work.length, "deployment")} in progress
      </span>
      <PanelBody flush className="pt-3">
        <ChoiceList>
          {work.map((item) => {
            const { run } = item
            const busy = cancelling.has(run.id)
            const summary = deployments.find((deployment) => deployment.id === run.projectId)
            const commit = runCommit(run)
            const stage = stageOf(run, item)
            const elapsed = (
              <span className="numeric w-16 shrink-0 text-right text-hint text-muted-foreground">
                {runClock(run, item, now)}
              </span>
            )
            const path = (
              <MiniReleasePath currentStep={item.currentStep} currentStatus={item.currentStatus} />
            )
            // Who started it and what it is building: beside the run's name
            // where there is room, under the stage on a phone. The starter is
            // named, because a face and then a subject is how every card
            // below draws a commit and its author.
            const starter = (
              <>
                <RunActorMark run={run} remote={summary?.sourceRemote} size="xs" />
                <span className="shrink-0">{runTriggerLine(run)}</span>
                {commit?.subject && (
                  <span className="min-w-0 truncate text-foreground/80">· {commit.subject}</span>
                )}
              </>
            )
            return (
              <ChoiceRow
                key={run.id}
                href={`/deploy/${run.projectId}/runs/${run.id}`}
                verb={`View ${item.projectName}`}
                leading={
                  summary ? (
                    <ProjectMark deployment={summary} size="sm" />
                  ) : (
                    <ProductLogo size="sm" fallback={CloudUpload} />
                  )
                }
                title={item.projectName}
                description={
                  <span className="flex min-w-0 items-center gap-1.5">
                    <span className="shrink-0">
                      {runTitle(run)} · {item.environment}
                    </span>
                    {roomy && starter}
                  </span>
                }
                trailing={
                  roomy ? (
                    <>
                      {path}
                      <span className="w-28 min-w-0 truncate">
                        <TextShimmer className="text-xs font-medium">{stage}</TextShimmer>
                      </span>
                      {elapsed}
                    </>
                  ) : (
                    elapsed
                  )
                }
                actions={
                  canCancel &&
                  isCancellable(run.state) &&
                  !run.cancelRequested && (
                    // Recoverable, so not in the danger colour: the run page's
                    // own Cancel, a size down.
                    <Button
                      variant="outline"
                      size="xs"
                      disabled={busy}
                      pending={busy}
                      onClick={() => onCancel(item)}
                    >
                      <StopCircle className="size-3" />
                      Cancel
                    </Button>
                  )
                }
              >
                {!roomy && (
                  <div className="min-w-0 space-y-1.5 text-hint text-muted-foreground">
                    <div className="flex min-w-0 items-center gap-3">
                      {path}
                      <TextShimmer className="truncate text-hint font-medium">{stage}</TextShimmer>
                    </div>
                    <div className="flex min-w-0 items-center gap-1.5">{starter}</div>
                  </div>
                )}
              </ChoiceRow>
            )
          })}
        </ChoiceList>
      </PanelBody>
    </Panel>
  )
}

/** The ways into the fleet, the same five `/deploy/new` opens on, one click nearer. */
const SOURCES: {
  key: string
  title: string
  verb: string
  description: string
  mark: React.ComponentType<{ className?: string }>
  products: string[]
}[] = [
  {
    key: "git",
    title: "Git repository",
    verb: "Import a Git repository",
    description: "Build from a branch; a push can deploy it.",
    mark: GitHubMark,
    products: ["github", "gitlab", "bitbucket", "codeberg"],
  },
  {
    key: "image",
    title: "Docker image",
    verb: "Run a Docker image",
    description: "Run an image from any registry.",
    mark: Box,
    products: ["docker", "quay", "harbor"],
  },
  {
    key: "template",
    title: "Template",
    verb: "Start from a template",
    description: "A reviewed application, ready to run.",
    mark: GridMasonry,
    products: ["n8n", "grafana", "uptime-kuma", "vaultwarden"],
  },
  {
    key: "database",
    title: "Database",
    verb: "Launch a database",
    description: "PostgreSQL, MySQL, MariaDB, Redis or MongoDB.",
    mark: Database,
    products: Object.keys(DATABASE_ENGINE_LABELS),
  },
  {
    key: "compose",
    title: "Compose",
    verb: "Deploy a Compose stack",
    description: "Several containers from one compose file.",
    mark: Layers,
    products: ["docker-compose"],
  },
]

/**
 * No projects yet. The empty state says what goes here, drawn as the places a
 * project comes from, and under it the five ways in as the cards `/deploy/new`
 * opens on — lit, because each is a choice (§16), and each lands on its own
 * tab through `?source=`.
 */
function FleetEmpty({ admin }: { admin: boolean }) {
  return (
    <>
      <EmptyState
        mark={<ProductLogos ids={["github", "docker", "docker-compose"]} size="md" />}
        title="Deploy your first project"
        description="Everything you deploy has a home here."
        action={
          <div className="flex flex-wrap items-center justify-center gap-2">
            {admin && (
              <Button size="sm" asChild>
                <Link href="/deploy/new">New project</Link>
              </Button>
            )}
            <Button size="sm" variant="outline" asChild>
              <Link href="/docker/stacks">See what is already running</Link>
            </Button>
          </div>
        }
      />
      {admin && (
        <section className="flex min-w-0 flex-col gap-3">
          <p className="eyebrow">Start from</p>
          <ChoiceGrid columns={3}>
            {SOURCES.map((source, index) => (
              <ChoiceCard
                key={source.key}
                index={index}
                href={`/deploy/new?source=${source.key}`}
                verb={source.verb}
                title={source.title}
                mark={source.mark}
                description={source.description}
                trailing={
                  <span className="mt-auto pt-1">
                    <ProductGlyphs ids={source.products} />
                  </span>
                }
              />
            ))}
          </ChoiceGrid>
        </section>
      )}
    </>
  )
}
