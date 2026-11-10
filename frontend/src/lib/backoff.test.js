import { expect, test } from "bun:test"
import {
  MAX_BACKOFF_MS,
  MAX_RETRY_AFTER_MS,
  askedToWait,
  backoffDelay,
  isTransient,
  retryAfterMs,
  waitAfterFailure,
} from "./backoff"
import { ApiError } from "./api"

test("Retry-After is read as seconds or as a date, and never trusted past the ceiling", () => {
  expect(retryAfterMs("30")).toBe(30_000)
  expect(retryAfterMs(" 0 ")).toBe(0)
  const now = Date.parse("2026-11-10T10:00:00Z")
  expect(retryAfterMs("Tue, 10 Nov 2026 10:00:45 GMT", now)).toBe(45_000)
  // A date already past is "now", not a negative wait.
  expect(retryAfterMs("Tue, 10 Nov 2026 09:00:00 GMT", now)).toBe(0)
  expect(retryAfterMs("86400")).toBe(MAX_RETRY_AFTER_MS)
  for (const none of [undefined, null, "", "soon", "-5", "1.5"])
    expect(retryAfterMs(none)).toBeUndefined()
})

test("only an answer that asking again can change is transient", () => {
  expect(isTransient(new ApiError(429, "rate_limited", "slow down"))).toBe(true)
  expect(isTransient(new ApiError(502, "unknown", "Bad Gateway"))).toBe(true)
  expect(isTransient(new ApiError(503, "not_available", "docker is not running"))).toBe(true)
  expect(isTransient(new ApiError(200, "bad_response", "cut off"))).toBe(true)
  // What fetch throws when nothing answered.
  expect(isTransient(new TypeError("Failed to fetch"))).toBe(true)
  for (const status of [400, 401, 403, 404, 409, 422])
    expect(isTransient(new ApiError(status, "refused", "no"))).toBe(false)
  expect(isTransient(new DOMException("aborted", "AbortError"))).toBe(false)
  expect(isTransient(undefined)).toBe(false)
})

test("the wait doubles from the cadence to the ceiling, half of each step random", () => {
  // A five-second poll: steps of 10, 20 and then the 30-second ceiling.
  expect(backoffDelay(1, 5000, 0)).toBe(5000)
  expect(backoffDelay(1, 5000, 1)).toBe(10_000)
  expect(backoffDelay(2, 5000, 0)).toBe(10_000)
  expect(backoffDelay(2, 5000, 1)).toBe(20_000)
  expect(backoffDelay(3, 5000, 1)).toBe(MAX_BACKOFF_MS)
  expect(backoffDelay(30, 5000, 1)).toBe(MAX_BACKOFF_MS)
  expect(backoffDelay(30, 5000, 0)).toBe(MAX_BACKOFF_MS / 2)
})

test("a read slower than the ceiling keeps its own pace", () => {
  expect(backoffDelay(1, 60_000, 0)).toBe(60_000)
  expect(backoffDelay(9, 300_000, 1)).toBe(300_000)
})

test("what the server asked for wins over the doubling, and is never undercut", () => {
  const limited = new ApiError(429, "rate_limited", "slow down", undefined, {
    retryAfterMs: 30_000,
  })
  expect(askedToWait(limited)).toBe(30_000)
  expect(waitAfterFailure(limited, 1, 2000, 0)).toBe(30_000)
  // Up to a fifth more, so the tabs of one account do not return together.
  expect(waitAfterFailure(limited, 1, 2000, 1)).toBe(36_000)
  // A read already slower than the wait is not sped up by it.
  expect(waitAfterFailure(limited, 1, 120_000, 0)).toBe(120_000)
  const down = new ApiError(502, "unknown", "Bad Gateway")
  expect(askedToWait(down)).toBeUndefined()
  expect(waitAfterFailure(down, 2, 5000, 1)).toBe(20_000)
})
