import { dedupeEvents, foldRestarts } from "@/lib/docker-events"
import type { DockerEvent, MetricEvent } from "@/lib/types"
import { crashed } from "@/components/docker/container"

/**
 * What happened to one container, as the marks its Usage charts carry: the
 * project's Runtime marks its releases going live, and a container's own
 * page marks the moments its readings change for a reason — an exit, an OOM
 * kill, a restart loop, a start. A memory line falling off a cliff reads as a
 * leak fixed or a container killed depending on whether a mark stands there.
 *
 * A restart loop is one mark spanning the loop (a band, where the chart draws
 * spans), not forty: its exits are one incident. A clean stop is a quiet mark
 * and a crash a red one, by the same rule the verdict uses (`crashed`), and a
 * health check that turned unhealthy is amber. Probes passing, execs and
 * bookkeeping (create, pull) mark nothing: they explain no line.
 */
export function usageMarkers(events: DockerEvent[]): MetricEvent[] {
  const marks: MetricEvent[] = []
  for (const entry of foldRestarts(dedupeEvents(events))) {
    if (entry.kind === "loop") {
      const span = (Date.parse(entry.until) - Date.parse(entry.since)) / 1000
      marks.push({
        ts: entry.since,
        kind: "action",
        title: `restarted ×${entry.times}`,
        detail: entry.oom
          ? "killed for memory"
          : entry.exitCode
            ? `exit ${entry.exitCode}`
            : undefined,
        severity: "error",
        durationSeconds: span > 0 ? span : undefined,
      })
      continue
    }
    const { event, oom } = entry
    const mark = markOf(event, Boolean(oom))
    if (mark) marks.push({ ts: event.time, kind: "action", ...mark })
  }
  return marks.sort((a, b) => Date.parse(a.ts) - Date.parse(b.ts))
}

function markOf(
  event: DockerEvent,
  oom: boolean,
): Pick<MetricEvent, "title" | "detail" | "severity"> | undefined {
  if (event.type !== "container") return undefined
  switch (event.action) {
    case "die": {
      if (oom) return { title: "killed for memory", severity: "error" }
      const code = Number(event.exitCode ?? 0)
      return crashed(code)
        ? { title: `exited (${code})`, severity: "error" }
        : { title: "stopped", detail: code ? `exit ${code}` : undefined, severity: "info" }
    }
    // A process inside it the kernel chose while the container lived on.
    case "oom":
      return { title: "a process was killed for memory", severity: "warning" }
    case "start":
      return { title: "started", severity: "info" }
    case "restart":
      return { title: "restarted", severity: "info" }
    case "pause":
      return { title: "paused", severity: "info" }
    case "unpause":
      return { title: "resumed", severity: "info" }
    default:
      return event.action.startsWith("health_status") && event.action.endsWith("unhealthy")
        ? { title: "failing its health check", severity: "warning" }
        : undefined
  }
}
