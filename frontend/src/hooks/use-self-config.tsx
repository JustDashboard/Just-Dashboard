"use client"

import { useCallback, useMemo, useState } from "react"
import { del, get, post, put } from "@/lib/api"
import type { DashboardConfigReport, DashboardConfigRun, DashboardSettings } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"

/**
 * The dashboard's own settings, and the restart that puts a change into
 * effect.
 *
 * The gotcha is the same one internal/selfcfg is built around: **the backend
 * goes away in the middle of the thing this is watching**. Applying a setting
 * recreates the container serving this API, so the poll starts failing, and a
 * UI that rendered a failed poll as an error would report the restart as
 * broken at exactly the moment it is working. So the last good report is kept
 * (usePoll does that already) and a failure while a run is in flight is
 * rendered as "restarting", not as a fault.
 *
 * The second gotcha has no equivalent in the update flow: a change to the port
 * or the address means the dashboard does not come back *here*. Nothing on
 * this side can follow it — the record says where it went, and the card says
 * so in as many words rather than spinning forever on an address that is now
 * answering nothing.
 */

const LIVE_POLL = 2000
const IDLE_POLL = 30_000

export function useSelfConfig() {
  const [live, setLive] = useState(false)

  const fetchReport = useCallback(async (signal: AbortSignal) => {
    const next = await get<DashboardConfigReport>("/dashboard/config", undefined, signal)
    setLive(next.run?.status === "running" || next.run?.status === "pending")
    return next
  }, [])

  const poll = usePoll(fetchReport, live ? LIVE_POLL : IDLE_POLL, [live])
  const { data: report, error, refresh } = poll

  const run = report?.run
  const running = run?.status === "running" || run?.status === "pending"

  const apply = useCallback(
    async (settings: DashboardSettings) => {
      const started = await put<DashboardConfigRun>("/dashboard/config", settings)
      // Straight to the fast poll: the first phase change lands within a
      // second or two, and waiting half a minute to notice would look broken.
      setLive(true)
      refresh()
      return started
    },
    [refresh],
  )

  const restart = useCallback(
    async (rebuild: boolean) => {
      const started = await post<DashboardConfigRun>("/dashboard/restart", { rebuild })
      setLive(true)
      refresh()
      return started
    },
    [refresh],
  )

  /**
   * Ask for the Tailscale certificate now.
   *
   * The keeper gets there on its own, but the operator who has just switched
   * HTTPS on in their tailnet is looking at a browser warning and should not
   * have to wait out a timer to see it go.
   */
  const issueCertificate = useCallback(async () => {
    await post("/dashboard/config/certificate")
    refresh()
  }, [refresh])

  const dismiss = useCallback(async () => {
    await del("/dashboard/config/run")
    refresh()
  }, [refresh])

  return useMemo(
    () => ({
      report,
      loading: poll.loading,
      // A poll that fails while a restart is in flight is the restart doing
      // its job, not a fault, and the two must not look the same.
      error: running ? undefined : error,
      restarting: Boolean(error) && Boolean(running),
      running,
      refresh,
      apply,
      restart,
      issueCertificate,
      dismiss,
    }),
    [report, poll.loading, error, running, refresh, apply, restart, issueCertificate, dismiss],
  )
}

/** What the phase of a running restart is called on screen. */
export function configPhaseLabel(run: DashboardConfigRun): string {
  switch (run.phase) {
    case "queued":
      return "Starting"
    case "applying":
      return run.action === "rebuild" ? "Rebuilding the images" : "Recreating the containers"
    case "waiting":
      return "Waiting for the dashboard to answer"
    case "rollback":
      return "Putting the previous configuration back"
    default:
      return "Restarting"
  }
}
