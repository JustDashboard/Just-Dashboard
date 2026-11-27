import { describe, expect, test } from "bun:test"
import { coverageNote, keptWords, periodWords } from "./window-headline-model"

const at = (iso) => iso.slice(5, 16).replace("T", " ")
const NOW = Date.parse("2026-11-27T19:00:00Z")

/** A window's answer with only what the words read. */
function answer({ complete = true, from, history } = {}) {
  return {
    status: "available",
    latency: true,
    complete,
    observedAt: "2026-11-27T19:00:00Z",
    entries: [],
    summary: {},
    coverage: { exists: true, from, held: 10, complete, cursor: 1, refreshedAt: "", history },
  }
}

describe("the stretch the figures are about", () => {
  test("a preset says itself, a dragged span its two ends", () => {
    expect(periodWords("1h", undefined, undefined, at)).toBe("over the last hour")
    expect(periodWords("30d", undefined, undefined, at)).toBe("over the last 30 days")
    expect(periodWords("custom", "2026-11-27T18:34:00Z", "2026-11-27T19:06:00Z", at)).toBe(
      "between 11-27 18:34 and 11-27 19:06",
    )
    expect(periodWords("custom", "2026-11-27T18:34:00Z", undefined, at)).toBe("since 11-27 18:34")
  })
})

describe("how far back the record reaches", () => {
  const week = NOW - 7 * 86_400_000

  test("a window inside the kept history needs no word", () => {
    const data = answer({ history: { from: "2026-11-01T00:00:00Z", days: 30, recording: true } })
    expect(coverageNote(data, week, at)).toBeUndefined()
    expect(keptWords(data.coverage.history)).toBe("Kept for 30 days")
  })

  test("a week asked of a record two days old says where it starts", () => {
    const data = answer({ history: { from: "2026-11-25T08:00:00Z", days: 30, recording: true } })
    expect(coverageNote(data, week, at)).toBe("Recorded since 11-25 08:00")
  })

  test("the history beside the coverage is read too", () => {
    const data = answer()
    data.history = { from: "2026-11-25T08:00:00Z", days: 30, recording: true }
    expect(coverageNote(data, week, at)).toBe("Recorded since 11-25 08:00")
  })

  test("without a kept history, a cut record is a floor", () => {
    const data = answer({ complete: false, from: "2026-11-27T02:00:00Z" })
    expect(coverageNote(data, week, at)).toBe("Held since 11-27 02:00, so the figures are a floor")
    expect(coverageNote(answer({ from: "2026-11-27T02:00:00Z" }), week, at)).toBeUndefined()
  })

  test("a route nobody records says so, rather than nothing", () => {
    const data = answer({ history: { days: 30, recording: false } })
    expect(coverageNote(data, week, at)).toBe("Not recorded past what the proxy holds")
    expect(keptWords(undefined)).toBeUndefined()
  })
})
