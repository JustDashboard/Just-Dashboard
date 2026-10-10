"use client"

import { useRef, type RefObject } from "react"
import { ArrowRight, FolderClosed, Globe, Servers, Users, type Icon } from "@/components/icons"
import { plural } from "@/lib/format"
import { cn } from "@/lib/utils"
import type {
  Certificate,
  Listener,
  PoolMember,
  RequestSummary,
  SiteSpec,
  UpstreamPool,
  VHost,
} from "@/lib/types"
import { ProductGlyph, hasProductLogo } from "@/components/product-logo"
import { StatusDot, type DotTone } from "@/components/status-dot"
import { AnimatedBeam } from "@/components/ui/animated-beam"
import { NumberTicker } from "@/components/ui/number-ticker"
import { WireMark, WireNode } from "@/components/deploy/wire"
import { EndpointText } from "@/components/config/request-path"
import { CertLife, expiryTone } from "@/components/proxy/expiry-status"
import { certificateProduct, certPathProduct } from "@/components/proxy/marks"
import { failuresText, memberCheck, memberRole } from "@/components/proxy/upstream-pools"
import {
  beamDuration,
  probeCount,
  siteFeatures,
  upstreamOwner,
  upstreamUnheard,
  visitorShares,
} from "@/components/proxy/site-overview"

/** The most servers of a pool drawn at the route's end; the Balancing table holds every one. */
const SHOWN_MEMBERS = 4

/**
 * The way a request reaches the site, drawn in the wiring vocabulary the
 * dashboard's own request path and a deployment's map already speak
 * (`deploy/wire`): who asked in the last hour, drawn as the browsers and bots
 * they were; the names they asked for and whose certificate answers them; the
 * engine and what the site's file switches on; and what answers at the far
 * end — each server of a pool with the check's reading of it, or the program
 * holding the socket the site points at, drawn as the product it is.
 *
 * It took the place of the readings over the logs and the two-column route
 * line. The hour's figures are its first node's words, and the wires carry
 * them: a pulse runs quicker down a busy site's, a server that refuses has a
 * red one, a certificate in its last weeks an amber one, and a site that is
 * off — or an engine that is stopped — has still ones.
 */
