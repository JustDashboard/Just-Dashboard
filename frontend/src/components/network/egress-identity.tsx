"use client"

import type { NetworkEgressIdentity } from "@/lib/types"
import { InfoTip } from "@/components/form"
import { Globe } from "@/components/icons"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Status, type DotTone } from "@/components/status-dot"
import { WireMark } from "@/components/deploy/wire"
import { SCOPE_WORD, identityVerdict } from "@/components/network/topology-reading"

const VERDICT_TONE: Record<NetworkEgressIdentity["public"], DotTone> = {
  nic: "running",
  unknown: "warning",
  no_route: "stopped",
  translated: "notice",
  unobserved: "notice",
}

/**
 * How this server leaves in each family, as the kernel answers it, and what
 * that says about the address the internet sees: one line a family, a mark
 * and a sentence. The NIC's source and the provider's identity are two facts
 * and stay two: the sentence says where traffic leaves and from which
 * address, the line under it what that establishes about the public one, and
 * whether the family is forwarded, since a gateway can route one and not the
 * other.
 *
 * It was a framed table of two rows whose widest cell wrapped a paragraph
 * under a verdict. The paragraph is the ⓘ beside the family now.
 */
export function WaysOut({ identity }: { identity: NetworkEgressIdentity[] }) {
  return (
    <Panel plain>
      <PanelHeader title="Ways out" />
      <PanelBody>
        <ul aria-label="Ways out" className="min-w-0">
          {identity.map((id) => (
            <WayOut key={id.family} id={id} />
          ))}
        </ul>
      </PanelBody>
    </Panel>
  )
}

function WayOut({ id }: { id: NetworkEgressIdentity }) {
  const family = id.family === "inet6" ? "IPv6" : "IPv4"
  const about = id.error ?? id.detail
  const routed = id.public !== "no_route" && Boolean(id.device)
  return (
    <li data-family={id.family} className="flex min-w-0 items-center gap-3.5 py-2.5">
      <WireMark size="md" tone={routed ? "logo" : "neutral"} shape="square">
        <Globe aria-hidden className={routed ? undefined : "text-muted-foreground"} />
      </WireMark>
      <div className="min-w-0 flex-1">
        <p className="eyebrow flex items-center gap-1.5">
          {family}
          {about && <InfoTip label={`About the ${family} way out`}>{about}</InfoTip>}
        </p>
        <p className="text-body leading-snug font-medium [overflow-wrap:anywhere]">
          {id.device ? (
            <>
              Leaves <span className="font-mono">{id.device}</span>
              {id.gateway && (
                <>
                  {" "}
                  via <span className="font-mono">{id.gateway}</span>
                </>
              )}
              {id.source && (
                <>
                  {" "}
                  from <span className="font-mono">{id.source}</span>
                  <span className="font-normal text-muted-foreground">
                    , {SCOPE_WORD[id.sourceScope]}
                  </span>
                </>
              )}
            </>
          ) : (
            <span className="text-muted-foreground">
              {id.public === "no_route" ? "No route out" : "The way out could not be read"}
            </span>
          )}
        </p>
        <p className="flex min-w-0 flex-wrap items-center gap-x-2 text-hint leading-snug text-muted-foreground">
          <Status
            tone={VERDICT_TONE[id.public]}
            label={identityVerdict(id)}
            className="text-hint"
          />
          <span aria-hidden className="text-muted-foreground/40">
            ·
          </span>
          {id.forwardingError ? (
            <span className="text-warning" title={id.forwardingError}>
              forwarding unreadable
            </span>
          ) : (
            <span>forwarding {id.forwarding ? "on" : "off"}</span>
          )}
        </p>
      </div>
    </li>
  )
}
