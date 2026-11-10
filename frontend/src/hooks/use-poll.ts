"use client"

import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { ApiError } from "@/lib/api"
import { askedToWait, isTransient, waitAfterFailure } from "@/lib/backoff"

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
  /**
   * Keep the cadence through a failure that names no wait: for the few reads
   * that watch the dashboard through its own restart (an update, a settings
   * restart, an uninstall), where a 502 is the expected answer for a while and
   * the page must notice the moment it ends. Every other poll backs off
   * (`pollLoop`). A 429's `Retry-After` is waited out either way.
   */
  steady?: boolean
}

/** The part of `document` a poll watches, so the schedule can be tested without one. */
type PollPage = Pick<Document, "visibilityState" | "addEventListener" | "removeEventListener">

/** What a fetch hands its schedule when it failed: the error, so the wait can fit it. */
export type PollFailure = { failed: unknown }

type LoopOptions = {
  /**
   * Keep the cadence through a failure that names no wait of its own. See
   * `PollOptions.steady`. Asked at each failure, so a caller may change its
   * mind without starting the loop again.
   */
  steady?: () => boolean
  /** The clock and the dice, so a test can hold both. */
  now?: () => number
  random?: () => number
}

/**
 * Fetches now and schedules the next fetch after each one settles, so a slow
 * endpoint cannot stack requests. The interval stops while the tab is hidden —
 * a dashboard left open in a background tab should not keep hammering the
 * server it is monitoring — and coming back fetches at once rather than
 * showing a stale page until a timer is next due. A fetch already in flight is
 * the refresh; its own settling schedules the next.
 *
 * A fetch that failed for a reason asking again cannot have fixed yet (the
 * limiter's 429, a 5xx, no answer at all: `isTransient`) is not asked again at
 * the full cadence. The next one waits what the answer's `Retry-After` asked
 * for, or a wait that doubles with each failure in a row (`lib/backoff.ts`),
 * and the first success puts the cadence back. A return to the tab does not
 * cut that wait short: the server asked for it whether or not anyone watched.
 *
 * An interval of 0 fetches once: it has no schedule to catch up on.
 * `fetchOnce` must not reject; it resolves with a `PollFailure` to say it
 * failed. Returns what stops it.
 */
export function pollLoop(
  fetchOnce: () => Promise<void | PollFailure>,
  intervalMs: number,
  page: PollPage = document,
  { steady = () => false, now = Date.now, random = Math.random }: LoopOptions = {},
): () => void {
  let stopped = false
  let fetching = false
  let failures = 0
  // While a failure's wait runs, the moment it ends.
  let heldUntil = 0
  let timer: ReturnType<typeof setTimeout> | undefined

  const arm = (delay: number) => {
    timer = setTimeout(() => {
      // Hidden, nothing rearms this: the return to the tab does.
      if (page.visibilityState === "visible") void run()
    }, delay)
  }

  const schedule = (failed?: PollFailure) => {
    heldUntil = 0
    if (stopped || intervalMs <= 0) return
    if (!failed || !isTransient(failed.failed)) {
      failures = 0
      arm(intervalMs)
      return
    }
    failures++
    // A read that watches the dashboard through its own restart keeps its
    // pace against a 502, and still waits when the limiter says how long.
    const wait =
      steady() && askedToWait(failed.failed) === undefined
        ? intervalMs
        : waitAfterFailure(failed.failed, failures, intervalMs, random())
    if (wait > intervalMs) heldUntil = now() + wait
    arm(wait)
  }

  const run = async () => {
    fetching = true
    const failed = await fetchOnce()
    fetching = false
    schedule(failed || undefined)
  }

  const onVisibility = () => {
    if (page.visibilityState !== "visible" || fetching) return
    clearTimeout(timer)
    const held = heldUntil - now()
    if (held > 0) arm(held)
    else void run()
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
  { enabled = true, seed, steady = false }: PollOptions<T> = {},
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
  const steadyRef = useRef(steady)
  useEffect(() => {
    fetcherRef.current = fetcher
    steadyRef.current = steady
  })

  const refresh = useCallback(() => setTick((t) => t + 1), [])

  useEffect(() => {
    if (!enabled) return
    let cancelled = false
    const controller = new AbortController()

    const stop = pollLoop(
      async () => {
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
          return { failed: err }
        }
      },
      intervalMs,
      document,
      { steady: () => steadyRef.current },
    )

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
