"use client"

import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from "react"
import Link from "next/link"
import { usePathname, useRouter, useSearchParams } from "next/navigation"
import { ArrowLeft } from "@/components/icons"
import { get } from "@/lib/api"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import type {
  BlueprintDetail,
  DeploymentCheckResult,
  DeploymentEngineRun,
  DeploymentEnvironmentConfiguration,
  DeploymentGitWatch,
  DeploymentOperations,
  DeploymentRelease,
  DeploymentRuntimeServices,
  DeploymentSummary,
  DeployProject,
  DeploymentRunsPage,
} from "@/lib/types"
import { Page, PageContext } from "@/components/page"
import { ErrorState, LoadingPanel } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { useProjectStart } from "@/components/deploy/project-verbs"
import { projectProduct } from "@/components/deploy/vocabulary"
import { OverviewSkeleton } from "@/components/deploy/overview-skeleton"
import { useDeploymentCheck } from "@/components/deploy/deploy-check"

/**
 * One project, read once for every page under it.
 *
 * The project shell (its header and identity line) and each page used to poll
 * the same detail endpoint separately, and a tab that opened from a link had to
 * fetch the project before it could draw anything. The layout owns the reads
 * now — detail, runs, releases and the slower operational evidence — and the
 * pages take what they need from here.
 *
 * So do the reads more than one page drew from separately: the environment's
 * configuration (what the source, its identity and the build are), whether
 * the branch deploys itself — the shell and the Overview each polled that on
 * the same fifteen seconds — and the template a blueprint project came from.
 *
 * Starting a run is the projects grid's own `useProjectStart`, so the header's
 * command, its menu and a card on the grid enqueue a run and word a refusal
 * the same way.
 */

export type ProjectDetail = {
  project: DeployProject
  running: boolean
  deployment: DeploymentSummary
  runtime?: DeploymentRuntimeServices
}

export type ProjectOperation = "deploy" | "redeploy" | "restart" | "force_build" | "stop" | "start"

export type ProjectContextValue = {
  projectId: number
  environmentId: number
  detail: ProjectDetail
  /** `buildMethod !== "legacy_compose"`: the persistent engine rather than the compatibility pipeline. */
  normalized: boolean
  archived: boolean
  markArchived: () => void
  /** Undoes `markArchived` after a restore, and re-reads the project to confirm it. */
  markUnarchived: () => void
  runs: DeploymentEngineRun[]
  runsLoading: boolean
  /** The newest page did not reach the oldest run, so an older page can be asked for. */
  hasOlderRuns: boolean
  releases: DeploymentRelease[]
  operations?: DeploymentOperations
  operationsLoading: boolean
  /**
   * The environment's desired configuration — `source`, `identity`, `build`
   * and the rest. Read once, again by `refresh`, and again whenever the
   * summary's desired revision moves; a settings page that edits it keeps
   * its own copy (`useConfiguration`).
   */
  configuration?: DeploymentEnvironmentConfiguration
  /** Whether a Git project's branch deploys itself: the one fifteen-second poll. */
  gitWatch?: DeploymentGitWatch
  /** The template a blueprint project was made from: its name, and how its first sign-in works. */
  blueprint?: BlueprintDetail
  /**
   * What the project is, as a `product-logo` id — `projectProduct` read over
   * the summary, the configuration and the template together, so the header,
   * the preview and the dialogs draw one mark.
   */
  product?: string
  /**
   * The run that recorded the live release, for "deployed 3h ago by
   * operator" — by the run's release or the release's run, and only a run
   * that succeeded. Undefined when it is older than the runs read here.
   */
  liveRun?: DeploymentEngineRun
  liveRelease?: DeploymentRelease
  refresh: () => void
  refreshOperations: () => void
  /**
   * Preflight for the saved plan against the commit a deployment would build
   * now (`POST …/check`), asked when the project opens and after each save
   * that left changes waiting. Undefined until it answers about the revision
   * now saved.
   */
  check?: DeploymentCheckResult
  checking: boolean
  recheck: () => void
  /**
   * Enqueues a run and opens its page, asking "Ready to deploy?" first when
   * the check found what a build should not go past unasked. Resolves once
   * the request is answered or the question is on screen.
   */
  start: (operation: ProjectOperation) => Promise<void>
  /** The operation whose request is in flight, for a present-participle label. */
  starting?: ProjectOperation
}

const ProjectContext = createContext<ProjectContextValue | null>(null)

export function useProject() {
  const value = useContext(ProjectContext)
  if (!value) throw new Error("useProject must be used inside a ProjectProvider")
  return value
}

