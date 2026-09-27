import type { Finding } from "@/components/finding-list"
import {
  certificateFindings,
  type CertificateFindingInput,
} from "@/components/proxy/findings/certificates"
import {
  engineFindings,
  type EngineFindingInput,
  type ProxySource,
} from "@/components/proxy/findings/engine"
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

/** The label each area puts on its findings. */
type Meta = "certificate" | "renewal" | "site" | "stream" | "streams" | "ports" | ProxySource

/**
 * The words on a finding's button, by where it leads. It was `Open ${meta}`,
 * which read "Open renewal" and "Open ports"; a finding about one site opens
 * that site, and the rest open the page that lists what they are about.
 */
const ACTION: Record<Meta, string> = {
  certificate: "Open certificates",
  certificates: "Open certificates",
  renewal: "Open certificates",
  site: "Open site",
  sites: "Open sites",
  stream: "Open streams",
  streams: "Open streams",
  ports: "Open ports",
}

export function findingAction(finding: ProxyFinding): string {
  return ACTION[finding.meta as Meta]
}
