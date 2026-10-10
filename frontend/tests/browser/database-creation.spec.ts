import { expect, test, type Page } from "@playwright/test"
import catalogue from "../../../backend/internal/api/testdata/database-drivers.json"
import { dockerInventory, mockFleet, TEMPLATES } from "./database-fleet-fixture"
import { json, mockNewProject } from "./deploy-fixture"

const CREATION_PAGES = ["/databases/new", "/deploy/new?source=database"]

async function setup(page: Page, path: string) {
  if (path.startsWith("/deploy")) {
    await mockNewProject(page)
    await page.route("**/api/v1/databases/drivers", (route) => json(route, catalogue))
    await page.route("**/api/v1/databases/inventory", (route) => json(route, dockerInventory()))
    await page.route("**/api/v1/databases/provision/options", (route) => json(route, TEMPLATES))
  } else {
    await mockFleet(page)
  }
}

for (const path of CREATION_PAGES) {
  test(`${path} offers the full settings panel and follows real startup stages`, async ({
    page,
  }) => {
    await setup(page, path)
    let finishProvision!: () => void
    let finishAdopt!: () => void
    let finishPing!: () => void
    const provision = new Promise<void>((resolve) => (finishProvision = resolve))
    const adopt = new Promise<void>((resolve) => (finishAdopt = resolve))
    const ping = new Promise<void>((resolve) => (finishPing = resolve))
    const requests: unknown[] = []
    let pings = 0
    const connection = { id: 20, name: "orders-db", driver: "postgres", database: "orders" }
    await page.route("**/api/v1/databases/provision", async (route) => {
      requests.push(route.request().postDataJSON())
      await provision
      await json(route, { container: "orders-db" })
    })
    await page.route("**/api/v1/databases/adopt", async (route) => {
      await adopt
      await json(route, connection)
    })
    await page.route("**/api/v1/databases/20/ping", async (route) => {
      pings++
      await ping
      // First-boot servers can accept credentials and then restart.
      await json(route, pings === 1 ? { ok: false, error: "Engine is restarting" } : { ok: true })
    })
    await page.route("**/api/v1/databases/20/url**", (route) =>
      json(route, { url: "postgres://jd:secret@localhost/orders" }),
    )
    await page.goto(path)
    for (const shelf of ["SQL", "Documents", "Key–value", "Analytics"]) {
      await expect(page.getByRole("group", { name: shelf })).toBeVisible()
    }
    await page.getByRole("button", { name: /^PostgreSQL/ }).click()
    const panel = page.locator("[data-slot=flow-panel]")
    await expect(panel).toHaveCount(1)
    await panel.getByLabel("Name", { exact: true }).fill("orders-db")
    await panel.getByLabel("Database", { exact: true }).fill("orders")
    await panel.getByRole("radio", { name: "17", exact: true }).click()
    await panel.getByText("Account", { exact: true }).click()
    await panel.getByLabel("User", { exact: true }).fill("orders_user")
    await panel.getByLabel("Password", { exact: true }).fill("password-123")
    await expect(panel.getByRole("switch")).not.toBeChecked()
    await panel.getByRole("button", { name: "Create", exact: true }).click()
    const progress = panel.locator("[data-slot=database-progress]")
    const current = progress.locator('[aria-current="step"]')
    await expect(current).toContainText("Start the container")
    await expect(progress.getByRole("status")).toContainText("Pulling the image")
    await expect(panel.locator('[style*="--border-beam-width"]')).toHaveCount(1)
    finishProvision()
    await expect(current).toContainText("Wait for the engine")
    finishAdopt()
    await expect(current).toContainText("Connect it")
    finishPing()
    await expect.poll(() => pings).toBe(2)
    if (path.startsWith("/deploy")) {
      await expect(page.getByRole("heading", { name: "orders-db is ready" })).toBeVisible()
      await expect(page.getByLabel("Connection string", { exact: true })).toHaveAttribute(
        "type",
        "password",
      )
      await expect(page.getByRole("link", { name: "Open in Databases" })).toHaveAttribute(
        "href",
        "/databases/20",
      )
    } else {
      await expect(page).toHaveURL(/\/databases\/20$/)
    }
    expect(requests).toEqual([
      {
        engine: "postgres",
        name: "orders-db",
        database: "orders",
        version: "17",
        user: "orders_user",
        password: "password-123",
        exposure: "local",
      },
    ])
  })

  test(`${path} retries a timed-out engine without creating a second container`, async ({
    page,
  }) => {
    await setup(page, path)
    await page.clock.install()
    let provisions = 0
    let adoptions = 0
    let reachable = false
    await page.route("**/api/v1/databases/provision", async (route) => {
      provisions++
      await json(route, { container: "orders-db" })
    })
    await page.route("**/api/v1/databases/adopt", async (route) => {
      adoptions++
      if (reachable) return json(route, { id: 20, name: "orders-db", driver: "postgres" })
      await route.fulfill({
        status: 409,
        contentType: "application/json",
        body: JSON.stringify({
          error: { code: "sign_in_failed", message: "Engine is starting up" },
        }),
      })
    })
    await page.route("**/api/v1/databases/20/ping", (route) => json(route, { ok: true }))
    await page.route("**/api/v1/databases/20/url**", (route) =>
      json(route, { url: "postgres://jd:secret@localhost/orders" }),
    )
    await page.goto(path)
    await page.getByRole("button", { name: /^PostgreSQL/ }).click()
    await page.getByRole("button", { name: "Create", exact: true }).click()
    await expect.poll(() => adoptions).toBe(1)
    await page.clock.fastForward(181_000)
    const panel = page.locator("[data-slot=flow-panel]")
    await expect(panel).toContainText("Database container already created")
    await expect(panel).toContainText("Engine is starting up")
    const failedStage = panel.getByRole("listitem").filter({ hasText: "Wait for the engine" })
    await expect(failedStage).toContainText("Failed")
    await expect(panel.getByLabel("Name", { exact: true })).toBeDisabled()
    reachable = true
    await panel.getByRole("button", { name: "Retry connection setup" }).click()
    if (path.startsWith("/deploy")) {
      await expect(page.getByRole("heading", { name: "orders-db is ready" })).toBeVisible()
    } else {
      await expect(page).toHaveURL(/\/databases\/20$/)
    }
    expect(provisions).toBe(1)
  })

  test(`${path} keeps progress readable with reduced motion on a phone`, async ({ page }) => {
    await page.emulateMedia({ reducedMotion: "reduce" })
    await page.setViewportSize({ width: 390, height: 844 })
    await setup(page, path)
    let finish!: () => void
    const pending = new Promise<void>((resolve) => (finish = resolve))
    await page.route("**/api/v1/databases/provision", async (route) => {
      await pending
      await json(route, { container: "orders-db" })
    })
    await page.goto(path)
    await page.getByRole("button", { name: /^PostgreSQL/ }).click()
    await page.getByRole("button", { name: "Create", exact: true }).click()
    const panel = page.locator("[data-slot=flow-panel]")
    await expect(panel.getByRole("status")).toContainText("Pulling the image")
    await expect(panel.locator('[style*="--border-beam-width"]')).toHaveCount(0)
    await expect(panel.locator('[aria-current="step"]')).toContainText("Start the container")
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth),
    ).toBe(false)
    await page.goto("/databases")
    finish()
  })
}
