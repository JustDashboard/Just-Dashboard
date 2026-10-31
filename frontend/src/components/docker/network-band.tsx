"use client"

import { BarReading, type ReadingPart } from "@/components/bar-reading"
import { timeAt } from "@/components/bar-reading-model"
import { LiveBytes } from "@/components/overview/readings"
import { ProductGlyph, containerProduct } from "@/components/product-logo"
import { Cidr } from "@/components/network/address"
import { RX, TX } from "@/components/network/rate-pair"
import { ago } from "@/components/procs/units"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { plural, rate } from "@/lib/format"
import type { Container, DockerNetwork, NetworkLivePoint } from "@/lib/types"
import { cn } from "@/lib/utils"
import { networkHue, type AddressPlan, type NetworkChange } from "@/components/docker/networks"

/** How many networks the traffic block names. */
const SHOWN = 5

/** A bridge moving less than this is idle, the threshold every Network page uses. */
const MOVING = 1024

/** Recent's axis, the last day of Docker's events. */
const DAY = 86_400_000

export type NetworkRate = {
  network: DockerNetwork
  rx: number
  tx: number
  points: NetworkLivePoint[]
}

/**
 * What the networks are doing: which are carrying the traffic now, how much
 * of the address space Docker carves them from is taken, and who joined or
 * left which network, each a bar with one line under it.
 *
 * Traffic is each bridge's own counters, read every two seconds from the host
 * (`useLiveTraffic`), so a network's figure is everything its containers sent
 * each other and the world; it moves, so it draws a trail, but it arrives on a
 * poll and is not marked live. Addresses is the pool every `compose up` takes a
 * subnet from, which runs out at thirty-one, so what it says at rest is the
 * next block one would get. A press in Traffic or Recent opens the network.
 */
