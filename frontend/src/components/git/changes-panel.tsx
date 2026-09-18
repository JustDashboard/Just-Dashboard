"use client"

import { useState } from "react"
import {
  Archive,
  Check,
  CloudUpload,
  GitCommit,
  Minus,
  Plus,
  RotateCounterClockwise,
  Trash,
  Warning,
} from "@/components/icons"
import { notify } from "@/lib/toast"
import { get, post } from "@/lib/api"
import { relativeTime } from "@/lib/format"
import { cn } from "@/lib/utils"
import { describeChange, gitLetter, gitStyle, gitTone, type GitSide } from "@/lib/git-status"
import type { GitFileChange, GitResult, GitStash, GitStatus } from "@/lib/types"
import type { usePoll } from "@/hooks/use-poll"
import type { ConfirmRequest } from "@/components/confirm-dialog"
import { GitExplain } from "@/components/git/help"
import { IdentityDialog } from "@/components/git/identity-dialog"
import type { GitPreview } from "@/components/git/preview-panel"
import type { GitRun } from "@/components/git/run"
import { EmptyState, ErrorState, LoadingRows } from "@/components/state"
import { RowActions } from "@/components/icon-action"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Textarea } from "@/components/ui/textarea"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"

/**
 * The staging-and-commit half of the workspace, written to be legible to
 * someone who has never staged anything: two labelled lists — what is about to
 * be committed, and what is not yet chosen — and a commit box that spells the
 * step out. Each file carries the one or two actions that make sense for where
 * it is, and clicking its name shows the diff in the preview column.
 *
 * A file staged and then edited again appears in both lists, once per side,
 * with the letter for that side: it used to appear once, under "ready to
 * commit", and the second edit was invisible until the commit went out
 * without it. Stashes sit at the top, because "where did my changes go" is
 * answered here or nowhere.
 */
