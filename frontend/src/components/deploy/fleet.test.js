import { describe, expect, test } from "bun:test"
import { failingTone, fleetCounts, fleetHaystack, matchesFilter, sortFleet } from "./fleet"

const run = (overrides) => ({
  id: 50,
  runNumber: 3,
  operation: "deploy",
  trigger: "manual",
  actor: "operator",
  state: "succeeded",
  requestedAt: "2026-09-01T10:00:00Z",
  endedAt: "2026-09-01T10:02:00Z",
  metadata: {},
  ...overrides,
})

const project = (overrides) => ({
  id: 1,
  name: "app",
  profile: "web",
  sourceKind: "git",
  buildMethod: "recipe",
  liveReleaseId: 10,
  health: "healthy",
  pendingChanges: false,
  lastRun: run({ releaseId: 10 }),
  ...overrides,
})

describe("needsAttention", () => {})

describe("fleetCounts", () => {
  test("counts what each chip would leave", () => {
    const counts = fleetCounts(
      [
        project({ id: 1, activeRun: run({ state: "running" }) }),
        project({ id: 2, lastRun: run({ state: "failed", releaseId: 4 }) }),
        project({ id: 3, pendingChanges: true }),
      ],
      {},
    )
    expect(counts).toEqual({ all: 3, deploying: 1, failed: 1, pending: 1, pulls: 0 })
  })

  test("the pull-request chip counts the projects with one open, from the fleet's own read", () => {
    const fleet = [project({ id: 1 }), project({ id: 2 }), project({ id: 3 })]
    const pulls = { 1: { open: 2, previews: 1 }, 2: { open: 0, previews: 1 } }
    expect(fleetCounts(fleet, {}, pulls).pulls).toBe(1)
    expect(matchesFilter(fleet[0], "pulls", {}, pulls)).toBe(true)
    // A preview whose pull request has closed is not a pull request to look at.
    expect(matchesFilter(fleet[1], "pulls", {}, pulls)).toBe(false)
    expect(matchesFilter(fleet[2], "pulls", {}, pulls)).toBe(false)
  })

  test("before the pull-request read lands, nothing counts as having one", () => {
    expect(fleetCounts([project()], {}).pulls).toBe(0)
    expect(matchesFilter(project(), "pulls", {})).toBe(false)
  })
})

describe("sortFleet", () => {
  test("broken first, then moving, then waiting, then the rest, and by name within each", () => {
    const order = sortFleet([
      project({ id: 1, name: "b-ready" }),
      project({ id: 2, name: "stopped", stopped: true }),
      project({ id: 3, name: "never", liveReleaseId: undefined, lastRun: undefined }),
      project({ id: 4, name: "a-ready" }),
      project({ id: 5, name: "pending", pendingChanges: true }),
      project({ id: 6, name: "moving", activeRun: run({ state: "running" }) }),
      project({ id: 7, name: "sick", health: "failed" }),
      project({ id: 8, name: "broke", lastRun: run({ state: "failed", releaseId: 2 }) }),
    ]).map((d) => d.name)
    expect(order).toEqual([
      "broke",
      "sick",
      "moving",
      "pending",
      "a-ready",
      "b-ready",
      "stopped",
      "never",
    ])
  })
})

describe("fleetHaystack", () => {
  test("finds a project by its product, its framework, its images and its last commit", () => {
    const haystack = fleetHaystack(
      project({
        framework: "nextjs",
        images: ["postgres:16", "acme/api:2"],
        lastRun: run({ metadata: { commit: { sha: "abc1234", subject: "Fix checkout" } } }),
      }),
    )
    expect(haystack).toContain("next.js")
    expect(haystack).toContain("nextjs")
    expect(haystack).toContain("postgres")
    expect(haystack).toContain("fix checkout")
  })

  test("a template is found by its product", () => {
    expect(
      fleetHaystack(project({ sourceKind: "blueprint", sourceRepository: "n8n@1.2.3" })),
    ).toContain("n8n")
  })
})

describe("fleetAttention", () => {})

describe("failingTone", () => {
  test("one colour for one share, wherever it is drawn", () => {
    expect(failingTone(0.009)).toBe("default")
    expect(failingTone(0.01)).toBe("warning")
    expect(failingTone(0.049)).toBe("warning")
    expect(failingTone(0.05)).toBe("danger")
  })
})
