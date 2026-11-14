"use client"

import { useEffect, useRef } from "react"
import Link from "next/link"
import { cn } from "@/lib/utils"
import type { Listener, VHost } from "@/lib/types"
import { SearchInput } from "@/components/page"
import { StatusDot, type DotTone } from "@/components/status-dot"
import { LoadingRows } from "@/components/state"
import { siteApp } from "@/components/proxy/site-apps"
import { SiteMark } from "@/components/proxy/site-band"
import { upstreamTargets } from "@/components/proxy/site-details"
import { isRedirect, isStatic, redirectTargets } from "@/components/proxy/site-filters"
import { siteHref, type SiteTab } from "@/components/proxy/site-tabs"

/** Where a site's requests end up, as one mono line; empty where its file says none of these. */
export function siteDestination(vhost: VHost): string {
  if (isRedirect(vhost)) return redirectTargets(vhost).join(", ")
  if (isStatic(vhost)) return (vhost.roots ?? []).join(", ")
  return upstreamTargets(vhost).join(", ")
}

/**
 * What the listing alone says about a site: a link that serves nothing, a
 * site switched off, or one that is on. The list's fuller word — not loaded,
 * its engine stopped — needs readings the Sites page takes and a site's page
 * does not, so the rail claims no more than this.
 */
function railState(vhost: VHost): { tone: DotTone; word?: string } {
  if (vhost.broken === "dangling") return { tone: "danger", word: "broken link" }
  if (vhost.broken === "stale") return { tone: "warning", word: "stale link" }
  if (!vhost.enabled) return { tone: "stopped", word: "disabled" }
  if (vhost.maintenance) return { tone: "warning", word: "maintenance" }
  return { tone: "running" }
}

/** A row's identity: two entries may share a name across layouts. */
const railKey = (v: VHost) => `${v.kind}:${v.layout ?? ""}:${v.name}`

/**
 * The Sites list, folded into a column beside the site it opened: the list's
 * own search, chip and order, so the next site to look at is one press away
 * and the view on screen goes with it. Each row is the site as what it fronts
 * over the engine serving it, its state in the tile's corner, its name, and
 * where its requests go — or, for one that is not serving, why. The one on
 * screen is filled.
 */
export function SiteRail({
  sites,
  current,
  tab,
  loading,
  listeners,
  query,
  onQuery,
  filtered,
  onClearFilter,
  caption,
  className,
}: {
  sites: VHost[]
  current: string
  /** The view on screen, carried to the next site. */
  tab: SiteTab
  loading: boolean
  /** The host's sockets, for the application a site fronts. */
  listeners: Listener[] | undefined
  query: string
  onQuery: (value: string) => void
  /** The list's chip is one other than All. */
  filtered: boolean
  onClearFilter: () => void
  caption: string
  className?: string
}) {
  const list = useRef<HTMLOListElement>(null)
  // The site on screen is kept in view: opened by Alt+↓ past the column's
  // edge, it would otherwise be off its own list.
  useEffect(() => {
    const row = list.current?.querySelector<HTMLElement>("[aria-current='page']")
    row?.scrollIntoView({ block: "nearest" })
  }, [current])

  return (
    <nav
      aria-label="Sites"
      data-slot="site-rail"
      className={cn("min-h-0 flex-col border-r border-hairline pr-4", className)}
    >
      <div className="flex shrink-0 flex-col gap-2 pb-3">
        <SearchInput
          value={query}
          onChange={(event) => onQuery(event.target.value)}
          placeholder="Name, domain, upstream or port"
          aria-label="Search the sites"
          containerClassName="sm:w-full"
        />
        <p className="flex min-w-0 items-center justify-between gap-2 text-hint text-muted-foreground">
          <span className="truncate">{caption}</span>
          {filtered && (
            <button
              type="button"
              onClick={onClearFilter}
              className="shrink-0 rounded-sm font-medium focus-ring hover:text-foreground"
            >
              Clear filter
            </button>
          )}
        </p>
      </div>
      <ol
        ref={list}
        aria-label="Sites to open"
        className="-mx-2 min-h-0 flex-1 [scrollbar-width:thin] space-y-0.5 overflow-y-auto px-2 pb-2"
      >
        {sites.map((vhost) => {
          const here = vhost.name === current
          const state = railState(vhost)
          const destination = siteDestination(vhost)
          return (
            <li key={railKey(vhost)} className="min-w-0">
              <Link
                href={siteHref(vhost.name, tab === "settings" ? "overview" : tab)}
                aria-current={here ? "page" : undefined}
                aria-label={vhost.name}
                className={cn(
                  "flex h-12 w-full min-w-0 items-center gap-2.5 rounded-lg px-2 text-left focus-ring-inset transition-colors",
                  here ? "bg-accent" : "hover:bg-row-hover",
                  !vhost.enabled && "opacity-80",
                )}
              >
                <span className="relative flex shrink-0">
                  <SiteMark vhost={vhost} app={siteApp(vhost, listeners, [])} size="sm" />
                  <span
                    aria-hidden
                    className="absolute -right-0.5 -bottom-0.5 flex size-2.5 items-center justify-center rounded-full bg-background"
                  >
                    <StatusDot tone={state.tone} className="size-1.5" />
                  </span>
                </span>
                <span className="min-w-0 flex-1">
                  <span
                    className={cn(
                      "block truncate text-body",
                      here ? "font-semibold" : "font-medium",
                    )}
                  >
                    {vhost.name}
                  </span>
                  <span
                    className={cn(
                      "block truncate text-hint",
                      state.word
                        ? state.tone === "danger"
                          ? "text-destructive"
                          : state.tone === "warning"
                            ? "text-warning"
                            : "text-muted-foreground"
                        : "font-mono text-muted-foreground",
                    )}
                  >
                    {state.word ?? (destination ? `→ ${destination}` : "served by configuration")}
                  </span>
                </span>
              </Link>
            </li>
          )
        })}
        {loading && sites.length === 0 && <LoadingRows rows={6} />}
        {!loading && sites.length === 0 && (
          <li className="px-3 py-4 text-hint text-muted-foreground">No site matches.</li>
        )}
      </ol>
    </nav>
  )
}
