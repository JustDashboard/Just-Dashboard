import type { Listener } from "@/lib/types"
import { DANGEROUS_PORTS, type ProxyFinding } from "@/components/proxy/findings/shared"

export type PortFindingInput = { ports?: Listener[] }

/** A database or control port answering on every interface. */
export function portFindings({ ports }: PortFindingInput): ProxyFinding[] {
  const out: ProxyFinding[] = []

  const exposed = (ports ?? []).filter((l) => l.exposed)
  const dangerous = exposed.filter((l) => DANGEROUS_PORTS[l.port])
  if (dangerous.length > 0) {
    out.push({
      id: "ports.dangerous",
      level: "warning",
      title:
        dangerous.length === 1
          ? `${DANGEROUS_PORTS[dangerous[0].port]} answers on every interface`
          : `${dangerous.length} database or control ports answer on every interface`,
      detail: dangerous.map((l) => `${l.port}/${l.protocol} ${l.process || "unknown"}`).join(", "),
      advice:
        "Bind these to loopback or a private address, or close them in the firewall. A database port on the internet is the commonest way a server is emptied.",
      // The process names are read afresh each time; the ports are the finding.
      fingerprint: dangerous
        .map((l) => `${l.port}/${l.protocol}`)
        .sort()
        .join(" "),
      meta: "ports",
      href: "/proxy/ports",
    })
  }

  return out
}
