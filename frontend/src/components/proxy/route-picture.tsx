"use client"

import { useMemo } from "react"
import {
  Box,
  CornerUpRight,
  FileText,
  FolderOpen,
  Globe,
  LockClosed,
  LockOpen,
} from "@/components/icons"
import { cn } from "@/lib/utils"
import { ProductGlyph, hasProductLogo } from "@/components/product-logo"
import { StatusDot, type DotTone } from "@/components/status-dot"
import { WireMark } from "@/components/wire/wire"
import { WireFlow, type WireFlowNode, type WireFlowWaiting } from "@/components/wire/wire-flow"
import { MoreTile } from "@/components/wire/wire-marks"
import { compactCount } from "@/lib/format"
import { ProjectFavicon } from "@/components/project-mark"
import { sitePath } from "@/components/proxy/site-verbs"
import { errorRateTone, share, trafficLabel } from "@/components/proxy/site-traffic"
import { upstreamLabel, upstreamTone } from "@/components/proxy/upstream-health"
import {
  CARRIES_PER_HOUR,
  requestPulse,
  type MapApp,
  type MapDomain,
  type RouteMap,
} from "@/components/proxy/route-map"

type MarkSize = "lg" | "md" | "sm"

const KIND_GLYPH = { files: FolderOpen, redirect: CornerUpRight, config: FileText, app: Box }

/** A node waits on its name and one line under it: neither side has an eyebrow. */
const WAITING: WireFlowWaiting = { eyebrow: false, hint: 1 }

/**
 * The proxy as the thing it is: the domains it answers for on the left, the
 * engine in the middle, and on the right what each domain is sent to, drawn
 * as itself — Grafana as Grafana, a Node program as Node — with its state in
 * the tile's corner. The Network topology's shape (`network/topology.tsx`),
 * and since 0.9.1 the same `WireFlow` that draws it.
 *
 * A wire is the last hour of the route's traffic, read from its access log:
 * it carries a pulse while the route served more than a request a minute,
 * quicker the busier it was (`requestPulse`), running toward the application.
 * It is still while the route is idle, amber where a domain is served in
 * plain HTTP, red where the upstream refuses or is gone — dashed then,
 * because nothing arrives at the end of it — and amber where it times out.
 * Pointing at a domain steps back every wire but its own and lights the
 * application it reaches; pointing at an application lights its domains.
 *
 * It was laid by hand as one column a side, each name beside its mark, in
 * the middle of a wide page with nothing on either side of it. As a
 * `WireFlow` the domains and the applications are rows on level lines through
 * the engine, as wide as the page, and a column a side only where a side has
 * no room for three across. A node's mark is the way to its page; its words
 * are a name and one line, with what there is no room for a hover away.
 *
 * On a phone it is the one phone form (`WireMarks`): the domains' locks,
 * the engine named, the applications' marks and the wires between them; the
 * Routes list under it names each one.
 *
 * `only` draws the routes of some domains alone, and what they reach: Sites
 * opening on the picture of its own routes. `loading` draws the shape it
 * will have, the engine named and grey nodes a side, while the sites are read.
 */
