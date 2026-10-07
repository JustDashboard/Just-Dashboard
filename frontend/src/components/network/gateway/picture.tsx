"use client"

import Link from "next/link"
import { createRef, useMemo, useRef, useState, type RefObject } from "react"
import { Bridge, Globe, Plus, Router, Servers, type Icon } from "@/components/icons"
import type { GatewayForward, GatewayNAT, GatewayView, NetworkLink } from "@/lib/types"
import { cn } from "@/lib/utils"
import { ProductGlyph } from "@/components/product-logo"
import { WireHost, WireMark, WireNode, WirePlaceholder } from "@/components/deploy/wire"
import { AnimatedBeam } from "@/components/ui/animated-beam"
import { Cidr } from "@/components/network/address"
import { LinkGlyph, linkProduct } from "@/components/network/marks"
import { ArrivalMark, Endpoint, Port, TargetMark } from "@/components/network/gateway/marks"
import {
  addressWord,
  forwardTitle,
  ownerOf,
  PROTOCOL_WORD,
  sourcesWord,
  targetPortOf,
} from "@/components/network/gateway/reading"

/** How many of each kind are drawn; the lists below hold the rest. */
const FORWARDS_DRAWN = 5
const NAT_DRAWN = 3

type Node = {
  id: string
  side: "in" | "out"
  mark: React.ReactNode
  eyebrow: React.ReactNode
  title: React.ReactNode
  hint?: React.ReactNode
  /** The wire carries: the entry's counter grew since the last read. */
  moving: boolean
  /** The wire is a promise: the entry is off, or not in the kernel. */
  dashed: boolean
  /** A dashed ring where an entry would go: wired, but drawn by the lane itself. */
  placeholder?: boolean
}

/** The firewall's own name, for the middle node's title. */
const FIREWALL: Record<GatewayView["capability"]["firewall"], string> = {
  ufw: "ufw",
  firewalld: "firewalld",
  iptables: "iptables",
  nftables: "nftables",
  none: "No firewall",
}

/**
 * What this server passes on, and to where, in the wiring vocabulary every
 * picture in the product speaks.
 *
 * Left is where traffic arrives — the internet through any device, or the
 * device a forward is tied to, with its public port in the port hue after a
 * colon, and a NAT entry's source network; the middle is this server, with the
 * firewall's capability as its hint; right is where it goes, each target drawn
 * as the product that usually answers on its port, and each NAT entry's way
 * out. A wire carries a pulse when its entry's packet counter grew since the
 * gateway was last read — that is the only honest "alive" a firewall counter
 * can give — runs still when it did not, and is dashed when the entry is
 * switched off or the table is not in the kernel. Pointing at a node steps
 * every other wire back.
 *
 * A kind with nothing made yet stands as a dashed ring with the form that makes
 * one behind it, and its counterpart on the right as a ring that only says
 * what would be there. Three lanes from `lg`; below it they stack and the wires
 * are not drawn.
 */
