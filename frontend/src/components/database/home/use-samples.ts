"use client"

import { historySamples, type Sample } from "@/components/database/home/samples"
import { read } from "@/components/database/home/read"
import { usePoll } from "@/hooks/use-poll"
import { get } from "@/lib/api"
import { useViewState } from "@/lib/view-state"
import { useMemo } from "react"

export const SAMPLE_EVERY_MS = 30_000
export const HISTORY_RANGES = [
  { hours: 1, label: "1 hour" },
  { hours: 6, label: "6 hours" },
  { hours: 24, label: "24 hours" },
  { hours: 168, label: "7 days" },
] as const

export type StatsHistory<T> = { samples: { at: number; stats: T; gap: boolean }[] }

/** Read history independently of the live snapshot so an offline server
 * still has recorded activity. Failed refreshes retain the previous run. */
export function useSamples<T>(
  id: number,
  toSample: (answer: T) => Sample | undefined,
  enabled: boolean,
  historyEnabled = true,
) {
  const [chosen, setHours] = useViewState(`databases.${id}.history.hours`, 1)
  const hours = HISTORY_RANGES.some((range) => range.hours === chosen) ? chosen : 1
  const live = usePoll(
    (signal) => get<T>(`/databases/${id}/stats`, undefined, signal),
    SAMPLE_EVERY_MS,
    [id],
    { enabled },
  )
  const history = usePoll(
    (signal) =>
      read<StatsHistory<T>>(
        `/databases/${id}/stats/history`,
        (answer) => Array.isArray(answer.samples),
        { hours },
        signal,
      ),
    SAMPLE_EVERY_MS,
    [id, hours],
    { enabled: historyEnabled },
  )
  const samples = useMemo(
    () => historySamples(history.data?.samples ?? [], toSample),
    [history.data, toSample],
  )
  return {
    answer: live.data,
    samples,
    hours,
    setHours,
    loading: history.loading,
    error: history.error ?? live.error,
    refresh: () => {
      history.refresh()
      live.refresh()
    },
  }
}
