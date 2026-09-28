import type { Listener, ListenerNetwork } from "@/lib/types"
import { managerHref } from "@/components/procs/shared"

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
  uplink: "Private uplink",
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
 * A private address on the interface carrying the default route. Whether the
 * internet reaches it is decided off this host: a cloud provider maps the
 * instance's public address onto it, and a router may forward a port to it.
 */
export function onUplink(listener: Pick<Listener, "network">): boolean {
  return listener.network === "uplink"
}

/** What a socket on the private uplink may also answer, for its reach's tooltip. */
export const UPLINK_CAVEAT =
  "A private address on the interface with the default route. If the provider maps a public address onto it, or a router forwards a port to it, the internet reaches it too."

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
 * double the services behind it. Sockets fold only when the protocol, port
 * and network agree, the families differ and one service holds both (see
 * `oneService`), so two programs on one port, or one program on two bridges,
 * stay apart.
 */
export function foldDualStack(listeners: Listener[]): Socket[] {
  const out: Socket[] = []
  const unpaired = new Map<string, Socket[]>()
  for (const listener of listeners) {
    const key = JSON.stringify([
      listener.protocol,
      listener.port,
      listener.reach,
      listener.network,
      listener.interface ?? "",
      listener.pid > 0 ? [listener.process, listener.user ?? ""] : [],
      // Two containers publishing one port, one per family, are two services.
      listener.container?.id ?? "",
      listener.source ?? "",
    ])
    const waiting = unpaired.get(key) ?? []
    const first = waiting.find((s) => s.family !== listener.family && oneService(s, listener))
    if (first) {
      first.twin = listener
      unpaired.set(
        key,
        waiting.filter((s) => s !== first),
      )
      continue
    }
    const socket: Socket = { ...listener }
    out.push(socket)
    unpaired.set(key, [...waiting, socket])
  }
  return out
}

/**
 * Whether two sockets of one program, run by one account, are one service.
 * One process is. So are two started by one parent, or started the same way
 * but for the address: Docker publishes a port with one docker-proxy per
 * family, dockerd's children both, each told its own -host-ip (and on a
 * dual-stack network its own -container-ip). Init is everybody's parent and
 * says nothing. Two sockets whose holders this account cannot see (PID 0)
 * are taken as one, as they always were.
 */
function oneService(a: Listener, b: Listener): boolean {
  if (a.pid === b.pid) return true
  if (a.ppid && a.ppid > 1 && a.ppid === b.ppid) return true
  return withoutAddresses(a.cmdline ?? "") === withoutAddresses(b.cmdline ?? "")
}

const BRACKETED_IPV6 = /\[[0-9a-f:.]+(?:%[\w.-]+)?\]/gi
// Two colons at least, which every IPv6 address has and a host:port has not.
const BARE_IPV6 = /(?<![\w:.])[0-9a-f]*:[0-9a-f]*:[0-9a-f:.]*(?:%[\w.-]+)?(?![\w:.%])/gi
const IPV4 = /(?<![\w.])\d{1,3}(?:\.\d{1,3}){3}(?![\w.])/g

/** A command line with every IP address in it replaced by one placeholder. */
export function withoutAddresses(cmdline: string): string {
  return cmdline
    .replace(BRACKETED_IPV6, "<address>")
    .replace(BARE_IPV6, "<address>")
    .replace(IPV4, "<address>")
}

