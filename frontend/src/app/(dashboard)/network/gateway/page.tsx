"use client"

import { NetworkReadWarning } from "@/components/network/read-warning"
import { useAuth } from "@/hooks/use-auth"
import { useCallback, useState } from "react"
import { Plus } from "@/components/icons"
import { get } from "@/lib/api"
import { bytes, plural } from "@/lib/format"
import type { GatewayForward, GatewayNAT, GatewayView, NetworkLink } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { Page, PageContext, Section } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { EmptyNote, ErrorState, LoadingPanel } from "@/components/state"
import { Button } from "@/components/ui/button"
import { NumberTicker } from "@/components/ui/number-ticker"
import { ForwardList, ForwardSheet } from "@/components/network/gateway/forwards"
import { ProxyHandoffs } from "@/components/network/gateway/handoffs"
import { NATList, NATSheet } from "@/components/network/gateway/nat"
import { GatewayNotices } from "@/components/network/gateway/notices"
import { GatewayPicture } from "@/components/network/gateway/picture"
import { useGrowth } from "@/components/network/gateway/use-growth"

type Sheet = { kind: "forward"; forward?: GatewayForward } | { kind: "nat"; entry?: GatewayNAT }

/**
 * What this server passes on, and to where.
 *
 * The page opens on the picture of it: every forward as a public port arriving
 * on the left, this server with its firewall's capability in the middle and the
 * target on the right drawn as the product that usually answers on that port,
 * then each NAT entry as a private network on its way out through a device. A
 * wire carries while its entry's packet counter is growing — the gateway is
 * read every five seconds, and that is the only honest "alive" a firewall
 * counter can give — and is dashed where the entry is switched off. Under it
 * the four readings the picture does not say: what is in force, what has been
 * carried since the table was loaded, whether the kernel forwards, and whether
 * the firewall lets a translated connection through.
 *
 * Then the forwards and the NAT entries as lit cards, each opening the editor
 * it is changed in, with the command that makes one in its own head. A firewall
 * that will not let the gateway write (firewalld, or another table that drops
 * forwarded traffic) is said above the picture with the rule to add, and the
 * commands are off. The reverse proxy, TCP and UDP streams and certificates are
 * not here: they are lit cards into Proxy & TLS, where their engine lives.
 */
