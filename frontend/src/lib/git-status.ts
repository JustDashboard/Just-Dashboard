import type { CSSProperties } from "react"
import type { GitFileChange } from "@/lib/types"

/**
 * What a changed file looks like, in one place.
 *
 * The letter, the colour and the sentence are the same three facts in the file
 * tree, the terminal's git tab and the Git page, and they were being derived
 * separately in each — which is how "M" ended up amber in one list and grey in
 * another for the same file. A status is also the one label a reader arrives
 * already knowing: green added, red deleted, amber modified. Anything that
 * only marks *whether* a file changed throws that away and makes them read the
 * letter.
 *
 * A file can be changed on both sides at once — staged, then edited again —
 * and git reports it as one entry with two letters. The change lists draw
 * such a file twice, once under each heading, and pass the `side` they are
 * drawing so the letter, the tone and the sentence describe *that* side: the
 * index's for "ready to commit", the working tree's for "changes". Callers
 * with no side (the file tree, which marks the file once) get the side that
 * is most recently touched — the working tree where it has anything, the
 * index otherwise.
 *
 * The colours are the `--git-*` tokens; every surface here is color-mix
 * against the card, so one set of hues holds on a near-black and a near-white
 * palette alike.
 */
export type GitTone = "added" | "modified" | "deleted" | "untracked" | "renamed" | "conflict"

export type GitSide = "staged" | "unstaged"

const LABELS: Record<string, string> = {
  M: "modified",
  T: "modified",
  A: "added",
  D: "deleted",
  R: "renamed",
  C: "copied",
  U: "conflicted",
  "?": "untracked",
}

function conflicted(change: GitFileChange): boolean {
  return change.label === "conflicted" || change.index === "U" || change.worktree === "U"
}

/** The status letter git itself prints for one side of the change. */
function letterFor(change: GitFileChange, side?: GitSide): string {
  if (change.label === "untracked") return "?"
  if (side === "staged") return change.index || "M"
  if (side === "unstaged") return change.worktree || change.index || "M"
  return change.worktree || change.index || "?"
}

/** The status as a word, for one side of the change. */
export function sideLabel(change: GitFileChange, side?: GitSide): string {
  if (conflicted(change)) return "conflicted"
  if (!side) return change.label
  return LABELS[letterFor(change, side)] ?? change.label
}

/**
 * A file's tone from its status.
 *
 * Conflict wins over everything: a `UU` file is "modified" by the label and is
 * the one thing in the list that will not commit.
 */
export function gitTone(change: GitFileChange, side?: GitSide): GitTone {
  if (conflicted(change)) return "conflict"
  switch (sideLabel(change, side)) {
    case "added":
      return "added"
    case "deleted":
      return "deleted"
    case "untracked":
      return "untracked"
    case "renamed":
    case "copied":
      return "renamed"
    default:
      return "modified"
  }
}

/** The one-letter mark git itself uses, so the two agree. */
export function gitLetter(change: GitFileChange, side?: GitSide): string {
  if (change.label === "untracked") return "U"
  if (conflicted(change)) return "!"
  return letterFor(change, side).toUpperCase()
}

/** The status as a sentence, for a tooltip. */
export function describeChange(change: GitFileChange, side?: GitSide): string {
  if (conflicted(change)) return "conflicted — resolve before committing"
  const label = sideLabel(change, side)
  const where = (side ?? (change.staged && !change.unstaged ? "staged" : "unstaged")) === "staged"
  const renamed = change.from ? ` from ${change.from}` : ""
  return `${label}${renamed} · ${where ? "staged" : "not staged"}`
}

/**
 * The colour, plus the tint drawn behind the row.
 *
 * Returned as a style object rather than classes because the value is a token
 * chosen at runtime; Tailwind cannot generate a class per status without the
 * six of them being written out somewhere to be scanned, which is the same
 * table twice.
 */
export function gitStyle(tone: GitTone): CSSProperties {
  const colour = `var(--git-${tone})`
  return {
    // The row tint is deliberately faint: a list where every second line is a
    // coloured band is harder to read than one with none.
    "--git-colour": colour,
    "--git-tint": `color-mix(in oklab, ${colour} 10%, transparent)`,
    "--git-edge": `color-mix(in oklab, ${colour} 55%, transparent)`,
  } as CSSProperties
}

/**
 * The shape of a working tree, as the five squares a git client draws for it.
 *
 * A repository row answers "is anything waiting here" with a count, and a
 * count of 28 says nothing about *what* 28 — one conflict among twenty-seven
 * tidy edits is a different morning from twenty-eight new files. The bar is
 * the composition at a glance, in the same hues the file lists already use,
 * and the count beside it stays the precise answer.
 *
 * Five squares because that is the number a diffstat has had since GitHub drew
 * the first one: enough to show a majority and a minority, few enough to read
 * without counting. The allocation gives every category that has a file in it
 * one square before proportion is considered — a bar that rounds the single
 * conflict away has hidden the only thing on the row worth seeing — and then
 * hands the rest to whichever category is furthest under its quota.
 */
export type WorkingTreePart = "conflict" | "staged" | "modified" | "untracked"

const SQUARES = 5

/** Severity order: a bar is read left to right, and the conflict comes first. */
const PARTS: WorkingTreePart[] = ["conflict", "staged", "modified", "untracked"]

export type WorkingTreeCounts = Record<WorkingTreePart, number>

/**
 * What git's per-repository summary implies about each category.
 *
 * `changes` is the total; staged, untracked and conflicted are counted
 * separately, so what is left is the ordinary unstaged edit — which the
 * summary never names and which is usually most of it.
 *
 * The four are disjoint because `gitx.Summary` counts one entry per file and
 * an untracked or conflicted entry is never also counted as staged. What that
 * makes the bar say about a file staged *and* edited again — one entry, on the
 * index side — is that it is staged, which is what the index says and what the
 * changes list's own "Ready to commit" heading says about it too.
 */
export function workingTreeCounts(repo: {
  changes: number
  staged: number
  untracked: number
  conflicts: number
}): WorkingTreeCounts {
  const named = repo.staged + repo.untracked + repo.conflicts
  return {
    conflict: Math.max(0, repo.conflicts),
    staged: Math.max(0, repo.staged),
    untracked: Math.max(0, repo.untracked),
    modified: Math.max(0, repo.changes - named),
  }
}

/** The five squares, in the order they are drawn. Empty for a clean tree. */
export function workingTreeSquares(counts: WorkingTreeCounts): WorkingTreePart[] {
  const total = PARTS.reduce((n, part) => n + counts[part], 0)
  if (total <= 0) return []

  const present = PARTS.filter((part) => counts[part] > 0)
  // More categories than squares can only happen if a fifth is ever added;
  // the severity order decides which ones survive.
  if (present.length >= SQUARES) return present.slice(0, SQUARES)

  const given = new Map<WorkingTreePart, number>(present.map((part) => [part, 1]))
  const quota = (part: WorkingTreePart) => (counts[part] / total) * SQUARES
  for (let left = SQUARES - present.length; left > 0; left--) {
    const next = present.reduce((a, b) =>
      quota(b) - given.get(b)! > quota(a) - given.get(a)! ? b : a,
    )
    given.set(next, given.get(next)! + 1)
  }
  return PARTS.flatMap((part) => Array<WorkingTreePart>(given.get(part) ?? 0).fill(part))
}
