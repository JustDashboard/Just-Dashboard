"use client"

import { useEffect, useRef, useState } from "react"
import Link from "next/link"
import { useRouter } from "next/navigation"
import { useSessionState } from "@/lib/view-state"
import {
  ArrowRight,
  CheckCircle,
  Clock,
  CrossCircle,
  External,
  Globe,
  LockClosed,
  PaperAirplane,
  Play,
  Plus,
  RotateCounterClockwise,
  Slash,
  StopCircle,
} from "@/components/icons"
import { errorMessage, get, post } from "@/lib/api"
import { notify } from "@/lib/toast"
import { relativeTime } from "@/lib/format"
import {
  canTest,
  cleanupFailed,
  previewHeld,
  previewOutOfDate,
  previewStatus,
} from "@/lib/pull-requests"
import { cn } from "@/lib/utils"
import type {
  DeploymentEngineRun,
  DeploymentPreview,
  GitBranch,
  GitComparison,
  GitHubIssue,
  GitHubRepo,
  GitHubStatus,
  GitHubWorkflowRun,
  GitPullRequest as PR,
  GitPullRequestSummary,
} from "@/lib/types"
import { usePoll, type PollState } from "@/hooks/use-poll"
import type { ConfirmRequest } from "@/components/confirm-dialog"
import { CommentDialog } from "@/components/git/comment-dialog"
import { IssueMark, SourceBranch, SourceMerge, SourcePull } from "@/components/git/glyphs"
import { BranchChip, ForgeFace, ShortSha } from "@/components/git/marks"
import { ChecksMark, CommentCount, PullStateMark, ReviewMark } from "@/components/git/pull-state"
import { MergePullDialog } from "@/components/git/merge-pull-dialog"
import type { GitPreview } from "@/components/git/preview-panel"
import { openPreview } from "@/components/git/pull-request-row"
import type { GitRun } from "@/components/git/run"
import { TestPullDialog } from "@/components/git/test-pull-dialog"
import { EmptyState, ErrorState, LoadingRows, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { Modal } from "@/components/modal"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import { Field, FormFacts, FormFact, OptionList, OptionRow } from "@/components/form"
import { ChipCount, FilterChip } from "@/components/tabs"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Textarea } from "@/components/ui/textarea"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { rowReveal } from "@/components/icon-action"
import { VerbActions, type Verb } from "@/components/verbs"

type PullState = "open" | "merged" | "closed"
type Deployment = GitPullRequestSummary["repos"][number]["deployments"][number]

/**
 * What GitHub knows about this repository: its pull requests, with their
 * review and check state on the row and, where a deploy project builds
 * previews from them, where the preview stands; the open issues, with a box
 * to answer one; the Actions runs on the current branch, because "did CI
 * pass on what I just pushed" is asked beside the push button; and the
 * button that opens a pull request from the branch that is checked out.
 *
 * It is a tab beside Changes and History because that is the sequence the
 * work actually takes — stage, commit, push, propose, merge — and the last
 * steps used to be the ones that sent you to a browser tab and a different
 * mental model.
 *
 * The previews come from the pull-request summary rather than from gh's
 * listing: gh knows nothing of deploy projects, and the summary is the one
 * read that joins a checkout to the projects deploying it. Testing a pull
 * request and closing its preview address those projects.
 */
