"use client"

import Link from "next/link"
import { useAuth } from "@/hooks/use-auth"
import { useEffect, useMemo, useState } from "react"
import { Plus, Trash } from "@/components/icons"
import { del, errorMessage, get, post } from "@/lib/api"
import { bytes, rate } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { NetworkLink, NetworkLivePoint, NetworkMasterPreview } from "@/lib/types"
import { SidePanel } from "@/components/side-panel"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Detail, DetailList } from "@/components/page"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { Notice } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { useConfirm } from "@/components/confirm-dialog"
import { ChartPanel } from "@/components/metrics/chart-panel"
import { Cidr } from "@/components/network/address"
import { RX, TX } from "@/components/network/rate-pair"
import { LinkMark, OWNER_LABEL, ROLE_LABEL, kindLabel } from "@/components/network/marks"
import { NativeProfileEditor } from "./native-profile"
import { DeviceHardware } from "./device-detail"
import { DeviceReadiness, READINESS_KINDS } from "./device-readiness"
import { BridgeSwitch, PortVlans } from "./bridge-switch"
import { addressProvenance } from "./device-reading"

const SERIES = [
  { key: "rx", label: "In", color: RX, kind: "area" as const },
  { key: "tx", label: "Out", color: TX, kind: "area" as const },
]
const formatRate = (value: number) => rate(value)
const axisRate = (value: number) => `${bytes(value, 0)}/s`

/**
 * One device: its traffic over the last fifteen minutes, what it is and what
 * it is attached to, its addresses and its counters — and the changes the
 * dashboard may make to it. Each change the guard refuses is still drawn,
 * disabled, with the guard's sentence beside it: a control that disappears
 * says nothing, one that is greyed out with no reason says less.
 */
