import type {
  Container,
  Job,
  PortOwner,
  StreamAccess,
  StreamAppendReason,
  StreamEntry,
  StreamIncludeMode,
  StreamIncludePlan,
  StreamModule,
  StreamResult,
  StreamSpec,
  StreamState,
  StreamStatus,
} from "@/lib/types"
import type { DotTone, Verdict } from "@/components/status-dot"
import { DANGEROUS_PORTS } from "@/components/proxy/findings/shared"
import { sourceError } from "@/lib/cidr"

/**
 * The spec fields of a listed stream: what the form edits and the API takes
 * back. The listing's own fields — path, managed, open — would be refused as
 * unknown by the save.
 */
export function streamSpecOf(stream: StreamSpec): StreamSpec {
  const {
    name,
    listen,
    address,
    protocol,
    udpMode,
    upstream,
    proxyProtocol,
    timeout,
    connectTimeout,
    allowFrom,
    rules,
    defaultAllow,
    maxConnPerIp,
    maxConnTotal,
    uploadRate,
    downloadRate,
  } = stream
  return {
    name,
    listen,
    address,
    protocol,
    udpMode,
    upstream,
    proxyProtocol,
    timeout,
    connectTimeout,
    allowFrom,
    rules,
    defaultAllow,
    maxConnPerIp,
    maxConnTotal,
    uploadRate,
    downloadRate,
  }
}

/**
 * A stream's access as the form edits it: an allow list is its rules with
 * everyone else turned away, and no rules at all lets everyone in.
 */
export function accessOf(
  spec: Pick<StreamSpec, "allowFrom" | "rules" | "defaultAllow">,
): StreamAccess {
  if (spec.rules && spec.rules.length > 0) {
    return { rules: spec.rules, defaultAllow: spec.defaultAllow ?? true }
  }
  if (spec.allowFrom.length > 0) {
    return {
      rules: spec.allowFrom.map((source) => ({ action: "allow", source })),
      defaultAllow: false,
    }
  }
  return { rules: [], defaultAllow: true }
}

/**
 * Why the access list cannot be saved, or "" — said on the form before the
 * server refuses it the same way.
 */
export function accessError(access: StreamAccess): string {
  for (const rule of access.rules) {
    const problem = sourceError(rule.source)
    if (problem) return problem
  }
  if (!access.defaultAllow && access.rules.length === 0) {
    return "Turning everyone away with no rule letting anyone in closes the port to all. Pause the stream instead."
  }
  return ""
}

/**
 * What a preview or a save posts: the access list as ordered rules, which
 * the server folds back into an allow list where that says the same, and a
 * UDP mode only where there is UDP.
 */
export function streamBody(spec: StreamSpec, access: StreamAccess): StreamSpec {
  return {
    ...streamSpecOf(spec),
    udpMode: spec.protocol === "tcp" ? undefined : (spec.udpMode ?? "session"),
    allowFrom: [],
    rules: access.rules.map((rule) => ({ ...rule, source: rule.source.trim() })),
    defaultAllow: access.defaultAllow,
  }
}

/** A stream's protocol as the page writes it. */
export function protocolLabel(protocol: StreamSpec["protocol"]): string {
  return protocol === "both" ? "TCP+UDP" : protocol.toUpperCase()
}

/** Whether a stream takes this protocol; one of both counts as each. */
export function carries(stream: Pick<StreamSpec, "protocol">, protocol: "tcp" | "udp"): boolean {
  return stream.protocol === protocol || stream.protocol === "both"
}

/** The longest timeout the backend takes: a day. */
const MAX_TIMEOUT = 86_400

const DURATION = /^(?:(\d+)\s*d)?\s*(?:(\d+)\s*h)?\s*(?:(\d+)\s*m)?\s*(?:(\d+)\s*s)?$/

/** The seconds a non-empty timeout adds up to, or null when nginx would not read it. */
function durationSeconds(value: string): number | null {
  const match = /^\d+$/.test(value) ? [value, "0", "0", "0", value] : DURATION.exec(value)
  if (!match) return null
  const [days, hours, minutes, seconds] = match.slice(1).map((part) => Number(part ?? 0))
  return days * 86_400 + hours * 3600 + minutes * 60 + seconds
}

