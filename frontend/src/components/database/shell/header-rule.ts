import type { SectionId } from "@/components/database/engine"
import { DATABASE_ID, databasePlace } from "@/components/database/shell/routes"

/**
 * What stands at the top of a page under a database, decided in one place.
 *
 * A project repeats its full header on every one of its pages, and a database
 * follows it: every page under `/databases/<id>` opens on the database's
 * header (`database-header.tsx`) — the engine's mark, the name with whether
 * it answers, one line of facts, the one command and the menu — at the same
 * height whichever page it is, so going from Backups to Settings moves
 * nothing above the page.
 *
 * Three kinds of page are the exception, each because it already has a line
 * of its own to open on:
 *
 * - **A workbench that heads itself** (Data, Query, Schema, the Diagram): one
 *   frame held to the window, whose own head carries the database's name and
 *   verbs (`workbench/chrome.tsx`). The shell draws nothing over it: `head`.
 * - **An item's page two levels down** (a statement, a table or a session
 *   under Performance; an account under Access): it opens on the item's own
 *   identity line, and a second header above that would be two things the
 *   page is about. It keeps the one-line strip that says which database the
 *   item belongs to: `strip`.
 * - Everything else is `full`, an address that is no page of the database
 *   included: the statement that it is not found stands under the header of
 *   the database it was looked for in.
 *
 * The two other pages held to the window, the assistant and Logs, are under
 * the full header as a project's are; `fills` says the page takes the height
 * the header leaves rather than scrolling with it.
 */
export type DatabaseHead = "full" | "strip" | "head"

/** The workbenches whose frame draws the database's line itself, as its head. */
export const HEADED: readonly SectionId[] = ["data", "query", "schema", "diagram"]

/** The sections with pages for one item under them, each with a line of its own. */
const ITEM_PAGES: Partial<Record<string, RegExp>> = {
  // /performance/statements/<key>, /performance/tables/<name>, /performance/sessions/<pid>
  performance: /^(statements|tables|sessions)\/[^/]+\/?$/,
  // /access/<account>
  access: /^[^/]+\/?$/,
}

/** Whether a path is one item's page: two levels under a database, with its own identity line. */
export function isItemPage(pathname: string): boolean {
  const match = /^\/databases\/([^/]+)\/([a-z]+)\/(.+)$/.exec(pathname)
  if (!match || !DATABASE_ID.test(match[1])) return false
  return Boolean(ITEM_PAGES[match[2]]?.test(match[3]))
}

/**
 * What a section's registry entry says of it, as far as the header cares: the
 * engine's own answer, `undefined` where the engine has no such page.
 */
type SectionOf = (section: SectionId) => { workbench?: boolean } | undefined

/** Which of the three a path opens on, for an engine that has the pages `sectionOf` knows. */
export function databaseHead(pathname: string, sectionOf: SectionOf): DatabaseHead {
  if (isItemPage(pathname)) return "strip"
  const place = databasePlace(pathname)
  // Only where the engine has the page: the statement that it has none is a
  // reading page, under the header.
  if (place && HEADED.includes(place.section) && sectionOf(place.section)?.workbench) return "head"
  return "full"
}

/**
 * Whether the page under a full header is held to the window (the assistant,
 * Logs): it is given the height the header leaves, and scrolls inside itself.
 */
export function fillsUnderHeader(pathname: string, sectionOf: SectionOf): boolean {
  const place = databasePlace(pathname)
  if (!place || HEADED.includes(place.section)) return false
  return Boolean(sectionOf(place.section)?.workbench)
}
