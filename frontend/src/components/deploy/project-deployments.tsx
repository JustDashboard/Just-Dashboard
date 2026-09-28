"use client"

import { useMemo, useRef, useState } from "react"
import { useSessionState } from "@/lib/view-state"
import { useRouter } from "next/navigation"
import { GitPullRequest, Globe, Pin, RotateCounterClockwise } from "@/components/icons"
import { get, post, put } from "@/lib/api"
import { relativeTime } from "@/lib/format"
import { notify } from "@/lib/toast"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import type {
  DeployCommit,
  DeploymentEngineRun,
  DeploymentPreview,
  DeploymentRelease,
  DeploymentRunSettingsDrift,
  DeploymentRunState,
  DeploymentRunsPage,
} from "@/lib/types"
import { Panel, PanelBody, PanelFooter, PanelHeader, PanelToolbar } from "@/components/panel"
import { Row, RowList } from "@/components/row-list"
import { ChoiceList, GroupRule } from "@/components/flow"
import { EmptyNote, EmptyState, ErrorState, LoadingPanel, LoadingRows } from "@/components/state"
import { ChipCount, ChipStrip, FilterChip } from "@/components/tabs"
import { Tag } from "@/components/tag"
import { Status } from "@/components/status-dot"
import { VerbActions } from "@/components/verbs"
import { useConfirm } from "@/components/confirm-dialog"
import {
  ProductGlyph,
  ProductLogo,
  gitProviderProduct,
  hostProduct,
} from "@/components/product-logo"
import { AuthorMark, ShortSha } from "@/components/git/marks"
import { Button } from "@/components/ui/button"
import { TextShimmer } from "@/components/ui/text-shimmer"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { useProject } from "@/components/deploy/project-context"
import {
  deploymentURL,
  hostOf,
  isActiveRun,
  isRetryable,
  runDurationSeconds,
  runFailed,
  runTitle,
  useNow,
} from "@/components/deploy/vocabulary"
import { Insights } from "@/components/deploy/insights"
import { deployWithCurrentSettings, runPlanIsStale } from "@/components/deploy/failure-cause"
import { RollbackDialog } from "@/components/deploy/rollback-dialog"
import { ReleaseComparisonSheet } from "@/components/deploy/release-comparison-sheet"
import { ProjectMark } from "@/components/deploy/project-mark"
import { RunRow } from "@/components/deploy/run-row"
import { useCheckedDeploy } from "@/components/deploy/deploy-check"
import { RunStrip } from "@/components/deploy/run-marks"
import { releaseVerbs, runVerbs, type RunVerbKey } from "@/components/deploy/run-verbs"

type StatusFilter = "all" | "ready" | "failed" | "cancelled"

const STATUS_FILTERS: { key: StatusFilter; label: string }[] = [
  { key: "all", label: "All" },
  { key: "ready", label: "Ready" },
  { key: "failed", label: "Failed" },
  { key: "cancelled", label: "Cancelled" },
]

/**
 * A verb pressed on a row: its participle, and what the row read when it was
 * pressed, so the participle holds until the poll brings the change rather
 * than only until the request returns.
 */
type Working = {
  verb: RunVerbKey
  word: string
  state: DeploymentRunState
  cancelRequested: boolean
  pinned?: boolean
  /** The request has been answered; now waiting on the poll. */
  sent?: boolean
}

function matchesFilter(run: DeploymentEngineRun, filter: StatusFilter) {
  if (filter === "all") return true
  if (filter === "ready") return run.state === "succeeded"
  if (filter === "failed") return runFailed(run.state)
  return run.state === "cancelled" || run.state === "superseded"
}

/** "Today", "Yesterday", "Sep 2" — the day a run was asked for, as a rule over its group. */
function dayLabel(iso: string, now: number) {
  const day = new Date(iso)
  const today = new Date(now)
  const start = (date: Date) =>
    new Date(date.getFullYear(), date.getMonth(), date.getDate()).getTime()
  const days = Math.round((start(today) - start(day)) / 86_400_000)
  if (days === 0) return "Today"
  if (days === 1) return "Yesterday"
  return day.toLocaleDateString(undefined, {
    month: "short",
    day: "numeric",
    year: day.getFullYear() === today.getFullYear() ? undefined : "numeric",
  })
}