export function ProjectProvider({
  projectId,
  children,
}: {
  projectId: number
  children: React.ReactNode
}) {
  const valid = Number.isInteger(projectId) && projectId > 0
  const [archivedLocally, setArchivedLocally] = useState(false)

  const detail = usePoll(
    (signal) => get<ProjectDetail>(`/deploy/${projectId}`, undefined, signal),
    5000,
    [projectId],
    { enabled: valid },
  )
  const archived = archivedLocally || Boolean(detail.data?.project.archivedAt)
  const environmentId = detail.data?.deployment.environmentId ?? 0
  const runs = usePoll(
    (signal) => get<DeploymentRunsPage>(`/deploy/${projectId}/runs`, { view: "engine" }, signal),
    5000,
    [projectId],
    { enabled: valid },
  )
  const releases = usePoll(
    (signal) =>
      get<DeploymentRelease[]>(
        `/deploy/${projectId}/environments/${environmentId}/releases`,
        { limit: 30 },
        signal,
      ),
    10000,
    [projectId, environmentId],
    { enabled: valid && environmentId > 0 },
  )
  // Operational evidence reads five feature owners, so it polls on its own
  // slower cadence rather than riding the five-second detail poll.
  const operations = usePoll(
    (signal) => get<DeploymentOperations>(`/deploy/${projectId}/operations`, undefined, signal),
    20000,
    [projectId],
    { enabled: valid && !archived },
  )
  const configuration = usePoll(
    (signal) =>
      get<DeploymentEnvironmentConfiguration>(
        `/deploy/${projectId}/environments/${environmentId}/configuration`,
        undefined,
        signal,
      ),
    0,
    [projectId, environmentId],
    { enabled: valid && environmentId > 0 },
  )
  const gitWatch = usePoll(
    (signal) =>
      get<DeploymentGitWatch>(
        `/deploy/${projectId}/environments/${environmentId}/git-watch`,
        undefined,
        signal,
      ),
    15000,
    [projectId, environmentId],
    {
      enabled:
        valid &&
        detail.data?.deployment.buildMethod !== "legacy_compose" &&
        detail.data?.deployment.sourceKind === "git" &&
        environmentId > 0 &&
        !archived,
    },
  )
  // A template's source reference is `id@version`; the definition is read by id.
  const blueprintId =
    detail.data?.deployment.sourceKind === "blueprint"
      ? (detail.data.deployment.sourceRef ?? "").split("@")[0]
      : ""
  const blueprint = usePoll(
    (signal) => get<BlueprintDetail>(`/deploy/blueprints/${blueprintId}`, undefined, signal),
    0,
    [blueprintId],
    { enabled: Boolean(blueprintId) },
  )
  // The poll objects are rebuilt every render; their refresh callbacks are the
  // stable part, so depending on those keeps the context value stable too.
  const refreshDetail = detail.refresh
  const refreshRuns = runs.refresh
  const refreshReleases = releases.refresh
  const refreshConfiguration = configuration.refresh
  const refresh = useCallback(() => {
    refreshDetail()
    refreshRuns()
    refreshReleases()
    refreshConfiguration()
  }, [refreshDetail, refreshRuns, refreshReleases, refreshConfiguration])
  // Every configuration write advances the environment's desired revision,
  // which the five-second summary carries: a save on any settings page — each
  // edits its own copy — is re-read here when it moves, so the rail's
  // per-page marks and the header's facts follow it. Not a dependency of the
  // read itself, which would clear its data and blank the facts meanwhile.
  const desiredRevision = detail.data?.deployment.desiredRevision
  const seenRevision = useRef(desiredRevision)
  useEffect(() => {
    if (desiredRevision === undefined || desiredRevision === seenRevision.current) return
    const first = seenRevision.current === undefined
    seenRevision.current = desiredRevision
    if (!first) refreshConfiguration()
  }, [desiredRevision, refreshConfiguration])

  const markUnarchived = useCallback(() => {
    setArchivedLocally(false)
    refreshDetail()
  }, [refreshDetail])

  const { can } = useAuth()
  const check = useDeploymentCheck({
    projectId,
    environmentId,
    desiredRevision,
    pending: Boolean(detail.data?.deployment.pendingChanges),
    enabled:
      valid &&
      !archived &&
      can("service.control") &&
      detail.data !== undefined &&
      detail.data.deployment.buildMethod !== "legacy_compose",
  })
  const recheck = check.recheck
  // The same request the projects grid's cards make, so a refused start is
  // worded once — and re-reads the detail, since a stop or start refused as
  // already done means this copy of `stopped` disagrees with the server.
  const { start, starting, gate } = useProjectStart(
    detail.data?.deployment ?? { id: projectId, environmentId },
    refreshDetail,
    { result: check.result, recheck: check.recheck, checking: check.checking },
  )

  const value = useMemo<ProjectContextValue | null>(() => {
    if (!detail.data) return null
    const summary = detail.data.deployment
    const runList = runs.data?.runs ?? []
    const liveRelease = releases.data?.find((release) => release.id === summary.liveReleaseId)
    const liveRun = summary.liveReleaseId
      ? runList.find(
          (run) =>
            run.state === "succeeded" &&
            (run.releaseId === summary.liveReleaseId || run.id === liveRelease?.runId),
        )
      : undefined
    return {
      projectId,
      environmentId: summary.environmentId,
      detail: detail.data,
      normalized: summary.buildMethod !== "legacy_compose",
      archived,
      markArchived: () => setArchivedLocally(true),
      markUnarchived,
      runs: runList,
      runsLoading: runs.loading && !runs.data,
      hasOlderRuns: runs.data?.nextBefore !== undefined,
      releases: releases.data ?? [],
      operations: operations.data,
      operationsLoading: operations.loading && !operations.data,
      configuration: configuration.data,
      gitWatch: gitWatch.data,
      blueprint: blueprint.data,
      product: projectProduct(
        summary,
        configuration.data,
        blueprint.data && { id: blueprint.data.id, image: blueprint.data.image.reference },
      ),
      liveRun,
      liveRelease,
      refresh,
      refreshOperations: operations.refresh,
      check: check.result,
      checking: check.checking,
      recheck: () => void recheck(),
      start,
      starting,
    }
  }, [
    detail.data,
    runs.data,
    runs.loading,
    releases.data,
    operations.data,
    operations.loading,
    operations.refresh,
    configuration.data,
    gitWatch.data,
    blueprint.data,
    projectId,
    archived,
    markUnarchived,
    refresh,
    check.result,
    check.checking,
    recheck,
    start,
    starting,
  ])

  if (!valid || (detail.error && !detail.data)) {
    return (
      <Page>
        <PageContext eyebrow="Deployments" title="Deployment unavailable" />
        {detail.error && <ErrorState error={detail.error} onRetry={detail.refresh} />}
        <Button variant="outline" size="sm" asChild className="w-fit">
          <Link href="/deploy">
            <ArrowLeft className="size-3.5" /> Back to deployments
          </Link>
        </Button>
      </Page>
    )
  }
  if (!value) return <ShellSkeleton projectId={projectId} />
  return (
    <ProjectContext.Provider value={value}>
      {children}
      {gate}
    </ProjectContext.Provider>
  )
}

