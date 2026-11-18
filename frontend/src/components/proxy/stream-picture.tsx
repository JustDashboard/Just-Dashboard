"use client"

import Link from "next/link"
import { createRef, useMemo, useRef, useState, type RefObject } from "react"
import { Connection, Servers } from "@/components/icons"
import { cn } from "@/lib/utils"
import type { StreamEntry, StreamTrafficSummary } from "@/lib/types"
import { listenLabel, protocolLabel, stateStatus, upstreamAddress } from "@/lib/streams"
import { ProductGlyph, hasProductLogo, portProduct } from "@/components/product-logo"
import { StatusDot, type DotTone } from "@/components/status-dot"
import { AnimatedBeam } from "@/components/ui/animated-beam"
import { useBreakpoint } from "@/lib/breakpoints"
import { WireMark, WireNode } from "@/components/wire/wire"
import { WireLane } from "@/components/wire/wire-lane"
import { MoreTile, WireMarks } from "@/components/wire/wire-marks"
import { useMotionHeld } from "@/components/wire/use-motion-held"
import {
  hourWords,
  restrictionWords,
  streamHref,
  streamPicture,
} from "@/components/proxy/stream-facts"

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
 * where the domains are.
 *
 * A port open to anyone is drawn in the warning colour, a database port open
 * to anyone in the danger one; a port nginx does not hold is a dashed red
 * line, a paused one a dashed grey one. A line moves only while nginx holds
 * the port and bytes crossed it in the last hour — the one way both ends are
 * known to be up. Pointing at a port steps back every line but its own.
 *
 * Three lanes from `lg`. On a phone it is the one phone form (`WireMarks`):
 * the ports' marks, nginx named, the addresses' marks, and the lines between;
 * the rows under it name each one.
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
  const container = useRef<HTMLDivElement>(null)
  const hub = useRef<HTMLDivElement>(null)
  const [focus, setFocus] = useState<string | null>(null)
  const wide = useBreakpoint("lg")
  const held = useMotionHeld()
  const picture = useMemo(() => streamPicture(streams, summaries), [streams, summaries])

  const ids = [
    ...picture.ports.map((s) => `p:${s.name}`),
    ...picture.upstreams.map((u) => `u:${u.address}`),
  ].join("\n")
  const refs = useMemo(() => {
    const out = new Map<string, RefObject<HTMLSpanElement | null>>()
    for (const id of ids.split("\n")) if (id) out.set(id, createRef<HTMLSpanElement>())
    return out
  }, [ids])

  // A port is joined to the addresses it reaches, and an address to its ports.
  const joined = (a: string, b: string) => {
    const [port, up] = a.startsWith("p:") ? [a, b] : [b, a]
    if (!port.startsWith("p:") || !up.startsWith("u:")) return false
    return picture.upstreams.find((u) => `u:${u.address}` === up)?.streams.includes(port.slice(2))
  }
  const lit = (id: string) => focus === null || focus === id || Boolean(joined(focus, id))
  const watch = (id: string) => ({
    onPointerEnter: () => setFocus(id),
    onPointerLeave: () => setFocus((held) => (held === id ? null : held)),
    onFocus: () => setFocus(id),
    onBlur: () => setFocus((held) => (held === id ? null : held)),
  })

  const engineMark = (size: "lg" | "sm") => (
    <span className="relative flex">
      <WireMark tone="logo" size={size}>
        <ProductGlyph id="nginx" />
      </WireMark>
      <StatusDot tone={engineTone} corner={size === "lg" ? "lg" : "sm"} />
    </span>
  )

  if (!wide)
    return (
      <WireMarks
        data-slot="stream-picture"
        className="animate-rise"
        tiers={[
          {
            title: "Ports on this host",
            nodes: [
              ...picture.ports.map((stream) => ({
                id: `p:${stream.name}`,
                mark: <PortMark stream={stream} size="sm" />,
                name: `${listenLabel(stream)} ${stream.name}`,
                href: stream.name === current ? undefined : streamHref(stream.name),
              })),
              ...(picture.hidden > 0
                ? [
                    {
                      id: "p:more",
                      mark: <MoreTile count={picture.hidden} />,
                      name: `${picture.hidden} more in the list below`,
                      round: true,
                      anonymous: true,
                    },
                  ]
                : []),
            ],
          },
          {
            id: "hub",
            mark: engineMark("lg"),
            eyebrow: "Stream module",
            title: "nginx",
            hint: engineHint,
          },
          {
            title: "Forwarded to",
            nodes: picture.upstreams.map((node) => ({
              id: `u:${node.address}`,
              mark: <UpstreamMark address={node.address} size="sm" />,
              name: node.address,
            })),
          },
        ]}
        wires={picture.edges.map((edge) => ({
          from: edge.from,
          to: edge.to,
          still: !edge.moving || held,
          dashed: edge.dashed,
          tone: edge.tone,
        }))}
      />
    )

  return (
    <div data-slot="stream-picture" className="relative animate-rise pt-4 pb-6">
      <div ref={container} className="relative">
        {picture.edges.map((edge, index) => {
          const into = edge.to === "hub"
          const from = into ? refs.get(edge.from) : hub
          const to = into ? hub : refs.get(edge.to)
          if (!from || !to) return null
          const end = into ? edge.from : edge.to
          return (
            <AnimatedBeam
              key={`${edge.from}→${edge.to}`}
              containerRef={container}
              fromRef={from}
              toRef={to}
              shape="s"
              startXOffset={into ? 26 : 30}
              endXOffset={into ? -30 : -26}
              still={!edge.moving || held}
              dashed={edge.dashed}
              tone={edge.tone}
              delay={(index % 6) * 0.35}
              className={cn("transition-opacity", !lit(end) && "opacity-15")}
            />
          )
        })}

        <div className="relative grid gap-y-8 lg:grid-cols-[minmax(0,1fr)_clamp(2.5rem,6vw,6rem)_minmax(0,0.7fr)_clamp(2.5rem,6vw,6rem)_minmax(0,1fr)] lg:items-center">
          <WireLane title="Ports on this host" count={streams.length} align="end" float>
            {picture.ports.map((stream) => (
              <li
                key={stream.name}
                {...watch(`p:${stream.name}`)}
                className={cn(
                  "min-w-0 transition-opacity",
                  !lit(`p:${stream.name}`) && "opacity-40",
                )}
              >
                <PortNode
                  stream={stream}
                  summary={summaries?.[stream.name]}
                  current={stream.name === current}
                  nodeRef={refs.get(`p:${stream.name}`)}
                />
              </li>
            ))}
            {picture.hidden > 0 && (
              <li className="min-w-0 text-hint text-muted-foreground lg:text-right">
                {picture.hidden} more in the list below
              </li>
            )}
          </WireLane>

          <div aria-hidden className="max-lg:hidden" />

          <div className="flex min-w-0 lg:justify-center lg:py-12">
            <WireNode
              nodeRef={hub}
              align="center"
              mark={engineMark("lg")}
              eyebrow="Stream module"
              title="nginx"
              hint={engineHint}
            />
          </div>

          <div aria-hidden className="max-lg:hidden" />

          <WireLane title="Forwarded to" count={picture.upstreams.length} float>
            {picture.upstreams.map((node) => (
              <li
                key={node.address}
                {...watch(`u:${node.address}`)}
                className={cn(
                  "min-w-0 transition-opacity",
                  !lit(`u:${node.address}`) && "opacity-40",
                )}
              >
                <UpstreamNode
                  address={node.address}
                  streams={node.streams}
                  nodeRef={refs.get(`u:${node.address}`)}
                />
              </li>
            ))}
          </WireLane>
        </div>
      </div>
    </div>
  )
}

