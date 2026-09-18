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
