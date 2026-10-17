import type { Job } from "@/lib/types"

/**
 * What the point-in-time recovery routes answer, as the server writes them
 * (`backend/internal/api/handlers_db_pitr.go`). Setting archiving up, a base
 * backup, a recovery and a switch each run as a background job; the job's
 * last `result` line says what it produced.
 */

/** One base backup, as WAL-G lists it. */
export type WalBackup = {
  name: string
  startedAt: string
  finishedAt: string
  /** The segment of the log it begins in. */
  walFile: string
  pgVersion: number
  /** What the destination holds for it: compressed and encrypted. */
  size: number
  /** The cluster it unpacks to. */
  dataSize: number
  permanent?: boolean
}

/** The stretch of time an archive can recover to. */
export type WalWindow = { from?: string; to?: string }

/** What an archive held when it was last listed. */
export type PitrReading = {
  backups: WalBackup[]
  /** The newest whole segment of log the archive holds, and when it arrived. */
  latest?: string
  latestAt?: string
  window: WalWindow
  /** Base backups and log together, in the destination. */
  size: number
  readAt: string
}

export type PitrTargetKind = "local" | "s3" | "b2"

export type PitrTarget = {
  bucket?: string
  region?: string
  endpoint?: string
  prefix?: string
  path?: string
}

/** One server's archive. Its keys never leave the server. */
export type PitrArchive = {
  id: number
  container: string
  connectionId: number
  stream: string
  targetKind: PitrTargetKind
  target: PitrTarget
  hasCredentials: boolean
  keyFingerprint: string
  /** When base backups are taken; empty is only by hand. */
  schedule: string
  /** How many days of moments are kept; zero is everything. */
  retentionDays: number
  image: string
  createdAt: string
  createdBy?: string
  /** Set once the server stopped writing into it. */
  endedAt?: string
  reading?: PitrReading
  lastBackupAt?: string
  lastBackupError?: string
}

/** What the server itself says about its archiving (pg_stat_archiver). */
export type WalArchiver = {
  archiveMode: string
  walLevel: string
  archivedCount: number
  lastArchived?: string
  lastArchivedAt?: string
  failedCount: number
  lastFailed?: string
  lastFailedAt?: string
  /** The segment being written now. */
  current?: string
  /** Finished segments not handed over yet; absent where the account may not list them. */
  waiting?: number
  version: number
  inRecovery: boolean
}

export type PitrRestorePoint = {
  id: number
  name: string
  lsn: string
  createdAt: string
  createdBy?: string
}

export type RecoveryTarget = {
  kind: "latest" | "time" | "name"
  time?: string
  name?: string
}

/** A copy recovered from an archive into a container of its own. */
export type PitrRestore = {
  id: number
  archiveId: number
  source: string
  container: string
  /** The copy's own connection, while it is kept beside the server. */
  connectionId?: number
  port: number
  target: RecoveryTarget
  baseBackup: string
  recoveredTo?: string
  /** "ready" beside the server it came from, "switched" in its place. */
  state: "ready" | "switched"
  /** After a switch: the name the server it replaced was set aside under. */
  replaced?: string
  createdAt: string
  createdBy?: string
  switchedAt?: string
}

/** `GET /databases/{id}/pitr`. */
export type DbPitr = {
  /** Whether this server can be set up, and why not. */
  available: boolean
  reason?: string
  container?: string
  running: boolean
  /** The archive the server writes into; absent while it writes none. */
  archive?: PitrArchive
  /** The archives it wrote into before, still kept. */
  earlier: PitrArchive[]
  /** The container was made again without the settings, somewhere other than this page. */
  drift?: boolean
  archiver?: WalArchiver
  /** What WAL-G last said in the server's log about a segment it could not archive. */
  archiveError?: string
  /** What the archive covers now. */
  window: WalWindow
  nextBackup?: string
  /** Why the destination could not be listed just now. */
  readError?: string
  restorePoints: PitrRestorePoint[]
  restores: PitrRestore[]
  /** The job running for the server now. */
  job?: Job
  defaults: { schedule: string; retentionDays: number; directory: string }
}

/** `POST /databases/{id}/pitr` with `preview`: what setting it up will do. */
export type PitrPlan = {
  container: string
  stream: string
  location: string
  settings: Record<string, string>
  /** The server's command line as it will be started. */
  command: string[]
  mounts: string[]
}

export type PitrSetupRequest = {
  targetKind?: PitrTargetKind
  target?: PitrTarget
  secrets?: { accessKeyId: string; secretAccessKey: string }
  /** Take the destination, keys and all, from this backup job. */
  fromJob?: number
  schedule: string
  retentionDays: number
  preview?: boolean
}

/** `POST /databases/{id}/pitr/restore` with `preview`: what a recovery will do. */
export type PitrRestorePlan = {
  container: string
  port: number
  backup: string
  backupAt: string
  settings: Record<string, string>
}

export type PitrRestoreRequest = {
  archiveId?: number
  target: RecoveryTarget
  name?: string
  preview?: boolean
}

/** The last `result` line of a point-in-time recovery job, parsed. */
export type PitrJobResult = {
  archive?: number
  backup?: string
  container?: string
  connectionId?: number
  port?: number
  recoveredTo?: string
  replaced?: string
  summary?: string
}
