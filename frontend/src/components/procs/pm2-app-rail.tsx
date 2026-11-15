"use client"

import { useEffect, useRef } from "react"
import { ChartActivity } from "@/components/icons"
import { bytes, percent } from "@/lib/format"
import { cn } from "@/lib/utils"
import { SearchInput } from "@/components/page"
import { ProductLogo, pm2Product } from "@/components/product-logo"
import { StatusDot } from "@/components/status-dot"
import { LoadingRows } from "@/components/state"
import { pm2AppState, pm2ModeWords } from "@/components/procs/pm2-facts"
import { pm2Failing, type PM2App } from "@/components/procs/pm2-shared"
import { cpuTone } from "@/components/procs/shared"

/**
 * The PM2 list, folded into a column beside the application it opened: its
 * search, its state chip and its order — what is crashing first, then by
 * name — so the next application to check is one press away. A cluster is one
 * row here, as it is one span of the list's band: each row is the application
 * as what runs it with its state in the logo's corner, its name, how it is
 * doing in a few words and, while it runs, what it takes; the one on screen
 * is filled.
 *
 * The rows hold their order while the pointer or focus is in the column
 * (`useInspectionOrder`), so an application that crashes under the pointer
 * does not move the row about to be pressed.
 */
export function PM2AppRail({
  apps,
  current,
  several,
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
  apps: PM2App[]
  /** The key of the application on screen. */
  current: string | null
  /** More than one account runs PM2, so each row says whose it is. */
  several: boolean
  loading: boolean
  query: string
  onQuery: (value: string) => void
  filtered: boolean
  onClearFilters: () => void
  caption: string
  onOpen: (app: PM2App) => void
  bindings: React.HTMLAttributes<HTMLElement>
  className?: string
}) {
  const list = useRef<HTMLOListElement>(null)
  // The application on screen is kept in view: opened from a link or by
  // Alt+↓ past the column's edge, it would otherwise be off its own list.
  useEffect(() => {
    const row = list.current?.querySelector<HTMLElement>("[aria-current='page']")
    row?.scrollIntoView({ block: "nearest" })
  }, [current])

  return (
    <nav
      aria-label="PM2 applications"
      data-slot="pm2-rail"
      className={cn("min-h-0 flex-col border-r border-hairline pr-4", className)}
    >
      <div className="flex shrink-0 flex-col gap-2 pb-3">
        <SearchInput
          value={query}
          onChange={(event) => onQuery(event.target.value)}
          placeholder="Name, script or account"
          aria-label="Search the applications"
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
        aria-label="Applications to open"
        className="-mx-2 min-h-0 flex-1 [scrollbar-width:thin] space-y-0.5 overflow-y-auto px-2 pb-2"
        {...bindings}
      >
        {apps.map((app) => (
          <RailRow
            key={app.key}
            app={app}
            current={app.key === current}
            several={several}
            onOpen={onOpen}
          />
        ))}
        {loading && apps.length === 0 && <LoadingRows rows={6} />}
        {!loading && apps.length === 0 && (
          <li className="px-3 py-4 text-hint text-muted-foreground">No application matches.</li>
        )}
      </ol>
    </nav>
  )
}

/** How an application is doing, in the rail's few words. */
function railLine(app: PM2App, several: boolean): { text: string; tone?: "danger" | "warning" } {
  const state = pm2AppState(app)
  const whose = several ? `${app.daemonId} · ` : ""
  const unstable = app.instances.reduce((n, p) => n + p.unstableRestarts, 0)
  if (app.online === 0 && app.instances.some(pm2Failing)) {
    return {
      text: `${whose}${state.word}${unstable > 0 ? ` · ${unstable} unstable` : ""}`,
      tone: "danger",
    }
  }
  if (state.partial) return { text: `${whose}${state.word}`, tone: "warning" }
  if (app.online === 0) return { text: `${whose}${state.word}` }
  return { text: `${whose}${pm2ModeWords(app)}` }
}

function RailRow({
  app,
  current,
  several,
  onOpen,
}: {
  app: PM2App
  current: boolean
  several: boolean
  onOpen: (app: PM2App) => void
}) {
  const line = railLine(app, several)
  const state = pm2AppState(app)
  const tone = cpuTone(app.cpu)
  return (
    <li className="min-w-0">
      <button
        type="button"
        data-workspace-item={app.key}
        data-workspace-name={app.name}
        aria-current={current ? "page" : undefined}
        aria-label={several ? `${app.name}, ${app.daemonId}` : app.name}
        onClick={() => onOpen(app)}
        className={cn(
          "flex h-12 w-full min-w-0 items-center gap-2.5 rounded-lg px-2 text-left focus-ring-inset transition-colors",
          current ? "bg-accent" : "hover:bg-row-hover",
        )}
      >
        <span className="relative flex shrink-0">
          <ProductLogo
            id={pm2Product(app.interpreter)}
            size="sm"
            fallback={ChartActivity}
            className="size-7 rounded-md [&_img]:size-4"
          />
          <span
            aria-hidden
            className="absolute -right-0.5 -bottom-0.5 flex size-2.5 items-center justify-center rounded-full bg-background"
          >
            {state.partial ? (
              <StatusDot tone="warning" className="size-1.5" />
            ) : (
              <StatusDot state={state.status} className="size-1.5" />
            )}
          </span>
        </span>
        <span className="min-w-0 flex-1">
          <span
            className={cn("block truncate text-body", current ? "font-semibold" : "font-medium")}
          >
            {app.name}
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
        {app.online > 0 && (
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
              {percent(app.cpu)}
            </span>
            <span className="numeric font-mono text-micro text-muted-foreground">
              {bytes(app.memory)}
            </span>
          </span>
        )}
      </button>
    </li>
  )
}
