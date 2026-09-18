"use client"

import { Fragment, useMemo, useState } from "react"
import Link from "next/link"
import { useRouter } from "next/navigation"
import {
  CloudUpload,
  External,
  Eye,
  Pin,
  Play,
  RotateCounterClockwise,
  StopCircle,
} from "@/components/icons"
import { get, post, put } from "@/lib/api"
import { relativeTime, timestamp } from "@/lib/format"
import { notify } from "@/lib/toast"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import type {
  DeployCommit,
  DeploymentEngineRun,
  DeploymentPreview,
  DeploymentRelease,
  DeploymentRunsPage,
} from "@/lib/types"
import { Panel, PanelBody, PanelFooter, PanelHeader } from "@/components/panel"
import { Row, RowList } from "@/components/row-list"
import { EmptyNote, EmptyState, ErrorState, LoadingPanel, LoadingRows } from "@/components/state"
import { FilterChip } from "@/components/tabs"
import { Tag } from "@/components/tag"
import { Status } from "@/components/status-dot"
import { VerbActions, type Verb } from "@/components/verbs"
import { useConfirm } from "@/components/confirm-dialog"
import { Button } from "@/components/ui/button"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { useProject } from "@/components/deploy/project-context"
import {
  RunStatus,
  deploymentURL,
  formatDuration,
  hostOf,
  isCancellable,
  isRetryable,
  runDurationSeconds,
  runFailed,
  runRef,
  runSubject,
  runTitle,
  runTriggerLine,
  shortRevision,
} from "@/components/deploy/vocabulary"
import { Insights } from "@/components/deploy/insights"
import { RollbackDialog } from "@/components/deploy/rollback-dialog"
import { ReleaseComparisonSheet } from "@/components/deploy/release-comparison-sheet"

type StatusFilter = "all" | "ready" | "failed" | "cancelled"

const STATUS_FILTERS: { key: StatusFilter; label: string }[] = [
  { key: "all", label: "All" },
  { key: "ready", label: "Ready" },
  { key: "failed", label: "Failed" },
  { key: "cancelled", label: "Cancelled" },
]

function matchesFilter(run: DeploymentEngineRun, filter: StatusFilter) {
  if (filter === "all") return true
  if (filter === "ready") return run.state === "succeeded"
  if (filter === "failed") return runFailed(run.state)
  return run.state === "cancelled" || run.state === "superseded"
}

/** "main · a1b2c3d · by operator · 3h ago" — a run's provenance, one line. */
function RunSubtitle({ run }: { run: DeploymentEngineRun }) {
  const project = useProject()
  const { deployment, project: record } = project.detail
  const parts: React.ReactNode[] = []
  if (deployment.sourceKind === "git" || deployment.sourceKind === "local") {
    parts.push(runRef(run, deployment.sourceRef || record.branch))
    const sha = shortRevision(run.sourceRevision || deployment.sourceRevision)
    if (sha)
      parts.push(
        <span key="sha" className="font-mono">
          {sha}
        </span>,
      )
  }
  parts.push(runTriggerLine(run))
  parts.push(
    <time key="time" dateTime={run.requestedAt} title={timestamp(run.requestedAt)}>
      {relativeTime(run.requestedAt)}
    </time>,
  )
  return (
    <>
      {parts.map((part, index) => (
        <Fragment key={index}>
          {index > 0 && " · "}
          {part}
        </Fragment>
      ))}
    </>
  )
}

/**
 * The project's release history: delivery figures over the run history, then
 * every run as a row — Vercel's anatomy (status, title, provenance, trailing
 * state) rather than the pre-rebuild split between a run list, an immutable
 * releases panel and an inline comparison. Rollback and comparison move to
 * the row menu and their own surfaces (`RollbackDialog`, `ReleaseComparisonSheet`).
 */
