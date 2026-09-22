"use client"

import { ArrowRight } from "@/components/icons"
import { plural } from "@/lib/format"
import type { GitRepo } from "@/lib/types"
import { cn } from "@/lib/utils"
import { AheadBehind } from "@/components/git/ahead-behind"
import { BranchChip, CommitLine, WorkingTreeBar } from "@/components/git/marks"
import { Tag } from "@/components/tag"
import { SpotlightBorder } from "@/components/ui/spotlight-border"

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
 * It is a **choice**, not a reading: every row here is a repository to enter,
 * which §15 pass 3 and §16 both settle — the lit edge belongs to things you
 * pick, wherever they are. The title is a real button whose accessible name is
 * the repository, and the press on the card around it is the convenience for
 * the pointer (§12).
 */
export function RepoRow({ repo, onOpen }: { repo: GitRepo; onOpen: () => void }) {
  return (
    <li className="min-w-0">
      <SpotlightBorder radius={420}>
        <div
          onClick={onOpen}
          className="group flex min-w-0 cursor-pointer items-center gap-3 rounded-xl px-3.5 py-2.5"
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
          </div>

          <ArrowRight
            aria-hidden
            className="size-3.5 shrink-0 text-muted-foreground transition-colors group-hover:text-foreground"
          />
        </div>
      </SpotlightBorder>
    </li>
  )
}
