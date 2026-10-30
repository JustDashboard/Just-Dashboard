import { afterEach, beforeEach, expect, jest, test } from "bun:test"
import { pollLoop } from "./use-poll"

/** A `document` that can be hidden and shown, and that counts its listeners. */
function fakePage() {
  const listeners = new Set()
  return {
    visibilityState: "visible",
    listeners,
    addEventListener: (_, listener) => listeners.add(listener),
    removeEventListener: (_, listener) => listeners.delete(listener),
    set(state) {
      this.visibilityState = state
      for (const listener of [...listeners]) listener()
    },
  }
}

/** A fetch that stays in flight until `settle`, and counts its starts. */
function fakeFetch() {
  const fetches = { started: 0, settle: () => {} }
  fetches.run = () => {
    fetches.started++
    return new Promise((resolve) => {
      fetches.settle = async () => {
        resolve()
        await Promise.resolve()
        await Promise.resolve()
      }
    })
  }
  return fetches
}

beforeEach(() => jest.useFakeTimers())
afterEach(() => jest.useRealTimers())

test("the interval fetches again only after the last fetch settled", async () => {
  const page = fakePage()
  const fetches = fakeFetch()
  const stop = pollLoop(fetches.run, 1000, page)
  expect(fetches.started).toBe(1)
  jest.advanceTimersByTime(5000)
  expect(fetches.started).toBe(1)
  await fetches.settle()
  jest.advanceTimersByTime(1000)
  expect(fetches.started).toBe(2)
  stop()
})

test("a hidden tab stops fetching and a return fetches at once, one time", async () => {
  const page = fakePage()
  const fetches = fakeFetch()
  const stop = pollLoop(fetches.run, 1000, page)
  await fetches.settle()

  page.set("hidden")
  jest.advanceTimersByTime(60_000)
  expect(fetches.started).toBe(1)

  page.set("visible")
  expect(fetches.started).toBe(2)
  await fetches.settle()
  // The return replaced the schedule rather than adding a second one.
  jest.advanceTimersByTime(999)
  expect(fetches.started).toBe(2)
  jest.advanceTimersByTime(1)
  expect(fetches.started).toBe(3)
  stop()
})

test("a return before the timer was due replaces it, so it does not fetch twice", async () => {
  const page = fakePage()
  const fetches = fakeFetch()
  const stop = pollLoop(fetches.run, 1000, page)
  await fetches.settle()

  page.set("hidden")
  jest.advanceTimersByTime(400)
  page.set("visible")
  expect(fetches.started).toBe(2)
  jest.advanceTimersByTime(600)
  expect(fetches.started).toBe(2)
  stop()
})

test("a return while a fetch is in flight leaves that fetch as the refresh", async () => {
  const page = fakePage()
  const fetches = fakeFetch()
  const stop = pollLoop(fetches.run, 1000, page)
  page.set("hidden")
  page.set("visible")
  expect(fetches.started).toBe(1)
  await fetches.settle()
  jest.advanceTimersByTime(1000)
  expect(fetches.started).toBe(2)
  stop()
})

test("an interval of 0 fetches once and never listens for the tab", async () => {
  const page = fakePage()
  const fetches = fakeFetch()
  const stop = pollLoop(fetches.run, 0, page)
  expect(page.listeners.size).toBe(0)
  await fetches.settle()
  page.set("hidden")
  page.set("visible")
  jest.advanceTimersByTime(60_000)
  expect(fetches.started).toBe(1)
  stop()
})

test("stopping removes the listener and cancels what was scheduled", async () => {
  const page = fakePage()
  const fetches = fakeFetch()
  const stop = pollLoop(fetches.run, 1000, page)
  await fetches.settle()
  expect(page.listeners.size).toBe(1)
  stop()
  expect(page.listeners.size).toBe(0)
  jest.advanceTimersByTime(60_000)
  expect(fetches.started).toBe(1)
})

test("a fetch that settles after stopping schedules nothing", async () => {
  const page = fakePage()
  const fetches = fakeFetch()
  const stop = pollLoop(fetches.run, 1000, page)
  stop()
  await fetches.settle()
  jest.advanceTimersByTime(60_000)
  expect(fetches.started).toBe(1)
})
