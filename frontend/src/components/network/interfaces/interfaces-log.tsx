"use client"

import { useMemo } from "react"
import { get } from "@/lib/api"
import type { SystemdList } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { kindViews } from "@/components/logs/kind-views"
import { ServiceLogs } from "@/components/logs/service-logs"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { LoadingRows } from "@/components/state"
import { interfaceLogSources } from "@/components/network/interfaces/log-sources"

/**
 * What the machine logged about its links, at the end of the page that lists
 * them: the kernel's ring, where a device's carrier dropping or a driver
 * resetting is, and the journal of the daemon that manages them. The units
 * are asked once, from systemd's own list rather than the logs page's walk of
 * every file on the host; where they cannot be read the ring stands alone.
 * The pane waits for that answer, so a remembered unit is not opened on the
 * ring first and then swapped.
 */
export function InterfacesLog() {
  const list = usePoll<SystemdList>((signal) => get("/systemd/", undefined, signal), 0)
  const sources = useMemo(() => interfaceLogSources(list.data?.units), [list.data])
  const known = list.data !== undefined || list.error !== undefined
  return (
    <Panel plain>
      <PanelHeader title="Link log" />
      <PanelBody flush className="pt-3">
        {known ? (
          <ServiceLogs
            sources={sources}
            views={kindViews}
            storageKey="network.interfaces.log"
            pickerLabel="Link log"
            paneClassName="h-[min(75vh,40rem)] min-h-80"
          />
        ) : (
          <LoadingRows rows={6} className="h-[min(75vh,40rem)] min-h-80" />
        )}
      </PanelBody>
    </Panel>
  )
}
