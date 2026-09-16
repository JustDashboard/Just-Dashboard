"use client"

import Link from "next/link"
import { ArrowRight } from "@/components/icons"
import { rowReveal } from "@/components/icon-action"
import { cn } from "@/lib/utils"

/**
 * A list of rows, separated by hairlines.
 *
 * The shape every "a few things, each with a name and a state" list in the
 * product had been drawing by hand — the idle containers on the Docker
 * overview, the compose projects under them, recent activity on the Overview,
 * the tables rail in Databases — each with its own padding, its own hover and
 * its own idea of where the secondary line goes. One list, one row.
 */
export function RowList({ className, ...props }: React.ComponentProps<"ul">) {
  return (
    <ul data-slot="row-list" className={cn("min-w-0 divide-y divide-hairline", className)} {...props} />
  )
}

/**
 * One row: a leading mark, a title with an optional second line, and whatever
 * sits at the right edge — a `Status`, a `Tag`, a figure, a menu.
 *
 * A row with an `href` is a link and the whole row is the target; the arrow
 * appears through `rowReveal` so it is permanently visible on touch. A row
 * with `onClick` is a button. A row with neither is inert, and takes no hover.
 *
 * Inside a plain `Panel` the row's wash extends a step past the text on both
 * sides, so the hover reads as a highlight behind the words rather than a bar
 * that starts exactly where they do.
 */
export function Row({
  href,
  onClick,
  leading,
  title,
  subtitle,
  trailing,
  mono,
  className,
  children,
}: {
  href?: string
  onClick?: () => void
  leading?: React.ReactNode
  title: React.ReactNode
  /** The row's second line. Prose or a literal value, never a state. */
  subtitle?: React.ReactNode
  trailing?: React.ReactNode
  /** The subtitle is a literal string from the host — an image ref, a path. */
  mono?: boolean
  className?: string
  /** Anything else the row carries, drawn after the trailing slot. */
  children?: React.ReactNode
}) {
  const pressable = Boolean(href || onClick)
  const inner = (
    <>
      {leading && <span className="flex shrink-0 items-center">{leading}</span>}
      <span className="min-w-0 flex-1">
        <span className="block truncate text-body font-medium">{title}</span>
        {subtitle && (
          <span
            className={cn(
              "block truncate text-hint text-muted-foreground",
              mono && "font-mono",
            )}
          >
            {subtitle}
          </span>
        )}
      </span>
      {trailing && <span className="flex shrink-0 items-center gap-2">{trailing}</span>}
      {href && (
        <ArrowRight
          aria-hidden
          className={cn("size-3.5 shrink-0 text-muted-foreground", rowReveal())}
        />
      )}
      {children}
    </>
  )
  const face = cn(
    "flex min-w-0 items-center gap-3 px-5 py-3 text-left",
    "group-data-[plain]/panel:-mx-3 group-data-[plain]/panel:rounded-md group-data-[plain]/panel:px-3",
    pressable && "group w-full transition-colors focus-ring-inset hover:bg-row-hover",
    className,
  )

  return (
    <li data-slot="row" className="min-w-0">
      {href ? (
        <Link href={href} className={face}>
          {inner}
        </Link>
      ) : onClick ? (
        <button type="button" onClick={onClick} className={face}>
          {inner}
        </button>
      ) : (
        <div className={face}>{inner}</div>
      )}
    </li>
  )
}
