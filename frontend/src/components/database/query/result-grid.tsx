"use client"

import { useMemo, useState } from "react"
import { MagnifyingGlass } from "@/components/icons"
import { cn } from "@/lib/utils"
import { IconAction } from "@/components/icon-action"
import { SearchInput } from "@/components/page"
import {
  DataGrid,
  EMPTY_LAYOUT,
  GridColumnsMenu,
  columnKind,
  type GridColumn,
  type GridLayout,
} from "@/components/database/grid"
import type { QueryResult } from "@/components/database/query/types"

/**
 * A column's type as the driver named it. For a type of the database's own —
 * an enum, a domain — some drivers hand over its number in the catalogue,
 * which is nobody's name for it: the header says nothing rather than "226200".
 */
const typeName = (raw: string | undefined) => (!raw || /^\d+$/.test(raw) ? "" : raw)

/**
 * A statement's rows in the section's grid, read-only: sorted and searched in
 * memory, copied by cell, range or row, and never edited — a result is not a
 * table, and there is nothing to write an edit back to.
 *
 * It is the same windowed grid the table editor uses, so five thousand rows
 * of forty columns are a few hundred elements, and a value the server cut for
 * display says so instead of being drawn as if it were whole.
 *
 * On a narrow pane the find box and the Columns menu, which the grid lays in
 * its strip, wrap onto lines of their own and take the rows' room. There they
 * sit behind one control in the strip, and open as one line when asked for.
 */
export function ResultGrid({
  label,
  result,
  resultKey,
  compact,
  toolbar,
  banner,
  footer,
}: {
  label: string
  result: QueryResult
  /** Which result this is: another one is another grid, with its own sort, find and scroll. */
  resultKey: string
  compact?: boolean
  toolbar?: React.ReactNode
  banner?: React.ReactNode
  footer?: React.ReactNode
}) {
  const columns = useMemo<GridColumn[]>(
    () =>
      result.columns.map((name, index) => ({
        // By position: `SELECT a.id, b.id` is two columns, not one.
        key: String(index),
        name,
        typeName: typeName(result.types?.[index]),
        kind: columnKind({
          typeName: typeName(result.types?.[index]),
          serverKind: result.kinds?.[index],
        }),
      })),
    [result],
  )
  // Held here so the find and the arrangement survive the strip changing
  // shape, and belong to this result: another result starts with neither.
  const [held, setHeld] = useState({ key: resultKey, find: "", layout: EMPTY_LAYOUT })
  const mine = held.key === resultKey ? held : { key: resultKey, find: "", layout: EMPTY_LAYOUT }
  const setFind = (find: string) => setHeld({ ...mine, find })
  const setLayout = (layout: GridLayout) => setHeld({ ...mine, layout })
  const [tools, setTools] = useState(false)

  return (
    <DataGrid
      key={resultKey}
      label={label}
      columns={columns}
      rows={result.rows}
      clipped={result.clipped}
      sortMode="client"
      findable={!compact}
      find={mine.find}
      onFindChange={setFind}
      layout={mine.layout}
      onLayoutChange={setLayout}
      columnsMenu={!compact}
      selectable={false}
      toolbar={
        compact ? (
          <>
            {toolbar}
            <span className="ml-auto flex shrink-0">
              <FindAndColumns open={tools} onOpenChange={setTools} find={mine.find} />
            </span>
          </>
        ) : (
          toolbar
        )
      }
      banner={
        <>
          {compact && tools && (
            <FindAndColumnsBar
              find={mine.find}
              onFind={setFind}
              columns={columns}
              layout={mine.layout}
              onLayout={setLayout}
            />
          )}
          {banner}
        </>
      }
      footer={footer}
    />
  )
}

/**
 * The grid's find box and Columns menu behind one control, for a pane too
 * narrow to give them a line of their own above the rows.
 */
function FindAndColumns({
  open,
  onOpenChange,
  find,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  find: string
}) {
  return (
    <IconAction
      label={open ? "Hide find and columns" : "Find in these rows, or choose columns"}
      aria-pressed={open}
      className={cn("size-7 shrink-0 max-sm:size-8", (open || find) && "bg-accent")}
      onClick={() => onOpenChange(!open)}
    >
      <MagnifyingGlass />
    </IconAction>
  )
}

/** The line that control opens: the find box and the Columns menu. */
function FindAndColumnsBar({
  find,
  onFind,
  columns,
  layout,
  onLayout,
}: {
  find: string
  onFind: (find: string) => void
  columns: readonly GridColumn[]
  layout: GridLayout
  onLayout: (layout: GridLayout) => void
}) {
  return (
    <div className="flex shrink-0 items-center gap-1.5 border-b border-hairline bg-surface-header px-2.5 py-1.5">
      <SearchInput
        dense
        autoFocus
        value={find}
        placeholder="Find in these rows"
        aria-label="Find in these rows"
        containerClassName="min-w-0 flex-1 sm:w-auto"
        onChange={(event) => onFind(event.target.value)}
      />
      <GridColumnsMenu columns={columns} layout={layout} onLayoutChange={onLayout} />
    </div>
  )
}
