import { askedToWait, isTransient, waitAfterFailure } from "@/lib/backoff"

/**
 * The schedule every poll runs on, apart from React so `bun test` can drive it
 * with a clock and a page of its own. `usePoll` (`hooks/use-poll.ts`) is this
 * as a hook.
 */

/** The part of `document` a poll watches, so the schedule can be tested without one. */
export type PollPage = Pick<
  Document,
  "visibilityState" | "addEventListener" | "removeEventListener"
>

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

/** What stops a loop, and what tells it its cadence changed. */
export type PollHandle = (() => void) & {
  /**
   * The interval it was given has a new value: the next fetch is due that long
   * after the last one settled. Nothing is fetched because of the change, and
   * a fetch in flight is left to finish.
   */
  retime: () => void
  /**
   * Fetch now, unless a fetch is in flight or a failure's wait is still
   * running: what a second reader of the same answer does on arriving
   * (`hooks/poll-share.ts`).
   */
  poke: () => void
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
 * The interval may be a function, read each time something is scheduled. A
 * caller whose cadence follows what it last read (two seconds while a run is
 * live, five minutes otherwise) changes what the function returns and calls
 * `retime`; starting the loop again instead would fetch at once what had just
 * been read, and abort whatever was in flight.
 *
 * An interval of 0 fetches once: it has no schedule to catch up on.
 * `fetchOnce` must not reject; it resolves with a `PollFailure` to say it
 * failed. Returns what stops it.
 */
export function pollLoop(
  fetchOnce: () => Promise<void | PollFailure>,
  intervalMs: number | (() => number),
  page: PollPage = document,
  { steady = () => false, now = Date.now, random = Math.random }: LoopOptions = {},
): PollHandle {
  const interval = typeof intervalMs === "function" ? intervalMs : () => intervalMs
  let stopped = false
  let fetching = false
  let listening = false
  let failures = 0
  // When the last fetch settled, which is what a changed cadence counts from.
  let settledAt = now()
  // While a failure's wait runs, the moment it ends.
  let heldUntil = 0
  let timer: ReturnType<typeof setTimeout> | undefined

  const arm = (delay: number) => {
    clearTimeout(timer)
    timer = setTimeout(() => {
      // Hidden, nothing rearms this: the return to the tab does.
      if (page.visibilityState === "visible") void run()
    }, delay)
  }

  const schedule = (failed?: PollFailure) => {
    heldUntil = 0
    settledAt = now()
    const every = interval()
    if (stopped || every <= 0) return
    if (!failed || !isTransient(failed.failed)) {
      failures = 0
      arm(every)
      return
    }
    failures++
    // A read that watches the dashboard through its own restart keeps its
    // pace against a 502, and still waits when the limiter says how long.
    const wait =
      steady() && askedToWait(failed.failed) === undefined
        ? every
        : waitAfterFailure(failed.failed, failures, every, random())
    heldUntil = settledAt + wait
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

  // Only a loop with a schedule has anything to catch up on when the tab returns.
  const listen = () => {
    const wanted = !stopped && interval() > 0
    if (wanted === listening) return
    listening = wanted
    if (wanted) page.addEventListener("visibilitychange", onVisibility)
    else page.removeEventListener("visibilitychange", onVisibility)
  }

  const stop = () => {
    stopped = true
    clearTimeout(timer)
    listen()
  }

  stop.retime = () => {
    if (stopped) return
    listen()
    // A fetch in flight reads the new cadence when it settles.
    if (fetching) return
    clearTimeout(timer)
    const every = interval()
    if (every <= 0) return
    arm(Math.max(0, Math.max(settledAt + every, heldUntil) - now()))
  }

  stop.poke = () => {
    if (stopped || fetching || heldUntil > now()) return
    clearTimeout(timer)
    void run()
  }

  void run()
  listen()
  return stop
}
