"use client"

import { useState } from "react"
import { get, put } from "@/lib/api"
import { notify } from "@/lib/toast"
import type { NetworkBridgeView, NetworkLink } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Detail, DetailList } from "@/components/page"
import { Notice } from "@/components/state"
import { Field, FieldRow } from "@/components/form"
import { Input } from "@/components/ui/input"
import { Button } from "@/components/ui/button"
import { useConfirm } from "@/components/confirm-dialog"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { NetworkReadWarning } from "@/components/network/read-warning"
import { ReadingWords } from "./device-detail"
import { describeVlans, sameVlans, vlanFields, vlanPolicy } from "./device-reading"

function useBridge(name: string | undefined, open: boolean) {
  return usePoll<NetworkBridgeView>(
    (signal) => get(`/network/links/${encodeURIComponent(name ?? "")}/bridge`, undefined, signal),
    30_000,
    [name],
    { enabled: open && !!name },
  )
}

/**
 * A bridge read as the switch it is: whether it runs spanning tree, filters
 * by VLAN and snoops multicast; what each port carries, beside what the
 * dashboard wants it to carry where the port is its own; and the addresses it
 * has learned behind each port.
 */
export function BridgeSwitch({
  link,
  open,
  onChanged,
}: {
  link: NetworkLink
  open: boolean
  onChanged: () => void
}) {
  const bridge = useBridge(link.name, open)
  const v = bridge.data
  return (
    <>
      <Panel plain>
        <PanelHeader title="Switch" />
        <PanelBody>
          {v && (
            <NetworkReadWarning
              error={bridge.error}
              refresh={bridge.refresh}
              lastSuccess={bridge.lastSuccess}
              reading="bridge"
            />
          )}
          {!v ? (
            bridge.error ? (
              <Notice tone="warning" title="The bridge could not be read">
                {bridge.error.message}
              </Notice>
            ) : (
              <p className="text-body text-muted-foreground">Reading…</p>
            )
          ) : (
            <div className="flex flex-col gap-5">
              {v.settingsRead.state === "ok" ? (
                <DetailList>
                  <Detail label="Spanning tree">{v.stp ? "on" : "off"}</Detail>
                  <Detail label="VLAN filtering">
                    {v.vlanFiltering ? `on · ${v.vlanProtocol ?? "802.1Q"}` : "off"}
                  </Detail>
                  {v.vlanFiltering && <Detail label="Default native VLAN">{v.defaultPvid}</Detail>}
                  <Detail label="Multicast snooping">{v.multicastSnooping ? "on" : "off"}</Detail>
                  <Detail label="Learned entries age out">{v.ageingSeconds} s</Detail>
                </DetailList>
              ) : (
                <ReadingWords reading={v.settingsRead} />
              )}

              <div>
                <p className="eyebrow mb-2">VLANs by port</p>
                {v.vlansRead.state !== "ok" ? (
                  <ReadingWords reading={v.vlansRead} />
                ) : (
                  <Table aria-label="VLANs by port">
                    <TableHeader>
                      <TableRow>
                        <TableHead>Port</TableHead>
                        <TableHead>Carries</TableHead>
                        <TableHead>Wanted</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {v.ports.map((p) => (
                        <TableRow key={p.name}>
                          <TableCell className="font-mono">
                            {p.name}
                            {p.self && (
                              <span className="font-sans text-muted-foreground"> · itself</span>
                            )}
                          </TableCell>
                          <TableCell>{describeVlans(p.vlans)}</TableCell>
                          <TableCell>
                            {p.desired ? (
                              sameVlans(p.desired, p.vlans) ? (
                                <span className="text-muted-foreground">as carried</span>
                              ) : (
                                <span className="text-warning">{describeVlans(p.desired)}</span>
                              )
                            ) : (
                              <span className="text-muted-foreground">not managed here</span>
                            )}
                          </TableCell>
                        </TableRow>
                      ))}
                    </TableBody>
                  </Table>
                )}
              </div>

              <div>
                <p className="eyebrow mb-2">
                  Forwarding database
                  {v.fdbRead.state === "ok" && v.fdbTotal > v.fdb.length
                    ? ` · first ${v.fdb.length} of ${v.fdbTotal}`
                    : ""}
                </p>
                {v.fdbRead.state !== "ok" ? (
                  <ReadingWords reading={v.fdbRead} />
                ) : v.fdb.length === 0 ? (
                  <p className="text-body text-muted-foreground">Nothing learned or configured.</p>
                ) : (
                  <Table aria-label="Forwarding database" containerClassName="max-h-72">
                    <TableHeader>
                      <TableRow>
                        <TableHead>Address</TableHead>
                        <TableHead>Port</TableHead>
                        <TableHead>VLAN</TableHead>
                        <TableHead>Kind</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {v.fdb.map((e, i) => (
                        <TableRow key={`${e.mac}-${e.port}-${e.vlan ?? 0}-${i}`}>
                          <TableCell className="font-mono">{e.mac}</TableCell>
                          <TableCell className="font-mono">{e.port}</TableCell>
                          <TableCell className="numeric">{e.vlan ?? "—"}</TableCell>
                          <TableCell>{e.static ? "configured" : e.state || "learned"}</TableCell>
                        </TableRow>
                      ))}
                    </TableBody>
                  </Table>
                )}
              </div>
            </div>
          )}
        </PanelBody>
      </Panel>
      {v?.managed && v.vlanFiltering && (
        <VlanPolicy
          device={link.name}
          bridge={v}
          onChanged={() => {
            bridge.refresh()
            onChanged()
          }}
        />
      )}
    </>
  )
}

