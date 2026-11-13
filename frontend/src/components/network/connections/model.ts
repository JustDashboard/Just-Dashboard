import { networkOf, type NetworkKind } from "@/lib/clients"
import type { Peer } from "@/lib/types"

/**
 * The arithmetic behind the Connections pages, kept apart from React so it is
 * tested in `bun test src`: an address's own page and its key in the address
 * bar, the list's filter, the peers folded for the picture, and the words a
 * folded peer and a block are given.
 */

/** The list's view in the address bar and its tab's session: one key, so the rail reads it too. */
export const CONNECTIONS_FILTER_KEY = "security.connections.filters"
export const CONNECTIONS_FILTER_DEFAULTS = { q: "", scope: "all", when: "live", hour: "" }
export type ConnectionsFilters = typeof CONNECTIONS_FILTER_DEFAULTS

/** An address's two views: what it holds now, and what socket history kept of one hour. */
export const ADDRESS_TABS = ["live", "past"] as const
export type AddressTab = (typeof ADDRESS_TABS)[number]

/**
 * The page of one remote address. The address is one path segment, so an
 * IPv6 address's colons are escaped; the view and the hour ride along as the
 * list's own words for them.
 */
export function addressHref(address: string, view?: { tab?: string; hour?: string }) {
  const query = new URLSearchParams()
  if (view?.tab && view.tab !== "live") query.set("tab", view.tab)
  if (view?.tab === "past" && view.hour) query.set("hour", view.hour)
  const search = query.toString()
  return `/network/connections/${encodeURIComponent(address)}${search ? `?${search}` : ""}`
}

const V4 = /^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$/

/**
 * The address a path segment names, or null where it names none. Read loosely
 * (escaped or not, any case, an IPv4-mapped IPv6 address as its IPv4 one, the
 * way the kernel's table lists it) and checked strictly, because the segment
 * goes into a request path.
 */
export function parseAddress(segment: string | undefined): string | null {
  if (!segment) return null
  let raw: string
  try {
    raw = decodeURIComponent(segment)
  } catch {
    return null
  }
  const address = raw
    .trim()
    .toLowerCase()
    .replace(/^::ffff:(?=\d+\.)/, "")
  const v4 = address.match(V4)
  if (v4) return v4.slice(1).every((part) => Number(part) <= 255) ? address : null
  if (!address.includes(":") || !/^[0-9a-f:.]+$/.test(address)) return null
  const doubles = address.split("::").length - 1
  if (doubles > 1) return null
  const groups = address.split(":").filter(Boolean)
  if (groups.length > 8 || groups.some((g) => g.length > 4 && !V4.test(g))) return null
  if (doubles === 0 && groups.length !== 8 && !V4.test(groups.at(-1) ?? "")) return null
  return address
}

/** The list's rows for its scope and search: address, port, program or service. */
export function matchPeers(peers: Peer[], scope: string, query: string): Peer[] {
  const q = query.trim().toLowerCase()
  return peers.filter(
    (p) =>
      (scope !== "public" || !p.private) &&
      (!q ||
        `${p.address} ${p.service ?? ""} ${p.processes.join(" ")} ${p.ports.join(" ")}`
          .toLowerCase()
          .includes(q)),
  )
}

/** The program a peer reached, in the words the row and the picture use. */
export function reached(peer: Pick<Peer, "processes" | "service">) {
  return peer.processes[0] || peer.service || "Unknown program"
}

/**
 * What folding by address kept of a peer's sockets, as one line: its
 * transports, the far end's ports and the states its sockets are in, busiest
 * state first — "TCP/UDP · from 51022, 51023 +15 · 14 established, 3 time-wait".
 */
export function foldedWords(peer: Peer): string | undefined {
  if (!peer.protocols || peer.protocols.length === 0) return undefined
  const parts = [peer.protocols.join("/").toUpperCase()]
  if (peer.remotePorts && peer.remotePorts.length > 0) {
    parts.push(`from ${peer.remotePorts.join(", ")}${peer.morePorts ? ` +${peer.morePorts}` : ""}`)
  }
  if (peer.states) {
    parts.push(
      Object.entries(peer.states)
        .sort(([, a], [, b]) => b - a)
        .map(([state, n]) => `${n} ${state.toLowerCase().replace("_", "-")}`)
        .join(", "),
    )
  }
  return parts.join(" · ")
}

/**
 * Whether an address may be offered a block: never one the server calls
 * private, and never one on the tailnet, a local network or this machine,
 * whatever the server said — an address opened from a past hour has no live
 * row to say so, and the sheet offered a tailnet address a block for that.
 */
export function blockable(address: string, isPrivate?: boolean) {
  return !isPrivate && networkOf(address).kind === "internet"
}

/** The reason a block is offered with: what the reader was looking at. */
export function blockContext(peer?: Pick<Peer, "processes" | "service" | "ports">) {
  if (!peer) return "seen in the socket history"
  const what = peer.service ?? peer.processes[0] ?? "this server"
  return `connected to ${what}${peer.ports.length ? ` on ${peer.ports.join(", ")}` : ""}`
}

/** The order the places are drawn in, and what each is called. */
export const PLACES: { kind: NetworkKind; title: string }[] = [
  { kind: "internet", title: "The internet" },
  { kind: "tailscale", title: "Tailnet" },
  { kind: "local", title: "Local networks" },
]

export type Place = {
  kind: NetworkKind
  title: string
  peers: Peer[]
  sockets: number
  active: number
}
export type Program = {
  key: string
  name: string
  ports: number[]
  sockets: number
  active: number
}

/**
 * The peers folded by where they are and by what they reached, busiest first.
 * Loopback never left the machine and is in neither.
 */
export function foldPeers(peers: Peer[]): { places: Place[]; programs: Program[] } {
  const byKind = new Map<NetworkKind, Place>()
  const byProgram = new Map<string, Program>()
  for (const peer of peers) {
    const kind = networkOf(peer.address).kind
    if (kind === "server") continue
    const title = PLACES.find((p) => p.kind === kind)?.title ?? kind
    const place = byKind.get(kind) ?? { kind, title, peers: [], sockets: 0, active: 0 }
    place.peers.push(peer)
    place.sockets += peer.count
    place.active += peer.established
    byKind.set(kind, place)

    const name = reached(peer)
    const program = byProgram.get(name) ?? { key: name, name, ports: [], sockets: 0, active: 0 }
    for (const p of peer.ports) if (!program.ports.includes(p)) program.ports.push(p)
    program.sockets += peer.count
    program.active += peer.established
    byProgram.set(name, program)
  }
  const places = PLACES.map((p) => byKind.get(p.kind)).filter((g): g is Place => !!g)
  for (const g of places) g.peers.sort((a, b) => b.count - a.count)
  const programs = [...byProgram.values()]
    .map((s) => ({ ...s, ports: s.ports.sort((a, b) => a - b) }))
    .sort((a, b) => b.sockets - a.sockets)
  return { places, programs }
}

/** A wire's pulse for how many connections it carries: quicker the more there are. */
export function beat(active: number) {
  if (active <= 1) return 3.6
  return Math.max(1.4, 3.6 - Math.log2(active) * 0.4)
}

/**
 * The address a step of Alt+↑ or Alt+↓ opens: the next row of the list, or
 * from an address the list no longer holds, its first or last.
 */
export function stepAddress(rows: string[], current: string, direction: 1 | -1) {
  const at = rows.indexOf(current)
  if (at < 0) return rows.at(direction > 0 ? 0 : -1)
  return rows[at + direction]
}
