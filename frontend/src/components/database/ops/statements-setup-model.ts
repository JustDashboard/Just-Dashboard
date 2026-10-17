import type { RunPhase } from "@/components/run-phases"
import type { DbStatementsEnable } from "@/components/database/ops/performance-types"
import type { DbPower } from "@/components/database/shell/types"

/**
 * Turning statement statistics on where PostgreSQL has never loaded the
 * module, decided apart from its drawing: where a server's setup starts,
 * whether the dashboard can do the restart in the middle of it, and how far
 * along it is.
 *
 * The module counts only once it is loaded as the server starts, so it is
 * three steps and not one: the library added to the ones the server loads
 * (`POST /statements/preload`), a restart (`POST /power`), and the extension
 * created (`POST /server/extensions`). Each is a route of its own, and the
 * server says which one is next (`GET /statements`), so a setup left halfway
 * — the tab closed during the restart, a server restarted by hand — resumes
 * at the step it stopped at.
 */

export type SetupStep = "preload" | "restart" | "extension"

export const SETUP_STEPS: readonly { key: SetupStep; label: string }[] = [
  { key: "preload", label: "Load the module at start" },
  { key: "restart", label: "Restart the server" },
  { key: "extension", label: "Create the extension" },
]

/**
 * Where a server's setup starts, from what the statements route said; nothing
 * where the guided setup has nothing to offer: the module is not installed,
 * it is loaded already (one statement turns it on, and the page offers that
 * on its own), or the library list cannot be changed from here (`blocked`).
 */
export function setupStart(enable: DbStatementsEnable | undefined): SetupStep | undefined {
  if (!enable?.available || enable.preloaded) return undefined
  if (enable.configured) return "restart"
  return enable.preload ? "preload" : undefined
}

/** Whether the dashboard can do the restart, and where it cannot, why — as a sentence. */
export type RestartAnswer = { can: true } | { can: false; reason: string }

/**
 * The restart is the power route's. It is possible where the summary offers
 * it — a container or a unit this dashboard can act on, running — and the
 * role may interrupt what is connected. Otherwise the reason is the server's
 * own sentence for why there is nothing here to restart it with, or the
 * role's, and the reader restarts it where it runs.
 */
export function restartAnswer(power: DbPower | undefined, mayInterrupt: boolean): RestartAnswer {
  if (power?.restart && mayInterrupt) return { can: true }
  if (power?.restart) {
    return { can: false, reason: "Your role may not restart a database." }
  }
  const why = power?.reason?.trim()
  if (why) return { can: false, reason: /[.!?]$/.test(why) ? why : `${why}.` }
  return power?.via
    ? { can: false, reason: "It is not running, so there is nothing to restart." }
    : { can: false, reason: "Nothing on this machine was found to restart it with." }
}

/** Where the work is: the step at it, and how that step stands. */
export type SetupProgress = { step: SetupStep; state: "waiting" | "running" | "failed" | "done" }

/**
 * The three steps as the run path draws them. Everything before the step at
 * hand is done; the step itself is waiting for the reader, running, or where
 * the setup failed; everything after it waits.
 */
export function setupPhases(progress: SetupProgress): RunPhase[] {
  const at = SETUP_STEPS.findIndex((one) => one.key === progress.step)
  return SETUP_STEPS.map((one, index) => {
    if (progress.state === "done" || index < at) return { label: one.label, state: "passed" }
    if (index > at) return { label: one.label, state: "pending" }
    const state = { waiting: "pending", running: "running", failed: "failed" } as const
    return { label: one.label, state: state[progress.state] }
  })
}
