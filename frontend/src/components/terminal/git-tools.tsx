"use client"

import { useCallback, useEffect, useRef, useState } from "react"
import {
  Check,
  ChevronDoubleDown,
  ChevronDoubleUp,
  ClockRewind,
  CornerUpLeft,
  GitHubMark,
  GitBranch as GitBranchIcon,
  GitCommit as GitCommitIcon,
  Minus,
  MoreHorizontal,
  Plus,
  RefreshClockwise,
  RotateCounterClockwise,
} from "@/components/icons"
import { notify } from "@/lib/toast"
import { get, post } from "@/lib/api"
import { relativeTime, timestamp } from "@/lib/format"
import { cn } from "@/lib/utils"
import { describeChange, gitLetter, gitStyle, gitTone, type GitSide } from "@/lib/git-status"
import { useViewState } from "@/lib/view-state"
import type {
  GitBranch,
  GitCommit,
  GitDetect,
  GitFileChange,
  GitRepo,
  GitResult,
  GitStatus,
} from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { GitHubAccountControl } from "@/components/git/github-account"
import { AheadBehind } from "@/components/git/ahead-behind"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Input } from "@/components/ui/input"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { EmptyState, ErrorState, LoadingRows, Notice, Spinner } from "@/components/state"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import type { ConfirmRequest } from "@/components/files/file-tree"
import { IconAction, RowActions, rowReveal } from "@/components/icon-action"
import { ChipCount, FilterChip } from "@/components/tabs"
import { Tag } from "@/components/tag"

type DiffRequest = {
  title: string
  subtitle?: string
  body: string
  /** The diff is of one file, whose name is already the title — so the
   *  renderer can drop git's header instead of repeating it. */
  singleFile?: boolean
}

type View = "changes" | "history" | "branches"
type Run = (label: string, fn: () => Promise<GitResult>) => Promise<GitResult>

/**
 * The git workflow for whatever repository the terminal's shell is sitting in.
 *
 * It is a full working-tree UI — stage, unstage, commit, push, pull, fetch,
 * branch, diff, discard — rather than the read-mostly view the dedicated Git
 * page carries, because a shell is where you are *doing* the work and reaching
 * for another tab to stage a hunk defeats the point. Everything renders inline
 * (no portalled sheet) so it survives the terminal being in real fullscreen;
 * diffs are handed up to the panel to show over the tree.
 *
 * Its chrome is two strips. The first is the repository's reading — which
 * branch, how far from its upstream — with the two verbs pressed all day
 * inline and the rest behind one menu, where each gets its word and a line
 * under it. The second switches between the three lists. It used to be four
 * strips before any content: the branch line wrapped onto a second row of
 * icon-only buttons, and the branches view opened with a permanent create
 * form nobody was filling in.
 */
