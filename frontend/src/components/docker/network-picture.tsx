"use client"

import { NetworkDevice, Slash } from "@/components/icons"
import { plural, rate } from "@/lib/format"
import type { Container, DockerNetwork } from "@/lib/types"
import { LogoGlyph } from "@/components/logo"
import { ProductGlyph, containerProduct, hasProductLogo } from "@/components/product-logo"
import { WireHost, WireMark } from "@/components/deploy/wire"
import { LinkGlyph, pulseDuration } from "@/components/network/marks"
import { RX, TX } from "@/components/network/rate-pair"
import { WireFlow, type WireFlowNode } from "@/components/wire-flow"
import type { ContainerTraffic } from "@/components/docker/network-members"
import {
  bareAddress,
  joinedNetworks,
  networkHref,
  networkHue,
  wayOut,
  type MemberReading,
} from "@/components/docker/networks"
import { deviceHref } from "@/components/network/interfaces/device-facts"

/** A wire moving less than this is drawn still, as on every Network picture. */
const MOVING = 1024

/** The hub's share of the width: a mark and three short lines, so the members keep theirs. */
const HUB_SHARE = 0.4

/** A side draws this many and counts the rest; the Members view lists every one. */
const MEMBERS = 5
const LEADS = 3

/**
 * A network drawn as what it is: where it leads on the left, the network in
 * the middle and each member on the right as the product it runs.
 *
 * Where it leads is how its members leave it — through this server's address
 * on its bridge, straight onto a network card for a macvlan, nowhere for an
 * internal one — and then the other networks its members are also on, since a
 * container on two networks is the only thing that joins them: "can the proxy
 * reach the database" is whether a line runs from this network to that one,
 * and through whom. Each of those opens that network.
 *
 * A member's wire pulses while that container moves more than a kilobyte a
 * second, faster the busier, running toward the container for what it
 * receives; a stopped member's is dashed: it is on the network and holds no
 * address. Pointing at a member lights the networks it joins this one to.
 *
 * It was the sheet's picture, the bridge on the left wired to its members; the
 * left of that is the middle of this, and "also on" was a third line under a
 * member's row.
 */
