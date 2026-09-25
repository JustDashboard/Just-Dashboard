"use client"

import { createRef, useMemo, useRef, type RefObject } from "react"
import Link from "next/link"
import { Box, CloudUpload, Database, Globe, Servers } from "@/components/icons"
import type { DbTopoEdge, DbTopoNode, DbTopology } from "@/lib/types"
import { cn } from "@/lib/utils"
import { AnimatedBeam } from "@/components/ui/animated-beam"
import { WireMark } from "@/components/deploy/wire"
import { ProductGlyph, hasProductLogo } from "@/components/product-logo"
import { Status, type Verdict } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { EmptyState } from "@/components/state"
import { describeEdge, edgeRank, splitTopology } from "@/components/database/fleet"
import { engineLabel } from "@/components/database/fleet-card"

/**
 * The map: every database in one lane, everything it feeds in the other, and
 * a wire between each pair with light travelling along it from the database
 * to the thing reading it.
 *
 * It is drawn as a node editor draws a graph — each thing a card with its
 * port at the inner edge, the wires leaving level and arriving level across
 * a lane kept clear for them — because the first version stood two columns
 * of bare marks a hand's width apart in the middle of the page, and a map of
 * seven databases read as a list that happened to have some lines on it. The
 * cards fill their lanes, the lane between them is wide enough for a wire to
 * bend in, and the whole picture sits on the dot grid the ER diagram draws
 * its tables on, so the two pictures of a database's surroundings are one
 * picture. The frame around it survives §15 pass 1 for the reason the diagram
 * workbench's does: a canvas is a region with its own ground, and a picture
 * with no edge has no middle.
 *
 * The wires are Magic UI's animated beam — `ui/animated-beam`, in its `s`
 * shape. A beam that pulses is a link something is using; a still line is one
 * that is declared and idle; a dashed one is a link that could carry (same
 * stack, same network) and has not been seen to. A broken binding is drawn in
 * the danger colour, a stale one in the warning colour, and the reader finds
 * the one that matters without reading a name.
 *
 * The cards are laid out by CSS and the beams follow them, so the picture is
 * two lanes from `lg` and a single column below it, where the lines would
 * cross the words; there each consumer says in words which databases reach
 * it.
 */
