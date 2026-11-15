import { expect, test } from "bun:test"
import { receiveApplied } from "./use-deferred-search"

// The echo guard, held without a document: which applied values replace what
// the reader is typing. The box itself is held by the Redis key specs.

test("the box's own value coming back is an echo, and leaves the draft alone", () => {
  expect(receiveApplied(["user"], "user")).toEqual({ echo: true, unechoed: [] })
})

test("an older echo arriving late keeps the values sent after it", () => {
  expect(receiveApplied(["u", "us", "user"], "us")).toEqual({ echo: true, unechoed: ["user"] })
})

test("a value the box never sent is a change from outside", () => {
  expect(receiveApplied(["user"], "session:*")).toEqual({ echo: false, unechoed: [] })
  expect(receiveApplied([], "")).toEqual({ echo: false, unechoed: [] })
})

test("Back to a value already echoed is a change from outside", () => {
  // Typed "a" (echoed), then "ab" (echoed): Back to "a" is not the box's echo.
  const afterA = receiveApplied(["a"], "a")
  const afterAb = receiveApplied([...afterA.unechoed, "ab"], "ab")
  expect(receiveApplied(afterAb.unechoed, "a").echo).toBe(false)
})
