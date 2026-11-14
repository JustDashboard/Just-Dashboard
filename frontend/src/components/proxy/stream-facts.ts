import type {
  StreamEntry,
  StreamSpec,
  StreamStatus,
  StreamTraffic,
  StreamTrafficSummary,
} from "@/lib/types"
import { duration } from "@/lib/format"
import { latencyWords } from "@/lib/latency-words"
import {
  blocksReloads,
  listenLabel,
  protocolLabel,
  sessionLength,
  streamOutage,
  streamsLive,
} from "@/lib/streams"
import type { DotTone } from "@/components/status-dot"
import { DANGEROUS_PORTS } from "@/components/proxy/findings/shared"

/*
  What the Streams list and a stream's own page say, worked out apart from the
  drawing so it is tested in `bun test` rather than through a browser.
*/

/** A stream's views, in the order its tab strip draws them; the first is the default. */
export const STREAM_TABS = ["overview", "clients", "connections", "settings"] as const
export type StreamTab = (typeof STREAM_TABS)[number]

export function streamTab(value: string | null | undefined): StreamTab {
  return (STREAM_TABS as readonly string[]).includes(value ?? "")
    ? (value as StreamTab)
    : "overview"
}

/** A stream's page, on a view: the overview leaves the address bare. */
export function streamHref(name: string, tab?: StreamTab): string {
  const base = `/proxy/streams/${encodeURIComponent(name)}`
  return tab && tab !== "overview" ? `${base}?tab=${tab}` : base
}

/**
 * Where a link written before streams had pages lands: `?stream=<name>`
 * opened that stream's form for an administrator, so it lands on Settings
 * where the form is editable, and on the stream otherwise.
 */
export function legacyStreamHref(name: string, stream: StreamEntry | undefined, admin: boolean) {
  const editable =
    admin &&
    stream !== undefined &&
    !stream.error &&
    !stream.paused &&
    stream.unsupported.length === 0
  return streamHref(name, editable ? "settings" : undefined)
}

/** How a pool spreads its connections, in the words of the stream's facts. */
const BALANCE_LABELS: Record<NonNullable<StreamSpec["balance"]>, string> = {
  "least-conn": "least busy first",
  "client-ip": "one server per client",
  random: "picked at random",
}

/** What kind of forward it is, as a run of short facts. */
export function streamTraits(stream: StreamEntry): string[] {
  if (stream.error) return ["could not be read"]
  return [
    `${protocolLabel(stream.protocol)} forwarding`,
    stream.udpMode === "request" &&
      (stream.protocol === "both" ? "one reply per UDP session" : "one reply per session"),
    stream.servers &&
      `${stream.servers.length} servers${stream.balance ? `, ${BALANCE_LABELS[stream.balance]}` : ""}`,
    stream.routes &&
      `${stream.routes.length} ${stream.routes.length === 1 ? "route" : "routes"} by TLS name`,
    stream.proxyProtocol && "PROXY header",
    stream.timeout && `${duration(stream.timeout)} idle timeout`,
    stream.connectTimeout && `${duration(stream.connectTimeout)} connect timeout`,
    !stream.managed && "written by hand",
    stream.link && "a symbolic link",
  ].filter((fact): fact is string => Boolean(fact))
}

/**
 * Every address a stream hands connections to: its pool's servers (or its
 * one upstream), then the backends its TLS-name routes reach.
 */
export function upstreamsOf(stream: StreamEntry): string[] {
  if (stream.error) return []
  const pool = stream.servers?.map((server) => server.address) ?? [stream.upstream]
  const routed = stream.routes?.map((route) => route.upstream) ?? []
  return [...new Set([...pool, ...routed].filter(Boolean))]
}

/** The row's mono line: where it listens and where that goes. */
export function routeLine(stream: StreamEntry): string {
  if (stream.error) return stream.path
  const upstreams = upstreamsOf(stream)
  const more = upstreams.length > 1 ? ` +${upstreams.length - 1}` : ""
  return `${listenLabel(stream)} → ${upstreams[0] ?? stream.upstream}${more}`
}

/** Who may connect, in a few words, and how worried to be about it. */
export function restrictionWords(stream: StreamEntry): {
  label: string
  tone?: "warning" | "danger"
  mono?: boolean
} {
  if (stream.error) return { label: "unknown" }
  if (stream.open) {
    const service = DANGEROUS_PORTS[stream.listen]
    const denies = stream.rules?.some((rule) => rule.action === "deny")
    return {
      label: `${service ? `${service} to anyone` : "anyone"}${denies ? " not denied" : ""}`,
      tone: service ? "danger" : "warning",
    }
  }
  if (stream.allowFrom.length > 0) return { label: stream.allowFrom.join(", "), mono: true }
  if (stream.rules && stream.rules.length > 0) {
    return {
      label: `${stream.rules.map((rule) => `${rule.action} ${rule.source}`).join(", ")}, then ${stream.defaultAllow ? "allow" : "deny"} the rest`,
      mono: true,
    }
  }
  return { label: "set in the file" }
}

