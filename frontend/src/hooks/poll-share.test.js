import { afterEach, beforeEach, expect, jest, test } from "bun:test"
import { FRESH_MS, KEEP_MS, createPollShare } from "./poll-share"

/** A `document` that can be hidden and shown. */
function fakePage() {
  const listeners = new Set()
  return {
    visibilityState: "visible",
    addEventListener: (_, listener) => listeners.add(listener),
    removeEventListener: (_, listener) => listeners.delete(listener),
  }
}

/** A world with its own clock, its own keys and one endpoint that counts what it is asked. */
function world() {
  const clock = { at: 1_000_000 }
  const share = createPollShare(() => fakePage(), { now: () => clock.at, random: () => 0 })
  const endpoint = { asked: 0, aborted: 0, pending: [] }
  /** The fetcher every reader of the key passes: in flight until `answer` or `fail`. */
  endpoint.fetch = (signal) => {
    endpoint.asked++
    return new Promise((resolve, reject) => {
      const request = { resolve, reject }
      endpoint.pending.push(request)
      signal.addEventListener("abort", () => {
        // Only a request still in flight is aborted; one that answered is past it.
        const at = endpoint.pending.indexOf(request)
        if (at < 0) return
        endpoint.pending.splice(at, 1)
        endpoint.aborted++
        reject(new DOMException("aborted", "AbortError"))
      })
    })
  }
  const settle = async (act) => {
    const next = endpoint.pending.shift()
    act(next)
    for (let i = 0; i < 6; i++) await Promise.resolve()
  }
  return {
    share,
    endpoint,
    advance(ms) {
      clock.at += ms
      jest.advanceTimersByTime(ms)
    },
    answer: (data) => settle((next) => next.resolve(data)),
    fail: (err) => settle((next) => next.reject(err)),
    reader: (every, extra = {}) => ({ interval: () => every, fetch: endpoint.fetch, ...extra }),
  }
}

beforeEach(() => jest.useFakeTimers())
afterEach(() => jest.useRealTimers())

test("three readers that arrive together are one request and one answer", async () => {
  const { share, endpoint, answer, reader } = world()
  const poll = share.of("/assistant")
  const seen = []
  poll.subscribe(() => seen.push(poll.read()))
  poll.join(reader(120_000))
  poll.join(reader(120_000))
  poll.join(reader(120_000))
  expect(endpoint.asked).toBe(1)
  expect(poll.read().answered).toBe(false)
  await answer({ enabled: true })
  expect(poll.read()).toMatchObject({ data: { enabled: true }, answered: true })
  expect(poll.read().error).toBeUndefined()
  expect(seen).toHaveLength(1)
  // The same object until something changes, which is what a store's snapshot must be.
  expect(poll.read()).toBe(poll.read())
})

test("one timer runs for the key, at the fastest cadence a reader asked for", async () => {
  const { share, endpoint, answer, advance, reader } = world()
  const poll = share.of("/jobs")
  poll.join(reader(30_000))
  const leaveFast = poll.join(reader(5000))
  await answer([])
  advance(4999)
  expect(endpoint.asked).toBe(1)
  advance(1)
  expect(endpoint.asked).toBe(2)
  await answer([])
  // The fast reader leaves: what is left is the slow one's cadence, counted
  // from the last answer, and its leaving asks for nothing.
  leaveFast()
  advance(5000)
  expect(endpoint.asked).toBe(2)
  advance(24_999)
  expect(endpoint.asked).toBe(2)
  advance(1)
  expect(endpoint.asked).toBe(3)
})

