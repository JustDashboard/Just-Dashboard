import type {
  PortExternalEvidence,
  PortExternalScope,
  PortSource,
  PortsExternal,
} from "@/lib/types"

type SocketLike = {
  port: number
  protocol: string
  family: string
  address?: string
  twin?: { family: string; address?: string }
}

const WILDCARDS = ["", "0.0.0.0", "::"]

/**
 * Whether a measured address can be this socket's: a wildcard socket answers
 * on every address, a translated one included; one bound to an address only
 * there; a loopback socket to no outside source at all.
 */
function reaches(socket: SocketLike, address: string | undefined) {
  const bound = [socket.address, socket.twin?.address].filter((a): a is string => a !== undefined)
  if (bound.length === 0 || bound.some((a) => WILDCARDS.includes(a))) return true
  return address !== undefined && bound.includes(address)
}

/** The address families a socket answers in: both, where its twin is the other family's. */
function families(socket: SocketLike): ("inet" | "inet6")[] {
  const out = new Set<"inet" | "inet6">()
  for (const family of [socket.family, socket.twin?.family]) {
    if (family === "ipv4") out.add("inet")
    if (family === "ipv6") out.add("inet6")
  }
  return [...out]
}

/**
 * The retained external checks of one socket's port, newest first and one per
 * source and family: an older measurement from the same source is history,
 * not a second witness. External checks measure TCP only.
 */
export function externalFor(
  external: PortsExternal | undefined,
  socket: SocketLike,
): PortExternalEvidence[] {
  if (!external || socket.protocol !== "tcp") return []
  const wanted = families(socket)
  const seen = new Set<string>()
  const out: PortExternalEvidence[] = []
  for (const evidence of [...external.evidence].sort((a, b) => b.at.localeCompare(a.at))) {
    if (
      evidence.port !== socket.port ||
      !wanted.includes(evidence.family) ||
      !reaches(socket, evidence.address)
    )
      continue
    const key = `${evidence.vantageId}:${evidence.family}`
    if (seen.has(key)) continue
    seen.add(key)
    out.push(evidence)
  }
  return out
}

/** The enrolled scopes that may be asked to check this socket's port, per family. */
export function scopesFor(external: PortsExternal | undefined, socket: SocketLike) {
  if (!external || socket.protocol !== "tcp") return []
  const wanted = families(socket)
  const out: { scope: PortExternalScope; family: "inet" | "inet6" }[] = []
  for (const scope of external.scopes) {
    if (!scope.ports.includes(socket.port)) continue
    for (const family of scope.families) {
      if (wanted.includes(family)) out.push({ scope, family })
    }
  }
  return out
}

/** One measurement in a sentence: what the source saw, never more than it saw. */
export function externalWords(evidence: PortExternalEvidence): string {
  if (evidence.status === "queued" || evidence.status === "running")
    return "Waiting for the source to measure"
  if (evidence.basis !== "measured") {
    return evidence.status === "expired"
      ? "Expired unmeasured — reachability unknown"
      : evidence.status === "cancelled"
        ? "Cancelled unmeasured — reachability unknown"
        : "No accepted measurement — reachability unknown"
  }
  const where = evidence.local
    ? `this host's ${evidence.address}`
    : `${evidence.address ?? "an address"}, not on this host`
  if (evidence.state === "connected") return `Connected to ${where}`
  if (evidence.state === "refused") return `Refused at ${where}`
  return `Did not connect to ${where}`
}

/** Whether a check is still waiting on its source. */
export function pendingCheck(evidence: PortExternalEvidence[]) {
  return evidence.some((e) => e.status === "queued" || e.status === "running")
}

/** The free-port search's sources that could not be read or have no adapter, said aloud. */
export function unreadSources(sources: PortSource[] | undefined) {
  return (sources ?? []).filter((s) => s.state !== "checked")
}
