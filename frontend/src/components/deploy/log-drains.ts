import { plural, relativeTime } from "@/lib/format"
import type { DeploymentSummary, LogDrain, LogDrainStream } from "@/lib/types"

export type DrainHeaderDraft = { name: string; value: string }

/**
 * What an endpoint's address means for what rides with it, by the server's
 * own rule: a header goes over https or to this machine, and a drain with no
 * header may use plain http anywhere — a collector on the local network
 * seldom has TLS — though its records then cross the network in the clear.
 */
export function drainEndpointProblem(
  endpoint: string,
  sendsHeaders: boolean,
): "credential-over-http" | "cleartext" | undefined {
  let url: URL
  try {
    url = new URL(endpoint.trim())
  } catch {
    return undefined
  }
  if (url.protocol !== "http:") return undefined
  const host = url.hostname.replace(/^\[|\]$/g, "")
  const loopback = host === "localhost" || host === "::1" || /^127\.\d+\.\d+\.\d+$/.test(host)
  if (loopback) return undefined
  return sendsHeaders ? "credential-over-http" : "cleartext"
}

const STREAM_WORDS: Record<LogDrainStream, string> = {
  output: "container output",
  requests: "requests",
  traces: "traces",
}

/** The streams a drain carries, in a phrase: "output, requests and traces". */
export function streamsPhrase(streams: LogDrainStream[]) {
  const order: LogDrainStream[] = ["output", "requests", "traces"]
  const words = order
    .filter((stream) => streams.includes(stream))
    .map((stream, index, all) =>
      // "container output" alone, "output" in a list of several.
      stream === "output" && all.length > 1 ? "output" : STREAM_WORDS[stream],
    )
  if (words.length <= 1) return words[0] ?? ""
  return `${words.slice(0, -1).join(", ")} and ${words[words.length - 1]}`
}

/** Which streams of which projects a drain carries, in one phrase. */
export function drainReach(
  drain: Pick<LogDrain, "streams" | "projects">,
  projects: Pick<DeploymentSummary, "id" | "name">[],
) {
  const streams = streamsPhrase(drain.streams)
  if (drain.projects.length === 0) return `${streams} of every project`
  const names = drain.projects
    .map((id) => projects.find((project) => project.id === id)?.name)
    .filter((name): name is string => Boolean(name))
  if (names.length === drain.projects.length && names.length <= 2) {
    return `${streams} of ${names.join(" and ")}`
  }
  return `${streams} of ${plural(drain.projects.length, "project")}`
}

/**
 * How a drain is doing, in the line under its name. Paused says so first;
 * a delivery that failed after the last success is a warning in the
 * destination's own words; otherwise what it has shipped and when last.
 */
export function drainHealth(drain: Pick<LogDrain, "enabled" | "status">): {
  tone: "warning" | "muted"
  line: string
} {
  const { status } = drain
  const lastSuccess = status.lastSuccessAt ? Date.parse(status.lastSuccessAt) : 0
  const lastError = status.lastErrorAt ? Date.parse(status.lastErrorAt) : 0
  const dropped = status.dropped > 0 ? ` · ${plural(status.dropped, "record")} dropped` : ""
  if (!drain.enabled) {
    return { tone: "muted", line: `Paused · ${plural(status.shipped, "record")} shipped${dropped}` }
  }
  if (lastError > lastSuccess && status.lastError) {
    const queued = status.queued > 0 ? ` · ${plural(status.queued, "record")} waiting` : ""
    return {
      tone: "warning",
      line: `Failing since ${relativeTime(status.lastErrorAt)}: ${status.lastError}${queued}`,
    }
  }
  if (!lastSuccess) {
    return { tone: "muted", line: "Nothing sent yet — records go out as deployments write them" }
  }
  return {
    tone: status.dropped > 0 ? "warning" : "muted",
    line: `${plural(status.shipped, "record")} shipped · last delivery ${relativeTime(status.lastSuccessAt)}${dropped}`,
  }
}

/**
 * What is wrong with a trace link as typed, by the server's rule: an http or
 * https address with `{traceId}` where the id goes. Empty is no link.
 */
export function traceLinkProblem(template: string): string | undefined {
  const value = template.trim()
  if (!value) return undefined
  if (!value.includes("{traceId}")) return "Put {traceId} where the trace's id goes."
  let url: URL
  try {
    url = new URL(value.replaceAll("{traceId}", "0"))
  } catch {
    return "The link is an http or https address."
  }
  if ((url.protocol !== "https:" && url.protocol !== "http:") || /[\s"'<>]/.test(value))
    return "The link is an http or https address."
  return undefined
}

/**
 * Where a request's trace opens: the first enabled drain that exports this
 * project's traces and names a link, with the id put in it. A request with no
 * trace, or no such drain, has none.
 */
export function traceLinkFor(
  drains: Pick<LogDrain, "name" | "enabled" | "streams" | "projects" | "traceUrl">[] | undefined,
  projectId: number,
  traceId: string | undefined,
): { href: string; label: string } | undefined {
  if (!traceId || !/^[0-9a-f]{32}$/.test(traceId)) return undefined
  const drain = drains?.find(
    (candidate) =>
      candidate.enabled &&
      candidate.streams.includes("traces") &&
      candidate.traceUrl &&
      !traceLinkProblem(candidate.traceUrl) &&
      (candidate.projects.length === 0 || candidate.projects.includes(projectId)),
  )
  if (!drain?.traceUrl) return undefined
  return { href: drain.traceUrl.replaceAll("{traceId}", traceId), label: `Open in ${drain.name}` }
}
