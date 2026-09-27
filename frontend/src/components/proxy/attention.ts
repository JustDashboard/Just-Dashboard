import type { Finding } from "@/components/finding-list"
import {
  certificateFindings,
  type CertificateFindingInput,
} from "@/components/proxy/findings/certificates"
import { engineFindings, type EngineFindingInput } from "@/components/proxy/findings/engine"
import { insightFindings } from "@/components/proxy/findings/insights"
import { portFindings, type PortFindingInput } from "@/components/proxy/findings/ports"
import type { ProxyFinding } from "@/components/proxy/findings/shared"
import { siteFindings, type SiteFindingInput } from "@/components/proxy/findings/sites"
import { streamFindings, type StreamFindingInput } from "@/components/proxy/findings/streams"

export { DANGEROUS_PORTS, type ProxyFinding } from "@/components/proxy/findings/shared"
export {
  unreadableSource,
  type ProxySource,
  type UnreadableSource,
} from "@/components/proxy/findings/engine"

const RANK: Record<Finding["level"], number> = { critical: 3, warning: 2, notice: 1 }

/**
 * Every area's findings as one list, worst first. Each area judges its own
 * conditions in findings/; the order they are gathered in is the order ties
 * keep, since the sort is stable.
 */
export function foldProxyFindings(
  input: CertificateFindingInput &
    SiteFindingInput &
    StreamFindingInput &
    PortFindingInput &
    EngineFindingInput,
): ProxyFinding[] {
  return [
    ...certificateFindings(input),
    ...siteFindings(input),
    ...streamFindings(input),
    ...portFindings(input),
    ...engineFindings(input),
    ...insightFindings(),
  ].sort((a, b) => RANK[b.level] - RANK[a.level])
}

/** The pages a finding can lead to, named as its button reads them. */
const PLACE: Record<string, string> = {
  "/proxy/sites": "sites",
  "/proxy/certificates": "certificates",
  "/proxy/tls": "TLS report",
  "/proxy/streams": "streams",
  "/proxy/ports": "ports",
}

/**
 * The words on a finding's button, from where it leads. It was `Open ${meta}`,
 * which read "Open renewal", and then a table of the metas each area used,
 * which left any meta added later with a wrench and no name. The href is the
 * one thing every finding has: a finding about one site or one certificate
 * opens that one, the rest open the page that lists what they are about, and
 * a page not named here is still a button that says it opens something.
 */
export function findingAction(finding: ProxyFinding): string {
  // Any origin will do: only the path and the query are read.
  const { pathname, searchParams } = new URL(finding.href, "http://proxy.invalid")
  if (pathname === "/proxy/sites" && searchParams.has("site")) return "Open site"
  if (pathname === "/proxy/certificates" && searchParams.has("cert")) return "Open certificate"
  const place = PLACE[pathname]
  return place ? `Open ${place}` : "Open"
}
