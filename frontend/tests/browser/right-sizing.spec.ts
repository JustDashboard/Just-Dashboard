import { expect, test, type Page } from "@playwright/test"
import type { EnvironmentSizing, FleetSizing } from "../../src/components/deploy/sizing-model"
import type { CapacityForecast } from "../../src/lib/capacity"
import { json, mockProject } from "./deploy-fixture"
import { mockHost } from "./host-fixture"

/**
 * Right-sizing and the capacity forecast, against mocked reads: the project
 * overview's Right-size card and its one verb, which saves the suggested
 * limits through the configuration PUT the Runtime settings make; the fleet's
 * list of what is off, with the same verb per row; and the Metrics page's
 * Capacity forecast. The maths is the server's (metrics/sizing.go, tested in
 * Go); these check what the page says and what the verb writes.
 */

const GIB = 2 ** 30

function sizing(overrides: Partial<EnvironmentSizing> = {}): EnvironmentSizing {
  return {
    projectId: 7,
    projectName: "api-production",
    environmentId: 12,
    environmentName: "production",
    environmentKind: "production",
    status: "available",
    verdict: "too_tight",
    offBy: 1.25,
    window: {
      from: "2026-09-01T12:00:00Z",
      to: "2026-09-03T12:00:00Z",
      samples: 11520,
      containers: 2,
      intervalSeconds: 15,
      coveredSeconds: 172800,
      neededSeconds: 86400,
    },
    memory: {
      verdict: "too_tight",
      limitMb: 512,
      liveLimitMb: 512,
      p95Mb: 491,
      peakMb: 500,
      suggestedMb: 640,
      oomKills: 0,
      offBy: 1.25,
      reasons: ["It used 491 MiB or more 5% of the time, 96% of the limit."],
    },
    cpu: {
      verdict: "oversized",
      limit: 4,
      liveLimit: 4,
      p95: 0.21,
      peak: 0.6,
      suggested: 0.35,
      throttledShare: null,
      offBy: 11.43,
      reasons: [
        "The limit is 11.4× what it needs: it was busy at 0.21 cores or less 95% of the time.",
      ],
    },
    apply: { memoryMb: 640, cpus: 0.35 },
    revision: 3,
    pending: false,
    oom: { watching: true, since: "2026-09-03T09:00:00Z", kills: 0 },
    ...overrides,
  }
}

/** The same environment once the suggestion is saved and not yet released. */
function applied(): EnvironmentSizing {
  const before = sizing()
  return sizing({
    verdict: "right",
    offBy: 1,
    revision: 4,
    pending: true,
    apply: null,
    memory: {
      ...before.memory!,
      verdict: "right",
      limitMb: 640,
      offBy: 1,
      reasons: ["The limit fits: its peak was 78% of it."],
    },
    cpu: {
      ...before.cpu!,
      verdict: "right",
      limit: 0.35,
      offBy: 1,
      reasons: ["The limit fits."],
    },
  })
}

/** The project's sizing read, answered from what the configuration now holds. */
async function routeSizing(page: Page, fixture: Awaited<ReturnType<typeof mockProject>>) {
  await page.route("**/api/v1/deploy/7/environments/12/sizing", (route) =>
    json(route, fixture.configuration().runtime.memoryMb === 640 ? applied() : sizing()),
  )
}

