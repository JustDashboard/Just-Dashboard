import { expect, test } from "@playwright/test"
import {
  expectSaved,
  json,
  mockProject,
  run,
  saveSettings,
  toolingScanReading,
  vulnerableScanReading,
} from "./deploy-fixture"

/**
 * The base-image vulnerability watch (`deploy/image_scan.go`): the
 * overview's Vulnerabilities panel, which says only what can be done; its
 * Rebuild — the runs route's own force_build of the live release's commit;
 * the report a link away, with every finding under whose it is, and Scan now;
 * the mark a release's row carries in the deployments list; and the scanning
 * terms in Build settings.
 */

test.use({ timezoneId: "UTC" })

test("the overview says what to do and rebuilds the live commit on a newer base", async ({
  page,
}) => {
  const dashboard = await mockProject(page, { vulnerabilities: vulnerableScanReading() })
  let rebuild: unknown
  await page.route("**/api/v1/deploy/7/environments/12/runs", async (route) => {
    if (route.request().method() !== "POST") return route.fallback()
    rebuild = route.request().postDataJSON()
    return json(route, { ...run, id: 88, operation: "force_build", state: "queued" })
  })
  await page.goto("/deploy/7")
  const panel = page.locator("#vulnerabilities")
  await expect(panel.getByRole("heading", { name: "Vulnerabilities" })).toBeVisible()
  await expect(panel.getByText("2 to fix", { exact: true })).toBeVisible()
  await expect(panel.getByText("2 high or worse to fix in release 2")).toBeVisible()
  // One row per thing to do, and none of the advisories: those are the report's.
  const actions = panel.locator('[data-slot="scan-action"]')
  await expect(actions).toHaveCount(1)
  await expect(actions.getByText("Rebuild on the newer node:22-alpine")).toBeVisible()
  await expect(actions.getByText("Fixes zlib, openssl")).toBeVisible()
  await expect(panel.locator('[data-slot="vulnerable-package"]')).toHaveCount(0)
  await expect(panel.getByRole("img", { name: /^By severity/ })).toHaveCount(0)
  await expect(panel.getByText(/known vulnerabilities/)).toHaveCount(0)
  await expect(panel.getByRole("link", { name: "Report" })).toHaveAttribute(
    "href",
    "/deploy/7/vulnerabilities",
  )

  await panel.getByRole("button", { name: "Rebuild" }).click()
  await expect(page).toHaveURL(/\/deploy\/7\/runs\/88$/)
  expect(rebuild).toEqual({ operation: "force_build", sourceRevision: "a12bc34d56ef7890" })
  expect(dashboard.actions()).not.toContain("scan")
})

test("a release whose findings are all in tools and the newest base is quiet", async ({ page }) => {
  await mockProject(page, { vulnerabilities: toolingScanReading() })
  await page.goto("/deploy/7")
  const panel = page.locator("#vulnerabilities")
  await expect(panel.getByText("Nothing high or worse to fix in release 1")).toBeVisible()
  await expect(
    panel.getByText(
      "183 known: 149 in development tools shipped with it and 34 in the node:22-alpine base image.",
      { exact: false },
    ),
  ).toBeVisible()
  await expect(panel.locator('[data-slot="scan-action"]')).toHaveCount(0)
  await expect(panel.getByRole("button", { name: "Rebuild" })).toHaveCount(0)
  // No verdict in the head when there is nothing to do.
  await expect(panel.getByText("Nothing high or worse to fix", { exact: true })).toHaveCount(0)

  await panel.getByRole("link", { name: "Report" }).click()
  await expect(page).toHaveURL(/\/deploy\/7\/vulnerabilities$/)
  const report = page.locator("#vulnerability-report")
  await expect(report.getByText("183 known vulnerabilities in release 1")).toBeVisible()
  const development = report.getByRole("region", { name: "Development tools shipped with it" })
  await expect(development.getByText(/here through drizzle-kit/)).toBeVisible()
  // esbuild's two copies are two rows, and neither offers a fix that is
  // only esbuild's makers' to ship.
  await expect(development.locator('[data-slot="vulnerable-package"]')).toHaveCount(2)
  await expect(development.getByText(/→/)).toHaveCount(0)
  await expect(development.getByText(/comes with drizzle-kit/).first()).toBeVisible()
  const base = report.getByRole("region", { name: "The base image" })
  await expect(base.getByRole("heading", { name: "The node:22-alpine base image" })).toBeVisible()
  await expect(base.getByText(/is on the newest node:22-alpine/)).toBeVisible()
  await expect(base.getByText("brace-expansion", { exact: true })).toBeVisible()
  await expect(report.getByRole("region", { name: "Your app's packages" })).toHaveCount(0)
  await expect(report.getByRole("button", { name: "Rebuild" })).toHaveCount(0)
})

