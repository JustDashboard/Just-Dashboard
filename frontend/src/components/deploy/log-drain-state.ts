import { plural } from "@/lib/format"
import type { LogDrain, LogDrainKind } from "@/lib/types"
import type { WireLine } from "@/components/wire-flow-model"
import { shortAge } from "@/components/deploy/short-age"

/**
 * What a drain's row and its line in the picture say about it, kept apart
 * from the page so it is tested in `bun test src`. The sentences a drain's
 * popover reads out are `drainHealth`'s and `drainReach`'s in `log-drains.ts`;
 * this is the one word beside the dot, the one figure after it, and the count
 * on the section's head.
 */

export type DrainState = "paused" | "failing" | "waiting" | "shipping"

/** `drainHealth`'s own rule, as a state: paused first, then a failure newer than the last success. */
export function drainState(drain: Pick<LogDrain, "enabled" | "status">): DrainState {
  if (!drain.enabled) return "paused"
  const { status } = drain
  const lastSuccess = status.lastSuccessAt ? Date.parse(status.lastSuccessAt) : 0
  const lastError = status.lastErrorAt ? Date.parse(status.lastErrorAt) : 0
  if (lastError > lastSuccess && status.lastError) return "failing"
  return lastSuccess ? "shipping" : "waiting"
}

/** The dot and the word in a drain's state column. */
export function drainStateWord(
  drain: Pick<LogDrain, "enabled" | "status">,
  now = Date.now(),
): { tone: "running" | "stopped" | "warning" | "unknown"; label: string } {
  switch (drainState(drain)) {
    case "paused":
      return { tone: "stopped", label: "Paused" }
    case "failing":
      return {
        tone: "warning",
        label: `Failing ${shortAge(drain.status.lastErrorAt, now)}`.trim(),
      }
    case "waiting":
      return { tone: "unknown", label: "Nothing sent" }
    default:
      return { tone: "running", label: "Shipping" }
  }
}

/**
 * The one figure a row has room for, the most pressing first: what waits
 * while a destination refuses, what was dropped, else what was shipped.
 */
export function drainFigure(drain: Pick<LogDrain, "enabled" | "status">): {
  text: string
  warning: boolean
} {
  const { status } = drain
  if (drainState(drain) === "failing" && status.queued > 0)
    return { text: `${status.queued.toLocaleString()} waiting`, warning: true }
  if (status.dropped > 0)
    return { text: `${status.dropped.toLocaleString()} dropped`, warning: true }
  return { text: `${status.shipped.toLocaleString()} shipped`, warning: false }
}

/**
 * A drain's line in the picture (design-system §2: the line is the state):
 * a pulse while its last delivery arrived, amber and still while the
 * destination refuses, dashed while it is paused, still before anything went.
 */
export function drainLine(drain: Pick<LogDrain, "enabled" | "status">): WireLine {
  switch (drainState(drain)) {
    case "paused":
      return { state: "idle" }
    case "failing":
      return { state: "warning" }
    case "waiting":
      return { state: "up" }
    default:
      return { state: "up", moving: true, duration: 2.2 }
  }
}

/** The section head's count: "2 drains · 1 failing · 1 paused". */
export function drainsCount(drains: Pick<LogDrain, "enabled" | "status">[]): string {
  const states = drains.map(drainState)
  const failing = states.filter((state) => state === "failing").length
  const paused = states.filter((state) => state === "paused").length
  return [
    plural(drains.length, "drain"),
    failing > 0 && `${failing} failing`,
    paused > 0 && `${paused} paused`,
  ]
    .filter(Boolean)
    .join(" · ")
}

/** Whether a drain carries a project's records: every project's, or the ones it names. */
export function drainTakes(drain: Pick<LogDrain, "projects">, projectId: number) {
  return drain.projects.length === 0 || drain.projects.includes(projectId)
}

/** The product each format is drawn as: the protocol's own mark for plain HTTP. */
export const DRAIN_PRODUCT: Record<LogDrainKind, string> = {
  otlp: "opentelemetry",
  loki: "grafana",
  http: "webhook",
}
