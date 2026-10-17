import {
  Cpu,
  Globe,
  Puzzle,
  SettingsGear,
  SettingsSliders,
  Warning,
  type Icon,
} from "@/components/icons"
import type { Capability } from "@/lib/types"
import type { Engine } from "@/components/database/engine"

/** One page of a database's Settings, as its address and the rail name it. */
export type SettingsPageKey =
  "general" | "network" | "resources" | "parameters" | "extensions" | "danger"

export type SettingsPage = {
  key: SettingsPageKey
  title: string
  /** After `/settings`: empty for General, which is the section's own address. */
  path: string
  icon: Icon
  /** Hidden in the rail unless the role holds it. */
  capability?: Capability
}

/**
 * The pages a database's Settings has, in the order a reader goes through
 * them: what the dashboard calls it and how it dials it, where it can be
 * reached from, what it may use of the machine, the server's own parameters,
 * what it is extended with, and the acts that end it. What else the server holds is Home's: it changes
 * nothing about this connection, so it is not a setting.
 *
 * Settings was one page of six sections under a strip of anchors, and the
 * section a reader most often came for — the danger zone, a parameter — was
 * the one at the foot of six screens. Each is its own page now, listed in the
 * rail the way a project's settings are (`PROJECT_SETTINGS_NAV`), and a page
 * the engine has no use for is not listed: a file has no server, so no
 * network and nothing to limit; a key–value server has modules where a SQL
 * engine has extensions; a document database's parameters are status
 * counters, which are Performance's.
 */
export function settingsPages(engine: Engine): SettingsPage[] {
  const server = engine.can("server")
  const keyvalue = engine.kind === "keyvalue"
  const pages: (SettingsPage | false)[] = [
    { key: "general", title: "General", path: "", icon: SettingsGear },
    server && { key: "network", title: "Network", path: "network", icon: Globe },
    server && { key: "resources", title: "Resources", path: "resources", icon: Cpu },
    engine.can("settings") &&
      engine.kind !== "document" && {
        key: "parameters",
        title: "Parameters",
        path: "parameters",
        icon: SettingsSliders,
      },
    (engine.can("extensions") || keyvalue) && {
      key: "extensions",
      title: keyvalue ? "Modules" : "Extensions",
      path: "extensions",
      icon: Puzzle,
    },
    {
      key: "danger",
      title: "Danger zone",
      path: "danger",
      icon: Warning,
      capability: "destructive",
    },
  ]
  return pages.filter((page): page is SettingsPage => Boolean(page))
}

/** A settings page's address under the section's own. */
export function settingsPageHref(settingsHref: string, page: Pick<SettingsPage, "path">) {
  return page.path ? `${settingsHref}/${page.path}` : settingsHref
}

/**
 * Where an address from the one-page Settings goes now: its `#section`
 * anchor, or a parameter search (`?q=`, `?only=`), names the page that holds
 * it. `null` when it names nothing, which is General.
 */
export function formerSettingsPage(
  hash: string,
  search: { get: (name: string) => string | null },
): SettingsPageKey | null {
  const anchor = decodeURIComponent(hash.replace(/^#/, ""))
  const named: Record<string, SettingsPageKey> = {
    reachability: "network",
    parameters: "parameters",
    extensions: "extensions",
    danger: "danger",
  }
  if (Object.hasOwn(named, anchor)) return named[anchor]
  if (search.get("q") || search.get("only")) return "parameters"
  return null
}
