import type { Listener } from "@/lib/types"
import { type ProxyFinding } from "@/components/proxy/findings/shared"
import {
  dangerousPorts,
  ownerTitle,
  pastFirewallWords,
  reachWords,
  type DangerousPort,
} from "@/components/proxy/ports"
import { portsHref } from "@/components/proxy/ports-list"

export type PortFindingInput = { ports?: Listener[] }

/**
 * A service the security catalogue calls dangerous answering off this
 * machine — a database, a control plane, a remote desktop, an open resolver,
 * or the dashboard's own backend past its Caddy — on every interface, or on one address, which reaches as far as that address does.
 * Counted per port, not per socket: a database bound to two addresses, or to
 * 0.0.0.0 and ::, is one database. Levelled by the security posture's own
 * grade of each socket, which GET /ports carries: critical where the
 * internet can reach one, a warning where only a tailnet, a LAN or a bridge
 * can, or where the firewall's inbound default refuses it. A port Docker
 * publishes, or one a rule admits from anywhere, is past that default.
 */
export function portFindings({ ports }: PortFindingInput): ProxyFinding[] {
  const out: ProxyFinding[] = []

  const dangerous = dangerousPorts(ports ?? [])
  if (dangerous.length > 0) {
    const everywhere = dangerous.every(onEveryInterface)
    // The firewall's default is the host's, said once when it holds every
    // port and beside each port otherwise: Docker publishes past it, and a
    // rule may admit a port before it.
    const held = dangerous.every((d) => d.inboundDefault)
    out.push({
      id: "ports.dangerous",
      level: dangerous.some((d) => d.level === "critical") ? "critical" : "warning",
      title:
        dangerous.length === 1
          ? `${dangerous[0].service} answers on ${where(dangerous[0])}`
          : `${dangerous.length} dangerous services answer ${everywhere ? "on every interface" : "off this machine"}`,
      detail: dangerous
        .map(
          (d) =>
            `${d.port}/${d.protocol} ${ownerTitle(d.sockets[0])}${onEveryInterface(d) ? "" : ` on ${placed(d.sockets)}`}${held ? "" : firewallNote(d)}`,
        )
        .join(", ")
        .concat(
          held ? `, though the firewall's inbound default is ${dangerous[0].inboundDefault}` : "",
        ),
      // A socket already on one address is not fixed by binding it to "a
      // private address"; it may be on one.
      advice: `${
        everywhere
          ? "Bind these to loopback or a private address, or close them in the firewall."
          : "Bind these to 127.0.0.1 unless something on the same network needs them, or close them in the firewall."
      } ${
        // The catalogue's reason is one service's; several are named in the detail.
        dangerous.length === 1
          ? dangerous[0].sockets[0].danger
          : "Each is a service the security catalogue says should not face the internet."
      }`,
      meta: "ports",
      // Opens the ports page on these ports alone, whatever it was left
      // filtered to.
      href: portsHref({ q: `port:${[...new Set(dangerous.map((d) => d.port))].join(",")}` }),
    })
  }

  return out
}

/** Where the firewall stands on one port, when it does not stand the same on all. */
function firewallNote(port: DangerousPort): string {
  const past = pastFirewallWords(port)
  if (past) return ` (${past.charAt(0).toLowerCase()}${past.slice(1)})`
  return port.inboundDefault ? ` (the firewall's inbound default is ${port.inboundDefault})` : ""
}

/** A wildcard bind already covers any one address the port is also on. */
function onEveryInterface(port: DangerousPort): boolean {
  return port.sockets.some((l) => l.scope !== "interface")
}

function where(port: DangerousPort): string {
  if (onEveryInterface(port)) return "every interface"
  return port.sockets.length === 1 ? port.sockets[0].address : `${port.sockets.length} addresses`
}

/**
 * Each address with the network it is on, one network named once: a Redis on
 * the tailnet's two addresses is "100.110.34.31 and fd7a:… (Tailnet only ·
 * tailscale0)".
 */
function placed(sockets: Listener[]): string {
  const byNetwork = new Map<string, string[]>()
  for (const socket of sockets) {
    const words = reachWords(socket)
    byNetwork.set(words, [...(byNetwork.get(words) ?? []), socket.address])
  }
  return [...byNetwork]
    .map(([words, addresses]) => `${addresses.join(" and ")} (${words})`)
    .join(", ")
}
