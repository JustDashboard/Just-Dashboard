"use client"

import { ChatBubble, CheckCircle, Clock, CrossCircle } from "@/components/icons"
import type { GitPullRequest } from "@/lib/types"
import { cn } from "@/lib/utils"
import { SourceMerge, SourcePull } from "@/components/git/glyphs"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"

/**
 * A pull request's state, drawn the way its forge draws it.
 *
 * A list of requests was a list of titles with "#148 · branch → branch"
 * under each in the same grey, and whether one was open, merged or closed
 * was a word to find. On GitHub the state is the glyph in front of the
 * title — green while open, violet once merged, red when closed without it,
 * grey as a draft — and that is the index a reader already has. The hues are
 * `--pull-*` (`globals.css` says why they are not the status hues).
 */

export type PullState = "open" | "draft" | "merged" | "closed"

export function pullStateOf(pull: Pick<GitPullRequest, "state" | "draft" | "merged">): PullState {
  const state = pull.state.toLowerCase()
  if (state === "merged" || pull.merged) return "merged"
  if (state === "closed") return "closed"
  return pull.draft ? "draft" : "open"
}

export const PULL_STATE_WORD: Record<PullState, string> = {
  open: "open",
  draft: "draft",
  merged: "merged",
  closed: "closed",
}

export const PULL_STATE_COLOUR: Record<PullState, string> = {
  open: "var(--pull-open)",
  draft: "var(--pull-draft)",
  merged: "var(--pull-merged)",
  closed: "var(--pull-closed)",
}

/** The glyph in front of a request's title, in its state's hue. */
export function PullStateMark({
  pull,
  className,
}: {
  pull: Pick<GitPullRequest, "state" | "draft" | "merged">
  className?: string
}) {
  const state = pullStateOf(pull)
  const Glyph = state === "merged" ? SourceMerge : SourcePull
  return (
    <Glyph
      role="img"
      aria-label={PULL_STATE_WORD[state]}
      className={cn("size-4 shrink-0", className)}
      style={{ color: PULL_STATE_COLOUR[state] }}
    />
  )
}

/** The state as its glyph and its word, for a header that has room to say it. */
export function PullStateWord({
  pull,
}: {
  pull: Pick<GitPullRequest, "state" | "draft" | "merged">
}) {
  const state = pullStateOf(pull)
  return (
    <span
      className="inline-flex items-center gap-1 text-xs font-medium capitalize"
      style={{ color: PULL_STATE_COLOUR[state] }}
    >
      <PullStateMark pull={pull} className="size-3.5" />
      {PULL_STATE_WORD[state]}
    </span>
  )
}

const CHECKS: Record<string, { word: string; Glyph: typeof CheckCircle; className: string }> = {
  success: { word: "checks passed", Glyph: CheckCircle, className: "text-success" },
  failure: { word: "checks failed", Glyph: CrossCircle, className: "text-destructive" },
  pending: { word: "checks running", Glyph: Clock, className: "text-warning" },
}

/**
 * How the checks on a request's head stand: a tick, a cross or a dot in the
 * status hue, the shape GitHub prints beside the title. `label` adds the
 * word, for a row with room for it.
 */
export function ChecksMark({
  checks,
  label,
  className,
}: {
  checks?: string
  label?: boolean
  className?: string
}) {
  const reading = checks ? CHECKS[checks] : undefined
  if (!reading) return null
  const { Glyph } = reading
  const mark = (
    <span className={cn("inline-flex shrink-0 items-center gap-1", reading.className, className)}>
      <Glyph aria-hidden className={cn("size-3.5", checks === "pending" && "animate-pulse")} />
      {label ? (
        <span className="text-hint">{reading.word}</span>
      ) : (
        <span className="sr-only">{reading.word}</span>
      )}
    </span>
  )
  if (label) return mark
  return (
    <Tooltip>
      <TooltipTrigger asChild>{mark}</TooltipTrigger>
      <TooltipContent>{reading.word}</TooltipContent>
    </Tooltip>
  )
}

/** What reviewers said, when they said something decisive. */
export function ReviewMark({ review }: { review?: string }) {
  if (review === "approved") {
    return (
      <span className="inline-flex shrink-0 items-center gap-1 text-hint text-success">
        <CheckCircle aria-hidden className="size-3.5" />
        approved
      </span>
    )
  }
  if (review === "changes_requested") {
    return (
      <span className="inline-flex shrink-0 items-center gap-1 text-hint text-destructive">
        <CrossCircle aria-hidden className="size-3.5" />
        changes requested
      </span>
    )
  }
  if (review === "review_required") {
    return <span className="shrink-0 text-hint text-muted-foreground">review required</span>
  }
  return null
}

/** How many comments a request or an issue has, the way a forge counts them in a list. */
export function CommentCount({ count }: { count: number }) {
  if (!count) return null
  return (
    <span
      className="numeric inline-flex shrink-0 items-center gap-0.5 text-hint text-muted-foreground"
      aria-label={`${count} comment${count === 1 ? "" : "s"}`}
    >
      <ChatBubble aria-hidden className="size-3.5" />
      {count}
    </span>
  )
}
