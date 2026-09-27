import type { LogFields, LogFilterState } from "@/components/logs/types"
import type { Tone } from "@/components/tone"
import type { LogLevel } from "@/lib/log-filter"

/**
 * What the dashboard knows about each kind of log, for drawing it.
 *
 * The reading is the server's (`backend/internal/logsx/lens*.go`): it names
 * what a line records — `auth_failed`, `slow`, `ban` — and the values in it,
 * before any filter looks at the line, so the live tail, the history, the
 * facets and the export agree. This is the other half: what those names are
 * called on screen, which of them matter enough to colour, the questions a
 * reader of that log asks first (quick views), the figures worth a tile
 * (readings) and the rankings worth a table (groups).
 *
 * Pure data, importing types only, so every surface — the console, the
 * facet popover, a service page's readings — reads one registry and a test
 * can hold it against the server's vocabulary
 * (`testdata/lenses.golden.json`): an event the parser emits cannot ship
 * without a word for it.
 */

export type LensEvent = {
  /** One to three words, lower case, in the product's voice: "auth failed". */
  label: string
  /** The word's colour in the level column. Status hues only for verdicts. */
  tone?: Tone
  /**
   * False for the high-volume neutral events — a request, a connection, a
   * cron run — where the word on every line is a column that says nothing;
   * those lines keep the level word.
   */
  mark?: false
  /** Drawn as a rule across the pane: a unit started, an app came up. */
  divider?: boolean
}

export type LensView = {
  id: string
  label: string
  fields?: LogFields
  levels?: LogLevel[]
  q?: string
  /** The server setting this view needs, named in its empty state. */
  requires?: string
}

export type LensReading = {
  id: string
  label: string
  fields?: LogFields
  levels?: LogLevel[]
  /** Count distinct values of this key rather than lines: "attacking addresses". */
  distinct?: string
  figure?: "count" | "per_minute"
  /** Applied only when the figure is above zero. */
  tone?: Tone
  hint: string
  requires?: string
}

export type LensGroup = {
  id: string
  label: string
  /** The key the matches are ranked by. */
  by: string
  fields?: LogFields
  levels?: LogLevel[]
  /** Keys whose last value is shown beside each row: a query beside its shape. */
  sample?: string[]
  measure?: string
}

export type LogLens = {
  id: string
  label: string
  /** A `ProductLogo` key, only where the log is a product's own. */
  product?: string
  /** Lenses whose lines this one hands on: syslog's auth and cron lines, nginx's two logs. */
  includes?: string[]
  /** The keys the Fields popover and Insights rank by. */
  facets: string[]
  views: LensView[]
  events: Record<string, LensEvent>
  readings?: LensReading[]
  readingsWindow?: "1h" | "24h" | "7d"
  groups?: LensGroup[]
  measure?: { key: string; label: string; format: "ms" | "bytes" | "count" }
  /** At most three, and only for values buried in the text. */
  columns?: string[]
  /** Applied on first open, as chips the reader can see and remove. */
  defaults?: { label: string; fields?: LogFields; levels?: LogLevel[] }
}

const ERRORS: LogLevel[] = ["critical", "error"]
const WARNINGS: LogLevel[] = ["critical", "error", "warn"]

const event = (...ids: string[]): LogFields => ({ event: ids })

const errorsView: LensView = { id: "errors", label: "Errors", levels: ERRORS }
const errorsReading: LensReading = {
  id: "errors",
  label: "Errors",
  levels: ERRORS,
  tone: "danger",
  hint: "Lines at error level or worse",
}
const restartsReading: LensReading = {
  id: "restarts",
  label: "Restarts",
  fields: event("startup"),
  hint: "Times the server started",
}

const POSTGRES: LogLens = {
  id: "postgres",
  label: "Postgres",
  product: "postgresql",
  facets: ["event", "level", "user", "db", "client", "app", "code", "fp"],
  views: [
    errorsView,
    {
      id: "slow",
      label: "Slow",
      fields: event("slow"),
      requires: "log_min_duration_statement",
    },
    { id: "auth", label: "Auth", fields: event("auth_failed") },
    { id: "locks", label: "Locks", fields: event("deadlock", "lock_wait") },
    {
      id: "connections",
      label: "Connections",
      fields: event("connection", "authorized", "disconnection", "too_many_clients"),
      requires: "log_connections",
    },
    {
      id: "maintenance",
      label: "Maintenance",
      fields: event("checkpoint", "autovacuum", "autoanalyze", "temp_file"),
    },
    {
      id: "lifecycle",
      label: "Lifecycle",
      fields: event("startup", "ready", "shutdown", "crash", "oom", "disk_full", "config"),
    },
  ],
  events: {
    startup: { label: "startup" },
    ready: { label: "ready", tone: "success" },
    shutdown: { label: "shutdown" },
    crash: { label: "crash", tone: "danger" },
    oom: { label: "out of memory", tone: "danger" },
    disk_full: { label: "disk full", tone: "danger" },
    checkpoint: { label: "checkpoint" },
    autovacuum: { label: "autovacuum" },
    autoanalyze: { label: "autoanalyze" },
    connection: { label: "connection", mark: false },
    authorized: { label: "authorized", mark: false },
    disconnection: { label: "disconnection", mark: false },
    auth_failed: { label: "auth failed", tone: "danger" },
    too_many_clients: { label: "too many clients", tone: "danger" },
    deadlock: { label: "deadlock", tone: "danger" },
    lock_wait: { label: "lock wait", tone: "warning" },
    slow: { label: "slow", tone: "warning" },
    duration: { label: "duration", mark: false },
    statement: { label: "statement", mark: false },
    temp_file: { label: "temp file" },
    cancel: { label: "cancelled", tone: "warning" },
    terminated: { label: "terminated", tone: "warning" },
    replication: { label: "replication" },
    archive_failed: { label: "archive failed", tone: "danger" },
    config: { label: "config" },
    error: { label: "error", tone: "danger" },
    fatal: { label: "fatal", tone: "danger" },
  },
  readings: [
    errorsReading,
    {
      id: "slow",
      label: "Slow statements",
      fields: event("slow"),
      tone: "warning",
      hint: "Statements past the logging threshold",
      requires: "log_min_duration_statement",
    },
    {
      id: "auth",
      label: "Auth failures",
      fields: event("auth_failed"),
      tone: "danger",
      hint: "Failed password, peer and ident checks",
    },
    {
      id: "locks",
      label: "Deadlocks & lock waits",
      fields: event("deadlock", "lock_wait"),
      tone: "warning",
      hint: "Transactions that waited on or broke a lock",
    },
    restartsReading,
  ],
  groups: [
    {
      id: "slow",
      label: "Slow statements",
      by: "fp",
      fields: event("slow"),
      sample: ["query"],
      measure: "duration_ms",
    },
    { id: "errors", label: "Errors by code", by: "code", levels: ERRORS, sample: ["query"] },
    {
      id: "auth",
      label: "Auth failures by client",
      by: "client",
      fields: event("auth_failed"),
      sample: ["user"],
    },
  ],
  measure: { key: "duration_ms", label: "Statement time", format: "ms" },
  columns: ["duration_ms", "user", "db"],
}

