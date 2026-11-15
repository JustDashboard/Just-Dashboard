"use client"

import { Fragment, useMemo } from "react"
import { ChevronRight } from "@/components/icons"
import { cn } from "@/lib/utils"
import { FactDot } from "@/components/metrics/host-identity"
import { PaneFooter, PaneHeader } from "@/components/panel"
import { ErrorState } from "@/components/state"
import { Skeleton } from "@/components/ui/skeleton"
import { Tag } from "@/components/tag"
import { isDefault, type EditValue } from "@/components/database/grid"
import { ValueText } from "@/components/database/kit/value-text"
import {
  drawnRows,
  inspectRow,
  type InspectedCell,
  type InspectedRow,
  type RowSource,
  type RowState,
} from "@/components/database/data/rows"
import { grouped } from "@/components/database/data/view"

/** The same three words and hues the row panel and the grid's gutter use. */
const STATE_WORD: Record<RowState, string> = {
  inserted: "new",
  updated: "edited",
  deleted: "to delete",
  clean: "",
}
const STATE_HUE: Record<RowState, string> = {
  inserted: "text-(--git-added)",
  updated: "text-(--git-modified)",
  deleted: "text-(--git-deleted)",
  clean: "",
}
const STATE_EDGE: Record<RowState, string> = {
  inserted: "border-l-(--git-added)",
  updated: "border-l-(--git-modified)",
  deleted: "border-l-(--git-deleted)",
  clean: "border-l-transparent",
}

/** How many of a row's first columns make its title. */
const TITLE_COLUMNS = 2

/**
 * A table's rows on a phone: one card a row, the full row a tap away.
 *
 * A grid on a phone is two columns and a sideways scroll — the columns that
 * say which row this is and the ones that were asked about are never on
 * screen together. So below the width a grid can be read at, a row is read
 * the way Schema reads a column there: its first columns as the name, the
 * rest on one mono line under it, and the row panel — every column with room
 * for its whole value, and an editor for its kind — one tap away.
 *
 * It is a third view of the same page and the same staged set as the grid
 * and the panel, not a list with a state of its own: a row edited in the
 * panel is "edited" here, in the hue it has there, and the filters, the
 * change bar and the pager around it are the grid's own. Every card is one
 * height whatever its row holds — a refusal takes the place of the second
 * line rather than a third.
 */
export function RowCards({
  label,
  source,
  offset,
  active,
  readOnlyReason,
  rowErrors,
  loading,
  error,
  onRetry,
  onOpenRow,
  toolbar,
  banner,
  empty,
  footer,
}: {
  /** What the list is, for a screen reader: "items rows". */
  label: string
  source: RowSource
  /** How many rows of the table stand before this page's first. */
  offset: number
  /** The row the panel last showed, marked so the reader finds their place again. */
  active: number
  readOnlyReason?: React.ReactNode
  rowErrors: Readonly<Record<string, string>>
  loading: boolean
  error: Error | null
  onRetry: () => void
  onOpenRow: (index: number) => void
  toolbar: React.ReactNode
  banner?: React.ReactNode
  empty: React.ReactNode
  footer?: React.ReactNode
}) {
  const count = drawnRows(source)
  const rows = useMemo(
    () =>
      Array.from({ length: count }, (_, index) => inspectRow(source, index)).filter(
        (row): row is InspectedRow => row !== null,
      ),
    [source, count],
  )
  const settled = !loading || count > 0
  return (
    <div data-slot="row-cards" className="flex min-h-0 min-w-0 flex-1 flex-col">
      <PaneHeader className="flex-wrap">
        <div className="flex min-w-0 flex-1 flex-wrap items-center gap-1.5">{toolbar}</div>
      </PaneHeader>
      {readOnlyReason && (
        <div className="flex shrink-0 items-center gap-2 border-b border-hairline px-2.5 py-1 text-hint text-muted-foreground">
          <Tag>Read-only</Tag>
          <span className="min-w-0 truncate">{readOnlyReason}</span>
        </div>
      )}
      {banner}
      <div className="min-h-0 flex-1 overflow-y-auto">
        {error && count === 0 ? (
          <div className="p-4">
            <ErrorState error={error} onRetry={onRetry} />
          </div>
        ) : !settled ? (
          <div role="status" aria-label="Loading the rows">
            {Array.from({ length: 12 }, (_, index) => (
              <div
                key={index}
                aria-hidden
                className="flex h-[52px] flex-col justify-center gap-2 border-b border-hairline px-3"
              >
                <Skeleton className="h-3 w-32" />
                <Skeleton className="h-2.5 w-52" />
              </div>
            ))}
          </div>
        ) : count === 0 ? (
          <div className="flex justify-center p-6">{empty}</div>
        ) : (
          <ul aria-label={label}>
            {rows.map((row) => (
              <li key={row.id}>
                <RowCard
                  row={row}
                  number={row.state === "inserted" ? null : offset + row.index + 1}
                  active={row.index === active}
                  refusal={rowErrors[row.id]}
                  onOpen={() => onOpenRow(row.index)}
                />
              </li>
            ))}
          </ul>
        )}
      </div>
      {footer && (
        <PaneFooter data-slot="row-cards-foot" className="gap-x-4 gap-y-1">
          <div className="ml-auto flex min-w-0 items-center gap-2">{footer}</div>
        </PaneFooter>
      )}
    </div>
  )
}

