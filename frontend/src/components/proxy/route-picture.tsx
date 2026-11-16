"use client"

import Link from "next/link"
import { createRef, useMemo, useRef, useState, type RefObject } from "react"
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
import { AnimatedBeam } from "@/components/ui/animated-beam"
import { WireMark, WireNode, WirePlaceholder } from "@/components/wire/wire"
import { WireLane } from "@/components/wire/wire-lane"
import { MoreTile, WireMarks } from "@/components/wire/wire-marks"
import { useMotionHeld } from "@/components/wire/use-motion-held"
import { useBreakpoint } from "@/lib/breakpoints"
import { compactCount } from "@/lib/format"
import { ProjectFavicon } from "@/components/project-mark"
import { sitePath } from "@/components/proxy/site-verbs"
import { errorRateTone, share } from "@/components/proxy/site-traffic"
import { upstreamLabel, upstreamTone } from "@/components/proxy/upstream-health"
import {
  CARRIES_PER_HOUR,
  requestPulse,
  type MapApp,
  type MapDomain,
  type RouteMap,
} from "@/components/proxy/route-map"

type Edge = {
  from: string
  to: string
  tone: "default" | "warning" | "danger"
  dashed: boolean
  /** Requests an hour, while the route carries more than a health check would. */
  carries?: number
}

const KIND_GLYPH = { files: FolderOpen, redirect: CornerUpRight, config: FileText, app: Box }

/**
 * The proxy as the thing it is: the domains it answers for on the left, the
 * engine in the middle, and on the right what each domain is sent to, drawn
 * as itself — Grafana as Grafana, a Node program as Node — with its state in
 * the tile's corner. The Network topology's shape (`network/topology.tsx`),
 * in the wiring vocabulary every picture in the product speaks.
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
 * On the page's own ground, with no frame or dot grid. Three lanes from `lg`.
 * On a phone it is the one phone form (`WireMarks`): the domains' locks,
 * the engine named, the applications' marks and the wires between them; the
 * Routes list under it names each one.
 *
 * `only` draws the routes of some domains alone, and what they reach: Sites
 * opening on the picture of its own routes.
 */
