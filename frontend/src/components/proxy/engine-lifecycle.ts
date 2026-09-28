import { ApiError } from "@/lib/api"
import type { EngineAction, ProxyDiagnostic, ProxyValidation, SystemdUnit } from "@/lib/types"
import type { Verdict } from "@/components/status-dot"

/**
 * Where the engine's service stands, for which command leads: a running
 * engine is reloaded, a stopped one started. `changing` is systemd between
 * the two — starting, stopping, or waiting to restart after a crash.
 */
export type EngineRun = "running" | "stopped" | "changing"

export function engineRun(unit: SystemdUnit): EngineRun {
  switch (unit.activeState) {
    case "active":
    case "reloading":
      return "running"
    case "inactive":
    case "failed":
      return "stopped"
    default:
      return "changing"
  }
}

/**
 * The state beside the engine's name when it is not running. systemd's
 * "inactive (dead)" is its own vocabulary; the line says what it means.
 */
export function stoppedLabel(unit: SystemdUnit): string {
  if (unit.activeState === "inactive") return "not running"
  if (unit.activeState === "failed") return "failed"
  return unit.subState && unit.subState !== unit.activeState
    ? `${unit.activeState} (${unit.subState})`
    : unit.activeState
}

/** A unit systemd refuses to start at all until it is unmasked on the host. */
export function isMasked(unit: SystemdUnit): boolean {
  return unit.unitFileState === "masked" || unit.unitFileState === "masked-runtime"
}

/** Whether the service comes back after a reboot, as the identity line says it. */
export type BootState = {
  label: string
  /** Worth the reader's attention: the proxy stays down after a reboot. */
  warn: boolean
  /** `systemctl enable` would make it start at boot. */
  canEnable: boolean
}

/**
 * What `UnitFileState` says about the next boot. Only the states that answer
 * it are named: `static`, `indirect`, `generated` and the like start the unit
 * when something else pulls it in, which the line cannot vouch for either way.
 */
export function bootState(unit: SystemdUnit): BootState | undefined {
  switch (unit.unitFileState) {
    case "enabled":
      return { label: "starts at boot", warn: false, canEnable: false }
    // Enabled under /run, which is gone after a reboot: this boot only.
    case "enabled-runtime":
    case "disabled":
      return { label: "won't start at boot", warn: true, canEnable: true }
    case "masked":
    case "masked-runtime":
      return { label: "masked, so it cannot start", warn: true, canEnable: false }
    default:
      return undefined
  }
}

/** systemd's `Result` of a failed unit, as the rest of a sentence about the engine. */
const RESULT: Record<string, string> = {
  "exit-code": "exited with an error",
  signal: "was killed by a signal",
  "core-dump": "crashed",
  timeout: "timed out starting or stopping",
  watchdog: "stopped answering its watchdog",
  "start-limit-hit": "failed to start too often, so systemd stopped trying",
  resources: "could not be given what it needs to start",
  "oom-kill": "was killed for running out of memory",
  protocol: "did not report its start the way its unit expects",
  "exec-condition": "was refused by its start condition",
}

/**
 * Why a failed engine failed, as its heading ("nginx exited with an error"),
 * and the facts that come with it: systemd's own word for the result, and how
 * many times it restarted the engine before this.
 */
export function failureSummary(
  engine: string,
  unit: SystemdUnit,
): { title: string; facts: string[] } {
  const result = unit.result && unit.result !== "success" ? unit.result : undefined
  const words = result ? RESULT[result] : undefined
  const facts: string[] = []
  if (result) facts.push(`result ${result}`)
  const restarts = unit.restarts ?? 0
  if (restarts > 0) {
    facts.push(
      restarts === 1 ? "restarted once by systemd" : `restarted ${restarts} times by systemd`,
    )
  }
  return { title: words ? `${engine} ${words}` : `${engine} failed`, facts }
}

/** What each verb is while it is under way. */
export const PARTICIPLE: Record<EngineAction, string> = {
  start: "Starting…",
  restart: "Restarting…",
  stop: "Stopping…",
  enable: "Enabling…",
  "reset-failed": "Clearing…",
}

/**
 * A start or restart turned down by the engine's config test: the server's
 * sentence, and the test itself where the response carried it.
 */
export type Refusal = { message: string; validation?: ProxyValidation }

export function refusalOf(err: unknown): Refusal | undefined {
  if (!(err instanceof ApiError) || err.code !== "invalid_config") return undefined
  const body = err.body as { validation?: unknown } | undefined
  const validation = body?.validation
  return {
    message: err.message,
    validation:
      validation && typeof validation === "object" && "output" in validation
        ? (validation as ProxyValidation)
        : undefined,
  }
}

/**
 * A refusal's first line, which is the server's sentence; the lines after it
 * are the test's output, which the refusal shows as its diagnostics instead.
 */
export function refusalHeadline(refusal: Refusal): string {
  return refusal.message.split("\n", 1)[0]
}

/**
 * Whether a diagnostic's file is one the config editor may open: under one of
 * the proxy's own directories, which is what `/proxy/config` reads. A module
 * file or an include from elsewhere is named, not offered.
 */
export function openableFile(file: string | undefined, roots: string[]): boolean {
  if (!file || !file.startsWith("/")) return false
  return roots.some((root) => {
    const dir = root.replace(/\/+$/, "")
    return dir !== "" && file.startsWith(`${dir}/`) && !file.slice(dir.length).includes("/../")
  })
}

/** A diagnostic's level as the verdict its dot is drawn in. */
export function diagnosticVerdict(level: ProxyDiagnostic["level"]): Verdict {
  switch (level) {
    case "warn":
      return "warning"
    case "notice":
    case "info":
      return "notice"
    default:
      return "critical"
  }
}