/**
 * A timeout as typed — "90", "90s", "10m", "1h30m" — in seconds, largest unit
 * first as nginx writes it. A bare number is seconds, as it is to nginx. Empty
 * is 0, which leaves nginx's own default; anything unreadable, longer than a
 * day, or a written zero is null. Zero is not "no timeout": nginx takes it and
 * drops every connection at once, and read as empty it was saved as the
 * default while the field still said 0.
 */
export function parseDuration(text: string): number | null {
  const value = text.trim()
  if (value === "") return 0
  const total = durationSeconds(value)
  return total !== null && total > 0 && total <= MAX_TIMEOUT ? total : null
}

/** Why parseDuration refused a timeout, with nginx's own default for the field named. */
export function durationError(text: string, fallback: string): string {
  return durationSeconds(text.trim()) === 0
    ? `0 makes nginx drop every connection at once. Leave it empty for nginx's ${fallback}.`
    : "Write it as 90s, 10m or 1h30m, up to 24h."
}

/** Seconds as nginx's time syntax, the way a person writes them: 10m, 1h30m. Empty for none. */
export function formatDuration(seconds?: number): string {
  if (!seconds) return ""
  let rest = seconds
  let out = ""
  for (const [size, unit] of [
    [3600, "h"],
    [60, "m"],
    [1, "s"],
  ] as const) {
    const n = Math.floor(rest / size)
    if (n > 0) {
      out += `${n}${unit}`
      rest -= n * size
    }
  }
  return out
}

/** The module is known to be missing — not merely unknown because nginx could not be asked. */
export function moduleMissing(module: StreamModule): boolean {
  return !module.usable && module.state !== "unknown"
}

/**
 * nginx reads the stream directory and can: what is saved there forwards at
 * the next reload. A module nginx could not be asked about is not held
 * against it.
 */
export function streamsLive(status: StreamStatus): boolean {
  return status.included && !moduleMissing(status.module)
}

/** What gets the module in, in one sentence, for the state that lacks it. */
export function moduleRemedy(module: StreamModule): string {
  switch (module.state) {
    case "not-installed":
      return module.package
        ? `Install ${module.package}, the package with nginx's stream module.`
        : "Install your distribution's package for nginx's stream module."
    case "not-loaded":
      return `Load it: add load_module ${module.path ?? "ngx_stream_module.so"}; at the top of nginx.conf.`
    case "absent":
      return "This nginx was built without it, so streams need an nginx build that has it, such as nginx.org's own packages."
  }
  return ""
}

/** The module's state in a word or two, for the setup list. */
export function moduleLabel(module: StreamModule): string {
  switch (module.state) {
    case "static":
      return "built in"
    case "loaded":
      return "loaded"
    case "not-installed":
      return "not installed"
    case "not-loaded":
      return "not loaded"
    case "absent":
      return "not in this build"
  }
  return "could not check"
}

/** The last part of a path, which is what a sentence names a file by. */
function baseName(path: string): string {
  return path.slice(path.lastIndexOf("/") + 1)
}

/** The directory a path is in. */
function dirName(path: string): string {
  return path.slice(0, path.lastIndexOf("/"))
}

/** Why nginx.conf is the file a connect edits, as the clause after "after every module it loads:". */
function appendReason(reason: StreamAppendReason | undefined, dropIn = ""): string {
  const dir = dirName(dropIn)
  switch (reason) {
    case "load-module-after":
      return `it includes ${dir} at its top level, but a module is loaded after that directory, and nginx refuses a module loaded after a stream block`
    case "directory-elsewhere":
      return `the directory it includes at its top level, ${dir}, leads outside the nginx directory, where the dashboard does not write`
    case "name-taken":
      return `${dir}, which it includes at its top level, already holds a ${baseName(dropIn)} that is not the dashboard's`
  }
  return "it includes no directory at its top level where a file of its own could go"
}

/** What connecting changes, as a sentence, for the plan the sheet shows. */
export function connectChange(
  plan: Pick<StreamIncludePlan, "mode" | "path" | "reason" | "dropIn" | "keepsCopy">,
): string {
  const kept = plan.keepsCopy ? " The file as it is now is kept beside it." : ""
  switch (plan.mode) {
    case "dropin":
      return `Creates ${baseName(plan.path)} in ${dirName(plan.path)}. nginx.conf already includes that directory at its top level, so the stream block sits beside http and nginx.conf stays as its package shipped it.`
    case "nginx.conf":
      return `Adds a stream block to the end of ${baseName(plan.path)}, after every module it loads: ${appendReason(plan.reason, plan.dropIn)}.${kept}`
    case "stream-block":
      return `Adds one include line to the stream block in ${plan.path}. nginx refuses a second stream block, so the directory goes into the one that is there.${kept}`
  }
}

