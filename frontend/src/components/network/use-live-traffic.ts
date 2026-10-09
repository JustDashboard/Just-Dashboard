"use client"

import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { get } from "@/lib/api"
import type { NetworkLive, NetworkLivePoint } from "@/lib/types"

/** The backend keeps fifteen minutes; so does the page. */
const KEEP = 450

export type LiveTraffic = {
  /** Each device's two-second readings, oldest first. */
  series: Record<string, NetworkLivePoint[]>
  /** The newest reading's time, in unix seconds; zero before the first answer. */
  now: number
  error?: Error
  /** When the last poll succeeded, in milliseconds; retained rings are older than an error. */
  lastSuccess?: number
  /** Ask again now, after a failed poll. */
  refresh: () => void
}

/**
 * Every device's two-second ring, kept in the page and topped up every two
 * seconds with only the points it has not drawn (`since`), so a quarter of an
 * hour of throughput is one request when the page opens and a handful of
 * points per poll after. Paused while the tab is hidden, as every poll is: a
 * background tab must not keep the server it watches busy.
 *
 * A device that leaves the answer leaves the page — Docker removes a veth when
 * its container stops, and a line that never moves again is a stale reading
 * dressed as a live one.
 */
export function useLiveTraffic(enabled = true, intervalMs = 2000): LiveTraffic {
  const [state, setState] = useState<Omit<LiveTraffic, "refresh">>({ series: {}, now: 0 })
  const [attempt, setAttempt] = useState(0)
  const refresh = useCallback(() => setAttempt((n) => n + 1), [])
  const since = useRef(0)

  useEffect(() => {
    if (!enabled) return
    let cancelled = false
    let timer: ReturnType<typeof setTimeout> | undefined
    const controller = new AbortController()

    const tick = async () => {
      try {
        const live = await get<NetworkLive>(
          "/network/traffic/live",
          since.current ? { since: since.current } : undefined,
          controller.signal,
        )
        if (cancelled) return
        setState((previous) => ({
          series: merge(previous.series, live.series),
          now: live.now,
          lastSuccess: Date.now(),
        }))
        const newest = Math.max(
          since.current,
          ...Object.values(live.series).map((points) => points.at(-1)?.t ?? 0),
        )
        since.current = newest
      } catch (err) {
        if (cancelled || controller.signal.aborted) return
        setState((previous) => ({
          ...previous,
          error: err instanceof Error ? err : new Error(String(err)),
        }))
      } finally {
        if (!cancelled) {
          timer = setTimeout(function next() {
            if (document.visibilityState === "visible") void tick()
            else timer = setTimeout(next, intervalMs)
          }, intervalMs)
        }
      }
    }
    void tick()
    return () => {
      cancelled = true
      controller.abort()
      clearTimeout(timer)
    }
  }, [enabled, intervalMs, attempt])

  return useMemo(() => ({ ...state, refresh }), [state, refresh])
}

/** The previous rings with the new points appended, trimmed, and gone devices dropped. */
export function merge(
  previous: Record<string, NetworkLivePoint[]>,
  incoming: Record<string, NetworkLivePoint[]>,
): Record<string, NetworkLivePoint[]> {
  const out: Record<string, NetworkLivePoint[]> = {}
  for (const [name, points] of Object.entries(incoming)) {
    const before = previous[name] ?? []
    const last = before.at(-1)?.t ?? 0
    const joined = before.concat(points.filter((p) => p.t > last))
    out[name] = joined.length > KEEP ? joined.slice(joined.length - KEEP) : joined
  }
  return out
}

/** A device's last `n` readings of one direction, for a tile's trend. */
export function lastValues(
  points: NetworkLivePoint[] | undefined,
  direction: "rx" | "tx" | "both",
  n = 150,
): number[] {
  const tail = (points ?? []).slice(-n)
  return tail.map((p) => (direction === "both" ? p.rx + p.tx : p[direction]))
}

/** Several devices summed point by point, aligned on time — the host's whole uplink, every tunnel. */
export function sumSeries(series: NetworkLivePoint[][]): NetworkLivePoint[] {
  const byTime = new Map<number, NetworkLivePoint>()
  for (const points of series) {
    for (const p of points) {
      const at = byTime.get(p.t)
      if (at) {
        at.rx += p.rx
        at.tx += p.tx
      } else byTime.set(p.t, { ...p })
    }
  }
  return [...byTime.values()].sort((a, b) => a.t - b.t)
}
