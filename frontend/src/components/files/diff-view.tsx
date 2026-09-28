"use client"

import { cn } from "@/lib/utils"
import { diffRows, gutterWidth, type DiffRow } from "@/components/files/diff-rows"
import { Checkbox } from "@/components/ui/checkbox"

/**
 * A unified diff, coloured the usual way: additions green, removals red, hunk
 * headers in the accent.
 *
 * One renderer for every place a diff is shown — the git page, the terminal's
 * companion panel, the file editor's review step — so the colouring and the
 * line handling stay identical wherever a diff turns up rather than drifting
 * into three near-copies. What is drawn and what is dropped is `diff-rows.ts`,
 * which is pure and tested; this file is only the paint.
 *
 * `lineNumbers` adds the two gutters a forge draws — where the line was, and
 * where it is now — and with them the faint row tint that makes a diff
 * skimmable rather than read. It is opt-in rather than the default because the
 * gutters cost about six characters of width, and the terminal's companion
 * column is 256px wide at its narrowest: there the numbers would be bought
 * with the code. The gutters stay put while the code scrolls sideways, which
 * is the whole reason to have them on a long line.
 */
export function DiffView({
  body,
  className,
  lineNumbers,
  /** The diff is of one known file, named elsewhere — drop its heading too. */
  singleFile,
  selection,
}: {
  body: string
  className?: string
  /** Draw the old and new line numbers down the left. */
  lineNumbers?: boolean
  singleFile?: boolean
  selection?: {
    lines: number[]
    selected: number[]
    onChange: (selected: number[]) => void
    disabled?: boolean
  }
}) {
  const rows = diffRows(body, singleFile, !!selection)
  const selected = new Set(selection?.selected)
  const selectable = new Set(selection?.lines)
  const toggle = (lines: number[]) => {
    const next = new Set(selected)
    if (lines.every((line) => next.has(line))) lines.forEach((line) => next.delete(line))
    else lines.forEach((line) => next.add(line))
    selection?.onChange([...next])
  }
  const hunkLines = (index: number) => {
    const lines: number[] = []
    for (let i = index + 1; i < rows.length; i++) {
      if (rows[i].kind === "hunk" || rows[i].kind === "heading") break
      const line = rows[i].sourceLine
      if (line !== undefined && selectable.has(line)) lines.push(line)
    }
    return lines
  }

  if (!lineNumbers) {
    return (
      <pre
        className={cn(
          "overflow-auto p-3 font-mono text-hint leading-relaxed sm:text-xs",
          className,
        )}
      >
        {rows.map((row, i) =>
          row.kind === "heading" ? (
            <div
              key={i}
              className="mt-3 mb-1 truncate border-b border-hairline pb-0.5 font-medium text-foreground first:mt-0"
            >
              {row.text}
            </div>
          ) : (
            <div key={i} className={cn("whitespace-pre", TEXT[row.kind])}>
              {row.text || " "}
            </div>
          ),
        )}
      </pre>
    )
  }

  // One column width for both gutters, sized to the widest number in this
  // diff. `ch` is the monospace cell, so the digits sit in their own column
  // rather than in a padding somebody guessed at.
  const gutter = `calc(${gutterWidth(rows)}ch + 0.9rem)`

  return (
    <pre
      style={{ "--jd-gutter": gutter } as React.CSSProperties}
      className={cn("overflow-auto font-mono text-hint leading-relaxed sm:text-xs", className)}
    >
      {rows.map((row, i) =>
        row.kind === "heading" ? (
          <div
            key={i}
            className="sticky left-0 truncate border-y border-hairline bg-surface-header px-3 py-1 font-medium text-foreground"
          >
            {row.text}
          </div>
        ) : (
          <div
            key={i}
            className={cn(
              "grid w-max min-w-full",
              selection
                ? "grid-cols-[1.75rem_var(--jd-gutter)_var(--jd-gutter)_1fr]"
                : "grid-cols-[var(--jd-gutter)_var(--jd-gutter)_1fr]",
            )}
            style={{ background: ROW_TINT[row.kind] }}
          >
            {selection && (
              <span className="sticky left-0 z-10 flex items-center justify-center bg-card">
                {row.kind === "hunk" && hunkLines(i).length > 0 ? (
                  <Checkbox
                    aria-label={`Select chunk at ${row.text}`}
                    checked={
                      hunkLines(i).every((line) => selected.has(line))
                        ? true
                        : hunkLines(i).some((line) => selected.has(line))
                          ? "indeterminate"
                          : false
                    }
                    disabled={selection.disabled}
                    onCheckedChange={() => toggle(hunkLines(i))}
                  />
                ) : row.sourceLine !== undefined && selectable.has(row.sourceLine) ? (
                  <Checkbox
                    aria-label={`Select ${row.kind === "add" ? "added" : "removed"} line ${"newNo" in row ? row.newNo : "oldNo" in row ? row.oldNo : ""}`}
                    checked={selected.has(row.sourceLine)}
                    disabled={selection.disabled}
                    onCheckedChange={() => toggle([row.sourceLine!])}
                  />
                ) : null}
              </span>
            )}
            <Gutter kind={row.kind} selection={!!selection}>
              {"oldNo" in row ? row.oldNo : ""}
            </Gutter>
            <Gutter kind={row.kind} selection={!!selection} second>
              {"newNo" in row ? row.newNo : ""}
            </Gutter>
            <span className={cn("py-px pr-3 pl-2 whitespace-pre", TEXT[row.kind])}>
              {row.text || " "}
            </span>
          </div>
        ),
      )}
    </pre>
  )
}

