"use client"

import { createRef, useMemo, useRef, useState, type RefObject } from "react"
import Link from "next/link"
import { Box, CloudUpload, Globe } from "@/components/icons"
import type { DbTopoEdge, DbTopoNode, DbTopology } from "@/lib/types"
import { cn } from "@/lib/utils"
import { WireHost, WireMark, WireNode } from "@/components/deploy/wire"
import { ProductGlyph, hasProductLogo } from "@/components/product-logo"
import { StatusDot, type DotTone, type Verdict } from "@/components/status-dot"
import { AnimatedBeam } from "@/components/ui/animated-beam"
import type { Engine } from "@/components/database/engine"
import {
  describeEdge,
  edgeRank,
  splitTopology,
  wiredOrder,
  worstStatus,
} from "@/components/database/fleet/fleet"
import { EngineGlyph } from "@/components/database/kit"

/**
 * What feeds what: every database in one lane, everything that reads it in
 * the other, and a wire between each pair with light travelling along it from
 * the database to its reader.
 *
 * It is drawn the way Configuration draws the path a request takes
 * (`config/request-path.tsx`), which the operator asked the Databases pictures
 * to match: every mark at the full size of the wiring vocabulary
 * (`deploy/wire`), a wide lane between the two sides so a wire leaves its mark
 * level and arrives level, and the two lanes centred on each other, so one
 * reader fed by three databases sits across from the middle of them rather
 * than level with the first. A database is its engine's logo, this server the
 * rack every picture draws it as (`WireHost`), a container or a deployment the
 * product it runs where one is known; whether each is up is the dot in its
 * corner, as it is on the services Configuration draws. It stands on the
 * page's own ground with no dot grid behind it, at the operator's request.
 *
 * The lanes are ordered for the wires rather than the alphabet: databases that
 * feed something first, and each reader across from the databases it is fed
 * by, so wires cross only where the links themselves do.
 *
 * A wire that pulses is carrying sessions now; a still one is declared and
 * idle; a dashed one could carry (same stack, same network) and has not been
 * seen to. A broken link is drawn in the danger colour and a stale one in the
 * warning colour, so the one that matters is found without reading a name.
 * Pointing at a thing steps the wires that are not its own back, which is
 * what makes a picture of twenty wires readable one database at a time.
 *
 * The marks are laid out by CSS and the wires follow them, so the picture is
 * two lanes from `lg` and one column below it, where lines would cross the
 * words; there each reader says in words which databases reach it.
 */