/**
 * The runs still going, then the rest under the day each was asked for. Each
 * group knows where it starts in the whole list, for the arrival stagger.
 */
function groupRuns(runs: DeploymentEngineRun[], now: number) {
  const groups: { label: string; start: number; runs: DeploymentEngineRun[] }[] = []
  const active = runs.filter((run) => isActiveRun(run.state))
  if (active.length > 0) groups.push({ label: "In progress", start: 0, runs: active })
  let position = active.length
  for (const run of runs) {
    if (isActiveRun(run.state)) continue
    const label = dayLabel(run.requestedAt, now)
    const last = groups.at(-1)
    if (last && last.label === label) last.runs.push(run)
    else groups.push({ label, start: position, runs: [run] })
    position += 1
  }
  return groups
}

/**
 * How long each finished run took against the others listed: its share of the
 * longest, and whether it took more than twice the middle one — which is when
 * a build is worth a second look, where "longer than the p95" of a dozen runs
 * would only ever name the longest.
 */
function durationScale(runs: DeploymentEngineRun[]) {
  const finished = runs
    .filter((run) => run.endedAt)
    .map((run) => runDurationSeconds(run) ?? 0)
    .sort((a, b) => a - b)
  const longest = finished.at(-1) ?? 0
  const median = finished.length ? finished[Math.floor(finished.length / 2)] : 0
  return (run: DeploymentEngineRun) => {
    if (!run.endedAt || longest <= 0) return undefined
    const seconds = runDurationSeconds(run) ?? 0
    return {
      share: (seconds / longest) * 100,
      slow: finished.length >= 5 && median > 0 && seconds > median * 2,
    }
  }
}

/**
 * The project's release history: delivery figures over the run history, then
 * every run as a card you open.
 *
 * Each run is drawn as itself (`RunRow`): who or what started it as their face
 * or their product, the run's name with its commit's subject, where it came
 * from as a branch, a commit and an author, and how it went, in fixed measures
 * so a column of runs is a column — with a light running round the one in
 * flight, and the release path's own bar beside it with the stage at work
 * lit. They were reading rows with a status word jammed in front of the
 * name, so every title started somewhere else and nothing said the row led
 * anywhere (§15 pass 3). The live release is a state, and it is the run's:
 * its row reads "Live" where the others read "Ready", since a live release is
 * ready by definition; a pinned one is a tag.
 *
 * They are grouped the way a history is read: what is in flight, then day by
 * day. The header carries the loaded runs' outcomes as a strip, and the
 * status chips count what they would show. A reason in the delivery figures
 * narrows the list to the failed runs.
 *
 * Each run's verbs are declared once (`run-verbs.tsx`) and drawn behind its
 * menu; rolling back and comparing open their own surfaces
 * (`RollbackDialog`, `ReleaseComparisonSheet`), and a verb in flight is said
 * on its row in the present participle until the poll shows what it changed
 * (§13).
 */
