"use client"

import { useState } from "react"
import { del, get, post } from "@/lib/api"
import { bytes, relativeTime } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { WGInterface, WGPeer, WGPeerCreated } from "@/lib/types"
import { Modal } from "@/components/modal"
import { SidePanel } from "@/components/side-panel"
import { Field, FieldRow } from "@/components/form"
import { Detail, DetailList } from "@/components/page"
import { Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Switch } from "@/components/ui/switch"
import { useConfirm } from "@/components/confirm-dialog"
import { ClientConfig } from "@/components/network/vpn/client-config"

const list = (raw: string) =>
  raw
    .split(/[\s,]+/)
    .map((s) => s.trim())
    .filter(Boolean)

/**
 * A new peer on a tunnel: a device (a phone or a laptop dialling in) or a
 * site (another server or an office whose network the tunnel joins). The
 * server makes the keys, picks the next address, writes the peer into the
 * tunnel's file and syncs it live; the answer is the client's configuration
 * and its QR code, shown in place of the form so the phone can be pointed at
 * it before the dialog closes.
 */
export function AddPeer({
  tunnel,
  kind,
  open,
  onOpenChange,
  onAdded,
  localNetworks,
}: {
  tunnel: WGInterface
  kind: "device" | "site"
  open: boolean
  onOpenChange: (open: boolean) => void
  onAdded: () => void
  /** Networks on this server a peer may be offered: Docker's, the dashboard's bridges. */
  localNetworks: string[]
}) {
  const [name, setName] = useState("")
  const [fullTunnel, setFullTunnel] = useState(tunnel.exitNode)
  const [share, setShare] = useState("")
  const [remote, setRemote] = useState("")
  const [endpoint, setEndpoint] = useState("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()
  const [made, setMade] = useState<WGPeerCreated>()

  const reset = () => {
    setName("")
    setShare("")
    setRemote("")
    setEndpoint("")
    setError(undefined)
    setMade(undefined)
  }
  const ready = name.trim() && (kind === "device" || list(remote).length > 0)
  const submit = async () => {
    setBusy(true)
    setError(undefined)
    try {
      const created = await post<WGPeerCreated>(
        `/network/vpn/wireguard/${encodeURIComponent(tunnel.name)}/peers`,
        {
          name: name.trim(),
          kind,
          fullTunnel: kind === "device" ? fullTunnel : undefined,
          shareNetworks: list(share),
          remoteNetworks: kind === "site" ? list(remote) : undefined,
          endpoint: kind === "site" && endpoint.trim() ? endpoint.trim() : undefined,
        },
      )
      setMade(created)
      for (const w of created.warnings) notify.warning(w)
      onAdded()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }
  return (
    <Modal
      open={open}
      onOpenChange={(next) => {
        onOpenChange(next)
        if (!next) reset()
      }}
      size="lg"
      title={
        made
          ? `${made.peer.name} is on ${tunnel.name}`
          : kind === "device"
            ? "Add a device"
            : "Join a site"
      }
      description={
        kind === "device"
          ? "A phone or laptop that dials into this tunnel"
          : "Another network joined through this tunnel"
      }
      footer={
        made ? (
          <Button onClick={() => onOpenChange(false)}>Done</Button>
        ) : (
          <>
            <Button variant="outline" onClick={() => onOpenChange(false)} disabled={busy}>
              Cancel
            </Button>
            <Button onClick={submit} disabled={!ready || busy} pending={busy}>
              {kind === "device" ? "Add device" : "Join site"}
            </Button>
          </>
        )
      }
    >
      {made ? (
        <div className="flex flex-col gap-4">
          <ClientConfig name={made.peer.name} config={made.config} qr={made.qr} />
          {!made.reloaded && (
            <Notice title="Not live yet">
              The tunnel is down, so the peer is in its file and joins when the tunnel comes up.
            </Notice>
          )}
          <p className="text-hint text-muted-foreground">
            The configuration is kept sealed; open the peer again to show this code, or forget it
            once the device has it.
          </p>
        </div>
      ) : (
        <div className="flex flex-col gap-5">
          <Field
            label="Name"
            htmlFor="peer-name"
            hint={
              kind === "device"
                ? "Whose it is: Ana's phone, the office laptop"
                : "The site: Office, Backup server"
            }
          >
            <Input
              id="peer-name"
              value={name}
              placeholder={kind === "device" ? "Ana's phone" : "Office"}
              onChange={(event) => setName(event.target.value)}
            />
          </Field>
          {kind === "device" ? (
            <Field
              label="Send its internet traffic here"
              hint={
                tunnel.exitNode
                  ? tunnel.families?.ipv6.exit.configured
                    ? "IPv4 and IPv6 default routes use this server while the tunnel is up. Local routes can remain native; this is not a client kill switch."
                    : "IPv4 internet goes through this server. IPv6 is captured by the tunnel and has no exit until IPv6 egress is enabled."
                  : "Needs the tunnel to be an exit node; without it only this server's networks are reached."
              }
            >
              <label className="flex h-9 items-center gap-2 text-body">
                <Switch
                  checked={fullTunnel}
                  onCheckedChange={setFullTunnel}
                  aria-label="Full tunnel"
                />
                {fullTunnel ? "Full tunnel" : "Only this server's networks"}
              </label>
            </Field>
          ) : (
            <FieldRow>
              <Field
                label="Its networks"
                htmlFor="peer-remote"
                hint="The site's own networks, routed into the tunnel"
              >
                <Input
                  id="peer-remote"
                  value={remote}
                  placeholder="192.168.1.0/24"
                  onChange={(event) => setRemote(event.target.value)}
                  className="font-mono"
                />
              </Field>
              <Field
                label="Its address"
                htmlFor="peer-endpoint"
                hint="Optional: where this server dials it, host:port"
              >
                <Input
                  id="peer-endpoint"
                  value={endpoint}
                  placeholder="office.example.net:51820"
                  onChange={(event) => setEndpoint(event.target.value)}
                  className="font-mono"
                />
              </Field>
            </FieldRow>
          )}
          <Field
            label="Networks it may reach here"
            htmlFor="peer-share"
            hint={
              localNetworks.length
                ? `Besides the tunnel's own: ${localNetworks.slice(0, 4).join(", ")}${localNetworks.length > 4 ? "…" : ""}`
                : "Besides the tunnel's own, comma separated"
            }
          >
            <Input
              id="peer-share"
              value={share}
              placeholder={localNetworks[0] ?? "10.0.4.0/24"}
              onChange={(event) => setShare(event.target.value)}
              className="font-mono"
            />
          </Field>
          {error && (
            <p role="alert" className="animate-rise text-body text-destructive">
              {error}
            </p>
          )}
        </div>
      )}
    </Modal>
  )
}

/**
 * One peer: whether it is connected, what it has moved, the networks it is
 * given, and its configuration — shown again from the sealed copy, or
 * forgotten once the device has it. Removing it revokes the key at once.
 */
export function PeerSheet({
  tunnel,
  peer,
  onOpenChange,
  onChanged,
}: {
  tunnel: WGInterface | undefined
  peer: WGPeer | undefined
  onOpenChange: (open: boolean) => void
  onChanged: () => void
}) {
  const { confirm, dialog } = useConfirm()
  const [config, setConfig] = useState<{ config: string; qr: string }>()
  const [loading, setLoading] = useState(false)
  if (!tunnel || !peer) return dialog
  const base = `/network/vpn/wireguard/${encodeURIComponent(tunnel.name)}/peers/${peer.id}`
  const show = async () => {
    setLoading(true)
    try {
      setConfig(await get<{ name: string; config: string; qr: string }>(`${base}/config`))
    } catch (err) {
      notify.error("The configuration could not be shown", err)
    } finally {
      setLoading(false)
    }
  }
  const forget = () =>
    confirm({
      title: `Forget ${peer.name}'s configuration`,
      confirmLabel: "Forget",
      description: (
        <p>
          The device keeps working; only the copy kept here goes, so its QR code cannot be shown
          again. Add the device again to make a new one.
        </p>
      ),
      action: async () => {
        await del(`${base}/config`)
        setConfig(undefined)
        notify.success("Configuration forgotten")
        onChanged()
      },
    })
  const remove = () =>
    confirm({
      title: `Remove ${peer.name}`,
      confirmLabel: "Remove",
      description: (
        <p>
          Its key stops working now: the device or site is cut off from {tunnel.name} until it is
          added again with a new one.
        </p>
      ),
      action: async () => {
        await del(base)
        notify.success(`${peer.name} removed`)
        onOpenChange(false)
        onChanged()
      },
    })
  return (
    <>
      <SidePanel
        open
        onOpenChange={(next) => {
          onOpenChange(next)
          if (!next) setConfig(undefined)
        }}
        title={peer.name || peer.address}
        description={`The ${peer.name} peer on ${tunnel.name}`}
        actions={
          <>
            <Status
              tone={peer.online ? "running" : peer.latestHandshake ? "stopped" : "unknown"}
              label={peer.online ? "Online" : peer.latestHandshake ? "Quiet" : "Never connected"}
            />
            {peer.id > 0 && tunnel.managed && (
              <Button size="sm" variant="outline" className="ml-auto" onClick={remove}>
                Remove
              </Button>
            )}
          </>
        }
      >
        <div className="flex flex-col gap-6">
          <DetailList>
            <Detail label="Kind">{peer.kind === "site" ? "Site" : "Device"}</Detail>
            <Detail label="Address">
              <span className="font-mono">{peer.address}</span>
            </Detail>
            {peer.address6 && (
              <Detail label="IPv6 address">
                <span className="font-mono break-all">{peer.address6}</span>
              </Detail>
            )}
            <Detail label="Routes to it">
              <span className="font-mono">{peer.allowedIps.join(", ") || "—"}</span>
            </Detail>
            <Detail label="Last handshake">
              {peer.latestHandshake
                ? relativeTime(new Date(peer.latestHandshake * 1000).toISOString())
                : "never"}
            </Detail>
            {peer.endpoint && (
              <Detail label="Connects from">
                <span className="font-mono">{peer.endpoint}</span>
              </Detail>
            )}
            <Detail label="Moved">
              <span className="numeric">
                ↓ {bytes(peer.rxBytes)} · ↑ {bytes(peer.txBytes)}
              </span>
            </Detail>
            <Detail label="Public key">
              <span className="font-mono break-all">{peer.publicKey}</span>
            </Detail>
          </DetailList>

          {peer.id === 0 ? (
            <Notice title="Not made here">
              This peer is in the tunnel&rsquo;s own file; its configuration was never kept here.
            </Notice>
          ) : config ? (
            <ClientConfig name={peer.name} config={config.config} qr={config.qr} />
          ) : peer.hasConfig ? (
            <div className="flex flex-wrap gap-2">
              <Button size="sm" onClick={() => void show()} pending={loading} disabled={loading}>
                Show QR code
              </Button>
              <Button size="sm" variant="outline" onClick={forget}>
                Forget the configuration
              </Button>
            </div>
          ) : (
            <p className="text-hint text-muted-foreground">
              Its configuration was forgotten here; the device still has its own copy.
            </p>
          )}
        </div>
      </SidePanel>
      {dialog}
    </>
  )
}
