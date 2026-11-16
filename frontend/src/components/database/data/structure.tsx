"use client"

import Link from "next/link"
import { ArrowRight, Linked } from "@/components/icons"
import { bytes } from "@/lib/format"
import { cn } from "@/lib/utils"
import { EmptyNote } from "@/components/state"
import { Tag } from "@/components/tag"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { PanelHeader } from "@/components/panel"
import { CodeView } from "@/components/database/kit"
import { ColumnsTable } from "@/components/database/schema/columns-table"
import { useDatabase } from "@/components/database/shell/database-context"
import { KindGlyph, rowObjectKind } from "@/components/database/data/kinds"
import type { DbTableDetail } from "@/components/database/data/types"
import { FactsLine } from "@/components/database/workbench/facts-line"
import { tableFacts } from "@/components/database/workbench/table-facts"

/**
 * What a table is made of, to read: its columns, its indexes, its keys and
 * the tables that point at it.
 *
 * Nothing here changes anything. Changing a column or adding an index is the
 * Schema page's work, where every change shows the statement it will run; the
 * strip above carries the way there. This is the answer to "what is this
 * column again?" while the reader is in the rows.
 */
export function TableStructure({ detail }: { detail: DbTableDetail }) {
  const { href } = useDatabase()

  return (
    <div className="min-h-0 flex-1 overflow-y-auto">
      <div className="space-y-6 p-4">
        {/* What the table amounts to, as one line of words: it was a strip
            of six captioned figures over the columns they were about. */}
        <FactsLine facts={tableFacts(detail, detail.columns.length)} />
        {detail.comment && (
          <p className="max-w-3xl text-body text-muted-foreground">{detail.comment}</p>
        )}

        <Part title="Columns" count={detail.columns.length}>
          {/* Schema's own columns table, read-only here: it reads down a
              column to a block where the pane is narrow, as a phone's is. */}
          <ColumnsTable detail={detail} linkTo="data" />
        </Part>

        <Part title="Indexes" count={detail.indexes.length}>
          {detail.indexes.length === 0 ? (
            <EmptyNote className="py-3 text-left">This {wordOf(detail)} has no index.</EmptyNote>
          ) : (
            <Table>
              <TableHeader>
                <TableRow className="hover:bg-transparent">
                  <TableHead className="h-8">Name</TableHead>
                  <TableHead className="h-8">Columns</TableHead>
                  <TableHead className="h-8">Kind</TableHead>
                  <TableHead className="h-8">Method</TableHead>
                  <TableHead className="h-8 text-right">Size</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {detail.indexes.map((index) => (
                  <TableRow key={index.name} className="hover:bg-transparent">
                    <TableCell className="py-1.5 font-mono font-medium">{index.name}</TableCell>
                    <TableCell className="max-w-96 truncate py-1.5 font-mono">
                      {index.columns.join(", ")}
                      {index.predicate && (
                        <span className="text-muted-foreground"> where {index.predicate}</span>
                      )}
                    </TableCell>
                    <TableCell className="py-1.5">
                      <span className="flex items-center gap-2">
                        {index.primary ? (
                          <Tag>primary</Tag>
                        ) : index.unique ? (
                          <Tag>unique</Tag>
                        ) : null}
                        {index.invalid && <Tag tone="warning">not usable</Tag>}
                      </span>
                    </TableCell>
                    <TableCell className="py-1.5 text-muted-foreground">{index.method}</TableCell>
                    <TableCell className="numeric py-1.5 text-right text-muted-foreground">
                      {index.size !== undefined ? bytes(index.size) : ""}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </Part>

        {(detail.foreignKeys.length > 0 || detail.constraints.length > 0) && (
          <Part
            title="Keys and constraints"
            count={detail.foreignKeys.length + detail.constraints.length}
          >
            <Table>
              <TableHeader>
                <TableRow className="hover:bg-transparent">
                  <TableHead className="h-8">Name</TableHead>
                  <TableHead className="h-8">Kind</TableHead>
                  <TableHead className="h-8">Definition</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {detail.foreignKeys.map((key) => (
                  <TableRow key={`fk:${key.name}`} className="hover:bg-transparent">
                    <TableCell className="py-1.5 font-mono font-medium">{key.name}</TableCell>
                    <TableCell className="py-1.5">
                      <span className="flex items-center gap-1">
                        <Linked aria-hidden className="size-3 text-chart-1" />
                        <Tag>foreign key</Tag>
                      </span>
                    </TableCell>
                    <TableCell className="py-1.5 font-mono">
                      <span className="flex flex-wrap items-center gap-x-1.5">
                        ({key.columns.join(", ")})
                        <ArrowRight className="size-3 text-muted-foreground" />
                        <Link
                          href={href("data", {
                            schema: key.refSchema ?? (detail.schema || null),
                            table: key.refTable,
                          })}
                          className="rounded-sm underline-offset-2 focus-ring hover:underline"
                        >
                          {key.refTable}
                        </Link>
                        ({key.refColumns.join(", ")})
                        {key.onDelete && key.onDelete !== "NO ACTION" && (
                          <span className="font-sans text-muted-foreground">
                            on delete {key.onDelete.toLowerCase()}
                          </span>
                        )}
                        {key.onUpdate && key.onUpdate !== "NO ACTION" && (
                          <span className="font-sans text-muted-foreground">
                            on update {key.onUpdate.toLowerCase()}
                          </span>
                        )}
                      </span>
                    </TableCell>
                  </TableRow>
                ))}
                {detail.constraints.map((constraint, index) => (
                  <TableRow key={`c:${index}:${constraint.name}`} className="hover:bg-transparent">
                    <TableCell className="py-1.5 font-mono font-medium">
                      {constraint.name || <span className="text-muted-foreground">unnamed</span>}
                    </TableCell>
                    <TableCell className="py-1.5">
                      <Tag>{constraint.type}</Tag>
                    </TableCell>
                    <TableCell className="max-w-[40rem] truncate py-1.5 font-mono">
                      {constraint.definition ?? constraint.columns.join(", ")}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </Part>
        )}

        {detail.referencedBy.length > 0 && (
          <Part title="Referenced by" count={detail.referencedBy.length}>
            <ul className="divide-y divide-hairline">
              {detail.referencedBy.map((reference) => (
                <li key={`${reference.schema}.${reference.table}.${reference.name}`}>
                  <Link
                    href={href("data", {
                      schema: reference.schema ?? (detail.schema || null),
                      table: reference.table,
                    })}
                    className="flex min-h-8 items-center gap-2 px-4 py-1.5 focus-ring-inset transition-colors hover:bg-row-hover"
                  >
                    <KindGlyph kind="table" />
                    <span className="font-mono text-xs font-medium">{reference.table}</span>
                    <span className="min-w-0 truncate font-mono text-xs text-muted-foreground">
                      ({reference.columns.join(", ")}) → ({reference.refColumns.join(", ")})
                    </span>
                    <ArrowRight className="ml-auto size-3 shrink-0 text-muted-foreground" />
                  </Link>
                </li>
              ))}
            </ul>
          </Part>
        )}
      </div>
    </div>
  )
}

function wordOf(detail: DbTableDetail): string {
  return rowObjectKind(detail.type) === "table" ? "table" : "view"
}

/** One titled part of the structure: a name, how many, and the rows under a hairline. */
function Part({
  title,
  count,
  children,
}: {
  title: string
  count: number
  children: React.ReactNode
}) {
  return (
    <section aria-label={title} className="min-w-0">
      {/* The one section head; its hairline runs to the same edges as the
          rules of the rows under it. */}
      <PanelHeader plain level={3} title={title} count={count} className="-mx-4 px-4 pb-2" />
      {/* The rows bleed to the section's edge, as a table in a plain panel does. */}
      <div className={cn("-mx-4 min-w-0")}>{children}</div>
    </section>
  )
}

/**
 * The statement that makes the table, as the engine keeps it — or, where the
 * engine keeps none, as the dashboard assembled it from the catalogue, which
 * is said in the pane's name so nobody takes it for the engine's own words.
 */
export function TableDefinition({ detail }: { detail: DbTableDetail }) {
  const { engine } = useDatabase()
  if (!detail.createSql) {
    return (
      <div className="flex min-h-0 flex-1 items-center justify-center p-6">
        <EmptyNote>
          {engine.label} did not say how this {wordOf(detail)} is defined.
        </EmptyNote>
      </div>
    )
  }
  return (
    <CodeView
      code={detail.createSql}
      language={engine.editor}
      filename={`${detail.name}.sql`}
      label={
        <span className="flex min-w-0 items-center gap-2">
          <span className="truncate font-mono">{detail.name}.sql</span>
          {detail.createSqlSource === "generated" && (
            <Tag title="Assembled by the dashboard from the catalogue: the engine keeps no CREATE text">
              generated
            </Tag>
          )}
        </span>
      }
      className="h-auto min-h-0 flex-1 rounded-none border-0"
    />
  )
}
