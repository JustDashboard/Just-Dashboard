/*
 * Backups: jobs and the resources they cover, archives and their manifests,
 * runs, restores and restore checks.
 */

export type BackupJob = {
  id: number
  revision: number
  name: string
  sources: string[]
  excludes: string[]
  targetKind: "local" | "s3" | "b2" | "sftp" | "rclone"
  target: {
    bucket?: string
    region?: string
    endpoint?: string
    prefix?: string
    /** The directory here, on the SFTP server, or inside the rclone remote. */
    path?: string
    /** SFTP: the server and account. */
    host?: string
    port?: number
    username?: string
    /** SFTP: the host key pinned the first time the server was reached. */
    hostKey?: string
    hostKeyFingerprint?: string
    /** rclone: the remote's backend, read from its sealed configuration (drive, onedrive…). */
    remoteType?: string
  }
  schedule: string
  retention: number
  /** Prunes artifacts older than this many days; 0 keeps by count alone. */
  retentionDays: number
  enabled: boolean
  createdAt: string
  hasCredentials: boolean
  lastRun?: BackupRun
  nextRun?: string
  /** When the newest successful artifact was taken. */
  lastSuccessAt?: string
  /** A scheduled job that has gone two intervals without a successful run. */
  /** The retained artifacts and what they add up to. */
  stored: { runs: number; bytes: number }
  sqlitePaths?: string[]
  // Saved database connections whose native dump every run captures.
  databaseDumps?: number[]
  /** Containers frozen for the archive step, so their volumes are quiet. */
  pauseContainers?: string[]
  /** New archives are sealed to the server's backup key before they are written. */
  encrypt?: boolean
  recovery?: BackupRecoveryPlan
  /**
   * Adopted from what a destination holds: the record of another server's job
   * (or this one's before a rebuild). It never runs, is never scheduled, and
   * its archives are never pruned from here.
   */
  imported?: boolean
  origin?: BackupJobOrigin
}

/** What an imported job was on the server it came from. */
export type BackupJobOrigin = {
  /** The install's identity, a random value made once per install. */
  id: string
  host?: string
  jobId: number
  jobName: string
  /** Archives this install wrote whose records it no longer had. */
  thisServer?: boolean
}

/** The server's backup key, by its public half; the secret never leaves but by export. */
export type BackupArchiveKey = {
  exists: boolean
  /** The age public key, as an exported key file's "public key" line prints it. */
  recipient?: string
  createdAt?: string
  exportedAt?: string
  encryptingJobs: number
  encryptedArchives: number
  /** Keys brought from other servers to open what they sealed; nothing new is sealed to them. */
  imported?: { recipient: string; importedAt: string }[]
}

export type BackupResourceKind =
  "dashboard" | "proxy" | "volume" | "stack" | "deployment" | "repository" | "database"

/**
 * One thing the dashboard already knows about that a backup could protect,
 * with the job it would write for it and the jobs that already cover it.
 */
export type BackupResource = {
  kind: BackupResourceKind
  id: string
  name: string
  detail?: string
  paths?: string[]
  connectionId?: number
  /** The project a deployment is; its `id` is the name jobs cover it by. */
  projectId?: number
  suggest: {
    name: string
    sources: string[]
    excludes?: string[]
    sqlitePaths?: string[]
    databaseDumps?: number[]
    pauseContainers?: string[]
  }
  coveredBy: { jobId: number; jobName: string; enabled: boolean }[]
  protected: boolean
  lastBackupAt?: string
}

export type BackupResourceReport = {
  resources: BackupResource[]
  unavailable: Record<string, string>
}

export type BackupArchiveEntry = {
  name: string
  size: number
  mode: string
  isDir: boolean
}

export type BackupRestoreResult = {
  runId: number
  destination: string
  entries: number
  bytes: number
  skipped?: string[]
  targets?: string[]
  /** What a restore in place made first: "volume NAME", or a directory. */
  created?: string[]
}

/** Where one recorded source goes in a restore in place on this host, and what it makes first. */
export type BackupRestoreTarget = {
  archivePath: string
  path: string
  target: string
  kind: "path" | "volume"
  volume?: string
  exists: boolean
  creates?: "volume" | "directory"
  refused?: string
}

export type BackupDatabaseDump = {
  connectionId: number
  name: string
  driver: string
  database: string
  method: string
  file: string
  archivePath: string
  digest: string
  bytes: number
}

export type BackupManifest = {
  version: number
  /** Over the bytes the destination holds: the ciphertext, for an encrypted archive. */
  artifactDigest: string
  complete: boolean
  sources?: BackupManifestSource[]
  databaseDumps?: BackupDatabaseDump[]
  pausedContainers?: string[]
  encryption?: BackupArchiveEncryption
  artifactBytes?: number
  /** The install, job and run that took the archive; written since 0.8.2. */
  origin?: {
    id: string
    host?: string
    jobId: number
    jobName: string
    runId: number
    startedAt: string
  }
}

export type BackupManifestSource = {
  path: string
  archivePath: string
  /** "volume" for a Docker volume's directory; absent for a path. */
  kind?: "volume"
  volume?: string
}

/** How an archive was sealed: age, to the key whose public half is `recipient`. */
export type BackupArchiveEncryption = {
  format: "age"
  recipient: string
  /** Over the .tar.gz inside, which is what a copy decrypted by hand is checked against. */
  plaintextDigest: string
  plaintextBytes: number
}

export type BackupRecoveryPlan = {
  image: string
  command: string[]
  schemaVersion: string
  expectedOutputDigest: string
  timeoutSeconds: number
  maxBytes: number
  automatic: boolean
}

export type BackupRestoreVerification = {
  id: number
  runId: number
  state: "running" | "passed" | "failed" | "cleanup_failed"
  startedAt: string
  endedAt?: string
  artifactDigest: string
  manifestDigest: string
  planDigest: string
  applicationImage?: string
  schemaVersion: string
  outputDigest?: string
  entries: number
  bytes: number
  cleanupComplete: boolean
  detail?: string
}

export type BackupRun = {
  id: number
  jobId: number
  startedAt: string
  endedAt?: string
  status: "running" | "success" | "failed"
  artifact: string
  sizeBytes: number
  log: string
  trigger: string
  duration?: string
  manifest?: BackupManifest
  restoreVerification?: BackupRestoreVerification
  /** Adopted from a destination; its artifact is the destination's object. */
  imported?: boolean
  /** A copy the import kept on this server, read first while it is there. */
  localCopy?: string
}
