import { describe, expect, test } from "bun:test"
import {
  DEFAULT_ROLLBACK_POLICY,
  clock,
  latencyTone,
  policyLine,
  shareTone,
  watchShown,
  watchTitle,
  windowRemaining,
} from "./rollback-watch-model"

const now = Date.parse("2026-10-27T12:05:00Z")

const watch = (overrides) => ({
  id: 4,
  projectId: 1,
  environmentId: 2,
  runId: 9,
  releaseId: 42,
  warmReleaseId: 41,
  warmRuntimeId: "abc123def4567890",
  state: "watching",
  policy: DEFAULT_ROLLBACK_POLICY,
  cutoverAt: "2026-10-27T12:00:00Z",
  expiresAt: "2026-10-27T12:10:00Z",
  baseline: { requests: 400, failures: 2, errorRate: 0.005, p95: 120, from: "", until: "" },
  observed: { requests: 50, failures: 0, errorRate: 0, p95: 130, from: "", until: "" },
  verdict: {
    code: "within_baseline",
    judged: true,
    tripped: false,
    reason: "",
    latencyJudged: true,
  },
  attempts: 0,
  createdAt: "2026-10-27T12:00:00Z",
  releaseNumber: 42,
  warmReleaseNumber: 41,
  ...overrides,
})

describe("watchShown", () => {
  test("an open window is drawn", () => {
    expect(watchShown(watch(), now)).toBe(true)
    expect(watchShown(watch({ state: "rolling_back" }), now)).toBe(true)
  })
  test("a clean close is the expected answer and is not drawn", () => {
    expect(
      watchShown(
        watch({ state: "closed", endReason: "window_passed", endedAt: "2026-10-27T12:04:00Z" }),
        now,
      ),
    ).toBe(false)
  })
  test("a rollback stays for an hour", () => {
    const rolledBack = watch({ state: "rolled_back", endedAt: "2026-10-27T11:30:00Z" })
    expect(watchShown(rolledBack, now)).toBe(true)
    expect(watchShown(rolledBack, now + 60 * 60 * 1000)).toBe(false)
    expect(watchShown(null, now)).toBe(false)
  })
})

describe("the countdown", () => {
  test("counts down to zero and no further", () => {
    expect(windowRemaining(watch(), now)).toBe(300)
    expect(windowRemaining(watch(), now + 400_000)).toBe(0)
    expect(clock(300)).toBe("5:00")
    expect(clock(65)).toBe("1:05")
  })
})

describe("tones against the line", () => {
  test("the 5xx share is red past its line and amber past half of it", () => {
    expect(shareTone(watch())).toBe("default")
    expect(
      shareTone(
        watch({ observed: { requests: 100, failures: 4, errorRate: 0.04, from: "", until: "" } }),
      ),
    ).toBe("warning")
    expect(
      shareTone(
        watch({ observed: { requests: 100, failures: 9, errorRate: 0.09, from: "", until: "" } }),
      ),
    ).toBe("danger")
  })
  test("past the line on too few failures is amber, not red", () => {
    expect(
      shareTone(
        watch({ observed: { requests: 63, failures: 4, errorRate: 0.0635, from: "", until: "" } }),
      ),
    ).toBe("warning")
  })
  test("a thin baseline is judged against the margin alone", () => {
    const thin = { requests: 3, failures: 2, errorRate: 0.66, from: "", until: "" }
    expect(
      shareTone(
        watch({
          baseline: thin,
          observed: { requests: 50, failures: 5, errorRate: 0.1, from: "", until: "" },
        }),
      ),
    ).toBe("danger")
  })
  test("p95 is plain when it was not judged", () => {
    expect(latencyTone(watch({ observed: { ...watch().observed, p95: 2000 } }))).toBe("danger")
    expect(
      latencyTone(
        watch({
          observed: { ...watch().observed, p95: 2000 },
          verdict: { ...watch().verdict, latencyJudged: false },
        }),
      ),
    ).toBe("default")
  })
})

describe("words", () => {
  test("the title follows the state", () => {
    expect(watchTitle(watch())).toBe("Release #42 on watch")
    expect(watchTitle(watch({ state: "rolled_back" }))).toBe("Release #42 rolled back")
    expect(watchTitle(watch({ state: "rolling_back" }))).toBe("Rolling back to release #41")
  })
  test("the terms read as one sentence", () => {
    expect(policyLine(DEFAULT_ROLLBACK_POLICY)).toBe(
      "The previous release is kept warm for 10 minutes. Traffic goes back to it when the 5xx share passes its own by 5 points or the p95 reaches 2× its own, over at least 20 requests.",
    )
    expect(policyLine({ ...DEFAULT_ROLLBACK_POLICY, automatic: false, windowSeconds: 90 })).toBe(
      "The previous release is kept warm for 90 seconds, to switch back to by hand.",
    )
    expect(policyLine({ ...DEFAULT_ROLLBACK_POLICY, keepWarm: false })).toBe(
      "The previous release is removed as soon as a new one is live.",
    )
  })
})
