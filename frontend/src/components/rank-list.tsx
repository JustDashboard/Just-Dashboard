"use client"

import { cn } from "@/lib/utils"

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
        const pressable = Boolean(item.onClick)
        const Name = pressable ? "button" : "div"
        return (
          <li
            key={item.key}
            data-slot="rank-row"
            className="flex h-8 min-w-0 items-center gap-3 rounded-sm px-2 transition-colors has-[>button:hover]:bg-row-hover"
          >
            <Name
              type={pressable ? "button" : undefined}
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
                {item.share === undefined ? "" : percent(item.share)}
              </span>
            )}
          </li>
        )
      })}
    </ul>
  )
}

/** A share in whole percent, without calling a sliver nothing or a near-whole all of it. */
export function percent(share: number) {
  if (share <= 0) return "0%"
  if (share >= 1) return "100%"
  const whole = Math.round(share * 100)
  if (whole === 0) return "<1%"
  if (whole === 100) return ">99%"
  return `${whole}%`
}

/**
 * One whole as one strip, its parts side by side in their colours. It stays
 * the block's width whatever the figures are, so nothing on the page grows
 * when the window changes: only the join between the parts moves.
 */
export function SplitBar({
  parts,
  label,
  className,
}: {
  parts: { key: string; count: number; color: string }[]
  label: string
  className?: string
}) {
  const total = parts.reduce((n, part) => n + part.count, 0)
  return (
    <span
      role="img"
      aria-label={label}
      data-slot="split-bar"
      className={cn("flex h-2 w-full gap-0.5 overflow-hidden rounded-full", className)}
    >
      {total === 0 ? (
        <span className="h-full w-full bg-meter-track" />
      ) : (
        parts
          .filter((part) => part.count > 0)
          .map((part) => (
            <span
              key={part.key}
              className="h-full transition-[width] duration-500 ease-out"
              style={{ width: `${(part.count / total) * 100}%`, backgroundColor: part.color }}
            />
          ))
      )}
    </span>
  )
}
