"use client"

import { createRef, useMemo, useRef, useState, type RefObject } from "react"
import {
  Box,
  Database,
  FolderClosed,
  Globe,
  LockClosed,
  LockOpen,
  Servers,
  type Icon,
} from "@/components/icons"
import { bytes, percent } from "@/lib/format"
import type { ContainerStats, DeploymentDomainRoute, DeploymentRuntimeService } from "@/lib/types"
import { cn } from "@/lib/utils"
import { ProductGlyph, hasProductLogo, issuerProduct } from "@/components/product-logo"
import { Status, StatusDot, type DotTone } from "@/components/status-dot"
import { AnimatedBeam } from "@/components/ui/animated-beam"
import { stateWord } from "@/components/docker/container-cells"
import { WireMark, WireNode, WirePlaceholder } from "@/components/deploy/wire"
import { CertificateReading, ROUTE_LABEL, ROUTE_TONE } from "@/components/deploy/vocabulary"

/** A live container of the release, as the map draws it. */
export type MapService = {
  service: DeploymentRuntimeService
  product: string
  stat?: ContainerStats
  /** The release's number, when the list has brought it. */
  release?: number
  /** A domain's requests reach it (`publicServices`). */
  reached: boolean
}

/** Something the release keeps its state in: a volume, a folder, a database. */
export type MapStore = {
  key: string
  kind: "volume" | "bind" | "database"
  eyebrow: string
  title: string
  detail?: string
  product?: string
  /** The container that keeps it, where Docker says; a reached service otherwise. */
  ownerId?: string
  status: { label: string; tone: DotTone }
}

/** A store's glyph where no product names it. A table, not a choice made in render. */
const STORE_GLYPH: Record<MapStore["kind"], Icon> = {
  volume: Servers,
  bind: FolderClosed,
  database: Database,
}

type Edge = {
  from: string
  to: string
  tone: "default" | "warning" | "danger"
  dashed: boolean
  carries: boolean
}

/**
 * How the live release runs, drawn as three lanes: the names it answers to,
 * the containers that answer, and where they keep what must survive them —
 * with a wire between each pair in the wiring vocabulary the rest of the
 * product speaks (`deploy/wire`, the database fleet's map).
 *
 * The overview's picture is how a release is *made* — source, release,
 * runtime, domains. This is how it *runs*: which container a hostname's
 * requests land on, and which volume or database each container holds open.
 * The lists under it say the same things one row at a time, with their verbs;
 * this is the shape of them at once.
 *
 * A wire pulses while it carries — the route is served and the container is
 * up — is still where it exists and carries nothing, dashed where a hop is
 * missing, amber where it works but should not be relied on, red where it is
 * broken. Every mark is the thing itself: a certificate's issuer, the product
 * an image is, a database's engine. Pointing at a node steps every wire that
 * is not its own back.
 *
 * On the page's own ground over the dot grid, like the settings pictures: the
 * grid gives it a middle where a frame would give it an outline. Three lanes
 * from `lg`; below it the lanes stack, the wires are not drawn, and each node
 * says in words what it is wired to.
 */
