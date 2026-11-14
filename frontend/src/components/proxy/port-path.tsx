"use client"

import { useRef } from "react"
import { FirewallCheck, Globe, NetworkDevice, Router } from "@/components/icons"
import { cn } from "@/lib/utils"
import { plural } from "@/lib/format"
import { ProductGlyph, hasProductLogo } from "@/components/product-logo"
import { DashboardMark } from "@/components/backups/marks"
import { AnimatedBeam } from "@/components/ui/animated-beam"
import { WireHost, WireMark, WireNode } from "@/components/deploy/wire"
import {
  LANE_WORDS,
  listenerProduct,
  ownerTitle,
  reachLane,
  reachVerdict,
  socketClients,
  type Socket,
} from "@/components/proxy/ports"
import { firewallWords } from "@/components/proxy/ports-firewall"
import { addressLine } from "@/components/proxy/port-marks"

const LANE_PRODUCT: Record<string, string> = {
  tailnet: "tailscale",
  vpn: "wireguard",
  docker: "docker",
}

type Tone = "default" | "warning" | "danger" | "success"

/**
 * The way to one port, left to right: who can reach it, what the firewall
 * does at the door, the socket, and the program behind it. The wires take
 * the socket's colour — red for a database the internet reaches — and a
 * pulse runs along them while somebody is connected. A socket on loopback
 * has no door: this server reaches it directly. Below `lg` the four stand
 * in a column with no wires, each saying its part in words.
 */
export function PortPath({ socket }: { socket: Socket }) {
  const container = useRef<HTMLDivElement>(null)
  const from = useRef<HTMLDivElement>(null)
  const door = useRef<HTMLDivElement>(null)
  const port = useRef<HTMLDivElement>(null)
  const owner = useRef<HTMLDivElement>(null)
  const lane = reachLane(socket)
  const verdict = reachVerdict(socket)
  const tone: Tone =
    verdict === "critical" ? "danger" : verdict === "warning" ? "warning" : "default"
  const fw = socket.firewall ? firewallWords(socket.firewall) : undefined
  const doorTone: Tone =
    fw?.verdict === "ok" ? "success" : fw?.verdict === "warning" ? "warning" : "default"
  const clients = socketClients(socket)
  const busy = (clients?.count ?? 0) > 0
  const product = listenerProduct(socket)
  const laneProduct = LANE_PRODUCT[lane]
  const wires: [React.RefObject<HTMLDivElement | null>, React.RefObject<HTMLDivElement | null>][] =
    fw
      ? [
          [from, door],
          [door, port],
          [port, owner],
        ]
      : [
          [from, port],
          [port, owner],
        ]

  return (
    <div data-slot="port-path" className="relative animate-rise pb-8 lg:pb-14">
      <div ref={container} className="relative">
        {wires.map(([a, b], index) => (
          <AnimatedBeam
            key={index}
            containerRef={container}
            fromRef={a}
            toRef={b}
            startXOffset={24}
            endXOffset={-24}
            still={!busy}
            tone={tone === "default" ? "default" : tone === "danger" ? "danger" : "warning"}
            duration={3}
            delay={index * 0.4}
            className="max-lg:hidden"
          />
        ))}
        <ol
          aria-label="The way to this port"
          className={cn(
            "relative grid gap-y-6 lg:items-center lg:justify-between",
            fw ? "lg:grid-cols-4" : "lg:grid-cols-3",
          )}
        >
          <li className="min-w-0">
            <WireNode
              nodeRef={from}
              align="center"
              mark={
                lane === "local" ? (
                  <WireHost size="md" />
                ) : (
                  <WireMark
                    tone={tone === "default" ? "logo" : tone}
                    size="md"
                    className={cn(tone === "default" && "")}
                  >
                    {laneProduct && tone === "default" && hasProductLogo(laneProduct) ? (
                      <ProductGlyph id={laneProduct} />
                    ) : lane === "internet" ? (
                      <Globe aria-hidden />
                    ) : (
                      <NetworkDevice aria-hidden />
                    )}
                  </WireMark>
                )
              }
              eyebrow="Who can reach it"
              title={LANE_WORDS[lane]}
              hint={socket.interface && <span className="font-mono">{socket.interface}</span>}
            />
          </li>
          {fw && (
            <li className="min-w-0">
              <WireNode
                nodeRef={door}
                align="center"
                mark={
                  <WireMark tone={doorTone === "default" ? "neutral" : doorTone} size="md">
                    <FirewallCheck aria-hidden />
                  </WireMark>
                }
                eyebrow="Firewall"
                title={fw.label}
              />
            </li>
          )}
          <li className="min-w-0">
            <WireNode
              nodeRef={port}
              align="center"
              mark={
                <WireMark tone={tone === "default" ? "neutral" : tone} size="md">
                  <span className="numeric font-mono text-xs font-semibold">{socket.port}</span>
                </WireMark>
              }
              eyebrow={socket.protocol.toUpperCase()}
              title={<span className="font-mono text-hint">{addressLine(socket)}</span>}
              hint={
                clients && (
                  <span className="numeric">
                    {clients.count > 0
                      ? plural(clients.count, "client") + " now"
                      : "nobody connected"}
                  </span>
                )
              }
            />
          </li>
          <li className="min-w-0">
            <WireNode
              nodeRef={owner}
              align="center"
              mark={
                socket.self ? (
                  <DashboardMark />
                ) : (
                  <WireMark tone="logo" size="md">
                    {product && hasProductLogo(product) ? (
                      <ProductGlyph id={product} />
                    ) : (
                      <Router aria-hidden />
                    )}
                  </WireMark>
                )
              }
              eyebrow="Listening"
              title={ownerTitle(socket)}
              hint={socket.user && <span className="font-mono">{socket.user}</span>}
            />
          </li>
        </ol>
      </div>
    </div>
  )
}