export function GatewayPicture({
  view,
  links,
  moved,
  onForward,
  onShare,
  onOpenForward,
  onOpenNAT,
}: {
  view: GatewayView
  links: NetworkLink[]
  /** Keys of the entries that carried a packet since the last read (`useGrowth`). */
  moved: Set<string>
  onForward: () => void
  onShare: () => void
  onOpenForward: (forward: GatewayForward) => void
  onOpenNAT: (entry: GatewayNAT) => void
}) {
  const container = useRef<HTMLDivElement>(null)
  const host = useRef<HTMLDivElement>(null)
  const [focus, setFocus] = useState<string | null>(null)
  const writable = view.capability.writable

  const nodes = useMemo(
    () => buildNodes(view, links, moved, { onOpenForward, onOpenNAT }),
    [view, links, moved, onOpenForward, onOpenNAT],
  )
  const ids = nodes.map((n) => n.id).join("\n")
  const refs = useMemo(() => {
    const map = new Map<string, RefObject<HTMLDivElement | null>>()
    for (const id of ids.split("\n")) if (id) map.set(id, createRef<HTMLDivElement>())
    return map
  }, [ids])

  const arrive = nodes.filter((n) => n.side === "in" && !n.placeholder)
  const leave = nodes.filter((n) => n.side === "out" && !n.placeholder)
  const watch = (id: string) => ({
    onPointerEnter: () => setFocus(id),
    onPointerLeave: () => setFocus((held) => (held === id ? null : held)),
    onFocus: () => setFocus(id),
    onBlur: () => setFocus((held) => (held === id ? null : held)),
  })
  // A wire meets a mark at its edge, not under its logo: half of `md` and a breath.
  const port = 26
  const { forwarding, capability } = view

  return (
    <div className="relative animate-rise py-4">
      <div aria-hidden className="wire-grid pointer-events-none absolute -inset-x-4 inset-y-0" />
      <div ref={container} className="relative">
        {nodes.map((node, index) => {
          const ref = refs.get(node.id)
          if (!ref) return null
          const lit = focus === null || focus === node.id
          return (
            <AnimatedBeam
              key={node.id}
              containerRef={container}
              fromRef={node.side === "in" ? ref : host}
              toRef={node.side === "in" ? host : ref}
              shape="s"
              startXOffset={port}
              endXOffset={-port}
              still={!node.moving}
              dashed={node.dashed}
              duration={node.moving ? 2.2 : 3}
              delay={(index % 5) * 0.3}
              className={cn("transition-opacity max-lg:hidden", !lit && "opacity-15")}
            />
          )
        })}

        <div className="relative grid gap-y-8 lg:grid-cols-[minmax(0,1fr)_clamp(2.5rem,6vw,6rem)_minmax(0,0.8fr)_clamp(2.5rem,6vw,6rem)_minmax(0,1fr)] lg:items-center">
          <Lane title="Arrives" className="lg:text-right">
            {arrive.map((node) => (
              <NodeItem
                key={node.id}
                node={node}
                nodeRef={refs.get(node.id)}
                align="end"
                dim={focus !== null && focus !== node.id}
                {...watch(node.id)}
              />
            ))}
            {view.forwards.length === 0 && (
              <PlaceholderItem
                nodeRef={refs.get("add:forward")}
                align="end"
                label="Forward a port"
                hint="a public port to a container or a machine"
                fallback={Globe}
                onPress={writable ? onForward : undefined}
              />
            )}
            {view.nat.length === 0 && (
              <PlaceholderItem
                nodeRef={refs.get("add:nat")}
                align="end"
                label="Share a network out"
                hint="a private network, through this server's address"
                fallback={Bridge}
                onPress={writable ? onShare : undefined}
              />
            )}
          </Lane>

          <div aria-hidden className="max-lg:hidden" />

          <div className="flex min-w-0 justify-center">
            <WireNode
              nodeRef={host}
              align="center"
              mark={<WireHost />}
              eyebrow="This server"
              title={FIREWALL[capability.firewall]}
              hint={
                <>
                  <span className={cn("block", !capability.writable && "text-warning")}>
                    {capability.writable ? "the gateway writes here" : "read-only for the gateway"}
                    {capability.docker && " · Docker"}
                  </span>
                  <span className="block">
                    IPv4 {forwarding.ipv4 ? "forwarding" : "not forwarding"}
                    {" · "}
                    IPv6 {forwarding.ipv6 ? "on" : "off"}
                  </span>
                </>
              }
            />
          </div>

          <div aria-hidden className="max-lg:hidden" />

          <Lane title="Goes to">
            {leave.map((node) => (
              <NodeItem
                key={node.id}
                node={node}
                nodeRef={refs.get(node.id)}
                align="start"
                dim={focus !== null && focus !== node.id}
                {...watch(node.id)}
              />
            ))}
            {view.forwards.length === 0 && (
              <PlaceholderItem
                nodeRef={refs.get("sink:forward")}
                align="start"
                label="Its target"
                hint="an address and a port, drawn as what answers there"
                fallback={Servers}
              />
            )}
            {view.nat.length === 0 && (
              <PlaceholderItem
                nodeRef={refs.get("sink:nat")}
                align="start"
                label="The way out"
                hint="the device its traffic leaves through"
                fallback={Router}
              />
            )}
          </Lane>
        </div>
      </div>
    </div>
  )
}

function Lane({
  title,
  className,
  children,
}: {
  title: string
  className?: string
  children: React.ReactNode
}) {
  return (
    <section className={cn("min-w-0", className)} aria-label={title}>
      <p className="eyebrow mb-4 lg:hidden">{title}</p>
      <ol className="flex flex-col gap-5">{children}</ol>
    </section>
  )
}

function NodeItem({
  node,
  nodeRef,
  align,
  dim,
  ...handlers
}: {
  node: Node
  nodeRef?: RefObject<HTMLDivElement | null>
  align: "start" | "end"
  dim: boolean
} & React.HTMLAttributes<HTMLLIElement>) {
  return (
    <li {...handlers} className={cn("min-w-0 transition-opacity", dim && "opacity-40")}>
      <WireNode
        nodeRef={nodeRef}
        align={align}
        mark={node.mark}
        eyebrow={node.eyebrow}
        title={node.title}
        hint={node.hint}
      />
    </li>
  )
}

/**
 * A dashed ring where an entry would go. With `onPress` it is the button that
 * opens the form that makes one; without, it is only the other end of the wire
 * the form would draw, so the picture says what each side will hold.
 */
function PlaceholderItem({
  nodeRef,
  align,
  label,
  hint,
  fallback,
  onPress,
}: {
  nodeRef?: RefObject<HTMLDivElement | null>
  align: "start" | "end"
  label: string
  hint: string
  fallback: Icon
  onPress?: () => void
}) {
  const ring = (
    <WirePlaceholder size="md" fallback={fallback}>
      <Plus aria-hidden />
    </WirePlaceholder>
  )
  return (
    <li className="min-w-0">
      <WireNode
        nodeRef={nodeRef}
        align={align}
        mark={
          onPress ? (
            <button
              type="button"
              onClick={onPress}
              aria-label={label}
              className="rounded-full focus-ring"
            >
              {ring}
            </button>
          ) : (
            ring
          )
        }
        title={<span className="text-muted-foreground">{label}</span>}
        hint={hint}
      />
    </li>
  )
}

