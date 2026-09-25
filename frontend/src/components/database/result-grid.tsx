"use client"

import { useState } from "react"
import {
  AcronymJson,
  ArrowDown,
  ArrowUp,
  ArrowUpDown,
  Copy,
  External,
  MoreHorizontal,
  Pencil,
  Trash,
} from "@/components/icons"
import { plural } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { DbForeignKey, QueryResult } from "@/lib/types"
import type { TableReference } from "@/components/database/browse-tab"
import { Modal } from "@/components/modal"
import { Well } from "@/components/panel"
import { DimActions, IconAction } from "@/components/icon-action"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import {
  stickyTableHeader,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { copyText } from "@/lib/clipboard"

/**
 * The one result grid every database view renders through, so a query result, a
 * table browse and an edited row all read the same. It keeps the read-only shape
 * from the original page and adds the two things a browser needs to become an
 * editor: per-row actions (when the caller can identify a row by primary key)
 * and a click-to-expand cell, because a JSON blob or a paragraph of text
 * truncated to a table cell is the one value you most need to see in full.
 */
export function ResultGrid({
  result,
  onEdit,
  onDelete,
  onDuplicate,
  onCopySQL,
  sort,
  onSort,
  foreignKeys,
  onFollow,
  references,
  onFollowReference,
  selection,
  onSelectionChange,
  className,
  maxHeightClass = "max-h-[calc(100svh-22rem)]",
  emptyTitle = "No rows",
  emptyDescription,
}: {
  result: QueryResult
  onEdit?: (row: Record<string, unknown>) => void
  onDelete?: (row: Record<string, unknown>) => void
  /**
   * Open the insert form pre-filled from this row. The commonest way anybody
   * creates a record by hand is "the same as that one but different", and
   * retyping eleven columns to change two is how a typo gets into the copy.
   */
  onDuplicate?: (row: Record<string, unknown>) => void
  /** Render rows as INSERT statements for this engine, server-side. */
  onCopySQL?: (rows: Record<string, unknown>[]) => void
  /** The column the server is ordering by, when the caller supports sorting. */
  sort?: { column: string; desc: boolean } | null
  onSort?: (column: string) => void
  /** Outgoing foreign keys, so a referencing value becomes a link. */
  foreignKeys?: DbForeignKey[]
  onFollow?: (fk: DbForeignKey, value: unknown) => void
  /**
   * The tables whose rows point at this one. A grid can always say what a row
   * points at, because the key is in the row; saying what points *at* it needs
   * the rest of the schema, and that is the question an operator actually
   * arrives with — "this customer is about to be deleted, what is attached to
   * them". One menu item per referencing table.
   */
  references?: TableReference[]
  onFollowReference?: (ref: TableReference, row: Record<string, unknown>) => void
  /** Row indices selected for a bulk action, when the caller supports one. */
  selection?: Set<number>
  onSelectionChange?: (next: Set<number>) => void
  className?: string
  maxHeightClass?: string
  /**
   * What to say when the query succeeded and matched nothing. The columns are
   * still worth rendering — they are how you read the shape of what you asked
   * for — but headers over an empty void look like a component that failed to
   * load rather than a table that genuinely has none.
   */
  emptyTitle?: string
  emptyDescription?: string
}) {
  const [detail, setDetail] = useState<{ column: string; value: unknown } | null>(null)
  const hasActions = Boolean(onEdit || onDelete || onDuplicate || onCopySQL)
  const selectable = Boolean(selection && onSelectionChange)

  // Single-column foreign keys become links. A composite key is deliberately
  // left alone: following one means matching several columns at once, and a
  // link on just the first of them would go somewhere wrong.
  const fkByColumn = new Map<string, DbForeignKey>()
  for (const fk of foreignKeys ?? []) {
    if (fk.columns.length === 1) fkByColumn.set(fk.columns[0], fk)
  }

  const allSelected = selectable && selection!.size > 0 && selection!.size === result.rows.length
  const toggleAll = () => {
    if (!onSelectionChange) return
    onSelectionChange(allSelected ? new Set() : new Set(result.rows.map((_, i) => i)))
  }
  const toggleRow = (i: number) => {
    if (!selection || !onSelectionChange) return
    const next = new Set(selection)
    if (next.has(i)) {
      next.delete(i)
    } else {
      next.add(i)
    }
    onSelectionChange(next)
  }

  if (result.columns.length === 0) {
    return (
      <p className="p-4 text-body text-muted-foreground">
        {plural(result.rowsAffected, "row")} affected in {result.duration}.
      </p>
    )
  }

  const rowRecord = (row: unknown[]): Record<string, unknown> => {
    const rec: Record<string, unknown> = {}
    result.columns.forEach((c, i) => {
      rec[c] = row[i]
    })
    return rec
  }

  return (
    <>
      <Table containerClassName={cn(maxHeightClass, className)}>
        <TableHeader className={stickyTableHeader}>
          <TableRow>
            {selectable && (
              <TableHead className="w-9">
                <Checkbox
                  checked={allSelected}
                  onCheckedChange={toggleAll}
                  aria-label="Select every row on this page"
                />
              </TableHead>
            )}
            {hasActions && <TableHead className="w-[5.5rem]" />}
            {result.columns.map((col, i) => (
              <TableHead key={col} className="whitespace-nowrap">
                {onSort ? (
                  <button
                    onClick={() => onSort(col)}
                    className="group/sort inline-flex items-center gap-1 hover:text-foreground"
                    title={`Sort by ${col}`}
                  >
                    {col}
                    <SortIcon active={sort?.column === col} desc={sort?.desc ?? false} />
                  </button>
                ) : (
                  col
                )}
                {result.types[i] && (
                  <span className="ml-1 text-micro font-normal text-muted-foreground/70 normal-case">
                    {result.types[i].toLowerCase()}
                  </span>
                )}
              </TableHead>
            ))}
          </TableRow>
        </TableHeader>
        <TableBody>
          {result.rows.length === 0 && (
            <TableRow className="hover:bg-transparent">
              <TableCell
                colSpan={result.columns.length + (selectable ? 1 : 0) + (hasActions ? 1 : 0)}
                className="py-10 text-center"
              >
                <p className="text-body font-medium text-foreground">{emptyTitle}</p>
                {emptyDescription && (
                  <p className="mx-auto mt-1 max-w-sm text-xs leading-relaxed text-muted-foreground">
                    {emptyDescription}
                  </p>
                )}
              </TableCell>
            </TableRow>
          )}
          {result.rows.map((row, i) => (
            <TableRow key={i} className="group" data-selected={selection?.has(i) || undefined}>
              {selectable && (
                <TableCell className="w-9">
                  <Checkbox
                    checked={selection!.has(i)}
                    onCheckedChange={() => toggleRow(i)}
                    aria-label={`Select row ${i + 1}`}
                  />
                </TableCell>
              )}
              {hasActions && (
                <TableCell className="w-[5.5rem]">
                  {/* The verbs have a column of their own here, so they are
                      always drawn and merely quiet (§6). Revealed on hover,
                      the reserved column was a hundred pixels of nothing down
                      every row until one of them sprouted buttons. */}
                  <DimActions>
                    {onEdit && (
                      <IconAction label="Edit row" onClick={() => onEdit(rowRecord(row))}>
                        <Pencil />
                      </IconAction>
                    )}
                    <DropdownMenu>
                      {/* A menu trigger cannot be an `IconAction`: that renders a
                          tooltip root, which `asChild` has no element to hand the
                          trigger props to. The label has to be the ARIA one. */}
                      <DropdownMenuTrigger asChild>
                        <Button
                          size="icon-sm"
                          variant="ghost"
                          aria-label="More row actions"
                          className="[&_svg:not([class*='size-'])]:size-3.5"
                        >
                          <MoreHorizontal />
                        </Button>
                      </DropdownMenuTrigger>
                      <DropdownMenuContent align="start" className="w-52">
                        <DropdownMenuItem onClick={() => copyJSON(rowRecord(row))}>
                          <AcronymJson className="size-3.5" />
                          Copy as JSON
                        </DropdownMenuItem>
                        {onCopySQL && (
                          <DropdownMenuItem onClick={() => onCopySQL([rowRecord(row)])}>
                            <Copy className="size-3.5" />
                            Copy as INSERT
                          </DropdownMenuItem>
                        )}
                        {onDuplicate && (
                          <DropdownMenuItem onClick={() => onDuplicate(rowRecord(row))}>
                            <Copy className="size-3.5" />
                            Duplicate row…
                          </DropdownMenuItem>
                        )}
                        {onFollowReference && references && references.length > 0 && (
                          <>
                            <DropdownMenuSeparator />
                            <DropdownMenuLabel className="text-hint font-medium text-muted-foreground">
                              Referenced by
                            </DropdownMenuLabel>
                            {references.map((ref) => (
                              <DropdownMenuItem
                                key={`${ref.table}.${ref.fk.name}`}
                                onClick={() => onFollowReference(ref, rowRecord(row))}
                              >
                                <External className="size-3.5" />
                                <span className="min-w-0 flex-1 truncate">{ref.table}</span>
                                <span className="max-w-[45%] shrink truncate font-mono text-hint text-muted-foreground">
                                  {ref.fk.columns[0]}
                                </span>
                              </DropdownMenuItem>
                            ))}
                          </>
                        )}
                        {onDelete && (
                          <>
                            <DropdownMenuSeparator />
                            <DropdownMenuItem
                              variant="destructive"
                              onClick={() => onDelete(rowRecord(row))}
                            >
                              <Trash className="size-3.5" />
                              Delete row…
                            </DropdownMenuItem>
                          </>
                        )}
                      </DropdownMenuContent>
                    </DropdownMenu>
                  </DimActions>
                </TableCell>
              )}
              {row.map((cell, j) => {
                const fk = fkByColumn.get(result.columns[j])
                const followable = fk && onFollow && cell !== null && cell !== undefined
                return (
                  <TableCell key={j} className="max-w-xs p-0 font-mono text-xs">
                    <span className="flex min-w-0 items-center gap-1 pr-2">
                      {/* The value is a real button, not a `<td>` with an
                          onClick. A cell handler is reachable by pointer only,
                          and the viewer behind it is the one way to read a JSON
                          blob or a paragraph that the column has truncated. */}
                      <button
                        type="button"
                        onClick={() => setDetail({ column: result.columns[j], value: cell })}
                        // An empty string is a value, and it renders as nothing —
                        // which leaves a focusable control a screen reader has no
                        // name for. It is the one cell that has to say what it is
                        // rather than show it.
                        aria-label={cell === "" ? `${result.columns[j]}: empty` : undefined}
                        className="min-w-0 flex-1 truncate px-4 py-3 text-left focus-ring-inset hover:bg-menu-hover"
                      >
                        <CellValue value={cell} />
                      </button>
                      {followable && (
                        <button
                          type="button"
                          onClick={() => onFollow(fk, cell)}
                          aria-label={`Open ${fk.refTable} where ${fk.refColumns[0]} = ${String(cell)}`}
                          title={`Open ${fk.refTable} where ${fk.refColumns[0]} = ${String(cell)}`}
                          className="shrink-0 rounded-sm p-1 text-muted-foreground/60 focus-ring hover:text-primary"
                        >
                          <External className="size-3" />
                        </button>
                      )}
                    </span>
                  </TableCell>
                )
              })}
            </TableRow>
          ))}
        </TableBody>
      </Table>

      <Modal
        open={detail !== null}
        onOpenChange={(o) => !o && setDetail(null)}
        size="lg"
        title={<span className="font-mono">{detail?.column}</span>}
      >
        {detail && <CellDetail value={detail.value} />}
      </Modal>
    </>
  )
}

function SortIcon({ active, desc }: { active: boolean; desc: boolean }) {
  if (!active)
    return (
      <ArrowUpDown className="size-3 opacity-0 transition-opacity group-hover/sort:opacity-50" />
    )
  return desc ? (
    <ArrowDown className="size-3 text-primary" />
  ) : (
    <ArrowUp className="size-3 text-primary" />
  )
}

function CellValue({ value }: { value: unknown }) {
  if (value === null || value === undefined)
    return <span className="text-muted-foreground italic">null</span>
  if (typeof value === "object") return <>{JSON.stringify(value)}</>
  if (typeof value === "boolean") return <span className="text-primary">{String(value)}</span>
  return <>{String(value)}</>
}

function CellDetail({ value }: { value: unknown }) {
  const text =
    value === null || value === undefined
      ? ""
      : typeof value === "object"
        ? JSON.stringify(value, null, 2)
        : String(value)
  const isNull = value === null || value === undefined
  return (
    <div className="space-y-2">
      <Well className="max-h-[60vh]">
        {isNull ? (
          <span className="text-body text-muted-foreground italic">null</span>
        ) : (
          <pre className="break-words whitespace-pre-wrap">{text}</pre>
        )}
      </Well>
      {!isNull && (
        <Button size="sm" variant="outline" onClick={() => void copyText(text, "Copied")}>
          <Copy className="size-3.5" />
          Copy value
        </Button>
      )}
    </div>
  )
}

/**
 * Copying a row as JSON is done here rather than on the server: it is a
 * transformation of what is already on the screen, with no engine-specific
 * quoting to get right. Copying it as an INSERT is the opposite case and goes
 * through the server, where the one implementation of each engine's syntax
 * lives.
 */
function copyJSON(row: Record<string, unknown>) {
  void copyText(JSON.stringify(row, null, 2), "Copied row as JSON")
}
