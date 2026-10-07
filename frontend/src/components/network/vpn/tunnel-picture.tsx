"use client"

import { createRef, useMemo, useRef, useState, type RefObject } from "react"
import { DesktopDevice, Location, Plus } from "@/components/icons"
import { bytes, relativeTime } from "@/lib/format"
import type { WGInterface, WGPeer } from "@/lib/types"
import { cn } from "@/lib/utils"
import { ProductGlyph } from "@/components/product-logo"
import { WireMark, WireNode, WirePlaceholder } from "@/components/deploy/wire"
import { AnimatedBeam } from "@/components/ui/animated-beam"

/**
 * One WireGuard tunnel as the server and the peers it carries: the devices
 * that dial in on the left — phones and laptops — the tunnel in the middle
 * with its port, its network and whether it is an exit, and the sites it
 * joins on the right, each with the networks it routes. A peer that
 * handshook in the last three minutes carries a moving wire; one that has
 * been seen but is quiet has a still one; one that never connected is
 * dashed. Pressing a peer opens it — its configuration and QR code — and
 * a dashed ring at the foot of each side adds another.
 */
export function TunnelPicture({
  tunnel,
  onPeer,
  onAdd,
}: {
  tunnel: WGInterface
  onPeer: (peer: WGPeer) => void
  onAdd?: (kind: "device" | "site") => void
}) {
  const container = useRef<HTMLDivElement>(null)
  const hub = useRef<HTMLDivElement>(null)
  const [focus, setFocus] = useState<string | null>(null)
  const devices = tunnel.peers.filter((p) => p.kind !== "site")
  const sites = tunnel.peers.filter((p) => p.kind === "site")
  const ids = tunnel.peers.map((p) => p.publicKey).join("\n")
  const refs = useMemo(() => {
    const map = new Map<string, RefObject<HTMLDivElement | null>>()
    for (const id of ids.split("\n")) if (id) map.set(id, createRef<HTMLDivElement>())
    return map
  }, [ids])
  const port = 22

  return (
    <div className="relative animate-rise py-4">
      <div aria-hidden className="wire-grid pointer-events-none absolute -inset-x-4 inset-y-0" />
      <div ref={container} className="relative">
        {tunnel.peers.map((peer, index) => {
          const ref = refs.get(peer.publicKey)
          if (!ref) return null
          const site = peer.kind === "site"
          const lit = focus === null || focus === peer.publicKey
          return (
            <AnimatedBeam
              key={peer.publicKey}
              containerRef={container}
              fromRef={site ? hub : ref}
              toRef={site ? ref : hub}
              shape="s"
              startXOffset={port}
              endXOffset={-port}
              still={!peer.online || !tunnel.up}
              dashed={peer.latestHandshake === 0}
              reverse={!site && peer.txBytes > peer.rxBytes}
              duration={peer.online ? 2.2 : 3}
              delay={(index % 5) * 0.3}
              className={cn("transition-opacity max-lg:hidden", !lit && "opacity-15")}
            />
          )
        })}
        <div className="relative grid gap-y-8 lg:grid-cols-[minmax(0,1fr)_clamp(2.5rem,6vw,6rem)_minmax(0,0.8fr)_clamp(2.5rem,6vw,6rem)_minmax(0,1fr)] lg:items-center">
          <Side
            title="Devices"
            peers={devices}
            refs={refs}
            focus={focus}
            setFocus={setFocus}
            onPeer={onPeer}
            align="end"
            add={onAdd && tunnel.managed ? () => onAdd("device") : undefined}
            addLabel="Add a device"
            addHint="a phone or a laptop, with a QR code"
          />
          <div aria-hidden className="max-lg:hidden" />
          <div className="flex min-w-0 justify-center">
            <WireNode
              nodeRef={hub}
              align="center"
              mark={
                <WireMark tone={tunnel.up ? "logo" : "neutral"} shape="square">
                  <ProductGlyph id="wireguard" />
                </WireMark>
              }
              eyebrow={`${tunnel.name} · udp ${tunnel.listenPort || "—"}`}
              title={
                <span className="font-mono">{tunnel.subnet || tunnel.addresses[0] || "—"}</span>
              }
              hint={
                <>
                  <span className={cn("block", !tunnel.up && "text-warning")}>
                    {!tunnel.up
                      ? "down"
                      : tunnel.exitNode
                        ? "exit node · routes the internet"
                        : "private network only"}
                  </span>
                  {tunnel.endpoint && (
                    <span className="block truncate font-mono">{tunnel.endpoint}</span>
                  )}
                </>
              }
            />
          </div>
          <div aria-hidden className="max-lg:hidden" />
          <Side
            title="Sites"
            peers={sites}
            refs={refs}
            focus={focus}
            setFocus={setFocus}
            onPeer={onPeer}
            align="start"
            add={onAdd && tunnel.managed ? () => onAdd("site") : undefined}
            addLabel="Join a site"
            addHint="another server or an office network"
          />
        </div>
      </div>
    </div>
  )
}

