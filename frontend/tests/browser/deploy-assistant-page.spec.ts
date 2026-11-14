import { expect, test } from "@playwright/test"
import { json, mockProject, user } from "./deploy-fixture"

/**
 * A project's assistant when its settings cannot be read: the assistant's own
 * off state with the read to try again, never the transport's error as the
 * page's headline.
 */

const off = { configured: false, keySet: false, models: [], presets: [], profiles: [] }

for (const width of [1280, 390]) {
  test(`a failed read is the off state with a retry, at ${width}`, async ({ page }) => {
    await page.setViewportSize({ width, height: 844 })
    await mockProject(page)
    let failing = true
    let reads = 0
    await page.route("**/api/v1/assistant", (route) => {
      reads += 1
      return failing
        ? route.fulfill({
            status: 502,
            contentType: "application/json",
            body: JSON.stringify({ error: { code: "bad_gateway", message: "upstream hung up" } }),
          })
        : json(route, off)
    })
    await page.goto("/deploy/7/assistant")

    const state = page.locator('[data-slot="project-assistant"] [data-slot="empty-state"]')
    await expect(state.getByText("The assistant did not answer", { exact: true })).toBeVisible()
    await expect(
      state.getByText("Its settings could not be read just now. Nothing was changed."),
    ).toBeVisible()
    // Who could answer it, drawn as on the off state, and one button.
    await expect(state.locator('img[src="/logos/claude.svg"]')).toBeVisible()
    await expect(state.getByRole("button")).toHaveCount(1)
    // The transport's words are nobody's headline.
    await expect(page.getByText("upstream hung up")).toHaveCount(0)
    await expect(page.getByText("The assistant's settings could not be read")).toHaveCount(0)
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth),
    ).toBe(true)

    // Trying again asks again, and what comes back is drawn in its place.
    const before = reads
    failing = false
    await state.getByRole("button", { name: "Try again", exact: true }).click()
    await expect(page.getByText("The assistant is off", { exact: true })).toBeVisible()
    expect(reads).toBeGreaterThan(before)
    await expect(page.getByRole("button", { name: "Set up the assistant" })).toBeVisible()
  })
}

test("a retry that fails again hands the button back", async ({ page }) => {
  await mockProject(page)
  let reads = 0
  await page.route("**/api/v1/assistant", (route) => {
    reads += 1
    return route.fulfill({
      status: 502,
      contentType: "application/json",
      body: JSON.stringify({ error: { code: "bad_gateway", message: "upstream hung up" } }),
    })
  })
  await page.goto("/deploy/7/assistant")
  const retry = page.getByRole("button", { name: "Try again", exact: true })
  await expect(retry).toBeVisible()
  const before = reads
  await retry.click()
  await expect.poll(() => reads).toBeGreaterThan(before)
  await expect(retry).toBeEnabled()
  await expect(page.getByText("The assistant did not answer", { exact: true })).toBeVisible()
})

test("an account that cannot ask is told so, with nothing to press", async ({ page }) => {
  await mockProject(page)
  await page.route("**/api/v1/auth/session", (route) =>
    json(route, { ...user, capabilities: ["read"], user: { ...user.user, role: "viewer" } }),
  )
  await page.route("**/api/v1/assistant", (route) =>
    route.fulfill({ status: 502, contentType: "application/json", body: "{}" }),
  )
  await page.goto("/deploy/7/assistant")
  await expect(page.getByText("Your role does not ask the assistant")).toBeVisible()
  await expect(page.getByRole("button", { name: "Try again" })).toHaveCount(0)
  await expect(page.getByRole("button", { name: "Set up the assistant" })).toHaveCount(0)
})
