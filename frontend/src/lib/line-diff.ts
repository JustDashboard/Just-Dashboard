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

/**
 * The lines that changed between `before` and `after`, with three unchanged
 * lines around each change and a gap where more were left out — the shape the
 * server's preview diff has, so the same view draws both. With `sectionOf`
 * every line carries the section it sits under; a removed line names its own
 * section without moving the running one, since the file after it has no
 * such line.
 */
export function lineDiff(
  before: string,
  after: string,
  { sectionOf }: { sectionOf?: SectionOf } = {},
): DiffLine[] {
  const left = before.replace(/\s+$/, "").split("\n")
  const right = after.replace(/\s+$/, "").split("\n")
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
  const line = (kind: DiffLine["kind"], text: string, section: string): DiffLine =>
    sectionOf ? { kind, text, section } : { kind, text }
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
 * `DiffView` (`components/files/diff-view.tsx`) draws them the way it draws
 * every other diff. A gap is a context line holding the ellipsis.
 */
export function unifiedBody(lines: DiffLine[]): string {
  return lines
    .map((l) => (l.kind === "added" ? "+" : l.kind === "removed" ? "-" : " ") + l.text)
    .join("\n")
}
