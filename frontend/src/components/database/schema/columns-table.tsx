"use client"

import Link from "next/link"
import { Fingerprint, Key, Linked } from "@/components/icons"
import { cn } from "@/lib/utils"
import { useColumnWidth } from "@/hooks/use-column-width"
import { Tag } from "@/components/tag"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { VerbActions, type Verb } from "@/components/verbs"
import type { DbColumn, DbTableDetail } from "@/components/database/data/types"
import { useDatabase } from "@/components/database/shell/database-context"
import { tableParams } from "@/components/database/schema/address"
import {
  COLUMNS_NARROW,
  columnDefault,
  columnMarks,
} from "@/components/database/schema/columns-model"

const HEAD = "h-8 px-3 first:pl-4 last:pr-4"
const CELL = "px-3 py-1 first:pl-4 last:pr-4"
/** A row's controls at the row's own height, so a line of the table is a line of text. */
const ROW_ACTIONS = "justify-end [&_button]:size-6"

/** What can be done to each column, where the reader may change the table. */
export type ColumnActions = { verbsFor: (column: DbColumn) => Verb[] }

/**
 * A table's columns: # · Name · Type · NULL · Default · Key, with what each
 * is for under its name and the marks of a key — the primary key, a unique
 * index, a number the engine counts, a computed value, a foreign key as the
 * way to the table it points at.
 *
 * Data's Structure tab and Schema's Columns tab drew this same table twice,
 * and Data's copy had no narrow shape: on a phone it was six columns that
 * scrolled sideways. One table now, which measures the width it is given
 * (not the window's) and reads down, a column to a block, under 640px, and
 * drops the position under 52rem. `actions` are Schema's edits; without them
 * it is the read-only table Data shows.
 *
 * A foreign key leads to the table it points at on the page it is drawn on
 * (`linkTo`): the rows of it from Data, its definition from Schema.
 */