export function DatabaseTopology({
  topology,
  compact,
  className,
}: {
  topology: DbTopology | undefined
  /** The overview's version: smaller marks, fewer words per node. */
  compact?: boolean
  className?: string
}) {
  const { databases, consumers, feeds, fedBy } = useMemo(() => splitTopology(topology), [topology])
  const container = useRef<HTMLDivElement>(null)
  // One ref per node, keyed by id and remade only when the set of nodes
  // changes, so a poll that changes nothing does not redraw every line.
  const nodeIds = (topology?.nodes ?? []).map((n) => n.id).join("\n")
  const refs = useMemo(() => {
    const map = new Map<string, RefObject<HTMLDivElement | null>>()
    for (const id of nodeIds.split("\n")) if (id) map.set(id, createRef<HTMLDivElement>())
    return map
  }, [nodeIds])
  const refFor = (id: string) => refs.get(id) ?? createRef<HTMLDivElement>()
  const edges = topology?.edges ?? []
  // A wire starts at the card's edge, not at the middle of its mark: half
  // the mark, the card's padding and its border, so the line reads as
  // leaving a port rather than emerging from under a logo.
  const port = compact ? 22 + 12 + 1 : 28 + 16 + 1

  if (!topology) return null
  if (databases.length === 0) {
    return compact ? null : (
      <EmptyState
        icon={Database}
        title="Nothing to map yet"
        description="Connect a database and the things it feeds appear here as they are found."
      />
    )
  }

  return (
    <div
      ref={container}
      className={cn(
        "relative min-w-0 overflow-hidden rounded-xl border border-hairline",
        compact ? "p-4 md:p-5" : "p-5 md:p-8",
        className,
      )}
    >
      <div aria-hidden className="wire-grid pointer-events-none absolute inset-0" />
      {edges.map((edge, i) => (
        <AnimatedBeam
          key={`${edge.from}→${edge.to}`}
          containerRef={container}
          fromRef={refFor(edge.from)}
          toRef={refFor(edge.to)}
          shape="s"
          startXOffset={port}
          endXOffset={-port}
          still={edge.sessions === 0 && edge.status !== "connected"}
          dashed={edge.via.every((v) => v === "stack" || v === "network")}
          tone={beamTone(edge)}
          duration={edge.sessions > 0 ? 1.8 : 3}
          delay={(i % 6) * 0.35}
          className="max-lg:hidden"
        />
      ))}

      <div
        className={cn(
          "relative grid gap-y-8",
          compact
            ? "lg:grid-cols-[minmax(0,1fr)_clamp(4rem,9vw,9rem)_minmax(0,1fr)]"
            : "lg:grid-cols-[minmax(0,1fr)_clamp(6rem,12vw,13rem)_minmax(0,1fr)]",
        )}
      >
        <div className="min-w-0">
          <LaneHead count={databases.length} className="lg:text-right">
            Databases
          </LaneHead>
          <ol className={cn("flex flex-col", compact ? "gap-3" : "gap-4")} aria-label="Databases">
            {databases.map((node) => (
              <li key={node.id}>
                <NodeCard
                  node={node}
                  side="start"
                  compact={compact}
                  nodeRef={refFor(node.id)}
                  mark={
                    <WireMark tone="logo" size={compact ? "md" : "lg"}>
                      <NodeGlyph node={node} />
                    </WireMark>
                  }
                  eyebrow={engineLabel(node.product ?? "")}
                  hint={
                    <>
                      {node.detail}
                      {!compact && (
                        <span className="block">{feedsLine(feeds.get(node.id) ?? [])}</span>
                      )}
                    </>
                  }
                />
              </li>
            ))}
          </ol>
        </div>

        {/* The lane the wires cross. Nothing is drawn in it on purpose. */}
        <div aria-hidden className="hidden lg:block" />

        <div className="min-w-0">
          <LaneHead count={consumers.length}>What reads them</LaneHead>
          <ol
            className={cn("flex flex-col", compact ? "gap-3" : "gap-4")}
            aria-label="What they feed"
          >
            {consumers.length === 0 && (
              <li className="rounded-xl border border-dashed border-hairline p-4 text-hint text-muted-foreground">
                Nothing has been seen reading these yet. A deployment that links a database, a
                container whose environment names one, or an open session will appear here.
              </li>
            )}
            {consumers.map((node) => {
              const sources = fedBy.get(node.id) ?? []
              const worst = sources.reduce(
                (m, s) => (edgeRank(s.edge.status) > edgeRank(m) ? s.edge.status : m),
                "observed",
              )
              return (
                <li key={node.id}>
                  <NodeCard
                    node={node}
                    side="end"
                    compact={compact}
                    nodeRef={refFor(node.id)}
                    mark={
                      <WireMark tone={markTone(node, worst)} size={compact ? "md" : "lg"}>
                        <NodeGlyph node={node} />
                      </WireMark>
                    }
                    eyebrow={kindWord(node)}
                    status={
                      node.status && node.kind !== "host" ? (
                        <Status
                          verdict={statusVerdict(worst, node.status)}
                          label={statusWord(worst, node.status)}
                        />
                      ) : undefined
                    }
                    hint={
                      <>
                        {node.detail && <span className="block truncate">{node.detail}</span>}
                        {/* How each link is known — and, below `lg`, from which
                            database, since the line cannot be drawn there. */}
                        <span className="mt-1 flex flex-wrap items-center gap-x-2 gap-y-0.5">
                          {sources.map(({ from, edge }) => (
                            <Tag key={from.id} mono={false}>
                              <span className="lg:hidden">{from.name} · </span>
                              {compact ? shortEdge(edge) : describeEdge(edge)}
                            </Tag>
                          ))}
                        </span>
                      </>
                    }
                  />
                </li>
              )
            })}
          </ol>
        </div>
      </div>
    </div>
  )
}

/**
 * What the lines mean, in one line under the map. The full page carries it;
 * the overview's compact map sits under a link to the page that does.
 */
export function TopologyLegend({ className }: { className?: string }) {
  return (
    <ul
      aria-label="How to read the map"
      className={cn(
        "flex flex-wrap items-center gap-x-5 gap-y-1.5 text-hint text-muted-foreground",
        className,
      )}
    >
      <LegendLine tone="live">carrying sessions</LegendLine>
      <LegendLine tone="still">declared, idle</LegendLine>
      <LegendLine tone="dashed">could reach it</LegendLine>
      <LegendLine tone="warning">link stale</LegendLine>
      <LegendLine tone="danger">link broken</LegendLine>
    </ul>
  )
}

function LegendLine({
  tone,
  children,
}: {
  tone: "live" | "still" | "dashed" | "warning" | "danger"
  children: React.ReactNode
}) {
  return (
    <li className="flex items-center gap-2">
      <svg aria-hidden width="28" height="6" viewBox="0 0 28 6" className="shrink-0">
        <path
          d="M 1,3 H 27"
          strokeWidth="1.5"
          strokeLinecap="round"
          strokeDasharray={tone === "dashed" ? "2 5" : undefined}
          className={cn(
            tone === "live" && "stroke-signal",
            tone === "still" && "stroke-border-strong",
            tone === "dashed" && "stroke-border-strong",
            tone === "warning" && "stroke-warning",
            tone === "danger" && "stroke-destructive",
          )}
        />
      </svg>
      <span>{children}</span>
    </li>
  )
}

