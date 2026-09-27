import type { StreamStatus } from "@/lib/types"
import { DANGEROUS_PORTS, type ProxyFinding } from "@/components/proxy/findings/shared"

export type StreamFindingInput = { streams?: StreamStatus }

/** Streams nginx is not reading, and a forward open to anyone. */
export function streamFindings({ streams }: StreamFindingInput): ProxyFinding[] {
  const out: ProxyFinding[] = []

  if (streams && streams.streams.length > 0 && !streams.included) {
    out.push({
      id: "streams.not-included",
      level: "warning",
      title: `${streams.streams.length} stream${streams.streams.length === 1 ? " is" : "s are"} written but nginx is not reading ${streams.streams.length === 1 ? "it" : "them"}`,
      detail: `nginx.conf has no stream block including ${streams.dir}.`,
      advice:
        "Add the include the Streams page prints, at the top level of nginx.conf beside the http block.",
      meta: "streams",
      href: "/proxy/streams",
    })
  }
  for (const stream of streams?.streams ?? []) {
    if (stream.allowFrom.length > 0) continue
    const service = DANGEROUS_PORTS[stream.listen]
    out.push({
      id: `stream.open.${stream.name}`,
      level: service ? "warning" : "notice",
      title: `Stream ${stream.name} forwards port ${stream.listen} to anyone`,
      detail: `${stream.protocol.toUpperCase()} ${stream.listen} → ${stream.upstream} with no allow list${service ? `, and ${stream.listen} is ${service}` : ""}.`,
      advice:
        "A stream has no authentication of its own. Restrict the source unless the service behind it authenticates for itself.",
      meta: "stream",
      href: "/proxy/streams",
    })
  }

  return out
}
