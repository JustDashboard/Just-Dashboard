"use client"

import { useEffect, useRef, useState } from "react"
import { Check, Cross } from "@/components/icons"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"
import { useColumnWidth } from "@/components/deploy/settings/use-column-width"
import { PaneFooter } from "@/components/panel"
import { Spinner } from "@/components/state"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { bytesLabel, isBinary, lineSafe } from "@/components/database/redis/bytes"
import { ReadError } from "@/components/database/redis/read-error"
import type { RedisBytes } from "@/components/database/redis/types"
import type { PagedStatus } from "@/components/database/redis/use-paged"
import { useRowWindow } from "@/components/database/redis/use-row-window"

const ROW_HEIGHT = 32
/** A row drawn down instead of across: two lines. */
const STACKED_HEIGHT = 52
/** The narrowest pane two columns and a row's controls are still drawn across in. */
const FITS = 448

export type MemberColumn<R> = {
  id: string
  label: string
  /** The column's track in the row's grid: `minmax(0,1fr)`, `8rem`. */
  width: string
  align?: "right"
  /**
   * Where the pane is too narrow for the columns: on the row's first line,
   * beside its controls. The first column always is; the rest go under it.
   */
  lead?: boolean
  /**
   * The word said before the cell when it is on the second line, where no
   * header names it — for every row, or only for the rows it says something
   * about.
   */
  word?: string | ((row: R) => string | undefined)
  cell: (row: R) => React.ReactNode
}

/**
 * The inside of a key, as rows: the fields of a hash, the elements of a
 * list, the members of a set.
 *
 * A key holds as many as it likes, so the rows arrive in pages (the foot says
 * how many of how many are here and fetches the next) and only the ones on
 * screen are drawn. Rows are one height and their controls sit in a column of
 * their own, so a value never moves when the pointer reaches its row.
 *
 * Where the pane is narrower than the columns need — a phone, a wide rail —
 * a row is drawn down instead of across: what names it and its controls on
 * one line, what it holds on the next. Nothing is dropped and nothing scrolls
 * sideways to be reached. Which shape is drawn is decided once, from the
 * pane's own width.
 */
