"use client"

import { relativeTime } from "@/lib/format"
import { workingTreeCounts, workingTreeSquares, type WorkingTreePart } from "@/lib/git-status"
import type { GitRepo } from "@/lib/types"
import { cn } from "@/lib/utils"
import { SourceBranch, SourceCommit } from "@/components/git/glyphs"
import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { githubAvatarUrl } from "@/hooks/use-github"
import { InitialsMark } from "@/components/account/user-avatar"

/**
 * The four marks a git surface repeats everywhere, drawn once.
 *
 * Each of these existed as three or four hand-laid spans across the repository
 * list, the workspace strip, the history list and the terminal's git tab, and
 * each had drifted: the branch was mono in one place and sans in another, the
 * short sha was `text-hint` here and `text-micro` there, and the working tree
 * was a sentence in the table and a number in the header. They are the same
 * four facts every time — which branch, how the tree stands, which commit, who
 * — so they are one component each.
 *
 * None of them is a pill: a branch and a sha are literal strings from the
 * repository and take `Tag mono`'s recessed ground (§4), the working tree is a
 * bar of squares, and the monogram is a square with a corner radius well under
 * half its height. The shape this product reserves for nothing at all stays
 * reserved.
 */

/** Which `--git-*` hue each square of the bar takes. */
const PART_COLOUR: Record<WorkingTreePart, string> = {
  conflict: "var(--git-conflict)",
  staged: "var(--git-added)",
  modified: "var(--git-modified)",
  untracked: "var(--git-untracked)",
}

const PART_WORD: Record<WorkingTreePart, string> = {
  conflict: "conflicted",
  staged: "staged",
  modified: "modified",
  untracked: "new",
}

/**
 * Where HEAD is, as a word.
 *
 * The server names a detached HEAD "detached at <sha>" so that a bare listing
 * of branches reads correctly on its own. Everywhere it is drawn here the tag
 * beside it already says "detached", so what is wanted is the position — and
 * the two surfaces that draw it, the list card and the workspace strip, have
 * to agree.
 */
export function branchLabel(branch?: string, detached?: boolean): string {
  return (detached ? (branch ?? "").replace(/^detached at /, "") : branch) || "—"
}

/**
 * The branch a checkout is on.
 *
 * The glyph is MDI's fork rather than Heroicons' share mark, because this is
 * the one screen where a reader identifies the thing *by* that drawing — see
 * `glyphs.tsx`. Detached HEAD colours the whole chip, since a detached
 * checkout is the state where the next commit goes nowhere.
 */
export function BranchChip({
  branch,
  detached,
  title,
  className,
}: {
  branch?: string
  detached?: boolean
  title?: string
  className?: string
}) {
  const label = branchLabel(branch, detached)
  return (
    <span
      title={title}
      className={cn(
        "inline-flex min-w-0 items-center gap-1 rounded-sm bg-surface-sunken px-1.5 py-px font-mono text-micro leading-[1.6]",
        detached ? "text-destructive" : "text-foreground/85",
        className,
      )}
    >
      <SourceBranch aria-hidden className="size-3 shrink-0 opacity-70" />
      <span className="truncate">{label}</span>
    </span>
  )
}

/**
 * The shape of a working tree as five squares, the way a diffstat is drawn.
 *
 * Renders nothing for a clean checkout: five empty squares on every tidy row
 * is a column of furniture, and "clean" is already said in words beside it.
 */
export function WorkingTreeBar({ repo, className }: { repo: GitRepo; className?: string }) {
  const counts = workingTreeCounts(repo)
  const squares = workingTreeSquares(counts)
  if (squares.length === 0) return null

  const sentence = (Object.keys(PART_WORD) as WorkingTreePart[])
    .filter((part) => counts[part] > 0)
    .map((part) => `${counts[part]} ${PART_WORD[part]}`)
    .join(" · ")

  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <span
          role="img"
          aria-label={sentence}
          className={cn("inline-flex shrink-0 items-center gap-px", className)}
        >
          {squares.map((part, i) => (
            <span
              key={i}
              aria-hidden
              className="size-2 rounded-[2px]"
              style={{ background: PART_COLOUR[part] }}
            />
          ))}
        </span>
      </TooltipTrigger>
      <TooltipContent>{sentence}</TooltipContent>
    </Tooltip>
  )
}

/**
 * A commit, as the handful of characters everyone actually quotes.
 *
 * Abbreviated only if it arrives full: `/git/` already reports `rev-parse
 * --short`, which is seven characters in a small repository and more in one
 * where seven would be ambiguous — truncating *that* would print a prefix
 * that resolves to nothing.
 */
