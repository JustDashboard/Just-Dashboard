"use client"

import { useCallback, useEffect, useSyncExternalStore } from "react"
import { get, ApiError } from "@/lib/api"
import { usePoll } from "@/hooks/use-poll"
import {
  currentRange,
  rangeSpec,
  serverRange,
  storeRange,
  subscribeRange,
  windowQuery,
  windowRefreshMs,
  type MetricsWindow,
  type RangeKey,
} from "@/lib/metrics-range"
import type { Health, MetricEvent, MetricsHistory, StorageHistory } from "@/lib/types"

function useMetricsRefresh(refresh: () => void) {
  useEffect(() => {
    window.addEventListener("jd:metrics-refresh", refresh)
    return () => window.removeEventListener("jd:metrics-refresh", refresh)
  }, [refresh])
}

export type HistoryState = {
  history: MetricsHistory | undefined
  error: Error | undefined
  loading: boolean
  /** True when the server is not recording history at all, which is a setting rather than a fault. */
  disabled: boolean
}

/**
 * Reads the server's recorded history for one window.
 *
 * This is the half of the metrics story that outlives the browser tab. The
 * live socket can only ever show the time since the page was opened; this
 * endpoint is what makes the overnight spike visible at all, so the Overview
 * page defaults to it rather than to the live feed.
 *
 * Disabled for the "live" range, where there is nothing to fetch.
 */
export function useMetricsHistory(win: MetricsWindow): HistoryState {
  const spec = rangeSpec(win.key)
  // The live range has nothing to fetch; a zoomed window always does, even
  // when the range underneath it was "live" — dragging across the live chart
  // is a request for the recorded detail of that span.
  const enabled = Boolean(spec.query) || win.from !== undefined
  const params = enabled ? windowQuery(win, spec.points) : null
  const signature = params ? JSON.stringify(params) : ""

  const fetcher = useCallback(
    (signal: AbortSignal) => {
      if (!params) return Promise.resolve(undefined)
      return get<MetricsHistory>("/system/metrics/history", params, signal)
    },
    // Compared by value: rebuilding the parameter object every render would
    // otherwise restart the poll on every parent re-render.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [signature],
  )

  // Polling is paused on a hidden tab by usePoll, so a dashboard left open in
  // a background tab does not keep asking the server it is monitoring for a
  // week of history every five minutes.
  const { data, error, loading, refresh } = usePoll<MetricsHistory | undefined>(
    fetcher,
    enabled ? windowRefreshMs(win) : 0,
    [signature],
  )

  useMetricsRefresh(refresh)

  const disabled = error instanceof ApiError && error.code === "metrics_history_disabled"
  return {
    history: data,
    error: disabled ? undefined : error,
    loading: enabled && loading,
    disabled,
  }
}

export type StorageState = {
  storage: StorageHistory | undefined
  error: Error | undefined
  loading: boolean
  disabled: boolean
}

/**
 * Reads per-filesystem capacity over the same window as the host series.
 *
 * Its own request rather than a field on the host response: the host charts
 * refresh every fifteen seconds and capacity moves in hours, so folding this
 * into that response would ship a breakdown of every mount forty times an hour
 * to draw a line that barely changes.
 */
export function useStorageHistory(win: MetricsWindow): StorageState {
  const spec = rangeSpec(win.key)
  const enabled = Boolean(spec.query) || win.from !== undefined
  const params = enabled ? windowQuery(win, Math.min(spec.points, 200)) : null
  const signature = params ? JSON.stringify(params) : ""

  const fetcher = useCallback(
    (signal: AbortSignal) => {
      if (!params) return Promise.resolve(undefined)
      return get<StorageHistory>("/system/metrics/storage", params, signal)
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [signature],
  )

  const { data, error, loading, refresh } = usePoll<StorageHistory | undefined>(
    fetcher,
    // Capacity is not a live figure. Refreshing it on the charts' cadence
    // would be four requests a minute for a line that moves in hours.
    enabled ? Math.max(windowRefreshMs(win), 60_000) : 0,
    [signature],
  )

  useMetricsRefresh(refresh)

  const disabled = error instanceof ApiError && error.code === "metrics_history_disabled"
  return {
    storage: data,
    error: disabled ? undefined : error,
    loading: enabled && loading,
    disabled,
  }
}

/**
 * What happened during the window, for the markers on the charts.
 *
 * Fetched separately from the series rather than folded into it, because the
 * two change at completely different rates: the series is re-read every
 * fifteen seconds and the events are the same handful of deploys and reboots
 * for hours at a time. It also keeps working when history recording is off —
 * deploys and audited actions are stored regardless, and a marker on a live
 * chart is worth as much as one on a recorded chart.
 */
export function useMetricEvents(win: MetricsWindow): MetricEvent[] {
  const spec = rangeSpec(win.key)
  // A live window still gets markers: it covers the last few minutes, which is
  // exactly when you are watching the effect of something you just did.
  const params = windowQuery(
    win.from !== undefined ? win : { key: win.key === "live" ? "1h" : win.key },
    0,
  )
  const signature = JSON.stringify(params)

  const fetcher = useCallback(
    (signal: AbortSignal) =>
      get<MetricEvent[]>("/system/metrics/events", { ...params, limit: 120 }, signal),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [signature],
  )

  const { data, refresh } = usePoll<MetricEvent[]>(
    fetcher,
    // Events are cheap but they are not live data, and a zoomed window is a
    // fixed span in the past that never needs re-reading at all.
    win.from !== undefined ? 0 : Math.max(spec.refreshMs, 30_000),
    [signature],
  )
  useMetricsRefresh(refresh)
  return data ?? EMPTY_EVENTS
}

// Module-level so a component reading events does not see a fresh array
// identity every render and re-run whatever depends on it.
const EMPTY_EVENTS: MetricEvent[] = []

/**
 * The server's verdict on the host.
 *
 * Polled rather than streamed: the checks read an hour of recorded history to
 * tell a spike from a trend, which is not work to repeat on every 2s frame. A
 * minute is fast enough for a condition that is, by construction, sustained.
 */
export function useHealth(intervalMs = 60_000): {
  health: Health | undefined
  error: Error | undefined
  loading: boolean
} {
  const fetcher = useCallback(
    (signal: AbortSignal) => get<Health>("/system/health", undefined, signal),
    [],
  )
  const { data, error, loading, refresh } = usePoll<Health>(fetcher, intervalMs, [])
  useMetricsRefresh(refresh)
  useEffect(() => {
    window.addEventListener("jd:health-changed", refresh)
    return () => window.removeEventListener("jd:health-changed", refresh)
  }, [refresh])
  return { health: data, error, loading }
}

/**
 * The chosen window, remembered across reloads.
 *
 * Backed by an external store rather than component state so the preference
 * can be read during render: reading localStorage in an effect and setting
 * state from it would render the wrong window first and then correct it, which
 * costs a fetch of the default range on every page load.
 */
export function useRangePreference(): [RangeKey, (key: RangeKey) => void] {
  const range = useSyncExternalStore(subscribeRange, currentRange, serverRange)
  return [range, storeRange]
}
