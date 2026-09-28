import type { DriftReport } from "@/lib/types"
import type { ProxyFinding } from "@/components/proxy/findings/shared"

export type InsightFindingInput = {
  /** What each TLS site hands out against the file it names. */
  drift?: DriftReport
}

/** The id prefix of a served certificate that is not the file's, which a page answers with a reload. */
export const SERVED_STALE = "insight.served-stale."

function days(n: number): string {
  if (n < 0) return "has expired"
  return n === 1 ? "expires in 1 day" : `expires in ${n} days`
}

/**
 * Conditions read from what the proxy is doing rather than from its files:
 * a certificate renewed on disk and never reloaded, and a name that reaches
 * another server block's certificate. A block that did not answer or could
 * not be asked is not one: the engine's own findings say when nginx is down.
 */
export function insightFindings({ drift }: InsightFindingInput): ProxyFinding[] {
  const out: ProxyFinding[] = []
  for (const site of drift?.sites ?? []) {
    const href = `/proxy/sites?site=${encodeURIComponent(site.site)}`
    const name = site.serverName ?? site.site
    if (site.state === "stale" && site.served && site.disk) {
      out.push({
        id: `${SERVED_STALE}${site.path}:${site.line}`,
        level: site.served.expired || site.served.daysLeft <= 7 ? "critical" : "warning",
        title: `${name} still serves the certificate it had before the file changed`,
        detail: `Stale: nginx still serves the certificate that ${days(site.served.daysLeft)}; the file on disk, ${site.certPath}, is valid for ${Math.max(site.disk.daysLeft, 0)} days.`,
        advice:
          "nginx reads certificate files only when it starts or reloads. A reload serves the file on disk without dropping a connection.",
        meta: site.site,
        href,
      })
    } else if (site.state === "mismatch" && site.served) {
      out.push({
        id: `insight.served-mismatch.${site.path}:${site.line}`,
        level: "warning",
        title: `${name} gets another server block's certificate`,
        detail: site.servedBy
          ? `Mismatch: a request for ${name} was answered with the certificate ${site.servedBy} names, not ${site.certPath}. SNI fell through to another server block.`
          : `Mismatch: a request for ${name} was answered with a certificate for ${site.served.domains.join(", ") || "another name"}, not ${site.certPath}. SNI fell through to another server block.`,
        advice:
          "nginx picks the block by server_name. Check the spelling in this site's server_name, or whether another block claims the same name on this port.",
        meta: site.site,
        href,
      })
    }
  }
  return out
}