export function Wiring({
  topology,
  keep,
  engineOf,
  stateOf,
  readers = true,
  className,
}: {
  topology: DbTopology
  /** Narrow the picture to some of the databases. */
  keep?: (node: DbTopoNode) => boolean
  /** A database node's engine, by what answered rather than by its driver. */
  engineOf: (node: DbTopoNode) => Engine
  /** Whether a database answers, where the page has read it: the dot on its mark. */
  stateOf?: (node: DbTopoNode) => DotTone | undefined
  /**
   * Draw the lane of readers. False where something beside the picture lists
   * them and the wires that would join the two lanes are not drawn.
   */
  readers?: boolean
  className?: string
}) {
  const { databases, consumers, edges, feeds, fedBy } = useMemo(
    () => wiredOrder(splitTopology(topology, keep)),
    [topology, keep],
  )
  const container = useRef<HTMLDivElement>(null)
  const [focus, setFocus] = useState<string | null>(null)
  // One ref per node, keyed by id and remade only when the set of nodes
  // changes, so a poll that changes nothing does not redraw every line.
  const ids = [...databases, ...consumers].map((node) => node.id).join("\n")
  const refs = useMemo(() => {
    const map = new Map<string, RefObject<HTMLDivElement | null>>()
    for (const id of ids.split("\n")) if (id) map.set(id, createRef<HTMLDivElement>())
    return map
  }, [ids])
  const refFor = (id: string) => refs.get(id)

  // A wire starts at the mark's edge, not under its logo.
  const port = 28 + 6
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

  return (
    <div data-slot="wiring" className={cn("relative min-w-0 animate-rise py-6", className)}>
      <div ref={container} className="relative min-w-0">
        {(readers ? edges : []).map((edge, index) => {
          const from = refFor(edge.from)
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
              startXOffset={port}
              endXOffset={-port}
              still={edge.sessions === 0 && edge.status !== "connected"}
              dashed={edge.via.every((via) => via === "stack" || via === "network")}
              tone={beamTone(edge)}
              duration={edge.sessions > 0 ? 1.8 : 2.4}
              delay={(index % 6) * 0.45}
              className={cn("transition-opacity max-lg:hidden", !lit && "opacity-15")}
            />
          )
        })}

        <div className="relative grid min-w-0 gap-y-10 lg:grid-cols-[minmax(0,1fr)_minmax(7rem,0.9fr)_minmax(0,1fr)] lg:items-center">
          <ol className="flex min-w-0 flex-col gap-7" aria-label="Databases">
            {databases.map((node) => {
              const engine = engineOf(node)
              return (
                <li
                  key={node.id}
                  {...watch(node.id)}
                  className={cn("min-w-0 transition-opacity", !related(node.id) && "opacity-40")}
                >
                  <WireNode
                    nodeRef={refFor(node.id)}
                    align="end"
                    mark={
                      <Cornered tone={stateOf?.(node)}>
                        <WireMark tone="logo">
                          <EngineGlyph engine={engine} />
                        </WireMark>
                      </Cornered>
                    }
                    eyebrow={engine.label}
                    title={
                      node.href ? (
                        <Link
                          href={node.href}
                          aria-label={`Open ${node.name}`}
                          className="rounded-sm focus-ring hover:underline"
                        >
                          {node.name}
                        </Link>
                      ) : (
                        node.name
                      )
                    }
                    hint={feedsLine(feeds.get(node.id) ?? [])}
                  />
                </li>
              )
            })}
          </ol>

          {/* The lane the wires cross. Nothing is drawn in it on purpose. */}
          <div aria-hidden className="hidden lg:block" />

          {readers && (
            <ol className="flex min-w-0 flex-col gap-7" aria-label="What reads them">
              {consumers.length === 0 && (
                <li className="text-hint leading-relaxed text-muted-foreground">
                  Nothing has been seen reading these yet. A deployment that links a database, a
                  container whose environment names one, or an open session will appear here.
                </li>
              )}
              {consumers.map((node) => {
                const sources = fedBy.get(node.id) ?? []
                const worst = worstStatus(sources.map((source) => source.edge))
                // Said in words only when something is wrong: a link that
                // broke or went stale, a reader that is not running.
                const verdict = consumerVerdict(worst, node.status)
                return (
                  <li
                    key={node.id}
                    {...watch(node.id)}
                    className={cn("min-w-0 transition-opacity", !related(node.id) && "opacity-40")}
                  >
                    <WireNode
                      nodeRef={refFor(node.id)}
                      mark={
                        <Cornered tone={node.kind === "host" ? undefined : runningTone(node)}>
                          <ConsumerMark node={node} worst={worst} size="lg" />
                        </Cornered>
                      }
                      eyebrow={consumerKind(node)}
                      title={
                        node.href ? (
                          <Link
                            href={node.href}
                            aria-label={`Open ${node.name}`}
                            className="block truncate rounded-sm focus-ring hover:underline"
                          >
                            {node.name}
                          </Link>
                        ) : (
                          <span className="block truncate">{node.name}</span>
                        )
                      }
                      hint={
                        <>
                          {verdict !== "ok" && node.kind !== "host" && (
                            <span
                              className={cn(
                                "block font-medium",
                                verdict === "critical" ? "text-destructive" : "text-warning",
                              )}
                            >
                              {consumerWord(worst, node.status)}
                            </span>
                          )}
                          {/* Which databases reach it — in words below `lg`,
                              where the wires are not drawn. */}
                          <span className="lg:hidden">
                            {sources.map((source) => source.from.name).join(", ")}
                            {sources.length > 0 && " · "}
                          </span>
                          {sources.length === 1
                            ? describeEdge(sources[0].edge)
                            : linksLine(sources.map((source) => source.edge))}
                        </>
                      }
                    />
                  </li>
                )
              })}
            </ol>
          )}
        </div>
      </div>
    </div>
  )
}

/** A mark with a state's dot in its corner, as Configuration draws a service. */
function Cornered({ tone, children }: { tone?: DotTone; children: React.ReactNode }) {
  if (!tone) return <>{children}</>
  return (
    <span className="relative flex">
      {children}
      <span className="absolute -right-0.5 -bottom-0.5 flex size-3.5 items-center justify-center rounded-full bg-background">
        <StatusDot tone={tone} />
      </span>
    </span>
  )
}

/** A container's or a deployment's state as its dot; none where nothing says. */
function runningTone(node: DbTopoNode): DotTone | undefined {
  switch (node.status) {
    case "running":
    case "connected":
      return "running"
    case "restarting":
    case "paused":
    case "created":
      return "warning"
    case "exited":
    case "dead":
      return "danger"
  }
  return undefined
}

