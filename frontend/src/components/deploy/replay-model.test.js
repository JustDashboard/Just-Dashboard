import { expect, test } from "bun:test"
import {
  REPLAY_DEFAULTS,
  driftAnswer,
  driftRequest,
  latencyHint,
  replayMs,
  replayDraftOf,
  replayPlanOf,
  replayRefusal,
  replayOf,
  replayPathsOf,
  replayPathsRefusal,
  replayPolicyLine,
  replaySummary,
  replayTerms,
  storedReplay,
} from "./replay-model"

const report = {
  replayed: true,
  domains: ["shop.example.test"],
  target: "10.20.0.7:3000",
  read: 812,
  eligible: 64,
  requests: 50,
  answered: 50,
  drifted: 2,
  driftPercent: 4,
  maxStatusDrift: 2,
  latencyFactor: 2,
  latencyRecorded: true,
  latencyJudged: true,
  recordedP95: 41.5,
  candidateP95: 38.2,
  verdict: { code: "status_drift", tripped: true, reason: "2 of 50 replayed requests (4.0%)…" },
  drifts: [
    {
      method: "GET",
      path: "/pricing",
      query: "plan=team",
      recordedStatus: 200,
      candidateStatus: 404,
    },
    {
      method: "HEAD",
      path: "/feed.xml",
      recordedStatus: 200,
      candidateStatus: 0,
      error: "timeout",
    },
  ],
  concurrency: 4,
  timeoutSeconds: 60,
  timedOut: false,
  durationMs: 1840,
}

test("absent terms run at the server's defaults", () => {
  expect(replayTerms(undefined)).toEqual({ ...REPLAY_DEFAULTS, paths: [] })
  expect(replayTerms({ enabled: true, requests: 120, paths: ["/docs"] })).toEqual({
    requests: 120,
    maxStatusDrift: 2,
    latencyFactor: 2,
    timeoutSeconds: 60,
    paths: ["/docs"],
  })
})

test("a replay that asks for nothing is stored as nothing", () => {
  expect(storedReplay(undefined)).toBeUndefined()
  expect(storedReplay({ enabled: false })).toBeUndefined()
  expect(storedReplay({ enabled: false, paths: [], requests: undefined })).toBeUndefined()
  // Terms typed while it is off are kept for when it is turned on.
  expect(storedReplay({ enabled: false, requests: 80 })).toEqual({ enabled: false, requests: 80 })
  expect(storedReplay({ enabled: true, paths: [], latencyFactor: undefined })).toEqual({
    enabled: true,
  })
})

test("the allow-list is read one prefix a line and held to the server's rule", () => {
  expect(replayPathsOf(" /docs \n\n/blog\n/docs\n")).toEqual(["/docs", "/blog"])
  expect(replayPathsRefusal(["/docs", "/api/v1/"])).toBeUndefined()
  expect(replayPathsRefusal(["docs"])).toContain("docs is not a path prefix")
  expect(replayPathsRefusal(["/a?b"])).toContain("is not a path prefix")
  expect(replayPathsRefusal(["/a b"])).toContain("is not a path prefix")
  expect(replayPathsRefusal(["/" + "a".repeat(256)])).toContain("is not a path prefix")
  expect(replayPathsRefusal(Array.from({ length: 33 }, (_, i) => `/p${i}`))).toBe(
    "Keep at most 32 path prefixes.",
  )
})

test("the draft holds the terms as typed and saves them as the server stores them", () => {
  const empty = replayDraftOf(undefined)
  expect(empty).toEqual({
    enabled: false,
    requests: "",
    maxStatusDrift: "",
    latencyFactor: "",
    timeoutSeconds: "",
    paths: "",
  })
  expect(replayPlanOf(empty)).toBeUndefined()
  const saved = { enabled: true, requests: 120, paths: ["/docs", "/blog"], latencyFactor: 1.5 }
  const draft = replayDraftOf(saved)
  expect(draft.paths).toBe("/docs\n/blog")
  expect(replayPlanOf(draft)).toEqual(saved)
  // Blank and zero are the default; "0." is on the way to "0.5".
  expect(
    replayPlanOf({ ...draft, requests: "0", latencyFactor: "", maxStatusDrift: "0." }),
  ).toEqual({
    enabled: true,
    paths: ["/docs", "/blog"],
  })
  expect(replayPlanOf({ ...empty, maxStatusDrift: "0.5" })).toEqual({
    enabled: false,
    maxStatusDrift: 0.5,
  })
})

