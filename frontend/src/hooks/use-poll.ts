"use client"

import { useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore } from "react"
import { ApiError } from "@/lib/api"
import { pollLoop, type PollHandle } from "@/hooks/poll-loop"
import { NO_SHARED_ANSWER, sharedPoll, type SharedReader } from "@/hooks/poll-share"

export { pollLoop, type PollFailure, type PollHandle } from "@/hooks/poll-loop"

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
  /**
   * The request's name, when other components make the same one: its path,
   * and its query when it has one (`"/assistant"`, `"/deploy/traffic"`).
   * Every poll that gives the same key shares one request, one timer at the
   * fastest cadence any of them asked for, and one last answer, which is also
   * what a page opened again draws until the new answer lands, with `loading`
   * true as for `seed` (`hooks/poll-share.ts`). `refresh` asks again for all
   * of them.
   *
   * The key is the resource's whole identity, so `deps` are not read: a
   * fetcher whose answer depends on anything must have it in the key. Without
   * a key a poll is its own, as it always was.
   */
  key?: string
}

const noSubscription = () => () => {}
const noSharedAnswer = () => NO_SHARED_ANSWER

/** `pollLoop` as a hook: the latest result, and a way to ask again. */
export function usePoll<T>(
  fetcher: (signal: AbortSignal) => Promise<T>,
  intervalMs = 5000,
  deps: unknown[] = [],
  { enabled = true, seed, steady = false, key }: PollOptions<T> = {},
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
  // So is the cadence: a caller's `busy ? 2_000 : 30_000` flipping is a new
  // schedule for the same loop, not a reason to abort and fetch again.
  const intervalRef = useRef(intervalMs)
  const loop = useRef<PollHandle | undefined>(undefined)
  useEffect(() => {
    fetcherRef.current = fetcher
    steadyRef.current = steady
    intervalRef.current = intervalMs
  })

  // A keyed poll is a reader of the one shared with every component that
  // gave the same key; the loop below is then not run at all.
  const shared = useMemo(() => (key === undefined ? undefined : sharedPoll(key)), [key])
  const answer = useSyncExternalStore(
    shared ? shared.subscribe : noSubscription,
    shared ? shared.read : noSharedAnswer,
    noSharedAnswer,
  )
  const reader = useRef<SharedReader>({
    interval: () => intervalRef.current,
    fetch: (signal) => fetcherRef.current(signal),
    steady: () => steadyRef.current,
  })
  useEffect(() => {
    if (!shared || !enabled) return
    return shared.join(reader.current)
  }, [shared, enabled])

  const refresh = useCallback(() => {
    if (shared) shared.refresh()
    else setTick((t) => t + 1)
  }, [shared])

  useEffect(() => {
    if (!enabled || shared) return
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
      () => intervalRef.current,
      document,
      { steady: () => steadyRef.current },
    )
    loop.current = stop

    return () => {
      cancelled = true
      controller.abort()
      stop()
      loop.current = undefined
    }
  }, [tick, enabled, resource, shared])

  useEffect(() => {
    loop.current?.retime()
    shared?.retime()
  }, [intervalMs, shared])

  if (shared) {
    // The kept answer of a key nobody was reading is drawn like a seed: there
    // at once, and loading until the read this arrival started has landed.
    const held = answer.answered || answer.data !== undefined
    return {
      data: held ? (answer.data as T | undefined) : seed,
      error: answer.error,
      lastSuccess: answer.lastSuccess,
      loading: enabled && !answer.answered,
      refresh,
    }
  }

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
