"use client"

import { useState } from "react"
import { useRouter } from "next/navigation"
import { ArrowRight, External, Play, RotateCounterClockwise } from "@/components/icons"
import { post } from "@/lib/api"
import { plural } from "@/lib/format"
import { parseRemote, type RepoPulls } from "@/lib/git-repos"
import { canTest, cleanupFailed, previewHeld, previewOutOfDate } from "@/lib/pull-requests"
import { notify } from "@/lib/toast"
import type { DeploymentEngineRun, DeploymentPreview, GitPullRequest, GitRepo } from "@/lib/types"
import { cn } from "@/lib/utils"
import { useAuth } from "@/hooks/use-auth"
import { AheadBehind } from "@/components/git/ahead-behind"
import { SourceMerge, SourcePull } from "@/components/git/glyphs"
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

/**
 * The grid a shelf of cards is laid out in — beside the card, as `ChoiceGrid`
 * keeps its own. As many columns as the width holds at twenty-two rems each:
 * two on a 1280 screen, three at 1440 and 1720, four past 1900, one on a
 * phone, where `min(…, 100%)` gives a screen narrower than a card a column
 * rather than a sideways scroll. Twenty was the first measure and made four
 * columns at 1720, where a pull request's title on the card's foot was three
 * words and an ellipsis. The rows are not forced equal: every card is one
 * height of its own making (see the foot), and equal rows let the tallest card
 * on a shelf set the height of all of them.
 */
export const REPO_GRID =
  "grid min-w-0 gap-3 grid-cols-[repeat(auto-fill,minmax(min(22rem,100%),1fr))]"

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
 * happened**, as a forge draws a commit. And on the foot, **what is waiting
 * to be merged**: the other half of "which of these needs me", which used to
 * be a tab inside the workspace, one click and a scroll away. The request is
 * a choice of its own with its own verbs — test it as a preview, merge it,
 * open it — inside a container that stops the press reaching the card,
 * because a card that opens the repository when its "Merge" is pressed is
 * the defect `ChoiceRow`'s actions slot exists to prevent.
 *
 * The foot is one line of one height whatever it holds, so every card on the
 * page is the same height. It used to stack up to three requests, and a card
 * with them stood twice the height of its neighbours, with the equal rows
 * stretching the rest of its shelf into empty space to match — while the
 * next shelf's cards stayed short. Now the first request is drawn and the
 * rest are counted beside it, and a card with none says why in a quiet word.
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
  const [first] = open
  const more = `${open.length - 1} more ${open.length > 2 ? "pull requests" : "pull request"}`
  const deployments = pulls?.deployments ?? []
  const changed = () => onPullsChanged?.()
  // What the foot says with no request to draw. Nothing while a GitHub
  // checkout's summary is on its way: "none" before gh has answered is a guess.
  const quiet = pulls?.error
    ? "pull requests unavailable"
    : pulls
      ? "no open pull requests"
      : /^(www\.)?github\.com$/.test(parseRemote(repo.remote)?.host ?? "")
        ? undefined
        : "not on GitHub"

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
    <li className="min-w-0" data-workspace-item={repo.path} data-workspace-name={repo.name}>
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
                data-workspace-primary
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
              {/* One line: a long branch name gives way before a card grows. */}
              <span className="flex min-w-0 flex-1 items-center gap-2">
                <BranchChip
                  branch={repo.branch}
                  detached={repo.detached}
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
                {repo.detached && (
                  <Tag tone="danger" className="shrink-0">
                    detached
                  </Tag>
                )}
                {repo.gone && (
                  <Tag tone="danger" className="shrink-0">
                    upstream gone
                  </Tag>
                )}
                {!repo.detached && !repo.empty && !repo.upstream && (
                  <Tag className="shrink-0">not published</Tag>
                )}
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

            {/* What last happened, at the height of a commit with its marks,
                which "no commits yet" alone is not. */}
            <CommitLine
              className="min-h-4.5 min-w-0"
              sha={repo.head}
              subject={repo.subject}
              author={repo.author}
              at={repo.commitAt}
              empty={repo.empty}
            />

            {/* What is waiting to be merged. The line is the height of one
                compact request whatever it holds, which is what keeps every
                card one height. */}
            <div className="mt-auto border-t border-hairline pt-2.5">
              {first ? (
                // The container swallows the press: the row and each verb in
                // it is a control of its own, and none of them is "open the
                // repository".
                <div
                  onClick={(event) => event.stopPropagation()}
                  className="flex h-[2.875rem] min-w-0 items-center gap-1"
                >
                  <ChoiceList aria-label={`Pull requests in ${repo.name}`} className="flex-1">
                    <PullRequestRow
                      compact
                      pull={first}
                      verbs={verbsFor(first)}
                      onOpen={onOpenPull ? () => onOpenPull(first.number) : undefined}
                    />
                  </ChoiceList>
                  {open.length > 1 && onOpenPull && (
                    <Button
                      size="xs"
                      variant="ghost"
                      className="numeric shrink-0 text-muted-foreground"
                      aria-label={more}
                      title={more}
                      onClick={() => onOpenPull()}
                    >
                      +{open.length - 1}
                    </Button>
                  )}
                </div>
              ) : (
                <p
                  className="flex h-[2.875rem] min-w-0 items-center gap-1.5 text-hint text-muted-foreground"
                  // gh could not answer for this checkout — not signed in as
                  // its owner, most often: its own sentence is a hover away.
                  title={pulls?.error}
                >
                  {quiet && <SourcePull aria-hidden className="size-3.5 shrink-0 opacity-70" />}
                  <span className="truncate">{quiet}</span>
                </p>
              )}
            </div>
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
