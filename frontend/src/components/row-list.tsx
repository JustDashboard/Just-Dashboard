"use client"

import Link from "next/link"
import { ArrowRight } from "@/components/icons"
import { Bone } from "@/components/bone"
import { rowReveal } from "@/components/icon-action"
import { Skeleton } from "@/components/ui/skeleton"
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
    <ul
      data-slot="row-list"
      className={cn("min-w-0 divide-y divide-hairline", className)}
      {...props}
    />
  )
}

/**
 * The bleed a row takes inside a plain panel.
 *
 * A plain panel's title sits on the page's own edge, so a row's text has to
 * start there too — but its hover wash still wants to extend a step past the
 * words on both sides, or it reads as a bar that begins exactly where the
 * text does. `Row` applies this itself; a row laid out by hand (the containers
 * list on a phone, a disk line, an event) takes the same three classes so it
 * lines up with the rows it sits between.
 */
export const ROW_BLEED =
  "group-data-[plain]/panel:-mx-3 group-data-[plain]/panel:rounded-md group-data-[plain]/panel:px-3"

/**
 * The bleed a table takes inside a plain panel: the cells' own gutter (`px-4`)
 * out past the panel's edge, so the first column's words start on the edge
 * the panel's title starts on. Written by hand as `-mx-4`, `-mx-3` and
 * `-mx-2` on about thirty tables, which is three different edges. Put it on
 * the table's container (`containerClassName`) or on a flush `PanelBody`.
 */
export const TABLE_BLEED = "group-data-[plain]/panel:-mx-4"

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
 *
 * `current` is the one row of the list that is open beside it — a revision in
 * the editor, the evidence a link pointed at: `aria-current` and the
 * selection ground (`bg-accent`, §3), not a class string each caller types.
 * Anything else — an `id` to link to, a `data-` attribute, an `aria-label` —
 * goes on the row's `<li>`.
 *
 * A row is a reading. A list of *things you open* — a project, a service, a
 * data copy — is a `ChoiceList` of `ChoiceRow` cards (D5); an `href` row is
 * right where the list is a reading first and each line merely links to its
 * detail, as an activity feed does.
 */
export function Row({
  href,
  onClick,
  current,
  leading,
  title,
  subtitle,
  trailing,
  mono,
  className,
  children,
  ...props
}: Omit<React.ComponentProps<"li">, "title" | "onClick" | "children"> & {
  href?: string
  onClick?: () => void
  /** This row is the one open beside the list: `aria-current` and the selection ground. */
  current?: boolean
  leading?: React.ReactNode
  title: React.ReactNode
  /** The row's second line. Prose or a literal value, never a state. */
  subtitle?: React.ReactNode
  trailing?: React.ReactNode
  /** The subtitle is a literal string from the host — an image ref, a path. */
  mono?: boolean
  /** The row's face, not its `<li>`. */
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
            className={cn("block truncate text-hint text-muted-foreground", mono && "font-mono")}
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
  // No `w-full` on the pressable face: a block-level link or button with an
  // explicit width and the plain bleed's negative margins keeps its width and
  // drops the right-hand margin, so its wash stopped twelve pixels short of the
  // row's right edge on every plain list in the product. With the width left
  // auto the box stretches over both margins, which is what the bleed means.
  const face = cn(
    "flex min-w-0 items-center gap-3 px-5 py-3 text-left",
    ROW_BLEED,
    pressable && "group focus-ring-inset transition-colors hover:bg-row-hover",
    current && "bg-accent hover:bg-accent",
    className,
  )
  const mark = current ? (href ? ("page" as const) : true) : undefined

  return (
    <li data-slot="row" {...props} className="min-w-0">
      {href ? (
        <Link href={href} aria-current={mark} className={face}>
          {inner}
        </Link>
      ) : onClick ? (
        // A button's auto width is shrink-to-fit even as a flex container, so
        // unlike the link it has to be told to span the row — and, under the
        // plain bleed, to span both negative margins as well.
        <button
          type="button"
          onClick={onClick}
          aria-current={mark}
          className={cn(face, "w-full group-data-[plain]/panel:w-[calc(100%+1.5rem)]")}
        >
          {inner}
        </button>
      ) : (
        <div aria-current={mark} className={face}>
          {inner}
        </div>
      )}
    </li>
  )
}

const GHOST_WIDTHS = ["w-40", "w-32", "w-48", "w-36"]

/**
 * Rows that have not arrived, at a `Row`'s own pitch: the same face (`px-5
 * py-3`, the plain panel's bleed), a mark's place, a bar on the title's line
 * and one on the subtitle's, and a short one where the trailing state stands.
 * Each line is a `Bone`, which keeps the line box its words will have, so a
 * list of five is as tall loading as loaded and nothing under it moves.
 *
 * `LoadingRows` is what is left for a list whose shape is not known yet; a
 * list of `Row`s waits as this, a `ChoiceList` as `ChoiceListSkeleton`.
 */
export function RowsSkeleton({
  rows = 3,
  mark = true,
  subtitle = true,
  className,
}: {
  rows?: number
  /** The rows carry a leading mark (a 32px tile). */
  mark?: boolean
  /** The rows carry a second line. */
  subtitle?: boolean
  className?: string
}) {
  return (
    <RowList aria-hidden data-slot="rows-skeleton" className={className}>
      {Array.from({ length: rows }, (_, index) => (
        <li key={index} className="min-w-0">
          <div className={cn("flex min-w-0 items-center gap-3 px-5 py-3", ROW_BLEED)}>
            {mark && <Skeleton className="size-8 shrink-0 rounded-lg" />}
            <span className="min-w-0 flex-1">
              <span className="block text-body">
                <Bone className={GHOST_WIDTHS[index % GHOST_WIDTHS.length]} />
              </span>
              {subtitle && (
                <span className="block text-hint">
                  <Bone className="w-56" />
                </span>
              )}
            </span>
            <span className="shrink-0 text-hint">
              <Bone className="w-14" />
            </span>
          </div>
        </li>
      ))}
    </RowList>
  )
}
