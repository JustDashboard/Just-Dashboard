"use client"

import { createRef, useMemo, useRef, useState, type RefObject } from "react"
import { Box, Globe, NetworkDevice, Servers, type Icon } from "@/components/icons"
import { bytes, percent, rate } from "@/lib/format"
import { cn } from "@/lib/utils"
import { ProductGlyph, hasProductLogo } from "@/components/product-logo"
import { StatusDot } from "@/components/status-dot"
import { AnimatedBeam } from "@/components/ui/animated-beam"
import { WireMark, WireNode, WirePlaceholder } from "@/components/deploy/wire"
import { pulseDuration } from "@/components/network/marks"
import { looksLikeDatabase } from "@/components/docker/shared"
import {
  bucketTone,
  networkShortName,
  stackNetworks,
  waysIn,
  type Binding,
  type ServiceReading,
} from "@/components/docker/stack-service-readings"

/** Below this a wire is still: a keep-alive is not traffic. */
const CARRIES = 1024

const BINDING: Record<Binding, { label: string; glyph: Icon }> = {
  all: { label: "Every interface", glyph: Globe },
  loopback: { label: "This server only", glyph: Servers },
  address: { label: "One address", glyph: NetworkDevice },
}

type Edge = {
  from: string
  to: string
  tone: "default" | "warning" | "danger"
  dashed: boolean
  /** Bytes a second, while the service is moving more than a keep-alive. */
  carries?: number
}

function traffic(reading: ServiceReading | undefined) {
  return reading?.rate ? reading.rate.rx + reading.rate.tx : 0
}

/**
 * How the stack is reached and what it is wired to, in three lanes: the
 * ports it publishes on this server, its services, and the Docker networks
 * they are on — the deployment Runtime map's shape (`deploy/runtime-map`),
 * in the wiring vocabulary every picture in the product speaks.
 *
 * A wire pulses while the service at its end moves more than a kilobyte a
 * second, quicker the busier it is, the way the Network section's pictures
 * draw a device's traffic; it is still while the service is idle, red and
 * dashed where a port is published to a service that is not running, and
 * amber where a database answers on every interface. Every mark is the thing
 * itself — the service as its image's product with its state in the tile's
 * corner — and pointing at a node steps every wire that is not its own back.
 *
 * On the page's own ground over the dot grid. Three lanes from `lg`; below,
 * the lanes stack, the wires are not drawn and each node says in words what
 * it is wired to.
 */