function LaneHead({
  count,
  className,
  children,
}: {
  count: number
  className?: string
  children: React.ReactNode
}) {
  return (
    <p className={cn("eyebrow mb-3", className)}>
      {children}
      <span className="numeric ml-1.5 font-medium tracking-normal text-muted-foreground/70">
        {count}
      </span>
    </p>
  )
}

/**
 * One thing on the map: a card with its mark at the inner edge — the port a
 * wire leaves from or arrives at — and its name and readings beside it. A
 * node with somewhere to go is one card-sized link: the title carries the
 * name and the press lands anywhere on the card.
 */
function NodeCard({
  node,
  side,
  compact,
  nodeRef,
  mark,
  eyebrow,
  status,
  hint,
}: {
  node: DbTopoNode
  /** Which lane it stands in: the port faces the other one. */
  side: "start" | "end"
  compact?: boolean
  nodeRef: RefObject<HTMLDivElement | null>
  mark: React.ReactNode
  eyebrow: React.ReactNode
  status?: React.ReactNode
  hint?: React.ReactNode
}) {
  const title = node.href ? (
    <Link
      href={node.href}
      aria-label={`Open ${node.name}`}
      className="truncate rounded-sm focus-ring after:absolute after:inset-0 after:rounded-xl"
    >
      {node.name}
    </Link>
  ) : (
    <span className="truncate">{node.name}</span>
  )
  return (
    <div
      className={cn(
        // Positioned, so the card paints over the wires drawn under it and
        // the stretched link inside it has a box to fill.
        "group/node relative flex min-w-0 items-center rounded-xl border border-hairline bg-card transition-colors",
        compact ? "gap-3 px-3 py-2.5" : "gap-4 px-4 py-3.5",
        node.href && "hover:border-border-strong",
        side === "start" && "lg:flex-row-reverse lg:text-right",
      )}
    >
      <div ref={nodeRef} className="flex shrink-0">
        {mark}
      </div>
      <div className="min-w-0 flex-1">
        <p className="eyebrow">{eyebrow}</p>
        <div
          className={cn(
            "flex min-w-0 flex-wrap items-center gap-x-2 gap-y-0.5 text-body leading-snug font-medium",
            side === "start" && "lg:justify-end",
          )}
        >
          {title}
          {status}
        </div>
        {hint && <div className="text-hint leading-snug text-muted-foreground">{hint}</div>}
      </div>
    </div>
  )
}

function NodeGlyph({ node }: { node: DbTopoNode }) {
  if (node.product && hasProductLogo(node.product)) return <ProductGlyph id={node.product} />
  switch (node.kind) {
    case "database":
      return <Database />
    case "deployment":
      return <CloudUpload />
    case "container":
      return <Box />
    case "host":
      return <Servers />
    default:
      return <Globe />
  }
}

function kindWord(node: DbTopoNode) {
  switch (node.kind) {
    case "deployment":
      return "Deployment"
    case "container":
      return "Container"
    case "host":
      return "This server"
    case "remote":
      return "Another machine"
    default:
      return ""
  }
}

function beamTone(edge: DbTopoEdge): "default" | "success" | "warning" | "danger" {
  switch (edge.status) {
    case "broken":
    case "failed":
    case "error":
      return "danger"
    case "stale":
    case "pending":
      return "warning"
  }
  return "default"
}

function markTone(node: DbTopoNode, worst: string): "neutral" | "logo" | "danger" | "warning" {
  if (edgeRank(worst) >= 3 || node.status === "exited" || node.status === "dead") return "danger"
  if (edgeRank(worst) === 2) return "warning"
  return node.product && hasProductLogo(node.product) ? "logo" : "neutral"
}

function statusVerdict(worst: string, status: string): Verdict {
  if (edgeRank(worst) >= 3 || status === "exited" || status === "dead") return "critical"
  if (edgeRank(worst) === 2 || status === "restarting" || status === "paused") return "warning"
  return "ok"
}

function statusWord(worst: string, status: string) {
  if (edgeRank(worst) >= 3) return "link broken"
  if (edgeRank(worst) === 2) return "link stale"
  return status
}

function feedsLine(edges: DbTopoEdge[]) {
  if (edges.length === 0) return "feeds nothing yet"
  const sessions = edges.reduce((sum, e) => sum + e.sessions, 0)
  const parts = [edges.length === 1 ? "feeds 1 thing" : `feeds ${edges.length} things`]
  if (sessions > 0) parts.push(sessions === 1 ? "1 session open" : `${sessions} sessions open`)
  return parts.join(" · ")
}

function shortEdge(edge: DbTopoEdge) {
  if (edge.sessions > 0) return `${edge.sessions} open`
  if (edge.via.includes("binding")) return "linked"
  if (edge.via.includes("env")) return "configured"
  if (edge.via.includes("stack")) return "same stack"
  return "seen"
}
