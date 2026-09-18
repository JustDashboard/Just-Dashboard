import type { DbDriver } from "@/lib/types"

/**
 * A connection string, assembled from the fields an operator actually knows.
 *
 * Every engine spells its address differently — a URL for most, `user:pass@tcp(host)/db`
 * for MySQL, a bare path for SQLite — and the one form that asked for the
 * whole string was the one place in the product where a typo produced no
 * error, only a connection that refused everything asked of it afterwards.
 * The fields here mirror `dbx.BuildDSN` on the server, which is what renders
 * the same string for a container it detected itself, so a hand-entered server
 * and a detected one arrive as the same shape.
 */
export type DsnFields = {
  host: string
  port: string
  user: string
  password: string
  database: string
  /** Postgres sslmode, MongoDB authSource — the one engine-specific knob each has. */
  option: string
}

export const DEFAULT_PORT: Record<DbDriver, string> = {
  postgres: "5432",
  mysql: "3306",
  sqlite: "",
  sqlserver: "1433",
  clickhouse: "9000",
  oracle: "1521",
  mongodb: "27017",
  redis: "6379",
}

export const EMPTY_FIELDS: DsnFields = {
  host: "127.0.0.1",
  port: "",
  user: "",
  password: "",
  database: "",
  option: "",
}

/** IPv6 literals need brackets before a port can follow them. */
function hostPort(fields: DsnFields, driver: DbDriver) {
  const host = fields.host.trim() || "127.0.0.1"
  const bracketed = host.includes(":") && !host.startsWith("[") ? `[${host}]` : host
  const port = fields.port.trim() || DEFAULT_PORT[driver]
  return port ? `${bracketed}:${port}` : bracketed
}

function credentials(user: string, password: string) {
  const u = user.trim()
  if (!u && !password) return ""
  if (!password) return `${encodeURIComponent(u)}@`
  return `${encodeURIComponent(u)}:${encodeURIComponent(password)}@`
}

export function buildDsn(driver: DbDriver, fields: DsnFields): string {
  const db = fields.database.trim()
  const at = hostPort(fields, driver)
  switch (driver) {
    case "sqlite":
      return fields.database.trim()
    case "postgres":
      return `postgres://${credentials(fields.user, fields.password)}${at}/${db}?sslmode=${fields.option || "disable"}`
    case "mysql":
      // The MySQL driver takes its own format rather than a URL, and does not
      // percent-decode it — the password travels as typed.
      return `${fields.user.trim()}:${fields.password}@tcp(${at})/${db}`
    case "sqlserver":
      return `sqlserver://${credentials(fields.user, fields.password)}${at}?database=${encodeURIComponent(db)}`
    case "clickhouse":
      return `clickhouse://${credentials(fields.user, fields.password)}${at}/${db || "default"}`
    case "oracle":
      return `oracle://${credentials(fields.user, fields.password)}${at}/${db}`
    case "mongodb": {
      const auth = fields.option.trim()
      return `mongodb://${credentials(fields.user, fields.password)}${at}/${db}${auth ? `?authSource=${encodeURIComponent(auth)}` : ""}`
    }
    case "redis":
      return `redis://${credentials(fields.user, fields.password)}${at}/${db.replace(/^db/, "") || "0"}`
  }
}

/** The same string with the password hidden, for showing what will be saved. */
export function maskDsn(driver: DbDriver, fields: DsnFields): string {
  if (!fields.password) return buildDsn(driver, fields)
  return buildDsn(driver, { ...fields, password: "••••••" }).replace(
    encodeURIComponent("••••••"),
    "••••••",
  )
}

/** What each engine calls the thing the `database` field names. */
export function databaseLabel(driver: DbDriver): string {
  switch (driver) {
    case "sqlite":
      return "Database file"
    case "redis":
      return "Database index"
    case "oracle":
      return "Service name"
    case "clickhouse":
      return "Database"
    default:
      return "Database"
  }
}
