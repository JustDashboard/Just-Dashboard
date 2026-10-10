"use client"

import { useMemo, useState } from "react"
import Link from "next/link"
import {
  ArrowRight,
  Code,
  Cross,
  External,
  Eye,
  Fingerprint,
  GridSquare,
  Key,
  Linked,
  Notes,
  Table as TableIcon,
} from "@/components/icons"
import { cn, ringSafeScroll } from "@/lib/utils"
import { IconAction } from "@/components/icon-action"
import { DetailList, Detail, SearchInput } from "@/components/page"
import { PaneHeader } from "@/components/panel"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import {
  DIAGRAM_COLORS,
  type DiagramColor,
  type DiagramDocument,
} from "@/components/database/diagram/document"
import type { DbGraphEdge, DbGraphTable, DbSchemaGraph } from "@/components/database/diagram/types"

/**
 * The column beside the canvas: what the schema amounts to and every table in
 * it while nothing is focused; the focused table's relations in both
 * directions, its columns and its annotations once something is.
 *
 * A table is named with its schema wherever the picture holds more than one,
 * and every list here is keyed by the table's id — so a second `orders` is a
 * second row, with its own checkbox, its own colour and its own note.
 */
export function Inspector({
  graph,
  doc,
  hidden,
  qualified,
  focused,
  relation,
  canQuery,
  onFocus,
  onToggleHidden,
  onShowAll,
  onColor,
  onNote,
  onOpenTable,
  onOpenStructure,
  onQuery,
  hrefOutside,
  onClose,
}: {
  graph: DbSchemaGraph
  doc: DiagramDocument
  hidden: Set<string>
  /** The picture holds several schemas: names are said with theirs. */
  qualified: boolean
  focused?: DbGraphTable
  relation?: DbGraphEdge
  canQuery: boolean
  onFocus: (id: string) => void
  onToggleHidden: (id: string, visible: boolean) => void
  onShowAll: () => void
  onColor: (id: string, color: DiagramColor | null) => void
  onNote: (table: DbGraphTable) => void
  onOpenTable: (table: DbGraphTable) => void
  onOpenStructure: (table: DbGraphTable) => void
  onQuery: (table: DbGraphTable) => void
  /** Where a table that is not in the picture can be read: its page in Schema. */
  hrefOutside: (schema: string, table: string) => string
  onClose: () => void
}) {
  const [filter, setFilter] = useState("")
  const byId = useMemo(() => new Map(graph.tables.map((t) => [t.id, t])), [graph.tables])
  // A table is said with its schema where the picture holds several — and
  // always when it is in another schema than the table being read, which is
  // the case of a key that leaves a one-schema picture.
  const label = (id: string, schema: string | undefined, name: string) => {
    const table = byId.get(id)
    const of = table?.schema ?? schema ?? ""
    const outside = focused !== undefined && of !== "" && of !== focused.schema
    return (qualified || outside) && of ? `${of}.${table?.name ?? name}` : (table?.name ?? name)
  }
  const columns = graph.tables.reduce((n, t) => n + t.columns.length, 0)
  const outgoing = focused ? graph.edges.filter((e) => e.from === focused.id) : []
  const incoming = focused ? graph.edges.filter((e) => e.to === focused.id) : []
  const sorted = useMemo(() => {
    const q = filter.trim().toLowerCase()
    return [...graph.tables]
      .filter((t) => !q || t.name.toLowerCase().includes(q) || t.schema.toLowerCase().includes(q))
      .sort((a, b) => a.name.localeCompare(b.name) || a.schema.localeCompare(b.schema))
  }, [graph.tables, filter])
  const shownCount = graph.tables.filter((t) => !hidden.has(t.id)).length
  const hiddenCount = graph.tables.length - shownCount

  return (
    <aside
      aria-label="Diagram inspector"
      className="flex min-h-0 min-w-0 flex-col border-t border-hairline lg:border-t-0 lg:border-l"
    >
      <PaneHeader className="h-10 justify-between gap-2">
        <span className="min-w-0 truncate text-body font-medium">
          {focused ? (
            <span className="font-mono text-xs">
              {qualified && focused.schema && (
                <span className="font-normal text-muted-foreground">{focused.schema}.</span>
              )}
              {focused.name}
            </span>
          ) : relation ? (
            "Relation"
          ) : (
            "In this picture"
          )}
        </span>
        <IconAction label="Close the inspector" className="size-7" onClick={onClose}>
          <Cross />
        </IconAction>
      </PaneHeader>
      <div className={cn("min-h-0 flex-1 overflow-y-auto", ringSafeScroll)}>
        {focused ? (
          <div className="space-y-4 p-3">
            <DetailList>
              {focused.schema && (
                <Detail label="Schema">
                  <span className="font-mono">{focused.schema}</span>
                </Detail>
              )}
              <Detail label="Rows">
                <span className="numeric">
                  {focused.rows > 0 ? `~${focused.rows.toLocaleString("en-US")}` : "not counted"}
                </span>
              </Detail>
              <Detail label="Columns">
                <span className="numeric">{focused.columns.length}</span>
              </Detail>
              {focused.type && focused.type.toLowerCase() !== "table" && (
                <Detail label="Kind">{focused.type}</Detail>
              )}
            </DetailList>
            {focused.comment && (
              <p className="text-hint leading-relaxed text-muted-foreground">{focused.comment}</p>
            )}

            <div className="flex flex-wrap gap-1.5">
              <Button size="xs" variant="outline" onClick={() => onOpenTable(focused)}>
                <GridSquare />
                Data
              </Button>
              <Button size="xs" variant="outline" onClick={() => onOpenStructure(focused)}>
                <TableIcon />
                Schema
              </Button>
              {canQuery && (
                <Button size="xs" variant="outline" onClick={() => onQuery(focused)}>
                  <Code />
                  Query
                </Button>
              )}
            </div>

            <div className="space-y-1.5">
              <div className="flex items-center justify-between">
                <p className="eyebrow">Note</p>
                <Button size="xs" variant="ghost" onClick={() => onNote(focused)}>
                  <Notes />
                  {doc.notes[focused.id] ? "Edit" : "Add"}
                </Button>
              </div>
              {doc.notes[focused.id] ? (
                <p className="text-hint leading-relaxed whitespace-pre-wrap">
                  {doc.notes[focused.id]}
                </p>
              ) : (
                <p className="text-hint text-muted-foreground">
                  What this table is for, kept with the diagram.
                </p>
              )}
            </div>

            <div className="space-y-1.5">
              <p className="eyebrow">Colour</p>
              <div role="group" aria-label="Colour" className="flex flex-wrap gap-1.5">
                <button
                  type="button"
                  aria-label="No colour"
                  aria-pressed={!doc.colors[focused.id]}
                  onClick={() => onColor(focused.id, null)}
                  className={cn(
                    "size-5 rounded-full border focus-ring transition-colors",
                    !doc.colors[focused.id]
                      ? "border-foreground"
                      : "border-border hover:border-border-strong",
                  )}
                />
                {DIAGRAM_COLORS.map((c) => (
                  <button
                    key={c}
                    type="button"
                    aria-label={c}
                    aria-pressed={doc.colors[focused.id] === c}
                    onClick={() => onColor(focused.id, c)}
                    className={cn(
                      "size-5 rounded-full border-2 focus-ring transition-colors",
                      doc.colors[focused.id] === c ? "border-foreground" : "border-transparent",
                    )}
                    style={{ background: `var(--tag-${c})` }}
                  />
                ))}
              </div>
            </div>

            <RelationList
              title="References"
              empty="Points at nothing."
              edges={outgoing}
              other={(e) => e.to}
              known={(e) => byId.has(e.to)}
              describe={(e) =>
                `${e.fromColumn} → ${label(e.to, e.toSchema, e.toTable)}.${e.toColumn}`
              }
              outside={(e) => hrefOutside(e.toSchema ?? focused.schema, e.toTable)}
              onFocus={onFocus}
            />
            <RelationList
              title="Referenced by"
              empty="Nothing points here."
              edges={incoming}
              other={(e) => e.from}
              known={(e) => byId.has(e.from)}
              describe={(e) =>
                `${label(e.from, e.fromSchema, e.fromTable)}.${e.fromColumn} → ${e.toColumn}`
              }
              outside={(e) => hrefOutside(e.fromSchema ?? focused.schema, e.fromTable)}
              onFocus={onFocus}
            />

            <div className="space-y-1.5">
              <p className="eyebrow">Columns</p>
              <ul className="divide-y divide-hairline">
                {focused.columns.map((c) => (
                  <li key={c.name} className="flex items-center gap-2 py-1 text-hint">
                    {c.primaryKey ? (
                      <Key aria-hidden className="size-3 shrink-0 text-chart-2" />
                    ) : c.foreignKey ? (
                      <Linked aria-hidden className="size-3 shrink-0 text-chart-1" />
                    ) : c.unique ? (
                      <Fingerprint
                        aria-hidden
                        className="size-3 shrink-0 text-muted-foreground/60"
                      />
                    ) : (
                      <span className="size-3 shrink-0" />
                    )}
                    <span className="min-w-0 flex-1 truncate font-mono">{c.name}</span>
                    <span className="shrink-0 truncate font-mono text-micro text-muted-foreground">
                      {c.type.toLowerCase()}
                      {c.nullable ? "" : " · not null"}
                    </span>
                  </li>
                ))}
              </ul>
            </div>
          </div>
        ) : relation ? (
          <div className="space-y-4 p-3">
            <DetailList>
              <Detail label="Constraint">
                <span className="font-mono">{relation.name || "unnamed"}</span>
              </Detail>
              <Detail label="From">
                <span className="font-mono">
                  {label(relation.from, relation.fromSchema, relation.fromTable)}.
                  {relation.fromColumn}
                </span>
              </Detail>
              <Detail label="To">
                <span className="font-mono">
                  {label(relation.to, relation.toSchema, relation.toTable)}.{relation.toColumn}
                </span>
              </Detail>
              <Detail label="Cardinality">
                {relation.cardinality === "one-to-one" ? "one to one" : "many to one"}
              </Detail>
              <Detail label="On delete">{relation.onDelete?.toLowerCase() || "no action"}</Detail>
              {relation.onUpdate && relation.onUpdate !== "NO ACTION" && (
                <Detail label="On update">{relation.onUpdate.toLowerCase()}</Detail>
              )}
            </DetailList>
            <div className="flex flex-wrap gap-1.5">
              <Button size="xs" variant="outline" onClick={() => onFocus(relation.from)}>
                {relation.fromTable}
                <ArrowRight />
              </Button>
              <Button size="xs" variant="outline" onClick={() => onFocus(relation.to)}>
                {relation.toTable}
                <ArrowRight />
              </Button>
            </div>
          </div>
        ) : (
          <div className="space-y-4 p-3">
            <DetailList>
              <Detail label="Tables">
                <span className="numeric">
                  {shownCount}
                  {hiddenCount > 0 && (
                    <span className="text-muted-foreground"> shown · {hiddenCount} hidden</span>
                  )}
                </span>
              </Detail>
              <Detail label="Relations">
                <span className="numeric">{graph.edges.length}</span>
              </Detail>
              <Detail label="Columns">
                <span className="numeric">{columns}</span>
              </Detail>
            </DetailList>
            <div className="space-y-1.5">
              <div className="flex min-h-6 items-center justify-between gap-2">
                <p className="eyebrow">Tables</p>
                {hiddenCount > 0 && (
                  <Button size="xs" variant="ghost" onClick={onShowAll}>
                    <Eye />
                    Show all
                  </Button>
                )}
              </div>
              <SearchInput
                dense
                value={filter}
                onChange={(e) => setFilter(e.target.value)}
                placeholder="Find a table"
                aria-label="Find a table in the list"
                containerClassName="w-full sm:w-full"
              />
              <ul className="divide-y divide-hairline">
                {sorted.map((t) => {
                  const visible = !hidden.has(t.id)
                  const name = qualified && t.schema ? `${t.schema}.${t.name}` : t.name
                  return (
                    <li key={t.id} className="flex items-center gap-2 py-1">
                      <Checkbox
                        checked={visible}
                        onCheckedChange={(v) => onToggleHidden(t.id, Boolean(v))}
                        aria-label={`Show ${name} on the diagram`}
                      />
                      <button
                        type="button"
                        onClick={() => visible && onFocus(t.id)}
                        disabled={!visible}
                        aria-label={visible ? `Focus ${name}` : `${name} is hidden`}
                        className={cn(
                          "min-w-0 flex-1 truncate rounded-sm text-left font-mono text-xs focus-ring-inset hover:underline",
                          !visible && "text-muted-foreground",
                        )}
                      >
                        {qualified && t.schema && (
                          <span className="text-muted-foreground">{t.schema}.</span>
                        )}
                        {t.name}
                      </button>
                      {doc.colors[t.id] && (
                        <span
                          aria-hidden
                          className="size-2 shrink-0 rounded-full"
                          style={{ background: `var(--tag-${doc.colors[t.id]})` }}
                        />
                      )}
                      {t.rows > 0 && (
                        <span className="numeric shrink-0 text-micro text-muted-foreground">
                          {t.rows.toLocaleString("en-US")}
                        </span>
                      )}
                    </li>
                  )
                })}
              </ul>
            </div>
          </div>
        )}
      </div>
    </aside>
  )
}

