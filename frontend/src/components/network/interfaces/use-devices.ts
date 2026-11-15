"use client"

import { useMemo } from "react"
import { get } from "@/lib/api"
import type { NetworkLink } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useSessionState } from "@/lib/view-state"
import { useLiveTraffic } from "@/components/network/use-live-traffic"

/**
 * The machine's devices, read every ten seconds, with each one's in and out
 * replaced by the live ring's newest reading: what the Interfaces list and a
 * device's page both draw, so a device reads the same on either.
 */
export function useDevices() {
  const links = usePoll<NetworkLink[]>((signal) => get("/network/links", undefined, signal), 10_000)
  const live = useLiveTraffic()
  const devices = useMemo(
    () =>
      links.data?.map((link) => {
        const last = live.series[link.name]?.at(-1)
        return last ? { ...link, rxRate: last.rx, txRate: last.tx } : link
      }),
    [links.data, live.series],
  )
  return { links, live, devices }
}

/**
 * The list's filter, kept for the tab: the rail beside a device walks the
 * list as it was left, and going back finds it the same.
 */
export function useDeviceFilter() {
  const [everything, setEverything] = useSessionState("network.interfaces.everything", false)
  const [query, setQuery] = useSessionState("network.interfaces.query", "")
  return { everything, setEverything, query, setQuery }
}
