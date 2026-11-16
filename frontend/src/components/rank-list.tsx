"use client"

import Link from "next/link"
import { sharePercent } from "@/lib/format"
import { cn } from "@/lib/utils"
import { Skeleton } from "@/components/ui/skeleton"

/**
 * A ranked list that is read rather than measured: a name, a figure and the
 * name's share of the whole, in columns that line up down the list and end on
 * the right-hand edge of the block's head.
 *
 * Insights drew each of these as a `BarList`, and a page of bars of different
 * lengths is a page of white slabs with the figures floating beside them: the
 * row ended wherever its bar did and the figure sat on another line. The share
 * says what the bar's length said, on the line the name is on. A proportion
 * that wants a picture is a `SplitBar` over the list: one strip, the block's
 * full width, that does not grow from row to row.
 *
 * The rows bleed 8px either side so a row's hover wash has room around its
 * text while the text itself stays on the head's left and right edges.
 */
export type RankItem = {
  key: string
  label: React.ReactNode
  /** What the name is, drawn before it. */
  mark?: React.ReactNode
  /** The row's colour in the `SplitBar` above the list, as a legend. */
  swatch?: string
  /** A literal — a path, an address — set in mono. Default true. */
  mono?: boolean
  value: React.ReactNode
  /** 0–1 of the whole, drawn as a percentage in the last column. */
  share?: number
  /** One short reading beside the name; gives way before the name does. */
  hint?: React.ReactNode
  title?: string
  onClick?: () => void
  /** The page the name opens, for a row that is a way to one rather than a filter. */
  href?: string
  /** A verb for the row, drawn before the figures so they stay the last columns. */
  trailing?: React.ReactNode
}

export function RankList({
  items,
  className,
  emptyLabel = "Nothing yet",
}: {
  items: RankItem[]
  className?: string
  emptyLabel?: React.ReactNode
}) {
  if (items.length === 0) {
    return <p className={cn("py-3 text-hint text-muted-foreground", className)}>{emptyLabel}</p>
  }
  const marked = items.some((item) => item.mark !== undefined)
  const shared = items.some((item) => item.share !== undefined)
  return (
    <ul className={cn("-mx-2 flex min-w-0 flex-col", className)} data-slot="rank-list">
      {items.map((item) => {
        const pressable = Boolean(item.href ?? item.onClick)
        const Name: React.ElementType = item.href ? Link : item.onClick ? "button" : "div"
        return (
          <li
            key={item.key}
            data-slot="rank-row"
            className="flex h-8 min-w-0 items-center gap-3 rounded-sm px-2 transition-colors has-[>a:hover]:bg-row-hover has-[>button:hover]:bg-row-hover"
          >
            <Name
              href={item.href}
              type={pressable && !item.href ? "button" : undefined}
              onClick={item.onClick}
              title={item.title}
              aria-label={pressable ? item.title : undefined}
              className={cn(
                "flex h-full min-w-0 flex-1 items-center gap-2 text-left",
                pressable && "focus-ring-inset",
              )}
            >
              {item.swatch && (
                <span
                  aria-hidden
                  className="size-2 shrink-0 rounded-full"
                  style={{ backgroundColor: item.swatch }}
                />
              )}
              {marked && (
                <span aria-hidden className="flex size-3.5 shrink-0 items-center justify-center">
                  {item.mark}
                </span>
              )}
              {/* The name keeps its width and the hint gives way first, all of
                  it, before the name loses a character: a shared shrink cut an
                  address to "198.51.100…" for two pixels of caption on a phone.
                  A hint with too little room wraps onto a line the row does not
                  show, rather than standing as a one-letter stub. */}
              <span className="flex h-4 min-w-0 flex-1 flex-wrap items-baseline gap-x-2 overflow-hidden">
                <span
                  className={cn(
                    "max-w-full min-w-0 shrink-0 truncate text-xs leading-4",
                    item.mono === false ? "font-medium" : "font-mono",
                  )}
                >
                  {item.label}
                </span>
                {item.hint && (
                  <span className="min-w-[6ch] flex-[1_1_6ch] truncate pr-px text-micro leading-4 text-muted-foreground">
                    {item.hint}
                  </span>
                )}
              </span>
            </Name>
            {item.trailing && <span className="flex shrink-0 items-center">{item.trailing}</span>}
            <span
              data-slot="rank-value"
              className="numeric min-w-12 shrink-0 text-right text-xs font-medium text-foreground"
            >
              {item.value}
            </span>
            {shared && (
              <span
                data-slot="rank-share"
                className="numeric w-10 shrink-0 text-right text-hint text-muted-foreground"
              >
                {item.share === undefined ? "" : sharePercent(item.share)}
              </span>
            )}
          </li>
        )
      })}
    </ul>
  )
}

/**
 * A `RankList` that has not arrived: a name and a figure on rows of the list's
 * own height, so nothing moves when the figures land. It is not bled as the
 * list is: its rows have no wash, and the names already start on the edge.
 */
export function RankSkeleton({ rows = 5, className }: { rows?: number; className?: string }) {
  return (
    <div className={cn("flex flex-col", className)} aria-hidden data-slot="rank-skeleton">
      {Array.from({ length: rows }).map((_, index) => (
        <div key={index} className="flex h-8 items-center justify-between gap-3">
          <Skeleton className="h-3" style={{ width: `${62 - ((index * 17) % 34)}%` }} />
          <Skeleton className="h-3 w-12 shrink-0" />
        </div>
      ))}
    </div>
  )
}

/** @deprecated import `sharePercent` from `@/lib/format` */
export const percent = sharePercent