export function MemberTable<R>({
  label,
  columns,
  rows,
  rowId,
  actions,
  actionsWidth = "4.5rem",
  fits = FITS,
  paged,
  total,
  noun,
  nouns,
  filtered,
  empty,
}: {
  /** What the rows are, for assistive technology: "Fields of user:1:profile". */
  label: string
  columns: MemberColumn<R>[]
  rows: R[]
  rowId: (row: R) => string
  /** The row's own controls, in the last column. */
  actions?: (row: R) => React.ReactNode
  actionsWidth?: string
  /** The pane width, in pixels, below which the rows are drawn down instead of across. */
  fits?: number
  paged: PagedStatus
  /** How many the key holds in all: its real cardinality, not how many are loaded. */
  total: number
  noun: string
  nouns: string
  /** A filter is on, so "all of them" is not what is listed. */
  filtered?: boolean
  empty: React.ReactNode
}) {
  const [pane, width] = useColumnWidth()
  // A single column has nothing to put under itself: it only loses its floor.
  const stacked = width > 0 && width < fits && columns.length > 1
  const { attach, onScroll, slice, height, offset } = useRowWindow(
    rows.length,
    stacked ? STACKED_HEIGHT : ROW_HEIGHT,
  )
  const leads = columns.filter((column, at) => at === 0 || column.lead)
  const under = columns.filter((column, at) => at !== 0 && !column.lead)
  const template = {
    gridTemplateColumns: [
      ...columns.map((column) => column.width),
      ...(actions ? [actionsWidth] : []),
    ].join(" "),
  }
  const loaded = rows.length.toLocaleString()
  const all = `${total.toLocaleString()} ${total === 1 ? noun : nouns}`

  return (
    <>
      <div ref={pane} className="flex min-h-0 min-w-0 flex-1 flex-col">
        <div ref={attach} onScroll={onScroll} className="min-h-0 flex-1 overflow-auto">
          {paged.error && !paged.state ? (
            <ReadError error={paged.error} onRetry={paged.reload} className="m-3" />
          ) : paged.loading ? (
            <div className="space-y-2.5 p-3" aria-hidden>
              {Array.from({ length: 10 }, (_, i) => (
                <div key={i} className="flex gap-6">
                  <Skeleton className="h-3.5" style={{ width: `${18 + ((i * 7) % 12)}%` }} />
                  <Skeleton className="h-3.5" style={{ width: `${30 + ((i * 11) % 34)}%` }} />
                </div>
              ))}
            </div>
          ) : rows.length === 0 && paged.done ? (
            <div className="flex h-full items-center justify-center p-4">{empty}</div>
          ) : (
            <div role="table" aria-label={label} data-shape={stacked ? "stacked" : "columns"}>
              {!stacked && (
                <div
                  role="row"
                  style={template}
                  className="sticky top-0 z-10 grid h-8 items-center gap-x-3 border-b border-hairline bg-card px-3 text-hint font-medium text-muted-foreground"
                >
                  {columns.map((column) => (
                    <div
                      key={column.id}
                      role="columnheader"
                      className={cn("truncate", column.align === "right" && "text-right")}
                    >
                      {column.label}
                    </div>
                  ))}
                  {actions && (
                    <div role="columnheader">
                      <span className="sr-only">Actions</span>
                    </div>
                  )}
                </div>
              )}
              <div style={{ height }} className="relative">
                <div style={{ transform: `translateY(${offset}px)` }}>
                  {rows.slice(slice.start, slice.end).map((row) =>
                    stacked ? (
                      <div
                        key={rowId(row)}
                        role="row"
                        className="group flex h-13 flex-col justify-center border-b border-hairline px-3 font-mono text-xs transition-colors hover:bg-row-hover"
                      >
                        <div role="presentation" className="flex h-6 min-w-0 items-center gap-3">
                          {leads.map((column, at) => (
                            <div
                              key={column.id}
                              role="cell"
                              className={cn(
                                "flex min-w-0 items-center",
                                at === leads.length - 1 && "flex-1",
                              )}
                            >
                              {column.cell(row)}
                            </div>
                          ))}
                          {actions && (
                            <div role="cell" className="flex shrink-0 items-center justify-end">
                              {actions(row)}
                            </div>
                          )}
                        </div>
                        <div role="presentation" className="flex h-6 min-w-0 items-center gap-4">
                          {under.map((column, at) => {
                            const word =
                              typeof column.word === "function" ? column.word(row) : column.word
                            return (
                              <div
                                key={column.id}
                                role="cell"
                                className={cn(
                                  "flex min-w-0 items-center gap-1.5",
                                  // A figure that ended its column on the
                                  // right stands after its word here, not a
                                  // pane's width away from it.
                                  at === 0 && column.align !== "right" ? "flex-1" : "shrink-0",
                                  at !== 0 && "ml-auto",
                                )}
                              >
                                {word && (
                                  <span className="shrink-0 font-sans text-hint text-muted-foreground">
                                    {word}
                                  </span>
                                )}
                                {column.cell(row)}
                              </div>
                            )
                          })}
                        </div>
                      </div>
                    ) : (
                      <div
                        key={rowId(row)}
                        role="row"
                        style={template}
                        className="group grid h-8 items-center gap-x-3 border-b border-hairline px-3 font-mono text-xs transition-colors hover:bg-row-hover"
                      >
                        {columns.map((column) => (
                          <div
                            key={column.id}
                            role="cell"
                            className={cn(
                              "flex min-w-0 items-center",
                              column.align === "right" && "justify-end",
                            )}
                          >
                            {column.cell(row)}
                          </div>
                        ))}
                        {actions && (
                          <div role="cell" className="flex items-center justify-end">
                            {actions(row)}
                          </div>
                        )}
                      </div>
                    ),
                  )}
                </div>
              </div>
            </div>
          )}
        </div>
      </div>
      {paged.state !== undefined && (
        <PaneFooter className="gap-2 text-hint text-muted-foreground">
          <span data-slot="redis-member-count" className="numeric min-w-0 flex-1 truncate">
            {paged.error
              ? `${paged.error.message}`
              : paged.done
                ? filtered
                  ? `${loaded} of ${all} match`
                  : all
                : filtered
                  ? `${loaded} found so far in ${all}`
                  : `${loaded} of ${all}`}
          </span>
          {paged.reloading && <Spinner className="size-3" />}
          {paged.error ? (
            <Button size="xs" variant="outline" onClick={paged.done ? paged.reload : paged.more}>
              Try again
            </Button>
          ) : (
            !paged.done && (
              <Button size="xs" variant="outline" pending={paged.loadingMore} onClick={paged.more}>
                Load more
              </Button>
            )
          )}
        </PaneFooter>
      )}
    </>
  )
}

