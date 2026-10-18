"use client"

import { historySamples, type Sample } from "@/components/database/home/samples"
import { read } from "@/components/database/home/read"
import { usePoll } from "@/hooks/use-poll"
import { get } from "@/lib/api"
import { useViewState } from "@/lib/view-state"
import { useMemo, useState } from "react"

export const SAMPLE_EVERY_MS = 30_000
export const HISTORY_RANGES = [
  { hours: 1, label: "1 hour", short: "1h" },
  { hours: 6, label: "6 hours", short: "6h" },
  { hours: 24, label: "24 hours", short: "24h" },
  { hours: 168, label: "7 days", short: "7d" },
  { hours: 336, label: "14 days", short: "14d" },
  { hours: 720, label: "30 days", short: "30d" },
  { hours: 2160, label: "90 days", short: "90d" },
] as const

/** What the recorder keeps until an operator has chosen longer. */
export const DEFAULT_RETENTION_HOURS = 168

export type StatsHistory<T> = {
  samples: { at: number; stats: T; gap: boolean }[]
  retentionHours: number
}

/** The ranges the recorded history can answer: none reaches past what is kept. */
export function rangesWithin(retentionHours: number) {
  return HISTORY_RANGES.filter((range) => range.hours <= retentionHours)
}

function longestWithin(retentionHours: number) {
  return rangesWithin(retentionHours).at(-1)?.hours ?? 1
}

/** The days the recorder keeps its readings, as the footnotes say it. */
export function keptFor(retentionHours: number) {
  const days = Math.round(retentionHours / 24)
  return `${days} ${days === 1 ? "day" : "days"}`
}

/** Read history independently of the live snapshot so an offline server
 * still has recorded activity. Failed refreshes retain the previous run. */
export function useSamples<T>(
  id: number,
  toSample: (answer: T) => Sample | undefined,
  enabled: boolean,
  historyEnabled = true,
) {
  const [chosen, setHours] = useViewState(`databases.${id}.history.hours`, 1)
  // What the recorder keeps is learned from its first answer and held across
  // range changes, which restart the poll. A range saved while more was kept
  // falls back to the longest one that is still kept, rather than asking for
  // history that no longer exists.
  const [kept, setKept] = useState<number>()
  const known = HISTORY_RANGES.some((range) => range.hours === chosen)
  const hours = !known ? 1 : kept === undefined || chosen <= kept ? chosen : longestWithin(kept)
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
  const answered = history.data?.retentionHours
  if (answered !== undefined && answered !== kept) setKept(answered)
  const samples = useMemo(
    () => historySamples(history.data?.samples ?? [], toSample),
    [history.data, toSample],
  )
  return {
    answer: live.data,
    samples,
    retentionHours: kept ?? DEFAULT_RETENTION_HOURS,
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
