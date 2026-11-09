"use client"

import { useEffect, useRef } from "react"
import { Cpu } from "@/components/icons"
import { bytes, percent } from "@/lib/format"
import type { ProcessRow } from "@/lib/types"
import { cn } from "@/lib/utils"
import { SearchInput } from "@/components/page"
import { ProductLogo, processProduct } from "@/components/product-logo"
import { StatusDot } from "@/components/status-dot"
import { LoadingRows } from "@/components/state"
import { cpuTone, ownerName, processKey, processStateTone } from "@/components/procs/shared"

/**
 * The table, folded into a column beside the process it opened: the same
 * ranking, the same filters and the same search, so the next process to look
 * at is one press away rather than a trip back to the table and a scroll to
 * where it was. Each row is the process as the product it is, its PID, who
 * runs it and its two readings; the one on screen is filled.
 *
 * The rows hold their order while the pointer or focus is in the column
 * (`useInspectionOrder`), so the row about to be pressed does not move.
 */
export function ProcessRail({
  rows,
  current,
  currentRow,
  loading,
  query,
  onQuery,
  filtered,
  onClearFilters,
  caption,
  onOpen,
  bindings,
  className,
}: {
  rows: ProcessRow[]
  current: number | null
  /** The process on screen, drawn first when the list does not hold it. */
  currentRow?: ProcessRow
  loading: boolean
  query: string
  onQuery: (value: string) => void
  filtered: boolean
  onClearFilters: () => void
  caption: string
  onOpen: (pid: number) => void
  bindings: React.HTMLAttributes<HTMLElement>
  className?: string
}) {
  const listed = rows.some((row) => row.pid === current)
  const pinned = !listed && currentRow ? currentRow : undefined
  const list = useRef<HTMLOListElement>(null)
  // The process on screen is kept in view: opened from a deep link or by
  // Alt+↓ past the column's edge, it would otherwise be off its own list.
  useEffect(() => {
    const row = list.current?.querySelector<HTMLElement>("[aria-current='page']")
    row?.scrollIntoView({ block: "nearest" })
  }, [current])

  return (
    <nav
      aria-label="Processes"
      data-slot="process-rail"
      className={cn("min-h-0 flex-col border-r border-hairline pr-4", className)}
    >
      <div className="flex shrink-0 flex-col gap-2 pb-3">
        <SearchInput
          value={query}
          onChange={(event) => onQuery(event.target.value)}
          placeholder="Name, PID, user or owner"
          aria-label="Search the processes"
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
        aria-label="Processes to open"
        className="-mx-2 min-h-0 flex-1 [scrollbar-width:thin] space-y-0.5 overflow-y-auto px-2 pb-2"
        {...bindings}
      >
        {pinned && <RailRow row={pinned} current onOpen={onOpen} />}
        {pinned && rows.length > 0 && (
          <li aria-hidden className="mx-3 my-1.5 border-t border-hairline" />
        )}
        {rows.map((row) => (
          <RailRow key={processKey(row)} row={row} current={row.pid === current} onOpen={onOpen} />
        ))}
        {loading && rows.length === 0 && <LoadingRows rows={8} />}
        {!loading && rows.length === 0 && !pinned && (
          <li className="px-3 py-4 text-hint text-muted-foreground">No process matches.</li>
        )}
      </ol>
    </nav>
  )
}

function RailRow({
  row,
  current,
  onOpen,
}: {
  row: ProcessRow
  current: boolean
  onOpen: (pid: number) => void
}) {
  const tone = cpuTone(row.cpuPercent)
  return (
    <li className="min-w-0">
      <button
        type="button"
        data-workspace-item={processKey(row)}
        data-workspace-name={row.name}
        aria-current={current ? "page" : undefined}
        aria-label={`${row.name}, PID ${row.pid}`}
        onClick={() => onOpen(row.pid)}
        className={cn(
          "flex h-12 w-full min-w-0 items-center gap-2.5 rounded-lg px-2 text-left focus-ring-inset transition-colors",
          current ? "bg-accent" : "hover:bg-row-hover",
        )}
      >
        <span className="relative flex shrink-0">
          <ProductLogo
            id={processProduct(row.name)}
            size="sm"
            fallback={Cpu}
            className="size-7 rounded-md [&_img]:size-4"
          />
          <span
            aria-hidden
            className="absolute -right-0.5 -bottom-0.5 flex size-2.5 items-center justify-center rounded-full bg-background"
          >
            <StatusDot state={processStateTone(row.state)} className="size-1.5" />
          </span>
        </span>
        <span className="min-w-0 flex-1">
          <span className="flex min-w-0 items-baseline gap-1.5">
            <span className={cn("truncate text-body", current ? "font-semibold" : "font-medium")}>
              {row.name}
            </span>
            <span className="numeric shrink-0 font-mono text-micro text-muted-foreground">
              {row.pid}
            </span>
          </span>
          <span className="block truncate text-hint text-muted-foreground">
            {row.manager === "kernel"
              ? "kernel thread"
              : [ownerName(row), row.username].filter(Boolean).join(" · ") || "unmanaged"}
          </span>
        </span>
        <span className="flex shrink-0 flex-col items-end">
          <span
            className={cn(
              "numeric font-mono text-hint",
              tone === "danger"
                ? "font-medium text-destructive"
                : tone === "warning"
                  ? "text-warning"
                  : "text-muted-foreground",
            )}
          >
            {row.cpuReady === false ? "—" : percent(row.cpuPercent)}
          </span>
          <span className="numeric font-mono text-micro text-muted-foreground">
            {row.rss ? bytes(row.rss) : "—"}
          </span>
        </span>
      </button>
    </li>
  )
}
