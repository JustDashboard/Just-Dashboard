"use client"

import { useSyncExternalStore } from "react"
import { SessionWireMark } from "@/components/account/marks"
import { WireHost, WirePlaceholder } from "@/components/wire/wire"
import { Plus } from "@/components/icons"
import { WireFlow, type WireFlowNode } from "@/components/wire/wire-flow"
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
import { ComputerMark } from "@/components/remote-desktop/marks"

const noSubscribe = () => () => {}

const SKELETON_SIZE = { lg: "size-14", md: "size-11", sm: "size-9" } as const

/** This browser's id among the picture's nodes. */
const YOU = "you"

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
 * is open, since every screen comes through it. Pointing at a computer lights
 * its own wire and the browser's and dims the rest.
 *
 * Since 0.9.1 it is a `WireFlow`. It was laid by hand with the computers as
 * one column, each name beside its mark, so four of them and the ring that
 * adds one stood 340px tall against the right of a page whose left held one
 * mark. Wide, the computers are rows on level lines into the server, and the
 * browser's line runs level into it from the other side; a column where a
 * side has no room for three across. A computer's mark opens its screen, and
 * its words are its name and one line: the Computers list under the picture
 * says the rest.
 *
 * On a phone it is the one phone form (`WireMarks`): this browser, the server
 * named, and the computers' marks fanned under it with their wires, unnamed —
 * the Computers cards under the picture are the list, and a phone was shown
 * the four names twice.
 */
export function ComputerPicture({
  overview,
  hostname,
}: {
  overview: RdOverview
  hostname?: string
}) {
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

  const you: WireFlowNode = {
    id: YOU,
    name: "This browser",
    // The browser as the picture's own tile, the size of the computers' marks,
    // so its line meets it where theirs meet them.
    mark: (size) =>
      agent ? (
        <SessionWireMark userAgent={agent} size={size} />
      ) : (
        <span className={SKELETON_SIZE[size]} />
      ),
    eyebrow: "You",
    title: "This browser",
    hint: overview.tailnet.installed ? "the screen arrives here" : undefined,
    line: { state: "up", moving: live, duration: 2.2 },
  }

  return (
    <div data-slot="computer-picture" className="animate-rise">
      <WireFlow
        phone="marks"
        // Every screen comes through the browser, so it stays lit with
        // whichever computer is pointed at, and they all do with it.
        related={(focus, id) => focus === YOU || id === YOU}
        start={{ title: "You", nodes: [you] }}
        hub={{
          mark: <WireHost />,
          eyebrow: "This server",
          title: hostname ?? "This server",
          hint: running ? (
            overview.stream.state === "running" ? (
              `guacd ${engine.version} · Moonlight Web ${overview.stream.version}`
            ) : (
              `guacd ${engine.version}`
            )
          ) : (
            <span className="text-warning">engine not running</span>
          ),
        }}
        end={{
          title: "Computers",
          count: overview.computers.length,
          heading: "always",
          nodes: [
            ...overview.computers.map(computerNode),
            {
              id: "add",
              placeholder: true,
              href: "/remote-desktop/new",
              label: "Add a computer",
              mark: (size) => (
                <WirePlaceholder size={size}>
                  <Plus />
                </WirePlaceholder>
              ),
              title: <span className="font-normal text-muted-foreground">Add a computer</span>,
              line: { state: "idle" },
            },
          ],
        }}
      />
    </div>
  )
}

/**
 * A computer: its system's mark with its state in the corner, its name, and
 * one line of what a session to it would meet.
 *
 * The line is short enough to stand under a mark in a row. A computer that
 * answers says how it is reached and how far it is; the name of the server on
 * it is the expected one and is a hover away, as is when an offline computer
 * was last seen. One that is on and not answering names its server, since
 * that is what to go and look at.
 */
function computerNode(computer: RdComputerView): WireFlowNode {
  const reach = reachability(computer)
  const server = protocolLabel(computer.protocol, computer.os)
  const path = computer.peer
    ? computer.peer.direct
      ? "direct"
      : computer.peer.relay
        ? `relayed via ${computer.peer.relay}`
        : undefined
    : undefined
  const seen = computer.peer?.lastSeen
    ? `seen ${relativeTime(isoOf(computer.peer.lastSeen))}`
    : undefined
  const open = [path, latency(computer.reach.latencyMs)].filter(Boolean).join(" · ")
  return {
    id: computer.id,
    name: computer.name,
    href: sessionHref(computer.id),
    mark: (size) => <ComputerMark os={computer.os} reach={reach} size={size} />,
    title: (
      <span className="block truncate" title={computer.name}>
        {computer.name}
      </span>
    ),
    hint:
      reach.kind === "live" ? (
        <span className="font-medium text-success">in a session</span>
      ) : reach.kind === "open" ? (
        <span className="numeric block truncate" title={[server, open].filter(Boolean).join(" · ")}>
          {open || server}
        </span>
      ) : reach.kind === "offline" ? (
        <span title={seen}>offline</span>
      ) : (
        <span className="block text-balance text-warning">{server} not answering</span>
      ),
    line:
      reach.kind === "live"
        ? { state: "up", moving: true, duration: 2.2 }
        : reach.kind === "open"
          ? { state: "up" }
          : reach.kind === "offline"
            ? { state: "down" }
            : { state: "warning", dashed: true },
  }
}
