"use client"

import { createContext, useCallback, useContext, useEffect, useMemo, useState } from "react"
import Link from "next/link"
import { useRouter, useSearchParams } from "next/navigation"
import { ArrowLeft } from "@/components/icons"
import { get, post } from "@/lib/api"
import { notify } from "@/lib/toast"
import { usePoll } from "@/hooks/use-poll"
import type {
  DeploymentEngineRun,
  DeploymentOperations,
  DeploymentRelease,
  DeploymentRuntimeServices,
  DeploymentSummary,
  DeployProject,
  DeploymentRunsPage,
} from "@/lib/types"
import { Page, PageHeader, PageState } from "@/components/page"
import { ErrorState } from "@/components/state"
import { Button } from "@/components/ui/button"

/**
 * One project, read once for every page under it.
 *
 * The project shell (header, facts, tab strip) and each tab used to poll the
 * same detail endpoint separately, and a tab that opened from a link had to
 * fetch the project before it could draw anything. The layout owns the reads
 * now — detail, runs, releases and the slower operational evidence — and the
 * pages take what they need from here.
 */

export type ProjectDetail = {
  project: DeployProject
  running: boolean
  deployment: DeploymentSummary
  runtime?: DeploymentRuntimeServices
}

export type ProjectOperation = "deploy" | "redeploy" | "restart" | "force_build" | "stop" | "start"

// What to say when `start(operation)` itself never reaches the server — the
// operation-specific words a reader needs to tell "could not stop the
// application" from "could not start the rebuild" apart.
const OPERATION_FAILURE_TITLES: Record<ProjectOperation, string> = {
  deploy: "Could not start the deployment",
  redeploy: "Could not start the redeploy",
  restart: "Could not restart the application",
  force_build: "Could not start the rebuild",
  stop: "Could not stop the application",
  start: "Could not start the application",
}

export type ProjectContextValue = {
  projectId: number
  environmentId: number
  detail: ProjectDetail
  /** `buildMethod !== "legacy_compose"`: the persistent engine rather than the compatibility pipeline. */
  normalized: boolean
  archived: boolean
  markArchived: () => void
  runs: DeploymentEngineRun[]
  runsLoading: boolean
  /** The newest page did not reach the oldest run, so an older page can be asked for. */
  hasOlderRuns: boolean
  releases: DeploymentRelease[]
  operations?: DeploymentOperations
  operationsLoading: boolean
  /** The run that recorded the live release, for "deployed 3h ago by operator". */
  liveRun?: DeploymentEngineRun
  liveRelease?: DeploymentRelease
  refresh: () => void
  refreshOperations: () => void
  /** Enqueues a run and opens its page. Resolves once the request is answered. */
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
  const router = useRouter()
  const valid = Number.isInteger(projectId) && projectId > 0
  const [archivedLocally, setArchivedLocally] = useState(false)
  const [starting, setStarting] = useState<ProjectOperation>()

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

  // The poll objects are rebuilt every render; their refresh callbacks are the
  // stable part, so depending on those keeps the context value stable too.
  const refreshDetail = detail.refresh
  const refreshRuns = runs.refresh
  const refreshReleases = releases.refresh
  const refresh = useCallback(() => {
    refreshDetail()
    refreshRuns()
    refreshReleases()
  }, [refreshDetail, refreshRuns, refreshReleases])

  const start = useCallback(
    async (operation: ProjectOperation) => {
      const deployment = detail.data?.deployment
      if (!deployment) return
      setStarting(operation)
      try {
        const run = await post<DeploymentEngineRun>(
          `/deploy/${projectId}/environments/${deployment.environmentId}/runs`,
          { operation },
        )
        router.push(`/deploy/${projectId}/runs/${run.id}`)
      } catch (error) {
        notify.error(OPERATION_FAILURE_TITLES[operation], error)
        setStarting(undefined)
        // A stop/start refused as `already_stopped`/`not_stopped` means the
        // card's own copy of `stopped` disagrees with the server right now —
        // re-read immediately rather than leave it wrong for up to 5s.
        refreshDetail()
      }
    },
    [detail.data?.deployment, projectId, router, refreshDetail],
  )

  const value = useMemo<ProjectContextValue | null>(() => {
    if (!detail.data) return null
    const summary = detail.data.deployment
    const runList = runs.data?.runs ?? []
    const liveRun = summary.liveReleaseId
      ? runList.find((run) => run.releaseId === summary.liveReleaseId && run.state === "succeeded")
      : undefined
    return {
      projectId,
      environmentId: summary.environmentId,
      detail: detail.data,
      normalized: summary.buildMethod !== "legacy_compose",
      archived,
      markArchived: () => setArchivedLocally(true),
      runs: runList,
      runsLoading: runs.loading && !runs.data,
      hasOlderRuns: runs.data?.nextBefore !== undefined,
      releases: releases.data ?? [],
      operations: operations.data,
      operationsLoading: operations.loading && !operations.data,
      liveRun,
      liveRelease: releases.data?.find((release) => release.id === summary.liveReleaseId),
      refresh,
      refreshOperations: operations.refresh,
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
    projectId,
    archived,
    refresh,
    start,
    starting,
  ])

  if (!valid || (detail.error && !detail.data)) {
    return (
      <Page>
        <PageHeader eyebrow="Deployments" title="Deployment unavailable" />
        {detail.error && <ErrorState error={detail.error} onRetry={detail.refresh} />}
        <Button variant="outline" size="sm" asChild className="w-fit">
          <Link href="/deploy">
            <ArrowLeft className="size-3.5" /> Back to deployments
          </Link>
        </Button>
      </Page>
    )
  }
  if (!value) return <PageState eyebrow="Deployments" title="Deployment" />
  return <ProjectContext.Provider value={value}>{children}</ProjectContext.Provider>
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