export function NetworkPicture({
  network,
  members,
  options,
  host,
  traffic,
  containers,
  tab,
  loading,
  expected,
  onMembers,
}: {
  /** The network as the inspect or the listing has it; undefined while neither has landed. */
  network?: DockerNetwork
  members: MemberReading[]
  /** The inspect's driver options: a macvlan's parent is one. */
  options?: Record<string, string>
  /** This server's name, for the node it is. */
  host?: string
  traffic: Map<string, ContainerTraffic>
  containers: Map<string, Container>
  /** The view on screen, carried to the network a node opens. */
  tab: string
  loading: boolean
  /** How many members to hold room for while waiting; three where nothing says. */
  expected?: number
  onMembers: () => void
}) {
  if (loading || !network) {
    return (
      <WireFlow
        loading
        start={{ title: "Leads to", nodes: [], loadingRows: 1 }}
        hub={{ mark: <HubMark driver="bridge" />, eyebrow: "Network" }}
        hubShare={HUB_SHARE}
        end={{
          title: "Members",
          nodes: [],
          loadingRows: Math.min(Math.max(expected ?? 3, 1), MEMBERS + 1),
        }}
      />
    )
  }

  const way = wayOut({ ...network, options })
  const joined = joinedNetworks(members)
  const byName = new Map(members.map((member) => [member.name, member]))

  const leads: WireFlowNode[] = []
  if (way.kind === "gateway") {
    leads.push({
      id: "way",
      mark: (size) => <WireHost size={size} />,
      eyebrow: "Out through",
      title: host || "This server",
      name: host || "This server",
      hint: way.gateway ? (
        <span className="font-mono">{way.gateway}</span>
      ) : (
        "Docker's own address translation"
      ),
      line: { state: "up" },
    })
  } else if (way.kind === "parent") {
    leads.push({
      id: "way",
      mark: (size) => (
        <WireMark tone="logo" shape="square" size={size}>
          <LinkGlyph kind="ether" />
        </WireMark>
      ),
      eyebrow: "Straight onto",
      title: way.device ? <span className="font-mono">{way.device}</span> : "A network card",
      name: way.device ?? "A network card",
      hint: way.gateway ? (
        <span className="font-mono">gateway {way.gateway}</span>
      ) : (
        "the card's own network"
      ),
      href: way.device ? deviceHref(way.device) : undefined,
      label: way.device ? `Open ${way.device} on Interfaces` : undefined,
      line: { state: "up" },
    })
  } else if (way.kind === "internal") {
    leads.push({
      id: "way",
      mark: (size) => (
        <WireMark shape="square" size={size}>
          <Slash aria-hidden className="text-muted-foreground" />
        </WireMark>
      ),
      eyebrow: "No way out",
      title: <span className="text-muted-foreground">Cut off</span>,
      name: "Cut off from the internet",
      hint: "its members reach only each other",
      line: { state: "idle" },
    })
  }
  for (const other of joined) {
    const through = other.through
    leads.push({
      id: `joined:${other.name}`,
      mark: (size) => (
        <WireMark hue={networkHue(other.name)} shape="square" size={size}>
          <NetworkDevice aria-hidden />
        </WireMark>
      ),
      eyebrow: "Joined to",
      title: other.name,
      name: other.name,
      hint: `through ${through[0]}${through.length > 1 ? ` and ${through.length - 1} more` : ""}`,
      href: networkHref(other.name, tab),
      label: `Open ${other.name}`,
      line: { state: other.up ? "up" : "down" },
    })
  }

  const nodes: WireFlowNode[] = members.map((member) => {
    const container = containers.get(member.id)
    const product = container ? containerProduct(container) : undefined
    const reading = traffic.get(member.id)
    const running = member.state === "running"
    const moving = running && reading !== undefined && reading.rx + reading.tx >= MOVING
    return {
      id: `member:${member.id}`,
      mark: (size) => (
        <WireMark tone="logo" shape="square" size={size} className={running ? "" : "opacity-60"}>
          {member.dashboard ? (
            <LogoGlyph small className="text-brand" />
          ) : product && hasProductLogo(product) ? (
            <ProductGlyph id={product} />
          ) : (
            <ProductGlyph id="docker" />
          )}
        </WireMark>
      ),
      title: <span className="block truncate">{member.name}</span>,
      name: member.name,
      // Two lines, the second kept whether or not there is a rate to say,
      // so a member's first reading does not make the picture taller.
      hint: (
        <>
          <span className="block truncate font-mono">
            {bareAddress(member.ipv4 ?? member.ipv6) ??
              (running
                ? network.driver === "host"
                  ? "the server's addresses"
                  : "no address"
                : "stopped")}
          </span>
          <span className="numeric block truncate font-mono text-micro">
            {reading && running ? (
              <>
                <span style={{ color: RX }}>↓ {rate(reading.rx)}</span>{" "}
                <span style={{ color: TX }}>↑ {rate(reading.tx)}</span>
              </>
            ) : (
              "\u00a0"
            )}
          </span>
        </>
      ),
      href: `/docker/containers/${encodeURIComponent(member.id)}`,
      label: `Open ${member.name}`,
      line: running
        ? {
            state: "up",
            moving,
            // The pulse runs toward the container for what it receives.
            reverse: reading !== undefined && reading.tx > reading.rx,
            duration: pulseDuration(reading ? reading.rx + reading.tx : 0),
          }
        : { state: "down" },
    }
  })

  // A bridge is named for its device on this server; a network on a card's
  // own network has none of its own, and the card is the node beside it.
  const device = network.bridge
  return (
    <WireFlow
      start={{
        title: "Leads to",
        nodes: leads,
        max: LEADS,
        more: { words: (hidden) => `${plural(hidden, "more network")}` },
        empty:
          way.kind === "host"
            ? "Its containers are on the server's own interfaces."
            : way.kind === "none"
              ? "No interface at all: nothing in, nothing out."
              : undefined,
      }}
      hub={{
        mark: way.kind === "host" ? <WireHost size="lg" /> : <HubMark driver={network.driver} />,
        eyebrow:
          way.kind === "host"
            ? "This server"
            : way.kind === "none"
              ? "No network"
              : network.driver === "bridge"
                ? "Bridge"
                : network.driver,
        title: device ? (
          <span className="font-mono">{device}</span>
        ) : way.kind === "host" ? (
          host || network.name
        ) : (
          network.name
        ),
        // The line is held open for a network with no subnet of its own: the
        // picture keeps room for the hub's words, and what stands under it
        // must not move when such a network lands.
        hint: network.subnets[0] ? (
          <span className="font-mono">{network.subnets[0]}</span>
        ) : (
          <span aria-hidden>&nbsp;</span>
        ),
      }}
      hubShare={HUB_SHARE}
      end={{
        title: "Members",
        nodes,
        max: MEMBERS,
        more: { onPress: onMembers, words: (hidden) => `${hidden} more` },
        empty: "Nothing is attached.",
      }}
      related={(focus, id) => {
        const [kind, key] = split(focus)
        const [otherKind, otherKey] = split(id)
        if (kind === "joined" && otherKind === "member") {
          return (
            joined
              .find((j) => j.name === key)
              ?.through.some((n) => byName.get(n)?.id === otherKey) ?? false
          )
        }
        if (kind === "member" && otherKind === "joined") {
          return members.find((m) => m.id === key)?.others.includes(otherKey) ?? false
        }
        return false
      }}
    />
  )
}

function split(id: string): [string, string] {
  const at = id.indexOf(":")
  return at < 0 ? [id, ""] : [id.slice(0, at), id.slice(at + 1)]
}

/** The network's own mark: Docker's for a bridge or an overlay, the kind of device otherwise. */
function HubMark({ driver }: { driver: string }) {
  return (
    <WireMark tone="logo" shape="square" size="lg">
      {driver === "bridge" || driver === "overlay" ? (
        <ProductGlyph id="docker" />
      ) : (
        <LinkGlyph kind={driver} />
      )}
    </WireMark>
  )
}
