"use client"

import { useMemo } from "react"
import { get } from "@/lib/api"
import {
  DNS_SERVICE_BASE,
  readDNSList,
  readDNSProvision,
  type DNSServiceProvision,
} from "@/lib/network-dns-services"
import type { DNSView } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { kindViews } from "@/components/logs/kind-views"
import { ServiceLogs } from "@/components/logs/service-logs"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { dnsLogSources } from "@/components/network/dns/log-sources"

/**
 * The logs of what answers this host's lookups, on the page about them:
 * resolved's unit, the resolver programs on port 53, and the containers that
 * run an ad-blocker or a setup this dashboard provisioned. A resolver that
 * refuses a name or a DNSSEC check says why here, not on a Logs page that
 * knows it as one unit among hundreds.
 *
 * The setups are an administrator's to list, so only one is asked for them.
 */
export function DNSLog({ view }: { view: DNSView }) {
  const { can } = useAuth()
  const provisions = usePoll<DNSServiceProvision[]>(
    async (signal) =>
      readDNSList(await get(`${DNS_SERVICE_BASE}/provisions`, undefined, signal), readDNSProvision),
    60_000,
    [],
    { enabled: can("system.admin") },
  )
  const sources = useMemo(() => dnsLogSources(view, provisions.data), [view, provisions.data])
  if (sources.length === 0) return null
  return (
    <Panel plain>
      <PanelHeader title="DNS log" />
      <PanelBody flush className="pt-3">
        <ServiceLogs
          sources={sources}
          views={kindViews}
          storageKey="network.dns.log"
          pickerLabel="DNS log"
          paneClassName="h-[min(75vh,40rem)] min-h-80"
        />
      </PanelBody>
    </Panel>
  )
}
