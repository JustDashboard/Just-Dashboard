"use client"

import { useState } from "react"
import Link from "next/link"
import { ArrowUpRight, Check, ChevronDown, External, GitBranch } from "@/components/icons"
import { get, post } from "@/lib/api"
import { relativeTime } from "@/lib/format"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"
import type {
  GitBranch as Branch,
  GitDetect,
  GitPullRequest,
  GitResult,
  GitStatus,
} from "@/lib/types"
import { useViewState } from "@/lib/view-state"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { useGitHubAccount } from "@/hooks/use-github"
import { AheadBehind } from "@/components/git/ahead-behind"
import { SourcePull } from "@/components/git/glyphs"
import { SearchInput } from "@/components/page"
import { ProductGlyph } from "@/components/product-logo"
import { EmptyState, ErrorState, LoadingRows } from "@/components/state"
import { StatusDot, type DotTone } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { ChipCount, FilterChip } from "@/components/tabs"
import { IconAction } from "@/components/icon-action"
import { Button } from "@/components/ui/button"
import { DiffTools } from "@/components/terminal/diff-tools"

type View = "changes" | "branches" | "pulls"

/**
 * The repository the shell is in: what changed, which branch it is on, and
 * the pull requests open against it.
 *
 * The diff is still the first view, because beside a shell the first question
 * is what the work is. The other two are the two git moves made *from* a
 * shell's directory rather than in it — "put me on that branch" and "put me on
 * that pull request so I can try it" — each one press, with the shell's own
 * prompt showing the result. Everything else stays on the Git page.
 */
export function GitTools({
  detect,
  detectLoading,
  detectError,
  status,
  active,
  onChanged,
}: {
  detect?: GitDetect
  detectLoading: boolean
  detectError?: Error
  status: { data?: GitStatus | null; loading: boolean; error?: Error }
  /** Whether the tab is on screen; GitHub is only asked while it is. */
  active: boolean
  onChanged: () => void
}) {
  const [view, setView] = useViewState<View>("terminal.tools.git", "changes")
  const repo = detect?.inRoots ? detect.repo : undefined
  const branch = status.data?.repo.branch ?? repo?.branch ?? ""

  // Asked while the tab is up so the chip can carry the count, and through gh,
  // which is why it waits for the tab rather than running beside every shell.
  const github = useGitHubAccount(repo?.path, active && Boolean(repo))
  const signedIn = Boolean(github.data?.available && github.data.account?.loggedIn)
  const pulls = usePoll(
    (signal) =>
      get<GitPullRequest[]>("/git/github/pulls", { path: repo?.path, state: "open" }, signal),
    60_000,
    [repo?.path],
    { enabled: active && signedIn && Boolean(repo) },
  )

  if (detectError) return <ErrorState error={detectError} className="m-3" />
  if (detectLoading && !detect) return <LoadingRows className="p-3" rows={5} />
  if (!detect?.available) {
    return (
      <EmptyState
        className="m-3"
        icon={GitBranch}
        title="git is not installed"
        description="Install git on the host and the shell's changes show up here."
      />
    )
  }
  if (!repo) {
    return (
      <EmptyState
        className="m-3"
        icon={GitBranch}
        title="Not in a repository"
        description="cd into a git checkout and what you have changed there shows up here."
      />
    )
  }

  const ahead = status.data?.repo.ahead ?? repo.ahead
  const behind = status.data?.repo.behind ?? repo.behind
  const changed = status.data?.files.length ?? 0

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="flex min-h-10 shrink-0 items-center gap-2 border-b border-hairline px-2 py-1.5">
        <button
          type="button"
          title={`${repo.path} — switch branch`}
          aria-label={`On ${branch || "a detached HEAD"}, switch branch`}
          onClick={() => setView("branches")}
          className="flex min-w-0 items-center gap-1.5 rounded-md px-1.5 py-1 focus-ring transition-colors hover:bg-row-hover"
        >
          <ProductGlyph id="git" />
          <span className="min-w-0 truncate font-mono text-xs text-foreground">
            {branch || "detached"}
          </span>
          <ChevronDown aria-hidden className="size-3 shrink-0 text-muted-foreground" />
        </button>
        <AheadBehind ahead={ahead} behind={behind} />
        <span className="flex-1" />
        <Link
          href={`/git?repo=${encodeURIComponent(repo.path)}`}
          className="mr-1 inline-flex shrink-0 items-center gap-1 rounded-sm text-hint font-medium text-muted-foreground focus-ring transition-colors hover:text-foreground"
        >
          Open in Git
          <ArrowUpRight aria-hidden className="size-3" />
        </Link>
      </div>
      <div
        role="group"
        aria-label="Git view"
        className="flex shrink-0 items-center gap-1 border-b border-hairline px-2 py-1.5"
      >
        <FilterChip selected={view === "changes"} onClick={() => setView("changes")}>
          Changes
          {changed > 0 && <ChipCount>{changed}</ChipCount>}
        </FilterChip>
        <FilterChip selected={view === "branches"} onClick={() => setView("branches")}>
          Branches
        </FilterChip>
        <FilterChip selected={view === "pulls"} onClick={() => setView("pulls")}>
          Pull requests
          {pulls.data && pulls.data.length > 0 && <ChipCount>{pulls.data.length}</ChipCount>}
        </FilterChip>
      </div>
      {view === "changes" && <DiffTools repoPath={repo.path} status={status} />}
      {view === "branches" && (
        <BranchList repoPath={repo.path} current={branch} onChanged={onChanged} />
      )}
      {view === "pulls" && (
        <PullList
          repoPath={repo.path}
          current={branch}
          github={github}
          pulls={pulls}
          onChanged={() => {
            pulls.refresh()
            onChanged()
          }}
        />
      )}
    </div>
  )
}

