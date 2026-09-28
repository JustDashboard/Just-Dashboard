import { describe, expect, test } from "bun:test"
import {
  failingTone,
  fleetAttention,
  fleetCounts,
  fleetHaystack,
  fleetLive,
  fleetTraffic,
  matchesFilter,
  needsAttention,
  sortFleet,
} from "./fleet"

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

const pulse = (overrides) => ({
  status: "available",
  perMinute: 10,
  errorRate: 0,
  pages: 100,
  points: [1, 2, 3],
  ...overrides,
})

describe("needsAttention", () => {
  test("a project whose health was never observed is not news", () => {
    expect(needsAttention(project({ health: "unavailable" }), {})).toBe(false)
  })

  test("a failed deploy, a failing check and a failing site each are", () => {
    const failed = project({ lastRun: run({ state: "failed", releaseId: undefined }) })
    expect(needsAttention(failed, {})).toBe(true)
    expect(needsAttention(project({ health: "unhealthy" }), {})).toBe(true)
    expect(needsAttention(project(), { 1: pulse({ errorRate: 0.06 }) })).toBe(true)
    expect(needsAttention(project(), { 1: pulse({ errorRate: 0.02 }) })).toBe(false)
  })

  test("a pulse the ingress could not read says nothing", () => {
    expect(needsAttention(project(), { 1: pulse({ status: "unavailable", errorRate: 0.5 }) })).toBe(
      false,
    )
  })
})

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
    expect(counts).toEqual({ all: 3, deploying: 1, failed: 1, attention: 1, pending: 1, pulls: 0 })
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

describe("fleetAttention", () => {
  test("a failed deploy beside a live release is a warning carrying the engine's reason", () => {
    const [finding] = fleetAttention(
      [
        project({
          name: "docs",
          lastRun: run({ id: 77, state: "failed", terminalReason: "bun run build exited 1" }),
        }),
      ],
      {},
    )
    expect(finding.level).toBe("warning")
    expect(finding.title).toStartWith("docs: #3 Deploy failed")
    expect(finding.detail).toBe("bun run build exited 1")
    expect(finding.meta).toBe("deploy failed")
    expect(finding.action).toEqual({ label: "Open run", href: "/deploy/1/runs/77" })
  })

  test("the same failure with nothing live is critical, and sorts first", () => {
    const findings = fleetAttention(
      [
        project({ id: 1, name: "noisy" }),
        project({
          id: 2,
          name: "never",
          liveReleaseId: undefined,
          lastRun: run({ state: "failed" }),
        }),
      ],
      { 1: pulse({ errorRate: 0.2, perMinute: 4 }) },
    )
    expect(findings.map((f) => [f.id, f.level])).toEqual([
      ["project-2", "critical"],
      ["project-1", "warning"],
    ])
    expect(findings[1].title).toBe("noisy is failing 20.0% of requests")
    expect(findings[1].action.href).toBe("/deploy/1/logs")
  })

  test("a live release failing its check opens the runtime", () => {
    const [finding] = fleetAttention([project({ health: "unhealthy" })], {})
    expect(finding).toMatchObject({ id: "project-1", level: "critical" })
    expect(finding.action.href).toBe("/deploy/1/runtime")
  })

  test("a project failing three ways is one finding, titled by the worst", () => {
    const findings = fleetAttention(
      [
        project({
          name: "docs",
          health: "unhealthy",
          lastRun: run({ id: 4, state: "failed", terminalReason: "The check answered 502." }),
        }),
      ],
      { 1: pulse({ errorRate: 0.21, perMinute: 0.4 }) },
    )
    expect(findings).toHaveLength(1)
    const [finding] = findings
    expect(finding.level).toBe("critical")
    expect(finding.title).toBe("docs is failing its health check")
    expect(finding.meta).toBe("unhealthy · deploy failed · traffic")
    expect(finding.detail).toStartWith(
      "The live release's last readiness check did not pass. #3 Deploy failed",
    )
    expect(finding.detail).toContain(": The check answered 502.")
    expect(finding.detail).toEndWith(
      "21.0% of its 0.40 requests a minute failed over the last hour.",
    )
    expect(finding.action.href).toBe("/deploy/1/runtime")
  })

  test("a healthy fleet has nothing to say", () => {
    expect(fleetAttention([project(), project({ id: 2, health: "unavailable" })], {})).toEqual([])
  })
})

describe("fleetLive", () => {
  test("counts what is serving and names it, the commonest product first", () => {
    const live = fleetLive([
      project({ id: 1, framework: "nextjs" }),
      project({ id: 2, sourceKind: "blueprint", sourceRepository: "n8n@1.0.0" }),
      project({ id: 3, framework: "nextjs" }),
      project({ id: 4, stopped: true }),
      project({ id: 5, liveReleaseId: undefined, lastRun: undefined }),
    ])
    expect(live).toEqual({ serving: 3, stopped: 1, notDeployed: 1, products: ["nextjs", "n8n"] })
  })

  test("a live release is serving whatever its newest deploy is doing", () => {
    const live = fleetLive([
      project({ id: 1, lastRun: run({ state: "failed", releaseId: undefined }) }),
      project({ id: 2, activeRun: run({ state: "running" }) }),
      project({ id: 3, health: "unhealthy" }),
      project({ id: 4, liveReleaseId: undefined, lastRun: run({ state: "failed" }) }),
    ])
    expect(live.serving).toBe(3)
  })
})

describe("failingTone", () => {
  test("one colour for one share, wherever it is drawn", () => {
    expect(failingTone(0.009)).toBe("default")
    expect(failingTone(0.01)).toBe("warning")
    expect(failingTone(0.049)).toBe("warning")
    expect(failingTone(0.05)).toBe("danger")
  })
})

describe("fleetTraffic", () => {
  test("adds the sites up bucket by bucket and weighs the failing share by traffic", () => {
    const traffic = fleetTraffic(
      [project({ id: 1, name: "busy" }), project({ id: 2, name: "flaky" }), project({ id: 3 })],
      {
        1: pulse({ perMinute: 90, errorRate: 0, pages: 10, points: [1, 1, 1] }),
        2: pulse({ perMinute: 10, errorRate: 0.5, pages: 5, points: [2, 2] }),
        3: pulse({ status: "unavailable" }),
      },
    )
    expect(traffic).toMatchObject({
      sites: 2,
      perMinute: 100,
      pages: 15,
      points: [1, 3, 3],
      failingSites: 1,
    })
    expect(traffic.share).toBeCloseTo(0.05)
    expect(traffic.worst).toEqual({ name: "flaky", errorRate: 0.5 })
  })

  test("nothing routed is nothing to read", () => {
    expect(fleetTraffic([project()], {})).toBeUndefined()
    expect(fleetTraffic([project()], undefined)).toBeUndefined()
  })
})
