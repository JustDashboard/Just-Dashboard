"use client"

import Link from "next/link"
import { createRef, useMemo, useRef, useState, useSyncExternalStore, type RefObject } from "react"
import { ClientMark } from "@/components/client-mark"
import { WireHost, WireNode, WirePlaceholder } from "@/components/deploy/wire"
import { Plus } from "@/components/icons"
import { AnimatedBeam } from "@/components/ui/animated-beam"
import { relativeTime } from "@/lib/format"
import {
  isoOf,
  latency,
  protocolLabel,
  reachability,
  sessionHref,
  type RdComputerView,
  type RdOverview,
} from "@/lib/remote-desktop"
import { cn } from "@/lib/utils"
import { ComputerMark, HueKey } from "@/components/remote-desktop/marks"

const noSubscribe = () => () => {}

/**
 * The way a session travels, drawn: this browser, this server running guacd
 * (and Stream mode's bridge once it is installed), and every computer the
 * server can open a screen on.
 *
 * A wire to a computer pulses while a session to it is open, is a still line
 * while its port answers, and is dashed — amber where the computer is on but
 * its remote desktop server is not answering, grey where the tailnet says it
 * is offline — so which computers can be used right now is read across the
 * picture before a word is. The browser's wire pulses whenever any session
 * is open, since every screen comes through it. Hovering a computer lights
 * its own wire and dims the rest.
 *
 * Wide, the three stand in lanes; below `lg` they stack and the wires go, so
 * each computer's line says what its wire would have.
 */
export function ComputerPicture({
  overview,
  hueOf,
  hostname,
}: {
  overview: RdOverview
  hueOf: (computer: RdComputerView) => string
  hostname?: string
}) {
  const container = useRef<HTMLDivElement>(null)
  const you = useRef<HTMLDivElement>(null)
  const hub = useRef<HTMLDivElement>(null)
  const [focus, setFocus] = useState<string | null>(null)
  const ids = overview.computers.map((computer) => computer.id).join(",")
  const refs = useMemo(() => {
    const out = new Map<string, RefObject<HTMLSpanElement | null>>()
    for (const id of ids.split(",")) if (id) out.set(id, createRef<HTMLSpanElement>())
    return out
  }, [ids])
  // Read on the client only: the server rendering this page has no browser
  // of the reader's to draw.
  const agent = useSyncExternalStore(
    noSubscribe,
    () => navigator.userAgent,
    () => "",
  )
  const live = overview.computers.some((computer) => computer.live > 0)
  const engine = overview.engine
  const running = engine.state === "running"

  return (
    <div data-slot="computer-picture" className="relative animate-rise py-4">
      <div ref={container} className="relative">
        <AnimatedBeam
          containerRef={container}
          fromRef={you}
          toRef={hub}
          shape="s"
          startXOffset={30}
          endXOffset={-30}
          still={!live}
          duration={2.2}
          className="max-lg:hidden"
        />
        {overview.computers.map((computer, index) => {
          const to = refs.get(computer.id)
          if (!to) return null
          const reach = reachability(computer)
          return (
            <AnimatedBeam
              key={computer.id}
              containerRef={container}
              fromRef={hub}
              toRef={to}
              shape="s"
              startXOffset={30}
              endXOffset={-24}
              still={reach.kind !== "live"}
              dashed={reach.kind === "closed" || reach.kind === "offline"}
              tone={reach.kind === "closed" ? "warning" : "default"}
              duration={2.2}
              delay={(index % 6) * 0.35}
              className={cn(
                "transition-opacity max-lg:hidden",
                reach.kind === "offline" && "opacity-40",
                focus !== null && focus !== computer.id && "opacity-15",
              )}
            />
          )
        })}

        <div className="relative grid gap-y-8 lg:grid-cols-[minmax(0,0.8fr)_clamp(2.5rem,7vw,7rem)_minmax(0,0.8fr)_clamp(2.5rem,7vw,7rem)_minmax(0,1fr)] lg:items-center">
          <div className="flex min-w-0 lg:justify-end">
            <WireNode
              nodeRef={you}
              align="end"
              mark={agent ? <ClientMark userAgent={agent} /> : <span className="size-11" />}
              eyebrow="You"
              title="This browser"
              hint={overview.tailnet.installed ? "the screen arrives here" : undefined}
            />
          </div>

          <div aria-hidden className="max-lg:hidden" />

          <div className="flex min-w-0 lg:justify-center">
            <WireNode
              nodeRef={hub}
              align="center"
              mark={<WireHost />}
              eyebrow="This server"
              title={hostname ?? "This server"}
              hint={
                running ? (
                  overview.stream.state === "running" ? (
                    `guacd ${engine.version} · Moonlight Web ${overview.stream.version}`
                  ) : (
                    `guacd ${engine.version}`
                  )
                ) : (
                  <span className="text-warning">engine not running</span>
                )
              }
            />
          </div>

          <div aria-hidden className="max-lg:hidden" />

          <section aria-label="Computers" className="min-w-0">
            <p className="eyebrow mb-3 flex items-center gap-1.5">
              Computers
              <span className="numeric text-muted-foreground/70">{overview.computers.length}</span>
            </p>
            <ul className="flex min-w-0 flex-col gap-4">
              {overview.computers.map((computer) => (
                <li
                  key={computer.id}
                  onPointerEnter={() => setFocus(computer.id)}
                  onPointerLeave={() => setFocus((held) => (held === computer.id ? null : held))}
                  onFocus={() => setFocus(computer.id)}
                  onBlur={() => setFocus((held) => (held === computer.id ? null : held))}
                  className={cn(
                    "min-w-0 transition-opacity",
                    focus !== null && focus !== computer.id && "opacity-40",
                  )}
                >
                  <ComputerNode
                    computer={computer}
                    hue={hueOf(computer)}
                    nodeRef={refs.get(computer.id)}
                  />
                </li>
              ))}
              <li className="min-w-0">
                <WireNode
                  mark={
                    <Link
                      href="/remote-desktop/new"
                      aria-label="Add a computer"
                      className="rounded-full focus-ring"
                    >
                      <WirePlaceholder size="md">
                        <Plus />
                      </WirePlaceholder>
                    </Link>
                  }
                  title={
                    <Link
                      href="/remote-desktop/new"
                      className="text-muted-foreground hover:text-foreground hover:underline"
                    >
                      Add a computer
                    </Link>
                  }
                />
              </li>
            </ul>
          </section>
        </div>
      </div>
    </div>
  )
}

