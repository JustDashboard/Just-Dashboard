import { expect, test } from "bun:test"
import {
  driftCounts,
  driftReading,
  driftRepairOutcome,
  driftReportObservations,
  driftReviewKey,
  selectedDriftRepairRequest,
} from "./network-drift"

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
  ).toEqual({ differences: 1, unknown: 2, matching: 0 })
})

test("selected execution sends only reviewed identities and refuses advisory or shifted evidence", () => {
  const report = {
    consistent: true,
    savedGeneration: "generation",
    repairPlan: {
      generation: "generation",
      status: "review_required",
      executable: true,
      items: [
        {
          id: "one",
          executable: true,
          reviewToken: "reviewed",
          resource: "/owned/path",
          after: "render",
        },
        { id: "advice", executable: false, reviewToken: "advisory" },
      ],
    },
  }
  expect(selectedDriftRepairRequest(report, ["one"])).toEqual({
    generation: "generation",
    selections: [{ id: "one", reviewToken: "reviewed" }],
  })
  for (const ids of [[], ["unknown"], ["one", "one"], ["advice"], ["one", "advice"]]) {
    expect(selectedDriftRepairRequest(report, ids)).toBeUndefined()
  }
  expect(selectedDriftRepairRequest({ ...report, consistent: false }, ["one"])).toBeUndefined()
  expect(
    selectedDriftRepairRequest({ ...report, savedGeneration: "changed" }, ["one"]),
  ).toBeUndefined()
  expect(
    selectedDriftRepairRequest(
      { ...report, repairPlan: { ...report.repairPlan, executable: false } },
      ["one"],
    ),
  ).toBeUndefined()
  expect(
    selectedDriftRepairRequest(
      { ...report, repairPlan: { ...report.repairPlan, status: "blocked" } },
      ["one"],
    ),
  ).toBeUndefined()
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

test("repair outcomes require independent phase evidence and retain boot execution limits", () => {
  const render = {
    phase: "saved",
    watchdog: "completed",
    runtime: "not_applied",
    persistence: "written",
    boot: "not_verified",
  }
  expect(driftRepairOutcome(render)).toMatchObject({
    tone: "success",
    title: "Selected repairs applied",
    description: "Selected boot inputs saved; boot execution remains unverified.",
  })
  const pending = { ...render, phase: "awaiting_confirmation", watchdog: "armed" }
  expect(driftRepairOutcome(pending).title).toBe("Repairs await reconnection confirmation")
  expect(
    driftRepairOutcome({
      ...render,
      runtime: "applied",
      persistence: "not_applicable",
      boot: "not_applicable",
    }).description,
  ).toBe("Runtime applied; not saved for boot.")
  expect(driftRepairOutcome({ ...render, runtime: "applied" }).description).toContain(
    "boot execution remains unverified",
  )
  for (const changed of [
    { phase: "degraded" },
    { phase: "boot_degraded" },
    { phase: "unreadable" },
    { watchdog: "armed" },
    { runtime: "unknown" },
    { persistence: "unknown" },
    { boot: "unknown" },
    { boot: "failed" },
    { recoveryErrors: ["restore failed"] },
    { persistence: "not_applicable", boot: "not_applicable" },
  ]) {
    expect(driftRepairOutcome({ ...render, ...changed }).tone).toBe("warning")
  }
  expect(driftRepairOutcome({ ...pending, watchdog: "failed_to_arm" }).tone).toBe("warning")
})
