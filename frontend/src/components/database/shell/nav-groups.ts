import type { NavScope } from "@/components/nav-scope"
import type { DbDriver } from "@/lib/types"
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
 * the address alone, before the list has been read.
 */
export type KnownDatabase = { name: string; driver: DbDriver; flavor?: string }

/** Where that memory is kept (`useViewState`), by connection id. */
export const KNOWN_DATABASES_KEY = "databases.known"
