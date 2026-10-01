import { SECTION_IDS, sectionHref, type SectionId } from "@/components/database/engine"

/**
 * The section's addresses that are not one database's page, and the reading
 * of an address back into the place it names. A database's own pages are
 * built by `sectionHref` in the engine registry.
 */

export const DATABASES_HREF = "/databases"
export const DATABASES_MAP_HREF = "/databases/map"
export const NEW_DATABASE_HREF = "/databases/new"

/**
 * How Add a database opens: on starting one in a container, on connecting one
 * that already runs somewhere, or on the servers found on this machine.
 */
export type AddMode = "start" | "connect" | "found"

/**
 * The address of Add a database. `key` names one found server (an inventory
 * key) for the flow to open on.
 */
export function newDatabaseHref(options?: { mode?: AddMode; key?: string }): string {
  const query = new URLSearchParams()
  if (options?.mode) query.set("mode", options.mode)
  if (options?.key) query.set("key", options.key)
  const text = query.toString()
  return text ? `${NEW_DATABASE_HREF}?${text}` : NEW_DATABASE_HREF
}

/**
 * A connection's id as an address spells it: digits with no leading zero.
 * The layout, the rail and the palette all read it with this, so `/databases/007`
 * is not database 7 to one of them and an unknown address to another.
 */
export const DATABASE_ID = /^[1-9]\d*$/

/** The database a path is inside, or `null` — whatever page of it that is. */
export function databaseIdFrom(pathname: string): number | null {
  const match = /^\/databases\/([^/]+)(?:\/.*)?$/.exec(pathname)
  return match && DATABASE_ID.test(match[1]) ? Number(match[1]) : null
}

/** The database and the page of it a path names, or `null` when it names neither. */
export function databasePlace(pathname: string): { id: number; section: SectionId } | null {
  const match = /^\/databases\/([^/]+)(?:\/([a-z]+))?\/?$/.exec(pathname)
  if (!match || !DATABASE_ID.test(match[1])) return null
  const section = (match[2] ?? "home") as SectionId
  // `home` is the bare path; spelled out it is no page.
  if (match[2] === "home" || !SECTION_IDS.includes(section)) return null
  return { id: Number(match[1]), section }
}

/**
 * The pages the section had while the connection was a query parameter, and
 * where each one's work went.
 */
const LEGACY_PAGES: Record<string, SectionId> = {
  overview: "home",
  browse: "data",
  structure: "schema",
  diagram: "diagram",
  query: "query",
  find: "search",
  monitor: "performance",
  advisor: "advisor",
  server: "access",
  backups: "backups",
  logs: "logs",
  generate: "generate",
  connection: "settings",
}

/** What an old address said beyond the connection, and still means on the new page. */
const LEGACY_PARAMS = ["schema", "table", "sql", "source", "view"]

/**
 * Where an address from before the connection moved into the path goes now,
 * or `null` when it is not one of those.
 *
 * They are still out there: a board stores the link of every database card
 * drawn on it, a backend a version behind still emits them, and a bookmark
 * outlives every release. `/databases/browse?conn=3&schema=public&table=orders`
 * is `/databases/3/data?schema=public&table=orders`. An old page with no
 * connection named opened whichever database the tab remembered; with nothing
 * to say which, it opens the control center rather than a guess.
 */
export function legacyDatabasesHref(
  pathname: string,
  searchParams: { get: (name: string) => string | null },
): string | null {
  const match = /^\/databases\/([a-z]+)\/?$/.exec(pathname)
  if (!match) return null
  if (match[1] === "topology") return DATABASES_MAP_HREF
  // Its own entry only: `/databases/constructor` is not an old page.
  if (!Object.hasOwn(LEGACY_PAGES, match[1])) return null
  const section = LEGACY_PAGES[match[1]]
  const conn = searchParams.get("conn") ?? ""
  if (!DATABASE_ID.test(conn)) return DATABASES_HREF
  return sectionHref(
    conn,
    section,
    Object.fromEntries(LEGACY_PARAMS.map((name) => [name, searchParams.get(name)])),
  )
}