export function ChangesPanel({
  repoPath,
  status,
  stashes,
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
  status: ReturnType<typeof usePoll<GitStatus>>
  stashes: GitStash[]
  busy?: string
  canControl: boolean
  canDestruct: boolean
  run: GitRun
  confirm: (req: ConfirmRequest) => void
  onSelect: (p: GitPreview) => void
  /** What the preview column is showing, so the row that opened it reads as chosen. */
  active?: string
  onChanged: () => void
}) {
  const [message, setMessage] = useState("")
  const [amend, setAmend] = useState(false)
  const [identityOpen, setIdentityOpen] = useState(false)
  const q = { path: repoPath }

  const showDiff = async (file: GitFileChange, side: GitSide) => {
    try {
      const res = await get<{ diff: string }>("/git/diff", {
        path: repoPath,
        file: file.path,
        staged: side === "staged" ? "true" : undefined,
      })
      onSelect({
        kind: "diff",
        title: file.path,
        subtitle: side === "staged" ? "staged — ready to commit" : `working tree — ${file.label}`,
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
    if (!msg && !amend) {
      notify.error("Write a short message describing your changes first")
      return
    }
    try {
      await run("Committed", () =>
        post<GitResult>("/git/commit", { message: msg, amend }, { query: q }),
      )
      setMessage("")
      setAmend(false)
      if (thenPush) await run("Pushed", () => post<GitResult>("/git/push", undefined, { query: q }))
    } catch {
      /* run already surfaced it */
    }
  }

  const discard = (file: GitFileChange) => {
    const untracked = file.label === "untracked"
    confirm({
      title: untracked
        ? `Delete ${file.path.split("/").pop()}`
        : `Discard changes to ${file.path.split("/").pop()}`,
      phrase: "discard changes",
      confirmLabel: untracked ? "Delete" : "Discard",
      description: (
        <p className="text-destructive">
          {untracked ? (
            <>
              <span className="font-mono break-all">{file.path}</span> has never been committed, so
              discarding it deletes it. It is not recoverable.
            </>
          ) : (
            <>
              Puts <span className="font-mono break-all">{file.path}</span> back the way it was at
              the last commit. The current edits are not recoverable.
            </>
          )}
        </p>
      ),
      action: async (c) => {
        await post("/git/discard", { file: file.path }, { confirm: c, query: q })
        onChanged()
      },
    })
  }

  const discardAll = (clean: boolean) =>
    confirm({
      title: clean ? "Discard everything" : "Discard every change",
      phrase: "reset hard",
      confirmLabel: "Discard all",
      description: (
        <p className="text-destructive">
          Every tracked file is put back to the last commit.{" "}
          {clean
            ? "Untracked files are deleted as well."
            : "Untracked files are left alone."}{" "}
          This cannot be undone.
        </p>
      ),
      action: async (c) => {
        await post("/git/reset", { ref: "HEAD", hard: true, clean }, { confirm: c, query: q })
        onChanged()
      },
    })

  if (status.error) return <ErrorState error={status.error} className="m-3" />
  if (status.loading && !status.data) return <LoadingRows className="p-3" rows={5} />

  const files = status.data?.files ?? []
  const staged = files.filter((f) => f.staged)
  const unstaged = files.filter((f) => f.unstaged)
  const conflicts = files.filter((f) => f.label === "conflicted").length
  const identity = status.data?.identity
  const hasIdentity = Boolean(identity?.name && identity?.email)
  const canCommit =
    staged.length > 0 && (message.trim().length > 0 || amend) && conflicts === 0 && hasIdentity

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="min-h-0 flex-1 overflow-auto">
        {stashes.length > 0 && (
          <Group label="Set aside" explain="stash" count={stashes.length}>
            {stashes.map((s) => (
              <StashRow
                key={s.sha}
                stash={s}
                active={active === `stash:${s.sha}`}
                onClick={() => onSelect({ kind: "stash", stash: s })}
                actions={
                  canControl && (
                    <RowAction
                      label="Pop this stash"
                      disabled={!!busy}
                      onClick={() =>
                        void run("Stash popped", () =>
                          post<GitResult>(
                            "/git/stash/apply",
                            { index: s.index, pop: true },
                            { query: q },
                          ),
                        ).catch(() => {})
                      }
                    >
                      <RotateCounterClockwise className="size-3.5" />
                    </RowAction>
                  )
                }
              />
            ))}
          </Group>
        )}

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
                    side="staged"
                    active={active === `staged:${f.path}`}
                    onClick={() => showDiff(f, "staged")}
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
                action={
                  <>
                    {canDestruct && (
                      <DropdownMenu>
                        <DropdownMenuTrigger asChild>
                          <button
                            type="button"
                            disabled={!!busy}
                            className="rounded-sm px-1.5 py-0.5 text-hint text-destructive transition-colors hover:bg-wash-danger disabled:opacity-40"
                          >
                            Discard all
                          </button>
                        </DropdownMenuTrigger>
                        <DropdownMenuContent align="end" className="w-72">
                          <DropdownMenuItem
                            className="items-start gap-2.5 py-1.5"
                            onSelect={() => discardAll(false)}
                          >
                            <span className="min-w-0 flex-1">
                              <span className="block text-body leading-tight font-medium">
                                Discard tracked changes
                              </span>
                              <span className="mt-0.5 block text-hint leading-snug text-muted-foreground">
                                Every edited file goes back to the last commit. New files stay.
                              </span>
                            </span>
                          </DropdownMenuItem>
                          <DropdownMenuItem
                            variant="destructive"
                            className="items-start gap-2.5 py-1.5"
                            onSelect={() => discardAll(true)}
                          >
                            <span className="min-w-0 flex-1">
                              <span className="block text-body leading-tight font-medium">
                                Discard everything
                              </span>
                              <span className="mt-0.5 block text-hint leading-snug text-muted-foreground">
                                Edited files go back, and new files are deleted too.
                              </span>
                            </span>
                          </DropdownMenuItem>
                        </DropdownMenuContent>
                      </DropdownMenu>
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
                    side="unstaged"
                    active={active === `unstaged:${f.path}`}
                    onClick={() => showDiff(f, "unstaged")}
                    actions={
                      <>
                        {canDestruct && f.label !== "conflicted" && (
                          <RowAction
                            label={f.label === "untracked" ? "Delete" : "Discard"}
                            className="text-destructive"
                            disabled={!!busy}
                            onClick={() => discard(f)}
                          >
                            {f.label === "untracked" ? (
                              <Trash className="size-3.5" />
                            ) : (
                              <RotateCounterClockwise className="size-3.5" />
                            )}
                          </RowAction>
                        )}
                        {canControl && f.label !== "conflicted" && (
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
          {conflicts > 0 && (
            <p className="flex items-center gap-1.5 text-hint text-destructive">
              <Warning className="size-3.5 shrink-0" />
              {conflicts} conflicted file{conflicts === 1 ? "" : "s"} must be resolved in a shell
              before anything can be committed.
            </p>
          )}
          <Textarea
            value={message}
            onChange={(e) => setMessage(e.target.value)}
            onKeyDown={(e) => {
              if ((e.metaKey || e.ctrlKey) && e.key === "Enter" && canCommit) {
                e.preventDefault()
                void commit(false)
              }
            }}
            placeholder={
              amend ? "New message — leave empty to keep the old one" : "Describe what you changed…"
            }
            rows={3}
            className="resize-none font-mono text-xs"
          />
          <div className="flex min-w-0 items-center gap-2">
            <label className="flex shrink-0 cursor-pointer items-center gap-1.5 text-hint text-muted-foreground">
              <Checkbox
                checked={amend}
                onCheckedChange={(v) => setAmend(Boolean(v))}
                aria-label="Amend the previous commit"
                className="size-3.5"
              />
              Amend
            </label>
            {/* Who the commit is recorded as. Missing is said plainly, with
                the fix beside it, because git's own refusal arrives after
                the message is written — the worst moment to learn it. */}
            {hasIdentity ? (
              <button
                type="button"
                onClick={() => setIdentityOpen(true)}
                className="min-w-0 truncate text-left text-hint text-muted-foreground hover:text-foreground hover:underline"
                title={`${identity?.name} <${identity?.email}> — change who commits are recorded as`}
              >
                as {identity?.name}
              </button>
            ) : (
              <button
                type="button"
                onClick={() => setIdentityOpen(true)}
                className="min-w-0 truncate text-left text-hint text-warning hover:underline"
              >
                git has no name and address here — set them
              </button>
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
                  pending={busy === "Committed"}
                  onClick={() => void commit(false)}
                >
                  <GitCommit className="size-3.5" />
                  Commit
                </Button>
              </TooltipTrigger>
              <TooltipContent>Commit the staged changes (Ctrl+Enter)</TooltipContent>
            </Tooltip>
          </div>
          {staged.length === 0 && files.length > 0 && (
            <p className="text-hint text-muted-foreground">
              Stage at least one change above before committing.
            </p>
          )}
        </div>
      )}
      <IdentityDialog
        open={identityOpen}
        onOpenChange={setIdentityOpen}
        repoPath={repoPath}
        identity={identity}
        onSaved={onChanged}
      />
    </div>
  )
}

/**
 * A titled run of rows. The title is an eyebrow with its count and its
 * definition one hover away; the tone that used to sit in a dot before it
 * said nothing the title did not.
 */
function Group({
  label,
  explain,
  count,
  action,
  children,
}: {
  label: string
  explain: string
  count: number
  action?: React.ReactNode
  children: React.ReactNode
}) {
  return (
    <div>
      <div className="sticky top-0 z-10 flex h-8 items-center gap-1.5 border-b border-hairline bg-card px-3">
        <span className="eyebrow">{label}</span>
        <span className="numeric text-hint text-muted-foreground">{count}</span>
        <GitExplain name={explain} />
        <span className="flex-1" />
        {action}
      </div>
      {children}
    </div>
  )
}

/**
 * One changed file: its status letter in the status's own colour, and its
 * path in the ordinary ink. The row used to carry a tinted band and a coloured
 * edge as well as the coloured letter and a coloured path — four ways of
 * saying "modified" on a list in which every row is, by definition, modified.
 */
function FileRow({
  file,
  side,
  active,
  onClick,
  actions,
}: {
  file: GitFileChange
  side: GitSide
  active?: boolean
  onClick: () => void
  actions?: React.ReactNode
}) {
  const tone = gitTone(file, side)
  return (
    <div
      className={cn(
        "group flex min-w-0 items-center gap-2 py-1 pr-1.5 pl-3 transition-colors hover:bg-row-hover",
        active && "bg-accent",
      )}
      style={gitStyle(tone)}
    >
      <Tooltip>
        <TooltipTrigger asChild>
          <span className="w-3 shrink-0 text-center font-mono text-micro font-medium text-(--git-colour)">
            {gitLetter(file, side)}
          </span>
        </TooltipTrigger>
        <TooltipContent>{describeChange(file, side)}</TooltipContent>
      </Tooltip>
      {/* No tooltip on the name: the row is a list of paths and clicking one
          to see its diff is the only thing it does. */}
      <button
        type="button"
        onClick={onClick}
        aria-pressed={active}
        className={cn(
          "min-w-0 flex-1 truncate text-left font-mono text-xs focus-ring-inset hover:underline",
          file.label === "deleted" && "text-muted-foreground line-through",
          file.label === "conflicted" && "text-(--git-colour)",
        )}
      >
        {file.path}
      </button>
      <RowActions className="gap-0">{actions}</RowActions>
    </div>
  )
}

function StashRow({
  stash,
  active,
  onClick,
  actions,
}: {
  stash: GitStash
  active?: boolean
  onClick: () => void
  actions?: React.ReactNode
}) {
  return (
    <div
      className={cn(
        "group flex min-w-0 items-center gap-2 py-1 pr-1.5 pl-3 transition-colors hover:bg-row-hover",
        active && "bg-accent",
      )}
    >
      <Archive className="size-3 shrink-0 text-muted-foreground" aria-hidden />
      <button
        type="button"
        onClick={onClick}
        aria-pressed={active}
        className="min-w-0 flex-1 truncate text-left text-xs focus-ring-inset hover:underline"
      >
        {stash.message}
        <span className="text-muted-foreground">
          {stash.branch ? ` · ${stash.branch}` : ""}
          {stash.at ? ` · ${relativeTime(stash.at)}` : ""}
        </span>
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
  hint,
  onClick,
  children,
}: {
  disabled?: boolean
  hint: string
  onClick: () => void
  children: React.ReactNode
}) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <button
          type="button"
          disabled={disabled}
          onClick={onClick}
          className="rounded-sm px-1.5 py-0.5 text-hint text-muted-foreground transition-colors hover:bg-accent hover:text-foreground disabled:opacity-40"
        >
          {children}
        </button>
      </TooltipTrigger>
      <TooltipContent>{hint}</TooltipContent>
    </Tooltip>
  )
}
