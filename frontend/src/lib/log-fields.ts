/**
 * What each value a lens reads out of a line is, for drawing it.
 *
 * Pure data, like `lib/log-lenses.ts`: the drawing is `components/logs/field-value.tsx`,
 * which reads `kind` to pick the product's existing mark for the value — an
 * address as the network it is on, a status by its family, a duration by its
 * latency tone, a unit as the product it runs — so a client address in a
 * Postgres line and one in a request row are the same drawing.
 *
 * Labels are lower case: they are read inside sentences ("Only lines where
 * user is postgres", "Clear the client filter") far more often than on their
 * own.
 */
export type LogFieldKind =
  | "address"
  | "status"
  | "method"
  | "path"
  | "duration"
  | "bytes"
  | "query"
  | "lane"
  | "product"
  | "code"
  | "text"

export type LogField = {
  label: string
  kind: LogFieldKind
  /**
   * False for a value that is only ever one line's — a statement's text, an
   * error sentence, a user agent — where "only lines like this" is better
   * asked of something that groups them (a query's fingerprint).
   */
  filterable?: false
  /** Which resolver names the product a `product` value is. */
  product?: "unit" | "process" | "jail" | "package" | "image" | "port"
}

export const LOG_FIELDS: Record<string, LogField> = {
  // What every line has, by name rather than read out of it.
  event: { label: "event", kind: "code" },
  level: { label: "level", kind: "code" },
  stream: { label: "stream", kind: "code" },
  source: { label: "source", kind: "lane" },
  pattern: { label: "pattern", kind: "query", filterable: false },

  // Who and where.
  user: { label: "user", kind: "lane" },
  db: { label: "database", kind: "lane" },
  role: { label: "role", kind: "lane" },
  client: { label: "client", kind: "address" },
  port: { label: "port", kind: "text" },
  src: { label: "source address", kind: "address" },
  dst: { label: "destination", kind: "address" },
  spt: { label: "source port", kind: "text" },
  dpt: { label: "port", kind: "product", product: "port" },
  host: { label: "host", kind: "text" },
  domain: { label: "domain", kind: "text" },
  upstream: { label: "upstream", kind: "text" },
  iface: { label: "interface", kind: "text" },
  proto: { label: "protocol", kind: "code" },
  flags: { label: "flags", kind: "code" },
  len: { label: "length", kind: "bytes" },
  conn: { label: "connection", kind: "code" },
  key: { label: "key", kind: "code" },

  // What ran it.
  app: { label: "application", kind: "lane" },
  pid: { label: "pid", kind: "code" },
  thread: { label: "thread", kind: "code" },
  program: { label: "program", kind: "product", product: "process" },
  unit: { label: "unit", kind: "product", product: "unit" },
  service: { label: "service", kind: "lane" },
  container: { label: "container", kind: "code" },
  component: { label: "component", kind: "lane" },
  logger: { label: "logger", kind: "lane" },
  ctx: { label: "context", kind: "code" },
  jail: { label: "jail", kind: "product", product: "jail" },
  invocation: { label: "invocation", kind: "code" },
  message_id: { label: "message id", kind: "code" },
  dev: { label: "device", kind: "code" },

  // A request.
  method: { label: "method", kind: "method" },
  path: { label: "path", kind: "path" },
  status: { label: "status", kind: "status" },
  class: { label: "status class", kind: "code" },
  bytes: { label: "size", kind: "bytes" },
  duration_ms: { label: "duration", kind: "duration" },
  agent: { label: "user agent", kind: "text", filterable: false },
  bot: { label: "bot", kind: "code" },
  referer: { label: "referrer", kind: "text", filterable: false },

  // A statement.
  query: { label: "query", kind: "query", filterable: false },
  fp: { label: "query shape", kind: "code" },
  query_id: { label: "query id", kind: "code" },
  table: { label: "table", kind: "lane" },
  ns: { label: "namespace", kind: "lane" },
  plan: { label: "plan", kind: "code" },
  rows: { label: "rows", kind: "text" },
  rows_examined: { label: "rows examined", kind: "text" },
  rows_sent: { label: "rows sent", kind: "text" },
  docs_examined: { label: "documents examined", kind: "text" },
  keys_examined: { label: "keys examined", kind: "text" },

  // What went wrong.
  code: { label: "code", kind: "code" },
  severity: { label: "severity", kind: "code" },
  error: { label: "error", kind: "text", filterable: false },

  // A checkpoint.
  buffers: { label: "buffers", kind: "text" },
  write_s: { label: "write", kind: "text" },
  sync_s: { label: "sync", kind: "text" },
  total_s: { label: "total", kind: "text" },

  // A unit's run.
  result: { label: "result", kind: "code" },
  exit_code: { label: "exit code", kind: "code" },
  exit_status: { label: "exit status", kind: "code" },
  signal: { label: "signal", kind: "code" },
  restarts: { label: "restarts", kind: "text" },
  cpu: { label: "CPU time", kind: "text" },
  memory: { label: "memory", kind: "bytes" },

  // Accounts and commands.
  action: { label: "action", kind: "code" },
  command: { label: "command", kind: "query" },
  runas: { label: "as user", kind: "lane" },
  tty: { label: "terminal", kind: "code" },

  // Packages.
  package: { label: "package", kind: "product", product: "package" },
  packages: { label: "packages", kind: "text" },
  version: { label: "version", kind: "code" },
  old_version: { label: "previous version", kind: "code" },
}

/** What a key is, and a plain word for one no lens declared (a structured line's own). */
export function fieldOf(key: string): LogField {
  return LOG_FIELDS[key] ?? { label: key, kind: "text" }
}

export function isFilterable(key: string): boolean {
  return fieldOf(key).filterable !== false
}
