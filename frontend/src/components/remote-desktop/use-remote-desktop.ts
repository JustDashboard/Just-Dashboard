"use client"

import { useCallback, useMemo } from "react"
import { usePoll } from "@/hooks/use-poll"
import { get } from "@/lib/api"
import { computerHues, type RdComputer, type RdOverview } from "@/lib/remote-desktop"

/**
 * The one read every Remote Desktop page hangs off: the engine, the tailnet,
 * every computer with whether its port answered just now, and the fortnight's
 * sessions. Polled, since a computer waking up or a session ending elsewhere
 * should show without a reload; the backend probes each port with a short
 * timeout, so a poll never waits on a computer that is off.
 */
export function useRemoteDesktop(intervalMs = 10_000) {
  const overview = usePoll(
    (signal) => get<RdOverview>("/remote-desktop/", undefined, signal),
    intervalMs,
  )
  const computers = overview.data?.computers
  const hues = useMemo(() => computerHues(computers ?? []), [computers])
  const hueOf = useCallback(
    (computer: Pick<RdComputer, "id">) => hues.get(computer.id) ?? "var(--tag-blue)",
    [hues],
  )
  return { ...overview, hueOf }
}
