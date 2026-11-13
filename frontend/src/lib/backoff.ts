/**
 * How long to wait before asking a server again that just refused or did not answer.
 *
 * A dashboard asks many things at once, and every tab of one account shares one bucket in the
 * backend's limiter. A page that keeps its full cadence against a 429 or a 502 spends each refill
 * on whichever request happens to come first and shows a patchwork of errors; a page that waits
 * what it was asked to wait gets all of its answers together. The polls (`hooks/use-poll.ts`) and
 * the shell's first session probe (`hooks/use-auth.tsx`) both wait by these rules.
 */

/** No answer may hold a reader longer than this, whatever a header says. */
export const MAX_RETRY_AFTER_MS = 5 * 60_000

/** Where the doubling stops for a read that has no slower cadence of its own. */
export const MAX_BACKOFF_MS = 30_000

/**
 * A `Retry-After` header as milliseconds from `now`: a count of seconds, or an HTTP date. Nothing
 * for a header that is absent or says neither, and never more than `MAX_RETRY_AFTER_MS`: the
 * header is the server's text, and a proxy in front of it can write one too.
 */
export function retryAfterMs(
  header: string | null | undefined,
  now = Date.now(),
): number | undefined {
  const value = header?.trim()
  if (!value) return undefined
  let ms: number
  if (/^\d+$/.test(value)) {
    ms = Number(value) * 1000
  } else {
    // An HTTP date opens with the day's name. `Date.parse` alone reads "-5" and
    // "1.5" as dates of its own invention.
    const at = /^[A-Za-z]{3}/.test(value) ? Date.parse(value) : Number.NaN
    if (Number.isNaN(at)) return undefined
    ms = at - now
  }
  return Math.min(Math.max(ms, 0), MAX_RETRY_AFTER_MS)
}

type Refusal = { status?: unknown; code?: unknown; retryAfterMs?: unknown; name?: unknown }

/**
 * Whether a failed request is worth asking again unchanged: the limiter's 429, a 5xx (a restart
 * answers 502 through the proxy), a body that arrived cut off, or no answer at all, which `fetch`
 * reports as a plain `TypeError`. A 4xx is the server's considered answer and an abort is the
 * caller's own; neither is.
 */
export function isTransient(err: unknown): boolean {
  if (!err || typeof err !== "object") return false
  const refusal = err as Refusal
  if (refusal.name === "AbortError") return false
  if (typeof refusal.status !== "number") return true
  return refusal.status === 429 || refusal.status >= 500 || refusal.code === "bad_response"
}

/** What the server asked for on the failed request, when it asked. */
export function askedToWait(err: unknown): number | undefined {
  if (!err || typeof err !== "object") return undefined
  const wait = (err as Refusal).retryAfterMs
  return typeof wait === "number" && wait >= 0 ? wait : undefined
}

/**
 * The wait after `failures` consecutive failures of a read whose own cadence is `baseMs`: it
 * doubles from the cadence to `MAX_BACKOFF_MS`, with half of each step random ("equal jitter", as
 * `retryDelay` in `hooks/use-socket.ts`), so the reads of one page, and the tabs of one account,
 * do not all come back in the same instant. Never shorter than the cadence itself, so a read that
 * is already slower than the ceiling keeps its own pace.
 */
export function backoffDelay(failures: number, baseMs: number, random = Math.random()): number {
  const base = Math.max(baseMs, 1000)
  const step = Math.min(base * 2 ** Math.max(failures, 1), Math.max(MAX_BACKOFF_MS, base))
  return Math.max(baseMs, step / 2 + (step / 2) * random)
}

/**
 * The wait after a failure, all told: what the server asked for when it asked (a little more,
 * at random, so the bucket's refill is not met by every reader at once), and the doubling wait
 * otherwise.
 */
export function waitAfterFailure(
  err: unknown,
  failures: number,
  baseMs: number,
  random = Math.random(),
): number {
  const asked = askedToWait(err)
  if (asked === undefined) return backoffDelay(failures, baseMs, random)
  return Math.max(baseMs, asked + asked * 0.2 * random)
}
