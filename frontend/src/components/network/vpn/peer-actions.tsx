"use client"

import { useState } from "react"
import { get, patch, post } from "@/lib/api"
import { notify } from "@/lib/toast"
import type { WGInterface, WGPeer, WGPeerEdited, WGSiteVerification } from "@/lib/types"
import { Modal } from "@/components/modal"
import { Field, FieldRow } from "@/components/form"
import { Row, RowList } from "@/components/row-list"
import { Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Switch } from "@/components/ui/switch"
import { Download } from "@/components/icons"
import { useConfirm } from "@/components/confirm-dialog"
import { ClientConfig } from "@/components/network/vpn/client-config"
import {
  type PeerDraft,
  editWithdraws,
  networkProblem,
  peerEdit,
  sharedOf,
  siteNetworks,
  siteOutcomeTone,
} from "./record-logic"

/** The tunnel's own networks, which a device's routes start from. */
export function tunnelNetworks(tunnel: WGInterface) {
  return [tunnel.subnet, tunnel.families?.ipv4.subnet, tunnel.families?.ipv6.subnet].filter(
    (n): n is string => Boolean(n),
  )
}

/**
 * Changing a peer after it exists. A name changes here and in the file; a
 * site's networks, endpoint and keepalive change on this server and are
 * routed or withdrawn live; a device's routes and keepalive are in its own
 * configuration, which is regenerated from the sealed copy — same keys — and
 * shown to import again. Taking a network away from a site is confirmed first.
 */
