/**
 * The DNS page's views, and which one an address opens. The page did four jobs
 * down one 4,800px column; each is a view named in the address (`?tab=`) now,
 * so a link can open the settings or the lookups rather than the top of
 * everything.
 */

export const DNS_VIEWS = ["resolver", "lookups", "settings", "services", "log"] as const
export type DNSViewKey = (typeof DNS_VIEWS)[number]

export const DNS_VIEW_NAME: Record<DNSViewKey, string> = {
  resolver: "Resolver",
  lookups: "Lookups",
  settings: "Settings",
  services: "Services",
  log: "Log",
}

/**
 * The anchors links already carry, from when the page was one column: the
 * resolver owner's hand-off (`#dns-links`, written by the server) and the
 * ad-blocking choice (`#dns-upstreams`). Each opens the view its section moved
 * to, so neither link lands on a view that does not hold its target.
 */
const ANCHOR_VIEW: Record<string, DNSViewKey> = {
  "dns-links": "resolver",
  "dns-upstreams": "settings",
  "dns-hosts": "settings",
}

/** The view an address opens: its `?tab=`, else the view its anchor is in, else the resolver. */
export function dnsViewOf(tab: string | null | undefined, hash = ""): DNSViewKey {
  if (tab && (DNS_VIEWS as readonly string[]).includes(tab)) return tab as DNSViewKey
  return ANCHOR_VIEW[hash.replace(/^#/, "")] ?? "resolver"
}

export const LOOKUP_TOOLS = ["resolve", "policy", "dnssec"] as const
export type LookupTool = (typeof LOOKUP_TOOLS)[number]

/** The lookup a `?tool=` names, among those this account may run. */
export function lookupToolOf(tool: string | null | undefined, allowed: readonly LookupTool[]) {
  return allowed.find((one) => one === tool) ?? allowed[0] ?? "resolve"
}

/** The address of a view, with what else it carries: the default view has no `tab`. */
export function dnsHref(view: DNSViewKey, extra: Record<string, string | undefined> = {}) {
  const query = new URLSearchParams()
  if (view !== "resolver") query.set("tab", view)
  for (const [key, value] of Object.entries(extra)) if (value) query.set(key, value)
  const text = query.toString()
  return text ? `/network/dns?${text}` : "/network/dns"
}
