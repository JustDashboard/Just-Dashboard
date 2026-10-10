import { expect, test } from "bun:test"
import {
  driftCounts,
  driftReading,
  driftRepairOutcome,
  driftMoves,
  driftRows,
  driftSummary,
  driftVerdict,
  driftReviewKey,
  selectedDriftRepairRequest,
} from "./network-drift"

test("missing, unreadable and incomplete comparisons keep distinct readings", () => {
  expect(driftReading("missing").label).toBe("Missing")
  expect(driftReading("unreadable").label).toBe("Could not read")
  expect(driftReading("unknown").tone).toBe("unknown")
  expect(driftReading("future_status").tone).toBe("unknown")
})

const observation = (id, status, resource = id) => ({ id, resource, status })
const report = (over = {}) => ({
  spec: observation("spec", "matching", "spec.json"),
  journal: observation("journal", "not_required", "change.json"),
  files: [],
  runtime: [],
  boot: { status: "missing", unit: "jd-network.service", execution: { status: "unrecorded" } },
  blocklists: [
    { id: 1, name: "Abuse", enabled: false, enforcement: "unknown", cache: {}, runtime: {} },
  ],
  ...over,
})

test("counts include unavailable configuration and boot evidence without inventing differences", () => {
  expect(
    driftCounts(
      ["matching", "unknown", "unreadable", "missing", "conflict"].map((status) => ({ status })),
    ),
  ).toEqual({ differences: 2, unknown: 2, matching: 1 })
  expect(driftCounts(driftRows(report({ spec: { status: "unreadable" } })))).toEqual({
    differences: 1,
    unknown: 2,
    matching: 0,
  })
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

test("every table row belongs to the domain whose picture line colours it", () => {
  const rows = driftRows(
    report({
      files: [observation("f1", "drift", "/etc/links"), observation("f2", "matching")],
      runtime: [observation("k1", "conflict")],
    }),
  )
  expect(rows.map((row) => row.domain)).toEqual([
    "config",
    "config",
    "files",
    "files",
    "kernel",
    "boot",
    "boot",
    "blocklists",
  ])
  expect(driftSummary(rows, "files")).toMatchObject({ total: 2, differences: 1, tone: "warning" })
  expect(driftSummary(rows, "kernel").tone).toBe("danger")
  expect(driftSummary(rows, "blocklists").tone).toBe("default")
})

test("a verdict says stale evidence before it says anything the evidence shows", () => {
  const rows = driftRows(report({ files: [observation("f1", "drift")] }))
  expect(driftVerdict(rows, true, true).label).toBe("Last known evidence")
  expect(driftVerdict(rows, false, false).label).toBe("Changed during inspection")
  expect(driftVerdict(rows, true, false)).toEqual({ tone: "warning", label: "2 differences" })
  const matching = report({ boot: { status: "matching", execution: { status: "succeeded" } } })
  expect(driftVerdict(driftRows(matching), true, false)).toEqual({
    tone: "running",
    label: "Everything matches",
  })
  const unrecorded = report({ boot: { status: "matching", execution: { status: "unrecorded" } } })
  expect(driftVerdict(driftRows(unrecorded), true, false).label).toBe("1 reading incomplete")
})

test("moves name the rows whose status changed between two inspections", () => {
  const rows = driftRows(report({ files: [observation("f1", "drift", "/etc/links")] }))
  expect(driftMoves(undefined, rows, 1)).toEqual([])
  const before = new Map(rows.map((row) => [row.id, row.status]))
  before.set("f1", "matching")
  before.delete("boot:unit")
  expect(driftMoves(before, rows, 7)).toEqual([
    { id: "f1", resource: "/etc/links", from: "matching", to: "drift", at: 7 },
  ])
})
