/**
 * The server's rules for the names Add a database sends, read as the reader
 * types. The server is still the one that decides; these only mean the form
 * says so before the round trip does, in the server's own terms.
 */

/** A saved connection's name. */
export const CONNECTION_NAME = /^[A-Za-z0-9][A-Za-z0-9 ._-]{0,63}$/

/** The operator's label for what a database is for. */
export const ENVIRONMENT = /^[A-Za-z0-9][A-Za-z0-9 ._-]{0,31}$/

/** A container started from here. */
export const CONTAINER_NAME = /^[A-Za-z0-9][A-Za-z0-9_.-]{0,62}$/

/** The database a new server is created with. */
export const DATABASE_NAME = /^[A-Za-z_][A-Za-z0-9_]{0,62}$/

/** The account a new server is created with. */
export const ACCOUNT_NAME = /^[A-Za-z_][A-Za-z0-9_]{0,31}$/

/** A password typed for a new server: what every engine's bootstrap can carry unquoted. */
export const PROVISION_PASSWORD = /^[A-Za-z0-9._~!*+=,-]{8,128}$/

/** The environments everybody has; any other word can be typed. */
export const ENVIRONMENTS = ["production", "staging", "development", "testing"] as const

/**
 * A name nobody is using yet: the base, then `-2`, `-3`… — what the server
 * does for a name it derives itself, done here for one the form suggests.
 */
export function freeName(base: string, taken: readonly string[]): string {
  const used = new Set(taken.map((name) => name.toLowerCase()))
  const clean = base.replace(/[^A-Za-z0-9 ._-]+/g, "-").replace(/^[^A-Za-z0-9]+/, "") || "database"
  const root = clean.slice(0, 60)
  if (!used.has(root.toLowerCase())) return root
  for (let n = 2; ; n++) {
    const candidate = `${root}-${n}`
    if (!used.has(candidate.toLowerCase())) return candidate
  }
}

/** The name a connection typed in by hand is offered: the database, else the host. */
export function suggestedName(
  fields: { host: string; database: string },
  fallback: string,
  taken: readonly string[],
): string {
  const file = fields.database.split("/").pop() ?? ""
  const base = file.replace(/\.(db|sqlite3?|duckdb)$/i, "") || fields.host.split(".")[0] || fallback
  return freeName(base, taken)
}

/**
 * A password for an account the dashboard makes: thirty characters from the
 * set every engine takes unquoted, drawn with the browser's own generator.
 */
export function generatePassword(length = 30): string {
  const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789"
  const bytes = crypto.getRandomValues(new Uint8Array(length))
  return Array.from(bytes, (byte) => alphabet[byte % alphabet.length]).join("")
}
