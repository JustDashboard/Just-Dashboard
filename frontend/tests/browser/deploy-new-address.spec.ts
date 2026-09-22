import { expect, test } from "@playwright/test"
import { gotoStep, json, mockNewProject } from "./deploy-fixture"

test("hostname and HTTPS edits preserve visitor password protection", async ({ page }) => {
  const journey = await mockNewProject(page)
  await page.goto("/deploy/new")
  await page.getByRole("button", { name: "Import Wayy01/wesmokefish" }).click()
  await gotoStep(page, "runtime")
  await page.getByRole("switch", { name: "Ask visitors for a password" }).click()
  await page.getByRole("textbox", { name: "User name" }).fill("reviewer")
  await page.locator("#public-protection-password").fill("private-preview-password")

  await page.getByRole("textbox", { name: "Hostname", exact: true }).fill("preview.example.com")
  const https = page.getByRole("button", { name: "Serve this hostname over HTTPS" })
  await https.click()
  await https.click()
  await expect(page.getByRole("switch", { name: "Ask visitors for a password" })).toBeChecked()
  await expect(page.locator("#public-protection-password")).toHaveValue("private-preview-password")

  await gotoStep(page, "review")
  await page.getByRole("button", { name: "Save only", exact: true }).click()
  await expect.poll(() => journey.commits()).toBe(1)
  expect(journey.configuration()).toMatchObject({
    domains: [
      {
        hostname: "preview.example.com",
        https: true,
        protection: { username: "reviewer", hash: "fixture-sealed-password" },
      },
    ],
  })
})

test("turning off public publishing removes every hostname", async ({ page }) => {
  const journey = await mockNewProject(page)
  await page.goto("/deploy/new")
  await page.getByRole("button", { name: "Import Wayy01/wesmokefish" }).click()
  await gotoStep(page, "runtime")
  await page.getByRole("button", { name: "Add another hostname" }).click()
  await page.getByRole("textbox", { name: "Also answers to 2" }).fill("alias.example.com")

  const publish = page.getByRole("switch", { name: "Publish on a public hostname" })
  await publish.click()
  await expect(publish).not.toBeChecked()
  await expect(page.getByRole("textbox", { name: "Hostname", exact: true })).toBeHidden()
  await gotoStep(page, "review")
  await page.getByRole("button", { name: "Save only", exact: true }).click()
  await expect.poll(() => journey.commits()).toBe(1)
  expect(journey.configuration()).toMatchObject({ domains: [] })
})

test("closing the database sheet ignores its delayed connection response", async ({ page }) => {
  await mockNewProject(page)
  await page.route("**/api/v1/databases/", (route) =>
    json(route, [{ id: 9, name: "orders-db", driver: "postgres", readOnly: false }]),
  )
  let release: (() => void) | undefined
  let requested = false
  await page.route("**/api/v1/databases/9/url**", async (route) => {
    requested = true
    await new Promise<void>((resolve) => {
      release = resolve
    })
    await json(route, { url: "postgres://user:secret@db/app", reference: "${{database.9}}" })
  })
  await page.goto("/deploy/new")
  await page.getByRole("button", { name: "Import Wayy01/wesmokefish" }).click()
  await gotoStep(page, "variables")
  await page.getByRole("button", { name: "Add database", exact: true }).click()
  await page.getByRole("button", { name: "Use existing", exact: true }).click()
  await page.getByRole("combobox", { name: "Existing database" }).click()
  await page.getByRole("option", { name: "orders-db · postgres" }).click()
  await page.getByRole("button", { name: "Connect database", exact: true }).click()
  await expect.poll(() => requested).toBe(true)
  await page.keyboard.press("Escape")
  await expect(page.getByRole("dialog")).toBeHidden()
  release?.()
  await page.getByRole("button", { name: "Add database", exact: true }).click()
  await expect(page.getByRole("dialog")).toBeVisible()
  await expect(page.getByRole("button", { name: "Connect database", exact: true })).toBeEnabled()
  await expect(page.locator('input[value="${{database.9}}"]')).toHaveCount(0)
})

test("standalone database setup resumes its container after switching sources", async ({
  page,
}) => {
  await mockNewProject(page)
  let provisions = 0
  let failAddress = true
  await page.route("**/api/v1/databases/**", (route) => {
    const path = new URL(route.request().url()).pathname
    if (path.endsWith("/provision/options"))
      return json(route, [{ engine: "postgres", label: "PostgreSQL", image: "postgres:17" }])
    if (path.endsWith("/provision")) {
      provisions++
      return json(route, { container: "standalone-db" })
    }
    if (path.endsWith("/adopt"))
      return json(route, { id: 42, name: "standalone-db", driver: "postgres" })
    if (path.endsWith("/ping")) return json(route, { ok: true })
    if (path.endsWith("/url"))
      return failAddress
        ? route.fulfill({
            status: 503,
            contentType: "application/json",
            body: JSON.stringify({ error: { code: "unavailable", message: "Try again" } }),
          })
        : json(route, { url: "postgres://app:secret@localhost/app" })
    return json(route, [])
  })
  await page.goto("/deploy/new?source=database")
  await page.getByText("PostgreSQL", { exact: true }).click()
  await page.getByRole("button", { name: "Create database", exact: true }).click()
  await expect(page.getByText("Database container already created")).toBeVisible()
  await page.getByRole("button", { name: "Git repository", exact: true }).click()
  failAddress = false
  await page.getByRole("button", { name: "Database", exact: true }).click()
  await page.getByRole("button", { name: "Retry connection setup", exact: true }).click()
  await expect(page.getByRole("heading", { name: "standalone-db is ready" })).toBeVisible()
  expect(provisions).toBe(1)
  await page.getByRole("button", { name: "Create another", exact: true }).click()
  await page.getByRole("button", { name: "Git repository", exact: true }).click()
  await page.getByRole("button", { name: "Database", exact: true }).click()
  await expect(page.getByRole("button", { name: "Create database", exact: true })).toBeVisible()
})

test("database engine loading can be retried after an API failure", async ({ page }) => {
  await mockNewProject(page)
  let attempts = 0
  await page.route("**/api/v1/databases/provision/options", (route) => {
    attempts++
    return attempts === 1
      ? route.fulfill({
          status: 503,
          contentType: "application/json",
          body: JSON.stringify({ error: { code: "unavailable", message: "Docker unavailable" } }),
        })
      : json(route, [{ engine: "postgres", label: "PostgreSQL", image: "postgres:17" }])
  })
  await page.goto("/deploy/new?source=database")
  await page.getByRole("button", { name: "Retry loading database engines" }).click()
  await expect(page.getByText("PostgreSQL", { exact: true })).toBeVisible()
  await expect(page.getByText("Docker unavailable", { exact: true })).toBeHidden()
})
