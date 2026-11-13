import type { Capability, Listener, ListenerClients, ListenerNetwork } from "@/lib/types"
import { managerHref } from "@/components/procs/shared"
import { imageProduct, portProduct, processProduct, unitProduct } from "@/components/product-logo"

/**
 * The owner of a socket as the product it is — Postgres on 5432, nginx on
 * 443, Grafana for a container of its image. Read from the container's image
 * or the program first and the port second, so a `postgres` on an unusual
 * port is still Postgres and a `python` on 5432 is not. The Ports table
 * draws its rows with it, and the overview the applications its routes reach.
 */
export function listenerProduct(listener: Listener): string | undefined {
  const unit = listener.activates || (listener.manager === "systemd" ? listener.managerName : "")
  return listener.container
    ? imageProduct(listener.container.image)
    : (processProduct(listener.displayName || listener.process || "") ??
        (unit ? unitProduct(unit) : undefined) ??
        portProduct(listener.port))
}

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

/**
 * Every process holding a folded socket, from both families: the owner the
 * row names, and the workers and supervisors holding the same socket.
 */
export function socketHolders(socket: Socket): number[] {
  const all = [...(socket.pids ?? [socket.pid]), ...(socket.twin?.pids ?? [])]
  if (socket.twin) all.push(socket.twin.pid)
  return [...new Set(all.filter((pid) => pid > 0))].sort((a, b) => a - b)
}

/** How many peers a socket's clients line names; the backend sends at most five. */
const CLIENT_PEERS = 5

/**
 * A folded socket's clients over both families: a dual-stack service's
 * IPv4 callers are on one socket and its IPv6 callers on the other.
 */
export function socketClients(socket: Socket): ListenerClients | undefined {
  const own = socket.clients
  const twin = socket.twin?.clients
  if (!own || !twin) return own ?? twin
  const byAddress = new Map<string, number>()
  for (const peer of [...own.peers, ...twin.peers]) {
    byAddress.set(peer.address, (byAddress.get(peer.address) ?? 0) + peer.count)
  }
  const peers = [...byAddress]
    .map(([address, count]) => ({ address, count }))
    .sort((a, b) => b.count - a.count || a.address.localeCompare(b.address))
    .slice(0, CLIENT_PEERS)
  return { count: own.count + twin.count, peers }
}

/**
 * The `?socket=` a row's detail opens at: `tcp:0.0.0.0:443`, `tcp:::22`.
 * The protocol is before the first colon and the port after the last, so an
 * IPv6 address needs no brackets.
 */
export function socketParam(socket: Pick<Listener, "protocol" | "address" | "port">): string {
  return `${socket.protocol}:${socket.address}:${socket.port}`
}

