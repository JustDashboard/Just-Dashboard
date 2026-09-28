/**
 * A unified diff, reduced to what is worth drawing — and to where each line
 * actually is in the two files.
 *
 * This is the read side of `diff.ts`, which is the write side: that one turns
 * two blobs into a patch, this one turns a patch into rows. It is pure and
 * lives apart from the component so the counting is testable, because the
 * counting is the part that silently goes wrong: a line number off by one on a
 * conflicted merge is worse than no line numbers at all.
 *
 * The rules git's own output demands:
 *
 *   - A hunk header is `@@ -old[,count] +new[,count] @@`, and **the counts are
 *     optional** — a one-line hunk is written `@@ -1 +1 @@`. A parser that
 *     requires the comma silently numbers the whole file from line 1.
 *   - `\ No newline at end of file` is a note about the line above it and
 *     advances neither counter.
 *   - Whether `--- ` and `+++ ` are plumbing depends on *where* they are: they
 *     are header lines between `diff --git` and the first hunk, and a removed
 *     and an added line everywhere else. A deleted SQL comment reads
 *     `--- a note`, and dropping it for looking like a header would quietly
 *     take content out of the diff. So the header is a state, and the first
 *     `@@` ends it.
 *
 * **git's plumbing header is dropped.** Every diff opens with four lines that
 * exist for `git apply` and for nobody reading one:
 *
 * ```
 * diff --git a/path/to/thing.txt b/path/to/thing.txt
 * index 03e0908..7bcfcaa 100644
 * --- a/path/to/thing.txt
 * +++ b/path/to/thing.txt
 * ```
 *
 * They say the path four times — a path already at the top of the panel that
 * opened the diff — plus two blob hashes nobody can do anything with. On a
 * narrow side panel that is the first screenful. What survives is the part
 * that carries information: a rename, and the `diff --git` line itself *when
 * there is more than one file*, redrawn as a heading, because in a commit's
 * diff it is the only thing separating one file from the next.
 */
export type DiffRow = /** A file's name, where one diff holds several. */
(
  | { kind: "heading"; text: string }
  /** `@@ -1,7 +1,9 @@` — where in the file the next run of lines sits. */
  | { kind: "hunk"; text: string }
  /** `rename from` / `rename to`: the one header line that is news. */
  | { kind: "rename"; text: string }
  | { kind: "add"; text: string; newNo: number }
  | { kind: "del"; text: string; oldNo: number }
  | { kind: "context"; text: string; oldNo: number; newNo: number }
  /** Anything with no place in either file — the no-newline note, blank tails. */
  | { kind: "meta"; text: string }
) & { sourceLine?: number }

/** `@@ -12,7 +12,9 @@ func thing()` and `@@ -1 +1 @@` alike. */
const HUNK = /^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@/

export function diffRows(body: string, singleFile?: boolean, sourceLines = false): DiffRow[] {
  const lines = body.split("\n")
  // A file heading is only worth drawing where a reader could lose track of
  // which file they are in.
  const showHeadings = !singleFile && lines.filter((l) => l.startsWith("diff --git ")).length > 1

  const out: DiffRow[] = []
  let inHeader = false
  let oldNo = 0
  let newNo = 0

  for (const [sourceLine, line] of lines.entries()) {
    const push = (row: DiffRow) => out.push(sourceLines ? { ...row, sourceLine } : row)
    if (line.startsWith("diff --git ")) {
      inHeader = true
      // A new file starts both counters over; until its first hunk says
      // where, neither is meaningful.
      oldNo = 0
      newNo = 0
      if (showHeadings) push({ kind: "heading", text: pathOf(line) })
      continue
    }
    const hunk = HUNK.exec(line)
    if (hunk) {
      inHeader = false
      oldNo = Number(hunk[1])
      newNo = Number(hunk[3])
      push({ kind: "hunk", text: line })
      continue
    }
    if (inHeader) {
      if (isPlumbing(line)) continue
      if (line.startsWith("rename ")) {
        push({ kind: "rename", text: line })
        continue
      }
    }
    // A note about the line above, not a line of either file.
    if (line.startsWith("\\")) {
      push({ kind: "meta", text: line })
      continue
    }
    if (line.startsWith("+")) {
      push({ kind: "add", text: line, newNo: newNo++ })
    } else if (line.startsWith("-")) {
      push({ kind: "del", text: line, oldNo: oldNo++ })
    } else if (oldNo > 0 || newNo > 0) {
      push({ kind: "context", text: line, oldNo: oldNo++, newNo: newNo++ })
    } else {
      // Before the first hunk there is nothing to count against — a plain
      // message, or a diff that is not one at all.
      push({ kind: "meta", text: line })
    }
  }
  return out
}

/** The widest line number the rows carry, as a count of digits. */
export function gutterWidth(rows: DiffRow[]): number {
  let widest = 0
  for (const row of rows) {
    if (row.kind === "add") widest = Math.max(widest, row.newNo)
    else if (row.kind === "del") widest = Math.max(widest, row.oldNo)
    else if (row.kind === "context") widest = Math.max(widest, row.oldNo, row.newNo)
  }
  return Math.max(2, String(widest).length)
}

function isPlumbing(line: string): boolean {
  return (
    line.startsWith("index ") ||
    line.startsWith("--- ") ||
    line.startsWith("+++ ") ||
    line.startsWith("new file mode ") ||
    line.startsWith("deleted file mode ") ||
    line.startsWith("old mode ") ||
    line.startsWith("new mode ") ||
    line.startsWith("similarity index ")
  )
}

/**
 * The path out of a `diff --git a/x b/x` line.
 *
 * The `b/` side, because for a rename that is where the file ended up. A path
 * containing a space makes the split ambiguous — git quotes those — so the
 * fallback is the line with only the prefix removed rather than a guess.
 */
function pathOf(line: string): string {
  const rest = line.slice("diff --git ".length)
  const b = rest.lastIndexOf(" b/")
  if (b > 0) return rest.slice(b + 3)
  return rest
}
