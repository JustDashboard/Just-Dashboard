import type {
  NetworkEgressIdentity,
  NetworkFlowEdge,
  NetworkIncident,
  NetworkLink,
  NetworkLivePoint,
} from "@/lib/types"

/** What a family's identity establishes, in a few words for the identity line. */
export function identityVerdict(identity: NetworkEgressIdentity): string {
  switch (identity.public) {
    case "nic":
      return "NIC address is public"
    case "translated":
      return "translated upstream; public address not observed"
    case "unobserved":
      return "public address not observed"
    case "no_route":
      return "no route out"
    default:
      return "could not be read"
  }
}

/** The words for where an address sits, as the identity table says them. */
export const SCOPE_WORD: Record<NetworkEgressIdentity["sourceScope"], string> = {
  public: "public",
  private: "private",
  shared: "carrier-grade NAT",
  "unique-local": "unique local",
  "link-local": "link-local",
  loopback: "loopback",
  none: "none",
}

/**
 * How old the live ring's newest reading is. The sampler reads every two
 * seconds and the page asks every two, so anything past three intervals is a
 * stream that stopped, whatever the last poll said.
 */
export function observationAge(
  newest: number,
  clockSeconds: number,
  interval = 2,
): { seconds: number; stale: boolean } | undefined {
  if (!newest) return undefined
  const seconds = Math.max(0, Math.round(clockSeconds - newest))
  return { seconds, stale: seconds > interval * 3 }
}

/** The newest point across every device's ring. */
export function newestPoint(series: Record<string, NetworkLivePoint[]>): number {
  let newest = 0
  for (const points of Object.values(series)) newest = Math.max(newest, points.at(-1)?.t ?? 0)
  return newest
}

/** An age in words: "2 s", "3 min", "1 h". */
export function ageWords(seconds: number): string {
  if (seconds < 60) return `${seconds} s`
  if (seconds < 3600) return `${Math.floor(seconds / 60)} min`
  return `${Math.floor(seconds / 3600)} h`
}

/**
 * Which nodes each node exchanges traffic with, from the flow edges, both
 * ways. The host is in it too, so pointing at a node lights the server's
 * wires to every node it trades with through it.
 */
export function flowNeighbours(edges: NetworkFlowEdge[]): Map<string, Set<string>> {
  const out = new Map<string, Set<string>>()
  const link = (a: string, b: string) => {
    if (!out.has(a)) out.set(a, new Set())
    out.get(a)!.add(b)
  }
  for (const e of edges) {
    link(e.from, e.to)
    link(e.to, e.from)
  }
  return out
}

/** Every flow a node takes part in, either end. */
export function nodeFlows(edges: NetworkFlowEdge[], node: string): number {
  return edges.reduce((n, e) => (e.from === node || e.to === node ? n + e.flows : n), 0)
}

export type WindowShare = { name: string; role: NetworkLink["role"]; bytes: number }

/**
 * What each device carried in a window of the live ring, busiest first: the
 * resources a chart's selection drills into. A reading is bytes a second
 * over two seconds, so a point carries twice its rate.
 */
export function windowBreakdown(
  series: Record<string, NetworkLivePoint[]>,
  links: NetworkLink[],
  fromMs: number,
  toMs: number,
  interval = 2,
): WindowShare[] {
  const out: WindowShare[] = []
  for (const link of links) {
    if (link.role === "container" || link.owner === "kernel" || link.role === "loopback") continue
    let bytes = 0
    for (const p of series[link.name] ?? []) {
      const at = p.t * 1000
      if (at >= fromMs && at <= toMs) bytes += (p.rx + p.tx) * interval
    }
    if (bytes > 0) out.push({ name: link.name, role: link.role, bytes })
  }
  return out.sort((a, b) => b.bytes - a.bytes || a.name.localeCompare(b.name))
}

/** How long an incident lasted, or has lasted, in words. */
export function incidentSpan(incident: NetworkIncident, nowMs: number): string {
  const end = incident.resolvedAt ? Date.parse(incident.resolvedAt) : nowMs
  return ageWords(Math.max(0, Math.round((end - Date.parse(incident.openedAt)) / 1000)))
}
