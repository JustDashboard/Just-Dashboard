"use client"

import { Suspense, useCallback, useState } from "react"
import { useSearchParams } from "next/navigation"
import { LockClosed } from "@/components/icons"
import { get } from "@/lib/api"
import type { VPNView, WGInterface, WGPeer, WireGuardView } from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { SaveBarProvider } from "@/components/deploy/settings/save-bar"
import { FactDot, HostFact, HostIdentity } from "@/components/metrics/host-identity"
import { Modal } from "@/components/modal"
import { Page, PageContext } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { EmptyState, ErrorState, LoadingRows, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { WireFlow } from "@/components/wire-flow"
import { InstallFollowUp, InstallHandoff } from "@/components/network/install"
import { NetworkReadWarning } from "@/components/network/read-warning"
import { ArchivedTunnels } from "@/components/network/vpn/archive"
import { usePeerSheets } from "@/components/network/vpn/peers"
import { WireGuardSetup } from "@/components/network/vpn/setup"
import { TailscaleBlock, TailscaleMissing } from "@/components/network/vpn/tailscale"
import { TunnelExit } from "@/components/network/vpn/tunnel-exit"
import {
  IdentitySkeleton,
  TunnelIdentity,
  useTunnelControl,
} from "@/components/network/vpn/tunnel-identity"
import { PEERS_DRAWN, TunnelPicture } from "@/components/network/vpn/tunnel-picture"
import { VPNLog } from "@/components/network/vpn/vpn-log"

/**
 * The tunnels into and out of this server.
 *
 * It opens on what it is about, with no row of figures over it: each VPN is
 * its own identity line. WireGuard first, because it is the one the dashboard
 * runs: a tunnel's line (who is online, its network, its port, Add a device),
 * its picture straight under it — the devices that dial in, the tunnel, the
 * sites it joins, every wire moving while its peer is connected — and whether
 * its clients' internet leaves through here. A tunnel's record is its own
 * page (`/network/vpn/<name>`). A server with no tunnel opens on the one-step
 * setup; one without the tools on their install. Then Tailscale: this
 * server's place on the tailnet, what it offers, and every other machine with
 * how it is reached; Headscale's nodes under it where Headscale runs. The log
 * of all of it ends the page.
 *
 * Every read here names who connects, so the page is an administrator's,
 * as the SSH configuration is.
 */
export default function NetworkVPNPage() {
  return (
    <Suspense
      fallback={
        <Page>
          <PageContext eyebrow="Network" title="VPN" />
        </Page>
      }
    >
      <NetworkVPNContent />
    </Suspense>
  )
}

function NetworkVPNContent() {
  const { can } = useAuth()
  const admin = can("system.admin")
  const vpn = usePoll<VPNView>((signal) => get("/network/vpn", undefined, signal), 10_000, [], {
    enabled: admin,
  })

  if (!admin) {
    return (
      <Page>
        <PageContext eyebrow="Network" title="VPN" />
        <EmptyState
          icon={LockClosed}
          title="VPN needs the admin capability"
          description="Tunnel peers and their client configurations are visible to administrators."
        />
      </Page>
    )
  }

  if (!vpn.data) {
    return (
      <Page className="animate-rise">
        <PageContext eyebrow="Network" title="VPN" />
        {vpn.error ? (
          <ErrorState error={vpn.error} onRetry={vpn.refresh} retryable />
        ) : (
          // The page's own shape while it is read: both lines and the picture
          // stand where they will, so nothing moves when the answer lands.
          <>
            <section aria-label="WireGuard" aria-busy className="flex min-w-0 flex-col gap-6">
              <IdentitySkeleton mark="wireguard" />
              <WireFlow
                loading
                start={{ title: "Devices", nodes: [] }}
                end={{ title: "Sites", nodes: [], loadingRows: 2 }}
                hub={{ mark: null }}
              />
            </section>
            <section aria-label="Tailscale" aria-busy className="flex min-w-0 flex-col gap-6">
              <IdentitySkeleton mark="tailscale" />
              <LoadingRows rows={4} />
            </section>
          </>
        )}
      </Page>
    )
  }
  const { wireguard, tailscale, headscale } = vpn.data

  return (
    <SaveBarProvider>
      <Page className="animate-rise">
        <PageContext eyebrow="Network" title="VPN" />
        <NetworkReadWarning error={vpn.error} refresh={vpn.refresh} lastSuccess={vpn.lastSuccess} />

        <WireGuard wireguard={wireguard} onChanged={vpn.refresh} />
        {wireguard.installed && <ArchivedTunnels onRestored={vpn.refresh} />}

        {tailscale.error ? (
          <Notice title="Tailscale could not be read">{tailscale.error}</Notice>
        ) : !tailscale.installed ? (
          <TailscaleMissing onInstalled={vpn.refresh} />
        ) : (
          <TailscaleBlock tailscale={tailscale} headscale={headscale} onChanged={vpn.refresh} />
        )}

        <VPNLog vpn={vpn.data} />
      </Page>
    </SaveBarProvider>
  )
}

/**
 * WireGuard on this server: its tunnels, each on its own line with its
 * picture; the setup where there is none; the install where the tools are
 * missing. The dialog that makes another tunnel is opened from a tunnel's
 * menu, or by a link from Address planning that carries a planned range.
 */
function WireGuard({ wireguard, onChanged }: { wireguard: WireGuardView; onChanged: () => void }) {
  const [creating, setCreating] = useState(false)
  const [dismissedReservationId, setDismissedReservationId] = useState("")
  const query = useSearchParams().get("ipamReservation") ?? ""
  const initialReservationId =
    /^[a-f0-9]{32}$/.test(query) && query !== dismissedReservationId ? query : ""
  const closeCreation = () => {
    setCreating(false)
    if (initialReservationId) setDismissedReservationId(initialReservationId)
  }
  const created = () => {
    closeCreation()
    onChanged()
  }
  const peers = usePeerSheets(wireguard.interfaces, onChanged)
  const create = useCallback(() => setCreating(true), [])

  return (
    <section aria-label="WireGuard" className="flex min-w-0 flex-col gap-6">
      {wireguard.error && <Notice title="WireGuard could not be read">{wireguard.error}</Notice>}
      {wireguard.installed && <InstallFollowUp pkg="wireguard-tools" />}
      {!wireguard.installed ? (
        <InstallHandoff
          pkg="wireguard-tools"
          products={["wireguard"]}
          title="WireGuard is not installed"
          description="The kernel has WireGuard built in; the tools that configure it are one package. Then a tunnel is one step: devices get a QR code, other sites a file."
          onInstalled={onChanged}
        />
      ) : wireguard.interfaces.length === 0 ? (
        <>
          <HostIdentity
            label="About WireGuard"
            slot="tunnel-identity"
            mark="wireguard"
            title="WireGuard"
            state={<Status tone="stopped" label="No tunnel yet" />}
            facts={
              <>
                <HostFact>
                  <span className="font-mono">{wireguard.package || "wireguard-tools"}</span>{" "}
                  installed
                </HostFact>
                {wireguard.kernel && (
                  <>
                    <FactDot />
                    <HostFact>in this kernel</HostFact>
                  </>
                )}
              </>
            }
          />
          <Panel plain>
            <PanelHeader title="Set up a tunnel" />
            <PanelBody>
              <div className="max-w-3xl">
                <WireGuardSetup initialReservationId={initialReservationId} onCreated={created} />
              </div>
            </PanelBody>
          </Panel>
        </>
      ) : (
        wireguard.interfaces.map((tunnel) => (
          <TunnelBlock
            key={tunnel.name}
            tunnel={tunnel}
            onChanged={onChanged}
            onAdd={peers.add}
            onPeer={peers.open}
            onCreate={create}
          />
        ))
      )}
      {wireguard.installed && wireguard.interfaces.length > 0 && (
        <Modal
          open={creating || Boolean(initialReservationId)}
          onOpenChange={(open) => (open ? setCreating(true) : closeCreation())}
          title="Create WireGuard tunnel"
          description="WireGuard validates and creates its native tunnel. Selected shared plans recheck current ownership and overlap."
          size="lg"
        >
          <WireGuardSetup initialReservationId={initialReservationId} onCreated={created} />
        </Modal>
      )}
      {peers.sheets}
    </section>
  )
}

/**
 * One tunnel on the VPN page: the line it opens on, its picture straight
 * under it, and its internet exit. A column of the picture draws six peers
 * and sends the rest to the tunnel's own page, which draws them all.
 */
function TunnelBlock({
  tunnel,
  onChanged,
  onAdd,
  onPeer,
  onCreate,
}: {
  tunnel: WGInterface
  onChanged: () => void
  onAdd: (tunnel: string, kind: "device" | "site") => void
  onPeer: (tunnel: string, peer: WGPeer) => void
  onCreate: () => void
}) {
  const control = useTunnelControl(tunnel, onChanged)
  const add = useCallback(
    (kind: "device" | "site") => onAdd(tunnel.name, kind),
    [onAdd, tunnel.name],
  )
  const open = useCallback((peer: WGPeer) => onPeer(tunnel.name, peer), [onPeer, tunnel.name])
  return (
    <div data-slot="tunnel" className="flex min-w-0 flex-col gap-6">
      <TunnelIdentity tunnel={tunnel} control={control} onAdd={add} onCreate={onCreate} linked />
      <TunnelPicture tunnel={tunnel} onPeer={open} onAdd={add} max={PEERS_DRAWN} />
      <TunnelExit tunnel={tunnel} control={control} />
      {control.dialog}
    </div>
  )
}
