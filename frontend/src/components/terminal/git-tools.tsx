"use client"

import { useCallback, useState } from "react"
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
  Plus,
  RefreshClockwise,
  RotateCounterClockwise,
} from "@/components/icons"
import { notify } from "@/lib/toast"
import { get, post } from "@/lib/api"
import { relativeTime, timestamp } from "@/lib/format"
import { cn } from "@/lib/utils"
import { describeChange, gitLetter, gitStyle, gitTone } from "@/lib/git-status"
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
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Input } from "@/components/ui/input"
import { EmptyState, ErrorState, LoadingRows, Notice, Spinner } from "@/components/state"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import type { ConfirmRequest } from "@/components/files/file-tree"
import { RowActions, rowReveal } from "@/components/icon-action"

type DiffRequest = {
  title: string
  subtitle?: string
  body: string
  /** The diff is of one file, whose name is already the title — so the
   *  renderer can drop git's header instead of repeating it. */
  singleFile?: boolean
}

/**
 * The git workflow for whatever repository the terminal's shell is sitting in.
 *
 * It is a full working-tree UI — stage, unstage, commit, push, pull, fetch,
 * branch, diff, discard — rather than the read-mostly view the dedicated Git
 * page carries, because a shell is where you are *doing* the work and reaching
 * for another tab to stage a hunk defeats the point. Everything renders inline
 * (no portalled sheet) so it survives the terminal being in real fullscreen;
 * diffs are handed up to the panel to show over the tree.
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
  const [tab, setTab] = useViewState<"changes" | "history" | "branches">(
    "terminal.git.tab",
    "changes",
  )
  const [busy, setBusy] = useState<string>()

  const repo = detect?.inRoots ? detect.repo : undefined
  const repoPath = repo?.path

  const run = useCallback(
    async (label: string, fn: () => Promise<GitResult>) => {
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

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <RepoHeader repo={repo} busy={busy} canControl={canControl} run={run} status={status} />

      <div className="flex shrink-0 gap-0.5 border-b border-hairline bg-surface-header/60 px-2 py-1">
        {(["changes", "history", "branches"] as const).map((t) => (
          <button
            key={t}
            onClick={() => setTab(t)}
            className={cn(
              "rounded-sm px-2 py-1 text-xs capitalize transition-colors",
              tab === t
                ? "bg-plot-primary text-primary"
                : "text-muted-foreground hover:text-foreground",
            )}
          >
            {t}
          </button>
        ))}
      </div>

      {tab === "changes" && (
        <ChangesTab
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
      {tab === "history" && <HistoryTab repoPath={repoPath!} onShowDiff={onShowDiff} />}
      {tab === "branches" && (
        <BranchesTab repoPath={repoPath!} busy={busy} canControl={canControl} run={run} />
      )}
    </div>
  )
}

function RepoHeader({
  repo,
  busy,
  canControl,
  run,
  status,
}: {
  repo: GitRepo
  busy?: string
  canControl: boolean
  run: (label: string, fn: () => Promise<GitResult>) => Promise<GitResult>
  status: ReturnType<typeof usePoll<GitStatus>>
}) {
  const q = { path: repo.path }
  const stashes = status.data?.stashes ?? 0
  return (
    <div className="flex flex-wrap items-center gap-1.5 border-b border-hairline bg-surface-header px-2 py-1.5">
      <GitBranchIcon
        className={cn("size-3.5 shrink-0", repo.detached ? "text-destructive" : "text-success")}
      />
      <Tooltip>
        <TooltipTrigger asChild>
          <span className="max-w-[9rem] min-w-0 truncate font-mono text-xs font-medium">
            {repo.branch}
          </span>
        </TooltipTrigger>
        <TooltipContent>
          {repo.detached ? `Detached at ${repo.branch}` : `On branch ${repo.branch}`}
        </TooltipContent>
      </Tooltip>
      <AheadBehind ahead={repo.ahead} behind={repo.behind} />
      <span className="flex-1" />
      {/* Whose push this would be. Compact, because the rest of this strip is
          single-icon buttons and a full chip would push them onto a second
          line in a panel that is already narrow. */}
      <GitHubAccountControl repoPath={repo.path} compact />
      <GitButton
        label="Fetch from all remotes"
        busy={busy === "Fetched"}
        disabled={!!busy}
        onClick={() =>
          run("Fetched", () =>
            post<GitResult>("/git/fetch", undefined, { query: { ...q, prune: true } }),
          )
        }
      >
        <RefreshClockwise className="size-3.5" />
      </GitButton>
      {canControl && (
        <>
          <GitButton
            label="Pull (fast-forward only)"
            busy={busy === "Pulled"}
            disabled={!!busy}
            onClick={() =>
              run("Pulled", () => post<GitResult>("/git/pull", undefined, { query: q }))
            }
          >
            <ChevronDoubleDown className="size-3.5" />
          </GitButton>
          <GitButton
            label="Push the current branch"
            busy={busy === "Pushed"}
            disabled={!!busy}
            onClick={() =>
              run("Pushed", () => post<GitResult>("/git/push", undefined, { query: q }))
            }
          >
            <ChevronDoubleUp className="size-3.5" />
          </GitButton>
          <GitButton
            label="Stash working tree changes"
            busy={busy === "Stashed"}
            disabled={!!busy || status.data?.clean}
            onClick={() => run("Stashed", () => post<GitResult>("/git/stash", {}, { query: q }))}
          >
            <CornerUpLeft className="size-3.5" />
          </GitButton>
          {stashes > 0 && (
            <GitButton
              label={`Pop the latest stash (${stashes})`}
              busy={busy === "Stash popped"}
              disabled={!!busy}
              onClick={() =>
                run("Stash popped", () =>
                  post<GitResult>("/git/stash/pop", undefined, { query: q }),
                )
              }
            >
              <RotateCounterClockwise className="size-3.5" />
            </GitButton>
          )}
        </>
      )}
    </div>
  )
}

