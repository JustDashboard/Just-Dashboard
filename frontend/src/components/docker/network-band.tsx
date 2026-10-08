"use client"

import { ArrowRight } from "@/components/icons"
import { rowReveal } from "@/components/icon-action"
import { LiveBytes } from "@/components/overview/readings"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ProductGlyphs, containerProduct } from "@/components/product-logo"
import { Cidr } from "@/components/network/address"
import { RX, RatePair, TX } from "@/components/network/rate-pair"
import { ShareBar } from "@/components/procs/workloads"
import { ago } from "@/components/procs/units"
import { plural, rate } from "@/lib/format"
import type { Container, DockerNetwork, NetworkLivePoint } from "@/lib/types"
import { cn } from "@/lib/utils"
import { networkHue, type AddressPlan, type NetworkChange } from "@/components/docker/networks"

/** How many networks the traffic block names. */
const SHOWN = 5

/** A bridge moving less than this is idle, the threshold every Network page uses. */
const MOVING = 1024

export type NetworkRate = {
  network: DockerNetwork
  rx: number
  tx: number
  points: NetworkLivePoint[]
}

/**
 * What the networks are doing: which are carrying the traffic now, how much
 * of the address space Docker carves them from is taken, and who joined or
 * left which network.
 *
 * The page had none of it — a column of cards, each a name, a subnet and a
 * count — so nothing on it moved and nothing said which network was the busy
 * one, or that the pool every `compose up` takes a subnet from runs out at
 * thirty-one. Traffic is each bridge's own counters, read every two seconds
 * from the host (`useLiveTraffic`), so a network's figure is everything its
 * containers sent each other and the world; each span eases to the next
 * reading and each figure glides to it. A row opens its network.
 */
