import type { Finding } from "@/components/finding-list"
import {
  certificateFindings,
  type CertificateFindingInput,
} from "@/components/proxy/findings/certificates"
import { engineFindings } from "@/components/proxy/findings/engine"
import { insightFindings } from "@/components/proxy/findings/insights"
import { portFindings, type PortFindingInput } from "@/components/proxy/findings/ports"
import type { ProxyFinding } from "@/components/proxy/findings/shared"
import { siteFindings, type SiteFindingInput } from "@/components/proxy/findings/sites"
import { streamFindings, type StreamFindingInput } from "@/components/proxy/findings/streams"

export { DANGEROUS_PORTS, type ProxyFinding } from "@/components/proxy/findings/shared"

const RANK: Record<Finding["level"], number> = { critical: 3, warning: 2, notice: 1 }

/**
 * Every area's findings as one list, worst first. Each area judges its own
 * conditions in findings/; the order they are gathered in is the order ties
 * keep, since the sort is stable.
 */
export function foldProxyFindings(
  input: CertificateFindingInput & SiteFindingInput & StreamFindingInput & PortFindingInput,
): ProxyFinding[] {
  return [
    ...certificateFindings(input),
    ...siteFindings(input),
    ...streamFindings(input),
    ...portFindings(input),
    ...engineFindings(),
    ...insightFindings(),
  ].sort((a, b) => RANK[b.level] - RANK[a.level])
}
