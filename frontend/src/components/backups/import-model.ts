import { plural } from "@/lib/format"
import type { BackupJob, BackupManifestSource, BackupRun } from "@/lib/types"
import { rcloneService, sftpPlace } from "@/components/backups/shared"

/**
 * What a destination holds that this server did not write, as
 * `POST /backups/catalogue` answers it: the archives grouped by the install
 * that took them and the job there, each with what it covers, its size and
 * time, whether it is sealed and whether a key this server holds opens it.
 */
export type CatalogueArchive = {
  key: string
  name: string
  bytes: number
  modifiedAt?: string
  startedAt?: string
  /** Where what is said about it came from: the manifest beside it, or nothing (written before 0.8.2). */
  manifest: "sidecar" | "none" | "unreadable"
  contents: { paths: number; volumes: number; databaseDumps: number; sqliteSnapshots: number }
  kinds: ("paths" | "volume" | "dump" | "snapshot")[]
  sources?: BackupManifestSource[]
  databaseDumps?: { name: string; driver: string; database: string; bytes: number }[]
  digest?: string
  encrypted: boolean
  recipient?: string
  openable: boolean
  needsKey?: boolean
  importable: boolean
  problem?: string
  importedRun?: number
  importedJob?: number
}

export type CatalogueJob = {
  originJobId: number
  name: string
  archives: CatalogueArchive[]
  bytes: number
  newest?: string
  importedJob?: number
}

export type CatalogueOrigin = {
  /** Empty for the archives that carry no manifest, grouped by their names. */
  id: string
  host?: string
  thisServer?: boolean
  jobs: CatalogueJob[]
}

export type Catalogue = {
  destination: string
  origins: CatalogueOrigin[]
  archives: number
  /** Archives this server wrote and still has the runs of, left out. */
  ours: number
  truncated?: boolean
  hostKey?: string
  hostKeyFingerprint?: string
}

export type ImportResult = {
  destination: string
  jobs: BackupJob[]
  runs: BackupRun[]
  skipped: { key: string; reason: string }[]
}

/** A destination a catalogue is read from: one of this server's jobs', or one entered. */
export type DestinationRequest =
  | { jobId: number }
  | {
      targetKind: BackupJob["targetKind"]
      target: BackupJob["target"]
      secrets?: Record<string, string>
    }

/** One place this server's jobs write to, to read the catalogue of. */
export type KnownDestination = {
  key: string
  /** The job whose destination and keys are read. */
  jobId: number
  kind: BackupJob["targetKind"]
  service: string
  place: string
  jobs: string[]
}

/**
 * The places this server's jobs write, each once. Unlike the picture's
 * destinations, a bucket's prefix tells two apart: a catalogue reads only
 * where a job writes, so two prefixes of one bucket are two places to read.
 */
export function knownDestinations(jobs: BackupJob[]): KnownDestination[] {
  const out = new Map<string, KnownDestination>()
  for (const job of jobs) {
    const t = job.target
    const key = [
      job.targetKind,
      (t.endpoint ?? "").trim().toLowerCase(),
      t.bucket ?? "",
      (t.prefix ?? "").replace(/^\/+|\/+$/g, ""),
      (t.host ?? "").toLowerCase(),
      t.port ?? "",
      t.username ?? "",
      t.remoteType ?? "",
      t.path ?? "",
    ].join("|")
    const known = out.get(key)
    if (known) {
      known.jobs.push(job.name)
      continue
    }
    out.set(key, {
      key,
      jobId: job.id,
      kind: job.targetKind,
      service: serviceName(job),
      place: placeName(job),
      jobs: [job.name],
    })
  }
  return [...out.values()]
}

function serviceName(job: BackupJob): string {
  switch (job.targetKind) {
    case "local":
      return "A directory on this server"
    case "sftp":
      return `SFTP · ${job.target.host ?? ""}`
    case "rclone":
      return rcloneService(job.target.remoteType)
    case "b2":
      return "Backblaze B2"
    default:
      return job.target.endpoint
        ? `S3 · ${job.target.endpoint.replace(/^https?:\/\//, "")}`
        : "Amazon S3"
  }
}

function placeName(job: BackupJob): string {
  switch (job.targetKind) {
    case "local":
      return job.target.path ?? ""
    case "sftp":
      return sftpPlace(job)
    case "rclone":
      return job.target.path || "/"
    default: {
      const prefix = (job.target.prefix ?? "").replace(/^\/+|\/+$/g, "")
      return prefix ? `${job.target.bucket ?? ""}/${prefix}` : (job.target.bucket ?? "")
    }
  }
}

/** The fields of a destination typed in, as the import dialog holds them. */
export type DestinationFields = {
  kind: BackupJob["targetKind"]
  path: string
  bucket: string
  region: string
  endpoint: string
  prefix: string
  accessKey: string
  secretKey: string
  host: string
  port: string
  username: string
  password: string
  privateKey: string
  passphrase: string
  rcloneConfig: string
  /** An SFTP server's key as a reading of its catalogue presented it, held to on import. */
  hostKey?: string
}

