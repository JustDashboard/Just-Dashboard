import type { DotTone } from "@/components/status-dot"
import type { WireLine } from "@/components/wire-flow-model"
import type {
  TailscalePeer,
  TailscaleView,
  WGAlert,
  WGArchived,
  WGFamilyState,
  WGInterface,
  WGPeer,
} from "@/lib/types"
import { relativeTime } from "@/lib/format"
import { literalAddress } from "../draft-input"

/**
 * What the VPN pages say about a tunnel, a peer, the tailnet and its machines,
 * kept apart from React so it is tested in `bun test src`.
 */

export const TUNNEL_TABS = ["overview", "traffic", "history"] as const
export type TunnelTab = (typeof TUNNEL_TABS)[number]

/** A tunnel's own page, on the view asked for. */
export function tunnelHref(name: string, tab: string = "overview") {
  const path = `/network/vpn/${encodeURIComponent(name)}`
  return tab === "overview" ? path : `${path}?tab=${encodeURIComponent(tab)}`
}

type Reading = { tone: DotTone; label: string; live?: boolean }

/** A tunnel's state as the dot and words beside its name. */
export function tunnelState(tunnel: Pick<WGInterface, "up" | "peers">): Reading {
  if (!tunnel.up) return { tone: "stopped", label: "Down" }
  if (tunnel.peers.length === 0) return { tone: "running", label: "Up, no peers yet" }
  const online = tunnel.peers.filter((p) => p.online).length
  return {
    tone: "running",
    label: `${online} of ${tunnel.peers.length} online`,
    live: online > 0,
  }
}

/** Everything the tunnel's peers moved since it came up. */
export function tunnelMoved(tunnel: Pick<WGInterface, "peers">) {
  return tunnel.peers.reduce((sum, p) => sum + p.rxBytes + p.txBytes, 0)
}

const FINDING: Record<WGAlert["kind"], string> = {
  stale_handshake: "handshake stale",
  transport_captured: "transport inside a tunnel",
  quota_warning: "near its budget",
  quota_exceeded: "past its budget",
}

/** A finding as the few words its peer's node carries; an unknown kind keeps its own name. */
export function findingWords(alert: Pick<WGAlert, "kind">) {
  return FINDING[alert.kind] ?? String(alert.kind).replaceAll("_", " ")
}

/** The findings about one peer. */
export function peerFindings(tunnel: Pick<WGInterface, "alerts">, peer: Pick<WGPeer, "publicKey">) {
  return (tunnel.alerts ?? []).filter((a) => a.peer === peer.publicKey)
}

/** The findings no drawn peer carries: about the tunnel itself, or a peer that has since gone. */
export function tunnelFindings(tunnel: Pick<WGInterface, "alerts" | "peers">) {
  const keys = new Set(tunnel.peers.map((p) => p.publicKey))
  return (tunnel.alerts ?? []).filter((a) => !a.peer || !keys.has(a.peer))
}

/**
 * The order a column draws its peers in: the ones with a finding first, so a
 * column cut to what fits never hides the peer that needs a look, and the
 * rest as the tunnel's file lists them.
 */
export function drawnPeers<T extends Pick<WGPeer, "publicKey">>(
  peers: T[],
  alerts: Pick<WGAlert, "peer">[] | undefined,
): T[] {
  const flagged = new Set((alerts ?? []).map((a) => a.peer))
  return [
    ...peers.filter((p) => flagged.has(p.publicKey)),
    ...peers.filter((p) => !flagged.has(p.publicKey)),
  ]
}

/** When a peer was last heard, as its node's eyebrow. */
export function peerSeen(
  peer: Pick<WGPeer, "online" | "latestHandshake" | "handshakeState">,
): string {
  if (peer.online) return "online"
  if (!peer.latestHandshake) return "never connected"
  const when = relativeTime(new Date(peer.latestHandshake * 1000).toISOString())
  return `${peer.handshakeState === "stale" ? "stale · " : ""}seen ${when}`
}

/** A peer's mark: green while it is connected, amber while something about it needs a look. */
export function peerTone(
  peer: Pick<WGPeer, "online" | "handshakeState" | "transport">,
  findings: number,
): "success" | "warning" | "neutral" {
  if (findings > 0 || peer.handshakeState === "stale" || peer.transport?.state === "captured")
    return "warning"
  return peer.online ? "success" : "neutral"
}

/**
 * The wire between a peer and its tunnel: it carries a pulse while the peer
 * handshook in the last three minutes, is still while it is quiet, dashed
 * where it never connected or the tunnel is down, and amber where its
 * session or its transport needs a look.
 */
