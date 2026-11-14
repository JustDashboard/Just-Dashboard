"use client"

import { useAuth } from "@/hooks/use-auth"
import { useMemo } from "react"
import { Bridge, Globe, Plus, Router, Servers, type Icon } from "@/components/icons"
import type { GatewayForward, GatewayNAT, GatewayView, NetworkLink } from "@/lib/types"
import { cn } from "@/lib/utils"
import { ProductGlyph } from "@/components/product-logo"
import { WireHost, WireMark, WirePlaceholder } from "@/components/deploy/wire"
import { WireFlow, type WireFlowNode, type WireLine } from "@/components/wire-flow"
import { staggerOf } from "@/components/wire-flow-model"
import { Cidr } from "@/components/network/address"
import { LinkGlyph, linkProduct } from "@/components/network/marks"
import { ArrivalMark, Endpoint, Port, TargetMark } from "@/components/network/gateway/marks"
import {
  addressWord,
  forwardTitle,
  natMode,
  natWord,
  ownerOf,
  PROTOCOL_WORD,
  sourcesWord,
  targetPortOf,
} from "@/components/network/gateway/reading"

/** How many of each kind are drawn; the lists below hold the rest. */
const FORWARDS_DRAWN = 5
const NAT_DRAWN = 3

type Node = WireFlowNode & { side: "in" | "out" }

/**
 * Room over and under this server's mark for the lines that hang under it:
 * with nothing made the picture is two rows, and they ran into the list below.
 */
const HUB_ROOM = "lg:py-20"

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
 * what would be there. Three lanes from `lg` (`WireFlow`); below it they stack
 * and the wires are not drawn.
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
  const { can } = useAuth()
  const writable = can("system.admin") && view.capability.writable

  const nodes = useMemo(
    () => buildNodes(view, links, moved, { onOpenForward, onOpenNAT }),
    [view, links, moved, onOpenForward, onOpenNAT],
  )
  const arrive: WireFlowNode[] = nodes.filter((n) => n.side === "in")
  const leave: WireFlowNode[] = nodes.filter((n) => n.side === "out")
  // A kind with nothing made yet: a dashed ring on each side, wired dashed
  // and still, so the lanes read as complete. The one on the left opens the
  // form that makes one; the one on the right only says what would be there.
  if (view.forwards.length === 0) {
    arrive.push(
      placeholder(
        "add:forward",
        "Forward a port",
        "a public port to a container or a machine",
        Globe,
        writable ? onForward : undefined,
      ),
    )
    leave.push(
      placeholder(
        "sink:forward",
        "Its target",
        "an address and a port, drawn as what answers there",
        Servers,
      ),
    )
  }
  if (view.nat.length === 0) {
    arrive.push(
      placeholder(
        "add:nat",
        "Share a network out",
        "a private network, through this server's address",
        Bridge,
        writable ? onShare : undefined,
      ),
    )
    leave.push(
      placeholder("sink:nat", "The way out", "the device its traffic leaves through", Router),
    )
  }
  const { forwarding, capability } = view

  return (
    <WireFlow
      className="animate-rise"
      start={{ title: "Arrives", nodes: arrive }}
      end={{ title: "Goes to", nodes: leave }}
      hub={{
        className: HUB_ROOM,
        mark: <WireHost />,
        eyebrow: "This server",
        title: FIREWALL[capability.firewall],
        hint: (
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
        ),
      }}
    />
  )
}

/** The picture before the gateway has answered: this server and grey rows where its entries will be. */
export function GatewayPictureLoading({ rows = 4 }: { rows?: number }) {
  return (
    <WireFlow
      loading
      start={{ title: "Arrives", nodes: [], loadingRows: rows }}
      end={{ title: "Goes to", nodes: [], loadingRows: rows }}
      hub={{ className: HUB_ROOM, mark: <WireHost />, eyebrow: "This server" }}
    />
  )
}

/**
 * A dashed ring where an entry would go. With `onPress` it is the button that
 * opens the form that makes one; without, it is only the other end of the wire
 * the form would draw, so the picture says what each side will hold.
 */
function placeholder(
  id: string,
  label: string,
  hint: string,
  fallback: Icon,
  onPress?: () => void,
): WireFlowNode {
  return {
    id,
    placeholder: true,
    mark: (
      <WirePlaceholder size="md" fallback={fallback}>
        <Plus aria-hidden />
      </WirePlaceholder>
    ),
    title: <span className="text-muted-foreground">{label}</span>,
    name: label,
    hint,
    onPress,
    line: { state: "idle" },
  }
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
  // A wire carries when its entry's counter grew since the last read, and is
  // a promise while the entry is off or not in the kernel. Its pulse waits by
  // its place in the order the entries are read, not by its column's.
  const wire =
    (moving: boolean, dashed: boolean) =>
    (index: number): WireLine => ({
      state: "up",
      moving,
      dashed,
      duration: moving ? 2.2 : 3,
      delay: staggerOf(index),
    })

  for (const f of view.forwards.slice(0, FORWARDS_DRAWN)) {
    const line = wire(inForce(f.enabled) && moved.has(`forward:${f.id}`), !inForce(f.enabled))
    nodes.push({
      id: `forward:${f.id}:in`,
      side: "in",
      mark: <ArrivalMark device={f.interface} links={links} />,
      onPress: () => open.onOpenForward(f),
      label: `Open ${forwardTitle(f)}`,
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
      line: line(nodes.length),
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
      line: line(nodes.length),
    })
  }

  for (const n of view.nat.slice(0, NAT_DRAWN)) {
    const line = wire(inForce(n.enabled) && moved.has(`nat:${n.id}`), !inForce(n.enabled))
    const owner = n.owner ? ownerOf(n.owner) : undefined
    const exit = links.find((l) => l.name === n.interface)
    const exitProduct = exit ? linkProduct(exit) : undefined
    nodes.push({
      id: `nat:${n.id}:in`,
      side: "in",
      mark: (
        <WireMark tone="logo" shape="square" size="md">
          {owner?.product === "wireguard" || owner?.product === "tailscale" ? (
            <ProductGlyph id={owner.product} />
          ) : (
            <Bridge aria-hidden />
          )}
        </WireMark>
      ),
      // A NAT entry another feature made is that feature's: it opens there.
      ...(owner
        ? { href: "/network/vpn", label: `${n.name}, made by ${owner.name}` }
        : { onPress: () => open.onOpenNAT(n), label: `Open ${n.name}` }),
      eyebrow: owner ? `NAT · ${owner.name}` : "NAT",
      title: <Cidr cidr={n.source} className={cn(!n.enabled && "text-muted-foreground")} />,
      hint: <span className="block truncate">{n.enabled ? n.name : "switched off"}</span>,
      line: line(nodes.length),
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
      hint:
        natMode(n) === "masquerade" && !(n.destinations ?? []).length ? (
          "masqueraded"
        ) : (
          <span className="block truncate font-mono">{natWord(n)}</span>
        ),
      line: line(nodes.length),
    })
  }

  return nodes
}
