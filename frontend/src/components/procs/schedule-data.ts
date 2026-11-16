"use client"

import { useEffect, useMemo } from "react"
import { get } from "@/lib/api"
import { scheduleLanes } from "@/lib/schedule"
import type { Crontab, LogSourceIndex } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useNow } from "@/hooks/use-now"
import { useViewState } from "@/lib/view-state"
import { BAND_WINDOW } from "@/components/procs/schedule-band"
import { cronLogSource } from "@/components/procs/shared"
import type { TimerList } from "@/components/procs/timers"

/** How soon a log index that failed to answer is asked again. */
const INDEX_RETRY = 30_000

/**
 * Everything that runs on a clock, read once for the Scheduled page and for
 * the page of one schedule opened from it: one account's crontab, the host's
 * timers, the package-owned cron files and where cron's log is. The page's
 * band and the detail pages' rail are both made of the lanes this returns.
 *
 * `user` is whose crontab is read; left out, it is the account the Scheduled
 * page was left on.
 */
export function useSchedules(user?: string) {
  const [remembered, setUser] = useViewState("processes.cron.user", "root")
  const account = user ?? remembered
  const users = usePoll((signal) => get<string[]>("/cron/users", undefined, signal), 0)
  const crontab = usePoll(
    (signal) => get<Crontab>(`/cron/user/${encodeURIComponent(account)}`, undefined, signal),
    30_000,
    [account],
  )
  const timers = usePoll((signal) => get<TimerList>("/systemd/timers", undefined, signal), 30_000)
  const system = usePoll((signal) => get<Crontab[]>("/cron/system", undefined, signal), 0)
  // Where cron's log is on this host is a question the log index answers
  // once: the daemon's unit, a cron file, or the journal by program. A read
  // that failed is asked again, or the page would say so until reloaded.
  const logs = usePoll((signal) => get<LogSourceIndex>("/logs/sources", undefined, signal), 0)
  useEffect(() => {
    if (!logs.error) return
    const retry = setTimeout(logs.refresh, INDEX_RETRY)
    return () => clearTimeout(retry)
  }, [logs.error, logs.refresh])
  const cron = useMemo(() => cronLogSource(logs.data), [logs.data])

  // Every time is on the host's clock, the one its schedules are written in.
  const zone = crontab.data?.timeZone ?? system.data?.find((file) => file.timeZone)?.timeZone
  const systemd = Boolean(timers.data?.available)
  // The lanes are drawn from this minute: an expression's runs are computed
  // once a minute, and every countdown ticks from them in between.
  const now = useNow(60_000)
  const minute = Math.floor(now / 60_000) * 60_000
  const lanes = useMemo(
    () =>
      scheduleLanes({
        crontab: crontab.data,
        timers: timers.data?.timers,
        system: system.data,
        from: minute,
        until: minute + BAND_WINDOW,
        zone,
        systemd,
      }),
    [crontab.data, timers.data, system.data, minute, zone, systemd],
  )
  return {
    user: account,
    setUser,
    users,
    crontab,
    timers,
    system,
    logs,
    cron,
    zone,
    systemd,
    lanes,
    settled: Boolean(crontab.data && timers.data),
  }
}
