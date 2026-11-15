"use client"

import type { Listener, VHost } from "@/lib/types"
import { Box, CornerUpRight, FileText, FolderOpen } from "@/components/icons"
import { ProductLogo } from "@/components/product-logo"
import { StatusDot, type DotTone } from "@/components/status-dot"
import { Rail, RailLine, RailName, RailRow, railInk } from "@/components/rail"
import { siteProduct } from "@/components/proxy/marks"
import { siteApp } from "@/components/proxy/site-apps"
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

/** The glyph for what a site serves, where no logo names it. */
function kindGlyph(vhost: VHost) {
  if (isRedirect(vhost)) return CornerUpRight
  if (isStatic(vhost)) return FolderOpen
  return vhost.upstreams.length > 0 ? Box : FileText
}

/** A row's identity: two entries may share a name across layouts. */
const railKey = (v: VHost) => `${v.kind}:${v.layout ?? ""}:${v.name}`

/**
 * The Sites list, folded into a column beside the site it opened: the list's
 * own search, chip and order, so the next site to look at is one press away
 * and the view on screen goes with it. Each row is the site as what it fronts
 * over the engine serving it, its state in the tile's corner, its name, and
 * where its requests go — or, for one that is not serving, why. The one on
 * screen is filled; a disabled site's words are in the muted ink.
 */
export function SiteRail({
  sites,
  current,
  tab,
  loading,
  error,
  onRetry,
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
  /** The list could not be read: said in place of the rows. */
  error?: Error
  onRetry?: () => void
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
  return (
    <Rail
      slot="site-rail"
      label="Sites"
      search={{ placeholder: "Name, domain, upstream or port", query, onQuery }}
      rows={sites}
      keyOf={railKey}
      current={(vhost) => vhost.name === current}
      row={(vhost, here) => {
        const state = railState(vhost)
        const destination = siteDestination(vhost)
        return (
          <RailRow
            item={railKey(vhost)}
            name={vhost.name}
            current={here}
            href={siteHref(vhost.name, tab === "settings" ? "overview" : tab)}
            quiet={!vhost.enabled}
            // One tile on every row, so the names stand on one edge: the
            // application where the host can name it, the kind of thing the
            // site serves otherwise, the engine failing both.
            mark={
              <ProductLogo
                id={siteApp(vhost, listeners, [])?.product ?? siteProduct(vhost)}
                size="sm"
                fallback={kindGlyph(vhost)}
                className="size-7 rounded-md [&_img]:size-4"
              />
            }
            dot={<StatusDot tone={state.tone} className="size-1.5" />}
          >
            <RailName current={here}>{vhost.name}</RailName>
            <RailLine
              ink={state.word ? railInk(state.tone) : undefined}
              className={state.word ? undefined : "font-mono"}
            >
              {state.word ?? (destination ? `→ ${destination}` : "served by configuration")}
            </RailLine>
          </RailRow>
        )
      }}
      loading={loading}
      error={error}
      onRetry={onRetry}
      empty="No site matches."
      filtered={filtered}
      onClearFilters={onClearFilter}
      caption={caption}
      className={className}
    />
  )
}