export function RuntimeMap({
  services,
  domains,
  domainsReason,
  stores,
  storesReason,
}: {
  services: MapService[]
  /** The routes the proxy owner reports; undefined while they are being read. */
  domains?: DeploymentDomainRoute[]
  /** Why the routes could not be read. */
  domainsReason?: string
  stores?: MapStore[]
  storesReason?: string
}) {
  const container = useRef<HTMLDivElement>(null)
  const [focus, setFocus] = useState<string | null>(null)
  const reached = services.filter((one) => one.reached)
  const fallbackOwner = reached[0]?.service.containerId
  const byId = new Map(services.map((one) => [one.service.containerId, one]))

  const edges: Edge[] = []
  for (const domain of domains ?? []) {
    for (const target of reached) {
      const up = target.service.state === "running"
      edges.push({
        from: `d:${domain.hostname}`,
        to: `s:${target.service.containerId}`,
        tone: !up || ROUTE_TONE[domain.route] === "danger" ? "danger" : "default",
        dashed: domain.route === "missing" || domain.route === "unavailable",
        carries: up && domain.route === "served",
      })
    }
  }
  for (const store of stores ?? []) {
    const owner = store.ownerId && byId.has(store.ownerId) ? store.ownerId : fallbackOwner
    if (!owner) continue
    const up = byId.get(owner)?.service.state === "running"
    const tone = store.status.tone
    edges.push({
      from: `s:${owner}`,
      to: `t:${store.key}`,
      tone: tone === "danger" ? "danger" : tone === "warning" ? "warning" : "default",
      dashed: tone === "danger" || tone === "unknown",
      carries: up && tone === "running",
    })
  }

  // One ref per node, remade only when the set of nodes changes, so a poll
  // that changes nothing does not redraw every wire.
  const ids = [
    ...(domains ?? []).map((domain) => `d:${domain.hostname}`),
    ...services.map((one) => `s:${one.service.containerId}`),
    ...services.map((one) => `p:${one.service.containerId}`),
    ...(stores ?? []).map((store) => `t:${store.key}`),
  ].join("\n")
  const refs = useMemo(() => {
    const map = new Map<string, RefObject<HTMLSpanElement | null>>()
    for (const id of ids.split("\n")) if (id) map.set(id, createRef<HTMLSpanElement>())
    return map
  }, [ids])
  const refFor = (id: string) => refs.get(id)

  const related = (id: string) =>
    focus === null ||
    focus === id ||
    edges.some(
      (edge) => (edge.from === focus && edge.to === id) || (edge.to === focus && edge.from === id),
    )
  const watch = (id: string) => ({
    onPointerEnter: () => setFocus(id),
    onPointerLeave: () => setFocus((held) => (held === id ? null : held)),
    onFocus: () => setFocus(id),
    onBlur: () => setFocus((held) => (held === id ? null : held)),
  })
  // A wire meets a mark at its edge, not under its logo: half of `md`, and a breath.
  const port = 26
  const nameOf = (id: string) => {
    const one = byId.get(id)
    return one ? one.service.service || one.service.name : undefined
  }
  const reachedNames = reached.map((one) => one.service.service || one.service.name).join(", ")

  return (
    <div className="relative animate-rise py-4">
      <div aria-hidden className="wire-grid pointer-events-none absolute -inset-x-4 inset-y-0" />
      <div ref={container} className="relative">
        {edges.map((edge, index) => {
          const service = edge.from.startsWith("s:")
          const from = refFor(service ? `p:${edge.from.slice(2)}` : edge.from)
          const to = refFor(edge.to)
          if (!from || !to) return null
          const lit = focus === null || edge.from === focus || edge.to === focus
          return (
            <AnimatedBeam
              key={`${edge.from}→${edge.to}`}
              containerRef={container}
              fromRef={from}
              toRef={to}
              shape="s"
              startXOffset={service ? 0 : port}
              endXOffset={-port}
              still={!edge.carries}
              dashed={edge.dashed}
              tone={edge.tone}
              duration={service ? 3.2 : 2.4}
              delay={(index % 6) * 0.35}
              className={cn("transition-opacity max-lg:hidden", !lit && "opacity-15")}
            />
          )
        })}

        <div className="relative grid gap-y-8 lg:grid-cols-[minmax(0,1fr)_clamp(2.5rem,5vw,5rem)_minmax(0,1.1fr)_clamp(2.5rem,5vw,5rem)_minmax(0,1fr)] lg:items-center">
          <Lane title="Domains" count={domains?.length} className="lg:text-right">
            {domains === undefined ? (
              <Waiting align="end" reason={domainsReason} glyph={Globe} what="domains" />
            ) : domains.length === 0 ? (
              <li>
                <WireNode
                  align="end"
                  mark={
                    <WirePlaceholder size="md">
                      <Globe />
                    </WirePlaceholder>
                  }
                  title={<span className="text-muted-foreground">No public domain</span>}
                  hint="Reached only from this server"
                />
              </li>
            ) : (
              domains.map((domain) => {
                const id = `d:${domain.hostname}`
                const issuer = issuerProduct(domain.certificateIssuer)
                return (
                  <li
                    key={domain.hostname}
                    {...watch(id)}
                    className={cn("min-w-0 transition-opacity", !related(id) && "opacity-40")}
                  >
                    <WireNode
                      align="end"
                      mark={
                        <span ref={refFor(id)} className="flex">
                          <WireMark tone="logo" size="md">
                            {hasProductLogo(issuer) ? (
                              <ProductGlyph id={issuer} />
                            ) : domain.https ? (
                              <LockClosed aria-hidden />
                            ) : (
                              <LockOpen aria-hidden />
                            )}
                          </WireMark>
                        </span>
                      }
                      eyebrow={domain.https ? "HTTPS" : "HTTP only"}
                      title={
                        <a
                          href={`${domain.https ? "https" : "http"}://${domain.hostname}/`}
                          target="_blank"
                          rel="noopener noreferrer"
                          className="block truncate rounded-sm font-mono focus-ring hover:underline"
                        >
                          {domain.hostname}
                        </a>
                      }
                      hint={
                        <span className="mt-0.5 flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 lg:justify-end">
                          {domain.route !== "served" && (
                            <Status
                              tone={ROUTE_TONE[domain.route]}
                              label={ROUTE_LABEL[domain.route]}
                            />
                          )}
                          {domain.https && <CertificateReading domain={domain} issuer={false} />}
                          {reachedNames && <span className="lg:hidden">to {reachedNames}</span>}
                        </span>
                      }
                    />
                  </li>
                )
              })
            )}
          </Lane>

          <div aria-hidden className="hidden lg:block" />

          <Lane title="Services" count={services.length}>
            {services.map((one) => {
              const { service, stat } = one
              const id = `s:${service.containerId}`
              const up = service.state === "running"
              const readings =
                up && stat
                  ? [
                      stat.cpuReady ? `${percent(stat.cpuPercent)} CPU` : undefined,
                      bytes(stat.memUsage),
                    ]
                      .filter(Boolean)
                      .join(" · ")
                  : undefined
              return (
                <li
                  key={service.containerId}
                  {...watch(id)}
                  className={cn("min-w-0 transition-opacity", !related(id) && "opacity-40")}
                >
                  <div className="flex min-w-0 items-center gap-3">
                    <WireNode
                      className="min-w-0 flex-1"
                      mark={
                        <span ref={refFor(id)} className="relative flex">
                          <WireMark tone="logo" size="md">
                            {hasProductLogo(one.product) ? (
                              <ProductGlyph id={one.product} />
                            ) : (
                              <Box aria-hidden />
                            )}
                          </WireMark>
                          {/* The container's state in the tile's corner, as the overview's runtime mark carries it. */}
                          <span className="absolute -right-0.5 -bottom-0.5 flex size-3.5 items-center justify-center rounded-full bg-background">
                            <StatusDot state={service.state} />
                          </span>
                        </span>
                      }
                      eyebrow={
                        service.liveRelease
                          ? `Live${one.release !== undefined ? ` · #${one.release}` : ""}`
                          : `Release${one.release !== undefined ? ` #${one.release}` : ""}`
                      }
                      title={
                        <span className="block truncate">
                          {service.name || service.containerId.slice(0, 12)}
                        </span>
                      }
                      hint={
                        <span className="mt-0.5 flex min-w-0 flex-wrap items-center gap-x-2 gap-y-0.5">
                          <span className={cn(!up && "text-warning")}>
                            {stateWord(service.state)}
                          </span>
                          {readings && <span className="numeric">{readings}</span>}
                        </span>
                      }
                    />
                    {/* The port a wire to its data leaves from, at the lane's edge
                        so a column of them reads as one. */}
                    <span
                      ref={refFor(`p:${service.containerId}`)}
                      aria-hidden
                      className={cn(
                        "relative z-10 hidden size-1.5 shrink-0 rounded-full lg:block",
                        edges.some((edge) => edge.from === id)
                          ? "bg-border-strong"
                          : "bg-transparent",
                      )}
                    />
                  </div>
                </li>
              )
            })}
          </Lane>

          <div aria-hidden className="hidden lg:block" />

          <Lane title="Data" count={stores?.length}>
            {stores === undefined ? (
              <Waiting align="start" reason={storesReason} glyph={Database} what="storage" />
            ) : stores.length === 0 ? (
              <li>
                <WireNode
                  mark={
                    <WirePlaceholder size="md">
                      <Database />
                    </WirePlaceholder>
                  }
                  title={<span className="text-muted-foreground">Keeps nothing</span>}
                  hint="No volume, folder or database is declared"
                />
              </li>
            ) : (
              stores.map((store) => {
                const id = `t:${store.key}`
                const owner = nameOf(store.ownerId ?? "") ?? reachedNames
                const Glyph = STORE_GLYPH[store.kind]
                return (
                  <li
                    key={store.key}
                    {...watch(id)}
                    className={cn("min-w-0 transition-opacity", !related(id) && "opacity-40")}
                  >
                    <WireNode
                      mark={
                        <span ref={refFor(id)} className="flex">
                          <WireMark tone="logo" size="md">
                            {hasProductLogo(store.product) ? (
                              <ProductGlyph id={store.product} />
                            ) : (
                              <Glyph aria-hidden />
                            )}
                          </WireMark>
                        </span>
                      }
                      eyebrow={store.eyebrow}
                      title={
                        <span
                          className={cn("block truncate", store.kind === "bind" && "font-mono")}
                        >
                          {store.title}
                        </span>
                      }
                      hint={
                        <span className="mt-0.5 flex min-w-0 flex-wrap items-center gap-x-2 gap-y-0.5">
                          {store.status.tone !== "running" && (
                            <Status tone={store.status.tone} label={store.status.label} />
                          )}
                          {store.detail && <span className="min-w-0 truncate">{store.detail}</span>}
                          {owner && <span className="lg:hidden">· kept by {owner}</span>}
                        </span>
                      }
                    />
                  </li>
                )
              })
            )}
          </Lane>
        </div>
      </div>
    </div>
  )
}

function Lane({
  title,
  count,
  className,
  children,
}: {
  title: string
  count?: number
  className?: string
  children: React.ReactNode
}) {
  return (
    <div className="min-w-0 self-stretch lg:flex lg:flex-col lg:justify-center">
      <p className={cn("eyebrow mb-4", className)}>
        {title}
        {count !== undefined && count > 0 && (
          <span className="numeric ml-1.5 font-medium tracking-normal text-muted-foreground/70">
            {count}
          </span>
        )}
      </p>
      <ol className="flex flex-col gap-5" aria-label={title}>
        {children}
      </ol>
    </div>
  )
}

/** A lane whose owner has not answered yet, or could not be read. */
function Waiting({
  align,
  reason,
  glyph,
  what,
}: {
  align: "start" | "end"
  reason?: string
  glyph: Icon
  what: string
}) {
  const Glyph = glyph
  return (
    <li>
      <WireNode
        align={align}
        mark={
          <WirePlaceholder size="md">
            <Glyph />
          </WirePlaceholder>
        }
        title={
          <span className="text-muted-foreground">{reason ? "Not read" : `Reading ${what}…`}</span>
        }
        hint={reason}
      />
    </li>
  )
}