/**
 * The install of this package that is running now, whoever started it and
 * from whichever page. A second one would only fail on the package manager's
 * lock, so the page watches this one instead.
 */
export function runningInstall(jobs: Job[], pkg: string): Job | undefined {
  return jobs.find(
    (job) =>
      job.kind === "packages.install" &&
      job.status === "running" &&
      (job.target ?? "").split(", ").includes(pkg),
  )
}

/** What disconnecting takes out: only what connecting put in. */
export function disconnectChange(mode: StreamIncludeMode, path: string): string {
  switch (mode) {
    case "dropin":
      return `It removes ${path}, the file the dashboard added.`
    case "nginx.conf":
      return `It removes the stream block the dashboard added to the end of ${baseName(path)}.`
    case "stream-block":
      return `It removes the include line the dashboard added to the stream block in ${path}.`
  }
}

/**
 * Why nginx's test refuses every stream file here, so no save can pass: a
 * stream block with no module to read it, or the directory included where a
 * stream is not allowed — inside http, nginx refuses proxy_pass. Null when a
 * save can pass.
 */
export function saveBlocked(status: StreamStatus): "module" | "misplaced" | null {
  if (moduleMissing(status.module) && status.included) return "module"
  if (status.includedIn) return "misplaced"
  return null
}

/**
 * Why nginx's test fails for the whole host because of the stream directory,
 * so every reload — every site's, not only the streams' — is refused: a
 * stream block with no module to read it, or stream files included where a
 * stream is not allowed. An include in the wrong block breaks nothing until a
 * file is there; then nginx reads that file as, say, http and refuses it.
 * Null when the directory stops no reload.
 */
export function streamOutage(status: StreamStatus): "module" | "misplaced" | null {
  if (moduleMissing(status.module) && status.included) return "module"
  if (status.includedIn && status.streams.length > 0) return "misplaced"
  return null
}

/**
 * Where the directory is included instead, as a phrase: "inside http", or
 * "at the top level, outside any block" rather than "inside the top level".
 */
export function includedPlace(includedIn: string): string {
  return includedIn.startsWith("the top level") ? `at ${includedIn}` : `inside ${includedIn}`
}

/**
 * The family a stream takes every address of, when it listens that way:
 * `listen 5432;` is 0.0.0.0, every IPv4 address and no IPv6 one. Written as
 * "0.0.0.0:5432" or "only on 0.0.0.0", that read as a restriction.
 */
export function listenFamily(address?: string): "IPv4" | "IPv6" | null {
  if (address === "0.0.0.0") return "IPv4"
  if (address === "::") return "IPv6"
  return null
}

/** Where a stream listens, the way it would be dialled: the port alone when it takes every address. */
export function listenLabel(stream: Pick<StreamSpec, "address" | "listen">): string {
  if (!stream.address || listenFamily(stream.address)) return String(stream.listen)
  return stream.address.includes(":")
    ? `[${stream.address}]:${stream.listen}`
    : `${stream.address}:${stream.listen}`
}

/**
 * A stream nginx cannot bind while something else holds its port: every
 * reload on the host fails on it until one of them moves, so it outranks
 * every other state.
 */
export function blocksReloads(stream: Pick<StreamEntry, "state" | "blocker">): boolean {
  return stream.state === "not-listening" && stream.blocker !== undefined
}

/**
 * A file that cannot be read first, then a stream stopping every reload, one
 * that forwards nothing, one another stream shadows, then streams open to
 * anyone — a database port first among those — and paused streams last.
 */
function rank(stream: StreamEntry): number {
  // Paused forwards nothing and asks nothing of anyone until it is resumed.
  if (stream.paused) return 7
  if (stream.error) return 0
  if (blocksReloads(stream)) return 1
  if (stream.state === "not-listening") return 2
  if (stream.state === "shadowed") return 3
  if (!stream.open) return 6
  return DANGEROUS_PORTS[stream.listen] ? 4 : 5
}

/** Worst first, then by port. The order is the page's answer to "which of these needs me". */
export function byUrgency(a: StreamEntry, b: StreamEntry): number {
  return rank(a) - rank(b) || a.listen - b.listen || a.name.localeCompare(b.name)
}

/** The states the page filters by, in the order its chips run. */
export const STREAM_STATES: StreamState[] = [
  "live",
  "not-listening",
  "shadowed",
  "not-read",
  "unknown",
  "paused",
]

