"use client"

import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { ApiError } from "@/lib/api"

export type PollState<T> = {
  data: T | undefined
  error: Error | undefined
  loading: boolean
  lastSuccess?: number
  refresh: () => void
}

type PollOptions<T> = {
  /**
   * Whether to fetch at all. False leaves the last result in place and makes
   * no request.
   *
   * `useSocket` has always had this and `usePoll` did not, which turned out to
   * matter: a detail panel that is closed still renders, and a fetcher built
   * as `/docker/stacks/${id ?? ""}` with no id addresses the *list* endpoint —
   * so a closed panel quietly received an array, rendered `data.services.map`
   * on it, and took the whole page down with a TypeError. Guarding each call
   * site with a ternary would have worked; a hook that can be switched off is
   * the version the next panel gets for free.
   */
  enabled?: boolean
  /**
   * A reading to draw until the first answer lands: what the same read said
   * the last time the page was open. A page the reader comes back to is drawn
   * at once rather than as its skeleton, and `loading` stays true until the
   * new answer replaces it.
   */
  seed?: T
}

/** The part of `document` a poll watches, so the schedule can be tested without one. */
type PollPage = Pick<Document, "visibilityState" | "addEventListener" | "removeEventListener">

/**
 * Fetches now and schedules the next fetch after each one settles, so a slow
 * endpoint cannot stack requests. The interval stops while the tab is hidden —
 * a dashboard left open in a background tab should not keep hammering the
 * server it is monitoring — and coming back fetches at once rather than
 * showing a stale page until a timer is next due. A fetch already in flight is
 * the refresh; its own settling schedules the next.
 *
 * An interval of 0 fetches once: it has no schedule to catch up on.
 * `fetchOnce` must not reject. Returns what stops it.
 */
export function pollLoop(
  fetchOnce: () => Promise<void>,
  intervalMs: number,
  page: PollPage = document,
): () => void {
  let stopped = false
  let fetching = false
  let timer: ReturnType<typeof setTimeout> | undefined

  const schedule = () => {
    if (stopped || intervalMs <= 0) return
    timer = setTimeout(() => {
      // Hidden, nothing rearms this: the return to the tab does.
      if (page.visibilityState === "visible") void run()
    }, intervalMs)
  }

  const run = async () => {
    fetching = true
    await fetchOnce()
    fetching = false
    schedule()
  }

  const onVisibility = () => {
    if (page.visibilityState !== "visible" || fetching) return
    clearTimeout(timer)
    void run()
  }

  void run()
  if (intervalMs > 0) page.addEventListener("visibilitychange", onVisibility)
  return () => {
    stopped = true
    clearTimeout(timer)
    page.removeEventListener("visibilitychange", onVisibility)
  }
}

/** `pollLoop` as a hook: the latest result, and a way to ask again. */
export function usePoll<T>(
  fetcher: (signal: AbortSignal) => Promise<T>,
  intervalMs = 5000,
  deps: unknown[] = [],
  { enabled = true, seed }: PollOptions<T> = {},
): PollState<T> {
  // A different resource must never borrow the previous resource's rows or
  // permissions while it loads. Refreshes of the same resource retain data.
  // Callers supply a fixed-length list of resource identity dependencies.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  const resource = useMemo(() => ({}), deps)
  const [result, setResult] = useState<{
    resource: object
    data?: T
    error?: Error
    lastSuccess?: number
  }>()
  const [tick, setTick] = useState(0)
  // The fetcher is closed over by the interval, so it is kept in a ref that
  // is synced after render rather than assigned during it — writing a ref
  // mid-render is what makes a component's output depend on when it ran.
  const fetcherRef = useRef(fetcher)
  useEffect(() => {
    fetcherRef.current = fetcher
  })

  const refresh = useCallback(() => setTick((t) => t + 1), [])

  useEffect(() => {
    if (!enabled) return
    let cancelled = false
    const controller = new AbortController()

    const stop = pollLoop(async () => {
      try {
        const next = await fetcherRef.current(controller.signal)
        if (cancelled) return
        setResult({ resource, data: next, lastSuccess: Date.now() })
      } catch (err) {
        if (cancelled || controller.signal.aborted) return
        if (err instanceof DOMException && err.name === "AbortError") return
        setResult((previous) => ({
          resource,
          data: previous?.resource === resource ? previous.data : undefined,
          error: err instanceof Error ? err : new Error(String(err)),
          lastSuccess: previous?.resource === resource ? previous.lastSuccess : undefined,
        }))
      }
    }, intervalMs)

    return () => {
      cancelled = true
      controller.abort()
      stop()
    }
  }, [intervalMs, tick, enabled, resource])

  // A disabled poll is not loading: nothing is in flight, and reporting
  // otherwise would leave a caller showing a skeleton forever.
  const current = result?.resource === resource ? result : undefined
  return {
    data: current ? current.data : seed,
    error: current?.error,
    lastSuccess: current?.lastSuccess,
    loading: enabled && !current,
    refresh,
  }
}

export function isAuthError(error: Error | undefined) {
  return error instanceof ApiError && error.isAuthProblem
}
