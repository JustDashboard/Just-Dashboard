import type {
  CounterTotal,
  EntryFlow,
  EntryReadiness,
  ExternalEvidence,
  ForwardCheck,
  GatewayImpact,
  GatewayNAT,
  GatewayForward,
} from "@/lib/types"
import type { Tone } from "@/components/tone"
import { portProduct } from "@/components/product-logo"

/**
 * What a forward or a NAT entry says about itself, as the words and the
 * request bodies the Gateway page needs in more than one place — the picture,
 * the list, the editor and the switch in a row's trailing slot all describe
 * the same entry, so they describe it from here and cannot disagree.
 */

/** The body `POST /gateway/forwards` and `PUT /gateway/forwards/{id}` take. */
export type ForwardRequest = {
  name: string
  protocol: GatewayForward["protocol"]
  interface: string
  ports: string
  target: string
  targetPort: string
  sourceNat: GatewayForward["sourceNat"]
  sources: string[]
  enabled?: boolean
}

/** The body `POST /gateway/nat` and `PUT /gateway/nat/{id}` take. */
export type NATRequest = {
  name: string
  source: string
  interface: string
  toAddress: string
  mode?: NATMode
  translated?: string
  destinations?: string[]
  enabled?: boolean
}

/** How a NAT entry translates. */
export type NATMode = "masquerade" | "snat" | "one-to-one" | "nptv6"

export const PROTOCOL_WORD: Record<GatewayForward["protocol"], string> = {
  tcp: "TCP",
  udp: "UDP",
  both: "TCP and UDP",
}

/** The port the target answers on: its own, or the one the visitor arrived on. */
export const targetPortOf = (f: Pick<GatewayForward, "ports" | "targetPort">) =>
  f.targetPort || f.ports

/** An address with its port, bracketed where the address has colons of its own. */
export function hostPort(address: string, port: string) {
  return address.includes(":") ? `[${address}]:${port}` : `${address}:${port}`
}

/** `:8080 → 10.0.4.5:80`, the way a forward is named wherever it is listed. */
export const forwardTitle = (f: GatewayForward) =>
  `:${f.ports} → ${hostPort(f.target, targetPortOf(f))}`

/** The first port of a port or a range, for the product that usually answers there. */
export function firstPort(ports: string) {
  const n = Number.parseInt(ports, 10)
  return Number.isInteger(n) ? n : undefined
}

/** The product that usually answers on the target's port, when there is one. */
export function targetProduct(f: Pick<GatewayForward, "ports" | "targetPort">) {
  const port = firstPort(targetPortOf(f))
  return port === undefined ? undefined : portProduct(port)
}

/** Who may arrive: "anyone", a network, or the first two and a count. */
export function sourcesWord(sources: string[]) {
  if (sources.length === 0) return "anyone"
  if (sources.length <= 2) return sources.join(", ")
  return `${sources.slice(0, 2).join(", ")} +${sources.length - 2}`
}

/** What happens to the visitor's address on its way to the target. */
export const addressWord = (f: Pick<GatewayForward, "masquerade">) =>
  f.masquerade ? "masqueraded" : "keeps the visitor's address"

/** The facts a forward's second line is made of, in the order they are read. */
export function forwardFacts(f: GatewayForward) {
  return [
    f.name,
    PROTOCOL_WORD[f.protocol],
    f.interface ? `on ${f.interface}` : "any device",
    sourcesWord(f.sources),
    addressWord(f),
  ]
}

/** Comma- or space-separated networks, as the sources field takes them. */
export function splitList(raw: string) {
  return raw
    .split(/[\s,]+/)
    .map((s) => s.trim())
    .filter(Boolean)
}

/** A forward as the request that would save it unchanged, optionally with its switch moved. */
export function forwardRequest(f: GatewayForward, enabled?: boolean): ForwardRequest {
  return {
    name: f.name,
    protocol: f.protocol,
    interface: f.interface,
    ports: f.ports,
    target: f.target,
    targetPort: f.targetPort,
    sourceNat: f.sourceNat,
    sources: f.sources,
    ...(enabled === undefined ? {} : { enabled }),
  }
}

/** A NAT entry as the request that would save it unchanged, optionally with its switch moved. */
export function natRequest(n: GatewayNAT, enabled?: boolean): NATRequest {
  return {
    name: n.name,
    source: n.source,
    interface: n.interface,
    toAddress: n.toAddress,
    mode: natMode(n),
    translated: n.translated ?? "",
    destinations: n.destinations ?? [],
    ...(enabled === undefined ? {} : { enabled }),
  }
}

/** An entry's mode, from the field or, for an older reading, from its address. */
export function natMode(n: Pick<GatewayNAT, "mode" | "toAddress">): NATMode {
  return n.mode ?? (n.toAddress ? "snat" : "masquerade")
}

/** How an entry goes out, as its row says it. */
export function natWord(n: GatewayNAT) {
  const dests = n.destinations ?? []
  const only =
    dests.length === 0
      ? ""
      : dests.length === 1
        ? ` · only to ${dests[0]}`
        : ` · only to ${dests.length} networks`
  switch (natMode(n)) {
    case "one-to-one":
      return `one-to-one with ${n.translated}`
    case "nptv6":
      return `IPv6 prefix ↔ ${n.translated}`
    case "snat":
      return `as ${n.toAddress}${only}`
  }
  return `masqueraded${only}`
}

