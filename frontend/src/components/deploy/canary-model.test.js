import { describe, expect, test } from "bun:test"
import {
  DEFAULT_CANARY_TERMS,
  canaryEndLabel,
  canaryProgress,
  canaryRemaining,
  canaryTitle,
  candidateShareTone,
  windowWords,
  withStrategy,
} from "./canary-model"

const canary = (overrides = {}) => ({
  id: 3,
  projectId: 1,
  environmentId: 2,
  runId: 9,
  releaseId: 42,
  liveReleaseId: 41,
  percent: 10,
  durationSeconds: 600,
  autoPromote: true,
  policy: {
    keepWarm: true,
    windowSeconds: 600,
    automatic: true,
    errorRateMargin: 5,
    latencyFactor: 2,
    minRequests: 20,
  },
  state: "running",
  startedAt: "2026-10-29T12:00:00Z",
  endsAt: "2026-10-29T12:10:00Z",
  releaseNumber: 7,
  liveReleaseNumber: 6,
  ...overrides,
})

describe("canary terms", () => {
  test("choosing the strategy brings its terms, and another takes them away", () => {
    const chosen = withStrategy({ strategy: "blue_green" }, "canary")
    expect(chosen.strategy).toBe("canary")
    expect(chosen.canary).toEqual(DEFAULT_CANARY_TERMS)
    const kept = withStrategy(
      { strategy: "canary", canary: { percent: 25, durationSeconds: 120 } },
      "canary",
    )
    expect(kept.canary.percent).toBe(25)
    expect("canary" in withStrategy(chosen, "stop_first")).toBe(false)
  })

  test("read as words", () => {
    expect(windowWords(60)).toBe("a minute")
    expect(windowWords(600)).toBe("10 minutes")
  })
})

describe("a trial", () => {
  const now = Date.parse("2026-10-29T12:04:00Z")

  test("counts down its window", () => {
    expect(canaryRemaining(canary(), now)).toBe(360)
    expect(canaryProgress(canary(), now)).toBeCloseTo(0.4)
    expect(canaryRemaining(canary(), Date.parse("2026-10-29T13:00:00Z"))).toBe(0)
  })

  test("says what it is doing", () => {
    expect(canaryTitle(canary())).toBe("Release #7 on 10% of the requests")
    expect(canaryTitle(canary({ decision: "abort" }))).toBe("Aborting release #7")
    expect(canaryTitle(canary({ state: "holding" }))).toBe("Release #7 waits to be promoted")
    expect(canaryTitle(canary({ state: "aborted" }))).toBe("Release #7's canary aborted")
    expect(canaryEndLabel("failed")).toBe("Aborted on its figures")
  })

  test("colours the candidate's 5xx against the live release's line", () => {
    const live = { requests: 400, failures: 2, errorRate: 0.005, from: "", until: "" }
    expect(
      candidateShareTone(
        canary({
          live,
          candidate: { requests: 60, failures: 30, errorRate: 0.5, from: "", until: "" },
        }),
      ),
    ).toBe("danger")
    expect(
      candidateShareTone(
        canary({
          live,
          candidate: { requests: 8, failures: 4, errorRate: 0.5, from: "", until: "" },
        }),
      ),
    ).toBe("warning")
    expect(
      candidateShareTone(
        canary({
          live,
          candidate: { requests: 60, failures: 0, errorRate: 0, from: "", until: "" },
        }),
      ),
    ).toBe("default")
  })
})
