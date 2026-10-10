import { expect, test } from "@playwright/test"
import { DOCKER_CONTAINERS, mockDockerHost } from "./docker-fixture"

/**
 * The Docker overview after it took §15's exit: no tiles, the daemon's
 * identity line with its verdicts, the band of what the containers use and
 * what just happened to them, and every container as a table of readings.
 *
 * The host behind it has two things wrong — the shop's worker was killed for
 * memory and Uptime Kuma is failing its health check — and one container
 * stopped by hand, which is not a problem and must not read as one.
 */

const id = (name: string) => DOCKER_CONTAINERS.find((c) => c.name === name)!.id

test("the overview opens on the daemon and its verdicts, with no tiles", async ({ page }) => {
  await mockDockerHost(page)
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto("/docker")

  const identity = page.locator("[data-slot='host-identity']")
  await expect(identity.getByText("Docker 29.2.0")).toBeVisible()
  await expect(identity.getByText("overlay2 on cgroup v2")).toBeVisible()
  await expect(identity.getByText("10 of 12 containers running")).toBeVisible()
  await expect(identity.getByText("3 of 4 projects up")).toBeVisible()
  await expect(identity.getByText("2 containers failing")).toBeVisible()
  await expect(identity.getByText("2 issues to look at")).toBeVisible()

  await expect(page.locator("[data-slot='stat-tile']")).toHaveCount(0)
  await expect(page.getByText("Runtime health")).toHaveCount(0)
})

test("failing containers lead the table and the verdict narrows it to them", async ({ page }) => {
  await mockDockerHost(page)
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto("/docker")

  const table = page.getByRole("table")
  await expect(table.getByRole("columnheader", { name: "CPU · last hour" })).toBeVisible()
  const rows = table.locator("tbody tr")
  await expect(rows).toHaveCount(12)
  await expect(rows.nth(0)).toContainText("monitoring-uptime-kuma")
  await expect(rows.nth(0)).toContainText("Unhealthy")
  await expect(rows.nth(1)).toContainText("shop-worker")
  await expect(rows.nth(1)).toContainText("killed for memory")
  // Stopped by hand is last, and quiet.
  await expect(rows.nth(11)).toContainText("mailhog")
  await expect(rows.nth(11)).toContainText("finished")

  await page.getByRole("button", { name: "2 containers failing" }).click()
  await expect(rows).toHaveCount(2)
  await expect(page.getByRole("button", { name: /Failing/ })).toHaveAttribute(
    "aria-pressed",
    "true",
  )
})

test("a container's readings sit in the columns that name them", async ({ page }) => {
  await mockDockerHost(page)
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto("/docker")

  const api = page.getByRole("row").filter({ hasText: "shop-api" })
  await expect(api).toContainText("38.4%")
  // Against its limit, said as a share of it.
  await expect(api).toContainText("of 512.0 MB")
  const db = page.getByRole("row").filter({ hasText: "shop-db" })
  await expect(db).toContainText("no limit")
  // The posture issue is counted beside the name, not in the state.
  await expect(db.getByRole("button", { name: /1 issue/ })).toBeVisible()
  await expect(api.getByRole("img", { name: /processor over the last hour/ })).toBeVisible()
})

test("project chips narrow the table to one compose project", async ({ page }) => {
  await mockDockerHost(page)
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto("/docker")

  const rows = page.getByRole("table").locator("tbody tr")
  await expect(rows).toHaveCount(12)
  await page
    .getByRole("group", { name: "Project" })
    .getByRole("button", { name: /monitoring/ })
    .click()
  await expect(rows).toHaveCount(4)
  await page.getByRole("button", { name: /Standalone/ }).click()
  await expect(rows).toHaveCount(2)
  await page.getByRole("button", { name: "Clear every filter" }).click()
  await expect(rows).toHaveCount(12)
})

test("the band names the busiest containers and the last thing each did", async ({ page }) => {
  await mockDockerHost(page)
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto("/docker")

  const processor = page.getByLabel("Processor by container")
  await expect(processor.getByRole("button").first()).toHaveAccessibleName("Open shop-api")
  const memory = page.getByLabel("Memory by container")
  await expect(memory.getByRole("button").first()).toHaveAccessibleName(
    "Open monitoring-prometheus",
  )

  const recent = page.getByLabel("Recent changes")
  // An OOM kill and its exit are one line, and the crash before it is counted.
  await expect(recent.getByText("ran out of memory")).toBeVisible()
  await expect(recent.getByText("· 2 crashes")).toBeVisible()
  await expect(recent.getByText("failing its health check")).toBeVisible()
  // Two days ago is not recent.
  await expect(recent.getByText("mailhog")).toHaveCount(0)
})

test("an event that lands while the page is open joins Recent", async ({ page }) => {
  const mocks = await mockDockerHost(page)
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto("/docker")

  const recent = page.getByLabel("Recent changes")
  await expect(recent.getByText("failing its health check")).toBeVisible()
  mocks.emit({
    time: new Date().toISOString(),
    type: "container",
    action: "restart",
    name: "monitoring-grafana",
    id: id("monitoring-grafana"),
    image: "grafana/grafana:11.4.0",
    stack: "monitoring",
    message: "monitoring-grafana restarted",
    level: "notice",
    source: "compose",
  })
  const line = recent.getByRole("button", { name: "Open monitoring-grafana" })
  await expect(line).toContainText("restarted")
  await expect(line).toContainText("just now")
})

test("a row opens its container", async ({ page }) => {
  await mockDockerHost(page)
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto("/docker")

  await page.getByRole("row").filter({ hasText: "automation-n8n" }).getByText("Running").click()
  await expect(page).toHaveURL(new RegExp(`/docker/containers/${id("automation-n8n")}$`))
})

test("on a phone the table is drawn down the row, with nothing dropped", async ({ page }) => {
  await mockDockerHost(page)
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto("/docker")

  await expect(page.getByRole("columnheader")).toHaveCount(0)
  const worker = page.getByRole("listitem").filter({ hasText: "shop-worker" })
  await expect(worker.getByText("killed for memory · 12 minutes ago")).toBeVisible()
  const api = page.getByRole("listitem").filter({ hasText: "shop-api" })
  await expect(api.getByText("38.4% CPU")).toBeVisible()
  await expect(api.getByText(/of 512\.0 MB/)).toBeVisible()

  const overflow = await page.evaluate(
    () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
  )
  expect(overflow).toBeLessThanOrEqual(1)
})
