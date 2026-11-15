"use client"

import Link from "next/link"
import { useRouter } from "next/navigation"
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react"
import { ArrowDown, ArrowUp, Crosshair, Globe, RefreshClockwise, Wrench } from "@/components/icons"
import { get } from "@/lib/api"
import { bytes, plural, rate } from "@/lib/format"
import type { NetworkLink, NetworkOverview } from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { NetworkChange } from "@/components/network/change-status"
import { NetworkReadWarning } from "@/components/network/read-warning"
import { ReconnectGuardPanel } from "@/components/network/reconnect-guard"
import { Page, PageContext } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { NoReading, ReadingLine, ReadingLines } from "@/components/reading-line"
import { ErrorState } from "@/components/state"
import { Status } from "@/components/status-dot"
import { TileTrend } from "@/components/metrics/sparkline"
import { HostIdentity, HostFact, FactDot } from "@/components/metrics/host-identity"
import { ChartPanel } from "@/components/metrics/chart-panel"
import { NetworkFact } from "@/components/client-mark"
import { LiveBytes, StreamState } from "@/components/overview/readings"
import { WireMark } from "@/components/deploy/wire"
import {
  Topology as TopologyPicture,
  TopologyLoading,
  buildNodes,
} from "@/components/network/topology"
import { WaysOut } from "@/components/network/egress-identity"
import { FlowPaths, type FlowNode } from "@/components/network/flow-paths"
import {
  ageWords,
  identityVerdict,
  newestPoint,
  observationAge,
  windowBreakdown,
} from "@/components/network/topology-reading"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { VerbMenu, type Verb } from "@/components/verbs"
import { useViewState } from "@/lib/view-state"
import {
  lastValues,
  sumSeries,
  useLiveTraffic,
  type LiveTraffic,
} from "@/components/network/use-live-traffic"
import { deviceHref } from "@/components/network/interfaces/device-facts"

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

/** What the picture drew at the last visit, so the grey page is the page's own shape. */
type Shape = { outside: number; inside: number; facts: number }
// Two lines of facts: what a laptop's width gives this server's line.
const FIRST_VISIT: Shape = { outside: 3, inside: 3, facts: 36 }

/**
 * Is the network doing what it should, and what is it carrying right now?
 *
 * The page opens on what the machine is on the network, as a project's page
 * opens on the project: the address the internet reaches it at, whether its
 * uplink is up, its uplink and gateway, and how this browser reaches it (the
 * path every guard on these pages protects), with Run a check as its command
 * and the other diagnostics behind the menu. Then the topology, which is the
 * page (the internet, the tailnet and every tunnel on the left, this server
 * in the middle with its firewall, routing, connections and what it refused,
 * the networks it is the gateway of on the right, each wire moving with its
 * device's bytes), and under it the paths traffic is taking through it, from
 * connection tracking; what the uplink receives and sends, each a line with
 * its last fifteen minutes; that quarter of an hour as a chart whose
 * selection lists the devices that carried it; and each family's way out.
 *
 * Five tiles stood under the picture. In and Out are the two lines; the VPN
 * peers online and the packets refused were said a second time, the picture's
 * own nodes carrying them already, and the connections are this server's, so
 * they went onto its node.
 *
 * The overview is read every fifteen seconds and the throughput every two, so
 * the wires and figures move between reads of the rest: the devices are drawn
 * with the live ring's newest rates over the overview's own. The page has its
 * shape before the first read lands: the line, the picture grey with as many
 * rows as it drew last time, and the two lines waiting.
 */
