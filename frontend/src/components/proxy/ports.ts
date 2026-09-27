import type { Listener, ListenerNetwork } from "@/lib/types"
import { DANGEROUS_PORTS } from "@/components/proxy/findings/shared"

/**
 * Where a socket answers, per network the backend places its address on —
 * the same placement the security posture levels its findings by.
 */
const NETWORK_WORDS: Record<ListenerNetwork, string> = {
  loopback: "This server only",
  all: "Every interface",
  public: "Public address",
  tailnet: "Tailnet only",
  vpn: "VPN only",
  private: "Private network",
  docker: "Docker bridge",
  bridge: "Bridge",
  "link-local": "Link-local",
}

/** The network alone, as a socket's reach reads: "Tailnet only". */
export function networkWords(listener: Pick<Listener, "network">): string {
  return NETWORK_WORDS[listener.network]
}

/** The network and the interface it is on: "Tailnet only · tailscale0". */
export function reachWords(listener: Pick<Listener, "network" | "interface">): string {
  const words = networkWords(listener)
  return listener.interface ? `${words} · ${listener.interface}` : words
}

/** Every interface or a public address: a reach the internet shares. */
export function internetFacing(listener: Pick<Listener, "reach">): boolean {
  return listener.reach === "all" || listener.reach === "public"
}

/**
 * Who can connect, in three answers: the internet, one private network (a
 * tailnet, VPN, LAN or bridge), or this server alone. "Interface-bound" would
 * also describe a database on a public IP, which is the internet's.
 */
export type ReachGroup = "internet" | "private" | "local"

export function reachGroup(listener: Pick<Listener, "reach">): ReachGroup {
  if (listener.reach === "loopback") return "local"
  return internetFacing(listener) ? "internet" : "private"
}

/** A service's socket, with the other family's twin when it has one. */
export type Socket = Listener & { twin?: Listener }

/**
 * One row per service and network. sshd on 0.0.0.0 and on :: is one service
 * on every interface, as is a server on 127.0.0.1 and ::1 or on a tailnet's
 * IPv4 and IPv6 addresses; listed apart, every count on the page was nearly
 * double the services behind it. Sockets fold only when the owner, protocol,
 * port and network all agree and the families differ, so two programs on
 * one port, or one program on two bridges, stay apart.
 */
export function foldDualStack(listeners: Listener[]): Socket[] {
  const out: Socket[] = []
  const unpaired = new Map<string, Socket>()
  for (const listener of listeners) {
    const key = [
      listener.protocol,
      listener.port,
      listener.pid,
      listener.reach,
      listener.network,
      listener.interface ?? "",
    ].join("|")
    const first = unpaired.get(key)
    if (first && first.family !== listener.family) {
      first.twin = listener
      unpaired.delete(key)
      continue
    }
    const socket: Socket = { ...listener }
    out.push(socket)
    if (!first) unpaired.set(key, socket)
  }
  return out
}

/** A folded socket's addresses, IPv4 first as the listing orders them. */
export function socketAddresses(socket: Socket): string[] {
  return socket.twin ? [socket.address, socket.twin.address] : [socket.address]
}

export type ReachTally = Record<ReachGroup | "all" | "tcp" | "udp", number>

/** The page's counts, over folded sockets: a dual-stack service counts once. */
export function tallyReach(sockets: Socket[]): ReachTally {
  const tally: ReachTally = { all: 0, internet: 0, private: 0, local: 0, tcp: 0, udp: 0 }
  for (const socket of sockets) {
    tally.all++
    tally[reachGroup(socket)]++
    if (socket.protocol === "tcp") tally.tcp++
    if (socket.protocol === "udp") tally.udp++
  }
  return tally
}

/**
 * The Internet-facing tile's hint: whether the count is binds to every
 * interface, to a public address, or both. It has to fit half a phone's
 * width, about thirty characters, where the tile truncates it.
 */
export function internetHint(sockets: Pick<Listener, "reach">[]): string {
  const every = sockets.filter((l) => l.reach === "all").length
  const one = sockets.filter((l) => l.reach === "public").length
  if (every + one === 0) return "nothing the internet can reach"
  if (one === 0) return "on every interface"
  if (every === 0) return one === 1 ? "on a public address" : "on public addresses"
  return `${every} on all · ${one} on a public IP`
}

const NETWORK_HINT: Record<ListenerNetwork, string> = {
  loopback: "loopback",
  all: "every interface",
  public: "public",
  tailnet: "tailnet",
  vpn: "VPN",
  private: "LAN",
  docker: "Docker",
  bridge: "bridge",
  "link-local": "link-local",
}

/**
 * The Private networks tile's hint: which networks the count is on, most
 * sockets first, two named and the rest counted so it fits half a phone.
 */
export function privateHint(sockets: Pick<Listener, "reach" | "network">[]): string {
  const counts = new Map<string, number>()
  for (const socket of sockets) {
    if (reachGroup(socket) !== "private") continue
    const word = NETWORK_HINT[socket.network]
    counts.set(word, (counts.get(word) ?? 0) + 1)
  }
  if (counts.size === 0) return "tailnet, VPN, LAN or bridge"
  const words = [...counts].sort((a, b) => b[1] - a[1]).map(([word]) => word)
  if (words.length <= 2) return words.join(" · ")
  return `${words.slice(0, 2).join(" · ")} +${words.length - 2}`
}

/**
 * A database or control port answering off the machine, once per protocol
 * and port however many addresses it is bound to — Postgres on 0.0.0.0 and
 * on :: is one database, as the posture's finding for it is one finding.
 */
export type DangerousPort = {
  protocol: string
  port: number
  service: string
  /** Every exposed socket on the port, in listing order. */
  sockets: Listener[]
  /** Any of them faces the internet, which the posture calls critical. */
  internet: boolean
}

export function dangerousPorts(listeners: Listener[]): DangerousPort[] {
  const byPort = new Map<string, DangerousPort>()
  for (const l of listeners) {
    const service = DANGEROUS_PORTS[l.port]
    if (!l.exposed || !service) continue
    const key = `${l.protocol}/${l.port}`
    const entry = byPort.get(key) ?? {
      protocol: l.protocol,
      port: l.port,
      service,
      sockets: [],
      internet: false,
    }
    entry.sockets.push(l)
    entry.internet ||= internetFacing(l)
    byPort.set(key, entry)
  }
  return [...byPort.values()]
}

/**
 * The verdict a listening socket is drawn with, graded by who can connect as
 * the posture grades it and by what answers. A database or control port the
 * internet can reach is critical and one on a private network a warning, as
 * the posture levels them; any other socket the internet can reach is a
 * warning, and one only a private network reaches a notice — sshd on the
 * tailnet is not an alarm. Loopback has none.
 */
export function reachVerdict(
  listener: Pick<Listener, "port" | "exposed" | "reach">,
): "critical" | "warning" | "notice" | undefined {
  if (!listener.exposed) return undefined
  const dangerous = Boolean(DANGEROUS_PORTS[listener.port])
  if (internetFacing(listener)) return dangerous ? "critical" : "warning"
  return dangerous ? "warning" : "notice"
}
