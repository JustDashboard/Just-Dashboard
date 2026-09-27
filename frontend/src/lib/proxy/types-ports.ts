/**
 * How far a socket's bind reaches, from its address alone: loopback is this
 * machine only, interface is one specific address (a tailnet, a LAN, a
 * bridge, a public IP), all is 0.0.0.0 or ::.
 */
export type ListenerScope = "loopback" | "interface" | "all"

export type Listener = {
  protocol: string
  address: string
  port: number
  pid: number
  process: string
  cmdline?: string
  user?: string
  scope: ListenerScope
  /** Every scope but loopback: reachable from off this machine. */
  exposed: boolean
}