export default function NetworkOverviewPage() {
  const overview = usePoll<NetworkOverview>(
    (signal) => get("/network/overview", undefined, signal),
    15_000,
  )
  const live = useLiveTraffic()
  const [shape, setShape] = useViewState<Shape>("network.overview.shape", FIRST_VISIT)

  const links = useMemo(
    () => withLiveRates(overview.data?.links ?? [], live),
    [overview.data, live],
  )
  const data = overview.data
  const drawn = useMemo(() => (data ? buildNodes(data, links) : undefined), [data, links])
  const outside = drawn?.filter((node) => node.lane === "outside").length
  const inside = drawn?.filter((node) => node.lane === "inside").length
  useEffect(() => {
    if (outside === undefined || inside === undefined) return
    setShape((held) =>
      held.outside === outside && held.inside === inside ? held : { ...held, outside, inside },
    )
  }, [outside, inside, setShape])
  // The line of facts wraps by what this server has to say and how wide the
  // window is, so its height is remembered too rather than guessed.
  const measured = useCallback(
    (facts: number) => setShape((held) => (held.facts === facts ? held : { ...held, facts })),
    [setShape],
  )

  const failed = !data && Boolean(overview.error)
  const nodeOf = (id: string) => flowNode(id, data!, links)
  return (
    <Page className="animate-rise">
      <PageContext eyebrow="Network" title="Network" />
      <NetworkIdentity
        overview={data}
        links={links}
        failed={failed}
        factsHeight={shape.facts}
        onFacts={measured}
        onRefresh={() => {
          overview.refresh()
          live.refresh()
        }}
      />
      {failed && <ErrorState error={overview.error!} onRetry={overview.refresh} />}
      {data && (
        <NetworkReadWarning
          error={overview.error}
          refresh={overview.refresh}
          lastSuccess={overview.lastSuccess}
        />
      )}
      {data && live.error && live.now > 0 && (
        <NetworkReadWarning
          error={live.error}
          refresh={live.refresh}
          lastSuccess={live.lastSuccess}
          reading="live throughput"
        />
      )}
      {(data?.observations ?? [])
        .filter((source) => source.state === "failed")
        .map((source) => (
          <ErrorState
            key={source.source}
            error={new Error(`${source.label}: ${source.error || "Could not read this source"}`)}
            onRetry={overview.refresh}
          />
        ))}
      {data?.persistence.change && <NetworkChange change={data.persistence.change} />}

      {!failed && (
        <Panel plain>
          <PanelHeader
            title="Topology"
            actions={
              <span className="numeric text-hint text-muted-foreground">
                {data ? (
                  <>
                    {plural(
                      links.filter((l) => l.owner !== "kernel" && l.role !== "container").length,
                      "device",
                    )}
                    {" · "}
                    {plural(data.dockerNetworks.length, "Docker network")}
                  </>
                ) : (
                  <Skeleton className="inline-block h-3 w-40 align-middle" />
                )}
              </span>
            }
          />
          <PanelBody>
            {data ? (
              <TopologyPicture overview={data} links={links} />
            ) : (
              <TopologyLoading outside={shape.outside} inside={shape.inside} />
            )}
          </PanelBody>
        </Panel>
      )}

      {data && <FlowPaths flows={data.flows} nodes={nodeOf} />}

      {!failed && <Readings links={data ? links : undefined} live={live} />}

      {data && <TrafficChart links={links} live={live} />}

      {data && <WaysOut identity={data.identity} />}

      {data && <ReconnectGuardPanel />}
    </Page>
  )
}

/** A flow edge's end as the topology names it, with the page it opens. */
function flowNode(id: string, overview: NetworkOverview, links: NetworkLink[]): FlowNode {
  if (id === "host") return { label: overview.hostname || "This server" }
  if (id === "internet") {
    const uplink = links.find((l) => l.uplink)
    return {
      label: "The internet",
      href: uplink ? deviceHref(uplink.name) : undefined,
    }
  }
  if (id.startsWith("docker:")) {
    const network = overview.dockerNetworks.find((n) => `docker:${n.id}` === id)
    return { label: network?.name ?? "a Docker network", href: "/docker/networks" }
  }
  const name = id.replace(/^link:/, "")
  const link = links.find((l) => l.name === name)
  if (link?.owner === "tailscale") return { label: "The tailnet", href: "/network/vpn" }
  return { label: name, href: deviceHref(name) }
}

/** Each device with the live ring's newest reading, where the ring has one. */
function withLiveRates(links: NetworkLink[], live: LiveTraffic): NetworkLink[] {
  return links.map((link) => {
    const last = live.series[link.name]?.at(-1)
    return last ? { ...link, rxRate: last.rx, txRate: last.tx } : link
  })
}

/**
 * The line of facts as one block. On a phone it wraps, so it holds two lines
 * of room there from the start and the picture under it does not move when
 * the read lands.
 */
