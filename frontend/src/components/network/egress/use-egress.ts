"use client"

import { useState } from "react"
import { get } from "@/lib/api"
import { usePoll } from "@/hooks/use-poll"
import type { EgressView } from "@/lib/network-egress"
import type { NetworkLink } from "@/lib/types"

/**
 * Every group, read every fifteen seconds and every five while a simulation
 * runs, so its progress and result arrive without a reload. The list and a
 * group's page read the same view: the server has no read of one group, and
 * the page's rail is the list anyway.
 */
export function useEgressView() {
  const [running, setRunning] = useState(false)
  return usePoll<EgressView>(
    async (signal) => {
      const view = await get<EgressView>("/network/egress", undefined, signal)
      setRunning(Boolean(view.running))
      return view
    },
    running ? 5_000 : 15_000,
  )
}

/**
 * The host's devices, for the form's device picker and a member's mark. The
 * list page read them every minute for a form that is usually closed; now
 * only while something draws them.
 */
export function useEgressLinks(enabled: boolean) {
  return usePoll<NetworkLink[]>((signal) => get("/network/links", undefined, signal), 60_000, [], {
    enabled,
  })
}
