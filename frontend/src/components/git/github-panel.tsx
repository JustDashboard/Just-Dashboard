"use client"

import { useEffect, useState } from "react"
import { BranchPlus, External, GitMerge, GitPullRequest, Plus } from "@/components/icons"
import { errorMessage, get, post } from "@/lib/api"
import { notify } from "@/lib/toast"
import { relativeTime } from "@/lib/format"
import { cn } from "@/lib/utils"
import type {
  GitBranch,
  GitComparison,
  GitHubRepo,
  GitHubStatus,
  GitHubWorkflowRun,
  GitPullRequest as PR,
} from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { MergePullDialog } from "@/components/git/merge-pull-dialog"
import type { GitPreview } from "@/components/git/preview-panel"
import type { GitRun } from "@/components/git/run"
import { EmptyState, ErrorState, LoadingRows, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { Modal } from "@/components/modal"
import { Field, FormFacts, FormFact, OptionList, OptionRow } from "@/components/form"
import { ChipCount, FilterChip } from "@/components/tabs"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Textarea } from "@/components/ui/textarea"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { rowReveal } from "@/components/icon-action"
import { VerbActions, type Verb } from "@/components/verbs"

type PullState = "open" | "merged" | "closed"

/**
 * What GitHub knows about this repository: its pull requests, with their
 * review and check state on the row; the Actions runs on the current branch,
 * because "did CI pass on what I just pushed" is asked beside the push
 * button; and the button that opens a pull request from the branch that is
 * checked out.
 *
 * It is a tab beside Changes and History because that is the sequence the
 * work actually takes — stage, commit, push, propose, merge — and the last
 * steps used to be the ones that sent you to a browser tab and a different
 * mental model.
 */