export function DeviceSheet({
  link,
  links,
  points,
  open,
  onOpenChange,
  onChanged,
  onOpenNamespace,
}: {
  link: NetworkLink | undefined
  links: NetworkLink[]
  points?: NetworkLivePoint[]
  open: boolean
  onOpenChange: (open: boolean) => void
  onChanged: () => void
  /** Opens the namespace a veth leads into: a managed one by name, or a container's. */
  onOpenNamespace?: (kind: "named" | "container", name: string) => void
}) {
  const { can } = useAuth()
  const admin = can("system.admin")
  const destructive = admin && can("destructive")
  const { confirm, dialog } = useConfirm()
  const [busy, setBusy] = useState<string>()
  const [mtu, setMtu] = useState("")
  const [address, setAddress] = useState("")
  const [bridge, setBridge] = useState<string>()
  const [preview, setPreview] = useState<{
    key: string
    value?: NetworkMasterPreview
    error?: string
  }>()

  // The membership change is read before it is offered: what it would leave
  // behind, and whether the guards allow it at all.
  const linkName = link?.name
  const previewKey = linkName && bridge !== undefined ? `${linkName}>${bridge}` : undefined
  useEffect(() => {
    if (!linkName || bridge === undefined || !previewKey) return
    const abort = new AbortController()
    const master = bridge === "__none" ? "" : bridge
    get<NetworkMasterPreview>(
      `/network/links/${encodeURIComponent(linkName)}/master/preview`,
      { master },
      abort.signal,
    )
      .then((value) => setPreview({ key: previewKey, value }))
      .catch((err) => {
        if (!abort.signal.aborted) setPreview({ key: previewKey, error: errorMessage(err) })
      })
    return () => abort.abort()
  }, [linkName, bridge, previewKey])
  const shownPreview = preview?.key === previewKey ? preview : undefined

  const rows = useMemo(
    () => (points ?? []).map((p) => ({ ts: p.t * 1000, rx: p.rx, tx: p.tx })),
    [points],
  )
  if (!link) return dialog

  const name = encodeURIComponent(link.name)
  const act = async (key: string, label: string, run: () => Promise<unknown>) => {
    setBusy(key)
    try {
      await run()
      notify.success(label)
      onChanged()
      return true
    } catch (err) {
      notify.error(`${link.name}: not changed`, err)
      return false
    } finally {
      setBusy(undefined)
    }
  }

  const setState = (up: boolean) => {
    if (up) {
      void act("state", `${link.name} is up`, () => post(`/network/links/${name}/up`))
      return
    }
    confirm({
      title: `Set ${link.name} down`,
      confirmLabel: "Set down",
      description: (
        <p>
          Everything on <span className="font-mono">{link.name}</span> stops until it is set up
          again
          {link.managed
            ? "."
            : ", and a device the dashboard did not make comes back up at the next boot."}
        </p>
      ),
      action: async () => {
        await post(`/network/links/${name}/down`)
        notify.success(`${link.name} is down`)
        onChanged()
      },
    })
  }

  const remove = () =>
    confirm({
      title: `Delete ${link.name}`,
      confirmLabel: "Delete",
      description: (
        <p>
          The device goes now and is not made again at boot. Anything using{" "}
          <span className="font-mono">{link.name}</span> — a bridge port, a route, a container —
          loses it.
        </p>
      ),
      action: async () => {
        await del(`/network/links/${name}`)
        notify.success(`${link.name} deleted`)
        onOpenChange(false)
        onChanged()
      },
    })

  const removeAddress = (cidr: string) =>
    confirm({
      title: `Remove ${cidr}`,
      confirmLabel: "Remove",
      description: (
        <p>
          <span className="font-mono">{link.name}</span> stops answering at{" "}
          <span className="font-mono">{cidr}</span>, and the routes the kernel made for its network
          go with it.
        </p>
      ),
      action: async () => {
        await del(`/network/links/${name}/addresses`, { query: { cidr } })
        notify.success(`${cidr} removed`)
        onChanged()
      },
    })

  const up = link.adminUp && (link.carrier || link.state === "unknown")
  const ports = links.filter((l) => l.master === link.name)
  const canEdit = admin && !link.guard
  const bridgeValue = bridge ?? link.master ?? "__none"
  const bridges = links.filter(
    (candidate) =>
      candidate.kind === "bridge" && candidate.owner !== "docker" && candidate.name !== link.name,
  )
  return (
    <>
      <SidePanel
        open={open}
        onOpenChange={onOpenChange}
        width="lg"
        title={
          <span className="flex min-w-0 items-center gap-3">
            <LinkMark link={link} />
            <span className="min-w-0">
              <span className="block truncate font-mono">{link.name}</span>
              <span className="flex items-center gap-2 text-hint font-normal text-muted-foreground">
                {ROLE_LABEL[link.role]} · {kindLabel(link)}
                {link.managed && <Tag>made here</Tag>}
              </span>
            </span>
          </span>
        }
        description={`The ${link.name} device's traffic, settings and addresses`}
        actions={
          <>
            <Status
              tone={up ? "running" : link.adminUp ? "warning" : "stopped"}
              label={up ? "Up" : link.adminUp ? "No carrier" : "Down"}
            />
            <span className="ml-auto flex items-center gap-2">
              {link.adminUp ? (
                <Button
                  size="sm"
                  variant="outline"
                  disabled={!destructive || !!link.guard || busy !== undefined}
                  onClick={() => setState(false)}
                >
                  Set down
                </Button>
              ) : (
                <Button
                  size="sm"
                  variant="outline"
                  pending={busy === "state"}
                  disabled={!admin || busy !== undefined}
                  onClick={() => setState(true)}
                >
                  Set up
                </Button>
              )}
              {destructive && link.managed && (
                <Button size="sm" variant="outline" onClick={remove} disabled={!!link.guard}>
                  <Trash aria-hidden />
                  Delete
                </Button>
              )}
            </span>
          </>
        }
      >
        <div className="flex flex-col gap-6">
          {link.guard && <Notice title="Guarded">{link.guard}</Notice>}
          {link.dockerJoin && (
            <Notice tone="warning" title="Docker join incomplete">
              {link.dockerJoinReason}
            </Notice>
          )}
          {(link.peerNamespace || link.container) && (
            <Panel plain>
              <PanelHeader title="Leads into" />
              <PanelBody>
                <div className="flex flex-wrap items-center gap-2">
                  {link.peerNamespace && (
                    <Button
                      size="sm"
                      variant="outline"
                      onClick={() => onOpenNamespace?.("named", link.peerNamespace!)}
                    >
                      Open namespace {link.peerNamespace}
                    </Button>
                  )}
                  {link.container && (
                    <>
                      <Button
                        size="sm"
                        variant="outline"
                        onClick={() => onOpenNamespace?.("container", link.container!)}
                      >
                        Open {link.container}&rsquo;s namespace
                      </Button>
                      {admin && (
                        <Button size="sm" variant="ghost" asChild>
                          <Link
                            href={`/network/investigate?container=${encodeURIComponent(link.container)}`}
                          >
                            Investigate a connection from {link.container}
                          </Link>
                        </Button>
                      )}
                    </>
                  )}
                </div>
                {link.peer && (
                  <p className="mt-2 text-hint text-muted-foreground">
                    Its other end is <span className="font-mono">{link.peer}</span>
                    {link.peerNamespace ? ` inside ${link.peerNamespace}` : ""}.
                  </p>
                )}
              </PanelBody>
            </Panel>
          )}

          <ChartPanel
            plain
            title="Last 15 minutes"
            rows={rows}
            series={SERIES}
            format={formatRate}
            axisFormat={axisRate}
            showPeaks={false}
            height={150}
            note="Collecting — the first readings arrive within a few seconds."
          />

          <Panel plain>
            <PanelHeader title="Device" />
            <PanelBody>
              <DetailList>
                <Detail label="Kind">{kindLabel(link)}</Detail>
                <Detail label="Kept by">{OWNER_LABEL[link.owner]}</Detail>
                {link.mac && (
                  <Detail label="MAC">{<span className="font-mono">{link.mac}</span>}</Detail>
                )}
                <Detail label="MTU">
                  <span className="numeric font-mono">{link.mtu}</span>
                </Detail>
                {link.qdisc && <Detail label="Queue">{link.qdisc}</Detail>}
                {link.speedMbps ? <Detail label="Speed">{link.speedMbps} Mb/s</Detail> : null}
                {link.parent && (
                  <Detail label="Rides on">
                    {<span className="font-mono">{link.parent}</span>}
                  </Detail>
                )}
                {link.vlanId ? <Detail label="VLAN">{link.vlanId}</Detail> : null}
                {link.vni ? <Detail label="VNI">{link.vni}</Detail> : null}
                {(link.local || link.remote) && (
                  <Detail label="Ends">
                    <span className="font-mono">
                      {link.local ?? "any"} → {link.remote ?? "any"}
                      {link.port ? `:${link.port}` : ""}
                    </span>
                  </Detail>
                )}
                {link.master && (
                  <Detail label="Port of">
                    {<span className="font-mono">{link.master}</span>}
                  </Detail>
                )}
                {link.container && <Detail label="Container">{link.container}</Detail>}
                {link.dockerNetwork && <Detail label="Docker network">{link.dockerNetwork}</Detail>}
                {link.xdp && (
                  <Detail label="XDP program">
                    {<span className="font-mono">{link.xdp}</span>}
                  </Detail>
                )}
              </DetailList>
              {canEdit && (
                <form
                  className="mt-4 flex items-end gap-2"
                  onSubmit={(event) => {
                    event.preventDefault()
                    const value = Number(mtu)
                    if (!Number.isInteger(value)) return
                    void act("mtu", `${link.name} MTU is ${value}`, () =>
                      post(`/network/links/${name}/mtu`, { mtu: value }),
                    ).then((changed) => changed && setMtu(""))
                  }}
                >
                  <label className="min-w-0 flex-1 space-y-1.5">
                    <span className="text-body font-medium">MTU</span>
                    <Input
                      inputMode="numeric"
                      value={mtu}
                      placeholder={String(link.mtu)}
                      onChange={(event) => setMtu(event.target.value)}
                      aria-label="New MTU"
                    />
                  </label>
                  <Button
                    type="submit"
                    size="sm"
                    variant="outline"
                    className="h-9"
                    disabled={!mtu || busy !== undefined}
                    pending={busy === "mtu"}
                  >
                    Set
                  </Button>
                </form>
              )}
            </PanelBody>
          </Panel>

          {link.role !== "loopback" && <DeviceHardware name={link.name} open={open} />}

          {READINESS_KINDS.has(link.kind) && (
            <DeviceReadiness
              link={link}
              open={open}
              managed={link.managed}
              remotes={link.remotes}
              onChanged={onChanged}
            />
          )}

          {link.kind === "bridge" && <BridgeSwitch link={link} open={open} onChanged={onChanged} />}
          {link.kind !== "bridge" && <PortVlans link={link} open={open} onChanged={onChanged} />}

          {canEdit && link.kind !== "bridge" && link.role !== "loopback" && (
            <Panel plain>
              <PanelHeader title="Bridge membership" />
              <PanelBody>
                <form
                  className="flex items-end gap-2"
                  onSubmit={(event) => {
                    event.preventDefault()
                    const master = bridgeValue === "__none" ? "" : bridgeValue
                    const effects = shownPreview?.value?.effects ?? []
                    confirm({
                      title: master
                        ? `Join ${link.name} to ${master}`
                        : `Remove ${link.name} from its bridge`,
                      confirmLabel: "Apply",
                      description: (
                        <div className="flex flex-col gap-2">
                          <p>
                            This changes which network receives the device&rsquo;s Ethernet frames.
                            The server refuses a change that would disturb its uplink, addresses or
                            your connection.
                          </p>
                          {effects.length > 0 && (
                            <ul className="list-disc pl-5">
                              {effects.map((e) => (
                                <li key={e.detail}>{e.detail}</li>
                              ))}
                            </ul>
                          )}
                        </div>
                      ),
                      action: async () => {
                        await post(`/network/links/${name}/master`, { master })
                        setBridge(undefined)
                        notify.success(
                          master
                            ? `${link.name} joined ${master}`
                            : `${link.name} is no longer a bridge port`,
                        )
                        onChanged()
                      },
                    })
                  }}
                >
                  <label className="min-w-0 flex-1 space-y-1.5">
                    <span className="text-body font-medium">Bridge</span>
                    <Select value={bridgeValue} onValueChange={setBridge}>
                      <SelectTrigger aria-label="Bridge membership">
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectItem value="__none">No bridge</SelectItem>
                        {bridges.map((candidate) => (
                          <SelectItem key={candidate.name} value={candidate.name}>
                            {candidate.name}
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                  </label>
                  <Button
                    type="submit"
                    size="sm"
                    variant="outline"
                    disabled={
                      busy !== undefined ||
                      bridgeValue === (link.master ?? "__none") ||
                      shownPreview?.value?.allowed === false
                    }
                  >
                    Apply
                  </Button>
                </form>
                {shownPreview && bridgeValue !== (link.master ?? "__none") && (
                  <div className="mt-3 flex flex-col gap-2" aria-label="What the move would do">
                    {shownPreview.error ? (
                      <Notice tone="warning" title="The move could not be previewed">
                        {shownPreview.error}
                      </Notice>
                    ) : shownPreview.value?.allowed === false ? (
                      <Notice tone="warning" title="Refused">
                        {shownPreview.value.refusal}
                      </Notice>
                    ) : null}
                    {shownPreview.value && shownPreview.value.effects.length > 0 && (
                      <ul className="flex list-disc flex-col gap-1 pl-5 text-hint text-muted-foreground">
                        {shownPreview.value.effects.map((e) => (
                          <li key={e.detail}>{e.detail}</li>
                        ))}
                      </ul>
                    )}
                  </div>
                )}
              </PanelBody>
            </Panel>
          )}

          {link.role === "bridge" && (
            <Panel plain>
              <PanelHeader
                title="Ports"
                actions={
                  <span className="numeric text-hint text-muted-foreground">{ports.length}</span>
                }
              />
              <PanelBody>
                {ports.length === 0 ? (
                  <p className="text-body text-muted-foreground">
                    No device is a port of this bridge.
                  </p>
                ) : (
                  <ul className="flex flex-col gap-2">
                    {ports.map((port) => (
                      <li key={port.name} className="flex min-w-0 items-center gap-3 text-body">
                        <LinkMark link={port} />
                        <span className="truncate font-mono">{port.name}</span>
                        <span className="truncate text-hint text-muted-foreground">
                          {port.container ?? kindLabel(port)}
                        </span>
                      </li>
                    ))}
                  </ul>
                )}
              </PanelBody>
            </Panel>
          )}

          <Panel plain>
            <PanelHeader
              title="Addresses"
              actions={
                <span className="numeric text-hint text-muted-foreground">
                  {link.addresses.length}
                </span>
              }
            />
            <PanelBody>
              <ul className="flex flex-col divide-y divide-hairline">
                {link.addresses.map((a) => (
                  <li key={a.cidr} className="flex min-w-0 items-center gap-3 py-2 text-body">
                    <Cidr cidr={a.cidr} className="min-w-0 truncate" />
                    <span className="text-hint text-muted-foreground">
                      {a.family === "inet" ? "IPv4" : "IPv6"} · {a.scope} · {addressProvenance(a)}
                      {a.public ? " · public" : ""}
                    </span>
                    <span className="ml-auto flex shrink-0 items-center gap-2">
                      {destructive && a.managed && !a.guard ? (
                        <Button
                          size="xs"
                          variant="ghost"
                          aria-label={`Remove ${a.cidr}`}
                          onClick={() => removeAddress(a.cidr)}
                        >
                          <Trash aria-hidden />
                        </Button>
                      ) : a.guard ? (
                        <span
                          className="max-w-[14rem] truncate text-hint text-muted-foreground"
                          title={a.guard}
                        >
                          guarded
                        </span>
                      ) : null}
                    </span>
                  </li>
                ))}
              </ul>
              {canEdit && (
                <form
                  className="mt-3 flex items-end gap-2"
                  onSubmit={(event) => {
                    event.preventDefault()
                    if (!address.trim()) return
                    void act("address", `${address.trim()} added to ${link.name}`, () =>
                      post(`/network/links/${name}/addresses`, { cidr: address.trim() }),
                    ).then((changed) => changed && setAddress(""))
                  }}
                >
                  <label className="min-w-0 flex-1 space-y-1.5">
                    <span className="text-body font-medium">Add a static address</span>
                    <Input
                      value={address}
                      placeholder="10.20.0.1/24 or 2001:db8::1/64"
                      onChange={(event) => setAddress(event.target.value)}
                      aria-label="Address in CIDR form"
                      className="font-mono"
                    />
                  </label>
                  <Button
                    type="submit"
                    size="sm"
                    variant="outline"
                    className="h-9"
                    disabled={!address.trim() || busy !== undefined}
                    pending={busy === "address"}
                  >
                    <Plus aria-hidden />
                    Add
                  </Button>
                </form>
              )}
            </PanelBody>
          </Panel>

          {!link.managed &&
            link.addresses.some(
              (a) => a.origin && a.origin !== "static" && a.origin !== "link-local",
            ) && (
              <p className="-mt-3 text-hint text-muted-foreground">
                Addresses added here are static and kept by the dashboard. How {link.name} acquires
                its own — DHCP, router advertisements or a fixed address — is its network
                manager&rsquo;s setting{admin ? ", edited in its profile below" : ""}.
              </p>
            )}

          {admin && !link.managed && (
            <NativeProfileEditor
              key={link.name}
              device={link.name}
              open={open}
              onChanged={onChanged}
            />
          )}

          <Panel plain>
            <PanelHeader title="Since it was created" />
            <PanelBody>
              <DetailList>
                <Detail label="Received">
                  <span className="numeric">
                    {bytes(link.counters.rxBytes)} · {link.counters.rxPackets.toLocaleString()}{" "}
                    packets
                  </span>
                </Detail>
                <Detail label="Sent">
                  <span className="numeric">
                    {bytes(link.counters.txBytes)} · {link.counters.txPackets.toLocaleString()}{" "}
                    packets
                  </span>
                </Detail>
                <Detail label="Errors">
                  <span
                    className={
                      link.counters.rxErrors + link.counters.txErrors > 0
                        ? "numeric text-warning"
                        : "numeric"
                    }
                  >
                    {(link.counters.rxErrors + link.counters.txErrors).toLocaleString()}
                  </span>
                </Detail>
                <Detail label="Dropped">
                  <span
                    className={
                      link.counters.rxDropped + link.counters.txDropped > 0
                        ? "numeric text-warning"
                        : "numeric"
                    }
                  >
                    {(link.counters.rxDropped + link.counters.txDropped).toLocaleString()}
                  </span>
                </Detail>
              </DetailList>
            </PanelBody>
          </Panel>
        </div>
      </SidePanel>
      {dialog}
    </>
  )
}
