import { parseAddress } from "@/lib/cidr"
import type { NetworkRoute, NetworkRouting, NetworkRule } from "@/lib/types"
import { inferredAnswer, type RouteFamily } from "./decision-reading"

type Prefix = { bytes: number[]; bits: number; family: RouteFamily }

/** An address or a network the route filter asks about; null when it is neither. */
export function parseTarget(text: string): Prefix | null {
  const [address, prefix, extra] = text.trim().split("/")
  if (!address || extra !== undefined) return null
  const bytes = parseAddress(address)
  if (!bytes) return null
  const max = bytes.length * 8
  const bits = prefix === undefined ? max : Number(prefix)
  if (prefix !== undefined && (!/^\d{1,3}$/.test(prefix) || bits > max)) return null
  return { bytes, bits, family: bytes.length === 4 ? "inet" : "inet6" }
}

function routePrefix(route: NetworkRoute): Prefix | null {
  if (route.destination === "default")
    return {
      bytes: Array(route.family === "inet" ? 4 : 16).fill(0),
      bits: 0,
      family: route.family,
    }
  return parseTarget(route.destination)
}

function contains(outer: Prefix, inner: Prefix) {
  if (outer.family !== inner.family || outer.bits > inner.bits) return false
  for (let bit = 0; bit < outer.bits; bit++) {
    const mask = 0x80 >> (bit & 7)
    if ((outer.bytes[bit >> 3] & mask) !== (inner.bytes[bit >> 3] & mask)) return false
  }
  return true
}

/** Whether a route's destination holds the whole target. */
export function routeCovers(route: NetworkRoute, target: Prefix) {
  const prefix = routePrefix(route)
  return prefix !== null && contains(prefix, target)
}

/**
 * The routes of each table that cover a target, and the one a lookup in that
 * table selects: the most specific, the lowest metric between equals. Which
 * table a packet reaches is the rules' decision; the kernel lookup answers
 * that for a literal address.
 */
export function targetMatches(tables: NetworkRouting["tables"], target: Prefix) {
  const out = new Map<number, { covering: NetworkRoute[]; selected?: NetworkRoute }>()
  for (const table of tables) {
    const covering = table.routes.filter(
      (route) => route.family === target.family && routeCovers(route, target),
    )
    let selected: NetworkRoute | undefined
    let bits = -1
    for (const route of covering) {
      const prefix = routePrefix(route)!
      if (
        prefix.bits > bits ||
        (prefix.bits === bits && selected && route.metric < selected.metric)
      ) {
        selected = route
        bits = prefix.bits
      }
    }
    out.set(table.id, { covering, selected })
  }
  return out
}

/** A rule's selectors as `ip rule` writes them. */
export function ruleSelectors(rule: NetworkRule) {
  const parts: string[] = []
  if (rule.not) parts.push("not")
  if (rule.from) parts.push(`from ${rule.from}`)
  if (rule.to) parts.push(`to ${rule.to}`)
  if (rule.iif) parts.push(`iif ${rule.iif}`)
  if (rule.oif) parts.push(`oif ${rule.oif}`)
  if (rule.fwmark) parts.push(`fwmark ${rule.fwmark}`)
  if (rule.uidRange) parts.push(`uidrange ${rule.uidRange}`)
  if (rule.tos) parts.push(`tos ${rule.tos}`)
  if (rule.ipProto) parts.push(`ipproto ${rule.ipProto}`)
  if (rule.sport) parts.push(`sport ${rule.sport}`)
  if (rule.dport) parts.push(`dport ${rule.dport}`)
  if (rule.l3mdev) parts.push("l3mdev")
  return parts
}

/** What a rule does with what it selects, in words. */
export function ruleThen(rule: NetworkRule) {
  if (rule.action === "lookup") {
    if (rule.l3mdev) return "look up its VRF's table"
    const suppress =
      rule.suppressPrefixLength !== undefined
        ? `, ignoring answers /${rule.suppressPrefixLength} or shorter`
        : ""
    return `look up ${rule.tableName ?? rule.table}${suppress}`
  }
  if (rule.action === "goto")
    return rule.unresolved
      ? `goto ${rule.goto} (no rule there; passed over)`
      : `continue at ${rule.goto}`
  if (rule.action === "nop") return "nothing"
  return rule.action
}

