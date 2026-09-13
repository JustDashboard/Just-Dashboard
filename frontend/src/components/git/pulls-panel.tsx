"use client"

import { useEffect, useState } from "react"
import { External, GitPullRequest, Plus } from "@/components/icons"
import { errorMessage, get, post } from "@/lib/api"
import { notify } from "@/lib/toast"
import { relativeTime } from "@/lib/format"
import type { GitBranch, GitHubRepo, GitHubStatus, GitPullRequest as PR } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { EmptyState, ErrorState, LoadingRows, Notice } from "@/components/state"
import { Tag } from "@/components/tag"
import { Modal } from "@/components/modal"
import { PaneHeader } from "@/components/panel"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Textarea } from "@/components/ui/textarea"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { ROW_REVEAL } from "@/components/icon-action"
import { cn } from "@/lib/utils"

/**
 * The open pull requests on this repository, and the button that opens one.
 *
 * It is a tab beside Changes and History because that is the sequence the work
 * actually takes — stage, commit, push, propose — and the last step used to be
 * the one that sent you to a browser tab and a different mental model. The
 * request is opened by gh, as the signed-in account, from the branch that is
 * checked out.
 */
export function PullsPanel({
  repoPath,
  branch,
  github,
  canControl,
  onChanged,
}: {
  repoPath: string
  branch: string
  github?: GitHubStatus
  canControl: boolean
  onChanged: () => void
}) {
  const [creating, setCreating] = useState(false)
  const signedIn = github?.available && github.account?.loggedIn

  const pulls = usePoll(
    (signal) => get<PR[]>("/git/github/pulls", { path: repoPath }, signal),
    60000,
    [repoPath],
    { enabled: !!signedIn },
  )

  // The sign-in state decides everything below it, so it is waited for rather
  // than guessed at: rendering "no open pull requests" while the answer is
  // still in flight reads as an answer.
  if (!github) return <LoadingRows className="p-3" rows={4} />
  if (!github.available) {
    return (
      <EmptyState
        className="m-3"
        icon={GitPullRequest}
        title="The GitHub CLI is not installed"
        description="Pull requests are opened through gh. Install it on this host to use them from here."
      />
    )
  }
  if (!signedIn) {
    return (
      <EmptyState
        className="m-3"
        icon={GitPullRequest}
        title="Not signed in to GitHub"
        description="Sign in from the header above to see and open pull requests for this repository."
      />
    )
  }
  if (pulls.error) return <ErrorState error={pulls.error} className="m-3" />
  if (pulls.loading && !pulls.data) return <LoadingRows className="p-3" rows={4} />

  const list = pulls.data ?? []
  const mine = list.find((p) => p.head === branch)

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      {canControl && (
        <PaneHeader className="gap-2 px-2.5">
          <p className="min-w-0 flex-1 truncate text-hint text-muted-foreground">
            {mine ? (
              <>
                <span className="font-mono">{branch}</span> already has one open
              </>
            ) : (
              <>
                from <span className="font-mono">{branch}</span>
              </>
            )}
          </p>
          {mine ? (
            <Button size="sm" variant="outline" asChild>
              <a href={mine.url} target="_blank" rel="noreferrer">
                <External className="size-3.5" />
                Open #{mine.number}
              </a>
            </Button>
          ) : (
            <Button size="sm" variant="outline" onClick={() => setCreating(true)}>
              <Plus className="size-3.5" />
              New pull request
            </Button>
          )}
        </PaneHeader>
      )}

      <div className="min-h-0 flex-1 overflow-auto p-1.5">
        {list.length === 0 ? (
          <EmptyState
            className="m-2"
            icon={GitPullRequest}
            title="No open pull requests"
            description="Push a branch and open one to get it reviewed and merged."
          />
        ) : (
          list.map((p) => (
            <a
              key={p.number}
              href={p.url}
              target="_blank"
              rel="noreferrer"
              className="group flex min-w-0 items-start gap-2 rounded-md px-2 py-1.5 hover:bg-row-hover"
            >
              <GitPullRequest
                className={`mt-0.5 size-4 shrink-0 ${p.draft ? "text-muted-foreground" : "text-success"}`}
              />
              <span className="min-w-0 flex-1">
                <span className="flex min-w-0 items-center gap-1.5">
                  <span className="truncate text-body">{p.title}</span>
                  {p.draft && <Tag>draft</Tag>}
                  {p.head === branch && <Tag tone="warning">this branch</Tag>}
                </span>
                <span className="block truncate text-hint text-muted-foreground">
                  #{p.number} · {p.head} → {p.base}
                  {p.author ? ` · ${p.author}` : ""}
                  {p.createdAt ? ` · ${relativeTime(p.createdAt)}` : ""}
                </span>
              </span>
              <External
                className={cn("mt-0.5 size-3.5 shrink-0 text-muted-foreground", ROW_REVEAL)}
              />
            </a>
          ))
        )}
      </div>

      <CreatePullDialog
        open={creating}
        onOpenChange={setCreating}
        repoPath={repoPath}
        branch={branch}
        onCreated={() => {
          pulls.refresh()
          onChanged()
        }}
      />
    </div>
  )
}