const MYSQL: LogLens = {
  id: "mysql",
  label: "MySQL",
  product: "mysql",
  facets: ["event", "level", "user", "client", "db", "code", "component"],
  views: [
    errorsView,
    { id: "auth", label: "Auth", fields: event("auth_failed") },
    {
      id: "aborted",
      label: "Aborted",
      fields: event("aborted_connection", "too_many_connections"),
    },
    { id: "deadlocks", label: "Deadlocks", fields: event("deadlock") },
    {
      id: "lifecycle",
      label: "Lifecycle",
      fields: event("startup", "ready", "shutdown", "crash"),
    },
  ],
  events: {
    startup: { label: "startup" },
    ready: { label: "ready", tone: "success" },
    shutdown: { label: "shutdown" },
    crash: { label: "crash", tone: "danger" },
    innodb: { label: "innodb" },
    deadlock: { label: "deadlock", tone: "danger" },
    aborted_connection: { label: "aborted", tone: "warning" },
    too_many_connections: { label: "too many connections", tone: "danger" },
    auth_failed: { label: "access denied", tone: "danger" },
    replication: { label: "replication" },
    config_warning: { label: "config warning", tone: "warning" },
    error: { label: "error", tone: "danger" },
  },
  readings: [
    errorsReading,
    {
      id: "auth",
      label: "Access denied",
      fields: event("auth_failed"),
      tone: "danger",
      hint: "Logins the server refused",
    },
    {
      id: "aborted",
      label: "Aborted connections",
      fields: event("aborted_connection"),
      tone: "warning",
      hint: "Clients that went away without closing",
    },
    {
      id: "deadlocks",
      label: "Deadlocks",
      fields: event("deadlock"),
      tone: "danger",
      hint: "Transactions InnoDB rolled back to break a deadlock",
    },
    restartsReading,
  ],
  groups: [
    { id: "errors", label: "Errors by code", by: "code", levels: ERRORS, sample: ["component"] },
    {
      id: "auth",
      label: "Access denied by client",
      by: "client",
      fields: event("auth_failed"),
      sample: ["user"],
    },
  ],
  columns: ["user", "client"],
}

const REDIS: LogLens = {
  id: "redis",
  label: "Redis",
  product: "redis",
  facets: ["event", "level", "role"],
  views: [
    { id: "warnings", label: "Warnings", levels: WARNINGS },
    {
      id: "persistence",
      label: "Persistence",
      fields: event("saved", "bgsave", "aof", "persistence_failed"),
    },
    { id: "replication", label: "Replication", fields: event("replication") },
    { id: "memory", label: "Memory", fields: event("memory_warning") },
    {
      id: "lifecycle",
      label: "Lifecycle",
      fields: event("startup", "ready", "shutdown", "loading", "crash"),
    },
  ],
  events: {
    startup: { label: "startup" },
    ready: { label: "ready", tone: "success" },
    shutdown: { label: "shutdown" },
    loading: { label: "loading" },
    saved: { label: "saved", tone: "success" },
    bgsave: { label: "bgsave" },
    aof: { label: "aof" },
    persistence_failed: { label: "save failed", tone: "danger" },
    memory_warning: { label: "memory warning", tone: "warning" },
    replication: { label: "replication" },
    security_attack: { label: "attack", tone: "danger" },
    client_closed: { label: "client closed", tone: "warning" },
    crash: { label: "crash", tone: "danger" },
  },
  readings: [
    {
      id: "warnings",
      label: "Warnings",
      levels: WARNINGS,
      tone: "warning",
      hint: "Lines Redis marked # or worse",
    },
    {
      id: "persistence",
      label: "Persistence failures",
      fields: event("persistence_failed"),
      tone: "danger",
      hint: "Snapshots and AOF writes that failed",
    },
    {
      id: "attacks",
      label: "Security attacks",
      fields: event("security_attack"),
      tone: "danger",
      hint: "HTTP or cross-protocol requests sent to the Redis port",
    },
    restartsReading,
  ],
  columns: ["role"],
}