function ChangesTab({
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
  run: (label: string, fn: () => Promise<GitResult>) => Promise<GitResult>
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
    run("Staged", () => post<GitResult>("/git/stage", { files }, { query: q }))
  const unstage = (files: string[]) =>
    run("Unstaged", () => post<GitResult>("/git/unstage", { files }, { query: q }))

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
      title: `Discard changes to ${file.path.split("/").pop()}`,
      danger: true,
      confirmLabel: "Discard",
      body: (
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
  const staged = files.filter((f) => f.staged)
  const unstaged = files.filter((f) => !f.staged)

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="min-h-0 flex-1 overflow-auto">
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
                tone="success"
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
                tone="warning"
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
                    onDiff={() => showDiff(f.path, false)}
                    action={
                      <>
                        {canDestruct && f.label !== "untracked" && (
                          <RowButton
                            label="Discard"
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
        <div className="shrink-0 space-y-2 border-t border-hairline bg-surface-header/60 p-2">
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
          <div className="flex items-center gap-2">
            <label className="flex cursor-pointer items-center gap-1.5 text-hint text-muted-foreground">
              <Checkbox
                checked={amend}
                onCheckedChange={(v) => setAmend(Boolean(v))}
                className="size-3.5"
              />
              Amend
            </label>
            <span className="flex-1" />
            <Tooltip>
              <TooltipTrigger asChild>
                <Button
                  size="xs"
                  variant="outline"
                  disabled={!!busy || (!message.trim() && !amend) || staged.length === 0}
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
                <Button
                  size="xs"
                  disabled={!!busy || (!message.trim() && !amend) || staged.length === 0}
                  onClick={() => void commit(false)}
                >
                  <GitCommitIcon className="size-3.5" />
                  Commit
                </Button>
              </TooltipTrigger>
              <TooltipContent>Commit the staged changes (Ctrl+Enter)</TooltipContent>
            </Tooltip>
          </div>
          {staged.length === 0 && (message.trim() || amend) && (
            <p className="text-hint text-muted-foreground">Stage something first.</p>
          )}
        </div>
      )}
    </div>
  )
}

function HistoryTab({
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

  return (
    <div className="min-h-0 flex-1 space-y-0.5 overflow-auto p-1">
      {log.data.map((c) => (
        <Tooltip key={c.sha}>
          <TooltipTrigger asChild>
            <button
              onClick={() => show(c)}
              className="flex w-full min-w-0 items-start gap-2 rounded-md px-2 py-1.5 text-left hover:bg-row-hover"
            >
              <GitCommitIcon className="mt-0.5 size-3.5 shrink-0 text-muted-foreground" />
              <div className="min-w-0 flex-1">
                <p className="truncate text-xs">{c.subject}</p>
                <p className="truncate text-micro text-muted-foreground">
                  <span className="font-mono">{c.short}</span> · {c.author} · {relativeTime(c.at)}
                  {c.isMerge ? " · merge" : ""}
                </p>
              </div>
              {(c.insertions > 0 || c.deletions > 0) && (
                <span className="numeric shrink-0 font-mono text-micro">
                  <span className="text-(--git-added)">+{c.insertions}</span>{" "}
                  <span className="text-(--git-deleted)">−{c.deletions}</span>
                </span>
              )}
            </button>
          </TooltipTrigger>
          <TooltipContent>{`${c.short} · ${timestamp(c.at)} — click to see this commit's diff`}</TooltipContent>
        </Tooltip>
      ))}
    </div>
  )
}

function BranchesTab({
  repoPath,
  busy,
  canControl,
  run,
}: {
  repoPath: string
  busy?: string
  canControl: boolean
  run: (label: string, fn: () => Promise<GitResult>) => Promise<GitResult>
}) {
  const [newBranch, setNewBranch] = useState("")
  const branches = usePoll(
    (signal) => get<GitBranch[]>("/git/branches", { path: repoPath }, signal),
    0,
    [repoPath],
  )
  const q = { path: repoPath }

  if (branches.error) return <ErrorState error={branches.error} className="m-3" />
  if (branches.loading && !branches.data) return <LoadingRows className="p-3" rows={5} />

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      {canControl && (
        <form
          className="flex shrink-0 gap-1.5 border-b border-hairline bg-surface-header/60 p-2"
          onSubmit={(e) => {
            e.preventDefault()
            const name = newBranch.trim()
            if (!name) return
            void run(`Created ${name}`, () =>
              post<GitResult>("/git/branch", { ref: name }, { query: q }),
            ).then(() => {
              setNewBranch("")
              branches.refresh()
            })
          }}
        >
          <Input
            value={newBranch}
            onChange={(e) => setNewBranch(e.target.value)}
            placeholder="New branch from HEAD"
            className="h-7 text-xs"
          />
          <Tooltip>
            <TooltipTrigger asChild>
              <Button
                type="submit"
                size="xs"
                variant="outline"
                disabled={!!busy || !newBranch.trim()}
              >
                Create
              </Button>
            </TooltipTrigger>
            <TooltipContent>Create the branch at HEAD and stay where you are</TooltipContent>
          </Tooltip>
        </form>
      )}
      <div className="min-h-0 flex-1 space-y-0.5 overflow-auto p-1">
        {branches.data?.map((b) => (
          <div
            key={b.name}
            className="group flex min-w-0 items-center gap-2 rounded-md px-2 py-1.5 hover:bg-row-hover"
          >
            <GitBranchIcon
              className={cn(
                "size-3.5 shrink-0",
                b.current ? "text-success" : "text-muted-foreground",
              )}
            />
            <div className="min-w-0 flex-1">
              <p className="truncate font-mono text-xs">
                {b.name}
                {b.current && <span className="ml-1.5 text-micro text-success">current</span>}
                {b.remote && (
                  <span className="ml-1.5 text-micro text-muted-foreground">remote</span>
                )}
              </p>
              {b.subject && (
                <p className="truncate text-micro text-muted-foreground">{b.subject}</p>
              )}
            </div>
            <AheadBehind ahead={b.ahead} behind={b.behind} />
            {canControl && !b.current && !b.remote && (
              <Tooltip>
                <TooltipTrigger asChild>
                  <Button
                    size="xs"
                    variant="ghost"
                    disabled={!!busy}
                    className={cn("shrink-0", rowReveal())}
                    onClick={() =>
                      run(`Switched to ${b.name}`, () =>
                        post<GitResult>("/git/checkout", { ref: b.name }, { query: q }),
                      ).then(() => branches.refresh())
                    }
                  >
                    Switch
                  </Button>
                </TooltipTrigger>
                <TooltipContent>{`Check out ${b.name}`}</TooltipContent>
              </Tooltip>
            )}
          </div>
        ))}
      </div>
    </div>
  )
}

// --- small shared pieces ---

function AheadBehind({ ahead, behind }: { ahead: number; behind: number }) {
  if (!ahead && !behind) return null
  return (
    <span className="numeric flex items-center gap-1 font-mono text-hint">
      {ahead > 0 && (
        <Tooltip>
          <TooltipTrigger asChild>
            <span className="inline-flex items-center gap-0.5 text-success">
              <ChevronDoubleUp className="size-3" />
              {ahead}
            </span>
          </TooltipTrigger>
          <TooltipContent>{`${ahead} commit${ahead === 1 ? "" : "s"} to push`}</TooltipContent>
        </Tooltip>
      )}
      {behind > 0 && (
        <Tooltip>
          <TooltipTrigger asChild>
            <span className="inline-flex items-center gap-0.5 text-warning">
              <ChevronDoubleDown className="size-3" />
              {behind}
            </span>
          </TooltipTrigger>
          <TooltipContent>{`${behind} commit${behind === 1 ? "" : "s"} to pull`}</TooltipContent>
        </Tooltip>
      )}
    </span>
  )
}

function FileGroup({
  label,
  count,
  tone,
  action,
  children,
}: {
  label: string
  count: number
  tone: "success" | "warning"
  action?: React.ReactNode
  children: React.ReactNode
}) {
  return (
    <div>
      <div className="sticky top-0 z-10 flex items-center gap-2 bg-surface-header/95 px-2 py-1 backdrop-blur">
        <span
          className={cn("size-1.5 rounded-full", tone === "success" ? "bg-success" : "bg-warning")}
        />
        <span className="text-hint font-medium">{label}</span>
        <span className="numeric text-micro text-muted-foreground">{count}</span>
        <span className="flex-1" />
        {action}
      </div>
      {children}
    </div>
  )
}

function FileRow({
  file,
  onDiff,
  action,
}: {
  file: GitFileChange
  onDiff: () => void
  action?: React.ReactNode
}) {
  const tone = gitTone(file)
  return (
    <div
      className="group relative flex min-w-0 items-center gap-2 bg-(--git-tint) px-2 py-1 before:absolute before:inset-y-0 before:left-0 before:w-[2px] before:bg-(--git-edge) before:content-[''] hover:bg-row-hover"
      style={gitStyle(tone)}
    >
      <Tooltip>
        <TooltipTrigger asChild>
          <span className="w-6 shrink-0 text-center font-mono text-micro text-(--git-colour)">
            {gitLetter(file)}
          </span>
        </TooltipTrigger>
        <TooltipContent>{describeChange(file)}</TooltipContent>
      </Tooltip>
      {/* No tooltip on the name. The row is a list of paths and clicking one
          to see its diff is the only thing it does — a hint that repeats the
          path back and explains the obvious is in the way of reading the
          list, which is what this panel is for. The status letter beside it
          keeps its tooltip, because a letter is not self-explanatory. */}
      <button
        onClick={onDiff}
        className={cn(
          "min-w-0 flex-1 truncate text-left font-mono text-xs text-(--git-colour) hover:underline",
          file.label === "deleted" && "line-through",
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
