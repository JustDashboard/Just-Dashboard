import { expect, test } from "@playwright/test"
import { json, mockHost, user } from "./host-fixture"
import { risen } from "./risen"

/**
 * An address no page claims, inside the shell: a picture of the address that
 * was asked for and the pages that are there, and the nearest of them as the
 * way on. `app-states.spec.ts` holds that the shell and the heading are kept.
 */

test.beforeEach(async ({ page }) => {
  await mockHost(page)
})

test("an address under a section offers that section's page before the Overview", async ({
  page,
}) => {
  await page.goto("/metrics/no-such-view")
  const state = page.locator("[data-slot=empty-state]")
  await expect(state).toContainText("Page not found")
  // The address asked for is said whole, in the sentence.
  await expect(state.getByText("/metrics/no-such-view", { exact: true })).toBeVisible()
  const picture = state.locator("[data-slot=wire-flow]")
  await expect(picture).toContainText("/no-such-view")
  await expect(picture).toContainText("no page here")
  await expect(picture).toContainText("This server")
  await expect(picture.getByRole("link", { name: "Open Metrics" })).toHaveAttribute(
    "href",
    "/metrics",
  )
  await expect(picture.getByRole("link", { name: "Open the Overview" })).toHaveAttribute(
    "href",
    "/",
  )
  // One brand-faced command, the nearest page; the Overview is the second way.
  const nearest = state.getByRole("link", { name: "Open Metrics", exact: true }).last()
  await expect(nearest).toHaveAttribute("data-variant", "default")
  await expect(state.getByRole("link", { name: "Back to the Overview" })).toHaveAttribute(
    "data-variant",
    "outline",
  )
  await nearest.click()
  await expect(page).toHaveURL(/\/metrics$/)
})

test("an address under no page offers the Overview alone", async ({ page }) => {
  await page.goto("/this-page-does-not-exist")
  const state = page.locator("[data-slot=empty-state]")
  // The node names the address's last part, cut where it runs long; the
  // sentence under the picture says it whole.
  await expect(state.locator("[data-slot=wire-flow]")).toContainText("/this-page-does-not-exi…")
  await expect(state.getByText("/this-page-does-not-exist", { exact: true })).toBeVisible()
  await expect(state.getByRole("link", { name: /^Open / })).toHaveCount(1)
  await expect(state.getByRole("link", { name: "Back to the Overview" })).toHaveAttribute(
    "data-variant",
    "default",
  )
  // No dashed box round a question mark any more.
  await expect(state).not.toHaveClass(/border-dashed/)
})

test("a page the role does not reach is not offered", async ({ page }) => {
  await page.route("**/api/v1/auth/session", (route) =>
    json(route, { ...user, capabilities: ["read"], user: { ...user.user, role: "viewer" } }),
  )
  await page.goto("/metrics/alerts/nowhere")
  const state = page.locator("[data-slot=empty-state]")
  // Alerts is an administrator's: a reader is offered Metrics above it.
  await expect(state.getByRole("link", { name: "Open Metrics", exact: true }).last()).toBeVisible()
  await expect(state.getByRole("link", { name: "Open Alerts" })).toHaveCount(0)
})

test("on a phone the picture is its marks and nothing is cut off", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto("/docker/images/a/very-long-address-that-names-nothing-at-all-on-this-server")
  const state = page.locator("[data-slot=empty-state]")
  await expect(state).toContainText("Page not found")
  await risen(page)
  const picture = state.locator("[data-slot=wire-flow]")
  await expect(picture.getByRole("link", { name: "Open Images" })).toBeVisible()
  await expect(state.getByRole("link", { name: "Open Images", exact: true }).last()).toBeVisible()
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth > document.documentElement.clientWidth,
    ),
  ).toBe(false)
  const box = (await state.boundingBox())!
  expect(box.x).toBeGreaterThanOrEqual(0)
  expect(box.x + box.width).toBeLessThanOrEqual(390)
})
