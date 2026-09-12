"use client"

import { memo } from "react"
import { Handle, Position, type NodeProps } from "@xyflow/react"
import { Eye, Fingerprint, Key, Layout, Linked } from "@/components/icons"
import { cn } from "@/lib/utils"
import type { DbGraphColumn, DbGraphTable } from "@/lib/types"

export const ROW_HEIGHT = 26
export const HEADER_HEIGHT = 38
export const NODE_WIDTH = 264

export type TableNodeData = {
  table: DbGraphTable
  /** Dimmed when something else is focused and this is not related to it. */
  dimmed: boolean
  focused: boolean
  /** Hides everything but keys, for reading a large schema's shape. */
  keysOnly: boolean
  onOpen: (schema: string, table: string) => void
}

/**
 * One table, drawn as the list of columns it is.
 *
 * The point of a schema diagram is the columns — which one is the key, which
 * one points somewhere else — so boxes with only names on them answer a
 * question nobody was asking. Every column is a row here, and every row is an
 * anchor: an edge lands on `orders.customer_id`, not on `orders`.
 *
 * The header takes the panel chrome (`surface-header`, a hairline under it) so
 * a table reads as "name, then columns" the way every other panel in the
 * product does. The two key colours are the one deliberate constant — gold for
 * a primary key, blue for a foreign one, matching the edges — because they
 * carry meaning rather than decoration and have to stay put across themes.
 */
function TableNodeComponent({ data, selected }: NodeProps & { data: TableNodeData }) {
  const { table, dimmed, focused, keysOnly, onOpen } = data
  const columns = keysOnly
    ? table.columns.filter((c) => c.primaryKey || c.foreignKey || c.unique)
    : table.columns
  const hidden = table.columns.length - columns.length

  return (
    <div
      className={cn(
        "overflow-hidden rounded-lg border bg-card shadow-sm transition-opacity duration-200",
        dimmed && "opacity-25",
        focused ? "ring-2 ring-chart-1" : selected && "ring-1 ring-chart-1/50",
      )}
      style={{ width: NODE_WIDTH }}
    >
      <button
        onClick={() => onOpen(table.schema, table.name)}
        className="flex w-full items-center gap-2 border-b border-hairline bg-surface-header px-2.5 text-left transition-colors hover:bg-row-hover"
        style={{ height: HEADER_HEIGHT }}
        title={`Open ${table.name}`}
      >
        <Layout className="size-3.5 shrink-0 text-muted-foreground" />
        <span className="min-w-0 flex-1 truncate font-mono text-xs font-semibold">
          {table.name}
        </span>
        {table.rows > 0 && (
          <span className="shrink-0 rounded-sm bg-muted px-1 text-micro font-medium text-muted-foreground tabular-nums">
            {compactRows(table.rows)}
          </span>
        )}
      </button>

      <div className="divide-y divide-hairline/50">
        {columns.map((c) => (
          <ColumnRow key={c.name} table={table.name} column={c} />
        ))}
        {hidden > 0 && (
          <div
            className="flex items-center gap-1.5 bg-surface-sunken px-2.5 text-micro text-muted-foreground"
            style={{ height: ROW_HEIGHT }}
          >
            <Eye className="size-3" />
            {hidden} more {hidden === 1 ? "column" : "columns"}
          </div>
        )}
        {columns.length === 0 && (
          <div
            className="px-2.5 text-micro text-muted-foreground italic"
            style={{ height: ROW_HEIGHT, lineHeight: `${ROW_HEIGHT}px` }}
          >
            no columns readable
          </div>
        )}
      </div>
    </div>
  )
}

function ColumnRow({ table, column }: { table: string; column: DbGraphColumn }) {
  // Both sides carry a handle for every column, because which side an edge
  // leaves by depends on where the layout put the other table — and an edge
  // referring to a handle that does not exist is dropped silently rather than
  // drawn badly.
  const handleStyle = { opacity: 0, width: 1, height: 1, border: 0, minWidth: 0, minHeight: 0 }
  return (
    <div
      className="relative flex items-center gap-2 px-2.5 transition-colors hover:bg-row-hover"
      style={{ height: ROW_HEIGHT }}
      title={`${column.name} · ${column.type}${column.nullable ? " · nullable" : " · not null"}`}
    >
      <Handle
        type="source"
        position={Position.Left}
        id={`${table}.${column.name}.left.s`}
        style={{ ...handleStyle, left: 0 }}
        isConnectable={false}
      />
      <Handle
        type="target"
        position={Position.Left}
        id={`${table}.${column.name}.left.t`}
        style={{ ...handleStyle, left: 0 }}
        isConnectable={false}
      />
      <Handle
        type="source"
        position={Position.Right}
        id={`${table}.${column.name}.right.s`}
        style={{ ...handleStyle, right: 0 }}
        isConnectable={false}
      />
      <Handle
        type="target"
        position={Position.Right}
        id={`${table}.${column.name}.right.t`}
        style={{ ...handleStyle, right: 0 }}
        isConnectable={false}
      />
      {column.primaryKey ? (
        <Key className="size-3 shrink-0 text-chart-2" />
      ) : column.foreignKey ? (
        <Linked className="size-3 shrink-0 text-chart-1" />
      ) : column.unique ? (
        <Fingerprint className="size-3 shrink-0 text-muted-foreground/60" />
      ) : (
        <span className="size-3 shrink-0" />
      )}
      <span
        className={cn(
          "min-w-0 flex-1 truncate font-mono text-hint",
          column.primaryKey ? "font-semibold text-foreground" : "text-foreground/80",
        )}
      >
        {column.name}
      </span>
      <span
        className={cn(
          "shrink-0 truncate font-mono text-micro",
          column.nullable ? "text-muted-foreground/60" : "text-muted-foreground",
        )}
      >
        {shortType(column.type)}
      </span>
    </div>
  )
}

/** A row count for a 30px-wide chip: 1_234_567 → "1.2M", not "1,234,567". */
function compactRows(n: number): string {
  if (n < 1000) return String(n)
  if (n < 1_000_000) return `${(n / 1000).toFixed(n < 10_000 ? 1 : 0)}k`
  return `${(n / 1_000_000).toFixed(1)}M`
}

/**
 * Type names are for recognition here, not reproduction. "timestamp with time
 * zone" in a ten-pixel column pushes the name out of the box, and a reader
 * already knows what timestamptz means.
 */
function shortType(type: string): string {
  const map: Record<string, string> = {
    "timestamp with time zone": "timestamptz",
    "timestamp without time zone": "timestamp",
    "character varying": "varchar",
    "double precision": "float8",
    bigint: "int8",
    integer: "int4",
    smallint: "int2",
    boolean: "bool",
    character: "char",
  }
  const mapped = map[type.toLowerCase()] ?? type
  return mapped.length > 14 ? mapped.slice(0, 13) + "…" : mapped
}

export const TableNode = memo(TableNodeComponent)
