"use client"

import { useState } from "react"
import {
  Check,
  CloudUpload,
  GitCommit,
  Minus,
  Plus,
  RotateCounterClockwise,
} from "@/components/icons"
import { notify } from "@/lib/toast"
import { get, post } from "@/lib/api"
import { cn } from "@/lib/utils"
import { describeChange, gitLetter, gitStyle, gitTone } from "@/lib/git-status"
import type { GitFileChange, GitResult, GitStatus } from "@/lib/types"
import type { usePoll } from "@/hooks/use-poll"
import type { ConfirmRequest } from "@/components/confirm-dialog"
import { GitExplain } from "@/components/git/help"
import type { GitPreview } from "@/components/git/preview-panel"
import { EmptyState, ErrorState, LoadingRows } from "@/components/state"
import { RowActions } from "@/components/icon-action"
import { PaneHeader } from "@/components/panel"
import { Button } from "@/components/ui/button"
import { Textarea } from "@/components/ui/textarea"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"

type GitRun = (label: string, fn: () => Promise<GitResult>) => Promise<GitResult>

/**
 * The staging-and-commit half of the workspace, written to be legible to
 * someone who has never staged anything: two labelled lists — what is about to
 * be committed, and what is not yet chosen — and a commit box that spells the
 * step out. Each file carries the one or two actions that make sense for where
 * it is, and clicking its name shows the diff in the preview column.
 */
