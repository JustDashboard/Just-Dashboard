import { expect, test } from "@playwright/test"
import { DETAIL, mockStackDetail } from "./stack-detail-fixture"

/**
 * A stack's own page (`/docker/stacks/<name>`), over `shop`: six services,
 * one in a restart loop, one a step from its memory limit and one compose
 * never created, in a checkout with uncommitted changes.
 */

test("opens on the stack's identity line and says what is failing", async ({ page }) => {
  await mockStackDetail(page)
  await page.goto("/docker/stacks/shop")

  const identity = page.locator("[data-slot='host-identity']")
  await expect(identity.getByText("shop", { exact: true })).toBeVisible()
  await expect(identity.getByText("compose.yaml")).toBeVisible()
  await expect(identity.getByText("/srv/shop")).toBeVisible()
  await expect(identity.getByText("2 uncommitted")).toBeVisible()
  await expect(identity.getByText("1 behind")).toBeVisible()
  await expect(identity.getByText("4 of 6 services running")).toBeVisible()
  await expect(identity.getByText("3 ports published")).toBeVisible()

  // The verdict counts the restart loop, and pressing it narrows the table to it.
  const verdict = identity.getByRole("button", { name: "1 service failing" })
  await expect(verdict).toBeVisible()
  await verdict.click()
  const rows = page.locator("[data-workspace-item]")
  await expect(rows).toHaveCount(1)
  await expect(rows.first()).toHaveAttribute("data-workspace-item", "worker")
  await expect(verdict).toHaveAttribute("aria-pressed", "true")
  await page.keyboard.press("Escape")
  await expect(rows).toHaveCount(6)
})

test("the services are a table of live readings", async ({ page }) => {
  await mockStackDetail(page)
  await page.setViewportSize({ width: 1600, height: 1000 })
  await page.goto("/docker/stacks/shop")

  const row = (name: string) => page.locator(`tr[data-workspace-item='${name}']`)
  // Worst first: the loop, then the service never created, then the rest by name.
  await expect(page.locator("tr[data-workspace-item]")).toHaveCount(6)
  expect(
    await page
      .locator("tr[data-workspace-item]")
      .evaluateAll((rows) => rows.map((r) => r.getAttribute("data-workspace-item"))),
  ).toEqual(["worker", "search", "api", "cache", "db", "web"])

  await expect(row("worker").getByText("Restarting")).toBeVisible()
  await expect(row("worker").getByText(/^restarted ×5 in .+ · exit 1$/)).toBeVisible()
  await expect(row("api").getByText(/^up 26m/)).toBeVisible()
  await expect(row("cache").getByText(/no health check/)).toBeVisible()
  await expect(row("search").getByText("Not created")).toBeVisible()
  await expect(row("search").getByRole("button", { name: "Create it" })).toBeVisible()

  // Memory against its limit, amber near it; the network's rates from two frames.
  const db = row("db")
  await expect(db.getByText("/ 512 MB")).toBeVisible()
  await expect(db.locator(".text-warning").first()).toBeVisible()
  await expect(row("web").getByTitle("Received")).toContainText("/s")
  await expect(row("web").getByText("80 → 80")).toBeVisible()

  // A running service's dot breathes: the row is fed by the containers socket.
  const breathing = await row("api").evaluate((el) =>
    [...el.querySelectorAll("span")].some((s) => getComputedStyle(s).animationName !== "none"),
  )
  expect(breathing).toBe(true)
})

