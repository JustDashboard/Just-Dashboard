"use client"

import * as React from "react"

import { ChevronDown } from "@/components/icons"
import { useRevealOnWheel } from "@/hooks/use-reveal-on-wheel"
import { cn } from "@/lib/utils"

/**
 * A cell is one step denser than body text. A table is a grid of values being
 * compared down a column, not prose being read across a line, and that step is
 * what keeps a forty-row process list on one screen.
 *
 * The base was `text-sm`, which no table in the product actually wanted: 109
 * cells re-specified `text-xs` on top of it, one cell at a time.
 */
function Table({
  className,
  containerClassName,
  ...props
}: React.ComponentProps<"table"> & {
  /**
   * Applied to the scroll container rather than the table. A long table can
   * cap its height here so the page stays a sane length and the filters above
   * it stay on screen, instead of scrolling away after a few hundred rows.
   */
  containerClassName?: string
}) {
  // A capped table is sized to fit the screen under the page's header, so the
  // wheel over one that is half below the fold brings the whole of it up first.
  const container = React.useRef<HTMLDivElement>(null)
  useRevealOnWheel(container)
  // A table that scrolls and has nothing in it to focus could not be reached
  // by a keyboard at all, so its rows past the fold could not be read. Only
  // then is the container itself a stop: a table with buttons or links in it
  // is scrolled by moving through them, and keeps the tab order it had.
  const [reachable, setReachable] = React.useState(false)
  React.useEffect(() => {
    const node = container.current
    if (!node || typeof ResizeObserver === "undefined") return
    const measure = () =>
      setReachable(
        (node.scrollHeight > node.clientHeight + 1 || node.scrollWidth > node.clientWidth + 1) &&
          !node.querySelector(
            'a[href], button, input, select, textarea, [tabindex]:not([tabindex="-1"])',
          ),
      )
    // The observer reports once as it starts, which is the first reading.
    const observer = new ResizeObserver(measure)
    observer.observe(node)
    if (node.firstElementChild) observer.observe(node.firstElementChild)
    return () => observer.disconnect()
  }, [])
  return (
    <div
      ref={container}
      data-slot="table-container"
      tabIndex={reachable ? 0 : undefined}
      role={reachable ? "region" : undefined}
      aria-label={reachable ? (props["aria-label"] ?? "Table, scrolls") : undefined}
      className={cn(
        "scroll-affordance relative w-full overflow-auto",
        reachable && "focus-ring-inset",
        containerClassName,
      )}
    >
      <table
        data-slot="table"
        className={cn("w-full caption-bottom text-xs", className)}
        {...props}
      />
    </div>
  )
}

function TableHeader({ className, ...props }: React.ComponentProps<"thead">) {
  return <thead data-slot="table-header" className={cn("[&_tr]:border-b", className)} {...props} />
}

/** Header classes for a table inside a capped-height container: the column
 *  names stay put while the rows scroll under them. The background comes from
 *  the rule in globals.css, so a sticky header and a static one match. */
const stickyTableHeader = "sticky top-0 z-10 [&_tr]:border-b"

function TableBody({ className, ...props }: React.ComponentProps<"tbody">) {
  return (
    <tbody
      data-slot="table-body"
      className={cn("[&_tr:last-child]:border-0", className)}
      {...props}
    />
  )
}

function TableFooter({ className, ...props }: React.ComponentProps<"tfoot">) {
  return (
    <tfoot
      data-slot="table-footer"
      className={cn("border-t bg-muted/50 font-medium [&>tr]:last:border-b-0", className)}
      {...props}
    />
  )
}

/** Anything inside a row that owns its own click. */
const INTERACTIVE =
  "a, button, input, select, textarea, label, [role='checkbox'], [role='menuitem'], [contenteditable='true']"

/**
 * Spread on the control that is a row's name, the one that opens what the
 * row's press opens (a `RowLink`, the name's link): `<Link {...ROW_NAME}>`.
 * A row holding one leaves the keyboard to it and is no tab stop itself. It
 * is the mark a `Workspace` already looks for when it puts the keyboard back
 * on a row, so the two agree on where a row's place is.
 */
const ROW_NAME = { "data-workspace-primary": "" } as const

/**
 * A column that a phone does without. Below `sm` it is not drawn, so a table
 * of readings keeps the columns that say what each row is and how it is, and
 * the rest wait for a wider screen (or the page the row opens). Put it on the
 * head and on every cell of the column.
 */
const SECONDARY_COLUMN = "max-sm:hidden"