export default function NetworkGatewayPage() {
  const { can } = useAuth()
  const gateway = usePoll<GatewayView>(
    (signal) => get("/network/gateway", undefined, signal),
    5_000,
  )
  const links = usePoll<NetworkLink[]>((signal) => get("/network/links", undefined, signal), 30_000)
  const moved = useGrowth(gateway.data)
  const [sheet, setSheet] = useState<Sheet>()
  const openForward = useCallback(
    (forward?: GatewayForward) => setSheet({ kind: "forward", forward }),
    [],
  )
  const openNAT = useCallback((entry?: GatewayNAT) => setSheet({ kind: "nat", entry }), [])

  if (!gateway.data) {
    return (
      <Page className="animate-rise">
        <PageContext eyebrow="Network" title="Gateway" />
        {gateway.error ? (
          <ErrorState error={gateway.error} onRetry={gateway.refresh} />
        ) : (
          <LoadingPanel />
        )}
      </Page>
    )
  }
  const view = gateway.data
  const writable = can("system.admin") && view.capability.writable
  const devices = links.data ?? []
  const inForce = (enabled: boolean) => enabled && view.loaded
  const forwardsOn = view.forwards.filter((f) => inForce(f.enabled)).length
  const natOn = view.nat.filter((n) => inForce(n.enabled)).length
  const entries = view.forwards.length + view.nat.length
  const packets = [...view.forwards, ...view.nat].reduce((n, e) => n + e.packets, 0)
  const carried = [...view.forwards, ...view.nat].reduce((n, e) => n + e.bytes, 0)
  const made = view.nat.filter((n) => n.owner).length

  return (
    <Page className="animate-rise">
      <PageContext eyebrow="Network" title="Gateway" />
      {gateway.data && <NetworkReadWarning error={gateway.error} refresh={gateway.refresh} />}

      <GatewayNotices view={view} />

      <Panel plain>
        <PanelHeader
          title="Where traffic goes"
          actions={
            <span className="text-hint text-muted-foreground">
              {plural(view.forwards.length, "forward")} ·{" "}
              {plural(view.nat.length, "NAT entry", "NAT entries")}
            </span>
          }
        />
        <PanelBody>
          <GatewayPicture
            view={view}
            links={devices}
            moved={moved}
            onForward={() => openForward()}
            onShare={() => openNAT()}
            onOpenForward={openForward}
            onOpenNAT={openNAT}
          />
        </PanelBody>
      </Panel>

      <StatGrid columns={4}>
        <StatTile
          label="In force"
          value={
            <>
              <NumberTicker value={forwardsOn + natOn} />
              <span className="text-muted-foreground"> / {entries}</span>
            </>
          }
          // Amber is for entries that are saved and not in the kernel, never for
          // one the reader switched off on purpose.
          tone={entries > 0 && !view.loaded ? "warning" : "default"}
          hint={
            entries === 0
              ? "nothing forwarded or shared yet"
              : `${plural(forwardsOn, "forward")} · ${plural(natOn, "NAT entry", "NAT entries")}${made ? ` · ${made} made by the VPN page` : ""}`
          }
        />
        <StatTile
          label="Carried since load"
          value={<NumberTicker value={packets} />}
          trailing="packets"
          hint={`${bytes(carried)} through forwards and NAT`}
        />
        <StatTile
          label="Forwarding"
          value={view.forwarding.ipv4 ? "IPv4 on" : "IPv4 off"}
          tone={view.forwarding.ipv4 ? "success" : entries > 0 ? "warning" : "default"}
          hint={`IPv6 ${view.forwarding.ipv6 ? "on" : "off"}`}
        />
        <StatTile
          label="Firewall admission"
          value={
            !view.admission.needed
              ? "Nothing to admit"
              : view.admission.present
                ? "Admitted"
                : "Missing"
          }
          tone={
            view.admission.needed ? (view.admission.present ? "success" : "warning") : "default"
          }
          hint={
            view.admission.needed
              ? "translated connections pass the forward chain"
              : "no entry is in force"
          }
        />
      </StatGrid>

      <Panel plain>
        <PanelHeader
          title="Port forwards"
          actions={
            <>
              <span className="numeric text-hint text-muted-foreground">
                {view.forwards.length}
              </span>
              <Button
                size="xs"
                variant="outline"
                onClick={() => openForward()}
                disabled={!writable}
              >
                <Plus aria-hidden />
                Forward a port
              </Button>
            </>
          }
        />
        <PanelBody>
          {view.forwards.length === 0 ? (
            <EmptyNote>No port is forwarded.</EmptyNote>
          ) : (
            <ForwardList
              forwards={view.forwards}
              writable={writable}
              onOpen={openForward}
              onChanged={gateway.refresh}
            />
          )}
        </PanelBody>
      </Panel>

      <Panel plain>
        <PanelHeader
          title="NAT"
          actions={
            <>
              <span className="numeric text-hint text-muted-foreground">{view.nat.length}</span>
              <Button size="xs" variant="outline" onClick={() => openNAT()} disabled={!writable}>
                <Plus aria-hidden />
                Share a network
              </Button>
            </>
          }
        />
        <PanelBody>
          {view.nat.length === 0 ? (
            <EmptyNote>No network is shared out.</EmptyNote>
          ) : (
            <NATList
              entries={view.nat}
              writable={writable}
              onOpen={openNAT}
              onChanged={gateway.refresh}
            />
          )}
        </PanelBody>
      </Panel>

      <Section title="Proxy & TLS">
        <ProxyHandoffs />
      </Section>

      {sheet?.kind === "forward" && (
        <ForwardSheet
          key={sheet.forward?.id ?? "new"}
          forward={sheet.forward}
          links={devices}
          writable={writable}
          onOpenChange={(open) => !open && setSheet(undefined)}
          onSaved={gateway.refresh}
        />
      )}
      {sheet?.kind === "nat" && (
        <NATSheet
          key={sheet.entry?.id ?? "new"}
          entry={sheet.entry}
          links={devices}
          writable={writable}
          onOpenChange={(open) => !open && setSheet(undefined)}
          onSaved={gateway.refresh}
        />
      )}
    </Page>
  )
}