test.describe("right-sizing", () => {
  test("the overview card suggests limits and applies them through the configuration", async ({
    page,
  }) => {
    const fixture = await mockProject(page)
    await routeSizing(page, fixture)
    await page.goto("/deploy/7")
    const card = page.locator("section", { has: page.locator("#right-size-title") }).last()
    await expect(card.getByText("Too tight", { exact: true })).toBeVisible()
    await expect(card.getByText("640 MiB", { exact: true })).toBeVisible()
    await expect(card.getByText("0.35 cores", { exact: true })).toBeVisible()
    await expect(card.getByText("limit 512 MiB · p95 491 MiB · peak 500 MiB")).toBeVisible()
    await expect(card.getByText("limit 4 cores · p95 0.21 cores · peak 0.6 cores")).toBeVisible()
    await expect(card.getByText(/96% of the limit/)).toBeVisible()
    await expect(card.getByText(/From 11,520 samples over 2 days/)).toBeVisible()
    await expect(
      card.getByRole("button", { name: "How the memory suggestion is worked out" }),
    ).toBeVisible()
    await page.setViewportSize({ width: 1280, height: 900 })
    await card.scrollIntoViewIfNeeded()
    await card.screenshot({ path: "test-results/right-size-card-1280.png" })
    await page.setViewportSize({ width: 390, height: 844 })
    await card.scrollIntoViewIfNeeded()
    await card.screenshot({ path: "test-results/right-size-card-390.png" })
    await page.setViewportSize({ width: 1280, height: 900 })

    await card.getByRole("button", { name: "Apply suggested limits" }).click()
    await expect(page.getByText("Saved the suggested limits for api-production")).toBeVisible()
    // Only the two limits changed; the rest of the runtime is what it was.
    const runtime = fixture.configuration().runtime
    expect(runtime.memoryMb).toBe(640)
    expect(runtime.cpus).toBe(0.35)
    expect(runtime.command).toEqual(["bun", "start"])
    expect(runtime.internalPort).toBe(3000)
    await expect(card.getByText("Sized right", { exact: true })).toBeVisible()
    await expect(card.getByText(/The saved limits apply with the next release/)).toBeVisible()
    await expect(card.getByRole("button", { name: "Apply suggested limits" })).toHaveCount(0)
  })

  test("too little history says how much more it needs and offers nothing", async ({ page }) => {
    await mockProject(page)
    await page.route("**/api/v1/deploy/7/environments/12/sizing", (route) =>
      json(
        route,
        sizing({
          verdict: "insufficient_history",
          memory: null,
          cpu: null,
          apply: null,
          window: { ...sizing().window, samples: 1440, coveredSeconds: 6 * 3600 },
        }),
      ),
    )
    await page.goto("/deploy/7")
    const card = page.locator("section", { has: page.locator("#right-size-title") }).last()
    await expect(
      card.getByText("Not enough history yet: 6 h of the 24 h of history a suggestion needs."),
    ).toBeVisible()
    await expect(card.getByRole("button", { name: "Apply suggested limits" })).toHaveCount(0)
  })

  test("the fleet lists what is off, furthest first, with the verb per row", async ({ page }) => {
    const fixture = await mockProject(page)
    const fleet: FleetSizing = {
      status: "available",
      retentionSeconds: 604800,
      environments: [
        sizing(),
        sizing({
          projectId: 9,
          projectName: "blog",
          environmentId: 31,
          environmentName: "staging",
          environmentKind: "staging",
          verdict: "oversized",
          offBy: 8,
          memory: { ...sizing().memory!, verdict: "oversized", limitMb: 4096, suggestedMb: 512 },
          cpu: { ...sizing().cpu!, verdict: "right" },
          apply: { memoryMb: 512 },
        }),
        sizing({ projectId: 11, projectName: "docs", environmentId: 40, verdict: "right" }),
        sizing({
          projectId: 12,
          projectName: "wiki",
          environmentId: 41,
          verdict: "insufficient_history",
        }),
      ],
    }
    await page.route("**/api/v1/deploy/sizing", (route) => json(route, fleet))
    await page.goto("/deploy")
    const panel = page.locator("section", { has: page.locator("#fleet-right-size-title") }).last()
    await expect(
      panel.getByText("2 to resize · 1 sized right · 1 still gathering history"),
    ).toBeVisible()
    const rows = panel.getByRole("list", { name: "Environments to resize" }).getByRole("listitem")
    await expect(rows).toHaveCount(2)
    await expect(rows.first()).toContainText("api-production")
    await expect(rows.first()).toContainText("memory 512 MiB → 640 MiB · CPU 4 cores → 0.35 cores")
    await expect(rows.nth(1)).toContainText("blog")
    await expect(rows.nth(1)).toContainText("staging")
    await expect(rows.nth(1)).toContainText("memory 4 GiB → 512 MiB")
    await expect(rows.nth(1).getByText("Oversized", { exact: true })).toBeVisible()
    await panel.scrollIntoViewIfNeeded()
    await panel.screenshot({ path: "test-results/right-size-fleet-1280.png" })

    await panel
      .getByRole("button", { name: "Apply suggested limits to api-production production" })
      .click()
    await expect(page.getByText("Saved the suggested limits for api-production")).toBeVisible()
    expect(fixture.configuration().runtime.memoryMb).toBe(640)
    expect(fixture.configuration().runtime.cpus).toBe(0.35)
  })
})

