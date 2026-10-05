"use client"

import { useState } from "react"
import { CheckCircle, Clock, CrossCircle, External, Slash } from "@/components/icons"
import { errorMessage, get, post } from "@/lib/api"
import { plural, relativeTime } from "@/lib/format"
import type { GitHubCheckRun, GitPullRequest, GitResult } from "@/lib/types"
import { cn } from "@/lib/utils"
import { usePoll } from "@/hooks/use-poll"
import { CommentCard, PullReview } from "@/components/git/github-review"
import { SourceBranch, SourceMerge } from "@/components/git/glyphs"
import { BranchChip, ForgeFace } from "@/components/git/marks"
import { MergePullDialog } from "@/components/git/merge-pull-dialog"
import { PreviewHeader } from "@/components/git/preview-header"
import type { PreviewContext } from "@/components/git/preview-panel"
import {
  ChecksMark,
  PullStateMark,
  PullStateWord,
  ReviewMark,
  pullStateOf,
} from "@/components/git/pull-state"
import { IconAction } from "@/components/icon-action"
import { ErrorState, LoadingRows } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"

/** The repository's page on GitHub, from a request's own address. */
export function repoUrlOf(pullUrl: string | undefined): string | undefined {
  const match = /^(https:\/\/[^/]+\/[^/]+\/[^/]+)\/pull\/\d+/.exec(pullUrl ?? "")
  return match?.[1]
}

/**
 * A pull request, laid out the way its forge lays one out, because that is
 * the page a reader is comparing it with.
 *
 * It used to be a status line, three buttons and the description as one grey
 * paragraph with its Markdown printed verbatim — "## What changed" — and the
 * conversation behind a chip under the checks, with commenting a button that
 * opened a dialog over the very text being answered. Now, top to bottom:
 *
 * - **who wants what**: the state in its forge colour, the author's face, and
 *   the sentence GitHub writes — wants to merge `head` into `base` — with
 *   how the checks and the reviews stand and the size of the change;
 * - **the verbs**: Merge is the one command face, pinned to the head commit
 *   so what was reviewed is what lands; checking the branch out and opening
 *   it on GitHub beside it;
 * - **the description**, drawn as the first comment of the conversation,
 *   rendered from its Markdown (`markdown.tsx`);
 * - **the checks** on the head commit, each with its mark;
 * - **the conversation and the changed files**, and under them the comment
 *   box as a section of the page (`github-review.tsx`) — answering is
 *   something done *on* the request, below what is being answered.
 */
