import { buildDsn } from "@/lib/db-dsn"
import type { DbDriver } from "@/lib/types"

/**
 * The connection string the connect form sends, written from its fields.
 *
 * `lib/db-dsn` renders the address every driver takes; what it has no field
 * for is transport encryption, which each driver spells its own way — a
 * `sslmode` for PostgreSQL, `tls=` for MySQL, a second scheme for Redis. The
 * form asks one question (off, required, verified) and this file is where it
 * is turned into each driver's words. It is the same kind of knowledge as the
 * address itself: how a string is written, not what an engine can do.
 */

/** Off, encrypted without checking who answers, or encrypted and checked. */
export type TlsMode = "off" | "on" | "verify"

export type ConnectFields = {
  host: string
  port: string
  user: string
  password: string
  database: string
  tls: TlsMode
  /** MongoDB's `authSource`: the database the account lives in. */
  authSource: string
}

export const EMPTY_CONNECT_FIELDS: ConnectFields = {
  host: "",
  port: "",
  user: "",
  password: "",
  database: "",
  tls: "off",
  authSource: "",
}

type Spelling = { params?: Record<string, string>; scheme?: string }

/**
 * How each driver's string says each mode. A mode a driver has no spelling
 * for is not offered: Redis has no "encrypt without verifying" in a URL, and
 * an Oracle wallet does not fit in one, so its string is pasted whole.
 */
const TLS: Record<DbDriver, Partial<Record<TlsMode, Spelling>>> = {
  postgres: {
    off: { params: { sslmode: "disable" } },
    on: { params: { sslmode: "require" } },
    verify: { params: { sslmode: "verify-full" } },
  },
  mysql: {
    off: {},
    on: { params: { tls: "skip-verify" } },
    verify: { params: { tls: "true" } },
  },
  mongodb: {
    off: {},
    on: { params: { tls: "true", tlsInsecure: "true" } },
    verify: { params: { tls: "true" } },
  },
  redis: { off: {}, verify: { scheme: "rediss" } },
  sqlserver: {
    off: { params: { encrypt: "disable" } },
    on: { params: { encrypt: "true", TrustServerCertificate: "true" } },
    verify: { params: { encrypt: "true" } },
  },
  clickhouse: {
    off: {},
    on: { params: { secure: "true", skip_verify: "true" } },
    verify: { params: { secure: "true" } },
  },
  oracle: {},
  sqlite: {},
}

const ORDER: TlsMode[] = ["off", "on", "verify"]

/** The modes a driver's string can say, in order. Fewer than two: no control. */
export function tlsModes(driver: DbDriver): TlsMode[] {
  return ORDER.filter((mode) => TLS[driver][mode] !== undefined)
}

export const TLS_WORD: Record<TlsMode, string> = {
  off: "Off",
  on: "Required",
  verify: "Verified",
}

/**
 * The one engine-specific knob `lib/db-dsn` writes itself, through its single
 * `option` field: PostgreSQL's sslmode, MongoDB's authSource. Whatever else a
 * mode needs is appended here.
 */
const WRITTEN: Partial<Record<DbDriver, "sslmode" | "authSource">> = {
  postgres: "sslmode",
  mongodb: "authSource",
}

/** The string the server is sent for these fields. */
export function composeDsn(driver: DbDriver, fields: ConnectFields): string {
  const modes = tlsModes(driver)
  const mode = modes.includes(fields.tls) ? fields.tls : (modes[0] ?? "off")
  const spelling = TLS[driver][mode] ?? {}
  const written = WRITTEN[driver]
  const option =
    written === "sslmode"
      ? (spelling.params?.sslmode ?? "")
      : written === "authSource"
        ? fields.authSource
        : ""
  let dsn = buildDsn(driver, { ...fields, option })
  if (spelling.scheme) dsn = dsn.replace(/^[a-z]+:\/\//, `${spelling.scheme}://`)
  const params = Object.entries(spelling.params ?? {})
    .filter(([name]) => name !== written)
    .map(([name, value]) => `${name}=${encodeURIComponent(value)}`)
    .join("&")
  if (!params) return dsn
  return `${dsn}${dsn.includes("?") ? "&" : "?"}${params}`
}

const MASK = "••••••"

/** The same string with the password hidden, for showing what will be saved. */
export function maskedDsn(driver: DbDriver, fields: ConnectFields): string {
  if (!fields.password) return composeDsn(driver, fields)
  return composeDsn(driver, { ...fields, password: MASK }).replace(encodeURIComponent(MASK), MASK)
}

/** A pasted string with whatever stands where a password would hidden. */
export function maskPasted(text: string): string {
  return text.replace(/(:\/\/[^:/?#@\s]*:)[^@\s]*(@)/, `$1${MASK}$2`).replace(
    // MySQL's own form has no scheme, and its password is not escaped, so it
    // may hold an `@` of its own: `user:p@ss@tcp(host)/db`.
    /^([^:/?#@\s]+:).*(@(?:tcp|unix)\()/,
    `$1${MASK}$2`,
  )
}

const SCHEMES: Record<string, DbDriver> = {
  postgres: "postgres",
  postgresql: "postgres",
  mysql: "mysql",
  mariadb: "mysql",
  mongodb: "mongodb",
  "mongodb+srv": "mongodb",
  redis: "redis",
  rediss: "redis",
  sqlserver: "sqlserver",
  mssql: "sqlserver",
  clickhouse: "clickhouse",
  oracle: "oracle",
}

/** The driver a pasted URL's scheme names, when it names one. */
export function driverOfUrl(text: string): DbDriver | undefined {
  const scheme = /^([a-z][a-z0-9+]*):\/\//i.exec(text.trim())?.[1]?.toLowerCase()
  return scheme && Object.hasOwn(SCHEMES, scheme) ? SCHEMES[scheme] : undefined
}

/**
 * A pasted string as the driver takes it. Every driver here takes the URL an
 * application would use except MySQL's, whose Go driver has a form of its own
 * and does not percent-decode it: `mysql://user:p%40ss@host:3306/db?tls=true`
 * becomes `user:p@ss@tcp(host:3306)/db?tls=true`.
 */
export function pastedDsn(driver: DbDriver, text: string): string {
  const raw = text.trim()
  if (driver !== "mysql") return raw
  const match = /^(?:mysql|mariadb):\/\/(?:([^@/]*)@)?([^/?#]+)(?:\/([^?#]*))?(\?[^#]*)?$/i.exec(
    raw,
  )
  if (!match) return raw
  const [, credentials = "", address, database = "", query = ""] = match
  const cut = credentials.indexOf(":")
  const user = safeDecode(cut < 0 ? credentials : credentials.slice(0, cut))
  const password = cut < 0 ? "" : safeDecode(credentials.slice(cut + 1))
  const at = /:\d+$/.test(address) ? address : `${address}:3306`
  return `${user}${cut < 0 ? "" : `:${password}`}@tcp(${at})/${safeDecode(database)}${query}`
}

function safeDecode(text: string): string {
  try {
    return decodeURIComponent(text)
  } catch {
    // A stray `%` is a literal percent sign in somebody's password.
    return text
  }
}
