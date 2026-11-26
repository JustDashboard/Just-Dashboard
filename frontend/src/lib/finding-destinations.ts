/** Names navigation by its destination, never by an internal module or an ambiguous "Open". */
export function findingNavigation(href: string): { href: string; label: string } | undefined {
  if (!href.startsWith("/") || href.startsWith("//")) return undefined
  const url = new URL(href, "http://dashboard.invalid")
  const container = url.pathname === "/docker/containers" && url.searchParams.get("container")
  if (container) {
    url.pathname += `/${encodeURIComponent(container)}`
    url.searchParams.delete("container")
  }
  const job = url.pathname === "/backups" && url.searchParams.get("job")
  if (job) {
    url.pathname += `/${encodeURIComponent(job)}`
    url.searchParams.delete("job")
  }
  const exact: Record<string, string> = {
    "/proxy/sites": url.searchParams.has("site") ? "Open site" : "Open sites",
    "/proxy/certificates": url.searchParams.has("cert") ? "Open certificate" : "Open certificates",
    "/proxy/tls": "Inspect TLS report",
    "/proxy/streams": url.searchParams.has("stream") ? "Open stream" : "Review streams",
    "/proxy/ports": "Inspect listening ports",
    "/proxy/config": "Inspect proxy configuration",
    "/proxy/traffic": "Inspect request errors",
    "/proxy/tunnel": "Review Cloudflare Tunnel",
    "/docker/containers": "Inspect containers",
    "/docker/images": "Review Docker storage",
    "/docker/volumes": "Inspect volumes",
    "/docker/stacks": "Review stacks",
    "/deploy/credentials": "Review credentials",
    "/network/firewall": "Review firewall controls",
    "/network/connections": "Inspect connections",
    "/network/tools": "Open network tools",
    "/network/dns": "Review DNS",
    "/network/gateway": "Review gateway",
    "/network/routing": "Review routing and forwarding",
    "/network/protection": "Review network protection",
    "/network/investigate": "Inspect network path",
    "/network/drift": "Review configuration drift",
    "/network/ipam": "Review address pools",
    "/network/external": "Inspect external reachability",
    "/network/egress": "Review outbound routes",
    "/network/traffic": "Inspect network traffic",
    "/network/interfaces": "Inspect interfaces",
    "/network/vpn": "Review VPN",
    "/security/ssh": "Review SSH controls",
    "/security/intrusion": "Review intrusion protection",
    "/packages": "Review host updates",
    "/processes/services": "Review service controls",
    "/dashboard/configuration": "Review dashboard access",
    "/backups": "Review backup jobs",
    "/files": "Open files",
    "/git": "Inspect repository",
    "/terminal": "Open terminal",
    "/logs": "Read logs",
  }
  let label = exact[url.pathname]
  if (/^\/docker\/containers\/[^/]+$/.test(url.pathname)) label = "Inspect container"
  if (/^\/docker\/stacks\/[^/]+$/.test(url.pathname)) label = "Review stack controls"
  if (/^\/backups\/[^/]+$/.test(url.pathname)) label = "Read backup run"
  if (/^\/deploy\/\d+\/runs\/\d+$/.test(url.pathname)) label = "Read failed run"
  if (/^\/deploy\/\d+\/runtime$/.test(url.pathname)) label = "Inspect runtime"
  if (/^\/deploy\/\d+\/logs$/.test(url.pathname)) label = "Read project logs"
  if (
    /^\/deploy\/\d+\/settings\/(general|build|runtime|variables|domains|storage|databases|automation|danger)$/.test(
      url.pathname,
    )
  )
    label = "Review project settings"
  // The one settings view a domain finding sends to is named for what is there.
  if (/^\/deploy\/\d+\/settings\/domains$/.test(url.pathname)) label = "Open domains"
  if (/^\/proxy\/sites\/[^/]+$/.test(url.pathname)) label = "Open site"
  if (/^\/proxy\/certificates\/[^/]+$/.test(url.pathname)) label = "Open certificate"
  if (/^\/proxy\/ports\/[^/]+$/.test(url.pathname)) label = "Open port"
  if (/^\/proxy\/streams\/[^/]+$/.test(url.pathname)) label = "Open stream"
  if (
    /^\/databases\/\d+(\/(data|query|search|schema|diagram|generate|performance|logs|access|backups|settings))?$/.test(
      url.pathname,
    )
  )
    label = "Inspect database"
  return label ? { href: url.pathname + url.search + url.hash, label } : undefined
}

/**
 * What a finding's link says: where it goes, in the destination's own word. A
 * check that said "Open owning page" for a site, a certificate and a project's
 * domains alike left the reader to find out by pressing it. The old words
 * stand only for an address this table does not name.
 */
export function findingLinkLabel(href: string): string {
  return findingNavigation(href)?.label ?? "Open owning page"
}