export function PullPreview({
  number,
  title,
  ctx,
  onClose,
}: {
  number: number
  title?: string
  ctx: PreviewContext
  onClose: () => void
}) {
  const pull = usePoll(
    (signal) => get<GitPullRequest>(`/git/github/pulls/${number}`, { path: ctx.repoPath }, signal),
    30_000,
    [ctx.repoPath, number],
  )
  const [merging, setMerging] = useState(false)
  const p = pull.data
  const q = { path: ctx.repoPath }
  // Read against the head the detail reported, never a bare number: the
  // route wants the sha, and a listing's checks for an older head would be a
  // verdict on code that is no longer on the request.
  const checks = usePoll(
    (signal) =>
      get<GitHubCheckRun[]>(
        `/git/github/pulls/${number}/checks`,
        { path: ctx.repoPath, head: p?.headSha },
        signal,
      ),
    60_000,
    [ctx.repoPath, number, p?.headSha],
    { enabled: Boolean(p?.headSha) },
  )
  const repoUrl = repoUrlOf(p?.url)
  const state = p ? pullStateOf(p) : undefined
  const open = p?.state === "open"
  const mergeBlocked = !p || p.draft || p.mergeable === "conflicting"

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <PreviewHeader
        mono={false}
        leading={p ? <PullStateMark pull={p} /> : undefined}
        title={p?.title ?? title ?? `#${number}`}
        subtitle={
          p ? (
            <>
              <span className="numeric">#{p.number}</span> · {p.author ?? "unknown"}
              {p.createdAt ? ` · opened ${relativeTime(p.createdAt)}` : ""}
            </>
          ) : (
            `#${number}`
          )
        }
        onClose={onClose}
      />
      <div className="min-h-0 flex-1 overflow-auto">
        {pull.error && <ErrorState error={pull.error} className="m-3" />}
        {pull.loading && !p && <LoadingRows className="p-3" rows={5} />}
        {p && (
          <div className="animate-rise space-y-3 px-3 py-3">
            {/* Who wants what, in GitHub's own sentence. */}
            <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1.5 text-hint text-muted-foreground">
              <PullStateWord pull={p} />
              <span className="inline-flex min-w-0 items-center gap-1">
                <ForgeFace login={p.author} provider="github" size="xs" />
                <span className="font-medium text-foreground">{p.author}</span>
              </span>
              <span>{state === "merged" ? "merged" : "wants to merge"}</span>
              <BranchChip branch={p.head} className="max-w-[14rem]" />
              <span>into</span>
              <BranchChip branch={p.base} className="max-w-[10rem]" />
              {p.fork && <Tag tone="warning">fork</Tag>}
            </div>

            <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1.5">
              <ChecksMark checks={p.checks} label />
              <ReviewMark review={p.review} />
              {p.mergeable === "conflicting" && (
                <Status tone="danger" className="text-hint" label="has conflicts" />
              )}
              {p.mergeable === "mergeable" && open && !p.draft && (
                <Status tone="running" className="text-hint" label="no conflicts" />
              )}
              <DiffStat files={p.files} additions={p.additions} deletions={p.deletions} />
              {p.labels?.map((label) => (
                <Tag key={label}>{label}</Tag>
              ))}
            </div>

            <div className="flex flex-wrap items-center gap-2">
              {ctx.canControl && open && (
                <Button
                  size="sm"
                  disabled={!!ctx.busy || mergeBlocked}
                  onClick={() => setMerging(true)}
                  title={
                    p.draft
                      ? "A draft cannot be merged"
                      : p.mergeable === "conflicting"
                        ? "Resolve the conflicts first"
                        : undefined
                  }
                >
                  <SourceMerge className="size-4" />
                  Merge
                </Button>
              )}
              {ctx.canControl && open && (
                <Button
                  size="sm"
                  variant="outline"
                  disabled={!!ctx.busy}
                  onClick={() =>
                    void ctx
                      .run(`Checked out ${p.head}`, () =>
                        post<GitResult>(`/git/github/pulls/${number}/checkout`, undefined, {
                          query: q,
                        }).then(() => ({
                          command: "gh pr checkout",
                          output: `On ${p.head}`,
                          ok: true,
                        })),
                      )
                      .catch(() => undefined)
                  }
                >
                  <SourceBranch className="size-4" />
                  Check out
                </Button>
              )}
              <Button size="sm" variant="ghost" asChild>
                <a href={p.url} target="_blank" rel="noreferrer">
                  <External className="size-4" />
                  Open on GitHub
                </a>
              </Button>
            </div>

            <CommentCard
              author={p.author}
              action="opened this pull request"
              at={p.createdAt}
              body={p.body}
              empty="No description provided."
              repoUrl={repoUrl}
            />
          </div>
        )}

        {p?.headSha && <ChecksSection checks={checks} />}

        {p && (
          <PullReview
            key={p.number}
            pull={p}
            ctx={ctx}
            repoUrl={repoUrl}
            onChanged={pull.refresh}
          />
        )}
      </div>
      {p && (
        <MergePullDialog
          open={merging}
          onOpenChange={setMerging}
          repoPath={ctx.repoPath}
          pull={p}
          headSha={p.headSha}
          onMerged={() => {
            pull.refresh()
            ctx.onChanged()
          }}
        />
      )}
    </div>
  )
}

/**
 * The size of a change as GitHub prints it: the file count, the lines added
 * and taken away in the git hues, and five squares shared between the two.
 */
function DiffStat({
  files,
  additions = 0,
  deletions = 0,
}: {
  files?: number
  additions?: number
  deletions?: number
}) {
  const total = additions + deletions
  const added = total > 0 ? Math.round((additions / total) * 5) : 0
  const removed = total > 0 ? 5 - added : 0
  return (
    <span className="numeric inline-flex items-center gap-1.5 text-hint text-muted-foreground">
      {plural(files ?? 0, "file")}
      <span className="font-mono">
        <span className="text-(--git-added)">+{additions.toLocaleString()}</span>{" "}
        <span className="text-(--git-deleted)">−{deletions.toLocaleString()}</span>
      </span>
      <span aria-hidden className="inline-flex gap-px">
        {Array.from({ length: 5 }, (_, i) => (
          <span
            key={i}
            className="size-2 rounded-[2px]"
            style={{
              background:
                i < added
                  ? "var(--git-added)"
                  : i < added + removed
                    ? "var(--git-deleted)"
                    : "var(--meter-track)",
            }}
          />
        ))}
      </span>
    </span>
  )
}

