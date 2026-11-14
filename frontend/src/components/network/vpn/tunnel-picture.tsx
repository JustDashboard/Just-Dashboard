"use client"

import { useMemo } from "react"
import { DesktopDevice, Location, Plus, WarningFill } from "@/components/icons"
import { bytes, relativeTime } from "@/lib/format"
import type { WGAlert, WGInterface, WGPeer } from "@/lib/types"
import { cn } from "@/lib/utils"
import { ProductGlyph } from "@/components/product-logo"
import { WireMark, WirePlaceholder } from "@/components/deploy/wire"
import { WireFlow, type WireFlowColumn, type WireFlowNode } from "@/components/wire-flow"
import {
  drawnPeers,
  findingWords,
  peerFindings,
  peerLine,
  peerSeen,
  peerTone,
  tunnelFindings,
  tunnelHref,
} from "./tunnel-reading"

/** How many peers a side draws on the VPN page; the tunnel's own page draws them all. */
export const PEERS_DRAWN = 6

/**
 * One WireGuard tunnel as the server and the peers it carries (`WireFlow`):
 * the devices that dial in on the left, the tunnel in the middle with where
 * its clients dial and whether it is an exit, and the sites it joins on the
 * right, each with the networks it routes. A peer that handshook in the last
 * three minutes carries a moving wire; one that is quiet has a still one; one
 * that never connected is dashed. Pressing a peer opens it, and a dashed ring
 * at the foot of each side adds another.
 *
 * What needs a look is said on the peer it is about: an amber mark in the
 * corner of its tile and the finding in a few words under it, where a box
 * over the picture once listed them. The sentence is in the peer's sheet.
 */
export function TunnelPicture({
  tunnel,
  onPeer,
  onAdd,
  max,
}: {
  tunnel: WGInterface
  onPeer: (peer: WGPeer) => void
  onAdd?: (kind: "device" | "site") => void
  /** Draw this many peers a side and send the rest to the tunnel's page. */
  max?: number
}) {
  const columns = useMemo(() => {
    const side = (kind: "device" | "site"): WireFlowColumn => {
      const site = kind === "site"
      const peers = drawnPeers(
        tunnel.peers.filter((p) => (p.kind === "site") === site),
        tunnel.alerts,
      )
      const nodes: WireFlowNode[] = peers.map((peer) => peerNode(tunnel, peer, onPeer))
      // The ring is left out of a column that is already cut short: counted
      // with the peers it would make the "more" one too many, and the
      // command on the tunnel's line adds a peer either way.
      const cut = max !== undefined && peers.length > max + 1
      if (onAdd && tunnel.managed && !cut)
        nodes.push({
          id: `add:${kind}`,
          placeholder: true,
          mark: (
            <WirePlaceholder size="md">
              <Plus aria-hidden />
            </WirePlaceholder>
          ),
          title: (
            <span className="text-muted-foreground">{site ? "Join a site" : "Add a device"}</span>
          ),
          name: site ? "Join a site" : "Add a device",
          hint: site
            ? "another server or an office network"
            : "a phone or a laptop, with a QR code",
          onPress: () => onAdd(kind),
          line: { state: "idle" },
        })
      return {
        title: site ? "Sites" : "Devices",
        nodes,
        max,
        more: { href: tunnelHref(tunnel.name) },
        empty: site ? "No site joined" : "No device yet",
      }
    }
    return { devices: side("device"), sites: side("site") }
  }, [tunnel, onPeer, onAdd, max])
  const loose = tunnelFindings(tunnel)

  return (
    <WireFlow
      className="animate-rise"
      start={columns.devices}
      end={columns.sites}
      hub={{
        mark: (
          <WireMark tone={tunnel.up ? "logo" : "neutral"} shape="square">
            <ProductGlyph id="wireguard" />
          </WireMark>
        ),
        eyebrow: tunnel.endpoint ? "Clients dial" : tunnel.name,
        title: <span className="font-mono">{tunnel.endpoint || tunnel.addresses[0] || "—"}</span>,
        hint: (
          <>
            <span className={cn("block", !tunnel.up && "text-warning")}>
              {!tunnel.up
                ? "down"
                : tunnel.exitNode
                  ? "exit node · routes the internet"
                  : "private network only"}
            </span>
            {loose.length > 0 && <Findings findings={loose} label={`${tunnel.name} findings`} />}
          </>
        ),
      }}
    />
  )
}

function peerNode(tunnel: WGInterface, peer: WGPeer, onPeer: (peer: WGPeer) => void): WireFlowNode {
  const site = peer.kind === "site"
  const findings = peerFindings(tunnel, peer)
  const tone = peerTone(peer, findings.length)
  const name = peer.name || peer.address
  return {
    id: peer.publicKey,
    mark: (size) => (
      <span className="relative flex">
        <WireMark tone={tone} shape="square" size={size}>
          {site ? <Location aria-hidden /> : <DesktopDevice aria-hidden />}
        </WireMark>
        {findings.length > 0 && (
          <span
            aria-hidden
            data-slot="peer-finding-mark"
            className="absolute -top-1 -right-1 flex size-4 items-center justify-center rounded-full bg-background text-warning"
          >
            <WarningFill className="size-3.5" />
          </span>
        )}
      </span>
    ),
    onPress: () => onPeer(peer),
    label: `Open ${name}`,
    name,
    eyebrow: peerSeen(peer),
    title: peer.name || <span className="font-mono">{peer.address}</span>,
    hint: (
      <>
        <span className="block truncate font-mono">
          {site
            ? peer.allowedIps.filter((ip) => ip !== peer.address).join(", ") || peer.address
            : peer.address}
        </span>
        {(peer.rxBytes > 0 || peer.txBytes > 0) && (
          <span className="numeric block font-mono text-micro">
            <span className="text-[var(--chart-5)]">↓ {bytes(peer.rxBytes)}</span>
            <span className="mx-1.5 text-muted-foreground/50">·</span>
            <span className="text-[var(--chart-2)]">↑ {bytes(peer.txBytes)}</span>
          </span>
        )}
        {findings.length > 0 && <Findings findings={findings} label={`${name} findings`} />}
      </>
    ),
    line: peerLine(tunnel, peer),
  }
}

/** What needs a look, in a few amber words; the whole sentence is the line's title. */
function Findings({ findings, label }: { findings: WGAlert[]; label: string }) {
  return (
    <span
      role="note"
      aria-label={label}
      title={findings.map(findingSentence).join("\n")}
      className="block text-warning"
    >
      {findings.map(findingWords).join(" · ")}
    </span>
  )
}

/** A finding as its sentence and since when. */
export function findingSentence(finding: WGAlert) {
  return finding.since
    ? `${finding.message} · since ${relativeTime(new Date(finding.since * 1000).toISOString())}`
    : finding.message
}
