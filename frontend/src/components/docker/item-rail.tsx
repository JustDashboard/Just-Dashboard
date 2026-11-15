"use client"

import { Fragment, useEffect, useRef } from "react"
import Link from "next/link"
import { cn } from "@/lib/utils"
import { SearchInput } from "@/components/page"
import { LoadingRows } from "@/components/state"

/**
 * The list a Docker item was opened from, folded into a column beside it: a
 * container's, a stack's, an image's, a volume's and a network's pages all
 * stand beside this one rail. It holds what the five have in common (the
 * list's own search, what it is showing and the way out of its filters, the
 * item on screen kept in view and drawn first when the filters leave it out);
 * what differs is the row and where the list comes from, and each page's own
 * file (`container-rail.tsx`, `stack-rail.tsx`, `image-rail.tsx`,
 * `volume-rail.tsx`, `network-rail.tsx`) keeps those.
 *
 * The same column the processes and the services have
 * (`procs/process-rail.tsx`). From `xl` only; narrower, the page is one pane.
 */
export function DockerRail<T>({
  slot,
  label,
  search,
  rows,
  keyOf,
  row,
  current,
  pinned,
  loading,
  loadingRows = 8,
  empty,
  query,
  onQuery,
  filtered,
  onClearFilters,
  caption,
  bindings,
  className,
}: {
  /** The rail's `data-slot`, which its page's specs find it by. */
  slot: string
  /** What the list is of, in the plural: "Volumes". */
  label: string
  search: { label: string; placeholder: string }
  rows: T[]
  keyOf: (row: T) => string
  /** One row of the list; `current` is whether it is the item on screen. */
  row: (row: T, current: boolean) => React.ReactNode
  /** Whether a row is the item on screen. */
  current: (row: T) => boolean
  /** The item on screen, drawn first when the list does not hold it. */
  pinned?: T
  loading: boolean
  loadingRows?: number
  /** What the list says when nothing matches: "No volume matches." */
  empty: string
  query: string
  onQuery: (value: string) => void
  /** A chip or a filter other than the search is on. */
  filtered: boolean
  onClearFilters: () => void
  caption: string
  /** `useInspectionOrder`'s, where the rows hold their order under the pointer. */
  bindings?: React.HTMLAttributes<HTMLElement>
  className?: string
}) {
  const here = rows.find(current)
  const first = !here && pinned !== undefined ? pinned : undefined
  const list = useRef<HTMLOListElement>(null)
  // The item on screen is kept in view: opened from a link or by Alt+↓ past
  // the column's edge, it would otherwise be off its own list.
  const key = here ? keyOf(here) : first !== undefined ? keyOf(first) : ""
  useEffect(() => {
    const on = list.current?.querySelector<HTMLElement>("[aria-current='page']")
    on?.scrollIntoView({ block: "nearest" })
  }, [key, rows.length])

  return (
    <nav
      aria-label={label}
      data-slot={slot}
      className={cn("min-h-0 flex-col border-r border-hairline pr-4", className)}
    >
      <div className="flex shrink-0 flex-col gap-2 pb-3">
        <SearchInput
          value={query}
          onChange={(event) => onQuery(event.target.value)}
          placeholder={search.placeholder}
          aria-label={search.label}
          containerClassName="sm:w-full"
        />
        <p className="flex min-w-0 items-center justify-between gap-2 text-hint text-muted-foreground">
          <span className="truncate">{caption}</span>
          {filtered && (
            <button
              type="button"
              onClick={onClearFilters}
              className="shrink-0 rounded-sm font-medium focus-ring hover:text-foreground"
            >
              Clear filters
            </button>
          )}
        </p>
      </div>
      <ol
        ref={list}
        aria-label={`${label} to open`}
        className="-mx-2 min-h-0 flex-1 [scrollbar-width:thin] space-y-0.5 overflow-y-auto px-2 pb-2"
        {...bindings}
      >
        {first !== undefined && row(first, true)}
        {first !== undefined && rows.length > 0 && (
          <li aria-hidden className="mx-3 my-1.5 border-t border-hairline" />
        )}
        {rows.map((entry) => (
          <Fragment key={keyOf(entry)}>{row(entry, current(entry))}</Fragment>
        ))}
        {loading && rows.length === 0 && first === undefined && <LoadingRows rows={loadingRows} />}
        {!loading && rows.length === 0 && first === undefined && (
          <li className="px-3 py-4 text-hint text-muted-foreground">{empty}</li>
        )}
      </ol>
    </nav>
  )
}

/**
 * One row of the rail: the item's mark with its state as a dot in the mark's
 * corner, two lines of words and a reading at the end. It is a link where the
 * page hands it an address, and a button where opening the item is the
 * page's to do (it carries the open view to the next item).
 */
export function DockerRailRow({
  item,
  name,
  label = name,
  title,
  current,
  href,
  onOpen,
  mark,
  dot,
  end,
  children,
}: {
  /** What Alt+↑ and Alt+↓ walk the rail by. */
  item: string
  name: string
  /** The row's accessible name, where it is not the item's name. */
  label?: string
  title?: string
  current: boolean
  href?: string
  onOpen?: () => void
  mark: React.ReactNode
  /** The item's state, drawn in the mark's corner. */
  dot?: React.ReactNode
  /** The reading at the row's end. */
  end?: React.ReactNode
  /** The row's two lines. */
  children: React.ReactNode
}) {
  const className = cn(
    "flex h-12 w-full min-w-0 items-center gap-2.5 rounded-lg px-2 text-left focus-ring-inset transition-colors",
    current ? "bg-accent" : "hover:bg-row-hover",
  )
  const content = (
    <>
      {dot === undefined ? (
        mark
      ) : (
        <span className="relative flex shrink-0">
          {mark}
          {dot && (
            <span
              aria-hidden
              className="absolute -right-0.5 -bottom-0.5 flex size-2.5 items-center justify-center rounded-full bg-background"
            >
              {dot}
            </span>
          )}
        </span>
      )}
      <span className="min-w-0 flex-1">{children}</span>
      {end}
    </>
  )
  const shared = {
    "data-workspace-item": item,
    "data-workspace-name": name,
    "aria-current": current ? ("page" as const) : undefined,
    "aria-label": label,
    title,
    className,
  }
  return (
    <li className="min-w-0">
      {href !== undefined ? (
        // A rail of a hundred items is not a hundred pages to fetch ahead.
        <Link href={href} prefetch={false} {...shared}>
          {content}
        </Link>
      ) : (
        <button type="button" onClick={onOpen} {...shared}>
          {content}
        </button>
      )}
    </li>
  )
}
