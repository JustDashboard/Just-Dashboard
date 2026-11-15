"use client"

import { get, post } from "@/lib/api"
import { plural, relativeTime } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { WGArchived, WGInterface } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useConfirm } from "@/components/confirm-dialog"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ProductLogo } from "@/components/product-logo"
import { Button } from "@/components/ui/button"
import { useHubSession } from "@/components/network/hub-session"
import { NetworkReadWarning } from "@/components/network/read-warning"
import { tunnelHref } from "./tunnel-reading"

/** The tunnels removed from here, read once a minute: the VPN page's list and a tunnel page's rail. */
export function useArchive(enabled = true) {
  return usePoll<WGArchived[]>(
    (signal) => get("/network/vpn/archive", undefined, signal),
    60_000,
    [],
    {
      enabled,
    },
  )
}

/**
 * Putting a removed tunnel back, after a confirmation that says what comes
 * with it. The file holds the server's key and its peers, so every client's
 * existing configuration connects again; the exit node, the firewall opening
 * and saved client configurations are not in it, and the answer says which it
 * could not bring back.
 *
 * Restoring lets the file's peers back onto this server's network, so the
 * server refuses it to a session opened through Just Hub; `allowed` is false
 * for one, and nothing offers it.
 */
export function useRestore(onRestored: () => void) {
  const { confirm, dialog } = useConfirm()
  const allowed = !useHubSession()
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
        onRestored()
      },
    })
  return { restore, dialog, allowed }
}

/** An archived tunnel's mono line: what the file holds, or why it cannot come back. */
export function archivedLine(a: WGArchived) {
  return a.restorable
    ? `udp ${a.listenPort} · ${a.addresses.join(", ")} · ${plural(a.peers, "peer")}`
    : (a.refusal ?? "cannot be restored")
}

/**
 * Tunnels removed from here, one card each. A file was moved aside, not
 * deleted, because the server's key lives nowhere else. A card opens the
 * tunnel's own page, where its history is kept after it is gone; Restore is
 * the card's one verb.
 */
export function ArchivedTunnels({ onRestored }: { onRestored: () => void }) {
  const archive = useArchive()
  const { restore, dialog, allowed } = useRestore(() => {
    archive.refresh()
    onRestored()
  })
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
  return (
    <Panel plain>
      <PanelHeader
        title="Archived tunnels"
        actions={<span className="numeric text-hint text-muted-foreground">{list.length}</span>}
      />
      <PanelBody>
        <NetworkReadWarning
          error={archive.error}
          refresh={archive.refresh}
          lastSuccess={archive.lastSuccess}
          reading="tunnel archive"
        />
        <ChoiceList aria-label="Archived tunnels">
          {list.map((a) => (
            <ChoiceRow
              key={a.file}
              href={tunnelHref(a.name, "history")}
              verb={`Open ${a.name}'s history`}
              leading={<ProductLogo id="wireguard" size="sm" />}
              title={<span className="font-mono">{a.name}</span>}
              description={<span className="font-mono">{archivedLine(a)}</span>}
              trailing={
                <>
                  <span className="text-hint text-muted-foreground max-sm:hidden">
                    removed {relativeTime(new Date(a.archivedAt * 1000).toISOString())}
                  </span>
                  {allowed && (
                    <Button
                      size="xs"
                      variant="outline"
                      disabled={!a.restorable}
                      onClick={() => restore(a)}
                      aria-label={`Restore ${a.name}`}
                    >
                      Restore
                    </Button>
                  )}
                </>
              }
            />
          ))}
        </ChoiceList>
      </PanelBody>
      {dialog}
    </Panel>
  )
}