export function SiteRouteMap({
  vhost,
  spec,
  engine,
  summary,
  recorded,
  cert,
  pools,
  listeners,
  stopped,
}: {
  vhost: VHost
  spec?: SiteSpec
  engine: { product: string; name: string }
  /** The site's last hour of requests, where it keeps a record of them. */
  summary?: RequestSummary
  /** Whether the site has a request record at all. */
  recorded: boolean
  cert?: Certificate
  pools: UpstreamPool[]
  listeners?: Listener[]
  /** The engine serving it is not running. */
  stopped?: boolean
}) {
  const container = useRef<HTMLDivElement>(null)
  const visitorsNode = useRef<HTMLDivElement>(null)
  const domainsNode = useRef<HTMLDivElement>(null)
  const engineNode = useRef<HTMLDivElement>(null)
  const ends = [
    useRef<HTMLDivElement>(null),
    useRef<HTMLDivElement>(null),
    useRef<HTMLDivElement>(null),
    useRef<HTMLDivElement>(null),
  ]

  const off = !vhost.enabled || Boolean(stopped)
  const asked = (summary?.total ?? 0) > 0
  const duration = beamDuration(summary?.perMinute)
  const targets = siteTargets(vhost, spec, pools, listeners)
  const certTone = cert ? expiryTone(cert) : "default"

  const wires: {
    from: RefObject<HTMLDivElement | null>
    to: RefObject<HTMLDivElement | null>
    still: boolean
    tone: "default" | "warning" | "danger"
  }[] = [
    { from: visitorsNode, to: domainsNode, still: off || !asked, tone: "default" },
    {
      from: domainsNode,
      to: engineNode,
      still: off,
      tone: certTone === "danger" ? "danger" : certTone === "warning" ? "warning" : "default",
    },
    ...targets.slice(0, SHOWN_MEMBERS).map((target, index) => ({
      from: engineNode,
      to: ends[index],
      still: off || target.idle,
      tone: target.tone === "danger" ? ("danger" as const) : ("default" as const),
    })),
  ]

  return (
    <section
      aria-label="How a request reaches the site"
      data-slot="site-route-map"
      className="animate-rise"
    >
      <div ref={container} className="relative min-w-0">
        {wires.map((wire, index) => (
          <AnimatedBeam
            key={index}
            containerRef={container}
            fromRef={wire.from}
            toRef={wire.to}
            shape={index > 1 ? "s" : "arc"}
            still={wire.still}
            tone={wire.tone}
            duration={duration}
            delay={index * 0.35}
          />
        ))}

        <ol className="grid min-w-0 gap-y-7 lg:grid-cols-[repeat(3,minmax(0,1fr))_minmax(0,1.3fr)] lg:items-center lg:gap-x-6 lg:pb-16">
          <li className="min-w-0">
            <WireNode
              nodeRef={visitorsNode}
              align="center"
              mark={<VisitorsMark product={visitorShares(summary, 1)[0]?.product} />}
              eyebrow="Visitors"
              title={<VisitorsTitle summary={summary} recorded={recorded} />}
              hint={<VisitorsHint summary={summary} recorded={recorded} />}
            />
          </li>
          <li className="min-w-0">
            <WireNode
              nodeRef={domainsNode}
              align="center"
              mark={
                <WireMark tone="logo">
                  <Globe aria-hidden />
                </WireMark>
              }
              eyebrow={vhost.serverNames.length > 1 ? "Domains" : "Domain"}
              title={<Names names={vhost.serverNames} />}
              hint={<CertificateHint vhost={vhost} cert={cert} />}
            />
          </li>
          <li className="min-w-0">
            <WireNode
              nodeRef={engineNode}
              align="center"
              mark={
                <Mark
                  product={engine.product}
                  fallback={Servers}
                  tone={stopped ? "stopped" : vhost.enabled ? "running" : "unknown"}
                />
              }
              eyebrow="Proxy"
              title={
                <>
                  {engine.name} <Ports listen={vhost.listen} />
                </>
              }
              hint={<Features vhost={vhost} spec={spec} />}
            />
          </li>
          <li className="flex min-w-0 flex-col gap-5">
            {targets.slice(0, SHOWN_MEMBERS).map((target, index) => (
              <WireNode
                key={target.key}
                nodeRef={ends[index]}
                mark={<Mark product={target.product} fallback={target.glyph} tone={target.tone} />}
                eyebrow={index === 0 ? target.eyebrow : undefined}
                title={target.title}
                hint={target.hint}
              />
            ))}
            {targets.length > SHOWN_MEMBERS && (
              <p className="pl-[4.375rem] text-hint text-muted-foreground">
                and {plural(targets.length - SHOWN_MEMBERS, "more server")}
              </p>
            )}
          </li>
        </ol>
      </div>
    </section>
  )
}

/** Who asked most, drawn as the browser or bot it was; nobody asking draws people. */
function VisitorsMark({ product }: { product?: string }) {
  return (
    <WireMark tone="logo">
      {product ? <ProductGlyph id={product} /> : <Users aria-hidden />}
    </WireMark>
  )
}

function VisitorsTitle({ summary, recorded }: { summary?: RequestSummary; recorded: boolean }) {
  if (!recorded) return <span className="text-muted-foreground">Not recorded</span>
  if (!summary) return <span className="text-muted-foreground">—</span>
  // The figure glides to each poll's, as a reading that moves does (§11).
  return (
    <span>
      <NumberTicker value={summary.perMinute} decimalPlaces={summary.perMinute < 10 ? 1 : 0} />{" "}
      <span className="font-normal text-muted-foreground">a minute</span>
    </span>
  )
}