function ComputerNode({
  computer,
  hue,
  nodeRef,
}: {
  computer: RdComputerView
  hue: string
  nodeRef?: RefObject<HTMLSpanElement | null>
}) {
  const reach = reachability(computer)
  const server = protocolLabel(computer.protocol, computer.os)
  const path = computer.peer
    ? computer.peer.direct
      ? "direct"
      : computer.peer.relay
        ? `relayed via ${computer.peer.relay}`
        : undefined
    : undefined
  return (
    <WireNode
      mark={
        <span ref={nodeRef} className="flex">
          <ComputerMark os={computer.os} reach={reach} />
        </span>
      }
      title={
        <span className="flex min-w-0 items-center gap-2">
          <HueKey color={hue} />
          <Link
            href={sessionHref(computer.id)}
            className="min-w-0 truncate rounded-sm focus-ring hover:underline"
          >
            {computer.name}
          </Link>
        </span>
      }
      hint={
        <span className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-0.5">
          {reach.kind === "live" ? (
            <span className="font-medium text-success">in a session</span>
          ) : reach.kind === "open" ? (
            <>
              <span>{server}</span>
              {path && <span>{path}</span>}
              <span className="numeric">{latency(computer.reach.latencyMs)}</span>
            </>
          ) : reach.kind === "offline" ? (
            <span>
              offline
              {computer.peer?.lastSeen
                ? ` · seen ${relativeTime(isoOf(computer.peer.lastSeen))}`
                : ""}
            </span>
          ) : (
            <span className="text-warning">{server} not answering</span>
          )}
        </span>
      }
    />
  )
}