/**
 * The VLANs a port of a VLAN-filtering bridge carries, for a port the
 * dashboard made: read from its bridge, edited here.
 */
export function PortVlans({
  link,
  open,
  onChanged,
}: {
  link: NetworkLink
  open: boolean
  onChanged: () => void
}) {
  const bridge = useBridge(link.master, open && !!link.master && link.managed)
  const v = bridge.data
  if (!link.master || !link.managed || !v?.managed || !v.vlanFiltering) return null
  return (
    <VlanPolicy
      device={link.name}
      bridge={v}
      onChanged={() => {
        bridge.refresh()
        onChanged()
      }}
    />
  )
}

/**
 * The tagged/untagged policy, in the words a switch port is configured in:
 * one native VLAN — untagged, and the VLAN untagged frames arriving belong
 * to — and the VLANs carried tagged beside it. Leaving both empty returns
 * the port to the kernel's default, VLAN 1 untagged. Taking a VLAN away
 * needs the destructive capability and is confirmed first.
 */
function VlanPolicy({
  device,
  bridge,
  onChanged,
}: {
  device: string
  bridge: NetworkBridgeView
  onChanged: () => void
}) {
  const { can } = useAuth()
  const admin = can("system.admin")
  const { confirm, dialog } = useConfirm()
  const port = bridge.ports.find((p) => p.name === device)
  const current = port?.desired ?? [{ vid: bridge.defaultPvid || 1, pvid: true, untagged: true }]
  const initial = vlanFields(current)
  const [native, setNative] = useState(initial.native)
  const [tagged, setTagged] = useState(initial.tagged)
  const policy = vlanPolicy(native, tagged)
  const target = policy.vlans.length ? policy.vlans : [{ vid: 1, pvid: true, untagged: true }]
  const removed = current.filter((c) => !target.some((t) => t.vid === c.vid)).map((c) => c.vid)
  const changed = !policy.error && !sameVlans(target, current)
  const save = async () => {
    await put(`/network/links/${encodeURIComponent(device)}/vlans`, { vlans: policy.vlans })
    notify.success(`${device} carries ${describeVlans(target)}`)
    onChanged()
  }
  return (
    <Panel plain>
      <PanelHeader title={port?.self ? `${device}'s own VLANs` : `VLANs on ${bridge.name}`} />
      <PanelBody>
        <form
          className="flex flex-col gap-3"
          onSubmit={(event) => {
            event.preventDefault()
            if (!changed) return
            if (removed.length === 0) {
              void save().catch((err) => notify.error(`${device}: not changed`, err))
              return
            }
            confirm({
              title: `Take VLAN ${removed.join(", ")} off ${device}`,
              confirmLabel: "Apply",
              description: (
                <p>
                  Frames of VLAN {removed.join(", ")} stop crossing{" "}
                  <span className="font-mono">{device}</span>. The server refuses the change if it
                  would move your connection, and puts it back if the path moves after it.
                </p>
              ),
              action: save,
            })
          }}
        >
          <p className="text-body text-muted-foreground">
            Now: {port ? describeVlans(port.vlans) : "not read"}
          </p>
          <FieldRow>
            <Field label="Native VLAN" htmlFor={`${device}-native`} hint="Untagged on this port">
              <Input
                id={`${device}-native`}
                inputMode="numeric"
                value={native}
                placeholder="1"
                onChange={(event) => setNative(event.target.value)}
                disabled={!admin}
              />
            </Field>
            <Field
              label="Tagged VLANs"
              htmlFor={`${device}-tagged`}
              hint="Ids or ranges, separated by commas"
            >
              <Input
                id={`${device}-tagged`}
                value={tagged}
                placeholder="20, 30-32"
                onChange={(event) => setTagged(event.target.value)}
                disabled={!admin}
                className="font-mono"
              />
            </Field>
          </FieldRow>
          {policy.error && (
            <p role="alert" className="text-body text-destructive">
              {policy.error}
            </p>
          )}
          <Button
            type="submit"
            size="sm"
            variant="outline"
            className="self-start"
            disabled={!admin || !changed}
          >
            Apply VLANs
          </Button>
        </form>
        {dialog}
      </PanelBody>
    </Panel>
  )
}
