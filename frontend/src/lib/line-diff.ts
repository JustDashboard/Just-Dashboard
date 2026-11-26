import type { DiffLine } from "@/lib/types"

/**
 * Two versions of a text as the lines that changed between them, for every
 * area that compares a file with the one it replaces: a stack's compose file,
 * a drift repair's owned file. Docker kept the helpers beside its stack views
 * and Network's drift review imported them from there.
 *
 * Pure, so the reading is tested apart from the views that draw it.
 */

const CONTEXT = 3

/**
 * The section a line sits under, kept while the lines are walked: a compose
 * file's service, for instance. Given the section so far and the next line,
 * it answers the section from that line on.
 */
export type SectionOf = (current: string, line: string) => string

/** Marks a last line that has no newline after it; no line split on newlines can hold one. */
const UNTERMINATED = "\n"

/**
 * A text's lines exactly as a file holds them: an empty file is none, the
 * newline that ends the last line is not one more, and a last line without
 * that newline is told apart from the same line with it.
 */
function exactLines(text: string): string[] {
  if (text === "") return []
  const lines = text.split("\n")
  if (lines[lines.length - 1] === "") lines.pop()
  else lines[lines.length - 1] += UNTERMINATED
  return lines
}

/** A text's lines for reading: whitespace at its end is not a change worth a line. */
function readingLines(text: string): string[] {
  return text.replace(/\s+$/, "").split("\n")
}

/**
 * The lines that changed between `before` and `after`, with three unchanged
 * lines around each change and a gap where more were left out — the shape the
 * server's preview diff has, so the same view draws both. With `sectionOf`
 * every line carries the section it sits under; a removed line names its own
 * section without moving the running one, since the file after it has no
 * such line.
 *
 * By default whitespace at the end of either text is no change, which suits a
 * compose file read for what it declares. `exact` compares what will be
 * written byte for byte, for a change shown as the one to review before it is
 * applied: an absent or empty file is no lines, so a new file is only added
 * lines, and a trailing space or a final newline is a change like any other.
 */
export function lineDiff(
  before: string,
  after: string,
  { sectionOf, exact = false }: { sectionOf?: SectionOf; exact?: boolean } = {},
): DiffLine[] {
  const split = exact ? exactLines : readingLines
  const left = split(before)
  const right = split(after)
  const n = left.length
  const m = right.length
  // The longest common subsequence from each pair of positions to the end.
  const lcs = Array.from({ length: n + 1 }, () => new Int32Array(m + 1))
  for (let i = n - 1; i >= 0; i--) {
    for (let j = m - 1; j >= 0; j--) {
      lcs[i][j] =
        left[i] === right[j] ? lcs[i + 1][j + 1] + 1 : Math.max(lcs[i + 1][j], lcs[i][j + 1])
    }
  }
  const track = sectionOf ?? (() => "")
  const line = (kind: DiffLine["kind"], raw: string, section: string): DiffLine => {
    const noNewline = raw.endsWith(UNTERMINATED)
    const text = noNewline ? raw.slice(0, -1) : raw
    const entry: DiffLine = sectionOf ? { kind, text, section } : { kind, text }
    if (noNewline) entry.noNewline = true
    return entry
  }
  const lines: DiffLine[] = []
  let section = ""
  let i = 0
  let j = 0
  while (i < n && j < m) {
    if (left[i] === right[j]) {
      section = track(section, left[i])
      lines.push(line("same", left[i], section))
      i++
      j++
    } else if (lcs[i + 1][j] >= lcs[i][j + 1]) {
      lines.push(line("removed", left[i], track(section, left[i])))
      i++
    } else {
      section = track(section, right[j])
      lines.push(line("added", right[j], section))
      j++
    }
  }
  for (; i < n; i++) lines.push(line("removed", left[i], track(section, left[i])))
  for (; j < m; j++) {
    section = track(section, right[j])
    lines.push(line("added", right[j], section))
  }

  const keep = lines.map(() => false)
  lines.forEach((entry, k) => {
    if (entry.kind === "same") return
    for (let c = Math.max(0, k - CONTEXT); c <= Math.min(lines.length - 1, k + CONTEXT); c++) {
      keep[c] = true
    }
  })
  const out: DiffLine[] = []
  let gap = false
  lines.forEach((entry, k) => {
    if (!keep[k]) {
      gap = out.length > 0
      return
    }
    if (gap) out.push({ kind: "gap", text: "…" })
    gap = false
    out.push(entry)
  })
  return out
}

/** How many lines a diff adds and removes. */
export function diffCounts(lines: DiffLine[]) {
  return {
    added: lines.filter((l) => l.kind === "added").length,
    removed: lines.filter((l) => l.kind === "removed").length,
  }
}

/**
 * The lines as a unified diff's body (`+`, `-` and a space before each), so
 * `DiffView` (`components/diff-view.tsx`) draws them the way it draws
 * every other diff. A gap is a context line holding the ellipsis, and a last
 * line with no newline after it is followed by git's note saying so.
 */
export function unifiedBody(lines: DiffLine[]): string {
  return lines
    .flatMap((l) => {
      const text = (l.kind === "added" ? "+" : l.kind === "removed" ? "-" : " ") + l.text
      return l.noNewline ? [text, "\\ No newline at end of file"] : [text]
    })
    .join("\n")
}
