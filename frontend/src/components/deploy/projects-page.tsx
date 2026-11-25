"use client"

import { useCallback, useMemo, useState } from "react"
import Link from "next/link"
import { useRouter, useSearchParams } from "next/navigation"
import {
  Archive,
  Bell,
  Box,
  CloudUpload,
  Cross,
  Database,
  GitHubMark,
  GridMasonry,
  Key,
  Layers,
  MagnifyingGlass,
  Plus,
  StopCircle,
} from "@/components/icons"
import { get, post } from "@/lib/api"
import { plural } from "@/lib/format"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { useSessionState } from "@/lib/view-state"
import type {
  ArchivedDeployment,
  DeploymentActiveWork,
  DeploymentFleet,
  DeploymentSummary,
  FleetPullRequests,
  TrafficPulse,
} from "@/lib/types"
import { useConfirm } from "@/components/confirm-dialog"
import { ChoiceCard, ChoiceGrid } from "@/components/choice-card"
import { ChoiceList, ChoiceRow } from "@/components/choice-row"
import { IconAction } from "@/components/icon-action"
import { Page, PageContext } from "@/components/page"
import { SearchInput, SearchInputSkeleton } from "@/components/search-input"
import { StatusWords } from "@/components/status-dot"
import { Toolbar } from "@/components/toolbar"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ProductGlyphs, ProductLogo, ProductLogos } from "@/components/product-logo"
import { EmptyState, ErrorState } from "@/components/state"
import { ChipCount, ChipStrip, FilterChip } from "@/components/chips"
import { VerbMenu, type Verb } from "@/components/verbs"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { ArchivedProjects } from "@/components/deploy/archived-projects"
import { FleetRightSize } from "@/components/deploy/right-size"
import { PROJECT_CARD_HEIGHT, ProjectCard, runClock, stageOf } from "@/components/deploy/fleet-card"
import { ProjectMark } from "@/components/project-mark"
import { MiniReleasePath } from "@/components/deploy/run-pipeline"
import { RunActorMark } from "@/components/deploy/run-marks"
import {
  DATABASE_ENGINE_LABELS,
  isCancellable,
  runCommit,
  runTitle,
  runTriggerLine,
  RUN_IN_FLIGHT_POLL_MS,
} from "@/components/deploy/vocabulary"
import { useNow } from "@/hooks/use-now"
import {
  FLEET_FILTERS,
  fleetCounts,
  fleetHaystack,
  matchesFilter,
  sortFleet,
  type FleetFilter,
} from "@/components/deploy/fleet"
import { useBreakpoint } from "@/lib/breakpoints"

/**
 * The fleet: every project, what it is, and what is happening to it now.
 *
 * `?view=archived` switches the same route to the archived list — a query
 * flag rather than a second page, because it is the same destination with its
 * controls in the list toolbar.
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

/**
 * The fleet before its first answer, in the shape it arrives in: the
 * toolbar's search, then three cards. The route's Suspense fallback draws the
 * same thing under the same header.
 */
export function FleetSkeleton() {
  return (
    <>
      <SearchInputSkeleton />
      <ul aria-hidden className={GRID}>
        {[0, 1, 2].map((index) => (
          <li key={index}>
            <Skeleton className={cn(PROJECT_CARD_HEIGHT, "rounded-xl")} />
          </li>
        ))}
      </ul>
    </>
  )
}

/**
 * The page reads top to bottom as the question an operator brings to it: the
 * runs in flight are the thing moving; the Attention list names what somebody
 * has to act on, in the engine's own words; and the projects follow, worst
 * first, each drawn as the product it is, carrying its own traffic.
 */
