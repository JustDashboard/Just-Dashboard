import type { DotTone } from "@/components/status-dot"
import type { WGEndpointEvidence, WGEvent, WGPeer, WGQuota, WGSiteVerification } from "@/lib/types"
import { relativeTime } from "@/lib/format"
import { literalAddress } from "../draft-input"

export const HISTORY_WINDOWS = ["6h", "24h", "7d"] as const
export type HistoryWindow = (typeof HISTORY_WINDOWS)[number]

/** A peer's handshake as the word and the tone its Status takes. */
export function handshakeReading(
  peer: Pick<WGPeer, "handshakeState" | "online" | "latestHandshake">,
): { label: string; tone: DotTone } {
  switch (peer.handshakeState) {
    case "online":
      return { label: "Online", tone: "running" }
    case "stale":
      return { label: "Handshake stale", tone: "warning" }
    case "idle":
      return { label: "Quiet", tone: "stopped" }
    case "never":
      return { label: "Never connected", tone: "unknown" }
  }
  if (peer.online) return { label: "Online", tone: "running" }
  return peer.latestHandshake
    ? { label: "Quiet", tone: "stopped" }
    : { label: "Never connected", tone: "unknown" }
}

const EVENT_LABEL: Record<string, string> = {
  created: "Created",
  create_failed: "Creation failed",
  up: "Brought up",
  down: "Taken down",
  exit: "Exit changed",
  removed: "Removed and archived",
  restored: "Restored from the archive",
  restore_failed: "Restore failed",
  peer_added: "Peer added",
  peer_add_failed: "Peer not added",
  peer_removed: "Peer removed",
  peer_remove_failed: "Peer not removed",
  peer_edited: "Peer edited",
  peer_edit_failed: "Peer edit failed",
  config_forgotten: "Saved configuration forgotten",
  quota_set: "Budget set",
  quota_cleared: "Budget cleared",
  handshake_stale: "Handshake went stale",
  handshake_recovered: "Handshake recovered",
  transport_captured: "Transport routed into a tunnel",
  transport_restored: "Transport native again",
  site_verified: "Site verified",
  firewall_opened: "Firewall opened",
  firewall_closed: "Firewall opening removed",
  firewall_unchanged: "Firewall unchanged",
}

/** A lifecycle event's kind as words; an unknown kind keeps its own name. */
export function eventLabel(kind: string) {
  return EVENT_LABEL[kind] ?? kind.replaceAll("_", " ")
}

export function outcomeTone(outcome: WGEvent["outcome"]): DotTone {
  switch (outcome) {
    case "ok":
      return "running"
    case "recovered":
      return "notice"
    case "degraded":
      return "warning"
    case "failed":
      return "danger"
  }
}

const UNITS: Record<string, number> = { k: 2 ** 10, m: 2 ** 20, g: 2 ** 30, t: 2 ** 40 }

/**
 * A budget typed as "50 GB" or "500 MiB", in bytes; undefined when it is not
 * one. Both spellings are binary, as `bytes` prints them, so a budget reads
 * back as it was typed.
 */
export function parseBudget(raw: string): number | undefined {
  const match = raw.trim().match(/^(\d+(?:\.\d+)?)\s*([kmgt])i?b$/i)
  if (!match) return undefined
  const value = Math.round(Number(match[1]) * UNITS[match[2].toLowerCase()])
  return value >= UNITS.m && value <= 2 ** 50 ? value : undefined
}

/** How much of a budget is used, 0–100; a passed budget reads 100. */
export function quotaPercent(q: Pick<WGQuota, "usedBytes" | "limitBytes">) {
  if (q.limitBytes <= 0) return 0
  return Math.min(100, (q.usedBytes / q.limitBytes) * 100)
}

/** Comma- or space-separated networks as the API takes them. */
export function networkList(raw: string) {
  return raw
    .split(/[\s,]+/)
    .map((s) => s.trim())
    .filter(Boolean)
}

/** The first network in a list that is not an address with a prefix length. */
export function networkProblem(raw: string): string | undefined {
  for (const net of networkList(raw)) {
    const [address, bits, extra] = net.split("/")
    const parsed = literalAddress(address)
    const max = parsed?.length === 4 ? 32 : 128
    if (
      !parsed ||
      extra !== undefined ||
      bits === undefined ||
      !/^\d{1,3}$/.test(bits) ||
      Number(bits) > max
    )
      return `${net} is not a network such as 192.168.1.0/24.`
    if (Number(bits) === 0) return `${net} is every address; that is the full tunnel option.`
  }
}

/**
 * A device whose recorded routes are a full tunnel, or whose routes are not
 * recorded: the kill-switch file is offered and the server says no to a split
 * tunnel, rather than the page guessing from nothing.
 */