/** One check run's mark and word: GitHub's status/conclusion pair folded to a reading. */
function checkMark(check: GitHubCheckRun): {
  Glyph: typeof CheckCircle
  tone: string
  word: string
} {
  if (check.status !== "completed") {
    return { Glyph: Clock, tone: "text-warning", word: check.status.replace("_", " ") }
  }
  switch (check.conclusion) {
    case "success":
      return { Glyph: CheckCircle, tone: "text-success", word: "passed" }
    case "failure":
    case "timed_out":
    case "action_required":
    case "startup_failure":
      return {
        Glyph: CrossCircle,
        tone: "text-destructive",
        word: check.conclusion.replace("_", " "),
      }
    default:
      return {
        Glyph: Slash,
        tone: "text-muted-foreground",
        word: check.conclusion || "completed",
      }
  }
}

/**
 * The checks on the head commit, failures first — the one a reader opens
 * this list for — each with its mark, its name and the app that ran it.
 */
function ChecksSection({
  checks,
}: {
  checks: { data?: GitHubCheckRun[]; error?: unknown; loading: boolean }
}) {
  const rows = [...(checks.data ?? [])].sort(
    (a, b) => rank(checkMark(a).tone) - rank(checkMark(b).tone),
  )
  const failed = rows.filter((c) => checkMark(c).tone === "text-destructive").length
  const passed = rows.filter((c) => checkMark(c).tone === "text-success").length
  return (
    <section className="border-t border-hairline">
      <div className="flex h-8 items-center gap-2 px-3">
        <span className="eyebrow">Checks</span>
        {checks.data && (
          <span className="numeric flex items-center gap-2 text-hint text-muted-foreground">
            {passed > 0 && <span className="text-success">{passed} passed</span>}
            {failed > 0 && <span className="text-destructive">{failed} failed</span>}
            {rows.length - passed - failed > 0 && (
              <span>{rows.length - passed - failed} other</span>
            )}
          </span>
        )}
      </div>
      {checks.error != null && (
        <p className="px-3 pb-2 text-hint text-muted-foreground">
          Could not list checks: {errorMessage(checks.error)}
        </p>
      )}
      {checks.loading && !checks.data && <LoadingRows className="px-3 pb-2" rows={2} />}
      {checks.data && rows.length === 0 && (
        <p className="px-3 pb-3 text-hint text-muted-foreground">No checks on this commit.</p>
      )}
      {rows.length > 0 && (
        <ul className="divide-y divide-hairline border-t border-hairline">
          {rows.map((check, i) => {
            const mark = checkMark(check)
            return (
              <li
                key={`${check.app ?? ""}:${check.name}:${i}`}
                className="group flex min-w-0 items-center gap-2 px-3 py-1.5"
              >
                <mark.Glyph
                  aria-hidden
                  className={cn(
                    "size-4 shrink-0",
                    mark.tone,
                    check.status !== "completed" && "animate-pulse",
                  )}
                />
                <span className="min-w-0 flex-1 truncate text-xs">{check.name}</span>
                <span className={cn("shrink-0 text-micro", mark.tone)}>{mark.word}</span>
                {check.app && (
                  <span className="max-w-[8rem] shrink-0 truncate text-micro text-muted-foreground">
                    {check.app}
                  </span>
                )}
                {check.url && (
                  <IconAction
                    label={`Open ${check.name}`}
                    reveal
                    onClick={() => window.open(check.url, "_blank", "noopener")}
                  >
                    <External />
                  </IconAction>
                )}
              </li>
            )
          })}
        </ul>
      )}
    </section>
  )
}

function rank(tone: string): number {
  return tone === "text-destructive"
    ? 0
    : tone === "text-warning"
      ? 1
      : tone === "text-success"
        ? 3
        : 2
}
