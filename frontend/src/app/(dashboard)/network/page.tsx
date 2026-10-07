"use client"

import { useMemo } from "react"
import { useRouter } from "next/navigation"
import { Connection, LockClosed, ShieldCheck, Topology } from "@/components/icons"
import { get } from "@/lib/api"
import { bytes, plural, rate } from "@/lib/format"
import type { NetworkLink, NetworkOverview } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { Page, PageContext, Section } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { ErrorState, LoadingPanel } from "@/components/state"
import { Status } from "@/components/status-dot"
import { FindingList } from "@/components/finding-list"
import { NumberTicker } from "@/components/ui/number-ticker"
import { TileTrend } from "@/components/metrics/sparkline"
import { HostIdentity, HostFact, FactDot } from "@/components/metrics/host-identity"
import { ChartPanel } from "@/components/metrics/chart-panel"
import { NetworkFact } from "@/components/client-mark"
import { ProductGlyphs } from "@/components/product-logo"
import { LiveBytes, SeriesKey, StreamState } from "@/components/overview/readings"
import { Topology as TopologyPicture } from "@/components/network/topology"
import { linkProduct } from "@/components/network/marks"
import {
  lastValues,
  sumSeries,
  useLiveTraffic,
  type LiveTraffic,
} from "@/components/network/use-live-traffic"

const IN = "var(--chart-5)"
const OUT = "var(--chart-2)"
const TUNNELS = "var(--chart-4)"
const CONTAINERS = "var(--chart-3)"

const SERIES = [
  { key: "rx", label: "In", color: IN, kind: "area" as const },
  { key: "tx", label: "Out", color: OUT, kind: "area" as const },
  { key: "tunnels", label: "Tunnels", color: TUNNELS, kind: "line" as const },
  { key: "containers", label: "Containers", color: CONTAINERS, kind: "line" as const },
]

const formatRate = (value: number) => rate(value)
const axisRate = (value: number) => `${bytes(value, 0)}/s`

/**
 * Is the network doing what it should, and what is it carrying right now?
 *
 * The page answers in the host Overview's order: what the machine is on the
 * network (the address the internet reaches it at, its uplink and gateway,
 * and how this browser reaches it — the path every guard on these pages
 * protects — with the verdict at the line's end); the topology, which is the
 * page (the internet, the tailnet and every tunnel on the left, this server
 * in the middle, the networks it is the gateway of on the right, each wire
 * moving with its device's bytes); the readings that move, each live and with
 * its last fifteen minutes; that quarter of an hour as a chart; and what
 * needs attention, worst first, each opening the page that fixes it.
 *
 * The overview is read every fifteen seconds and the throughput every two, so
 * the wires and figures move between reads of the rest: the devices are drawn
 * with the live ring's newest rates over the overview's own.
 */
export default function NetworkOverviewPage() {
  const router = useRouter()
  const overview = usePoll<NetworkOverview>(
    (signal) => get("/network/overview", undefined, signal),
    15_000,
  )
  const live = useLiveTraffic()

  const links = useMemo(
    () => withLiveRates(overview.data?.links ?? [], live),
    [overview.data, live],
  )

  if (!overview.data) {
    return (
      <Page className="animate-rise">
        <PageContext eyebrow="Network" title="Network" />
        {overview.error ? (
          <ErrorState error={overview.error} onRetry={overview.refresh} />
        ) : (
          <LoadingPanel />
        )}
      </Page>
    )
  }
  const data = overview.data
  return (
    <Page className="animate-rise">
      <PageContext eyebrow="Network" title="Network" />
      <NetworkIdentity overview={data} links={links} />

      <Panel plain>
        <PanelHeader
          title="Topology"
          actions={
            <span className="text-hint text-muted-foreground">
              {plural(
                links.filter((l) => l.owner !== "kernel" && l.role !== "container").length,
                "device",
              )}
              {" · "}
              {plural(data.dockerNetworks.length, "Docker network")}
            </span>
          }
        />
        <PanelBody>
          <TopologyPicture overview={data} links={links} />
        </PanelBody>
      </Panel>

      <Readings overview={data} links={links} live={live} />

      <TrafficChart links={links} live={live} />

      <Panel plain>
        <PanelHeader
          title="Attention"
          actions={
            data.findings.length > 0 ? (
              <span className="text-hint text-muted-foreground">
                {plural(data.findings.length, "finding")}
              </span>
            ) : undefined
          }
        />
        <PanelBody>
          <FindingList
            findings={data.findings.map((f) => ({
              id: f.id,
              level: f.level,
              title: f.title,
              detail: f.detail,
              action: { label: "Open", onClick: () => router.push(f.href) },
            }))}
            emptyLabel="Nothing on the network needs attention"
          />
        </PanelBody>
      </Panel>
    </Page>
  )
}

