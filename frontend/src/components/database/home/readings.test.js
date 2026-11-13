import { describe, expect, test } from "bun:test"
import { ago, compact, perSecond } from "./readings"

describe("how a figure is written", () => {
  test("every digit up to ten thousand, compact past it", () => {
    expect(compact(0)).toBe("0")
    expect(compact(9999)).toBe("9,999")
    expect(compact(16_103_894)).toBe("16.1M")
    expect(compact(28_600)).toBe("28.6K")
  })

  test("a rate keeps one decimal only while it is small", () => {
    expect(perSecond(0)).toBe("0")
    expect(perSecond(0.44)).toBe("0.4")
    expect(perSecond(3)).toBe("3")
    expect(perSecond(42.7)).toBe("43")
    expect(perSecond(12_345)).toBe("12.3K")
  })

  test("an age is measured against the newest reading", () => {
    expect(ago(0, 10_000)).toBe("just now")
    expect(ago(0, 610_000)).toBe("10m ago")
    // One unit: an age of three hours and ten seconds is three hours.
    expect(ago(0, 3 * 3600 * 1000 + 10_000)).toBe("3h ago")
    expect(ago(0, 47 * 3600 * 1000)).toBe("47h ago")
    expect(ago(0, 9 * 86_400 * 1000)).toBe("9d ago")
  })
})