const MONGODB: LogLens = {
  id: "mongodb",
  label: "MongoDB",
  product: "mongodb",
  facets: ["event", "level", "component", "ns", "plan", "client", "app", "user", "code"],
  views: [
    { id: "slow", label: "Slow", fields: event("slow") },
    errorsView,
    { id: "auth", label: "Auth", fields: event("auth_failed", "authorized") },
    {
      id: "connections",
      label: "Connections",
      fields: event("connection", "disconnection", "client_metadata"),
    },
    { id: "replication", label: "Replication", fields: event("replication") },
    {
      id: "lifecycle",
      label: "Lifecycle",
      fields: event("startup", "ready", "shutdown", "checkpoint", "index"),
    },
  ],
  events: {
    connection: { label: "connection", mark: false },
    disconnection: { label: "disconnection", mark: false },
    client_metadata: { label: "client metadata", mark: false },
    slow: { label: "slow", tone: "warning" },
    authorized: { label: "authorized", mark: false },
    auth_failed: { label: "auth failed", tone: "danger" },
    startup: { label: "startup" },
    ready: { label: "ready", tone: "success" },
    shutdown: { label: "shutdown" },
    checkpoint: { label: "checkpoint" },
    replication: { label: "replication" },
    index: { label: "index" },
  },
  readings: [
    errorsReading,
    {
      id: "slow",
      label: "Slow operations",
      fields: event("slow"),
      tone: "warning",
      hint: "Operations past slowms (100 ms unless set)",
    },
    {
      id: "auth",
      label: "Auth failures",
      fields: event("auth_failed"),
      tone: "danger",
      hint: "Authentication the server refused",
    },
    {
      id: "connections",
      label: "Connections",
      fields: event("connection"),
      hint: "Connections accepted",
    },
    restartsReading,
  ],
  groups: [
    {
      id: "slow",
      label: "Slow operations by namespace",
      by: "ns",
      fields: event("slow"),
      sample: ["plan", "query"],
      measure: "duration_ms",
    },
    {
      id: "connections",
      label: "Connections by client",
      by: "client",
      fields: event("connection"),
      sample: ["app"],
    },
  ],
  measure: { key: "duration_ms", label: "Operation time", format: "ms" },
  columns: ["duration_ms", "ns", "plan"],
  defaults: {
    label: "Connection noise hidden",
    fields: event("!connection", "!disconnection", "!client_metadata"),
  },
}

const CLICKHOUSE: LogLens = {
  id: "clickhouse",
  label: "ClickHouse",
  product: "clickhouse",
  facets: ["event", "level", "component", "code"],
  views: [
    { id: "exceptions", label: "Exceptions", fields: event("exception") },
    { id: "queries", label: "Queries", fields: event("query") },
    { id: "merges", label: "Merges", fields: event("merge", "too_many_parts") },
    { id: "auth", label: "Auth", fields: event("auth_failed") },
  ],
  events: {
    startup: { label: "startup" },
    ready: { label: "ready", tone: "success" },
    shutdown: { label: "shutdown" },
    query: { label: "query", mark: false },
    exception: { label: "exception", tone: "danger" },
    merge: { label: "merge" },
    memory_limit: { label: "memory limit", tone: "danger" },
    too_many_parts: { label: "too many parts", tone: "danger" },
    auth_failed: { label: "auth failed", tone: "danger" },
  },
  readings: [
    {
      id: "exceptions",
      label: "Exceptions",
      fields: event("exception"),
      tone: "danger",
      hint: "Queries and background jobs that threw",
    },
    {
      id: "memory",
      label: "Memory limit",
      fields: event("memory_limit"),
      tone: "danger",
      hint: "Queries stopped at the memory limit",
    },
    {
      id: "parts",
      label: "Too many parts",
      fields: event("too_many_parts"),
      tone: "danger",
      hint: "Inserts refused while merges fell behind",
    },
    {
      id: "auth",
      label: "Auth failures",
      fields: event("auth_failed"),
      tone: "danger",
      hint: "Logins the server refused",
    },
    restartsReading,
  ],
  groups: [
    {
      id: "exceptions",
      label: "Exceptions by code",
      by: "code",
      fields: event("exception"),
      sample: ["component"],
    },
  ],
  measure: { key: "duration_ms", label: "Query time", format: "ms" },
  columns: ["duration_ms", "query_id"],
  defaults: { label: "Info and above", levels: ["critical", "error", "warn", "info"] },
}

const MSSQL: LogLens = {
  id: "mssql",
  label: "SQL Server",
  product: "sqlserver",
  facets: ["event", "level", "component", "client", "code"],
  views: [
    errorsView,
    { id: "auth", label: "Logins", fields: event("auth_failed") },
    { id: "storage", label: "Storage", fields: event("log_full", "io_slow") },
    { id: "lifecycle", label: "Lifecycle", fields: event("startup", "ready", "recovery") },
  ],
  events: {
    startup: { label: "startup" },
    ready: { label: "ready", tone: "success" },
    recovery: { label: "recovery" },
    backup: { label: "backup" },
    log_full: { label: "log full", tone: "danger" },
    io_slow: { label: "slow i/o", tone: "warning" },
    auth_failed: { label: "login failed", tone: "danger" },
    error: { label: "error", tone: "danger" },
  },
  readings: [
    errorsReading,
    {
      id: "auth",
      label: "Failed logins",
      fields: event("auth_failed"),
      tone: "warning",
      hint: "Logins the server refused",
    },
    restartsReading,
  ],
  groups: [
    {
      id: "auth",
      label: "Failed logins by client",
      by: "client",
      fields: event("auth_failed"),
    },
  ],
}

// Access logs, whoever writes them: nginx's combined format, Apache's, Caddy's JSON.
const requestViews: LensView[] = [
  { id: "5xx", label: "5xx", fields: { class: ["5xx"] } },
  { id: "4xx", label: "4xx", fields: { class: ["4xx"] } },
  { id: "slow", label: "Slow", fields: { duration_ms: [">1000"] } },
  { id: "bots", label: "Bots", fields: { bot: ["1"] } },
  { id: "probes", label: "Probes", fields: event("probe") },
]

