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
import { plural } from "@/lib/format"
import { forgeRepository, worktreePlace, type RepoPulls } from "@/lib/git-repos"
import { canTest, cleanupFailed, previewHeld, previewOutOfDate } from "@/lib/pull-requests"
import { notify } from "@/lib/toast"
import type { DeploymentEngineRun, DeploymentPreview, GitPullRequest, GitRepo } from "@/lib/types"
import { cn } from "@/lib/utils"
import { useAuth } from "@/hooks/use-auth"
import { AheadBehind } from "@/components/git/ahead-behind"
import { SourceBranch, SourceMerge, SourcePull } from "@/components/git/glyphs"
import { LanguageBar, LanguageList, RepoMark } from "@/components/git/languages"
import { BranchChip, CommitLine, WorkingTreeBar, branchLabel } from "@/components/git/marks"
import { MergePullDialog } from "@/components/git/merge-pull-dialog"
import { ChecksMark, PullStateMark } from "@/components/git/pull-state"
import { PullRequestRow } from "@/components/git/pull-request-row"
import { TestPullDialog } from "@/components/git/test-pull-dialog"
import { CONTROL, ChoiceList, ChoiceRow } from "@/components/flow"
import { IconAction } from "@/components/icon-action"
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
 * sideways scroll. The card grew a language strip and a worktree list, and at
 * twenty-two a worktree's branch and its place on disk were an ellipsis each.
 * Rows stretch to their tallest card, so a shelf reads as one row of equal
 * cards whatever each holds.
 */
export const REPO_GRID =
  "grid min-w-0 gap-3 grid-cols-[repeat(auto-fill,minmax(min(24rem,100%),1fr))]"