test("draws how the stack is reached and what happened to it", async ({ page }) => {
  await mockStackDetail(page)
  await page.setViewportSize({ width: 1440, height: 1600 })
  await page.goto("/docker/stacks/shop")

  const map = page.locator("[data-slot='stack-map']")
  await expect(map.getByRole("list", { name: "Ways in" }).getByRole("listitem")).toHaveCount(3)
  await expect(map.getByText(":443", { exact: true })).toBeVisible()
  await expect(map.getByText("This server only")).toBeVisible()
  const networks = map.getByRole("list", { name: "Networks" })
  await expect(networks.getByText("backend", { exact: true })).toBeVisible()
  await expect(networks.getByText("frontend", { exact: true })).toBeVisible()

  const recent = page.getByRole("region", { name: "Recent changes" })
  await expect(recent.getByText("exited 1")).toBeVisible()
  await expect(recent.getByText(/^restarted ×5 in \d+ min · exit 1$/)).toBeVisible()
  await expect(recent.getByText("passing its check again")).toBeVisible()

  const memory = page.getByRole("region", { name: "Memory by service" })
  await expect(memory.getByRole("button", { name: "Open db" })).toContainText(/9\d% of its limit/)
})

test("a service's compose verbs run for that service", async ({ page }) => {
  const mocks = await mockStackDetail(page)
  await page.setViewportSize({ width: 1440, height: 1000 })
  await page.goto("/docker/stacks/shop")

  const api = page.locator("tr[data-workspace-item='api']")
  await api.hover()
  await api.getByRole("button", { name: "Restart", exact: true }).click()
  const dialog = page.getByRole("dialog")
  await expect(dialog.getByText("docker compose restart api")).toBeVisible()
  await dialog.getByRole("button", { name: "Restart", exact: true }).click()
  await expect.poll(() => mocks.runs).toEqual(["restart:api"])

  await api.getByRole("button", { name: "More actions for api" }).click()
  await page.getByRole("menuitem", { name: "Recreate service" }).click()
  await expect(
    page.getByRole("dialog").getByText(/--force-recreate --remove-orphans api$/),
  ).toBeVisible()
  await page.getByRole("dialog").getByRole("button", { name: "Recreate", exact: true }).click()
  await expect.poll(() => mocks.runs).toEqual(["restart:api", "recreate:api"])

  await page
    .locator("tr[data-workspace-item='search']")
    .getByRole("button", { name: "Create it" })
    .click()
  await expect.poll(() => mocks.runs).toEqual(["restart:api", "recreate:api", "up:search"])
})

test("a row opens its container", async ({ page }) => {
  await mockStackDetail(page)
  await page.setViewportSize({ width: 1440, height: 1000 })
  await page.goto("/docker/stacks/shop")
  await page
    .locator("tr[data-workspace-item='db']")
    .getByRole("button", { name: "db", exact: true })
    .click()
  await expect(page).toHaveURL(/\/docker\/containers\/a3c8e2f4a6b8/)
})

test("on a phone each service is drawn down the row, with nothing dropped", async ({ page }) => {
  await mockStackDetail(page)
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto("/docker/stacks/shop")

  const db = page.locator("li[data-workspace-item='db']")
  await expect(db.getByText("postgres:16-alpine")).toBeVisible()
  await expect(db.getByText(/^\d+\.\d% CPU$/)).toBeVisible()
  await expect(db.getByText(/^4\d\d\.\d MB$/)).toBeVisible()
  await expect(db.getByText(/^up 3d 4h · healthy$/)).toBeVisible()
  await expect(page.locator("tr[data-workspace-item]")).toHaveCount(0)
  const overflow = await page.evaluate(
    () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
  )
  expect(overflow).toBeLessThanOrEqual(1)
})

test("a stack nothing has been created for says so", async ({ page }) => {
  await mockStackDetail(page, {
    containers: [],
    detail: {
      ...DETAIL,
      services: DETAIL.services.map((s) => ({
        ...s,
        container: "",
        state: "missing",
        status: "",
        missing: true,
      })),
      running: 0,
      containers: 0,
      deployed: false,
      state: "not-deployed",
      summary: "Not deployed · 6 services defined",
      git: undefined,
    },
  })
  await page.goto("/docker/stacks/shop")
  const identity = page.locator("[data-slot='host-identity']")
  await expect(identity.getByText("Not deployed")).toBeVisible()
  await expect(page.locator("[data-slot='stack-map']")).toHaveCount(0)
  await expect(page.getByRole("button", { name: "Create it" }).first()).toBeVisible()
})