function TableRow({
  className,
  onActivate,
  onClick,
  onKeyDown,
  ref,
  ...props
}: React.ComponentProps<"tr"> & {
  /**
   * Makes the whole row the hit target for its primary action, rather than
   * asking for a click on the few characters of the name. A click that landed
   * on a control inside the row is left to that control, so the row's gesture
   * and its buttons never fight over the same press.
   *
   * It is a convenience for the pointer. The keyboard's way in is the row's
   * name, the control marked with `ROW_NAME`, which says what it opens. A row
   * that was a tab stop beside its own name button cost two presses of Tab,
   * and the first announced every cell of the row as one name (A11Y-10). A
   * row with no name control is still a stop, so it can be reached: give it
   * an `aria-label`, or better, a name control.
   */
  onActivate?: () => void
}) {
  const own = React.useRef<HTMLTableRowElement | null>(null)
  // A stop until the row is seen to hold its name: which control that is
  // arrives with the row's data, so it is looked for after every render.
  const [stop, setStop] = React.useState(Boolean(onActivate))
  // No dependency list on purpose: the children are what changes, and the
  // update is skipped when the answer is the same, so it settles in one pass.
  // eslint-disable-next-line react-hooks/exhaustive-deps -- see above
  React.useLayoutEffect(() => {
    const next = Boolean(onActivate && !own.current?.querySelector("[data-workspace-primary]"))
    setStop((previous) => (previous === next ? previous : next))
  })
  const attach = React.useCallback(
    (node: HTMLTableRowElement | null) => {
      own.current = node
      if (typeof ref === "function") return ref(node)
      if (ref) ref.current = node
    },
    [ref],
  )
  return (
    <tr
      data-slot="table-row"
      ref={attach}
      tabIndex={stop ? 0 : undefined}
      className={cn(
        // Hover is a wash, selection is a fill, focus is a ring — three
        // mechanisms, so a row that is all three at once still reads. These
        // were all `bg-row-hover`, which meant a keyboard user could not tell
        // the row they had arrived at from the one under the pointer.
        "border-b border-hairline transition-colors hover:bg-row-hover has-aria-expanded:bg-row-hover data-[state=selected]:bg-accent",
        onActivate && "cursor-pointer",
        stop && "focus-ring-inset",
        className,
      )}
      onClick={(event) => {
        onClick?.(event)
        if (!onActivate || event.defaultPrevented) return
        if ((event.target as HTMLElement).closest(INTERACTIVE)) return
        onActivate()
      }}
      onKeyDown={(event) => {
        onKeyDown?.(event)
        if (!onActivate || event.defaultPrevented) return
        if (event.key !== "Enter" && event.key !== " ") return
        if (event.target !== event.currentTarget) return
        event.preventDefault()
        onActivate()
      }}
      {...props}
    />
  )
}

function TableHead({
  className,
  label,
  secondary,
  children,
  ...props
}: React.ComponentProps<"th"> & {
  /**
   * The column's name for a screen reader, where it has no visible title: a
   * column of verbs, a spacer, a mark. A header cell with nothing in it is
   * read as a nameless column (axe `empty-table-header`). Drawn only when the
   * head has no children of its own.
   */
  label?: string
  /** A column a phone does without (`SECONDARY_COLUMN`). */
  secondary?: boolean
}) {
  return (
    <th
      data-slot="table-head"
      className={cn(
        // Column names are a quiet row of labels, not small caps: at 10px
        // tracked-out caps a nine-column header was the loudest line in the
        // table, above rows it exists only to name.
        "h-10 px-4 text-left align-middle text-hint font-medium whitespace-nowrap text-muted-foreground [&:has([role=checkbox])]:pr-0 [&>[role=checkbox]]:translate-y-[2px]",
        secondary && SECONDARY_COLUMN,
        className,
      )}
      {...props}
    >
      {children ?? (label ? <span className="sr-only">{label}</span> : null)}
    </th>
  )
}

/**
 * A column's head that orders the rows under it.
 *
 * The head says which way the column runs (`aria-sort` on the `th`, `none`
 * on the others), its words are a real button with the focus ring, and one
 * chevron, drawn only on the column in use, points the way the rows run. On
 * a right-aligned column of figures the chevron goes before the words, so
 * the words stay on the edge the figures end on. It was written five times,
 * with arrows in two and chevrons in three, and the Files copy said nothing
 * to a screen reader and drew no ring (DBO-07, A11Y-19).
 */
function SortHead({
  label,
  active,
  descending,
  onSort,
  align = "left",
  after,
  secondary,
  className,
}: {
  label: string
  /** This column is the one the rows are ordered by. */
  active: boolean
  /** Which way, when `active`. */
  descending: boolean
  onSort: () => void
  align?: "left" | "right"
  /** Beside the head's button and not part of it: what the column means, as an `InfoTip`. */
  after?: React.ReactNode
  secondary?: boolean
  className?: string
}) {
  const right = align === "right"
  return (
    <TableHead
      aria-sort={active ? (descending ? "descending" : "ascending") : "none"}
      secondary={secondary}
      className={cn(right && "text-right", className)}
    >
      <button
        type="button"
        onClick={onSort}
        className={cn(
          "inline-flex items-center gap-1 rounded-sm focus-ring transition-colors hover:text-foreground",
          right && "flex-row-reverse",
          active && "text-foreground",
        )}
      >
        {label}
        {active && (
          <ChevronDown
            aria-hidden
            data-sort-glyph=""
            className={cn("size-3 shrink-0", !descending && "rotate-180")}
          />
        )}
      </button>
      {after && <span className="ml-1 inline-flex align-middle">{after}</span>}
    </TableHead>
  )
}

/**
 * A cell's figures are tabular (`.numeric`) by default. A table is the one
 * place every figure is compared with the one above it, and leaving it to each
 * column meant every figure column opted in by hand — and the ones that forgot
 * shimmered as they polled. On a cell of words it changes only the tracking,
 * by a hundredth of an em.
 */
function TableCell({
  className,
  secondary,
  ...props
}: React.ComponentProps<"td"> & {
  /** A column a phone does without (`SECONDARY_COLUMN`). */
  secondary?: boolean
}) {
  return (
    <td
      data-slot="table-cell"
      className={cn(
        "numeric px-4 py-3 align-middle whitespace-nowrap [&:has([role=checkbox])]:pr-0 [&>[role=checkbox]]:translate-y-[2px]",
        secondary && SECONDARY_COLUMN,
        className,
      )}
      {...props}
    />
  )
}

function TableCaption({ className, ...props }: React.ComponentProps<"caption">) {
  return (
    <caption
      data-slot="table-caption"
      className={cn("mt-4 text-xs text-muted-foreground", className)}
      {...props}
    />
  )
}

export {
  ROW_NAME,
  SECONDARY_COLUMN,
  SortHead,
  stickyTableHeader,
  Table,
  TableHeader,
  TableBody,
  TableFooter,
  TableHead,
  TableRow,
  TableCell,
  TableCaption,
}