const requestReadings: LensReading[] = [
  {
    id: "rate",
    label: "Requests/min",
    fields: { class: ["*"] },
    figure: "per_minute",
    hint: "Every request answered",
  },
  {
    id: "4xx",
    label: "4xx",
    fields: { class: ["4xx"] },
    tone: "warning",
    hint: "Refused: not found, not allowed",
  },
  {
    id: "5xx",
    label: "5xx",
    fields: { class: ["5xx"] },
    tone: "danger",
    hint: "The server failed to answer",
  },
  {
    id: "probes",
    label: "Probes",
    fields: event("probe"),
    hint: "Scanners asking for files this server does not have",
  },
]

const requestGroups: LensGroup[] = [
  {
    id: "5xx",
    label: "5xx by path",
    by: "path",
    fields: { class: ["5xx"] },
    sample: ["status"],
    measure: "duration_ms",
  },
  {
    id: "404",
    label: "404s by path",
    by: "path",
    fields: { status: ["404"], event: ["!probe"] },
    sample: ["referer"],
  },
  { id: "clients", label: "Clients", by: "client", sample: ["agent"] },
  { id: "agents", label: "Agents", by: "agent" },
]

const requestEvents: Record<string, LensEvent> = {
  request: { label: "request", mark: false },
  client_error: { label: "client error", tone: "warning" },
  server_error: { label: "server error", tone: "danger" },
  probe: { label: "probe" },
}

const requestMeasure: LogLens["measure"] = {
  key: "duration_ms",
  label: "Response time",
  format: "ms",
}

const HTTP_ACCESS: LogLens = {
  id: "http-access",
  label: "Access log",
  facets: ["event", "class", "status", "method", "path", "client", "host", "bot"],
  views: requestViews,
  events: requestEvents,
  readings: requestReadings,
  groups: requestGroups,
  measure: requestMeasure,
}

const upstreamEvents = [
  "upstream_refused",
  "upstream_timeout",
  "upstream_closed",
  "no_live_upstreams",
]

const upstreamView: LensView = {
  id: "upstream",
  label: "Upstream",
  fields: event(...upstreamEvents),
}

const NGINX_ERROR: LogLens = {
  id: "nginx-error",
  label: "nginx error log",
  product: "nginx",
  facets: ["event", "level", "client", "host", "upstream"],
  views: [
    upstreamView,
    { id: "rate-limited", label: "Rate limited", fields: event("rate_limited") },
    { id: "tls", label: "TLS", fields: event("ssl_error") },
    { id: "config", label: "Config", fields: event("config", "startup", "reload") },
  ],
  events: {
    upstream_refused: { label: "upstream refused", tone: "danger" },
    upstream_timeout: { label: "upstream timeout", tone: "danger" },
    upstream_closed: { label: "upstream closed", tone: "danger" },
    no_live_upstreams: { label: "no live upstreams", tone: "danger" },
    ssl_error: { label: "tls error", tone: "warning" },
    body_too_large: { label: "body too large", tone: "warning" },
    rate_limited: { label: "rate limited", tone: "warning" },
    not_found: { label: "not found" },
    forbidden: { label: "forbidden", tone: "warning" },
    config: { label: "config" },
    startup: { label: "startup" },
    reload: { label: "reload" },
    error: { label: "error", tone: "danger" },
  },
  readings: [
    {
      id: "upstream",
      label: "Upstream failures",
      fields: event(...upstreamEvents),
      tone: "danger",
      hint: "The app behind nginx refused, timed out or closed",
    },
    {
      id: "rate-limited",
      label: "Rate limited",
      fields: event("rate_limited"),
      tone: "warning",
      hint: "Requests limit_req or limit_conn turned away",
    },
    {
      id: "tls",
      label: "TLS errors",
      fields: event("ssl_error"),
      tone: "warning",
      hint: "Handshakes that failed",
    },
    errorsReading,
  ],
  groups: [
    {
      id: "upstream",
      label: "Upstream failures by upstream",
      by: "upstream",
      fields: event(...upstreamEvents),
      sample: ["host"],
    },
    { id: "hosts", label: "Errors by host", by: "host", levels: ERRORS, sample: ["path"] },
  ],
  columns: ["client", "upstream"],
}

const NGINX: LogLens = {
  id: "nginx",
  label: "nginx",
  product: "nginx",
  includes: ["http-access", "nginx-error"],
  facets: ["event", "level", "class", "status", "method", "path", "client", "host", "upstream"],
  views: [...requestViews.slice(0, 3), upstreamView, ...requestViews.slice(3)],
  events: {},
  readings: requestReadings,
  groups: [requestGroups[0], NGINX_ERROR.groups![0], requestGroups[2], requestGroups[3]],
  measure: requestMeasure,
}

const CADDY: LogLens = {
  id: "caddy",
  label: "Caddy",
  product: "caddy",
  includes: ["http-access"],
  facets: ["event", "level", "class", "status", "method", "path", "client", "host", "logger"],
  views: [
    ...requestViews.slice(0, 3),
    { id: "upstream", label: "Upstream", fields: event("upstream_refused", "upstream_timeout") },
    {
      id: "certificates",
      label: "Certificates",
      fields: event("cert_obtained", "cert_renewed", "cert_failed"),
    },
    ...requestViews.slice(3),
  ],
  events: {
    upstream_refused: { label: "upstream refused", tone: "danger" },
    upstream_timeout: { label: "upstream timeout", tone: "danger" },
    error: { label: "error", tone: "danger" },
    cert_obtained: { label: "cert obtained", tone: "success" },
    cert_renewed: { label: "cert renewed", tone: "success" },
    cert_failed: { label: "cert failed", tone: "danger" },
    startup: { label: "startup" },
    config: { label: "config" },
  },
  readings: requestReadings,
  groups: requestGroups,
  measure: requestMeasure,
}

