import { expect, test } from "bun:test"
import {
  driftCounts,
  driftFileProduct,
  driftMoves,
  driftReading,
  driftRepairOutcome,
  driftReviewKey,
  driftRows,
  driftSorted,
  driftVerdict,
  selectedDriftRepairRequest,
  systemdInstant,
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
  ).toEqual({ differences: 2, conflicts: 1, unknown: 2, matching: 1 })
  expect(
    driftCounts(
      driftRows({
        spec: { id: "spec", domain: "spec", resource: "/n/spec.json", status: "unreadable" },
        journal: {
          id: "journal",
          domain: "journal",
          resource: "/n/change.json",
          status: "not_required",
        },
        files: [],
        runtime: [],
        boot: { status: "missing", unit: "just-dashboard-network.service" },
        blocklists: [{ id: 1, name: "Local", enabled: false, enforcement: "unknown" }],
      }),
    ),
  ).toEqual({ differences: 1, conflicts: 0, unknown: 2, matching: 0 })
})

const observation = (domain, resource, status = "matching", extra = {}) => ({
  id: `${domain}:${resource}`,
  domain,
  resource,
  status,
  owned: true,
  ...extra,
})

const report = {
  consistent: true,
  spec: observation("spec", "/etc/just-dashboard/network/spec.json"),
  journal: observation("journal", "/etc/just-dashboard/network/change.json", "not_required"),
  files: [
    observation("render", "/etc/just-dashboard/network/gateway.nft"),
    observation("render", "/etc/just-dashboard/network/links.batch", "drift"),
    observation("render", "/etc/systemd/system/just-dashboard-network.service"),
  ],
  runtime: [
    observation("route", "3", "missing", {
      expected: { family: "inet", destination: "10.30.0.0/24", table: "100" },
    }),
    observation("address", "office/lan0/10.20.0.1/24"),
    observation("link", "wg-office", "matching", { expected: { kind: "wireguard" } }),
    observation("admission", "inet/DOCKER-USER", "conflict", { observed: { tool: "iptables" } }),
  ],
  boot: {
    status: "matching",
    unit: "just-dashboard-network.service",
    execution: { status: "failed", commands: [] },
  },
  blocklists: [
    { id: 1, name: "Spamhaus DROP", enabled: true, enforcement: "verified" },
    { id: 2, name: "FireHOL level 1", enabled: true, enforcement: "degraded" },
  ],
}

test("each comparison belongs to one domain and is drawn as the product that reads it", () => {
  const rows = driftRows(report)
  const by = Object.fromEntries(rows.map((row) => [row.id, row]))
  expect(rows.map((row) => row.domain)).toEqual([
    "saved",
    "saved",
    "files",
    "files",
    "files",
    "kernel",
    "kernel",
    "kernel",
    "kernel",
    "boot",
    "boot",
    "blocklists",
    "blocklists",
  ])
  expect(by["render:/etc/just-dashboard/network/gateway.nft"]).toMatchObject({
    product: "netfilter",
    label: "gateway.nft",
    detail: "/etc/just-dashboard/network",
  })
  expect(by["route:3"]).toMatchObject({
    product: "linux",
    label: "10.30.0.0/24",
    detail: "table 100 · route 3",
  })
  expect(by["address:office/lan0/10.20.0.1/24"]).toMatchObject({
    label: "10.20.0.1/24",
    detail: "on lan0 · in office",
  })
  expect(by["link:wg-office"].product).toBe("wireguard")
  expect(by["admission:inet/DOCKER-USER"]).toMatchObject({
    product: "netfilter",
    label: "DOCKER-USER",
    detail: "inet · iptables",
  })
  expect(by["boot:activation"].status).toBe("drift")
  expect(by["blocklist:1"].product).toBe("spamhaus")
  expect(by["blocklist:2"]).toMatchObject({ product: "firehol", status: "drift" })
  expect(driftFileProduct("/etc/sysctl.d/90-just-dashboard.conf")).toBe("linux")
  expect(driftFileProduct("/etc/just-dashboard/network/network-recovery")).toBeUndefined()
})

test("rows sort worst first, and in restore order within a state", () => {
  expect(
    driftSorted(driftRows(report))
      .map((row) => row.id)
      .slice(0, 6),
  ).toEqual([
    "admission:inet/DOCKER-USER",
    "render:/etc/just-dashboard/network/links.batch",
    "boot:activation",
    "blocklist:2",
    "route:3",
    "spec:/etc/just-dashboard/network/spec.json",
  ])
})

test("the verdict puts a failed read and a changing configuration before any difference", () => {
  const rows = driftRows(report)
  expect(driftVerdict(report, rows, true).label).toBe("Last known evidence")
  expect(driftVerdict({ consistent: false }, rows, false).label).toBe("Changed during inspection")
  expect(driftVerdict(report, rows, false)).toEqual({
    tone: "danger",
    label: "5 differences",
    narrows: "differ",
  })
  expect(driftVerdict(report, [{ status: "drift" }], false).tone).toBe("warning")
  expect(driftVerdict(report, [{ status: "unknown" }, { status: "matching" }], false)).toEqual({
    tone: "unknown",
    label: "1 incomplete",
  })
  expect(driftVerdict(report, [{ status: "matching" }], false).label).toBe("Everything matches")
})

test("moves are the comparisons whose state changed between two inspections", () => {
  const before = driftRows(report)
  const after = driftRows({
    ...report,
    files: [report.files[0], { ...report.files[1], status: "matching" }],
    runtime: [...report.runtime, observation("sysctl", "net.ipv4.ip_forward", "drift")],
  })
  expect(driftMoves(before, before, "t")).toEqual([])
  expect(driftMoves(before, after, "t").map(({ id, from, to }) => ({ id, from, to }))).toEqual([
    { id: "render:/etc/just-dashboard/network/links.batch", from: "drift", to: "matching" },
    { id: "sysctl:net.ipv4.ip_forward", from: undefined, to: "drift" },
    {
      id: "render:/etc/systemd/system/just-dashboard-network.service",
      from: "matching",
      to: undefined,
    },
  ])
})

test("a systemd timestamp is read only where its zone is unambiguous", () => {
  expect(systemdInstant("Sat 2026-10-10 14:58:02 UTC")).toBe(Date.parse("2026-10-10T14:58:02Z"))
  expect(systemdInstant("Sat 2026-10-10 17:58:02 +0300")).toBe(Date.parse("2026-10-10T14:58:02Z"))
  expect(systemdInstant("Sat 2026-10-10 17:58:02 EEST")).toBeUndefined()
  expect(systemdInstant(undefined)).toBeUndefined()
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