/**
 * The rule and table to light for the browser's replies, and how much of it
 * the kernel vouches for: `kernel` names a table the kernel reported and a
 * rule the model evaluated to it; `table` names the kernel's table with the
 * rule undetermined; `inferred` is the old reading from routes alone, used
 * where the server sent no decision.
 */
export function replyHighlight(routing: NetworkRouting, family: RouteFamily) {
  const decision = routing.clientDecision
  const clientFamily: RouteFamily = routing.clientPath.address.includes(":") ? "inet6" : "inet"
  if (decision && clientFamily === family) {
    return {
      basis: decision.basis === "kernel_and_model" ? ("kernel" as const) : ("table" as const),
      rulePriority: decision.basis === "kernel_and_model" ? decision.rulePriority : undefined,
      table: decision.table,
      candidates: decision.candidates,
      reason: decision.reason,
    }
  }
  const inferred = inferredAnswer(routing, family)
  if (!inferred) return undefined
  return {
    basis: "inferred" as const,
    rulePriority: inferred.rule.priority,
    table: inferred.table,
    candidates: [] as number[],
    reason: undefined as string | undefined,
  }
}

export type NexthopDraft = { gateway: string; device: string; weight: string }

/** Why a multipath draft would be refused, before the server says so. */
export function nexthopProblem(rows: NexthopDraft[], destination: string) {
  if (rows.length < 2) return "A multipath route needs at least two nexthops."
  if (rows.length > 16) return "Use at most sixteen nexthops."
  const family = parseTarget(destination.trim() === "default" ? "" : destination)?.family
  const seen = new Set<string>()
  for (const [index, row] of rows.entries()) {
    const gateway = row.gateway.trim()
    if (!gateway && !row.device) return `Nexthop ${index + 1} needs a gateway, a device, or both.`
    if (gateway) {
      const parsed = parseAddress(gateway)
      if (!parsed) return `Nexthop ${index + 1}'s gateway is not an address.`
      const rowFamily: RouteFamily = parsed.length === 4 ? "inet" : "inet6"
      if (family && rowFamily !== family)
        return `Nexthop ${index + 1}'s gateway is not in the destination's family.`
    }
    if (row.weight.trim()) {
      const weight = Number(row.weight)
      if (!Number.isInteger(weight) || weight < 1 || weight > 256)
        return `Nexthop ${index + 1}'s weight is 1 to 256.`
    }
    const key = `${gateway}|${row.device}`
    if (seen.has(key)) return `Nexthop ${index + 1} repeats an earlier one.`
    seen.add(key)
  }
  return undefined
}

/** A route's hop, whatever form it takes. */
export function routeVia(route: NetworkRoute) {
  if (route.gateway) return route.gateway
  if (route.nexthops.length > 0)
    return route.nexthops
      .map(
        (n) => `${n.gateway ?? n.device ?? "?"}${n.weight && n.weight > 1 ? ` ×${n.weight}` : ""}`,
      )
      .join(", ")
  return undefined
}

export type RouteRow = {
  /** Stable within one reading: a kernel route has no id of its own. */
  key: string
  route: NetworkRoute
  table: { id: number; name: string }
}

/** Every route with the table it is in, in the tables' order: main first. */
export function routeRows(tables: NetworkRouting["tables"]): RouteRow[] {
  return tables.flatMap((table) =>
    table.routes.map((route, index) => ({
      key: `${table.id}:${route.family}:${route.destination}:${route.device ?? ""}:${index}`,
      route,
      table: { id: table.id, name: table.name },
    })),
  )
}

/**
 * The default route the page names itself by: the main table's, IPv4 before
 * IPv6 and the lowest metric between two, because that is the one "how does
 * this server reach the internet" is asking about.
 */