export function StackMap({
  stack,
  readings,
  networksRead,
  productOf,
  onOpen,
}: {
  stack: string
  readings: ServiceReading[]
  /** Whether the containers socket has said which networks the containers are on. */
  networksRead: boolean
  productOf: (service: string) => string | undefined
  onOpen: (reading: ServiceReading) => void
}) {
  const container = useRef<HTMLDivElement>(null)
  const [focus, setFocus] = useState<string | null>(null)
  const ways = useMemo(() => waysIn(readings), [readings])
  const networks = useMemo(() => stackNetworks(readings), [readings])
  const byKey = new Map(readings.map((r) => [r.key, r]))

  const edges: Edge[] = []
  for (const way of ways) {
    for (const target of way.targets) {
      const reading = byKey.get(target.service)
      const up = reading?.state === "running"
      const moving = traffic(reading)
      edges.push({
        from: `w:${way.key}`,
        to: `s:${target.service}`,
        tone: !up
          ? "danger"
          : way.binding === "all" && looksLikeDatabase(reading?.service.image)
            ? "warning"
            : "default",
        dashed: !up,
        carries: up && moving > CARRIES ? moving : undefined,
      })
    }
  }
  for (const network of networks) {
    for (const service of network.services) {
      const reading = byKey.get(service)
      const up = reading?.state === "running"
      const moving = traffic(reading)
      edges.push({
        from: `s:${service}`,
        to: `n:${network.name}`,
        tone: "default",
        dashed: !up,
        carries: up && moving > CARRIES ? moving : undefined,
      })
    }
  }

  // One ref per node, remade only when the set of nodes changes, so a frame
  // that changes nothing but a reading does not redraw every wire.
  const ids = [
    ...ways.map((way) => `w:${way.key}`),
    ...readings.map((r) => `s:${r.key}`),
    ...readings.map((r) => `p:${r.key}`),
    ...networks.map((network) => `n:${network.name}`),
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

  return (
    <div data-slot="stack-map" className="relative animate-rise py-4">
      <div aria-hidden className="wire-grid pointer-events-none absolute inset-0" />
      <div ref={container} className="relative">
        {edges.map((edge, index) => {
          const fromService = edge.from.startsWith("s:")
          const from = refFor(fromService ? `p:${edge.from.slice(2)}` : edge.from)
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
              startXOffset={fromService ? 0 : port}
              endXOffset={-port}
              still={edge.carries === undefined}
              dashed={edge.dashed}
              tone={edge.tone}
              duration={edge.carries !== undefined ? pulseDuration(edge.carries) : 3}
              delay={(index % 6) * 0.35}
              className={cn("transition-opacity max-lg:hidden", !lit && "opacity-15")}
            />
          )
        })}

        <div className="relative grid gap-y-8 lg:grid-cols-[minmax(0,1fr)_clamp(2.5rem,5vw,5rem)_minmax(0,1.1fr)_clamp(2.5rem,5vw,5rem)_minmax(0,1fr)] lg:items-center">
          <Lane title="Ways in" count={ways.length} className="lg:text-right">
            {ways.length === 0 ? (
              <li>
                <WireNode
                  align="end"
                  mark={
                    <WirePlaceholder size="md">
                      <Globe />
                    </WirePlaceholder>
                  }
                  title={<span className="text-muted-foreground">Nothing published</span>}
                  hint="Reached only on its own networks"
                />
              </li>
            ) : (
              ways.map((way) => {
                const id = `w:${way.key}`
                const Glyph = BINDING[way.binding].glyph
                return (
                  <li
                    key={way.key}
                    {...watch(id)}
                    className={cn("min-w-0 transition-opacity", !related(id) && "opacity-40")}
                  >
                    <WireNode
                      align="end"
                      mark={
                        <span ref={refFor(id)} className="flex">
                          <WireMark tone="logo" size="md">
                            <Glyph aria-hidden />
                          </WireMark>
                        </span>
                      }
                      eyebrow={way.binding === "address" ? way.ip : BINDING[way.binding].label}
                      title={
                        <span className="font-mono">
                          :{way.hostPort}
                          {way.protocol !== "tcp" && (
                            <span className="text-muted-foreground">/{way.protocol}</span>
                          )}
                        </span>
                      }
                      hint={
                        <span className="font-mono">
                          {way.targets.map((t) => `${t.service}:${t.port}`).join(", ")}
                        </span>
                      }
                    />
                  </li>
                )
              })
            )}
          </Lane>

          <div aria-hidden className="hidden lg:block" />

          <Lane title="Services" count={readings.length}>
            {readings.map((reading) => {
              const id = `s:${reading.key}`
              const product = productOf(reading.key)
              const up = reading.state === "running"
              const { stat } = reading
              const figures =
                up && stat
                  ? [
                      stat.cpuReady !== false ? percent(stat.cpuPercent) : undefined,
                      bytes(stat.memUsage),
                      reading.rate ? `${rate(reading.rate.rx + reading.rate.tx)}` : undefined,
                    ]
                      .filter(Boolean)
                      .join(" · ")
                  : undefined
              const wired = edges.some((edge) => edge.from === id)
              return (
                <li
                  key={reading.key}
                  {...watch(id)}
                  className={cn("min-w-0 transition-opacity", !related(id) && "opacity-40")}
                >
                  <div className="flex min-w-0 items-center gap-3">
                    <WireNode
                      className="min-w-0 flex-1"
                      mark={
                        <span ref={refFor(id)} className="relative flex">
                          {reading.bucket === "missing" ? (
                            <WirePlaceholder size="md" product={product} fallback={Box} />
                          ) : (
                            <>
                              <WireMark tone="logo" size="md">
                                {hasProductLogo(product) ? (
                                  <ProductGlyph id={product} />
                                ) : (
                                  <Box aria-hidden />
                                )}
                              </WireMark>
                              {/* The service's state in the tile's corner, as the
                                  runtime map's containers carry theirs. */}
                              <span className="absolute -right-0.5 -bottom-0.5 flex size-3.5 items-center justify-center rounded-full bg-background">
                                <StatusDot
                                  tone={bucketTone(reading.bucket)}
                                  live={up && reading.container !== undefined}
                                />
                              </span>
                            </>
                          )}
                        </span>
                      }
                      title={
                        reading.containerId ? (
                          <button
                            type="button"
                            onClick={() => onOpen(reading)}
                            className="block max-w-full truncate rounded-sm text-left focus-ring hover:underline"
                          >
                            {reading.key}
                          </button>
                        ) : (
                          <span className="block truncate text-muted-foreground">
                            {reading.key}
                          </span>
                        )
                      }
                      hint={
                        <span className="mt-0.5 flex min-w-0 flex-wrap items-center gap-x-2 gap-y-0.5">
                          <span
                            className={cn(
                              reading.bucket === "failing" && "text-destructive",
                              (reading.bucket === "starting" || reading.bucket === "missing") &&
                                "text-warning",
                            )}
                          >
                            {reading.word}
                          </span>
                          {figures && <span className="numeric">{figures}</span>}
                          {reading.container && reading.container.networks.length > 0 && (
                            <span className="lg:hidden">
                              · on{" "}
                              {reading.container.networks
                                .map((name) => networkShortName(name, stack))
                                .join(", ")}
                            </span>
                          )}
                        </span>
                      }
                    />
                    {/* The port a wire to a network leaves from, at the lane's
                        edge so a column of them reads as one. */}
                    <span
                      ref={refFor(`p:${reading.key}`)}
                      aria-hidden
                      className={cn(
                        "relative z-10 hidden size-1.5 shrink-0 rounded-full lg:block",
                        wired ? "bg-border-strong" : "bg-transparent",
                      )}
                    />
                  </div>
                </li>
              )
            })}
          </Lane>

          <div aria-hidden className="hidden lg:block" />

          <Lane title="Networks" count={networks.length}>
            {!networksRead ? (
              <li>
                <WireNode
                  mark={
                    <WirePlaceholder size="md">
                      <NetworkDevice />
                    </WirePlaceholder>
                  }
                  title={<span className="text-muted-foreground">Reading networks…</span>}
                />
              </li>
            ) : networks.length === 0 ? (
              <li>
                <WireNode
                  mark={
                    <WirePlaceholder size="md">
                      <NetworkDevice />
                    </WirePlaceholder>
                  }
                  title={<span className="text-muted-foreground">On no network</span>}
                  hint="No container of this stack exists to attach to one"
                />
              </li>
            ) : (
              networks.map((network) => {
                const id = `n:${network.name}`
                const short = networkShortName(network.name, stack)
                return (
                  <li
                    key={network.name}
                    {...watch(id)}
                    className={cn("min-w-0 transition-opacity", !related(id) && "opacity-40")}
                  >
                    <WireNode
                      mark={
                        <span ref={refFor(id)} className="flex">
                          <WireMark tone="logo" size="md">
                            {hasProductLogo("docker") ? (
                              <ProductGlyph id="docker" />
                            ) : (
                              <NetworkDevice aria-hidden />
                            )}
                          </WireMark>
                        </span>
                      }
                      eyebrow={short === network.name ? "Shared network" : "Network"}
                      title={<span className="block truncate">{short}</span>}
                      hint={
                        <span className="flex min-w-0 flex-wrap gap-x-2">
                          {short !== network.name && (
                            <span className="truncate font-mono">{network.name}</span>
                          )}
                          <span className="lg:hidden">{network.services.join(", ")}</span>
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