export function RoutePicture({
  map: whole,
  only,
  engine,
  engineMark,
  engineTone,
  engineHint,
  loading,
}: {
  map: RouteMap
  /** The domains to draw; every one the map holds when left out. */
  only?: (domain: MapDomain) => boolean
  /** The engine's name as the identity line gives it. */
  engine: string
  engineMark?: string
  /** The engine's own state, in the tile's corner. */
  engineTone: DotTone
  engineHint?: React.ReactNode
  /** The sites are not read yet: the picture waits at the shape it will have. */
  loading?: boolean
}) {
  const map = useMemo(() => narrowed(whole, only), [whole, only])

  // What a node is joined to through the engine: a domain's applications,
  // an application's domains.
  const joined = (a: string, b: string) => {
    const [domain, app] = a.startsWith("d:") ? [a, b] : [b, a]
    if (!domain.startsWith("d:") || !app.startsWith("a:")) return false
    return map.domains.find((d) => `d:${d.id}` === domain)?.apps.includes(app.slice(2)) ?? false
  }

  const domains = map.domains.map(domainNode)
  if (map.hidden > 0) {
    // The map is already cut to what the picture has room for (`MAP_LIMIT`),
    // so the tile for the rest is a node of the picture's own, not `max`'s.
    const words = `${map.hidden} more on Sites`
    domains.push({
      id: "d:more",
      placeholder: true,
      href: "/proxy/sites",
      name: words,
      mark: (size) => <MoreTile count={map.hidden} size={size} />,
      title: <span className="font-normal text-muted-foreground">{words}</span>,
    })
  }

  return (
    <div data-slot="route-picture" className="animate-rise">
      <WireFlow
        phone="marks"
        loading={loading}
        related={joined}
        start={{
          title: "Domains",
          count: loading ? undefined : map.total,
          heading: "always",
          nodes: domains,
          loadingLines: WAITING,
        }}
        hub={{
          mark: (
            <span className="relative flex">
              <WireMark tone="logo" size="lg">
                {engineMark && hasProductLogo(engineMark) ? (
                  <ProductGlyph id={engineMark} />
                ) : (
                  <Globe aria-hidden />
                )}
              </WireMark>
              <StatusDot tone={engineTone} corner="lg" />
            </span>
          ),
          eyebrow: "Reverse proxy",
          title: engine,
          hint: engineHint,
          // The engine is known before its sites are: its name waits as
          // itself, and so does the line under it once its ports are read.
          loadingLines: { title: engine, hint: engineHint ? 0 : 1, foot: engineHint },
        }}
        end={{
          title: "Applications",
          count: loading ? undefined : map.apps.length,
          heading: "always",
          nodes: map.apps.map(appNode),
          loadingLines: WAITING,
        }}
      />
    </div>
  )
}

/**
 * A node's line of detail: whole facts side by side, set by the node's own
 * alignment (under the mark in a row, beside it in a column), and wrapping
 * between two of them rather than inside one.
 */
function Facts({ title, children }: { title?: string; children: React.ReactNode }) {
  return (
    <span className="-mx-1 block" title={title}>
      {children}
    </span>
  )
}

const FACT = "mx-1 inline-block"

/**
 * A domain: its lock, its name, and what its last hour was.
 *
 * The lock already says the domain is served over TLS, so the word and who
 * signed the certificate stand only where there is no hour to read, and are
 * a hover away once there is: with them a busy domain's line ran to a second
 * one under a mark in a row. Plain HTTP is not the expected state and always
 * says so.
 */
function domainNode(domain: MapDomain): WireFlowNode {
  const hour = domain.traffic
  const requests = hour?.requests ?? 0
  const failing = hour ? errorRateTone(hour.errorRate) : "default"
  const served = domain.tls ? "TLS" : "plain HTTP"
  return {
    id: `d:${domain.id}`,
    name: domain.label,
    href: sitePath(domain.id),
    mark: (size) => <DomainMark domain={domain} size={size} />,
    title: (
      <span className="inline-flex max-w-full items-baseline gap-1.5 align-bottom">
        <span className="truncate" title={domain.label}>
          {domain.label}
        </span>
        {domain.aliases > 0 && (
          <span
            className="numeric shrink-0 text-hint font-normal text-muted-foreground"
            title={`and ${domain.aliases} more ${domain.aliases === 1 ? "name" : "names"}`}
          >
            +{domain.aliases}
          </span>
        )}
      </span>
    ),
    hint: (
      <Facts title={hour ? `${served} · ${trafficLabel(hour)}` : undefined}>
        {!domain.tls ? (
          <span className={cn(FACT, "text-warning")}>plain HTTP</span>
        ) : (
          !hour && (
            <span className={cn(FACT, "inline-flex items-center gap-1 align-bottom")}>
              {domain.issuer && <ProductGlyph id={domain.issuer} className="size-3" />}
              TLS
            </span>
          )
        )}
        {hour && (
          <>
            <span className={cn(FACT, "numeric")}>{compactCount(hour.requests)} req/h</span>
            {hour.errorRate > 0 && (
              <span
                className={cn(
                  FACT,
                  "numeric",
                  failing === "danger" && "font-medium text-destructive",
                  failing === "warning" && "text-warning",
                )}
              >
                {share(hour.errorRate)} 5xx
              </span>
            )}
          </>
        )}
      </Facts>
    ),
    line: {
      state: domain.down || failing === "danger" ? "danger" : !domain.tls ? "warning" : "up",
      moving: requests >= CARRIES_PER_HOUR,
      duration: requestPulse(requests),
    },
  }
}