export function ProjectDeployments() {
  const project = useProject()
  const router = useRouter()
  const { can } = useAuth()
  const checkedDeploy = useCheckedDeploy(project.projectId)
  const { deployment, project: record } = project.detail
  const [filter, setFilter] = useSessionState<StatusFilter>(
    `deploy.${project.projectId}.deployments.filter`,
    "all",
  )
  const [environmentId, setEnvironmentId] = useSessionState<number | undefined>(
    `deploy.${project.projectId}.deployments.environment`,
    undefined,
  )
  const [rollback, setRollback] = useState<{ open: boolean; releaseId?: number }>({ open: false })
  const [compare, setCompare] = useState<{
    open: boolean
    releaseId?: number
    fromReleaseId?: number
  }>({ open: false })
  // Keyed by run, so a verb on one row neither replaces nor clears another's.
  const [working, setWorking] = useState<Record<number, Working>>({})
  const list = useRef<HTMLDivElement>(null)

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
  // A pull request's preview came through the forge that holds the repository.
  const forge = gitProviderProduct(hostProduct(deployment.sourceRemote))

  // A remembered preview environment may have been closed since; production
  // is the answer then, not an empty list under a name nobody can pick.
  const selectedEnv =
    environmentId !== undefined && environments.some((entry) => entry.id === environmentId)
      ? environmentId
      : project.environmentId

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
  // The newest run, when it can be retried, is the one a fix was saved for.
  // Its drift counts what no plan revision does — a variable given another
  // scope — and says whether the source itself moved; older rows go by the
  // plan revision alone.
  const newest = environmentRuns[0]
  const retryable = newest && isRetryable(newest.state) ? newest : undefined
  const drift = usePoll(
    (signal) =>
      get<DeploymentRunSettingsDrift>(
        `/deploy/${project.projectId}/runs/${retryable?.id}/settings-drift`,
        undefined,
        signal,
      ),
    0,
    [project.projectId, retryable?.id, deployment.desiredRevision],
    { enabled: retryable !== undefined && can("service.control") },
  )
  const driftOf = (run: DeploymentEngineRun) =>
    drift.data?.runId === run.id ? drift.data : undefined
  const runs = environmentRuns.filter((run) => matchesFilter(run, filter))
  const counts = Object.fromEntries(
    STATUS_FILTERS.map((entry) => [
      entry.key,
      environmentRuns.filter((run) => matchesFilter(run, entry.key)).length,
    ]),
  ) as Record<StatusFilter, number>
  const now = useNow(60_000)
  const groups = groupRuns(runs, now)
  const meter = durationScale(environmentRuns)
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

  const press = (run: DeploymentEngineRun, entry: Pick<Working, "verb" | "word" | "pinned">) =>
    setWorking((current) => ({
      ...current,
      [run.id]: { ...entry, state: run.state, cancelRequested: run.cancelRequested },
    }))
  const answered = (id: number) =>
    setWorking((current) =>
      current[id] ? { ...current, [id]: { ...current[id], sent: true } } : current,
    )
  const settle = (id: number) =>
    setWorking((current) => {
      const next = { ...current }
      delete next[id]
      return next
    })
  // A pressed verb's participle, until the request fails or the poll shows
  // what it changed.
  const pendingOf = (run: DeploymentEngineRun, release?: DeploymentRelease) => {
    const entry = working[run.id]
    if (!entry || !entry.sent) return entry
    const moved =
      entry.verb === "pin"
        ? release?.pinned !== entry.pinned
        : run.state !== entry.state || run.cancelRequested !== entry.cancelRequested
    return moved ? undefined : entry
  }

  const togglePin = async (run: DeploymentEngineRun, release: DeploymentRelease) => {
    const pinned = !release.pinned
    press(run, { verb: "pin", word: pinned ? "Pinning…" : "Unpinning…", pinned: release.pinned })
    try {
      await put(
        `/deploy/${project.projectId}/environments/${project.environmentId}/releases/${release.id}/pin`,
        { pinned },
      )
      notify.success(pinned ? "Release pinned" : "Release unpinned")
      answered(run.id)
      project.refresh()
    } catch (error) {
      notify.error(pinned ? "Could not pin release" : "Could not unpin release", error)
      settle(run.id)
    }
  }

  const domains = useMemo(() => {
    const observed = project.operations?.domains.domains.map((domain) => domain.hostname)
    if (observed && observed.length > 0) return observed
    const url = deploymentURL(deployment.endpoint)
    const host = url && hostOf(url)
    return host ? [host] : []
  }, [project.operations, deployment.endpoint])

  const deployCurrent = async (run: DeploymentEngineRun) => {
    press(run, { verb: "deploy", word: "Starting…" })
    const request = deployWithCurrentSettings(run, deployment, driftOf(run))
    const deploy = async () => {
      try {
        const created = await post<DeploymentEngineRun>(
          `/deploy/${project.projectId}/environments/${run.environmentId}/runs`,
          request,
        )
        answered(run.id)
        router.push(`/deploy/${project.projectId}/runs/${created.id}`)
      } catch (error) {
        notify.error("Could not start deployment", error)
        settle(run.id)
      }
    }
    const asked = await checkedDeploy.start(
      run.environmentId,
      "sourceRevision" in request ? { sourceRevision: request.sourceRevision } : {},
      deploy,
    )
    // A check that asks leaves the press to its dialog.
    if (asked) settle(run.id)
  }

  const act = async (run: DeploymentEngineRun, verb: "cancel" | "retry") => {
    press(run, { verb, word: verb === "cancel" ? "Cancelling…" : "Starting…" })
    try {
      const result = await post<DeploymentEngineRun>(
        `/deploy/${project.projectId}/runs/${run.id}/${verb}`,
      )
      answered(run.id)
      if (verb === "retry") router.push(`/deploy/${project.projectId}/runs/${result.id}`)
      else project.refresh()
    } catch (error) {
      notify.error(
        verb === "cancel" ? "Could not cancel deployment" : "Could not retry deployment",
        error,
      )
      settle(run.id)
    }
  }

  const url = deploymentURL(deployment.endpoint)
  const releaseOf = (run: DeploymentEngineRun) =>
    project.releases.find((candidate) => candidate.id === run.releaseId)
  const verbsFor = (run: DeploymentEngineRun, busy?: RunVerbKey) => [
    ...runVerbs({
      run,
      release: releaseOf(run),
      liveReleaseId: deployment.liveReleaseId,
      url,
      can,
      working: busy,
      stale: driftOf(run)?.changed ?? runPlanIsStale(run, deployment),
      on: {
        open: () => router.push(`/deploy/${project.projectId}/runs/${run.id}`),
        visit: () => window.open(url, "_blank", "noopener,noreferrer"),
        redeploy: () => void project.start("redeploy"),
        retry: () => void act(run, "retry"),
        cancel: () => void act(run, "cancel"),
        deploy: () => void deployCurrent(run),
      },
    }),
    ...releaseVerbs({
      release: releaseOf(run),
      liveReleaseId: deployment.liveReleaseId,
      can,
      working: busy,
      on: {
        changes: (release) => setCompare({ open: true, releaseId: release.id }),
        compare: (release) =>
          setCompare({
            open: true,
            releaseId: deployment.liveReleaseId,
            fromReleaseId: release.id,
          }),
        rollback: (release) => setRollback({ open: true, releaseId: release.id }),
        pin: (release) => void togglePin(run, release),
      },
    }),
  ]
  // Where each run came from: the environment's live source, or — before a
  // release has gone live and fixed one — the branch the project follows.
  const source = { ...deployment, sourceRef: deployment.sourceRef || record.branch }

  // Production is the public one; the project's own favicon here would only
  // repeat the header's mark. A preview is the forge its pull request is on.
  const environmentMark = (id: number) =>
    id === project.environmentId ? (
      <Globe aria-hidden className="size-3.5 text-muted-foreground" />
    ) : forge ? (
      <ProductGlyph id={forge} />
    ) : (
      <GitPullRequest aria-hidden className="size-3.5 text-muted-foreground" />
    )

  return (
    <div className="space-y-8">
      {checkedDeploy.gate}
      {project.normalized && (
        <Insights
          projectId={project.projectId}
          onFailureClick={() => {
            setFilter("failed")
            list.current?.scrollIntoView({ block: "start", behavior: "smooth" })
          }}
        />
      )}

      <div ref={list} className="scroll-mt-6">
        <Panel plain>
          <PanelHeader
            title="Deployments"
            actions={
              <>
                <span className="max-sm:hidden">
                  <RunStrip runs={environmentRuns.slice(0, 20)} />
                </span>
                {environments.length > 1 && (
                  <Select
                    value={String(selectedEnv)}
                    onValueChange={(value) => setEnvironmentId(Number(value))}
                  >
                    <SelectTrigger size="sm" className="w-40 sm:ml-2" aria-label="Environment">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      {environments.map((entry) => (
                        <SelectItem key={entry.id} value={String(entry.id)}>
                          {environmentMark(entry.id)}
                          {entry.label}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                )}
              </>
            }
          />
          <PanelToolbar>
            <ChipStrip>
              {STATUS_FILTERS.map((entry) => (
                <FilterChip
                  key={entry.key}
                  selected={filter === entry.key}
                  // A filter that would show nothing is drawn the way the build
                  // console's Errors chip is: there, and not pressable.
                  disabled={counts[entry.key] === 0 && filter !== entry.key}
                  className="disabled:opacity-50"
                  onClick={() => setFilter(entry.key)}
                >
                  {entry.label} <ChipCount>{counts[entry.key]}</ChipCount>
                </FilterChip>
              ))}
            </ChipStrip>
          </PanelToolbar>
          <PanelBody flush className="pt-4">
            {project.runsLoading && runs.length === 0 ? (
              <LoadingPanel rows={5} plain />
            ) : project.runs.length === 0 ? (
              <EmptyState
                mark={<ProjectMark deployment={deployment} size="md" />}
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
              <div className="space-y-5">
                {groups.map((group) => (
                  <div key={group.label} className="space-y-2.5">
                    {/* A day's count beside its date reads as part of it
                        ("Sep 1 3"); only what is in flight is counted. */}
                    <GroupRule
                      label={group.label}
                      count={group.label === "In progress" ? group.runs.length : undefined}
                    />
                    <ChoiceList aria-label={group.label}>
                      {group.runs.map((run, offset) => {
                        const release = releaseOf(run)
                        const isLive = Boolean(release) && release!.id === deployment.liveReleaseId
                        const pending = pendingOf(run, release)
                        const preview =
                          run.environmentId !== project.environmentId &&
                          (environments.find((entry) => entry.id === run.environmentId)?.label ??
                            "Preview")
                        return (
                          <RunRow
                            key={run.id}
                            run={run}
                            deployment={source}
                            release={release}
                            live={isLive}
                            index={group.start + offset}
                            meter={meter(run)}
                            tags={
                              <>
                                {pending && (
                                  <TextShimmer className="text-hint">{pending.word}</TextShimmer>
                                )}
                                {release?.pinned && <Tag icon={Pin}>Pinned</Tag>}
                                {preview && <Tag>{preview}</Tag>}
                              </>
                            }
                            actions={
                              <VerbActions
                                dim
                                verbs={verbsFor(run, pending?.verb)}
                                menuLabel={`Actions for ${runTitle(run)}`}
                              />
                            }
                          />
                        )
                      })}
                    </ChoiceList>
                  </div>
                ))}
              </div>
            )}
          </PanelBody>
          {project.runs.length > 0 && !exhausted && (
            <PanelFooter className="mt-4">
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
      </div>

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
        remote={deployment.sourceRemote}
        initialReleaseId={rollback.releaseId}
      />
      <ReleaseComparisonSheet
        open={compare.open}
        onOpenChange={(open) => setCompare((current) => ({ ...current, open }))}
        projectId={project.projectId}
        environmentId={project.environmentId}
        releaseId={compare.releaseId}
        fromReleaseId={compare.fromReleaseId}
        releases={project.releases}
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
 *
 * These rows are readings with a button, not destinations, so they stay a
 * `RowList`; each commit is drawn as a forge draws one — its sha, then its
 * author's square in their own hue.
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
          <LoadingRows rows={3} className="py-4" />
        ) : commits.error ? (
          <ErrorState error={commits.error} />
        ) : commits.data && commits.data.length === 0 ? (
          <EmptyNote>No recoverable commits were found.</EmptyNote>
        ) : (
          <RowList aria-label="Recoverable commits">
            {commits.data?.map((commit) => (
              <Row
                key={commit.sha}
                title={commit.subject}
                subtitle={
                  <span className="flex min-w-0 items-center gap-1.5">
                    <ShortSha sha={commit.short} />
                    <AuthorMark name={commit.author} />
                    <span className="truncate max-sm:hidden">{commit.author}</span>
                    <span className="shrink-0">· {relativeTime(commit.date)}</span>
                  </span>
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
                          subject: {
                            mark: <ProductLogo id="git" size="sm" />,
                            name: commit.subject,
                            facts: <ShortSha sha={commit.short} />,
                          },
                          description: (
                            <p>
                              <b>{projectName}</b> will rebuild {commit.short}. The current workload
                              remains the recovery point until activation.
                            </p>
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