export function RoutePicture({
  map: whole,
  only,
  engine,
  engineMark,
  engineTone,
  engineHint,
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
}) {
  const container = useRef<HTMLDivElement>(null)
  const hub = useRef<HTMLDivElement>(null)
  const [focus, setFocus] = useState<string | null>(null)
  const held = useMotionHeld()
  const wide = useBreakpoint("lg")
  const map = useMemo(() => narrowed(whole, only), [whole, only])

  const edges: Edge[] = []
  for (const domain of map.domains) {
    const requests = domain.traffic?.requests ?? 0
    const failing = domain.traffic ? errorRateTone(domain.traffic.errorRate) === "danger" : false
    edges.push({
      from: `d:${domain.id}`,
      to: "hub",
      tone: domain.down || failing ? "danger" : !domain.tls ? "warning" : "default",
      dashed: false,
      carries: requests >= CARRIES_PER_HOUR ? requests : undefined,
    })
  }
  for (const app of map.apps) {
    const down = app.state === "refused" || app.state === "missing"
    const slow = app.state === "timeout" || app.state === "unresolvable" || app.state === "error"
    edges.push({
      from: "hub",
      to: `a:${app.id}`,
      tone: down ? "danger" : slow ? "warning" : "default",
      dashed: down,
      carries: !down && app.requests >= CARRIES_PER_HOUR ? app.requests : undefined,
    })
  }

  const ids = [...map.domains.map((d) => `d:${d.id}`), ...map.apps.map((a) => `a:${a.id}`)].join(
    "\n",
  )
  const refs = useMemo(() => {
    const out = new Map<string, RefObject<HTMLSpanElement | null>>()
    for (const id of ids.split("\n")) if (id) out.set(id, createRef<HTMLSpanElement>())
    return out
  }, [ids])

  // What a node is joined to through the engine: a domain's applications,
  // an application's domains.
  const joined = (a: string, b: string) => {
    const [domain, app] = a.startsWith("d:") ? [a, b] : [b, a]
    if (!domain.startsWith("d:") || !app.startsWith("a:")) return false
    return map.domains.find((d) => `d:${d.id}` === domain)?.apps.includes(app.slice(2)) ?? false
  }
  const lit = (id: string) => focus === null || focus === id || joined(focus, id)
  const wireLit = (edge: Edge) => {
    if (focus === null) return true
    const end = edge.from === "hub" ? edge.to : edge.from
    return end === focus || joined(focus, end)
  }
  const watch = (id: string) => ({
    onPointerEnter: () => setFocus(id),
    onPointerLeave: () => setFocus((held) => (held === id ? null : held)),
    onFocus: () => setFocus(id),
    onBlur: () => setFocus((held) => (held === id ? null : held)),
  })
  // A wire meets a mark at its edge: half of `sm` or `md`, and a breath.
  const domainPort = 20
  const appPort = 26

  const hubMark = (
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
  )

  if (!wide)
    return (
      <WireMarks
        data-slot="route-picture"
        className="animate-rise"
        tiers={[
          {
            title: "Domains",
            nodes: [
              ...map.domains.map((domain) => ({
                id: `d:${domain.id}`,
                mark: <DomainMark domain={domain} size="sm" />,
                name: domain.label,
                href: sitePath(domain.id),
              })),
              ...(map.hidden > 0
                ? [
                    {
                      id: "d:more",
                      mark: <MoreTile count={map.hidden} />,
                      name: `${map.hidden} more on Sites`,
                      href: "/proxy/sites",
                      round: true,
                      anonymous: true,
                    },
                  ]
                : []),
            ],
          },
          { id: "hub", mark: hubMark, eyebrow: "Reverse proxy", title: engine, hint: engineHint },
          {
            title: "Applications",
            nodes: map.apps.map((app) => ({
              id: `a:${app.id}`,
              mark: <AppMark app={app} size="sm" />,
              name: app.name,
              href: app.container
                ? `/docker/containers/${encodeURIComponent(app.container)}`
                : undefined,
            })),
          },
        ]}
        wires={edges.map((edge) => ({
          from: edge.from,
          to: edge.to,
          still: edge.carries === undefined || held,
          dashed: edge.dashed,
          tone: edge.tone,
          duration: requestPulse(edge.carries ?? 0),
        }))}
      />
    )

  return (
    // Wide, the lanes' headings and the engine's words hang outside their
    // boxes so every mark sits on one line; each column is padded the same
    // above and below to hold them, and the bottom padding holds what the
    // engine's words run past the row.
    <div data-slot="route-picture" className="relative animate-rise pt-4 pb-6">
      <div ref={container} className="relative">
        {edges.map((edge, index) => {
          const into = edge.to === "hub"
          const from = into ? refs.get(edge.from) : hub
          const to = into ? hub : refs.get(edge.to)
          if (!from || !to) return null
          return (
            <AnimatedBeam
              key={`${edge.from}→${edge.to}`}
              containerRef={container}
              fromRef={from}
              toRef={to}
              shape="s"
              startXOffset={into ? domainPort : 30}
              endXOffset={into ? -30 : -appPort}
              still={edge.carries === undefined || held}
              dashed={edge.dashed}
              tone={edge.tone}
              duration={requestPulse(edge.carries ?? 0)}
              delay={(index % 6) * 0.35}
              className={cn("transition-opacity", !wireLit(edge) && "opacity-15")}
            />
          )
        })}

        <div className="relative grid gap-y-8 lg:grid-cols-[minmax(0,1fr)_clamp(2.5rem,6vw,6rem)_minmax(0,0.7fr)_clamp(2.5rem,6vw,6rem)_minmax(0,1fr)] lg:items-center">
          <WireLane title="Domains" count={map.total} align="end" float>
            {map.domains.map((domain) => (
              <li
                key={domain.id}
                {...watch(`d:${domain.id}`)}
                className={cn("min-w-0 transition-opacity", !lit(`d:${domain.id}`) && "opacity-40")}
              >
                <DomainNode domain={domain} nodeRef={refs.get(`d:${domain.id}`)} />
              </li>
            ))}
            {map.hidden > 0 && (
              <li className="min-w-0">
                <WireNode
                  align="end"
                  mark={
                    <Link
                      href="/proxy/sites"
                      aria-label={`${map.hidden} more on Sites`}
                      className="rounded-full focus-ring"
                    >
                      <WirePlaceholder size="sm">
                        <Globe />
                      </WirePlaceholder>
                    </Link>
                  }
                  title={
                    <Link href="/proxy/sites" className="text-muted-foreground hover:underline">
                      {map.hidden} more on Sites
                    </Link>
                  }
                />
              </li>
            )}
          </WireLane>

          <div aria-hidden className="max-lg:hidden" />

          <div className="flex min-w-0 lg:justify-center lg:py-12">
            <WireNode
              nodeRef={hub}
              align="center"
              mark={hubMark}
              eyebrow="Reverse proxy"
              title={engine}
              hint={engineHint}
            />
          </div>

          <div aria-hidden className="max-lg:hidden" />

          <WireLane title="Applications" count={map.apps.length} float>
            {map.apps.map((app) => (
              <li
                key={app.id}
                {...watch(`a:${app.id}`)}
                className={cn("min-w-0 transition-opacity", !lit(`a:${app.id}`) && "opacity-40")}
              >
                <AppNode app={app} nodeRef={refs.get(`a:${app.id}`)} />
              </li>
            ))}
          </WireLane>
        </div>
      </div>
    </div>
  )
}

