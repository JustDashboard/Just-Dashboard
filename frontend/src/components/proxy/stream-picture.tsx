"use client"

import { useMemo } from "react"
import { Connection, Servers } from "@/components/icons"
import { cn } from "@/lib/utils"
import type { StreamEntry, StreamTrafficSummary } from "@/lib/types"
import { listenLabel, protocolLabel, stateStatus, upstreamAddress } from "@/lib/streams"
import { ProductGlyph, hasProductLogo, portProduct } from "@/components/product-logo"
import { StatusDot, type DotTone } from "@/components/status-dot"
import { WireMark } from "@/components/wire/wire"
import { WireFlow, type WireFlowNode, type WireLine } from "@/components/wire/wire-flow"
import { MoreTile } from "@/components/wire/wire-marks"
import {
  hourWords,
  restrictionWords,
  streamHref,
  streamPicture,
  type PictureEdge,
} from "@/components/proxy/stream-facts"

type MarkSize = "lg" | "md" | "sm"

/** A stream's state as the dot in its port's corner. */
function stateTone(stream: StreamEntry): DotTone {
  const status = stateStatus(stream)
  if (status.tone) return status.tone
  return status.verdict === "ok" ? "running" : status.verdict === "critical" ? "danger" : "warning"
}

/**
 * The forwards as the thing they are: the host's ports on the left, each
 * drawn as the service its number is (5432 as PostgreSQL), nginx's stream
 * module in the middle, and on the right every address those ports are
 * handed to. The overview's route picture (`route-picture.tsx`) with ports
 * where the domains are, and since 0.9.1 the same `WireFlow`.
 *
 * A port open to anyone is drawn in the warning colour, a database port open
 * to anyone in the danger one; a port nginx does not hold is a dashed red
 * line, a paused one a dashed grey one. A line moves only while nginx holds
 * the port and bytes crossed it in the last hour — the one way both ends are
 * known to be up. Pointing at a port steps back every line but its own and
 * those of the addresses it reaches.
 *
 * It was laid by hand as one column a side. As a `WireFlow` the ports and
 * the addresses are rows on level lines through nginx, as wide as the page
 * (one forward on its own page is a single line across it), and a column a
 * side only where a side has no room for three across. A port's mark is the
 * way to its page; under it are its number and name and who may connect,
 * with its protocol and its hour a hover away: the rows under the picture
 * say both.
 *
 * On a phone it is the one phone form (`WireMarks`): the ports' marks, nginx
 * named, the addresses' marks, and the lines between; the rows under it name
 * each one.
 */
export function StreamPicture({
  streams,
  summaries,
  engineTone,
  engineHint,
  current,
}: {
  streams: StreamEntry[]
  summaries?: Record<string, StreamTrafficSummary>
  /** The module's state, in nginx's corner. */
  engineTone: DotTone
  engineHint?: React.ReactNode
  /** The stream whose page this is: its port is not a link to itself. */
  current?: string
}) {
  const picture = useMemo(() => streamPicture(streams, summaries), [streams, summaries])
  const lineTo = (id: string) => wire(picture.edges.find((e) => e.from === id || e.to === id))

  // A port is joined to the addresses it reaches, and an address to its ports.
  const joined = (a: string, b: string) => {
    const [port, up] = a.startsWith("p:") ? [a, b] : [b, a]
    if (!port.startsWith("p:") || !up.startsWith("u:")) return false
    return (
      picture.upstreams.find((u) => `u:${u.address}` === up)?.streams.includes(port.slice(2)) ??
      false
    )
  }

  const ports: WireFlowNode[] = picture.ports.map((stream) =>
    portNode({
      stream,
      summary: summaries?.[stream.name],
      current: stream.name === current,
      line: lineTo(`p:${stream.name}`),
    }),
  )
  if (picture.hidden > 0) {
    // `streamPicture` has already cut the ports to what a picture holds, so
    // the tile for the rest is a node of the picture's own, not `max`'s.
    const words = `${picture.hidden} more in the list below`
    ports.push({
      id: "p:more",
      placeholder: true,
      name: words,
      mark: (size) => <MoreTile count={picture.hidden} size={size} />,
      title: <span className="font-normal text-muted-foreground">{words}</span>,
    })
  }

  return (
    <div data-slot="stream-picture" className="animate-rise">
      <WireFlow
        phone="marks"
        related={joined}
        start={{
          title: "Ports on this host",
          count: streams.length,
          heading: "always",
          nodes: ports,
        }}
        hub={{
          mark: (
            <span className="relative flex">
              <WireMark tone="logo" size="lg">
                <ProductGlyph id="nginx" />
              </WireMark>
              <StatusDot tone={engineTone} corner="lg" />
            </span>
          ),
          eyebrow: "Stream module",
          title: "nginx",
          hint: engineHint,
        }}
        end={{
          title: "Forwarded to",
          count: picture.upstreams.length,
          heading: "always",
          nodes: picture.upstreams.map((node) => ({
            id: `u:${node.address}`,
            name: node.address,
            mark: (size) => <UpstreamMark address={node.address} size={size} />,
            title: (
              <span className="block truncate font-mono text-hint" title={node.address}>
                {node.address}
              </span>
            ),
            hint: (
              <span className="block truncate" title={node.streams.join(", ")}>
                {node.streams.join(", ")}
              </span>
            ),
            line: lineTo(`u:${node.address}`),
          })),
        }}
      />
    </div>
  )
}

