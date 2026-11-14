import { expect, test } from "@playwright/test"
import { NETS, mockNetworks, user } from "./docker-networks-fixture"
import { risen } from "./risen"

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
  await expect(table.getByRole("link", { name: "old-staging", exact: true })).toBeVisible()
  await expect(table.getByRole("link", { name: "wiki_default", exact: true })).toBeVisible()
  await expect(table.getByRole("link", { name: "proxy", exact: true })).toHaveCount(0)
  await verdict.click()
  await expect(table.getByRole("link", { name: "proxy", exact: true })).toBeVisible()

  // The line is a project's: the verdict beside the name, Create network its one command.
  await expect(identity).toHaveAttribute("data-layout", "project")
  await expect(identity.getByRole("button", { name: "Create network", exact: true })).toBeVisible()
  await expect(page.getByRole("button", { name: "Create network", exact: true })).toHaveCount(1)
  await expect(page.locator('[data-slot="stat-grid"]')).toHaveCount(0)
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
  // At rest it says the block the next network would get.
  await expect(addresses.getByText("First free")).toContainText("172.25.0.0/16")
  await expect(addresses.getByRole("img", { name: /^Addresses: / })).toHaveAttribute(
    "aria-label",
    /shop_backend 1 block/,
  )

  // Docker names the container by id; the page names it. Newest first.
  const recent = band.getByRole("region", { name: "Recent network changes" })
  const line = recent.locator("[data-slot=reading-line]")
  await expect(line).toContainText("shop-web-1")
  await expect(line).toContainText("joined")
  await recent.getByRole("button", { name: "Open monitoring_default" }).hover()
  await expect(line).toContainText("monitoring-alertmanager-1 left monitoring_default")

  // A press opens its network's own page.
  await traffic.getByRole("button", { name: "Open shop_backend" }).click()
  await expect(page).toHaveURL(/\/docker\/networks\/shop_backend$/)
})

test("the band is there before anything lands, and does not move when it does", async ({
  page,
}) => {
  await mockNetworks(page)
  let release = () => {}
  const held = new Promise<void>((resolve) => (release = resolve))
  await page.route("**/api/v1/docker/networks/", async (route) => {
    await held
    return route.fallback()
  })
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto("/docker/networks")

  const band = page.locator("[data-slot=network-band]")
  const blocks = band.locator("[data-slot=bar-reading]")
  await expect(blocks).toHaveCount(3)
  await expect(band.getByRole("region", { name: "Traffic by network" })).toContainText(
    "Reading the bridges…",
  )
  // Without the list no block is known to be free, so none is called free yet.
  await expect(band.getByText("First free")).toHaveCount(0)
  const heights = await blocks.evaluateAll((all) =>
    all.map((block) => block.getBoundingClientRect().height),
  )
  expect(new Set(heights).size).toBe(1)
  await risen(page)
  const before = await band.boundingBox()
  const identity = page.locator("[data-slot=host-identity]")
  const line = await identity.boundingBox()

  release()
  await expect(page.getByRole("table").first()).toBeVisible()
  await expect(band.getByText("First free")).toBeVisible()
  await risen(page)
  expect(await identity.boundingBox()).toEqual(line)
  expect(await band.boundingBox()).toEqual(before)
})

test("on a phone the opening line and the band are where they will be before the list lands", async ({
  page,
}) => {
  await mockNetworks(page)
  let release = () => {}
  const held = new Promise<void>((resolve) => (release = resolve))
  await page.route("**/api/v1/docker/networks/", async (route) => {
    await held
    return route.fallback()
  })
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto("/docker/networks")

  const identity = page.locator("[data-slot=host-identity]")
  const band = page.locator("[data-slot=network-band]")
  await expect(band.locator("[data-slot=bar-reading]")).toHaveCount(3)
  await risen(page)
  const line = await identity.boundingBox()
  const top = (await band.boundingBox())!.y

  release()
  await expect(identity.getByRole("button", { name: "2 networks unused" })).toBeVisible()
  await expect(band.getByText("First free")).toBeVisible()
  await risen(page)
  expect(await identity.boundingBox()).toEqual(line)
  expect((await band.boundingBox())!.y).toBe(top)
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
  // A local network takes `docker network connect` whatever its attachable
  // flag says; compose's networks included.
  await expect(
    backend.getByRole("button", { name: "Attach a container to shop_backend" }),
  ).toBeEnabled()
  const staging = table.getByRole("row").filter({ hasText: "old-staging" })
  await expect(staging.getByRole("button", { name: "Remove old-staging" })).toBeEnabled()

  // A network nothing is on, one carrying megabytes, the host network and one
  // with two subnets are the same row.
  const heights = await table
    .getByRole("row")
    .evaluateAll((rows) => rows.slice(1).map((row) => row.getBoundingClientRect().height))
  expect(heights).toHaveLength(11)
  expect(new Set(heights).size).toBe(1)

  // The owner chips count and narrow.
  await page.getByRole("button", { name: /^Compose\s*4$/ }).click()
  await expect(table.getByRole("row")).toHaveCount(5)

  // The name is the link to the network's own page, and the row is its larger target.
  await expect(table.getByRole("link", { name: "shop_backend", exact: true })).toHaveAttribute(
    "href",
    "/docker/networks/shop_backend",
  )
  await backend.getByText("via 172.19.0.1").click()
  await expect(page).toHaveURL(/\/docker\/networks\/shop_backend$/)
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

  // One network or two, names or none, stopped or carrying: one height, and
  // the row under a container on two networks is where the row under one is.
  const rows = table.getByRole("row")
  const boxes = await rows.evaluateAll((all) =>
    all.slice(1).map((row) => {
      const box = row.getBoundingClientRect()
      return { y: box.y, height: box.height }
    }),
  )
  expect(boxes).toHaveLength(16)
  expect(new Set(boxes.map((box) => box.height)).size).toBe(1)
  for (let i = 1; i < boxes.length; i++) {
    expect(boxes[i].y - boxes[i - 1].y).toBe(boxes[0].height)
  }
  // Two long names stay on the row's one line.
  const alertmanager = table.getByRole("row").filter({ hasText: "monitoring-alertmanager-1" })
  await expect(alertmanager.getByText("alertmanager", { exact: true })).toBeVisible()
})