const FACTS = "flex min-w-0 flex-wrap content-start items-center gap-x-2 gap-y-1"

/** Whether the default route's device can carry anything, as a dot and a word. */
function uplinkState(overview: NetworkOverview, links: NetworkLink[]) {
  const primary = overview.defaults[0]
  if (!primary) return { tone: "warning", label: "No default route" } as const
  const uplink = links.find((l) => l.name === primary.device)
  if (uplink && (!uplink.adminUp || !uplink.carrier))
    return { tone: "danger", label: "Uplink down" } as const
  return { tone: "running", label: "Uplink up" } as const
}

/**
 * What the machine is on the network, laid out as a project's header: the
 * address the internet reaches it at as the name, whether its uplink is up
 * beside it, its uplink, gateway and second family as facts, and how this
 * browser reaches it last, because that is the path every guard on these
 * pages protects. Run a check is the command; the other diagnostics and the
 * refresh are behind the menu.
 */
function NetworkIdentity({
  overview,
  links,
  failed,
  factsHeight,
  onFacts,
  onRefresh,
}: {
  /** Undefined until the first read. */
  overview?: NetworkOverview
  links: NetworkLink[]
  failed: boolean
  /** The height the facts took at the last visit, held while the read is out. */
  factsHeight: number
  onFacts: (height: number) => void
  onRefresh: () => void
}) {
  const router = useRouter()
  const facts = useRef<HTMLSpanElement>(null)
  const arrived = overview !== undefined
  useLayoutEffect(() => {
    const node = facts.current
    if (!node) return
    const report = () => onFacts(node.offsetHeight)
    report()
    const observer = new ResizeObserver(report)
    observer.observe(node)
    return () => observer.disconnect()
    // Measured again when the read lands: the line is not there before it.
  }, [onFacts, arrived])
  // The two diagnostics behind the menu are an administrator's; a reader's
  // menu holds the refresh alone rather than a way to a page that refuses.
  const { can } = useAuth()
  const verbs: Verb[] = [
    { key: "refresh", label: "Refresh", icon: RefreshClockwise, run: onRefresh },
    ...(can("system.admin")
      ? [
          {
            key: "external",
            label: "Check from outside",
            icon: Globe,
            run: () => router.push("/network/external"),
          },
          {
            key: "captures",
            label: "Capture packets",
            icon: Crosshair,
            run: () => router.push("/network/captures"),
          },
        ]
      : []),
  ]
  const command = (
    <Button asChild size="sm">
      <Link href="/network/tools">
        <Wrench className="size-3.5" />
        Run a check
      </Link>
    </Button>
  )
  const menu = <VerbMenu verbs={verbs} label="Network actions" />
  if (!overview)
    return (
      <HostIdentity
        label="About the network"
        fallback={Globe}
        title={
          failed ? (
            "Network"
          ) : (
            // Beside a character of no width, so the grey block stands on a
            // line of the title's own height.
            <span className="inline-flex items-center">
              <Skeleton className="h-4 w-40" />
              &#8203;
            </span>
          )
        }
        state={
          failed ? (
            <Status tone="danger" label="Not read" />
          ) : (
            <span className="text-xs font-medium">
              <TextShimmer>Reading the network…</TextShimmer>
            </span>
          )
        }
        facts={
          failed ? (
            <span>The network could not be read.</span>
          ) : (
            <span className={FACTS} style={{ minHeight: factsHeight }}>
              <Skeleton className="my-0.5 h-3 w-72 max-w-full" />
            </span>
          )
        }
        command={command}
        menu={menu}
      />
    )
  const v4 = overview.publicAddresses.find((a) => !a.includes(":"))
  const v6 = overview.publicAddresses.find((a) => a.includes(":"))
  const primary = overview.defaults[0]
  const uplink = links.find((l) => l.name === primary?.device)
  const family = (name: "inet" | "inet6") => overview.identity.find((id) => id.family === name)
  const way = family(primary?.family === "inet6" ? "inet6" : "inet")
  const state = uplinkState(overview, links)
  return (
    <HostIdentity
      label="About the network"
      fallback={Globe}
      title={
        <span className="truncate font-mono">
          {(v4 ?? v6)?.replace(/\/\d+$/, "") ?? overview.hostname ?? "No public address"}
        </span>
      }
      state={<Status tone={state.tone} label={state.label} />}
      command={command}
      menu={menu}
      facts={
        <span ref={facts} className={FACTS}>
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
          {way && way.public !== "nic" && way.public !== "no_route" && (
            <>
              <FactDot />
              <HostFact>
                <span title={way.detail}>{identityVerdict(way)}</span>
              </HostFact>
            </>
          )}
          <FactDot />
          <HostFact>
            <span data-testid="forwarding-families">
              {forwardingWord("IPv4", family("inet"), overview.forwarding.ipv4)}
              {" · "}
              {forwardingWord("IPv6", family("inet6"), overview.forwarding.ipv6)}
            </span>
          </HostFact>
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
        </span>
      }
    />
  )
}