function Fleet() {
  const { can } = useAuth()
  const router = useRouter()
  const { confirm, dialog } = useConfirm()
  const admin = can("system.admin")
  // The header's shape, and the in-progress rows', chosen once rather than
  // drawn twice and hidden: the tests and a screen reader find each link by
  // its name, and a hidden twin is a second answer to every query (§12). A
  // run in flight keeps its stage and clock beside its name from `sm`; its
  // release path joins the line only from `lg`, where the sidebar leaves the
  // name room to be read.
  const roomy = useBreakpoint("sm")
  const lined = useBreakpoint("lg")
  const wide = useBreakpoint("xl")
  // While anything is building the fleet follows it every two seconds, so a
  // card's stage keeps up with the run; at rest it reads every five.
  const [inFlight, setInFlight] = useState(false)
  const fleet = usePoll(
    (signal) => get<DeploymentFleet>("/deploy/", { view: "fleet" }, signal),
    inFlight ? RUN_IN_FLIGHT_POLL_MS : 5000,
  )
  const running = (fleet.data?.activeWork.length ?? 0) > 0
  if (running !== inFlight) setInFlight(running)
  // Every project's last hour in one answer, cached to the minute per route
  // on the server, so a fleet of forty is one read rather than forty.
  const pulse = usePoll(
    (signal) => get<Record<string, TrafficPulse>>("/deploy/traffic", undefined, signal),
    30000,
    [],
    { key: "/deploy/traffic" },
  )
  // Read once for the count beside Archived, and again when a card archives
  // its project: the archive does not otherwise change under the reader, and
  // each row costs the server a history read.
  const archive = usePoll(
    (signal) => get<ArchivedDeployment[]>("/deploy/", { view: "archived" }, signal),
    0,
  )
  // Every GitHub project's open pull requests, on the Git page's minute, for
  // the Pull requests chip alone: a card does not draw them. The server
  // answers from its cache, and a failure leaves the chip hidden rather than
  // the page with an error in it.
  const pullRequests = usePoll(
    (signal) => get<FleetPullRequests>("/deploy/pull-requests", undefined, signal),
    60000,
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
  const [filter, setFilter] = useSessionState<FleetFilter>("deploy.fleet.filter.075", "all")

  const deployments = useMemo(() => fleet.data?.deployments ?? [], [fleet.data])
  const pulses = pulse.data
  const pulls = useMemo(() => pullRequests.data?.projects ?? {}, [pullRequests.data])
  const counts = useMemo(
    () => fleetCounts(deployments, pulses, pulls),
    [deployments, pulses, pulls],
  )
  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase()
    return sortFleet(
      deployments.filter(
        (deployment) =>
          (!q || fleetHaystack(deployment).includes(q)) &&
          matchesFilter(deployment, filter, pulses, pulls),
      ),
    )
  }, [deployments, filter, pulses, pulls, query])
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
  // two ragged rows; each page is its word (§13).
  const pages: Verb[] = [
    {
      key: "notifications",
      label: "Notifications",
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
            icon: Key,
            run: () => router.push("/deploy/credentials"),
          },
        ]
      : []),
    {
      key: "archived",
      label: "Archived",
      // The count annotates the item at its end, where a shortcut would sit.
      shortcut: archivedCount > 0 ? String(archivedCount) : undefined,
      icon: Archive,
      run: () => router.push("/deploy?view=archived"),
    },
  ]

  // One verb and a ⋯, after how many of the fleet a search or a chip left.
  // Notifications and Credentials are the sidebar's, which shows them beside
  // this page from md; on a phone, where the sidebar is a drawer away, the
  // menu carries them too. Archived is only here. On a phone the verb is one
  // word, so the search keeps the room it needs on its line. The fleet is
  // cards only (25 Nov 2026), so no layout switch stands beside them.
  const actions = (
    <div className="ml-auto flex shrink-0 items-center gap-2">
      {narrowed && (
        <span className="numeric text-hint text-muted-foreground">
          {filtered.length} of {deployments.length}
        </span>
      )}
      {admin && (
        <Button size="sm" asChild>
          <Link href="/deploy/new" aria-label={roomy ? undefined : "New project"}>
            <Plus className="size-3.5" />
            {roomy ? "New project" : "New"}
          </Link>
        </Button>
      )}
      <VerbMenu
        label="More deployment pages"
        verbs={roomy ? pages.filter((verb) => verb.key === "archived") : pages}
      />
    </div>
  )

  return (
    <Page>
      <PageContext title="Deployments" />

      {fleet.loading && !fleet.data && <FleetSkeleton />}
      {fleet.error && !fleet.data && <ErrorState error={fleet.error} onRetry={fleet.refresh} />}

      {/* The fleet rises once, when the first snapshot
          lands (§11), with the page's own rhythm between its blocks. */}
      {fleet.data && (
        <div className="flex min-w-0 animate-rise flex-col gap-6 md:gap-8">
          {fleet.data.activeWork.length > 0 && (
            <InProgressPanel
              work={fleet.data.activeWork}
              deployments={deployments}
              wide={roomy}
              roomy={lined}
              canCancel={can("service.control")}
              cancelling={cancelling}
              onCancel={cancel}
            />
          )}

          {/* Only what somebody has to act on, with the engine's own reason.
              The Attention chip below narrows the cards to the same projects. */}

          {empty && <FleetEmpty admin={admin} />}

          {deployments.length > 0 && (
            <div className="flex min-w-0 flex-col gap-4">
              {/* From xl it is one line: search, chips, the count, the verb.
                  Below xl the search, the count and the verb share the first
                  line and the chips take the whole of the second, scrolling
                  on a phone to the screen's edge, where a chip cut in half
                  says there is more. */}
              <Toolbar className="justify-between gap-x-4">
                <div className="flex w-full min-w-0 items-center gap-2 xl:w-auto">
                  <SearchInput
                    value={query}
                    onChange={(event) => setQuery(event.target.value)}
                    // On a phone the field shares its line with the layout,
                    // the verb and the menu, and the full placeholder was cut
                    // mid-word; its name still says what it searches.
                    placeholder={roomy ? "Search deployments" : "Search"}
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
                  {!wide && actions}
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
                        )}
                      >
                        {label}
                        <ChipCount>{counts[key]}</ChipCount>
                      </FilterChip>
                    ) : null,
                  )}
                </ChipStrip>
                {wide && actions}
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
              ) : (
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
              )}
            </div>
          )}

          {/* After the projects: the limits worth changing are a question
              about the fleet once its state has been read. */}
          {deployments.length > 0 && <FleetRightSize />}
        </div>
      )}
      {dialog}
    </Page>
  )
}

