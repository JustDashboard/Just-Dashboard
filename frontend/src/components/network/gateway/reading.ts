import type { GatewayForward, GatewayNAT } from "@/lib/types"
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
  enabled?: boolean
}

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
    ...(enabled === undefined ? {} : { enabled }),
  }
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