test("a term the server would refuse is named under its field", () => {
  const on = { ...replayDraftOf(undefined), enabled: true }
  expect(replayRefusal(on)).toBeUndefined()
  expect(replayRefusal({ ...on, requests: "900" })).toEqual({
    field: "requests",
    message: "The count is a whole number from 1 to 500.",
  })
  expect(replayRefusal({ ...on, requests: "12.5" })?.field).toBe("requests")
  expect(replayRefusal({ ...on, requests: "many" })).toEqual({
    field: "requests",
    message: "The count is a number.",
  })
  expect(replayRefusal({ ...on, maxStatusDrift: "120" })?.message).toBe(
    "The drift allowed is between 0 and 100.",
  )
  expect(replayRefusal({ ...on, latencyFactor: "0.5" })?.message).toBe(
    "The p95 factor is between 1 and 10.",
  )
  expect(replayRefusal({ ...on, timeoutSeconds: "301" })?.field).toBe("timeoutSeconds")
  expect(replayRefusal({ ...on, paths: "/ok\napi" })).toEqual({
    field: "paths",
    message: "api is not a path prefix: one begins with / and has no space, query or fragment.",
  })
})

test("the settings head says what the next release is held to", () => {
  expect(replayPolicyLine(undefined)).toBe("Off · releases go live on their health checks alone")
  expect(replayPolicyLine({ enabled: false, requests: 80 })).toBe(
    "Off · releases go live on their health checks alone",
  )
  expect(replayPolicyLine({ enabled: true })).toBe(
    "On · the last 50 requests, 2% drift, p95 within 2×",
  )
  expect(
    replayPolicyLine({ enabled: true, requests: 200, paths: ["/docs"], maxStatusDrift: 0.5 }),
  ).toBe("On · the last 200 requests under 1 prefix, 0.5% drift, p95 within 2×")
})

test("a replayed request's time reads in the unit it was measured in", () => {
  expect(replayMs(undefined)).toBe("—")
  expect(replayMs(0.4)).toBe("< 1 ms")
  expect(replayMs(41.5)).toBe("42 ms")
  expect(replayMs(1840)).toBe("1.8 s")
})

test("every replay step's evidence reads as a report", () => {
  expect(replayOf(undefined)).toBeUndefined()
  expect(replayOf({ outcome: "passed", checks: [] })).toBeUndefined()
  expect(replayOf(report)).toBe(report)
  const skipped = {
    replayed: false,
    reason: "traffic replay is turned off for this plan",
    drifts: [],
  }
  expect(replayOf(skipped)).toBe(skipped)
  expect(replaySummary(skipped)).toBe("Not replayed: traffic replay is turned off for this plan")
})

test("the step row says how many were replayed, drifted, and both p95s", () => {
  expect(replaySummary(report)).toBe("50 requests · 2 drifted · p95 38 ms (42 ms recorded)")
  expect(replaySummary({ ...report, drifted: 0, latencyJudged: false })).toBe(
    "50 requests · none drifted",
  )
  expect(latencyHint(report)).toBe("42 ms recorded")
  expect(latencyHint({ ...report, latencyJudged: false, latencyRecorded: false })).toBe(
    "the ingress records no times",
  )
  expect(latencyHint({ ...report, latencyJudged: false })).toBe("too few timed requests")
})

test("a drifted request reads as the record has it and what came back", () => {
  expect(driftRequest(report.drifts[0])).toBe("GET /pricing?plan=team")
  expect(driftRequest(report.drifts[1])).toBe("HEAD /feed.xml")
  expect(driftAnswer(report.drifts[0])).toBe("404")
  expect(driftAnswer(report.drifts[1])).toBe("no answer (timeout)")
  expect(driftAnswer({ ...report.drifts[1], error: "out_of_time" })).toBe("not asked in time")
})
