import { truncateMiddle } from "@/lib/format"
import type { DbConnection } from "@/lib/types"
import type { Engine } from "@/components/database/engine"
import type { DbConnectionSummary } from "@/components/database/shell/types"

/** One thing a connection is, as a word in a line of them. */
export type ConnectionFact = {
  key: string
  text: string
  /** A literal from the connection — an address, a name — rather than a word of ours. */
  mono?: boolean
  /** The whole value, where `text` is a shortened one. */
  title?: string
}

/**
 * What a database is and where, as the strip and the home both say it: the
 * engine and its version, the address it is dialled at, and the database on
 * that server. One function, so the two never disagree about any of them.
 */
export function connectionFacts(
  conn: DbConnection,
  engine: Engine,
  summary?: DbConnectionSummary,
): ConnectionFact[] {
  const facts: ConnectionFact[] = [
    {
      key: "engine",
      text: summary?.versionNumber ? `${engine.label} ${summary.versionNumber}` : engine.label,
    },
  ]
  if (conn.port) facts.push({ key: "address", text: `${conn.host}:${conn.port}`, mono: true })
  if (conn.database) {
    facts.push({
      key: "database",
      // A key–value store's "database" is a number, and a bare 0 says nothing.
      text: engine.kind === "keyvalue" ? `db ${conn.database}` : truncateMiddle(conn.database, 40),
      mono: true,
      title: conn.database,
    })
  }
  return facts
}
