"use client"

import { useState } from "react"
import { useRouter } from "next/navigation"
import {
  ArrowRight,
  External,
  FolderOpen,
  Play,
  RotateCounterClockwise,
  Terminal,
  Trash,
} from "@/components/icons"
import { post } from "@/lib/api"
import { plural, relativeTime } from "@/lib/format"
import { forgeRepository, type RepoPulls } from "@/lib/git-repos"
import { canTest, cleanupFailed, previewHeld, previewOutOfDate } from "@/lib/pull-requests"
import { notify } from "@/lib/toast"
import type { DeploymentEngineRun, DeploymentPreview, GitPullRequest, GitRepo } from "@/lib/types"
import { cn } from "@/lib/utils"
import { useAuth } from "@/hooks/use-auth"
import { AheadBehind } from "@/components/git/ahead-behind"
import { SourceFork, SourceMerge, SourcePull } from "@/components/git/glyphs"
import { LanguageBar, LanguageList, RepoMark } from "@/components/git/languages"
import { AuthorMark, BranchChip, ShortSha, TreeState } from "@/components/git/marks"
import { MergePullDialog } from "@/components/git/merge-pull-dialog"
import { PullRequestRow } from "@/components/git/pull-request-row"
import { TestPullDialog } from "@/components/git/test-pull-dialog"
import { WORKTREES_SECTION } from "@/components/git/worktree-list"
import { CONTROL, ChoiceList, ChoiceRow } from "@/components/flow"
import { Modal } from "@/components/modal"
import { ProductGlyph, hostProduct } from "@/components/product-logo"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { BlurFade } from "@/components/ui/blur-fade"
import { SpotlightBorder } from "@/components/ui/spotlight-border"
import { VerbMenu, type Verb } from "@/components/verbs"

type Deployment = RepoPulls["deployments"][number]

/**
 * The grid a shelf of cards is laid out in — beside the card, as `ChoiceGrid`
 * keeps its own. As many columns as the width holds at twenty-four rems each:
 * two on a 1280 screen, three at 1720, four past 2100, one on a phone, where
 * `min(…, 100%)` gives a screen narrower than a card a column rather than a
 * sideways scroll. The card grew a language strip and its foot a request's
 * marks, and at twenty-two the strip's names were an ellipsis each.
 * Every card is one shape (see the card), so a row of them is even.
 */
export const REPO_GRID =
  "grid min-w-0 gap-3 grid-cols-[repeat(auto-fill,minmax(min(24rem,100%),1fr))]"

/**
 * One checkout on this host, as a card on a shelf of its account's — drawn
 * the way a forge draws a repository, because that is the picture of a
 * repository a reader already has.
 *
 * The order down the card is the order the question is asked in. **What this
 * is**: its mark — the logo of the language it is mostly written in — its
 * name, and where it lives on the forge and on disk, with how far its branch
 * has drifted held out at the right. **What it is made of**: the language
 * strip, a forge's bar in Linguist's hues, and the largest languages by their
 * own logos. **Where HEAD is and how the tree stands**, with how many
 * worktrees the repository has, then **what last happened**, as a forge draws
 * a commit. And on the foot, **what is waiting to be merged**.
 *
 * **Every card is one shape.** Nothing on it is a list that grows: the
 * worktrees are counted here and listed in a section of their own under the
 * shelves (`worktree-list.tsx`), and the foot is one line of one height —
 * the first request, the rest counted beside it, or why there is none. A
 * card that grew with its worktrees made every card in its row as tall, for
 * nothing, because the grid's rows stretch to their tallest card. The
 * language strip keeps its room when there is nothing to measure for the
 * same reason. The request on the foot is a choice of its own with its verbs
 * — test it as a preview, merge it, open it — inside a container that stops
 * the press reaching the card, because a card that opens the repository when
 * its "Merge" is pressed is the defect `ChoiceRow`'s actions slot exists to
 * prevent.
 *
 * It is a **choice**, not a reading: every card is a repository to enter, so
 * it carries the lit edge §16 gives to things you pick. The title is a real
 * button whose accessible name is the repository, and the press on the card
 * around it is the convenience for the pointer (§12), skipping the controls
 * the card holds (`CONTROL`).
 */
