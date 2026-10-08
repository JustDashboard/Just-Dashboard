import { expect, test } from "@playwright/test"
import { N8N, POSTGRES, RUNNER, mockContainerPage } from "./container-page-fixture"

/**
 * One container's page after the 2026-10-08 overhaul, against n8n in a
 * compose project of five with a crash-looping task runner beside it.
 */

test("a container opens on its identity, its live readings and the project it runs in", async ({
  page,
}) => {
  await mockContainerPage(page)
  await page.setViewportSize({ width: 1440, height: 1000 })
  await page.goto(`/docker/containers/${N8N}?tab=overview`)

  const identity = page.locator("[data-slot=host-identity]")
  await expect(identity.getByText("automations-n8n-1", { exact: true })).toBeVisible()
  await expect(identity.getByText(":1.98.2")).toBeVisible()
  await expect(identity.getByText("restarted unless stopped")).toBeVisible()
  await expect(identity.getByText(/^up 4d/)).toBeVisible()
  // The verdict, with what qualifies it.
  await expect(identity.getByText("Running", { exact: true })).toBeVisible()
  await expect(identity.getByText("healthy", { exact: true })).toBeVisible()

  // Four readings off the container's own socket, the socket said to be live.
  const readings = page.getByTestId("container-readings")
  await expect(readings.locator("[data-slot=stat-tile]")).toHaveCount(4)
  await expect(page.getByText("Live", { exact: true })).toBeVisible()
  await expect(readings.getByText("of 2.0 GB")).toBeVisible()
  await expect(readings.getByText("of 2 cores")).toBeVisible()
  await expect(readings.getByText(/^in .*\/s · out .*\/s$/)).toBeVisible()

  // Its project, as a live table with this container marked and not a link.
  const company = page.getByRole("region", { name: "Containers beside it" })
  await expect(company.getByText("4 of 5 running")).toBeVisible()
  const self = company.locator("tr[aria-current=page]")
  await expect(self.getByText("this one")).toBeVisible()
  await expect(self.getByRole("link", { name: "n8n", exact: true })).toHaveCount(0)
  await expect(company.getByRole("row").filter({ hasText: "runner" })).toContainText("Restarting")
  await company.getByRole("link", { name: "postgres", exact: true }).click()
  await expect(page).toHaveURL(new RegExp(`/docker/containers/${POSTGRES}\\?tab=overview$`))
})

test("the picture draws how it is reached and what it keeps", async ({ page }) => {
  await mockContainerPage(page)
  await page.setViewportSize({ width: 1440, height: 1000 })
  await page.goto(`/docker/containers/${N8N}?tab=overview`)

  const ways = page.getByRole("list", { name: "Reached at" })
  await expect(ways.getByRole("link", { name: "automations.example.test" })).toBeVisible()
  await expect(ways.getByText("Every interface")).toBeVisible()
  await expect(ways.getByText(/the firewall's deny does not apply/)).toBeVisible()
  await expect(ways.getByText("proxy", { exact: true })).toBeVisible()

  const keeps = page.getByRole("list", { name: "Keeps" })
  await expect(keeps.getByText("automations_n8n_data")).toBeVisible()
  await expect(keeps.getByText("/srv/automations/files")).toBeVisible()
  await expect(keeps.getByText("gone when it stops")).toBeVisible()

  // The same ports, read down a table with the firewall's answer beside each.
  const ports = page.getByRole("table").filter({ hasText: "Firewall" })
  await expect(ports.getByRole("row").filter({ hasText: "9464" })).toContainText(
    "ufw denies it, and Docker goes around",
  )
  await expect(page.getByText("The firewall does not apply to these ports")).toBeVisible()
})

test("a crash loop is the verdict and the first thing on its page", async ({ page }) => {
  await mockContainerPage(page)
  await page.setViewportSize({ width: 1440, height: 1000 })
  await page.goto(`/docker/containers/${RUNNER}?tab=overview`)

  const identity = page.locator("[data-slot=host-identity]")
  await expect(identity.getByText("Restarting in a loop")).toBeVisible()
  await expect(identity.getByText("exit 1")).toBeVisible()
  await expect(page.getByText("Restart loop: 23 starts in the last 19 minutes.")).toBeVisible()
  await expect(page.getByRole("button", { name: "Read those lines" })).toBeVisible()

  // Twenty-three exits and starts are one line in Recent, not forty-six.
  const recent = page.getByRole("list", { name: "Recent events" })
  await expect(recent.getByText(/^Restarted ×\d+ in/)).toBeVisible()
  await expect(recent.getByRole("listitem")).toHaveCount(4)

  // Not running, so the readings say so rather than show a stale frame.
  await expect(page.getByText("Not running", { exact: true }).first()).toBeVisible()
})

test("Recent's way to every event opens the logs on Events", async ({ page }) => {
  await mockContainerPage(page)
  await page.goto(`/docker/containers/${N8N}?tab=overview`)
  await page.getByRole("button", { name: "All events" }).click()
  await expect(page.getByRole("tab", { name: "Logs" })).toHaveAttribute("data-state", "active")
  await expect(page.getByRole("button", { name: "Events", exact: true })).toHaveAttribute(
    "aria-pressed",
    "true",
  )
})

test("every verb is on the container's own page", async ({ page }) => {
  await mockContainerPage(page)
  await page.goto(`/docker/containers/${N8N}?tab=overview`)
  await expect(page.getByRole("button", { name: "Restart", exact: true })).toBeVisible()
  await expect(page.getByRole("button", { name: "Stop", exact: true })).toBeVisible()
  await page.getByRole("button", { name: "More actions for automations-n8n-1" }).click()
  await expect(page.getByRole("menuitem", { name: "Pause" })).toBeVisible()
  await expect(page.getByRole("menuitem", { name: "Copy container id" })).toBeVisible()
  await expect(page.getByRole("menuitem", { name: "Remove" })).toBeVisible()
})

test("the environment is a table that keeps credentials hidden from its filter", async ({
  page,
}) => {
  await mockContainerPage(page)
  await page.goto(`/docker/containers/${N8N}?tab=env`)

  await expect(page.getByRole("tab", { name: /Environment/ })).toContainText("17")
  const table = page.getByRole("table")
  await expect(table.getByRole("row").filter({ hasText: "N8N_HOST" })).toContainText(
    "automations.example.test",
  )
  await expect(page.getByText("correct-horse-battery")).toHaveCount(0)
  await expect(page.getByRole("button", { name: "Reveal DB_POSTGRESDB_PASSWORD" })).toBeVisible()

  const filter = page.getByRole("textbox", { name: "Filter the environment" })
  await filter.fill("redis")
  await expect(table.getByRole("row")).toHaveCount(2)
  // A hidden value is not searched: a match would say what it contains.
  await filter.fill("correct-horse")
  await expect(page.getByText("Nothing in the environment matches.")).toBeVisible()
})

for (const width of [390, 1280]) {
  test(`the container page does not scroll sideways at ${width}px`, async ({ page }) => {
    await mockContainerPage(page)
    await page.setViewportSize({ width, height: 900 })
    await page.goto(`/docker/containers/${N8N}?tab=overview`)
    await expect(page.getByRole("list", { name: "Keeps" })).toBeVisible()
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth),
    ).toBe(true)
  })
}
