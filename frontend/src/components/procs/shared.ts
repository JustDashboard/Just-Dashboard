import type { LogSource, LogSourceIndex, ProcessGroup, ProcessRow } from "@/lib/types"
import { journalIdSource, journalSource } from "@/lib/log-sources"
import type { Tone } from "@/components/tone"
import { processProduct, unitProduct } from "@/components/product-logo"

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

/**
 * Why no signal would reach this process, if none would. A kernel thread
 * ignores signals from user space and a zombie has already exited; both used
 * to take a Kill that reported success and changed nothing.
 */
export function uncontrollable(
  process: Pick<ProcessRow, "state" | "manager">,
): "zombie" | "kernel" | null {
  if (process.state === "zombie") return "zombie"
  if (process.manager === "kernel") return "kernel"
  return null
}

/**
 * The line under a process's name: its command line, or what an empty one
 * means. A kernel thread has none; a zombie's went with its memory; anything
 * else is a command line this account could not read. All three used to say
 * "Kernel worker".
 */
export function processCaption(process: Pick<ProcessRow, "cmdline" | "state" | "manager">) {
  if (process.cmdline) return process.cmdline
  if (process.state === "zombie") return "exited — waiting for its parent to reap it"
  if (process.manager === "kernel") return "kernel thread"
  return "command line not readable"
}

/** The supervisor as the reader knows it: a container by its name, a unit by its own. */
export function ownerName(process: Pick<ProcessRow, "managerName" | "managerLabel">) {
  return process.managerLabel || process.managerName || ""
}

/** A workload's name as the reader knows it. */
export function groupName(group: ProcessGroup): string {
  if (group.manager === "container") return group.label || group.name
  if (group.manager === "systemd") return group.name.replace(/\.service$/, "")
  return group.name
}

/** What a workload is drawn as: the program it runs, or its supervisor's mark. */
export function groupProduct(group: ProcessGroup): string | undefined {
  switch (group.manager) {
    case "systemd":
      return unitProduct(group.name)
    case "container":
      return processProduct(group.label ?? "") ?? "docker"
    case "pm2":
      return "pm2"
    case "kernel":
      return "linux"
    default:
      return processProduct(group.name)
  }
}

/**
 * CPU as cores, for a sum over many processes. A process's share is read
 * against one core, as the table prints it — 100% is a core — and forty
 * renderers summed to 340% read as a number past a ceiling; 3.4 cores reads
 * as what it is.
 */
export function cores(percentOfOneCore: number): string {
  const n = percentOfOneCore / 100
  if (n >= 10) return `${n.toFixed(0)} cores`
  if (n >= 0.995) return `${n.toFixed(1)} cores`
  if (n >= 0.005) return `${n.toFixed(2)} cores`
  return "idle"
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

/** Who can reach a listening address. */
export function reach(address: string) {
  if (!address || address === "0.0.0.0" || address === "::" || address === "*")
    return "every interface"
  if (address.startsWith("127.") || address === "::1") return "this machine only"
  return "one address"
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
