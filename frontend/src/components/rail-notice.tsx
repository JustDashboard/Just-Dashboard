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

/** The mark in front of the words. */
const MARK: Record<RailNoticeTone, string> = {
  brand: "border-rule-brand bg-wash-brand text-brand",
  success: "border-rule-success bg-wash-success text-success",
  warning: "border-rule-warning bg-wash-warning text-warning",
  danger: "border-rule-danger bg-wash-danger text-destructive",
}

/**
 * A notice in the rail, above the account card: the dashboard's own update,
 * what a linked hub has to say. Both were the same shell written twice — a
 * card in the notice's tone, a tinted tile with a tooltip when the rail is
 * collapsed, a dismiss on a 16px target — and this is that shell once, with
 * the dismiss at the 24px a target needs.
 *
 * Collapsed, it is the tile alone, a link to where the notice is read in
 * full, named and tipped by `label`. Open, it is the card, and `children` are
 * its contents: a `RailNoticeHead` first, then the notice's own lines.
 * `beam` runs a light round the edge while something is under way.
 */
export function RailNotice({
  tone,
  collapsed,
  label,
  href,
  glyph,
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
  /** The open card's landmark name: "Dashboard update". */
  section: string
  slot?: string
  /** A new key arrives again (§11), rather than repainting in place. */
  contentKey?: string
  beam?: boolean
  className?: string
  /** The open card's contents; a collapsed notice draws none. */
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
      <div key={contentKey} className="space-y-3 p-3">
        {children}
      </div>
    </section>
  )
}

/**
 * The first line of a `RailNotice`: its mark on a tile in the notice's tone,
 * what it is and who says it, and — for a notice that can be put away — the
 * dismiss, a 24px target at the line's end.
 */
export function RailNoticeHead({
  tone,
  mark,
  onDismiss,
  dismissLabel = "Dismiss",
  className,
  children,
}: {
  tone: RailNoticeTone
  mark: React.ReactNode
  onDismiss?: () => void
  dismissLabel?: string
  className?: string
  children: React.ReactNode
}) {
  return (
    <div className={cn("flex min-w-0 items-start gap-2.5", className)}>
      <span
        aria-hidden
        className={cn(
          "flex size-8 shrink-0 items-center justify-center rounded-lg border",
          MARK[tone],
        )}
      >
        {mark}
      </span>
      <div className="min-w-0 flex-1 space-y-1 self-center">{children}</div>
      {onDismiss && (
        <IconAction
          label={dismissLabel}
          size="icon-xs"
          onClick={onDismiss}
          className="-mt-0.5 -mr-1 text-muted-foreground"
        >
          <Cross />
        </IconAction>
      )}
    </div>
  )
}
