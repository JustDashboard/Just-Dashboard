"use client"

import { useEffect, useMemo, useState } from "react"
import Link from "next/link"
import {
  ArrowRight,
  ChevronDown,
  ChevronUp,
  Copy,
  Download,
  RotateCounterClockwise,
  SidebarRightClose,
  Trash,
} from "@/components/icons"
import { get } from "@/lib/api"
import { copyText } from "@/lib/clipboard"
import { bytes } from "@/lib/format"
import { notify } from "@/lib/toast"
import { IconAction } from "@/components/icon-action"
import { SearchInput } from "@/components/page"
import { EmptyState } from "@/components/state"
import { Button } from "@/components/ui/button"
import {
  duplicateValues,
  exceedsLimit,
  isDefault,
  type CellValue,
  type ChangeSetController,
  type EditValue,
  type GridColumn,
} from "@/components/database/grid"
import { useDatabase } from "@/components/database/shell/database-context"
import { RowField } from "@/components/database/data/field"
import { encodeFilters, filterText } from "@/components/database/data/filters"
import { ForeignKeyPeek } from "@/components/database/data/fk-peek"
import { ForeignKeyPicker } from "@/components/database/data/fk-picker"
import { KindGlyph } from "@/components/database/data/kinds"
import {
  cellKey,
  rowRecord,
  type InspectedCell,
  type InspectedRow,
} from "@/components/database/data/rows"
import type {
  CellRead,
  DbIncomingForeignKey,
  DbTableDetail,
  Filter,
} from "@/components/database/data/types"
import { grouped } from "@/components/database/data/view"

/** How many tables that point here are counted for one row. */
const REFERENCES = 12

const STATE_WORD = { inserted: "new", updated: "edited", deleted: "to delete", clean: "" } as const
const STATE_HUE = {
  inserted: "text-(--git-added)",
  updated: "text-(--git-modified)",
  deleted: "text-(--git-deleted)",
  clean: "",
} as const

/**
 * The active row, read down instead of across: every column with room for
 * its whole value and an editor for its kind, the row a foreign key points at
 * one press away, and the rows elsewhere that point at this one.
 *
 * It is a second view of the same staged set as the grid, not a form with a
 * Save of its own: a field settled here is a pending cell there, in the same
 * change bar, applied by the same Apply. So nothing here can be lost by
 * closing it, and it never asks before the reader moves to another row.
 */
