import type { SectionParams, SelectionKey } from "@/components/database/engine"

/**
 * The reader's place inside one database, as it is remembered between its
 * pages: the schema and the table, or the database and the collection.
 *
 * The address is where a place lives, and a page's address holds only what
 * that page can use — Settings has no table. So a round trip through a page
 * that cannot hold the table used to lose it: Data, Settings, Data again
 * opened on nothing. What the last address said is therefore kept for the
 * tab, per database, and a link to a page that can use it carries it back.
 *
 * Everything here is the arithmetic of that memory, with no React in it: what
 * an address says, what is remembered after it has spoken, and what a bare
 * address is completed with.
 */
export type Place = Partial<Record<SelectionKey, string>>

/** Every key a link ever carries. A Redis key and a handed-over statement are not among them. */
export const PLACE_KEYS: readonly SelectionKey[] = ["schema", "table", "db", "collection"]

/** What each key is inside of: a table belongs to its schema, a collection to its database. */
const WITHIN: Partial<Record<SelectionKey, SelectionKey>> = { table: "schema", collection: "db" }

type Query = { get: (name: string) => string | null }

/**
 * What an address says about the place, for a page that can hold `carries`.
 *
 * An address that names any of them is the whole truth about all of them:
 * `?schema=public` on Data means no table is open. One that names none says
 * nothing — it is a bare link, not a statement that nothing is selected.
 */
export function saidPlace(query: Query, carries: readonly SelectionKey[]): Place | null {
  if (!carries.some((key) => query.get(key))) return null
  return Object.fromEntries(carries.map((key) => [key, query.get(key) ?? ""]))
}

/** The keys of a write that are part of the place, with a cleared one as `""`. */
export function namedPlace(params: SectionParams): Place {
  return Object.fromEntries(
    PLACE_KEYS.filter((key) => key in params).map((key) => [key, params[key] ?? ""]),
  )
}

/**
 * The memory after `said` has spoken. A key it names takes its value, and an
 * empty one is forgotten. A table is remembered only inside its schema: when
 * the schema changes and nothing names a table, the old one is not carried
 * into a schema it was never in.
 *
 * Hands back `held` itself when nothing changed, so a caller can tell by
 * identity whether there is anything to write.
 */
export function rememberedPlace(held: Place, said: Place): Place {
  const next: Place = { ...held }
  for (const [key, value] of Object.entries(said) as [SelectionKey, string][]) {
    if (value) next[key] = value
    else delete next[key]
  }
  for (const [inner, outer] of Object.entries(WITHIN) as [SelectionKey, SelectionKey][]) {
    if (outer in said && !(inner in said) && (said[outer] ?? "") !== (held[outer] ?? "")) {
      delete next[inner]
    }
  }
  const keys = Object.keys(next) as SelectionKey[]
  const same = keys.length === Object.keys(held).length && keys.every((k) => next[k] === held[k])
  return same ? held : next
}

/**
 * A bare address completed with the remembered place, or `null` when there is
 * nothing to add: the page holds none of it, the address already speaks for
 * itself, or nothing is remembered.
 */
export function restoredQuery(
  query: string,
  carries: readonly SelectionKey[],
  held: Place,
): string | null {
  const params = new URLSearchParams(query)
  if (saidPlace(params, carries)) return null
  const known = carries.filter((key) => held[key])
  if (known.length === 0) return null
  for (const key of known) params.set(key, held[key]!)
  return params.toString()
}

/** A query string with a write applied: a named key is set, and an empty one removed. */
export function writtenQuery(query: string, params: SectionParams): string {
  const next = new URLSearchParams(query)
  for (const [key, value] of Object.entries(params)) {
    if (value) next.set(key, value)
    else next.delete(key)
  }
  return next.toString()
}