/** The ink on a row's text, by what the row is. */
const TEXT: Record<DiffRow["kind"], string | undefined> = {
  heading: "font-medium text-foreground",
  hunk: "text-primary",
  rename: "text-(--git-renamed)",
  add: "text-(--git-added)",
  del: "text-(--git-deleted)",
  context: undefined,
  meta: "text-muted-foreground",
}

/**
 * The wash behind a changed row, faint enough that a file of them is still
 * read as code. A hunk header takes the recessed ground instead, because it is
 * a divider rather than a line of either file.
 */
const ROW_TINT: Record<DiffRow["kind"], string | undefined> = {
  heading: undefined,
  hunk: "var(--surface-sunken)",
  rename: undefined,
  add: "color-mix(in oklab, var(--git-added) 9%, transparent)",
  del: "color-mix(in oklab, var(--git-deleted) 9%, transparent)",
  context: undefined,
  meta: undefined,
}

/**
 * The gutter's own ground has to be opaque, not the row's tint: it stays put
 * while the code scrolls under it, and a translucent column would show the
 * code passing behind the numbers.
 */
const GUTTER_GROUND: Record<DiffRow["kind"], string> = {
  heading: "var(--card)",
  hunk: "var(--surface-sunken)",
  rename: "var(--card)",
  add: "color-mix(in oklab, var(--git-added) 14%, var(--card))",
  del: "color-mix(in oklab, var(--git-deleted) 14%, var(--card))",
  context: "var(--card)",
  meta: "var(--card)",
}

function Gutter({
  kind,
  second,
  selection,
  children,
}: {
  kind: DiffRow["kind"]
  second?: boolean
  selection?: boolean
  children: React.ReactNode
}) {
  return (
    <span
      aria-hidden
      style={{
        background: GUTTER_GROUND[kind],
        left: selection
          ? second
            ? "calc(1.75rem + var(--jd-gutter))"
            : "1.75rem"
          : second
            ? "var(--jd-gutter)"
            : 0,
      }}
      className={cn(
        "sticky z-10 px-1.5 py-px text-right text-muted-foreground/60 tabular-nums select-none",
        second && "border-r border-hairline",
      )}
    >
      {children}
    </span>
  )
}