export function peerLine(
  tunnel: Pick<WGInterface, "up">,
  peer: Pick<
    WGPeer,
    "online" | "latestHandshake" | "handshakeState" | "transport" | "kind" | "rxBytes" | "txBytes"
  >,
): WireLine {
  if (!tunnel.up) return { state: "down" }
  if (!peer.latestHandshake) return { state: "idle" }
  const amber = peer.handshakeState === "stale" || peer.transport?.state === "captured"
  return {
    state: amber ? "warning" : "up",
    moving: peer.online,
    // Towards whichever end received more: a phone that mostly downloads
    // draws its pulse out to the phone.
    reverse: peer.kind !== "site" && peer.txBytes > peer.rxBytes,
    duration: peer.online ? 2.2 : 3,
  }
}

const RUNTIME: Record<WGFamilyState["runtime"], string> = {
  not_configured: "not configured",
  present: "present",
  down: "down",
  missing: "missing",
  unreadable: "unreadable",
}

/** One address family of a tunnel, as the two lines its row reads. */
export function familyReading(state: WGFamilyState): {
  network: string
  runtime: string
  exit: string
  reason?: string
  verdict?: Reading
} {
  const exit = state.exit
  const words = [
    exit.configured
      ? `Exit configured${exit.interface ? ` through ${exit.interface}` : ""}`
      : "Exit off",
    exit.translated === undefined
      ? ""
      : exit.translated === 1
        ? "1 client connection translated"
        : `${exit.translated.toLocaleString("en-US")} client connections translated`,
  ].filter(Boolean)
  const reason =
    state.reason ||
    exit.reason ||
    (!exit.configured && state.configured && !exit.capability.writable
      ? exit.capability.reason
      : undefined)
  return {
    network: state.subnet || "No tunnel network",
    runtime: RUNTIME[state.runtime] ?? String(state.runtime).replaceAll("_", " "),
    exit: words.join(" · "),
    reason: reason || undefined,
    verdict:
      exit.runtime === "disabled"
        ? undefined
        : exit.runtime === "verified"
          ? { tone: "running", label: "Rules verified" }
          : exit.runtime === "degraded"
            ? { tone: "warning", label: "Degraded" }
            : { tone: "unknown", label: "Unverified" },
  }
}

/** The tunnels a rail lists: the live ones, then the removed ones whose name is free again. */
export function railTunnels(
  live: Pick<WGInterface, "name" | "up" | "peers">[],
  archived: Pick<WGArchived, "name" | "archivedAt">[],
): { name: string; line: string; tone: DotTone; archived: boolean }[] {
  const names = new Set(live.map((t) => t.name))
  const seen = new Set<string>()
  const removed = archived.filter((a) => {
    if (names.has(a.name) || seen.has(a.name)) return false
    seen.add(a.name)
    return true
  })
  return [
    ...live.map((t) => {
      const state = tunnelState(t)
      return { name: t.name, line: state.label, tone: state.tone, archived: false }
    }),
    ...removed.map((a) => ({
      name: a.name,
      line: `removed ${relativeTime(new Date(a.archivedAt * 1000).toISOString())}`,
      tone: "unknown" as DotTone,
      archived: true,
    })),
  ]
}

const BACKEND_STATE: Record<string, Reading> = {
  NeedsLogin: { tone: "warning", label: "Needs sign-in" },
  NeedsMachineAuth: { tone: "warning", label: "Awaiting approval" },
  Stopped: { tone: "stopped", label: "Stopped" },
  Starting: { tone: "notice", label: "Starting" },
  NoState: { tone: "unknown", label: "Not started" },
}

/** The tailnet's state beside this server's name: who is online, or why nobody is. */
export function tailnetState(
  view: Pick<TailscaleView, "running" | "backendState" | "peers">,
): Reading {
  if (view.running) {
    const online = view.peers.filter((p) => p.online).length
    return {
      tone: "running",
      label: view.peers.length
        ? `${online} of ${view.peers.length} online`
        : "Running, no other machine",
      live: true,
    }
  }
  return (
    BACKEND_STATE[view.backendState] ?? {
      tone: "warning",
      // The daemon's own word, spaced: "InUseOtherUser" reads "In use other user".
      label: view.backendState
        ? view.backendState
            .replace(/([a-z])([A-Z])/g, "$1 $2")
            .replace(/^(.)(.*)$/, (_, a: string, b: string) => a + b.toLowerCase())
        : "Not running",
    }
  )
}

/** A peer's system as the product it is. */
export function osProduct(os: string) {
  const o = os.toLowerCase()
  if (o.includes("windows")) return "windows"
  if (o.includes("mac") || o.includes("ios") || o.includes("darwin")) return "apple"
  if (o.includes("android")) return "android"
  if (o.includes("linux")) return "linux"
  return undefined
}