export function RowInspector({
  schema,
  table,
  detail,
  columns,
  row,
  rowNumber,
  rows,
  editable,
  canInsert,
  canDelete,
  defaultOnUpdate,
  changeSet,
  onMove,
  onClose,
}: {
  schema: string
  table: string
  detail: DbTableDetail
  columns: readonly GridColumn[]
  row: InspectedRow | null
  /** The row's number in the table, as the grid's gutter prints it. */
  rowNumber: number | null
  /** How many rows the grid draws. */
  rows: number
  editable: boolean
  canInsert: boolean
  canDelete: boolean
  defaultOnUpdate: boolean
  changeSet: ChangeSetController
  onMove: (index: number) => void
  onClose: () => void
}) {
  const { id, engine } = useDatabase()
  // A key is called one where it tells a row from the others. ClickHouse's is
  // the order its rows are stored in, and repeats: it is called that.
  const identifies = engine.capabilities.rowIdentity !== "none"
  const [query, setQuery] = useState("")
  // The whole of values the page carried only the start of, by row and column.
  const [whole, setWhole] = useState<Record<string, CellValue>>({})
  const [loading, setLoading] = useState<string | null>(null)
  const needle = query.trim().toLowerCase()

  const keyCut = Boolean(
    row?.origin?.clipped?.some((key) => columns.find((c) => c.key === key)?.primaryKey),
  )
  const cells = row?.cells.filter(
    (cell) => !needle || cell.column.name.toLowerCase().includes(needle),
  )

  const lockFor = (cell: InspectedCell): string | null => {
    // Read-only as a whole is said once, above the grid: the fields are
    // simply readings, with no reason repeated under each.
    if (!row || !editable) return ""
    if (cell.column.generated) return `Computed by the database`
    if (row.state === "deleted") return "This row is marked for deletion"
    if (keyCut)
      return "Only the start of this row's key was loaded, so the row cannot be changed here"
    if (cell.preview && whole[`${row.id}\u0000${cell.column.key}`] === undefined) {
      return "Only the start of this value is loaded"
    }
    return null
  }

  const stage = (cell: InspectedCell, value: EditValue) => {
    if (!row) return
    const action = {
      type: "edit" as const,
      edits: [
        {
          rowId: row.id,
          column: cell.column.key,
          value,
          kind: cell.column.kind,
          origin: row.origin,
        },
      ],
    }
    if (exceedsLimit(changeSet.changes, action)) {
      notify.warning("Too many changes for one apply", {
        description: "Apply or discard what is staged, then go on.",
      })
      return
    }
    changeSet.dispatch(action)
  }

  /** Reads one whole value. `save` hands it to the reader as a file instead of to the field. */
  const load = async (cell: InspectedCell, save: boolean) => {
    if (!row?.origin) return
    const slot = `${row.id}\u0000${cell.column.key}`
    setLoading(slot)
    try {
      const read = await get<CellRead>(`/databases/${id}/cell`, {
        schema: schema || undefined,
        table,
        column: cell.column.name,
        key: JSON.stringify(cellKey(columns, row.origin)),
      })
      if (save) {
        const data =
          read.encoding === "base64"
            ? Uint8Array.from(atob(String(read.value)), (char) => char.charCodeAt(0))
            : read.encoding === "json"
              ? JSON.stringify(read.value)
              : String(read.value ?? "")
        const url = URL.createObjectURL(new Blob([data]))
        const link = document.createElement("a")
        link.href = url
        link.download = `${table}-${cell.column.name}${read.encoding === "base64" ? ".bin" : ".txt"}`
        link.click()
        URL.revokeObjectURL(url)
        return
      }
      const value: CellValue =
        read.encoding === "null"
          ? null
          : read.encoding === "base64"
            ? `\\x${Array.from(atob(String(read.value)), (char) => char.charCodeAt(0).toString(16).padStart(2, "0")).join("")}`
            : read.encoding === "json"
              ? JSON.stringify(read.value)
              : String(read.value)
      setWhole((held) => ({ ...held, [slot]: value }))
    } catch (err) {
      notify.error(`Could not read ${cell.column.name}`, err)
    } finally {
      setLoading(null)
    }
  }

  return (
    <aside aria-label="Row" className="flex min-h-0 min-w-0 flex-1 flex-col">
      <div className="flex h-10 shrink-0 items-center gap-1 border-b border-hairline bg-surface-header pr-1.5 pl-3">
        <span className="min-w-0 truncate text-body font-medium">
          {row ? (rowNumber !== null ? `Row ${grouped(rowNumber)}` : "New row") : "Row"}
        </span>
        {row && row.state !== "clean" && (
          <span className={`shrink-0 font-mono text-hint ${STATE_HUE[row.state]}`}>
            {STATE_WORD[row.state]}
          </span>
        )}
        <span className="flex-1" />
        <IconAction
          label="Previous row"
          className="size-7 max-sm:size-8"
          disabled={!row || row.index <= 0}
          onClick={() => row && onMove(row.index - 1)}
        >
          <ChevronUp />
        </IconAction>
        <IconAction
          label="Next row"
          className="size-7 max-sm:size-8"
          disabled={!row || row.index >= rows - 1}
          onClick={() => row && onMove(row.index + 1)}
        >
          <ChevronDown />
        </IconAction>
        <IconAction label="Hide the row" className="size-7 max-sm:size-8" onClick={onClose}>
          <SidebarRightClose />
        </IconAction>
      </div>

      {!row ? (
        <EmptyState
          className="m-3 border-0"
          title="No row selected"
          description="Press a cell in the grid, or Space on one, to read its row here."
        />
      ) : (
        <>
          {row.cells.length > 8 && (
            <div className="shrink-0 border-b border-hairline p-1.5">
              <SearchInput
                dense
                value={query}
                placeholder="Find a column"
                aria-label="Find a column"
                containerClassName="sm:w-full"
                onChange={(event) => setQuery(event.target.value)}
              />
            </div>
          )}
          <div className="min-h-0 flex-1 divide-y divide-hairline overflow-y-auto">
            {cells?.map((cell) => {
              const slot = `${row.id}\u0000${cell.column.key}`
              const lock = lockFor(cell)
              const value = cell.edited ? cell.value : (whole[slot] ?? cell.value)
              const compared = cell.column.foreignKey ? filterText(readable(value)) : null
              return (
                <RowField
                  // By row as well as by column: a draft the column refused
                  // belongs to the row it was typed on, not to the next one.
                  key={slot}
                  cell={cell}
                  keyWord={cell.column.primaryKey ? (identifies ? "key" : "sort key") : null}
                  inserted={row.state === "inserted"}
                  lock={lock}
                  allowDefault={
                    cell.column.defaultExpr !== undefined &&
                    (defaultOnUpdate || row.state === "inserted")
                  }
                  whole={whole[slot]}
                  onStage={(next) => stage(cell, next)}
                  onRevert={() => changeSet.revertCell(row.id, cell.column.key)}
                >
                  {cell.preview && whole[slot] === undefined && row.origin && (
                    <div className="flex flex-wrap items-center gap-1">
                      <Button
                        type="button"
                        size="xs"
                        variant="outline"
                        pending={loading === slot}
                        onClick={() => void load(cell, false)}
                      >
                        Load the whole value
                        {cell.size !== undefined && (
                          <span className="numeric text-muted-foreground">{bytes(cell.size)}</span>
                        )}
                      </Button>
                    </div>
                  )}
                  {cell.column.kind === "binary" && cell.original !== null && row.origin && (
                    <Button
                      type="button"
                      size="xs"
                      variant="ghost"
                      className="text-muted-foreground"
                      disabled={loading === slot}
                      onClick={() => void load(cell, true)}
                    >
                      <Download />
                      Save the stored value as a file
                    </Button>
                  )}
                  {cell.column.foreignKey && (lock === null || compared !== null) && (
                    <div className="flex min-w-0 flex-wrap items-center gap-1">
                      {lock === null && (
                        <ForeignKeyPicker
                          foreignKey={cell.column.foreignKey}
                          schema={schema}
                          current={compared}
                          onPick={(picked) => stage(cell, picked)}
                        />
                      )}
                      {compared !== null && (
                        <ForeignKeyPeek
                          foreignKey={cell.column.foreignKey}
                          value={compared}
                          schema={schema}
                        />
                      )}
                    </div>
                  )}
                </RowField>
              )
            })}
            {cells?.length === 0 && (
              <p className="px-3 py-6 text-center text-body text-muted-foreground">
                No column is called that.
              </p>
            )}
            {row.origin && detail.referencedBy.length > 0 && !needle && (
              <References schema={schema} row={row} references={detail.referencedBy} />
            )}
          </div>

          <div className="flex shrink-0 flex-wrap items-center gap-1 border-t border-hairline bg-surface-header px-2 py-1.5">
            <Button
              type="button"
              size="xs"
              variant="ghost"
              className="max-sm:h-8"
              onClick={() =>
                void copyText(JSON.stringify(rowRecord(row), null, 2), "Row copied as JSON")
              }
            >
              <Copy />
              Copy as JSON
            </Button>
            {editable && canInsert && row.state !== "deleted" && (
              <Button
                type="button"
                size="xs"
                variant="ghost"
                className="max-sm:h-8"
                onClick={() => {
                  const values = Object.fromEntries(
                    row.cells.map((cell) => [cell.column.key, cell.value]),
                  )
                  changeSet.insertRow(duplicateValues(columns, values, row.origin?.clipped))
                }}
              >
                Duplicate
              </Button>
            )}
            <span className="flex-1" />
            {editable && row.state !== "clean" && row.state !== "inserted" && (
              <Button
                type="button"
                size="xs"
                variant="ghost"
                className="max-sm:h-8"
                onClick={() => changeSet.revertRow(row.id)}
              >
                <RotateCounterClockwise />
                {row.state === "deleted" ? "Restore" : "Revert"}
              </Button>
            )}
            {editable && row.state === "inserted" && (
              <Button
                type="button"
                size="xs"
                variant="ghost"
                className="max-sm:h-8"
                onClick={() => changeSet.revertRow(row.id)}
              >
                <Trash />
                Remove
              </Button>
            )}
            {editable &&
              canDelete &&
              !keyCut &&
              (row.state === "clean" || row.state === "updated") && (
                <Button
                  type="button"
                  size="xs"
                  variant="ghost"
                  className="text-destructive max-sm:h-8"
                  onClick={() => changeSet.deleteRows([{ rowId: row.id, origin: row.origin }])}
                >
                  <Trash />
                  Delete
                </Button>
              )}
          </div>
        </>
      )}
    </aside>
  )
}