export function GitTools({
  dir,
  detect,
  detectLoading,
  detectError,
  status,
  canControl,
  canDestruct,
  onShowDiff,
  onConfirm,
  onChanged,
}: {
  dir: string
  detect: GitDetect | undefined
  detectLoading: boolean
  detectError?: Error
  status: ReturnType<typeof usePoll<GitStatus>>
  canControl: boolean
  canDestruct: boolean
  onShowDiff: (req: DiffRequest) => void
  onConfirm: (req: ConfirmRequest) => void
  onChanged: () => void
}) {
  const [view, setView] = useViewState<View>("terminal.git.tab", "changes")
  const [busy, setBusy] = useState<string>()
  const [creatingBranch, setCreatingBranch] = useState(false)

  const repo = detect?.inRoots ? detect.repo : undefined
  const repoPath = repo?.path

  const run = useCallback<Run>(
    async (label, fn) => {
      setBusy(label)
      try {
        const res = await fn()
        notify.success(label, {
          description: res.output?.split("\n").slice(0, 3).join("\n"),
        })
        status.refresh()
        onChanged()
        return res
      } catch (err) {
        notify.error(`${label} failed`, err)
        throw err
      } finally {
        setBusy(undefined)
      }
    },
    [status, onChanged],
  )

  if (detectError) return <ErrorState error={detectError} className="m-3" />

  if (detectLoading && !detect) {
    return (
      <div className="flex items-center gap-2 p-4 text-xs text-muted-foreground">
        <Spinner className="size-3.5" /> Looking for a repository…
      </div>
    )
  }

  if (detect && !detect.available) {
    return (
      <EmptyState
        className="m-3"
        icon={GitHubMark}
        title="git is not installed"
        description="Install git on this host to work with repositories from the terminal."
      />
    )
  }

  if (detect && detect.root && !detect.inRoots) {
    return (
      <Notice
        className="m-3"
        tone="warning"
        title="Outside the configured git roots"
        icon={GitHubMark}
      >
        This is a checkout at <span className="font-mono break-all">{detect.root}</span>, but it
        falls outside <code className="font-mono">JD_GIT_ROOTS</code>, so the dashboard will not act
        on it. Add its parent to that setting to enable git here.
      </Notice>
    )
  }

  if (!repo) {
    return (
      <EmptyState
        className="m-3"
        icon={GitHubMark}
        title="Not a git repository"
        description={
          <>
            <span className="font-mono break-all">{dir || "This shell"}</span> is not inside a
            checkout. Run <code className="font-mono">git init</code> here, or cd into a project.
          </>
        }
      />
    )
  }

  const changed = status.data?.files.length ?? 0

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <RepoStrip repo={repo} busy={busy} canControl={canControl} run={run} status={status} />

      <div className="flex h-9 shrink-0 items-center gap-0.5 border-b border-hairline px-1.5">
        <FilterChip selected={view === "changes"} onClick={() => setView("changes")}>
          Changes
          {changed > 0 && <ChipCount>{changed}</ChipCount>}
        </FilterChip>
        <FilterChip selected={view === "history"} onClick={() => setView("history")}>
          History
        </FilterChip>
        <FilterChip selected={view === "branches"} onClick={() => setView("branches")}>
          Branches
        </FilterChip>
        <span className="flex-1" />
        {view === "branches" && canControl && (
          <IconAction
            label="New branch from HEAD"
            className="size-7"
            disabled={!!busy || creatingBranch}
            onClick={() => setCreatingBranch(true)}
          >
            <Plus />
          </IconAction>
        )}
      </div>

      {view === "changes" && (
        <ChangesView
          repoPath={repoPath!}
          status={status}
          busy={busy}
          canControl={canControl}
          canDestruct={canDestruct}
          onShowDiff={onShowDiff}
          onConfirm={onConfirm}
          run={run}
        />
      )}
      {view === "history" && <HistoryView repoPath={repoPath!} onShowDiff={onShowDiff} />}
      {view === "branches" && (
        <BranchesView
          repoPath={repoPath!}
          busy={busy}
          canControl={canControl}
          creating={creatingBranch}
          onDoneCreating={() => setCreatingBranch(false)}
          run={run}
        />
      )}
    </div>
  )
}

/**
 * The repository's reading, and its verbs.
 *
 * Pull and push are inline: they are the two pressed every hour and their
 * glyphs are the ones every git client shares. Fetch, stash and pop go behind
 * the menu — not hidden, but given a sentence, which is the only form a verb
 * like "stash" is usable in by somebody who has not already learnt it.
 */
