"use client"

import { cn } from "@/lib/utils"

/**
 * A unified diff, coloured the usual way: additions green, removals red, hunk
 * headers in the accent.
 *
 * One renderer for every place a diff is shown — the git page, the terminal's
 * companion panel — so the colouring and the line handling stay identical
 * wherever a diff turns up rather than drifting into three near-copies.
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
export function DiffView({
  body,
  className,
  /** The diff is of one known file, named elsewhere — drop its heading too. */
  singleFile,
}: {
  body: string
  className?: string
  singleFile?: boolean
}) {
  return (
    <pre
      className={cn("overflow-auto p-3 font-mono text-hint leading-relaxed sm:text-xs", className)}
    >
      {rows(body, singleFile).map((row, i) =>
        row.heading ? (
          <div
            key={i}
            className="mt-3 mb-1 truncate border-b border-hairline pb-0.5 font-medium text-foreground first:mt-0"
          >
            {row.text}
          </div>
        ) : (
          <div key={i} className={cn("whitespace-pre", row.cls)}>
            {row.text || " "}
          </div>
        ),
      )}
    </pre>
  )
}

type Row = { text: string; cls?: string; heading?: boolean }

/**
 * The diff, reduced to what is worth drawing.
 *
 * A pass over the whole body rather than a decision per line, because whether
 * a line is plumbing depends on *where* it is: `--- ` and `+++ ` are header
 * lines between `diff --git` and the first hunk, and are a removed and an
 * added line everywhere else. A deleted SQL comment reads `--- a note`, and
 * dropping it for looking like a header would quietly take content out of the
 * diff. So the header is a state, and the first `@@` ends it.
 */
function rows(body: string, singleFile?: boolean): Row[] {
  const lines = body.split("\n")
  // A file heading is only worth drawing where a reader could lose track of
  // which file they are in.
  const showHeadings = !singleFile && lines.filter((l) => l.startsWith("diff --git ")).length > 1

  const out: Row[] = []
  let inHeader = false
  for (const line of lines) {
    if (line.startsWith("diff --git ")) {
      inHeader = true
      if (showHeadings) out.push({ text: pathOf(line), heading: true })
      continue
    }
    if (line.startsWith("@@")) {
      inHeader = false
      out.push({ text: line, cls: "text-primary" })
      continue
    }
    if (inHeader) {
      if (isPlumbing(line)) continue
      // A rename is the one header line that is news rather than plumbing.
      if (line.startsWith("rename ")) {
        out.push({ text: line, cls: "text-(--git-renamed)" })
        continue
      }
    }
    if (line.startsWith("+")) out.push({ text: line, cls: "text-(--git-added)" })
    else if (line.startsWith("-")) out.push({ text: line, cls: "text-(--git-deleted)" })
    else out.push({ text: line })
  }
  return out
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