export function ProjectDeployments() {
  const project = useProject()
  const router = useRouter()
  const { can } = useAuth()
  const { deployment, project: record } = project.detail
  const [filter, setFilter] = useState<StatusFilter>("all")
  const [environmentId, setEnvironmentId] = useState<number>()
  const [rollback, setRollback] = useState<{ open: boolean; releaseId?: number }>({ open: false })
  const [compare, setCompare] = useState<{ open: boolean; releaseId?: number }>({ open: false })
  const [pinningReleaseId, setPinningReleaseId] = useState<number>()

  const previews = usePoll(
    (signal) =>
      get<DeploymentPreview[]>(`/deploy/${project.projectId}/previews`, undefined, signal),
    30000,
    [project.projectId],
    { enabled: project.normalized && !project.archived },
  )

  const environments = useMemo(() => {
    const labels = new Map<number, string>()
    labels.set(project.environmentId, "Production")
    for (const preview of previews.data ?? []) {
      if (!labels.has(preview.environmentId)) {
        labels.set(preview.environmentId, `PR ${preview.providerRef}`)
      }
    }
    for (const run of project.runs) {
      if (!labels.has(run.environmentId))
        labels.set(run.environmentId, `Environment ${run.environmentId}`)
    }
    return [...labels.entries()].map(([id, label]) => ({ id, label }))
  }, [project.environmentId, previews.data, project.runs])

  const selectedEnv = environmentId ?? project.environmentId

  // The context poll always hands back the newest page; "Load older"
  // appends pages of its own, keyed to the filters they were fetched under
  // so switching environment or status starts back at page one instead of
  // showing a tail that belongs to a different query.
  const pageKey = `${selectedEnv}|${filter}`
  const [olderPage, setOlderPage] = useState<{
    key: string
    runs: DeploymentEngineRun[]
    nextBefore?: number
  }>()
  const [loadingOlder, setLoadingOlder] = useState(false)
  const older = olderPage?.key === pageKey ? olderPage : undefined

  const allRuns = useMemo(() => {
    if (!older) return project.runs
    const seen = new Set(project.runs.map((item) => item.id))
    return [...project.runs, ...older.runs.filter((item) => !seen.has(item.id))]
  }, [project.runs, older])

  const environmentRuns = allRuns.filter((run) => run.environmentId === selectedEnv)
  const runs = environmentRuns.filter((run) => matchesFilter(run, filter))
  // Before the first click the newest page's own cursor says whether older
  // runs exist at all, so a project with one run never offers to load more.
  const exhausted = older ? older.nextBefore === undefined : !project.hasOlderRuns

  const loadOlder = async () => {
    const cursor =
      older?.nextBefore ??
      (environmentRuns.length > 0 ? Math.min(...environmentRuns.map((run) => run.id)) : undefined)
    setLoadingOlder(true)
    try {
      const page = await get<DeploymentRunsPage>(`/deploy/${project.projectId}/runs`, {
        view: "engine",
        environment: selectedEnv,
        // "Ready" is the one status chip a single backend state names
        // exactly; the others are unions the filter cannot express, so an
        // older page for them comes back unfiltered and `matchesFilter`
        // narrows it client-side, same as the first page already does.
        state: filter === "ready" ? "succeeded" : undefined,
        before: cursor,
        limit: 30,
      })
      setOlderPage({
        key: pageKey,
        runs: [...(older?.runs ?? []), ...page.runs],
        nextBefore: page.nextBefore,
      })
    } catch (error) {
      notify.error("Could not load older deployments", error)
    } finally {
      setLoadingOlder(false)
    }
  }

  const togglePin = async (release: DeploymentRelease) => {
    const pinned = !release.pinned
    setPinningReleaseId(release.id)
    try {
      await put(
        `/deploy/${project.projectId}/environments/${project.environmentId}/releases/${release.id}/pin`,
        { pinned },
      )
      notify.success(pinned ? "Release pinned" : "Release unpinned")
      project.refresh()
    } catch (error) {
      notify.error(pinned ? "Could not pin release" : "Could not unpin release", error)
    } finally {
      setPinningReleaseId(undefined)
    }
  }

  const domains = useMemo(() => {
    const observed = project.operations?.domains.domains.map((domain) => domain.hostname)
    if (observed && observed.length > 0) return observed
    const url = deploymentURL(deployment.endpoint)
    const host = url && hostOf(url)
    return host ? [host] : []
  }, [project.operations, deployment.endpoint])

  const act = async (run: DeploymentEngineRun, verb: "cancel" | "retry") => {
    try {
      const result = await post<DeploymentEngineRun>(
        `/deploy/${project.projectId}/runs/${run.id}/${verb}`,
      )
      if (verb === "retry") router.push(`/deploy/${project.projectId}/runs/${result.id}`)
      else project.refresh()
    } catch (error) {
      notify.error(
        verb === "cancel" ? "Could not cancel deployment" : "Could not retry deployment",
        error,
      )
    }
  }

  const rowVerbs = (run: DeploymentEngineRun): Verb[] => {
    const release = project.releases.find((candidate) => candidate.id === run.releaseId)
    const isLive = Boolean(release) && release!.id === deployment.liveReleaseId
    const url = deploymentURL(deployment.endpoint)
    const verbs: Verb[] = [
      {
        key: "open",
        label: "Open deployment",
        detail: "The build log and release status for this run.",
        icon: External,
        run: () => router.push(`/deploy/${project.projectId}/runs/${run.id}`),
      },
    ]
    if (isLive && url) {
      verbs.push({
        key: "visit",
        label: "Visit",
        detail: "Open the live site in a new tab.",
        icon: External,
        run: () => window.open(url, "_blank", "noopener,noreferrer"),
      })
    }
    if (isLive && can("service.control")) {
      verbs.push({
        key: "redeploy",
        label: "Redeploy",
        detail: "Run this release again, unchanged.",
        icon: Play,
        run: () => void project.start("redeploy"),
      })
    }
    if (release && can("system.admin")) {
      verbs.push({
        key: "pin",
        label: release.pinned ? "Unpin release" : "Pin release",
        detail: release.pinned
          ? "Allow this release to be cleaned up automatically again."
          : "Keep this release from being cleaned up automatically.",
        icon: Pin,
        disabled: pinningReleaseId === release.id,
        run: () => void togglePin(release),
      })
    }
    if (
      release &&
      release.state === "retained" &&
      release.id !== deployment.liveReleaseId &&
      can("destructive")
    ) {
      verbs.push({
        key: "rollback",
        label: "Roll back to this release",
        detail: "Make this retained release live again.",
        icon: RotateCounterClockwise,
        run: () => setRollback({ open: true, releaseId: release.id }),
      })
    }
    if (release && release.id !== deployment.liveReleaseId) {
      verbs.push({
        key: "compare",
        label: "Compare with live",
        detail: "What changed between this release and the live one.",
        icon: Eye,
        run: () => setCompare({ open: true, releaseId: release.id }),
      })
    }
    if (isRetryable(run.state) && can("service.control")) {
      verbs.push({
        key: "retry",
        label: "Retry",
        detail: "Run this deployment again from the same source.",
        icon: RotateCounterClockwise,
        run: () => void act(run, "retry"),
      })
    }
    if (isCancellable(run.state) && can("service.control")) {
      verbs.push({
        key: "cancel",
        label: "Cancel",
        detail: "Stop this deployment before it finishes.",
        icon: StopCircle,
        run: () => void act(run, "cancel"),
      })
    }
    return verbs
  }

  return (
    <div className="space-y-6">
      {project.normalized && <Insights projectId={project.projectId} />}

      <div className="flex min-w-0 flex-wrap items-center gap-2">
        {STATUS_FILTERS.map((entry) => (
          <FilterChip
            key={entry.key}
            selected={filter === entry.key}
            onClick={() => setFilter(entry.key)}
          >
            {entry.label}
          </FilterChip>
        ))}
        {environments.length > 1 && (
          <Select
            value={String(selectedEnv)}
            onValueChange={(value) => setEnvironmentId(Number(value))}
          >
            <SelectTrigger size="sm" className="ml-auto w-44" aria-label="Environment">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {environments.map((entry) => (
                <SelectItem key={entry.id} value={String(entry.id)}>
                  {entry.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        )}
      </div>

      <Panel plain>
        <PanelHeader title="Deployments" />
        <PanelBody flush>
          {project.runsLoading && runs.length === 0 ? (
            <LoadingPanel rows={5} />
          ) : project.runs.length === 0 ? (
            <EmptyState
              icon={CloudUpload}
              title="No deployments yet"
              description="The first deployment will appear here as soon as its request is accepted."
              className="border-0 py-6"
            />
          ) : runs.length === 0 ? (
            <EmptyState
              title="No deployments match"
              description="Try another status, or clear the filters to see everything."
              action={
                <Button size="sm" variant="outline" onClick={() => setFilter("all")}>
                  Clear filters
                </Button>
              }
              className="border-0 py-6"
            />
          ) : (
            <RowList aria-label="Deployments">
              {runs.map((run) => {
                const release = project.releases.find((candidate) => candidate.id === run.releaseId)
                const isLive = Boolean(release) && release!.id === deployment.liveReleaseId
                return (
                  <Row
                    key={run.id}
                    // Not `href`/`onClick`: `Row` wraps its whole content in
                    // one `<a>`/`<button>`, and this row's trailing
                    // `VerbActions` nests a trigger button inside that same
                    // element — invalid HTML (a control cannot nest inside
                    // another), which React and the browser then disagree
                    // about on every re-render, detaching the dialog this
                    // menu opens mid-interaction. Design system §12 already
                    // names the fix (`TableRow`'s `onActivate`): the row
                    // itself stays inert and its title is the real link.
                    leading={
                      <span className="flex items-center gap-1.5">
                        <RunStatus state={run.state} />
                        <span className="numeric text-hint text-muted-foreground">
                          {formatDuration(runDurationSeconds(run))}
                        </span>
                      </span>
                    }
                    title={
                      <Link
                        href={`/deploy/${project.projectId}/runs/${run.id}`}
                        className="flex min-w-0 items-baseline gap-2 rounded-sm focus-ring hover:underline"
                      >
                        <span>{runTitle(run)}</span>
                        {runSubject(run) && (
                          <span className="min-w-0 truncate font-normal text-muted-foreground">
                            {runSubject(run)}
                          </span>
                        )}
                      </Link>
                    }
                    subtitle={<RunSubtitle run={run} />}
                    trailing={
                      <>
                        {isLive && <Tag tone="success">Live</Tag>}
                        {release?.pinned && <Tag>Pinned</Tag>}
                        {run.environmentId !== project.environmentId && (
                          <Tag>
                            {environments.find((entry) => entry.id === run.environmentId)?.label ??
                              "Preview"}
                          </Tag>
                        )}
                        <VerbActions
                          reveal
                          verbs={rowVerbs(run)}
                          menuLabel={`Actions for ${runTitle(run)}`}
                        />
                      </>
                    }
                  />
                )
              })}
            </RowList>
          )}
        </PanelBody>
        {project.runs.length > 0 && !exhausted && (
          <PanelFooter>
            <Button
              size="sm"
              variant="outline"
              disabled={loadingOlder}
              pending={loadingOlder}
              onClick={() => void loadOlder()}
            >
              Load older deployments
            </Button>
          </PanelFooter>
        )}
      </Panel>

      {!project.normalized && (
        <LegacyRecovery
          projectId={project.projectId}
          projectName={record.name}
          currentSha={record.currentSha}
          active={Boolean(deployment.activeRun)}
          canRollback={can("destructive")}
        />
      )}

      <RollbackDialog
        open={rollback.open}
        onOpenChange={(open) => setRollback((current) => ({ ...current, open }))}
        projectId={project.projectId}
        environmentId={project.environmentId}
        liveRelease={project.liveRelease}
        releases={project.releases}
        runs={project.runs}
        domains={domains}
        initialReleaseId={rollback.releaseId}
      />
      <ReleaseComparisonSheet
        open={compare.open}
        onOpenChange={(open) => setCompare((current) => ({ ...current, open }))}
        projectId={project.projectId}
        environmentId={project.environmentId}
        releaseId={deployment.liveReleaseId}
        fromReleaseId={compare.releaseId}
      />
    </div>
  )
}

/**
 * The legacy compose pipeline has no immutable releases to roll back to, only
 * the commits it built from. Rolling back means `git reset --hard` on the
 * server's checkout and rebuilding — rare, and not reversible the way an
 * immutable release swap is — so it is the one rollback in this rebuild that
 * asks for a typed phrase rather than the plain confirmation the dialog above
 * uses (design-system confirm-dialog: typing is for the rare and unrecoverable).
 */
function LegacyRecovery({
  projectId,
  projectName,
  currentSha,
  active,
  canRollback,
}: {
  projectId: number
  projectName: string
  currentSha?: string
  active: boolean
  canRollback: boolean
}) {
  const router = useRouter()
  const { confirm, dialog } = useConfirm()
  const commits = usePoll(
    (signal) => get<DeployCommit[]>(`/deploy/${projectId}/commits`, { limit: 25 }, signal),
    0,
    [projectId],
  )
  return (
    <Panel plain>
      <PanelHeader title="Recovery" />
      <PanelBody flush>
        {commits.loading && !commits.data ? (
          <LoadingRows rows={3} className="p-4" />
        ) : commits.error ? (
          <ErrorState error={commits.error} className="m-4" />
        ) : commits.data && commits.data.length === 0 ? (
          <EmptyNote>No recoverable commits were found.</EmptyNote>
        ) : (
          <RowList aria-label="Recoverable commits">
            {commits.data?.map((commit) => (
              <Row
                key={commit.sha}
                title={commit.subject}
                subtitle={
                  <>
                    <span className="font-mono">{commit.short}</span> · {commit.author} ·{" "}
                    {relativeTime(commit.date)}
                  </>
                }
                trailing={
                  commit.sha === currentSha ? (
                    <Status tone="running" label="Current" />
                  ) : canRollback ? (
                    <Button
                      size="sm"
                      variant="outline"
                      disabled={active}
                      title={
                        active
                          ? "Wait for the active deployment to finish or cancel it first."
                          : undefined
                      }
                      onClick={() =>
                        confirm({
                          title: "Roll back",
                          confirmLabel: "Roll back",
                          phrase: commit.short,
                          description: (
                            <>
                              <p>
                                <b>{projectName}</b> will rebuild {commit.short}. The current
                                workload remains the recovery point until activation.
                              </p>
                              <p className="text-xs text-muted-foreground">{commit.subject}</p>
                            </>
                          ),
                          action: async (confirmation) => {
                            const result = await post<{ runId: number }>(
                              `/deploy/${projectId}/rollback`,
                              { commit: commit.sha },
                              { confirm: confirmation },
                            )
                            router.push(`/deploy/${projectId}/runs/${result.runId}`)
                          },
                        })
                      }
                    >
                      <RotateCounterClockwise className="size-3.5" /> Roll back
                    </Button>
                  ) : null
                }
              />
            ))}
          </RowList>
        )}
      </PanelBody>
      {dialog}
    </Panel>
  )
}
