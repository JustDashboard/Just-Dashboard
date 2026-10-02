"use client"

import { useState } from "react"
import Link from "next/link"
import { ArrowRight, Eye } from "@/components/icons"
import { get } from "@/lib/api"
import { usePoll } from "@/hooks/use-poll"
import { Detail, DetailList } from "@/components/page"
import { EmptyNote, ErrorState, LoadingRows } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import type { GridForeignKey } from "@/components/database/grid"
import { ValueText } from "@/components/database/kit"
import { useDatabase } from "@/components/database/shell/database-context"
import { encodeFilters } from "@/components/database/data/filters"
import { KindGlyph } from "@/components/database/data/kinds"
import type { BrowsePage, Filter } from "@/components/database/data/types"

/** How many of the referenced row's columns the peek draws. */
const PEEK_COLUMNS = 14

/**
 * A look at the row a foreign key points at, without leaving the row that
 * points at it: the referenced row's first columns in a popover, and the way
 * to go there for good.
 *
 * It asks for the row when it is opened and not before — a page of a hundred
 * orders is not a hundred reads of `customers`.
 */
export function ForeignKeyPeek({
  foreignKey,
  value,
  schema,
}: {
  foreignKey: GridForeignKey
  /** The key's value in this row, as the text it is compared as. */
  value: string
  /** This table's schema, which a key that names none points into. */
  schema: string
}) {
  const { href } = useDatabase()
  const [open, setOpen] = useState(false)
  const target = foreignKey.schema ?? schema
  const filters: Filter[] = [{ column: foreignKey.column, op: "eq", value }]
  const there = href("data", {
    schema: target || null,
    table: foreignKey.table,
    filters: encodeFilters(filters),
  })
  return (
    <div className="flex min-w-0 items-center gap-1">
      <Popover open={open} onOpenChange={setOpen}>
        <PopoverTrigger asChild>
          <Button type="button" size="xs" variant="ghost" className="min-w-0 text-muted-foreground">
            <Eye />
            <span className="truncate">
              Peek at <span className="font-mono">{foreignKey.table}</span>
            </span>
          </Button>
        </PopoverTrigger>
        <PopoverContent align="start" className="w-96 max-w-[calc(100vw-2rem)] p-0">
          {open && <Peek foreignKey={foreignKey} schema={target} filters={filters} there={there} />}
        </PopoverContent>
      </Popover>
      <Button type="button" size="xs" variant="ghost" className="text-muted-foreground" asChild>
        <Link href={there}>
          Open
          <ArrowRight />
        </Link>
      </Button>
    </div>
  )
}

function Peek({
  foreignKey,
  schema,
  filters,
  there,
}: {
  foreignKey: GridForeignKey
  schema: string
  filters: Filter[]
  there: string
}) {
  const { id } = useDatabase()
  const query = encodeFilters(filters)
  const row = usePoll(
    (signal) =>
      get<BrowsePage>(
        `/databases/${id}/browse`,
        {
          schema: schema || undefined,
          table: foreignKey.table,
          limit: 1,
          filters: query,
          cellLimit: 512,
        },
        signal,
      ),
    0,
    [id, schema, foreignKey.table, query],
  )
  const page = row.data
  const values = page?.rows[0]
  return (
    <>
      <div className="flex items-center gap-1.5 border-b border-hairline bg-surface-header px-3 py-2">
        <KindGlyph kind="table" />
        <span className="min-w-0 flex-1 truncate font-mono text-xs font-medium">
          {schema ? `${schema}.` : ""}
          {foreignKey.table}
        </span>
        <Button size="xs" variant="outline" asChild>
          <Link href={there}>Open the row</Link>
        </Button>
      </div>
      <div className="max-h-80 overflow-y-auto p-3">
        {row.error ? (
          <ErrorState error={row.error} onRetry={row.refresh} />
        ) : !page ? (
          <LoadingRows rows={5} />
        ) : !values ? (
          <EmptyNote className="py-4">
            No row of {foreignKey.table} has this {foreignKey.column}. The key points at nothing.
          </EmptyNote>
        ) : (
          <DetailList>
            {page.columns.slice(0, PEEK_COLUMNS).map((name, index) => (
              <Detail key={name} label={<span className="font-mono">{name}</span>}>
                <ValueText
                  value={values[index]}
                  type={page.kinds?.[index] ?? page.types[index]}
                  clamp={160}
                  className="break-words"
                />
              </Detail>
            ))}
            {page.columns.length > PEEK_COLUMNS && (
              <Detail label="">
                <span className="text-muted-foreground">
                  and {page.columns.length - PEEK_COLUMNS} more columns
                </span>
              </Detail>
            )}
          </DetailList>
        )}
      </div>
    </>
  )
}
