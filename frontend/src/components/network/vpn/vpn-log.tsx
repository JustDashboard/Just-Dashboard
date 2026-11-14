"use client"

import { useMemo } from "react"
import type { VPNView } from "@/lib/types"
import { kindViews } from "@/components/logs/kind-views"
import { ServiceLogs } from "@/components/logs/service-logs"
import { Logs } from "@/components/icons"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { EmptyState } from "@/components/state"
import { vpnLogSources } from "@/components/network/vpn/log-sources"

/**
 * The logs of the tunnels, on the page about them: a WireGuard tunnel's unit
 * says why it did not come up, tailscaled why the tailnet is not reachable or
 * a login is pending, Headscale's container what its nodes asked for.
 */
export function VPNLog({ vpn }: { vpn: VPNView }) {
  const sources = useMemo(() => vpnLogSources(vpn), [vpn])
  // The view is always in the strip, so it says why it has nothing to show.
  if (sources.length === 0)
    return (
      <EmptyState
        icon={Logs}
        title="Nothing here writes a log yet"
        description="A WireGuard tunnel's unit, tailscaled and a Headscale container write here once one of them runs."
      />
    )
  return (
    <Panel plain>
      <PanelHeader title="VPN log" />
      <PanelBody flush className="pt-3">
        <ServiceLogs
          sources={sources}
          views={kindViews}
          storageKey="network.vpn.log"
          pickerLabel="VPN log"
          paneClassName="h-[min(75vh,40rem)] min-h-80"
        />
      </PanelBody>
    </Panel>
  )
}