export function GitHubPanel({
  repoPath,
  branch,
  github,
  summary,
  busy,
  canControl,
  canAdmin,
  run,
  confirm,
  onSelect,
  active,
  activePull,
  onChanged,
}: {
  repoPath: string
  branch: string
  github?: GitHubStatus
  /** The checkout's open requests joined to their previews, read by the workspace. */
  summary: PollState<GitPullRequestSummary>
  busy?: string
  canControl: boolean
  canAdmin: boolean
  run: GitRun
  confirm: (request: ConfirmRequest) => void
  onSelect: (p: GitPreview) => void
  active?: string
  /** The pull request the address bar opened the workspace on: scrolled into view once listed. */
  activePull?: number
  onChanged: () => void
}) {
  const router = useRouter()
  const [creating, setCreating] = useState(false)
  const [merging, setMerging] = useState<PR | null>(null)
  const [testing, setTesting] = useState<{ pull: PR; deployment: Deployment }>()
  const [choosing, setChoosing] = useState<PR>()
  const [commenting, setCommenting] = useState<{ number: number; title: string }>()
  // A cleanup retry in flight, so no row offers a second one until it answers.
  const [retrying, setRetrying] = useState(false)
  const [state, setState] = useSessionState<PullState>("git.github.pulls", "open")
  const signedIn = Boolean(github?.available && github.account?.loggedIn)
  const listRef = useRef<HTMLUListElement>(null)

  const repo = usePoll(
    (signal) => get<GitHubRepo>("/git/github/repo", { path: repoPath }, signal),
    300_000,
    [repoPath],
    { enabled: signedIn },
  )
  const pulls = usePoll(
    (signal) => get<PR[]>("/git/github/pulls", { path: repoPath, state }, signal),
    60_000,
    [repoPath, state],
    { enabled: signedIn },
  )
  const runs = usePoll(
    (signal) =>
      get<GitHubWorkflowRun[]>(
        "/git/github/runs",
        { path: repoPath, branch: branch || undefined, limit: 8 },
        signal,
      ),
    60_000,
    [repoPath, branch],
    { enabled: signedIn && Boolean(branch) },
  )
  const issues = usePoll(
    (signal) =>
      get<GitHubIssue[]>(
        "/git/github/issues",
        { path: repoPath, state: "open", limit: 10 },
        signal,
      ),
    120_000,
    [repoPath],
    { enabled: signedIn },
  )

  // The row the address bar named, brought into view once the list has it.
  // Only that: the preview column already opened on it.
  const listed = pulls.data?.some((p) => p.number === activePull) ?? false
  useEffect(() => {
    if (!activePull || !listed) return
    listRef.current
      ?.querySelector(`[data-pull="${activePull}"]`)
      ?.scrollIntoView({ block: "nearest" })
  }, [activePull, listed])

  // The sign-in state decides everything below it, so it is waited for rather
  // than guessed at: rendering "no open pull requests" while the answer is
  // still in flight reads as an answer.
  if (!github) return <LoadingRows className="p-3" rows={4} />
  if (!github.available) {
    return (
      <EmptyState
        className="m-3"
        icon={SourcePull}
        title="The GitHub CLI is not installed"
        description="Pull requests and workflow runs come through gh. Install it on this host to use them from here."
      />
    )
  }
  if (!signedIn) {
    return (
      <EmptyState
        className="m-3"
        icon={SourcePull}
        title="Not signed in to GitHub"
        description="Sign in from the header above to see and open pull requests for this repository."
      />
    )
  }

  const list = pulls.data ?? []
  const mine = state === "open" ? list.find((p) => p.head === branch) : undefined
  const q = { path: repoPath }
  // The summary answers `[]` for a checkout gh cannot read, and one entry
  // otherwise; either way it is the one place a preview and a project meet.
  const joined = summary.data?.repos?.[0]
  const deployments = joined?.deployments ?? []
  const previewOf = (number: number): DeploymentPreview | undefined =>
    joined?.pulls.find((p) => p.number === number)?.preview ?? undefined
  const refreshPulls = () => {
    pulls.refresh()
    summary.refresh()
  }

  // A checkout deployed by several projects can only be acted on against the
  // one the preview names; without it, the single project is the answer.
  const projectOf = (preview: DeploymentPreview) =>
    preview.projectId ?? (deployments.length === 1 ? deployments[0].projectId : undefined)

  const closePreview = (p: PR, preview: DeploymentPreview) => {
    const projectId = projectOf(preview)
    if (!projectId) return
    confirm({
      title: `Close the preview of #${p.number}`,
      description:
        "Its container, volumes and tailnet address are removed and the variables copied from production are dropped. Production is untouched, and the pull request stays open.",
      confirmLabel: "Close preview",
      // The dialog says "… completed" itself once the post answers; a toast
      // of this action's own announced the same close a second time.
      action: async () => {
        await post(`/deploy/${projectId}/pull-requests/${p.number}/preview/close`)
      },
      onDone: refreshPulls,
    })
  }

  // A failed removal is retried as the run it was, through the run's own
  // retry route, as the Overview retries it: the close route would only find
  // the preview already closed and point back at this run.
  const retryCleanup = async (
    projectId: number,
    previewRun: NonNullable<DeploymentPreview["lastRun"]>,
  ) => {
    setRetrying(true)
    try {
      const created = await post<DeploymentEngineRun>(
        `/deploy/${projectId}/runs/${previewRun.id}/retry`,
        {},
      )
      refreshPulls()
      router.push(`/deploy/${projectId}/runs/${created.id}`)
    } catch (error) {
      notify.error("Could not retry the cleanup", error)
      setRetrying(false)
    }
  }

  const verbsFor = (p: PR): Verb[] => {
    const preview = previewOf(p.number)
    const verbs: Verb[] = []
    // Merge first and the removal last: the order a menu is read in is the
    // order the verbs are wanted in, and the one that takes something away
    // sits behind its rule at the end.
    if (canControl && p.state === "open") {
      verbs.push({
        key: "merge",
        label: "Merge",
        icon: SourceMerge,
        disabled: !!busy || p.draft,
        run: () => setMerging(p),
      })
    }
    // Held while its preview is ready at this commit, building, closing or
    // waiting on a failed cleanup: the Overview's rule, so pressing it here
    // never announces a build the route would not run.
    if (canAdmin && deployments.length > 0 && canTest(p) && !previewHeld(preview)) {
      verbs.push({
        key: "test",
        label: previewOutOfDate(preview) ? "Update preview" : "Test this pull request",
        icon: Play,
        disabled: !!busy,
        run: () =>
          deployments.length === 1
            ? setTesting({ pull: p, deployment: deployments[0] })
            : setChoosing(p),
      })
    }
    if (preview?.lastRun && cleanupFailed(preview) && canControl) {
      const projectId = projectOf(preview)
      const previewRun = preview.lastRun
      if (projectId)
        verbs.push({
          key: "retry-cleanup",
          label: "Retry cleanup",
          icon: RotateCounterClockwise,
          disabled: !!busy || retrying,
          run: () => void retryCleanup(projectId, previewRun),
        })
    }
    if (preview?.state === "open" && preview.address?.published) {
      verbs.push({
        key: "preview",
        label: "Open preview",
        icon: External,
        run: () => openPreview(preview),
      })
    }
    verbs.push({
      key: "open",
      label: "Open on GitHub",
      icon: External,
      run: () => window.open(p.url, "_blank", "noopener"),
    })
    if (canControl && p.state === "open") {
      verbs.push({
        key: "checkout",
        label: "Check out the branch",
        icon: SourceBranch,
        disabled: !!busy,
        run: () =>
          void run(`Checked out ${p.head}`, () =>
            post(`/git/github/pulls/${p.number}/checkout`, undefined, { query: q }).then(() => ({
              command: "gh pr checkout",
              output: `On ${p.head}`,
              ok: true,
            })),
          ).catch(() => undefined),
      })
    }
    if (canAdmin && preview?.state === "open") {
      verbs.push({
        key: "close",
        label: "Close preview",
        icon: StopCircle,
        danger: true,
        disabled: !!busy,
        run: () => closePreview(p, preview),
      })
    }
    return verbs
  }

  const owner = repo.data?.nameWithOwner.split("/")[0]

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      {/* The repository on GitHub, as GitHub heads it: its owner's face and
          its name, whether the world can see it, what it merges into, and
          what this account may do there. */}
      {repo.data && (
        <div className="flex shrink-0 flex-wrap items-center gap-x-2 gap-y-1 border-b border-hairline px-3 py-2 text-hint text-muted-foreground">
          <ForgeFace login={owner} provider="github" size="xs" />
          <a
            href={repo.data.url}
            target="_blank"
            rel="noreferrer"
            className="min-w-0 truncate text-body font-medium text-foreground hover:underline"
          >
            {repo.data.nameWithOwner}
          </a>
          <span className="inline-flex items-center gap-1">
            {repo.data.private ? (
              <LockClosed aria-hidden className="size-3" />
            ) : (
              <Globe aria-hidden className="size-3" />
            )}
            {repo.data.private ? "private" : "public"}
          </span>
          {repo.data.permission && <Tag>{repo.data.permission.toLowerCase()}</Tag>}
          <span className="basis-full" />
          <span className="inline-flex min-w-0 items-center gap-1">
            default
            <BranchChip branch={repo.data.defaultBranch} />
          </span>
          {/* The projects built from this repository, as the way across to
              them: the Overview links back here the same way. */}
          {deployments.map((d) => (
            <Tag key={d.projectId} asChild>
              <Link href={`/deploy/${d.projectId}`} className="hover:text-foreground">
                deployed as {d.name}
              </Link>
            </Tag>
          ))}
        </div>
      )}

      <div className="flex shrink-0 flex-wrap items-center gap-1 border-b border-hairline px-2 py-1.5">
        {(["open", "merged", "closed"] as const).map((key) => (
          <FilterChip key={key} selected={state === key} onClick={() => setState(key)}>
            <PullStateMark
              pull={{ state: key, draft: false, merged: key === "merged" }}
              className="size-3.5"
            />
            {key === "open" ? "Open" : key === "merged" ? "Merged" : "Closed"}
            {key === state && pulls.data && <ChipCount>{pulls.data.length}</ChipCount>}
          </FilterChip>
        ))}
        <span className="flex-1" />
        {canControl &&
          (mine ? (
            <Button size="xs" variant="outline" asChild>
              <a href={mine.url} target="_blank" rel="noreferrer">
                <External className="size-3" />
                Open #{mine.number}
              </a>
            </Button>
          ) : (
            <Tooltip>
              <TooltipTrigger asChild>
                <Button size="xs" variant="outline" onClick={() => setCreating(true)}>
                  <Plus className="size-3" />
                  New pull request
                </Button>
              </TooltipTrigger>
              <TooltipContent>
                Propose <span className="font-mono">{branch}</span> for review and merging
              </TooltipContent>
            </Tooltip>
          ))}
      </div>

      <div className="min-h-0 flex-1 overflow-auto">
        {pulls.error && <ErrorState error={pulls.error} className="m-3" />}
        {pulls.loading && !pulls.data && <LoadingRows className="p-3" rows={4} />}
        {pulls.data && list.length === 0 && (
          <EmptyState
            className="m-3"
            icon={SourcePull}
            title={`No ${state} pull requests`}
            description={
              state === "open"
                ? "Push a branch and open one to get it reviewed and merged."
                : undefined
            }
          />
        )}
        {list.length > 0 && (
          <ul ref={listRef} className="animate-rise divide-y divide-hairline">
            {list.map((p) => {
              const reading = previewStatus(previewOf(p.number))
              const selected = active === `pull:${p.number}`
              const when = p.updatedAt ?? p.createdAt
              return (
                <li
                  key={p.number}
                  data-pull={p.number}
                  className={cn(
                    "group flex min-w-0 items-start gap-2.5 py-2 pr-1.5 pl-3 transition-colors hover:bg-row-hover",
                    selected && "bg-accent",
                  )}
                >
                  <PullStateMark pull={p} className="mt-px" />
                  <button
                    type="button"
                    aria-pressed={selected}
                    onClick={() => onSelect({ kind: "pull", number: p.number, title: p.title })}
                    className="min-w-0 flex-1 space-y-1 text-left focus-ring-inset"
                  >
                    <span className="flex min-w-0 items-start gap-1.5">
                      <span className="line-clamp-2 text-body leading-snug font-medium">
                        {p.title}
                      </span>
                      {p.head === branch && (
                        <Tag tone="warning" className="mt-0.5">
                          this branch
                        </Tag>
                      )}
                    </span>
                    <span className="flex min-w-0 items-center gap-1.5 text-hint text-muted-foreground">
                      <span className="numeric shrink-0">#{p.number}</span>
                      <BranchChip branch={p.head} className="max-w-[45%]" />
                      <ArrowRight aria-hidden className="size-3 shrink-0" />
                      <BranchChip branch={p.base} className="max-w-[35%]" />
                    </span>
                    <span className="flex min-w-0 flex-wrap items-center gap-x-2.5 gap-y-1 text-hint text-muted-foreground">
                      {p.author && (
                        <span className="inline-flex min-w-0 items-center gap-1">
                          <ForgeFace login={p.author} provider="github" size="xs" />
                          <span className="truncate">{p.author}</span>
                        </span>
                      )}
                      {when && <span className="shrink-0">{relativeTime(when)}</span>}
                      <ChecksMark checks={p.checks} label />
                      <ReviewMark review={p.review} />
                      <CommentCount count={p.comments} />
                      {reading && (
                        <Status
                          className="text-hint"
                          tone={reading.tone}
                          label={`preview ${reading.label.toLowerCase()}`}
                        />
                      )}
                      {p.labels?.slice(0, 3).map((label) => (
                        <Tag key={label}>{label}</Tag>
                      ))}
                    </span>
                  </button>
                  <VerbActions verbs={verbsFor(p)} reveal className="mt-0.5" />
                </li>
              )
            })}
          </ul>
        )}

        {/* What is open against the repository besides the pull requests.
            A reading with two verbs, not a choice: there is no issue preview
            to open, and its page on GitHub is one press away. */}
        <SectionStrip
          icon={<IssueMark className="size-3.5 text-(--pull-open)" />}
          label="Issues"
          count={issues.data?.length}
        />
        {issues.error && (
          <p className="px-3 py-2 text-hint text-muted-foreground">
            Could not list issues: {errorMessage(issues.error)}
          </p>
        )}
        {issues.loading && !issues.data && <LoadingRows className="p-3" rows={2} />}
        {issues.data && issues.data.length === 0 && (
          <p className="px-3 py-3 text-hint text-muted-foreground">No open issues.</p>
        )}
        {issues.data && issues.data.length > 0 && (
          <ul className="animate-rise divide-y divide-hairline">
            {issues.data.map((issue) => (
              <li
                key={issue.number}
                className="group flex min-w-0 items-start gap-2.5 py-2 pr-1.5 pl-3 transition-colors hover:bg-row-hover"
              >
                <IssueMark aria-hidden className="mt-px size-4 shrink-0 text-(--pull-open)" />
                <div className="min-w-0 flex-1 space-y-1">
                  <span className="line-clamp-2 text-body leading-snug font-medium">
                    {issue.title}
                  </span>
                  <span className="flex min-w-0 flex-wrap items-center gap-x-2.5 gap-y-1 text-hint text-muted-foreground">
                    <span className="numeric">#{issue.number}</span>
                    {issue.author && (
                      <span className="inline-flex min-w-0 items-center gap-1">
                        <ForgeFace login={issue.author} provider="github" size="xs" />
                        <span className="truncate">{issue.author}</span>
                      </span>
                    )}
                    {issue.updatedAt && <span>{relativeTime(issue.updatedAt)}</span>}
                    <CommentCount count={issue.comments} />
                    {issue.labels?.slice(0, 3).map((label) => (
                      <Tag key={label}>{label}</Tag>
                    ))}
                  </span>
                </div>
                <VerbActions
                  verbs={[
                    ...(canControl
                      ? [
                          {
                            key: "comment",
                            label: "Comment",
                            icon: PaperAirplane,
                            inline: true,
                            run: () => setCommenting({ number: issue.number, title: issue.title }),
                          } satisfies Verb,
                        ]
                      : []),
                    {
                      key: "open",
                      label: "Open on GitHub",
                      icon: External,
                      inline: true,
                      run: () => window.open(issue.url, "_blank", "noopener"),
                    },
                  ]}
                  reveal
                  className="mt-0.5"
                />
              </li>
            ))}
          </ul>
        )}

        {branch && (
          <>
            <SectionStrip
              icon={<Play className="size-3.5 text-muted-foreground" />}
              label="Workflow runs"
              detail={<BranchChip branch={branch} className="max-w-[60%]" />}
            />
            {runs.error && (
              <p className="px-3 py-2 text-hint text-muted-foreground">
                Could not list workflow runs: {errorMessage(runs.error)}
              </p>
            )}
            {runs.loading && !runs.data && <LoadingRows className="p-3" rows={2} />}
            {runs.data && runs.data.length === 0 && (
              <p className="px-3 py-3 text-hint text-muted-foreground">
                No Actions runs on this branch.
              </p>
            )}
            {runs.data && runs.data.length > 0 && (
              <ul className="animate-rise divide-y divide-hairline">
                {runs.data.map((r) => (
                  <li key={r.id} className="min-w-0">
                    <button
                      type="button"
                      onClick={() => onSelect({ kind: "workflow", id: r.id })}
                      aria-pressed={active === `workflow:${r.id}`}
                      className={cn(
                        "group flex w-full min-w-0 items-center gap-2.5 px-3 py-2 text-left focus-ring-inset transition-colors hover:bg-row-hover",
                        active === `workflow:${r.id}` && "bg-accent",
                      )}
                    >
                      <RunMark run={r} />
                      <span className="min-w-0 flex-1">
                        <span className="block truncate text-xs font-medium">
                          {r.workflow || r.name}
                        </span>
                        <span className="flex min-w-0 items-center gap-1.5 text-micro text-muted-foreground">
                          <span className="truncate">
                            {r.status !== "completed"
                              ? r.status.replace("_", " ")
                              : r.conclusion || "completed"}
                            {r.event ? ` · ${r.event}` : ""}
                          </span>
                          <ShortSha sha={r.sha} />
                          {r.createdAt && (
                            <span className="shrink-0">{relativeTime(r.createdAt)}</span>
                          )}
                        </span>
                      </span>
                      <External
                        className={cn("size-3.5 shrink-0 text-muted-foreground", rowReveal())}
                      />
                    </button>
                  </li>
                ))}
              </ul>
            )}
          </>
        )}
      </div>

      <CreatePullDialog
        open={creating}
        onOpenChange={setCreating}
        repoPath={repoPath}
        branch={branch}
        onCreated={() => {
          refreshPulls()
          onChanged()
        }}
      />
      {merging && (
        <MergePullDialog
          open
          onOpenChange={(o) => !o && setMerging(null)}
          repoPath={repoPath}
          pull={merging}
          headSha={merging.headSha}
          onMerged={() => {
            refreshPulls()
            onChanged()
          }}
        />
      )}
      {choosing && (
        <Modal
          open
          onOpenChange={(o) => !o && setChoosing(undefined)}
          title={`Which project tests #${choosing.number}?`}
          description={`${deployments.length} projects deploy this repository.`}
          size="sm"
        >
          <ChoiceList>
            {deployments.map((d) => (
              <ChoiceRow
                key={d.projectId}
                title={d.name}
                verb={`Test in ${d.name}`}
                description={`project ${d.projectId}`}
                onSelect={() => {
                  setTesting({ pull: choosing, deployment: d })
                  setChoosing(undefined)
                }}
              />
            ))}
          </ChoiceList>
        </Modal>
      )}
      {testing && (
        <TestPullDialog
          open
          onOpenChange={(o) => !o && setTesting(undefined)}
          projectId={testing.deployment.projectId}
          projectName={testing.deployment.name}
          pull={testing.pull}
          preview={previewOf(testing.pull.number)}
          onStarted={(runId) => {
            refreshPulls()
            router.push(`/deploy/${testing.deployment.projectId}/runs/${runId}`)
          }}
          onRefresh={refreshPulls}
        />
      )}
      {commenting && (
        <CommentDialog
          open
          onOpenChange={(o) => !o && setCommenting(undefined)}
          repoPath={repoPath}
          number={commenting.number}
          title={commenting.title}
          onCommented={() => issues.refresh()}
        />
      )}
    </div>
  )
}

