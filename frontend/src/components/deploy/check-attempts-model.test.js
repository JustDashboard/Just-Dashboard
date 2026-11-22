import { describe, expect, test } from "bun:test"
import { checkAttemptCount } from "./check-attempts-model"

describe("check attempt count", () => {
  test("counts the attempts folded into a kept one", () => {
    expect(
      checkAttemptCount([
        { number: 1, code: "connection_failed", repeats: 12, lastNumber: 13, spanMillis: 3000 },
        { number: 14, outcome: "passed" },
      ]),
    ).toBe(14)
  })

  test("evidence saved before folding counts one per entry", () => {
    expect(checkAttemptCount([{ number: 1 }, { number: 2 }, { number: 3 }])).toBe(3)
    expect(checkAttemptCount([])).toBe(0)
  })
})
