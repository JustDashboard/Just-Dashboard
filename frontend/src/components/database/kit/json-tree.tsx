"use client"

import { useState } from "react"
import { ChevronDown, ChevronRight, Copy, Route } from "@/components/icons"
import { copyText } from "@/lib/clipboard"
import { cn } from "@/lib/utils"
import { RowActions } from "@/components/icon-action"
import { VALUE_KIND_CLASS } from "@/components/database/kit/value-text"
import { jsonPath, sizeWord, valueKind } from "@/components/database/kit/values"

/** How many entries of one object or array are drawn before the rest are asked for. */
const PAGE = 100

/** The most characters of one string drawn in a row. */
const CLAMP = 240

type Segment = string | number

/**
 * A document, as a tree you open one level at a time.
 *
 * It is how a JSON column, a Mongo document or a Redis JSON value is read
 * when the one-line preview is not enough: keys in the muted voice, values in
 * their kind's hue, an object or an array folded behind its size until it is
 * opened. Every row can hand over two things — the path to it, in the form a
 * query names it by, and its value as JSON.
 *
 * A large document costs what is on screen and no more: a level that is
 * closed is not rendered, a level with thousands of entries draws a hundred
 * and offers the next hundred, and a long string is cut on its row.
 */
export function JsonTree({
  value,
  expand = 1,
  className,
}: {
  value: unknown
  /** How many levels are open to begin with. */
  expand?: number
  className?: string
}) {
  return (
    <div
      data-slot="json-tree"
      className={cn("min-w-0 font-mono text-xs leading-relaxed", className)}
    >
      <Node value={value} path={[]} depth={0} expand={expand} />
    </div>
  )
}

function Node({
  name,
  value,
  path,
  depth,
  expand,
}: {
  /** The key or index this value sits under; absent for the document itself. */
  name?: Segment
  value: unknown
  path: Segment[]
  depth: number
  expand: number
}) {
  const container = typeof value === "object" && value !== null
  const [open, setOpen] = useState(depth < expand)
  const [shown, setShown] = useState(PAGE)

  const entries: [Segment, unknown][] = !container
    ? []
    : Array.isArray(value)
      ? value.map((item, index) => [index, item])
      : Object.entries(value)
  const kind = valueKind(value)

  return (
    <div>
      <div
        className="group flex min-h-6 min-w-0 items-center gap-1 rounded-sm pr-1 hover:bg-row-hover"
        style={{ paddingLeft: `${depth * 14 + 2}px` }}
      >
        {container ? (
          <button
            type="button"
            // A disclosure, not a tree widget: nothing here is selected, and
            // every row's controls are reached with Tab as they are drawn.
            aria-expanded={open}
            aria-label={`${open ? "Collapse" : "Expand"} ${name ?? "document"}`}
            onClick={() => setOpen(!open)}
            className="flex size-4 shrink-0 items-center justify-center rounded-sm text-muted-foreground focus-ring hover:text-foreground"
          >
            {open ? <ChevronDown className="size-3" /> : <ChevronRight className="size-3" />}
          </button>
        ) : (
          <span className="size-4 shrink-0" />
        )}
        {name !== undefined && (
          <span className="shrink-0 text-muted-foreground">
            {name}
            <span className="text-muted-foreground/50">:</span>
          </span>
        )}
        {container ? (
          <span className="min-w-0 truncate text-muted-foreground/70">
            {Array.isArray(value) ? "[ ]" : "{ }"} {sizeWord(value)}
          </span>
        ) : (
          <span className={cn("min-w-0 truncate", VALUE_KIND_CLASS[kind])}>
            {typeof value === "string"
              ? JSON.stringify(value.length > CLAMP ? `${value.slice(0, CLAMP)}…` : value)
              : String(value)}
          </span>
        )}
        <RowActions className="ml-auto pl-2">
          <button
            type="button"
            aria-label={`Copy the path to ${name ?? "the document"}`}
            title="Copy path"
            onClick={() => void copyText(jsonPath(path), "Path copied")}
            className="flex size-5 items-center justify-center rounded-sm text-muted-foreground focus-ring hover:text-foreground"
          >
            <Route className="size-3" />
          </button>
          <button
            type="button"
            aria-label={`Copy the value of ${name ?? "the document"}`}
            title="Copy value"
            onClick={() =>
              void copyText(
                typeof value === "string" ? value : JSON.stringify(value, null, 2),
                "Value copied",
              )
            }
            className="flex size-5 items-center justify-center rounded-sm text-muted-foreground focus-ring hover:text-foreground"
          >
            <Copy className="size-3" />
          </button>
        </RowActions>
      </div>
      {container && open && (
        <div>
          {entries.slice(0, shown).map(([key, child]) => (
            <Node
              key={key}
              name={key}
              value={child}
              path={[...path, key]}
              depth={depth + 1}
              expand={expand}
            />
          ))}
          {entries.length > shown && (
            <button
              type="button"
              onClick={() => setShown(shown + PAGE)}
              className="rounded-sm py-0.5 text-hint text-muted-foreground focus-ring hover:text-foreground"
              style={{ marginLeft: `${(depth + 1) * 14 + 22}px` }}
            >
              Show {Math.min(PAGE, entries.length - shown).toLocaleString()} more of{" "}
              {entries.length.toLocaleString()}
            </button>
          )}
        </div>
      )}
    </div>
  )
}