export function NetworkBand({
  rates,
  trafficReady,
  trafficFailed,
  plan,
  poolsFailed,
  failed,
  changes,
  networkIds,
  containers,
  containersReady,
  now,
  onOpen,
}: {
  rates: NetworkRate[]
  trafficReady: boolean
  trafficFailed: boolean
  /** Undefined until `docker info` has said which pools Docker carves from. */
  plan?: AddressPlan
  poolsFailed: boolean
  /** The list of networks could not be read, so nothing can be said about any of them. */
  failed?: boolean
  changes: NetworkChange[]
  /** The networks that exist now: a line about one removed since opens nothing. */
  networkIds: Set<string>
  containers: Map<string, Container>
  /** Whether the containers socket has answered, so an unknown id means a removed container. */
  containersReady: boolean
  now: number
  onOpen: (network: DockerNetwork | string) => void
}) {
  const busiest = rates
    .filter((r) => r.rx + r.tx >= MOVING)
    .sort((a, b) => b.rx + b.tx - (a.rx + a.tx))
    .slice(0, SHOWN)
  const rx = rates.reduce((n, r) => n + r.rx, 0)
  const tx = rates.reduce((n, r) => n + r.tx, 0)
  const shown = busiest.reduce((n, r) => n + r.rx + r.tx, 0)
  const unread = "Docker's networks could not be read."

  return (
    <div
      data-slot="network-band"
      className="grid min-w-0 gap-x-10 gap-y-6 lg:grid-cols-2 xl:grid-cols-3"
    >
      <BarReading
        title="Traffic"
        label="Traffic by network"
        list="Busiest networks"
        trail
        capacity={rx + tx}
        rest={Math.max(rx + tx - shown, 0)}
        restLabel="Other networks"
        format={rate}
        figure={
          trafficReady && (
            <span className="numeric flex h-7 items-center gap-2 font-mono text-hint">
              <span style={{ color: RX }}>
                ↓ <LiveBytes value={rx} suffix="/s" />
              </span>
              <span style={{ color: TX }}>
                ↑ <LiveBytes value={tx} suffix="/s" />
              </span>
            </span>
          )
        }
        message={
          failed ? (
            unread
          ) : trafficFailed ? (
            "The host's interface counters could not be read."
          ) : !trafficReady ? (
            <TextShimmer>Reading the bridges…</TextShimmer>
          ) : busiest.length === 0 ? (
            "Nothing is moving on Docker's bridges."
          ) : undefined
        }
        parts={busiest.map((r) => ({
          key: r.network.id,
          value: r.rx + r.tx,
          color: networkHue(r.network.name),
          mark: (
            <ProductGlyph
              id={memberProducts(r.network, containers)[0] ?? "docker"}
              className="size-3.5"
            />
          ),
          name: r.network.name,
          // On one line: the line is a fixed height, and a stacked pair fills it.
          figure: (
            <span className="flex items-center gap-2 font-mono text-hint font-normal">
              <span style={{ color: RX }}>
                ↓ <LiveBytes value={r.rx} suffix="/s" />
              </span>
              <span style={{ color: TX }}>
                ↑ <LiveBytes value={r.tx} suffix="/s" />
              </span>
            </span>
          ),
          label: `${r.network.name} ${rate(r.rx + r.tx)}`,
          press: { label: `Open ${r.network.name}`, opens: true, onPress: () => onOpen(r.network) },
        }))}
      />

      <BarReading
        title="Addresses"
        label="Address space"
        list="Networks holding blocks"
        fixed
        capacity={plan?.total}
        rest={plan ? plan.used - blocksDrawn(plan) : 0}
        restLabel="Further into the pool"
        free="Free"
        format={(blocks) => plural(blocks, "block")}
        figure={plan && <AddressFigure plan={plan} />}
        message={
          failed ? (
            unread
          ) : plan ? undefined : poolsFailed ? (
            "Docker did not say which address pools it carves networks from."
          ) : (
            <TextShimmer>Reading Docker&apos;s address pools…</TextShimmer>
          )
        }
        note={
          plan &&
          (plan.firstFree ? (
            <span className="truncate">
              First free <Cidr cidr={plan.firstFree} className="text-foreground" />
            </span>
          ) : (
            <span className="truncate text-destructive">none — a new network will fail</span>
          ))
        }
        parts={plan ? addressParts(plan) : []}
      />

      <BarReading
        title="Recent"
        label="Recent network changes"
        list="Latest network changes"
        className="lg:col-span-2 xl:col-span-1"
        ticks
        figure={
          <span className="numeric flex h-7 items-center text-hint text-muted-foreground">
            Docker&apos;s network events
          </span>
        }
        message={
          changes.length > 0
            ? undefined
            : "No container has joined or left a network since the dashboard started listening."
        }
        parts={changes.map((change) =>
          changePart(
            change,
            change.networkId !== undefined && networkIds.has(change.networkId),
            containersReady,
            now,
            onOpen,
          ),
        )}
      />
    </div>
  )
}

/** The products a network's containers run, most first, for the line after its name. */
export function memberProducts(network: DockerNetwork, containers: Map<string, Container>) {
  const ids = (network.endpoints ?? [])
    .map((e) => containers.get(e.container))
    .filter((c): c is Container => c !== undefined)
    .map(containerProduct)
  const counts = new Map<string, number>()
  for (const id of ids) counts.set(id, (counts.get(id) ?? 0) + 1)
  return [...counts.keys()].sort((a, b) => counts.get(b)! - counts.get(a)!)
}

/**
 * How many blocks taken, against the whole pool. It turns amber as the pool
 * fills and red when it is full, because a `compose up` that finds no free block
 * fails to create its network.
 */
function AddressFigure({ plan }: { plan: AddressPlan }) {
  const tone =
    plan.pressure === "full"
      ? "text-destructive"
      : plan.pressure === "warning"
        ? "text-warning"
        : "text-foreground"
  const pools = `${plan.builtin ? "Docker's built-in pools" : "default-address-pools"}: ${poolWords(plan)}`
  const outside = plan.outside.map((o) => `${o.network} ${o.cidr}`).join(", ")
  return (
    <span
      className="numeric flex h-7 items-center gap-1 text-hint text-muted-foreground"
      title={outside ? `${pools}. Outside the pools: ${outside}` : pools}
    >
      <span className={cn("font-medium", tone)}>{plan.used.toLocaleString()}</span>
      of {plural(plan.total, "block")} taken
    </span>
  )
}

