import type { Job } from "@/lib/types"

/**
 * What the dump, restore and copy routes answer, as the server writes them.
 * A dump, a restore and a copy each run as a background job; the job's last
 * `result` line says what it produced.
 */

/** What a dump was asked to hold. `{}` is everything, in the tool's own defaults. */
export type DbDumpContents = {
  schemaOnly?: boolean
  dataOnly?: boolean
  tables?: string[]
  excludeTables?: string[]
  /** Absent = the tool's default. */
  compression?: "gzip" | "none"
  /** A key–value server's numbered databases; absent = every one that holds a key. */
  databases?: number[]
}

export type DbDumpOrigin = "dump" | "upload" | "safety"

export type DbBackupFile = {
  file: string
  size: number
  /** The dump's start when it has a description, else when the file was written. */
  takenAt: string
  /** Read from the file's first bytes, not its name. */
  format: string
  // Everything below comes from the description kept beside the dump; a file
  // put in the directory by hand has none of it.
  durationMs?: number
  database?: string
  tool?: string
  toolVersion?: string
  summary?: string
  contents?: DbDumpContents
  note?: string
  origin?: DbDumpOrigin
  /** The account that asked for it. */
  by?: string
}

/** Which dump options this connection's engine honours; the request refuses the others. */
export type DbDumpSupport = {
  schemaOnly: boolean
  dataOnly: boolean
  tables: boolean
  compression: boolean
  /** A key–value server's numbered databases. */
  databases: boolean
  /** A restore into a new database, and a copy. */
  newDatabase: boolean
}

export type DbBackups = {
  /** Where the dumps are kept on the server. */
  dir: string
  /** Newest first. A dump still being written is not among them. */
  files: DbBackupFile[]
  options: DbDumpSupport
  /** The dump, restore or copy running against this connection now. */
  job?: Job
}

export type DbBackupRequest = {
  database?: string
  schemaOnly?: boolean
  dataOnly?: boolean
  tables?: string[]
  excludeTables?: string[]
  compression?: "gzip" | "none"
  databases?: number[]
  note?: string
}

export type DbRestoreRequest = {
  file: string
  target?: "this" | { newDatabase: string }
  dumpFirst?: boolean
}

export type DbCopyRequest = { name: string; structureOnly?: boolean }

/** The last `result` line of a transfer job, parsed. */
export type DbTransferResult = {
  /** A dump: the file written. A restore: the dump it read. */
  file?: string
  size?: number
  /** What was dumped, or the database a restore or a copy loaded into. */
  database?: string
  summary?: string
  tool?: string
  /** The dump taken of the database before a restore replaced it. */
  safetyDump?: string
  /** The end of what the restoring tool said. */
  output?: string
}

/** One table or collection of `GET /databases/{id}/tables`, as far as a dump's picker reads it. */
export type DbDumpObject = { schema: string; name: string; type?: string }
