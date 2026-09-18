"use client"

import { useEffect, useMemo, useState } from "react"
import Link from "next/link"
import { useRouter, useSearchParams } from "next/navigation"
import {
  Box,
  CloudUpload,
  External,
  GitBranch,
  GridSquare,
  Home,
  Layers,
  ListUnordered,
  Logs,
  Plus,
  Puzzle,
  RefreshClockwise,
  SettingsGear,
  StopCircle,
} from "@/components/icons"
import { get, post } from "@/lib/api"
import { relativeTime, plural } from "@/lib/format"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import type {
  DeploymentActiveWork,
  DeploymentEngineRun,
  DeploymentFleet,
  DeploymentSummary,
} from "@/lib/types"
import { Page, PageHeader, SearchInput, Toolbar } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Row, RowList } from "@/components/row-list"
import { EmptyState, ErrorState, LoadingPanel } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { FilterChip } from "@/components/tabs"
import { VerbMenu, type Verb } from "@/components/verbs"
import { Button } from "@/components/ui/button"
import { ArchivedProjects } from "@/components/deploy/archived-projects"
import {
  ProjectStatus,
  RunStatus,
  WorkloadMark,
  deploymentURL,
  formatDuration,
  hostOf,
  humanize,
  isCancellable,
  projectState,
  runDurationSeconds,
  shortRevision,
  sourceLine,
  useNow,
} from "@/components/deploy/vocabulary"

/**
 * The fleet: every project, and what is happening to it right now.
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

type FilterKey = "all" | "deploying" | "failed" | "attention" | "pending"

const FILTERS: { key: FilterKey; label: string }[] = [
  { key: "all", label: "All" },
  { key: "deploying", label: "Deploying" },
  { key: "failed", label: "Failed" },
  { key: "attention", label: "Attention" },
  { key: "pending", label: "Pending changes" },
]

/** Unhealthy news and unknown news are both reasons to look, so the chip folds them together. */
function needsAttention(deployment: DeploymentSummary) {
  return (
    deployment.health === "unhealthy" ||
    deployment.health === "failed" ||
    deployment.health === "unavailable"
  )
}

function matchesFilter(deployment: DeploymentSummary, filter: FilterKey) {
  switch (filter) {
    case "deploying":
      return Boolean(deployment.activeRun)
    case "failed":
      return projectState(deployment) === "failed"
    case "attention":
      return needsAttention(deployment)
    case "pending":
      return deployment.pendingChanges
    default:
      return true
  }
}

const VIEW_KEY = "jd.deploy.fleet"

type FleetView = { query: string; filter: FilterKey; layout: "grid" | "list" }

/** The furniture, not the question: kept across a visit so a filtered fleet does not reset itself. */
function loadFleetView(): Partial<FleetView> {
  try {
    const raw = sessionStorage.getItem(VIEW_KEY)
    return raw ? (JSON.parse(raw) as Partial<FleetView>) : {}
  } catch {
    return {}
  }
}

function saveFleetView(view: FleetView) {
  try {
    sessionStorage.setItem(VIEW_KEY, JSON.stringify(view))
  } catch {
    // Private browsing or a full quota: the choice still applies this session.
  }
}

