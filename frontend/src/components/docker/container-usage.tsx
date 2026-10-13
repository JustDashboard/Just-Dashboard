"use client"

import { useMemo } from "react"
import { get } from "@/lib/api"
import { byteScale, cpuScale, peakOf } from "@/lib/container-usage"
import {
  containerRows,
  HISTORY_RANGES,
  memoryLimit,
  RANGES,
  rangeSpec,
  windowQuery,
  windowRefreshMs,
  type ContainerRow,
} from "@/lib/metrics-range"
import type { ContainerHistory } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useMetricEvents } from "@/hooks/use-metrics-history"
import { useMetricsWindow } from "@/hooks/use-metrics-window"

/** What a live feed hands the charts: its rows, and the limit memory is drawn against. */
export type LiveRows = { rows: ContainerRow[]; memoryLimit: number }

/**
 * The window a container's charts cover and the rows they draw over it.
 *
 * With `live`, the Live range draws the stats socket's own rows — a frame a
 * second, the last five minutes — and every other range reads the recorded
 * history. The two are never spliced into one line (§10): their cadences differ
 * fifteenfold, and the recorded points are means where the live ones are not.
 * Without `live` a container has no buffer of its own, so a "live" preference
 * resolves to the narrowest recorded window rather than to nothing at all.
 */
export function useContainerUsage(containerId: string, live?: LiveRows) {
  const controls = useMetricsWindow()
  const win = controls.window
  const named = win.key === "live" && win.from === undefined
  const streaming = named && live !== undefined
  const effective = named && !live ? { ...win, key: "1h" as const } : win
  const params = windowQuery(effective, rangeSpec(effective.key).points)
  const signature = JSON.stringify(params)

  const { data, error, loading } = usePoll<ContainerHistory>(
    (signal) =>
      get<ContainerHistory>(
        `/docker/containers/${encodeURIComponent(containerId)}/stats/history`,
        params,
        signal,
      ),
    windowRefreshMs(effective),
    [containerId, signature],
    { enabled: !streaming },
  )

  // The same deploys, restarts and reboots the host charts are marked with.
  // A container's memory falling off a cliff is a different event depending on
  // whether the stack was redeployed a second earlier, and that is exactly the
  // fact these markers carry.
  const events = useMetricEvents(effective)

  const recorded = useMemo<ContainerRow[]>(() => (data ? containerRows(data) : []), [data])
  return {
    controls: { ...controls, window: effective },
    ranges: live ? RANGES : HISTORY_RANGES,
    streaming,
    rows: streaming ? live.rows : recorded,
    limit: streaming ? live.memoryLimit : memoryLimit(data),
    events,
    history: data,
    error: streaming ? undefined : error,
    loading: !streaming && loading,
  }
}

/**
 * Every axis a container's charts draw, ending on a round figure and ticking
 * at its quarters, peaks included, so the top tick is a number someone would
 * say aloud. Memoised, like everything handed to `ChartPanel`.
 */
export function useContainerScales(rows: ContainerRow[], limit: number, streaming: boolean) {
  return useMemo(() => {
    const memoryCeiling = Math.max(limit, peakOf(rows, ["mem", "memPeak"]))
    return {
      cpu: cpuScale(peakOf(rows, ["cpu", "cpuPeak"])),
      network: byteScale(peakOf(rows, ["netRx", "netTx", "netRxPeak", "netTxPeak"])),
      block: byteScale(
        peakOf(rows, ["blockRead", "blockWrite", "blockReadPeak", "blockWritePeak"]),
      ),
      // A limited container is scaled to its limit rather than to the data. A
      // container sitting at a quarter of its ceiling draws a short line,
      // which is the useful picture: an axis fitted to the series makes every
      // container look equally close to being killed, and pushes the limit
      // line off the top of the chart where recharts silently discards it.
      // The ticks are the limit's quarters, so the top one names the limit.
      memory:
        limit > 0
          ? {
              domain: [0, Math.round(memoryCeiling * 1.04)] as [number, number],
              ticks:
                memoryCeiling === limit
                  ? [0, limit / 4, limit / 2, (limit * 3) / 4, limit]
                  : undefined,
              // The limit is the line that explains an OOM kill, so it is
              // drawn even when the series never gets near it.
              thresholds: [
                {
                  value: limit,
                  label: streaming ? "limit" : "highest recorded limit",
                  tone: "danger" as const,
                },
              ],
            }
          : { ...byteScale(memoryCeiling), thresholds: undefined },
    }
  }, [rows, limit, streaming])
}