test("a reader arriving on a fresh answer asks nothing, on a stale one asks once", async () => {
  const { share, endpoint, answer, advance, reader } = world()
  const poll = share.of("/deploy/traffic")
  poll.join(reader(60_000))
  await answer({ 1: {} })
  advance(FRESH_MS - 1)
  poll.join(reader(60_000))
  expect(endpoint.asked).toBe(1)
  advance(20_000)
  // Twenty seconds on, a page that has just opened is owed a current answer.
  poll.join(reader(60_000))
  poll.join(reader(60_000))
  expect(endpoint.asked).toBe(2)
  expect(poll.read().answered).toBe(true)
  await answer({ 1: {}, 2: {} })
  expect(poll.read().data).toEqual({ 1: {}, 2: {} })
  // And the schedule carries on from that answer, not beside it.
  advance(59_999)
  expect(endpoint.asked).toBe(2)
  advance(1)
  expect(endpoint.asked).toBe(3)
})

test("readers that only want the one read share it, and a later one reads again", async () => {
  const { share, endpoint, answer, advance, reader } = world()
  const poll = share.of("/system/host")
  poll.join(reader(0))
  poll.join(reader(0))
  await answer({ hostname: "atlas" })
  advance(600_000)
  expect(endpoint.asked).toBe(1)
  poll.join(reader(0))
  expect(endpoint.asked).toBe(2)
})

test("the last answer outlives its readers, and is drawn while the next is fetched", async () => {
  const { share, endpoint, answer, advance, reader } = world()
  const poll = share.of("/deploy/traffic")
  const leave = poll.join(reader(30_000))
  await answer({ 1: {} })
  const read = poll.read().lastSuccess
  leave()
  advance(0)
  // Nobody is reading: nothing is asked however long it is left.
  advance(120_000)
  expect(endpoint.asked).toBe(1)
  expect(poll.read()).toEqual({ data: { 1: {} }, lastSuccess: read, answered: false })
  // The page is opened again: the answer is there at once, and marked as not yet current.
  poll.join(reader(30_000))
  expect(endpoint.asked).toBe(2)
  expect(poll.read()).toMatchObject({ data: { 1: {} }, answered: false })
  await answer({ 2: {} })
  expect(poll.read()).toMatchObject({ data: { 2: {} }, answered: true })
})

test("an answer nobody has read for long enough is dropped", async () => {
  const { share, answer, advance, reader } = world()
  const poll = share.of("/deploy/traffic")
  const leave = poll.join(reader(30_000))
  await answer({ 1: {} })
  leave()
  advance(0)
  advance(KEEP_MS - 1)
  expect(poll.read().data).toEqual({ 1: {} })
  advance(1)
  expect(poll.read()).toEqual({ answered: false })
})

test("one page handing a key to the next is not a stop and a start", async () => {
  const { share, endpoint, answer, advance, reader } = world()
  const poll = share.of("/deploy/traffic")
  const leave = poll.join(reader(30_000))
  await answer({ 1: {} })
  advance(1000)
  // The old page's reader goes and the new page's arrives in the same commit.
  leave()
  poll.join(reader(30_000))
  advance(0)
  expect(endpoint.asked).toBe(1)
  expect(endpoint.aborted).toBe(0)
  expect(poll.read().answered).toBe(true)
  advance(29_000)
  expect(endpoint.asked).toBe(2)
})

test("leaving while a request is in flight aborts it, and its late answer is ignored", async () => {
  const { share, endpoint, advance, reader } = world()
  const poll = share.of("/jobs")
  const leave = poll.join(reader(5000))
  leave()
  advance(0)
  expect(endpoint.aborted).toBe(1)
  for (let i = 0; i < 6; i++) await Promise.resolve()
  expect(poll.read()).toEqual({ answered: false })
  advance(60_000)
  expect(endpoint.asked).toBe(1)
})

test("a failure keeps the last answer beside the error, for every reader", async () => {
  const { share, answer, fail, advance, reader } = world()
  const poll = share.of("/jobs")
  poll.join(reader(5000))
  poll.join(reader(5000))
  await answer(["job"])
  const read = poll.read().lastSuccess
  advance(5000)
  await fail(new Error("docker is not running"))
  expect(poll.read()).toMatchObject({ data: ["job"], lastSuccess: read, answered: true })
  expect(poll.read().error.message).toBe("docker is not running")
  // What comes back to an idle key is the answer, never an old failure.
  const share2 = world()
  const other = share2.share.of("/jobs")
  const leave = other.join(share2.reader(5000))
  await share2.fail(new Error("no"))
  leave()
  share2.advance(0)
  expect(other.read().error).toBeUndefined()
})