/** A port on this host: the service its number is, the stream's name, and who may connect. */
function PortNode({
  stream,
  summary,
  current,
  nodeRef,
}: {
  stream: StreamEntry
  summary?: StreamTrafficSummary
  current: boolean
  nodeRef?: RefObject<HTMLSpanElement | null>
}) {
  const who = restrictionWords(stream)
  const hour = hourWords(summary)
  return (
    <WireNode
      align="end"
      mark={
        <span ref={nodeRef} className="flex">
          <PortMark stream={stream} size="md" />
        </span>
      }
      title={
        <span className="flex min-w-0 items-baseline gap-2 lg:justify-end">
          <span className="numeric shrink-0 font-mono">{listenLabel(stream)}</span>
          {current ? (
            <span className="truncate text-muted-foreground">{stream.name}</span>
          ) : (
            <Link
              href={streamHref(stream.name)}
              className="truncate rounded-sm text-muted-foreground focus-ring hover:text-foreground hover:underline"
            >
              {stream.name}
            </Link>
          )}
        </span>
      }
      hint={
        <span className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-0.5 lg:justify-end">
          <span>{protocolLabel(stream.protocol)}</span>
          <span
            className={cn(
              who.mono && "font-mono",
              who.tone === "danger" && "font-medium text-destructive",
              who.tone === "warning" && "text-warning",
            )}
          >
            {who.label}
          </span>
          {hour && <span className="numeric">{hour}</span>}
        </span>
      }
    />
  )
}

/** An address a port is handed to, drawn as the service its port is where that says one. */
function UpstreamNode({
  address,
  streams,
  nodeRef,
}: {
  address: string
  streams: string[]
  nodeRef?: RefObject<HTMLSpanElement | null>
}) {
  return (
    <WireNode
      mark={
        <span ref={nodeRef} className="flex">
          <UpstreamMark address={address} size="md" />
        </span>
      }
      title={
        <span className="block truncate font-mono text-hint" title={address}>
          {address}
        </span>
      }
      hint={<span className="block truncate">{streams.join(", ")}</span>}
    />
  )
}

/** A port as the service its number is, with the stream's state in the corner. */
function PortMark({ stream, size }: { stream: StreamEntry; size: "md" | "sm" }) {
  const product = portProduct(stream.listen)
  return (
    <span className="relative flex">
      <WireMark tone="logo" size={size}>
        {hasProductLogo(product) ? <ProductGlyph id={product} /> : <Connection aria-hidden />}
      </WireMark>
      <StatusDot tone={stateTone(stream)} corner={size === "md" ? "md" : "sm"} />
    </span>
  )
}

/** An address a port is handed to, as the service its port is where that says one. */
function UpstreamMark({ address, size }: { address: string; size: "md" | "sm" }) {
  const port = upstreamAddress(address)?.port
  const product = port ? portProduct(port) : undefined
  return (
    <WireMark tone="logo" size={size}>
      {hasProductLogo(product) ? <ProductGlyph id={product} /> : <Servers aria-hidden />}
    </WireMark>
  )
}