function VisitorsHint({ summary, recorded }: { summary?: RequestSummary; recorded: boolean }) {
  if (!recorded) return "no access log of its own"
  if (!summary) return null
  const probes = probeCount(summary)
  const shares = visitorShares(summary)
  return (
    <>
      <span className="block">{plural(summary.total, "request")} in the last hour</span>
      {shares.length > 0 && (
        <span className="mt-1 flex flex-wrap items-center justify-center gap-x-2.5 gap-y-1 max-lg:justify-start">
          {shares.map(({ product, share }) => (
            <span key={product} className="inline-flex items-center gap-1">
              <ProductGlyph id={product} className="size-3" />
              <span className="numeric">{Math.round(share * 100)}%</span>
            </span>
          ))}
        </span>
      )}
      {probes > 0 && <span className="block text-warning">{plural(probes, "probe")} refused</span>}
    </>
  )
}

/** The names, the first in full and the rest counted: a list of twelve aliases is not a title. */
function Names({ names }: { names: string[] }) {
  if (names.length === 0) return <span className="text-muted-foreground">Any host</span>
  return (
    <span className="block truncate font-mono" title={names.join(", ")}>
      {names[0]}
      {names.length > 1 && <span className="text-muted-foreground"> +{names.length - 1}</span>}
    </span>
  )
}

/** Whose certificate answers the names and how much of its term is left, or that nothing does. */
function CertificateHint({ vhost, cert }: { vhost: VHost; cert?: Certificate }) {
  if (!vhost.tls) return <span className="text-warning">plain HTTP</span>
  const product = cert ? certificateProduct(cert) : certPathProduct(vhost.certPath)
  const tone = cert ? expiryTone(cert) : "default"
  return (
    <span className="flex flex-col items-center gap-1.5 max-lg:items-start">
      <span className="inline-flex items-center gap-1.5">
        {product && <ProductGlyph id={product} className="size-3" />}
        <span
          className={cn(
            tone === "danger" && "text-destructive",
            tone === "warning" && "text-warning",
          )}
        >
          {!cert ? "TLS" : cert.expired ? "expired" : `${plural(cert.daysLeft, "day")} left`}
        </span>
      </span>
      {cert && <CertLife cert={cert} className="w-24" />}
    </span>
  )
}

/** The ports the site listens on, in the hue the dashboard's request path gives a port. */
function Ports({ listen }: { listen: string[] }) {
  const ports = [...new Set(listen.map((l) => l.match(/(\d+)/)?.[1]).filter(Boolean))]
  if (ports.length === 0) return null
  return (
    <span className="numeric font-mono text-[var(--tag-pink)]">
      {ports.map((port) => `:${port}`).join(" ")}
    </span>
  )
}

/** What the site's file switches on, each word in the hue of its kind. */
function Features({ vhost, spec }: { vhost: VHost; spec?: SiteSpec }) {
  const words = siteFeatures(vhost, spec)
  if (words.length === 0) return <span>nothing switched on</span>
  return (
    // Spaced rather than dotted: a run of words that wraps would start its
    // second line on a separator.
    <span className="inline-flex flex-wrap justify-center gap-x-2 max-lg:justify-start">
      {words.map((word) => (
        <span key={word.label} className="whitespace-nowrap" style={{ color: word.hue }}>
          {word.label}
        </span>
      ))}
    </span>
  )
}

/** A product on its tile, and where it has one, its state in the corner as the request path draws it. */
function Mark({
  product,
  fallback: Fallback,
  tone,
}: {
  product?: string
  fallback: Icon
  tone?: DotTone
}) {
  const tile = (
    <WireMark tone="logo">
      {hasProductLogo(product) ? <ProductGlyph id={product} /> : <Fallback aria-hidden />}
    </WireMark>
  )
  if (!tone) return tile
  return (
    <span className="relative flex">
      {tile}
      <span className="absolute -right-0.5 -bottom-0.5 flex size-3.5 items-center justify-center rounded-full bg-background">
        <StatusDot tone={tone} />
      </span>
    </span>
  )
}

/** One thing at the route's far end, as its node draws it. */
type Target = {
  key: string
  product?: string
  glyph: Icon
  tone?: DotTone
  eyebrow: string
  title: React.ReactNode
  hint?: React.ReactNode
  /** Nothing goes down this wire: a backup, a server marked down. */
  idle: boolean
}