/** A domain: its lock, its name, and what its last hour was. */
function DomainNode({
  domain,
  nodeRef,
}: {
  domain: MapDomain
  nodeRef?: RefObject<HTMLSpanElement | null>
}) {
  const hour = domain.traffic
  const failing = hour ? errorRateTone(hour.errorRate) : "default"
  return (
    <WireNode
      align="end"
      mark={
        <span ref={nodeRef} className="flex">
          <DomainMark domain={domain} size="md" />
        </span>
      }
      title={
        <Link
          href={sitePath(domain.id)}
          className="block max-w-full truncate rounded-sm focus-ring hover:underline"
        >
          {domain.label}
          {domain.aliases > 0 && (
            <span className="numeric ml-1.5 text-hint font-normal text-muted-foreground">
              +{domain.aliases}
            </span>
          )}
        </Link>
      }
      hint={
        <span className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-0.5 lg:justify-end">
          {domain.tls ? (
            domain.issuer ? (
              <span className="inline-flex items-center gap-1">
                <ProductGlyph id={domain.issuer} className="size-3" />
                TLS
              </span>
            ) : (
              <span>TLS</span>
            )
          ) : (
            <span className="text-warning">plain HTTP</span>
          )}
          {hour && (
            <>
              <span className="numeric">{compactCount(hour.requests)} req/h</span>
              {hour.errorRate > 0 && (
                <span
                  className={cn(
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
        </span>
      }
    />
  )
}

/** What answers behind the engine, as itself, with its state in the tile's corner. */
function AppNode({ app, nodeRef }: { app: MapApp; nodeRef?: RefObject<HTMLSpanElement | null> }) {
  const tone: DotTone | undefined = app.state ? upstreamTone(app.state) : undefined
  return (
    <WireNode
      mark={
        <span ref={nodeRef} className="flex">
          <AppMark app={app} size="md" />
        </span>
      }
      title={
        app.container ? (
          <Link
            href={`/docker/containers/${encodeURIComponent(app.container)}`}
            className="block max-w-full truncate rounded-sm focus-ring hover:underline"
          >
            {app.name}
          </Link>
        ) : (
          <span className={cn("block truncate", app.kind !== "app" && "font-mono text-hint")}>
            {app.name}
          </span>
        )
      }
      hint={
        <span className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-0.5">
          {app.kind === "app" && app.address !== app.name && (
            <span className="truncate font-mono">{app.address}</span>
          )}
          {app.kind === "files" && <span>static files</span>}
          {app.kind === "redirect" && <span>redirect</span>}
          {app.state && (
            <span
              className={cn(
                tone === "danger" && "font-medium text-destructive",
                tone === "warning" && "text-warning",
              )}
            >
              {upstreamLabel({ state: app.state, ms: app.ms })}
            </span>
          )}
          {app.domains.length > 1 && <span className="numeric">{app.domains.length} domains</span>}
        </span>
      }
    />
  )
}

/** A domain's lock: green and closed where it is served over TLS, amber and open where not. */
function DomainMark({ domain, size }: { domain: MapDomain; size: "md" | "sm" }) {
  return (
    <WireMark tone={domain.tls ? "success" : "warning"} size={size}>
      {domain.tls ? <LockClosed aria-hidden /> : <LockOpen aria-hidden />}
    </WireMark>
  )
}

/** What answers behind the engine, as itself, with the upstream check's state in the corner. */
function AppMark({ app, size }: { app: MapApp; size: "md" | "sm" }) {
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
      {tone && <StatusDot tone={tone} corner={size === "md" ? "md" : "sm"} />}
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