/** One git move at a time, with its failure in git's own words. */
function useGitMove(onDone: () => void) {
  const [busy, setBusy] = useState<string>()
  const move = async (key: string, success: string, request: () => Promise<unknown>) => {
    setBusy(key)
    try {
      await request()
      notify.success(success)
      onDone()
    } catch (err) {
      notify.error("git refused", err)
    } finally {
      setBusy(undefined)
    }
  }
  return { busy, move }
}

function BranchList({
  repoPath,
  current,
  onChanged,
}: {
  repoPath: string
  current: string
  onChanged: () => void
}) {
  const { can } = useAuth()
  const canControl = can("service.control")
  const [filter, setFilter] = useState("")
  // Read again when the shell changes branch on its own, not on a timer.
  const branches = usePoll(
    (signal) => get<Branch[]>("/git/branches", { path: repoPath }, signal),
    0,
    [repoPath, current],
  )
  const { busy, move } = useGitMove(() => {
    branches.refresh()
    onChanged()
  })

  if (branches.error && !branches.data) return <ErrorState error={branches.error} className="m-3" />
  if (!branches.data) return <LoadingRows className="p-3" rows={5} />

  const needle = filter.trim().toLowerCase()
  const matches = (b: Branch) => !needle || b.name.toLowerCase().includes(needle)
  const local = branches.data.filter((b) => !b.remote)
  const localNames = new Set(local.map((b) => b.name))
  // A remote branch that already has a local one is reached through that one.
  const remote = branches.data.filter((b) => b.remote && b.local && !localNames.has(b.local))
  const shownLocal = local.filter(matches)
  const shownRemote = remote.filter(matches)

  const checkout = (b: Branch) =>
    move(b.name, b.remote ? `Checked out ${b.local}` : `Switched to ${b.name}`, () =>
      post<GitResult>(
        "/git/checkout",
        b.remote ? { ref: b.name, local: b.local } : { ref: b.name },
        { query: { path: repoPath } },
      ),
    )

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      {local.length + remote.length > 6 && (
        <div className="shrink-0 border-b border-hairline px-2 py-1.5">
          <SearchInput
            dense
            value={filter}
            spellCheck={false}
            onChange={(event) => setFilter(event.target.value)}
            placeholder="Filter branches"
            containerClassName="sm:w-full"
          />
        </div>
      )}
      <div className="min-h-0 flex-1 overflow-y-auto">
        <ul aria-label="Local branches" className="py-1">
          {shownLocal.map((b) => (
            <BranchRow
              key={b.name}
              branch={b}
              current={b.current}
              detail={
                b.worktree
                  ? `checked out in ${b.worktree}`
                  : [b.subject, b.at && relativeTime(b.at)].filter(Boolean).join(" · ")
              }
              action={
                canControl && !b.current && !b.worktree ? (
                  <Button
                    size="xs"
                    variant="ghost"
                    className="shrink-0"
                    disabled={Boolean(busy)}
                    pending={busy === b.name}
                    onClick={() => void checkout(b)}
                  >
                    Switch
                  </Button>
                ) : undefined
              }
            />
          ))}
        </ul>
        {shownRemote.length > 0 && (
          <>
            <p className="eyebrow px-3 pt-2 pb-1">Remote</p>
            <ul aria-label="Remote branches" className="pb-1">
              {shownRemote.map((b) => (
                <BranchRow
                  key={b.name}
                  branch={b}
                  muted
                  detail={[b.subject, b.at && relativeTime(b.at)].filter(Boolean).join(" · ")}
                  action={
                    canControl ? (
                      <Button
                        size="xs"
                        variant="ghost"
                        className="shrink-0"
                        title={`Make a local ${b.local} that tracks ${b.name}, and switch to it`}
                        disabled={Boolean(busy)}
                        pending={busy === b.name}
                        onClick={() => void checkout(b)}
                      >
                        Check out
                      </Button>
                    ) : undefined
                  }
                />
              ))}
            </ul>
          </>
        )}
        {shownLocal.length + shownRemote.length === 0 && (
          <p className="px-3 py-4 text-center text-xs text-muted-foreground">
            {needle ? (
              <>
                No branch matches <span className="font-medium text-foreground">{filter}</span>.
              </>
            ) : (
              "No branches yet."
            )}
          </p>
        )}
      </div>
    </div>
  )
}

