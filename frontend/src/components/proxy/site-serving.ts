import type { SiteDeleteResult, SystemdUnit, VHostLinkResult } from "@/lib/types"
import { nginxNotRunning } from "./site-outcome"

/**
 * Whether the Sites page may call a site "serving".
 *
 * An enabled site is a file nginx reads, and it is served only once a running
 * nginx has loaded it. The cards drew every enabled site as serving, so with
 * nginx stopped, or after a reload that failed, they said "serving" under a
 * toast saying the site was not. Two things tell the page better: systemd's
 * reading of the engine's unit, and what each verb's reload answered. The
 * newer of the two says whether the engine runs, and a verb whose reload
 * failed marks its site until nginx is known to have read the configuration
 * since.
 */

/** A unit whose process is up: one `reloading` still serves what it had. */
export function unitRunning(unit: Pick<SystemdUnit, "activeState">): boolean {
  return unit.activeState === "active" || unit.activeState === "reloading"
}

/** systemd's words for where a unit stands: "inactive (dead)", "failed". */
export function unitState(unit: Pick<SystemdUnit, "activeState" | "subState">): string {
  return unit.subState && unit.subState !== unit.activeState
    ? `${unit.activeState} (${unit.subState})`
    : unit.activeState
}

/** The engine's unit as last read, and when, in this browser's milliseconds. */
export type UnitReading = { at: number; unit: SystemdUnit }

/**
 * What a verb's reload answered: nginx reloaded, there was no nginx to
 * signal, or the reload failed and a running nginx, if there is one, still
 * has the configuration from before.
 */
export type ReloadOutcome = "reloaded" | "notRunning" | "failed"

/**
 * How a verb's reload went, or undefined when it asked for none.
 *
 * `nginx -s reload` finding no process at its pid file means nginx is not
 * running only where systemd does not say otherwise: a unit that is up with
 * its pid file gone or stale is an nginx serving what it had, which the
 * reload cannot reach. That is a failed reload, and saying nginx is not
 * running would be as wrong as saying it serves the change.
 */
export function reloadOutcome(
  res: Pick<SiteDeleteResult | VHostLinkResult, "reload" | "reloadError">,
  unit: UnitReading | undefined,
): ReloadOutcome | undefined {
  if (!res.reloadError) return res.reload ? "reloaded" : undefined
  if (nginxNotRunning(res) && !(unit && unitRunning(unit.unit))) return "notRunning"
  return "failed"
}

/** What the page has heard from the verbs' reloads. */
export type ReloadsHeard = {
  /** The newest reload a verb answered, for whether nginx runs. */
  last?: { at: number; outcome: ReloadOutcome }
  /** When a verb last reloaded nginx: every change made before then is loaded. */
  loadedAt?: number
}

export function hear(
  heard: ReloadsHeard,
  at: number,
  outcome: ReloadOutcome | undefined,
): ReloadsHeard {
  if (!outcome) return heard
  return { last: { at, outcome }, loadedAt: outcome === "reloaded" ? at : heard.loadedAt }
}

/** Whether the engine runs, and which reading says so. */
export type EngineRun = { running: boolean; from: "unit" | "reload" }

/**
 * Whether the engine runs, by the newer of the two readings, or undefined
 * when neither says. A reload that failed with nginx running says nothing
 * about it; a host whose nginx has no unit has only the verbs to go on.
 */
export function engineRun(
  unit: UnitReading | undefined,
  reload: ReloadsHeard["last"],
): EngineRun | undefined {
  const byReload =
    reload && reload.outcome !== "failed"
      ? { at: reload.at, running: reload.outcome === "reloaded", from: "reload" as const }
      : undefined
  const byUnit = unit
    ? { at: unit.at, running: unitRunning(unit.unit), from: "unit" as const }
    : undefined
  const newer = byReload && (!byUnit || byReload.at > byUnit.at) ? byReload : byUnit
  return newer && { running: newer.running, from: newer.from }
}

/**
 * A change a verb made that nginx did not reload: when, and the nginx
 * process systemd had then, if it had one.
 */
export type Unloaded = { at: number; pid?: number }

export function markUnloaded(at: number, unit: UnitReading | undefined): Unloaded {
  return { at, pid: unit && unitRunning(unit.unit) ? unit.unit.mainPid : undefined }
}

/**
 * Whether nginx may still be running without the change: until a verb
 * reloads it, or systemd shows another nginx process than the one the
 * reload could not reach — one started since, which read the configuration
 * as it is now. A reload keeps nginx's process; only a start changes it.
 */
export function stillUnloaded(
  mark: Unloaded | undefined,
  loadedAt: number | undefined,
  unit: UnitReading | undefined,
): boolean {
  if (!mark) return false
  if (loadedAt !== undefined && loadedAt > mark.at) return false
  if (unit && unitRunning(unit.unit) && unit.unit.mainPid !== mark.pid) return false
  return true
}