/** The socket `?socket=` names, by either of a folded socket's addresses. */
export function findSocket(sockets: Socket[], param: string): Socket | undefined {
  const first = param.indexOf(":")
  const last = param.lastIndexOf(":")
  if (first < 0 || last <= first) return undefined
  const protocol = param.slice(0, first)
  const address = param.slice(first + 1, last)
  const port = Number(param.slice(last + 1))
  return sockets.find(
    (s) =>
      s.protocol === protocol &&
      s.port === port &&
      (s.address === address || s.twin?.address === address),
  )
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

/**
 * Something the ports page can do to a socket's owner in place, through the
 * route its own page uses and under that route's capability: the unit, the
 * container, the PM2 app, or the process itself.
 */
export type OwnerAction = {
  key: "restart" | "stop" | "disable" | "reload" | "terminate" | "kill"
  label: string
  progressive: string
  danger?: boolean
  /** The confirmation's title and the sentences under it. */
  title: string
  description: string[]
  path: string
  body?: unknown
}

/**
 * What the operator may do to a socket's owner from its row. A unit restarts,
 * stops and is disabled on boot; a container restarts and stops; a PM2 app
 * reloads and restarts; a process nothing supervises is terminated or killed,
 * addressed by its PID and start time so a PID the kernel has handed on since
 * the poll is refused rather than signalled. The dashboard's own sockets get
 * none: stopping the dashboard from its own page leaves nothing to start it
 * again with.
 */
export function ownerActions(
  socket: Socket,
  can: (capability: Capability) => boolean,
): OwnerAction[] {
  if (socket.self) return []
  const { container } = socket
  if (container) {
    if (!can("destructive")) return []
    const name = container.name
    const path = `/docker/containers/${encodeURIComponent(container.id)}`
    return [
      {
        key: "restart",
        label: "Restart container",
        progressive: "Restarting",
        title: "Restart container",
        description: [
          `Docker stops ${name} and starts it again. Connections to it drop while it does.`,
        ],
        path: `${path}/restart`,
      },
      {
        key: "stop",
        label: "Stop container",
        progressive: "Stopping",
        danger: true,
        title: "Stop container",
        description: [
          `Docker stops ${name}. Every port it publishes closes until it is started again from its page.`,
        ],
        path: `${path}/stop`,
      },
    ]
  }
  if (socket.manager === "pm2" && socket.managerName) {
    const app = socket.managerName
    const path = `/pm2/${encodeURIComponent(app)}`
    const actions: OwnerAction[] = []
    if (can("service.control")) {
      actions.push({
        key: "reload",
        label: "Reload app",
        progressive: "Reloading",
        title: "Reload PM2 app",
        description: [
          `PM2 starts ${app} afresh and stops the old instances once the new ones are up. In cluster mode no connection is refused; an app in fork mode is restarted instead.`,
        ],
        path: `${path}/reload`,
      })
    }
    if (can("destructive")) {
      actions.push({
        key: "restart",
        label: "Restart app",
        progressive: "Restarting",
        title: "Restart PM2 app",
        description: [
          `PM2 stops every instance of ${app} and starts it again. Connections to it drop while it does.`,
        ],
        path: `${path}/restart`,
      })
    }
    return actions
  }
  // The unit the row's own page opens: the service a socket unit starts, or
  // the service the owner runs in.
  const unit = socket.socketUnit
    ? socket.activates
    : socket.manager === "systemd"
      ? socket.managerName
      : undefined
  if (unit) {
    const path = `/systemd/${encodeURIComponent(unit)}`
    // systemd holds a socket unit's socket itself, so stopping or disabling
    // the service it starts leaves the port answering.
    const held = socket.socketUnit
      ? `${socket.socketUnit} keeps listening and starts it again on the next connection; stopping ${socket.socketUnit} on the services page is what closes the port.`
      : undefined
    const actions: OwnerAction[] = []
    if (can("destructive")) {
      actions.push(
        {
          key: "restart",
          label: "Restart unit",
          progressive: "Restarting",
          title: "Restart unit",
          description: [
            `systemd stops ${unit} and starts it again. Connections to it drop while it does.`,
          ],
          path: `${path}/restart`,
        },
        {
          key: "stop",
          label: "Stop unit",
          progressive: "Stopping",
          danger: true,
          title: "Stop unit",
          description: held
            ? [`systemd stops ${unit}.`, held]
            : [
                `systemd stops ${unit}. Its ports close until it is started again, from the services page or at the next boot if it is enabled.`,
              ],
          path: `${path}/stop`,
        },
      )
    }
    if (can("system.admin")) {
      actions.push({
        key: "disable",
        label: "Disable on boot",
        progressive: "Disabling",
        title: "Disable unit on boot",
        description: [
          `${unit} no longer starts when the server boots. It keeps running, and its ports stay open, until it is stopped.`,
          ...(held ? [held] : []),
        ],
        path: `${path}/disable`,
      })
    }
    return actions
  }
  // A signal reaches one process: a socket two PIDs hold, init, a kernel
  // thread and an owner whose start time was unreadable are left to the
  // processes page.
  const pids = socketPids(socket)
  if (
    pids.length !== 1 ||
    socket.pid <= 1 ||
    socket.manager === "kernel" ||
    !socket.startedAt ||
    !can("destructive")
  ) {
    return []
  }
  const label = `${ownerTitle(socket)} (${socket.pid})`
  const path = `/processes/${socket.pid}/signal`
  return [
    {
      key: "terminate",
      label: "Terminate process",
      progressive: "Terminating",
      title: "Terminate process",
      description: [
        `${label} is asked to shut down. Most programs finish what they are doing and exit; one that ignores the request is still running afterwards, and Kill is the next step.`,
      ],
      path,
      body: { signal: "SIGTERM", startedAt: socket.startedAt },
    },
    {
      key: "kill",
      label: "Kill process",
      progressive: "Killing",
      danger: true,
      title: "Kill process",
      description: [
        `${label} is ended by the kernel immediately. Anything it had not written is lost, and files it held may be left half-written.`,
      ],
      path,
      body: { signal: "SIGKILL", startedAt: socket.startedAt },
    },
  ]
}

/**
 * A listening port's own page, `/proxy/ports/tcp-443`: the protocol and the
 * port, which name one row on nearly every host. Where two rows share both —
 * systemd-resolved on 127.0.0.53 and 127.0.0.54, a program on loopback and a
 * container on the tailnet on one port — the key carries the address too,
 * `udp-53-127.0.0.53`, so it is still one row. Either of a folded pair's
 * addresses names it.
 */
export type PortKey = { protocol: string; port: number; address?: string }

const PORT_KEY = /^(tcp|udp)-(\d{1,5})(?:-(.+))?$/

export function parsePortKey(key: string): PortKey | undefined {
  const match = PORT_KEY.exec(key)
  if (!match) return undefined
  const port = Number(match[2])
  if (port > 65535) return undefined
  return { protocol: match[1], port, address: match[3] || undefined }
}

/** The key for a socket: the address only where another row shares its protocol and port. */
export function portKey(socket: Socket, sockets: Socket[] = []): string {
  const shared = sockets.some(
    (other) =>
      other !== socket &&
      other.protocol === socket.protocol &&
      other.port === socket.port &&
      other.address !== socket.address,
  )
  const base = `${socket.protocol}-${socket.port}`
  return shared ? `${base}-${socket.address}` : base
}

/** The port's page, opened on a tab other than its first. */
export function portHref(socket: Socket, sockets: Socket[] = [], tab?: string): string {
  const path = `/proxy/ports/${encodeURIComponent(portKey(socket, sockets))}`
  return tab && tab !== "reach" ? `${path}?tab=${tab}` : path
}

/**
 * The row a key names. A key with an address that no longer matches — the
 * other row on the port closed, or a link was written before it opened —
 * still finds the port while only one row holds it; a bare key on a port two
 * rows share finds the one the list puts first.
 */
export function findPort(sockets: Socket[], key: PortKey): Socket | undefined {
  const onPort = sockets.filter((s) => s.protocol === key.protocol && s.port === key.port)
  if (key.address) {
    const exact = onPort.find((s) => s.address === key.address || s.twin?.address === key.address)
    if (exact) return exact
  }
  return onPort.length === 1 || !key.address ? onPort[0] : undefined
}

/**
 * The `?socket=` the side sheet opened at, before a port had a page: the
 * page's own `tcp:0.0.0.0:443`, or the host diagnostic's
 * `tcp-ipv4-0.0.0.0-443` (`backend/internal/netsec/diag_host.go`), which the
 * sheet never read.
 */
export function parseSocketParam(param: string): PortKey | undefined {
  const dashed = /^(tcp|udp)-ipv[46]-(.+)-(\d{1,5})$/.exec(param)
  if (dashed) return { protocol: dashed[1], address: dashed[2], port: Number(dashed[3]) }
  const first = param.indexOf(":")
  const last = param.lastIndexOf(":")
  if (first < 0 || last <= first) return undefined
  const protocol = param.slice(0, first)
  const port = Number(param.slice(last + 1))
  if ((protocol !== "tcp" && protocol !== "udp") || !Number.isInteger(port) || port > 65535) {
    return undefined
  }
  return { protocol, port, address: param.slice(first + 1, last) || undefined }
}

/** The page a parsed key opens. */
export function keyHref(key: PortKey): string {
  const base = `${key.protocol}-${key.port}`
  return `/proxy/ports/${encodeURIComponent(key.address ? `${base}-${key.address}` : base)}`
}

/**
 * Who can reach a socket, as the picture draws them: the internet, the
 * tailnet, a VPN, a private network, Docker's bridges, or this server alone.
 * A socket on every interface is the internet's: whoever else can reach it,
 * the widest reach is the one that matters.
 */
export type ReachLane = "internet" | "tailnet" | "vpn" | "private" | "docker" | "local"

export const LANE_ORDER: ReachLane[] = ["internet", "tailnet", "vpn", "private", "docker", "local"]

export const LANE_WORDS: Record<ReachLane, string> = {
  internet: "The internet",
  tailnet: "The tailnet",
  vpn: "A VPN",
  private: "A private network",
  docker: "Docker's networks",
  local: "Only this server",
}

export function reachLane(listener: Pick<Listener, "reach" | "network">): ReachLane {
  if (listener.reach === "loopback") return "local"
  if (internetFacing(listener)) return "internet"
  switch (listener.network) {
    case "tailnet":
      return "tailnet"
    case "vpn":
      return "vpn"
    case "docker":
    case "bridge":
      return "docker"
    default:
      return "private"
  }
}

/** The chip a lane is narrowed by. */
export function laneFilter(lane: ReachLane): ReachGroup {
  return lane === "internet" ? "internet" : lane === "local" ? "local" : "private"
}

type Severity = 0 | 1 | 2 | 3
const SEVERITY: Record<"critical" | "warning" | "notice", Severity> = {
  critical: 3,
  warning: 2,
  notice: 1,
}

function severity(socket: Socket): Severity {
  const verdict = reachVerdict(socket)
  return verdict ? SEVERITY[verdict] : 0
}

export type PictureTone = "default" | "warning" | "danger"

function toneOf(level: Severity): PictureTone {
  return level === 3 ? "danger" : level === 2 ? "warning" : "default"
}

export type PictureLane = { lane: ReachLane; sockets: number; tone: PictureTone }
export type PictureOwner = {
  key: string
  title: string
  sockets: Socket[]
  lanes: ReachLane[]
  tone: PictureTone
}
export type PictureWire = {
  lane: ReachLane
  owner: string
  tone: PictureTone
  /** Somebody is connected to a socket on this wire now. */
  busy: boolean
}

export type ReachPictureModel = {
  lanes: PictureLane[]
  owners: PictureOwner[]
  wires: PictureWire[]
  /** Owners past the picture's room, which the list below holds. */
  hidden: number
}

/**
 * The page's picture: each network that reaches a socket, wired to the
 * programs listening there, the worst first. A wire is as red as the worst
 * socket on it; it moves where somebody is connected. Past `room` owners the
 * rest are counted rather than drawn — the list under the picture has them.
 */
export function reachPicture(sockets: Socket[], room = 8): ReachPictureModel {
  const lanes = new Map<ReachLane, { sockets: number; level: Severity }>()
  const owners = new Map<string, { title: string; sockets: Socket[]; level: Severity }>()
  const wires = new Map<string, PictureWire & { level: Severity }>()
  for (const socket of sockets) {
    const lane = reachLane(socket)
    const level = severity(socket)
    const into = lanes.get(lane) ?? { sockets: 0, level: 0 }
    into.sockets++
    into.level = Math.max(into.level, level) as Severity
    lanes.set(lane, into)

    const key = ownerKey(socket)
    const owner = owners.get(key) ?? { title: ownerTitle(socket), sockets: [], level: 0 }
    owner.sockets.push(socket)
    owner.level = Math.max(owner.level, level) as Severity
    owners.set(key, owner)

    const id = `${lane}\u0000${key}`
    const wire = wires.get(id) ?? { lane, owner: key, tone: "default", busy: false, level: 0 }
    wire.level = Math.max(wire.level, level) as Severity
    wire.busy ||= (socketClients(socket)?.count ?? 0) > 0
    wires.set(id, wire)
  }
  const ranked = [...owners]
    .map(([key, o]) => ({
      key,
      title: o.title,
      sockets: [...o.sockets].sort((a, b) => a.port - b.port),
      level: o.level,
      lanes: LANE_ORDER.filter((lane) => o.sockets.some((s) => reachLane(s) === lane)),
    }))
    .sort(
      (a, b) =>
        b.level - a.level ||
        LANE_ORDER.indexOf(a.lanes[0]) - LANE_ORDER.indexOf(b.lanes[0]) ||
        a.title.localeCompare(b.title),
    )
  const shown = ranked.slice(0, room)
  const drawn = new Set(shown.map((o) => o.key))
  return {
    lanes: LANE_ORDER.filter((lane) => lanes.has(lane)).map((lane) => ({
      lane,
      sockets: lanes.get(lane)!.sockets,
      tone: toneOf(lanes.get(lane)!.level),
    })),
    owners: shown.map(({ level, ...o }) => ({ ...o, tone: toneOf(level) })),
    wires: [...wires.values()]
      .filter((w) => drawn.has(w.owner))
      .map(({ level, ...w }) => ({ ...w, tone: toneOf(level) })),
    hidden: ranked.length - shown.length,
  }
}

/** One program, as the picture draws it: a container, an app, a unit or a process and its account. */
function ownerKey(socket: Socket): string {
  if (socket.container) return `container:${socket.container.id}`
  return `owner:${ownerTitle(socket)}\u0000${socket.user ?? ""}`
}

/** The list keys its view and filters under, which the port page's rail reads too. */
export const PORTS_VIEW_KEYS = {
  query: "proxy.ports.query",
  reach: "proxy.ports.reach",
  proto: "proxy.ports.proto",
  sort: "proxy.ports.sort",
  hideEphemeral: "proxy.ports.hideEphemeral",
  onlyNew: "proxy.ports.new",
} as const