export function GitHubPanel({
  repoPath,
  branch,
  github,
  busy,
  canControl,
  run,
  onSelect,
  active,
  onChanged,
}: {
  repoPath: string
  branch: string
  github?: GitHubStatus
  busy?: string
  canControl: boolean
  run: GitRun
  onSelect: (p: GitPreview) => void
  active?: string
  onChanged: () => void
}) {
  const [creating, setCreating] = useState(false)
  const [merging, setMerging] = useState<PR | null>(null)
  const [state, setState] = useState<PullState>("open")
  const signedIn = Boolean(github?.available && github.account?.loggedIn)

  const repo = usePoll(
    (signal) => get<GitHubRepo>("/git/github/repo", { path: repoPath }, signal),
    300_000,
    [repoPath],
    { enabled: signedIn },
  )
  const pulls = usePoll(
    (signal) => get<PR[]>("/git/github/pulls", { path: repoPath, state }, signal),
    60_000,
    [repoPath, state],
    { enabled: signedIn },
  )
  const runs = usePoll(
    (signal) =>
      get<GitHubWorkflowRun[]>(
        "/git/github/runs",
        { path: repoPath, branch: branch || undefined, limit: 8 },
        signal,
      ),
    60_000,
    [repoPath, branch],
    { enabled: signedIn && Boolean(branch) },
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
        description="Pull requests and workflow runs come through gh. Install it on this host to use them from here."
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

  const list = pulls.data ?? []
  const mine = state === "open" ? list.find((p) => p.head === branch) : undefined
  const q = { path: repoPath }

  const verbsFor = (p: PR): Verb[] => {
    const verbs: Verb[] = [
      {
        key: "open",
        label: "Open on GitHub",
        detail: "The request's own page, with the conversation and the review.",
        icon: External,
        run: () => window.open(p.url, "_blank", "noopener"),
      },
    ]
    if (canControl && p.state === "open") {
      verbs.unshift({
        key: "merge",
        label: "Merge",
        detail: "Merge it into its base branch on GitHub.",
        icon: GitMerge,
        disabled: !!busy || p.draft,
        run: () => setMerging(p),
      })
      verbs.push({
        key: "checkout",
        label: "Check out the branch",
        detail: `Fetch ${p.head} and switch this working tree to it.`,
        icon: BranchPlus,
        disabled: !!busy,
        run: () =>
          void run(`Checked out ${p.head}`, () =>
            post(`/git/github/pulls/${p.number}/checkout`, undefined, { query: q }).then(() => ({
              command: "gh pr checkout",
              output: `On ${p.head}`,
              ok: true,
            })),
          ).catch(() => undefined),
      })
    }
    return verbs
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      {repo.data && (
        <div className="flex shrink-0 flex-wrap items-center gap-x-2 gap-y-1 border-b border-hairline px-3 py-1.5 text-hint text-muted-foreground">
          <a
            href={repo.data.url}
            target="_blank"
            rel="noreferrer"
            className="min-w-0 truncate font-mono text-foreground hover:underline"
          >
            {repo.data.nameWithOwner}
          </a>
          <Tag>{repo.data.private ? "private" : "public"}</Tag>
          <span className="truncate">
            default <span className="font-mono">{repo.data.defaultBranch}</span>
          </span>
          {repo.data.permission && <Tag>{repo.data.permission.toLowerCase()}</Tag>}
        </div>
      )}

      <div className="flex shrink-0 items-center gap-1 border-b border-hairline px-2 py-1.5">
        {(["open", "merged", "closed"] as const).map((key) => (
          <FilterChip key={key} selected={state === key} onClick={() => setState(key)}>
            {key === "open" ? "Open" : key === "merged" ? "Merged" : "Closed"}
            {key === state && pulls.data && <ChipCount>{pulls.data.length}</ChipCount>}
          </FilterChip>
        ))}
        <span className="flex-1" />
        {canControl &&
          (mine ? (
            <Button size="xs" variant="outline" asChild>
              <a href={mine.url} target="_blank" rel="noreferrer">
                <External className="size-3" />
                Open #{mine.number}
              </a>
            </Button>
          ) : (
            <Tooltip>
              <TooltipTrigger asChild>
                <Button size="xs" variant="outline" onClick={() => setCreating(true)}>
                  <Plus className="size-3" />
                  New pull request
                </Button>
              </TooltipTrigger>
              <TooltipContent>
                Propose <span className="font-mono">{branch}</span> for review and merging
              </TooltipContent>
            </Tooltip>
          ))}
      </div>

      <div className="min-h-0 flex-1 overflow-auto">
        {pulls.error && <ErrorState error={pulls.error} className="m-3" />}
        {pulls.loading && !pulls.data && <LoadingRows className="p-3" rows={4} />}
        {pulls.data && list.length === 0 && (
          <EmptyState
            className="m-3"
            icon={GitPullRequest}
            title={`No ${state} pull requests`}
            description={
              state === "open"
                ? "Push a branch and open one to get it reviewed and merged."
                : undefined
            }
          />
        )}
        {list.length > 0 && (
          <ul className="animate-rise divide-y divide-hairline">
            {list.map((p) => (
              <li
                key={p.number}
                className={cn(
                  "group flex min-w-0 items-start gap-2 py-1.5 pr-1.5 pl-3 transition-colors hover:bg-row-hover",
                  active === `pull:${p.number}` && "bg-accent",
                )}
              >
                <button
                  type="button"
                  aria-pressed={active === `pull:${p.number}`}
                  onClick={() => onSelect({ kind: "pull", number: p.number, title: p.title })}
                  className="min-w-0 flex-1 text-left focus-ring-inset"
                >
                  <span className="flex min-w-0 items-center gap-1.5">
                    <span className="truncate text-body">{p.title}</span>
                    {p.draft && <Tag>draft</Tag>}
                    {p.head === branch && <Tag tone="warning">this branch</Tag>}
                  </span>
                  <span className="mt-0.5 flex min-w-0 flex-wrap items-center gap-x-2 gap-y-0.5 text-hint text-muted-foreground">
                    <span className="truncate">
                      #{p.number} · <span className="font-mono">{p.head}</span> →{" "}
                      <span className="font-mono">{p.base}</span>
                      {p.author ? ` · ${p.author}` : ""}
                      {p.createdAt ? ` · ${relativeTime(p.createdAt)}` : ""}
                    </span>
                    {p.checks && (
                      <Status
                        className="text-hint"
                        tone={
                          p.checks === "success"
                            ? "running"
                            : p.checks === "failure"
                              ? "danger"
                              : "warning"
                        }
                        label={
                          p.checks === "success"
                            ? "checks passed"
                            : p.checks === "failure"
                              ? "checks failed"
                              : "checks running"
                        }
                      />
                    )}
                    {p.review === "approved" && (
                      <Status className="text-hint" tone="running" label="approved" />
                    )}
                    {p.review === "changes_requested" && (
                      <Status className="text-hint" tone="danger" label="changes requested" />
                    )}
                  </span>
                </button>
                <VerbActions verbs={verbsFor(p)} reveal className="mt-0.5" />
              </li>
            ))}
          </ul>
        )}

        {branch && (
          <>
            <div className="sticky top-0 z-10 flex h-8 items-center gap-1.5 border-y border-hairline bg-card px-3">
              <span className="eyebrow">Workflow runs</span>
              <span className="truncate font-mono text-hint text-muted-foreground">{branch}</span>
            </div>
            {runs.error && (
              <p className="px-3 py-2 text-hint text-muted-foreground">
                Could not list workflow runs: {errorMessage(runs.error)}
              </p>
            )}
            {runs.loading && !runs.data && <LoadingRows className="p-3" rows={2} />}
            {runs.data && runs.data.length === 0 && (
              <p className="px-3 py-3 text-hint text-muted-foreground">
                No Actions runs on this branch.
              </p>
            )}
            {runs.data && runs.data.length > 0 && (
              <ul className="animate-rise divide-y divide-hairline">
                {runs.data.map((r) => (
                  <li key={r.id} className="min-w-0">
                    <a
                      href={r.url}
                      target="_blank"
                      rel="noreferrer"
                      className="group flex min-w-0 items-center gap-2 px-3 py-1.5 transition-colors hover:bg-row-hover"
                    >
                      <Status
                        tone={
                          r.status !== "completed"
                            ? "warning"
                            : r.conclusion === "success"
                              ? "running"
                              : r.conclusion === "failure" || r.conclusion === "timed_out"
                                ? "danger"
                                : "stopped"
                        }
                        label=""
                        className="gap-0"
                      />
                      <span className="min-w-0 flex-1">
                        <span className="block truncate text-xs">{r.workflow || r.name}</span>
                        <span className="block truncate text-micro text-muted-foreground">
                          {r.status !== "completed" ? r.status.replace("_", " ") : r.conclusion}
                          {r.event ? ` · ${r.event}` : ""}
                          {r.sha ? ` · ${r.sha.slice(0, 7)}` : ""}
                          {r.createdAt ? ` · ${relativeTime(r.createdAt)}` : ""}
                        </span>
                      </span>
                      <External
                        className={cn("size-3.5 shrink-0 text-muted-foreground", rowReveal())}
                      />
                    </a>
                  </li>
                ))}
              </ul>
            )}
          </>
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
      {merging && (
        <MergePullDialog
          open
          onOpenChange={(o) => !o && setMerging(null)}
          repoPath={repoPath}
          pull={merging}
          onMerged={() => {
            pulls.refresh()
            onChanged()
          }}
        />
      )}
    </div>
  )
}

/**
 * Opening a pull request, with the two things gh would otherwise have prompted
 * for filled in: which branch it targets, and whether it is a draft — and the
 * one thing GitHub's own form shows that this one used not to: what the
 * request would carry, counted before anything is pushed.
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
  // The comparison, tagged with what it compares, so a stale answer for the
  // previous base is never shown against the new one.
  const [compare, setCompare] = useState<{ key: string; data: GitComparison }>()

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

  // What the request would carry, from the local branches: an answer before
  // the push, and a warning when there is nothing to propose.
  const compareKey = `${base}|${branch}`
  useEffect(() => {
    if (!open || !base || !branch) return
    let alive = true
    get<GitComparison>("/git/compare", { path: repoPath, base, head: branch })
      .then((data) => alive && setCompare({ key: compareKey, data }))
      .catch(() => undefined)
    return () => {
      alive = false
    }
  }, [open, repoPath, base, branch, compareKey])
  const carrying = compare?.key === compareKey ? compare.data : undefined

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
      description={`${branch} into ${base || "…"}. The branch is pushed first.`}
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
      <div className="space-y-3">
        <FormFacts>
          {repo?.nameWithOwner && <FormFact label="on">{repo.nameWithOwner}</FormFact>}
          <FormFact label="from" mono>
            {branch}
          </FormFact>
          <FormFact label="into" mono>
            {base || "…"}
          </FormFact>
          {carrying && (
            <FormFact label="carrying">
              {carrying.ahead} commit{carrying.ahead === 1 ? "" : "s"} · {carrying.files} file
              {carrying.files === 1 ? "" : "s"}
            </FormFact>
          )}
        </FormFacts>
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
        {carrying && carrying.ahead === 0 && (
          <Notice title={`${branch} has nothing ${base} lacks`} tone="warning">
            GitHub refuses a pull request with no commits in it. Commit something first, or pick
            another base.
          </Notice>
        )}
        <Field label="Title" htmlFor="pr-title">
          <Input
            id="pr-title"
            value={title}
            onChange={(e) => setTitle(e.target.value)}
            placeholder="What does this change?"
          />
        </Field>
        <Field label="Merge into">
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
        </Field>
        <Field label="Description" htmlFor="pr-body" hint="What a reviewer needs to know. Markdown works.">
          <Textarea
            id="pr-body"
            rows={5}
            value={body}
            onChange={(e) => setBody(e.target.value)}
            className="resize-none text-body"
          />
        </Field>
        <OptionList>
          <OptionRow
            title="Open as a draft"
            hint="Nobody is asked to review it yet."
            checked={draft}
            onCheckedChange={setDraft}
          />
        </OptionList>
      </div>
    </Modal>
  )
}