function RepoStrip({
  repo,
  busy,
  canControl,
  run,
  status,
}: {
  repo: GitRepo
  busy?: string
  canControl: boolean
  run: Run
  status: ReturnType<typeof usePoll<GitStatus>>
}) {
  const q = { path: repo.path }
  const stashes = status.data?.stashes ?? 0
  const clean = Boolean(status.data?.clean)
  // The strip is one row whatever the column's width, and the branch is the
  // reading that must survive on it. Below 400px the account's login goes
  // (the avatar stays, and the login is one press away in its menu) rather
  // than the branch being squeezed to nothing; dragged to the column's
  // narrowest, or on a phone, pull and push join the menu as well, instead
  // of being pushed off the edge.
  const stripRef = useRef<HTMLDivElement>(null)
  const [width, setWidth] = useState(Infinity)
  useEffect(() => {
    const el = stripRef.current
    if (!el) return
    const observer = new ResizeObserver(() => setWidth(el.clientWidth))
    observer.observe(el)
    return () => observer.disconnect()
  }, [])
  const narrow = width < 320
  const avatarOnly = width < 400

  const pull = () =>
    void run("Pulled", () => post<GitResult>("/git/pull", undefined, { query: q })).catch(
      () => undefined,
    )
  const push = () =>
    void run("Pushed", () => post<GitResult>("/git/push", undefined, { query: q })).catch(
      () => undefined,
    )

  const menuBusy =
    busy === "Fetched" ||
    busy === "Stashed" ||
    busy === "Stash popped" ||
    (narrow && (busy === "Pulled" || busy === "Pushed"))

  const more: {
    key: string
    label: string
    detail: string
    icon: React.ComponentType<{ className?: string }>
    disabled?: boolean
    run: () => void
  }[] = [
    {
      key: "fetch",
      label: "Fetch",
      detail: "Update what is known about every remote, and forget branches that are gone.",
      icon: RefreshClockwise,
      run: () =>
        void run("Fetched", () =>
          post<GitResult>("/git/fetch", undefined, { query: { ...q, prune: true } }),
        ).catch(() => undefined),
    },
  ]
  if (canControl && narrow) {
    more.unshift(
      {
        key: "pull",
        label: "Pull",
        detail: "Fast-forward the branch to its upstream.",
        icon: ChevronDoubleDown,
        run: pull,
      },
      {
        key: "push",
        label: "Push",
        detail: "Send the branch's commits to its upstream.",
        icon: ChevronDoubleUp,
        run: push,
      },
    )
  }
  if (canControl) {
    more.push({
      key: "stash",
      label: "Stash changes",
      detail: clean
        ? "Nothing to set aside — the working tree is clean."
        : "Set the working tree changes aside and get a clean checkout back.",
      icon: CornerUpLeft,
      disabled: clean,
      run: () =>
        void run("Stashed", () => post<GitResult>("/git/stash", {}, { query: q })).catch(
          () => undefined,
        ),
    })
    if (stashes > 0) {
      more.push({
        key: "pop",
        label: `Pop the latest stash`,
        detail: `${stashes} stashed — bring the most recent one back into the working tree.`,
        icon: RotateCounterClockwise,
        run: () =>
          void run("Stash popped", () =>
            post<GitResult>("/git/stash/pop", undefined, { query: q }),
          ).catch(() => undefined),
      })
    }
  }

  return (
    <div
      ref={stripRef}
      className="flex h-9 shrink-0 items-center gap-1.5 border-b border-hairline pr-1.5 pl-3"
    >
      <Tooltip>
        <TooltipTrigger asChild>
          <span className="min-w-12 flex-1 truncate font-mono text-xs font-medium">
            {repo.branch}
          </span>
        </TooltipTrigger>
        <TooltipContent>
          {repo.detached ? `Detached at ${repo.branch}` : `On branch ${repo.branch}`}
        </TooltipContent>
      </Tooltip>
      {repo.detached && <Tag tone="danger">detached</Tag>}
      <AheadBehind ahead={repo.ahead} behind={repo.behind} />
      {/* Whose push this would be. Compact — the avatar and the login — because
          the rest of this strip is one reading and three buttons. */}
      <GitHubAccountControl repoPath={repo.path} compact={avatarOnly ? "avatar" : true} />
      {canControl && !narrow && (
        <>
          <GitButton
            label="Pull (fast-forward only)"
            busy={busy === "Pulled"}
            disabled={!!busy}
            onClick={pull}
          >
            <ChevronDoubleDown className="size-3.5" />
          </GitButton>
          <GitButton
            label="Push the current branch"
            busy={busy === "Pushed"}
            disabled={!!busy}
            onClick={push}
          >
            <ChevronDoubleUp className="size-3.5" />
          </GitButton>
        </>
      )}
      <DropdownMenu>
        <Tooltip>
          <TooltipTrigger asChild>
            <DropdownMenuTrigger asChild>
              <Button
                type="button"
                size="sm"
                variant="ghost"
                aria-label="More git actions"
                disabled={!!busy}
                className="size-7 shrink-0 p-0 text-muted-foreground hover:text-foreground"
              >
                {menuBusy ? (
                  <Spinner className="size-3.5" />
                ) : (
                  <MoreHorizontal className="size-3.5" />
                )}
              </Button>
            </DropdownMenuTrigger>
          </TooltipTrigger>
          <TooltipContent>{narrow ? "Pull, push, fetch, stash" : "Fetch, stash"}</TooltipContent>
        </Tooltip>
        <DropdownMenuContent align="end" className="w-68">
          {more.map((verb) => (
            <DropdownMenuItem
              key={verb.key}
              disabled={verb.disabled}
              className="items-start gap-2.5 py-1.5"
              onSelect={verb.run}
            >
              <verb.icon className="mt-0.5 size-3.5 shrink-0" />
              <span className="min-w-0 flex-1">
                <span className="block text-body leading-tight font-medium">{verb.label}</span>
                <span className="mt-0.5 block text-hint leading-snug text-muted-foreground">
                  {verb.detail}
                </span>
              </span>
            </DropdownMenuItem>
          ))}
        </DropdownMenuContent>
      </DropdownMenu>
    </div>
  )
}