export function ChangesPanel({
  repoPath,
  status,
  busy,
  canControl,
  canDestruct,
  run,
  confirm,
  onSelectDiff,
  activePath,
  committer,
}: {
  repoPath: string
  status: ReturnType<typeof usePoll<GitStatus>>
  busy?: string
  canControl: boolean
  canDestruct: boolean
  run: GitRun
  confirm: (req: ConfirmRequest) => void
  onSelectDiff: (p: GitPreview) => void
  activePath?: string
  /** The identity git will record, when the page knows it. */
  committer?: string
}) {
  const [message, setMessage] = useState("")
  const q = { path: repoPath }

  const showDiff = async (file: GitFileChange, staged: boolean) => {
    try {
      const res = await get<{ diff: string }>("/git/diff", {
        path: repoPath,
        file: file.path,
        staged: staged ? "true" : undefined,
      })
      onSelectDiff({
        kind: "diff",
        title: file.path,
        subtitle: staged ? "staged — ready to commit" : `working tree — ${file.label}`,
        body: res.diff || "No textual diff (binary file, or no line changes).",
        singleFile: true,
      })
    } catch (err) {
      notify.error("Could not read the diff", err)
    }
  }

  // Fire-and-forget: `run` already reports failures, so the rejection is
  // swallowed here rather than left to surface as an unhandled one.
  const stage = (files: string[]) =>
    void run("Staged", () => post<GitResult>("/git/stage", { files }, { query: q })).catch(() => {})
  const unstage = (files: string[]) =>
    void run("Unstaged", () => post<GitResult>("/git/unstage", { files }, { query: q })).catch(
      () => {},
    )

  const commit = async (thenPush: boolean) => {
    const msg = message.trim()
    if (!msg) {
      notify.error("Write a short message describing your changes first")
      return
    }
    try {
      await run("Committed", () => post<GitResult>("/git/commit", { message: msg }, { query: q }))
      setMessage("")
      if (thenPush) await run("Pushed", () => post<GitResult>("/git/push", undefined, { query: q }))
    } catch {
      /* run already surfaced it */
    }
  }

  const discard = (file: GitFileChange) =>
    confirm({
      title: `Discard changes to ${file.path.split("/").pop()}`,
      phrase: "discard changes",
      confirmLabel: "Discard",
      description: (
        <p className="text-destructive">
          Puts <span className="font-mono break-all">{file.path}</span> back the way it was at the
          last commit. The current edits are not recoverable.
        </p>
      ),
      action: async (c) => {
        await post("/git/discard", { file: file.path }, { confirm: c, query: q })
        status.refresh()
      },
    })

  const discardAll = () =>
    confirm({
      title: "Discard every change",
      phrase: "reset hard",
      confirmLabel: "Discard all",
      description: (
        <p className="text-destructive">
          Every tracked file is put back to the last commit. Untracked files are left alone. This
          cannot be undone.
        </p>
      ),
      action: async (c) => {
        await post("/git/reset", { ref: "HEAD", hard: true }, { confirm: c, query: q })
        status.refresh()
      },
    })

  if (status.error) return <ErrorState error={status.error} className="m-3" />
  if (status.loading && !status.data) return <LoadingRows className="p-3" rows={5} />

  const files = status.data?.files ?? []
  const staged = files.filter((f) => f.staged)
  const unstaged = files.filter((f) => !f.staged)
  const canCommit = staged.length > 0 && message.trim().length > 0

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="min-h-0 flex-1 overflow-auto">
        {files.length === 0 ? (
          <div className="p-4">
            <EmptyState
              icon={Check}
              title="Nothing to commit"
              description="You have no uncommitted changes. Edit a file — here or in a terminal — and it will show up ready to stage."
            />
          </div>
        ) : (
          <>
            {staged.length > 0 && (
              <Group
                label="Ready to commit"
                explain="stage"
                count={staged.length}
                tone="success"
                action={
                  canControl && (
                    <GroupAction
                      disabled={!!busy}
                      hint="Take every staged file back out of the next commit"
                      onClick={() => unstage([])}
                    >
                      Unstage all
                    </GroupAction>
                  )
                }
              >
                {staged.map((f) => (
                  <FileRow
                    key={"s" + f.path}
                    file={f}
                    active={activePath === f.path}
                    onClick={() => showDiff(f, true)}
                    actions={
                      canControl && (
                        <RowAction
                          label="Unstage"
                          disabled={!!busy}
                          onClick={() => unstage([f.path])}
                        >
                          <Minus className="size-3.5" />
                        </RowAction>
                      )
                    }
                  />
                ))}
              </Group>
            )}

            {unstaged.length > 0 && (
              <Group
                label="Changes"
                explain="changes"
                count={unstaged.length}
                tone="warning"
                action={
                  <>
                    {canDestruct && (
                      <GroupAction
                        danger
                        disabled={!!busy}
                        hint="Restore every tracked file to HEAD — untracked files are left alone"
                        onClick={discardAll}
                      >
                        Discard all
                      </GroupAction>
                    )}
                    {canControl && (
                      <GroupAction
                        disabled={!!busy}
                        hint="Stage every change in the working tree"
                        onClick={() => stage([])}
                      >
                        Stage all
                      </GroupAction>
                    )}
                  </>
                }
              >
                {unstaged.map((f) => (
                  <FileRow
                    key={"u" + f.path}
                    file={f}
                    active={activePath === f.path}
                    onClick={() => showDiff(f, false)}
                    actions={
                      <>
                        {canDestruct && f.label !== "untracked" && (
                          <RowAction
                            label="Discard"
                            className="text-destructive"
                            disabled={!!busy}
                            onClick={() => discard(f)}
                          >
                            <RotateCounterClockwise className="size-3.5" />
                          </RowAction>
                        )}
                        {canControl && (
                          <RowAction
                            label="Stage"
                            disabled={!!busy}
                            onClick={() => stage([f.path])}
                          >
                            <Plus className="size-3.5" />
                          </RowAction>
                        )}
                      </>
                    }
                  />
                ))}
              </Group>
            )}
          </>
        )}
      </div>

      {canControl && (
        <div className="shrink-0 space-y-2 border-t border-hairline bg-surface-header/60 p-3">
          <Textarea
            value={message}
            onChange={(e) => setMessage(e.target.value)}
            onKeyDown={(e) => {
              if ((e.metaKey || e.ctrlKey) && e.key === "Enter" && canCommit) {
                e.preventDefault()
                void commit(false)
              }
            }}
            placeholder="Describe what you changed…"
            rows={3}
            className="resize-none font-mono text-xs"
          />
          <div className="flex items-center gap-2">
            {committer && (
              <span className="min-w-0 truncate text-hint text-muted-foreground">
                as {committer}
              </span>
            )}
            <span className="flex-1" />
            <Tooltip>
              <TooltipTrigger asChild>
                <Button
                  size="sm"
                  variant="outline"
                  disabled={!!busy || !canCommit}
                  onClick={() => void commit(true)}
                >
                  <CloudUpload className="size-3.5" />
                  Commit &amp; push
                </Button>
              </TooltipTrigger>
              <TooltipContent>
                Commit the staged changes, then push them to the remote
              </TooltipContent>
            </Tooltip>
            <Tooltip>
              <TooltipTrigger asChild>
                <Button
                  size="sm"
                  disabled={!!busy || !canCommit}
                  onClick={() => void commit(false)}
                >
                  <GitCommit className="size-3.5" />
                  Commit
                </Button>
              </TooltipTrigger>
              <TooltipContent>Commit the staged changes (Ctrl+Enter)</TooltipContent>
            </Tooltip>
          </div>
          {staged.length === 0 && (
            <p className="text-hint text-muted-foreground">
              Stage at least one change above before committing.
            </p>
          )}
        </div>
      )}
    </div>
  )
}

