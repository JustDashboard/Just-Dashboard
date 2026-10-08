import { expect, test } from "bun:test"
import { driftCounts, driftReading, driftReportObservations, driftReviewKey } from "./network-drift"

test("missing, unreadable and incomplete comparisons keep distinct readings", () => {
  expect(driftReading("missing").label).toBe("Missing")
  expect(driftReading("unreadable").label).toBe("Could not read")
  expect(driftReading("unknown").tone).toBe("unknown")
  expect(driftReading("future_status").tone).toBe("unknown")
})

test("counts include unavailable configuration and boot evidence without inventing differences", () => {
  expect(
    driftCounts(
      ["matching", "unknown", "unreadable", "missing", "conflict"].map((status) => ({ status })),
    ),
  ).toEqual({ differences: 2, unknown: 2, matching: 1 })
  expect(
    driftCounts(
      driftReportObservations({
        spec: { status: "unreadable" },
        journal: { status: "not_required" },
        files: [],
        runtime: [],
        boot: { status: "missing" },
        blocklists: [{ id: 1, enabled: false, enforcement: "unknown" }],
      }),
    ),
  ).toEqual({ differences: 1, unknown: 1, matching: 0 })
})

test("review identity binds comparison bytes, boot ownership and unresolved journal", () => {
  const report = {
    savedGeneration: "generation",
    consistent: true,
    spec: { id: "spec", status: "matching" },
    journal: { id: "journal", status: "not_required" },
    boot: { status: "matching", owned: true, fragmentPath: "/owned/unit" },
    repairPlan: { generation: "generation", items: [], blockers: [] },
    files: [
      {
        id: "render:links",
        status: "drift",
        expected: { sha256: "expected" },
        observed: { sha256: "observed" },
      },
    ],
    runtime: [],
  }
  const key = driftReviewKey(report)
  expect(driftReviewKey({ ...report, checkedAt: "later" })).toBe(key)
  expect(
    driftReviewKey({ ...report, repairPlan: { ...report.repairPlan, createdAt: "later" } }),
  ).toBe(key)
  expect(driftReviewKey({ ...report, consistent: false })).not.toBe(key)
  expect(
    driftReviewKey({
      ...report,
      files: [{ ...report.files[0], observed: { sha256: "replacement" } }],
    }),
  ).not.toBe(key)
  expect(
    driftReviewKey({
      ...report,
      files: [{ ...report.files[0], expected: { sha256: "new cache" } }],
    }),
  ).not.toBe(key)
  expect(driftReviewKey({ ...report, change: { phase: "awaiting_confirmation" } })).not.toBe(key)
  expect(driftReviewKey({ ...report, spec: { ...report.spec, status: "unreadable" } })).not.toBe(
    key,
  )
  expect(
    driftReviewKey({ ...report, boot: { ...report.boot, fragmentPath: "/foreign/unit" } }),
  ).not.toBe(key)
})
