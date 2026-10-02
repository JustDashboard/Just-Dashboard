"use client"

import { useMemo } from "react"
import { DataGrid, columnKind, type GridColumn } from "@/components/database/grid"
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
 */
export function ResultGrid({
  label,
  result,
  resultKey,
  toolbar,
  banner,
  footer,
}: {
  label: string
  result: QueryResult
  /** Which result this is: another one is another grid, with its own sort, find and scroll. */
  resultKey: string
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
  return (
    <DataGrid
      key={resultKey}
      label={label}
      columns={columns}
      rows={result.rows}
      clipped={result.clipped}
      sortMode="client"
      findable
      selectable={false}
      toolbar={toolbar}
      banner={banner}
      footer={footer}
    />
  )
}