const sshFailures = ["ssh_failed", "ssh_invalid_user", "ssh_max_attempts"]

const AUTH: LogLens = {
  id: "auth",
  label: "Auth log",
  facets: ["event", "user", "client", "method", "program"],
  views: [
    { id: "accepted", label: "Accepted", fields: event("ssh_accepted") },
    { id: "failed", label: "Failed", fields: event(...sshFailures) },
    { id: "sudo", label: "sudo", fields: event("sudo", "sudo_failed") },
    { id: "accounts", label: "Accounts", fields: event("account", "su", "login") },
    { id: "scanners", label: "Scanners", fields: event("ssh_scan", "ssh_preauth_closed") },
  ],
  // An attempt against a public SSH port is routine, and its level stays
  // info: the word's tone is what says so, not an amber page.
  events: {
    ssh_accepted: { label: "accepted", tone: "success" },
    ssh_failed: { label: "failed", tone: "warning" },
    ssh_invalid_user: { label: "invalid user", tone: "warning" },
    ssh_preauth_closed: { label: "preauth closed" },
    ssh_disconnected: { label: "disconnected" },
    ssh_max_attempts: { label: "too many attempts", tone: "warning" },
    ssh_scan: { label: "scan" },
    session_opened: { label: "session opened", mark: false },
    session_closed: { label: "session closed", mark: false },
    sudo: { label: "sudo" },
    sudo_failed: { label: "sudo failed", tone: "danger" },
    su: { label: "su" },
    account: { label: "account" },
    login: { label: "login" },
    cron_session: { label: "cron session", mark: false },
  },
  readings: [
    {
      id: "accepted",
      label: "Accepted logins",
      fields: event("ssh_accepted"),
      hint: "SSH sessions that signed in",
    },
    // Untoned for the reason the events' levels stay info: a public SSH port
    // is tried all day, and the page that shows it must not read as alarmed.
    {
      id: "failed",
      label: "Failed attempts",
      fields: event(...sshFailures),
      hint: "Wrong passwords and users",
    },
    {
      id: "invalid",
      label: "Invalid users",
      fields: event("ssh_invalid_user"),
      hint: "Names with no account here",
    },
    {
      id: "attackers",
      label: "Attackers",
      fields: event(...sshFailures),
      distinct: "client",
      hint: "Distinct failing addresses",
    },
    {
      id: "sudo-failed",
      label: "sudo failures",
      fields: event("sudo_failed"),
      tone: "danger",
      hint: "Wrong passwords and refused commands under sudo",
    },
  ],
  readingsWindow: "24h",
  groups: [
    {
      id: "attackers",
      label: "Attackers",
      by: "client",
      fields: event(...sshFailures),
      sample: ["user"],
    },
    { id: "usernames", label: "Usernames tried", by: "user", fields: event("ssh_invalid_user") },
    {
      id: "logins",
      label: "Logins",
      by: "user",
      fields: event("ssh_accepted"),
      sample: ["client"],
    },
    { id: "sudo", label: "sudo commands", by: "command", fields: event("sudo"), sample: ["user"] },
  ],
  columns: ["user", "client", "method"],
  defaults: {
    label: "Scans and cron sessions hidden",
    fields: event("!cron_session", "!ssh_scan"),
  },
}

const FIREWALL: LogLens = {
  id: "firewall",
  label: "Firewall",
  facets: ["event", "client", "dpt", "proto", "iface"],
  views: [
    { id: "blocked", label: "Blocked", fields: event("block") },
    { id: "limit", label: "Limit", fields: event("limit") },
    { id: "allowed", label: "Allowed", fields: event("allow") },
    { id: "audit", label: "Audit", fields: event("audit") },
  ],
  events: {
    block: { label: "blocked" },
    allow: { label: "allowed" },
    audit: { label: "audit" },
    limit: { label: "rate limited", tone: "warning" },
  },
  readings: [
    {
      id: "blocked",
      label: "Blocked",
      fields: event("block"),
      hint: "Packets the firewall dropped",
    },
    {
      id: "sources",
      label: "Sources",
      fields: event("block"),
      distinct: "client",
      hint: "Distinct addresses dropped",
    },
    {
      id: "limited",
      label: "Rate-limited",
      fields: event("limit"),
      tone: "warning",
      hint: "Connections a limit rule turned away",
    },
  ],
  readingsWindow: "24h",
  groups: [
    { id: "sources", label: "Sources", by: "client", fields: event("block"), sample: ["dpt"] },
    { id: "ports", label: "Ports", by: "dpt", fields: event("block"), sample: ["proto"] },
    { id: "interfaces", label: "Interfaces", by: "iface" },
  ],
  columns: ["client", "dpt", "proto"],
}

