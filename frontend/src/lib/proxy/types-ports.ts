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

export type Listener = {
  protocol: string
  /** The socket's family: a service on 0.0.0.0 and :: is one of each. */
  family: "ipv4" | "ipv6"
  address: string
  port: number
  pid: number
  /** The owner's parent; absent where it could not be read. */
  ppid?: number
  process: string
  cmdline?: string
  user?: string
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
