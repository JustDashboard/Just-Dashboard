import { expect, test, type Page } from "@playwright/test"
import { hostPorts } from "./fixtures/proxy/ports"
import { json, mockProxy } from "./proxy-fixtures"

/**
 * The ports page and what it feeds, against sockets shaped as a real host
 * lists them. A socket bound to one specific address — a tailnet IP, the
 * public IP — used to be drawn and counted as loopback, because only a
 * wildcard bind counted as exposed; a database on a public IP then raised
 * nothing anywhere.
 */

async function mockHost(page: Page) {
  await mockProxy(page, { included: true })
  // Registered after the proxy tables, so it answers first.
  await page.route("**/api/v1/ports", (route) => json(route, hostPorts))
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
  await expect(page.getByText("2 on every interface · 3 on one address")).toBeVisible()
  await expect(page.getByText("a database or control port off the machine")).toBeVisible()

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