/** The pools in one line: `172.17–31 in /16s · 192.168.0.0/16 in /20s`. */
function poolWords(plan: AddressPlan) {
  if (plan.builtin) return "172.17–172.31 /16s · 192.168 /20s"
  return plan.pools.map((p) => `${p.base} in /${p.size}s`).join(" · ")
}

/** The blocks the plan can name a holder for; a long pool only draws its start. */
function blocksDrawn(plan: AddressPlan) {
  return plan.blocks.filter((block) => block.network).length
}

/** One part per network holding blocks, in the order Docker hands them out. */
function addressParts(plan: AddressPlan): ReadingPart[] {
  const held = new Map<string, string[]>()
  for (const block of plan.blocks) {
    if (block.network) held.set(block.network, [...(held.get(block.network) ?? []), block.cidr])
  }
  return [...held].map(([network, cidrs]) => {
    const hue = networkHue(network)
    return {
      key: network,
      value: cidrs.length,
      color: hue,
      // A block in its network's colour, as the strip of blocks this replaced drew it.
      mark: <span aria-hidden className="size-2 rounded-sm" style={{ background: hue }} />,
      name: network,
      detail: <Cidr cidr={cidrs[0]} />,
      figure: plural(cidrs.length, "block"),
      label: `${network} ${plural(cidrs.length, "block")}`,
      title: cidrs.join(", "),
    }
  })
}

/** A network's key: its colour as the 2×10 bar a legend draws. */
export function HueKey({ color }: { color: string }) {
  return (
    <span aria-hidden className="h-2.5 w-0.5 shrink-0 rounded-full" style={{ background: color }} />
  )
}

const VERB: Record<NetworkChange["action"], string> = {
  joined: "joined",
  left: "left",
  created: "created",
  deleted: "removed",
}

/**
 * A change: who joined or left which network, or which network was made or
 * removed, and when, as a tick in the network's colour so it is found against
 * the bars and the table that carry the same one. A network that is gone since
 * opens nothing.
 */
function changePart(
  change: NetworkChange,
  exists: boolean,
  named: boolean,
  now: number,
  onOpen: (network: string) => void,
): ReadingPart {
  const hue = networkHue(change.network)
  const verb = VERB[change.action]
  const who =
    change.container ??
    (change.containerId ? (named ? "a removed container" : "a container") : undefined)
  const when = ago(change.at / 1000, now)
  return {
    key: change.key,
    value: 1,
    at: timeAt(change.at, now, DAY),
    color: hue,
    mark: (
      <span
        aria-hidden
        className={cn(
          "size-1.5 rounded-full",
          (change.action === "left" || change.action === "deleted") && "opacity-40",
        )}
        style={{ background: hue }}
      />
    ),
    name: who ? (
      <>
        <span className={cn(!change.container && "text-muted-foreground")}>{who}</span>{" "}
        <span className="font-normal text-muted-foreground">{verb}</span>{" "}
        <span style={{ color: hue }}>{change.network}</span>
      </>
    ) : (
      <>
        <span style={{ color: hue }}>{change.network}</span>{" "}
        <span className="font-normal text-muted-foreground">{verb}</span>
      </>
    ),
    figure: <span className="text-hint font-normal text-muted-foreground">{when}</span>,
    label: `${who ? `${who} ${verb} ${change.network}` : `${change.network} ${verb}`} ${when}`,
    title: new Date(change.at).toLocaleString(),
    press: exists
      ? {
          label: `Open ${change.network}`,
          opens: true,
          onPress: () => onOpen(change.networkId!),
        }
      : undefined,
  }
}
