import { describe, expect, test } from "bun:test"
import { oldestReading, reading, stillRefreshing, updatedLabel } from "./freshness"

describe("how old the overview is", () => {
  test("a reading carries the moment it answered", async () => {
    const before = Date.now()
    const answer = await reading(Promise.resolve(["app.example.com"]))
    expect(answer.value).toEqual(["app.example.com"])
    expect(answer.at).toBeGreaterThanOrEqual(before)
    expect(answer.at).toBeLessThanOrEqual(Date.now())
  })

  test("a fetch that fails is not stamped", async () => {
    await expect(reading(Promise.reject(new Error("refused")))).rejects.toThrow("refused")
  })

  // The certificates are read every five minutes and the sites every thirty
  // seconds; the page is as old as the older of the two.
  test("the page is as old as its oldest reading", () => {
    expect(oldestReading([1_000, 5_000, undefined, 3_000])).toBe(1_000)
    expect(oldestReading([undefined, 4_000])).toBe(4_000)
  })

  test("with nothing read there is no age to give", () => {
    expect(oldestReading([])).toBeUndefined()
    expect(oldestReading([undefined, undefined])).toBeUndefined()
  })

  test("the age reads in seconds, then minutes", () => {
    const at = 1_000_000
    expect(updatedLabel(at, at)).toBe("Updated just now")
    expect(updatedLabel(at, at + 4_900)).toBe("Updated just now")
    expect(updatedLabel(at, at + 14_000)).toBe("Updated 14s ago")
    expect(updatedLabel(at, at + 150_000)).toBe("Updated 2m 30s ago")
    expect(updatedLabel(at, at + 3_600_000)).toBe("Updated 1h ago")
  })

  // The clock ticks once a second, so a reading can land after the tick the
  // label is drawn with.
  test("a reading newer than the clock is just now, not a negative age", () => {
    expect(updatedLabel(10_000, 9_000)).toBe("Updated just now")
  })
})

describe("waiting on a refresh", () => {
  const failed = new Error("refused")

  test("it waits while any source has not answered since it was asked", () => {
    const asked = {
      sites: { at: 1_000, error: undefined },
      certs: { at: 2_000, error: undefined },
    }
    expect(stillRefreshing(asked, { ...asked })).toBe(true)
    expect(
      stillRefreshing(asked, {
        sites: { at: 9_000, error: undefined },
        certs: { at: 2_000, error: undefined },
      }),
    ).toBe(true)
  })

  test("an answer or a failure since both count as answered", () => {
    const asked = {
      sites: { at: 1_000, error: undefined },
      certs: { at: 2_000, error: failed },
      status: { at: undefined, error: undefined },
    }
    expect(
      stillRefreshing(asked, {
        // A new answer.
        sites: { at: 9_000, error: undefined },
        // A new failure beside the last answer, which the poll keeps.
        certs: { at: 2_000, error: new Error("refused") },
        // A first answer.
        status: { at: 9_500, error: undefined },
      }),
    ).toBe(false)
  })

  test("a source whose poll was switched off is not waited for", () => {
    const asked = { certbot: { at: undefined, error: undefined } }
    expect(stillRefreshing(asked, {})).toBe(false)
  })
})