function ChangesView({
  repoPath,
  status,
  busy,
  canControl,
  canDestruct,
  onShowDiff,
  onConfirm,
  run,
}: {
  repoPath: string
  status: ReturnType<typeof usePoll<GitStatus>>
  busy?: string
  canControl: boolean
  canDestruct: boolean
  onShowDiff: (req: DiffRequest) => void
  onConfirm: (req: ConfirmRequest) => void
  run: Run
}) {
  const [message, setMessage] = useState("")
  const [amend, setAmend] = useState(false)
  const q = { path: repoPath }

  const showDiff = async (file: string, staged: boolean) => {
    try {
      const res = await get<{ diff: string }>("/git/diff", {
        path: repoPath,
        file,
        staged: staged ? "true" : undefined,
      })
      onShowDiff({
        title: file,
        subtitle: staged ? "staged changes" : "working tree changes",
        body: res.diff || "No textual diff (binary file, or no changes).",
        singleFile: true,
      })
    } catch (err) {
      notify.error("Could not read the diff", err)
    }
  }

  const stage = (files: string[]) =>
    run("Staged", () => post<GitResult>("/git/stage", { files }, { query: q })).catch(
      () => undefined,
    )
  const unstage = (files: string[]) =>
    run("Unstaged", () => post<GitResult>("/git/unstage", { files }, { query: q })).catch(
      () => undefined,
    )

  const commit = async (thenPush: boolean) => {
    const msg = message.trim()
    if (!msg && !amend) {
      notify.error("A commit message is required")
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
      /* run already reported it */
    }
  }

  const discard = (file: GitFileChange) =>
    onConfirm({
      title:
        file.label === "untracked"
          ? `Delete ${file.path.split("/").pop()}`
          : `Discard changes to ${file.path.split("/").pop()}`,
      danger: true,
      confirmLabel: file.label === "untracked" ? "Delete" : "Discard",
      body:
        file.label === "untracked" ? (
          <>
            <span className="font-mono break-all">{file.path}</span> has never been committed, so
            discarding it deletes it. It is not recoverable.
          </>
        ) : (
          <>
            <span className="font-mono break-all">{file.path}</span> is restored to its committed
            state. The current contents are not recoverable.
          </>
        ),
      run: async () => {
        await post("/git/discard", { file: file.path }, { confirm: "discard changes", query: q })
        notify.success("Discarded", { description: file.path })
        status.refresh()
      },
    })

  if (status.error) return <ErrorState error={status.error} className="m-3" />
  if (status.loading && !status.data) return <LoadingRows className="p-3" rows={4} />

  const files = status.data?.files ?? []
  // A file staged and then edited again is on both sides, and is listed
  // under both headings with the letter for each — see lib/git-status.
  const staged = files.filter((f) => f.staged)
  const unstaged = files.filter((f) => f.unstaged)
  const canCommit = !busy && (Boolean(message.trim()) || amend) && staged.length > 0

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="min-h-0 flex-1 overflow-auto py-1">
        {files.length === 0 ? (
          <div className="p-3">
            <EmptyState icon={Check} title="Working tree clean" description="Nothing to commit." />
          </div>
        ) : (
          <>
            {staged.length > 0 && (
              <FileGroup
                label="Staged"
                count={staged.length}
                action={
                  canControl && (
                    <GroupButton
                      disabled={!!busy}
                      hint="Take every staged file back out of the next commit"
                      onClick={() => unstage([])}
                    >
                      Unstage all
                    </GroupButton>
                  )
                }
              >
                {staged.map((f) => (
                  <FileRow
                    key={"s" + f.path}
                    file={f}
                    side="staged"
                    onDiff={() => showDiff(f.path, true)}
                    action={
                      canControl && (
                        <RowButton
                          label="Unstage"
                          disabled={!!busy}
                          onClick={() => unstage([f.path])}
                        >
                          <Minus className="size-3.5" />
                        </RowButton>
                      )
                    }
                  />
                ))}
              </FileGroup>
            )}

            {unstaged.length > 0 && (
              <FileGroup
                label="Changes"
                count={unstaged.length}
                action={
                  <>
                    {canDestruct && (
                      <GroupButton
                        disabled={!!busy}
                        danger
                        hint="Restore every tracked file to HEAD — untracked files are left alone"
                        onClick={() =>
                          onConfirm({
                            title: "Discard all working-tree changes",
                            danger: true,
                            confirmLabel: "Discard all",
                            body: "Every tracked change is restored to HEAD. Untracked files are left alone. This cannot be undone.",
                            run: async () => {
                              await post(
                                "/git/reset",
                                { ref: "HEAD", hard: true },
                                { confirm: "reset hard", query: q },
                              )
                              notify.success("Discarded all changes")
                              status.refresh()
                            },
                          })
                        }
                      >
                        Discard all
                      </GroupButton>
                    )}
                    {canControl && (
                      <GroupButton
                        disabled={!!busy}
                        hint="Stage every change in the working tree"
                        onClick={() => stage([])}
                      >
                        Stage all
                      </GroupButton>
                    )}
                  </>
                }
              >
                {unstaged.map((f) => (
                  <FileRow
                    key={"u" + f.path}
                    file={f}
                    side="unstaged"
                    onDiff={() => showDiff(f.path, false)}
                    action={
                      <>
                        {canDestruct && f.label !== "conflicted" && (
                          <RowButton
                            label={f.label === "untracked" ? "Delete" : "Discard"}
                            className="text-destructive"
                            disabled={!!busy}
                            onClick={() => discard(f)}
                          >
                            <RotateCounterClockwise className="size-3.5" />
                          </RowButton>
                        )}
                        {canControl && (
                          <RowButton
                            label="Stage"
                            disabled={!!busy}
                            onClick={() => stage([f.path])}
                          >
                            <Plus className="size-3.5" />
                          </RowButton>
                        )}
                      </>
                    }
                  />
                ))}
              </FileGroup>
            )}
          </>
        )}
      </div>

      {canControl && (
        <div className="shrink-0 space-y-2 border-t border-hairline p-2.5">
          <textarea
            value={message}
            onChange={(e) => setMessage(e.target.value)}
            onKeyDown={(e) => {
              // Ctrl/Cmd+Enter commits, the convention every git client shares.
              if ((e.metaKey || e.ctrlKey) && e.key === "Enter") {
                e.preventDefault()
                void commit(false)
              }
            }}
            placeholder={amend ? "Amend message (blank keeps the previous one)" : "Commit message"}
            rows={2}
            className="w-full resize-none rounded-md border border-input bg-transparent px-2 py-1.5 font-mono text-xs focus-ring"
          />
          {/* The buttons wrap under the checkbox once the column is too
              narrow for the row: at its narrowest width the two of them,
              side by side with "Amend", ran past the panel's edge. */}
          <div className="flex flex-wrap items-center gap-x-2 gap-y-1.5">
            <label className="flex cursor-pointer items-center gap-1.5 text-hint text-muted-foreground">
              <Checkbox
                checked={amend}
                onCheckedChange={(v) => setAmend(Boolean(v))}
                aria-label="Amend the previous commit"
                className="size-3.5"
              />
              Amend
            </label>
            <div className="ml-auto flex flex-wrap justify-end gap-2">
              <Tooltip>
                <TooltipTrigger asChild>
                  <Button
                    size="xs"
                    variant="outline"
                    disabled={!canCommit}
                    onClick={() => void commit(true)}
                  >
                    <ChevronDoubleUp className="size-3.5" />
                    Commit &amp; push
                  </Button>
                </TooltipTrigger>
                <TooltipContent>Commit the staged changes, then push the branch</TooltipContent>
              </Tooltip>
              <Tooltip>
                <TooltipTrigger asChild>
                  <Button size="xs" disabled={!canCommit} onClick={() => void commit(false)}>
                    <GitCommitIcon className="size-3.5" />
                    Commit
                  </Button>
                </TooltipTrigger>
                <TooltipContent>Commit the staged changes (Ctrl+Enter)</TooltipContent>
              </Tooltip>
            </div>
          </div>
          {staged.length === 0 && (message.trim() || amend) && (
            <p className="text-hint text-muted-foreground">Stage something first.</p>
          )}
        </div>
      )}
    </div>
  )
}

