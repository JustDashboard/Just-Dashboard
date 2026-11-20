"use client"

import { InOut } from "@/components/in-out"
import { useEffect, useMemo } from "react"
import { DesktopDevice, Location, Plus, WarningFill } from "@/components/icons"
import { relativeTime } from "@/lib/format"
import type { WGAlert, WGInterface, WGPeer } from "@/lib/types"
import { cn } from "@/lib/utils"
import { useViewState } from "@/lib/view-state"
import { Bone } from "@/components/bone"
import { ProductGlyph } from "@/components/product-logo"
import { WireMark, WirePlaceholder } from "@/components/wire/wire"
import {
  WireFlow,
  type WireFlowColumn,
  type WireFlowNode,
  type WireFlowWaiting,
} from "@/components/wire/wire-flow"
import { fitColumn } from "@/components/wire/wire-flow-model"
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
 * What one node of the picture says, for its waiting row: a peer with the
 * lines under its name, the ring that adds one, or the "N more" tile.
 */
type Said = { kind: "peer"; carried: boolean; finding: boolean } | { kind: "ring" | "more" }
type Shape = { devices: Said[]; sites: Said[] }

const RING: Said = { kind: "ring" }
const MORE: Said = { kind: "more" }
const PEER: Said = { kind: "peer", carried: true, finding: false }

/** A tunnel nobody has opened here yet waits as three devices and two sites. */
const UNSEEN: Shape = { devices: [PEER, PEER, PEER], sites: [PEER, PEER] }

/** A node's waiting row: the lines it said, its in and out at the size they are set in. */
function waitingAs(said: Said): WireFlowWaiting {
  if (said.kind !== "peer") return { eyebrow: false, hint: said.kind === "ring" ? 1 : 0 }
  return {
    hint: said.finding ? 2 : 1,
    foot: said.carried ? (
      <span className="block text-micro">
        <Bone className="w-20" />
      </span>
    ) : undefined,
  }
}

/**
 * Where the shape of the last picture drawn is kept: the VPN page's first
 * tunnel, cut to what a side draws there, and a tunnel's own page, which
 * draws every peer.
 */
const shapeKey = (whole: boolean) => `network.vpn.picture.${whole ? "tunnel" : "list"}`

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
 *
 * The nodes each side drew and the lines each said are kept for the next
 * visit (`TunnelPictureWaiting`). On a wide page a side is rows of nodes, so
 * four devices are two rows where three are one: a waiting picture that
 * guessed three stood half as tall as the one that landed on it.
 */
export function TunnelPicture({
  tunnel,
  onPeer,
  onAdd,
  max,
  remember = true,
}: {
  tunnel: WGInterface
  onPeer: (peer: WGPeer) => void
  onAdd?: (kind: "device" | "site") => void
  /** Draw this many peers a side and send the rest to the tunnel's page. */
  max?: number
  /** Keep its shape for the waiting picture: the first tunnel of a page of several. */
  remember?: boolean
}) {
  const columns = useMemo(() => {
    const side = (kind: "device" | "site"): WireFlowColumn & { said: Said[] } => {
      const site = kind === "site"
      const peers = drawnPeers(
        tunnel.peers.filter((p) => (p.kind === "site") === site),
        tunnel.alerts,
      )
      const nodes: WireFlowNode[] = peers.map((peer) => peerNode(tunnel, peer, onPeer))
      const said: Said[] = peers.map((peer) => ({
        kind: "peer",
        carried: peer.rxBytes > 0 || peer.txBytes > 0,
        finding: peerFindings(tunnel, peer).length > 0,
      }))
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
      if (nodes.length > said.length) said.push(RING)
      const fit = fitColumn(said, max)
      return {
        title: site ? "Sites" : "Devices",
        nodes,
        max,
        more: { href: tunnelHref(tunnel.name) },
        empty: site ? "No site joined" : "No device yet",
        said: fit.hidden > 0 ? [...fit.shown, MORE] : fit.shown,
      }
    }
    return { devices: side("device"), sites: side("site") }
  }, [tunnel, onPeer, onAdd, max])
  const loose = tunnelFindings(tunnel)
  const [kept, keep] = useViewState<Shape>(shapeKey(max === undefined), UNSEEN)
  const shape = useMemo(
    () => ({ devices: columns.devices.said, sites: columns.sites.said }),
    [columns],
  )
  useEffect(() => {
    if (remember && JSON.stringify(kept) !== JSON.stringify(shape)) keep(shape)
  }, [remember, kept, shape, keep])

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

/**
 * A tunnel's picture before it is read, on the shape the last one had: as
 * many nodes a side, each with the lines it said, so the picture that lands
 * is the height of the one that waited. `whole` is a tunnel's own page.
 */
export function TunnelPictureWaiting({ whole = false }: { whole?: boolean }) {
  const [kept] = useViewState<Shape>(shapeKey(whole), UNSEEN)
  const side = (title: string, said: Said[]): WireFlowColumn => ({
    title,
    nodes: [],
    loadingRows: said.length,
    loadingLines: (row) => waitingAs(said[row] ?? PEER),
  })
  return (
    <WireFlow
      loading
      start={side("Devices", kept.devices)}
      end={side("Sites", kept.sites)}
      hub={{
        mark: (
          <WireMark tone="neutral" shape="square">
            <ProductGlyph id="wireguard" />
          </WireMark>
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
  // A site's networks are cut where the node has no room for them all, and
  // whole in the line's title.
  const reach = site
    ? peer.allowedIps.filter((ip) => ip !== peer.address).join(", ") || peer.address
    : peer.address
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
        <span className="block truncate font-mono" title={reach}>
          {reach}
        </span>
        {(peer.rxBytes > 0 || peer.txBytes > 0) && (
          <InOut rx={peer.rxBytes} tx={peer.txBytes} unit="bytes" className="block text-micro" />
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
