import { expect, test, type Page } from "@playwright/test"
import { ctId, inspectOf, mockNetworkPage, netId, readOnly } from "./docker-network-page-fixture"
import { NETWORKS } from "./docker-networks-fixture"
import { risen } from "./risen"

/**
 * A network's own page, against `docker-networks-fixture`'s host: the shop's
 * database network behind its API, monitoring with Alertmanager killed, a
 * shared `proxy` joined to two other networks, a macvlan on the LAN, the
 * default bridge and two networks nothing is on.
 *
 * It was a sheet over the Networks table (`?network=`) until 0.8.7.
 */

const identity = (page: Page) => page.locator("[data-slot=network-identity]")
const rail = (page: Page) => page.locator("[data-slot=network-rail]")
const picture = (page: Page) => page.locator("[data-slot=wire-flow]")

test("opens on the network: who made it, its driver, subnet and members, where it stands", async ({
  page,
}) => {
  await mockNetworkPage(page)
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto("/docker/networks/shop_backend")

  const line = identity(page)
  await expect(line.getByText("shop_backend", { exact: true })).toBeVisible()
  await expect(line.getByText("in use", { exact: true })).toBeVisible()
  await expect(line.getByText("no internet", { exact: true })).toBeVisible()
  // A compose project's network is a way to its stack.
  await expect(line.getByRole("link", { name: "compose · shop" })).toHaveAttribute(
    "href",
    "/docker/stacks/shop",
  )
  await expect(line.getByText("bridge", { exact: true })).toBeVisible()
  await expect(line.getByText("172.19.0.0/16")).toBeVisible()
  await expect(line.getByText("4 containers", { exact: true })).toBeVisible()
  // One brand-faced command; the rest are behind the menu.
  await expect(line.getByRole("button", { name: "Attach a container", exact: true })).toBeVisible()
  await expect(line.getByRole("button", { name: "More actions for shop_backend" })).toBeVisible()

  await expect(page).toHaveTitle(/shop_backend/)
  await expect(page.getByRole("link", { name: "Networks", exact: true }).first()).toHaveAttribute(
    "href",
    "/docker/networks",
  )
  // No row of tiles: the sheet's four are the identity line and four readings in words.
  await expect(page.locator('[data-slot="stat-grid"]')).toHaveCount(0)
  await expect(page.getByRole("dialog")).toHaveCount(0)
})

test("the picture draws where it leads and who is on it", async ({ page }) => {
  await mockNetworkPage(page)
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto("/docker/networks/shop_backend")

  const flow = picture(page)
  await expect(flow).not.toHaveAttribute("data-loading", "true")
  const leads = flow.getByRole("region", { name: "Leads to" })
  // An internal network has no way out, and is joined to the shop's other
  // network through the two containers on both.
  await expect(leads.getByText("No way out")).toBeVisible()
  await expect(leads.getByText("through shop-api-1 and 1 more")).toBeVisible()
  await expect(leads.getByRole("link", { name: "Open shop_default" })).toHaveAttribute(
    "href",
    "/docker/networks/shop_default",
  )
  const members = flow.getByRole("region", { name: "Members" })
  for (const name of ["shop-api-1", "shop-db-1", "shop-redis-1", "shop-worker-1"]) {
    await expect(members.getByText(name, { exact: true })).toBeVisible()
  }
  await expect(members.getByText("172.19.0.2")).toBeVisible()
  await expect(members.getByRole("link", { name: "Open shop-db-1" })).toHaveAttribute(
    "href",
    `/docker/containers/${ctId("shop-db-1")}`,
  )
  await expect(flow.getByText("br-5e6f7a5e6f7a")).toBeVisible()

  // The readings under it, in words.
  await expect(page.getByText("4 of 65,533 taken")).toBeVisible()
  await expect(page.getByText("Members answer by name")).toBeVisible()
  await expect(
    page.locator("[data-slot=reading-line]").filter({ hasText: "Received" }),
  ).toContainText(/\/s in/, { timeout: 10_000 })
})