function HistoryView({
  repoPath,
  onShowDiff,
}: {
  repoPath: string
  onShowDiff: (req: DiffRequest) => void
}) {
  const log = usePoll(
    (signal) => get<GitCommit[]>("/git/log", { path: repoPath, limit: 100 }, signal),
    0,
    [repoPath],
  )

  const show = async (c: GitCommit) => {
    onShowDiff({ title: c.subject, subtitle: `${c.short} · loading…`, body: "Loading…" })
    try {
      const res = await get<{ diff: string }>("/git/diff", { path: repoPath, ref: c.sha })
      onShowDiff({
        title: c.subject,
        subtitle: `${c.short} · ${c.author} · ${timestamp(c.at)}`,
        body: res.diff,
      })
    } catch (err) {
      onShowDiff({ title: c.subject, subtitle: c.short, body: String(err) })
    }
  }

  if (log.error) return <ErrorState error={log.error} className="m-3" />
  if (log.loading && !log.data) return <LoadingRows className="p-3" rows={5} />
  if (!log.data?.length)
    return <EmptyState className="m-3" icon={ClockRewind} title="No commits yet" />

  // A list of commits, each a click away from its diff. No glyph on the row —
  // every row is a commit — and no tooltip repeating the line beneath it.
  return (
    <div className="min-h-0 flex-1 overflow-auto py-1">
      {log.data.map((c) => (
        <button
          key={c.sha}
          type="button"
          onClick={() => show(c)}
          className="flex w-full min-w-0 items-start gap-3 px-3 py-1.5 text-left focus-ring-inset transition-colors hover:bg-row-hover"
        >
          <span className="min-w-0 flex-1">
            <span className="block truncate text-xs">{c.subject}</span>
            <span className="block truncate text-micro text-muted-foreground">
              <span className="font-mono">{c.short}</span> · {c.author} · {relativeTime(c.at)}
              {c.isMerge ? " · merge" : ""}
            </span>
          </span>
          {(c.insertions > 0 || c.deletions > 0) && (
            <span className="numeric mt-px shrink-0 font-mono text-micro">
              <span className="text-(--git-added)">+{c.insertions}</span>{" "}
              <span className="text-(--git-deleted)">−{c.deletions}</span>
            </span>
          )}
        </button>
      ))}
    </div>
  )
}

