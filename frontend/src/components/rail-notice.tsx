"use client"

import Link from "next/link"
import { Cross } from "@/components/icons"
import { cn } from "@/lib/utils"
import { IconAction } from "@/components/icon-action"
import { BorderBeam } from "@/components/ui/border-beam"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"

export type RailNoticeTone = "brand" | "success" | "warning" | "danger"

/** The card: the account card's own ground for news in the brand's tone, a wash for the rest. */
const CARD: Record<RailNoticeTone, string> = {
  brand: "border-sidebar-border bg-card",
  success: "border-rule-success bg-wash-success",
  warning: "border-rule-warning bg-wash-warning",
  danger: "border-rule-danger bg-wash-danger",
}

/** The collapsed rail's tile. */
const TILE: Record<RailNoticeTone, string> = {
  brand: "bg-plot-brand text-brand hover:bg-plot-brand-hover",
  success: "bg-plot-success text-success hover:bg-plot-success-hover",
  warning: "bg-plot-warning text-warning",
  danger: "bg-plot-danger text-destructive hover:bg-plot-danger-hover",
}

/** The mark's ink: the tone says itself in the glyph, never in a tile behind it. */
const INK: Record<RailNoticeTone, string> = {
  brand: "text-signal",
  success: "text-success",
  warning: "text-warning",
  danger: "text-destructive",
}

/**
 * A notice in the rail, above the account card: the dashboard's own update,
 * what a linked hub has to say. Drawn as Just Hub's own rail notices are
 * (Spoke's `BannerCard`): a compact card in the notice's tone, a 16px glyph in
 * the tone's ink at the start of its first line, the words in one column
 * beside it, and the dismiss at that line's end.
 *
 * `head` is the first line, which shares its row with the dismiss; `children`
 * are the lines under it, which take the column's whole width back.
 *
 * Collapsed, it is the tile alone, a link to where the notice is read in
 * full, named and tipped by `label`. `beam` runs a light round the edge while
 * something is under way.
 */
export function RailNotice({
  tone,
  collapsed,
  label,
  href,
  glyph,
  mark,
  head,
  onDismiss,
  dismissLabel = "Dismiss",
  section,
  slot,
  contentKey,
  beam,
  className,
  children,
}: {
  tone: RailNoticeTone
  collapsed: boolean
  /** What the collapsed tile is called and says in its tooltip. */
  label: string
  /** Where the collapsed tile leads. */
  href: string
  /** What the collapsed tile shows. */
  glyph: React.ReactNode
  /** The open card's 16px glyph, drawn in the tone's ink. Defaults to `glyph`. */
  mark?: React.ReactNode
  /** The open card's first line. */
  head?: React.ReactNode
  /** For a notice that can be put away: a 24px target at the first line's end. */
  onDismiss?: () => void
  dismissLabel?: string
  /** The open card's landmark name: "Dashboard update". */
  section: string
  slot?: string
  /** A new key arrives again (§11), rather than repainting in place. */
  contentKey?: string
  beam?: boolean
  className?: string
  /** The lines under the head; a collapsed notice draws none. */
  children?: React.ReactNode
}) {
  if (collapsed) {
    return (
      <Tooltip>
        <TooltipTrigger asChild>
          <Link
            href={href}
            aria-label={label}
            data-slot={slot}
            className={cn(
              "relative flex size-8 items-center justify-center rounded-lg focus-ring transition-colors",
              TILE[tone],
            )}
          >
            {beam && <BorderBeam size={24} duration={2.5} />}
            {glyph}
          </Link>
        </TooltipTrigger>
        <TooltipContent side="right">{label}</TooltipContent>
      </Tooltip>
    )
  }
  return (
    <section
      aria-label={section}
      data-slot={slot}
      className={cn(
        "relative isolate min-w-0 animate-rise overflow-hidden rounded-lg border",
        CARD[tone],
        className,
      )}
    >
      {beam && <BorderBeam size={72} duration={4} />}
      <div
        key={contentKey}
        className="grid min-w-0 grid-cols-[1rem_minmax(0,1fr)_auto] gap-x-2.5 gap-y-2 py-2.5 pr-2 pl-3"
      >
        {/* As tall as the head's first line, so the glyph sits on it whatever its size. */}
        <span
          aria-hidden
          className={cn("flex h-4 items-center [&_svg]:size-4 [&_svg]:shrink-0", INK[tone])}
        >
          {mark ?? glyph}
        </span>
        <div className="min-w-0 self-center">{head}</div>
        {onDismiss ? (
          <IconAction
            label={dismissLabel}
            size="icon-xs"
            onClick={onDismiss}
            className="-my-1 text-muted-foreground"
          >
            <Cross />
          </IconAction>
        ) : (
          <span className="pr-1" />
        )}
        {children && (
          <div className="col-span-2 col-start-2 min-w-0 space-y-2 pr-1">{children}</div>
        )}
      </div>
    </section>
  )
}

/**
 * A line of a `RailNotice` that goes somewhere: words in the secondary ink
 * that turn primary under the pointer (hover is colour only, motion.md), at
 * the rail's hint size, its glyph after them. Not a button: a notice's way
 * on is a line of it, as in Just Hub's rail, and the one command a notice may
 * carry (Update now) is the only button in it.
 */
export const RAIL_NOTICE_LINK =
  "group/notice-link flex w-fit max-w-full min-w-0 items-center gap-1 rounded-sm text-hint font-medium text-secondary-foreground focus-ring transition-[color,background-color] duration-fast hover:text-foreground [&_svg]:size-3 [&_svg]:shrink-0 [&_svg]:text-muted-foreground"