function BranchRow({
  branch,
  current,
  muted,
  detail,
  action,
}: {
  branch: Branch
  current?: boolean
  muted?: boolean
  detail: string
  action?: React.ReactNode
}) {
  return (
    <li
      data-branch={branch.name}
      data-current={current || undefined}
      className="flex min-w-0 items-center gap-2 py-1.5 pr-1.5 pl-3 transition-colors hover:bg-row-hover"
    >
      <span className="flex w-3.5 shrink-0 justify-center">
        {current && <Check aria-label="Current branch" className="size-3.5 text-success" />}
      </span>
      <div className="min-w-0 flex-1">
        <p
          className={cn(
            "truncate font-mono text-xs",
            muted ? "text-muted-foreground" : "text-foreground",
          )}
          title={branch.name}
        >
          {branch.name}
        </p>
        {detail && (
          <p className="truncate text-micro text-muted-foreground" title={detail}>
            {detail}
          </p>
        )}
      </div>
      <AheadBehind ahead={branch.ahead} behind={branch.behind} />
      {action}
    </li>
  )
}

const CHECKS: Record<string, [DotTone, string]> = {
  success: ["running", "Checks passed"],
  failure: ["danger", "Checks failed"],
  pending: ["warning", "Checks running"],
}

function PullList({
  repoPath,
  current,
  github,
  pulls,
  onChanged,
}: {
  repoPath: string
  current: string
  github: ReturnType<typeof useGitHubAccount>
  pulls: { data?: GitPullRequest[]; error?: Error; loading: boolean }
  onChanged: () => void
}) {
  const { can } = useAuth()
  const canControl = can("service.control")
  const { busy, move } = useGitMove(onChanged)
  const gitPage = `/git?repo=${encodeURIComponent(repoPath)}`

  if (!github.data) return <LoadingRows className="p-3" rows={4} />
  if (!github.data.available) {
    return (
      <EmptyState
        className="m-3"
        icon={SourcePull}
        title="The GitHub CLI is not installed"
        description="Pull requests come through gh. Install it on this host to see them here."
      />
    )
  }
  if (!github.data.account?.loggedIn) {
    return (
      <EmptyState
        className="m-3"
        icon={SourcePull}
        title="Not signed in to GitHub"
        description="Sign in from the Git page and this repository's pull requests show up here."
        action={
          <Button size="sm" variant="outline" asChild>
            <Link href={gitPage}>Open in Git</Link>
          </Button>
        }
      />
    )
  }
  if (pulls.error && !pulls.data) return <ErrorState error={pulls.error} className="m-3" />
  if (!pulls.data) return <LoadingRows className="p-3" rows={4} />
  if (pulls.data.length === 0) {
    return (
      <EmptyState
        className="m-3"
        icon={SourcePull}
        title="No open pull requests"
        description="Pull requests opened against this repository show up here."
      />
    )
  }

  return (
    <ul aria-label="Open pull requests" className="min-h-0 flex-1 overflow-y-auto py-1">
      {pulls.data.map((p) => {
        const here = p.head === current
        const checks = p.checks ? CHECKS[p.checks] : undefined
        return (
          <li
            key={p.number}
            data-pull={p.number}
            className="group flex min-w-0 items-center gap-2 py-1.5 pr-1.5 pl-3 transition-colors hover:bg-row-hover"
          >
            <SourcePull
              aria-hidden
              className={cn(
                "size-3.5 shrink-0",
                p.draft ? "text-muted-foreground" : "text-success",
              )}
            />
            <div className="min-w-0 flex-1">
              <p className="flex min-w-0 items-center gap-1.5 text-xs">
                <span className="min-w-0 truncate font-medium text-foreground" title={p.title}>
                  {p.title}
                </span>
                {checks && (
                  <span role="img" aria-label={checks[1]} title={checks[1]} className="flex">
                    <StatusDot tone={checks[0]} />
                  </span>
                )}
              </p>
              <p
                className="truncate text-micro text-muted-foreground"
                title={`${p.head} → ${p.base}`}
              >
                <span className="numeric">#{p.number}</span> ·{" "}
                <span className="font-mono">{p.head}</span>
                {p.author && ` · ${p.author}`}
                {p.updatedAt && ` · ${relativeTime(p.updatedAt)}`}
              </p>
            </div>
            {p.draft && <Tag>draft</Tag>}
            {here ? (
              <Tag tone="success">checked out</Tag>
            ) : (
              canControl && (
                <Button
                  size="xs"
                  variant="ghost"
                  className="shrink-0"
                  title={`Check out ${p.head} to try it here`}
                  disabled={Boolean(busy)}
                  pending={busy === String(p.number)}
                  onClick={() =>
                    void move(String(p.number), `Checked out #${p.number}`, () =>
                      post(`/git/github/pulls/${p.number}/checkout`, undefined, {
                        query: { path: repoPath },
                      }),
                    )
                  }
                >
                  Check out
                </Button>
              )
            )}
            <IconAction
              label={`Open #${p.number} on GitHub`}
              reveal
              onClick={() => window.open(p.url, "_blank", "noopener")}
            >
              <External />
            </IconAction>
          </li>
        )
      })}
    </ul>
  )
}
