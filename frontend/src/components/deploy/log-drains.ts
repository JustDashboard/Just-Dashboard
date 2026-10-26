import { plural, relativeTime } from "@/lib/format"
import type { DeploymentSummary, LogDrain } from "@/lib/types"

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

/** Which streams of which projects a drain carries, in one phrase. */
export function drainReach(
  drain: Pick<LogDrain, "streams" | "projects">,
  projects: Pick<DeploymentSummary, "id" | "name">[],
) {
  const streams =
    drain.streams.length === 2
      ? "output and requests"
      : drain.streams[0] === "output"
        ? "container output"
        : "requests"
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