/**
 * The list's verdict, at the identity line's end: what the Live tile used to
 * say, with the chip a press narrows the list to when it counts streams.
 */
export function streamsVerdict(
  status: StreamStatus,
  listed: StreamEntry[],
): { tone: DotTone; label: string; chip?: "not-live" } | null {
  const read = status.streams
  if (read.length === 0) return null
  const outage = streamOutage(status)
  if (outage) return { tone: "danger", label: "every reload refused" }
  if (!streamsLive(status)) return { tone: "warning", label: "not read by nginx" }
  const stopping = read.filter(blocksReloads).length
  if (stopping > 0) {
    return {
      tone: "danger",
      label: `${stopping} stop${stopping === 1 ? "s" : ""} every reload`,
      chip: "not-live",
    }
  }
  const down = listed.filter((s) => !s.paused && (s.error || s.state !== "live")).length
  if (down > 0) return { tone: "warning", label: `${down} not live`, chip: "not-live" }
  return { tone: "running", label: read.length === 1 ? "live" : `all ${read.length} live` }
}

/** A window's sessions in a sentence: "142 sessions", with a floor marked. */
export function sessionsWords(traffic: Pick<StreamTraffic, "sessions" | "complete">): string {
  const floor = traffic.complete ? "" : " or more"
  return `${traffic.sessions.toLocaleString()}${floor} ${traffic.sessions === 1 ? "session" : "sessions"}`
}

/** How long sessions lasted, in words: half inside 2.4 s, and the rest as its detail. */
export function lengthWords(span: StreamTraffic["duration"]): { reading: string; hint: string } {
  return {
    reading: latencyWords("p50", sessionLength(span.p50)),
    hint: [
      latencyWords("p90", sessionLength(span.p90)),
      latencyWords("p99", sessionLength(span.p99)),
      `longest ${sessionLength(span.max)}`,
    ].join(" · "),
  }
}

/** The list row's hour: "142 sessions/h", or nothing for a stream that does not log. */
export function hourWords(summary: StreamTrafficSummary | undefined): string | undefined {
  if (!summary) return undefined
  const floor = summary.complete ? "" : "+"
  return `${summary.sessions.toLocaleString()}${floor} ${summary.sessions === 1 ? "session" : "sessions"}/h`
}

export type PictureEdge = {
  from: string
  to: string
  tone: "default" | "warning" | "danger"
  dashed: boolean
  /** Moves only while nginx holds the port and bytes crossed it in the hour. */
  moving: boolean
}

/**
 * The picture of every forward: the host's ports on the left, nginx in the
 * middle, each address they reach on the right. A port open to anyone is
 * drawn in the warning colour (a database port in the danger one), one that
 * nginx does not hold is dashed and red, a paused one dashed and still. At
 * most `cap` ports are drawn; the rest are counted.
 */
export function streamPicture(
  streams: StreamEntry[],
  summaries: Record<string, StreamTrafficSummary> | undefined,
  cap = 8,
) {
  const shown = streams.filter((s) => !s.error).slice(0, cap)
  const up = (stream: StreamEntry) => stream.state === "live"
  const failing = (stream: StreamEntry) =>
    stream.state === "not-listening" || stream.state === "unknown"
  const carried = (stream: StreamEntry) => up(stream) && (summaries?.[stream.name]?.bytes ?? 0) > 0
  const upstreams: { address: string; streams: string[] }[] = []
  const edges: PictureEdge[] = shown.map((stream) => ({
    from: `p:${stream.name}`,
    to: "hub",
    tone: failing(stream)
      ? "danger"
      : stream.open
        ? DANGEROUS_PORTS[stream.listen]
          ? "danger"
          : "warning"
        : "default",
    dashed: !up(stream),
    moving: carried(stream),
  }))
  for (const stream of shown) {
    for (const address of upstreamsOf(stream)) {
      const node = upstreams.find((u) => u.address === address)
      if (node) node.streams.push(stream.name)
      else upstreams.push({ address, streams: [stream.name] })
    }
  }
  // An address two streams reach is up while either is, and carries while
  // either carried; it is red only when every way to it is failing.
  for (const node of upstreams) {
    const via = shown.filter((stream) => node.streams.includes(stream.name))
    edges.push({
      from: "hub",
      to: `u:${node.address}`,
      tone: via.every(failing) ? "danger" : "default",
      dashed: !via.some(up),
      moving: via.some(carried),
    })
  }
  return { ports: shown, upstreams, edges, hidden: streams.length - shown.length }
}
