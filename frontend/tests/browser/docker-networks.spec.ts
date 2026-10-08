import { expect, test } from "@playwright/test"
import { NETS, mockNetworks } from "./docker-networks-fixture"

/**
 * The Networks page against a host with eleven networks: Docker's own three,
 * two compose projects' networks, a shared `proxy`, a deployment's database
 * network, a macvlan on the LAN and two networks nothing is attached to.
 */

const id = (name: string) => NETS.find((n) => n.name === name)!.id

test("opens on the engine, with the unused networks as its verdict", async ({ page }) => {
  await mockNetworks(page)
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto("/docker/networks")

  const identity = page.locator("[data-slot=host-identity]")
  await expect(identity.getByText("Docker 29.8.0")).toBeVisible()
  await expect(identity.getByText("11 networks, 8 made here")).toBeVisible()
  await expect(identity.getByText("16 containers on them")).toBeVisible()

  // The verdict narrows the table to what it counts, and lets go when pressed again.
  const verdict = identity.getByRole("button", { name: "2 networks unused" })
  await verdict.click()
  await expect(verdict).toHaveAttribute("aria-pressed", "true")
  const table = page.getByRole("table").first()
  await expect(table.getByRole("button", { name: "old-staging", exact: true })).toBeVisible()
  await expect(table.getByRole("button", { name: "wiki_default", exact: true })).toBeVisible()
  await expect(table.getByRole("button", { name: "proxy", exact: true })).toHaveCount(0)
  await verdict.click()
  await expect(table.getByRole("button", { name: "proxy", exact: true })).toBeVisible()
})

test("the band says which networks are busy, how much of the pool is taken and who moved", async ({
  page,
}) => {
  await mockNetworks(page)
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto("/docker/networks")

  const band = page.locator("[data-slot=network-band]")
  // Busiest first: proxy and shop's two carry megabytes, the rest kilobytes.
  const traffic = band.getByRole("region", { name: "Traffic by network" })
  const lines = traffic.getByRole("button", { name: /^Open / })
  await expect(lines.first()).toBeVisible()
  const names = await lines.evaluateAll((all) => all.map((b) => b.getAttribute("aria-label")))
  expect(names.slice(0, 3).sort()).toEqual(["Open proxy", "Open shop_backend", "Open shop_default"])
  expect(names).not.toContain("Open old-staging")

  // Docker's built-in pools hold thirty-one networks; nine blocks are taken
  // (the macvlan's LAN subnet holds a 192.168 block of its own).
  const addresses = band.getByRole("region", { name: "Address space" })
  await expect(addresses.getByText("of 31 blocks taken")).toBeVisible()
  await expect(addresses.getByRole("img")).toHaveAttribute(
    "aria-label",
    "9 of 31 address blocks taken; the first free is 172.25.0.0/16",
  )

  // Docker names the container by id; the page names it.
  const recent = band.getByRole("region", { name: "Recent network changes" })
  await expect(recent.getByText("shop-web-1").first()).toBeVisible()
  await expect(recent.getByText("joined").first()).toBeVisible()
})