const KERNEL: LogLens = {
  id: "kernel",
  label: "Kernel",
  includes: ["firewall"],
  facets: ["event", "level", "program", "dev"],
  views: [
    { id: "oom", label: "OOM", fields: event("oom_kill", "oom") },
    { id: "crashes", label: "Crashes", fields: event("segfault") },
    { id: "storage", label: "Storage", fields: event("io_error", "fs_error", "readonly_fs") },
    { id: "network", label: "Network", fields: event("link_down", "link_up") },
    { id: "apparmor", label: "AppArmor", fields: event("apparmor_denied") },
  ],
  events: {
    oom_kill: { label: "oom kill", tone: "danger" },
    oom: { label: "out of memory", tone: "danger" },
    segfault: { label: "segfault", tone: "danger" },
    io_error: { label: "i/o error", tone: "danger" },
    fs_error: { label: "filesystem error", tone: "danger" },
    readonly_fs: { label: "read-only fs", tone: "danger" },
    link_down: { label: "link down", tone: "warning" },
    link_up: { label: "link up", tone: "success" },
    apparmor_denied: { label: "apparmor denied", tone: "warning" },
    hung_task: { label: "hung task", tone: "warning" },
    mce: { label: "hardware error", tone: "danger" },
  },
  readings: [
    {
      id: "oom",
      label: "OOM kills",
      fields: event("oom_kill", "oom"),
      tone: "danger",
      hint: "Processes the kernel killed for memory",
    },
    {
      id: "segfaults",
      label: "Segfaults",
      fields: event("segfault"),
      tone: "danger",
      hint: "Processes that crashed on a bad address",
    },
    {
      id: "storage",
      label: "Storage errors",
      fields: event("io_error", "fs_error", "readonly_fs"),
      tone: "danger",
      hint: "Disk and filesystem errors",
    },
  ],
}

const FAIL2BAN: LogLens = {
  id: "fail2ban",
  label: "fail2ban",
  product: "fail2ban",
  facets: ["event", "jail", "client"],
  views: [
    { id: "bans", label: "Bans", fields: event("ban", "increase", "restore_ban") },
    { id: "strikes", label: "Strikes", fields: event("found") },
    { id: "unbans", label: "Unbans", fields: event("unban") },
    { id: "jails", label: "Jails", fields: event("jail_started", "jail_stopped") },
    { id: "errors", label: "Errors", fields: event("error") },
  ],
  events: {
    ban: { label: "ban", tone: "warning" },
    unban: { label: "unban" },
    restore_ban: { label: "ban restored" },
    increase: { label: "ban increased", tone: "warning" },
    found: { label: "strike" },
    ignore: { label: "ignored" },
    already_banned: { label: "already banned" },
    jail_started: { label: "jail started" },
    jail_stopped: { label: "jail stopped" },
    error: { label: "error", tone: "danger" },
  },
  readings: [
    { id: "bans", label: "Bans", fields: event("ban"), tone: "warning", hint: "Addresses banned" },
    { id: "strikes", label: "Strikes", fields: event("found"), hint: "Matches a jail counted" },
    {
      id: "unbans",
      label: "Unbans",
      fields: event("unban"),
      hint: "Bans that expired or were lifted",
    },
    {
      id: "errors",
      label: "Errors",
      fields: event("error"),
      tone: "danger",
      hint: "fail2ban's own errors",
    },
  ],
  readingsWindow: "24h",
  groups: [
    { id: "strikes", label: "Strikes by jail", by: "jail", fields: event("found") },
    { id: "bans", label: "Bans by jail", by: "jail", fields: event("ban") },
    {
      id: "offenders",
      label: "Repeat offenders",
      by: "client",
      fields: event("ban"),
      sample: ["jail"],
    },
  ],
  columns: ["jail", "client"],
}

const unitFailures = ["failed", "oom", "start_limit", "core_dumped"]

const SYSTEMD: LogLens = {
  id: "systemd",
  label: "systemd",
  facets: ["event", "level", "unit", "result"],
  views: [
    {
      id: "failures",
      label: "Failures",
      fields: event("failed", "oom", "start_limit", "killed", "core_dumped"),
    },
    {
      id: "lifecycle",
      label: "Lifecycle",
      fields: event(
        "starting",
        "started",
        "stopping",
        "stopped",
        "deactivated",
        "reloading",
        "reloaded",
        "exited",
      ),
    },
  ],
  events: {
    starting: { label: "starting" },
    started: { label: "started", tone: "success", divider: true },
    stopping: { label: "stopping" },
    stopped: { label: "stopped", divider: true },
    deactivated: { label: "deactivated" },
    reloading: { label: "reloading" },
    reloaded: { label: "reloaded" },
    exited: { label: "exited", divider: true },
    killed: { label: "killed", tone: "warning" },
    failed: { label: "failed", tone: "danger", divider: true },
    restart_scheduled: { label: "restart scheduled", tone: "warning" },
    start_limit: { label: "start limit hit", tone: "danger" },
    oom: { label: "oom kill", tone: "danger" },
    resources: { label: "resources" },
    core_dumped: { label: "core dumped", tone: "danger" },
  },
  readings: [
    { id: "starts", label: "Starts", fields: event("started"), hint: "Units that came up" },
    {
      id: "failures",
      label: "Failures",
      fields: event(...unitFailures),
      tone: "danger",
      hint: "Units that failed, crashed or hit their start limit",
    },
    {
      id: "restarts",
      label: "Restarts scheduled",
      fields: event("restart_scheduled"),
      tone: "warning",
      hint: "Restart= brought a unit back after it died",
    },
    {
      id: "oom",
      label: "OOM kills",
      fields: event("oom"),
      tone: "danger",
      hint: "Units killed for their memory",
    },
  ],
}

const startupFailures = ["port_in_use", "env_missing", "db_unreachable", "schema_missing"]

