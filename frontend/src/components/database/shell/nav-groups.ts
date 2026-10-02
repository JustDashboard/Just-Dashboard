import type { NavScope } from "@/components/nav-scope"
import type { DbCapabilities, DbConnection, DbDriver, DbFlavor } from "@/lib/types"
import { groupSections, type EngineSection, type SectionId } from "@/components/database/engine"

/**
 * One database's pages as the rail's panel: Home alone, then Work, Schema,
 * Insights and Operate, each holding only what the engine has, in the
 * engine's own words.
 *
 * It has two callers, which is why it is here and not in the hook: the
 * database's layout, publishing the panel once the connection is read, and
 * the rail itself, drawing the same panel from the address before that.
 */
export function databaseNavGroups(
  sections: EngineSection[],
  hrefOf: (section: SectionId) => string,
): NavScope["groups"] {
  return groupSections(sections).map((group) => ({
    label: group.label,
    items: group.sections.map((section) => ({
      title: section.title,
      href: hrefOf(section.id),
      icon: section.icon,
    })),
  }))
}

/**
 * A database the rail has drawn before: enough to draw its panel again from
 * the address alone, before the list has been read. A saved row knows only
 * the driver; `flavor` and `capabilities` are what the server said it is and
 * can do the last time it was asked, and they are what decide its pages — so
 * with them the panel drawn early is the one that then registers.
 */
export type KnownDatabase = {
  name: string
  driver: DbDriver
  flavor?: DbFlavor
  capabilities?: DbCapabilities
}

/**
 * Where that memory is kept, by connection id. It is the tab's
 * (`useSessionState`), not the browser's: names of databases are the
 * server's data, and what a tab remembers is dropped on sign-out where the
 * arrangement of the furniture is not.
 */
export const KNOWN_DATABASES_KEY = "databases.known"

/** The entry for an id, and nothing an object answers to by inheritance. */
export function knownDatabase(
  held: Record<string, KnownDatabase>,
  id: number,
): KnownDatabase | undefined {
  return Object.hasOwn(held, id) ? held[id] : undefined
}

/**
 * The memory after a read of the list: every connection it holds and none it
 * does not, each keeping what was learned about it while it is still the same
 * driver. Handed back unchanged when nothing differs, so a poll that says the
 * same thing writes nothing.
 */
export function knownDatabases(
  list: Pick<DbConnection, "id" | "name" | "driver" | "flavor" | "capabilities">[],
  held: Record<string, KnownDatabase>,
): Record<string, KnownDatabase> {
  const next = Object.fromEntries(
    list.map((conn) => {
      const before = knownDatabase(held, conn.id)
      const learned = before?.driver === conn.driver ? before : undefined
      const flavor = conn.flavor ?? learned?.flavor
      const capabilities = conn.capabilities ?? learned?.capabilities
      const row: KnownDatabase = {
        name: conn.name,
        driver: conn.driver,
        ...(flavor && { flavor }),
        ...(capabilities && { capabilities }),
      }
      return [String(conn.id), row]
    }),
  )
  return JSON.stringify(next) === JSON.stringify(held) ? held : next
}

/**
 * The memory after one database's summary has answered: what the server is
 * and what it can do, on the row the list already made. Unchanged when it
 * says nothing new, or when the list has no such row to put it on.
 */
export function learnedDatabase(
  held: Record<string, KnownDatabase>,
  id: number,
  learned: Pick<KnownDatabase, "flavor" | "capabilities">,
): Record<string, KnownDatabase> {
  const row = knownDatabase(held, id)
  if (!row) return held
  const next = { ...row, ...learned }
  return JSON.stringify(next) === JSON.stringify(row) ? held : { ...held, [id]: next }
}
