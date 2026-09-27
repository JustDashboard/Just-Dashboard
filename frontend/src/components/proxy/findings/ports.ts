import type { Listener } from "@/lib/types"
import { DANGEROUS_PORTS, type ProxyFinding } from "@/components/proxy/findings/shared"
import { reachWhere } from "@/components/proxy/ports"

export type PortFindingInput = { ports?: Listener[] }

/**
 * A database or control port answering off this machine — on every
 * interface, or on one address, which reaches as far as that address does.
 */
export function portFindings({ ports }: PortFindingInput): ProxyFinding[] {
  const out: ProxyFinding[] = []

  const exposed = (ports ?? []).filter((l) => l.exposed)
  const dangerous = exposed.filter((l) => DANGEROUS_PORTS[l.port])
  if (dangerous.length > 0) {
    const everywhere = dangerous.every((l) => l.scope !== "interface")
    out.push({
      id: "ports.dangerous",
      level: "warning",
      title:
        dangerous.length === 1
          ? `${DANGEROUS_PORTS[dangerous[0].port]} answers on ${reachWhere(dangerous[0])}`
          : `${dangerous.length} database or control ports answer ${everywhere ? "on every interface" : "off this machine"}`,
      detail: dangerous
        .map(
          (l) =>
            `${l.port}/${l.protocol} ${l.process || "unknown"}${l.scope === "interface" ? ` on ${l.address}` : ""}`,
        )
        .join(", "),
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
