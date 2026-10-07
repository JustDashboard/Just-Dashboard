"use client"

import Link from "next/link"
import { createRef, useMemo, useRef, useState, type RefObject } from "react"
import { Bridge, Globe, Linked } from "@/components/icons"
import { rate } from "@/lib/format"
import type { NetworkLink, NetworkOverview } from "@/lib/types"
import { cn } from "@/lib/utils"
import { ProductGlyph, ProductGlyphs, imageProduct } from "@/components/product-logo"
import { WireHost, WireMark, WireNode, WirePlaceholder } from "@/components/deploy/wire"
import { AnimatedBeam } from "@/components/ui/animated-beam"
import { carrying, linkGlyph, pulseDuration } from "@/components/network/marks"

type Tone = "default" | "warning" | "danger"

/** One node of the picture: where it sits, what it is, and the device its wire measures. */
type Node = {
  id: string
  lane: "outside" | "inside"
  mark: React.ReactNode
  eyebrow: React.ReactNode
  title: React.ReactNode
  hint?: React.ReactNode
  /** The device whose counters the wire carries; undefined draws a still wire. */
  devices: NetworkLink[]
  tone?: Tone
  dashed?: boolean
  href?: string
}

/**
 * The server as a router, drawn in the wiring vocabulary every picture in the
 * product speaks (`deploy/wire`, the runtime map's lanes): what is outside it
 * on the left — the internet through its uplink, the tailnet, each WireGuard
 * or tunnel to another site — this server in the middle with its firewall
 * and gateway as its facts, and the networks it is the gateway of on the
 * right — each Docker network with the products its containers run, each
 * bridge or VLAN made here.
 *
 * A wire is a device's traffic, read every two seconds: it carries a pulse
 * while the device moves more than a kilobyte a second, faster the busier it
 * is (`pulseDuration`), running the way most of the bytes go — into the
 * server for what arrives, out of it for what leaves. It is still while the
 * device is idle, dashed where it is down, amber where the internet reaches a
 * server no firewall filters. Pointing at a node steps every other wire back,
 * as on the runtime map. Where nothing is set up yet a dashed ring stands in
 * the place it would go, with the way to set it up.
 *
 * On the page's own ground over the dot grid. Three lanes from `lg`; below it
 * the lanes stack, the wires are not drawn, and each node says its traffic in
 * words.
 */
