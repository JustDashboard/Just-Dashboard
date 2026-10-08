"use client"

import { NetworkReadWarning } from "@/components/network/read-warning"
import { useState } from "react"
import { useAuth } from "@/hooks/use-auth"
import { Plus, Trash } from "@/components/icons"
import { del, get, post } from "@/lib/api"
import { bytes, plural } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { NetworkOverview, VPNView, WGInterface, WGPeer } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { Page, PageContext, Section } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { EmptyState, ErrorState, LoadingPanel, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { NumberTicker } from "@/components/ui/number-ticker"
import { Button } from "@/components/ui/button"
import { Switch } from "@/components/ui/switch"
import { useConfirm } from "@/components/confirm-dialog"
import { ProductGlyph, ProductLogos } from "@/components/product-logo"
import { InstallHandoff } from "@/components/network/install"
import { TunnelPicture } from "@/components/network/vpn/tunnel-picture"
import { AddPeer, PeerSheet } from "@/components/network/vpn/peers"
import { WireGuardSetup } from "@/components/network/vpn/setup"
import { WireGuardFamilyEvidence } from "@/components/network/vpn/family-evidence"
import { TailscaleBlock } from "@/components/network/vpn/tailscale"

/**
 * The tunnels into and out of this server.
 *
 * WireGuard first, because it is the one the dashboard runs: each tunnel is
 * its picture — the devices that dial in, the tunnel, the sites it joins,
 * every wire moving while its peer is connected — with its switches in its
 * head (up, exit node) and every peer a press away from its QR code. A
 * server with no tunnel opens on the one-step setup; one without the tools
 * on their install. Then Tailscale: this server's place on the tailnet, what
 * it offers, and every other machine with how it is reached; Headscale's
 * nodes under it where Headscale runs.
 *
 * Every read here names who connects, so the page is an administrator's,
 * as the SSH configuration is.
 */
export default function NetworkVPNPage() {
  const { can } = useAuth()
  const admin = can("system.admin")
  const vpn = usePoll<VPNView>((signal) => get("/network/vpn", undefined, signal), 10_000, [], {
    enabled: admin,
  })
  const overview = usePoll<NetworkOverview>(
    (signal) => get("/network/overview", undefined, signal),
    60_000,
  )
  const [adding, setAdding] = useState<{ tunnel: string; kind: "device" | "site" }>()
  const [opened, setOpened] = useState<{ tunnel: string; key: string }>()

  if (!admin) {
    return (
      <Page>
        <PageContext eyebrow="Network" title="VPN" />
        <EmptyState
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
        {vpn.error ? <ErrorState error={vpn.error} onRetry={vpn.refresh} /> : <LoadingPanel />}
      </Page>
    )
  }
  const { wireguard, tailscale, headscale } = vpn.data
  const peers = wireguard.interfaces.flatMap((t) => t.peers)
  const online = peers.filter((p) => p.online).length
  const moved = peers.reduce((n, p) => n + p.rxBytes + p.txBytes, 0)
  const tsOnline = tailscale.peers.filter((p) => p.online).length
  const exits = [
    ...wireguard.interfaces.filter((t) => t.exitNode).map((t) => t.name),
    ...(tailscale.prefs.advertisingExitNode ? ["Tailscale"] : []),
  ]
  const localNetworks = [
    ...(overview.data?.dockerNetworks.flatMap((n) => n.subnets) ?? []),
    ...(overview.data?.links
      .filter((l) => l.managed && l.role === "bridge")
      .flatMap((l) => l.addresses.map((a) => network(a.cidr))) ?? []),
  ]
  const addingTunnel = wireguard.interfaces.find((t) => t.name === adding?.tunnel)
  const openTunnel = wireguard.interfaces.find((t) => t.name === opened?.tunnel)
  const openPeer = openTunnel?.peers.find((p) => p.publicKey === opened?.key)

  return (
    <Page className="animate-rise">
      <PageContext eyebrow="Network" title="VPN" />
      {vpn.data && (
        <NetworkReadWarning error={vpn.error} refresh={vpn.refresh} lastSuccess={vpn.lastSuccess} />
      )}

      <StatGrid columns={4}>
        <StatTile
          label={
            <>
              <ProductGlyph id="wireguard" className="mr-1.5 inline-block size-3 align-[-1.5px]" />
              WireGuard
            </>
          }
          value={
            wireguard.interfaces.length === 0 ? (
              "No tunnel"
            ) : (
              <>
                <NumberTicker value={online} />
                <span className="text-muted-foreground"> / {peers.length}</span>
              </>
            )
          }
          trailing={wireguard.interfaces.length ? "peers online" : undefined}
          hint={
            wireguard.interfaces.length
              ? `${plural(wireguard.interfaces.length, "tunnel")} · ${wireguard.interfaces.map((t) => t.name).join(", ")}`
              : wireguard.installed
                ? "one step to set up"
                : "wireguard-tools is not installed"
          }
        />
        <StatTile
          label={
            <>
              <ProductGlyph id="tailscale" className="mr-1.5 inline-block size-3 align-[-1.5px]" />
              Tailnet
            </>
          }
          value={
            !tailscale.installed ? (
              "Not here"
            ) : (
              <>
                <NumberTicker value={tsOnline} />
                <span className="text-muted-foreground"> / {tailscale.peers.length}</span>
              </>
            )
          }
          trailing={tailscale.installed ? "online" : undefined}
          hint={
            tailscale.tailnet ||
            (tailscale.installed ? tailscale.backendState : "Tailscale is not installed")
          }
        />
        <StatTile
          label="Exit"
          value={exits.length ? exits.join(" · ") : "None"}
          hint={
            exits.length
              ? "configured exit offers; provider path untested"
              : "this server routes no client's internet"
          }
        />
        <StatTile
          label="Moved"
          value={bytes(moved)}
          hint={`through WireGuard since each tunnel came up`}
        />
      </StatGrid>

      <Section title="WireGuard">
        {wireguard.error && <Notice title="WireGuard could not be read">{wireguard.error}</Notice>}
        {!wireguard.installed ? (
          <InstallHandoff
            pkg="wireguard-tools"
            products={["wireguard"]}
            title="WireGuard is not installed"
            description="The kernel has WireGuard built in; the tools that configure it are one package. Once it is in, a tunnel is one step: devices get a QR code, other sites a file."
            onInstalled={vpn.refresh}
          />
        ) : wireguard.interfaces.length === 0 ? (
          <Panel plain>
            <PanelHeader title="Set up a tunnel" />
            <PanelBody>
              <WireGuardSetup onCreated={() => vpn.refresh()} />
            </PanelBody>
          </Panel>
        ) : (
          wireguard.interfaces.map((tunnel) => (
            <TunnelBlock
              key={tunnel.name}
              tunnel={tunnel}
              onChanged={vpn.refresh}
              onAdd={(kind) => setAdding({ tunnel: tunnel.name, kind })}
              onPeer={(peer) => setOpened({ tunnel: tunnel.name, key: peer.publicKey })}
            />
          ))
        )}
      </Section>

      <Section title="Tailscale">
        {tailscale.error ? (
          <Notice title="Tailscale could not be read">{tailscale.error}</Notice>
        ) : !tailscale.installed ? (
          <EmptyState
            title="Tailscale is not installed"
            mark={<ProductLogos ids={["tailscale", "headscale"]} />}
            description="Tailscale joins this server to a private network of your devices without opening a port. Install it from tailscale.com/download, then sign in once; its peers, exit node and subnet routes appear here."
          />
        ) : (
          <TailscaleBlock tailscale={tailscale} headscale={headscale} onChanged={vpn.refresh} />
        )}
      </Section>

      {addingTunnel && adding && (
        <AddPeer
          tunnel={addingTunnel}
          kind={adding.kind}
          open
          onOpenChange={(open) => !open && setAdding(undefined)}
          onAdded={vpn.refresh}
          localNetworks={localNetworks}
        />
      )}
      <PeerSheet
        tunnel={openTunnel}
        peer={openPeer}
        onOpenChange={(open) => !open && setOpened(undefined)}
        onChanged={vpn.refresh}
      />
    </Page>
  )
}

/** A network address from an interface address: 192.168.50.1/24 is 192.168.50.0/24. */
function network(cidr: string) {
  const [address, bits] = cidr.split("/")
  const n = Number(bits)
  const parts = address.split(".").map(Number)
  if (parts.length !== 4 || !Number.isInteger(n)) return cidr
  const value = parts.reduce((v, p) => (v << 8) | p, 0) >>> 0
  const masked = n === 0 ? 0 : (value & (~0 << (32 - n))) >>> 0
  return `${[24, 16, 8, 0].map((s) => (masked >>> s) & 255).join(".")}/${n}`
}

/**
 * One tunnel: its state and switches in the head, its picture, and its
 * peers as the picture's nodes. A hand-written tunnel is drawn the same way
 * and says it is read-only here.
 */
function TunnelBlock({
  tunnel,
  onChanged,
  onAdd,
  onPeer,
}: {
  tunnel: WGInterface
  onChanged: () => void
  onAdd: (kind: "device" | "site") => void
  onPeer: (peer: WGPeer) => void
}) {
  const { confirm, dialog } = useConfirm()
  const [busy, setBusy] = useState(false)
  const base = `/network/vpn/wireguard/${encodeURIComponent(tunnel.name)}`
  const act = async (run: () => Promise<unknown>, label: string) => {
    setBusy(true)
    try {
      await run()
      notify.success(label)
      onChanged()
    } catch (err) {
      notify.error(`${tunnel.name} was not changed`, err)
    } finally {
      setBusy(false)
    }
  }
  const down = () =>
    confirm({
      title: `Take ${tunnel.name} down`,
      confirmLabel: "Take down",
      description: <p>Every device and site on it is disconnected until it is brought up again.</p>,
      action: async () => {
        await post(`${base}/down`)
        notify.success(`${tunnel.name} is down`)
        onChanged()
      },
    })
  const remove = () =>
    confirm({
      title: `Remove ${tunnel.name}`,
      confirmLabel: "Remove",
      description: (
        <p>
          The tunnel stops, its peers are cut off and it is not started at boot. Its file is moved
          aside rather than deleted, so its key is not lost if this was a mistake.
        </p>
      ),
      action: async () => {
        const result = await del<{ firewall: { removed: boolean; reason?: string } }>(base)
        notify.success(`${tunnel.name} removed`, {
          description: result.firewall?.removed
            ? "Its dashboard-created firewall opening was removed."
            : result.firewall?.reason,
        })
        onChanged()
      },
    })
  const setExit = (on: boolean) => {
    const apply = () =>
      act(
        () => post(`${base}/exit`, { on }),
        on ? `${tunnel.name} has IPv4 exit rules` : `${tunnel.name} is a private network only`,
      )
    if (on) return void apply()
    confirm({
      title: `Stop using ${tunnel.name} as an exit node`,
      confirmLabel: "Turn off",
      description: (
        <p>
          Clients using this tunnel for internet access lose that access until they change their
          configuration or this exit is enabled again.
        </p>
      ),
      action: async () => {
        await post(`${base}/exit`, { on: false })
        notify.success(`${tunnel.name} is a private network only`)
        onChanged()
      },
    })
  }
  const setIPv6Exit = (on: boolean) => {
    if (on)
      return void act(
        () => post(`${base}/exit`, { on: true, ipv6: true }),
        `${tunnel.name} has IPv4 and IPv6 exit rules`,
      )
    confirm({
      title: `Turn off ${tunnel.name}'s IPv6 exit`,
      confirmLabel: "Turn off IPv6",
      description: "Full-tunnel clients lose IPv6 internet access. IPv4 exit remains configured.",
      action: async () => {
        await post(`${base}/exit`, { on: true, ipv6: false })
        onChanged()
      },
    })
  }
  const online = tunnel.peers.filter((p) => p.online).length
  return (
    <Panel plain>
      <PanelHeader
        title={
          <span className="inline-flex items-center gap-2">
            <ProductGlyph id="wireguard" />
            <span className="font-mono">{tunnel.name}</span>
          </span>
        }
        actions={
          <span className="flex flex-wrap items-center gap-3">
            <Status
              tone={tunnel.up ? "running" : "stopped"}
              label={tunnel.up ? `${online} of ${tunnel.peers.length} online` : "Down"}
            />
            {tunnel.managed && (
              <>
                <label className="flex items-center gap-2 text-hint text-muted-foreground">
                  IPv4 exit
                  <Switch
                    checked={tunnel.exitNode}
                    disabled={
                      busy ||
                      (!tunnel.exitNode && tunnel.families?.ipv4.exit.capability.writable === false)
                    }
                    onCheckedChange={setExit}
                    aria-label={`${tunnel.name} as an exit node`}
                  />
                </label>
                {tunnel.ipv6Enabled && (
                  <label className="flex items-center gap-2 text-hint text-muted-foreground">
                    IPv6 exit
                    <Switch
                      checked={Boolean(tunnel.families?.ipv6.exit.configured)}
                      disabled={
                        busy ||
                        (!tunnel.families?.ipv6.exit.configured &&
                          tunnel.families?.ipv6.exit.capability.writable === false)
                      }
                      onCheckedChange={setIPv6Exit}
                      aria-label={`${tunnel.name} IPv6 exit`}
                    />
                  </label>
                )}
                {tunnel.up ? (
                  <Button size="xs" variant="outline" onClick={down} disabled={busy}>
                    Take down
                  </Button>
                ) : (
                  <Button
                    size="xs"
                    variant="outline"
                    onClick={() => void act(() => post(`${base}/up`), `${tunnel.name} is up`)}
                    disabled={busy}
                  >
                    Bring up
                  </Button>
                )}
                <Button size="xs" variant="outline" onClick={() => onAdd("device")} disabled={busy}>
                  <Plus aria-hidden />
                  Device
                </Button>
                <Button
                  size="icon-xs"
                  variant="ghost"
                  onClick={remove}
                  aria-label={`Remove ${tunnel.name}`}
                >
                  <Trash aria-hidden />
                </Button>
              </>
            )}
          </span>
        }
      />
      <PanelBody>
        <WireGuardFamilyEvidence tunnel={tunnel} />
        {!tunnel.managed && (
          <Notice title="Written by hand">
            This tunnel&rsquo;s file was not written here, so it is read and never changed.
          </Notice>
        )}
        <TunnelPicture tunnel={tunnel} onPeer={onPeer} onAdd={onAdd} />
      </PanelBody>
      {dialog}
    </Panel>
  )
}
