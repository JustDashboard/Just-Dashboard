import type { DbDriver, DbFlavor, Job } from "@/lib/types"

/**
 * What the server says about the machine's databases beyond the saved
 * connections: everything discovery found (`GET /databases/inventory`), how
 * well each connection is backed up (`GET /databases/backups/summary`), and
 * the answers of the routes the control center and Add a database call. The
 * shapes are the backend's own, kept beside the two areas that read them.
 */

export type DbInstanceKind = "server" | "file" | "data" | "embedded"
export type DbInstanceSource = "docker" | "compose" | "host" | "file" | "volume"

/** How the credentials are known. Never the credential itself. */
export type DbCredentials = "env" | "args" | "secret-file" | "open" | "peer" | "needed" | "unknown"

export type DbConfidence =
  "image" | "fingerprint" | "command" | "port" | "process" | "socket" | "unit" | "magic" | "marker"

export type DbEndpoint = {
  kind: "tcp" | "unix" | "container" | "file"
  host?: string
  port?: number
  path?: string
  bind?: string
  scope?: "loopback" | "private" | "public"
  primary?: boolean
}

export type DbInstanceContainer = {
  /** Absent for a compose service that is declared and was never created. */
  id?: string
  name: string
  image: string
  composeProject?: string
  composeService?: string
  health?: string
  status?: string
  networkMode?: string
  dataVolumes: { type: string; name?: string; source?: string; destination: string }[]
  environmentId?: string
}

export type DbInstanceHost = {
  pid?: number
  process?: string
  unit?: string
  unitState?: string
  enabled?: boolean
  user?: string
  cluster?: string
  dataDir?: string
  configFile?: string
}

export type DbFileHolder =
  "self" | "container" | "deployment" | "compose" | "application" | "tool" | "system"

export type DbInstanceFile = {
  /** A host path; for an embedded file, the path inside its container. */
  path: string
  size: number
  modified: string
  wal?: boolean
  holder: DbFileHolder
  containers?: string[]
  volume?: string
  project?: string
}

/** One thing discovery found on this machine, connected or not. */
export type DbInstance = {
  /** Its stable identity; handed back verbatim to connect and ignore. */
  key: string
  kind: DbInstanceKind
  name: string
  /** The driver's id where one opens it, the product's own id otherwise. */
  engine: string
  /** Empty: seen, and nothing here can open it. */
  driver: DbDriver | ""
  flavor?: DbFlavor
  variant?: string
  label: string
  version?: string
  source: DbInstanceSource
  /**
   * `running` is the only state a server can be connected in. Otherwise it is
   * Docker's word (exited, created, paused…), `declared` for a compose
   * service with no container, systemd's (inactive, failed…), or `file` and
   * `data` for what is not a server.
   */
  state: string
  endpoints: DbEndpoint[]
  container?: DbInstanceContainer
  host?: DbInstanceHost
  file?: DbInstanceFile
  user?: string
  database?: string
  credentials: DbCredentials
  confidence: DbConfidence
  evidence: string[]
  connectable: boolean
  reason?: string
  /** The saved connections that already point here. */
  connections: number[]
  ignored?: boolean
  /** The dashboard's own store: listed, never connected. */
  self?: boolean
}

export type DbInventoryScan = {
  source: "docker" | "compose" | "listeners" | "sockets" | "units" | "files"
  /** False: this collector could not read. The request still succeeded. */
  ok: boolean
  reason?: string
  truncated?: boolean
  /** Files only: a scan is under way and the rows shown are the previous one's. */
  running?: boolean
  count: number
  durationMs: number
  checkedAt: string
}

export type DbInventory = {
  instances: DbInstance[]
  scans: DbInventoryScan[]
  ignored: string[]
  /** `full` for an administrator, `reduced` for every other role. */
  detail: "full" | "reduced"
  checkedAt: string
}

export type DbInventoryConnectRequest = {
  key: string
  name?: string
  user?: string
  password?: string
  database?: string
}

/** `POST /databases/sync`: what connecting everything that states its credentials did. */
export type DbSyncReport = {
  added: string[]
  already: string[]
  unreachable: { container: string; driver: string; reason: string }[]
  needsCredentials: { driver: string; host: string; port: number; name: string }[]
  ignored: string[]
  scans: DbInventoryScan[]
}

export type DbBackupNewest = {
  file: string
  size: number
  takenAt: string
  format: string
  durationMs?: number
  tool?: string
  summary?: string
}

export type DbBackupSummary = {
  connections: {
    id: number
    name: string
    count: number
    totalSize: number
    newest: DbBackupNewest | null
    /** A dump, restore or copy running against this connection now. */
    job?: Job
  }[]
}

export type DbPowerAction = "start" | "stop" | "restart"

export type DbPowerResponse = {
  action: DbPowerAction
  via: "docker" | "systemd"
  target: string
  state?: string
}

/** One engine `POST /databases/provision` can start in a container. */
export type DbProvisionTemplate = {
  /** The id to send back: postgres, pgvector, valkey, … */
  engine: string
  label: string
  image: string
  driver: DbDriver
  flavor: DbFlavor
  /** Newest first; a closed list. */
  versions: { version: string; image: string }[]
  defaultVersion: string
  port: number
  /** Empty: the engine has no accounts and `user` must not be sent. */
  defaultUser: string
  /** False: there is no initial database to name. */
  database: boolean
}

export type DbProvisionRequest = {
  engine: string
  name?: string
  database?: string
  exposure?: "local" | "public"
  version?: string
  user?: string
  password?: string
}

export type DbProvisionResponse = {
  container: string
  engine: string
  driver: DbDriver
  flavor: DbFlavor
  version: string
  image: string
  host: string
  port: number
  user: string
  database: string
  exposure: "local" | "public"
  firewall: string
  firewallError?: string
}

export type DbCreateRequest = {
  name: string
  driver: DbDriver
  dsn: string
  /** Dial before saving; a server that does not answer is not saved. */
  probe?: boolean
  environment?: string
  readOnly?: boolean
  notes?: string
}

export type DbTestResponse =
  | { ok: true; version: string; versionNumber: string; flavor: DbFlavor; flavorLabel: string }
  | { ok: false; error: string }

export type DbHostGrantRequest = {
  driver: DbDriver
  host: string
  port: number
  user: string
  password: string
  database: string
  name: string
  superuser: boolean
}