/** A state in the words its chip and its card use. */
export function stateLabel(state: StreamState): string {
  switch (state) {
    case "live":
      return "live"
    case "not-listening":
      return "not listening"
    case "shadowed":
      return "shadowed"
    case "not-read":
      return "not read"
    case "paused":
      return "paused"
  }
  return "unknown"
}

/** A state as a chip names it. */
export function stateTitle(state: StreamState): string {
  const label = stateLabel(state)
  return label[0].toUpperCase() + label.slice(1)
}

/** A stream's state as its card's Status reads it: a verdict, or a plain dot for one never checked. */
export function stateStatus(stream: Pick<StreamEntry, "state" | "error">): {
  verdict?: Verdict
  tone?: DotTone
  label: string
} {
  if (stream.error) return { verdict: "critical", label: "unreadable" }
  switch (stream.state) {
    case "live":
      return { verdict: "ok", label: "live" }
    case "not-listening":
      return { verdict: "critical", label: "not listening" }
    case "shadowed":
    case "not-read":
      return { verdict: "warning", label: stateLabel(stream.state) }
    case "paused":
      return { tone: "stopped", label: "paused" }
  }
  return { tone: "unknown", label: "unknown" }
}

/** How many streams are in each state, and in all. */
export function stateCounts(streams: StreamEntry[]): Record<StreamState | "all", number> {
  const counts = {
    all: streams.length,
    live: 0,
    "not-listening": 0,
    shadowed: 0,
    "not-read": 0,
    unknown: 0,
    paused: 0,
  }
  for (const stream of streams) counts[stream.state]++
  return counts
}

/** What holds a port, named the way a sentence names it. */
export function portOwnerName(owner: PortOwner): string {
  switch (owner.kind) {
    case "stream":
      return owner.name ? `the stream ${owner.name}` : `a stream server in ${owner.file}`
    case "site":
      return owner.name ? `the site ${owner.name}` : `an http server in ${owner.file}`
  }
  if (!owner.name) return "another program"
  return owner.pid ? `${owner.name} (pid ${owner.pid})` : owner.name
}

/** The toast a save ends with: how far it got, in the words that are true of it. */
export function saveOutcome(
  res: StreamResult,
  live: boolean,
): { tone: "success" | "warning"; title: string; description?: string; recheck?: boolean } {
  const title = res.renamed ? `${res.renamed} renamed to ${res.name}` : res.name
  const kept = res.renamed ? `${res.renamed}.conf is kept as ${res.renamed}.conf.bak.` : ""
  const join = (...parts: (string | undefined)[]) => parts.filter(Boolean).join(" ") || undefined
  if (res.reloadError) {
    return {
      tone: "warning",
      title: `${title} saved, reload failed`,
      description: `The file passed nginx's test and is on disk, but nginx did not reload, so it is not forwarding yet: ${res.reloadError}`,
    }
  }
  if (!live) {
    return {
      tone: "warning",
      title: `${title} saved, not yet live`,
      description: join(
        "nginx does not read the stream directory yet, so its test could not check this file.",
        kept,
      ),
    }
  }
  if (res.listening === false) {
    return {
      tone: "warning",
      title: `${title} saved, not listening yet`,
      description: join(res.listenNote, kept),
      recheck: true,
    }
  }
  if (res.listening) {
    return {
      tone: "success",
      title: `${title} saved and listening`,
      description: join(...res.warnings, kept),
    }
  }
  return {
    tone: "success",
    title: `${title} saved and reloaded`,
    description: join(res.listenNote, ...res.warnings, kept),
  }
}

/** The list's chips: by protocol, by who may connect, and by what nginx does with it. */
export type StreamFilter = "all" | "tcp" | "udp" | "open" | "restricted" | "not-live" | "paused"

export const STREAM_FILTERS: { key: StreamFilter; label: string }[] = [
  { key: "all", label: "All" },
  { key: "tcp", label: "TCP" },
  { key: "udp", label: "UDP" },
  { key: "open", label: "Open" },
  { key: "restricted", label: "Restricted" },
  { key: "not-live", label: "Not live" },
  { key: "paused", label: "Paused" },
]

/**
 * Whether a stream is under a chip. A file that could not be read has no
 * protocol or allow list to be sorted by, so only All and Not live take it.
 */