test("a network that reaches out goes through this server, and a macvlan straight onto its card", async ({
  page,
}) => {
  await mockNetworkPage(page)
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto("/docker/networks/proxy")
  const leads = picture(page).getByRole("region", { name: "Leads to" })
  await expect(leads.getByText("Out through")).toBeVisible()
  await expect(leads.getByText("172.21.0.1")).toBeVisible()
  await expect(leads.getByRole("link", { name: "Open shop_default" })).toBeVisible()
  await expect(leads.getByRole("link", { name: "Open monitoring_default" })).toBeVisible()
  await expect(identity(page).getByText("IPv6", { exact: true })).toBeVisible()

  await page.goto("/docker/networks/lan")
  const card = picture(page).getByRole("region", { name: "Leads to" })
  await expect(card.getByText("Straight onto")).toBeVisible()
  await expect(card.getByRole("link", { name: "Open enp3s0 on Interfaces" })).toHaveAttribute(
    "href",
    "/network/interfaces/enp3s0",
  )

  await page.goto("/docker/networks/bridge")
  await expect(page.getByText("No names on the default bridge")).toBeVisible()
  // Docker's own takes no container from here and is never removed.
  await expect(identity(page).getByRole("button", { name: "Attach a container" })).toHaveCount(0)
  await identity(page).getByRole("button", { name: "More actions for bridge" }).click()
  await expect(
    page.getByRole("menuitem", { name: "Docker's own network — cannot be removed" }),
  ).toBeDisabled()
})

test("the views are in the address, and Members lists every member on one line", async ({
  page,
}) => {
  await mockNetworkPage(page)
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto("/docker/networks/shop_backend")

  const tabs = page.getByRole("tablist", { name: "Views of shop_backend" })
  await expect(tabs.getByRole("tab")).toHaveText(["Overview", /^Members\s*4$/, "Configuration"])
  await tabs.getByRole("tab", { name: /Members/ }).click()
  await expect(page).toHaveURL(/\/docker\/networks\/shop_backend\?tab=members$/)

  const list = page.getByRole("list", { name: "Containers on this network" })
  const rows = list.locator("[data-slot=choice-row]")
  await expect(rows).toHaveCount(4)
  const db = rows.filter({ hasText: "shop-db-1" })
  await expect(db.getByText("172.19.0.2")).toBeVisible()
  // What the others call it: its service, its name and the alias it was given.
  await expect(db.locator("[data-slot=member-names]")).toHaveAttribute(
    "aria-label",
    "Answers to db, shop-db-1, database",
  )
  await expect(db.getByText("db", { exact: true })).toBeVisible()
  await expect(db.getByText("running", { exact: true })).toBeVisible()
  // The API joins this network to the shop's other one.
  const api = rows.filter({ hasText: "shop-api-1" })
  await expect(api.getByText("also on shop_default")).toBeVisible()
  await expect(api.getByRole("link", { name: "Open shop-api-1" })).toHaveAttribute(
    "href",
    `/docker/containers/${ctId("shop-api-1")}`,
  )

  // A member with another network and three names is the same row as one with neither.
  const heights = await rows.evaluateAll((all) =>
    all.map((row) => row.getBoundingClientRect().height),
  )
  expect(new Set(heights).size).toBe(1)

  await tabs.getByRole("tab", { name: "Configuration" }).click()
  await expect(page).toHaveURL(/\?tab=configuration$/)
  await expect(page.getByText("no — its members reach only each other")).toBeVisible()
  await expect(page.getByText("yes, while running", { exact: true })).toBeVisible()
  await expect(page.getByText("com.docker.compose.project=shop")).toBeVisible()
  await expect(page.getByText(netId("shop_backend"))).toBeVisible()

  await tabs.getByRole("tab", { name: "Overview" }).click()
  await expect(page).toHaveURL(/\/docker\/networks\/shop_backend$/)

  // A link to a view opens on it.
  await page.goto("/docker/networks/lan?tab=configuration")
  await expect(page.getByRole("tab", { name: "Configuration" })).toHaveAttribute(
    "aria-selected",
    "true",
  )
  await expect(page.getByText("enp3s0")).toBeVisible()
})

