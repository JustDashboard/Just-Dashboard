"use client"

import { useEffect, useState } from "react"
import { appendLive, CONTAINER_STALE_MS } from "@/lib/container-usage"
import type { ContainerRow } from "@/lib/metrics-range"
import type { ContainerStats } from "@/lib/types"
import { useSocket, type SocketState } from "@/hooks/use-socket"

type Feed = {
  containerId: string
  /** The newest frame, which the next one is measured against. */
  last: ContainerStats
  rows: ContainerRow[]
  /** When the newest frame arrived here, for telling a quiet socket from a stalled one. */
  receivedAt: number
}

/**
 * Each container's live window, outside React. A project's Overview and its
 * Runtime page read the same container, and keeping the window here is what
 * lets the Runtime charts open on the minutes the Overview was already
 * watching instead of starting from nothing on every visit.
 */
const feeds = new Map<string, Feed>()

export type ContainerLive = {
  /** The newest frame Docker sent. */
  stats?: ContainerStats
  /** Docker's one frame a second over the last five minutes, rates already measured. */
  rows: ContainerRow[]
  /** The newest row: what the container is doing this second. */
  now?: ContainerRow
  /** Frames are arriving on an open socket — the one condition worth a live dot (§11). */
  live: boolean
  /** Frames stopped arriving on a socket that still claims to be open. */
  stale: boolean
  state: SocketState
}

/**
 * One container's stats socket, as a reading and a rolling window of rows.
 *
 * Docker sends a frame a second, and a rate needs two of them, so the window
 * is measured frame against frame with `containerRates` — the same arithmetic
 * Docker's own page uses, which keeps a counter reset, a replaced container or
 * an interface that came or went from drawing a spike.
 */
export function useContainerLive(
  containerId: string | undefined,
  onStats?: (stats: ContainerStats) => void,
): ContainerLive {
  const [feed, setFeed] = useState(() => (containerId ? feeds.get(containerId) : undefined))
  const [now, setNow] = useState(0)
  const { state } = useSocket(
    `/docker/containers/${encodeURIComponent(containerId ?? "")}/stats/stream`,
    {
      enabled: Boolean(containerId),
      onMessage: (message) => {
        if (message.type !== "stats" || !message.data || !containerId) return
        const stats = message.data as ContainerStats
        const before = feeds.get(containerId)
        const rows = appendLive(before?.rows ?? [], stats, before?.last)
        if (before && rows === before.rows) return
        const next = { containerId, last: stats, rows, receivedAt: Date.now() }
        feeds.set(containerId, next)
        setFeed(next)
        onStats?.(stats)
      },
    },
  )
  // A socket that stops sending without closing says nothing at all, so the
  // only way to notice is to keep asking how long ago the last frame was.
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(timer)
  }, [])

  // A reading of the container this was showing a moment ago is not a
  // reading of this one.
  const current = feed?.containerId === containerId ? feed : undefined
  // Only an open socket can be stale: one still connecting has not yet been
  // asked for a frame, and a window kept from an earlier visit is not late.
  const stale =
    state === "open" && current !== undefined && now - current.receivedAt > CONTAINER_STALE_MS
  return {
    stats: current?.last,
    rows: current?.rows ?? NO_ROWS,
    now: current?.rows.at(-1),
    live: state === "open" && current !== undefined && !stale,
    stale,
    state,
  }
}

const NO_ROWS: ContainerRow[] = []