/** Each device with the live ring's newest reading, where the ring has one. */
function withLiveRates(links: NetworkLink[], live: LiveTraffic): NetworkLink[] {
  return links.map((link) => {
    const last = live.series[link.name]?.at(-1)
    return last ? { ...link, rxRate: last.rx, txRate: last.tx } : link
  })
}

/**
 * What the machine is on the network, as the host Overview's identity line:
 * the address the internet reaches it at as the name, its uplink, gateway and
 * second family as facts, and how this browser reaches it last, because that
 * is the path every guard on these pages protects.
 */
function NetworkIdentity({ overview, links }: { overview: NetworkOverview; links: NetworkLink[] }) {
  const v4 = overview.publicAddresses.find((a) => !a.includes(":"))
  const v6 = overview.publicAddresses.find((a) => a.includes(":"))
  const primary = overview.defaults[0]
  const uplink = links.find((l) => l.name === primary?.device)
  const worst = overview.findings[0]?.level
  return (
    <HostIdentity
      fallback={Topology}
      title={
        <span className="font-mono">
          {(v4 ?? v6)?.replace(/\/\d+$/, "") ?? overview.hostname ?? "No public address"}
        </span>
      }
      facts={
        <>
          {primary ? (
            <HostFact>
              <span className="font-mono">{primary.device}</span>
              {uplink?.speedMbps ? (
                <span className="ml-1.5">· {speed(uplink.speedMbps)}</span>
              ) : null}
              {primary.gateway && (
                <span className="ml-1.5">
                  via <span className="font-mono">{primary.gateway}</span>
                </span>
              )}
            </HostFact>
          ) : (
            <HostFact>no default route</HostFact>
          )}
          {v4 && v6 && (
            <>
              <FactDot />
              <HostFact>
                <span className="font-mono">{v6.replace(/\/\d+$/, "")}</span>
              </HostFact>
            </>
          )}
          <FactDot />
          <HostFact>{overview.forwarding.ipv4 ? "routing" : "not routing"}</HostFact>
          {overview.persistence.made > 0 && (
            <>
              <FactDot />
              <HostFact>
                <span
                  className={overview.persistence.unit === "enabled" ? undefined : "text-warning"}
                >
                  {plural(overview.persistence.made, "change")}{" "}
                  {overview.persistence.unit === "enabled"
                    ? "kept for boot"
                    : "not restored at boot"}
                </span>
              </HostFact>
            </>
          )}
          {overview.client.address && (
            <>
              <FactDot />
              <span className="inline-flex min-w-0 items-center gap-1.5">
                <span>you</span>
                <NetworkFact ip={overview.client.address} glyph />
                {overview.client.device && !overview.client.local && (
                  <span className="text-muted-foreground">
                    through <span className="font-mono">{overview.client.device}</span>
                  </span>
                )}
              </span>
            </>
          )}
        </>
      }
      aside={
        worst ? (
          <Status
            tone={worst === "critical" ? "danger" : worst === "warning" ? "warning" : "notice"}
            label={worst === "notice" ? "Worth a look" : "Needs attention"}
            className="text-body"
          />
        ) : (
          <Status tone="running" label="All clear" className="text-body" />
        )
      }
    />
  )
}

function speed(mbps: number) {
  return mbps >= 1000 ? `${mbps / 1000} Gb/s` : `${mbps} Mb/s`
}

/**
 * The five readings that move: in and out through the uplink, each live and
 * with its last fifteen minutes as its trend; the connections, the VPN peers
 * online across WireGuard and the tailnet, and what the gateway's blocklists
 * and limits have refused since the table was loaded. What the dashboard has
 * made and whether it comes back at boot is a fact in the identity line.
 */