const APP: LogLens = {
  id: "app",
  label: "Application",
  facets: ["event", "level", "stream", "status", "path", "component"],
  views: [
    errorsView,
    { id: "exceptions", label: "Exceptions", fields: event("exception") },
    // Not "Requests": a page that shows this lens beside its own Requests
    // view would offer two buttons of one name.
    { id: "requests", label: "HTTP", fields: event("request") },
    { id: "5xx", label: "5xx", fields: { class: ["5xx"] } },
    {
      id: "startup",
      label: "Startup",
      fields: event("startup", "shutdown", ...startupFailures),
    },
  ],
  events: {
    request: { label: "request", mark: false },
    exception: { label: "exception", tone: "danger" },
    startup: { label: "startup", divider: true },
    shutdown: { label: "shutdown" },
    oom: { label: "out of memory", tone: "danger" },
    port_in_use: { label: "port in use", tone: "danger" },
    db_unreachable: { label: "db unreachable", tone: "danger" },
    schema_missing: { label: "schema missing", tone: "danger" },
    env_missing: { label: "env missing", tone: "warning" },
    deprecation: { label: "deprecation", tone: "warning" },
  },
  readings: [
    {
      id: "exceptions",
      label: "Exceptions",
      fields: event("exception"),
      tone: "danger",
      hint: "Uncaught errors and their stack traces",
    },
    errorsReading,
    {
      id: "oom",
      label: "OOM",
      fields: event("oom"),
      tone: "danger",
      hint: "The runtime ran out of memory",
    },
    {
      id: "startup",
      label: "Startup failures",
      fields: event(...startupFailures),
      tone: "danger",
      hint: "A port taken, a variable missing, a database not there",
    },
  ],
  groups: [
    {
      id: "exceptions",
      label: "Exceptions",
      by: "error",
      fields: event("exception"),
      sample: ["component"],
    },
  ],
  measure: requestMeasure,
  columns: ["status", "duration_ms"],
}

const PM2: LogLens = {
  ...APP,
  id: "pm2",
  label: "PM2",
  product: "pm2",
  includes: ["app"],
  events: {},
}

const PACKAGES: LogLens = {
  id: "packages",
  label: "Packages",
  facets: ["event", "package", "user", "command"],
  views: [
    { id: "installs", label: "Installs", fields: event("install") },
    { id: "upgrades", label: "Upgrades", fields: event("upgrade") },
    { id: "removals", label: "Removals", fields: event("remove", "purge") },
    { id: "errors", label: "Errors", fields: event("error") },
    { id: "unattended", label: "Unattended", fields: event("unattended_run", "unattended_done") },
  ],
  events: {
    transaction_start: { label: "transaction" },
    command: { label: "command" },
    install: { label: "install" },
    upgrade: { label: "upgrade" },
    remove: { label: "remove" },
    purge: { label: "purge" },
    transaction_end: { label: "done" },
    configure: { label: "configure" },
    unattended_run: { label: "unattended run" },
    unattended_done: { label: "unattended done" },
    error: { label: "error", tone: "danger" },
  },
  readings: [
    { id: "upgrades", label: "Upgrades", fields: event("upgrade"), hint: "Packages upgraded" },
    { id: "installs", label: "Installs", fields: event("install"), hint: "Packages installed" },
    {
      id: "removals",
      label: "Removals",
      fields: event("remove", "purge"),
      hint: "Packages removed or purged",
    },
    {
      id: "errors",
      label: "Errors",
      fields: event("error"),
      tone: "danger",
      hint: "Transactions that failed",
    },
  ],
  readingsWindow: "7d",
  groups: [
    {
      id: "packages",
      label: "By package",
      by: "package",
      fields: event("install", "upgrade", "remove", "purge"),
      sample: ["version"],
    },
  ],
}

const CRON: LogLens = {
  id: "cron",
  label: "Cron",
  facets: ["event", "user", "command"],
  views: [
    { id: "runs", label: "Runs", fields: event("run") },
    { id: "discarded", label: "Output discarded", fields: event("output_discarded") },
    { id: "errors", label: "Errors", fields: event("error") },
  ],
  events: {
    run: { label: "run", mark: false },
    output_discarded: { label: "output lost", tone: "warning" },
    session: { label: "session", mark: false },
    error: { label: "error", tone: "danger" },
  },
  readings: [
    { id: "runs", label: "Runs", fields: event("run"), hint: "Jobs cron started" },
    {
      id: "discarded",
      label: "Output discarded",
      fields: event("output_discarded"),
      tone: "warning",
      hint: "Jobs whose output had no mailer to go to",
    },
    {
      id: "errors",
      label: "Errors",
      fields: event("error"),
      tone: "danger",
      hint: "cron's own errors",
    },
  ],
  readingsWindow: "24h",
  groups: [
    { id: "runs", label: "Runs by command", by: "command", fields: event("run"), sample: ["user"] },
  ],
  defaults: { label: "Cron sessions hidden", fields: event("!session") },
}

const certbotFailures = ["renew_failed", "challenge_failed", "rate_limited", "error"]

const CERTBOT: LogLens = {
  id: "certbot",
  label: "certbot",
  product: "lets-encrypt",
  facets: ["event", "domain"],
  views: [
    {
      id: "renewals",
      label: "Renewals",
      fields: event("renewing", "renewed", "obtained", "not_due"),
    },
    { id: "failures", label: "Failures", fields: event(...certbotFailures) },
  ],
  events: {
    renewing: { label: "renewing" },
    not_due: { label: "not due" },
    renewed: { label: "renewed", tone: "success" },
    obtained: { label: "obtained", tone: "success" },
    renew_failed: { label: "renewal failed", tone: "danger" },
    challenge_failed: { label: "challenge failed", tone: "danger" },
    rate_limited: { label: "rate limited", tone: "danger" },
    error: { label: "error", tone: "danger" },
  },
  readings: [
    {
      id: "renewed",
      label: "Renewed",
      fields: event("renewed", "obtained"),
      hint: "Certificates issued or renewed",
    },
    {
      id: "failed",
      label: "Failed",
      fields: event(...certbotFailures),
      tone: "danger",
      hint: "Renewals that did not go through",
    },
    {
      id: "not-due",
      label: "Not due",
      fields: event("not_due"),
      hint: "Checks that found nothing to do",
    },
  ],
  readingsWindow: "7d",
  groups: [{ id: "domains", label: "By domain", by: "domain", sample: ["error"] }],
}

