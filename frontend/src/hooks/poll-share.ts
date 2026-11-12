import { pollLoop, type PollFailure, type PollHandle, type PollPage } from "@/hooks/poll-loop"

/**
 * One request, one timer and one last answer per key, however many components
 * read it.
 *
 * Every `usePoll` used to own its data, so two components that needed the same
 * reading fetched it twice and kept two timers: the Overview read `/assistant`
 * three times within 12 ms, a page with a job console read `/jobs` on two
 * parallel five-second loops, and coming back to a page drew its skeleton
 * again although the answer had been in hand a moment before. A poll that
 * names its request with `key` joins the others that named the same one:
 *
 *  - one loop runs for the key, at the fastest cadence any of its readers
 *    asked for, and stops once the last of them has left (a moment after, so
 *    one page handing a key to the next is not a stop and a start);
 *  - a reader that arrives while a request is in flight, or within
 *    `FRESH_MS` of the last answer, is given that answer and asks nothing;
 *    one that arrives later asks once, for everybody;
 *  - the last answer outlives its readers for `KEEP_MS`, so a page that is
 *    opened again draws it at once, with `loading` true until the new answer
 *    replaces it (what `seed` did by hand);
 *  - `refresh` aborts what is in flight and asks again, as it does for a poll
 *    of one, because the caller has usually just changed what the answer is.
 *
 * An answer belongs to the account that was given it. Another account signing
 * in forgets everything at once, and whatever is still on screen is read for
 * again (`forgetSharedPolls`). Signing out asks nothing more of a server the
 * session is about to leave: what nobody is reading is dropped there and then,
 * and the rest when the page reading it goes, with nothing kept
 * (`releaseSharedPolls`).
 */

/** An answer this young is not asked for again by a reader that has just arrived. */
export const FRESH_MS = 2000

/** How long an answer nobody is reading is kept for whoever opens the page again. */
export const KEEP_MS = 5 * 60_000

export type SharedSnapshot = {
  data?: unknown
  error?: Error
  lastSuccess?: number
  /** Something has answered, or failed, since readers last arrived at an idle key. */
  answered: boolean
}

/** What one reader brings: its cadence, its fetcher and whether it must not back off. */
export type SharedReader = {
  interval: () => number
  fetch: (signal: AbortSignal) => Promise<unknown>
  steady?: () => boolean
}

const EMPTY: SharedSnapshot = { answered: false }

type Clock = { now: () => number; random: () => number }

export class SharedPoll {
  private snapshot: SharedSnapshot = EMPTY
  private readers = new Set<SharedReader>()
  private listeners = new Set<() => void>()
  private loop: PollHandle | undefined
  private controller: AbortController | undefined
  private refreshing = false
  private released = false
  private rest: ReturnType<typeof setTimeout> | undefined
  private drop: ReturnType<typeof setTimeout> | undefined

  constructor(
    private page: () => PollPage,
    private clock: Clock,
  ) {}

  /** For `useSyncExternalStore`: the same object until something changes. */
  read = (): SharedSnapshot => this.snapshot

  subscribe = (listener: () => void): (() => void) => {
    this.listeners.add(listener)
    return () => this.listeners.delete(listener)
  }

  /** A reader arrives. Returns what takes it away again. */
  join(reader: SharedReader): () => void {
    clearTimeout(this.rest)
    clearTimeout(this.drop)
    this.readers.add(reader)
    if (!this.loop) {
      this.start()
    } else {
      this.loop.retime()
      // The loop itself declines while a request is in flight or a failure's
      // wait is running, so this only ever adds the one request a stale
      // answer is owed.
      const answered = this.snapshot.lastSuccess
      if (answered === undefined || this.clock.now() - answered >= FRESH_MS) this.loop.poke()
    }
    return () => this.leave(reader)
  }

  /** A reader's cadence changed. */
  retime() {
    this.loop?.retime()
  }

  /** Ask again now, for every reader, dropping whatever was in flight. */
  refresh = () => {
    if (this.refreshing || this.readers.size === 0) return
    // Nine readers hearing one "settings saved" event are one request.
    this.refreshing = true
    queueMicrotask(() => {
      this.refreshing = false
      if (this.readers.size === 0) return
      this.halt()
      this.start()
    })
  }

  /** Forget the answer. Readers still here are read for again from nothing. */
  clear() {
    clearTimeout(this.rest)
    clearTimeout(this.drop)
    this.released = false
    this.halt()
    this.set(EMPTY)
    if (this.readers.size > 0) this.start()
  }

