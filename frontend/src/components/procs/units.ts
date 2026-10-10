import { duration } from "@/lib/format"
import type { SystemdUnit } from "@/lib/types"
import type { DotTone } from "@/components/status-dot"

/**
 * The words a systemd unit's state is read in, on the Services page, its
 * sheet and anywhere else a unit is drawn.
 */

/** Where a unit sits for the page's state chips. */
export type UnitBucket = "active" | "failed" | "changing" | "inactive"

export function unitBucket(unit: Pick<SystemdUnit, "activeState">): UnitBucket {
  switch (unit.activeState) {
    case "active":
      return "active"
    case "failed":
      return "failed"
    case "activating":
    case "deactivating":
    case "reloading":
    case "refreshing":
      return "changing"
    default:
      return "inactive"
  }
}

/**
 * A unit's state as one tone. A failed unit is the one thing on the page to
 * act on, so it takes the destructive hue where `toneFor` gives every
 * vocabulary's "failed" the warning one; a unit on its way somewhere is
 * amber until it arrives.
 */
export function unitTone(unit: Pick<SystemdUnit, "activeState">): DotTone {
  switch (unitBucket(unit)) {
    case "failed":
      return "danger"
    case "changing":
      return "warning"
    case "active":
      return "running"
    default:
      return "stopped"
  }
}

/**
 * The state in one word, as the reader asks it — "is it running" — rather
 * than systemd's pair. `active (exited)` is a oneshot that ran and is kept
 * active, which "exited" alone reads as a crash; `activating (auto-restart)`
 * is systemd waiting to start it again after it died.
 */
export function unitStateWord(unit: Pick<SystemdUnit, "activeState" | "subState">): string {
  const { activeState: active, subState: sub } = unit
  if (active === "active") {
    if (sub === "running") return "running"
    if (sub === "exited") return "done"
    if (sub === "reload") return "reloading"
    return sub || "active"
  }
  if (active === "activating") return sub === "auto-restart" ? "restarting" : "starting"
  if (active === "deactivating") return "stopping"
  if (active === "reloading") return "reloading"
  if (active === "failed") return "failed"
  return "inactive"
}

/** systemd's result words, where they say more than the exit beside them. */
export const RESULT_WORDS: Record<string, string> = {
  timeout: "timed out",
  "oom-kill": "killed for memory",
  "start-limit-hit": "start limit hit",
  watchdog: "watchdog timeout",
  "core-dump": "core dumped",
  resources: "resources unavailable",
  protocol: "protocol violation",
  "exec-condition": "condition failed",
}

const SIGNALS: Record<number, string> = {
  1: "SIGHUP",
  2: "SIGINT",
  3: "SIGQUIT",
  4: "SIGILL",
  6: "SIGABRT",
  7: "SIGBUS",
  8: "SIGFPE",
  9: "SIGKILL",
  11: "SIGSEGV",
  13: "SIGPIPE",
  14: "SIGALRM",
  15: "SIGTERM",
}

export function signalName(n: number): string {
  return SIGNALS[n] ?? `signal ${n}`
}

/** How the unit's main process last ended, in words; nothing while it has not. */
export function exitWords(unit: Pick<SystemdUnit, "exitCode" | "exitStatus">): string | null {
  const status = unit.exitStatus ?? 0
  switch (unit.exitCode) {
    case "exited":
      return `exit ${status}`
    case "killed":
      return `killed by ${signalName(status)}`
    case "dumped":
      return `dumped core on ${signalName(status)}`
    default:
      return null
  }
}

/** How the main process last ended, as the rest of a sentence about it. */
export function exitSentence(unit: Pick<SystemdUnit, "exitCode" | "exitStatus">): string | null {
  const status = unit.exitStatus ?? 0
  switch (unit.exitCode) {
    case "exited":
      return `exited with status ${status}`
    case "killed":
      return `was killed by ${signalName(status)}`
    case "dumped":
      return `dumped core on ${signalName(status)}`
    default:
      return null
  }
}

/**
 * Why a failed unit failed, in the fewest words that say it: systemd's
 * result where it is more than "the process exited badly", else the exit.
 */
export function failureWords(
  unit: Pick<SystemdUnit, "result" | "exitCode" | "exitStatus">,
): string {
  const result = unit.result ? RESULT_WORDS[unit.result] : undefined
  return result ?? exitWords(unit) ?? unit.result ?? "failed"
}

/** A unit's name as it is read: `nginx` for `nginx.service`. */
export function unitShortName(name: string): string {
  return name.replace(/\.service$/, "")
}

/**
 * "12m ago" from a unix time, against a clock the caller keeps moving. To
 * the minute: a list of changes that ticks its seconds is a column of moving
 * digits nobody reads.
 */
export function ago(unixSeconds: number, now: number): string {
  const seconds = now / 1000 - unixSeconds
  if (seconds < 45) return "just now"
  return `${duration(Math.max(60, Math.floor(seconds / 60) * 60))} ago`
}

/** A start this close after boot was the boot, not a change anybody made. */
const BOOT_SETTLE = 120

/** How far back the recent changes reach. */
const RECENT = 24 * 3600

export type UnitChange = {
  unit: SystemdUnit
  /** Unix seconds. */
  at: number
  verb: string
  tone: DotTone
}

/**
 * The last thing that happened to a unit, if it happened since the host
 * finished booting and within the last day: a start, a stop, a oneshot that
 * ran, a failure, a restart in progress. An active unit's change is when it
 * became active — a reload moves systemd's state-change time without
 * restarting anything.
 */
export function unitChange(unit: SystemdUnit, now: number, bootedAt?: number): UnitChange | null {
  const bucket = unitBucket(unit)
  const at = bucket === "active" ? (unit.activeSince ?? unit.changedAt) : unit.changedAt
  if (!at) return null
  if (bootedAt && at <= bootedAt + BOOT_SETTLE) return null
  if (now / 1000 - at > RECENT) return null
  switch (bucket) {
    case "failed":
      return { unit, at, verb: "failed", tone: "danger" }
    case "changing":
      return { unit, at, verb: unitStateWord(unit), tone: "warning" }
    case "active":
      return { unit, at, verb: unit.subState === "exited" ? "ran" : "started", tone: "running" }
    default:
      return {
        unit,
        at,
        verb: unit.type === "oneshot" && unit.result === "success" ? "finished" : "stopped",
        tone: "stopped",
      }
  }
}

/** Every unit's recent change, newest first. */
export function recentChanges(units: SystemdUnit[], now: number, bootedAt?: number) {
  return units
    .map((unit) => unitChange(unit, now, bootedAt))
    .filter((change): change is UnitChange => change !== null)
    .sort((a, b) => b.at - a.at)
}

/**
 * The command lines in one of `systemctl show`'s Exec properties. Each
 * command is printed as `{ path=… ; argv[]=… ; ignore_errors=no ; … }`, and
 * the argv can hold " ; " itself — nginx's is `-g daemon on; master_process
 * on;` — so it is read up to the field after it rather than split.
 */
export function execCommands(value: string | undefined): string[] {
  if (!value) return []
  const commands = [...value.matchAll(/argv\[\]=(.*?) ; (?:ignore_errors|flags)=/g)].map((m) =>
    m[1].trim(),
  )
  return commands.length > 0 ? commands : [value.trim()]
}
