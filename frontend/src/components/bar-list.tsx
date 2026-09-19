"use client"

import { cn } from "@/lib/utils"
import type { Tone } from "@/components/tone"

/**
 * A ranked list with a bar behind each name.
 *
 * The pattern is Tremor's BarList — the one honest way to show "the top ten
 * of something" without a chart nobody can read a value off — rewritten onto
 * the tokens: the bar is the meter's own track, the name sits on it in body
 * type, and the figure is `.numeric` at the right edge so a column of them
 * can be read down. Nothing is framed; the list is rows of a plain panel.
 *
 * `share` is the bar's length as a fraction of the longest, and `signal` the
 * fraction of *that* bar drawn in the danger tone — a path's 5xx share, a
 * client's refused share — so "the busiest" and "the failing" are one row
 * read two ways rather than two lists compared across a gap.
 */
export type BarListItem = {
  key: string
  label: React.ReactNode
  value: React.ReactNode
  /** 0–1: this bar against the longest one. */
  share: number
  /** 0–1: the part of this bar to draw in the signal tone. */
  signal?: number
  tone?: Tone
  /** One short line under the label — the p95, a scanner's tell. */
  hint?: React.ReactNode
  title?: string
  onClick?: () => void
  /** Anything at the right of the figure: a verb. */
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
  return (
    <ul className={cn("flex flex-col gap-1", className)} data-slot="bar-list">
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
              // "Show every request to /api/checkout" rather than "/api/checkout
              // 13 × 5xx". A button's content is its name unless told otherwise.
              aria-label={pressable ? item.title : undefined}
              className={cn(
                "relative flex min-w-0 flex-1 flex-col justify-center rounded-sm px-2 py-1 text-left",
                pressable && "focus-ring-inset transition-colors hover:bg-row-hover",
              )}
            >
              <span
                aria-hidden
                className="absolute inset-y-0 left-0 rounded-sm bg-meter-track"
                style={{ width: `${Math.max(item.share * 100, 1)}%` }}
              >
                {item.signal !== undefined && item.signal > 0 && (
                  <span
                    className={cn(
                      "absolute inset-y-0 left-0 rounded-sm",
                      item.tone === "warning" ? "bg-warning" : "bg-destructive",
                    )}
                    style={{ width: `${Math.max(item.signal * 100, 2)}%` }}
                  />
                )}
              </span>
              <span className="relative block min-w-0 truncate font-mono text-xs">{item.label}</span>
              {item.hint && (
                <span className="relative block min-w-0 truncate text-micro text-muted-foreground">
                  {item.hint}
                </span>
              )}
            </Row>
            <span className="numeric w-16 shrink-0 text-right text-hint text-muted-foreground">
              {item.value}
            </span>
            {item.trailing}
          </li>
        )
      })}
    </ul>
  )
}