const SYSLOG: LogLens = {
  id: "syslog",
  label: "Syslog",
  includes: [
    "auth",
    "cron",
    "kernel",
    "firewall",
    "systemd",
    "fail2ban",
    "certbot",
    "postgres",
    "mysql",
    "redis",
    "nginx-error",
  ],
  facets: ["event", "level", "program", "unit", "user", "client"],
  // The views of what it hands on that a reader of the whole log asks for
  // first; the rest are one press away in the Fields popover, by program.
  views: [
    errorsView,
    { id: "ssh-failed", label: "SSH failed", fields: event(...sshFailures) },
    { id: "sudo", label: "sudo", fields: event("sudo", "sudo_failed") },
    { id: "unit-failures", label: "Unit failures", fields: event(...unitFailures) },
    { id: "oom", label: "OOM", fields: event("oom_kill", "oom") },
  ],
  events: {},
  readings: [
    errorsReading,
    {
      id: "auth",
      label: "Auth failures",
      fields: event("ssh_failed", "ssh_invalid_user"),
      tone: "warning",
      hint: "SSH attempts that failed",
    },
    {
      id: "oom",
      label: "OOM kills",
      fields: event("oom_kill", "oom"),
      tone: "danger",
      hint: "Processes killed for memory",
    },
  ],
  groups: [{ id: "programs", label: "By program", by: "program", levels: WARNINGS }],
}

/**
 * A stack's containers each have their own lens — the database's, the
 * app's — and the one thing they share is which service spoke. Its readings
 * are the ones that add up across a database and a web server: levels, which
 * every lens sets, how many services the errors came from, and the start-ups
 * every container lens names.
 */
const STACK: LogLens = {
  id: "stack",
  label: "Compose stack",
  // The lenses a container is read through: each line names its own, and
  // these are what the stack's own readings are named in.
  includes: [
    "app",
    "postgres",
    "mysql",
    "redis",
    "mongodb",
    "clickhouse",
    "mssql",
    "nginx",
    "caddy",
  ],
  facets: ["service", "event", "level"],
  views: [errorsView],
  events: {},
  readings: [
    errorsReading,
    {
      id: "failing",
      label: "Services with errors",
      levels: ERRORS,
      distinct: "service",
      tone: "danger",
      hint: "How many of its services logged one",
    },
    {
      id: "warnings",
      label: "Warnings",
      levels: ["warn"],
      tone: "warning",
      hint: "Lines at warning level",
    },
    {
      id: "starts",
      label: "Starts",
      fields: event("startup"),
      hint: "Times a service said it was starting",
    },
  ],
}

const LENSES: Record<string, LogLens> = Object.fromEntries(
  [
    POSTGRES,
    MYSQL,
    REDIS,
    MONGODB,
    CLICKHOUSE,
    MSSQL,
    HTTP_ACCESS,
    NGINX_ERROR,
    NGINX,
    CADDY,
    AUTH,
    FIREWALL,
    KERNEL,
    FAIL2BAN,
    SYSTEMD,
    APP,
    PM2,
    PACKAGES,
    CRON,
    CERTBOT,
    SYSLOG,
    STACK,
  ].map((lens) => [lens.id, lens]),
)

/** The id the server reads a stack through when its containers disagree. */
export const STACK_LENS = STACK.id

/** The lens for an id; nothing for "none", "" or an id this build does not know. */
export function lensFor(id: string | undefined): LogLens | undefined {
  return id ? LENSES[id] : undefined
}

/** Every lens a reader can ask a source to be read through, by name. */
export const LENS_CHOICES: { id: string; label: string }[] = Object.values(LENSES)
  .filter((lens) => lens.id !== STACK.id)
  .map((lens) => ({ id: lens.id, label: lens.label }))
  .sort((a, b) => a.label.localeCompare(b.label))

/**
 * What an event is called and how it is drawn. The line's own lens first —
 * a syslog line the server read as auth says so — then the stream's, then
 * what the stream's lens hands on, then systemd's, because every journal
 * unit's stream carries the manager's lines about it. Nothing for an id no
 * lens here names, and every caller draws the level instead.
 */
export function eventMeta(
  lensId: string | undefined,
  eventId: string | undefined,
  lineLens?: string,
): LensEvent | undefined {
  if (!eventId) return undefined
  const seen = new Set<string>()
  const find = (id: string | undefined): LensEvent | undefined => {
    const lens = lensFor(id)
    if (!lens || seen.has(lens.id)) return undefined
    seen.add(lens.id)
    const own = lens.events[eventId]
    if (own) return own
    for (const included of lens.includes ?? []) {
      const found = find(included)
      if (found) return found
    }
    return undefined
  }
  return find(lineLens) ?? find(lensId) ?? find(SYSTEMD.id)
}

/** An event's word, falling back to the id itself read as words. */
export function eventLabel(lensId: string | undefined, eventId: string, lineLens?: string) {
  return eventMeta(lensId, eventId, lineLens)?.label ?? eventId.replace(/_/g, " ")
}

/**
 * The filter a lens opens on: its defaults' predicates and levels, replacing
 * whatever the last source left, so the noise a lens hides is hidden from the
 * first line and shown as a chip that can be pressed away.
 */
export function withLensDefaults(filter: LogFilterState, lens: LogLens | undefined) {
  if (!lens?.defaults) return filter
  return {
    ...filter,
    fields: lens.defaults.fields ?? {},
    levels: lens.defaults.levels ?? filter.levels,
  }
}