/**
 * What answers at the far end of the site: the servers of its pool as the
 * check last read them, the one address it proxies to, the directory it
 * serves, the PHP socket it hands scripts to, or where it redirects. A server
 * is drawn as the program holding its socket where this host has one.
 */
function siteTargets(
  vhost: VHost,
  spec: SiteSpec | undefined,
  pools: UpstreamPool[],
  listeners: Listener[] | undefined,
): Target[] {
  if (spec?.kind === "static") {
    return [
      {
        key: "root",
        glyph: FolderClosed,
        eyebrow: "Directory",
        title: <span className="block truncate font-mono">{spec.root || "—"}</span>,
        hint: spec.spa ? "static files · single-page app" : "static files",
        idle: false,
      },
    ]
  }
  if (spec?.kind === "redirect") {
    const code = spec.redirectCode ?? (spec.permanent ? 301 : 302)
    return [
      {
        key: "redirect",
        glyph: ArrowRight,
        eyebrow: "Redirects to",
        title: <EndpointText url={spec.redirectTo ?? ""} className="block" />,
        hint: `${code} · ${code === 301 || code === 308 ? "permanent" : "temporary"}`,
        idle: false,
      },
    ]
  }
  if (spec?.kind === "php") {
    return [
      {
        key: "php",
        product: "php",
        glyph: Servers,
        eyebrow: "PHP-FPM",
        title: <span className="block truncate font-mono">{spec.phpSocket || "—"}</span>,
        hint: spec.root,
        idle: false,
      },
    ]
  }

  const members = pools.flatMap((pool) => pool.members)
  if (members.length > 0) {
    const named = pools.find((pool) => pool.name)?.name
    return members.map((member, index) => memberTarget(member, index, named, vhost, listeners))
  }

  const upstreams = spec?.upstream ? [spec.upstream] : vhost.upstreams
  if (upstreams.length === 0) {
    return [
      {
        key: "config",
        glyph: Servers,
        eyebrow: "Upstream",
        title: <span className="text-muted-foreground">Served by its configuration</span>,
        idle: true,
      },
    ]
  }
  return upstreams.map((upstream) => {
    const owner = upstreamOwner(upstream, vhost.name, listeners)
    const unheard = !owner && upstreamUnheard(upstream, listeners)
    const port = upstream.match(/:(\d+)/)?.[1]
    return {
      key: upstream,
      product: owner?.product,
      glyph: Servers,
      tone: unheard ? "danger" : owner ? "running" : undefined,
      eyebrow: "Upstream",
      title: <EndpointText url={upstream} className="block" />,
      hint: unheard ? (
        <span className="text-destructive">nothing listens on :{port}</span>
      ) : owner ? (
        <span className="block truncate">
          {owner.name}
          {owner.pid ? <span className="numeric"> · pid {owner.pid}</span> : null}
        </span>
      ) : undefined,
      idle: false,
    }
  })
}

function memberTarget(
  member: PoolMember,
  index: number,
  pool: string | undefined,
  vhost: VHost,
  listeners: Listener[] | undefined,
): Target {
  const owner = upstreamOwner(member.address, vhost.name, listeners)
  const check = memberCheck(member)
  const failures = failuresText(member)
  return {
    key: member.address,
    product: owner?.product,
    glyph: Servers,
    tone: check.tone,
    eyebrow: pool ? `Pool · ${pool}` : "Upstream",
    title: (
      <span className="flex min-w-0 items-baseline gap-2">
        <EndpointText url={member.address} />
        <span
          className={cn(
            "shrink-0 text-hint font-normal",
            check.tone === "danger" ? "text-destructive" : "text-muted-foreground",
          )}
        >
          {check.label}
        </span>
      </span>
    ),
    hint: (
      <span className="block truncate">
        {memberRole(member)}
        {failures && <span className="text-destructive"> · {failures}</span>}
      </span>
    ),
    idle: Boolean(member.down || (member.backup && index > 0)),
  }
}
