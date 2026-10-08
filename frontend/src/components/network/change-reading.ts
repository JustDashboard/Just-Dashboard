import type { NetworkChangeStatus } from "@/lib/types"

export const CHANGE_PHASE: Record<NetworkChangeStatus["phase"], string> = {
  prepared: "Preparing a change",
  runtime_applied: "Runtime applied",
  persisted: "Persistence written",
  saved: "Saved",
  recovering: "Recovery in progress",
  recovered: "Recovered",
  degraded: "Recovery needs attention",
  boot_degraded: "Boot restoration failed",
  unreadable: "Change status unreadable",
}

export function changeStatus(change: NetworkChangeStatus) {
  const danger =
    ["degraded", "boot_degraded", "unreadable"].includes(change.phase) ||
    change.boot === "failed" ||
    change.watchdog === "failed_to_arm" ||
    Boolean(change.recoveryErrors?.length)
  const runtimeOnly = change.persistence === "not_applicable" && change.boot === "not_applicable"
  const verified =
    change.phase === "saved" &&
    change.runtime === "applied" &&
    change.watchdog === "completed" &&
    ((change.persistence === "written" && change.boot === "enabled") || runtimeOnly)
  return {
    label:
      change.phase === "saved" && runtimeOnly
        ? "Runtime applied"
        : (CHANGE_PHASE[change.phase] ?? "Change state unknown"),
    tone: danger ? ("danger" as const) : verified ? ("running" as const) : ("warning" as const),
  }
}

export const RUNTIME_STATE: Record<NetworkChangeStatus["runtime"], string> = {
  not_applied: "Not applied",
  applied: "Applied",
  undo_attempted: "Undo attempted; verify current state",
  restored: "Previous runtime restored",
  unknown: "Unknown",
}
export const PERSISTENCE_STATE: Record<NetworkChangeStatus["persistence"], string> = {
  not_applicable: "Runtime only; not saved for boot",
  not_written: "Not written",
  written: "Written",
  restored: "Previous files restored",
  unknown: "Unknown",
}
export const BOOT_STATE: Record<NetworkChangeStatus["boot"], string> = {
  not_applicable: "Not applicable to this runtime change",
  not_verified: "Not verified",
  enabled: "Restore unit enabled",
  unsupported: "Not supported on this host",
  failed: "Restore unit could not be enabled",
  unknown: "Unknown",
}
export const WATCHDOG_STATE: Record<NetworkChangeStatus["watchdog"], string> = {
  unsupported: "Independent recovery unavailable on this host",
  armed: "Independent recovery armed",
  completed: "Change completed; watchdog released",
  failed_to_arm: "Independent recovery could not be armed",
  recovered: "Recovered by the host watchdog",
}
