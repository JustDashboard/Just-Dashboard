import type { LogSource, LogSourceIndex, ProcessRow } from "@/lib/types"
import { journalIdSource, journalSource } from "@/lib/log-sources"
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
      return `/docker/containers/${encodeURIComponent(process.managerName)}`
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

/**
 * sshd's unit under either distribution's name, and the per-connection
 * instances a socket-activated sshd runs as: the units whose journal is
 * login records, which the server reads to administrators only
 * (`authJournalUnit` in `handlers_logs.go`). A page that knows it would be
 * refused says so rather than opening a read that is.
 */
export function authUnit(unit: string): boolean {
  const name = unit.replace(/\.service$/, "")
  return name === "ssh" || name === "sshd" || name.startsWith("ssh@") || name.startsWith("sshd@")
}

/** The names cron's daemon runs under: Debian's, Red Hat's, and cronie's own. */
const CRON_UNITS = ["cron.service", "crond.service", "cronie.service"]

/**
 * Where this host's cron writes what it ran, in the order that reads it
 * best: the daemon's own unit, whose journal holds each job's line and the
 * daemon's; a cron file where syslog splits one out (`/var/log/cron` on Red
 * Hat); or, on a journal with neither, the jobs' lines by program — the same
 * reading the logs page's rail offers. Nothing when the host has none of
 * them, which is a host without cron or without a readable log of it.
 */
export function cronLogSource(index: LogSourceIndex | undefined): LogSource | undefined {
  if (!index || !Array.isArray(index.sources)) return undefined
  const unit = index.units?.find((u) => CRON_UNITS.includes(u.name))
  if (unit) {
    return { id: journalSource(unit.name), label: unit.name, kind: "journal", rotated: false }
  }
  const file = index.sources.find((s) => s.path && s.lens === "cron")
  if (file) return file
  if (index.sources.some((s) => s.kind === "journal")) {
    return {
      id: journalIdSource(["CRON", "crond"]),
      label: "cron",
      kind: "journal-id",
      rotated: false,
    }
  }
  return undefined
}
