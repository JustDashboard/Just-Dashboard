"use client"

import { useMemo, useState } from "react"
import { Plus, Trash } from "@/components/icons"
import { del, post } from "@/lib/api"
import { bytes, rate } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { NetworkLink, NetworkLivePoint } from "@/lib/types"
import { SidePanel } from "@/components/side-panel"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Detail, DetailList } from "@/components/page"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { Notice } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { useConfirm } from "@/components/confirm-dialog"
import { ChartPanel } from "@/components/metrics/chart-panel"
import { Cidr } from "@/components/network/address"
import { RX, TX } from "@/components/network/rate-pair"
import { LinkMark, OWNER_LABEL, ROLE_LABEL, kindLabel } from "@/components/network/marks"

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
}: {
  link: NetworkLink | undefined
  links: NetworkLink[]
  points?: NetworkLivePoint[]
  open: boolean
  onOpenChange: (open: boolean) => void
  onChanged: () => void
}) {
  const { confirm, dialog } = useConfirm()
  const [busy, setBusy] = useState<string>()
  const [mtu, setMtu] = useState("")
  const [address, setAddress] = useState("")

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
    } catch (err) {
      notify.error(`${link.name}: not changed`, err)
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
  const canEdit = !link.guard
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
                  disabled={!!link.guard || busy !== undefined}
                  onClick={() => setState(false)}
                >
                  Set down
                </Button>
              ) : (
                <Button
                  size="sm"
                  variant="outline"
                  pending={busy === "state"}
                  disabled={busy !== undefined}
                  onClick={() => setState(true)}
                >
                  Set up
                </Button>
              )}
              {link.managed && (
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
                    ).then(() => setMtu(""))
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
                      {a.family === "inet" ? "IPv4" : "IPv6"} · {a.scope}
                      {a.dynamic ? " · from DHCP" : ""}
                      {a.public ? " · public" : ""}
                    </span>
                    <span className="ml-auto flex shrink-0 items-center gap-2">
                      {a.managed && !a.guard ? (
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
                    ).then(() => setAddress(""))
                  }}
                >
                  <label className="min-w-0 flex-1 space-y-1.5">
                    <span className="text-body font-medium">Add an address</span>
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