test("the report lists the base's fixes a rebuild takes, and Scan now asks for one", async ({
  page,
}) => {
  const dashboard = await mockProject(page, { vulnerabilities: vulnerableScanReading() })
  await page.goto("/deploy/7/vulnerabilities")
  const report = page.locator("#vulnerability-report")
  await expect(report.getByText("16 known vulnerabilities in release 2")).toBeVisible()
  await expect(report.getByText(/against a vulnerability database 6 hours old/)).toBeVisible()
  const base = report.getByRole("region", { name: "The base image" })
  await expect(base.getByText(/now names a newer image than release 2 was built on/)).toBeVisible()
  await expect(base.getByText("1.2.13-r0 → 1.2.13-r1")).toBeVisible()
  await expect(base.getByRole("link", { name: "CVE-2022-37434" })).toHaveAttribute(
    "href",
    "https://avd.aquasec.com/nvd/cve-2022-37434",
  )
  // busybox's high has no fix: listed, with nothing to upgrade to.
  await expect(base.getByText("busybox", { exact: true })).toBeVisible()
  await expect(report.getByRole("button", { name: "Rebuild" })).toBeVisible()
  await report.getByRole("button", { name: "Scan now" }).click()
  await expect(report.getByRole("button", { name: "Scanning…" })).toBeDisabled()
  expect(dashboard.actions()).toContain("scan")
})

test("a clean scan offers no rebuild", async ({ page }) => {
  await mockProject(page)
  await page.goto("/deploy/7")
  const panel = page.locator("#vulnerabilities")
  await expect(panel.getByText("Nothing high or worse to fix in release 2")).toBeVisible()
  await expect(panel.getByText(/4 in the node:22-alpine base image/)).toBeVisible()
  await expect(panel.getByRole("button", { name: "Rebuild" })).toHaveCount(0)
  await expect(panel.getByRole("button", { name: "Scan now" })).toHaveCount(0)
})

test("a live release not scanned yet links to the report, where its first scan is asked for", async ({
  page,
}) => {
  const dashboard = await mockProject(page, {
    vulnerabilities: { ...vulnerableScanReading(), scan: null, releases: [] },
  })
  await page.goto("/deploy/7")
  const panel = page.locator("#vulnerabilities")
  await expect(panel.getByText("The live release has not been scanned yet.")).toBeVisible()
  await panel.getByRole("link", { name: "Report" }).click()
  await expect(page).toHaveURL(/\/deploy\/7\/vulnerabilities$/)
  const report = page.locator("#vulnerability-report")
  await report.getByRole("button", { name: "Scan now" }).click()
  await expect.poll(() => dashboard.actions()).toContain("scan")
})

test("scanning off says so and points at its terms", async ({ page }) => {
  await mockProject(page, {
    vulnerabilities: {
      ...vulnerableScanReading(),
      policy: { enabled: false, severityFloor: "HIGH" },
    },
  })
  await page.goto("/deploy/7")
  const panel = page.locator("#vulnerabilities")
  await expect(panel.getByText("Scanning is off for this environment.")).toBeVisible()
  await expect(panel.getByRole("link", { name: /Turn it on in Build settings/ })).toHaveAttribute(
    "href",
    "/deploy/7/settings/build#vulnerabilities",
  )
  await expect(panel.getByRole("button", { name: "Rebuild" })).toHaveCount(0)
})

test("a release's row carries what can be fixed on it, and only that", async ({ page }) => {
  await mockProject(page, { vulnerabilities: vulnerableScanReading() })
  // The run that made release 20, finished, beside one still in flight.
  await page.route("**/api/v1/deploy/7/runs**", (route) => {
    const url = new URL(route.request().url())
    if (url.searchParams.get("view") !== "engine") return route.fallback()
    return json(route, {
      runs: [{ ...run, id: 83, runNumber: 2, state: "succeeded", releaseId: 20 }, run],
      running: true,
    })
  })
  await page.goto("/deploy/7/deployments")
  const mark = page.getByLabel(/^Vulnerabilities: /)
  await expect(mark).toHaveCount(1)
  await expect(mark).toHaveText("2 to fix")
  await expect(mark).toHaveAttribute(
    "title",
    "2 high or worse that an upgrade, or a rebuild on the newer base image, fixes.",
  )
})

test("the scanning terms are saved from Build settings", async ({ page }) => {
  const dashboard = await mockProject(page, { vulnerabilities: vulnerableScanReading() })
  await page.goto("/deploy/7/settings/build")
  const section = page.locator("#vulnerabilities")
  await expect(
    section.getByText("Scanned after each build and daily, counting high or worse"),
  ).toBeVisible()
  await section.getByRole("radio", { name: "Critical" }).click()
  await saveSettings(page)
  await expectSaved(page)
  expect(dashboard.actions()).toContain("vulnerabilities:CRITICAL")
})
