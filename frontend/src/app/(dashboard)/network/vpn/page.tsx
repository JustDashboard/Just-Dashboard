"use client"

import { Suspense, useCallback, useEffect, useState } from "react"
import { useSearchParams } from "next/navigation"
import { Tabs as TabsPrimitive } from "radix-ui"
import { LockClosed, RefreshClockwise } from "@/components/icons"
import { get } from "@/lib/api"
import { plural } from "@/lib/format"
import type { VPNView, WGInterface, WGPeer, WireGuardView } from "@/lib/types"
import { useViewState } from "@/lib/view-state"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { SaveBarProvider } from "@/components/deploy/settings/save-bar"
import { FactDot, HostFact, HostIdentity } from "@/components/metrics/host-identity"
import { Modal } from "@/components/modal"
import { Page, PageContext } from "@/components/page"
import { ProductGlyph } from "@/components/product-logo"
import { WireMark } from "@/components/deploy/wire"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { EmptyState, ErrorState, LoadingRows, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { ChipCount, tabClasses } from "@/components/tabs"
import { Tabs } from "@/components/ui/tabs"
import { VerbMenu } from "@/components/verbs"
import { WireFlow } from "@/components/wire-flow"
import { IdentityFacts, IdentityStateLoading } from "@/components/network/identity-facts"
import { InstallFollowUp, InstallHandoff } from "@/components/network/install"
import { NetworkReadWarning } from "@/components/network/read-warning"
import { ArchivedTunnels } from "@/components/network/vpn/archive"
import { usePeerSheets } from "@/components/network/vpn/peers"
import { WireGuardSetup } from "@/components/network/vpn/setup"
import { Headscale, TailscaleBlock, TailscaleMissing } from "@/components/network/vpn/tailscale"
import { TunnelExit } from "@/components/network/vpn/tunnel-exit"
import {
  IdentitySkeleton,
  TunnelIdentity,
  useTunnelControl,
} from "@/components/network/vpn/tunnel-identity"
import { PEERS_DRAWN, TunnelPicture } from "@/components/network/vpn/tunnel-picture"
import { tailnetState } from "@/components/network/vpn/tunnel-reading"
import {
  VPN_VIEW_NAME,
  runsHeadscale,
  vpnReading,
  vpnViewOf,
  vpnViews,
  type VPNViewKey,
} from "@/components/network/vpn/views"
import { VPNLog } from "@/components/network/vpn/vpn-log"

/**
 * The tunnels into and out of this server, one kind of VPN at a time.
 *
 * It was every kind down one column: each WireGuard tunnel with its picture,
 * then Tailscale with what it offers and every machine, then Headscale's
 * nodes, then the log. Each is a view named in the address (`?tab=`) now, as
 * DNS, Routing and Traffic have theirs:
 *
 * - **WireGuard**, the one the dashboard runs: each tunnel's line (who is
 *   online, its network, its port, Add a device) with its picture straight
 *   under it — the devices that dial in, the tunnel, the sites it joins, every
 *   wire moving while its peer is connected — and whether its clients'
 *   internet leaves through here; then the tunnels removed from here. A
 *   tunnel's record is its own page (`/network/vpn/<name>`). A server with no
 *   tunnel opens on the one-step setup; one without the tools on their install.
 * - **Tailscale**: this server's place on the tailnet, what it offers, and
 *   every other machine with how it is reached.
 * - **Headscale**, where this server runs one: its users and its nodes.
 * - **Log**: what the tunnels' units, tailscaled and Headscale have written.
 *
 * The page opens on the VPNs as one thing (how many tunnels are up, whether
 * the server is on its tailnet, each kind in a few words), then the strip.
 * Each view opens on what it is about, with no row of figures over it: a
 * tunnel, the tailnet and Headscale each have their own identity line. With
 * no `?tab=` the page opens on WireGuard, as it always began with it.
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

const GLYPH: Partial<Record<VPNViewKey, string>> = {
  wireguard: "wireguard",
  tailscale: "tailscale",
  headscale: "headscale",
}

const PANEL = "min-w-0 pt-6 focus-ring data-[state=inactive]:hidden"
/** A view's blocks, a band of ground apart, as the page's own are. */
const BLOCKS = "flex min-w-0 flex-col gap-8"

function NetworkVPNContent() {
  const { can } = useAuth()
  const admin = can("system.admin")
  const search = useSearchParams()
  const vpn = usePoll<VPNView>((signal) => get("/network/vpn", undefined, signal), 10_000, [], {
    enabled: admin,
  })
  const data = vpn.data

  // Whether this server runs a Headscale, as the last visit found it: the
  // strip has its tabs before the read lands.
  const [held, setHeld] = useViewState("network.vpn.headscale", false)
  const headscale = data ? runsHeadscale(data) : held
  useEffect(() => {
    if (data && held !== headscale) setHeld(headscale)
  }, [data, held, headscale, setHeld])

  const reservation = /^[a-f0-9]{32}$/.test(search.get("ipamReservation") ?? "")
  const view = vpnViewOf(search.get("tab"), headscale, reservation)
  // The tailnet's offers are a draft under one save bar: once opened, the
  // view stays in the document so a look at a tunnel does not throw it away.
  const [opened, setOpened] = useState<VPNViewKey[]>([view])
  if (!opened.includes(view)) setOpened([...opened, view])

  // Replaced rather than pushed: a view is not a place Back should step through.
  const show = useCallback((next: string) => {
    const url = new URL(window.location.href)
    if (next === "wireguard") url.searchParams.delete("tab")
    else url.searchParams.set("tab", next)
    // A planned range is asked for once: choosing a view is leaving its form.
    url.searchParams.delete("ipamReservation")
    window.history.replaceState(null, "", `${url.pathname}${url.search}`)
  }, [])

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

  const count: Partial<Record<VPNViewKey, number>> = data
    ? {
        wireguard: data.wireguard.interfaces.length,
        tailscale: data.tailscale.peers.length,
        headscale: data.headscale.nodes.length,
      }
    : {}
  const failed = !data && Boolean(vpn.error)

  return (
    <SaveBarProvider>
      <Page className="animate-rise">
        <PageContext eyebrow="Network" title="VPN" />
        {/* One block, as on a process's page: the strip's hairline is the line
            under the identity, rather than a second one a band of ground away. */}
        <div className="flex min-w-0 flex-col gap-5">
          <Identity vpn={data} failed={failed} headscale={headscale} onRefresh={vpn.refresh} />
          {data && (
            <NetworkReadWarning
              error={vpn.error}
              refresh={vpn.refresh}
              lastSuccess={vpn.lastSuccess}
            />
          )}
          <Tabs value={view} onValueChange={show} className="min-w-0 gap-0">
            <TabsPrimitive.List
              aria-label="Kinds of VPN"
              className="flex shrink-0 [scrollbar-width:none] gap-1 overflow-x-auto border-b border-hairline [&::-webkit-scrollbar]:hidden"
            >
              {vpnViews(headscale).map((key) => (
                <TabsPrimitive.Trigger
                  key={key}
                  value={key}
                  className={tabClasses(view === key, "h-10")}
                >
                  {GLYPH[key] && <ProductGlyph id={GLYPH[key]} />}
                  {VPN_VIEW_NAME[key]}
                  {(count[key] ?? 0) > 0 && <ChipCount>{count[key]}</ChipCount>}
                </TabsPrimitive.Trigger>
              ))}
            </TabsPrimitive.List>

            {failed ? (
              <TabsPrimitive.Content value={view} className={PANEL}>
                <ErrorState error={vpn.error!} onRetry={vpn.refresh} retryable />
              </TabsPrimitive.Content>
            ) : !data ? (
              // The view's own shape while it is read: its line and its picture
              // stand where they will, so nothing moves when the answer lands.
              <TabsPrimitive.Content value={view} className={PANEL} aria-busy>
                <Waiting view={view} />
              </TabsPrimitive.Content>
            ) : (
              <>
                <TabsPrimitive.Content value="wireguard" className={PANEL}>
                  <div className={BLOCKS}>
                    <WireGuard wireguard={data.wireguard} onChanged={vpn.refresh} />
                    {data.wireguard.installed && <ArchivedTunnels onRestored={vpn.refresh} />}
                  </div>
                </TabsPrimitive.Content>

                {/* Kept in the document once opened, and hidden by hand: Radix
                  shows a force-mounted panel whichever tab is chosen. */}
                <TabsPrimitive.Content
                  value="tailscale"
                  forceMount
                  hidden={view !== "tailscale"}
                  className={PANEL}
                >
                  {opened.includes("tailscale") &&
                    (data.tailscale.error ? (
                      <Notice title="Tailscale could not be read">{data.tailscale.error}</Notice>
                    ) : !data.tailscale.installed ? (
                      <TailscaleMissing onInstalled={vpn.refresh} />
                    ) : (
                      <TailscaleBlock tailscale={data.tailscale} onChanged={vpn.refresh} />
                    ))}
                </TabsPrimitive.Content>

                {headscale && (
                  <TabsPrimitive.Content value="headscale" className={PANEL}>
                    <Headscale headscale={data.headscale} />
                  </TabsPrimitive.Content>
                )}

                <TabsPrimitive.Content value="log" className={PANEL}>
                  <VPNLog vpn={data} />
                </TabsPrimitive.Content>
              </>
            )}
          </Tabs>
        </div>
      </Page>
    </SaveBarProvider>
  )
}

/**
 * The line the page opens on, whichever view is open: the VPNs on this server
 * as one thing, with how many tunnels are up and whether it is on its tailnet
 * beside the name, then each kind in a few words under its own mark. It is
 * drawn from the first frame, so the read fills the line rather than moving
 * the strip under it.
 */
function Identity({
  vpn,
  failed,
  headscale,
  onRefresh,
}: {
  vpn?: VPNView
  failed: boolean
  /** This server runs a Headscale, as far as the last visit knew. */
  headscale: boolean
  onRefresh: () => void
}) {
  const menu = (
    <VerbMenu
      label="VPN actions"
      verbs={[{ key: "refresh", label: "Refresh", icon: RefreshClockwise, run: onRefresh }]}
    />
  )
  if (!vpn)
    return (
      <HostIdentity
        fallback={LockClosed}
        className="border-b-0 pb-0"
        label="About the VPNs"
        title="VPN"
        state={failed ? <Status tone="danger" label="Not read" /> : <IdentityStateLoading />}
        facts={
          <IdentityFacts loading={!failed}>
            <HostFact>WireGuard and Tailscale on this server</HostFact>
          </IdentityFacts>
        }
        menu={menu}
      />
    )
  const reading = vpnReading(vpn)
  const tunnels = vpn.wireguard.interfaces
  const peers = tunnels.flatMap((tunnel) => tunnel.peers)
  const tailnet = tailnetState(vpn.tailscale)
  return (
    <HostIdentity
      fallback={LockClosed}
      className="border-b-0 pb-0"
      label="About the VPNs"
      title="VPN"
      state={<Status tone={reading.tone} label={reading.label} />}
      facts={
        <IdentityFacts>
          <HostFact product="wireguard">
            {!vpn.wireguard.installed
              ? "not installed"
              : tunnels.length === 0
                ? "no tunnel yet"
                : `${plural(tunnels.length, "tunnel")} · ${peers.filter((p) => p.online).length} of ${plural(peers.length, "peer")} online`}
          </HostFact>
          <FactDot />
          <HostFact product="tailscale">
            {!vpn.tailscale.installed
              ? "not installed"
              : `${vpn.tailscale.tailnet || "tailnet"} · ${tailnet.label.toLowerCase()}`}
          </HostFact>
          {headscale && (
            <>
              <FactDot />
              <HostFact product="headscale">{plural(vpn.headscale.nodes.length, "node")}</HostFact>
            </>
          )}
        </IdentityFacts>
      }
      menu={menu}
    />
  )
}

/** A view before the first read: the line it opens on, and the block under it. */
function Waiting({ view }: { view: VPNViewKey }) {
  if (view === "wireguard")
    return (
      <section aria-label="WireGuard" aria-busy className="flex min-w-0 flex-col gap-6">
        <IdentitySkeleton mark="wireguard" />
        <WireFlow
          loading
          start={{ title: "Devices", nodes: [] }}
          end={{ title: "Sites", nodes: [], loadingRows: 2 }}
          hub={{
            mark: (
              <WireMark tone="neutral" shape="square">
                <ProductGlyph id="wireguard" />
              </WireMark>
            ),
          }}
        />
      </section>
    )
  if (view === "log") return <LoadingRows rows={6} />
  return (
    <section aria-label={VPN_VIEW_NAME[view]} aria-busy className="flex min-w-0 flex-col gap-6">
      <IdentitySkeleton mark={view} />
      <LoadingRows rows={4} />
    </section>
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
  /** Undefined for a session that may not add a peer. */
  onAdd?: (tunnel: string, kind: "device" | "site") => void
  onPeer: (tunnel: string, peer: WGPeer) => void
  onCreate: () => void
}) {
  const control = useTunnelControl(tunnel, onChanged)
  const add = useCallback(
    (kind: "device" | "site") => onAdd?.(tunnel.name, kind),
    [onAdd, tunnel.name],
  )
  const open = useCallback((peer: WGPeer) => onPeer(tunnel.name, peer), [onPeer, tunnel.name])
  return (
    <div data-slot="tunnel" className="flex min-w-0 flex-col gap-6">
      <TunnelIdentity
        tunnel={tunnel}
        control={control}
        onAdd={onAdd ? add : undefined}
        onCreate={onCreate}
        linked
      />
      <TunnelPicture
        tunnel={tunnel}
        onPeer={open}
        onAdd={onAdd ? add : undefined}
        max={PEERS_DRAWN}
      />
      <TunnelExit tunnel={tunnel} control={control} />
      {control.dialog}
    </div>
  )
}