/**
 * What answers behind the engine, as itself, with its state in the tile's
 * corner. Its address is under its name only where the name is not the
 * address already, cut short where it has no room with the whole of it a
 * hover away.
 */
function appNode(app: MapApp): WireFlowNode {
  const tone: DotTone | undefined = app.state ? upstreamTone(app.state) : undefined
  const down = app.state === "refused" || app.state === "missing"
  const slow = app.state === "timeout" || app.state === "unresolvable" || app.state === "error"
  const addressed = app.kind === "app" && app.address !== app.name
  return {
    id: `a:${app.id}`,
    name: app.name,
    href: app.container ? `/docker/containers/${encodeURIComponent(app.container)}` : undefined,
    mark: (size) => <AppMark app={app} size={size} />,
    title: (
      <span
        className={cn("block truncate", app.kind !== "app" && "font-mono text-hint")}
        title={addressed ? `${app.name} · ${app.address}` : app.name}
      >
        {app.name}
      </span>
    ),
    hint: (
      <Facts>
        {addressed && (
          <span
            // Less the margins either side of a fact, so it is cut at the node's own edge.
            className={cn(FACT, "max-w-[calc(100%-0.5rem)] truncate align-bottom font-mono")}
            title={app.address}
          >
            {app.address}
          </span>
        )}
        {app.kind === "files" && <span className={FACT}>static files</span>}
        {app.kind === "redirect" && <span className={FACT}>redirect</span>}
        {app.state && (
          <span
            className={cn(
              FACT,
              tone === "danger" && "font-medium text-destructive",
              tone === "warning" && "text-warning",
            )}
          >
            {upstreamLabel({ state: app.state, ms: app.ms })}
          </span>
        )}
        {app.domains.length > 1 && (
          <span className={cn(FACT, "numeric")}>{app.domains.length} domains</span>
        )}
      </Facts>
    ),
    line: {
      state: down ? "danger" : slow ? "warning" : "up",
      dashed: down,
      moving: !down && app.requests >= CARRIES_PER_HOUR,
      duration: requestPulse(app.requests),
    },
  }
}

/** A domain's lock: green and closed where it is served over TLS, amber and open where not. */
function DomainMark({ domain, size }: { domain: MapDomain; size: MarkSize }) {
  return (
    <WireMark tone={domain.tls ? "success" : "warning"} size={size}>
      {domain.tls ? <LockClosed aria-hidden /> : <LockOpen aria-hidden />}
    </WireMark>
  )
}

/** What answers behind the engine, as itself, with the upstream check's state in the corner. */
function AppMark({ app, size }: { app: MapApp; size: MarkSize }) {
  const Glyph = KIND_GLYPH[app.kind]
  const tone: DotTone | undefined = app.state ? upstreamTone(app.state) : undefined
  return (
    <span className="relative flex">
      <WireMark tone="logo" size={size}>
        <ProjectFavicon
          projectId={app.projectId}
          fallback={
            hasProductLogo(app.product) ? <ProductGlyph id={app.product} /> : <Glyph aria-hidden />
          }
        />
      </WireMark>
      {tone && <StatusDot tone={tone} corner={size} />}
    </span>
  )
}

/** The map with only the domains `only` keeps, and the applications they reach. */
function narrowed(map: RouteMap, only?: (domain: MapDomain) => boolean): RouteMap {
  if (!only) return map
  const domains = map.domains.filter(only)
  const kept = new Set(domains.flatMap((domain) => domain.apps))
  return {
    domains,
    apps: map.apps.filter((app) => kept.has(app.id)),
    hidden: 0,
    total: domains.length,
  }
}