/**
 * Opening a pull request, with the two things gh would otherwise have prompted
 * for filled in: which branch it targets, and whether it is a draft.
 *
 * The branch is pushed by the server before gh is asked, because a pull
 * request from a branch the remote has never seen is not a thing GitHub can
 * make — and being told that after writing a description is the worst moment
 * to learn it.
 */
function CreatePullDialog({
  open,
  onOpenChange,
  repoPath,
  branch,
  onCreated,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  repoPath: string
  branch: string
  onCreated: () => void
}) {
  const [title, setTitle] = useState("")
  const [body, setBody] = useState("")
  const [base, setBase] = useState("")
  const [draft, setDraft] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()
  const [repo, setRepo] = useState<GitHubRepo>()
  const [branches, setBranches] = useState<GitBranch[]>([])

  // Closing is where the form resets, rather than opening: setting state on
  // the way in is a second render before anything is on screen.
  const change = (next: boolean) => {
    if (!next) {
      setError(undefined)
      setBusy(false)
    }
    onOpenChange(next)
  }

  useEffect(() => {
    if (!open) return
    let alive = true
    void (async () => {
      try {
        const [info, list] = await Promise.all([
          get<GitHubRepo>("/git/github/repo", { path: repoPath }),
          get<GitBranch[]>("/git/branches", { path: repoPath }),
        ])
        if (!alive) return
        setRepo(info)
        setBranches(list.filter((b) => !b.remote && b.name !== branch))
        setBase((b) => b || info.defaultBranch)
      } catch (err) {
        if (alive) setError(errorMessage(err))
      }
    })()
    return () => {
      alive = false
    }
  }, [open, repoPath, branch])

  const create = async () => {
    setBusy(true)
    setError(undefined)
    try {
      const pr = await post<PR>(
        "/git/github/pulls",
        { title: title.trim(), body, base, head: branch, draft },
        { query: { path: repoPath } },
      )
      notify.success("Pull request opened", { description: pr.url })
      onCreated()
      change(false)
      setTitle("")
      setBody("")
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal
      open={open}
      onOpenChange={change}
      title="New pull request"
      description={
        <>
          {repo?.nameWithOwner ? `${repo.nameWithOwner} · ` : ""}
          <span className="font-mono">{branch}</span> into{" "}
          <span className="font-mono">{base || "…"}</span>. The branch is pushed first.
        </>
      }
      footer={
        <>
          <Button variant="ghost" onClick={() => change(false)}>
            Cancel
          </Button>
          <Tooltip>
            <TooltipTrigger asChild>
              <Button disabled={busy || !title.trim() || !base} onClick={create} pending={busy}>
                <GitPullRequest className="size-4" />
                Push and open
              </Button>
            </TooltipTrigger>
            <TooltipContent>
              Pushes {branch} to the remote, then opens the pull request as your GitHub account
            </TooltipContent>
          </Tooltip>
        </>
      }
    >
      {error && (
        <Notice title="Could not open the pull request" tone="danger">
          <span className="break-words whitespace-pre-wrap">{error}</span>
        </Notice>
      )}
      {repo?.permission === "READ" && (
        <Notice title="You have read access to this repository" tone="warning">
          GitHub will refuse a pull request from a branch here — it has to come from a fork.
        </Notice>
      )}

      <div className="space-y-3">
        <div className="space-y-1.5">
          <Label htmlFor="pr-title">Title</Label>
          <Input
            id="pr-title"
            value={title}
            onChange={(e) => setTitle(e.target.value)}
            placeholder="What does this change?"
          />
        </div>
        <div className="space-y-1.5">
          <Label>Merge into</Label>
          <Select value={base} onValueChange={setBase}>
            <SelectTrigger className="w-full">
              <SelectValue placeholder="Choose a branch" />
            </SelectTrigger>
            <SelectContent>
              {branches.map((b) => (
                <SelectItem key={b.name} value={b.name}>
                  {b.name}
                  {b.name === repo?.defaultBranch ? " · default" : ""}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
        <div className="space-y-1.5">
          <Label htmlFor="pr-body">Description</Label>
          <Textarea
            id="pr-body"
            rows={5}
            value={body}
            onChange={(e) => setBody(e.target.value)}
            placeholder="What a reviewer needs to know. Markdown works."
            className="resize-none text-body"
          />
        </div>
        <label className="flex cursor-pointer items-center gap-2 text-xs text-muted-foreground">
          <Checkbox checked={draft} onCheckedChange={(v) => setDraft(Boolean(v))} />
          Open as a draft — nobody is asked to review it yet
        </label>
      </div>
    </Modal>
  )
}
