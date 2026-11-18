"use client"

import { useMemo } from "react"
import { get } from "@/lib/api"
import type { SystemdList } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { kindViews } from "@/components/logs/kind-views"
import { EmbeddedLog } from "@/components/logs/embedded-log"
import { interfaceLogSources } from "@/components/network/interfaces/log-sources"

/**
 * What the machine logged about its links, at the end of the page that lists
 * them: the kernel's ring, where a device's carrier dropping or a driver
 * resetting is, and the journal of the daemon that manages them. The units
 * are asked once, from systemd's own list rather than the logs page's walk of
 * every file on the host; where they cannot be read the ring stands alone.
 * The pane waits for that answer, so a remembered unit is not opened on the
 * ring first and then swapped. On a phone each is a card into the Logs page.
 */
export function InterfacesLog() {
  const list = usePoll<SystemdList>((signal) => get("/systemd/", undefined, signal), 0)
  const known = list.data !== undefined || list.error !== undefined
  const sources = useMemo(
    () => (known ? interfaceLogSources(list.data?.units) : undefined),
    [known, list.data],
  )
  return (
    <EmbeddedLog
      title="Link log"
      sources={sources}
      views={kindViews}
      storageKey="network.interfaces.log"
      pickerLabel="Link log"
    />
  )
}
