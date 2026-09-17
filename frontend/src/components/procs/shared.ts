import type { ProcessRow } from "@/lib/types"
import type { Tone } from "@/components/tone"

/** The word for who supervises a process, as the table and the sheet print it. */
export function managerName(manager: ProcessRow["manager"]): string {
  switch (manager) {
    case "pm2":
      return "PM2"
    case "systemd":
      return "systemd"
    case "container":
      return "Container"
    case "session":
      return "Login session"
    case "kernel":
      return "Kernel"
    default:
      return "Unmanaged"
  }
}

/**
 * Where a process's supervisor is managed from, if this product has a page
 * for it. A systemd child opens its unit, a PM2 child its application, a
 * container child its container — the remedy for a runaway worker is usually
 * on that page rather than on this one.
 */
export function managerHref(process: Pick<ProcessRow, "manager" | "managerName">): string | null {
  if (!process.managerName) return null
  switch (process.manager) {
    case "systemd":
      return `/processes/services?unit=${encodeURIComponent(process.managerName)}`
    case "pm2":
      return `/processes/pm2?app=${encodeURIComponent(process.managerName)}`
    case "container":
      return `/docker/containers?container=${encodeURIComponent(process.managerName)}`
    default:
      return null
  }
}

/** A process state as the one status vocabulary reads it. */
export function processStateTone(state: ProcessRow["state"]): string {
  if (state === "blocked" || state === "zombie") return "failed"
  if (state === "sleeping") return "inactive"
  return state
}

/** The colour a CPU share takes, the same in a cell and on a meter. */
export function cpuTone(percent: number): Tone {
  if (percent >= 50) return "danger"
  if (percent >= 10) return "warning"
  return "default"
}

/** One process across polls: a PID alone is reused, the pair is not. */
export function processKey(process: { pid: number; createTime: string }): string {
  return `${process.pid}-${process.createTime}`
}
