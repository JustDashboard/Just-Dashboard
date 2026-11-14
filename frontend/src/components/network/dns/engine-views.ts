/**
 * The views of a connected DNS engine's page, and its address. A connection's
 * inventory was one sheet over the DNS page, twelve sections and three forms
 * down a drawer's width, with no address to send anybody. It is a page of its
 * own under `/network/dns/services/<id>` now, each part a view named in the
 * address (`?tab=`), and a retained change's review is part of the Changes
 * view (`?change=<id>`), so a review has an address too.
 */

export const ENGINE_TABS = [
  "overview",
  "clients",
  "records",
  "queries",
  "filters",
  "dhcp",
  "changes",
] as const
export type EngineTab = (typeof ENGINE_TABS)[number]

export const ENGINE_TAB_NAME: Record<EngineTab, string> = {
  overview: "Overview",
  clients: "Clients",
  records: "Records",
  queries: "Queries",
  filters: "Filters",
  dhcp: "DHCP",
  changes: "Changes",
}

/** The view an address opens: its `?tab=`, else the overview; a `?change=` is in Changes. */
export function engineTabOf(tab: string | null | undefined, change?: string | null): EngineTab {
  if (change) return "changes"
  return ENGINE_TABS.find((one) => one === tab) ?? "overview"
}

/** The address of an engine's page, on a view, and at a retained change's review. */
export function engineHref(id: string, tab: EngineTab = "overview", change?: string) {
  const query = new URLSearchParams()
  if (tab !== "overview") query.set("tab", tab)
  if (change) {
    query.set("tab", "changes")
    query.set("change", change)
  }
  const text = query.toString()
  const path = `/network/dns/services/${encodeURIComponent(id)}`
  return text ? `${path}?${text}` : path
}

/** The route's segment as a connection id. */
export function engineIdOf(segment: string | undefined) {
  if (!segment) return ""
  try {
    return decodeURIComponent(segment)
  } catch {
    return segment
  }
}

/** The engine before or after one in the list, for Alt+↑ and Alt+↓. */
export function engineSibling<T extends { id: string }>(
  engines: T[],
  id: string,
  step: 1 | -1,
): T | undefined {
  const at = engines.findIndex((engine) => engine.id === id)
  if (at < 0) return step > 0 ? engines[0] : engines.at(-1)
  return engines[at + step]
}
