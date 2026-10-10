"use client"

import { useState } from "react"
import { get, post } from "@/lib/api"
import { plural, relativeTime } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { WGArchived, WGInterface } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Row, RowList } from "@/components/row-list"
import { Button } from "@/components/ui/button"
import { useConfirm } from "@/components/confirm-dialog"
import { NetworkReadWarning } from "@/components/network/read-warning"
import { TunnelRecord } from "./record"

/**
 * Tunnels removed from here. Each file was moved aside, not deleted, because
 * the server's key lives nowhere else: restoring puts the same key and peers
 * back, so every client's existing configuration connects again. The exit
 * node, the firewall opening and saved client configurations are not in the
 * file; the restore says which it could not bring back.
 */
export function ArchivedTunnels({ onRestored }: { onRestored: () => void }) {
  const archive = usePoll<WGArchived[]>(
    (signal) => get("/network/vpn/archive", undefined, signal),
    60_000,
  )
  const { confirm, dialog } = useConfirm()
  const [record, setRecord] = useState<string>()
  const list = archive.data ?? []
  if (archive.error && list.length === 0)
    return (
      <NetworkReadWarning
        error={archive.error}
        refresh={archive.refresh}
        reading="tunnel archive"
      />
    )
  if (list.length === 0) return null
  const restore = (a: WGArchived) =>
    confirm({
      title: `Restore ${a.name}`,
      confirmLabel: "Restore",
      description: (
        <p>
          {a.name} starts again with its own key and {plural(a.peers, "peer")}, on udp{" "}
          {a.listenPort}. Its exit node and saved client configurations are not part of the file.
        </p>
      ),
      action: async () => {
        const res = await post<{
          interface: WGInterface
          warnings: string[]
          firewall: { opened: boolean; reason?: string }
        }>(`/network/vpn/archive/${encodeURIComponent(a.file)}/restore`)
        notify.success(`${res.interface.name} is back`, {
          description: res.firewall.opened
            ? "The firewall admits its port again."
            : res.firewall.reason,
        })
        for (const w of res.warnings) notify.warning(w)
        archive.refresh()
        onRestored()
      },
    })
  return (
    <Panel plain>
      <PanelHeader
        title="Archived tunnels"
        actions={
          <span className="numeric text-hint text-muted-foreground">
            {plural(list.length, "file")}
          </span>
        }
      />
      <PanelBody>
        <NetworkReadWarning
          error={archive.error}
          refresh={archive.refresh}
          lastSuccess={archive.lastSuccess}
          reading="tunnel archive"
        />
        <RowList aria-label="Archived tunnels">
          {list.map((a) => (
            <Row
              key={a.file}
              title={<span className="font-mono">{a.name}</span>}
              subtitle={
                a.restorable
                  ? `removed ${relativeTime(new Date(a.archivedAt * 1000).toISOString())} · udp ${a.listenPort} · ${a.addresses.join(", ")} · ${plural(a.peers, "peer")}`
                  : a.refusal
              }
              trailing={
                <>
                  <Button size="xs" variant="ghost" onClick={() => setRecord(a.name)}>
                    History
                  </Button>
                  <Button
                    size="xs"
                    variant="outline"
                    disabled={!a.restorable}
                    onClick={() => restore(a)}
                    aria-label={`Restore ${a.name}`}
                  >
                    Restore
                  </Button>
                </>
              }
            />
          ))}
        </RowList>
      </PanelBody>
      {record && (
        <TunnelRecord tunnel={record} open onOpenChange={(open) => !open && setRecord(undefined)} />
      )}
      {dialog}
    </Panel>
  )
}
