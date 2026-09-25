"use client"

import { createRef, useMemo, useRef, type RefObject } from "react"
import Link from "next/link"
import { Box, CloudUpload, Database, Globe, Servers } from "@/components/icons"
import type { DbTopoEdge, DbTopoNode, DbTopology } from "@/lib/types"
import { cn } from "@/lib/utils"
import { AnimatedBeam } from "@/components/ui/animated-beam"
import { WireMark, WireNode } from "@/components/deploy/wire"
import { ProductGlyph, hasProductLogo } from "@/components/product-logo"
import { Status, type Verdict } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { EmptyState } from "@/components/state"
import { describeEdge, edgeRank, splitTopology } from "@/components/database/fleet"
import { engineLabel } from "@/components/database/fleet-card"

/**
 * The map: every database on the left, everything it feeds on the right, and
 * a line between each pair with light travelling along it from the database
 * to the thing reading it.
 *
 * The lines are Magic UI's animated beam — `ui/animated-beam`, already this
 * product's — because this is exactly the picture it was made for: things
 * that exist and the traffic between them. A beam that pulses is a link
 * something is using; a still line is one that is declared and idle; a dashed
 * one is a link that could carry (same stack, same network) and has not been
 * seen to. A broken binding is drawn in the danger colour, a stale one in the
 * warning colour, and the reader finds the one that matters without reading a
 * name.
 *
 * The ends are laid out by CSS and the beams follow them, so the picture is
 * two columns of ordinary nodes at `lg` and a single column below it, where
 * the lines would cross the words; there each consumer says in words which
 * databases reach it.
 */
export function DatabaseTopology({
  topology,
  compact,
  className,
}: {
  topology: DbTopology | undefined
  /** The overview's version: fewer words per node, no empty-state frame. */
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
    <div ref={container} className={cn("relative", className)}>
      {edges.map((edge, i) => (
        <AnimatedBeam
          key={`${edge.from}→${edge.to}`}
          containerRef={container}
          fromRef={refFor(edge.from)}
          toRef={refFor(edge.to)}
          still={edge.sessions === 0 && edge.status !== "connected"}
          dashed={edge.via.every((v) => v === "stack" || v === "network")}
          tone={beamTone(edge)}
          duration={edge.sessions > 0 ? 1.8 : 3}
          delay={(i % 6) * 0.35}
          className="max-lg:hidden"
        />
      ))}

      <div className="grid gap-y-8 lg:grid-cols-[minmax(0,1fr)_minmax(0,1fr)] lg:gap-x-28">
        <ol className="flex flex-col gap-6" aria-label="Databases">
          {databases.map((node) => (
            <li key={node.id}>
              <WireNode
                nodeRef={refFor(node.id)}
                align="end"
                mark={
                  <NodeLink node={node}>
                    <WireMark tone="logo" size={compact ? "md" : "lg"}>
                      <NodeGlyph node={node} />
                    </WireMark>
                  </NodeLink>
                }
                eyebrow={engineLabel(node.product ?? "")}
                title={node.name}
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

        <ol className="flex flex-col gap-6" aria-label="What they feed">
          {consumers.length === 0 && (
            <li className="text-hint text-muted-foreground lg:pt-4">
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
                <WireNode
                  nodeRef={refFor(node.id)}
                  align="start"
                  mark={
                    <NodeLink node={node}>
                      <WireMark tone={markTone(node, worst)} size={compact ? "md" : "lg"}>
                        <NodeGlyph node={node} />
                      </WireMark>
                    </NodeLink>
                  }
                  eyebrow={kindWord(node)}
                  title={
                    <span className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-0.5">
                      <span className="truncate">{node.name}</span>
                      {node.status && node.kind !== "host" && (
                        <Status
                          verdict={statusVerdict(worst, node.status)}
                          label={statusWord(worst, node.status)}
                        />
                      )}
                    </span>
                  }
                  hint={
                    <>
                      {node.detail && <span className="block truncate">{node.detail}</span>}
                      {/* How each link is known — and, below `lg`, from which
                          database, since the line cannot be drawn there. */}
                      <span className="mt-1 flex flex-wrap items-center gap-1.5">
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
  )
}

function NodeLink({ node, children }: { node: DbTopoNode; children: React.ReactNode }) {
  if (!node.href) return <>{children}</>
  return (
    <Link href={node.href} aria-label={`Open ${node.name}`} className="rounded-full focus-ring">
      {children}
    </Link>
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