/**
 * Local branches, then remotes under their own label.
 *
 * "remote" was a word on every other row; a section says it once. A remote
 * branch is shown for reference and not offered a Switch, because checking one
 * out directly lands in a detached HEAD, which is the state a newcomer cannot
 * get out of.
 */
function BranchesView({
  repoPath,
  busy,
  canControl,
  creating,
  onDoneCreating,
  run,
}: {
  repoPath: string
  busy?: string
  canControl: boolean
  /** The view strip's + was pressed: show the create row until it settles. */
  creating: boolean
  onDoneCreating: () => void
  run: Run
}) {
  const branches = usePoll(
    (signal) => get<GitBranch[]>("/git/branches", { path: repoPath }, signal),
    0,
    [repoPath],
  )
  const q = { path: repoPath }

  const create = (name: string) => {
    if (!name) {
      onDoneCreating()
      return
    }
    void run(`Created ${name}`, () => post<GitResult>("/git/branch", { ref: name }, { query: q }))
      .then(() => {
        onDoneCreating()
        branches.refresh()
      })
      .catch(() => undefined)
  }

  const switchTo = (name: string) =>
    void run(`Switched to ${name}`, () =>
      post<GitResult>("/git/checkout", { ref: name }, { query: q }),
    )
      .then(() => branches.refresh())
      .catch(() => undefined)

  if (branches.error) return <ErrorState error={branches.error} className="m-3" />
  if (branches.loading && !branches.data) return <LoadingRows className="p-3" rows={5} />

  const local = branches.data?.filter((b) => !b.remote) ?? []
  const remote = branches.data?.filter((b) => b.remote) ?? []

  return (
    <div className="min-h-0 flex-1 overflow-auto py-1">
      {creating && <BranchNameRow busy={!!busy} onCommit={create} onCancel={onDoneCreating} />}
      {local.map((b) => (
        <div
          key={b.name}
          className="group flex min-w-0 items-center gap-2 px-3 py-1.5 transition-colors hover:bg-row-hover"
        >
          <div className="min-w-0 flex-1">
            <p
              className={cn(
                "flex min-w-0 items-center gap-1.5 font-mono text-xs",
                b.current ? "font-medium" : "text-foreground/90",
              )}
            >
              <span className="truncate">{b.name}</span>
              {b.current && <Tag tone="success">current</Tag>}
            </p>
            {b.worktree ? (
              <p className="truncate text-micro text-muted-foreground" title={b.worktree}>
                checked out in {b.worktree}
              </p>
            ) : (
              b.subject && <p className="truncate text-micro text-muted-foreground">{b.subject}</p>
            )}
          </div>
          <AheadBehind ahead={b.ahead} behind={b.behind} />
          {canControl && !b.current && !b.worktree && (
            <Tooltip>
              <TooltipTrigger asChild>
                <Button
                  size="xs"
                  variant="ghost"
                  disabled={!!busy}
                  className={cn("shrink-0", rowReveal())}
                  onClick={() => switchTo(b.name)}
                >
                  Switch
                </Button>
              </TooltipTrigger>
              <TooltipContent>{`Check out ${b.name}`}</TooltipContent>
            </Tooltip>
          )}
        </div>
      ))}

      {remote.length > 0 && (
        <>
          <p className="px-3 pt-3 pb-1 text-micro font-medium tracking-wide text-muted-foreground uppercase">
            Remotes
          </p>
          {remote.map((b) => (
            <div key={b.name} className="min-w-0 px-3 py-1.5 text-muted-foreground">
              <p className="truncate font-mono text-xs">{b.name}</p>
              {b.subject && <p className="truncate text-micro">{b.subject}</p>}
            </div>
          ))}
        </>
      )}

      {local.length === 0 && remote.length === 0 && !creating && (
        <EmptyState className="m-2" icon={GitBranchIcon} title="No branches" />
      )}
    </div>
  )
}