export function ColumnsTable({
  detail,
  actions,
  linkTo = "schema",
  className,
}: {
  detail: DbTableDetail
  actions?: ColumnActions
  linkTo?: "data" | "schema"
  className?: string
}) {
  const { engine, href } = useDatabase()
  const [box, width] = useColumnWidth<HTMLDivElement>()
  const narrow = width > 0 && width < COLUMNS_NARROW
  const marksOf = columnMarks(detail, engine.capabilities.rowIdentity)
  const acts = actions !== undefined && detail.columns.some((c) => actions.verbsFor(c).length > 0)

  const marks = (column: DbColumn) => {
    const mark = marksOf(column)
    const reference = mark.reference
    return (
      <>
        {mark.key && (
          <span className="flex items-center gap-1">
            <Key aria-hidden className="size-3 text-chart-2" />
            <Tag>{mark.key}</Tag>
          </span>
        )}
        {mark.unique && (
          <span className="flex items-center gap-1">
            <Fingerprint aria-hidden className="size-3 text-muted-foreground" />
            <Tag>unique</Tag>
          </span>
        )}
        {column.identity && <Tag title={column.identity}>numbered</Tag>}
        {column.generated && (
          <Tag title="Its value is its expression: it is changed by rewriting that, in Query">
            computed
          </Tag>
        )}
        {reference && (
          <Link
            href={
              linkTo === "data"
                ? href("data", {
                    schema: reference.schema || null,
                    table: reference.table,
                  })
                : href("schema", tableParams(reference.schema, reference.table))
            }
            className="flex items-center gap-1 rounded-sm font-mono text-xs text-muted-foreground focus-ring transition-colors hover:text-foreground"
          >
            <Linked aria-hidden className="size-3 text-chart-1" />
            {/* A key into another schema says which: the name alone would be
                read as this schema's table of that name. */}
            {reference.schema && reference.schema !== detail.schema ? `${reference.schema}.` : ""}
            {reference.table}.{reference.column}
          </Link>
        )}
      </>
    )
  }

  return (
    <div ref={box} data-slot="columns-table" className={cn("@container min-w-0", className)}>
      {narrow ? (
        <ul aria-label="Columns" className="divide-y divide-hairline border-b border-hairline">
          {detail.columns.map((column) => {
            const verbs = actions?.verbsFor(column) ?? []
            const said = columnDefault(column)
            return (
              <li key={column.name} className="group flex items-start gap-2 py-2 pr-2 pl-4">
                <div className="min-w-0 flex-1 space-y-1">
                  <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
                    <span className="font-mono text-xs font-medium">{column.name}</span>
                    <Tag mono className="block max-w-full truncate" title={column.type}>
                      {column.type}
                    </Tag>
                  </div>
                  <div className="flex min-w-0 flex-wrap items-center gap-x-2.5 gap-y-1 text-xs text-muted-foreground">
                    <span>{column.nullable ? "takes NULL" : "not null"}</span>
                    {said && (
                      <span className="max-w-full min-w-0 truncate font-mono" title={said}>
                        {column.generated ? "" : "default "}
                        {said}
                      </span>
                    )}
                    {marks(column)}
                  </div>
                  {column.comment && (
                    <p className="text-hint text-muted-foreground">{column.comment}</p>
                  )}
                </div>
                {verbs.length > 0 && (
                  <VerbActions verbs={verbs} menuLabel={`Actions for ${column.name}`} />
                )}
              </li>
            )
          })}
        </ul>
      ) : (
        <Table>
          <TableHeader>
            <TableRow className="hover:bg-transparent">
              <TableHead className={cn(HEAD, "w-10 pr-0 text-right @max-[52rem]:hidden")}>
                #
              </TableHead>
              <TableHead className={HEAD}>Name</TableHead>
              <TableHead className={HEAD}>Type</TableHead>
              <TableHead className={HEAD}>NULL</TableHead>
              <TableHead className={HEAD}>Default</TableHead>
              <TableHead className={HEAD}>Key</TableHead>
              {acts && (
                <TableHead className={cn(HEAD, "w-px")}>
                  <span className="sr-only">Actions</span>
                </TableHead>
              )}
            </TableRow>
          </TableHeader>
          <TableBody>
            {detail.columns.map((column) => {
              const verbs = actions?.verbsFor(column) ?? []
              const said = columnDefault(column)
              return (
                <TableRow
                  key={column.name}
                  className={cn("group", !actions && "hover:bg-transparent")}
                >
                  <TableCell
                    className={cn(
                      CELL,
                      "numeric pr-0 text-right text-muted-foreground @max-[52rem]:hidden",
                    )}
                  >
                    {column.position}
                  </TableCell>
                  <TableCell className={CELL}>
                    <span className="font-mono font-medium">{column.name}</span>
                    {/* What the column is for reads under its name: as a column
                        of its own it was the one that fell off the right edge
                        at an ordinary width. */}
                    {column.comment && (
                      <p
                        className="max-w-80 truncate text-hint text-muted-foreground"
                        title={column.comment}
                      >
                        {column.comment}
                      </p>
                    )}
                  </TableCell>
                  <TableCell className={CELL}>
                    <Tag mono className="block max-w-48 truncate" title={column.type}>
                      {column.type}
                    </Tag>
                  </TableCell>
                  <TableCell className={cn(CELL, "text-muted-foreground")}>
                    {column.nullable ? "yes" : "no"}
                  </TableCell>
                  <TableCell
                    className={cn(
                      CELL,
                      "max-w-64 truncate font-mono text-muted-foreground @4xl:max-w-[26rem]",
                    )}
                    title={said}
                  >
                    {said}
                  </TableCell>
                  <TableCell className={CELL}>
                    <span className="flex flex-wrap items-center gap-x-2.5 gap-y-1">
                      {marks(column)}
                    </span>
                  </TableCell>
                  {acts && (
                    <TableCell className={cn(CELL, "pl-0")}>
                      {verbs.length > 0 && (
                        <VerbActions
                          verbs={verbs}
                          mode="dim"
                          menuLabel={`Actions for ${column.name}`}
                          className={ROW_ACTIONS}
                        />
                      )}
                    </TableCell>
                  )}
                </TableRow>
              )
            })}
          </TableBody>
        </Table>
      )}
    </div>
  )
}