/** What the lines mean, in one line beside the map. */
export function WiringLegend({ className }: { className?: string }) {
  return (
    <ul
      aria-label="How to read the map"
      className={cn(
        "flex flex-wrap items-center gap-x-5 gap-y-1.5 text-hint text-muted-foreground",
        className,
      )}
    >
      <LegendLine kind="live">carrying sessions</LegendLine>
      <LegendLine kind="still">declared, idle</LegendLine>
      <LegendLine kind="dashed">could reach it</LegendLine>
      <LegendLine kind="warning">link stale</LegendLine>
      <LegendLine kind="danger">link broken</LegendLine>
    </ul>
  )
}

function LegendLine({
  kind,
  children,
}: {
  kind: "live" | "still" | "dashed" | "warning" | "danger"
  children: React.ReactNode
}) {
  return (
    <li className="flex items-center gap-2">
      <svg aria-hidden width="28" height="6" viewBox="0 0 28 6" className="shrink-0">
        <path
          d="M 1,3 H 27"
          strokeWidth="1.5"
          strokeLinecap="round"
          strokeDasharray={kind === "dashed" ? "2 5" : undefined}
          className={cn(
            kind === "live" && "stroke-signal",
            kind === "still" && "stroke-border-strong",
            kind === "dashed" && "stroke-border-strong",
            kind === "warning" && "stroke-warning",
            kind === "danger" && "stroke-destructive",
          )}
        />
      </svg>
      <span>{children}</span>
    </li>
  )
}

/** A reader drawn as itself: this server, the product it runs, or its kind. */
export function ConsumerMark({
  node,
  worst,
  size,
}: {
  node: DbTopoNode
  worst: string
  size: "lg" | "md" | "sm"
}) {
  if (node.kind === "host") return <WireHost size={size} />
  const wrong =
    edgeRank(worst) >= 3 || node.status === "exited" || node.status === "dead"
      ? "danger"
      : edgeRank(worst) === 2
        ? "warning"
        : undefined
  const product = node.product && hasProductLogo(node.product) ? node.product : undefined
  return (
    <WireMark tone={wrong ?? (product ? "logo" : "neutral")} size={size} shape="square">
      {product ? <ProductGlyph id={product} /> : <KindGlyph node={node} />}
    </WireMark>
  )
}

function KindGlyph({ node }: { node: DbTopoNode }) {
  switch (node.kind) {
    case "deployment":
      return <CloudUpload />
    case "container":
      return <Box />
    default:
      return <Globe />
  }
}

export function consumerKind(node: DbTopoNode) {
  switch (node.kind) {
    case "deployment":
      return "Deployment"
    case "container":
      return "Container"
    case "host":
      return "Processes here"
    case "remote":
      return "Another machine"
    default:
      return ""
  }
}

export function beamTone(edge: DbTopoEdge): "default" | "warning" | "danger" {
  if (edgeRank(edge.status) >= 3) return "danger"
  if (edgeRank(edge.status) >= 1) return "warning"
  return "default"
}

export function consumerVerdict(worst: string, status: string | undefined): Verdict {
  if (edgeRank(worst) >= 3 || status === "exited" || status === "dead") return "critical"
  if (edgeRank(worst) === 2 || status === "restarting" || status === "paused") return "warning"
  return "ok"
}

export function consumerWord(worst: string, status: string | undefined) {
  if (edgeRank(worst) >= 3) return "link broken"
  if (edgeRank(worst) === 2) return "link stale"
  return status ?? "linked"
}

function feedsLine(edges: DbTopoEdge[]) {
  if (edges.length === 0) return "nothing seen reading it"
  const sessions = edges.reduce((sum, edge) => sum + edge.sessions, 0)
  const parts = [edges.length === 1 ? "feeds 1 thing" : `feeds ${edges.length} things`]
  if (sessions > 0) parts.push(sessions === 1 ? "1 session open" : `${sessions} sessions open`)
  return parts.join(" · ")
}

/** Several links into one reader, as a count and what they carry together. */
function linksLine(edges: DbTopoEdge[]) {
  const sessions = edges.reduce((sum, edge) => sum + edge.sessions, 0)
  const parts = [`${edges.length} databases`]
  if (sessions > 0) parts.push(sessions === 1 ? "1 session open" : `${sessions} sessions open`)
  return parts.join(" · ")
}