/** One family's forwarding switch, said as unreadable rather than off when it is. */
function forwardingWord(
  label: string,
  identity: NetworkOverview["identity"][number] | undefined,
  on: boolean,
) {
  if (identity?.forwardingError) return `${label} forwarding unreadable`
  return `${label} ${(identity?.forwarding ?? on) ? "routing" : "not routing"}`
}

function speed(mbps: number) {
  return mbps >= 1000 ? `${mbps / 1000} Gb/s` : `${mbps} Mb/s`
}

/**
 * What the uplink receives and sends, each a line: its mark on the measure's
 * own hue, the rate as it is read every two seconds, and its last fifteen
 * minutes at the line's end. Each is the way to the uplink's own page. Before
 * the first read the lines stand in their place and say they are waiting.
 */
function Readings({ links, live }: { links?: NetworkLink[]; live: LiveTraffic }) {
  const uplinks = (links ?? []).filter((l) => l.uplink)
  const uplinkSeries = sumSeries(uplinks.map((l) => live.series[l.name] ?? []))
  const rx = uplinks.reduce((n, l) => n + l.rxRate, 0)
  const tx = uplinks.reduce((n, l) => n + l.txRate, 0)
  const href = uplinks[0] ? deviceHref(uplinks[0].name) : undefined
  const names = uplinks.map((l) => l.name).join(", ")
  const waiting = !links ? "Reading the uplink…" : "No uplink"
  return (
    <Panel plain>
      <PanelHeader
        title="Throughput"
        actions={
          live.now > 0 ? (
            <span className="flex items-center gap-3">
              <ObservationAge live={live} />
              <StreamState connection={live.error ? "closed" : "open"} />
            </span>
          ) : undefined
        }
      />
      <PanelBody>
        <ReadingLines>
          <ReadingLine
            href={href}
            label={`Open ${uplinks[0]?.name ?? "the uplink"}: what it received`}
            mark={
              <WireMark size="md" hue={IN}>
                <ArrowDown aria-hidden />
              </WireMark>
            }
            name="In"
            hint={uplinks.length > 0 ? `from the internet · ${names}` : waiting}
            trend={
              uplinks.length > 0 && (
                <TileTrend
                  values={lastValues(uplinkSeries, "rx")}
                  color={IN}
                  label="Received through the uplink over the last fifteen minutes"
                />
              )
            }
          >
            {uplinks.length > 0 ? (
              <>
                <LiveBytes value={rx} suffix="/s" /> received
              </>
            ) : (
              <NoReading>No reading yet</NoReading>
            )}
          </ReadingLine>
          <ReadingLine
            href={href}
            label={`Open ${uplinks[0]?.name ?? "the uplink"}: what it sent`}
            mark={
              <WireMark size="md" hue={OUT}>
                <ArrowUp aria-hidden />
              </WireMark>
            }
            name="Out"
            hint={uplinks.length > 0 ? `to the internet · ${names}` : waiting}
            trend={
              uplinks.length > 0 && (
                <TileTrend
                  values={lastValues(uplinkSeries, "tx")}
                  color={OUT}
                  label="Sent through the uplink over the last fifteen minutes"
                />
              )
            }
          >
            {uplinks.length > 0 ? (
              <>
                <LiveBytes value={tx} suffix="/s" /> sent
              </>
            ) : (
              <NoReading>No reading yet</NoReading>
            )}
          </ReadingLine>
        </ReadingLines>
      </PanelBody>
    </Panel>
  )
}

