"use client"

import { useCallback, useMemo, useState } from "react"
import { del, get, post } from "@/lib/api"
import type { UninstallMode, UninstallPlan, UninstallReport, UninstallRun } from "@/lib/types"
import { isLive } from "@/lib/uninstall"
import { usePoll } from "@/hooks/use-poll"

/**
 * Removing the dashboard, watched from the page it removes.
 *
 * The restart's gotcha with no recovery: the backend goes away in the middle
 * of the thing this is watching, and this time it does not come back. So the
 * last good report is kept (usePoll does that), and a failed poll while a run
 * is live is reported as `lost` rather than as an error — the page decides
 * from the phase whether that is the stack coming down or something wrong.
 */

const LIVE_POLL = 2000
const IDLE_POLL = 30_000

export function useSelfUninstall({ enabled = true }: { enabled?: boolean } = {}) {
  const [live, setLive] = useState(false)

  const fetchReport = useCallback(async (signal: AbortSignal) => {
    const next = await get<UninstallReport>("/dashboard/uninstall", undefined, signal)
    setLive(Boolean(next.run && isLive(next.run)))
    return next
  }, [])

  const poll = usePoll(fetchReport, live ? LIVE_POLL : IDLE_POLL, [], { enabled })
  const { data: report, error, refresh } = poll
  const running = Boolean(report?.run && isLive(report.run))

  /** What the dialog shows: everything that would go and everything that stays. */
  const plan = useCallback((signal?: AbortSignal) => {
    return get<UninstallPlan>("/dashboard/uninstall/plan", undefined, signal)
  }, [])

  const start = useCallback(
    async (mode: UninstallMode, deleteBackups: boolean, phrase: string) => {
      const run = await post<UninstallRun>(
        "/dashboard/uninstall",
        { mode, deleteBackups },
        // Only a wipe is typed for; the server ignores a phrase on a keep.
        mode === "wipe" ? { confirm: phrase } : {},
      )
      setLive(true)
      refresh()
      return run
    },
    [refresh],
  )

  const dismiss = useCallback(async () => {
    await del("/dashboard/uninstall/run")
    refresh()
  }, [refresh])

  return useMemo(
    () => ({
      report,
      running,
      // A poll that fails while a run is live is the run, not a fault.
      error: running ? undefined : error,
      lost: running && Boolean(error),
      plan,
      start,
      dismiss,
    }),
    [report, running, error, plan, start, dismiss],
  )
}
