"use client"

import { Trash } from "@/components/icons"
import { worktreePlace, type RepoPulls } from "@/lib/git-repos"
import type { GitRepo } from "@/lib/types"
import { cn } from "@/lib/utils"
import { useAuth } from "@/hooks/use-auth"
import { AheadBehind } from "@/components/git/ahead-behind"
import { SourceBranch } from "@/components/git/glyphs"
import { RepoMark } from "@/components/git/languages"
import { TreeState, branchLabel } from "@/components/git/marks"
import { ChecksMark, PullStateMark } from "@/components/git/pull-state"
import { ChoiceList, ChoiceRow, GroupRule } from "@/components/flow"
import { DimActions, IconAction } from "@/components/icon-action"

/** The id the section is reached by, from a card's worktree count. */
export const WORKTREES_SECTION = "git-worktrees"

/**
 * The linked worktrees on this host, as one section under the repository
 * shelves (`splitWorktrees` says why they are not inside the cards).
 *
 * Each row is a worktree to enter: the branch it has checked out, which
 * repository it belongs to — its logo and its name, so ten worktrees of three
 * repositories are told apart without reading paths — and where it sits under
 * that repository, then the request open from its branch in its state's
 * colour, how far the branch has drifted, how its own tree stands, and its
 * removal (`remove-checkout.tsx` decides how heavy that is). The rows are
 * choices, so they carry the lit edge (§16), two to a row on a wide screen
 * so a long list stays short.
 */
export function WorktreeSection({
  worktrees,
  mains,
  pulls,
  onOpen,
  onOpenPull,
  onRemove,
}: {
  worktrees: GitRepo[]
  /** The main checkouts the page lists, by path, to name each worktree's repository. */
  mains: Record<string, GitRepo>
  /** The requests assigned to each checkout, by its path. */
  pulls: Record<string, RepoPulls>
  onOpen: (path: string) => void
  onOpenPull: (path: string, number?: number) => void
  onRemove: (worktree: GitRepo, main: string) => void
}) {
  const { can } = useAuth()
  return (
    <section id={WORKTREES_SECTION} className="flex min-w-0 scroll-mt-4 flex-col gap-2.5">
      <GroupRule
        label="Worktrees"
        count={worktrees.length}
        leading={<SourceBranch aria-hidden className="size-4 text-muted-foreground" />}
      />
      <ChoiceList aria-label="Worktrees" className="grid gap-2 space-y-0 xl:grid-cols-2">
        {worktrees.map((tree, index) => {
          const main = tree.main ? mains[tree.main] : undefined
          const label = branchLabel(tree.branch, tree.detached)
          const pull = pulls[tree.path]?.pulls[0]
          const more = (pulls[tree.path]?.pulls.length ?? 0) - 1
          return (
            <ChoiceRow
              key={tree.path}
              index={index}
              workspaceItem={{ id: tree.path, name: label }}
              leading={<RepoMark repo={main ?? tree} size="sm" />}
              title={
                <span className={cn("font-mono text-xs", tree.detached && "text-destructive")}>
                  {label}
                </span>
              }
              verb={`Open the worktree on ${label}`}
              description={
                <span className="flex min-w-0 items-center gap-1.5">
                  <span className="shrink-0 font-medium text-foreground/80">
                    {main?.name ?? "worktree"}
                  </span>
                  <span className="min-w-0 truncate font-mono text-micro" title={tree.path}>
                    {tree.main ? worktreePlace(tree, tree.main) : tree.path}
                  </span>
                </span>
              }
              trailing={
                <>
                  {pull && (
                    <button
                      type="button"
                      onClick={() => onOpenPull(tree.path, pull.number)}
                      title={pull.title}
                      aria-label={`Open pull request #${pull.number}`}
                      className="flex shrink-0 items-center gap-1 rounded-sm px-1 text-hint focus-ring hover:bg-accent"
                    >
                      <PullStateMark pull={pull} className="size-3.5" />
                      <span className="numeric text-foreground/85">#{pull.number}</span>
                      <ChecksMark checks={pull.checks} />
                      {more > 0 && <span className="numeric text-muted-foreground">+{more}</span>}
                    </button>
                  )}
                  <AheadBehind ahead={tree.ahead} behind={tree.behind} />
                  <TreeState repo={tree} />
                </>
              }
              actions={
                can("destructive") && tree.main ? (
                  <DimActions>
                    <IconAction
                      label={
                        tree.dirty
                          ? `Delete the worktree on ${label}`
                          : `Remove the worktree on ${label}`
                      }
                      className="text-muted-foreground hover:text-destructive"
                      onClick={() => onRemove(tree, tree.main!)}
                    >
                      <Trash />
                    </IconAction>
                  </DimActions>
                ) : undefined
              }
              onSelect={() => onOpen(tree.path)}
            />
          )
        })}
      </ChoiceList>
    </section>
  )
}
