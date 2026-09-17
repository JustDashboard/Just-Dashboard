"use client"

import { memo } from "react"
import { Handle, Position, type NodeProps } from "@xyflow/react"
import { Eye, Fingerprint, Key, Linked, MoreHorizontal, Notes } from "@/components/icons"
import { cn } from "@/lib/utils"
import type { DbGraphColumn, DbGraphTable } from "@/lib/types"
import type { DiagramColor, DiagramDetail } from "./memory"

export const ROW_HEIGHT = 26
export const HEADER_HEIGHT = 38
export const NOTE_HEIGHT = 24
export const NODE_WIDTH = 264

export type TableNodeData = {
  table: DbGraphTable
  /** Dimmed when something else is focused and this is not related to it. */
  dimmed: boolean
  /** The table the operator is reading: focused by a click, or found by the search. */
  focused: boolean
  detail: DiagramDetail
  color?: DiagramColor
  note?: string
  /** Columns the search matched, so the row that answered is the row that lights. */
  matches?: string[]
  /** Tables cannot be dragged — a finished diagram. */
  locked: boolean
  onOpen: (table: DbGraphTable) => void
  onMenu: (table: DbGraphTable, at: { x: number; y: number }) => void
}

/**
 * One table, drawn as the list of columns it is.
 *
 * The point of a schema diagram is the columns — which one is the key, which
 * one points somewhere else — so boxes with only names on them answer a
 * question nobody was asking. Every column is a row here, and every row is an
 * anchor: an edge lands on `orders.customer_id`, not on `orders`. At the two
 * lower levels of detail the rows thin out to the keys, or to nothing, and the
 * edges land on the header instead.
 *
 * It does not lift: a node is a step of ground with a border, like every other
 * surface, and the one thing that marks it is its border — the brand orange
 * for the table being read, because that is what the hue is for. A colour the
 * operator chose is a bar down the left edge, in the same eight fixed hues the
 * terminal's tags use, so "the red ones are billing" is a label that stays put.
 */
function TableNodeComponent({ data, selected }: NodeProps & { data: TableNodeData }) {
  const { table, dimmed, focused, detail, color, note, matches, locked, onOpen, onMenu } = data
  const columns =
    detail === "names"
      ? []
      : detail === "keys"
        ? table.columns.filter((c) => c.primaryKey || c.foreignKey || c.unique)
        : table.columns
  const hidden = table.columns.length - columns.length
  const hit = new Set(matches ?? [])

  return (
    <div
      className={cn(
        "overflow-hidden rounded-lg border bg-card transition-[opacity,border-color] duration-200",
        dimmed && "opacity-30",
        focused ? "border-brand" : selected ? "border-border-strong" : "border-border",
        locked ? "cursor-default" : "cursor-grab active:cursor-grabbing",
      )}
      style={{
        width: NODE_WIDTH,
        boxShadow: color ? `inset 3px 0 0 0 var(--tag-${color})` : undefined,
      }}
    >
      <div
        className="flex items-center gap-1.5 border-b border-hairline bg-surface-header pr-1 pl-2.5"
        style={{ height: HEADER_HEIGHT }}
      >
        {/* The header carries the handles a names-only diagram lands on. */}
        <NodeHandles id={`${table.name}.`} />
        <button
          type="button"
          onClick={() => onOpen(table)}
          className="nodrag min-w-0 flex-1 truncate rounded-sm text-left font-mono text-xs font-semibold focus-ring-inset"
          title={`${table.schema ? `${table.schema}.` : ""}${table.name}`}
        >
          {table.name}
        </button>
        {table.rows > 0 && (
          <span
            className="numeric shrink-0 text-micro text-muted-foreground"
            title={`${table.rows.toLocaleString()} rows (estimated)`}
          >
            {compactRows(table.rows)}
          </span>
        )}
        <button
          type="button"
          aria-label={`Actions for ${table.name}`}
          onClick={(e) => {
            e.stopPropagation()
            onMenu(table, { x: e.clientX, y: e.clientY })
          }}
          className="nodrag nopan flex size-6 shrink-0 items-center justify-center rounded-sm text-muted-foreground focus-ring-inset transition-colors hover:bg-accent hover:text-foreground"
        >
          <MoreHorizontal className="size-3.5" />
        </button>
      </div>

      {detail !== "names" && (
        <div className="divide-y divide-hairline/50">
          {columns.map((c) => (
            <ColumnRow key={c.name} table={table.name} column={c} hit={hit.has(c.name)} />
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
          {columns.length === 0 && hidden === 0 && (
            <div
              className="px-2.5 text-micro text-muted-foreground italic"
              style={{ height: ROW_HEIGHT, lineHeight: `${ROW_HEIGHT}px` }}
            >
              no columns readable
            </div>
          )}
        </div>
      )}

      {note && (
        <div
          className="flex items-center gap-1.5 border-t border-hairline px-2.5 text-micro text-muted-foreground"
          style={{ height: NOTE_HEIGHT }}
          title={note}
        >
          <Notes className="size-3 shrink-0" />
          <span className="truncate">{note}</span>
        </div>
      )}
    </div>
  )
}

/**
 * Both sides carry a source and a target handle, because which side an edge
 * leaves by depends on where the layout put the other table — and an edge
 * referring to a handle that does not exist is dropped silently rather than
 * drawn badly.
 */
function NodeHandles({ id }: { id: string }) {
  const style = { opacity: 0, width: 1, height: 1, border: 0, minWidth: 0, minHeight: 0 }
  return (
    <>
      <Handle
        type="source"
        position={Position.Left}
        id={`${id}.left.s`}
        style={{ ...style, left: 0 }}
        isConnectable={false}
      />
      <Handle
        type="target"
        position={Position.Left}
        id={`${id}.left.t`}
        style={{ ...style, left: 0 }}
        isConnectable={false}
      />
      <Handle
        type="source"
        position={Position.Right}
        id={`${id}.right.s`}
        style={{ ...style, right: 0 }}
        isConnectable={false}
      />
      <Handle
        type="target"
        position={Position.Right}
        id={`${id}.right.t`}
        style={{ ...style, right: 0 }}
        isConnectable={false}
      />
    </>
  )
}

function ColumnRow({ table, column, hit }: { table: string; column: DbGraphColumn; hit: boolean }) {
  return (
    <div
      className={cn(
        "relative flex items-center gap-2 px-2.5 transition-colors",
        hit ? "bg-mark" : "hover:bg-row-hover",
      )}
      style={{ height: ROW_HEIGHT }}
      title={`${column.name} · ${column.type}${column.nullable ? " · nullable" : " · not null"}${
        column.foreignKey ? ` · references ${column.foreignKey}` : ""
      }`}
    >
      <NodeHandles id={`${table}.${column.name}`} />
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

/** A row count for a narrow slot: 1_234_567 → "1.2M", not "1,234,567". */
export function compactRows(n: number): string {
  if (n < 1000) return String(n)
  if (n < 1_000_000) return `${(n / 1000).toFixed(n < 10_000 ? 1 : 0)}k`
  return `${(n / 1_000_000).toFixed(1)}M`
}

/**
 * Type names are for recognition here, not reproduction. "timestamp with time
 * zone" in a ten-pixel column pushes the name out of the box, and a reader
 * already knows what timestamptz means.
 */
export function shortType(type: string): string {
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
