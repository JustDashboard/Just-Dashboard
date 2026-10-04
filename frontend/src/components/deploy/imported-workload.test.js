import { describe, expect, test } from "bun:test"
import { importedWorkloadState, isObservedImport } from "./imported-workload"
import { projectState } from "./vocabulary"
import { fleetRank } from "./fleet"

const summary = (workload) => ({
  sourceKind: "import",
  liveReleaseId: 0,
  importedWorkload: workload,
})

describe("existing workloads without dashboard releases", () => {
  test("a two-of-four Compose stack reads as partly running", () => {
    const project = summary({ state: "partial", running: 2, total: 4 })
    expect(importedWorkloadState(project)).toBe("partial")
    expect(projectState(project)).toBe("partial")
    expect(fleetRank(project)).toBeLessThan(
      fleetRank(summary({ state: "running", running: 1, total: 1 })),
    )
  })

  test("a running imported app is observed without inventing a release or a health check", () => {
    expect(projectState(summary({ state: "running", running: 1, total: 1 }))).toBe("observed")
    expect(projectState(summary({ state: "stopped", running: 0, total: 1 }))).toBe("stopped")
  })

  test("an unavailable manager is never presented as a stopped workload", () => {
    expect(projectState(summary({ state: "unavailable", running: 0, total: 4 }))).toBe(
      "unavailable",
    )
    expect(projectState(summary({ state: "missing", running: 0, total: 4 }))).toBe("unavailable")
    expect(projectState(summary({ state: "running", running: 1, total: 1 }), undefined, true)).toBe(
      "archived",
    )
  })

  test("legacy checkout imports retain the managed deployment state", () => {
    expect(projectState({ ...summary(undefined), importMode: "existing_checkout" })).toBe(
      "not_deployed",
    )
  })

  test("an adopted baseline keeps normal project state even with retained origin metadata", () => {
    const baseline = { ...summary({ state: "partial", running: 2, total: 4 }), liveReleaseId: 9 }
    expect(isObservedImport(baseline)).toBe(false)
    expect(projectState(baseline)).toBe("ready")
    expect(isObservedImport({ sourceKind: "image", liveReleaseId: 9 })).toBe(false)
    expect(isObservedImport({ sourceKind: "import", liveReleaseId: 0 })).toBe(false)
  })
})
