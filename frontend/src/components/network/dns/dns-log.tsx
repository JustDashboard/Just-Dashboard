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
import { Logs } from "@/components/icons"
import { kindViews } from "@/components/logs/kind-views"
import { ServiceLogs } from "@/components/logs/service-logs"
import { EmptyState } from "@/components/state"
import { Skeleton } from "@/components/ui/skeleton"
import { dnsLogSources } from "@/components/network/dns/log-sources"

const PANE = "h-[min(75vh,40rem)] min-h-80"

/**
 * The logs of what answers this host's lookups, on the page about them:
 * resolved's unit, the resolver programs on port 53, and the containers that
 * run an ad-blocker or a setup this dashboard provisioned. A resolver that
 * refuses a name or a DNSSEC check says why here, not on a Logs page that
 * knows it as one unit among hundreds.
 *
 * It is a view of its own, so the pane is the view and nothing stands under
 * it; a host with nothing to read a log from says so rather than drawing no
 * view at all. The setups are an administrator's to list, so only one is
 * asked for them.
 */
export function DNSLog({ view }: { view?: DNSView }) {
  const { can } = useAuth()
  const provisions = usePoll<DNSServiceProvision[]>(
    async (signal) =>
      readDNSList(await get(`${DNS_SERVICE_BASE}/provisions`, undefined, signal), readDNSProvision),
    60_000,
    [],
    { enabled: can("system.admin") },
  )
  const sources = useMemo(
    () => (view ? dnsLogSources(view, provisions.data) : []),
    [view, provisions.data],
  )
  if (!view) return <Skeleton className={`w-full rounded-xl ${PANE}`} />
  if (sources.length === 0)
    return (
      <EmptyState
        icon={Logs}
        title="Nothing here keeps a DNS log"
        description="systemd-resolved is not installed, and no resolver or DNS filter runs on this server."
      />
    )
  return (
    <ServiceLogs
      sources={sources}
      views={kindViews}
      storageKey="network.dns.log"
      pickerLabel="DNS log"
      paneClassName={PANE}
    />
  )
}
