"use client"

import Link from "next/link"
import { createRef, useMemo, useRef, useState, type RefObject } from "react"
import {
  ArrowRight,
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
import { WireMark, WireNode, WirePlaceholder } from "@/components/deploy/wire"
import { sitePath } from "@/components/proxy/site-verbs"
import { compactCount, errorRateTone, share } from "@/components/proxy/site-traffic"
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
 * On the page's own ground over the dot grid. Three lanes from `lg`; below,
 * the lanes stack, the wires are not drawn and each domain says in words
 * what it reaches.
 */
export function RoutePicture({
  map,
  engine,
  engineMark,
  engineTone,
  engineHint,
}: {
  map: RouteMap
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
  const apps = useMemo(() => new Map(map.apps.map((a) => [a.id, a])), [map.apps])

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

  return (
    <div data-slot="route-picture" className="relative animate-rise py-4">
      <div aria-hidden className="wire-grid pointer-events-none absolute -inset-x-4 inset-y-0" />
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
              still={edge.carries === undefined}
              dashed={edge.dashed}
              tone={edge.tone}
              duration={requestPulse(edge.carries ?? 0)}
              delay={(index % 6) * 0.35}
              className={cn("transition-opacity max-lg:hidden", !wireLit(edge) && "opacity-15")}
            />
          )
        })}

        <div className="relative grid gap-y-8 lg:grid-cols-[minmax(0,1fr)_clamp(2.5rem,6vw,6rem)_minmax(0,0.7fr)_clamp(2.5rem,6vw,6rem)_minmax(0,1fr)] lg:items-center">
          <Lane title="Domains" count={map.total} className="lg:text-right">
            {map.domains.map((domain) => (
              <li
                key={domain.id}
                {...watch(`d:${domain.id}`)}
                className={cn("min-w-0 transition-opacity", !lit(`d:${domain.id}`) && "opacity-40")}
              >
                <DomainNode
                  domain={domain}
                  apps={domain.apps.map((id) => apps.get(id)).filter((a) => a !== undefined)}
                  nodeRef={refs.get(`d:${domain.id}`)}
                />
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
          </Lane>

          <div aria-hidden className="max-lg:hidden" />

          <div className="flex min-w-0 lg:justify-center">
            <WireNode
              nodeRef={hub}
              align="center"
              mark={
                <span className="relative flex">
                  <WireMark tone="logo" size="lg">
                    {engineMark && hasProductLogo(engineMark) ? (
                      <ProductGlyph id={engineMark} />
                    ) : (
                      <Globe aria-hidden />
                    )}
                  </WireMark>
                  <span className="absolute -right-0.5 -bottom-0.5 flex size-4 items-center justify-center rounded-full bg-background">
                    <StatusDot tone={engineTone} className="size-2" />
                  </span>
                </span>
              }
              eyebrow="Reverse proxy"
              title={engine}
              hint={engineHint}
            />
          </div>

          <div aria-hidden className="max-lg:hidden" />

          <Lane title="Applications" count={map.apps.length}>
            {map.apps.map((app) => (
              <li
                key={app.id}
                {...watch(`a:${app.id}`)}
                className={cn("min-w-0 transition-opacity", !lit(`a:${app.id}`) && "opacity-40")}
              >
                <AppNode app={app} nodeRef={refs.get(`a:${app.id}`)} />
              </li>
            ))}
          </Lane>
        </div>
      </div>
    </div>
  )
}

/** A domain: its lock, its name, and what its last hour was. */
function DomainNode({
  domain,
  apps,
  nodeRef,
}: {
  domain: MapDomain
  apps: MapApp[]
  nodeRef?: RefObject<HTMLSpanElement | null>
}) {
  const hour = domain.traffic
  const failing = hour ? errorRateTone(hour.errorRate) : "default"
  return (
    <WireNode
      align="end"
      mark={
        <span ref={nodeRef} className="flex">
          <WireMark tone={domain.tls ? "success" : "warning"} size="sm">
            {domain.tls ? <LockClosed aria-hidden /> : <LockOpen aria-hidden />}
          </WireMark>
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
          {/* Below `lg` there are no wires, so the line says where it goes. */}
          {apps.length > 0 && (
            <span className="inline-flex min-w-0 items-center gap-1 lg:hidden">
              <ArrowRight aria-hidden className="size-3 shrink-0" />
              <span className="truncate">{apps.map((a) => a.name).join(", ")}</span>
            </span>
          )}
        </span>
      }
    />
  )
}

/** What answers behind the engine, as itself, with its state in the tile's corner. */
function AppNode({ app, nodeRef }: { app: MapApp; nodeRef?: RefObject<HTMLSpanElement | null> }) {
  const Glyph = KIND_GLYPH[app.kind]
  const tone: DotTone | undefined = app.state ? upstreamTone(app.state) : undefined
  return (
    <WireNode
      mark={
        <span ref={nodeRef} className="relative flex">
          <WireMark tone="logo" size="md">
            {hasProductLogo(app.product) ? (
              <ProductGlyph id={app.product} />
            ) : (
              <Glyph aria-hidden />
            )}
          </WireMark>
          {tone && (
            <span className="absolute -right-0.5 -bottom-0.5 flex size-3.5 items-center justify-center rounded-full bg-background">
              <StatusDot tone={tone} />
            </span>
          )}
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

function Lane({
  title,
  count,
  className,
  children,
}: {
  title: string
  count: number
  className?: string
  children: React.ReactNode
}) {
  return (
    <div className="min-w-0 self-stretch lg:flex lg:flex-col lg:justify-center">
      <p className={cn("eyebrow mb-4", className)}>
        {title}
        {count > 0 && (
          <span className="numeric ml-1.5 font-medium tracking-normal text-muted-foreground/70">
            {count}
          </span>
        )}
      </p>
      <ol className="flex flex-col gap-4" aria-label={title}>
        {children}
      </ol>
    </div>
  )
}
