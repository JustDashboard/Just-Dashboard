"use client"

import { useEffect, useState } from "react"
import {
  BranchPlus,
  ClockRewind,
  Copy,
  CornerUpLeft,
  Cross,
  GitTag,
  RotateCounterClockwise,
  Warning,
} from "@/components/icons"
import { get, post } from "@/lib/api"
import { notify } from "@/lib/toast"
import { copyText } from "@/lib/clipboard"
import { relativeTime } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { GitCommit, GitResult } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import type { ConfirmRequest } from "@/components/confirm-dialog"
import { NameDialog } from "@/components/git/name-dialog"
import type { GitPreview } from "@/components/git/preview-panel"
import { RefTags } from "@/components/git/ref-tags"
import type { GitRun } from "@/components/git/run"
import { SearchInput } from "@/components/page"
import { EmptyState, ErrorState, LoadingRows } from "@/components/state"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { VerbActions, type Verb } from "@/components/verbs"

const PAGE = 100

/**
 * The repository's history: every commit a click from its message and the
 * files it touched, searchable by what the message says, narrowable to one
 * file, and paged rather than capped — the two hundredth commit used to be
 * the last one anybody could reach.
 *
 * Each commit carries its verbs behind one menu, where each gets a sentence:
 * branch or tag from here, copy the id, cherry-pick or revert it, and — for
 * anyone with the destructive capability — undo the branch back to it. That
 * last one is the recoverable half of reset (mixed): the working tree is
 * never touched, so nothing is lost.
 */
