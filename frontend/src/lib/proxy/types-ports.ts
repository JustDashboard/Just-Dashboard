import type { FirewallRule } from "../types"

/**
 * How far a socket's bind reaches, from its address alone: loopback is this
 * machine only, interface is one specific address (a tailnet, a LAN, a
 * bridge, a public IP), all is 0.0.0.0 or ::.
 */
export type ListenerScope = "loopback" | "interface" | "all"

/**
 * Who can connect, from the address and the interface it is on — the grade
 * the security posture levels its findings by: loopback is this machine, host
 * a bridge only its containers and VMs reach, network a tailnet, VPN or
 * private address, public the internet, all every interface.
 */
export type ListenerReach = "loopback" | "host" | "network" | "public" | "all"

/**
 * What a reach is made of, as the posture names it: `uplink` is a private
 * address on the interface carrying the default route (a cloud instance's
 * own, which its provider maps a public address onto, or a server's behind a
 * router that may forward to it), `private` one on another NIC or on no
 * interface the host listed, `docker` Docker's own bridge (docker0,
 * br-<network id>), `bridge` any other bridge or veth only this host's guests
 * are on, and `link-local` a link-local address on a link to other machines.
 */
export type ListenerNetwork =
  | "loopback"
  | "all"
  | "public"
  | "tailnet"
  | "vpn"
  | "uplink"
  | "private"
  | "docker"
  | "bridge"
  | "link-local"

/** The container a socket answers for. */
export type ListenerContainer = {
  /** The short ID, as `docker ps` prints it. */
  id: string
  name: string
  image: string
  /** The compose project and service that started it. */
  project?: string
  service?: string
  /**
   * A port Docker publishes on the host (through docker-proxy, or its NAT
   * rules alone), rather than a container on the host's network holding
   * the socket itself.
   */
  published?: boolean
  /** The dashboard deployment it runs for. */
  deployment?: { projectId: number; project: string; environment: string }
}

export type Listener = {
  protocol: string
  /** The socket's family: a service on 0.0.0.0 and :: is one of each. */
  family: "ipv4" | "ipv6"
  address: string
  port: number
  /** 0 where no process holds it that the dashboard can see, or none does. */
  pid: number
  /** The owner's parent; absent where it could not be read. */
  ppid?: number
  /** The kernel's name for the owner, as other code matches on it. */
  process: string
  /** The program, where `process` is the name of a thread (node's "MainThread"). */
  displayName?: string
  cmdline?: string
  user?: string
  /** When the owner started, so an action can refuse a PID reused since. */
  startedAt?: string
  /**
   * Who supervises the owner, as the processes page names it: a systemd
   * unit, a container's short ID, a login session, or a PM2 app.
   */
  manager?: "systemd" | "container" | "session" | "pm2" | "kernel" | "unmanaged"
  managerName?: string
  /** The systemd .socket unit listening here, and the service it starts. */
  socketUnit?: string
  activates?: string
  container?: ListenerContainer
  /** "docker-nat": a port Docker's NAT rules alone publish, with no socket. */
  source?: "docker-nat"
  /** One of the dashboard's own sockets. */
  self?: boolean
  scope: ListenerScope
  reach: ListenerReach
  network: ListenerNetwork
  /** The device holding the address; absent for a wildcard or loopback bind. */
  interface?: string
  /** Every scope but loopback: reachable from off this machine. */
  exposed: boolean
  /**
   * The security posture's level for a finding on this socket, graded by the
   * same rules with the same firewall; absent where it raises none.
   */
  level?: "critical" | "warning"
  /** The firewall's inbound default ("deny"), when it is what holds `level` to a warning. */
  inboundDefault?: string
  /**
   * Why a firewall refusing inbound by default does not hold the socket:
   * Docker publishes it ahead of the default, or rule `firewallRule` admits
   * it from anywhere. Absent where the default holds it or there is none.
   */
  pastFirewall?: "docker" | "rule"
  firewallRule?: number
  /** What the firewall does with a connection to an exposed socket; absent on loopback. */
  firewall?: ListenerFirewall
  /** The enabled proxy sites whose upstream reaches this TCP socket. */
  routes?: ListenerRoute[]
  /** The nginx stream listening on this socket, where nginx holds it. */
  stream?: string
  /** On a socket nginx holds, how many enabled nginx sites listen on its port. */
  servedSites?: number
}

/** A proxy site forwarding to a socket. */
export type ListenerRoute = {
  site: string
  /** The site's first name; absent for a catch-all. */
  serverName?: string
  tls: boolean
}

/** A host port a container publishes, or keeps for its next start while stopped. */
export type HostPortBinding = {
  container: string
  hostIp?: string
  hostPort: number
  protocol: string
  running: boolean
}

/** GET /ports/free: ports nothing listens on and no container keeps, free when asked. */
export type PortsFree = {
  ports: number[]
  /** Containers' ports the search passed over although nothing listens on them. */
  skipped: HostPortBinding[]
  /** False where Docker did not answer, so containers' ports were not avoided. */
  containersChecked: boolean
}

/**
 * The firewall's answer for one socket. `allowed` and `blocked` name the rule
 * that decided, or none where the inbound default did; `restricted` is a rule
 * admitting `from` ahead of a default that refuses everyone else; `docker` is
 * a port Docker publishes ahead of ufw's or iptables' input chain.
 */
export type ListenerFirewall = {
  verdict: "allowed" | "restricted" | "blocked" | "off" | "docker" | "unknown"
  rule?: number
  /** The deciding rule's action as the firewall prints it: ALLOW, LIMIT, DENY. */
  action?: string
  from?: string
  /** The inbound default ("deny"). */
  default?: string
  backend?: "ufw" | "firewalld" | "iptables"
}

/** GET /ports/firewall: the firewall as the ports page hands rules off to it. */
export type PortsFirewall = {
  backend: "" | "ufw" | "firewalld" | "iptables"
  available: boolean
  enabled: boolean
  editable: boolean
  incoming?: string
  /** Inbound rules admitting a port nothing listens on and Docker does not publish. */
  orphanRules: FirewallRule[]
}

/** A span of port numbers, both ends included. */
export type PortRange = { low: number; high: number }

/** GET /ports/meta: what the ports page reads besides the sockets. */
export type PortsMeta = {
  /**
   * The span the kernel hands a socket bound to port 0 from
   * (net.ipv4.ip_local_port_range); null where it could not be read.
   */
  ephemeralRange: PortRange | null
}