export type SplitPart = {
  key: string
  count: number
  color: string
  /** The part's name in the legend under the bar. */
  label?: React.ReactNode
  /** What the part weighs as the reader sees it: "1.2 MB", "2,053". */
  figure?: React.ReactNode
  /** Quieter words after the figure: its share, "about 400 keys". */
  note?: React.ReactNode
  /** Drawn before the name in place of the dot in the part's colour: a kind's own mark. */
  mark?: React.ReactNode
  /** What pressing the part's legend entry does, as its accessible name. */
  pickLabel?: string
}

/**
 * Each part's width along the bar, in percent, in the order given; a part
 * with nothing in it is left out. `total` scales the parts against a larger
 * whole (the busiest interface), so the track shows past them; by default
 * they fill it. A part above zero is never narrower than `minShare` of the
 * bar, so the one key type of a hundred or a trickle against the busiest
 * still shows, and the floors are taken back out of the others rather than
 * pushing the last part off the end.
 */
export function splitWidths(
  parts: Pick<SplitPart, "key" | "count">[],
  { total, minShare = 0.01 }: { total?: number; minShare?: number } = {},
): { key: string; width: number }[] {
  const held = parts.filter((part) => part.count > 0)
  const sum = held.reduce((n, part) => n + part.count, 0)
  const scale = Math.max(sum, total ?? 0)
  if (scale <= 0) return []
  const floored = held.map((part) => Math.max(part.count / scale, minShare))
  const over = floored.reduce((n, share) => n + share, 0)
  const fit = over > 1 ? 1 / over : 1
  return held.map((part, index) => ({ key: part.key, width: floored[index] * fit * 100 }))
}

/**
 * One whole as one strip, its parts side by side in their colours. It stays
 * the block's width whatever the figures are, so nothing on the page grows
 * when the window changes: only the join between the parts moves.
 *
 * It is every bar of coloured parts in the product: what a table's bytes are
 * made of, a database's keys by type, traffic by country, in and out against
 * the busiest interface (`total`). Four of them were drawn by hand beside this
 * one, with three gaps between the parts and three legends under them; there
 * is one gap now, two pixels, and one legend.
 *
 * `legend="static"` names the parts under the bar: a dot in the part's colour
 * (or its own `mark`), its name, its figure and a quieter note. `"pick"` makes
 * each entry a button that narrows the page to that part (`onPick`), pressed
 * while it is the one `picked`; pressing it again is the caller's to clear.
 * Without a legend the bar is the reading's picture and a `RankList` or the
 * sentence beside it names the parts.
 */
export function SplitBar({
  parts,
  label,
  legend = "none",
  picked,
  onPick,
  total,
  minShare,
  size = "md",
  gap = true,
  className,
}: {
  parts: SplitPart[]
  /** The bar as a screen reader hears it: what it divides and how. */
  label: string
  legend?: "none" | "static" | "pick"
  /** The part the page is narrowed to, under `legend="pick"`. */
  picked?: string
  onPick?: (key: string) => void
  /** Scale the parts against this rather than their sum. */
  total?: number
  /** The narrowest a part above zero is drawn, 0–1 of the bar. 1% by default. */
  minShare?: number
  /** `sm` is 4px, for a bar in a table cell; `md` 8px. */
  size?: "sm" | "md"
  /** The two-pixel join between parts; off for a bar too small to show it. */
  gap?: boolean
  className?: string
}) {
  const widths = splitWidths(parts, { total, minShare })
  const color = new Map(parts.map((part) => [part.key, part.color]))
  const filled = widths.reduce((n, part) => n + part.width, 0)
  const bar = (
    <span
      role="img"
      aria-label={label}
      data-slot="split-bar"
      className={cn(
        "flex w-full overflow-hidden rounded-full",
        size === "sm" ? "h-1" : "h-2",
        gap && "gap-0.5",
        legend === "none" && className,
      )}
    >
      {widths.map((part) => (
        <span
          key={part.key}
          className="h-full transition-[width] duration-500 ease-out"
          style={{ width: `${part.width}%`, backgroundColor: color.get(part.key) }}
        />
      ))}
      {/* The track shows only where no part is: an empty whole, or the rest
          of a larger one. Between the parts the gap is the page's ground. */}
      {filled < 99.95 && <span className="h-full flex-1 bg-meter-track" />}
    </span>
  )
  if (legend === "none") return bar
  const named = parts.filter((part) => part.count > 0)
  return (
    <div data-slot="split" className={cn("min-w-0 space-y-2", className)}>
      {bar}
      <ul data-slot="split-legend" className="flex flex-wrap gap-x-4 gap-y-1">
        {named.map((part) => {
          const words = (
            <>
              {part.mark ?? (
                <span
                  aria-hidden
                  className="size-1.5 shrink-0 rounded-full"
                  style={{ backgroundColor: part.color }}
                />
              )}
              {part.label !== undefined && (
                <span className="truncate text-muted-foreground">{part.label}</span>
              )}
              {part.figure !== undefined && (
                <span className="numeric font-medium text-foreground">{part.figure}</span>
              )}
              {part.note !== undefined && (
                <span className="numeric text-muted-foreground">{part.note}</span>
              )}
            </>
          )
          const pressed = picked === part.key
          return (
            <li key={part.key} className="flex min-w-0 text-hint">
              {legend === "pick" && onPick ? (
                <button
                  type="button"
                  aria-pressed={pressed}
                  aria-label={part.pickLabel}
                  onClick={() => onPick(part.key)}
                  className={cn(
                    "-mx-1 flex min-w-0 items-center gap-1.5 rounded-sm px-1 py-0.5 focus-ring transition-colors hover:bg-row-hover",
                    pressed && "bg-accent hover:bg-accent",
                  )}
                >
                  {words}
                </button>
              ) : (
                <span className="flex min-w-0 items-center gap-1.5">{words}</span>
              )}
            </li>
          )
        })}
      </ul>
    </div>
  )
}
