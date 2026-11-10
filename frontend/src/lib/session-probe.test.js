import { expect, test } from "bun:test"
import { ApiError } from "./api"
import { untilAnswered, waitingReason } from "./session-probe"

const signedOut = (err) => err instanceof ApiError && err.isAuthProblem

/** Answers in order: an Error is thrown, anything else is the answer. */
function scripted(...answers) {
  const probe = { asked: 0, pauses: [], waits: [] }
  probe.ask = async () => {
    const next = answers[probe.asked++]
    if (next instanceof Error) throw next
    return next
  }
  probe.options = {
    signedOut,
    live: () => true,
    onWait: (err) => probe.waits.push(err),
    pause: async (ms) => void probe.pauses.push(ms),
    random: () => 0,
  }
  return probe
}

const limited = new ApiError(429, "rate_limited", "too many requests, slow down", undefined, {
  retryAfterMs: 30_000,
})
const restarting = new ApiError(502, "unknown", "Bad Gateway")
const offline = new TypeError("Failed to fetch")
const session = { authenticated: true, user: { id: 1 } }

test("a 429, a 502 and a dead network are waited out, and the session is still found", async () => {
  const probe = scripted(limited, restarting, offline, session)
  await expect(untilAnswered(probe.ask, probe.options)).resolves.toBe(session)
  expect(probe.asked).toBe(4)
  // The limiter's own thirty seconds, then the doubling wait.
  expect(probe.pauses).toEqual([30_000, 2000, 4000])
  expect(probe.waits).toEqual([limited, restarting, offline])
})

test("only the server's own answer is signed out, and it is not asked twice", async () => {
  const nobody = new ApiError(401, "unauthorized", "authentication required")
  const probe = scripted(nobody, session)
  await expect(untilAnswered(probe.ask, probe.options)).rejects.toBe(nobody)
  expect(probe.asked).toBe(1)
  expect(probe.pauses).toEqual([])
  for (const code of ["account_disabled", "password_change_required"]) {
    const refused = scripted(new ApiError(403, code, "refused"))
    await expect(untilAnswered(refused.ask, refused.options)).rejects.toMatchObject({ code })
  }
})

test("a refusal that is not about the session keeps asking", async () => {
  const denied = new ApiError(403, "network_denied", "your network is not permitted")
  const probe = scripted(denied, denied, session)
  await expect(untilAnswered(probe.ask, probe.options)).resolves.toBe(session)
  expect(probe.pauses).toEqual([1000, 2000])
})

test("the wait stops at its ceiling", async () => {
  const probe = scripted(...Array(8).fill(restarting), session)
  probe.options.random = () => 1
  await untilAnswered(probe.ask, probe.options)
  expect(probe.pauses).toEqual([2000, 4000, 8000, 16_000, 30_000, 30_000, 30_000, 30_000])
})

test("it stops asking once nobody is waiting for the answer", async () => {
  const probe = scripted(restarting, restarting, session)
  let live = true
  probe.options.live = () => live
  probe.options.pause = async () => {
    // A sign-in completed while the probe waited.
    live = false
  }
  await expect(untilAnswered(probe.ask, probe.options)).resolves.toBeUndefined()
  expect(probe.asked).toBe(1)

  const gone = scripted(restarting, session)
  gone.options.live = () => false
  await expect(untilAnswered(gone.ask, gone.options)).resolves.toBeUndefined()
  expect(gone.waits).toEqual([])
})

test("the splash says why it is still there", () => {
  expect(waitingReason(limited)).toBe(
    "The server is answering too many requests at once. Trying again shortly.",
  )
  expect(waitingReason(restarting)).toBe("The server is not answering. Trying again.")
  expect(waitingReason(offline)).toBe("The server is not answering. Trying again.")
  expect(
    waitingReason(
      new ApiError(403, "network_denied", "your network is not permitted to reach this dashboard"),
    ),
  ).toBe("Your network is not permitted to reach this dashboard. Trying again.")
  expect(waitingReason(new ApiError(403, "unknown", ""))).toBe(
    "The server refused the request. Trying again.",
  )
})
