import { describe, expect, test } from "bun:test"
import {
  pushTrails,
  sameOrder,
  settleOrder,
  spanPercent,
  tickPercent,
  timeAt,
  trailMoves,
  trailSignature,
} from "./bar-reading-model"

const parts = (values: Record<string, number>) =>
  Object.entries(values).map(([key, value]) => ({ key, value }))

describe("settleOrder", () => {
  test("the first reading is heaviest first", () => {
    expect(settleOrder([], parts({ a: 1, b: 3, c: 2 }))).toEqual(["b", "c", "a"])
  })

  test("two parts within a rounding of each other keep their places", () => {
    const order = settleOrder(["a", "b"], parts({ a: 100, b: 110 }))
    expect(order).toEqual(["a", "b"])
  })

  test("a part that has clearly overtaken the one above it moves up", () => {
    expect(settleOrder(["a", "b"], parts({ a: 100, b: 200 }))).toEqual(["b", "a"])
  })

  test("a part that clears several climbs past all of them", () => {
    expect(settleOrder(["a", "b", "c"], parts({ a: 10, b: 9, c: 50 }))).toEqual(["c", "a", "b"])
  })

  test("a part that has gone leaves, and the rest hold", () => {
    expect(settleOrder(["a", "b", "c"], parts({ a: 3, c: 3.5 }))).toEqual(["a", "c"])
  })

  test("a new part joins by its value", () => {
    expect(settleOrder(["a", "c"], parts({ a: 10, b: 5, c: 1 }))).toEqual(["a", "b", "c"])
    expect(settleOrder(["a"], parts({ a: 10, b: 1 }))).toEqual(["a", "b"])
  })

  test("nothing at all is nothing", () => {
    expect(settleOrder(["a"], [])).toEqual([])
  })
})

test("sameOrder compares places, not membership", () => {
  expect(sameOrder(["a", "b"], ["a", "b"])).toBe(true)
  expect(sameOrder(["a", "b"], ["b", "a"])).toBe(false)
  expect(sameOrder(["a"], ["a", "b"])).toBe(false)
})

describe("pushTrails", () => {
  test("appends each part's reading and drops the oldest past the length", () => {
    const first = pushTrails(new Map(), parts({ a: 1 }), 2)
    const second = pushTrails(first, parts({ a: 2 }), 2)
    const third = pushTrails(second, parts({ a: 3 }), 2)
    expect(third.get("a")).toEqual([2, 3])
  })

  test("forgets a part no longer drawn, so it starts again when it returns", () => {
    const seen = pushTrails(pushTrails(new Map(), parts({ a: 1, b: 2 })), parts({ a: 1 }))
    expect(seen.has("b")).toBe(false)
    expect(pushTrails(seen, parts({ a: 1, b: 9 })).get("b")).toEqual([9])
  })

  test("the signature changes only when a reading does", () => {
    expect(trailSignature(parts({ a: 1 }))).toBe(trailSignature(parts({ a: 1 })))
    expect(trailSignature(parts({ a: 1 }))).not.toBe(trailSignature(parts({ a: 2 })))
  })
})

test("a trail is drawn only once it has a shape", () => {
  expect(trailMoves(undefined)).toBe(false)
  expect(trailMoves([1, 2])).toBe(false)
  expect(trailMoves([5, 5, 5, 5])).toBe(false)
  expect(trailMoves([5, 6, 5])).toBe(true)
})

test("a span stays on the bar whatever the total says", () => {
  expect(spanPercent(25, 100)).toBe(25)
  expect(spanPercent(300, 100)).toBe(100)
  expect(spanPercent(5, 0)).toBe(0)
  expect(spanPercent(-5, 100)).toBe(0)
})

test("a tick stays on its track", () => {
  expect(tickPercent(0.5)).toBe(50)
  expect(tickPercent(-1)).toBe(0)
  expect(tickPercent(2)).toBe(100)
})

describe("timeAt", () => {
  const day = 86_400_000
  const now = 10 * day

  test("the present is the near end and a day ago the far end", () => {
    expect(timeAt(now, now, day)).toBe(1)
    expect(timeAt(now - day, now, day)).toBe(0)
    expect(timeAt(now - day / 2, now, day)).toBe(0.5)
  })

  test("older than the window sits on the far end, and a clock ahead on the near one", () => {
    expect(timeAt(now - 5 * day, now, day)).toBe(0)
    expect(timeAt(now + day, now, day)).toBe(1)
  })
})