export function HistoryPanel({
  repoPath,
  branch,
  file,
  onClearFile,
  busy,
  canControl,
  canDestruct,
  run,
  confirm,
  onSelect,
  active,
  onChanged,
}: {
  repoPath: string
  branch: string
  /** Narrow the history to one file's commits. */
  file?: string
  onClearFile: () => void
  busy?: string
  canControl: boolean
  canDestruct: boolean
  run: GitRun
  confirm: (req: ConfirmRequest) => void
  onSelect: (p: GitPreview) => void
  active?: string
  onChanged: () => void
}) {
  const [search, setSearch] = useState("")
  const [term, setTerm] = useState("")
  const [loadingMore, setLoadingMore] = useState(false)
  const [naming, setNaming] = useState<{ kind: "branch" | "tag"; commit: GitCommit } | null>(null)

  // Typing settles for a third of a second before it becomes a query — a
  // history search is a git process per keystroke otherwise.
  useEffect(() => {
    const timer = setTimeout(() => setTerm(search.trim()), 300)
    return () => clearTimeout(timer)
  }, [search])

  // The first page follows the query; the pages after it are appended by the
  // button and tagged with the query they belong to, so a query change shows
  // the skeleton rather than the previous query's tail.
  const first = usePoll(
    (signal) =>
      get<GitCommit[]>(
        "/git/log",
        { path: repoPath, limit: PAGE, search: term || undefined, file },
        signal,
      ),
    0,
    [repoPath, term, file ?? ""],
  )
  const key = `${repoPath}|${term}|${file ?? ""}`
  const [extra, setExtra] = useState<{ key: string; commits: GitCommit[]; exhausted: boolean }>()
  const tail = extra?.key === key ? extra : undefined
  const commits = first.data ? [...first.data, ...(tail?.commits ?? [])] : undefined
  const exhausted = first.data ? (tail ? tail.exhausted : first.data.length < PAGE) : false
  const error = first.error

  const loadMore = async () => {
    if (!commits) return
    setLoadingMore(true)
    try {
      const rows = await get<GitCommit[]>("/git/log", {
        path: repoPath,
        limit: PAGE,
        skip: commits.length,
        search: term || undefined,
        file,
      })
      setExtra((prev) => ({
        key,
        commits: [...(prev?.key === key ? prev.commits : []), ...rows],
        exhausted: rows.length < PAGE,
      }))
    } catch (err) {
      notify.error("Could not load older commits", err)
    } finally {
      setLoadingMore(false)
    }
  }

  const q = { path: repoPath }

  const resetTo = (c: GitCommit, hard: boolean) =>
    confirm({
      title: hard ? `Reset hard to ${c.short}` : `Undo commits back to ${c.short}`,
      phrase: hard ? "reset hard" : undefined,
      confirmLabel: hard ? "Reset hard" : "Undo to here",
      description: hard ? (
        <p className="text-destructive">
          The branch moves back to <span className="font-mono">{c.short}</span> — “{c.subject}”
          — and every file is overwritten to match it. Commits after it and every uncommitted
          change are gone.
        </p>
      ) : (
        <div className="space-y-1.5">
          <p>
            The branch moves back to <span className="font-mono">{c.short}</span> — “{c.subject}”.
            Every commit after it is undone, but the changes they made stay in your working tree as
            edits you can re-commit or discard.
          </p>
          <p className="text-muted-foreground">Your files are not touched. Nothing is deleted.</p>
        </div>
      ),
      action: async (phrase) => {
        await post("/git/reset", { ref: c.sha, hard }, { confirm: phrase, query: q })
        onChanged()
      },
    })

  const verbsFor = (c: GitCommit, i: number): Verb[] => {
    const verbs: Verb[] = [
      {
        key: "copy",
        label: "Copy SHA",
        detail: "Put the full commit id on the clipboard.",
        icon: Copy,
        run: () => void copyText(c.sha, "Commit id copied"),
      },
    ]
    if (canControl) {
      verbs.push(
        {
          key: "branch",
          label: "Branch from here",
          detail: "Start a new branch at this commit and switch to it.",
          icon: BranchPlus,
          run: () => setNaming({ kind: "branch", commit: c }),
        },
        {
          key: "tag",
          label: "Tag this commit",
          detail: "Pin a name to this commit — a release, a point to come back to.",
          icon: GitTag,
          run: () => setNaming({ kind: "tag", commit: c }),
        },
        {
          key: "cherry",
          label: `Cherry-pick onto ${branch}`,
          detail: "Copy this one commit onto the current branch.",
          icon: CornerUpLeft,
          disabled: !!busy,
          run: () =>
            void run("Cherry-picked", () =>
              post<GitResult>("/git/cherry-pick", { ref: c.sha }, { query: q }),
            ).catch(() => undefined),
        },
        {
          key: "revert",
          label: "Revert this commit",
          detail:
            "Record a new commit that undoes this one. History keeps both, so it is safe after a push.",
          icon: RotateCounterClockwise,
          disabled: !!busy,
          run: () =>
            confirm({
              title: `Revert ${c.short}`,
              confirmLabel: "Revert",
              description: (
                <p>
                  A new commit is recorded that undoes “{c.subject}”. Nothing is rewritten; if the
                  undo clashes with later changes, git gives up cleanly and says which files.
                </p>
              ),
              action: async () => {
                await run("Reverted", () =>
                  post<GitResult>("/git/revert", { ref: c.sha }, { query: q }),
                )
              },
            }),
        },
      )
    }
    // Undoing to the current tip is a no-op, so it is offered only below it.
    if (canDestruct && i > 0 && !file && !term) {
      verbs.push(
        {
          key: "undo",
          label: "Undo to here, keep changes",
          detail: "Move the branch back to this commit; the later edits stay as uncommitted changes.",
          icon: ClockRewind,
          danger: true,
          run: () => resetTo(c, false),
        },
        {
          key: "hard",
          label: "Reset hard to here",
          detail: "Move the branch back and overwrite every file to match. Nothing after it survives.",
          icon: Warning,
          danger: true,
          run: () => resetTo(c, true),
        },
      )
    }
    return verbs
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="flex shrink-0 items-center gap-1.5 border-b border-hairline px-2 py-1.5">
        <SearchInput
          dense
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          placeholder="Search commit messages…"
          aria-label="Search commit messages"
          containerClassName="min-w-0 flex-1 sm:w-auto"
        />
      </div>
      {file && (
        <div className="flex shrink-0 items-center gap-1.5 border-b border-hairline bg-surface-header/60 px-3 py-1.5 text-hint text-muted-foreground">
          <span className="shrink-0">History of</span>
          <span className="min-w-0 flex-1 truncate font-mono text-foreground" title={file}>
            {file}
          </span>
          <Button
            type="button"
            size="sm"
            variant="ghost"
            aria-label="Show the whole history"
            className="size-6 shrink-0 p-0 text-muted-foreground hover:text-foreground"
            onClick={onClearFile}
          >
            <Cross className="size-3.5" />
          </Button>
        </div>
      )}

      <div className="min-h-0 flex-1 overflow-auto">
        {error && <ErrorState error={error} className="m-3" />}
        {!commits && !error && <LoadingRows className="p-3" rows={6} />}
        {commits && commits.length === 0 && (
          <EmptyState
            className="m-3"
            icon={ClockRewind}
            title={term || file ? "No commits match" : "No commits yet"}
            description={
              term || file
                ? "Try another word, or clear the filter."
                : "Once you commit, every version shows up here."
            }
          />
        )}
        {commits && commits.length > 0 && (
          <ul className="animate-rise divide-y divide-hairline">
            {commits.map((c, i) => (
              <li
                key={c.sha}
                className={cn(
                  "group flex min-w-0 items-start gap-2 py-1.5 pr-1.5 pl-3 transition-colors hover:bg-row-hover",
                  active === `commit:${c.sha}` && "bg-accent",
                )}
              >
                <button
                  type="button"
                  aria-pressed={active === `commit:${c.sha}`}
                  onClick={() =>
                    onSelect({ kind: "commit", sha: c.sha, subject: c.subject, file })
                  }
                  className="min-w-0 flex-1 text-left focus-ring-inset"
                >
                  <span className="flex min-w-0 items-center gap-1.5">
                    <span className="truncate text-body">{c.subject}</span>
                    {c.isMerge && <Tag>merge</Tag>}
                  </span>
                  <span className="mt-0.5 flex min-w-0 items-center gap-1.5">
                    <span className="truncate text-hint text-muted-foreground">
                      <span className="font-mono">{c.short}</span> · {c.author} ·{" "}
                      {relativeTime(c.at)}
                    </span>
                    <RefTags refs={c.refs} className="hidden min-w-0 items-center gap-1 sm:flex" />
                  </span>
                </button>
                {(c.insertions > 0 || c.deletions > 0) && (
                  <span className="numeric mt-0.5 shrink-0 font-mono text-hint">
                    <span className="text-(--git-added)">+{c.insertions}</span>{" "}
                    <span className="text-(--git-deleted)">−{c.deletions}</span>
                  </span>
                )}
                <VerbActions verbs={verbsFor(c, i)} reveal className="mt-0.5" />
              </li>
            ))}
          </ul>
        )}
        {commits && commits.length > 0 && !exhausted && (
          <div className="p-2">
            <Button
              size="sm"
              variant="outline"
              className="w-full"
              disabled={loadingMore}
              pending={loadingMore}
              onClick={() => void loadMore()}
            >
              Load older commits
            </Button>
          </div>
        )}
      </div>

      <NameDialog
        open={naming?.kind === "branch"}
        onOpenChange={(o) => !o && setNaming(null)}
        title="Branch from this commit"
        label="Branch name"
        hint="Letters, digits, dots, dashes and slashes. You switch to it straight away."
        placeholder="fix/the-thing"
        facts={naming ? [{ label: "from", value: naming.commit.short, mono: true }] : undefined}
        confirmLabel="Create and switch"
        onSubmit={async (name) => {
          if (!naming) return
          await run(`Created ${name}`, () =>
            post<GitResult>("/git/branch", { ref: name, from: naming.commit.sha }, { query: q }),
          )
        }}
      />
      <NameDialog
        open={naming?.kind === "tag"}
        onOpenChange={(o) => !o && setNaming(null)}
        title="Tag this commit"
        label="Tag name"
        hint="v1.2.0 is the usual shape. Tags are local until they are pushed."
        placeholder="v1.0.0"
        facts={naming ? [{ label: "at", value: naming.commit.short, mono: true }] : undefined}
        message={{
          label: "Message",
          hint: "With a message the tag records who made it and when — what a release wants. Leave it empty for a plain marker.",
          placeholder: "Release notes, or what this point is",
        }}
        confirmLabel="Create tag"
        onSubmit={async (name, message) => {
          if (!naming) return
          await run(`Tagged ${name}`, () =>
            post<GitResult>("/git/tag", { name, ref: naming.commit.sha, message }, { query: q }),
          )
        }}
      />
    </div>
  )
}
