import type { DotTone } from "@/components/status-dot"
import type { DockerEvent } from "@/lib/types"

/**
 * Docker's events and exits in words, written once.
 *
 * The same exit was spelled in three files and the same event in two, and
 * the copies had drifted: a health event was "unhealthy" on the stack list
 * and "failing its check" on the stack's own services, and a die killed for
 * memory read "out of memory" in one and "killed for memory" in the other.
 * Every Docker page that says what happened says it from here.
 */

/**
 * The exits a stop sends, which read as a stop rather than a failure: a clean
 * exit, an interrupt (SIGINT), a kill (SIGKILL) and a terminate (SIGTERM).
 */
export const DELIBERATE_EXITS: ReadonlySet<number> = new Set([0, 130, 137, 143])

/** Whether an exit was the program failing rather than being stopped. */
export function crashed(code: number) {
  return !DELIBERATE_EXITS.has(code)
}

/** An exit status in words, for somebody who has not memorised the signals. */
export function exitWords(code: number, oomKilled?: boolean): string
export function exitWords(code: number | undefined, oomKilled?: boolean): string | undefined
export function exitWords(code: number | undefined, oomKilled = false): string | undefined {
  if (oomKilled) return "killed for memory"
  if (code === undefined) return undefined
  switch (code) {
    case 0:
      return "exited cleanly"
    case 130:
      return "interrupted"
    case 137:
      return "killed"
    case 143:
      return "stopped"
    default:
      return `exit ${code}`
  }
}

/**
 * A container event as the verb after its name ("web failing its check",
 * "db exited 1") with the dot it takes, or nothing for an event a reader does
 * not need (an exec, a kill or a stop, which only lead to the die that says
 * how it ended). `oomKilled` is for a die the kernel caused,
 * when the caller has paired it with its oom.
 */
export function changeWords(
  event: Pick<DockerEvent, "action" | "exitCode" | "level">,
  oomKilled = false,
): { verb: string; tone: DotTone } | undefined {
  const action = event.action
  if (action.startsWith("health_status")) {
    return action.endsWith("unhealthy")
      ? { verb: "failing its check", tone: "danger" }
      : { verb: "passing its check again", tone: "running" }
  }
  switch (action) {
    case "start":
      return { verb: "started", tone: "running" }
    case "unpause":
      return { verb: "resumed", tone: "running" }
    case "restart":
      return { verb: "restarted", tone: "warning" }
    case "oom":
      return { verb: "out of memory", tone: "danger" }
    case "die": {
      if (oomKilled) return { verb: "killed for memory", tone: "danger" }
      const code = event.exitCode ? Number(event.exitCode) : undefined
      if (event.level === "error" && code !== undefined && crashed(code))
        return { verb: `exited ${code}`, tone: "danger" }
      return { verb: exitWords(code) ?? "exited", tone: "stopped" }
    }
    case "pause":
      return { verb: "paused", tone: "stopped" }
    case "destroy":
      return { verb: "removed", tone: "stopped" }
    default:
      return undefined
  }
}