export function ShortSha({ sha, className }: { sha?: string; className?: string }) {
  if (!sha) return null
  return (
    <span
      className={cn(
        "inline-flex shrink-0 items-center gap-1 rounded-sm bg-surface-sunken px-1 py-px font-mono text-micro leading-[1.6] text-muted-foreground",
        className,
      )}
    >
      <SourceCommit aria-hidden className="size-3 shrink-0 opacity-70" />
      {sha.length > 12 ? sha.slice(0, 7) : sha}
    </span>
  )
}

/**
 * Who made a commit, as the person they are everywhere else in the product.
 *
 * There is no avatar to fetch — these are checkouts on a host, not rows from
 * an API — so it is the account face without a picture (`InitialsMark`): the
 * initial on the tinted wash of the hue the name is given in the rail, a run's
 * actor and a variable's author. It was a solid square on a palette of its
 * own, which drew "mira" in one colour as the author of a commit and another
 * as the person who deployed it, in the same row (§14). It still earns its
 * pixels the way the language mark does in the deploy chooser: a column of
 * commits where mine and the bot's are two colours is scanned, and one where
 * they are the same grey is read.
 */
export function AuthorMark({ name, className }: { name?: string; className?: string }) {
  const who = (name ?? "").trim()
  if (!who) return null
  return <InitialsMark name={who} size="xs" className={className} />
}

/**
 * The two places a face goes: inside a line of text, where `AuthorMark` sits
 * in a commit line, and at a row's head, the size of `ProductLogo`'s small
 * tile.
 */
const FACE_BOX = { xs: "size-4 rounded-sm", sm: "size-8 rounded-md" } as const

/**
 * Who opened a pull request or pushed a revision, as the face their forge
 * shows for them.
 *
 * GitHub's picture is fetched through the dashboard (`/git/github/avatar`),
 * which is the one forge the server can ask; every other forge, and a GitHub
 * login with no picture to find, is the account face's initials on its hue —
 * the same mark the commit lines and a run's actor draw, so a login looks
 * alike wherever the picture is missing. Square either way: a face in a
 * circle is the pill §4 deleted.
 */
export function ForgeFace({
  login,
  provider,
  size = "sm",
  className,
}: {
  login?: string
  /** The forge the login belongs to, as a trigger names it: `github`, `gitlab`… */
  provider?: string
  size?: keyof typeof FACE_BOX
  className?: string
}) {
  const who = (login ?? "").trim()
  if (!who) return null
  if (provider !== "github") {
    return <InitialsMark name={who} size={size} className={cn(FACE_BOX[size], className)} />
  }
  return (
    <Avatar className={cn(FACE_BOX[size], className)}>
      <AvatarImage src={githubAvatarUrl(who)} alt="" />
      {/* The mark shows while the picture is on its way and stays if there
          is none; the root's radius clips it, so it draws square-edged. */}
      <AvatarFallback className="rounded-none bg-transparent">
        <InitialsMark name={who} size={size} className="size-full rounded-none" />
      </AvatarFallback>
    </Avatar>
  )
}

/**
 * The last thing that happened in a repository, on one line: who, what, when.
 *
 * The order is a forge's — mark, subject, then the attribution trailing off in
 * muted text — rather than a table's, because the subject is the part being
 * scanned and anything in front of it is a delay.
 */
export function CommitLine({
  sha,
  subject,
  author,
  at,
  empty,
  className,
}: {
  sha?: string
  subject?: string
  author?: string
  at?: string
  /** No commits yet — a fresh `git init`. */
  empty?: boolean
  className?: string
}) {
  if (empty || !subject) {
    return (
      <p className={cn("truncate text-hint text-muted-foreground italic", className)}>
        {empty ? "no commits yet" : "—"}
      </p>
    )
  }
  return (
    <p className={cn("flex min-w-0 items-center gap-1.5", className)}>
      <AuthorMark name={author} />
      <ShortSha sha={sha} />
      {/* The subject takes the slack, so a wide row spends it on the message
          and the attribution keeps a column of its own at the right. */}
      <span className="min-w-0 flex-1 truncate text-hint text-foreground/80" title={subject}>
        {subject}
      </span>
      {/* On a phone the subject is what the line is for: the name drops and
          the stamp stays, rather than both surviving as two characters
          each. */}
      <span className="shrink-0 truncate text-hint text-muted-foreground">
        <span className="hidden sm:inline">{author}</span>
        {at ? <span className="hidden sm:inline"> · </span> : null}
        {at ? relativeTime(at) : null}
      </span>
    </p>
  )
}
