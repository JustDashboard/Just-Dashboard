/**
 * A site's page, as addresses: its views (`?tab=`), the parts its Settings
 * are read in (`&view=`), and where the links that used to open the site's
 * form in a sheet land now.
 *
 * A site's whole configuration was edited in a sheet over the Sites list
 * until 0.8.7. It is the Settings view of the site's own page now, split the
 * way a deployment's settings are split across its rail, so every address
 * that opened the sheet has one place to go.
 */

export const SITE_TABS = ["overview", "logs", "settings"] as const
export type SiteTab = (typeof SITE_TABS)[number]

export const SITE_TAB_LABEL: Record<SiteTab, string> = {
  overview: "Overview",
  logs: "Logs",
  settings: "Settings",
}

/** The view an address asks for; anything else is the Overview. */
export function siteTab(value: string | null | undefined): SiteTab {
  return SITE_TABS.includes(value as SiteTab) ? (value as SiteTab) : "overview"
}

/**
 * The parts of a site's Settings, in the order the form was read down: what
 * the site is and serves, its encryption, who may reach it, how it is
 * hardened and limited, what it adds to an answer, the paths that go
 * elsewhere, and the file a save writes.
 */
export const SETTINGS_VIEWS = [
  "general",
  "https",
  "access",
  "hardening",
  "headers",
  "paths",
  "file",
] as const
export type SettingsView = (typeof SETTINGS_VIEWS)[number]

export const SETTINGS_VIEW_LABEL: Record<SettingsView, string> = {
  general: "General",
  https: "HTTPS",
  access: "Access",
  hardening: "Hardening",
  headers: "Headers & pages",
  paths: "Paths",
  file: "File",
}

/** The part an address asks for; anything else is General. */
export function settingsView(value: string | null | undefined): SettingsView {
  return SETTINGS_VIEWS.includes(value as SettingsView) ? (value as SettingsView) : "general"
}

/** A site's page on one of its views; the Overview is the bare address. */
export function siteHref(name: string, tab: SiteTab = "overview", view?: SettingsView): string {
  const base = `/proxy/sites/${encodeURIComponent(name)}`
  if (tab === "overview") return base
  if (tab === "settings" && view && view !== "general") return `${base}?tab=settings&view=${view}`
  return `${base}?tab=${tab}`
}

/** Where a site is edited: the Settings view of its page. */
export function siteSettingsHref(name: string, view?: SettingsView): string {
  return siteHref(name, "settings", view)
}

/**
 * Whether an address leaves a site's Settings: another page, or another view
 * of this one. A move between the parts of Settings stays in the form, and
 * so does an address that only changes the fragment.
 */
export function leavesSettings(from: URL, to: URL): boolean {
  if (to.origin !== from.origin) return false
  if (to.pathname !== from.pathname) return true
  return siteTab(to.searchParams.get("tab")) !== "settings"
}