test("a stopped member stays on its network, which then cannot be removed", async ({ page }) => {
  await mockNetworkPage(page)
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto("/docker/networks/monitoring_default?tab=members")

  await expect(identity(page).getByText("3 of 4 running", { exact: true })).toBeVisible()
  const rows = page.locator("[data-slot=choice-row]")
  const alertmanager = rows.filter({ hasText: "monitoring-alertmanager-1" })
  await expect(alertmanager.getByText("exited", { exact: true })).toBeVisible()
  await expect(alertmanager.getByText("holds no address")).toBeVisible()
  // With no address and no rate it is still the height of the rows that have both.
  await expect(rows.filter({ hasText: "monitoring-grafana-1" })).toContainText(/↓/, {
    timeout: 10_000,
  })
  const heights = await rows.evaluateAll((all) =>
    all.map((row) => row.getBoundingClientRect().height),
  )
  expect(new Set(heights).size).toBe(1)

  await identity(page).getByRole("button", { name: "More actions for monitoring_default" }).click()
  await expect(
    page.getByRole("menuitem", {
      name: "In use by 4 containers — cannot be removed while attached",
    }),
  ).toBeDisabled()
})

test("from xl the networks stand beside it as a rail, and Alt+↓ walks it with the view", async ({
  page,
}) => {
  await mockNetworkPage(page)
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto("/docker/networks/shop_backend?tab=members")

  const column = rail(page)
  await expect(column).toBeVisible()
  await expect(column.getByText("11 networks")).toBeVisible()
  const rows = column.getByRole("listitem")
  await expect(rows).toHaveCount(11)
  await expect(column.getByRole("button", { name: "shop_backend", exact: true })).toHaveAttribute(
    "aria-current",
    "page",
  )
  // The table's order: in use by name, then unused, then Docker's own.
  await expect(rows.nth(4)).toContainText("shop_backend")
  await expect(rows.nth(5)).toContainText("shop_default")

  await page.keyboard.press("Alt+ArrowDown")
  await expect(page).toHaveURL(/\/docker\/networks\/shop_default\?tab=members$/)
  await expect(column.getByRole("button", { name: "shop_default", exact: true })).toHaveAttribute(
    "aria-current",
    "page",
  )
  await page.getByRole("button", { name: "Previous network" }).click()
  await expect(page).toHaveURL(/\/docker\/networks\/shop_backend\?tab=members$/)

  // The rail's search is the table's, and the one on screen stays first when it is filtered out.
  await column.getByRole("textbox", { name: "Find networks" }).fill("monitoring")
  // The one named for it, and the proxy network Grafana is also on.
  await expect(column.getByText("2 of 11")).toBeVisible()
  await expect(column.getByRole("button", { name: "shop_backend", exact: true })).toHaveAttribute(
    "aria-current",
    "page",
  )
  await column.getByRole("button", { name: "monitoring_default", exact: true }).click()
  await expect(page).toHaveURL(/\/docker\/networks\/monitoring_default\?tab=members$/)
})

test("the addresses that opened the sheet land on the page", async ({ page }) => {
  await mockNetworkPage(page)
  await page.setViewportSize({ width: 1440, height: 900 })

  // The sheet's own address, which the server map and old links still write.
  await page.goto(`/docker/networks?network=${netId("shop_backend")}`)
  await expect(page).toHaveURL(/\/docker\/networks\/shop_backend$/)
  await expect(identity(page).getByText("shop_backend", { exact: true })).toBeVisible()
  await expect(page.getByRole("dialog")).toHaveCount(0)

  // A deployment's removal plan names a network by id in the path; the name replaces it.
  await page.goto(`/docker/networks/${netId("proxy")}?tab=members`)
  await expect(page).toHaveURL(/\/docker\/networks\/proxy\?tab=members$/)
  await expect(page.locator("[data-slot=choice-row]")).toHaveCount(3)
})