function Readings({
  overview,
  links,
  live,
}: {
  overview: NetworkOverview
  links: NetworkLink[]
  live: LiveTraffic
}) {
  const uplinks = links.filter((l) => l.uplink)
  const uplinkSeries = sumSeries(uplinks.map((l) => live.series[l.name] ?? []))
  const rx = uplinks.reduce((n, l) => n + l.rxRate, 0)
  const tx = uplinks.reduce((n, l) => n + l.txRate, 0)
  const tunnels = links.filter((l) => l.role === "tunnel" && l.owner !== "kernel")
  const tunnelsUp = tunnels.filter((l) => l.adminUp)
  const vpnPeers = overview.vpn.wireguard.peers + overview.vpn.tailscale.peers
  const vpnOnline = overview.vpn.wireguard.online + overview.vpn.tailscale.online
  return (
    <Section
      title="Throughput"
      actions={
        live.now > 0 ? <StreamState connection={live.error ? "closed" : "open"} /> : undefined
      }
    >
      <StatGrid columns={5}>
        <StatTile
          label={
            <>
              <SeriesKey color={IN} />
              In
            </>
          }
          value={<LiveBytes value={rx} suffix="/s" />}
          trend={
            <TileTrend
              values={lastValues(uplinkSeries, "rx")}
              color={IN}
              label="Received through the uplink over the last fifteen minutes"
            />
          }
          hint={`from the internet · ${uplinks.map((l) => l.name).join(", ") || "no uplink"}`}
        />
        <StatTile
          label={
            <>
              <SeriesKey color={OUT} />
              Out
            </>
          }
          value={<LiveBytes value={tx} suffix="/s" />}
          trend={
            <TileTrend
              values={lastValues(uplinkSeries, "tx")}
              color={OUT}
              label="Sent through the uplink over the last fifteen minutes"
            />
          }
          hint="to the internet"
        />
        <StatTile
          label={
            <>
              <Connection
                aria-hidden
                className="mr-1.5 inline-block size-3 align-[-1.5px] text-brand"
              />
              Connections
            </>
          }
          value={<NumberTicker value={overview.connections.total} />}
          hint={`${overview.connections.fromInternet} from the internet · ${overview.connections.listening} listening`}
        />
        <StatTile
          label={
            <>
              <LockClosed
                aria-hidden
                className="mr-1.5 inline-block size-3 align-[-1.5px] text-brand"
              />
              VPN
            </>
          }
          value={
            vpnPeers === 0 && tunnels.length === 0 ? (
              "None"
            ) : (
              <>
                <NumberTicker value={vpnOnline} />
                <span className="text-muted-foreground"> / {vpnPeers}</span>
              </>
            )
          }
          trailing={vpnPeers > 0 ? "online" : undefined}
          hint={
            <span className="inline-flex max-w-full min-w-0 items-center gap-2">
              <span className="truncate">
                {tunnels.length === 0
                  ? "no VPN or tunnel"
                  : tunnelsUp.map((l) => l.name).join(", ")}
              </span>
              <ProductGlyphs
                ids={[
                  ...new Set(tunnels.map((l) => linkProduct(l)).filter((p): p is string => !!p)),
                ]}
              />
            </span>
          }
        />
        <StatTile
          label={
            <>
              <ShieldCheck
                aria-hidden
                className="mr-1.5 inline-block size-3 align-[-1.5px] text-brand"
              />
              Refused
            </>
          }
          value={<NumberTicker value={overview.gateway.dropped} />}
          trailing="packets"
          hint={
            overview.gateway.blocklists + overview.gateway.limits === 0
              ? "no blocklist or rate limit yet"
              : `${plural(overview.gateway.blocklists, "blocklist")} · ${plural(overview.gateway.limits, "limit")} since loaded`
          }
        />
      </StatGrid>
    </Section>
  )
}

/**
 * The last fifteen minutes from the live ring: the uplink in and out as
 * areas, every tunnel and every Docker bridge as one line each, so whether a
 * spike was the internet, a VPN peer or a container is read off the colour.
 */
function TrafficChart({ links, live }: { links: NetworkLink[]; live: LiveTraffic }) {
  const rows = useMemo(() => {
    const series = (filter: (l: NetworkLink) => boolean) =>
      sumSeries(links.filter(filter).map((l) => live.series[l.name] ?? []))
    const uplink = series((l) => l.uplink)
    const tunnels = new Map(
      series((l) => l.role === "tunnel" && l.owner !== "kernel").map((p) => [p.t, p.rx + p.tx]),
    )
    const containers = new Map(
      series((l) => l.owner === "docker" && l.role === "bridge").map((p) => [p.t, p.rx + p.tx]),
    )
    return uplink.map((p) => ({
      ts: p.t * 1000,
      rx: p.rx,
      tx: p.tx,
      tunnels: tunnels.get(p.t) ?? 0,
      containers: containers.get(p.t) ?? 0,
    }))
  }, [links, live])
  return (
    <ChartPanel
      plain
      title="Last 15 minutes"
      rows={rows}
      series={SERIES}
      format={formatRate}
      axisFormat={axisRate}
      showPeaks={false}
      height={200}
      note="Collecting — the first readings arrive within a few seconds."
    />
  )
}