/** An edge of `streamPicture` as the line `WireFlow` draws. */
function wire(edge: PictureEdge | undefined): WireLine | undefined {
  if (!edge) return undefined
  return {
    state: edge.tone === "default" ? "up" : edge.tone,
    dashed: edge.dashed,
    moving: edge.moving,
  }
}

/**
 * A port on this host: the service its number is, the stream's name, and who
 * may connect. Its name is cut short where the node has no room, and so is a
 * long list of who may connect, each with the whole of it a hover away.
 */
function portNode({
  stream,
  summary,
  current,
  line,
}: {
  stream: StreamEntry
  summary?: StreamTrafficSummary
  current: boolean
  line?: WireLine
}): WireFlowNode {
  const who = restrictionWords(stream)
  const hour = hourWords(summary)
  const port = listenLabel(stream)
  return {
    id: `p:${stream.name}`,
    name: `${port} ${stream.name}`,
    href: current ? undefined : streamHref(stream.name),
    mark: (size) => <PortMark stream={stream} size={size} />,
    title: (
      <span
        className="inline-flex max-w-full items-baseline gap-2 align-bottom"
        title={`${port} ${stream.name}`}
      >
        <span className="numeric shrink-0 font-mono">{port}</span>
        <span className="truncate text-muted-foreground">{stream.name}</span>
      </span>
    ),
    hint: (
      <span
        className={cn(
          "block truncate",
          who.mono && "font-mono",
          who.tone === "danger" && "font-medium text-destructive",
          who.tone === "warning" && "text-warning",
        )}
        title={[protocolLabel(stream.protocol), who.label, hour].filter(Boolean).join(" · ")}
      >
        {who.label}
      </span>
    ),
    line,
  }
}

/** A port as the service its number is, with the stream's state in the corner. */
function PortMark({ stream, size }: { stream: StreamEntry; size: MarkSize }) {
  const product = portProduct(stream.listen)
  return (
    <span className="relative flex">
      <WireMark tone="logo" size={size}>
        {hasProductLogo(product) ? <ProductGlyph id={product} /> : <Connection aria-hidden />}
      </WireMark>
      <StatusDot tone={stateTone(stream)} corner={size} />
    </span>
  )
}

/** An address a port is handed to, as the service its port is where that says one. */
function UpstreamMark({ address, size }: { address: string; size: MarkSize }) {
  const port = upstreamAddress(address)?.port
  const product = port ? portProduct(port) : undefined
  return (
    <WireMark tone="logo" size={size}>
      {hasProductLogo(product) ? <ProductGlyph id={product} /> : <Servers aria-hidden />}
    </WireMark>
  )
}
