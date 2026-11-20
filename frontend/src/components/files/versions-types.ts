/** `GET /files/versions`: one file's earlier contents, newest first. */
export type FileVersions = {
  path: string
  versions: FileVersion[]
  /** What the dashboard last wrote here, when it did. */
  lastSave?: { revision: string; at: string; by: string }
  /** Whether saves keep versions at all (`JD_FILE_VERSIONS` above 0). */
  keeping: boolean
  perFile: number
  /** The largest content a version is kept of. */
  maxBytes: number
  /** The current file is over `maxBytes`, so its saves keep nothing. */
  tooLarge: boolean
  /** Whether this person sees the Backups page, and so backup copies here. */
  backups: boolean
}

/** One row of the history: an edit the dashboard replaced, or a backup run's copy. */
export type FileVersion = {
  /** `e<n>` for an edit version, `b<run>` for a backup's copy. */
  id: string
  kind: "edit" | "backup"
  at: string
  by?: string
  /**
   * How the dashboard replaced it: a save, a picture saved over its
   * original, an upload, a restore, or `displaced` — the dashboard's own save
   * that was later changed outside the dashboard, whose `at` and `by` are
   * the save's.
   */
  reason?: "save" | "image" | "upload" | "restore" | "displaced"
  /** It was not what the dashboard last wrote: changed outside the dashboard. */
  outside?: boolean
  size?: number
  revision?: string
  backup?: FileVersionBackup
}

export type FileVersionBackup = {
  runId: number
  jobId: number
  job: string
  at: string
  targetKind: "local" | "s3" | "b2" | "sftp" | "rclone"
  destination: string
  endpoint?: string
  encrypted?: boolean
  imported?: boolean
  /** Reading it stays on this server. */
  local: boolean
}

/** `GET /files/versions/content`: one version as text for the diff. */
export type FileVersionContent = {
  content: string
  binary: boolean
  size: number
  revision: string
}
