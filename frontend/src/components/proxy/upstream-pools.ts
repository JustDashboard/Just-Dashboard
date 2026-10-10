import type { DotTone } from "@/components/status-dot"
import type { PoolMember, UpstreamPool, UpstreamReport } from "@/lib/types"
import { plural } from "@/lib/format"
import { upstreamLabel, upstreamTone } from "./upstream-health"

/**
 * Reading the pools in GET /proxy/upstreams: who spreads a route's requests —
 * nginx across a block's servers or a name's addresses, or something past one
 * address that nginx cannot see — and what nginx itself logged meeting each
 * server over the last hour.
 */

/** The pools a site file forwards to; none from a backend that predates pools. */
export function poolsOfSite(report: UpstreamReport | undefined, path: string): UpstreamPool[] {
  return (report?.pools ?? []).filter((p) => p.files.includes(path))
}

const METHOD: Record<string, string> = {
  "round-robin": "in turn",
  least_conn: "to the server with the fewest active connections",
  ip_hash: "by the visitor's address",
  random: "at random",
}

function methodWords(method: string | undefined): string {
  if (!method) return ""
  if (METHOD[method]) return METHOD[method]
  if (method.startsWith("hash "))
    return `by a hash of ${method.slice(5).replace(/ consistent$/, "")}`
  if (method.startsWith("random ")) return "by the better of two random picks"
  return `by ${method}`
}

/** Who balances, in a sentence. */
export function balancingText(pool: UpstreamPool): string {
  const live = pool.members.filter((m) => !m.down)
  switch (pool.balancing) {
    case "native": {
      const keep = pool.keepalive ? `, keeping up to ${pool.keepalive} idle connections open` : ""
      const backups = live.filter((m) => m.backup).length
      const spare =
        backups === 0
          ? ""
          : ` ${backups === 1 ? "Its backup takes" : `Its ${backups} backups take`} over only when every primary has failed.`
      return `nginx sends requests ${methodWords(pool.method)} across ${plural(live.length - backups, "server")}${keep}, and sets one aside after it fails.${spare}`
    }
    case "native-dns": {
      const name = live.find((m) => (m.resolved?.length ?? 0) > 1)
      const count = name?.resolved?.length ?? 0
      return `nginx sends requests in turn across the ${count} addresses ${name?.address ?? "the name"} resolves to. It resolved them when it last loaded, so an address added since is not used until the next reload.`
    }
    default: {
      const provider = pool.provider
        ? ` The name suggests ${pool.provider}, which balances behind it.`
        : ""
      return `One address. nginx sees one server: anything spreading the traffic past it — a provider's load balancer, a floating address — is managed outside nginx, and when it fails the route fails.${provider}`
    }
  }
}

const VERDICT: Record<UpstreamPool["verdict"], { tone: DotTone; label: string }> = {
  serving: { tone: "running", label: "Serving" },
  degraded: { tone: "warning", label: "Degraded" },
  "on-backup": { tone: "warning", label: "On its backup" },
  down: { tone: "danger", label: "Nothing answers" },
  unknown: { tone: "unknown", label: "Not checked" },
}

export function poolVerdict(pool: UpstreamPool): { tone: DotTone; label: string } {
  return VERDICT[pool.verdict]
}

/** "primary, weight 3", "backup", "marked down". */
export function memberRole(m: PoolMember): string {
  if (m.down) return "marked down"
  const parts = [m.backup ? "backup" : "primary"]
  if (m.weight && m.weight !== 1) parts.push(`weight ${m.weight}`)
  if (m.maxFails !== undefined && m.maxFails > 0) {
    parts.push(
      `set aside after ${plural(m.maxFails, "failure")}${m.failTimeout ? ` for ${m.failTimeout}` : ""}`,
    )
  }
  return parts.join(", ")
}

/** The check's reading of one server, and its tone. */
export function memberCheck(m: PoolMember): { tone: DotTone; label: string } {
  if (m.down || !m.state) return { tone: "unknown", label: "not checked" }
  return { tone: upstreamTone(m.state), label: upstreamLabel({ state: m.state, ms: m.ms }) }
}

const FAILURE: Record<string, string> = {
  refused: "refused",
  timeout: "timed out",
  reset: "reset",
  closed: "closed early",
  disabled: "set aside",
  other: "other errors",
}

/** "refused 3×, set aside 1×", or empty for a server nginx logged nothing about. */
export function failuresText(m: PoolMember): string {
  const failures = m.failures ?? {}
  return Object.keys(FAILURE)
    .filter((kind) => failures[kind])
    .map((kind) => `${FAILURE[kind]} ${failures[kind]}×`)
    .join(", ")
}
