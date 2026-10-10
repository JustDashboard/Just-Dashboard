"use client"

import { useEffect, useMemo, useRef, useState } from "react"
import { ListUnordered } from "@/components/icons"
import { get } from "@/lib/api"
import { cn } from "@/lib/utils"
import { usePoll } from "@/hooks/use-poll"
import { SearchInput } from "@/components/page"
import { EmptyNote, ErrorState, LoadingRows } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import type { CellValue, GridForeignKey } from "@/components/database/grid"
import { ValueText } from "@/components/database/kit"
import { useDatabase } from "@/components/database/shell/database-context"
import { useDatabases } from "@/components/database/shell/databases-context"
import { encodeFilters, filterText } from "@/components/database/data/filters"
import { KindGlyph } from "@/components/database/data/kinds"
import { pickerLabel, pickerSearch, type PickerShape } from "@/components/database/data/rows"
import type { BrowsePage } from "@/components/database/data/types"

/** How many rows of the referenced table one look lists. */
const ROWS = 30

/**
 * The rows a foreign key can point at, to choose one from.
 *
 * A key is a number or a code that means nothing by itself: the reader knows
 * the customer's name, not that it is 4193. So the field's value can be taken
 * from the referenced table itself — its first rows, or the ones a typed word
 * is found in — and what is staged is that row's key, exactly as the engine
 * sent it.
 *
 * The search asks the server (`GET /browse`, any of the table's text columns
 * containing the word, or the key equal to it). It is the table's own rows,
 * not a guess from the page on screen.
 */
export function ForeignKeyPicker({
  foreignKey,
  schema,
  current,
  onPick,
}: {
  foreignKey: GridForeignKey
  /** This table's schema, which a key that names none points into. */
  schema: string
  /** The key's value in this row as compared text, or null when it has none. */
  current: string | null
  onPick: (value: CellValue) => void
}) {
  const [open, setOpen] = useState(false)
  const target = foreignKey.schema ?? schema
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <Button type="button" size="xs" variant="outline" className="min-w-0">
          <ListUnordered />
          <span className="truncate">Choose</span>
        </Button>
      </PopoverTrigger>
      <PopoverContent align="start" className="w-96 max-w-[calc(100vw-2rem)] p-0">
        {open && (
          <Rows
            foreignKey={foreignKey}
            schema={target}
            current={current}
            onPick={(value) => {
              onPick(value)
              setOpen(false)
            }}
          />
        )}
      </PopoverContent>
    </Popover>
  )
}

