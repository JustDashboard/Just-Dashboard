import { expect, test, type Page } from "@playwright/test"
import { bridgedDatabases, hostPorts } from "./fixtures/proxy/ports"
import { json, mockProxy } from "./proxy-fixtures"

/**
 * The ports page and what it feeds, against sockets shaped as a real host
 * lists them. A socket bound to one specific address — a tailnet IP, the
 * public IP — used to be drawn and counted as loopback, because only a
 * wildcard bind counted as exposed; a database on a public IP then raised
 * nothing anywhere.
 */

async function mockHost(page: Page, listeners: unknown[] = hostPorts) {
  await mockProxy(page, { included: true })
  // Registered after the proxy tables, so it answers first.
  await page.route("**/api/v1/ports", (route) => json(route, listeners))
}

test("a socket on one address is exposed, never loopback", async ({ page }) => {
  await mockHost(page)
  await page.goto("/proxy/ports")

  const table = page.getByRole("table")
  const rows = table.locator("tbody tr")
  const caddy = rows.filter({ hasText: "100.110.34.31" })
  await expect(caddy.getByText("exposed", { exact: true })).toBeVisible()
  await expect(caddy.getByText("loopback", { exact: true })).toHaveCount(0)
  await expect(rows.filter({ hasText: "203.0.113.5" }).getByText("Redis exposed")).toBeVisible()

  // The tiles say which kind of exposure they count, and count the public
  // Redis as the database it is.
  await expect(page.getByText("2 on all · 3 on one IP each")).toBeVisible()
  await expect(page.getByText("counted once per port")).toBeVisible()

  // Loopback is loopback: the DNS stub and the local Postgres, nothing else.
  await page.getByRole("button", { name: /^Loopback/ }).click()
  await expect(rows).toHaveCount(2)
  await expect(rows.filter({ hasText: "127.0.0.53" })).toBeVisible()
  await expect(rows.filter({ hasText: "127.0.0.1" })).toBeVisible()
})

test("a socket on one address can be taken to the firewall", async ({ page }) => {
  await mockHost(page)
  await page.goto("/proxy/ports")

  await page.getByRole("button", { name: "Actions for tcp port 8443" }).click()
  await page.getByRole("menuitem", { name: "Firewall" }).click()
  await expect(page).toHaveURL(/\/security\/firewall$/)
})

test("the overview names a database on a public address where it answers", async ({ page }) => {
  await mockHost(page)
  await page.goto("/proxy")

  await page.getByRole("button", { name: /^Redis answers on 203\.0\.113\.5/ }).click()
  await expect(page.getByText("6379/tcp redis-server on 203.0.113.5")).toBeVisible()
  await expect(page.getByText(/answers on every interface/)).toHaveCount(0)
})

test("a database is coloured by who can connect, and counted once however it is bound", async ({
  page,
}) => {
  await mockHost(page, bridgedDatabases)
  await page.goto("/proxy/ports")

  // Postgres on every interface is critical; Redis on the Docker bridges and
  // MongoDB on a link-local address are warnings, as the posture calls them.
  const rows = page.getByRole("table").locator("tbody tr")
  for (const address of ["0.0.0.0", "::"]) {
    const status = rows.filter({ hasText: address }).getByText("PostgreSQL exposed")
    await expect(status).toHaveClass(/text-destructive/)
  }
  for (const [address, label] of [
    ["10.0.0.1", "Redis exposed"],
    ["10.0.2.1", "Redis exposed"],
    ["fe80::b482:4dff:fe92:4281", "MongoDB exposed"],
  ]) {
    const status = rows.filter({ hasText: address }).getByText(label)
    await expect(status).toHaveClass(/text-warning/)
    await expect(status).not.toHaveClass(/text-destructive/)
  }

  // Three databases on five sockets.
  const tile = page.locator('[data-slot="stat-tile"]').filter({ hasText: "Databases exposed" })
  await expect(tile.getByText("3", { exact: true })).toBeVisible()

  await page.goto("/proxy")
  await expect(
    page.getByRole("button", { name: /^3 database or control ports answer off this machine/ }),
  ).toBeVisible()
})

test("databases only a bridge can reach are a warning on the tile too", async ({ page }) => {
  await mockHost(
    page,
    bridgedDatabases.filter((l) => l.port !== 5432),
  )
  await page.goto("/proxy/ports")
  const tile = page.locator('[data-slot="stat-tile"]').filter({ hasText: "Databases exposed" })
  await expect(tile.getByText("2", { exact: true })).toHaveClass(/text-warning/)
})

test("the tiles' hints fit a phone", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await mockHost(page)
  await page.goto("/proxy/ports")
  // Both tiles' hints are whole, not cut at the tile's edge.
  for (const text of ["2 on all · 3 on one IP each", "counted once per port"]) {
    const hint = page.getByText(text)
    await expect(hint).toBeVisible()
    expect(await hint.evaluate((el) => el.scrollWidth > el.clientWidth)).toBe(false)
  }
  const overflow = await page.evaluate(
    () => document.documentElement.scrollWidth > document.documentElement.clientWidth,
  )
  expect(overflow).toBe(false)
})

test("a listing that timed out can be tried again", async ({ page }) => {
  await mockProxy(page, { included: true })
  let requests = 0
  await page.route("**/api/v1/ports", async (route) => {
    if (++requests > 1) return json(route, hostPorts)
    await route.fulfill({
      status: 504,
      contentType: "application/json",
      body: JSON.stringify({
        error: {
          code: "timeout",
          message: "Listing the host's sockets took longer than 10 seconds.",
          retryable: true,
        },
      }),
    })
  })
  await page.goto("/proxy/ports")

  await expect(
    page.getByText("Listing the host's sockets took longer than 10 seconds."),
  ).toBeVisible()
  await page.getByRole("button", { name: "Try again" }).click()
  await expect(page.getByRole("table").locator("tbody tr").first()).toBeVisible()
  expect(requests).toBe(2)
})
