import { describe, expect, test } from "bun:test"
import {
  DEFAULT_ROLLBACK_POLICY,
  clock,
  latencyTone,
  policyLine,
  readingLine,
  shareTone,
  skewLine,
  warmLine,
  watchLead,
  watchProxy,
  watchShown,
  watchTitle,
  windowLine,
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
      "The previous release is kept warm for 10 minutes. Traffic goes back to it when the 5xx share passes its own by 5 points or 19 in 20 requests take 2× as long as its own did, over at least 20 requests.",
    )
    expect(policyLine({ ...DEFAULT_ROLLBACK_POLICY, automatic: false, windowSeconds: 90 })).toBe(
      "The previous release is kept warm for 90 seconds, to switch back to by hand.",
    )
    expect(policyLine({ ...DEFAULT_ROLLBACK_POLICY, keepWarm: false })).toBe(
      "The previous release is removed as soon as a new one is live.",
    )
  })
  test("the warm release is paused, or answers its own visitors", () => {
    expect(warmLine(watch())).toBe("release #41 paused")
    expect(
      warmLine(
        watch({
          warmRunning: true,
          warmServed: { requests: 1240, failures: 0, errorRate: 0, from: "", until: "" },
        }),
      ),
    ).toBe("release #41 answered 1,240 of its visitors' requests")
    expect(warmLine(watch({ warmRunning: true }))).toBe(
      "release #41 answered 0 of its visitors' requests",
    )
  })
  test("skew protection says how long a visitor is kept", () => {
    expect(skewLine(false, DEFAULT_ROLLBACK_POLICY)).toBeUndefined()
    expect(skewLine(true, DEFAULT_ROLLBACK_POLICY)).toBe(
      "A visitor stays on the release that answered them for up to 10 minutes after a cutover.",
    )
    expect(skewLine(true, { ...DEFAULT_ROLLBACK_POLICY, keepWarm: false })).toMatch(
      /only while it drains/,
    )
  })
})

describe("the Overview's block", () => {
  const judged = watch({
    baseline: { requests: 412, failures: 2, errorRate: 0.0049, p95: 118, from: "", until: "" },
    observed: { requests: 63, failures: 1, errorRate: 0.0159, p95: 131, from: "", until: "" },
  })
  test("the reading is one sentence in words, never a p95", () => {
    const lead = watchLead(judged)
    expect(lead.title).toBe("Release #42 fails 1.6% of its requests; 19 in 20 finish inside 131ms")
    expect(lead.hint).toBe("No worse than release #41 was.")
    expect(lead.tone).toBe("running")
    expect(`${lead.title} ${lead.hint}`).not.toMatch(/p95/)
    expect(watchLead(watch()).title).toBe(
      "Release #42 has failed none of its 50 requests; 19 in 20 finish inside 130ms",
    )
  })
  test("too few requests, no route and an environment that only watches say so", () => {
    const few = watchLead(
      watch({
        observed: { requests: 12, failures: 0, errorRate: 0, p95: 90, from: "", until: "" },
        verdict: {
          code: "not_enough_requests",
          judged: false,
          tripped: false,
          latencyJudged: false,
          reason: "Not enough to judge yet: 12 of the 20 requests a decision needs.",
        },
        policy: { ...DEFAULT_ROLLBACK_POLICY, automatic: false },
      }),
    )
    expect(few.tone).toBe("unknown")
    expect(few.hint).toBe(
      "Not enough to judge yet: 12 of the 20 requests a decision needs. Nothing is rolled back automatically in this environment.",
    )
    expect(watchLead(watch({ observed: undefined, verdict: undefined }))).toEqual({
      title: "Release #42 has answered no requests yet",
      hint: "20 requests decide.",
      tone: "unknown",
    })
    const noRoute = watchLead(
      watch({
        verdict: {
          code: "no_route",
          judged: false,
          tripped: false,
          latencyJudged: false,
          reason: "Release #42 has no domain, so nothing is judged automatically.",
        },
      }),
    )
    expect(noRoute).toEqual({
      title: "Release #42 has no domain, so nothing is judged automatically.",
      tone: "unknown",
    })
  })
  test("a slow release is said from its figures, and a failing one in the server's words", () => {
    const slow = watchLead(
      watch({
        observed: { requests: 120, failures: 0, errorRate: 0, p95: 2310, from: "", until: "" },
        verdict: {
          code: "latency",
          judged: true,
          tripped: true,
          latencyJudged: true,
          reason: "p95 rose to 2.31 s from 120 ms before (120 requests).",
        },
      }),
    )
    expect(slow.hint).toBe("19 in 20 requests now take up to 2.31s, from 120ms on release #41.")
    expect(slow.tone).toBe("danger")
    const failing = watchLead(
      watch({
        observed: { requests: 120, failures: 46, errorRate: 0.383, p95: 140, from: "", until: "" },
        verdict: {
          code: "error_rate",
          judged: true,
          tripped: true,
          latencyJudged: true,
          reason: "5xx answers rose to 38.3% (46 of 120 requests) from 0.5% before.",
        },
      }),
    )
    expect(failing.title).toBe(
      "Release #42 fails 38.3% of its requests; 19 in 20 finish inside 140ms",
    )
    expect(failing.hint).toBe("5xx answers rose to 38.3% (46 of 120 requests) from 0.5% before.")
    expect(failing.tone).toBe("danger")
  })
  test("a rollback is said by who made it, with the message that decided it", () => {
    expect(
      watchLead(watch({ state: "rolled_back", endMessage: "Traffic went back to release #41." })),
    ).toEqual({
      title: "Rolled back automatically",
      hint: "Traffic went back to release #41.",
      tone: "danger",
    })
    expect(
      watchLead(watch({ state: "rolled_back", endMessage: "Rolled back instantly by ana." })).title,
    ).toBe("Rolled back by hand")
  })
  test("each release's row of the picture reads in the sentence's words", () => {
    expect(readingLine(judged.baseline)).toBe("0.5% failed · 19 in 20 inside 118ms")
    expect(readingLine(watch().observed)).toBe("none failed · 19 in 20 inside 130ms")
    expect(readingLine({ requests: 9, failures: 1, errorRate: 1 / 9, from: "", until: "" })).toBe(
      "11.1% failed",
    )
    expect(readingLine(undefined)).toBe("no requests")
  })
  test("the window's line counts down while it watches", () => {
    expect(windowLine(watch(), 300)).toBe("5:00 left to decide · release #41 paused")
    expect(windowLine(watch({ state: "closing" }), 0)).toBe("release #41 paused")
  })
  test("the mark is the proxy that wrote the record", () => {
    expect(watchProxy(watch())).toBe("caddy")
    // nginx records no durations: requests with no time among them are its.
    expect(
      watchProxy(
        watch({ observed: { requests: 50, failures: 0, errorRate: 0, from: "", until: "" } }),
      ),
    ).toBe("nginx")
    expect(watchProxy(watch({ observed: undefined, baseline: undefined }))).toBe("caddy")
  })
})
