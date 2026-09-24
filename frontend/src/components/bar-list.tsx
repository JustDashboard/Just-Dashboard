"use client"

import { cn } from "@/lib/utils"
import type { Tone } from "@/components/tone"

/**
 * A ranked list with a bar under each name.
 *
 * The pattern is Tremor's BarList — the one honest way to show "the top ten
 * of something" without a chart nobody can read a value off — on the tokens
 * this product draws every proportion with: the meter's track and the
 * primary fill, as a thin line *under* the name rather than a slab behind
 * it. The name stays legible, the figure sits at the right in `.numeric` so
 * a column of them reads down, and the bar is the thing the eye compares.
 *
 * `share` is the bar's length as a fraction of the longest, and `signal` the
 * fraction of *that* bar drawn in the danger or warning tone — a path's 5xx
 * share, a client's refused share — so "the busiest" and "the failing" are
 * one row read two ways rather than two lists compared across a gap.
 *
 * A row may lead with a `mark` — the browser an agent is, the network an
 * address is on (§14). Clients, agents and status codes were identical grey
 * strings, and a column of ten of them is read rather than scanned. The mark
 * has a slot of its own, drawn on every row once any row has one, so the
 * names still start on one line; and it sits outside the name's truncation,
 * so a long name never ellipses its own mark away.
 */
export type BarListItem = {
  key: string
  label: React.ReactNode
  /** What the name is, drawn before it — a `ProductGlyph`, a network's mark. */
  mark?: React.ReactNode
  /**
   * The name is a literal — a path, an address — and is set in mono. Default
   * true; a product's name ("Chrome", "www.google.com" as a referrer) is a
   * word and reads as one.
   */
  mono?: boolean
  value: React.ReactNode
  /** 0–1: this bar against the longest one. */
  share: number
  /** 0–1: the part of this bar to draw in the signal tone. */
  signal?: number
  tone?: Tone
  /** One short reading beside the label — the p95, a scanner's tell. */
  hint?: React.ReactNode
  title?: string
  onClick?: () => void
  /**
   * A verb for the row, drawn before the figure so the figures stay the last
   * column and read down as one, whether a row has a verb or not.
   */
  trailing?: React.ReactNode
}

export function BarList({
  items,
  className,
  emptyLabel = "Nothing yet",
}: {
  items: BarListItem[]
  className?: string
  emptyLabel?: React.ReactNode
}) {
  if (items.length === 0) {
    return <p className={cn("py-3 text-hint text-muted-foreground", className)}>{emptyLabel}</p>
  }
  const marked = items.some((item) => item.mark !== undefined)
  return (
    <ul className={cn("flex flex-col", className)} data-slot="bar-list">
      {items.map((item) => {
        const pressable = Boolean(item.onClick)
        const Row = pressable ? "button" : "div"
        return (
          <li key={item.key} className="flex min-w-0 items-center gap-3">
            <Row
              type={pressable ? "button" : undefined}
              onClick={item.onClick}
              title={item.title}
              // The name a screen reader hears is the sentence, not the path:
              // "Show every request to /api/checkout" rather than the path and
              // its figures. A button's content is its name unless told otherwise.
              aria-label={pressable ? item.title : undefined}
              className={cn(
                "flex min-w-0 flex-1 flex-col gap-1 rounded-sm px-2 py-1.5 text-left",
                pressable && "focus-ring-inset transition-colors hover:bg-row-hover",
              )}
            >
              <span className="flex min-w-0 items-baseline gap-2">
                {marked && (
                  <span
                    aria-hidden
                    className="flex size-3.5 shrink-0 items-center justify-center self-center"
                  >
                    {item.mark}
                  </span>
                )}
                {/* The name is the row's identity, so it keeps its width and
                    the hint gives way first — all of it, before the name loses
                    a character: a shared shrink cut an address to
                    "198.51.100…" for two pixels of caption on a phone. The name
                    truncates only once it alone is wider than the row. The
                    hint's pixel of padding keeps its last glyph's overhang
                    from being clipped by its own truncation. */}
                <span
                  className={cn(
                    "max-w-full min-w-0 shrink-0 truncate text-xs",
                    item.mono === false ? "font-medium" : "font-mono",
                  )}
                >
                  {item.label}
                </span>
                {item.hint && (
                  <span className="min-w-0 truncate pr-px text-micro text-muted-foreground">
                    {item.hint}
                  </span>
                )}
              </span>
              <span
                aria-hidden
                className="relative block h-1 w-full overflow-hidden rounded-full bg-meter-track"
              >
                <span
                  className="absolute inset-y-0 left-0 rounded-full bg-primary transition-[width]"
                  style={{ width: `${Math.max(item.share * 100, 1.5)}%` }}
                />
                {item.signal !== undefined && item.signal > 0 && (
                  <span
                    className={cn(
                      "absolute inset-y-0 left-0 rounded-full",
                      item.tone === "warning"
                        ? "bg-warning"
                        : item.tone === "success"
                          ? "bg-success"
                          : "bg-destructive",
                    )}
                    style={{ width: `${Math.max(item.share * item.signal * 100, 1.5)}%` }}
                  />
                )}
              </span>
            </Row>
            {item.trailing}
            <span className="numeric w-14 shrink-0 text-right text-hint text-muted-foreground">
              {item.value}
            </span>
          </li>
        )
      })}
    </ul>
  )
}
