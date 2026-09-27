import type { VHost } from "@/lib/types"
import type { ProxyFinding } from "@/components/proxy/findings/shared"

export type SiteFindingInput = { vhosts?: VHost[] }

/** A site on disk and not serving, and one proxying an application in plain text. */
export function siteFindings({ vhosts }: SiteFindingInput): ProxyFinding[] {
  const out: ProxyFinding[] = []

  for (const vhost of vhosts ?? []) {
    if (vhost.kind !== "nginx") continue
    if (!vhost.enabled && vhost.enabledPath) {
      out.push({
        id: `site.disabled.${vhost.name}`,
        level: "notice",
        title: `${vhost.name} is on disk but not serving`,
        detail: "The file is in sites-available with no link in sites-enabled.",
        advice: "Enable it from Sites if it is meant to serve, or delete it if it is not.",
        meta: "site",
        href: `/proxy/sites?site=${encodeURIComponent(vhost.name)}`,
      })
      continue
    }
    if (vhost.enabled && !vhost.tls && vhost.upstreams.length > 0) {
      out.push({
        id: `site.plain.${vhost.name}`,
        level: "warning",
        title: `${vhost.name} serves an application in plain text`,
        detail: `${vhost.serverNames.join(", ") || vhost.name} proxies to ${vhost.upstreams[0]} with no TLS, so anything typed into it crosses the network readable.`,
        advice:
          "Issue a certificate from the Certificates page, then turn TLS on in the site's form.",
        meta: "site",
        href: `/proxy/sites?site=${encodeURIComponent(vhost.name)}`,
      })
    }
  }

  return out
}