function Rows({
  foreignKey,
  schema,
  current,
  onPick,
}: {
  foreignKey: GridForeignKey
  schema: string
  current: string | null
  onPick: (value: CellValue) => void
}) {
  const { id, engine } = useDatabase()
  const { drivers } = useDatabases()
  const operators = useMemo(
    () => drivers?.find((driver) => driver.id === engine.driver)?.filterOps ?? [],
    [drivers, engine.driver],
  )
  const [typed, setTyped] = useState("")
  // What is asked of the server follows the typing by a moment, not by a letter.
  const [needle, setNeedle] = useState("")
  useEffect(() => {
    const timer = setTimeout(() => setNeedle(typed.trim()), 250)
    return () => clearTimeout(timer)
  }, [typed])

  // What the table is made of, from its first answer: which columns a word
  // can be looked for in.
  const [shape, setShape] = useState<PickerShape>()
  const search = useMemo(
    () =>
      shape && needle
        ? pickerSearch(shape, foreignKey.column, needle, operators)
        : { exact: null, inside: [] },
    [shape, needle, foreignKey.column, operators],
  )
  const inside = encodeFilters(search.inside)
  // With nothing typed, the row the key points at now stands first: it is what
  // the reader is choosing a replacement for.
  const pinned =
    needle === "" && current !== null
      ? ({ column: foreignKey.column, op: "eq", value: current } as const)
      : search.exact
  const exactly = pinned ? encodeFilters([pinned]) : ""
  // A word nothing in this table can be compared with matches no row.
  const unsearchable = needle !== "" && shape !== undefined && !inside && !exactly
  const browse = (filters: string, match: boolean, limit: number, signal: AbortSignal) =>
    get<BrowsePage>(
      `/databases/${id}/browse`,
      {
        schema: schema || undefined,
        table: foreignKey.table,
        limit,
        cellLimit: 120,
        filters: filters || undefined,
        match: match ? "any" : undefined,
      },
      signal,
    )
  const rows = usePoll(
    (signal) => browse(inside, search.inside.length > 1, ROWS, signal),
    0,
    [id, schema, foreignKey.table, inside],
    { enabled: needle === "" || inside !== "" },
  )
  // The row whose key is the word itself, asked for apart so it can stand first.
  const exact = usePoll(
    (signal) => browse(exactly, false, 1, signal),
    0,
    [id, schema, foreignKey.table, exactly],
    { enabled: exactly !== "" },
  )
  const [kept, setKept] = useState<BrowsePage>()
  if (rows.data && rows.data !== kept) {
    setKept(rows.data)
    if (!shape) setShape({ columns: rows.data.columns, kinds: rows.data.kinds ?? [] })
  }
  const listed = needle !== "" && inside === "" ? undefined : (rows.data ?? kept)
  const page = unsearchable ? undefined : (listed ?? (exactly ? exact.data : undefined))
  const at = page ? page.columns.indexOf(foreignKey.column) : -1
  const first = exactly ? exact.data?.rows[0] : undefined
  const drawn = useMemo(() => {
    const rest = listed?.rows ?? []
    if (!first || at < 0) return rest
    const key = filterText(first[at] ?? null)
    return [first, ...rest.filter((row) => filterText(row[at] ?? null) !== key)]
  }, [listed, first, at])
  const loading = rows.loading || exact.loading
  const error = rows.error ?? exact.error

  const list = useRef<HTMLDivElement>(null)
  const step = (event: React.KeyboardEvent, from: "field" | "list") => {
    if (event.key !== "ArrowDown" && event.key !== "ArrowUp") return
    const options = [...(list.current?.querySelectorAll<HTMLElement>("[role=option]") ?? [])]
    if (options.length === 0) return
    event.preventDefault()
    const here = from === "list" ? options.indexOf(document.activeElement as HTMLElement) : -1
    const next = event.key === "ArrowDown" ? here + 1 : here - 1
    if (next < 0) list.current?.parentElement?.querySelector("input")?.focus()
    else options[Math.min(next, options.length - 1)].focus()
  }

  return (
    <>
      <div className="flex items-center gap-1.5 border-b border-hairline bg-surface-header px-3 py-2">
        <KindGlyph kind="table" />
        <span className="min-w-0 flex-1 truncate font-mono text-xs font-medium">
          {schema ? `${schema}.` : ""}
          {foreignKey.table}
        </span>
        <span className="shrink-0 font-mono text-hint text-muted-foreground">
          {foreignKey.column}
        </span>
      </div>
      <div className="border-b border-hairline p-1.5">
        <SearchInput
          dense
          autoFocus
          value={typed}
          placeholder={`Find a row of ${foreignKey.table}`}
          aria-label={`Find a row of ${foreignKey.table}`}
          containerClassName="sm:w-full"
          onChange={(event) => setTyped(event.target.value)}
          onKeyDown={(event) => step(event, "field")}
        />
      </div>
      <div
        ref={list}
        role="listbox"
        aria-label={`Rows of ${foreignKey.table}`}
        aria-busy={loading || undefined}
        className={cn("max-h-72 overflow-y-auto p-1", loading && page && "opacity-70")}
        onKeyDown={(event) => step(event, "list")}
      >
        {error && !unsearchable ? (
          <ErrorState
            error={error}
            onRetry={() => {
              rows.refresh()
              exact.refresh()
            }}
            className="m-2"
          />
        ) : unsearchable || (page && !loading && drawn.length === 0) ? (
          <EmptyNote className="px-2 py-6">
            {needle
              ? `No row of ${foreignKey.table} holds that.`
              : `${foreignKey.table} has no rows to point at.`}
          </EmptyNote>
        ) : !page ? (
          <LoadingRows rows={5} className="p-2" />
        ) : at < 0 ? (
          <EmptyNote className="px-2 py-6">
            {foreignKey.table} has no column called {foreignKey.column}.
          </EmptyNote>
        ) : (
          drawn.map((row, index) => {
            const key = filterText(row[at] ?? null)
            const chosen = key !== null && key === current
            return (
              <button
                key={index}
                type="button"
                role="option"
                aria-selected={chosen}
                className={cn(
                  "flex h-7 w-full min-w-0 items-center gap-2 rounded-md px-2 text-left focus-ring-inset transition-colors",
                  chosen ? "bg-accent" : "hover:bg-menu-hover",
                )}
                onClick={() => onPick(row[at] ?? null)}
              >
                <ValueText
                  value={row[at]}
                  type={page.kinds?.[at] ?? page.types[at]}
                  clamp={24}
                  className="shrink-0 font-medium"
                />
                <span className="min-w-0 flex-1 truncate text-xs text-muted-foreground">
                  {pickerLabel(page.columns, page.kinds ?? [], row, at)}
                </span>
              </button>
            )
          })
        )}
      </div>
      {listed?.truncated && (
        <p className="border-t border-hairline px-3 py-1.5 text-hint text-muted-foreground">
          The first {ROWS} rows. Type to find another.
        </p>
      )}
    </>
  )
}