export function EditPeer({
  tunnel,
  peer,
  open,
  onOpenChange,
  onChanged,
}: {
  tunnel: WGInterface
  peer: WGPeer
  open: boolean
  onOpenChange: (open: boolean) => void
  onChanged: () => void
}) {
  const site = peer.kind === "site"
  const networks = tunnelNetworks(tunnel)
  const initial: PeerDraft = {
    name: peer.name,
    keepalive: String(peer.keepalive),
    endpoint: peer.endpoint ?? "",
    remote: siteNetworks(peer).join(", "),
    fullTunnel: peer.clientRoutes?.includes("0.0.0.0/0") ?? false,
    share: sharedOf(peer, networks).join(", "),
  }
  const [draft, setDraft] = useState(initial)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()
  const [made, setMade] = useState<WGPeerEdited>()
  const { confirm, dialog } = useConfirm()
  const base = `/network/vpn/wireguard/${encodeURIComponent(tunnel.name)}/peers/${peer.id}`
  const body = peerEdit(peer, draft, networks)
  const keepaliveProblem =
    /^\d{1,5}$/.test(draft.keepalive.trim()) && Number(draft.keepalive) <= 65535
      ? undefined
      : "Seconds from 0 (off) to 65535."
  const remoteProblem = site ? networkProblem(draft.remote) : undefined
  const shareProblem = !site ? networkProblem(draft.share) : undefined
  const ready = Object.keys(body).length > 0 && !keepaliveProblem && !remoteProblem && !shareProblem
  const reachesClient =
    "fullTunnel" in body || "shareNetworks" in body || (!site && "keepalive" in body)
  const send = async () => {
    setBusy(true)
    setError(undefined)
    try {
      const res = await patch<WGPeerEdited>(base, body)
      for (const w of res.warnings) notify.warning(w)
      notify.success(`${res.peer.name || peer.name} changed`)
      onChanged()
      if (res.clientChanged) setMade(res)
      else onOpenChange(false)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }
  const submit = () => {
    if (!editWithdraws(peer, body)) return void send()
    confirm({
      title: `Withdraw from ${peer.name}`,
      confirmLabel: "Change",
      description: (
        <p>
          Traffic to what is taken away, or through the old endpoint, stops at once. Anything that
          relies on it loses its route until it is added back.
        </p>
      ),
      action: send,
    })
  }
  return (
    <Modal
      open={open}
      onOpenChange={onOpenChange}
      size="lg"
      title={made ? `${peer.name}'s new configuration` : `Edit ${peer.name || peer.address}`}
      description={site ? "A site joined through this tunnel" : "A device that dials this tunnel"}
      footer={
        made ? (
          <Button onClick={() => onOpenChange(false)}>Done</Button>
        ) : (
          <>
            <Button variant="outline" onClick={() => onOpenChange(false)} disabled={busy}>
              Cancel
            </Button>
            <Button onClick={submit} disabled={!ready || busy} pending={busy}>
              Save changes
            </Button>
          </>
        )
      }
    >
      {made?.config && made.qr ? (
        <div className="flex flex-col gap-4">
          <ClientConfig name={made.peer.name || peer.name} config={made.config} qr={made.qr} />
          <p className="text-hint text-muted-foreground">
            Same keys, new routes: {peer.name} keeps its old configuration until this one is
            imported on it.
          </p>
        </div>
      ) : (
        <div className="flex flex-col gap-5">
          <FieldRow>
            <Field label="Name" htmlFor="edit-peer-name">
              <Input
                id="edit-peer-name"
                value={draft.name}
                onChange={(event) => setDraft({ ...draft, name: event.target.value })}
              />
            </Field>
            <Field
              label="Keepalive"
              htmlFor="edit-peer-keepalive"
              hint={
                site
                  ? "Seconds; this server keeps the site's session alive"
                  : "Seconds; in the device's own configuration"
              }
              error={keepaliveProblem}
            >
              <Input
                id="edit-peer-keepalive"
                inputMode="numeric"
                value={draft.keepalive}
                aria-invalid={Boolean(keepaliveProblem)}
                onChange={(event) => setDraft({ ...draft, keepalive: event.target.value })}
              />
            </Field>
          </FieldRow>
          {site ? (
            <FieldRow>
              <Field
                label="Its networks"
                htmlFor="edit-peer-remote"
                hint="Routed into the tunnel on this server"
                error={remoteProblem}
              >
                <Input
                  id="edit-peer-remote"
                  value={draft.remote}
                  aria-invalid={Boolean(remoteProblem)}
                  onChange={(event) => setDraft({ ...draft, remote: event.target.value })}
                  className="font-mono"
                />
              </Field>
              <Field
                label="Its address"
                htmlFor="edit-peer-endpoint"
                hint="Where this server dials it; empty for a site that dials in"
              >
                <Input
                  id="edit-peer-endpoint"
                  value={draft.endpoint}
                  placeholder="office.example.net:51820"
                  onChange={(event) => setDraft({ ...draft, endpoint: event.target.value })}
                  className="font-mono"
                />
              </Field>
            </FieldRow>
          ) : (
            <>
              <Field
                label="Send its internet traffic here"
                hint={
                  peer.clientRoutes
                    ? "Its own configuration's routes, regenerated with the same keys."
                    : "Its routes were not recorded when it was made; saving sets both of these."
                }
              >
                <label className="flex h-9 items-center gap-2 text-body">
                  <Switch
                    checked={draft.fullTunnel}
                    onCheckedChange={(on) => setDraft({ ...draft, fullTunnel: on })}
                    aria-label="Full tunnel"
                  />
                  {draft.fullTunnel ? "Full tunnel" : "Only this server's networks"}
                </label>
              </Field>
              <Field
                label="Networks it may reach here"
                htmlFor="edit-peer-share"
                hint="Besides the tunnel's own, comma separated"
                error={shareProblem}
              >
                <Input
                  id="edit-peer-share"
                  value={draft.share}
                  aria-invalid={Boolean(shareProblem)}
                  onChange={(event) => setDraft({ ...draft, share: event.target.value })}
                  className="font-mono"
                />
              </Field>
            </>
          )}
          {reachesClient && !peer.hasConfig && (
            <Notice tone="warning" title="Its saved configuration was forgotten">
              This change is in the peer&rsquo;s own configuration, which cannot be regenerated
              without the copy kept here. Remove the peer and add it again instead.
            </Notice>
          )}
          {error && (
            <p role="alert" className="animate-rise text-body text-destructive">
              {error}
            </p>
          )}
        </div>
      )}
      {dialog}
    </Modal>
  )
}

/**
 * A site checked from this end: a fresh handshake, its networks routed into
 * the tunnel, its transport outside every tunnel, an answer from its tunnel
 * address and, with a host inside its networks, a round trip into its LAN.
 * The far side's own checks are listed for whoever runs that router.
 */
export function SiteVerify({ tunnel, peer }: { tunnel: WGInterface; peer: WGPeer }) {
  const [target, setTarget] = useState("")
  const [busy, setBusy] = useState(false)
  const [result, setResult] = useState<WGSiteVerification>()
  const [error, setError] = useState<string>()
  const run = async () => {
    setBusy(true)
    setError(undefined)
    try {
      setResult(
        await post<WGSiteVerification>(
          `/network/vpn/wireguard/${encodeURIComponent(tunnel.name)}/peers/${peer.id}/verify`,
          { target: target.trim() },
        ),
      )
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }
  return (
    <div className="flex min-w-0 flex-col gap-3">
      <p className="text-title font-medium">Verify the site</p>
      <div className="flex flex-wrap items-end gap-2">
        <Field
          label="A host on its network"
          htmlFor={`site-target-${peer.id}`}
          hint="Optional: proves the round trip into its LAN"
        >
          <Input
            id={`site-target-${peer.id}`}
            value={target}
            placeholder={siteNetworks(peer)[0]?.split("/")[0] ?? "192.168.1.10"}
            onChange={(event) => setTarget(event.target.value)}
            className="w-44 font-mono"
          />
        </Field>
        <Button size="sm" onClick={() => void run()} pending={busy} disabled={busy}>
          Verify
        </Button>
      </div>
      {error && (
        <p role="alert" className="text-hint text-destructive">
          {error}
        </p>
      )}
      {result && (
        <div className="space-y-3" aria-label={`${peer.name} verification`}>
          <Status
            tone={siteOutcomeTone(result.outcome)}
            label={
              result.outcome === "verified"
                ? "Verified end to end"
                : result.outcome === "partial"
                  ? "Partly verified"
                  : "Not working"
            }
          />
          <RowList>
            {result.checks.map((c) => (
              <Row
                key={c.name}
                title={c.name}
                subtitle={c.detail}
                trailing={
                  <Status
                    tone={
                      c.status === "pass"
                        ? "running"
                        : c.status === "fail"
                          ? "danger"
                          : c.status === "warn"
                            ? "warning"
                            : "unknown"
                    }
                    label={c.status}
                  />
                }
              />
            ))}
          </RowList>
          <div className="space-y-1">
            <p className="text-hint font-medium">At the site</p>
            <ul className="space-y-1 font-mono text-micro text-muted-foreground">
              {result.remoteSteps.map((step) => (
                <li key={step}>{step}</li>
              ))}
            </ul>
          </div>
        </div>
      )}
    </div>
  )
}

/**
 * The Linux kill-switch file of a full-tunnel device: its configuration with
 * an nftables filter that refuses everything outside the tunnel. A text file
 * only, because the phone apps refuse a configuration with PostUp lines.
 */
export function KillSwitchFile({ tunnel, peer }: { tunnel: WGInterface; peer: WGPeer }) {
  const [busy, setBusy] = useState(false)
  const save = async () => {
    setBusy(true)
    try {
      const res = await get<{ name: string; config: string }>(
        `/network/vpn/wireguard/${encodeURIComponent(tunnel.name)}/peers/${peer.id}/config`,
        { variant: "linux-killswitch" },
      )
      const file = `${res.name.replace(/[^a-zA-Z0-9_.-]+/g, "-").replace(/^-+|-+$/g, "") || "wireguard"}-killswitch.conf`
      const url = URL.createObjectURL(new Blob([res.config], { type: "text/plain" }))
      const a = document.createElement("a")
      a.href = url
      a.download = file
      a.click()
      URL.revokeObjectURL(url)
    } catch (err) {
      notify.error("The kill-switch file could not be made", err)
    } finally {
      setBusy(false)
    }
  }
  return (
    <div className="space-y-2">
      <Button size="sm" variant="outline" onClick={() => void save()} pending={busy}>
        <Download aria-hidden />
        Save Linux kill-switch file
      </Button>
      <p className="text-hint text-muted-foreground">
        For wg-quick on Linux: nothing leaves outside the tunnel, local routes included, and a lost
        interface keeps the device offline until it is reconnected. Windows has its own switch for
        this configuration; on a phone, turn on the system&rsquo;s &ldquo;block connections without
        VPN&rdquo;.
      </p>
    </div>
  )
}