function forecast(): CapacityForecast {
  return {
    status: "available",
    retentionSeconds: 604800,
    volumeIntervalSeconds: 900,
    mounts: [
      {
        mountpoint: "/",
        usedBytes: 48 * GIB,
        totalBytes: 100 * GIB,
        usedPercent: 48,
        status: "filling",
        growthBytesPerDay: 2 * GIB,
        daysToFull: 26,
        fullAt: "2026-09-29T12:00:00Z",
        r2: 0.99,
        confidence: "high",
        from: "2026-08-30T12:00:00Z",
        to: "2026-09-03T12:00:00Z",
        points: 96,
      },
      {
        mountpoint: "/srv",
        usedBytes: 10 * GIB,
        totalBytes: 500 * GIB,
        usedPercent: 2,
        status: "steady",
        growthBytesPerDay: 0,
        daysToFull: null,
        fullAt: null,
        r2: 1,
        confidence: "high",
        from: "2026-08-30T12:00:00Z",
        to: "2026-09-03T12:00:00Z",
        points: 96,
      },
    ],
    apps: [
      {
        projectId: 7,
        projectName: "api-production",
        environmentId: 12,
        environmentName: "production",
        sizeBytes: 5 * GIB,
        growthBytesPerDay: GIB,
        status: "filling",
        volumes: [
          {
            name: "api-data",
            sizeBytes: 5 * GIB,
            status: "filling",
            growthBytesPerDay: GIB,
            r2: 0.97,
            confidence: "high",
            from: "2026-08-30T12:00:00Z",
            to: "2026-09-03T12:00:00Z",
            points: 96,
            measured: true,
          },
        ],
      },
      {
        projectId: 9,
        projectName: "blog",
        environmentId: 31,
        environmentName: "production",
        sizeBytes: 120 * 2 ** 20,
        growthBytesPerDay: null,
        status: "insufficient_history",
        volumes: [
          {
            name: "blog-uploads",
            sizeBytes: 120 * 2 ** 20,
            status: "insufficient_history",
            growthBytesPerDay: null,
            r2: null,
            from: null,
            to: null,
            points: 0,
            measured: true,
          },
        ],
      },
    ],
  }
}

test("the metrics page forecasts each disk and ranks the growing apps", async ({ page }) => {
  await mockHost(page)
  await page.route("**/api/v1/system/metrics/capacity", (route) => json(route, forecast()))
  await page.goto("/metrics")
  const section = page
    .locator("section", { has: page.getByRole("heading", { name: "Capacity forecast" }) })
    .last()
  // The page draws its charts first; the forecast's read lands after them.
  await expect(section.getByRole("heading", { name: "Days until full" })).toBeVisible({
    timeout: 20_000,
  })
  const disks = section.getByRole("list", { name: "Filesystem forecasts" }).getByRole("listitem")
  await expect(disks).toHaveCount(2)
  await expect(disks.first()).toContainText("/")
  await expect(disks.first().getByText("Full in 4 weeks")).toBeVisible()
  await expect(disks.first()).toContainText(
    "48% of 100.0 GB · +2.0 GB a day · high confidence (R² 0.99)",
  )
  await expect(disks.first()).toContainText("fitted to 96 hourly means over 4 days")
  await expect(disks.nth(1).getByText("Not filling")).toBeVisible()
  const apps = section.getByRole("list", { name: "Fastest-growing apps" }).getByRole("listitem")
  await expect(apps).toHaveCount(2)
  await expect(apps.first()).toContainText("api-production")
  await expect(apps.first()).toContainText("5.0 GB in api-data")
  await expect(apps.first()).toContainText("+1.0 GB a day")
  await expect(apps.nth(1)).toContainText("measuring")
  await expect(
    section.getByRole("button", { name: "How the forecast is worked out" }),
  ).toBeVisible()
  // The five readings at the top stay the only tiles on the page.
  await expect(page.locator("[data-slot=stat-tile]")).toHaveCount(5)
  await page.setViewportSize({ width: 1280, height: 900 })
  await section.scrollIntoViewIfNeeded()
  await section.screenshot({ path: "test-results/capacity-forecast-1280.png" })
  await page.setViewportSize({ width: 390, height: 844 })
  await section.scrollIntoViewIfNeeded()
  await section.screenshot({ path: "test-results/capacity-forecast-390.png" })
})