/** The create row the + in the view strip opens: Enter creates, Esc cancels. */
function BranchNameRow({
  busy,
  onCommit,
  onCancel,
}: {
  busy: boolean
  onCommit: (name: string) => void
  onCancel: () => void
}) {
  const [name, setName] = useState("")
  const ref = useRef<HTMLInputElement>(null)
  useEffect(() => ref.current?.focus(), [])
  return (
    <form
      className="flex items-center gap-1.5 px-2 py-1"
      onSubmit={(e) => {
        e.preventDefault()
        onCommit(name.trim())
      }}
    >
      <Input
        ref={ref}
        value={name}
        disabled={busy}
        spellCheck={false}
        onChange={(e) => setName(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Escape") onCancel()
        }}
        onBlur={() => !name.trim() && onCancel()}
        placeholder="New branch from HEAD"
        className="h-7 font-mono text-xs"
      />
      <Button type="submit" size="xs" variant="outline" disabled={busy || !name.trim()}>
        Create
      </Button>
    </form>
  )
}

// --- small shared pieces ---

function FileGroup({
  label,
  count,
  action,
  children,
}: {
  label: string
  count: number
  action?: React.ReactNode
  children: React.ReactNode
}) {
  return (
    <div>
      <div className="sticky top-0 z-10 flex h-7 items-center gap-1.5 bg-card px-3">
        <span className="text-hint font-medium">{label}</span>
        <span className="numeric text-micro text-muted-foreground">{count}</span>
        <span className="flex-1" />
        {action}
      </div>
      {children}
    </div>
  )
}