export function killSwitchOffered(peer: Pick<WGPeer, "kind" | "clientRoutes" | "id">) {
  if (peer.kind !== "device" || peer.id === 0) return false
  return !peer.clientRoutes || peer.clientRoutes.includes("0.0.0.0/0")
}

export type PeerDraft = {
  name: string
  keepalive: string
  endpoint: string
  remote: string
  fullTunnel: boolean
  share: string
}

/** The edit's request: only what changed, so an unchanged field is left alone. */
export function peerEdit(
  peer: WGPeer,
  draft: PeerDraft,
  tunnelNetworks: string[],
): Record<string, unknown> {
  const body: Record<string, unknown> = {}
  if (draft.name.trim() && draft.name.trim() !== peer.name) body.name = draft.name.trim()
  if (draft.keepalive.trim() !== String(peer.keepalive))
    body.keepalive = Number(draft.keepalive.trim())
  if (peer.kind === "site") {
    if (draft.endpoint.trim() !== (peer.endpoint ?? "")) body.endpoint = draft.endpoint.trim()
    const before = siteNetworks(peer).join(",")
    const after = networkList(draft.remote)
    if (after.join(",") !== before) body.remoteNetworks = after
  } else {
    const routes = peer.clientRoutes
    const wasFull = routes?.includes("0.0.0.0/0") ?? false
    const share = networkList(draft.share)
    if (!routes || draft.fullTunnel !== wasFull) body.fullTunnel = draft.fullTunnel
    if (!routes || share.join(",") !== sharedOf(peer, tunnelNetworks).join(","))
      body.shareNetworks = share
  }
  return body
}

/** A site's networks: what it is routed besides its own tunnel address. */
export function siteNetworks(peer: Pick<WGPeer, "allowedIps" | "address" | "address6">) {
  const own = [peer.address, peer.address6].map((a) => a?.split("/")[0])
  return peer.allowedIps.filter((ip) => !own.includes(ip.split("/")[0]))
}

/** What a device shares beyond the tunnel's own networks and the default routes. */
export function sharedOf(peer: Pick<WGPeer, "clientRoutes">, tunnelNetworks: string[]) {
  return (peer.clientRoutes ?? []).filter(
    (route) => route !== "0.0.0.0/0" && route !== "::/0" && !tunnelNetworks.includes(route),
  )
}

/** Whether an edit takes something away, which the server asks the destructive budget for. */
export function editWithdraws(peer: WGPeer, body: Record<string, unknown>) {
  if (peer.kind !== "site") return false
  const after = (body.remoteNetworks as string[] | undefined) ?? siteNetworks(peer)
  return (
    siteNetworks(peer).some((net) => !after.includes(net)) ||
    (body.endpoint !== undefined && body.endpoint !== peer.endpoint)
  )
}

export const VERDICT_LABEL: Record<WGEndpointEvidence["verdict"], string> = {
  on_host_public: "A public address this host holds",
  provider_mapped: "Presumably the provider's public address",
  elsewhere: "Not an address of this host",
  private: "A private address",
  unresolved: "Does not resolve",
}

export function verdictTone(verdict: WGEndpointEvidence["verdict"]): DotTone {
  switch (verdict) {
    case "on_host_public":
      return "running"
    case "provider_mapped":
    case "private":
      return "notice"
    default:
      return "warning"
  }
}

export function siteOutcomeTone(outcome: WGSiteVerification["outcome"]): DotTone {
  return outcome === "verified" ? "running" : outcome === "partial" ? "warning" : "danger"
}

/** The tunnel's MTU, when one is typed: 1280 (IPv6's floor) to 9000. */
export function mtuProblem(raw: string) {
  const text = raw.trim()
  if (!text) return undefined
  if (!/^\d{4}$/.test(text) || Number(text) < 1280 || Number(text) > 9000)
    return "Use a whole number from 1280 to 9000."
}

/** Resolvers typed by hand: addresses only, as the server writes them. */
export function resolversProblem(raw: string) {
  const list = networkList(raw)
  if (list.length > 8) return "Use at most eight resolvers."
  const bad = list.find((r) => !literalAddress(r))
  if (bad) return `${bad} is not an IPv4 or IPv6 address.`
}

/** A key's expiry as words, read against the clock when it is drawn, as relativeTime is. */
export function keyExpiry(unix: number, now = Date.now()) {
  const when = relativeTime(new Date(unix * 1000).toISOString())
  return unix * 1000 <= now
    ? { expired: true, label: `key expired ${when}` }
    : { expired: false, label: `key expires ${when}` }
}
