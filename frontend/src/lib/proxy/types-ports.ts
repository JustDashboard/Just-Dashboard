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

export type Listener = {
  protocol: string
  address: string
  port: number
  pid: number
  process: string
  cmdline?: string
  user?: string
  scope: ListenerScope
  reach: ListenerReach
  /** Every scope but loopback: reachable from off this machine. */
  exposed: boolean
}
