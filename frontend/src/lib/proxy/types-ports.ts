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
 * What a reach is made of, as the posture names it: `private` is a LAN or a
 * cloud's private address on the uplink (which a provider may map a public
 * address onto), `docker` Docker's own bridge (docker0, br-<network id>),
 * `bridge` any other bridge or veth only this host's guests are on, and
 * `link-local` a link-local address on a link to other machines.
 */
export type ListenerNetwork =
  "loopback" | "all" | "public" | "tailnet" | "vpn" | "private" | "docker" | "bridge" | "link-local"

export type Listener = {
  protocol: string
  /** The socket's family: a service on 0.0.0.0 and :: is one of each. */
  family: "ipv4" | "ipv6"
  address: string
  port: number
  pid: number
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
}