export function NetworkBand({
  rates,
  trafficReady,
  trafficFailed,
  plan,
  changes,
  containers,
  now,
  onOpen,
}: {
  rates: NetworkRate[]
  trafficReady: boolean
  trafficFailed: boolean
  plan: AddressPlan
  changes: NetworkChange[]
  containers: Map<string, Container>
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

  return (
    <div
      data-slot="network-band"
      className="grid min-w-0 gap-x-10 gap-y-6 lg:grid-cols-2 xl:grid-cols-3"
    >
      <Panel plain aria-label="Traffic by network">
        <PanelHeader
          title="Traffic"
          actions={
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
        />
        <PanelBody className="space-y-3 pt-4">
          <ShareBar
            label="Traffic"
            capacity={rx + tx}
            rest={Math.max(rx + tx - shown, 0)}
            parts={busiest.map((r) => ({
              key: r.network.id,
              value: r.rx + r.tx,
              color: networkHue(r.network.name),
              label: `${r.network.name} ${rate(r.rx + r.tx)}`,
            }))}
            format={rate}
          />
          {trafficFailed ? (
            <p className="py-2 text-body text-muted-foreground">
              The host&apos;s interface counters could not be read.
            </p>
          ) : !trafficReady ? (
            <p className="py-2 text-body text-muted-foreground">Reading the bridges…</p>
          ) : busiest.length === 0 ? (
            <p className="py-2 text-body text-muted-foreground">
              Nothing is moving on Docker&apos;s bridges.
            </p>
          ) : (
            <ul className="-mx-2">
              {busiest.map((r) => (
                <BandLine
                  key={r.network.id}
                  label={`Open ${r.network.name}`}
                  onOpen={() => onOpen(r.network)}
                  lead={<HueKey color={networkHue(r.network.name)} />}
                  name={r.network.name}
                  detail={<ProductGlyphs max={3} ids={memberProducts(r.network, containers)} />}
                  figure={<RatePair rx={r.rx} tx={r.tx} />}
                />
              ))}
            </ul>
          )}
        </PanelBody>
      </Panel>

      <AddressBlock plan={plan} />

      <Panel plain aria-label="Recent network changes" className="lg:col-span-2 xl:col-span-1">
        <PanelHeader
          title="Recent"
          actions={
            <span className="numeric flex h-7 items-center text-hint text-muted-foreground">
              Docker&apos;s network events
            </span>
          }
        />
        <PanelBody className="pt-3">
          {changes.length === 0 ? (
            <p className="py-2 text-body text-muted-foreground">
              No container has joined or left a network since the dashboard started listening.
            </p>
          ) : (
            <ul className="-mx-2">
              {changes.map((change) => (
                <ChangeLine key={change.key} change={change} now={now} onOpen={onOpen} />
              ))}
            </ul>
          )}
        </PanelBody>
      </Panel>
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
 * The address space as its blocks, in the order Docker hands them out: each
 * block a network holds in that network's colour, each free one the meter's
 * track. A `compose up` that finds no free block fails to create its network,
 * so the figure in the head turns amber as the strip fills and red when it is
 * full.
 */
function AddressBlock({ plan }: { plan: AddressPlan }) {
  const tone =
    plan.pressure === "full"
      ? "text-destructive"
      : plan.pressure === "warning"
        ? "text-warning"
        : "text-foreground"
  return (
    <Panel plain aria-label="Address space">
      <PanelHeader
        title="Addresses"
        actions={
          <span className="numeric flex h-7 items-center gap-1 text-hint text-muted-foreground">
            <span className={cn("font-medium", tone)}>{plan.used.toLocaleString()}</span>
            of {plural(plan.total, "block")} taken
          </span>
        }
      />
      <PanelBody className="space-y-3 pt-4">
        <div
          role="img"
          aria-label={`${plan.used} of ${plan.total} address blocks taken${plan.firstFree ? `; the first free is ${plan.firstFree}` : ""}`}
          className="grid grid-cols-16 gap-0.5"
        >
          {plan.blocks.map((block) => (
            <span
              key={block.cidr}
              title={`${block.cidr} · ${block.network ?? "free"}`}
              className={cn(
                "h-3.5 rounded-sm transition-colors duration-700",
                !block.network && "bg-meter-track",
              )}
              style={block.network ? { background: networkHue(block.network) } : undefined}
            />
          ))}
        </div>
        <dl className="space-y-1.5 text-hint">
          <div className="flex min-w-0 items-baseline justify-between gap-3">
            <dt className="text-muted-foreground">First free</dt>
            <dd className="min-w-0 truncate">
              {plan.firstFree ? (
                <Cidr cidr={plan.firstFree} />
              ) : (
                <span className="text-destructive">none — a new network will fail</span>
              )}
            </dd>
          </div>
          <div className="flex min-w-0 items-baseline justify-between gap-3">
            <dt className="shrink-0 text-muted-foreground">
              {plan.builtin ? "Docker's built-in pools" : "default-address-pools"}
            </dt>
            <dd className="min-w-0 truncate text-right font-mono text-muted-foreground">
              {poolWords(plan)}
            </dd>
          </div>
          {plan.outside.length > 0 && (
            <div className="flex min-w-0 items-baseline justify-between gap-3">
              <dt className="shrink-0 text-muted-foreground">Outside the pools</dt>
              <dd className="flex min-w-0 flex-wrap justify-end gap-x-3 truncate">
                {plan.outside.map((o) => (
                  <span key={o.cidr} className="inline-flex items-center gap-1.5">
                    <HueKey color={networkHue(o.network)} />
                    <Cidr cidr={o.cidr} />
                  </span>
                ))}
              </dd>
            </div>
          )}
          {plan.total > plan.blocks.length && (
            <p className="text-muted-foreground">
              The first {plan.blocks.length} of {plan.total.toLocaleString()}, where Docker starts.
            </p>
          )}
        </dl>
      </PanelBody>
    </Panel>
  )
}

/** The pools in one line: `172.17–31 in /16s · 192.168.0.0/16 in /20s`. */
function poolWords(plan: AddressPlan) {
  if (plan.builtin) return "172.17–172.31 /16s · 192.168 /20s"
  return plan.pools.map((p) => `${p.base} in /${p.size}s`).join(" · ")
}

/** A network's key: its colour as the 2×10 bar a legend draws. */
export function HueKey({ color }: { color: string }) {
  return (
    <span aria-hidden className="h-2.5 w-0.5 shrink-0 rounded-full" style={{ background: color }} />
  )
}

/**
 * One line of the band: what finds it on the bar, its name, a detail and its
 * figure. A press opens the network; the arrow says so under the pointer.
 */
function BandLine({
  label,
  lead,
  name,
  detail,
  figure,
  title,
  onOpen,
}: {
  label: string
  lead: React.ReactNode
  name: React.ReactNode
  detail?: React.ReactNode
  figure: React.ReactNode
  title?: string
  onOpen?: () => void
}) {
  const body = (
    <>
      {lead}
      <span className="min-w-0 truncate">{name}</span>
      {detail && <span className="flex shrink-0 items-center">{detail}</span>}
      <span className="numeric ml-auto shrink-0 text-foreground">{figure}</span>
      {onOpen && (
        <ArrowRight
          aria-hidden
          className={cn("size-3.5 shrink-0 text-muted-foreground", rowReveal())}
        />
      )}
    </>
  )
  const line =
    "group flex min-h-9 w-full min-w-0 items-center gap-2.5 rounded-md px-2 py-1 text-left text-body"
  return (
    <li>
      {onOpen ? (
        <button
          type="button"
          aria-label={label}
          title={title}
          onClick={onOpen}
          className={cn(line, "focus-ring-inset transition-colors hover:bg-row-hover")}
        >
          {body}
        </button>
      ) : (
        <div className={line} title={title}>
          {body}
        </div>
      )}
    </li>
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
 * removed, and when. The network is named in its colour, so the line is
 * found against the row and the blocks that carry the same one. A network
 * that is gone since opens nothing.
 */
function ChangeLine({
  change,
  now,
  onOpen,
}: {
  change: NetworkChange
  now: number
  onOpen: (network: string) => void
}) {
  const hue = networkHue(change.network)
  const exists = change.action !== "deleted" && change.networkId !== undefined
  const network = (
    <span className="font-medium" style={{ color: hue }}>
      {change.network}
    </span>
  )
  const who = change.container ?? (change.containerId ? "a removed container" : undefined)
  return (
    <BandLine
      label={`Open ${change.network}`}
      onOpen={exists ? () => onOpen(change.networkId!) : undefined}
      title={new Date(change.at).toLocaleString()}
      lead={
        <span
          aria-hidden
          className={cn(
            "mx-px size-1.5 shrink-0 rounded-full",
            change.action === "left" || change.action === "deleted" ? "opacity-40" : "",
          )}
          style={{ background: hue }}
        />
      }
      name={
        who ? (
          <>
            <span className={cn("font-medium", !change.container && "text-muted-foreground")}>
              {who}
            </span>{" "}
            <span className="text-muted-foreground">{VERB[change.action]}</span> {network}
          </>
        ) : (
          <>
            {network} <span className="text-muted-foreground">{VERB[change.action]}</span>
          </>
        )
      }
      figure={<span className="text-hint text-muted-foreground">{ago(change.at / 1000, now)}</span>}
    />
  )
}