  /**
   * The account is signing out: its answer is not kept for whoever comes
   * next. Readers still here go on as they were until their page is gone,
   * because asking again now would be a request raced against the sign-out.
   */
  release() {
    if (this.readers.size > 0) {
      this.released = true
      return
    }
    clearTimeout(this.rest)
    clearTimeout(this.drop)
    this.halt()
    if (this.snapshot !== EMPTY) this.set(EMPTY)
  }

  private leave(reader: SharedReader) {
    if (!this.readers.delete(reader)) return
    if (this.readers.size > 0) {
      this.loop?.retime()
      return
    }
    // Not yet: navigating from one page that reads this key to another takes
    // the old reader away an instant before the new one arrives.
    this.rest = setTimeout(() => this.idle(), 0)
  }

  /** Nobody is reading: stop asking, and keep the answer for whoever comes back. */
  private idle() {
    if (this.readers.size > 0) return
    this.halt()
    if (this.released) {
      this.released = false
      if (this.snapshot !== EMPTY) this.set(EMPTY)
      return
    }
    // What is kept is the answer, not the failure: whoever comes back asks again.
    if (this.snapshot !== EMPTY)
      this.set({
        data: this.snapshot.data,
        lastSuccess: this.snapshot.lastSuccess,
        answered: false,
      })
    this.drop = setTimeout(() => this.set(EMPTY), KEEP_MS)
  }

  private start() {
    const controller = new AbortController()
    this.controller = controller
    this.loop = pollLoop(
      () => this.fetchOnce(controller),
      () => this.fastest(),
      this.page(),
      {
        steady: () => [...this.readers].some((reader) => reader.steady?.()),
        now: this.clock.now,
        random: this.clock.random,
      },
    )
  }

  private halt() {
    this.controller?.abort()
    this.controller = undefined
    this.loop?.()
    this.loop = undefined
  }

  /** The fastest cadence asked for; 0 when every reader only wants the one read. */
  private fastest(): number {
    let fastest = 0
    for (const reader of this.readers) {
      const every = reader.interval()
      if (every > 0 && (fastest === 0 || every < fastest)) fastest = every
    }
    return fastest
  }

  private async fetchOnce(controller: AbortController): Promise<void | PollFailure> {
    // Every reader of a key asks the same question, so any one's fetcher will do.
    const [reader] = this.readers
    if (!reader) return
    try {
      const data = await reader.fetch(controller.signal)
      if (controller.signal.aborted) return
      this.set({ data, lastSuccess: this.clock.now(), answered: true })
    } catch (err) {
      if (controller.signal.aborted) return
      if (err instanceof DOMException && err.name === "AbortError") return
      this.set({
        data: this.snapshot.data,
        lastSuccess: this.snapshot.lastSuccess,
        error: err instanceof Error ? err : new Error(String(err)),
        answered: true,
      })
      return { failed: err }
    }
  }

  private set(next: SharedSnapshot) {
    this.snapshot = next
    for (const listener of [...this.listeners]) listener()
  }
}

/** The keys in use, and a way to make a set of them a test owns. */
export function createPollShare(
  page: () => PollPage = () => document,
  clock: Clock = { now: Date.now, random: Math.random },
) {
  const polls = new Map<string, SharedPoll>()
  return {
    /**
     * The shared poll for a key, made on first use and kept: a component holds
     * on to the one it was given, so a key must never come to mean two of them.
     * What is dropped after `KEEP_MS` is the answer.
     */
    of(key: string): SharedPoll {
      let poll = polls.get(key)
      if (!poll) {
        poll = new SharedPoll(page, clock)
        polls.set(key, poll)
      }
      return poll
    },
    /** Forget every answer, and read again for whoever is still reading: the account changed. */
    forget() {
      for (const poll of polls.values()) poll.clear()
    },
    /** Keep no answer past its readers: the account is signing out. */
    release() {
      for (const poll of polls.values()) poll.release()
    },
  }
}

const shared = createPollShare()

export const sharedPoll = (key: string) => shared.of(key)

/** Called where another account's sign-in forgets the last one's working state. */
export const forgetSharedPolls = () => shared.forget()

/** Called on sign-out, beside `forgetWorkingState`. */
export const releaseSharedPolls = () => shared.release()

/** The snapshot of a poll nobody has keyed, and of any poll during server rendering. */
export const NO_SHARED_ANSWER = EMPTY