/**
 * Runs in flight, above everything else: the one thing on the page moving
 * under the reader, so it comes first and takes no frame.
 * Each is a destination — the run's own page — drawn as the project it is
 * releasing, with the release path sweeping and the stage it is at lit, which
 * is how its page draws it too.
 *
 * One line, so Cancel stands on the row's middle: the stage and the clock sit
 * at its end in fixed measures, so down the list the stages are one column and
 * the clocks end on one edge. From `lg` the release path leads them, and who
 * started the run follows its name; narrower, the stage's word says where it
 * is, and who started it is on the run's page. A phone has no room for the
 * stage beside the name and keeps one slim line under it — the path and the
 * stage, nothing else.
 */
function InProgressPanel({
  work,
  deployments,
  wide,
  roomy,
  canCancel,
  cancelling,
  onCancel,
}: {
  work: DeploymentActiveWork[]
  deployments: DeploymentSummary[]
  /** From `sm`: the stage beside the name. */
  wide: boolean
  /** From `lg`: the release path before the stage, and who started the run. */
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
      <StatusWords>{plural(work.length, "deployment")} in progress</StatusWords>
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
            // Who started it and what it is building, beside the run's name.
            // The starter is named, because a face and then a subject is how
            // every card below draws a commit and its author.
            const starter = (
              <>
                <RunActorMark run={run} remote={summary?.sourceRemote} size="xs" />
                <span className="shrink-0">{runTriggerLine(run)}</span>
                {commit?.subject && (
                  <span className="min-w-0 truncate text-muted-foreground">· {commit.subject}</span>
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
                    {/* Whole beside the starter, which gives way first; alone,
                        beside the stage and Cancel, it is what gives way. */}
                    <span className={roomy ? "shrink-0" : "min-w-0 truncate"}>
                      {runTitle(run)} · {item.environment}
                    </span>
                    {roomy && starter}
                  </span>
                }
                trailing={
                  wide ? (
                    <>
                      {roomy && path}
                      <span className="w-28 min-w-0 truncate text-xs">
                        <TextShimmer className="font-medium">{stage}</TextShimmer>
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
                {!wide && (
                  <div className="flex min-w-0 items-center gap-3">
                    {path}
                    <TextShimmer className="truncate text-hint font-medium">{stage}</TextShimmer>
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

/**
 * The ways into the fleet, one click nearer: the four sources `/deploy/new`
 * opens on, and Compose, which opens its Docker tab where stacks are offered.
 */
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
 * project comes from, and under it the five ways in as cards — lit, because
 * each is a choice (§16), and each lands on its tab of `/deploy/new` through
 * `?source=`.
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
