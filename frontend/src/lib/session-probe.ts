import { isTransient, waitAfterFailure } from "@/lib/backoff"

/**
 * The first question every page load asks: who is signed in. Only the server saying "nobody" is
 * that answer.
 *
 * The probe used to take any failure for it. A 429 from a limiter bucket that a restored window
 * of tabs had just drained, a 502 while the backend restarted itself, a network that had not come
 * back after a lid opened: each sent a signed-in person to the sign-in form, where the form stayed
 * although the session was still good, and the page they had asked for was gone. So a failure that
 * is not the server's "nobody" is waited out and asked again, by the rules every poll waits by
 * (`lib/backoff.ts`): what `Retry-After` asked for, or a wait that doubles to a ceiling.
 */

type ProbeOptions = {
  /** An error that is the server's considered "nobody is signed in": stop, and throw it. */
  signedOut: (err: unknown) => boolean
  /** False once nobody is waiting for the answer: a newer sign-in, or the page gone. */
  live: () => boolean
  /** Told each failure that is being waited out, so the splash can say why it is still there. */
  onWait?: (err: unknown) => void
  pause?: (ms: number) => Promise<void>
  random?: () => number
}

/** The first retry comes after one to two seconds; the rest double from there. */
const PROBE_BASE_MS = 1000

/**
 * Asks until something answers. Resolves with the answer, rejects with the error `signedOut`
 * accepted, and resolves with nothing once `live` says nobody is waiting.
 */
export async function untilAnswered<T>(
  ask: (attempt: number) => Promise<T>,
  {
    signedOut,
    live,
    onWait,
    pause = (ms) => new Promise((resolve) => setTimeout(resolve, ms)),
    random = Math.random,
  }: ProbeOptions,
): Promise<T | undefined> {
  for (let attempt = 1; ; attempt++) {
    try {
      return await ask(attempt)
    } catch (err) {
      if (!live()) return undefined
      if (signedOut(err)) throw err
      onWait?.(err)
      await pause(waitAfterFailure(err, attempt, PROBE_BASE_MS, random()))
      if (!live()) return undefined
    }
  }
}

type Refusal = { status?: unknown; message?: unknown }

/**
 * What the splash says while the probe waits: that the page is still coming, and why it is not
 * here yet. A refusal the server meant (the network allowlist's 403) is said in its own words.
 */
export function waitingReason(err: unknown): string {
  const refusal = (err ?? {}) as Refusal
  if (refusal.status === 429)
    return "The server is answering too many requests at once. Trying again shortly."
  if (isTransient(err)) return "The server is not answering. Trying again."
  const words = typeof refusal.message === "string" ? refusal.message.trim() : ""
  if (!words) return "The server refused the request. Trying again."
  const capital = words.charAt(0).toUpperCase() + words.slice(1)
  return `${/[.!?]$/.test(capital) ? capital : `${capital}.`} Trying again.`
}