test("attaching and detaching read what they disturb first, and removing goes back to the list", async ({
  page,
}) => {
  const mocks = await mockNetworkPage(page)
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto("/docker/networks/shop_backend?tab=members")

  await identity(page).getByRole("button", { name: "Attach a container", exact: true }).click()
  const attach = page.getByRole("dialog", { name: "Attach a container" })
  await attach.getByRole("combobox").click()
  // Only containers not already on it are offered.
  await expect(page.getByRole("option", { name: "shop-db-1" })).toHaveCount(0)
  await page.getByRole("option", { name: "caddy" }).click()
  await attach.getByRole("button", { name: "Attach", exact: true }).click()
  await expect(attach).toHaveCount(0)
  expect(mocks.writes).toContainEqual({
    method: "POST",
    path: `/docker/networks/${netId("shop_backend")}/connect`,
    body: { container: ctId("caddy") },
  })

  const db = page.locator("[data-slot=choice-row]").filter({ hasText: "shop-db-1" })
  await db.getByRole("button", { name: "Detach shop-db-1" }).click()
  // The press is the control's, not the row's: the container is not opened.
  await expect(page).toHaveURL(/\/docker\/networks\/shop_backend\?tab=members$/)
  await page
    .getByRole("dialog", { name: "Detach shop-db-1" })
    .getByRole("button", { name: "Detach", exact: true })
    .click()
  await expect
    .poll(() => mocks.writes.map((w) => `${w.method} ${w.path}`))
    .toContain(`POST /docker/networks/${netId("shop_backend")}/disconnect`)
  expect(
    mocks.writes.find((w) => w.path === `/docker/networks/${netId("shop_backend")}/disconnect`)
      ?.body,
  ).toEqual({ container: ctId("shop-db-1") })

  // A network nothing is on: unused, with Remove in the menu.
  await page.goto("/docker/networks/old-staging?tab=members")
  await expect(identity(page).getByText("unused", { exact: true })).toBeVisible()
  await expect(page.getByText("Nothing is attached")).toBeVisible()
  await identity(page).getByRole("button", { name: "More actions for old-staging" }).click()
  await page.getByRole("menuitem", { name: "Remove", exact: true }).click()
  await page
    .getByRole("dialog", { name: "Remove old-staging" })
    .getByRole("button", { name: "Remove", exact: true })
    .click()
  await expect
    .poll(() => mocks.writes.map((w) => `${w.method} ${w.path}`))
    .toContain(`DELETE /docker/networks/${netId("old-staging")}`)
  await expect(page).toHaveURL(/\/docker\/networks$/)
})

test("a reader is offered no change", async ({ page }) => {
  await mockNetworkPage(page, { session: readOnly })
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto("/docker/networks/old-staging?tab=members")

  await expect(identity(page).getByText("unused", { exact: true })).toBeVisible()
  await expect(page.getByRole("button", { name: "Attach a container" })).toHaveCount(0)
  await identity(page).getByRole("button", { name: "More actions for old-staging" }).click()
  await expect(page.getByRole("menuitem", { name: "Copy name" })).toBeVisible()
  await expect(page.getByRole("menuitem", { name: /Remove|cannot be removed/ })).toHaveCount(0)
  await page.keyboard.press("Escape")

  await page.goto("/docker/networks/shop_backend?tab=members")
  await expect(page.locator("[data-slot=choice-row]")).toHaveCount(4)
  await expect(page.getByRole("button", { name: /^Detach / })).toHaveCount(0)
  await expect(page.getByRole("button", { name: "Attach a container" })).toHaveCount(0)
})

test("a name that is no network says so, with the way back and the rest in the rail", async ({
  page,
}) => {
  await mockNetworkPage(page)
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto("/docker/networks/staging_default")

  await expect(page.getByText("No network named staging_default")).toBeVisible()
  // Not the daemon's own words.
  await expect(page.getByText(/not found/i)).toHaveCount(0)
  await expect(page.getByRole("link", { name: "Back to the networks" })).toHaveAttribute(
    "href",
    "/docker/networks",
  )
  await expect(rail(page).getByRole("listitem")).toHaveCount(11)
})