/**
 * One changed file: its status letter in the status's own colour, and its
 * path in the ordinary ink.
 *
 * The row used to carry a tinted band and a coloured edge as well as the
 * coloured letter and a coloured path — four ways of saying "modified" on a
 * list in which every row is, by definition, modified. The tree keeps its
 * band, because there one green line among forty plain ones is the point.
 */
function FileRow({
  file,
  side,
  onDiff,
  action,
}: {
  file: GitFileChange
  side: GitSide
  onDiff: () => void
  action?: React.ReactNode
}) {
  const tone = gitTone(file, side)
  return (
    <div
      className="group flex min-w-0 items-center gap-2 py-1 pr-1.5 pl-3 transition-colors hover:bg-row-hover"
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
      {/* No tooltip on the name. The row is a list of paths and clicking one
          to see its diff is the only thing it does — a hint that repeats the
          path back and explains the obvious is in the way of reading the
          list, which is what this panel is for. The status letter beside it
          keeps its tooltip, because a letter is not self-explanatory. */}
      <button
        type="button"
        onClick={onDiff}
        className={cn(
          "min-w-0 flex-1 truncate text-left font-mono text-xs focus-ring-inset hover:underline",
          file.label === "deleted" && "text-muted-foreground line-through",
        )}
      >
        {file.path}
      </button>
      <RowActions className="gap-0">{action}</RowActions>
    </div>
  )
}

function GitButton({
  label,
  busy,
  disabled,
  onClick,
  children,
}: {
  label: string
  busy?: boolean
  disabled?: boolean
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
          className="size-7 shrink-0 p-0 text-muted-foreground hover:text-foreground"
          onClick={onClick}
        >
          {busy ? <Spinner className="size-3.5" /> : children}
        </Button>
      </TooltipTrigger>
      <TooltipContent>{label}</TooltipContent>
    </Tooltip>
  )
}

function RowButton({
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
 * A group's own action — "Stage all", "Discard all".
 *
 * It carries a tooltip despite having a visible label, because the label is
 * the verb and the tooltip is the scope: "all" means every file in *this*
 * group, which is not the same as every file in the list, and the difference
 * matters most for the one that cannot be undone.
 */
function GroupButton({
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
          type="button"
          disabled={disabled}
          onClick={onClick}
          className={cn(
            "rounded-sm px-1.5 py-0.5 text-hint focus-ring transition-colors disabled:opacity-40",
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