/** The PIDs a folded socket stands for: two when Docker holds each family in its own proxy. */
export function socketPids(socket: Socket): number[] {
  const pids = [socket.pid]
  if (socket.twin && socket.twin.pid !== socket.pid) pids.push(socket.twin.pid)
  return pids.filter((pid) => pid > 0)
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
 * width, about thirty characters, where the tile truncates it. With nothing
 * the internet is sure to reach, a socket on the private uplink still keeps
 * it from saying nothing can: the provider may map a public address onto it.
 */
export function internetHint(sockets: Pick<Listener, "reach" | "network">[]): string {
  const every = sockets.filter((l) => l.reach === "all").length
  const one = sockets.filter((l) => l.reach === "public").length
  const uplink = sockets.filter(onUplink).length
  if (every + one === 0) {
    return uplink > 0 ? `+${uplink} if the uplink is mapped` : "nothing the internet can reach"
  }
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
  uplink: "uplink",
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
 * The overview's hint beside the Internet-facing figure: the rest of what
 * answers off the machine, as the ports page's Private networks tile counts
 * it, so both numbers a reader clicks through to are on the page.
 */
export function privateNetworksHint(tally: ReachTally): string {
  if (tally.private > 0) return `${tally.private} on private networks`
  return tally.internet > 0 ? "none on private networks" : "everything on this server"
}

/**
 * A service the security catalogue calls dangerous and the posture raises a
 * finding for — a database, a control plane, a remote desktop, an open
 * resolver, or the dashboard itself past its own Caddy —
 * once per protocol and port however many addresses it is bound to —
 * Postgres on 0.0.0.0 and on :: is one database, as the posture's finding for
 * it is one finding.
 */
export type DangerousPort = {
  protocol: string
  port: number
  service: string
  /** Every socket on the port the posture levels, in listing order. */
  sockets: Listener[]
  /** The port's level: its highest socket's, as the posture's one finding is at its widest bind. */
  level: "critical" | "warning"
  /** The firewall's inbound default, when it holds every socket on the port to a warning. */
  inboundDefault?: string
  /** Why the default does not hold the port, from the first socket it does not hold. */
  pastFirewall?: Listener["pastFirewall"]
  firewallRule?: number
}

/** The dangerous service a socket is, when the posture levels it. */
export function dangerousService(
  listener: Pick<Listener, "service" | "danger" | "level">,
): string | undefined {
  return listener.level && listener.danger ? listener.service : undefined
}

export function dangerousPorts(listeners: Listener[]): DangerousPort[] {
  const byPort = new Map<string, DangerousPort>()
  for (const l of listeners) {
    const service = dangerousService(l)
    if (!service || !l.level) continue
    const key = `${l.protocol}/${l.port}`
    const entry = byPort.get(key) ?? {
      protocol: l.protocol,
      port: l.port,
      service,
      sockets: [],
      level: l.level,
    }
    entry.sockets.push(l)
    if (l.level === "critical") entry.level = "critical"
    if (l.pastFirewall && !entry.pastFirewall) {
      entry.pastFirewall = l.pastFirewall
      entry.firewallRule = l.firewallRule
    }
    byPort.set(key, entry)
  }
  // The default holds a port only if it holds every socket on it.
  for (const entry of byPort.values()) {
    if (entry.sockets.every((l) => l.inboundDefault)) {
      entry.inboundDefault = entry.sockets[0].inboundDefault
    }
  }
  return [...byPort.values()]
}

/**
 * Why a firewall refusing inbound by default does not hold a socket, where
 * the posture does not credit the default, as the ports page's row says it.
 */
export function pastFirewallWords(
  port: Pick<Listener, "pastFirewall" | "firewallRule">,
): string | undefined {
  if (port.pastFirewall === "docker") return "Published by Docker past the firewall"
  if (port.pastFirewall === "rule")
    return `Firewall rule ${port.firewallRule} admits it from anywhere`
  return undefined
}

/**
 * The verdict a listening socket is drawn with. A database or control port
 * takes the security posture's level for it, from the same rules and the same
 * firewall: critical where the internet can reach it, a warning where only a
 * tailnet, a LAN or a bridge can, or where the firewall's inbound default
 * refuses it — which a port Docker publishes, or one a rule admits from
 * anywhere, gets past. Any other socket the internet can reach, or may
 * through the private uplink, is a warning, and one only a private network
 * reaches a notice — sshd on the tailnet is not an alarm. Loopback has none.
 */
export function reachVerdict(
  listener: Pick<Listener, "exposed" | "reach" | "network" | "level" | "service" | "danger">,
): "critical" | "warning" | "notice" | undefined {
  if (!listener.exposed) return undefined
  if (listener.level && dangerousService(listener)) return listener.level
  return internetFacing(listener) || onUplink(listener) ? "warning" : "notice"
}

/**
 * What a socket's row is headed by: the container it answers for, the PM2
 * app, the service a systemd socket unit starts where systemd is the only
 * holder the dashboard can see, or the program — by its own name where the
 * kernel's is a thread's (node's "MainThread").
 */
export function ownerTitle(
  listener: Pick<
    Listener,
    | "pid"
    | "process"
    | "displayName"
    | "manager"
    | "managerName"
    | "socketUnit"
    | "activates"
    | "container"
  >,
): string {
  if (listener.container) return listener.container.name
  if (listener.manager === "pm2" && listener.managerName) return listener.managerName
  if (listener.socketUnit && listener.pid <= 1) return listener.activates || listener.socketUnit
  return listener.displayName || listener.process || "unknown"
}

/**
 * How the owner runs, for the line under its title: "Container ·
 * caddy:2-alpine · stack just-dashboard", "ssh.socket → ssh.service",
 * "nginx.service", "PM2 · node", "Login session c521". Nothing for a
 * process nothing supervises.
 */
export function ownerLabel(
  listener: Pick<
    Listener,
    "process" | "displayName" | "manager" | "managerName" | "socketUnit" | "activates" | "container"
  >,
): string | undefined {
  const { container } = listener
  if (container) {
    const parts = ["Container", container.image]
    if (container.deployment) parts.push(`deployment ${container.deployment.project}`)
    else if (container.project) parts.push(`stack ${container.project}`)
    return parts.join(" · ")
  }
  if (listener.manager === "pm2")
    return `PM2 · ${listener.displayName || listener.process || "app"}`
  if (listener.socketUnit) {
    return listener.activates
      ? `${listener.socketUnit} → ${listener.activates}`
      : listener.socketUnit
  }
  if (listener.manager === "systemd" && listener.managerName) return listener.managerName
  if (listener.manager === "session" && listener.managerName) {
    return `Login session ${listener.managerName.replace(/^session-|\.scope$/g, "")}`
  }
  return undefined
}

/** A page the owner is managed from. */
export type OwnerLink = {
  key: "container" | "deployment" | "unit" | "app"
  label: string
  href: string
}

/**
 * Where a socket's owner is managed from, most direct first: the container
 * and the deployment it runs for, the PM2 app, or the service systemd starts
 * for it. A socket unit that starts a service per connection names none,
 * and nothing links to it.
 */
export function ownerLinks(
  listener: Pick<Listener, "manager" | "managerName" | "socketUnit" | "activates" | "container">,
): OwnerLink[] {
  const { container } = listener
  if (container) {
    const links: OwnerLink[] = [
      {
        key: "container",
        label: "Open container",
        href: `/docker/containers/${encodeURIComponent(container.name)}`,
      },
    ]
    if (container.deployment) {
      links.push({
        key: "deployment",
        label: "Open deployment",
        href: `/deploy/${container.deployment.projectId}`,
      })
    }
    return links
  }
  if (listener.manager === "pm2") {
    const href = managerHref({ manager: "pm2", managerName: listener.managerName })
    return href ? [{ key: "app", label: "Open app", href }] : []
  }
  const unit = listener.socketUnit
    ? listener.activates
    : listener.manager === "systemd"
      ? listener.managerName
      : undefined
  const href = unit ? managerHref({ manager: "systemd", managerName: unit }) : null
  return href ? [{ key: "unit", label: "Open unit", href }] : []
}

/**
 * The words a search finds a socket's owner by, beyond its process and
 * command: the program's own name, the container, its image, stack,
 * service and deployment, the units, the PM2 app — and the dashboard's own
 * tag, so "dashboard" finds its backend, which is named jd-server.
 */
export function ownerWords(listener: Listener): string[] {
  const { container } = listener
  return [
    listener.displayName,
    listener.managerName,
    listener.socketUnit,
    listener.activates,
    container?.name,
    container?.image,
    container?.project,
    container?.service,
    container?.deployment?.project,
    listener.self ? "this dashboard" : undefined,
  ].filter((word): word is string => Boolean(word))
}
