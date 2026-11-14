import { describe, expect, test } from "bun:test"
import { deliveryFacts, deliveryLead } from "./delivery-model"

const format = (seconds) => `${seconds}s`
const data = (overrides) => ({
  windowDays: 30,
  succeeded: 14,
  failed: 0,
  successRate: 1,
  failureStreak: 0,
  deploysPerWeek: 3.27,
  medianDurationSeconds: 131,
  p95DurationSeconds: 181,
  recoveredFailures: 0,
  meanRecoverySeconds: 0,
  ...overrides,
})

describe("deliveryLead", () => {
  test("a clean window says so", () => {
    expect(deliveryLead(data(), format)).toEqual({
      title: "14 releases in the last 30 days, all successful",
      hint: "About 3.3 a week · usually 131s each, 95% within 181s",
    })
  })

  test("a single release is not a plural", () => {
    expect(deliveryLead(data({ succeeded: 1 }), format).title).toBe(
      "1 release in the last 30 days, and it succeeded",
    )
  })

  test("failures lead the sentence and say how recovery went", () => {
    const lead = deliveryLead(
      data({ succeeded: 9, failed: 3, recoveredFailures: 2, meanRecoverySeconds: 5400 }),
      format,
    )
    expect(lead.title).toBe("9 of 12 releases succeeded in the last 30 days")
    expect(lead.hint).toContain("back up in 5400s on average after a failure")
    expect(
      deliveryLead(data({ succeeded: 0, failed: 2, medianDurationSeconds: 0 }), format),
    ).toEqual({
      title: "0 of 2 releases succeeded in the last 30 days",
      hint: "no failure followed by a success yet",
    })
  })

  test("a window of only cancelled runs decided nothing", () => {
    expect(
      deliveryLead(data({ succeeded: 0, windowDays: 7, medianDurationSeconds: 0 }), format).title,
    ).toBe("No release finished in the last 7 days")
  })
})

describe("deliveryFacts", () => {
  const within = (figure) => `19 in 20 inside ${figure}`
  const words = (facts) => facts.map((fact) => `${fact.figure} ${fact.words}`.trim())

  test("a clean window has a rate, a frequency and a time, and no recovery", () => {
    const facts = deliveryFacts(data(), format, within)
    expect(words(facts)).toEqual([
      "100% succeeded",
      "3.3 a week",
      "131s usually, 19 in 20 inside 181s",
    ])
    expect(facts[0].basis).toBe("14 of 14 decided releases")
    expect(facts[0].tone).toBeUndefined()
    expect(facts[1].basis).toBe("14 successful over 30 days")
  })

  test("failures add how recovery went, and a streak colours the rate", () => {
    const facts = deliveryFacts(
      data({
        succeeded: 9,
        failed: 3,
        successRate: 0.75,
        failureStreak: 1,
        recoveredFailures: 2,
        meanRecoverySeconds: 5400,
      }),
      format,
      within,
    )
    expect(words(facts).at(0)).toBe("75% succeeded")
    expect(facts[0].tone).toBe("warning")
    expect(words(facts).at(-1)).toBe("5400s to recover")
    expect(facts.at(-1).basis).toBe("Mean over 2 recovered failures")
  })

  test("a window nothing succeeded in has no frequency and no time", () => {
    const facts = deliveryFacts(
      data({ succeeded: 0, failed: 2, successRate: 0, medianDurationSeconds: 0 }),
      format,
      within,
    )
    expect(words(facts)).toEqual([
      "0% succeeded",
      "no release succeeded in this window",
      "no failure followed by a success yet",
    ])
  })

  test("a window of only cancelled runs has no facts", () => {
    expect(deliveryFacts(data({ succeeded: 0, medianDurationSeconds: 0 }), format, within)).toEqual(
      [],
    )
  })
})