/** A machine's state on the tailnet. */
export function machineState(
  p: Pick<TailscalePeer, "expired" | "online" | "active" | "lastSeen">,
): Reading {
  if (p.expired) return { tone: "danger", label: "Key expired" }
  if (p.online) return { tone: "running", label: p.active ? "Active" : "Online", live: p.active }
  return { tone: "stopped", label: p.lastSeen ? `Seen ${relativeTime(p.lastSeen)}` : "Offline" }
}

/** How this server reaches a machine: straight to an address, through a relay, or not at all. */
export function machineReach(p: Pick<TailscalePeer, "direct" | "curAddr" | "relay">): {
  word: string
  detail?: string
} {
  if (p.direct) return { word: "direct", detail: p.curAddr || undefined }
  if (p.relay) return { word: "relayed", detail: p.relay }
  return { word: "not reached" }
}

export type MachineFilter = "all" | "online" | "offline"

/** The machines a filter and a search leave. */
export function filterMachines<
  T extends Pick<
    TailscalePeer,
    "online" | "hostName" | "dnsName" | "tailscaleIps" | "userLoginName" | "os"
  >,
>(machines: T[], filter: MachineFilter, query: string): T[] {
  const q = query.trim().toLowerCase()
  return machines.filter((m) => {
    if (filter === "online" && !m.online) return false
    if (filter === "offline" && m.online) return false
    if (!q) return true
    return [m.hostName, m.dnsName, m.userLoginName ?? "", m.os, ...m.tailscaleIps].some((field) =>
      field.toLowerCase().includes(q),
    )
  })
}

/** What an operator has changed about this server's offers and not saved. */
export type OfferDraft = {
  /** Undefined while the exit-node offer is as it was read. */
  exitNode?: boolean
  /** Offered routes marked to be withdrawn. */
  withdraw: string[]
  /** A network typed to be offered. */
  route: string
}

export const NO_OFFER_DRAFT: OfferDraft = { withdraw: [], route: "" }

type Prefs = Pick<TailscaleView["prefs"], "advertiseRoutes" | "advertisingExitNode">

/** How many things the draft would change, for the save bar's count. */
export function offerChanges(prefs: Prefs, draft: OfferDraft) {
  const exit = draft.exitNode !== undefined && draft.exitNode !== prefs.advertisingExitNode ? 1 : 0
  const withdrawn = draft.withdraw.filter((r) => prefs.advertiseRoutes.includes(r)).length
  return exit + withdrawn + (draft.route.trim() ? 1 : 0)
}

/**
 * The request a draft becomes against the preferences as they are *now*: a
 * lost answer may have accepted the last save, so a retry is worked out from
 * a fresh read and sends only what still differs. Null when nothing does.
 */
export function offerRequest(
  prefs: Prefs,
  draft: OfferDraft,
): { advertiseExitNode?: boolean; advertiseRoutes?: string[] } | null {
  const body: { advertiseExitNode?: boolean; advertiseRoutes?: string[] } = {}
  if (draft.exitNode !== undefined && draft.exitNode !== prefs.advertisingExitNode)
    body.advertiseExitNode = draft.exitNode
  const add = draft.route.trim()
  const kept = prefs.advertiseRoutes.filter((r) => !draft.withdraw.includes(r))
  const routes = add && !kept.includes(add) ? [...kept, add] : kept
  if (
    routes.length !== prefs.advertiseRoutes.length ||
    routes.some((r, i) => r !== prefs.advertiseRoutes[i])
  )
    body.advertiseRoutes = routes
  return Object.keys(body).length ? body : null
}

/** Why a typed network cannot be offered; undefined when it can be sent. */
export function routeProblem(raw: string): string | undefined {
  const text = raw.trim()
  if (!text) return undefined
  const [address, bits, extra] = text.split("/")
  const parsed = literalAddress(address)
  const max = parsed?.length === 4 ? 32 : 128
  if (
    !parsed ||
    extra !== undefined ||
    bits === undefined ||
    !/^\d{1,3}$/.test(bits) ||
    Number(bits) > max
  )
    return "Use a network such as 10.0.4.0/24."
  if (Number(bits) === 0) return "Every address is the exit node, not a subnet route."
}

/** The subnet routes' state line: how many are offered and how many the tailnet serves. */
export function routesReading(
  routes: string[],
  approval: TailscaleView["approval"],
): { words: string; waiting: number } {
  if (routes.length === 0) return { words: "None offered", waiting: 0 }
  const states = routes.map((r) => approval?.routes.find((a) => a.route === r)?.state)
  const waiting = states.filter((s) => s === "not_serving").length
  const served = states.filter((s) => s === "serving").length
  return {
    words: [
      `${routes.length} offered`,
      served ? `${served} served` : "",
      waiting ? `${waiting} not served` : "",
    ]
      .filter(Boolean)
      .join(" · "),
    waiting,
  }
}

/** "3 need a look", for the tunnel's line. */
export function findingsWord(count: number) {
  return count === 1 ? "1 needs a look" : `${count} need a look`
}