function RowCard({
  row,
  number,
  active,
  refusal,
  onOpen,
}: {
  row: InspectedRow
  /** The row's number in the table; none for one that is only staged. */
  number: number | null
  active: boolean
  /** Why the server refused this row's change. */
  refusal: string | undefined
  onOpen: () => void
}) {
  // A table of one or two columns has nothing left for the second line, so
  // its title is the first column alone and the line under it the other.
  const titled = row.cells.length > TITLE_COLUMNS ? TITLE_COLUMNS : 1
  const title = row.cells.slice(0, titled)
  const rest = row.cells.slice(titled)
  return (
    <button
      type="button"
      data-slot="row-card"
      data-state={row.state}
      aria-label={number !== null ? `Row ${grouped(number)}` : "New row"}
      aria-current={active || undefined}
      onClick={onOpen}
      className={cn(
        // One height in every state, and off-screen cards cost nothing: a
        // page can be a thousand of them.
        "flex h-[52px] w-full items-center gap-2 border-b border-l-2 border-hairline pr-2 pl-2.5 text-left focus-ring-inset [contain-intrinsic-size:auto_52px] [content-visibility:auto] hover:bg-row-hover",
        STATE_EDGE[row.state],
        active && "bg-row-hover",
      )}
    >
      <span className="min-w-0 flex-1">
        <span
          className={cn(
            "flex min-w-0 items-center gap-1.5",
            row.state === "deleted" && "line-through decoration-(--git-deleted)",
          )}
        >
          {title.map((cell, index) => (
            <Fragment key={cell.column.key}>
              {index > 0 && <FactDot />}
              <CardValue
                cell={cell}
                className={cn(
                  "truncate text-body",
                  index === 0 ? "shrink-0 font-medium" : "min-w-0",
                )}
              />
            </Fragment>
          ))}
        </span>
        {refusal ? (
          <span role="alert" className="block truncate text-hint text-destructive" title={refusal}>
            {refusal}
          </span>
        ) : (
          <span className="flex min-w-0 items-center gap-1.5 overflow-hidden font-mono text-hint whitespace-nowrap text-muted-foreground">
            {rest.length === 0 ? (
              <span>{number !== null ? `row ${grouped(number)}` : "not yet stored"}</span>
            ) : (
              rest.map((cell, index) => (
                <Fragment key={cell.column.key}>
                  {index > 0 && <FactDot />}
                  <span className="shrink-0">{cell.column.name}</span>
                  <CardValue cell={cell} className="shrink-0 text-hint" />
                </Fragment>
              ))
            )}
          </span>
        )}
      </span>
      {row.state !== "clean" && (
        <span className={cn("shrink-0 font-mono text-hint", STATE_HUE[row.state])}>
          {STATE_WORD[row.state]}
        </span>
      )}
      <ChevronRight className="size-3.5 shrink-0 text-muted-foreground" aria-hidden />
    </button>
  )
}

/** One value on a card: the grid's own typed text, and the two words for a value that is not one yet. */
function CardValue({ cell, className }: { cell: InspectedCell; className?: string }) {
  const value: EditValue | undefined = cell.value
  if (value === undefined || isDefault(value)) {
    return (
      <span className={cn("font-mono text-muted-foreground/60 italic", className)}>default</span>
    )
  }
  return (
    <ValueText
      value={value}
      type={cell.column.typeName}
      clamp={48}
      className={cn(cell.edited && "text-(--git-modified)", className)}
    />
  )
}