/**
 * Opening a pull request, with the two things gh would otherwise have prompted
 * for filled in: which branch it targets, and whether it is a draft — and the
 * one thing GitHub's own form shows that this one used not to: what the
 * request would carry, counted before anything is pushed.
 *
 * The branch is pushed by the server before gh is asked, because a pull
 * request from a branch the remote has never seen is not a thing GitHub can
 * make — and being told that after writing a description is the worst moment
 * to learn it.
 */
function CreatePullDialog({
  open,
  onOpenChange,
  repoPath,
  branch,
  onCreated,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  repoPath: string
  branch: string
  onCreated: () => void
}) {
  const [title, setTitle] = useState("")
  const [body, setBody] = useState("")
  const [base, setBase] = useState("")
  const [draft, setDraft] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()
  const [repo, setRepo] = useState<GitHubRepo>()
  const [branches, setBranches] = useState<GitBranch[]>([])
  // The comparison, tagged with what it compares, so a stale answer for the
  // previous base is never shown against the new one.
  const [compare, setCompare] = useState<{ key: string; data: GitComparison }>()

  // Closing is where the form resets, rather than opening: setting state on
  // the way in is a second render before anything is on screen.
  const change = (next: boolean) => {
    if (!next) {
      setError(undefined)
      setBusy(false)
    }
    onOpenChange(next)
  }

  useEffect(() => {
    if (!open) return
    let alive = true
    void (async () => {
      try {
        const [info, list] = await Promise.all([
          get<GitHubRepo>("/git/github/repo", { path: repoPath }),
          get<GitBranch[]>("/git/branches", { path: repoPath }),
        ])
        if (!alive) return
        setRepo(info)
        setBranches(list.filter((b) => !b.remote && b.name !== branch))
        setBase((b) => b || info.defaultBranch)
      } catch (err) {
        if (alive) setError(errorMessage(err))
      }
    })()
    return () => {
      alive = false
    }
  }, [open, repoPath, branch])

  // What the request would carry, from the local branches: an answer before
  // the push, and a warning when there is nothing to propose.
  const compareKey = `${base}|${branch}`
  useEffect(() => {
    if (!open || !base || !branch) return
    let alive = true
    get<GitComparison>("/git/compare", { path: repoPath, base, head: branch })
      .then((data) => alive && setCompare({ key: compareKey, data }))
      .catch(() => undefined)
    return () => {
      alive = false
    }
  }, [open, repoPath, base, branch, compareKey])
  const carrying = compare?.key === compareKey ? compare.data : undefined

  const create = async () => {
    setBusy(true)
    setError(undefined)
    try {
      const pr = await post<PR>(
        "/git/github/pulls",
        { title: title.trim(), body, base, head: branch, draft },
        { query: { path: repoPath } },
      )
      notify.success("Pull request opened", { description: pr.url })
      onCreated()
      change(false)
      setTitle("")
      setBody("")
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal
      open={open}
      onOpenChange={change}
      title="New pull request"
      description={`${branch} into ${base || "…"}. The branch is pushed first.`}
      footer={
        <>
          <Button variant="ghost" onClick={() => change(false)}>
            Cancel
          </Button>
          <Tooltip>
            <TooltipTrigger asChild>
              <Button disabled={busy || !title.trim() || !base} onClick={create} pending={busy}>
                <SourcePull className="size-4" />
                Push and open
              </Button>
            </TooltipTrigger>
            <TooltipContent>
              Pushes {branch} to the remote, then opens the pull request as your GitHub account
            </TooltipContent>
          </Tooltip>
        </>
      }
    >
      <div className="space-y-3">
        <FormFacts>
          {repo?.nameWithOwner && <FormFact label="on">{repo.nameWithOwner}</FormFact>}
          <FormFact label="from" mono>
            {branch}
          </FormFact>
          <FormFact label="into" mono>
            {base || "…"}
          </FormFact>
          {carrying && (
            <FormFact label="carrying">
              {carrying.ahead} commit{carrying.ahead === 1 ? "" : "s"} · {carrying.files} file
              {carrying.files === 1 ? "" : "s"}
            </FormFact>
          )}
        </FormFacts>
        {error && (
          <Notice title="Could not open the pull request" tone="danger">
            <span className="break-words whitespace-pre-wrap">{error}</span>
          </Notice>
        )}
        {repo?.permission === "READ" && (
          <Notice title="You have read access to this repository" tone="warning">
            GitHub will refuse a pull request from a branch here — it has to come from a fork.
          </Notice>
        )}
        {carrying && carrying.ahead === 0 && (
          <Notice title={`${branch} has nothing ${base} lacks`} tone="warning">
            GitHub refuses a pull request with no commits in it. Commit something first, or pick
            another base.
          </Notice>
        )}
        <Field label="Title" htmlFor="pr-title">
          <Input
            id="pr-title"
            value={title}
            onChange={(e) => setTitle(e.target.value)}
            placeholder="What does this change?"
          />
        </Field>
        <Field label="Merge into">
          <Select value={base} onValueChange={setBase}>
            <SelectTrigger className="w-full">
              <SelectValue placeholder="Choose a branch" />
            </SelectTrigger>
            <SelectContent>
              {branches.map((b) => (
                <SelectItem key={b.name} value={b.name}>
                  {b.name}
                  {b.name === repo?.defaultBranch ? " · default" : ""}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </Field>
        <Field
          label="Description"
          htmlFor="pr-body"
          hint="What a reviewer needs to know. Markdown works."
        >
          <Textarea
            id="pr-body"
            rows={5}
            value={body}
            onChange={(e) => setBody(e.target.value)}
            className="resize-none text-body"
          />
        </Field>
        <OptionList>
          <OptionRow
            title="Open as a draft"
            hint="Nobody is asked to review it yet."
            checked={draft}
            onCheckedChange={setDraft}
          />
        </OptionList>
      </div>
    </Modal>
  )
}

/** A sticky strip naming a section of the tab, with its glyph and its count. */
function SectionStrip({
  icon,
  label,
  count,
  detail,
}: {
  icon: React.ReactNode
  label: string
  count?: number
  detail?: React.ReactNode
}) {
  return (
    <div className="sticky top-0 z-10 flex h-8 items-center gap-1.5 border-y border-hairline bg-card px-3">
      {icon}
      <span className="eyebrow">{label}</span>
      {count !== undefined && (
        <span className="numeric text-hint text-muted-foreground">{count}</span>
      )}
      {detail}
    </div>
  )
}

/**
 * How an Actions run stands, as the mark GitHub draws for it: a tick, a
 * cross, a clock while it runs, and a slashed circle for one that was
 * cancelled or skipped — in the status hues, because a run's outcome is a
 * reading of state.
 */
function RunMark({ run }: { run: GitHubWorkflowRun }) {
  if (run.status !== "completed") {
    return (
      <Clock
        role="img"
        aria-label="running"
        className="size-4 shrink-0 animate-pulse text-warning"
      />
    )
  }
  switch (run.conclusion) {
    case "success":
      return (
        <CheckCircle role="img" aria-label="succeeded" className="size-4 shrink-0 text-success" />
      )
    case "failure":
    case "timed_out":
    case "startup_failure":
      return (
        <CrossCircle role="img" aria-label="failed" className="size-4 shrink-0 text-destructive" />
      )
    default:
      return (
        <Slash
          role="img"
          aria-label={run.conclusion || "completed"}
          className="size-4 shrink-0 text-muted-foreground"
        />
      )
  }
}
