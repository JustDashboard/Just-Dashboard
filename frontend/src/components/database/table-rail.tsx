"use client"

import { useMemo } from "react"
import { useSessionState } from "@/lib/view-state"
import { cn, ringSafeScroll } from "@/lib/utils"
import { bytes, plural } from "@/lib/format"
import type { DbTable } from "@/lib/types"
import { SearchInput } from "@/components/page"
import { EmptyNote, LoadingRows } from "@/components/state"
import { Tag } from "@/components/tag"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"

export type TableSelection = { schema: string; table: string }

/**
 * The column of tables beside a workbench: Browse and Structure both open on
 * it, and it is one component so picking a table is the same gesture on both.
 *
 * A filter box and, where the catalogue spans several schemas, a schema
 * picker; then the rows, each a name over a second line of what it is and how
 * big. The chosen row is `bg-accent`, the neutral fill every selection in the
 * product takes — it was the primary tint, which is the mark reserved for a
 * command.
 */
export function TableRail({
  connId,
  tables,
  loading,
  selected,
  onSelect,
  action,
  className,
}: {
  /**
   * Which connection's catalogue this is. The filter and the schema are kept
   * per connection, not per rail: under one shared key, choosing the `public`
   * schema on Postgres and then switching to MySQL left every table filtered
   * out by a schema that engine has never heard of, under a search box that
   * was visibly empty.
   */
  connId: number
  tables: DbTable[] | undefined
  loading: boolean
  selected: TableSelection | null
  onSelect: (table: DbTable) => void
  /** The rail's one control — "New table" where the operator may create one. */
  action?: React.ReactNode
  className?: string
}) {
  const [query, setQuery] = useSessionState(`databases.${connId}.tables.query`, "")
  const [schema, setSchema] = useSessionState(`databases.${connId}.tables.schema`, "all")

  const schemaNames = useMemo(() => {
    const set = new Set<string>()
    for (const t of tables ?? []) set.add(t.schema)
    return [...set].sort()
  }, [tables])

  const visible = useMemo(() => {
    let list = tables ?? []
    if (schema !== "all") list = list.filter((t) => t.schema === schema)
    const q = query.trim().toLowerCase()
    if (q) list = list.filter((t) => t.name.toLowerCase().includes(q))
    return list
  }, [tables, schema, query])

  return (
    <div className={cn("flex min-h-0 min-w-0 flex-col", className)}>
      <div className="shrink-0 space-y-2 border-b border-hairline p-2.5">
        <div className="flex items-center gap-1.5">
          <SearchInput
            dense
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="Filter tables…"
            aria-label="Filter tables"
            containerClassName="min-w-0 flex-1 sm:w-auto"
          />
          {action}
        </div>
        {schemaNames.length > 1 && (
          <Select value={schema} onValueChange={setSchema}>
            <SelectTrigger size="sm" className="h-7 w-full text-xs sm:h-7" aria-label="Schema">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">All schemas</SelectItem>
              {schemaNames.map((s) => (
                <SelectItem key={s} value={s}>
                  {s}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        )}
      </div>

      <div
        key={tables ? "catalogue" : "waiting"}
        className={cn(
          "min-h-0 flex-1 space-y-px overflow-y-auto",
          tables && "animate-rise",
          ringSafeScroll,
        )}
      >
        {loading && !tables && <LoadingRows rows={6} className="p-1" />}
        {visible.map((t) => {
          const active = selected?.table === t.name && selected.schema === t.schema
          return (
            <button
              key={`${t.schema}.${t.name}`}
              type="button"
              aria-pressed={active}
              onClick={() => onSelect(t)}
              className={cn(
                "flex w-full min-w-0 items-center gap-2 rounded-md px-2 py-1.5 text-left focus-ring-inset transition-colors",
                active ? "bg-accent text-foreground" : "hover:bg-row-hover",
              )}
            >
              <span className="min-w-0 flex-1">
                <span className="block truncate text-body font-medium">{t.name}</span>
                <span className="block truncate text-hint text-muted-foreground">
                  {[
                    schemaNames.length > 1 ? t.schema : null,
                    // A negative estimate is the catalogue saying it has none —
                    // a view, a table the planner has never analysed, SQLite,
                    // which keeps no count at all. It used to be floored to
                    // zero and drawn as "no rows", so half the tables in a
                    // fresh database claimed to be empty. Say nothing instead;
                    // Count rows in the menu is the answer that is true.
                    t.estimatedRows > 0
                      ? `~${plural(t.estimatedRows, "row")}`
                      : t.estimatedRows === 0
                        ? "no rows"
                        : null,
                    t.size ? bytes(t.size) : null,
                  ]
                    .filter(Boolean)
                    .join(" · ")}
                </span>
              </span>
              {t.type && t.type.toLowerCase() !== "table" && (
                <Tag className="shrink-0">{t.type}</Tag>
              )}
            </button>
          )
        })}
        {tables?.length === 0 && <EmptyNote>No tables yet.</EmptyNote>}
        {tables && tables.length > 0 && visible.length === 0 && (
          <EmptyNote>No tables match the filter.</EmptyNote>
        )}
      </div>

      {tables && tables.length > 0 && (
        <div className="numeric shrink-0 border-t border-hairline px-3 py-1.5 text-hint text-muted-foreground">
          {visible.length === tables.length
            ? plural(tables.length, "table")
            : `${visible.length} of ${plural(tables.length, "table")}`}
        </div>
      )}
    </div>
  )
}