/**
 * A member as one line of text. What is not text is said to be bytes and
 * drawn written out; what the server cut is said to be cut. Neither is ever
 * offered for editing in a text box.
 */
export function MemberText({
  value,
  truncated,
  className,
}: {
  value: RedisBytes
  truncated?: boolean
  className?: string
}) {
  const text = bytesLabel(value)
  return (
    <span className={cn("flex min-w-0 items-center gap-2", className)}>
      <span title={text.length > 60 ? text.slice(0, 2000) : undefined} className="min-w-0 truncate">
        {text === "" ? <span className="text-muted-foreground/60 italic">empty</span> : text}
      </span>
      {isBinary(value) && <Tag>bytes</Tag>}
      {truncated && <Tag>first 64 KiB</Tag>}
    </span>
  )
}

/**
 * A value edited where it stands. Pressing it turns the text into a field;
 * Enter saves, Escape puts the text back. Leaving the field does neither —
 * what was typed stays until one of the two is chosen, so a stray click does
 * not write a half-typed value or throw it away.
 */
export function InlineEdit({
  value,
  label,
  align,
  onSave,
}: {
  value: string
  /** What is being edited, for the field's name: "Value of name". */
  label: string
  align?: "right"
  onSave: (next: string) => Promise<void>
}) {
  const [draft, setDraft] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const field = useRef<HTMLInputElement>(null)
  const text = useRef<HTMLButtonElement>(null)
  // The field takes the text's place and gives it back: when the edit ends,
  // by Enter or by Escape, the keyboard is on the value it was editing, not
  // on nothing — the reader's next field is one Tab away, not the page's top.
  const edited = useRef(false)
  useEffect(() => {
    if (draft !== null) {
      edited.current = true
    } else if (edited.current) {
      edited.current = false
      text.current?.focus()
    }
  }, [draft])

  if (draft === null) {
    return (
      <button
        ref={text}
        type="button"
        aria-label={`Edit ${label}`}
        onClick={() => setDraft(value)}
        className={cn(
          "-mx-1 min-w-0 flex-1 cursor-text truncate rounded-sm px-1 py-0.5 text-left focus-ring-inset hover:bg-control",
          align === "right" && "text-right",
        )}
      >
        {value === "" ? <span className="text-muted-foreground/60 italic">empty</span> : value}
      </button>
    )
  }

  const save = async () => {
    if (draft === value) return setDraft(null)
    setBusy(true)
    try {
      await onSave(draft)
      setDraft(null)
    } catch (err) {
      notify.error(`Could not save ${label}`, err)
      field.current?.focus()
    } finally {
      setBusy(false)
    }
  }

  return (
    <span className="-mx-1 flex min-w-0 flex-1 items-center gap-0.5">
      <input
        ref={field}
        autoFocus
        aria-label={label}
        value={draft}
        disabled={busy}
        spellCheck={false}
        autoComplete="off"
        onChange={(event) => setDraft(event.target.value)}
        onKeyDown={(event) => {
          if (event.key === "Enter") void save()
          if (event.key === "Escape") {
            event.stopPropagation()
            setDraft(null)
          }
        }}
        className={cn(
          "h-6 min-w-0 flex-1 rounded-sm border border-input bg-background px-1 font-mono text-xs focus-ring-inset",
          align === "right" && "text-right",
        )}
      />
      <button
        type="button"
        aria-label={`Save ${label}`}
        disabled={busy}
        onClick={() => void save()}
        className="flex size-6 shrink-0 items-center justify-center rounded-sm text-muted-foreground focus-ring-inset hover:bg-control hover:text-foreground"
      >
        {busy ? <Spinner className="size-3" /> : <Check className="size-3" />}
      </button>
      <button
        type="button"
        aria-label={`Discard the edit of ${label}`}
        disabled={busy}
        onClick={() => setDraft(null)}
        className="flex size-6 shrink-0 items-center justify-center rounded-sm text-muted-foreground focus-ring-inset hover:bg-control hover:text-foreground"
      >
        <Cross className="size-3" />
      </button>
    </span>
  )
}

/**
 * A text value short and plain enough to edit on its row: whole, on one line,
 * and holding nothing a field would drop or rewrite on the way back.
 */
export function editableInline(
  value: RedisBytes | undefined,
  truncated?: boolean,
): value is string {
  return lineSafe(value) && !truncated && value.length <= 512
}