function Group({
  label,
  explain,
  count,
  tone,
  action,
  children,
}: {
  label: string
  explain: string
  count: number
  tone: "success" | "warning"
  action?: React.ReactNode
  children: React.ReactNode
}) {
  return (
    <div>
      <PaneHeader className="sticky top-0 z-10 gap-2 px-3 backdrop-blur">
        <span
          className={cn("size-1.5 rounded-full", tone === "success" ? "bg-success" : "bg-warning")}
        />
        <span className="text-xs font-medium">{label}</span>
        <GitExplain name={explain} />
        <span className="numeric text-hint text-muted-foreground">{count}</span>
        <span className="flex-1" />
        {action}
      </PaneHeader>
      {children}
    </div>
  )
}

function FileRow({
  file,
  active,
  onClick,
  actions,
}: {
  file: GitFileChange
  active?: boolean
  onClick: () => void
  actions?: React.ReactNode
}) {
  const tone = gitTone(file)
  return (
    <div
      className={cn(
        "group relative flex min-w-0 items-center gap-2 bg-(--git-tint) px-3 py-1.5",
        "before:absolute before:inset-y-0 before:left-0 before:w-[2px] before:bg-(--git-edge) before:content-['']",
        "hover:bg-row-hover",
        active && "bg-accent",
      )}
      style={gitStyle(tone)}
    >
      <Tooltip>
        <TooltipTrigger asChild>
          <span className="w-5 shrink-0 text-center font-mono text-micro text-(--git-colour)">
            {gitLetter(file)}
          </span>
        </TooltipTrigger>
        <TooltipContent>{describeChange(file)}</TooltipContent>
      </Tooltip>
      {/* The name carries no tooltip — see the same row in the terminal's git
          tab for why. */}
      <button
        onClick={onClick}
        className={cn(
          "min-w-0 flex-1 truncate text-left font-mono text-xs text-(--git-colour) hover:underline",
          file.label === "deleted" && "line-through",
        )}
      >
        {file.path}
      </button>
      <RowActions className="gap-0">{actions}</RowActions>
    </div>
  )
}

function RowAction({
  label,
  disabled,
  className,
  onClick,
  children,
}: {
  label: string
  disabled?: boolean
  className?: string
  onClick: () => void
  children: React.ReactNode
}) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button
          type="button"
          size="sm"
          variant="ghost"
          aria-label={label}
          disabled={disabled}
          className={cn(
            "size-6 shrink-0 p-0 text-muted-foreground hover:text-foreground",
            className,
          )}
          onClick={onClick}
        >
          {children}
        </Button>
      </TooltipTrigger>
      <TooltipContent>{label}</TooltipContent>
    </Tooltip>
  )
}

/**
 * A whole-group action. The visible label is the verb; the tooltip is the
 * scope, which is the half that decides whether you meant to press it.
 */
function GroupAction({
  disabled,
  danger,
  hint,
  onClick,
  children,
}: {
  disabled?: boolean
  danger?: boolean
  hint: string
  onClick: () => void
  children: React.ReactNode
}) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <button
          disabled={disabled}
          onClick={onClick}
          className={cn(
            "rounded-sm px-1.5 py-0.5 text-hint transition-colors disabled:opacity-40",
            danger
              ? "text-destructive hover:bg-wash-danger"
              : "text-muted-foreground hover:bg-accent hover:text-foreground",
          )}
        >
          {children}
        </button>
      </TooltipTrigger>
      <TooltipContent>{hint}</TooltipContent>
    </Tooltip>
  )
}