test("a network the inspect cannot read is drawn from the list, and with neither the error is said", async ({
  page,
}) => {
  await mockNetworkPage(page)
  const unreachable = {
    status: 503,
    contentType: "application/json",
    body: JSON.stringify({
      error: { code: "docker_unavailable", message: "Docker is unreachable" },
    }),
  }
  await page.route(/\/api\/v1\/docker\/networks\/[^/?]+$/, (route) =>
    route.request().method() === "GET" ? route.fulfill(unreachable) : route.fallback(),
  )
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto("/docker/networks/shop_backend")
  await expect(identity(page).getByText("4 containers", { exact: true })).toBeVisible()
  await expect(
    picture(page).getByRole("region", { name: "Members" }).getByText("shop-db-1"),
  ).toBeVisible()

  await page.route("**/api/v1/docker/networks/", (route) => route.fulfill(unreachable))
  await page.reload()
  await expect(page.locator("[data-slot=error-state]")).toBeVisible()
  await expect(identity(page)).toHaveCount(0)
})

// A network nothing is on has no column as tall as the hub's mark and its
// three lines. The picture keeps the room those lines need under itself
// (`WireFlow`), where this page used to pad its own hub by a guess.
test("the picture keeps room for the hub's words where no column is tall enough", async ({
  page,
}) => {
  await mockNetworkPage(page)
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto("/docker/networks/old-staging")
  const flow = picture(page)
  await expect(flow.getByText("Nothing is attached.")).toBeVisible()
  await risen(page)
  const words = (await flow.getByText("172.23.0.0/16").boundingBox())!
  const box = (await flow.boundingBox())!
  const foot = words.y + words.height
  expect(foot).toBeLessThanOrEqual(box.y + box.height + 0.5)
  // The room is what the words reach past the columns by, not a fixed pad
  // around the mark: the mark stands at the picture's head.
  expect((await flow.locator("[data-slot=wire-hub-room]").boundingBox())!.height).toBeGreaterThan(0)
  const mark = (await flow.getByText("Bridge", { exact: true }).boundingBox())!
  expect(mark.y - box.y).toBeLessThan(120)
  // What stands under the picture begins after them.
  const under = (await page.locator("[data-slot=reading-lines]").first().boundingBox())!
  expect(under.y).toBeGreaterThanOrEqual(foot)
})

for (const width of [1280, 390]) {
  test(`the page has its shape before the network lands at ${width}px`, async ({ page }) => {
    await mockNetworkPage(page)
    let release = () => {}
    const held = new Promise<void>((resolve) => (release = resolve))
    await page.route(/\/api\/v1\/docker\/networks\/[^/?]*$/, async (route) => {
      if (route.request().method() !== "GET") return route.fallback()
      await held
      return route.fallback()
    })
    await page.setViewportSize({ width, height: 900 })
    await page.goto("/docker/networks/shop_backend")

    const line = identity(page)
    const tabs = page.getByRole("tablist")
    const flow = picture(page)
    await expect(line.getByText("shop_backend", { exact: true })).toBeVisible()
    await expect(tabs.getByRole("tab")).toHaveCount(3)
    await expect(flow).toHaveAttribute("data-loading", "true")
    await risen(page)
    const before = {
      line: await line.boundingBox(),
      tabs: await tabs.boundingBox(),
      flow: (await flow.boundingBox())!,
    }

    release()
    await expect(line.getByText("in use", { exact: true })).toBeVisible()
    await expect(flow).not.toHaveAttribute("data-loading", "true")
    await risen(page)
    expect(await line.boundingBox()).toEqual(before.line)
    expect(await tabs.boundingBox()).toEqual(before.tabs)
    const after = (await flow.boundingBox())!
    expect({ x: after.x, y: after.y, width: after.width }).toEqual({
      x: before.flow.x,
      y: before.flow.y,
      width: before.flow.width,
    })
  })
}