function RelationList({
  title,
  empty,
  edges,
  other,
  known,
  describe,
  outside,
  onFocus,
}: {
  title: string
  empty: string
  edges: DbGraphEdge[]
  other: (e: DbGraphEdge) => string
  /** The other end is in the picture: a key may point at a table outside it. */
  known: (e: DbGraphEdge) => boolean
  describe: (e: DbGraphEdge) => string
  /** Where the other end is read when it is not in the picture. */
  outside: (e: DbGraphEdge) => string
  onFocus: (id: string) => void
}) {
  const row =
    "flex w-full items-center gap-2 rounded-md py-1 text-left text-hint focus-ring-inset transition-colors hover:bg-row-hover"
  return (
    <div className="space-y-1.5">
      <p className="eyebrow">
        {title}
        {edges.length > 0 && <span className="numeric ml-1.5 normal-case">{edges.length}</span>}
      </p>
      {edges.length === 0 ? (
        <p className="text-hint text-muted-foreground">{empty}</p>
      ) : (
        <ul className="divide-y divide-hairline">
          {edges.map((e, i) => (
            <li key={`${e.name}-${i}`}>
              {known(e) ? (
                <button type="button" onClick={() => onFocus(other(e))} className={row}>
                  <span className="min-w-0 flex-1 truncate font-mono">{describe(e)}</span>
                  {e.cardinality === "one-to-one" && <Tag>1:1</Tag>}
                  <ArrowRight aria-hidden className="size-3 shrink-0 text-muted-foreground" />
                </button>
              ) : (
                // Not in this picture: the row leads to the table itself.
                <Link
                  href={outside(e)}
                  className={row}
                  title="Not in this picture: open it in Schema"
                >
                  <span className="min-w-0 flex-1 truncate font-mono">{describe(e)}</span>
                  {e.cardinality === "one-to-one" && <Tag>1:1</Tag>}
                  <Tag>not drawn</Tag>
                  <External aria-hidden className="size-3 shrink-0 text-muted-foreground" />
                </Link>
              )}
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}
