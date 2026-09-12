"use client"

import { ClockRewind, CornerUpLeft, GitCommit as GitCommitIcon } from "@/components/icons"
import { get, post } from "@/lib/api"
import { relativeTime, timestamp } from "@/lib/format"
import type { GitCommit } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import type { ConfirmRequest } from "@/components/confirm-dialog"
import type { GitPreview } from "@/components/git/preview-panel"
import { EmptyState, ErrorState, LoadingRows } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { rowReveal } from "@/components/icon-action"
import { cn } from "@/lib/utils"

/**
 * The repository's history. Each commit opens as a diff in the preview column,
 * and — for anyone with the destructive capability — carries an "undo to here"
 * that moves the branch back to that commit while keeping the changes since as
 * uncommitted edits. That is the recoverable half of reset (mixed), which is
 * the one a non-expert reaches for when they say "undo my last commit"; the
 * working tree is never touched, so nothing is lost.
 */
export function HistoryPanel({
  repoPath,
  canDestruct,
  confirm,
  onSelectDiff,
  onChanged,
}: {
  repoPath: string
  canDestruct: boolean
  confirm: (req: ConfirmRequest) => void
  onSelectDiff: (p: GitPreview) => void
  onChanged: () => void
}) {
  const log = usePoll(
    (signal) => get<GitCommit[]>("/git/log", { path: repoPath, limit: 200 }, signal),
    0,
    [repoPath],
  )

  const show = async (c: GitCommit) => {
    onSelectDiff({
      kind: "diff",
      title: c.subject,
      subtitle: `${c.short} · loading…`,
      body: "Loading…",
    })
    try {
      const res = await get<{ diff: string }>("/git/diff", { path: repoPath, ref: c.sha })
      onSelectDiff({
        kind: "diff",
        title: c.subject,
        subtitle: `${c.short} · ${c.author} · ${timestamp(c.at)}`,
        body: res.diff,
      })
    } catch (err) {
      onSelectDiff({ kind: "diff", title: c.subject, subtitle: c.short, body: String(err) })
    }
  }

  const resetTo = (c: GitCommit) =>
    confirm({
      title: `Undo commits back to ${c.short}`,
      confirmLabel: "Undo to here",
      description: (
        <div className="space-y-1.5">
          <p>
            The branch moves back to <span className="font-mono">{c.short}</span> — “{c.subject}”.
            Every commit after it is undone, but the changes they made stay in your working tree as
            edits you can re-commit or discard.
          </p>
          <p className="text-muted-foreground">Your files are not touched. Nothing is deleted.</p>
        </div>
      ),
      action: async () => {
        await post("/git/reset", { ref: c.sha, hard: false }, { query: { path: repoPath } })
        log.refresh()
        onChanged()
      },
    })

  if (log.error) return <ErrorState error={log.error} className="m-3" />
  if (log.loading && !log.data) return <LoadingRows className="p-3" rows={6} />
  if (!log.data?.length) {
    return (
      <EmptyState
        className="m-3"
        icon={ClockRewind}
        title="No commits yet"
        description="Once you commit, every version shows up here."
      />
    )
  }

  return (
    <div className="min-h-0 flex-1 space-y-0.5 overflow-auto p-1.5">
      {log.data.map((c, i) => (
        <div
          key={c.sha}
          className="group flex min-w-0 items-start gap-2 rounded-md px-2 py-1.5 hover:bg-row-hover"
        >
          <GitCommitIcon className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
          <button onClick={() => show(c)} className="min-w-0 flex-1 text-left">
            <p className="truncate text-body">{c.subject}</p>
            <p className="truncate text-hint text-muted-foreground">
              <span className="font-mono">{c.short}</span> · {c.author} · {relativeTime(c.at)}
              {c.isMerge ? " · merge" : ""}
            </p>
          </button>
          {(c.insertions > 0 || c.deletions > 0) && (
            <span className="numeric mt-0.5 shrink-0 font-mono text-hint">
              <span className="text-(--git-added)">+{c.insertions}</span>{" "}
              <span className="text-(--git-deleted)">−{c.deletions}</span>
            </span>
          )}
          {/* Undoing to the current tip is a no-op, so it is offered only below it. */}
          {canDestruct && i > 0 && (
            <Tooltip>
              <TooltipTrigger asChild>
                <Button
                  size="sm"
                  variant="ghost"
                  aria-label="Undo to here"
                  className={cn(
                    "size-6 shrink-0 p-0 text-muted-foreground hover:text-foreground",
                    rowReveal(),
                  )}
                  onClick={() => resetTo(c)}
                >
                  <CornerUpLeft className="size-3.5" />
                </Button>
              </TooltipTrigger>
              <TooltipContent>Undo commits back to here, keeping your changes</TooltipContent>
            </Tooltip>
          )}
        </div>
      ))}
    </div>
  )
}
