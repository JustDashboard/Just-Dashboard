"use client"

import { useEffect, useRef } from "react"
import Link from "next/link"
import { cn } from "@/lib/utils"
import { SearchInput } from "@/components/page"
import { LoadingRows } from "@/components/state"

export type RailItem = {
  id: string
  href: string
  /** The row's accessible name. */
  label: string
  mark: React.ReactNode
  title: React.ReactNode
  line: React.ReactNode
  /** The reading at the row's end: a state, a time. */
  end?: React.ReactNode
}

/**
 * The list a capture or a saved run was opened from, folded into a column
 * beside it (the process page's rail, `procs/process-rail.tsx`): the list's
 * own search and chips, the item on screen filled, each row a link carrying
 * the view that is open. From `xl` only; narrower, the page is one pane.
 */
export function ItemRail({
  label,
  items,
  current,
  loading,
  query,
  onQuery,
  placeholder,
  caption,
  filters,
  className,
}: {
  label: string
  items: RailItem[]
  current: string
  loading: boolean
  query: string
  onQuery: (value: string) => void
  placeholder: string
  caption: React.ReactNode
  /** The list's chips, where it has them. */
  filters?: React.ReactNode
  className?: string
}) {
  const list = useRef<HTMLOListElement>(null)
  // The item on screen is kept in view, opened from a link or walked to with
  // Alt+↓ past the column's edge.
  useEffect(() => {
    list.current
      ?.querySelector<HTMLElement>("[aria-current='page']")
      ?.scrollIntoView({ block: "nearest" })
  }, [current])
  return (
    <nav
      aria-label={label}
      data-slot="item-rail"
      className={cn("min-h-0 flex-col border-r border-hairline pr-4", className)}
    >
      <div className="flex shrink-0 flex-col gap-2 pb-3">
        <SearchInput
          value={query}
          onChange={(event) => onQuery(event.target.value)}
          placeholder={placeholder}
          aria-label={`Search ${label.toLowerCase()}`}
          containerClassName="sm:w-full"
        />
        {filters}
        <p className="truncate text-hint text-muted-foreground">{caption}</p>
      </div>
      <ol
        ref={list}
        aria-label={`${label} to open`}
        className="-mx-2 min-h-0 flex-1 [scrollbar-width:thin] space-y-0.5 overflow-y-auto px-2 pb-2"
      >
        {items.map((item) => {
          const on = item.id === current
          return (
            <li key={item.id} className="min-w-0">
              <Link
                href={item.href}
                aria-current={on ? "page" : undefined}
                aria-label={item.label}
                className={cn(
                  "flex h-12 w-full min-w-0 items-center gap-2.5 rounded-lg px-2 text-left focus-ring-inset transition-colors",
                  on ? "bg-accent" : "hover:bg-row-hover",
                )}
              >
                {item.mark}
                <span className="min-w-0 flex-1">
                  <span
                    className={cn("block truncate text-body", on ? "font-semibold" : "font-medium")}
                  >
                    {item.title}
                  </span>
                  <span className="block truncate font-mono text-micro text-muted-foreground">
                    {item.line}
                  </span>
                </span>
                {item.end && <span className="shrink-0">{item.end}</span>}
              </Link>
            </li>
          )
        })}
        {loading && items.length === 0 && <LoadingRows rows={6} />}
        {!loading && items.length === 0 && (
          <li className="px-3 py-4 text-hint text-muted-foreground">Nothing matches.</li>
        )}
      </ol>
    </nav>
  )
}