/** The nodes, from the gateway's entries and the devices. Pure, so it is testable. */
export function buildNodes(
  view: GatewayView,
  links: NetworkLink[],
  moved: Set<string>,
  open: {
    onOpenForward: (forward: GatewayForward) => void
    onOpenNAT: (entry: GatewayNAT) => void
  },
): Node[] {
  const nodes: Node[] = []
  const inForce = (enabled: boolean) => enabled && view.loaded

  for (const f of view.forwards.slice(0, FORWARDS_DRAWN)) {
    const state = {
      moving: inForce(f.enabled) && moved.has(`forward:${f.id}`),
      dashed: !inForce(f.enabled),
    }
    nodes.push({
      id: `forward:${f.id}:in`,
      side: "in",
      mark: (
        <button
          type="button"
          onClick={() => open.onOpenForward(f)}
          aria-label={`Open ${forwardTitle(f)}`}
          className="rounded-xl focus-ring"
        >
          <ArrivalMark device={f.interface} links={links} />
        </button>
      ),
      eyebrow: f.interface ? `On ${f.interface}` : "Any device",
      title: (
        <span className={cn(!f.enabled && "text-muted-foreground")}>
          <Port port={f.ports} />{" "}
          <span className="text-hint font-normal text-muted-foreground">
            {PROTOCOL_WORD[f.protocol]}
          </span>
        </span>
      ),
      hint: (
        <span className="block truncate">
          {f.enabled
            ? f.sources.length
              ? `from ${sourcesWord(f.sources)}`
              : f.name
            : "switched off"}
        </span>
      ),
      ...state,
    })
    nodes.push({
      id: `forward:${f.id}:out`,
      side: "out",
      mark: <TargetMark ports={f.ports} targetPort={f.targetPort} />,
      eyebrow: f.name,
      title: (
        <Endpoint
          address={f.target}
          port={targetPortOf(f)}
          className={cn(!f.enabled && "text-muted-foreground")}
        />
      ),
      hint: addressWord(f),
      ...state,
    })
  }

  for (const n of view.nat.slice(0, NAT_DRAWN)) {
    const state = {
      moving: inForce(n.enabled) && moved.has(`nat:${n.id}`),
      dashed: !inForce(n.enabled),
    }
    const owner = n.owner ? ownerOf(n.owner) : undefined
    const exit = links.find((l) => l.name === n.interface)
    const exitProduct = exit ? linkProduct(exit) : undefined
    const origin = (
      <WireMark tone="logo" shape="square" size="md">
        {owner?.product === "wireguard" || owner?.product === "tailscale" ? (
          <ProductGlyph id={owner.product} />
        ) : (
          <Bridge aria-hidden />
        )}
      </WireMark>
    )
    nodes.push({
      id: `nat:${n.id}:in`,
      side: "in",
      mark: owner ? (
        <Link
          href="/network/vpn"
          aria-label={`${n.name}, made by ${owner.name}`}
          className="rounded-xl focus-ring"
        >
          {origin}
        </Link>
      ) : (
        <button
          type="button"
          onClick={() => open.onOpenNAT(n)}
          aria-label={`Open ${n.name}`}
          className="rounded-xl focus-ring"
        >
          {origin}
        </button>
      ),
      eyebrow: owner ? `NAT · ${owner.name}` : "NAT",
      title: <Cidr cidr={n.source} className={cn(!n.enabled && "text-muted-foreground")} />,
      hint: <span className="block truncate">{n.enabled ? n.name : "switched off"}</span>,
      ...state,
    })
    nodes.push({
      id: `nat:${n.id}:out`,
      side: "out",
      mark: (
        <WireMark tone="logo" shape="square" size="md">
          {exitProduct ? (
            <ProductGlyph id={exitProduct} />
          ) : (
            <LinkGlyph kind={exit?.kind ?? "physical"} />
          )}
        </WireMark>
      ),
      eyebrow: "Leaves through",
      title: (
        <span className={cn("font-mono", !n.enabled && "text-muted-foreground")}>
          {n.interface}
        </span>
      ),
      hint: n.toAddress ? (
        <span className="block truncate font-mono">as {n.toAddress}</span>
      ) : (
        "masqueraded"
      ),
      ...state,
    })
  }

  // The dashed rings the picture draws for a kind with nothing made: they are
  // wired like any node, dashed and still, so the lanes read as complete.
  const placeholder = (id: string, side: Node["side"]): Node => ({
    id,
    side,
    mark: null,
    eyebrow: null,
    title: null,
    moving: false,
    dashed: true,
    placeholder: true,
  })
  if (view.forwards.length === 0) {
    nodes.push(placeholder("add:forward", "in"), placeholder("sink:forward", "out"))
  }
  if (view.nat.length === 0) {
    nodes.push(placeholder("add:nat", "in"), placeholder("sink:nat", "out"))
  }
  return nodes
}