function Side({
  title,
  peers,
  refs,
  focus,
  setFocus,
  onPeer,
  align,
  add,
  addLabel,
  addHint,
}: {
  title: string
  peers: WGPeer[]
  refs: Map<string, RefObject<HTMLDivElement | null>>
  focus: string | null
  setFocus: (next: string | null | ((held: string | null) => string | null)) => void
  onPeer: (peer: WGPeer) => void
  align: "start" | "end"
  add?: () => void
  addLabel: string
  addHint: string
}) {
  const site = title === "Sites"
  return (
    <section aria-label={title} className={cn("min-w-0", align === "end" && "lg:text-right")}>
      <p className="eyebrow mb-4 lg:hidden">{title}</p>
      <ol className="flex flex-col gap-5">
        {peers.map((peer) => (
          <li
            key={peer.publicKey}
            onPointerEnter={() => setFocus(peer.publicKey)}
            onPointerLeave={() => setFocus((held) => (held === peer.publicKey ? null : held))}
            className={cn(
              "min-w-0 transition-opacity",
              focus !== null && focus !== peer.publicKey && "opacity-40",
            )}
          >
            <WireNode
              nodeRef={refs.get(peer.publicKey)}
              align={align}
              mark={
                <button
                  type="button"
                  onClick={() => onPeer(peer)}
                  aria-label={`Open ${peer.name || peer.address}`}
                  className="rounded-xl focus-ring"
                >
                  <WireMark tone={peer.online ? "success" : "neutral"} shape="square" size="md">
                    {site ? <Location aria-hidden /> : <DesktopDevice aria-hidden />}
                  </WireMark>
                </button>
              }
              eyebrow={
                peer.online
                  ? "online"
                  : peer.latestHandshake
                    ? `seen ${relativeTime(new Date(peer.latestHandshake * 1000).toISOString())}`
                    : "never connected"
              }
              title={peer.name || <span className="font-mono">{peer.address}</span>}
              hint={
                <>
                  <span className="block truncate font-mono">
                    {site
                      ? peer.allowedIps.filter((ip) => ip !== peer.address).join(", ") ||
                        peer.address
                      : peer.address}
                  </span>
                  {(peer.rxBytes > 0 || peer.txBytes > 0) && (
                    <span className="numeric block font-mono text-micro">
                      <span className="text-[var(--chart-5)]">↓ {bytes(peer.rxBytes)}</span>
                      <span className="mx-1.5 text-muted-foreground/50">·</span>
                      <span className="text-[var(--chart-2)]">↑ {bytes(peer.txBytes)}</span>
                    </span>
                  )}
                </>
              }
            />
          </li>
        ))}
        {add && (
          <li className="min-w-0">
            <WireNode
              align={align}
              mark={
                <button
                  type="button"
                  onClick={add}
                  aria-label={addLabel}
                  className="rounded-full focus-ring"
                >
                  <WirePlaceholder size="md">
                    <Plus aria-hidden />
                  </WirePlaceholder>
                </button>
              }
              title={<span className="text-muted-foreground">{addLabel}</span>}
              hint={addHint}
            />
          </li>
        )}
      </ol>
    </section>
  )
}
