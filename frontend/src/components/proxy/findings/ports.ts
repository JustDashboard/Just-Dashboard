import type { Listener } from "@/lib/types"
import { type ProxyFinding } from "@/components/proxy/findings/shared"
import { dangerousPorts, reachWords, type DangerousPort } from "@/components/proxy/ports"

export type PortFindingInput = { ports?: Listener[] }

/**
 * A database or control port answering off this machine — on every
 * interface, or on one address, which reaches as far as that address does.
 * Counted per port, not per socket: a database bound to two addresses, or to
 * 0.0.0.0 and ::, is one database. Levelled by the security posture's own
 * grade of each socket, which GET /ports carries: critical where the
 * internet can reach one, a warning where only a tailnet, a LAN or a bridge
 * can, or where the firewall denies inbound by default.
 */
export function portFindings({ ports }: PortFindingInput): ProxyFinding[] {
  const out: ProxyFinding[] = []

  const dangerous = dangerousPorts(ports ?? [])
  if (dangerous.length > 0) {
    const everywhere = dangerous.every(onEveryInterface)
    // The firewall's default is the host's, so one port carrying it is all of them.
    const inbound = dangerous.find((d) => d.inboundDefault)?.inboundDefault
    out.push({
      id: "ports.dangerous",
      level: dangerous.some((d) => d.level === "critical") ? "critical" : "warning",
      title:
        dangerous.length === 1
          ? `${dangerous[0].service} answers on ${where(dangerous[0])}`
          : `${dangerous.length} database or control ports answer ${everywhere ? "on every interface" : "off this machine"}`,
      detail: dangerous
        .map(
          (d) =>
            `${d.port}/${d.protocol} ${d.sockets[0].process || "unknown"}${onEveryInterface(d) ? "" : ` on ${placed(d.sockets)}`}`,
        )
        .join(", ")
        .concat(inbound ? `, though the firewall's inbound default is ${inbound}` : ""),
      // A socket already on one address is not fixed by binding it to "a
      // private address"; it may be on one.
      advice: everywhere
        ? "Bind these to loopback or a private address, or close them in the firewall. A database port on the internet is the commonest way a server is emptied."
        : "Bind these to 127.0.0.1 unless something on the same network needs them, or close them in the firewall. A database port on the internet is the commonest way a server is emptied.",
      meta: "ports",
      href: "/proxy/ports",
    })
  }

  return out
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