test("Remove unused prunes the networks nothing is attached to, after reading the prune", async ({
  page,
}) => {
  const mocks = await mockNetworks(page)
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto("/docker/networks")

  await page.getByRole("button", { name: "Remove unused" }).click()
  const dialog = page.getByRole("dialog", { name: "Remove unused networks" })
  const removed = dialog.getByRole("list", { name: "Networks removed" })
  await expect(removed.getByText("old-staging", { exact: true })).toBeVisible()
  await expect(removed.getByText("wiki_default", { exact: true })).toBeVisible()
  await dialog.getByRole("button", { name: "Remove 2", exact: true }).click()
  await expect
    .poll(() => mocks.writes.map((w) => `${w.method} ${w.path}`))
    .toContain("POST /docker/networks/prune")
  expect(mocks.writes.find((w) => w.path === "/docker/networks/prune")?.body).toEqual({
    ids: [id("old-staging"), id("wiki_default")],
  })
})

test("on a phone both lists read down the row and nothing scrolls sideways", async ({ page }) => {
  await mockNetworks(page)
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto("/docker/networks")

  await expect(page.getByRole("columnheader")).toHaveCount(0)
  const networks = page.getByRole("list", { name: "Networks", exact: true })
  await expect(networks.getByRole("link", { name: "shop_backend", exact: true })).toBeVisible()
  const containers = page.getByRole("list", { name: "Containers", exact: true })
  await expect(containers.getByRole("button", { name: "shop-db-1" })).toBeVisible()

  const overflow = await page.evaluate(
    () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
  )
  expect(overflow).toBeLessThanOrEqual(1)

  // Every chip of both strips is on the screen: they wrap rather than run off its edge.
  for (const strip of ["Made by", "Which containers"]) {
    const chips = page.getByLabel(strip, { exact: true }).getByRole("button")
    const edges = await chips.evaluateAll((all) =>
      all.map((chip) => {
        const box = chip.getBoundingClientRect()
        return { left: box.left, right: box.right }
      }),
    )
    expect(edges.length).toBeGreaterThan(2)
    for (const edge of edges) {
      expect(edge.left).toBeGreaterThanOrEqual(0)
      expect(edge.right).toBeLessThanOrEqual(390)
    }
  }

  // A network with traffic, one with none and one with nothing on it are one
  // height, and so is every container, on one network or on two.
  await expect(networks.getByText(/↓ .*\/s/).first()).toBeVisible({ timeout: 10_000 })
  for (const list of [networks, containers]) {
    const boxes = await list.getByRole("listitem").evaluateAll((all) =>
      all
        .filter((item) => item.parentElement?.getAttribute("aria-label"))
        .map((item) => {
          const box = item.getBoundingClientRect()
          return { y: box.y, height: box.height }
        }),
    )
    expect(boxes.length).toBeGreaterThan(10)
    expect(new Set(boxes.map((box) => Math.round(box.height))).size).toBe(1)
  }

  // A row opens the network's page.
  await networks.getByRole("link", { name: "shop_backend", exact: true }).click()
  await expect(page).toHaveURL(/\/docker\/networks\/shop_backend$/)
})

test("a reader sees every network and is offered no change", async ({ page }) => {
  await mockNetworks(page)
  await page.route("**/api/v1/auth/session", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        ...user,
        capabilities: ["read"],
        user: { ...user.user, role: "readonly" },
      }),
    }),
  )
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto("/docker/networks")

  const table = page.getByRole("table").first()
  await expect(table.getByRole("link", { name: "shop_backend", exact: true })).toBeVisible()
  await expect(page.getByRole("button", { name: "Create network" })).toHaveCount(0)
  await expect(page.getByRole("button", { name: "Remove unused" })).toHaveCount(0)
  await expect(table.getByRole("button")).toHaveCount(0)
})

test("a host that lists no networks says so, and gives no verdict on none", async ({ page }) => {
  await mockNetworks(page, { networks: [], events: [] })
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto("/docker/networks")

  await expect(page.getByText("No networks", { exact: true })).toBeVisible()
  const identity = page.locator("[data-slot=host-identity]")
  await expect(identity.getByText("0 networks, 0 made here")).toBeVisible()
  await expect(identity.getByText(/Every network in use|unused/)).toHaveCount(0)
  await expect(identity.getByRole("button", { name: "Create network", exact: true })).toBeVisible()
})
