"use client"

import { createRef, useMemo, useRef, type RefObject } from "react"
import Link from "next/link"
import { Connection, Globe, LockClosed, LockOpen, ShieldCheck, type Icon } from "@/components/icons"
import { cn } from "@/lib/utils"
import type { ChainKind, ChainNode, ChainTone } from "@/lib/tls-chain"
import { ProductGlyph, hasProductLogo } from "@/components/product-logo"
import { StatusDot, type DotTone } from "@/components/status-dot"
import { AnimatedBeam } from "@/components/ui/animated-beam"
import { WireHost, WireMark } from "@/components/deploy/wire"
import { sitePath } from "@/components/proxy/site-verbs"

const GLYPH: Partial<Record<ChainKind, Icon>> = {
  domain: Globe,
  name: Globe,
  port: Connection,
  handshake: LockClosed,
  leaf: LockClosed,
  intermediate: ShieldCheck,
  root: ShieldCheck,
  proxy: Connection,
}

const DOT: Record<ChainTone, DotTone> = {
  success: "running",
  warning: "warning",
  danger: "danger",
  neutral: "unknown",
}

/** Up to six steps, one column each from `lg`. */
const COLUMNS: Record<number, string> = {
  4: "lg:grid-cols-4",
  5: "lg:grid-cols-5",
  6: "lg:grid-cols-6",
}

/**
 * What the scan met on its way to the name, drawn as the route picture on a
 * site's page draws a request (`site-route-map.tsx`): this server as the
 * visitor, the name and the address it reached, whatever answered the
 * handshake, the certificate it sent, the intermediate and the root a
 * verifier ended on — each step on a tile in its verdict's colour, a product
 * drawn as itself with its verdict in the corner.
 *
 * The lines carry a pulse while a scan is in flight and one more when its
 * answer lands; a line into a step nothing reached is dashed. A failed scan is
 * the same row cut short: the name, the port and the handshake, the step that
 * failed in red. Before anything is asked every step is drawn unjudged, so the
 * picture is in its place, at its height, from the first frame.
 *
 * `products` draws a step as the product it is — nginx where nginx answered,
 * Let's Encrypt where it signed. `extra` sets something under a step's words:
 * the leaf's term as a meter.
 */
export function TLSChainPicture({
  nodes,
  moving,
  arrived,
  products = {},
  extra = {},
  label = "How the scan reached it",
}: {
  nodes: ChainNode[]
  /** A scan is in flight: the lines carry a pulse until it answers. */
  moving?: boolean
  /** A key that changes when an answer lands, which sends one pulse down every line. */
  arrived?: string
  products?: Partial<Record<string, string>>
  extra?: Partial<Record<string, React.ReactNode>>
  label?: string
}) {
  const container = useRef<HTMLDivElement>(null)
  const keys = nodes.map((node) => node.key).join("\n")
  const refs = useMemo(() => {
    const out = new Map<string, RefObject<HTMLSpanElement | null>>()
    for (const key of keys.split("\n")) out.set(key, createRef<HTMLSpanElement>())
    return out
  }, [keys])

  return (
    <section
      aria-label={label}
      data-slot="tls-chain"
      className="relative min-w-0 animate-rise py-2"
    >
      <div ref={container} className="relative min-w-0">
        {nodes.slice(1).map((node, index) => {
          const from = refs.get(nodes[index].key)
          const to = refs.get(node.key)
          if (!from || !to) return null
          return (
            <AnimatedBeam
              key={`${nodes[index].key}→${node.key}-${moving ? "moving" : (arrived ?? "")}`}
              containerRef={container}
              fromRef={from}
              toRef={to}
              still={!moving && !arrived}
              once={!moving && Boolean(arrived)}
              dashed={node.wire.dashed}
              tone={node.wire.tone}
              duration={2.4}
              delay={index * 0.3}
            />
          )
        })}
        <ol
          className={cn(
            "relative grid min-w-0 gap-y-5 lg:items-start lg:gap-x-3",
            COLUMNS[nodes.length] ?? "lg:grid-cols-6",
          )}
        >
          {nodes.map((node) => (
            <ChainStep
              key={node.key}
              node={node}
              markRef={refs.get(node.key)}
              product={products[node.key]}
              extra={extra[node.key]}
            />
          ))}
        </ol>
      </div>
    </section>
  )
}

/**
 * One step: its tile, then what it is, its name and one line about it. Wide,
 * the words stand under the tile, centred, so every tile is on one line and
 * the lines between them run level; narrow, the steps stand in a column and
 * the words sit beside each tile.
 */
function ChainStep({
  node,
  markRef,
  product,
  extra,
}: {
  node: ChainNode
  markRef?: RefObject<HTMLSpanElement | null>
  product?: string
  extra?: React.ReactNode
}) {
  return (
    <li
      data-slot="chain-node"
      data-tone={node.tone}
      className="flex min-w-0 items-center gap-3.5 lg:flex-col lg:items-center lg:gap-2.5 lg:text-center"
    >
      <span ref={markRef} className="relative z-10 flex shrink-0">
        <StepMark node={node} product={product} />
      </span>
      <div className="min-w-0 flex-1 lg:w-full lg:flex-none">
        <p className="eyebrow truncate">{node.eyebrow}</p>
        <div
          data-slot="chain-title"
          title={node.title}
          className={cn(
            // A fixed line, so a name in mono and a dash in the body face
            // stand the same height and a step does not move as it is judged.
            "h-5 truncate text-body leading-5 font-medium",
            node.mono && "font-mono text-[0.92em]",
            node.tone === "neutral" && node.title === "—" && "text-muted-foreground",
          )}
        >
          {node.site ? (
            <Link
              href={sitePath(node.site)}
              className="rounded-sm underline-offset-4 focus-ring hover:underline"
            >
              {node.title}
            </Link>
          ) : (
            node.title
          )}
        </div>
        {/* The hint's line is held when there is none, so a step judged and
            the same step waiting for its answer are one height. */}
        <p
          title={node.hint}
          className={cn(
            "truncate text-hint leading-snug",
            node.tone === "warning"
              ? "text-warning"
              : node.tone === "danger"
                ? "text-destructive"
                : "text-muted-foreground",
          )}
        >
          {node.hint ?? "\u00a0"}
        </p>
        {extra !== undefined && (
          <div className="mt-1.5 flex h-1.5 items-center lg:justify-center">{extra}</div>
        )}
      </div>
    </li>
  )
}

/**
 * The step's tile: this server as the rack every picture draws it as; a
 * product as itself on its own tile with its verdict in the corner; anything
 * else its glyph on its verdict's tint.
 */
function StepMark({ node, product }: { node: ChainNode; product?: string }) {
  if (node.kind === "visitor") return <WireHost size="md" />
  if (product && hasProductLogo(product))
    return (
      <span className="relative flex">
        <WireMark tone="logo" size="md">
          <ProductGlyph id={product} />
        </WireMark>
        <span className="absolute -right-0.5 -bottom-0.5 flex size-3.5 items-center justify-center rounded-full bg-background">
          <StatusDot tone={DOT[node.tone]} />
        </span>
      </span>
    )
  const Glyph =
    node.kind === "handshake" && node.tone === "danger" ? LockOpen : (GLYPH[node.kind] ?? Globe)
  return (
    <WireMark tone={node.tone === "neutral" ? "neutral" : node.tone} size="md">
      <Glyph aria-hidden />
    </WireMark>
  )
}
