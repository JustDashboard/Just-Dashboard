import { expect, test } from "@playwright/test"
import {
  deployment,
  healthyOperations,
  json,
  mockProject,
  now,
  project,
  run,
} from "./deploy-fixture"

test("the project header is one compact row that keeps its readings apart across screen sizes", async ({
  page,
}, testInfo) => {
  await page.emulateMedia({ reducedMotion: "reduce" })
  await mockProject(page, { operations: healthyOperations })
  const completed = {
    ...run,
    state: "succeeded",
    releaseId: 20,
    endedAt: now,
    metadata: {
      commit: {
        sha: deployment.sourceRevision,
        author: "Header Author",
        subject: "Keep the detailed commit message in the deployment history",
      },
    },
  }
  await page.route("**/api/v1/deploy/7", (route) =>
    json(route, {
      project,
      running: false,
      deployment: { ...deployment, activeRun: undefined, lastRun: completed },
    }),
  )
  await page.route("**/api/v1/deploy/7/runs**", (route) => {
    if (new URL(route.request().url()).searchParams.get("view") !== "engine")
      return route.fallback()
    return json(route, { runs: [completed], running: false })
  })

  for (const width of [390, 768, 1280, 1720]) {
    await page.setViewportSize({ width, height: 900 })
    await page.goto("/deploy/7")
    const identity = page.getByRole("region", { name: "About api-production" })
    await expect(identity.getByText("acme/api", { exact: true })).toBeVisible()
    await expect(identity.getByText("Release #2", { exact: true })).toBeVisible()
    await expect(identity.getByText("Ready", { exact: true })).toBeVisible()
    await expect(identity.getByText("All checks passed", { exact: true })).toBeVisible()
    await expect(
      identity.getByRole("link", { name: "Automatic deployment settings" }),
    ).toHaveAttribute("href", "/deploy/7/settings/general#automatic-deployment")
    // The rail leads back and the address leads to the site: neither is repeated here.
    await expect(page.locator("[data-slot=page-context]")).toHaveCount(0)
    await expect(page.getByRole("link", { name: "Visit", exact: true })).toHaveCount(0)
    const [state, assessment] = await Promise.all(
      ["Ready", "All checks passed"].map((label) =>
        identity.getByText(label, { exact: true }).boundingBox(),
      ),
    )
    // The state and the assessment share a line now; they collided when they first did.
    expect(
      assessment!.y >= state!.y + state!.height || assessment!.x >= state!.x + state!.width,
    ).toBe(true)
    // It repeats above every project page, so on a desktop it is the height of
    // its tile and command rather than a block the page's content waits under.
    if (width >= 1280) expect((await identity.boundingBox())!.height).toBeLessThanOrEqual(64)
    await expect(identity.getByText(completed.metadata.commit.subject)).toHaveCount(0)
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth),
    ).toBe(true)
    await identity.screenshot({ path: testInfo.outputPath(`header-${width}.png`) })
  }

  await page.goto("/deploy/7/deployments")
  await expect(page.getByText(completed.metadata.commit.subject, { exact: true })).toBeVisible()
  await expect(page.getByText(completed.metadata.commit.author, { exact: true })).toBeVisible()

  // User-controlled repository and branch names must not widen the shared header.
  const longName = "a-very-long-repository-name-".repeat(8)
  await page.route("**/api/v1/deploy/7", (route) =>
    json(route, {
      project,
      running: false,
      deployment: {
        ...deployment,
        activeRun: undefined,
        sourceRepository: `acme/${longName}`,
        sourceRef: `feature/${longName}`,
      },
    }),
  )
  for (const width of [390, 1280]) {
    await page.setViewportSize({ width, height: 900 })
    await page.goto("/deploy/7/runtime")
    await expect(page.getByRole("region", { name: "About api-production" })).toBeVisible()
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth),
    ).toBe(true)
  }
})
