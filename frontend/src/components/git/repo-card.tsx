"use client"

import { useState } from "react"
import { useRouter } from "next/navigation"
import { ArrowRight, External, Play, RotateCounterClockwise } from "@/components/icons"
import { post } from "@/lib/api"
import { plural } from "@/lib/format"
import type { RepoPulls } from "@/lib/git-repos"
import { canTest, cleanupFailed, previewHeld, previewOutOfDate } from "@/lib/pull-requests"
import { notify } from "@/lib/toast"
import type { DeploymentEngineRun, DeploymentPreview, GitPullRequest, GitRepo } from "@/lib/types"
import { cn } from "@/lib/utils"
import { useAuth } from "@/hooks/use-auth"
import { AheadBehind } from "@/components/git/ahead-behind"
import { SourceMerge } from "@/components/git/glyphs"
import { BranchChip, CommitLine, WorkingTreeBar } from "@/components/git/marks"
import { MergePullDialog } from "@/components/git/merge-pull-dialog"
import { PullRequestRow } from "@/components/git/pull-request-row"
import { TestPullDialog } from "@/components/git/test-pull-dialog"
import { CONTROL, ChoiceList, ChoiceRow } from "@/components/flow"
import { Modal } from "@/components/modal"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { BlurFade } from "@/components/ui/blur-fade"
import { SpotlightBorder } from "@/components/ui/spotlight-border"
import type { Verb } from "@/components/verbs"

type Deployment = RepoPulls["deployments"][number]

/** How many pull requests a card shows before it says "and N more". */
const STRIP = 3

/**
 * The grid a shelf of cards is laid out in — beside the card, as `ChoiceGrid`
 * keeps its own. As many columns as the width holds at twenty-two rems each:
 * two on a 1280 screen, three at 1440 and 1720, four past 1900, one on a
 * phone, where `min(…, 100%)` gives a screen narrower than a card a column
 * rather than a sideways scroll. Twenty was the first measure and made four
 * columns at 1720, where a pull request's title on the card's foot was three
 * words and an ellipsis. The rows are equal, so a card with a pull request on
 * it does not push its neighbours' feet out of line.
 */
export const REPO_GRID =
  "grid min-w-0 auto-rows-fr gap-3 grid-cols-[repeat(auto-fill,minmax(min(22rem,100%),1fr))]"

/**
 * One checkout on this host, as a card on a shelf of its account's.
 *
 * It was a row the width of the page, and the argument for the change is
 * what a row that wide did with its width. Two lines of facts — name, branch,
 * path; who, which commit, when — sat at the left, two readings at the right,
 * and between them on a wide screen ran three hundred pixels of nothing; a
 * page of six looked like a table that had lost its columns, and the reader
 * said so. Stacked in a card the same facts are read top to bottom the way a
 * forge draws a repository, and three or four cards to a row put a whole
 * account on one screen.
 *
 * The order down the card is the order the question is asked in. **What this
 * is**: the name, with how far its branch has drifted from the upstream held
 * out at the right. **Where HEAD is**, with the shape of the working tree
 * held out beside it — the two readings that decide whether to open it, one
 * per line, so a shelf of cards is scanned across. Where on disk. **What last
 * happened**, as a forge draws a commit. And on the foot, when GitHub has
 * open pull requests for the checkout, up to three of them: what is waiting
 * to be merged is the other half of "which of these needs me", and it used
 * to be a tab inside the workspace, one click and a scroll away. Each is a
 * choice of its own with its own verbs — test it as a preview, merge it,
 * open it — inside a container that stops the press reaching the card,
 * because a card that opens the repository when its "Merge" is pressed is
 * the defect `ChoiceRow`'s actions slot exists to prevent. The strip sits on
 * the foot rather than under the commit so a row of cards shares one
 * baseline whether or not each has something to merge.
 *
 * It is a **choice**, not a reading: every card is a repository to enter,
 * which §15 pass 3 and §16 both settle — the lit edge belongs to things you
 * pick, wherever they are. The title is a real button whose accessible name
 * is the repository, and the press on the card around it is the convenience
 * for the pointer (§12), skipping the controls the card holds (`CONTROL`).
 */
export function RepoCard({
  repo,
  pulls,
  index = 0,
  onOpen,
  onOpenPull,
  onPullsChanged,
}: {
  repo: GitRepo
  /** The pull requests this card draws and the checkout's deploy projects, once the summary has arrived. */
  pulls?: RepoPulls
  /** Position on the shelf, for the arrival stagger. */
  index?: number
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
        detail:
          "Build this exact commit as a preview environment of the project, reachable only on your tailnet.",
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
          detail: "Run the failed removal again, so its containers and address are freed.",
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
        detail: "Merge it into its base branch on GitHub.",
        icon: SourceMerge,
        disabled: p.draft,
        run: () => setMerging(p),
      })
    }
    verbs.push({
      key: "github",
      label: "Open on GitHub",
      detail: "The request's own page, with the conversation and the review.",
      icon: External,
      run: () => window.open(p.url, "_blank", "noopener"),
    })
    return verbs
  }

  return (
    <li className="min-w-0">
      {/* Each card lands a beat after the one before it, capped so a shelf
          of forty does not take two seconds. */}
      <BlurFade delay={Math.min(index, 11) * 0.03} className="h-full">
        <SpotlightBorder radius={360} className="h-full">
          <div
            onClick={(event) => {
              // React carries a press inside an open menu — a group label, a
              // separator — up through its portal to here, though it landed
              // nowhere on the card.
              const target = event.target as HTMLElement
              if (!event.currentTarget.contains(target) || target.closest(CONTROL)) return
              onOpen()
            }}
            className="group group/choice flex h-full min-w-0 cursor-pointer flex-col gap-2 rounded-xl p-3.5"
          >
            {/* What it is, and how far it has drifted. */}
            <div className="flex min-w-0 items-center gap-2">
              <button
                type="button"
                // The card's own handler already fires on the pointer; this one
                // is for the keyboard and must not open the repository twice.
                onClick={(event) => {
                  event.stopPropagation()
                  onOpen()
                }}
                className="min-w-0 flex-1 truncate rounded-sm text-left text-title leading-tight font-medium focus-ring"
              >
                {repo.name}
              </button>
              <AheadBehind ahead={repo.ahead} behind={repo.behind} />
              <ArrowRight
                aria-hidden
                className="size-3.5 shrink-0 text-muted-foreground transition-colors group-hover/choice:text-foreground"
              />
            </div>

            {/* Where HEAD is, and the shape of the tree on it. */}
            <div className="flex min-w-0 items-start gap-2">
              <span className="flex min-w-0 flex-1 flex-wrap items-center gap-x-2 gap-y-1">
                <BranchChip
                  branch={repo.branch}
                  detached={repo.detached}
                  className="max-w-full"
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
              </span>
              <span className="flex shrink-0 items-center gap-1.5 leading-[1.6]">
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

            <p className="truncate font-mono text-hint text-muted-foreground" title={repo.path}>
              {repo.path}
            </p>

            {/* What last happened. */}
            <CommitLine
              className="min-w-0"
              sha={repo.head}
              subject={repo.subject}
              author={repo.author}
              at={repo.commitAt}
              empty={repo.empty}
            />

            {/* What is waiting to be merged. The container swallows the
                press: each row and each verb in it is a control of its own,
                and none of them is "open the repository". */}
            {open.length > 0 && (
              <div
                onClick={(event) => event.stopPropagation()}
                className="mt-auto border-t border-hairline pt-2.5"
              >
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
        </SpotlightBorder>
      </BlurFade>

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
