import type { ProcessRow } from "@/lib/types"

export type StorageFile = {
  path: string
  size: number
  allocated: number
  modified: string
  identity: string
  links: number
}
export type StorageDirectory = { path: string; allocated: number; entries: number }
export type StorageDuplicate = { sha256: string; files: StorageFile[]; reclaimable: number }
export type StorageReport = {
  requestedPath?: string
  hostFilesystem?: boolean
  path: string
  checkedAt: string
  complete: boolean
  entries: number
  allocated: number
  directories: StorageDirectory[]
  inodeDirectories: StorageDirectory[]
  largeFiles: StorageFile[]
  temporaryFiles: StorageFile[]
  duplicates: StorageDuplicate[]
  silences: { path: string; reason: string }[]
  skippedMounts: number
  hashedBytes: number
  duplicateScanComplete: boolean
  duplicateScanRequested: boolean
  duplicateScope: string
  filesystem?: { total: number; available: number; freeInodes: number; totalInodes: number }
}
export type StorageSelection = {
  file: StorageFile
  kind: "temporary" | "duplicate"
  keeper?: StorageFile
  sha256?: string
}
export type StorageCleanupResult = {
  items: { path: string; removed: boolean; allocated: number; error?: string }[]
  removedBytes: number
}
export type WorkloadSort = "cpu" | "memory" | "swap" | "io" | "handles"
/** A process named by its identity, so a fix cannot land on a reused PID. */
export type WorkloadMember = { pid: number; createTime: string }
/**
 * One workload: a supervisor's processes, or the copies of one program started
 * by hand — forty renderers are one culprit, and the fix is the group's.
 */
export type WorkloadGroup = {
  key: string
  manager: ProcessRow["manager"]
  name: string
  label?: string
  count: number
  cpuPercent: number
  memory: number
  swap: number
  ioRate: number
  handles: number
  users: string[]
  /** The heaviest member by the measure asked for. */
  pid: number
  cmdline: string
  members: WorkloadMember[]
  truncated?: boolean
  /** What started a hand-run group, where that is a program rather than a shell. */
  launcher?: { pid: number; name: string; cmdline: string; createTime: string; username: string }
}
export type WorkloadReport = {
  checkedAt: string
  sort: WorkloadSort
  processes: ProcessRow[]
  groups?: WorkloadGroup[]
  total: number
  silences: string[]
}
export type WorkloadControlResult = {
  items: { pid: number; ok: boolean; skipped?: boolean; error?: string }[]
  signalled?: number
  changed?: number
}
export type HealthInvestigation =
  | { kind: "storage"; path: string; inodes: boolean }
  | { kind: "workloads"; sort: WorkloadSort }
  | { kind: "network"; networkInterface?: string }
  | { kind: "services" }
  | { kind: "containers" }
  | { kind: "external"; reason: string }

export function advisorFileHref(path: string) {
  const parent = path.slice(0, path.lastIndexOf("/")) || "/"
  return `/files?path=${encodeURIComponent(parent)}&entry=${encodeURIComponent(path)}`
}

export function healthInvestigation(id: string): HealthInvestigation | undefined {
  if (id.startsWith("disk:")) return { kind: "storage", path: id.slice(5), inodes: false }
  if (id.startsWith("inodes:")) return { kind: "storage", path: id.slice(7), inodes: true }
  if (["load", "psi-cpu"].includes(id)) return { kind: "workloads", sort: "cpu" }
  if (["memory", "psi-mem"].includes(id)) return { kind: "workloads", sort: "memory" }
  if (id === "swap") return { kind: "workloads", sort: "swap" }
  if (["iowait", "psi-io"].includes(id)) return { kind: "workloads", sort: "io" }
  if (id === "files") return { kind: "workloads", sort: "handles" }
  if (id === "timewait") return { kind: "network" }
  if (id.startsWith("drops:")) return { kind: "network", networkInterface: id.slice(6) }
  if (id.startsWith("neterr:")) return { kind: "network", networkInterface: id.slice(7) }
  if (id.startsWith("tcp:") || id === "probes:beyond-host") return { kind: "network" }
  if (id === "steal")
    return {
      kind: "external",
      reason:
        "The hypervisor controls CPU steal. Share the measured history with your host, or move or resize the VM. Local process controls can reduce your demand but cannot recover CPU assigned to another tenant.",
    }
  if (id.startsWith("temp:"))
    return {
      kind: "external",
      reason:
        "Inspect cooling, airflow and the device with your hardware provider. Reducing CPU work may help CPU temperature, but cannot repair a fan or a hot disk. The limits below come from the sensor driver.",
    }
  if (id === "systemd.failed") return { kind: "services" }
  if (id.startsWith("docker.")) return { kind: "containers" }
  return undefined
}

/** A keeper is never selectable here, and overlapping temp/copy evidence is one operation. */
export function storageCandidates(report: StorageReport): StorageSelection[] {
  const candidates = new Map<string, StorageSelection>()
  for (const file of report.temporaryFiles) candidates.set(file.path, { kind: "temporary", file })
  const keepers = new Set(report.duplicates.map((group) => group.files[0]?.path))
  for (const group of report.duplicates) {
    const keeper = group.files[0]
    if (!keeper) continue
    for (const file of group.files.slice(1)) {
      if (!keepers.has(file.path))
        candidates.set(file.path, { kind: "duplicate", file, keeper, sha256: group.sha256 })
    }
  }
  for (const keeper of keepers) if (keeper) candidates.delete(keeper)
  return [...candidates.values()]
}
