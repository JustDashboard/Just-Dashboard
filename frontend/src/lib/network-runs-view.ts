import { captureFinished, type CaptureList, type CaptureRun } from "@/lib/network-captures"
import { diagnosticFinished, type DiagnosticRun } from "@/lib/network-diagnostics"

/**
 * What the Saved runs and Packet captures pages say about one run or one
 * capture in a line, and how their lists are narrowed and walked. Kept apart
 * from React so `bun test src` reads it.
 */

/** The tools' own names, as the Tools page labels them; an unknown key is shown as itself. */
const TOOL_WORDS: Record<string, string> = {
  dns: "DNS",
  dnsauth: "DNS authority",
  ping: "Ping",
  traceroute: "Traceroute",
  route: "Route lookup",
  mtu: "Path MTU",
  port: "Port check",
  scan: "Port scan",
  banner: "Banner grab",
  ssh: "SSH keys",
  http: "HTTP",
  httpsec: "Header grade",
  tls: "TLS cert",
  tlssurvey: "TLS versions",
  siteaudit: "Site audit",
  mx: "Mail path",
  starttls: "STARTTLS",
  dnsbl: "Blocklists",
  asn: "Ownership",
  whois: "Whois",
  capabilities: "Host support",
  capture: "Packet snapshot",
  listeners: "Listeners",
  egress: "Egress",
  neigh: "Neighbours",
  wol: "Wake-on-LAN",
}

type RunLike = Pick<DiagnosticRun, "kind" | "request" | "investigationRequest">

export function runTool(run: RunLike) {
  if (run.kind === "investigation") return "Connection path"
  return TOOL_WORDS[run.request.tool] ?? (run.request.tool || "Diagnostic")
}

export function runTarget(run: RunLike) {
  return run.investigationRequest?.target || run.request.target || "this host"
}

/** The request in one mono line: the target, then what narrows it. */
export function runRequestLine(run: RunLike) {
  const parts = [runTarget(run)]
  if (run.kind === "investigation") {
    const req = run.investigationRequest
    if (req) {
      parts.push(familyWord(req.family))
      const protocol = req.protocol.toUpperCase()
      parts.push(req.port ? `${protocol} ${req.port}` : protocol)
    }
    return parts.join(" · ")
  }
  const { port, record, option } = run.request
  if (port) parts[0] = `${parts[0]}:${port}`
  if (record) parts.push(record)
  if (option) parts.push(option)
  return parts.join(" · ")
}

export const RUN_SCOPES = [
  "all",
  "running",
  "completed",
  "failed",
  "interrupted",
  "cancelled",
] as const
export type RunScope = (typeof RUN_SCOPES)[number]

export const RUN_SCOPE_WORDS: Record<RunScope, string> = {
  all: "All",
  running: "Running",
  completed: "Completed",
  failed: "Failed",
  interrupted: "Interrupted",
  cancelled: "Cancelled",
}

/** Which chip a run is counted under: work in flight is one, whatever stage it is at. */
export function runScopeOf(run: Pick<DiagnosticRun, "status">): Exclude<RunScope, "all"> {
  if (!diagnosticFinished(run)) return "running"
  return run.status as Exclude<RunScope, "all" | "running">
}

export function runCounts(runs: Pick<DiagnosticRun, "status">[]) {
  const counts = Object.fromEntries(RUN_SCOPES.map((scope) => [scope, 0])) as Record<
    RunScope,
    number
  >
  counts.all = runs.length
  for (const run of runs) counts[runScopeOf(run)]++
  return counts
}

/** The chips worth drawing: All, every scope that holds a run, and the one chosen. */
export function runChips(counts: Record<RunScope, number>, chosen: RunScope) {
  return RUN_SCOPES.filter((scope) => scope === "all" || counts[scope] > 0 || scope === chosen)
}

export function runMatches(
  run: Pick<
    DiagnosticRun,
    "id" | "name" | "status" | "kind" | "request" | "investigationRequest" | "createdBy"
  >,
  scope: RunScope,
  query: string,
) {
  if (scope !== "all" && runScopeOf(run) !== scope) return false
  const words = query.trim().toLowerCase()
  if (!words) return true
  const hay = [run.name, runTool(run), runRequestLine(run), run.createdBy, run.id]
    .join("\n")
    .toLowerCase()
  return words.split(/\s+/).every((word) => hay.includes(word))
}

/** Where an item stands in the list it was opened from, and its two neighbours. */
export function neighbours<T extends { id: string }>(rows: T[], id: string) {
  const at = rows.findIndex((row) => row.id === id)
  return {
    at,
    previous: at > 0 ? rows[at - 1] : undefined,
    next: at >= 0 ? rows[at + 1] : rows[0],
  }
}

const PROTOCOL_WORDS: Record<CaptureRun["request"]["protocol"], string> = {
  all: "Every protocol",
  tcp: "TCP",
  udp: "UDP",
  icmp: "ICMP",
  icmp6: "ICMPv6",
}

export const familyWord = (family: string) => (family === "inet6" ? "IPv6" : "IPv4")

/** The capture's filter in words: a title, and the addresses it narrows to. */
export function captureFilter(request: CaptureRun["request"]) {
  const protocol = PROTOCOL_WORDS[request.protocol] ?? request.protocol
  return {
    title: request.port ? `${protocol} ${request.port}` : protocol,
    from: request.source || "any address",
    to: request.destination || "any address",
    narrowed: Boolean(request.source || request.destination || request.port),
  }
}

/** The capture in one mono line, as its row says it. */
export function captureLine(run: Pick<CaptureRun, "request">) {
  const { request } = run
  return [request.interface, familyWord(request.family), captureFilter(request).title].join(" · ")
}

/** The list's figures for the identity line. */
export function captureTotals(list: Pick<CaptureList, "runs">) {
  let capturing = 0
  let bytes = 0
  for (const run of list.runs) {
    if (!captureFinished(run)) capturing++
    bytes += run.result?.bytes ?? 0
  }
  return { capturing, bytes, retained: list.runs.length }
}

export function captureMatches(run: Pick<CaptureRun, "name" | "request">, query: string) {
  const words = query.trim().toLowerCase()
  if (!words) return true
  const hay = [run.name, captureLine(run), run.request.source, run.request.destination]
    .join("\n")
    .toLowerCase()
  return words.split(/\s+/).every((word) => hay.includes(word))
}

/**
 * How long ago a related saved run started against the capture, in words:
 * the reference and the two times are all that connect them.
 */
export function startedApart(diagnostic?: string, capture?: string) {
  if (!diagnostic || !capture) return undefined
  const seconds = Math.round((Date.parse(capture) - Date.parse(diagnostic)) / 1000)
  if (!Number.isFinite(seconds)) return undefined
  const span = Math.abs(seconds)
  const words =
    span < 60
      ? `${span} s`
      : span < 3600
        ? `${Math.round(span / 60)} min`
        : `${Math.round(span / 360) / 10} h`
  if (span === 0) return "started with the capture"
  return seconds > 0 ? `started ${words} before the capture` : `started ${words} after the capture`
}