/** How many of a checkout's open requests the card draws before it counts the rest. */
const PULLS_SHOWN = 2

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
 * own logos. **Where HEAD is and how the tree stands**, then **what last
 * happened**, as a forge draws a commit. Then the two things a repository on
 * this server has that a forge's listing does not: **its worktrees**, each
 * the branch it is on and the state of its own tree, with the request open
 * from that branch beside it; and **what is waiting to be merged**.
 *
 * The worktrees are inside the card because they are the same repository.
 * Drawn as cards of their own, beside it, a worktree read as a second
 * repository with the first one's name — and each removal was three screens
 * away, inside the workspace's menu. Here each row opens the worktree and
 * carries its own removal (`remove-checkout.tsx` decides how heavy that is).
 *
 * The requests are the first two, the rest counted, each a choice of its own
 * with its verbs — test it as a preview, merge it, open it — inside a
 * container that stops the press reaching the card, because a card that
 * opens the repository when its "Merge" is pressed is the defect
 * `ChoiceRow`'s actions slot exists to prevent.
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
  worktrees = [],
  worktreePulls = {},
  index = 0,
  onOpen,
  onOpenPull,
  onPullsChanged,
  onDelete,
  onRemoveWorktree,
}: {
  repo: GitRepo
  /** The pull requests this card draws and the checkout's deploy projects, once the summary has arrived. */
  pulls?: RepoPulls
  /** The linked worktrees of this checkout, drawn inside it. */
  worktrees?: GitRepo[]
  /** The requests assigned to each worktree, by its path. */
  worktreePulls?: Record<string, RepoPulls>
  /** Position on the shelf, for the arrival stagger. */
  index?: number
  /** Enters a checkout: this one, or one of its worktrees. */
  onOpen: (path: string) => void
  /** Opens a checkout's GitHub tab — on one pull request when given a number. */
  onOpenPull?: (path: string, number?: number) => void
  onPullsChanged?: () => void
  /** Deletes a checkout from the server, after saying what that loses. */
  onDelete?: (repo: GitRepo) => void
  onRemoveWorktree?: (worktree: GitRepo, main: string) => void
}) {
  const { can } = useAuth()
  const router = useRouter()
  const [testing, setTesting] = useState<{ pull: GitPullRequest; deployment: Deployment }>()
  const [choosing, setChoosing] = useState<GitPullRequest>()
  const [merging, setMerging] = useState<GitPullRequest>()
  // The retry in flight, so no row offers a second one until it answers.
  const [retrying, setRetrying] = useState(false)
  const open = pulls?.pulls ?? []
  const shown = open.slice(0, PULLS_SHOWN)
  const more = open.length - shown.length
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
  if (can("destructive") && repo.worktree && repo.main && onRemoveWorktree) {
    const main = repo.main
    menu.push({
      key: "remove-worktree",
      label: "Remove worktree",
      icon: SourceBranch,
      danger: true,
      run: () => onRemoveWorktree(repo, main),
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
                    className="min-w-0 truncate rounded-sm text-left text-title leading-tight font-semibold focus-ring"
                  >
                    {repo.name}
                  </button>
                  {repo.worktree && <Tag className="shrink-0">worktree</Tag>}
                  <span className="flex-1" />
                  <AheadBehind ahead={repo.ahead} behind={repo.behind} />
                </div>
                <p className="mt-1 flex min-w-0 items-center gap-1.5 text-hint text-muted-foreground">
                  {forge && forgeMark ? (
                    <>
                      <ProductGlyph id={forgeMark} className="size-3.5" />
                      <span className="truncate text-foreground/75">{forge.slug}</span>
                    </>
                  ) : repo.worktree && repo.main ? (
                    <span className="truncate">
                      worktree of <span className="font-mono">{repo.main}</span>
                    </span>
                  ) : (
                    <span className="truncate">no remote — only on this server</span>
                  )}
                </p>
                <p
                  className="mt-0.5 truncate font-mono text-micro text-muted-foreground/80"
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

            {/* What it is made of. */}
            {repo.languages && repo.languages.length > 0 && (
              <div className="mt-3 space-y-1.5 px-3.5">
                <LanguageBar languages={repo.languages} />
                <LanguageList languages={repo.languages} />
              </div>
            )}

            {/* Where HEAD is, the shape of the tree on it, and what last
                happened there. */}
            <div className="mt-3 space-y-2 border-t border-hairline px-3.5 pt-2.5">
              <div className="flex min-w-0 items-center gap-2">
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
                <TreeState repo={repo} />
              </div>
              {/* At the height of a commit with its marks, which "no commits
                  yet" alone is not. */}
              <CommitLine
                className="min-h-4.5 min-w-0"
                sha={repo.head}
                subject={repo.subject}
                author={repo.author}
                at={repo.commitAt}
                empty={repo.empty}
              />
            </div>

            <div className="mt-auto pt-3">
              {worktrees.length > 0 && (
                <FootSection label="Worktrees" count={worktrees.length}>
                  <ul aria-label={`Worktrees of ${repo.name}`} className="space-y-px">
                    {worktrees.map((tree) => (
                      <WorktreeRow
                        key={tree.path}
                        worktree={tree}
                        main={repo.path}
                        pull={worktreePulls[tree.path]?.pulls[0]}
                        onOpen={() => onOpen(tree.path)}
                        onOpenPull={
                          onOpenPull ? (number) => onOpenPull(tree.path, number) : undefined
                        }
                        onRemove={
                          can("destructive") && onRemoveWorktree
                            ? () => onRemoveWorktree(tree, repo.path)
                            : undefined
                        }
                      />
                    ))}
                  </ul>
                </FootSection>
              )}

              {/* What is waiting to be merged. */}
              {shown.length > 0 ? (
                <FootSection
                  label="Pull requests"
                  count={open.length}
                  mark={<SourcePull aria-hidden className="size-3 text-(--pull-open)" />}
                >
                  {/* The container swallows the press: each row and each verb
                      in it is a control of its own, and none of them is "open
                      the repository". */}
                  <div onClick={(event) => event.stopPropagation()} className="space-y-1">
                    <ChoiceList aria-label={`Pull requests in ${repo.name}`}>
                      {shown.map((pull) => (
                        <PullRequestRow
                          key={pull.number}
                          compact
                          pull={pull}
                          verbs={verbsFor(pull)}
                          onOpen={onOpenPull ? () => onOpenPull(repo.path, pull.number) : undefined}
                        />
                      ))}
                    </ChoiceList>
                    {more > 0 && onOpenPull && (
                      <Button
                        size="xs"
                        variant="ghost"
                        className="w-full justify-start text-muted-foreground"
                        onClick={() => onOpenPull(repo.path)}
                      >
                        {plural(more, "more pull request")}
                        <ArrowRight className="ml-auto" />
                      </Button>
                    )}
                  </div>
                </FootSection>
              ) : (
                quiet && (
                  <p
                    className="flex min-w-0 items-center gap-1.5 border-t border-hairline px-3.5 py-2.5 text-hint text-muted-foreground"
                    // gh could not answer for this checkout — not signed in as
                    // its owner, most often: its own sentence is a hover away.
                    title={pulls?.error}
                  >
                    <SourcePull aria-hidden className="size-3.5 shrink-0 opacity-70" />
                    <span className="truncate">{quiet}</span>
                  </p>
                )
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

/** How a working tree stands: the bar of squares and the count, or "clean" in the success hue. */
function TreeState({ repo }: { repo: GitRepo }) {
  return (
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
        {repo.conflicts > 0
          ? plural(repo.conflicts, "conflict")
          : repo.dirty
            ? plural(repo.changes, "change")
            : "clean"}
      </span>
    </span>
  )
}

/** A part of the card's foot: an eyebrow with its count over a hairline, then its rows. */
function FootSection({
  label,
  count,
  mark,
  children,
}: {
  label: string
  count: number
  mark?: React.ReactNode
  children: React.ReactNode
}) {
  return (
    <section className="space-y-1.5 border-t border-hairline px-2 pt-2 pb-2">
      <h3 className="flex items-center gap-1.5 px-1.5">
        {mark}
        <span className="eyebrow">{label}</span>
        <span className="numeric text-micro text-muted-foreground">{count}</span>
      </h3>
      {children}
    </section>
  )
}

/**
 * One linked worktree, inside the card of the repository it belongs to: the
 * branch it has checked out, where it sits under the main checkout, how its
 * own tree stands, and the request open from its branch — the four things
 * that say which of three worktrees is the one to go back to.
 *
 * The row opens the worktree's own workspace; the request opens on its
 * GitHub tab there, since that is the checkout the request is about. Its
 * removal is laid out in the row rather than overlaid on it (§6), revealed
 * under the pointer like every row action.
 */
function WorktreeRow({
  worktree,
  main,
  pull,
  onOpen,
  onOpenPull,
  onRemove,
}: {
  worktree: GitRepo
  main: string
  pull?: GitPullRequest
  onOpen: () => void
  onOpenPull?: (number: number) => void
  onRemove?: () => void
}) {
  const label = branchLabel(worktree.branch, worktree.detached)
  return (
    <li
      onClick={(event) => {
        event.stopPropagation()
        if ((event.target as HTMLElement).closest(CONTROL)) return
        onOpen()
      }}
      className="group/row flex min-w-0 items-center gap-2 rounded-md px-1.5 py-1 transition-colors hover:bg-row-hover"
    >
      <SourceBranch
        aria-hidden
        className={cn(
          "size-3.5 shrink-0",
          worktree.detached ? "text-destructive" : "text-muted-foreground",
        )}
      />
      <div className="min-w-0 flex-1">
        <button
          type="button"
          onClick={(event) => {
            event.stopPropagation()
            onOpen()
          }}
          title={`Open the worktree on ${label}`}
          className="block max-w-full truncate rounded-sm text-left font-mono text-hint font-medium text-foreground focus-ring"
        >
          {label}
        </button>
        <p className="truncate font-mono text-micro text-muted-foreground" title={worktree.path}>
          {worktreePlace(worktree, main)}
        </p>
      </div>
      {pull && (
        <button
          type="button"
          onClick={(event) => {
            event.stopPropagation()
            onOpenPull?.(pull.number)
          }}
          disabled={!onOpenPull}
          title={pull.title}
          className="flex shrink-0 items-center gap-1 rounded-sm px-1 text-hint focus-ring hover:bg-accent"
        >
          <PullStateMark pull={pull} className="size-3.5" />
          <span className="numeric text-foreground/85">#{pull.number}</span>
          <ChecksMark checks={pull.checks} />
        </button>
      )}
      <AheadBehind ahead={worktree.ahead} behind={worktree.behind} />
      <TreeState repo={worktree} />
      {onRemove && (
        <IconAction
          label={
            worktree.dirty ? `Delete the worktree on ${label}` : `Remove the worktree on ${label}`
          }
          reveal
          revealGroup="row"
          className="size-6 text-muted-foreground hover:text-destructive"
          onClick={(event) => {
            event.stopPropagation()
            onRemove()
          }}
        >
          <Trash />
        </IconAction>
      )}
    </li>
  )
}
