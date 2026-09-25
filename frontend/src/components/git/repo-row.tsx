"use client"

import { useState } from "react"
import { useRouter } from "next/navigation"
import { ArrowRight, External, Play, RotateCounterClockwise } from "@/components/icons"
import { post } from "@/lib/api"
import { plural } from "@/lib/format"
import { canTest, cleanupFailed, previewHeld, previewOutOfDate } from "@/lib/pull-requests"
import { notify } from "@/lib/toast"
import type {
  DeploymentEngineRun,
  DeploymentPreview,
  GitPullRequest,
  GitPullRequestSummary,
  GitRepo,
} from "@/lib/types"
import { cn } from "@/lib/utils"
import { useAuth } from "@/hooks/use-auth"
import { AheadBehind } from "@/components/git/ahead-behind"
import { SourceMerge } from "@/components/git/glyphs"
import { BranchChip, CommitLine, WorkingTreeBar } from "@/components/git/marks"
import { MergePullDialog } from "@/components/git/merge-pull-dialog"
import { PullRequestRow } from "@/components/git/pull-request-row"
import { TestPullDialog } from "@/components/git/test-pull-dialog"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import { Modal } from "@/components/modal"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { SpotlightBorder } from "@/components/ui/spotlight-border"
import type { Verb } from "@/components/verbs"

/** One checkout's entry in the pull-request summary: its open pull requests and who deploys it. */
export type RepoPulls = GitPullRequestSummary["repos"][number]
type Deployment = RepoPulls["deployments"][number]

/** How many pull requests a card shows before it says "and N more". */
const STRIP = 3

/**
 * One checkout on this host, as the card a git client draws for a repository.
 *
 * It replaced a five-column table, and the argument for the change is what the
 * table was spending its columns on. Two of them — Branch and Upstream — held
 * one short string each and were sized for the longest branch name in the
 * list, so on a screen with four repositories on it the eye crossed two
 * hundred pixels of nothing to read `main`. A third, Last commit, is three
 * facts (who, what, when) which a cell can only stack or truncate. Meanwhile
 * the question actually asked of this list — *which of these needs me* — was
 * answered by reading down a column.
 *
 * Two lines, which is what the facts want: **what this is** — name, where HEAD
 * is, where on disk — and **what last happened in it** — who, which commit,
 * when. The two readings that decide whether to open it are held out on the
 * right, one per line, so a column of cards is scanned down: how far the
 * branch has drifted from its upstream above, and the shape of the working
 * tree below. Three lines was the first draft and was wrong at width: the path
 * on a line of its own left half a wide screen empty beside it, and thirty
 * repositories became a page nobody scrolls.
 *
 * Under those, when GitHub has open pull requests for the checkout, a strip
 * of up to three of them: what is waiting to be merged is the other half of
 * "which of these needs me", and it used to be a tab inside the workspace,
 * one click and a scroll away from the question. Each is a choice of its
 * own with its own verbs — test it as a preview, merge it, open it — inside
 * a container that stops the press reaching the card, because a card that
 * opens the repository when its "Merge" is pressed is the defect
 * `ChoiceRow`'s actions slot exists to prevent.
 *
 * It is a **choice**, not a reading: every row here is a repository to enter,
 * which §15 pass 3 and §16 both settle — the lit edge belongs to things you
 * pick, wherever they are. The title is a real button whose accessible name is
 * the repository, and the press on the card around it is the convenience for
 * the pointer (§12).
 */
