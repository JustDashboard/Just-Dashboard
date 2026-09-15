"use client"

import { createContext, useCallback, useContext, useMemo, useRef, useState } from "react"
import { del, get, post } from "@/lib/api"
import type { SelfUpdateReport, UpdateRun } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"

/**
 * The dashboard's own version, shared by the sidebar notice and the Dashboard
 * page.
 *
 * A provider rather than a hook per component, for one reason and one gotcha.
 * The reason: both are on screen at once on /dashboard, and two polls of the
 * same three facts is one too many. The gotcha is the whole design problem of
 * this feature — **the backend goes away in the middle of the thing we are
 * watching**. An upgrade recreates the container serving this API, so the poll
 * starts failing, and a UI that renders a failed poll as an error would report
 * the update as broken at precisely the moment it is working. So the last good
 * report is kept (usePoll does that already), and a failure while a run was in
 * flight is rendered as "restarting", not as a fault.
 *
 * Every page load also *asks*: the first read carries `nudge`, which tells the
 * server to check the repository behind the request rather than inside it, and
 * one quick follow-up read picks the answer up. That is what makes a reload a
 * way of finding out there is a new version, without making a reload wait on
 * a network the server may not have.
 *
 * The frontend container is recreated too, so the page it is running is served
 * by something that is briefly not there. Nothing here can fix that — but the
 * JavaScript is already loaded, so as long as nobody reloads, this keeps
 * polling and picks the story back up. The card says so.
 */

const LIVE_POLL = 2000
const IDLE_POLL = 5 * 60 * 1000
/**
 * The one quick re-read after a page load.
 *
 * The load itself asks the server to check (`nudge`), and that check happens
 * *behind* the request rather than inside it — nothing about opening a page
 * should wait fifteen seconds on a repository that is unreachable. So the
 * first response is the cached answer, and this is what picks up the fresh one
 * a second or two later. Exactly one of these: after it the poll drops to the
 * idle cadence, because the question has been asked and answered.
 */
const SETTLE_POLL = 6000

type SelfUpdateValue = {
  report: SelfUpdateReport | undefined
  loading: boolean
  /** A genuine failure to read the status, as opposed to a restart. */
  error: Error | undefined
  /** The API is unreachable and an upgrade we started explains why. */
  restarting: boolean
  refresh: () => void
  /** Ask the repository now, and wait for the answer. */
  check: () => Promise<void>
  checking: boolean
  install: (confirm: string) => Promise<void>
  /** Forget a finished run, here and — where permitted — for everyone. */
  dismiss: () => Promise<void>
  /** The finished run this browser has not yet dismissed. */
  outcome: UpdateRun | undefined
}

const SelfUpdateContext = createContext<SelfUpdateValue | null>(null)

/** Runs that have been read on this screen, so a notice clears per browser. */
const SEEN_KEY = "jd.update.seen"

function readSeen(): string {
  if (typeof window === "undefined") return ""
  try {
    return window.localStorage.getItem(SEEN_KEY) ?? ""
  } catch {
    return ""
  }
}

export function SelfUpdateProvider({ children }: { children: React.ReactNode }) {
  const { can } = useAuth()
  const [live, setLive] = useState(false)
  // True until the load-time check has had a chance to land. See SETTLE_POLL.
  const [settling, setSettling] = useState(true)
  const readCount = useRef(0)
  const [checking, setChecking] = useState(false)
  const [seen, setSeen] = useState(readSeen)

  // The cadence is decided by the answer, in the fetch rather than in an
  // effect watching it: following a run means polling every two seconds, and
  // the rest of the time this is a fact that changes a few times a year that a
  // dashboard left open on a wall screen should not ask about every second.
  //
  // A *failed* fetch deliberately leaves the cadence alone. During an upgrade
  // the API is unreachable precisely because the upgrade is working, and
  // dropping back to the five-minute poll there would mean noticing it
  // finished five minutes later.
  const fetchReport = useCallback(async (signal: AbortSignal) => {
    const reads = ++readCount.current
    // `nudge` on the first read of this browser's session is the whole of
    // "check for updates when the page loads". It costs the server one
    // conditional background fetch, floored at a few minutes, so a tab that is
    // reloaded twenty times asks the repository once.
    const query = reads === 1 ? { nudge: true } : undefined
    const next = await get<SelfUpdateReport>("/dashboard/update", query, signal)
    setLive(next.run?.status === "running" || next.run?.status === "pending")
    if (reads >= 2) setSettling(false)
    return next
  }, [])

  const poll = usePoll(fetchReport, live ? LIVE_POLL : settling ? SETTLE_POLL : IDLE_POLL)
  const { data: report, error, refresh } = poll

  const run = report?.run
  const running = run?.status === "running" || run?.status === "pending"

  const check = useCallback(async () => {
    setChecking(true)
    try {
      await get<SelfUpdateReport>("/dashboard/update", { refresh: true })
      refresh()
    } finally {
      setChecking(false)
    }
  }, [refresh])

  const install = useCallback(
    async (confirm: string) => {
      await post<UpdateRun>("/dashboard/update/install", { version: report?.latest }, { confirm })
      // Straight to the fast poll: the first phase change lands within a
      // second or two and waiting five minutes to notice would look broken.
      setLive(true)
      refresh()
    },
    [report?.latest, refresh],
  )

  const dismiss = useCallback(async () => {
    const id = run?.id ?? ""
    if (id) {
      setSeen(id)
      try {
        window.localStorage.setItem(SEEN_KEY, id)
      } catch {
        // A browser with storage disabled simply sees the notice again on the
        // next load, which is a good deal better than a thrown exception.
      }
    }
    // Clearing the record for everyone is an admin's to do; a read-only
    // operator dismissing the notice on their own screen is not a failure and
    // should not be reported as one.
    if (!can("system.admin")) return
    try {
      await del("/dashboard/update/run")
      refresh()
    } catch {
      // The local dismissal above already did what the operator asked for.
    }
  }, [run?.id, can, refresh])

  const value = useMemo<SelfUpdateValue>(() => {
    const finished = run && (run.status === "success" || run.status === "failed") ? run : undefined
    return {
      report,
      loading: poll.loading,
      // A poll that fails while an upgrade is in flight is the upgrade doing
      // its job, not a fault, and the two must not look the same.
      error: running ? undefined : error,
      restarting: Boolean(error) && Boolean(running),
      refresh,
      check,
      checking,
      install,
      dismiss,
      outcome: finished && finished.id !== seen ? finished : undefined,
    }
  }, [report, poll.loading, error, running, refresh, check, checking, install, dismiss, run, seen])

  return <SelfUpdateContext.Provider value={value}>{children}</SelfUpdateContext.Provider>
}

export function useSelfUpdate() {
  const ctx = useContext(SelfUpdateContext)
  if (!ctx) throw new Error("useSelfUpdate must be used inside SelfUpdateProvider")
  return ctx
}

/** What the phase of a running upgrade is called on screen. */
export function phaseLabel(run: UpdateRun): string {
  switch (run.phase) {
    case "queued":
      return "Starting the updater"
    case "fetching":
      return "Fetching the new version"
    case "building":
      return "Building and restarting"
    case "restarting":
      return "Waiting for the dashboard"
    default:
      return "Updating"
  }
}