test("the networks table reads who made each network, its subnet and its members", async ({
  page,
}) => {
  await mockNetworks(page)
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto("/docker/networks")

  const table = page.getByRole("table").first()
  await expect(table.getByRole("columnheader")).toHaveText([
    "Network",
    "Subnet",
    "Containers",
    "Traffic",
    "Actions",
  ])
  const backend = table.getByRole("row").filter({ hasText: "shop_backend" })
  await expect(backend.getByText("compose · shop")).toBeVisible()
  await expect(backend.getByText("no internet")).toBeVisible()
  await expect(backend.getByText("via 172.19.0.1")).toBeVisible()
  await expect(backend.getByText("4 containers")).toBeVisible()

  // Docker refuses to remove a network with members, and Docker's own at all;
  // the button stays, disabled, with the reason as its name.
  await expect(
    backend.getByRole("button", { name: /cannot be removed while attached/ }),
  ).toBeDisabled()
  const bridge = table.getByRole("row").filter({ hasText: "the default bridge" })
  await expect(bridge.getByRole("button", { name: /Docker's own network/ })).toBeDisabled()
  const staging = table.getByRole("row").filter({ hasText: "old-staging" })
  await expect(staging.getByRole("button", { name: "Remove old-staging" })).toBeEnabled()

  // The owner chips count and narrow.
  await page.getByRole("button", { name: /^Compose\s*4$/ }).click()
  await expect(table.getByRole("row")).toHaveCount(5)
})

test("the containers table answers which network each container is on, at what address and by what name", async ({
  page,
}) => {
  await mockNetworks(page)
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto("/docker/networks")

  const table = page.getByRole("table").nth(1)
  const api = table.getByRole("row").filter({ hasText: "shop-api-1" })
  await expect(api.getByText("172.19.0.3")).toBeVisible()
  await expect(api.getByText("172.18.0.3")).toBeVisible()
  await expect(api.getByText("api", { exact: true })).toBeVisible()

  // On the default bridge nothing answers to a name, and the cell says so.
  const portainer = table.getByRole("row").filter({ hasText: "portainer" })
  await expect(portainer.getByText("no names on the default bridge")).toBeVisible()

  // A network's chip narrows the table to the containers on it.
  await api.getByRole("button", { name: "Show the containers on shop_default" }).click()
  await expect(table.getByRole("row")).toHaveCount(4)
  await expect(page.getByRole("button", { name: /only shop_default/ })).toBeVisible()
  await page.keyboard.press("Escape")
  await expect(table.getByRole("row")).toHaveCount(17)

  // Rates arrive with the socket's second frame.
  await expect(api.getByText(/↓ .*\/s/)).toBeVisible({ timeout: 10_000 })
})

test("a network's sheet is a live readout with its members as a table", async ({ page }) => {
  const mocks = await mockNetworks(page)
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto("/docker/networks")

  await page.getByRole("table").first().getByRole("button", { name: "shop_backend" }).click()
  await expect(page).toHaveURL(new RegExp(`network=${id("shop_backend")}`))
  const sheet = page.getByRole("dialog")
  await expect(sheet.getByText("compose · shop")).toBeVisible()
  await expect(sheet.getByText("all running")).toBeVisible()
  await expect(sheet.getByText("of 65,533")).toBeVisible()

  const members = sheet.getByRole("table")
  const db = members.getByRole("row").filter({ hasText: "shop-db-1" })
  await expect(db.getByText("172.19.0.2")).toBeVisible()
  await expect(db.getByText("db", { exact: true })).toBeVisible()
  await expect(db.getByText("database", { exact: true })).toBeVisible()

  await db.getByRole("button", { name: "Detach shop-db-1" }).click()
  await expect
    .poll(() => mocks.writes.map((w) => w.path))
    .toContain(`/docker/networks/${id("shop_backend")}/disconnect`)
})

test("Remove unused prunes the networks nothing is attached to", async ({ page }) => {
  const mocks = await mockNetworks(page)
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto("/docker/networks")

  await page.getByRole("button", { name: "Remove unused" }).click()
  const dialog = page.getByRole("alertdialog").or(page.getByRole("dialog"))
  await expect(dialog.getByText("old-staging, wiki_default")).toBeVisible()
  await dialog.getByRole("button", { name: "Remove", exact: true }).click()
  await expect
    .poll(() => mocks.writes.map((w) => `${w.method} ${w.path}`))
    .toContain("POST /docker/networks/prune")
})

test("on a phone both lists read down the row and nothing scrolls sideways", async ({ page }) => {
  await mockNetworks(page)
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto("/docker/networks")

  await expect(page.getByRole("columnheader")).toHaveCount(0)
  const networks = page.getByRole("list", { name: "Networks" })
  await expect(networks.getByRole("button", { name: "shop_backend" })).toBeVisible()
  const containers = page.getByRole("list", { name: "Containers" })
  await expect(containers.getByRole("button", { name: "shop-db-1" })).toBeVisible()

  const overflow = await page.evaluate(
    () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
  )
  expect(overflow).toBeLessThanOrEqual(1)
})
