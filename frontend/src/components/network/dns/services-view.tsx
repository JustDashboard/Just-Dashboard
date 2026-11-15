"use client"

import { useAuth } from "@/hooks/use-auth"
import type { DNSView } from "@/lib/types"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Adblock } from "@/components/network/dns/adblock"
import { GhostRows } from "@/components/network/dns/ghost"
import {
  DNSServiceManager,
  type DNSServiceAsk,
  type DNSServiceRequest,
} from "@/components/network/dns/services"

/**
 * The DNS services on this server: the filters the page found running
 * (AdGuard Home, Pi-hole, Technitium), each handed to the native connection
 * that reads it, and for an administrator the engines connected through their
 * own API and the setups this dashboard owns.
 *
 * The engines' manager is mounted whatever the first read is doing, so a
 * sheet opened from the page's menu before the resolver has answered is not
 * closed when it does.
 */
export function ServicesView({
  view,
  failed,
  request,
  onRequest,
  onUpstreams,
}: {
  /** Undefined while the first read is out. */
  view?: DNSView
  /** The first read failed: the page says so, and what it would have found is not drawn. */
  failed: boolean
  request?: DNSServiceRequest
  onRequest: (request: DNSServiceAsk) => void
  onUpstreams: () => void
}) {
  const { can } = useAuth()
  return (
    <div className="flex min-w-0 flex-col gap-6 md:gap-8">
      {!failed && (
        <Panel plain>
          <PanelHeader
            title="Ad-blocking"
            actions={
              view &&
              view.adblock.length > 0 && (
                <span className="numeric text-hint text-muted-foreground">
                  {view.adblock.length}
                </span>
              )
            }
          />
          <PanelBody>
            {view ? (
              <Adblock
                adblock={view.adblock}
                onRequest={can("system.admin") ? onRequest : undefined}
                onUpstreams={onUpstreams}
              />
            ) : (
              <GhostRows rows={1} />
            )}
          </PanelBody>
        </Panel>
      )}
      <DNSServiceManager key="native-dns-services" request={request} />
    </div>
  )
}