/** A staged value as the plain value a comparison takes; unset and DEFAULT are none. */
function readable(value: EditValue | undefined): CellValue {
  return value === undefined || isDefault(value) ? null : value
}

/**
 * The rows elsewhere that point at this one: "3 rows of orders reference this
 * row", each a link to those rows.
 *
 * The count is asked for once the reader has rested on a row for a moment —
 * walking down a page with the arrow keys must not send a count per table per
 * row — and it is asked by the values the row was *read* with: a staged edit
 * to its key has not happened yet, and nothing references the new one.
 */
function References({
  schema,
  row,
  references,
}: {
  schema: string
  row: InspectedRow
  references: DbIncomingForeignKey[]
}) {
  const { id, href } = useDatabase()
  const origin = row.origin!.values
  const listed = useMemo(
    () =>
      references.slice(0, REFERENCES).flatMap((reference) => {
        const filters: Filter[] = []
        for (let i = 0; i < reference.columns.length; i++) {
          const value = filterText(origin[reference.refColumns[i]] ?? null)
          if (value === null) return []
          filters.push({ column: reference.columns[i], op: "eq", value })
        }
        return [{ reference, filters: encodeFilters(filters) }]
      }),
    [references, origin],
  )
  const signature = JSON.stringify(listed.map((entry) => [entry.reference.name, entry.filters]))
  const [counts, setCounts] = useState<{
    signature: string
    values: Record<string, number | null>
  }>()

  useEffect(() => {
    if (listed.length === 0) return
    const controller = new AbortController()
    const timer = setTimeout(() => {
      for (const { reference, filters } of listed) {
        const key = `${reference.schema ?? ""}.${reference.table}.${reference.name}`
        get<{ count: number }>(
          `/databases/${id}/count`,
          { schema: reference.schema ?? (schema || undefined), table: reference.table, filters },
          controller.signal,
        ).then(
          (answer) =>
            setCounts((held) => ({
              signature,
              values: {
                ...(held?.signature === signature ? held.values : {}),
                [key]: answer.count,
              },
            })),
          () => {
            if (controller.signal.aborted) return
            setCounts((held) => ({
              signature,
              values: { ...(held?.signature === signature ? held.values : {}), [key]: null },
            }))
          },
        )
      }
    }, 350)
    return () => {
      clearTimeout(timer)
      controller.abort()
    }
    // `listed` is read through its signature: the same row asks once.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [id, schema, signature])

  if (listed.length === 0) return null
  const known = counts?.signature === signature ? counts.values : {}
  return (
    <section aria-label="Referenced by" className="px-3 py-3">
      <h3 className="eyebrow pb-1.5">Referenced by</h3>
      <ul className="space-y-0.5">
        {listed.map(({ reference, filters }) => {
          const key = `${reference.schema ?? ""}.${reference.table}.${reference.name}`
          const count = known[key]
          return (
            <li key={key}>
              <Link
                href={href("data", {
                  schema: reference.schema ?? (schema || null),
                  table: reference.table,
                  filters,
                })}
                className="group -mx-1.5 flex h-7 items-center gap-1.5 rounded-md px-1.5 focus-ring transition-colors hover:bg-row-hover"
              >
                <KindGlyph kind="table" />
                <span className="min-w-0 truncate font-mono text-xs">{reference.table}</span>
                <span className="min-w-0 truncate font-mono text-hint text-muted-foreground">
                  {reference.columns.join(", ")}
                </span>
                <span className="numeric ml-auto shrink-0 text-xs">
                  {count === undefined ? (
                    <span className="text-muted-foreground">…</span>
                  ) : count === null ? (
                    <span className="text-muted-foreground">not counted</span>
                  ) : (
                    `${grouped(count)} ${count === 1 ? "row" : "rows"}`
                  )}
                </span>
                <ArrowRight className="size-3 shrink-0 text-muted-foreground" />
              </Link>
            </li>
          )
        })}
      </ul>
      {references.length > REFERENCES && (
        <p className="pt-1 text-hint text-muted-foreground">
          and {references.length - REFERENCES} more tables point here
        </p>
      )}
    </section>
  )
}