export function Topology({
  overview,
  links,
}: {
  overview: NetworkOverview
  /** The devices with their freshest rates; the overview's own until the live ring lands. */
  links: NetworkLink[]
}) {
  const container = useRef<HTMLDivElement>(null)
  const host = useRef<HTMLDivElement>(null)
  const [focus, setFocus] = useState<string | null>(null)

  const nodes = useMemo(() => buildNodes(overview, links), [overview, links])
  const ids = nodes.map((n) => n.id).join("\n")
  const refs = useMemo(() => {
    const map = new Map<string, RefObject<HTMLDivElement | null>>()
    for (const id of ids.split("\n")) if (id) map.set(id, createRef<HTMLDivElement>())
    return map
  }, [ids])

  const outside = nodes.filter((n) => n.lane === "outside")
  const inside = nodes.filter((n) => n.lane === "inside")
  const watch = (id: string) => ({
    onPointerEnter: () => setFocus(id),
    onPointerLeave: () => setFocus((held) => (held === id ? null : held)),
    onFocus: () => setFocus(id),
    onBlur: () => setFocus((held) => (held === id ? null : held)),
  })
  // A wire meets a mark at its edge, not under its logo: half of `md` and a breath.
  const port = 26
  const firewall = overview.firewall

  return (
    <div className="relative animate-rise py-4">
      <div aria-hidden className="wire-grid pointer-events-none absolute -inset-x-4 inset-y-0" />
      <div ref={container} className="relative">
        {nodes.map((node, index) => {
          const ref = refs.get(node.id)
          if (!ref) return null
          const rx = node.devices.reduce((n, d) => n + d.rxRate, 0)
          const tx = node.devices.reduce((n, d) => n + d.txRate, 0)
          const moving = !node.dashed && node.devices.some(carrying)
          const lit = focus === null || focus === node.id
          // Into the server for what arrives on an outside device, out of it
          // for what a bridge carries to its containers (a bridge's transmit
          // is the host sending into it).
          const inward = node.lane === "outside" ? rx >= tx : rx > tx
          return (
            <AnimatedBeam
              key={node.id}
              containerRef={container}
              fromRef={node.lane === "outside" ? ref : host}
              toRef={node.lane === "outside" ? host : ref}
              shape="s"
              startXOffset={port}
              endXOffset={-port}
              reverse={node.lane === "outside" ? !inward : inward}
              still={!moving}
              dashed={node.dashed}
              tone={node.tone ?? "default"}
              duration={pulseDuration(rx + tx)}
              delay={(index % 5) * 0.3}
              className={cn("transition-opacity max-lg:hidden", !lit && "opacity-15")}
            />
          )
        })}

        <div className="relative grid gap-y-8 lg:grid-cols-[minmax(0,1fr)_clamp(2.5rem,6vw,6rem)_minmax(0,0.9fr)_clamp(2.5rem,6vw,6rem)_minmax(0,1fr)] lg:items-center">
          <Lane title="Outside" className="lg:text-right">
            {outside.map((node) => (
              <NodeItem
                key={node.id}
                node={node}
                nodeRef={refs.get(node.id)}
                align="end"
                dim={focus !== null && focus !== node.id}
                {...watch(node.id)}
              />
            ))}
          </Lane>

          <div aria-hidden className="max-lg:hidden" />

          <div className="flex min-w-0 justify-center">
            <WireNode
              nodeRef={host}
              align="center"
              mark={<WireHost />}
              eyebrow="This server"
              title={<span className="block truncate">{overview.hostname || "This server"}</span>}
              hint={
                <>
                  <span className={cn("block", !firewall.available && "text-warning")}>
                    {!firewall.available
                      ? "no firewall"
                      : !firewall.enabled
                        ? `${firewall.backend} not enforcing`
                        : `${firewall.backend} · ${firewall.incoming ?? "—"} inbound`}
                  </span>
                  <span className="block">
                    {overview.forwarding.ipv4 ? "routing on" : "routing off"}
                    {overview.made.forwards > 0 &&
                      ` · ${overview.made.forwards} forward${overview.made.forwards === 1 ? "" : "s"}`}
                    {overview.made.nat > 0 && ` · NAT`}
                  </span>
                </>
              }
            />
          </div>

          <div aria-hidden className="max-lg:hidden" />

          <Lane title="Inside">
            {inside.map((node) => (
              <NodeItem
                key={node.id}
                node={node}
                nodeRef={refs.get(node.id)}
                align="start"
                dim={focus !== null && focus !== node.id}
                {...watch(node.id)}
              />
            ))}
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
  const mark = node.href ? (
    <Link
      href={node.href}
      aria-label={typeof node.title === "string" ? node.title : undefined}
      className="rounded-xl focus-ring"
    >
      {node.mark}
    </Link>
  ) : (
    node.mark
  )
  return (
    <li {...handlers} className={cn("min-w-0 transition-opacity", dim && "opacity-40")}>
      <WireNode
        nodeRef={nodeRef}
        align={align}
        mark={mark}
        eyebrow={node.eyebrow}
        title={node.title}
        hint={node.hint}
      />
    </li>
  )
}

/** In and out as words, for the hint under a node and the stacked picture. */
function Throughput({ devices }: { devices: NetworkLink[] }) {
  if (devices.length === 0) return null
  const rx = devices.reduce((n, d) => n + d.rxRate, 0)
  const tx = devices.reduce((n, d) => n + d.txRate, 0)
  return (
    <span className="numeric block font-mono text-micro">
      <span className="text-[var(--chart-5)]">↓ {rate(rx)}</span>
      <span className="mx-1.5 text-muted-foreground/50">·</span>
      <span className="text-[var(--chart-2)]">↑ {rate(tx)}</span>
    </span>
  )
}

/** The nodes, from the overview's devices and Docker networks. Pure, so it is testable. */
export function buildNodes(overview: NetworkOverview, links: NetworkLink[]): Node[] {
  const byName = new Map(links.map((l) => [l.name, l]))
  const nodes: Node[] = []
  const uplinks = links.filter((l) => l.uplink)
  const unfiltered = !overview.firewall.available || !overview.firewall.enabled
  const v4 = overview.publicAddresses.find((a) => !a.includes(":"))
  const anyPublic = v4 ?? overview.publicAddresses[0]

  nodes.push({
    id: "internet",
    lane: "outside",
    mark: (
      <WireMark tone={unfiltered ? "warning" : "logo"} shape="square" size="md">
        <Globe aria-hidden />
      </WireMark>
    ),
    eyebrow: uplinks.length > 0 ? `Uplink · ${uplinks.map((l) => l.name).join(", ")}` : "No uplink",
    title: "The internet",
    hint: (
      <>
        <span className="block truncate font-mono">
          {anyPublic ? anyPublic.replace(/\/\d+$/, "") : "no public address"}
          {overview.publicAddresses.length > 1 && (
            <span className="ml-1.5 font-sans text-muted-foreground">
              +{overview.publicAddresses.length - 1}
            </span>
          )}
        </span>
        <Throughput devices={uplinks} />
      </>
    ),
    devices: uplinks,
    tone: unfiltered ? "warning" : undefined,
    dashed: uplinks.length === 0,
    href: "/network/interfaces",
  })

  for (const l of links) {
    if (l.uplink || l.role !== "tunnel" || l.owner === "kernel") continue
    const tailnet = l.owner === "tailscale"
    const down = !l.adminUp
    const address = l.addresses.find((a) => a.family === "inet")?.cidr ?? l.addresses[0]?.cidr
    nodes.push({
      id: `link:${l.name}`,
      lane: "outside",
      mark: (
        <WireMark tone="logo" shape="square" size="md">
          {tailnet ? (
            <ProductGlyph id="tailscale" />
          ) : l.kind === "wireguard" ? (
            <ProductGlyph id="wireguard" />
          ) : (
            (() => {
              const Glyph = linkGlyph(l.kind)
              return <Glyph aria-hidden />
            })()
          )}
        </WireMark>
      ),
      eyebrow: tailnet ? "Tailscale" : l.kind === "wireguard" ? "WireGuard" : l.kind.toUpperCase(),
      title: tailnet ? "Tailnet" : l.remote ? `${l.name} → ${l.remote}` : l.name,
      hint: (
        <>
          <span className="block truncate font-mono">
            {down ? "down" : address ? address.replace(/\/(32|128)$/, "") : l.name}
          </span>
          {!down && <Throughput devices={[l]} />}
        </>
      ),
      devices: [l],
      dashed: down,
      href: tailnet || l.kind === "wireguard" ? "/network/vpn" : "/network/interfaces",
    })
  }
  if (!links.some((l) => l.kind === "wireguard")) {
    nodes.push({
      id: "add:vpn",
      lane: "outside",
      mark: <WirePlaceholder size="md" product="wireguard" />,
      eyebrow: "VPN",
      title: <span className="text-muted-foreground">Add WireGuard</span>,
      hint: "devices and other sites, with a QR code each",
      devices: [],
      dashed: true,
      href: "/network/vpn",
    })
  }

  for (const network of overview.dockerNetworks) {
    const bridge = network.bridge ? byName.get(network.bridge) : undefined
    if (network.containers.length === 0 && network.name === "bridge") continue
    const products = network.containers.map((c) => imageProduct(c.image))
    nodes.push({
      id: `docker:${network.id}`,
      lane: "inside",
      mark: (
        <WireMark tone="logo" shape="square" size="md">
          <ProductGlyph id="docker" />
        </WireMark>
      ),
      eyebrow: `Docker · ${network.containers.length} container${network.containers.length === 1 ? "" : "s"}`,
      title: network.name,
      hint: (
        <>
          <span className="flex min-w-0 items-center gap-2 lg:justify-start">
            <span className="truncate font-mono">{network.subnets[0] ?? network.bridge}</span>
            <ProductGlyphs ids={[...new Set(products)]} max={4} />
          </span>
          {bridge && <Throughput devices={[bridge]} />}
        </>
      ),
      devices: bridge ? [bridge] : [],
      dashed: bridge ? !bridge.adminUp : false,
      href: "/docker/networks",
    })
  }

  for (const l of links) {
    if (l.owner === "docker" || l.uplink) continue
    const inside =
      l.managed ||
      (l.role === "bridge" && l.owner !== "kernel") ||
      (l.role === "vlan" && l.owner !== "kernel") ||
      (l.role === "physical" && l.addresses.length > 0)
    if (!inside || l.master) continue
    const address = l.addresses.find((a) => a.scope === "global")?.cidr
    nodes.push({
      id: `link:${l.name}`,
      lane: "inside",
      mark: (
        <WireMark tone="logo" shape="square" size="md">
          <LinkMarkGlyph link={l} />
        </WireMark>
      ),
      eyebrow:
        l.role === "bridge"
          ? `Bridge${l.members?.length ? ` · ${l.members.length} port${l.members.length === 1 ? "" : "s"}` : ""}`
          : l.role === "vlan"
            ? `VLAN${l.vlanId ? ` ${l.vlanId}` : ""}`
            : "Network card",
      title: <span className="font-mono">{l.name}</span>,
      hint: (
        <>
          <span className="block truncate font-mono">
            {!l.adminUp ? "down" : (address ?? "no address")}
          </span>
          {l.adminUp && <Throughput devices={[l]} />}
        </>
      ),
      devices: [l],
      dashed: !l.adminUp,
      href: "/network/interfaces",
    })
  }
  if (!links.some((l) => l.managed && l.role === "bridge")) {
    nodes.push({
      id: "add:bridge",
      lane: "inside",
      mark: <WirePlaceholder size="md" fallback={Bridge} />,
      eyebrow: "Private network",
      title: <span className="text-muted-foreground">Make a bridge</span>,
      hint: "a network of its own for VMs, namespaces or a VLAN",
      devices: [],
      dashed: true,
      href: "/network/interfaces",
    })
  }
  return nodes
}

/** A device's mark as a bare glyph, for inside a `WireMark`. */
function LinkMarkGlyph({ link }: { link: NetworkLink }) {
  const Glyph = link.kind === "vxlan" || link.kind.startsWith("gre") ? Linked : linkGlyph(link.kind)
  return <Glyph aria-hidden />
}