/** The product that keeps an entry, from its owner's word: "wireguard:wg0". */
export function ownerOf(owner: string) {
  const [product, device] = owner.split(":")
  const name =
    product === "wireguard" ? "WireGuard" : product === "tailscale" ? "Tailscale" : product
  return { product, name, device }
}

/** A counter as a short figure: 1.2k, 4.8M. Under a thousand it is the number. */
export function compact(n: number) {
  return Intl.NumberFormat("en-US", { notation: "compact", maximumFractionDigits: 1 }).format(n)
}

/**
 * The entries whose packet counter grew between two reads of the gateway,
 * keyed `forward:<id>` and `nat:<id>`. A counter that fell was reset by the
 * table being loaded again, and an entry with no earlier reading has not
 * grown — nothing is moving on the strength of a figure that has no past.
 */
export function grown(before: Record<string, number>, after: Record<string, number>) {
  return new Set(Object.keys(after).filter((key) => key in before && after[key] > before[key]))
}

/** Every entry's packet counter, under the key `grown` compares by. */
export function counters(forwards: GatewayForward[], nat: GatewayNAT[]) {
  return Object.fromEntries([
    ...forwards.map((f) => [`forward:${f.id}`, f.packets] as const),
    ...nat.map((n) => [`nat:${n.id}`, n.packets] as const),
  ])
}

/** An entry's installed state as a short word and the tone it is read in. */
export function readinessWord(r: EntryReadiness | undefined): { label: string; tone: Tone } {
  if (!r) return { label: "Unknown", tone: "default" }
  switch (r.policy) {
    case "disabled":
      return { label: "Switched off", tone: "default" }
    case "not_loaded":
      return { label: "Not loaded", tone: "warning" }
    case "missing":
      return { label: "Rules missing", tone: "warning" }
    case "partial":
      return { label: "Partly installed", tone: "warning" }
    case "drift":
      return { label: "Extra rules", tone: "warning" }
  }
  if (!r.forwarding) return { label: "Forwarding off", tone: "warning" }
  if (!r.ready) return { label: "Admission missing", tone: "warning" }
  if (r.reachability === "verified") return { label: "Verified from outside", tone: "success" }
  if (r.reachability === "failed") return { label: "Failed from outside", tone: "danger" }
  return { label: "Installed", tone: "default" }
}

/** Whether an installed entry has been measured from outside. */
export function reachabilityWord(r: EntryReadiness | undefined) {
  if (!r || r.policy === "disabled") return undefined
  switch (r.reachability) {
    case "verified":
      return "reached from an external source"
    case "failed":
      return "refused from an external source"
  }
  return "reachability unmeasured"
}

/** A preview impact's tone. */
export const impactTone = (i: Pick<GatewayImpact, "severity">): Tone =>
  i.severity === "refused" ? "danger" : i.severity === "warning" ? "warning" : "default"

/** A modeled flow's verdict as words. */
export function flowWord(f: Pick<EntryFlow, "verdict">) {
  switch (f.verdict) {
    case "blocked":
      return "Dropped by a checked layer"
    case "unknown":
      return "A checked layer may drop it"
  }
  return "Passes every checked layer"
}

/** A modeled layer verdict's tone. */
export const layerTone = (verdict: string): Tone =>
  verdict === "blocked"
    ? "danger"
    : verdict === "unknown"
      ? "warning"
      : verdict === "restricted"
        ? "default"
        : "success"

/** A total across reloads, as a hint line: "1.2k packets since 3 Oct, across 2 reloads". */
export function totalWord(total: CounterTotal | undefined, since: (iso: string) => string) {
  if (!total) return undefined
  const reloads =
    total.resets > 0 ? `, across ${total.resets} reload${total.resets === 1 ? "" : "s"}` : ""
  return `${compact(total.packets)} packets${total.since ? ` since ${since(total.since)}` : ""}${reloads}`
}

/** A target check's result as a sentence head. */
export function checkWord(c: ForwardCheck) {
  switch (c.status) {
    case "answering":
      return `Something answers at ${c.target}`
    case "refused":
      return `${c.target} refused the connection`
    case "timeout":
      return `Nothing answered at ${c.target}`
    case "unreachable":
      return `No route to ${c.target}`
    case "not_measurable":
      return "UDP cannot be checked without the service's own protocol"
  }
  return `The check failed: ${c.detail}`
}

export const checkTone = (c: ForwardCheck): Tone =>
  c.status === "answering" ? "success" : c.status === "not_measurable" ? "default" : "warning"

/** External evidence as a sentence head. */
export function externalWord(e: ExternalEvidence) {
  const who = e.vantage || "an enrolled source"
  return e.status === "connected"
    ? `${who} connected to ${hostPort(e.address, String(e.port))}`
    : `${who} could not connect to ${hostPort(e.address, String(e.port))}`
}