export function defaultRoute(routing: NetworkRouting) {
  const main = routing.tables.find((table) => table.id === 254) ?? routing.tables[0]
  const defaults = (main?.routes ?? []).filter(
    (route) => route.destination === "default" && route.type === "unicast",
  )
  const of = (family: RouteFamily) =>
    defaults.filter((route) => route.family === family).sort((a, b) => a.metric - b.metric)[0]
  return of("inet") ?? of("inet6")
}

/** Whether the server routes for others, as the one state the page opens on. */
export function forwardingState(forwarding: NetworkRouting["forwarding"]) {
  const v4 = forwarding.ipv4.enabled
  const v6 = forwarding.ipv6.enabled
  if (v4 && v6) return { on: true, label: "Forwarding IPv4 and IPv6" }
  if (v4) return { on: true, label: "Forwarding IPv4 only" }
  if (v6) return { on: true, label: "Forwarding IPv6 only" }
  return { on: false, label: "Not forwarding" }
}

type Forwarding = NetworkRouting["forwarding"]["ipv4"]

const HEALTH_WORD: Record<NonNullable<Forwarding["health"]>["status"], string> = {
  forwarding: "Forwarding traffic",
  idle: "On · idle",
  measuring: "On · measuring",
  partial: "Some devices do not forward",
  off: "On",
  unknown: "On · not measured",
}

/**
 * One family's forwarding in a dot and a few words: the kernel's own count
 * where it was measured, the switch's state where it was not. A rate says
 * forwarding happens; it does not say a particular flow was meant to pass.
 */
export function forwardingReading(state: Forwarding): {
  tone: "running" | "stopped" | "warning" | "unknown"
  /** The state alone: "Forwarding traffic". */
  word: string
  /** How much, where the kernel's counter was read twice: "43 datagrams/s over 15s". */
  rate?: string
  /** Both, for a line that has room for one string. */
  label: string
} {
  if (!state.available)
    return { tone: "unknown", word: "Not on this kernel", label: "Not on this kernel" }
  if (!state.enabled) return { tone: "stopped", word: "Off", label: "Off" }
  const health = state.health
  if (!health) return { tone: "running", word: "On", label: "On" }
  const rate =
    health.ratePerSecond !== undefined
      ? `${health.ratePerSecond < 10 ? health.ratePerSecond.toFixed(1) : Math.round(health.ratePerSecond)} datagrams/s over ${Math.round(health.windowSeconds ?? 0)}s`
      : undefined
  const word = HEALTH_WORD[health.status]
  return {
    tone: health.status === "partial" ? "warning" : "running",
    word,
    rate,
    label: rate ? `${word} · ${rate}` : word,
  }
}

/**
 * What depends on forwarding, each thing once with the families it needs:
 * the server lists them per family, and Tailscale's exit node is one thing
 * whichever family is asked about.
 */
export function forwardingNeeds(forwarding: NetworkRouting["forwarding"]) {
  const needs: { need: string; ipv4: boolean; ipv6: boolean }[] = []
  for (const family of ["ipv4", "ipv6"] as const)
    for (const need of forwarding[family].neededBy) {
      const held = needs.find((n) => n.need === need)
      if (held) held[family] = true
      else needs.push({ need, ipv4: family === "ipv4", ipv6: family === "ipv6" })
    }
  return needs
}

/** The product a dependency names, for the mark before it: "5 Docker networks" is Docker's. */
export function needProduct(need: string) {
  const text = need.toLowerCase()
  if (text.includes("docker")) return "docker"
  if (text.includes("wireguard")) return "wireguard"
  if (text.includes("tailscale")) return "tailscale"
  return undefined
}

/**
 * Whether the kernel can be asked about what was typed: one literal address,
 * and a source, when given, that is one too. A network narrows the tables and
 * is not a packet's destination.
 */
export function askable(target: string, source: string) {
  const one = (text: string) => !text.includes("/") && parseTarget(text) !== null
  return one(target.trim()) && (!source.trim() || one(source.trim()))
}
