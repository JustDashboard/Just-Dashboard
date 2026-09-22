"use client"

import { useState } from "react"
import { errorMessage, get, post } from "@/lib/api"
import type { GitConflict, GitResult } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { CodeEditor } from "@/components/code-editor"
import { PreviewHeader } from "@/components/git/preview-header"
import type { PreviewContext } from "@/components/git/preview-panel"
import { ErrorState, LoadingRows, Notice } from "@/components/state"
import { tabClasses } from "@/components/tabs"
import { Button } from "@/components/ui/button"

export function ConflictPreview({
  file,
  ctx,
  onClose,
}: {
  file: string
  ctx: PreviewContext
  onClose: () => void
}) {
  const conflict = usePoll(
    (signal) => get<GitConflict>("/git/conflict", { path: ctx.repoPath, file }, signal),
    0,
    [ctx.repoPath, file],
  )
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <PreviewHeader title={file} subtitle="Resolve conflict" onClose={onClose} />
      {conflict.error && (
        <ErrorState error={conflict.error} onRetry={conflict.refresh} className="m-3" />
      )}
      {conflict.loading && !conflict.data && <LoadingRows className="p-3" rows={8} />}
      {conflict.data && (
        <ConflictEditor
          key={conflict.data.version}
          conflict={conflict.data}
          ctx={ctx}
          onClose={onClose}
          onReload={conflict.refresh}
        />
      )}
    </div>
  )
}

function ConflictEditor({
  conflict,
  ctx,
  onClose,
  onReload,
}: {
  conflict: GitConflict
  ctx: PreviewContext
  onClose: () => void
  onReload: () => void
}) {
  const [side, setSide] = useState<"ours" | "theirs" | "base">("ours")
  const [result, setResult] = useState(conflict.result)
  const [error, setError] = useState<string>()
  const version = conflict[side]
  const canEdit = ctx.canControl && ctx.canWrite && conflict.editable
  const resolve = async () => {
    setError(undefined)
    try {
      await ctx.run("Conflict resolved", () =>
        post<GitResult>(
          "/git/conflict/resolve",
          { file: conflict.file, version: conflict.version, choice: "result", content: result },
          { query: { path: ctx.repoPath } },
        ),
      )
      onClose()
    } catch (err) {
      setError(errorMessage(err))
    }
  }
  const choose = (choice: "ours" | "theirs" | "delete") =>
    ctx.confirm({
      title:
        choice === "delete"
          ? `Delete ${conflict.file}`
          : `Use the ${choice === "ours" ? "current" : "incoming"} version`,
      phrase: "discard changes",
      confirmLabel: choice === "delete" ? "Delete and resolve" : "Use this version",
      description: (
        <p>
          The file <span className="font-mono">{conflict.file}</span> is{" "}
          {choice === "delete" ? "deleted" : "replaced with the selected version"} and marked
          resolved. Uncommitted edits to this file are discarded.
        </p>
      ),
      action: async (confirm) => {
        await ctx.run("Conflict resolved", () =>
          post<GitResult>(
            "/git/conflict/choose",
            { file: conflict.file, version: conflict.version, choice },
            { query: { path: ctx.repoPath }, confirm },
          ),
        )
        onClose()
      },
    })
  return (
    <div className="flex min-h-0 flex-1 flex-col overflow-auto">
      <div
        role="tablist"
        aria-label="Conflict versions"
        className="flex shrink-0 items-center border-b border-hairline px-2"
      >
        {(
          [
            ["ours", "Current"],
            ["theirs", "Incoming"],
            ["base", "Base"],
          ] as const
        ).map(([key, label]) => (
          <button
            type="button"
            role="tab"
            aria-selected={side === key}
            key={key}
            className={tabClasses(side === key, "h-9")}
            onClick={() => setSide(key)}
          >
            {label}
          </button>
        ))}
      </div>
      {conflict.operation === "rebase" && (
        <p className="px-3 py-2 text-hint text-muted-foreground">
          During a rebase, Current is the new base and Incoming is the commit being replayed.
        </p>
      )}
      <div className="max-h-48 min-h-16 shrink-0 overflow-auto border-b border-hairline">
        {!version.present ? (
          <p className="p-3 text-hint text-muted-foreground">This version has no file.</p>
        ) : version.mode === "160000" ? (
          <p className="p-3 font-mono text-hint">
            Submodule commit {version.object}. Choosing it records this commit in the parent
            repository; update the submodule checkout afterward.
          </p>
        ) : version.binary ? (
          <p className="p-3 text-hint text-muted-foreground">
            This version cannot be edited as text. Choose a complete version below.
          </p>
        ) : (
          <pre className="p-3 font-mono text-xs whitespace-pre">
            {version.content || "(empty file)"}
          </pre>
        )}
      </div>
      <div className="flex shrink-0 flex-wrap items-center gap-2 border-b border-hairline px-3 py-2">
        <span className="eyebrow min-w-0 flex-1">Result</span>
        {canEdit && (
          <>
            <Button
              size="xs"
              variant="outline"
              disabled={!conflict.ours.present || !!ctx.busy}
              onClick={() => setResult(conflict.ours.content)}
            >
              Use current
            </Button>
            <Button
              size="xs"
              variant="outline"
              disabled={!conflict.theirs.present || !!ctx.busy}
              onClick={() => setResult(conflict.theirs.content)}
            >
              Use incoming
            </Button>
          </>
        )}
        <Button size="xs" variant="ghost" disabled={!!ctx.busy} onClick={onReload}>
          Reload conflict
        </Button>
      </div>
      {error && (
        <Notice tone="danger" title="Could not resolve the conflict" className="m-3">
          {error}
        </Notice>
      )}
      {conflict.editable && (
        <div className="min-h-48 flex-1">
          <CodeEditor
            className="h-full"
            value={result}
            onChange={setResult}
            readOnly={!canEdit || !!ctx.busy}
          />
        </div>
      )}
      <div className="flex shrink-0 flex-wrap items-center gap-2 border-t border-hairline px-3 py-2">
        {ctx.canDestruct && (
          <Button
            size="xs"
            variant="ghost"
            className="text-destructive"
            disabled={!!ctx.busy}
            onClick={() => choose("delete")}
          >
            Delete file
          </Button>
        )}
        <span className="flex-1" />
        {ctx.canDestruct && !conflict.editable && (
          <>
            <Button
              size="xs"
              variant="outline"
              disabled={!conflict.ours.present || !!ctx.busy}
              onClick={() => choose("ours")}
            >
              Use current version
            </Button>
            <Button
              size="xs"
              variant="outline"
              disabled={!conflict.theirs.present || !!ctx.busy}
              onClick={() => choose("theirs")}
            >
              Use incoming version
            </Button>
          </>
        )}
        {canEdit && (
          <Button
            size="sm"
            disabled={!!ctx.busy}
            pending={ctx.busy === "Conflict resolved"}
            onClick={resolve}
          >
            Save and mark resolved
          </Button>
        )}
      </div>
    </div>
  )
}
