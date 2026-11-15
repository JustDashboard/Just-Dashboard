"use client"

import { useId } from "react"
import { Check } from "@/components/icons"
import type { PURPOSES } from "@/lib/sidebar-sections"
import { cn } from "@/lib/utils"
import { ChoiceCard } from "@/components/choice-card"
import { ProductLogo } from "@/components/product-logo"
import { Tag } from "@/components/tag"

type Purpose = (typeof PURPOSES)[number]

/**
 * What an experimental purpose says on its card. Stable is the rule and
 * carries no mark; the one purpose still being tested is the exception, so
 * it is flagged where it is chosen rather than on a shelf of its own. The
 * sentence gives way before the tag does.
 */
export function ExperimentalMark({ className }: { className?: string }) {
  return (
    <span className={cn("flex min-w-0 items-baseline gap-2", className)}>
      <Tag tone="warning" className="shrink-0">
        Experimental
      </Tag>
      <span className="truncate text-hint text-muted-foreground">Working, still being tested</span>
    </span>
  )
}

/**
 * One purpose the server is for, as a card you press to take or leave it:
 * its products drawn as themselves, its name, what it holds, and one tick.
 *
 * Welcome and Account → Workspace ask the same question over the same list,
 * and drew it as two different controls — tall cards with a Tick in one,
 * row cards with a Check and the bare Experimental tag in the other, where on
 * a phone the tag ran under the check mark. One card now, at two sizes:
 *
 * - `hero` is the first-run question's: logos over the name on a wide screen,
 *   the tick in the corner, 176px tall; a row on a phone.
 * - `row` is Workspace's: logos beside the words, the tick at the right,
 *   one height for every card in the grid.
 *
 * Under the name, in this order, at most: the `reading` (what the server
 * already runs for it), the experimental mark on its own line, and the
 * `detail` (the sections it means). The tick has its own column, so nothing
 * on the words' lines can run under it.
 *
 * The control is the whole card (`ChoiceCard`'s compact shape), named by the
 * purpose's title, with the rest of its words as its description. Several
 * purposes are taken at once, so it is a toggle (`aria-pressed`).
 */
export function PurposeCard({
  purpose,
  selected,
  onSelect,
  size,
  reading,
  detail,
}: {
  purpose: Purpose
  selected: boolean
  onSelect: () => void
  size: "hero" | "row"
  /** What the server already runs for this purpose: "1 project here". */
  reading?: React.ReactNode
  /** What the purpose holds: the sections it brings into the rail. */
  detail?: React.ReactNode
}) {
  const words = useId()
  const hero = size === "hero"
  const experimental = purpose.stage === "experimental"
  const more = Boolean(reading || experimental || detail)
  const tick = (
    <Check
      aria-hidden
      className={cn(
        "shrink-0 text-brand transition-opacity",
        hero ? "size-5" : "size-4",
        selected ? "opacity-100" : "opacity-0",
      )}
    />
  )
  return (
    <ChoiceCard
      selected={selected}
      onClick={onSelect}
      aria-label={purpose.title}
      aria-describedby={more ? words : undefined}
      className={cn(
        "flex-row items-center",
        hero
          ? "min-h-18 gap-3.5 px-4 py-3 sm:min-h-44 sm:flex-col sm:items-stretch sm:justify-between sm:gap-6 sm:p-6"
          : // One height for every card: the longest purpose lists its
            // sections on two lines, and the experimental one gives its
            // second to the mark, so every card is three lines.
            "min-h-20 gap-3 px-3.5 py-3",
      )}
    >
      <span className={cn("flex shrink-0 justify-between gap-3", hero && "items-start")}>
        <span className={cn("flex", hero ? "gap-2" : "gap-1")}>
          {purpose.products.map((product) => (
            <ProductLogo
              key={product}
              id={product}
              size={hero ? undefined : "sm"}
              className={cn(hero && "max-sm:size-9")}
            />
          ))}
        </span>
        {hero && <span className="max-sm:hidden">{tick}</span>}
      </span>
      <span className={cn("grid min-w-0 flex-1", hero ? "gap-1.5" : "gap-1")}>
        <span
          // The leading after the size, which would otherwise take its own.
          className={cn(
            "truncate font-semibold",
            hero ? "text-base sm:text-lg" : "text-title",
            "leading-tight",
          )}
        >
          {purpose.title}
        </span>
        {more && (
          // In a row the lines under the name are set solid, so the mark
          // takes exactly the line the sections give up.
          <span id={words} className={cn("grid min-w-0", hero && "gap-1.5")}>
            {reading}
            {experimental && <ExperimentalMark />}
            {detail && (
              <span
                className={cn(
                  "text-hint text-muted-foreground",
                  experimental ? "line-clamp-1" : "line-clamp-2",
                )}
              >
                {detail}
              </span>
            )}
          </span>
        )}
      </span>
      {hero ? <span className="sm:hidden">{tick}</span> : tick}
    </ChoiceCard>
  )
}