export function streamMatchesFilter(stream: StreamEntry, filter: StreamFilter): boolean {
  switch (filter) {
    case "all":
      return true
    case "tcp":
    case "udp":
      return !stream.error && carries(stream, filter)
    case "open":
      return !stream.error && stream.open
    case "restricted":
      return !stream.error && !stream.open
    case "not-live":
      return !stream.paused && (Boolean(stream.error) || stream.state !== "live")
    case "paused":
      return Boolean(stream.paused)
  }
}

/** How many streams each chip holds. */
export function streamFilterCounts(streams: StreamEntry[]): Record<StreamFilter, number> {
  const counts = Object.fromEntries(STREAM_FILTERS.map(({ key }) => [key, 0])) as Record<
    StreamFilter,
    number
  >
  for (const stream of streams) {
    for (const { key } of STREAM_FILTERS) {
      if (streamMatchesFilter(stream, key)) counts[key]++
    }
  }
  return counts
}

/** Whether a stream matches the search box: its name, port, address, upstream or sources. */
export function streamMatchesQuery(stream: StreamEntry, query: string): boolean {
  const needle = query.trim().toLowerCase()
  if (!needle) return true
  const sources = [...stream.allowFrom, ...(stream.rules ?? []).map((rule) => rule.source)]
  return [stream.name, listenLabel(stream), stream.upstream, ...sources]
    .filter(Boolean)
    .some((field) => field.toLowerCase().includes(needle))
}

/**
 * A new stream as a link describes it — ?new=1&listen=5432&upstream=…&protocol=udp
 * — or null when the link asks for none. A port or protocol the form could
 * not hold is left out rather than guessed; the save checks the rest.
 */
export function streamDraftFromQuery(params: URLSearchParams): Partial<StreamSpec> | null {
  if (params.get("new") !== "1") return null
  const draft: Partial<StreamSpec> = {}
  const listen = Number(params.get("listen"))
  if (Number.isInteger(listen) && listen >= 1 && listen <= 65535) draft.listen = listen
  const upstream = params.get("upstream")?.trim()
  if (upstream) draft.upstream = upstream
  const protocol = params.get("protocol")?.toLowerCase()
  if (protocol === "tcp" || protocol === "udp" || protocol === "both") draft.protocol = protocol
  const name = params.get("name")?.trim()
  if (name) draft.name = name
  return draft
}

/** A stream's fields as a new stream's starting point: the same forward, under a name of its own. */
export function duplicateSpec(stream: StreamEntry): StreamSpec {
  return { ...streamSpecOf(stream), name: `${stream.name}-copy` }
}

/** A running container's published port, as a stream could forward to it. */
export type ContainerTarget = {
  key: string
  container: string
  upstream: string
  protocol: "tcp" | "udp"
  /** Published on every address, so reachable around the stream's allow list. */
  exposed: boolean
}

/**
 * The published ports of running containers a stream of this protocol could
 * forward to, loopback-published first: a port Docker publishes on every
 * address is already reachable without the stream, and its allow list
 * guards nothing. Every-address ports are dialled on 127.0.0.1, one per port
 * — Docker lists the IPv4 and IPv6 bindings separately.
 */
export function containerTargets(
  containers: Container[],
  protocol: StreamSpec["protocol"],
): ContainerTarget[] {
  const seen = new Set<string>()
  const out: ContainerTarget[] = []
  for (const container of containers) {
    if (container.state !== "running") continue
    for (const port of container.ports) {
      if (!port.publicPort) continue
      const type = port.type === "udp" ? "udp" : "tcp"
      if (protocol !== "both" && type !== protocol) continue
      const ip = port.ip ?? ""
      const everywhere = ip === "" || ip === "0.0.0.0" || ip === "::"
      const host = everywhere ? "127.0.0.1" : ip.includes(":") ? `[${ip}]` : ip
      const upstream = `${host}:${port.publicPort}`
      const key = `${container.name}/${upstream}/${type}`
      if (seen.has(key)) continue
      seen.add(key)
      out.push({ key, container: container.name, upstream, protocol: type, exposed: everywhere })
    }
  }
  const scope = (target: ContainerTarget) =>
    target.exposed
      ? 2
      : target.upstream.startsWith("127.") || target.upstream.startsWith("[::1]")
        ? 0
        : 1
  return out.sort(
    (a, b) =>
      scope(a) - scope(b) ||
      a.container.localeCompare(b.container) ||
      a.upstream.localeCompare(b.upstream),
  )
}
