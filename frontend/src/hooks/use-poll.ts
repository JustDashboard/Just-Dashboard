"use client"

import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { ApiError } from "@/lib/api"

type PollState<T> = {
  data: T | undefined
  error: Error | undefined
  loading: boolean
  refresh: () => void
}

type PollOptions = {
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
}

/**
 * Fetches on mount and schedules the next poll after each request settles,
 * so a slow endpoint cannot stack requests. The interval is paused while
 * the tab is hidden — a dashboard left open in a background tab should not
 * keep hammering the server it is monitoring.
 */
export function usePoll<T>(
  fetcher: (signal: AbortSignal) => Promise<T>,
  intervalMs = 5000,
  deps: unknown[] = [],
  { enabled = true }: PollOptions = {},
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
    let timer: ReturnType<typeof setTimeout> | undefined

    const schedule = () => {
      if (cancelled || intervalMs <= 0) return
      timer = setTimeout(() => {
        if (document.visibilityState === "visible") void run()
        else schedule()
      }, intervalMs)
    }

    const run = async () => {
      try {
        const next = await fetcherRef.current(controller.signal)
        if (cancelled) return
        setResult({ resource, data: next })
      } catch (err) {
        if (cancelled || controller.signal.aborted) return
        if (err instanceof DOMException && err.name === "AbortError") return
        setResult((previous) => ({
          resource,
          data: previous?.resource === resource ? previous.data : undefined,
          error: err instanceof Error ? err : new Error(String(err)),
        }))
      } finally {
        schedule()
      }
    }

    void run()
    return () => {
      cancelled = true
      controller.abort()
      clearTimeout(timer)
    }
  }, [intervalMs, tick, enabled, resource])

  // A disabled poll is not loading: nothing is in flight, and reporting
  // otherwise would leave a caller showing a skeleton forever.
  const current = result?.resource === resource ? result : undefined
  return { data: current?.data, error: current?.error, loading: enabled && !current, refresh }
}

export function isAuthError(error: Error | undefined) {
  return error instanceof ApiError && error.isAuthProblem
}