test("on a phone it is one pane: no rail, nothing sideways, every word of the picture kept", async ({
  page,
}) => {
  await mockNetworkPage(page)
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto("/docker/networks/shop_backend")

  await expect(identity(page).getByText("shop_backend", { exact: true })).toBeVisible()
  await expect(rail(page)).toBeHidden()
  const flow = picture(page)
  await expect(flow.getByText("Leads to")).toBeVisible()
  await expect(flow.getByText("shop-redis-1", { exact: true })).toBeVisible()
  await expect(identity(page).getByRole("button", { name: "Attach a container" })).toBeVisible()
  const overflow = () =>
    page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
  expect(await overflow()).toBeLessThanOrEqual(1)

  await page.getByRole("tab", { name: /Members/ }).click()
  const rows = page.locator("[data-slot=choice-row]")
  await expect(rows).toHaveCount(4)
  // The names have no column here: they are the row's third line, on every row.
  const db = rows.filter({ hasText: "shop-db-1" })
  await expect(db.getByText("172.19.0.2")).toBeVisible()
  await expect(db.getByText("running", { exact: true })).toBeVisible()
  await expect(db.getByText("answers to db, shop-db-1, database")).toBeVisible()
  await expect(db.locator("[data-slot=member-names]")).toBeHidden()
  const heights = await rows.evaluateAll((all) =>
    all.map((row) => row.getBoundingClientRect().height),
  )
  expect(new Set(heights).size).toBe(1)
  expect(await overflow()).toBeLessThanOrEqual(1)

  await page.getByRole("tab", { name: "Configuration" }).click()
  await expect(page.getByText("How it behaves")).toBeVisible()
  expect(await overflow()).toBeLessThanOrEqual(1)
})

test("the dashboard's own network takes no container, and says why where the command would be", async ({
  page,
}) => {
  const own = {
    ...inspectOf("shop_backend")!,
    owner: { kind: "dashboard", project: "just-dashboard" },
  }
  await mockNetworkPage(page, {
    inspect: { shop_backend: own },
    networks: [{ ...own, members: undefined, endpoints: [] }],
  })
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto("/docker/networks/shop_backend")
  await expect(identity(page).getByText("made by this dashboard")).toBeVisible()
  await expect(identity(page).getByRole("button", { name: "Attach a container" })).toHaveCount(0)
  await identity(page).getByRole("button", { name: "More actions for shop_backend" }).click()
  await expect(
    page.getByRole("menuitem", {
      name: "The dashboard's own private network takes no other containers",
    }),
  ).toBeDisabled()
})

test("a network removed elsewhere while its page is open goes back to the list", async ({
  page,
}) => {
  await mockNetworkPage(page)
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto("/docker/networks/old-staging")
  await expect(identity(page).getByText("unused", { exact: true })).toBeVisible()

  // Somebody prunes it: the list no longer has it and the Engine no longer knows it.
  const without = NETWORKS.filter((network) => network.name !== "old-staging")
  await page.route("**/api/v1/docker/networks/", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify(without),
    }),
  )
  await page.route("**/api/v1/docker/networks/old-staging", (route) =>
    route.fulfill({
      status: 404,
      contentType: "application/json",
      body: JSON.stringify({
        error: { code: "not_found", message: "network old-staging not found" },
      }),
    }),
  )
  // The page's own refresh, rather than waiting ten seconds for the list's next read.
  await page.keyboard.press("F5")
  await expect(page).toHaveURL(/\/docker\/networks$/)
  await expect(page.getByRole("table").first()).toBeVisible()
})

test("the view a network was left on is the one the next opens on, and the address says so", async ({
  page,
}) => {
  await mockNetworkPage(page)
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto("/docker/networks/shop_backend")
  await page.getByRole("tab", { name: /Members/ }).click()
  await expect(page.locator("[data-slot=choice-row]")).toHaveCount(4)

  await page.goto("/docker/networks/proxy")
  await expect(page).toHaveURL(/\/docker\/networks\/proxy\?tab=members$/)
  await expect(page.locator("[data-slot=choice-row]")).toHaveCount(3)

  // A network the picture leads to opens on the view the picture is on.
  await page.getByRole("tab", { name: "Overview" }).click()
  await picture(page).getByRole("link", { name: "Open shop_default" }).click()
  await expect(page).toHaveURL(/\/docker\/networks\/shop_default$/)
  await expect(identity(page).getByText("shop_default", { exact: true })).toBeVisible()
  await expect(page.getByRole("tab", { name: "Overview" })).toHaveAttribute("aria-selected", "true")
})