function Fleet() {
  const { can } = useAuth()
  const fleet = usePoll(
    (signal) => get<DeploymentFleet>("/deploy/", { view: "fleet" }, signal),
    5000,
  )
  const [query, setQuery] = useState(() => loadFleetView().query ?? "")
  const [filter, setFilter] = useState<FilterKey>(() => loadFleetView().filter ?? "all")
  const [layout, setLayout] = useState<"grid" | "list">(() => loadFleetView().layout ?? "grid")

  useEffect(() => {
    saveFleetView({ query, filter, layout })
  }, [query, filter, layout])

  const deployments = useMemo(() => fleet.data?.deployments ?? [], [fleet.data])
  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase()
    return deployments.filter((deployment) => {
      if (
        q &&
        !`${deployment.name} ${deployment.endpoint ?? ""} ${deployment.sourceRef ?? ""}`
          .toLowerCase()
          .includes(q)
      )
        return false
      return matchesFilter(deployment, filter)
    })
  }, [deployments, filter, query])

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

  return (
    <Page>
      <PageHeader
        eyebrow="Apps"
        title="Deployments"
        actions={
          <>
            <Button variant="ghost" size="sm" asChild>
              <Link href="/deploy/notifications">Notifications</Link>
            </Button>
            {/* Reading the list itself needs system.admin (the routes seal
                every credential route alike), so the link is drawn only
                where the destination would not just refuse it. */}
            {can("system.admin") && (
              <Button variant="ghost" size="sm" asChild>
                <Link href="/deploy/credentials">Credentials</Link>
              </Button>
            )}
            <Button variant="ghost" size="sm" asChild>
              <Link href="/deploy?view=archived">Archived</Link>
            </Button>
            {can("system.admin") && (
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

      {fleet.loading && !fleet.data && <LoadingPanel rows={5} />}
      {fleet.error && !fleet.data && <ErrorState error={fleet.error} onRetry={fleet.refresh} />}

      {fleet.data && fleet.data.activeWork.length > 0 && (
        <InProgressPanel
          work={fleet.data.activeWork}
          slots={fleet.data.slots}
          canCancel={can("service.control")}
          cancelling={cancelling}
          onCancel={cancel}
        />
      )}

      {fleet.data && (
        <div className="space-y-4">
          <Toolbar>
            <SearchInput
              value={query}
              onChange={(event) => setQuery(event.target.value)}
              placeholder="Search deployments"
              aria-label="Search deployments"
              containerClassName="min-w-0 flex-1 sm:w-auto sm:max-w-sm"
            />
            {FILTERS.map((item) => (
              <FilterChip
                key={item.key}
                selected={filter === item.key}
                onClick={() => setFilter(item.key)}
              >
                {item.label}
              </FilterChip>
            ))}
            <span className="flex-1" />
            <span className="numeric text-hint text-muted-foreground">
              {filtered.length} of {deployments.length}
            </span>
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
          </Toolbar>

          {filtered.length === 0 ? (
            <EmptyState
              icon={CloudUpload}
              title={
                deployments.length === 0 ? "Deploy your first project" : "No deployments match"
              }
              description={
                deployments.length === 0
                  ? "Import a repository, launch a database, or start from an application template. Everything you deploy has a home here."
                  : "Change or clear the filters to see the rest of the fleet."
              }
              action={
                deployments.length === 0 ? (
                  <div className="flex flex-wrap items-center justify-center gap-2">
                    {can("system.admin") && (
                      <Button size="sm" asChild>
                        <Link href="/deploy/new">New project</Link>
                      </Button>
                    )}
                    <Button size="sm" variant="outline" asChild>
                      <Link href="/docker/stacks">See what is already running</Link>
                    </Button>
                  </div>
                ) : (
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
                )
              }
            />
          ) : layout === "grid" ? (
            <ul
              aria-label="Deployment projects"
              className="grid animate-rise gap-4 md:grid-cols-2 2xl:grid-cols-3"
            >
              {filtered.map((deployment) => (
                <li key={deployment.id} className="min-w-0">
                  <ProjectCard deployment={deployment} />
                </li>
              ))}
            </ul>
          ) : (
            <RowList aria-label="Deployment projects" className="animate-rise">
              {filtered.map((deployment) => {
                const url = deploymentURL(deployment.endpoint)
                return (
                  <Row
                    key={deployment.id}
                    href={`/deploy/${deployment.id}`}
                    leading={<WorkloadMark profile={deployment.profile} size="sm" />}
                    title={deployment.name}
                    subtitle={sourceLine(deployment).primary}
                    trailing={
                      <>
                        {url && (
                          <span className="hidden truncate text-hint text-muted-foreground md:inline">
                            {hostOf(url)}
                          </span>
                        )}
                        <ProjectStatus summary={deployment} />
                        <span className="numeric text-hint text-muted-foreground">
                          {relativeTime(deployment.lastRun?.requestedAt ?? deployment.updatedAt)}
                        </span>
                      </>
                    }
                  />
                )
              })}
            </RowList>
          )}
        </div>
      )}
    </Page>
  )
}

/**
 * Runs in flight, as a plain list above the fleet. It is the one thing on the
 * page that changes under the reader, so it gets the top and no frame.
 */
function InProgressPanel({
  work,
  slots,
  canCancel,
  cancelling,
  onCancel,
}: {
  work: DeploymentActiveWork[]
  slots: DeploymentFleet["slots"]
  canCancel: boolean
  cancelling: Set<number>
  onCancel: (item: DeploymentActiveWork) => void
}) {
  const now = useNow(1000)
  return (
    <Panel plain aria-labelledby="in-progress-title">
      <PanelHeader
        title={<span id="in-progress-title">In progress</span>}
        actions={
          <span className="numeric text-hint text-muted-foreground">
            {slots.heavyUsed}/{slots.heavyCapacity} build · {slots.lightUsed}/{slots.lightCapacity}{" "}
            control
          </span>
        }
      />
      {/* The live region announces the count, not the list: the list's own
          elapsed-time column re-renders every second, and a screen reader
          tied to that read every row back once a second. */}
      <span className="sr-only" aria-live="polite" aria-atomic="true">
        {plural(work.length, "deployment")} in progress
      </span>
      <PanelBody flush>
        <RowList>
          {work.map((item) => {
            const busy = cancelling.has(item.run.id)
            return (
              <Row
                key={item.run.id}
                leading={<RunStatus state={item.run.state} live />}
                title={
                  <Link
                    href={`/deploy/${item.run.projectId}/runs/${item.run.id}`}
                    className="rounded-sm focus-ring hover:underline"
                  >
                    {item.projectName}
                  </Link>
                }
                subtitle={`${item.environment} · ${
                  item.currentStep ? humanize(item.currentStep) : "Waiting for next step"
                }`}
                trailing={
                  <>
                    <span className="numeric text-hint text-muted-foreground">
                      {item.queuePosition
                        ? `Queue ${item.queuePosition}`
                        : formatDuration(runDurationSeconds(item.run, now))}
                    </span>
                    <Button variant="outline" size="xs" asChild>
                      <Link href={`/deploy/${item.run.projectId}/runs/${item.run.id}`}>View</Link>
                    </Button>
                    {canCancel && isCancellable(item.run.state) && !item.run.cancelRequested && (
                      <Button
                        variant="ghost"
                        size="xs"
                        className="text-destructive"
                        disabled={busy}
                        pending={busy}
                        onClick={() => onCancel(item)}
                      >
                        <StopCircle className="size-3" />
                        Cancel
                      </Button>
                    )}
                  </>
                }
              />
            )
          })}
        </RowList>
      </PanelBody>
    </Panel>
  )
}

/**
 * One project in the fleet.
 *
 * A `Panel interactive` rather than `plain` — the one exception design system
 * §15 names by precedent: a card is what a project grid is made of.
 */
function ProjectCard({ deployment }: { deployment: DeploymentSummary }) {
  const { can } = useAuth()
  const router = useRouter()
  const [redeploying, setRedeploying] = useState(false)
  const url = deploymentURL(deployment.endpoint)
  const source = sourceLine(deployment)
  const sha = shortRevision(deployment.sourceRevision)
  // A Compose stack's secondary line names its file; once more than one
  // service is running from it, how many is the fact worth a glance.
  const composeServices =
    deployment.profile === "compose" && (deployment.serviceCount ?? 0) > 1
      ? `${deployment.serviceCount} services`
      : undefined
  // The glyph before the source line — wayfinding for which kind of source
  // this is, the same idea as `WorkloadMark`'s own icon switch.
  const SourceIcon =
    source.kind === "compose"
      ? Layers
      : source.kind === "blueprint"
        ? Puzzle
        : source.kind === "image" || source.kind === "import"
          ? Box
          : GitBranch
  const base = `/deploy/${deployment.id}`

  const redeploy = async () => {
    setRedeploying(true)
    try {
      const run = await post<DeploymentEngineRun>(
        `/deploy/${deployment.id}/environments/${deployment.environmentId}/runs`,
        { operation: "redeploy" },
      )
      router.push(`/deploy/${deployment.id}/runs/${run.id}`)
    } catch (error) {
      notify.error("Could not start deployment", error)
      setRedeploying(false)
    }
  }

  const verbs: Verb[] = [
    {
      key: "open",
      label: "Open",
      detail: "The project overview.",
      icon: Home,
      run: () => router.push(base),
    },
    {
      key: "deployments",
      label: "Deployments",
      detail: "Every release of this project.",
      icon: CloudUpload,
      run: () => router.push(`${base}/deployments`),
    },
    {
      key: "logs",
      label: "Logs",
      detail: "Runtime logs for the live release.",
      icon: Logs,
      run: () => router.push(`${base}/logs`),
    },
    {
      key: "settings",
      label: "Settings",
      detail: "Build, runtime, variables and domains.",
      icon: SettingsGear,
      run: () => router.push(`${base}/settings/general`),
    },
  ]
  if (url) {
    verbs.push({
      key: "visit",
      label: "Visit",
      detail: "Open the live website in a new tab.",
      icon: External,
      run: () => window.open(url, "_blank", "noopener,noreferrer"),
    })
  }
  if (can("service.control") && deployment.liveReleaseId) {
    verbs.push({
      key: "redeploy",
      label: "Redeploy",
      detail: "Build and release the current source again.",
      icon: RefreshClockwise,
      disabled: Boolean(deployment.activeRun) || redeploying,
      run: () => void redeploy(),
    })
  }

  return (
    <Panel interactive className="h-full">
      <div className="flex h-full flex-col p-4">
        <div className="flex items-start gap-3">
          <WorkloadMark profile={deployment.profile} size="sm" />
          <div className="min-w-0 flex-1">
            <Link
              href={base}
              className="block truncate rounded-sm text-title font-medium focus-ring hover:underline"
            >
              {deployment.name}
            </Link>
            {url ? (
              <a
                href={url}
                target="_blank"
                rel="noopener noreferrer"
                className="mt-0.5 block truncate rounded-sm text-hint text-muted-foreground focus-ring hover:text-foreground"
              >
                {hostOf(url)}
              </a>
            ) : (
              <p className="mt-0.5 truncate text-hint text-muted-foreground">
                {deployment.liveReleaseId ? "Private service" : "Not deployed yet"}
              </p>
            )}
          </div>
          <VerbMenu verbs={verbs} label={`Actions for ${deployment.name}`} />
        </div>

        {/* Rows 2–3 are one link to the project, as the old card did — the
            trailing menu above needs its own click target, so only this part
            of the card can be the link. */}
        <Link
          href={base}
          className="-mx-2 mt-4 -mb-2 flex flex-col gap-2 rounded-md px-2 pt-2 pb-2 text-xs focus-ring-inset hover:bg-row-hover"
        >
          <span className="flex min-w-0 items-center gap-2">
            <SourceIcon aria-hidden className="size-3.5 shrink-0 text-muted-foreground" />
            <span className="min-w-0 truncate">
              {source.primary}
              {(composeServices ?? source.secondary) && (
                <span
                  className={cn(
                    "text-muted-foreground",
                    !composeServices && source.mono && "font-mono",
                  )}
                >
                  {" · "}
                  {composeServices ?? source.secondary}
                </span>
              )}
            </span>
            {!source.mono && sha && (
              <span className="ml-auto shrink-0 font-mono text-hint text-muted-foreground">
                {sha}
              </span>
            )}
          </span>
          <span className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
            <ProjectStatus summary={deployment} />
            <Dot />
            <span className="text-muted-foreground">
              {relativeTime(deployment.lastRun?.requestedAt ?? deployment.updatedAt)}
            </span>
            {deployment.environmentKind !== "production" && (
              <>
                <Dot />
                <Tag>{deployment.environmentName}</Tag>
              </>
            )}
            {deployment.pendingChanges && (
              <>
                <Dot />
                <Status tone="warning" label="Changes pending" />
              </>
            )}
          </span>
        </Link>
      </div>
    </Panel>
  )
}

function Dot() {
  return (
    <span aria-hidden="true" className="text-muted-foreground/40">
      ·
    </span>
  )
}
