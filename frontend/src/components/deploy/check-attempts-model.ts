/**
 * A health check's attempts as the backend keeps them (`deploy/checks.go`):
 * repeats of one answer are folded into the first attempt that gave it, which
 * says how many it stands for (`repeats`), the last of them (`lastNumber`) and
 * how long they took (`spanMillis`). The last attempt is always its own entry.
 */

type Attempt = Record<string, unknown>

/** How many times a check asked, counting the attempts folded into a kept one. */
export function checkAttemptCount(attempts: Attempt[]): number {
  return attempts.reduce<number>(
    (count, attempt) => count + 1 + (typeof attempt.repeats === "number" ? attempt.repeats : 0),
    0,
  )
}
