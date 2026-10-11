import { expect, test } from "bun:test"
import { retryDelay } from "./use-socket"

test("reconnect delays double from one second up to fifteen", () => {
  const ceilings = [1, 2, 3, 4, 5, 6, 10].map((attempt) => retryDelay(attempt, 1))
  expect(ceilings).toEqual([1000, 2000, 4000, 8000, 15000, 15000, 15000])
})

test("half of each delay is jitter, so sockets that drop together do not redial together", () => {
  expect(retryDelay(1, 0)).toBe(500)
  expect(retryDelay(4, 0)).toBe(4000)
  expect(retryDelay(4, 0.5)).toBe(6000)
  expect(retryDelay(9, 0)).toBe(7500)
})
