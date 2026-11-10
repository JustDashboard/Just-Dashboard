"use client"

import { useEffect, useRef } from "react"
import { Servers } from "@/components/icons"
import { bytes, duration, percent } from "@/lib/format"
import type { SystemdUnit } from "@/lib/types"
import { cn } from "@/lib/utils"
import { SearchInput } from "@/components/page"
import { ProductLogo, unitProduct } from "@/components/product-logo"
import { StatusDot } from "@/components/status-dot"
import { LoadingRows } from "@/components/state"
import { cpuTone } from "@/components/procs/shared"
import {
  failureWords,
  unitBucket,
  unitShortName,
  unitStateWord,
  unitTone,
} from "@/components/procs/units"

/**
 * The Services table, folded into a column beside the unit it opened: its
 * filters, its search and its order (failed first, then by name), so the
 * next unit to check is one press away. Each row is the unit as the product
 * it runs with its state in the logo's corner, its name, how it is doing in
 * a few words and, while it runs, its two readings; the one on screen is
 * filled.
 *
 * The rows hold their order while the pointer or focus is in the column
 * (`useInspectionOrder`), so a unit that fails under the pointer does not
 * move the row about to be pressed.
 */
export function ServiceRail({
  units,
  current,
  currentUnit,
  loading,
  query,
  onQuery,
  filtered,
  onClearFilters,
  caption,
  now,
  onOpen,
  bindings,
  className,
}: {
  units: SystemdUnit[]
  current: string
  /** The unit on screen, drawn first when the list does not hold it. */
  currentUnit?: SystemdUnit
  loading: boolean
  query: string
  onQuery: (value: string) => void
  filtered: boolean
  onClearFilters: () => void
  caption: string
  now: number
  onOpen: (name: string) => void
  bindings: React.HTMLAttributes<HTMLElement>
  className?: string
}) {
  const listed = units.some((unit) => unit.name === current)
  const pinned = !listed && currentUnit ? currentUnit : undefined
  const list = useRef<HTMLOListElement>(null)
  // The unit on screen is kept in view: opened from a link or by Alt+↓ past
  // the column's edge, it would otherwise be off its own list.
  useEffect(() => {
    const row = list.current?.querySelector<HTMLElement>("[aria-current='page']")
    row?.scrollIntoView({ block: "nearest" })
  }, [current])

  return (
    <nav
      aria-label="Services"
      data-slot="service-rail"
      className={cn("min-h-0 flex-col border-r border-hairline pr-4", className)}
    >
      <div className="flex shrink-0 flex-col gap-2 pb-3">
        <SearchInput
          value={query}
          onChange={(event) => onQuery(event.target.value)}
          placeholder="Name or description"
          aria-label="Search the services"
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
        aria-label="Services to open"
        className="-mx-2 min-h-0 flex-1 [scrollbar-width:thin] space-y-0.5 overflow-y-auto px-2 pb-2"
        {...bindings}
      >
        {pinned && <RailRow unit={pinned} current now={now} onOpen={onOpen} />}
        {pinned && units.length > 0 && (
          <li aria-hidden className="mx-3 my-1.5 border-t border-hairline" />
        )}
        {units.map((unit) => (
          <RailRow
            key={unit.name}
            unit={unit}
            current={unit.name === current}
            now={now}
            onOpen={onOpen}
          />
        ))}
        {loading && units.length === 0 && <LoadingRows rows={8} />}
        {!loading && units.length === 0 && !pinned && (
          <li className="px-3 py-4 text-hint text-muted-foreground">No service matches.</li>
        )}
      </ol>
    </nav>
  )
}

/** How a unit is doing, in the rail's few words. */
function railLine(unit: SystemdUnit, now: number): { text: string; tone?: "danger" | "warning" } {
  const bucket = unitBucket(unit)
  if (bucket === "failed") return { text: failureWords(unit), tone: "danger" }
  if (unit.subState === "auto-restart") return { text: "restarting", tone: "warning" }
  if (bucket === "changing") return { text: unitStateWord(unit), tone: "warning" }
  if (bucket === "active" && unit.subState === "running" && unit.activeSince) {
    return { text: `up ${duration(Math.max(0, now / 1000 - unit.activeSince))}` }
  }
  if (unit.unitFileState === "masked") return { text: "masked" }
  return { text: unitStateWord(unit) }
}

function RailRow({
  unit,
  current,
  now,
  onOpen,
}: {
  unit: SystemdUnit
  current: boolean
  now: number
  onOpen: (name: string) => void
}) {
  const line = railLine(unit, now)
  const running = unitBucket(unit) === "active" && (unit.cpuReady || (unit.memoryBytes ?? 0) > 0)
  const tone = cpuTone(unit.cpuPercent ?? 0)
  return (
    <li className="min-w-0">
      <button
        type="button"
        data-workspace-item={unit.name}
        data-workspace-name={unit.name}
        aria-current={current ? "page" : undefined}
        aria-label={unit.name}
        title={unit.description}
        onClick={() => onOpen(unit.name)}
        className={cn(
          "flex h-12 w-full min-w-0 items-center gap-2.5 rounded-lg px-2 text-left focus-ring-inset transition-colors",
          current ? "bg-accent" : "hover:bg-row-hover",
        )}
      >
        <span className="relative flex shrink-0">
          <ProductLogo
            id={unitProduct(unit.name)}
            size="sm"
            fallback={Servers}
            className="size-7 rounded-md [&_img]:size-4"
          />
          <span
            aria-hidden
            className="absolute -right-0.5 -bottom-0.5 flex size-2.5 items-center justify-center rounded-full bg-background"
          >
            <StatusDot tone={unitTone(unit)} className="size-1.5" />
          </span>
        </span>
        <span className="min-w-0 flex-1">
          <span
            className={cn("block truncate text-body", current ? "font-semibold" : "font-medium")}
          >
            {unitShortName(unit.name)}
          </span>
          <span
            className={cn(
              "numeric block truncate text-hint",
              line.tone === "danger"
                ? "text-destructive"
                : line.tone === "warning"
                  ? "text-warning"
                  : "text-muted-foreground",
            )}
          >
            {line.text}
          </span>
        </span>
        {running && (
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
              {unit.cpuReady ? percent(unit.cpuPercent ?? 0) : "—"}
            </span>
            <span className="numeric font-mono text-micro text-muted-foreground">
              {(unit.memoryBytes ?? 0) > 0 ? bytes(unit.memoryBytes) : "—"}
            </span>
          </span>
        )}
      </button>
    </li>
  )
}
