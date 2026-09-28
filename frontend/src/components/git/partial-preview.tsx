"use client"

import { useState } from "react"
import { errorMessage, get, post } from "@/lib/api"
import type { GitPartialDiff, GitResult } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { DiffView } from "@/components/files/diff-view"
import { PreviewHeader } from "@/components/git/preview-header"
import type { PreviewContext } from "@/components/git/preview-panel"
import { ErrorState, LoadingRows, Notice } from "@/components/state"
import { Button } from "@/components/ui/button"

export function PartialPreview({
  file,
  staged,
  ctx,
  onClose,
}: {
  file: string
  staged: boolean
  ctx: PreviewContext
  onClose: () => void
}) {
  const diff = usePoll(
    (signal) =>
      get<GitPartialDiff>(
        "/git/patch",
        { path: ctx.repoPath, file, staged: String(staged) },
        signal,
      ),
    0,
    [ctx.repoPath, file, staged],
  )
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <PreviewHeader
        title={file}
        subtitle={staged ? "Staged — ready to commit" : "Working tree"}
        onClose={onClose}
      />
      {diff.error && <ErrorState error={diff.error} onRetry={diff.refresh} className="m-3" />}
      {diff.loading && !diff.data && <LoadingRows className="p-3" rows={8} />}
      {diff.data && (
        <PartialSelection
          key={diff.data.version}
          diff={diff.data}
          ctx={ctx}
          onReload={diff.refresh}
        />
      )}
    </div>
  )
}

function PartialSelection({
  diff,
  ctx,
  onReload,
}: {
  diff: GitPartialDiff
  ctx: PreviewContext
  onReload: () => void
}) {
  const [selected, setSelected] = useState<number[]>([])
  const [error, setError] = useState<string>()
  const apply = async (whole = false) => {
    setError(undefined)
    try {
      await ctx.run(diff.staged ? "Unstaged" : "Staged", () =>
        whole
          ? post<GitResult>(
              diff.staged ? "/git/unstage" : "/git/stage",
              { files: [diff.file] },
              { query: { path: ctx.repoPath } },
            )
          : post<GitResult>(
              "/git/patch/stage",
              { file: diff.file, staged: diff.staged, version: diff.version, lines: selected },
              { query: { path: ctx.repoPath } },
            ),
      )
      onReload()
    } catch (err) {
      setError(errorMessage(err))
    }
  }
  return (
    <>
      {error && (
        <Notice title="Could not apply the selection" tone="danger" className="m-3">
          {error}
        </Notice>
      )}
      {diff.reason && <p className="px-3 py-2 text-hint text-muted-foreground">{diff.reason}</p>}
      <DiffView
        lineNumbers
        singleFile
        body={diff.body || "No textual changes."}
        className="min-h-0 flex-1"
        selection={
          ctx.canControl && !diff.reason
            ? { lines: diff.lines, selected, onChange: setSelected, disabled: !!ctx.busy }
            : undefined
        }
      />
      <div className="flex shrink-0 flex-wrap items-center gap-2 border-t border-hairline px-3 py-2">
        <Button size="xs" variant="ghost" disabled={!!ctx.busy} onClick={onReload}>
          Reload diff
        </Button>
        <span className="flex-1" />
        {ctx.canControl && (
          <>
            <Button
              size="xs"
              variant="outline"
              disabled={!!ctx.busy}
              onClick={() => void apply(true)}
            >
              {diff.staged ? "Unstage file" : "Stage file"}
            </Button>
            {!diff.reason && (
              <Button
                size="xs"
                disabled={!!ctx.busy || selected.length === 0}
                onClick={() => void apply()}
              >
                {diff.staged ? "Unstage" : "Stage"} selected ({selected.length})
              </Button>
            )}
          </>
        )}
      </div>
    </>
  )
}