export function RepoCard({
  repo,
  pulls,
  worktrees = 0,
  index = 0,
  onOpen,
  onOpenPull,
  onPullsChanged,
  onDelete,
}: {
  repo: GitRepo
  /** The pull requests this card draws and the checkout's deploy projects, once the summary has arrived. */
  pulls?: RepoPulls
  /** How many linked worktrees this checkout has; they are listed in their own section. */
  worktrees?: number
  /** Position on the shelf, for the arrival stagger. */
  index?: number
  onOpen: (path: string) => void
  /** Opens a checkout's GitHub tab — on one pull request when given a number. */
  onOpenPull?: (path: string, number?: number) => void
  onPullsChanged?: () => void
  /** Deletes a checkout from the server, after saying what that loses. */
  onDelete?: (repo: GitRepo) => void
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
  const forge = forgeRepository(repo.remote)
  const forgeMark = forge ? (hostProduct(forge.host) ?? "git") : undefined
  const github = forge?.host === "github.com"
  // What the foot says with no request to draw. Nothing while a GitHub
  // checkout's summary is on its way: "none" before gh has answered is a guess.
  const quiet = pulls?.error
    ? "pull requests unavailable"
    : pulls
      ? "no open pull requests"
      : github
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

  // The card's own menu: the places this checkout also lives, then the one
  // act that takes it away, behind its rule at the end.
  const menu: Verb[] = []
  if (forge?.url) {
    const url = forge.url
    menu.push({
      key: "forge",
      label: github ? "Open on GitHub" : `Open on ${forge.host}`,
      icon: External,
      run: () => window.open(url, "_blank", "noopener"),
    })
  }
  menu.push({
    key: "files",
    label: "Open in Files",
    icon: FolderOpen,
    run: () => router.push(`/files?path=${encodeURIComponent(repo.path)}`),
  })
  if (can("terminal")) {
    menu.push({
      key: "shell",
      label: "Open a shell here",
      icon: Terminal,
      run: () => router.push(`/terminal?cwd=${encodeURIComponent(repo.path)}`),
    })
  }
  if (can("destructive") && onDelete) {
    menu.push({
      key: "delete",
      label: "Delete from server…",
      icon: Trash,
      danger: true,
      run: () => onDelete(repo),
    })
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
              onOpen(repo.path)
            }}
            className="group group/choice flex h-full min-w-0 cursor-pointer flex-col rounded-xl"
          >
            {/* What it is, where it lives, and how far it has drifted. */}
            <div className="flex min-w-0 items-start gap-3 px-3.5 pt-3.5">
              <RepoMark repo={repo} />
              <div className="min-w-0 flex-1">
                <div className="flex min-w-0 items-center gap-2">
                  <button
                    data-workspace-primary
                    type="button"
                    title={repo.path}
                    // The card's own handler already fires on the pointer; this
                    // one is for the keyboard and must not open it twice.
                    onClick={(event) => {
                      event.stopPropagation()
                      onOpen(repo.path)
                    }}
                    className="min-w-0 truncate rounded-sm text-left text-title leading-5 font-semibold focus-ring"
                  >
                    {repo.name}
                  </button>
                  <span className="flex-1" />
                  <AheadBehind ahead={repo.ahead} behind={repo.behind} />
                </div>
                <p className="mt-1 flex min-w-0 items-center gap-1.5 text-xs text-muted-foreground">
                  {forge && forgeMark ? (
                    <>
                      <ProductGlyph id={forgeMark} className="size-3.5" />
                      <span className="truncate">{forge.slug}</span>
                    </>
                  ) : (
                    <span className="truncate">no remote — only on this server</span>
                  )}
                </p>
                <p
                  className="mt-0.5 truncate font-mono text-hint text-muted-foreground/70"
                  title={repo.path}
                >
                  {repo.path}
                </p>
              </div>
              <div className="-mt-1 -mr-1.5 flex shrink-0 items-center">
                <VerbMenu verbs={menu} label={`More for ${repo.name}`} />
                <ArrowRight
                  aria-hidden
                  className="size-3.5 text-muted-foreground transition-colors group-hover/choice:text-foreground"
                />
              </div>
            </div>

            {/* What it is made of. The strip's room is kept when there is
                nothing to measure, so every card is the same shape. */}
            <div className="mt-3 space-y-1.5 px-3.5">
              {repo.languages && repo.languages.length > 0 ? (
                <>
                  <LanguageBar languages={repo.languages} />
                  <LanguageList languages={repo.languages} className="h-4" />
                </>
              ) : (
                <>
                  <span aria-hidden className="block h-1.5 rounded-full bg-meter-track" />
                  <p className="h-4 text-xs leading-4 text-muted-foreground">
                    {repo.empty ? "no commits yet" : "no source files to measure"}
                  </p>
                </>
              )}
            </div>

            {/* Where HEAD is, the shape of the tree on it, and what last
                happened there. */}
            <div className="mt-3 border-t border-hairline px-3.5 py-2.5">
              <div className="flex min-w-0 items-center gap-2">
                {/* One line: a long branch name gives way before a card grows. */}
                <span className="flex min-w-0 flex-1 items-center gap-2">
                  <BranchChip
                    className="text-xs"
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
                      word. "tracks origin/main" is the normal case and is
                      already on the branch chip's tooltip. */}
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
                {worktrees > 0 && (
                  <button
                    type="button"
                    onClick={() =>
                      document.getElementById(WORKTREES_SECTION)?.scrollIntoView({ block: "start" })
                    }
                    title="Listed under Worktrees, below the repositories"
                    className="flex shrink-0 items-center gap-1 rounded-sm px-1 text-xs text-muted-foreground focus-ring hover:bg-accent hover:text-foreground"
                  >
                    <SourceFork aria-hidden className="size-3.5" />
                    {plural(worktrees, "worktree")}
                  </button>
                )}
                <TreeState repo={repo} />
              </div>
              {/* The subject on a line of its own, at the size of what is
                  read: squeezed between the sha and the attribution it kept
                  thirty characters of a card four hundred pixels wide. Who
                  and when go under it, quieter, and the sha to the right
                  under the tree's state. Both lines keep their height when
                  there is nothing to say, so every card is one shape. */}
              <p
                className={cn(
                  "mt-2 h-5 truncate text-body leading-5",
                  repo.subject && !repo.empty
                    ? "text-foreground/90"
                    : "text-muted-foreground italic",
                )}
                title={repo.subject}
              >
                {repo.empty ? "no commits yet" : repo.subject || "—"}
              </p>
              <p className="mt-0.5 flex h-4 min-w-0 items-center gap-1.5 text-xs text-muted-foreground">
                {repo.subject && !repo.empty && (
                  <>
                    <AuthorMark name={repo.author} />
                    <span className="truncate">{repo.author}</span>
                    {repo.commitAt && (
                      <>
                        <span aria-hidden>·</span>
                        <span className="shrink-0 whitespace-nowrap">
                          {relativeTime(repo.commitAt)}
                        </span>
                      </>
                    )}
                    <ShortSha sha={repo.head} className="ml-auto text-hint leading-4" />
                  </>
                )}
              </p>
            </div>

            {/* What is waiting to be merged, on one line of one height
                whatever it holds — the first request, the rest counted — so
                a card with ten requests is the height of one with none and
                never stretches the row it stands in. */}
            <div className="mt-auto border-t border-hairline px-2 pt-2 pb-2">
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
                      onOpen={onOpenPull ? () => onOpenPull(repo.path, first.number) : undefined}
                    />
                  </ChoiceList>
                  {open.length > 1 && onOpenPull && (
                    <Button
                      size="xs"
                      variant="ghost"
                      className="numeric shrink-0 text-muted-foreground"
                      aria-label={more}
                      title={more}
                      onClick={() => onOpenPull(repo.path)}
                    >
                      +{open.length - 1}
                    </Button>
                  )}
                </div>
              ) : (
                <p
                  className="flex h-[2.875rem] min-w-0 items-center gap-1.5 px-1.5 text-xs text-muted-foreground"
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