export const EMPTY_DESTINATION: DestinationFields = {
  kind: "s3",
  path: "/var/backups/just-dashboard",
  bucket: "",
  region: "",
  endpoint: "",
  prefix: "",
  accessKey: "",
  secretKey: "",
  host: "",
  port: "22",
  username: "",
  password: "",
  privateKey: "",
  passphrase: "",
  rcloneConfig: "",
}

/** Whether the entered destination has what reading it needs. */
export function destinationReady(f: DestinationFields): boolean {
  switch (f.kind) {
    case "local":
      return f.path.trim() !== ""
    case "sftp":
      return (
        f.host.trim() !== "" &&
        f.username.trim() !== "" &&
        f.path.trim() !== "" &&
        (f.password !== "" || f.privateKey.trim() !== "")
      )
    case "rclone":
      return f.rcloneConfig.trim() !== ""
    case "b2":
      return f.bucket.trim() !== "" && f.endpoint.trim() !== ""
    default:
      return f.bucket.trim() !== ""
  }
}

/** The request body naming an entered destination; keys only for the kind that takes them. */
export function destinationBody(f: DestinationFields): DestinationRequest {
  switch (f.kind) {
    case "local":
      return { targetKind: "local", target: { path: f.path.trim() } }
    case "sftp":
      return {
        targetKind: "sftp",
        target: {
          host: f.host.trim(),
          port: Number(f.port) || 22,
          username: f.username.trim(),
          path: f.path.trim(),
          ...(f.hostKey ? { hostKey: f.hostKey } : {}),
        },
        secrets: {
          ...(f.password ? { password: f.password } : {}),
          ...(f.privateKey.trim() ? { privateKey: f.privateKey.trim() } : {}),
          ...(f.passphrase ? { passphrase: f.passphrase } : {}),
        },
      }
    case "rclone":
      return {
        targetKind: "rclone",
        target: { path: f.path.trim() },
        secrets: { rcloneConfig: f.rcloneConfig.trim() },
      }
    default:
      return {
        targetKind: f.kind,
        target: {
          bucket: f.bucket.trim(),
          region: f.region.trim(),
          endpoint: f.endpoint.trim(),
          prefix: f.prefix.trim(),
        },
        ...(f.accessKey || f.secretKey
          ? { secrets: { accessKeyId: f.accessKey.trim(), secretAccessKey: f.secretKey } }
          : {}),
      }
  }
}

/** What an archive covers, in words: "2 paths · a volume · 1 database dump". */
export function contentsWords(contents: CatalogueArchive["contents"]): string {
  const parts: string[] = []
  if (contents.paths > 0) parts.push(plural(contents.paths, "path"))
  if (contents.volumes > 0) parts.push(plural(contents.volumes, "volume"))
  if (contents.databaseDumps > 0) parts.push(plural(contents.databaseDumps, "database dump"))
  if (contents.sqliteSnapshots > 0) parts.push(plural(contents.sqliteSnapshots, "SQLite snapshot"))
  return parts.join(" · ")
}

export type ArchiveState = {
  state: "importable" | "imported" | "needs-key" | "no-manifest" | "refused"
  label: string
  tone: "running" | "warning" | "danger" | "notice"
}

/** Where an archive stands for this server, as its row says it. */
export function archiveState(archive: CatalogueArchive): ArchiveState {
  if (archive.importedRun) return { state: "imported", label: "imported", tone: "running" }
  if (archive.importable) return { state: "importable", label: "can import", tone: "notice" }
  if (archive.needsKey) return { state: "needs-key", label: "needs its key", tone: "warning" }
  if (archive.manifest === "none")
    return { state: "no-manifest", label: "no manifest", tone: "notice" }
  return { state: "refused", label: "cannot import", tone: "danger" }
}

/** The archives of a job that an import would take now. */
export function importableArchives(job: CatalogueJob): CatalogueArchive[] {
  return job.archives.filter((archive) => archive.importable)
}

/** The keys the catalogue's sealed archives need and this server does not hold, each once. */
export function missingKeys(catalogue: Catalogue): string[] {
  const out = new Set<string>()
  for (const origin of catalogue.origins)
    for (const job of origin.jobs)
      for (const archive of job.archives)
        if (archive.needsKey && archive.recipient) out.add(archive.recipient)
  return [...out]
}

/** An install as its group's title says it. */
export function originTitle(origin: CatalogueOrigin): string {
  if (!origin.id) return "Written before archives carried their manifest"
  if (origin.thisServer) return "This server, records no longer here"
  return origin.host || "A server that did not record its name"
}

/** The words a finished import is reported in. */
export function importSummary(result: ImportResult): string {
  const names = [...new Set(result.jobs.map((job) => job.name))]
  const taken = result.runs.length
  const into =
    names.length === 1 ? ` into ${names[0]}` : names.length > 1 ? ` into ${names.length} jobs` : ""
  const head = taken > 0 ? `Imported ${plural(taken, "archive")}${into}` : "Nothing imported"
  return result.skipped.length > 0 ? `${head}; ${result.skipped.length} left` : head
}

/** A key shortened for a line, keeping both ends so two can still be told apart. */
export function shortKey(recipient: string): string {
  return recipient.length > 24 ? `${recipient.slice(0, 12)}…${recipient.slice(-8)}` : recipient
}