/**
 * How old the newest two-second reading is, counted on this browser's clock
 * so a stream that stopped arriving shows it even before a poll fails.
 */
function ObservationAge({ live }: { live: LiveTraffic }) {
  const [clock, setClock] = useState(() => Date.now() / 1000)
  useEffect(() => {
    const timer = setInterval(() => setClock(Date.now() / 1000), 1000)
    return () => clearInterval(timer)
  }, [])
  const age = observationAge(newestPoint(live.series), clock)
  if (!age) return null
  return age.stale ? (
    <Status
      tone="warning"
      label={`Last reading ${ageWords(age.seconds)} ago`}
      className="text-hint"
    />
  ) : (
    <span className="numeric text-hint text-muted-foreground" data-testid="observation-age">
      observed {ageWords(age.seconds)} ago
    </span>
  )
}

/**
 * The last fifteen minutes from the live ring: the uplink in and out as
 * areas, every tunnel and every Docker bridge as one line each, so whether a
 * spike was the internet, a VPN peer or a container is read off the colour.
 * Under it, the devices each line is drawn from, each opening its sheet; a
 * stretch selected on the chart lists what carried it, busiest first.
 */
function TrafficChart({ links, live }: { links: NetworkLink[]; live: LiveTraffic }) {
  const [selected, setSelected] = useState<{ from: number; to: number }>()
  const zoom = useCallback((from: number, to: number) => setSelected({ from, to }), [])
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
  const groups = [
    { label: "In and out", members: links.filter((l) => l.uplink) },
    {
      label: "Tunnels",
      members: links.filter((l) => l.role === "tunnel" && l.owner !== "kernel"),
    },
    {
      label: "Containers",
      members: links.filter((l) => l.owner === "docker" && l.role === "bridge"),
    },
  ]
  const breakdown = selected ? windowBreakdown(live.series, links, selected.from, selected.to) : []
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
      onZoom={zoom}
      note="Collecting — the first readings arrive within a few seconds."
      footer={
        selected ? (
          <div className="flex flex-col gap-2" aria-label="What carried the selection">
            <div className="flex flex-wrap items-center gap-3 text-hint text-muted-foreground">
              <span>
                Carried between{" "}
                <time dateTime={new Date(selected.from).toISOString()}>
                  {new Date(selected.from).toLocaleTimeString()}
                </time>{" "}
                and{" "}
                <time dateTime={new Date(selected.to).toISOString()}>
                  {new Date(selected.to).toLocaleTimeString()}
                </time>
              </span>
              <Button size="xs" variant="outline" onClick={() => setSelected(undefined)}>
                Clear
              </Button>
            </div>
            {breakdown.length === 0 ? (
              <p className="text-hint text-muted-foreground">No device moved bytes then.</p>
            ) : (
              <ul className="flex flex-col gap-1">
                {breakdown.slice(0, 8).map((share) => (
                  <li key={share.name} className="flex items-center gap-3 text-body">
                    <Link
                      href={deviceHref(share.name)}
                      className="font-mono underline-offset-2 focus-ring hover:underline"
                    >
                      {share.name}
                    </Link>
                    <span className="text-hint text-muted-foreground">{share.role}</span>
                    <span className="numeric ml-auto">{bytes(share.bytes)}</span>
                  </li>
                ))}
              </ul>
            )}
          </div>
        ) : (
          <p className="flex flex-wrap gap-x-4 gap-y-1 text-hint text-muted-foreground">
            {groups
              .filter((g) => g.members.length > 0)
              .map((g) => (
                <span key={g.label}>
                  {g.label}:{" "}
                  {g.members.map((l, i) => (
                    <span key={l.name}>
                      {i > 0 && ", "}
                      <Link
                        href={deviceHref(l.name)}
                        className="font-mono underline-offset-2 focus-ring hover:underline"
                      >
                        {l.name}
                      </Link>
                    </span>
                  ))}
                </span>
              ))}
            <span>Select a stretch of the chart to see what carried it.</span>
          </p>
        )
      }
    />
  )
}
