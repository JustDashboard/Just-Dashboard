import { expect, test } from "@playwright/test"
import {
  expectSaved,
  json,
  mockProject,
  run,
  saveSettings,
  vulnerableScanReading,
} from "./deploy-fixture"

/**
 * The base-image vulnerability watch (`deploy/image_scan.go`): the
 * overview's Vulnerabilities panel, its Rebuild — the runs route's own
 * force_build of the live release's commit — and Scan now, the mark a
 * release's row carries in the deployments list, and the scanning terms in
 * Build settings.
 */

test.use({ timezoneId: "UTC" })

test("the overview says what the scan found and rebuilds the live commit on a newer base", async ({
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
  await expect(panel.getByText("2 fixable by a newer base")).toBeVisible()
  // One figure per severity, each with how many have a fix.
  await expect(panel.getByText("Critical", { exact: true })).toBeVisible()
  await expect(panel.getByText("1 with a fix").first()).toBeVisible()
  await expect(panel.getByText("A rebuild is likely to fix some of these")).toBeVisible()
  await expect(panel.getByText("node:22-alpine", { exact: true })).toBeVisible()
  await expect(panel.getByRole("link", { name: "CVE-2022-37434" })).toHaveAttribute(
    "href",
    "https://avd.aquasec.com/nvd/cve-2022-37434",
  )
  await expect(panel.getByText("1.2.13-r0 → 1.2.13-r1")).toBeVisible()
  await expect(panel.getByText(/against a vulnerability database 6 hours old/)).toBeVisible()

  await panel.getByRole("button", { name: "Rebuild" }).click()
  await expect(page).toHaveURL(/\/deploy\/7\/runs\/88$/)
  expect(rebuild).toEqual({ operation: "force_build", sourceRevision: "a12bc34d56ef7890" })
  expect(dashboard.actions()).not.toContain("scan")
})

test("a clean scan offers no rebuild, and Scan now asks for one", async ({ page }) => {
  const dashboard = await mockProject(page)
  await page.goto("/deploy/7")
  const panel = page.locator("#vulnerabilities")
  await expect(panel.getByText("No high or worse vulnerabilities")).toBeVisible()
  await expect(panel.getByText("Nothing high or worse was found.")).toBeVisible()
  await expect(panel.getByRole("button", { name: "Rebuild" })).toHaveCount(0)
  await panel.getByRole("button", { name: "Scan now" }).click()
  await expect(panel.getByRole("button", { name: "Scanning…" })).toBeDisabled()
  expect(dashboard.actions()).toContain("scan")
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
  await expect(panel.getByRole("button", { name: "Scan now" })).toHaveCount(0)
})

test("a release's row carries what its scan found", async ({ page }) => {
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
  await expect(mark).toHaveText("2 fixable")
  await expect(mark).toHaveAttribute(
    "title",
    /1 critical and 2 high at high or worse, 2 with a published fix; a newer base image is out/,
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
