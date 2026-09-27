import type { VHost } from "@/lib/types"
import type { ProxyFinding } from "@/components/proxy/findings/shared"

export type SiteFindingInput = { vhosts?: VHost[] }

/**
 * A link in sites-enabled to nothing, one serving another file, a site on
 * disk and not serving, and one proxying an application in plain text.
 */
export function siteFindings({ vhosts }: SiteFindingInput): ProxyFinding[] {
  const out: ProxyFinding[] = []

  for (const vhost of vhosts ?? []) {
    if (vhost.kind !== "nginx") continue
    const link = `sites-enabled/${vhost.name}`
    if (vhost.broken === "dangling") {
      out.push({
        id: `site.dangling.${vhost.name}`,
        level: "critical",
        title: `${link} points at a file that is gone`,
        detail: `It points at ${vhost.linkTarget ?? "a missing file"}. nginx reads every entry in sites-enabled, so it refuses every reload — and would not start — until the link is removed.`,
        advice:
          vhost.layout === "sites-available"
            ? `Enable ${vhost.name} from Sites to point the link back at its file, or remove the link.`
            : "Remove the link from Sites, or put the file back where it points.",
        meta: "site",
        href: "/proxy/sites",
      })
      continue
    }
    if (vhost.broken === "stale") {
      out.push({
        id: `site.stale.${vhost.name}`,
        level: "warning",
        title: `${vhost.name} is not what nginx serves under its name`,
        detail: vhost.linkTarget
          ? `${link} points at ${vhost.linkTarget}, so nginx serves that file and not ${vhost.path}.`
          : `${link} is a separate file rather than a link, so nginx serves that copy and not ${vhost.path}.`,
        advice: vhost.linkTarget
          ? `Enable ${vhost.name} from Sites to point the link at this file.`
          : `Compare this file with the served copy from Sites, then replace ${link} with a link to this one.`,
        meta: "site",
        href: "/proxy/sites",
      })
      continue
    }
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