export function RepoRow({
  repo,
  pulls,
  onOpen,
  onOpenPull,
  onPullsChanged,
}: {
  repo: GitRepo
  /** The checkout's open pull requests and its deploy projects, once the summary has arrived. */
  pulls?: RepoPulls
  onOpen: () => void
  /** Opens the workspace's GitHub tab — on one pull request when given a number. */
  onOpenPull?: (number?: number) => void
  onPullsChanged?: () => void
}) {
  const { can } = useAuth()
  const router = useRouter()
  const [testing, setTesting] = useState<{ pull: GitPullRequest; deployment: Deployment }>()
  const [choosing, setChoosing] = useState<GitPullRequest>()
  const [merging, setMerging] = useState<GitPullRequest>()
  // The retry in flight, so no row offers a second one until it answers.
  const [retrying, setRetrying] = useState(false)
  const open = pulls?.pulls ?? []
  const deployments = pulls?.deployments ?? []
  const changed = () => onPullsChanged?.()

  // A failed removal is retried as the run it was, through the run's own
  // retry route — as the Overview retries it — against the project the
  // preview names; a checkout deployed by one project is that project.
  const projectOf = (preview: DeploymentPreview) =>
    preview.projectId ?? (deployments.length === 1 ? deployments[0].projectId : undefined)
  const retryCleanup = async (
    projectId: number,
    run: NonNullable<DeploymentPreview["lastRun"]>,
  ) => {
    setRetrying(true)
    try {
      const created = await post<DeploymentEngineRun>(
        `/deploy/${projectId}/runs/${run.id}/retry`,
        {},
      )
      changed()
      router.push(`/deploy/${projectId}/runs/${created.id}`)
    } catch (error) {
      notify.error("Could not retry the cleanup", error)
      setRetrying(false)
    }
  }

  const verbsFor = (p: GitPullRequest): Verb[] => {
    const verbs: Verb[] = []
    const built = p.preview
    // Held while its preview is ready at this commit, building, closing or
    // waiting on a failed cleanup: the Overview's rule, so the two pages
    // never disagree about whether pressing it builds anything.
    if (can("system.admin") && deployments.length > 0 && canTest(p) && !previewHeld(built)) {
      verbs.push({
        key: "test",
        label: previewOutOfDate(built) ? "Update preview" : "Test this pull request",
        icon: Play,
        run: () =>
          deployments.length === 1
            ? setTesting({ pull: p, deployment: deployments[0] })
            : setChoosing(p),
      })
    }
    if (built?.lastRun && cleanupFailed(built) && can("service.control")) {
      const projectId = projectOf(built)
      const run = built.lastRun
      if (projectId)
        verbs.push({
          key: "retry-cleanup",
          label: "Retry cleanup",
          icon: RotateCounterClockwise,
          progressive: "Retrying…",
          disabled: retrying,
          run: () => void retryCleanup(projectId, run),
        })
    }
    if (can("service.control")) {
      verbs.push({
        key: "merge",
        label: "Merge",
        icon: SourceMerge,
        disabled: p.draft,
        run: () => setMerging(p),
      })
    }
    verbs.push({
      key: "github",
      label: "Open on GitHub",
      icon: External,
      run: () => window.open(p.url, "_blank", "noopener"),
    })
    return verbs
  }

  return (
    <li className="min-w-0">
      <SpotlightBorder radius={420}>
        <div
          onClick={onOpen}
          className="group/repo flex min-w-0 cursor-pointer items-center gap-3 rounded-xl px-3.5 py-2.5"
        >
          <div className="min-w-0 flex-1 space-y-1">
            {/* What it is. The path takes the slack, so a wide screen spends
                it on the one fact here that can be arbitrarily long. */}
            <div className="flex min-w-0 items-center gap-x-2 gap-y-1">
              <button
                type="button"
                // The card's own handler already fires on the pointer; this one
                // is for the keyboard and must not open the repository twice.
                onClick={(event) => {
                  event.stopPropagation()
                  onOpen()
                }}
                className="max-w-[18rem] min-w-0 shrink-0 truncate rounded-sm text-left text-body font-medium focus-ring"
              >
                {repo.name}
              </button>
              <BranchChip
                branch={repo.branch}
                detached={repo.detached}
                className="max-w-[16rem]"
                title={
                  repo.detached
                    ? "Detached HEAD"
                    : repo.upstream
                      ? `tracks ${repo.upstream}`
                      : "no upstream — pushing publishes this branch"
                }
              />
              {/* Only the states that change what the next push does get a
                  word. "tracks origin/main" is the normal case and is already
                  on the branch chip's tooltip. */}
              {repo.detached && <Tag tone="danger">detached</Tag>}
              {repo.gone && <Tag tone="danger">upstream gone</Tag>}
              {!repo.detached && !repo.empty && !repo.upstream && <Tag>not published</Tag>}
              {/* gh could not answer for this checkout — not signed in as its
                  owner, most often. One quiet word, with gh's own sentence a
                  hover away; the card is still the repository. */}
              {pulls?.error && <Tag title={pulls.error}>pull requests unavailable</Tag>}
              <p
                className="min-w-0 flex-1 truncate font-mono text-hint text-muted-foreground"
                title={repo.path}
              >
                {repo.path}
              </p>
              <AheadBehind ahead={repo.ahead} behind={repo.behind} />
            </div>

            {/* What last happened, and what is outstanding. */}
            <div className="flex min-w-0 items-center gap-3">
              <CommitLine
                className="min-w-0 flex-1"
                sha={repo.head}
                subject={repo.subject}
                author={repo.author}
                at={repo.commitAt}
                empty={repo.empty}
              />
              {/* A fixed measure, so the attribution to its left ends on the
                  same column in every card: a row of them is a row (§15 pass
                  9), and "clean" against "28 changes" is otherwise ragged. */}
              <span className="flex shrink-0 items-center justify-end gap-1.5 sm:w-[7.5rem]">
                <WorkingTreeBar repo={repo} />
                <span
                  className={cn(
                    "numeric text-hint",
                    repo.conflicts > 0
                      ? "text-destructive"
                      : repo.dirty
                        ? "text-warning"
                        : "text-muted-foreground",
                  )}
                >
                  {repo.dirty ? plural(repo.changes, "change") : "clean"}
                </span>
              </span>
            </div>

            {/* What is waiting to be merged. The container swallows the
                press: each row and each verb in it is a control of its own,
                and none of them is "open the repository". */}
            {open.length > 0 && (
              <div onClick={(event) => event.stopPropagation()} className="pt-1.5">
                <ChoiceList aria-label={`Pull requests in ${repo.name}`}>
                  {open.slice(0, STRIP).map((p) => (
                    <PullRequestRow
                      key={p.number}
                      compact
                      pull={p}
                      verbs={verbsFor(p)}
                      onOpen={onOpenPull ? () => onOpenPull(p.number) : undefined}
                    />
                  ))}
                </ChoiceList>
                {open.length > STRIP && onOpenPull && (
                  <Button
                    size="xs"
                    variant="ghost"
                    className="mt-1 text-muted-foreground"
                    onClick={() => onOpenPull()}
                  >
                    and {open.length - STRIP} more
                  </Button>
                )}
              </div>
            )}
          </div>

          <ArrowRight
            aria-hidden
            className="size-3.5 shrink-0 text-muted-foreground transition-colors group-hover/repo:text-foreground"
          />
        </div>
      </SpotlightBorder>

      {/* The dialogs stand beside the card, not inside it: a press in a
          portal still bubbles through the React tree, and inside the card
          it would open the repository under the dialog. */}
      {choosing && (
        <Modal
          open
          onOpenChange={(o) => !o && setChoosing(undefined)}
          title={`Which project tests #${choosing.number}?`}
          description={`${deployments.length} projects deploy ${pulls?.repository ?? repo.name}.`}
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
          preview={testing.pull.preview}
          onStarted={(runId) => {
            changed()
            router.push(`/deploy/${testing.deployment.projectId}/runs/${runId}`)
          }}
          onRefresh={changed}
        />
      )}
      {merging && (
        <MergePullDialog
          open
          onOpenChange={(o) => !o && setMerging(undefined)}
          repoPath={repo.path}
          pull={merging}
          headSha={merging.headSha}
          onMerged={changed}
        />
      )}
    </li>
  )
}
