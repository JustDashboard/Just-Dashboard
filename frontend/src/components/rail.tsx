"use client"

import { Fragment, createContext, useContext, useEffect, useRef } from "react"
import Link from "next/link"
import { cn } from "@/lib/utils"
import { SearchInput } from "@/components/search-input"
import { ErrorState } from "@/components/state"
import { Skeleton } from "@/components/ui/skeleton"

/**
 * The list a detail page was opened from, folded into a column beside it from
 * `xl` (D9): a process's, a container's, a site's, an account's. Twenty-two
 * rails were written by hand, each with the same column, the same 48px row and
 * the same scroll into view, and each drifting in what it did not share: some
 * rows were buttons that could not open in a new tab, six loaded with 36px
 * bars inside a column of 48px rows, and a list that failed to load said "No
 * stack matches." This is the one column; a page's own rail file keeps only
 * where its list comes from and what its row says (`components/rail.md`).
 *
 * The head is one anatomy: the search, then the filters, then one line with
 * the count and the way out of the filters. The width is `w-rail` whatever a
 * caller asks, so sibling families keep their titles on one edge.
 */
export function Rail<T>({
  slot,
  label,
  listLabel = `${label} to open`,
  rows,
  keyOf,
  row,
  current,
  pinned,
  loading,
  loadingRows = 6,
  error,
  onRetry,
  empty,
  search,
  filters,
  caption,
  action,
  filtered = false,
  onClearFilters,
  divideBefore,
  foot,
  bindings,
  className,
}: {
  /** The rail's `data-slot`, which its page's specs find it by. */
  slot: string
  /** What the list is of, in the plural: "Volumes". */
  label: string
  /** The list's own name, where "<label> to open" would not read: "Groups to open". */
  listLabel?: string
  rows: readonly T[]
  keyOf: (row: T) => string
  /** One row of the list (a `RailRow`); `current` is whether it is the item on screen. */
  row: (row: T, current: boolean) => React.ReactNode
  /** Whether a row is the item on screen. */
  current: (row: T) => boolean
  /** The item on screen, drawn first and set apart when the list does not hold it. */
  pinned?: T
  loading: boolean
  /** How many ghost rows stand in while the list is read. */
  loadingRows?: number
  /** The list could not be read: said in place of the rows, never as "No x matches". */
  error?: Error
  onRetry?: () => void
  /** What the list says when nothing matches: "No volume matches." */
  empty?: string
  /** The list's own search, where it has one. */
  search?: { label?: string; placeholder: string; query: string; onQuery: (value: string) => void }
  /** The list's chips or switch, under the search. */
  filters?: React.ReactNode
  /** What the list holds and how it is ordered: "12 containers · failing first". */
  caption?: React.ReactNode
  /** A switch at the caption line's end, in place of the way out of the filters. */
  action?: React.ReactNode
  /** A chip or filter other than the search is on, and `onClearFilters` takes it off. */
  filtered?: boolean
  onClearFilters?: () => void
  /** A rule (true) or a group's name (a string) over a row. */
  divideBefore?: (row: T, index: number) => boolean | string | undefined
  /** A last line under the rows: "The first 300 of 2,114." */
  foot?: React.ReactNode
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

  const nothing = rows.length === 0 && first === undefined
  // "No x matches" is the search's answer, so it is said under the item on
  // screen too; a list that failed or is still being read never says it.
  const none = !loading && !error && rows.length === 0 && Boolean(empty)

  return (
    <RailColumn
      slot={slot}
      label={label}
      listLabel={listLabel}
      listRef={list}
      busy={loading && nothing}
      search={search}
      filters={filters}
      caption={caption}
      action={
        filtered && onClearFilters ? (
          <RailAction onClick={onClearFilters}>Clear filters</RailAction>
        ) : (
          action
        )
      }
      bindings={bindings}
      className={className}
    >
      {first !== undefined && row(first, true)}
      {first !== undefined && (rows.length > 0 || error || none) && <RailRule />}
      {error && rows.length === 0 ? (
        <li className="min-w-0">
          <ErrorState error={error} onRetry={onRetry} retryable={Boolean(onRetry)} />
        </li>
      ) : (
        rows.map((entry, index) => {
          const divide = divideBefore?.(entry, index)
          return (
            <Fragment key={keyOf(entry)}>
              {typeof divide === "string" ? (
                <li aria-hidden className="eyebrow px-2 pt-3 pb-1 first:pt-0">
                  {divide}
                </li>
              ) : (
                divide && <RailRule />
              )}
              {row(entry, current(entry))}
            </Fragment>
          )
        })
      )}
      {loading && nothing && !error && <RailGhosts rows={loadingRows} />}
      {none && <RailNote>{empty}</RailNote>}
      {foot && <li className="px-3 py-3 text-hint text-muted-foreground">{foot}</li>}
    </RailColumn>
  )
}

/**
 * The rail's column without its rows: the head (search, filters, the caption
 * line) and the scrolling list. `Rail` draws its rows into it; a page that
 * draws its own (the legacy `DetailRail`) hands them as children.
 */
export function RailColumn({
  slot,
  label,
  listLabel = `${label} to open`,
  listRef,
  busy,
  search,
  filters,
  caption,
  action,
  bindings,
  className,
  children,
}: {
  slot: string
  label: string
  listLabel?: string
  listRef?: React.Ref<HTMLOListElement>
  /** The list is being read and has nothing yet. */
  busy?: boolean
  search?: { label?: string; placeholder: string; query: string; onQuery: (value: string) => void }
  filters?: React.ReactNode
  caption?: React.ReactNode
  /** The caption line's end: the way out of the filters, or a switch. */
  action?: React.ReactNode
  bindings?: React.HTMLAttributes<HTMLElement>
  className?: string
  children: React.ReactNode
}) {
  const head = search || filters || caption !== undefined || action
  return (
    <nav
      aria-label={label}
      data-slot={slot}
      // The width is the rail's, after the caller's classes: a page that still
      // asks for its own (`w-64`, `w-80`) gets the one every sibling has.
      className={cn(
        "hidden min-h-0 shrink-0 flex-col border-r border-hairline pr-4 xl:flex",
        className,
        "w-rail",
      )}
    >
      {head && (
        <div className="flex shrink-0 flex-col gap-2 pb-3">
          {search && (
            <SearchInput
              value={search.query}
              onChange={(event) => search.onQuery(event.target.value)}
              placeholder={search.placeholder}
              aria-label={search.label ?? `Search the ${label.toLowerCase()}`}
              containerClassName="sm:w-full"
            />
          )}
          {filters}
          {(caption !== undefined || action) && (
            <p className="flex min-w-0 items-center justify-between gap-2 text-hint text-muted-foreground">
              <span className="numeric truncate">{caption}</span>
              {action}
            </p>
          )}
        </div>
      )}
      <ol
        ref={listRef}
        aria-label={listLabel}
        aria-busy={busy || undefined}
        className="-mx-2 min-h-0 flex-1 [scrollbar-width:thin] space-y-0.5 overflow-y-auto px-2 pb-2"
        {...bindings}
      >
        {children}
      </ol>
    </nav>
  )
}

/** A word-button at the caption line's end: "Clear filters", "System groups". */
export function RailAction({
  pressed,
  onClick,
  children,
}: {
  /** A switch says whether it is on. */
  pressed?: boolean
  onClick: () => void
  children: React.ReactNode
}) {
  return (
    <button
      type="button"
      aria-pressed={pressed}
      onClick={onClick}
      className="shrink-0 rounded-sm font-medium focus-ring hover:text-foreground"
    >
      {children}
    </button>
  )
}

/** The line a list says in place of rows: "No volume matches." */
export function RailNote({ children }: { children: React.ReactNode }) {
  return <li className="px-3 py-4 text-hint text-muted-foreground">{children}</li>
}

/** The rule that sets the item on screen, or a shelf of the list, apart. */
function RailRule() {
  return <li aria-hidden className="mx-3 my-1.5 border-t border-hairline" />
}

/** The class every rail row is drawn with: one 48px row in every state. */
export function railRowClass(current: boolean, className?: string) {
  return cn(
    "flex h-12 w-full min-w-0 items-center gap-2.5 rounded-lg px-2 text-left focus-ring-inset transition-colors",
    current ? "bg-accent" : "hover:bg-row-hover",
    className,
  )
}

/**
 * One row of the rail: the item's mark with its state as a dot in the mark's
 * corner, two lines of words and a reading at the end. It is a link, so it
 * opens in a new tab and is announced as one. Where the page carries state the
 * address does not hold, `onOpen` takes an unmodified press instead of the
 * link; it returns `false` to let the link follow after all (a page with
 * unsaved work asks first and answers whether it took the press).
 */
export function RailRow({
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
  quiet,
  children,
}: {
  /** What Alt+↑ and Alt+↓ and the workspace's keys walk the rail by. */
  item: string
  name: string
  /** The row's accessible name, where it is not the item's name. */
  label?: string
  title?: string
  current: boolean
  /** Where the row goes. Without one the row is a button (deprecated: every rail has an address). */
  href?: string
  onOpen?: () => boolean | void
  mark: React.ReactNode
  /** The item's state, drawn in the mark's corner. */
  dot?: React.ReactNode
  /** The reading at the row's end. */
  end?: React.ReactNode
  /** An item that is switched off or not in use: its words in the muted ink, never faded. */
  quiet?: boolean
  /** The row's two lines: a `RailName` over a `RailLine`. */
  children: React.ReactNode
}) {
  const className = railRowClass(current, quiet ? "text-muted-foreground" : undefined)
  const content = (
    <>
      {dot === undefined ? (
        <span className="flex shrink-0">{mark}</span>
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
      {label !== undefined && end ? (
        // A flex item of the row's own, so what it holds stays on the row's
        // centre line as it did unwrapped.
        <span aria-hidden className="flex shrink-0 items-center">
          {end}
        </span>
      ) : (
        end
      )}
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
        <Link
          href={href}
          // A rail of a hundred items is not a hundred pages to fetch ahead,
          // and the column keeps its place while the page beside it changes.
          prefetch={false}
          scroll={false}
          onClick={
            onOpen &&
            ((event) => {
              if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return
              if (event.button !== 0) return
              if (onOpen() !== false) event.preventDefault()
            })
          }
          {...shared}
        >
          <Labelled.Provider value={label !== undefined}>{content}</Labelled.Provider>
        </Link>
      ) : (
        <button type="button" onClick={() => onOpen?.()} {...shared}>
          <Labelled.Provider value={label !== undefined}>{content}</Labelled.Provider>
        </button>
      )}
    </li>
  )
}

/**
 * Whether the row is named by its `label` rather than by what it shows. Its
 * name is then the label, which starts with the item's name, and the row's
 * second line and its end are said by the label or not at all: a voice user
 * says the name they see, and the name a screen reader hears holds the words
 * on screen (WCAG 2.5.3, axe `label-content-name-mismatch`). The second
 * line's words are hidden from the name's check, as they were already left
 * out of the name.
 */
const Labelled = createContext(false)

/** A rail row's first line: the item's name, heavier on the one on screen. */
export function RailName({
  current,
  mono,
  className,
  children,
}: {
  current: boolean
  /** A name the machine spells: a device, an account, a volume. */
  mono?: boolean
  className?: string
  children: React.ReactNode
}) {
  return (
    <span
      className={cn(
        "block truncate text-body",
        mono && "font-mono",
        current ? "font-semibold" : "font-medium",
        className,
      )}
    >
      {children}
    </span>
  )
}

/** A rail row's second line, in the muted ink unless the row gives it another. */
export function RailLine({
  ink,
  className,
  style,
  children,
}: {
  /** The line's ink where it says something to look at: `railInk("danger")`. */
  ink?: string
  className?: string
  style?: React.CSSProperties
  children: React.ReactNode
}) {
  const labelled = useContext(Labelled)
  return (
    <span
      aria-hidden={labelled || undefined}
      className={cn("block truncate text-hint", ink || "text-muted-foreground", className)}
      style={style}
    >
      {children}
    </span>
  )
}

/** The two readings at a row's end, one over the other: CPU over memory. */
export function RailFigures({
  top,
  bottom,
  ink,
}: {
  top: React.ReactNode
  bottom: React.ReactNode
  /** The first figure's ink, where it is a warning. */
  ink?: string
}) {
  return (
    <span className="flex shrink-0 flex-col items-end">
      <span className={cn("numeric font-mono text-hint", ink || "text-muted-foreground")}>
        {top}
      </span>
      <span className="numeric font-mono text-micro text-muted-foreground">{bottom}</span>
    </span>
  )
}

/** One reading at a row's end: a size, a count, a time left. */
export function RailFigure({
  ink,
  mono = true,
  title,
  children,
}: {
  ink?: string
  /** Figures are mono; a word at the end ("unused") is not. */
  mono?: boolean
  title?: string
  children: React.ReactNode
}) {
  return (
    <span
      title={title}
      className={cn(
        "numeric shrink-0 text-hint",
        mono && "font-mono",
        ink || "text-muted-foreground",
      )}
    >
      {children}
    </span>
  )
}

/**
 * A rail's rows before its list is read: the 48px row of the list that comes,
 * a mark and a name, so the column has its final shape while it loads.
 */
export function RailGhosts({ rows = 6 }: { rows?: number }) {
  return (
    <>
      {Array.from({ length: rows }, (_, i) => (
        <li
          key={i}
          aria-hidden
          data-slot="rail-ghost"
          className="flex h-12 items-center gap-2.5 px-2"
        >
          <Skeleton className="size-7 shrink-0 rounded-md" />
          <Skeleton className="h-3 flex-1" style={{ maxWidth: `${50 + ((i * 17) % 40)}%` }} />
        </li>
      ))}
    </>
  )
}

/** The tone of a reading as its ink: a warning amber, a danger red. */
export function railInk(tone: "danger" | "warning" | undefined | null | string) {
  return tone === "danger" ? "text-destructive" : tone === "warning" ? "text-warning" : ""
}
