"use client"

import { get } from "@/lib/api"
import type { NetworkBridgeView, NetworkLink } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import { Detail, DetailList } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { LoadingRows, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { LinkMark, kindLabel } from "@/components/network/marks"
import { NetworkReadWarning } from "@/components/network/read-warning"
import { ReadingWords } from "./device-detail"
import { deviceHref, deviceState } from "./device-facts"
import { SECOND_LINE } from "./device-list"
import { describeVlans, sameVlans } from "./device-reading"

/** A bridge read as a switch: its settings, what each port carries and what it has learned. */
export function useBridge(name: string | undefined, enabled = true) {
  return usePoll<NetworkBridgeView>(
    (signal) => get(`/network/links/${encodeURIComponent(name ?? "")}/bridge`, undefined, signal),
    30_000,
    [name],
    { enabled: enabled && !!name },
  )
}

/**
 * A bridge's ports, and the bridge as the switch it is. Each port is a card
 * that opens that device, with the VLANs it carries on its line (beside what
 * the dashboard wants it to carry, where the two differ); under them whether
 * the bridge runs spanning tree, filters by VLAN and snoops multicast, and
 * the addresses it has learned behind each port.
 */
export function DevicePorts({ link, links }: { link: NetworkLink; links: NetworkLink[] }) {
  const bridge = useBridge(link.name)
  const v = bridge.data
  const ports = links.filter((l) => l.master === link.name)
  const vlans = v && v.vlansRead.state === "ok" ? v : undefined
  const self = vlans?.ports.find((p) => p.self)
  return (
    <div className="flex min-w-0 flex-col gap-8 pb-6">
      <Panel plain>
        <PanelHeader
          title="Ports"
          actions={<span className="numeric text-hint text-muted-foreground">{ports.length}</span>}
        />
        <PanelBody>
          {ports.length === 0 ? (
            <p className="py-2 text-body text-muted-foreground">
              No device is a port of this bridge.
            </p>
          ) : (
            <ChoiceList>
              {ports.map((port) => {
                const state = deviceState(port)
                const carried = vlans?.ports.find((p) => p.name === port.name)
                const differs =
                  carried?.desired !== undefined && !sameVlans(carried.desired, carried.vlans)
                return (
                  <ChoiceRow
                    key={port.name}
                    leading={<LinkMark link={port} />}
                    title={
                      <span className="inline-flex min-w-0 items-center gap-2">
                        <span className="truncate font-mono">{port.name}</span>
                        <Tag>{kindLabel(port)}</Tag>
                      </span>
                    }
                    verb={`Open ${port.name}`}
                    description={
                      <span data-slot="port-vlans" className={SECOND_LINE}>
                        {port.container ? `${port.container} · ` : ""}
                        {!v ? (
                          " "
                        ) : !carried ? (
                          v.vlanFiltering ? (
                            "VLANs not read"
                          ) : (
                            "every VLAN"
                          )
                        ) : (
                          <>
                            {describeVlans(carried.vlans)}
                            {differs && (
                              <span className="text-warning">
                                {" "}
                                · wanted {describeVlans(carried.desired!)}
                              </span>
                            )}
                          </>
                        )}
                      </span>
                    }
                    trailing={
                      <Status
                        tone={state.tone}
                        label={state.label}
                        className="hidden w-20 sm:inline-flex"
                      />
                    }
                    href={deviceHref(port.name)}
                  />
                )
              })}
            </ChoiceList>
          )}
        </PanelBody>
      </Panel>

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
              <LoadingRows rows={4} />
            )
          ) : (
            <div className="flex flex-col gap-3">
              {v.settingsRead.state === "ok" ? (
                <DetailList>
                  <Detail label="Spanning tree">{v.stp ? "on" : "off"}</Detail>
                  <Detail label="VLAN filtering">
                    {v.vlanFiltering ? `on · ${v.vlanProtocol ?? "802.1Q"}` : "off"}
                  </Detail>
                  {v.vlanFiltering && <Detail label="Default native VLAN">{v.defaultPvid}</Detail>}
                  <Detail label="Multicast snooping">{v.multicastSnooping ? "on" : "off"}</Detail>
                  <Detail label="Learned entries age out">{v.ageingSeconds} s</Detail>
                  {self && (
                    <Detail label="Its own VLANs">
                      {describeVlans(self.vlans)}
                      {self.desired && !sameVlans(self.desired, self.vlans) && (
                        <span className="text-warning">
                          {" "}
                          · wanted {describeVlans(self.desired)}
                        </span>
                      )}
                    </Detail>
                  )}
                </DetailList>
              ) : (
                <ReadingWords reading={v.settingsRead} />
              )}
              {v.vlansRead.state !== "ok" && (
                <p>
                  <span className="text-body text-muted-foreground">VLANs by port: </span>
                  <ReadingWords reading={v.vlansRead} />
                </p>
              )}
            </div>
          )}
        </PanelBody>
      </Panel>

      {v && (
        <Panel plain>
          <PanelHeader
            title="Forwarding database"
            actions={
              v.fdbRead.state === "ok" && (
                <span className="numeric text-hint text-muted-foreground">
                  {v.fdbTotal > v.fdb.length
                    ? `first ${v.fdb.length} of ${v.fdbTotal}`
                    : v.fdb.length}
                </span>
              )
            }
          />
          <PanelBody>
            {v.fdbRead.state !== "ok" ? (
              <ReadingWords reading={v.fdbRead} />
            ) : v.fdb.length === 0 ? (
              <p className="py-2 text-body text-muted-foreground">Nothing learned or configured.</p>
            ) : (
              <div className="overflow-hidden rounded-xl border">
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
              </div>
            )}
          </PanelBody>
        </Panel>
      )}
    </div>
  )
}