test("a refused request is not asked again by a reader that arrives during the wait", async () => {
  const { share, endpoint, fail, advance, reader } = world()
  const poll = share.of("/jobs")
  poll.join(reader(5000))
  await fail({ status: 429, code: "rate_limited", retryAfterMs: 30_000 })
  advance(10_000)
  poll.join(reader(5000))
  expect(endpoint.asked).toBe(1)
  advance(19_999)
  expect(endpoint.asked).toBe(1)
  advance(1)
  expect(endpoint.asked).toBe(2)
})

test("refresh drops what is in flight and asks again, once however many ask", async () => {
  const { share, endpoint, answer, reader } = world()
  const poll = share.of("/assistant")
  poll.join(reader(120_000))
  poll.join(reader(120_000))
  await answer({ model: "a" })
  // A request that left before the change must not be what the readers are shown.
  poll.join(reader(120_000))
  poll.refresh()
  poll.refresh()
  poll.refresh()
  await Promise.resolve()
  expect(endpoint.asked).toBe(2)
  expect(poll.read()).toMatchObject({ data: { model: "a" }, answered: true })
  await answer({ model: "b" })
  expect(poll.read().data).toEqual({ model: "b" })

  const busy = share.of("/jobs")
  busy.join(reader(5000))
  expect(endpoint.asked).toBe(3)
  busy.refresh()
  await Promise.resolve()
  expect(endpoint.aborted).toBe(1)
  expect(endpoint.asked).toBe(4)
})

test("a reader that must keep its pace through a restart keeps it for the key", async () => {
  const { share, endpoint, fail, advance, reader } = world()
  const poll = share.of("/dashboard/update")
  poll.join(reader(2000, { steady: () => true }))
  poll.join(reader(300_000))
  await fail({ status: 502, code: "unknown" })
  advance(2000)
  expect(endpoint.asked).toBe(2)
})

test("forgetting drops every answer, and readers still there are read for afresh", async () => {
  const { share, endpoint, answer, advance, reader } = world()
  const kept = share.of("/assistant")
  const idle = share.of("/deploy/traffic")
  kept.join(reader(120_000))
  await answer({ model: "a" })
  const leave = idle.join(reader(30_000))
  await answer({ 1: {} })
  leave()
  advance(0)
  share.forget()
  expect(idle.read()).toEqual({ answered: false })
  expect(kept.read()).toEqual({ answered: false })
  expect(endpoint.asked).toBe(3)
  // A key means one poll for as long as the page lives.
  expect(share.of("/assistant")).toBe(kept)
})

test("signing out asks nothing more, and keeps nothing once the readers have gone", async () => {
  const { share, endpoint, answer, advance, reader } = world()
  const read = share.of("/assistant")
  const idle = share.of("/deploy/traffic")
  const leaveRead = read.join(reader(120_000))
  await answer({ model: "a" })
  const leaveIdle = idle.join(reader(30_000))
  await answer({ 1: {} })
  leaveIdle()
  advance(0)
  expect(idle.read().data).toEqual({ 1: {} })

  share.release()
  // What nobody is reading is gone at once; the shell's own reading stays
  // drawn, and no request is raced against the sign-out.
  expect(idle.read()).toEqual({ answered: false })
  expect(read.read().data).toEqual({ model: "a" })
  expect(endpoint.asked).toBe(2)

  // The shell unmounts: this time the answer does not outlive its readers.
  leaveRead()
  advance(0)
  expect(read.read()).toEqual({ answered: false })

  // Whoever signs in next is read for from nothing, and kept as usual.
  const leaveNext = read.join(reader(120_000))
  expect(endpoint.asked).toBe(3)
  await answer({ model: "b" })
  leaveNext()
  advance(0)
  expect(read.read().data).toEqual({ model: "b" })
})