/**
 * The project shell's silhouette while its first read is in flight: the way
 * back and the name with the command's place beside it, then the identity
 * line under them — the mark's tile, where it answers, the facts — then the
 * page's own: the Overview's preview, wiring and readings on the Overview, a
 * plain block elsewhere. It was a page titled "Deployment" over a framed
 * table, so every project opened on a heading and a box that were both
 * replaced a moment later by different ones.
 */
function ShellSkeleton({ projectId }: { projectId: number }) {
  const overview = usePathname() === `/deploy/${projectId}`
  return (
    <Page aria-busy="true">
      <div className="flex min-w-0 flex-wrap items-end justify-between gap-x-6 gap-y-3">
        <div className="min-w-0 space-y-1.5">
          <p className="eyebrow">
            <Link
              href="/deploy"
              className="inline-flex items-center gap-1 rounded-sm focus-ring hover:underline"
            >
              <ArrowLeft className="size-3" /> Deployments
            </Link>
          </p>
          <h1 className="sr-only">Loading deployment</h1>
          <Skeleton aria-hidden className="h-8 w-56 max-w-full" />
        </div>
        <div aria-hidden className="flex shrink-0 items-center gap-2">
          <Skeleton className="h-8 w-20" />
          <Skeleton className="h-8 w-32" />
        </div>
      </div>
      <div aria-hidden className="flex min-w-0 items-center gap-4 border-b border-hairline pb-6">
        <Skeleton className="size-12 shrink-0 rounded-xl" />
        <div className="min-w-0 flex-1 space-y-2">
          <Skeleton className="h-4 w-44 max-w-full" />
          <div className="flex min-w-0 gap-3">
            <Skeleton className="h-3 w-32" />
            <Skeleton className="h-3 w-24" />
            <Skeleton className="hidden h-3 w-40 sm:block" />
          </div>
        </div>
      </div>
      {overview ? <OverviewSkeleton /> : <LoadingPanel plain />}
    </Page>
  )
}

/**
 * The project page used to switch on `?tab=`; every tab is a route now. A
 * bookmark, a notification link or an older deep link still lands where it
 * meant to.
 */
const LEGACY_TABS: Record<string, string> = {
  overview: "",
  deployments: "/deployments",
  logs: "/logs",
  runtime: "/runtime",
  diagnostics: "/runtime",
  metrics: "/runtime",
  console: "/console",
  players: "/players",
  settings: "/game-settings",
  configuration: "/settings/build",
  "runtime-settings": "/settings/runtime",
  variables: "/settings/variables",
  network: "/settings/domains",
  storage: "/settings/storage",
  dependencies: "/settings/databases",
  automations: "/settings/automation",
  lifecycle: "/settings/danger",
}

export function useLegacyTabRedirect(projectId: number) {
  const search = useSearchParams()
  const router = useRouter()
  const tab = search.get("tab")
  useEffect(() => {
    if (!tab) return
    const target = LEGACY_TABS[tab]
    if (target === undefined) return
    const rest = new URLSearchParams(search)
    rest.delete("tab")
    const query = rest.toString()
    router.replace(`/deploy/${projectId}${target}${query ? `?${query}` : ""}`)
  }, [tab, search, router, projectId])
  return Boolean(tab && LEGACY_TABS[tab] !== undefined)
}
