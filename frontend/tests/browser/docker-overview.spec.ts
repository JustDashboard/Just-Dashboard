import { expect, test, type Page } from "@playwright/test"
import { DOCKER_CONTAINERS, DOCKER_STACKS } from "./docker-fixture"
import { ENGINE_NETWORKS, mockDockerEngine } from "./docker-engine-fixture"
import { risen } from "./risen"

/**
 * The Docker overview: the engine's identity line with its verdict, the band
 * of what the containers use and what just happened to them, the engine as a
 * picture of what it holds, and the compose projects beside the disk.
 *
 * The host behind it has two things wrong — the shop's worker was killed for
 * memory and Uptime Kuma is failing its health check — and one container
 * stopped by hand, which is not a problem and must not read as one.
 */

const id = (name: string) => DOCKER_CONTAINERS.find((c) => c.name === name)!.id
const networkId = (name: string) => ENGINE_NETWORKS.find((n) => n.name === name)!.id

const picture = (page: Page) => page.locator("[data-slot=wire-flow]")
const drawn = (page: Page) => page.locator("[data-slot=wire-flow]:not([data-loading])")
const node = (page: Page, key: string) => page.locator(`[data-node="${key}"]`)

/** A press that leaves the page loads the next route's code first. */
const NAVIGATION = { timeout: 15_000 }

test("the overview opens on the engine and its verdict, with no tiles and no table", async ({
  page,
}) => {
  await mockDockerEngine(page)
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto("/docker")

  const identity = page.locator("[data-slot='host-identity']")
  await expect(identity).toHaveAttribute("data-layout", "project")
  await expect(identity).toContainText("Docker 29.2.0")
  await expect(identity.getByText("srv-1")).toBeVisible()
  await expect(identity.getByText("overlay2 on cgroup v2")).toBeVisible()
  await expect(identity.getByText("10 of 12 containers running")).toBeVisible()
  await expect(identity.getByText("3 of 4 projects up")).toBeVisible()
  await expect(identity.getByText("2 containers failing")).toBeVisible()
  await expect(identity.getByRole("link", { name: "Open Deployments" })).toBeVisible()

  await expect(drawn(page)).toBeVisible()
  await expect(page.locator("[data-slot='stat-tile']")).toHaveCount(0)
  await expect(page.locator("[data-slot='stat-grid']")).toHaveCount(0)
  // The table of every container is the Containers page's, not a second copy here.
  await expect(page.getByRole("table")).toHaveCount(0)
})

test("the verdict opens the containers table narrowed to what is failing", async ({ page }) => {
  await mockDockerEngine(page)
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto("/docker")

  await page.getByRole("button", { name: "2 containers failing" }).click()
  await expect(page).toHaveURL(/\/docker\/containers$/, NAVIGATION)
  await expect(page.getByRole("button", { name: /^Failing/ })).toHaveAttribute(
    "aria-pressed",
    "true",
  )
  await expect(page.getByRole("table").locator("tbody tr")).toHaveCount(2)
})

test("the section's title leads to the table of every container", async ({ page }) => {
  await mockDockerEngine(page)
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto("/docker")

  const section = page.getByRole("region", { name: "What the engine holds" })
  await expect(section.getByRole("heading")).toContainText("Containers")
  await expect(section.getByRole("heading")).toContainText("12")
  await section.getByRole("link", { name: "All containers" }).click()
  await expect(page).toHaveURL(/\/docker\/containers$/, NAVIGATION)
  await expect(page.getByRole("table").locator("tbody tr")).toHaveCount(12)
})

test("the picture draws the projects and loose containers, the engine, and what it keeps", async ({
  page,
}) => {
  await mockDockerEngine(page)
  await page.setViewportSize({ width: 1440, height: 1200 })
  await page.goto("/docker")
  await expect(drawn(page)).toBeVisible()

  // Failing first, projects before the containers started by hand, then what is down.
  const left = page.getByRole("region", { name: "Stacks and containers" })
  const order = await left
    .locator("[data-node]")
    .evaluateAll((all) => all.map((each) => each.getAttribute("data-node")))
  expect(order).toEqual([
    "stack:monitoring",
    "stack:shop",
    "stack:automation",
    `container:${id("portainer")}`,
    `container:${id("mailhog")}`,
    "stack:blog",
  ])
  await expect(node(page, "stack:shop")).toContainText("4 of 5 running · 1 failing")
  await expect(node(page, "stack:monitoring")).toContainText("4 of 4 running · 1 failing")
  await expect(node(page, "stack:automation")).toContainText("1 of 1 running")
  // Stopped by hand is quiet, and a compose file never brought up says so.
  await expect(node(page, `container:${id("mailhog")}`)).toContainText("stopped · 2 days ago")
  await expect(node(page, "stack:blog")).toContainText("not deployed · 2 services defined")

  await expect(picture(page).getByText("Engine", { exact: true })).toBeVisible()
  await expect(picture(page).getByText("10 of 12 containers running")).toBeVisible()

  // The networks carrying the most first, Docker's empty `host` and `none`
  // left out, and what there is no room for counted.
  const right = page.getByRole("region", { name: "Networks and volumes" })
  await expect(node(page, `network:${networkId("shop_default")}`)).toContainText("5 containers")
  await expect(node(page, `network:${networkId("bridge")}`)).toContainText("2 containers")
  await expect(right.locator(`[data-node="network:${networkId("host")}"]`)).toHaveCount(0)
  const moreNetworks = right.getByRole("link", { name: "2 more networks" })
  await expect(moreNetworks).toBeVisible()
  // The ones not drawn are named under the count.
  await expect(right).toContainText("automation_default, wiki_default")
  // The volume nothing mounts leads, in the words of what a prune would do.
  await expect(right.locator("[data-node^='volume:']").first()).toContainText("nothing mounts it")
  await expect(node(page, "volume:shop_pgdata")).toContainText("shop-db")
  await expect(right.getByRole("link", { name: "2 more volumes" })).toBeVisible()
})

test("pointing at a project lights what is its own, and a node opens its page", async ({
  page,
}) => {
  await mockDockerEngine(page)
  await page.setViewportSize({ width: 1440, height: 1200 })
  await page.goto("/docker")
  await expect(drawn(page)).toBeVisible()

  await node(page, "stack:shop").hover()
  await expect(node(page, `network:${networkId("shop_default")}`)).not.toHaveAttribute("data-dim")
  await expect(node(page, "volume:shop_pgdata")).not.toHaveAttribute("data-dim")
  await expect(node(page, `network:${networkId("monitoring_default")}`)).toHaveAttribute(
    "data-dim",
    "",
  )
  await expect(node(page, "stack:monitoring")).toHaveAttribute("data-dim", "")
  await page.mouse.move(0, 0)
  await expect(node(page, "stack:monitoring")).not.toHaveAttribute("data-dim")

  await picture(page).getByRole("link", { name: "Open shop", exact: true }).click()
  await expect(page).toHaveURL(/\/docker\/stacks\/shop$/, NAVIGATION)
  await page.goBack()
  await picture(page).getByRole("link", { name: "Open portainer" }).click()
  await expect(page).toHaveURL(new RegExp(`/docker/containers/${id("portainer")}$`), NAVIGATION)
  await page.goBack()
  await picture(page).getByRole("link", { name: "2 more networks" }).click()
  await expect(page).toHaveURL(/\/docker\/networks$/, NAVIGATION)
})

for (const width of [1280, 390]) {
  test(`the page has its shape before anything is read, and nothing moves when it lands at ${width}px`, async ({
    page,
  }) => {
    await mockDockerEngine(page)
    let land!: () => void
    const held = new Promise<void>((resolve) => (land = resolve))
    for (const path of ["containers/", "stacks/", "networks/", "volumes/"]) {
      await page.route(`**/api/v1/docker/${path}`, async (route) => {
        await held
        await route.fallback()
      })
    }
    await page.routeWebSocket(/\/api\/v1\/docker\/containers\/stream/, async (socket) => {
      await held
      socket.send(JSON.stringify({ type: "containers", data: DOCKER_CONTAINERS, ts: 0 }))
    })
    // The second visit: the first learnt how many rows the picture holds here.
    await page.addInitScript(() =>
      localStorage.setItem(
        "jd.view.state.entry.docker.overview.picture.rows",
        JSON.stringify({ start: 6, end: 8 }),
      ),
    )
    await page.setViewportSize({ width, height: 900 })
    await page.goto("/docker")

    const identity = page.locator("[data-slot='host-identity']")
    const band = page.locator("[data-slot=container-band]")
    const projects = page.getByRole("heading", { name: /Compose projects/ })
    await expect(page.locator("[data-slot=wire-flow][data-loading]")).toBeVisible()
    await expect(band.locator("[data-slot=bar-reading]")).toHaveCount(3)
    await expect(page.getByLabel("Processor by container")).toContainText("Reading the containers…")
    await risen(page)
    const before = {
      identity: await identity.boundingBox(),
      band: await band.boundingBox(),
      picture: await picture(page).boundingBox(),
      projects: await projects.boundingBox(),
    }

    land()
    await expect(drawn(page)).toBeVisible()
    await expect(identity.getByText("2 containers failing")).toBeVisible()
    await expect(page.locator("[data-slot=choice-row]").first()).toBeVisible()
    await risen(page)
    expect(await identity.boundingBox()).toEqual(before.identity)
    expect(await band.boundingBox()).toEqual(before.band)
    expect(await picture(page).boundingBox()).toEqual(before.picture)
    // The projects' title gains its count; where it stands is what must not change.
    const after = await projects.boundingBox()
    expect([after!.x, after!.y]).toEqual([before.projects!.x, before.projects!.y])
  })
}

test("the band names the busiest containers and the last thing each did", async ({ page }) => {
  await mockDockerEngine(page)
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto("/docker")

  const processor = page.getByLabel("Processor by container")
  await expect(processor.getByRole("button").first()).toHaveAccessibleName("Open shop-api")
  const memory = page.getByLabel("Memory by container")
  await expect(memory.getByRole("button").first()).toHaveAccessibleName(
    "Open monitoring-prometheus",
  )

  const recent = page.getByLabel("Recent changes")
  const line = recent.locator("[data-slot=bar-reading-line]")
  // An OOM kill and its exit are one line, and the crash before it is counted.
  await expect(line).toContainText("ran out of memory")
  await expect(line).toContainText("· 2 crashes")
  await recent.getByRole("button", { name: "Open monitoring-uptime-kuma" }).hover()
  await expect(line).toContainText("failing its health check")
  // Two days ago is not recent.
  await expect(recent.getByRole("button", { name: "Open mailhog" })).toHaveCount(0)

  await processor.getByRole("button", { name: "Open automation-n8n" }).click()
  await expect(page).toHaveURL(
    new RegExp(`/docker/containers/${id("automation-n8n")}$`),
    NAVIGATION,
  )
})

test("every block of the band is one height, whatever it has to say", async ({ page }) => {
  await mockDockerEngine(page)
  // Nothing has been measured yet: the blocks say so, and the page must not
  // move when the readings land.
  await page.routeWebSocket(/\/api\/v1\/docker\/containers\/stream/, () => {})
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto("/docker")

  const band = page.locator("[data-slot=container-band]")
  const blocks = band.locator("[data-slot=bar-reading]")
  await expect(blocks).toHaveCount(3)
  await risen(page)
  const heights = await blocks.evaluateAll((all) =>
    all.map((block) => block.getBoundingClientRect().height),
  )
  expect(new Set(heights).size).toBe(1)
})

test("reading another part changes the line and nothing else on the page", async ({ page }) => {
  await mockDockerEngine(page)
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto("/docker")

  const band = page.locator("[data-slot=container-band]")
  const processor = page.getByLabel("Processor by container")
  const line = processor.locator("[data-slot=bar-reading-line]")
  await expect(line).toContainText("shop-api")
  await expect(drawn(page)).toBeVisible()
  await risen(page)
  const before = await band.boundingBox()
  const pictureBefore = await picture(page).boundingBox()

  await processor.getByRole("button", { name: "Open automation-n8n" }).hover()
  await expect(line).toContainText("automation-n8n")
  expect(await band.boundingBox()).toEqual(before)
  expect(await picture(page).boundingBox()).toEqual(pictureBefore)

  // Off the block, the line goes back to the heaviest.
  await page.mouse.move(0, 0)
  await expect(line).toContainText("shop-api")
})

test("a reading that moves draws its trail on the line, and the order holds", async ({ page }) => {
  await mockDockerEngine(page, { tick: 200 })
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto("/docker")

  const processor = page.getByLabel("Processor by container")
  const marks = () =>
    processor
      .locator("[data-slot=reading-strip] button")
      .evaluateAll((all) => all.map((mark) => mark.getAttribute("aria-label")))
  const line = processor.locator("[data-slot=bar-reading-line]")
  await expect(line.locator("svg[viewBox='0 0 56 16']")).toBeVisible()

  // A dozen frames that each wobble a figure: nobody swaps places over a wobble.
  const first = await marks()
  await page.waitForTimeout(2400)
  expect(await marks()).toEqual(first)
})

test("an event that lands while the page is open joins Recent", async ({ page }) => {
  const mocks = await mockDockerEngine(page)
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto("/docker")

  const recent = page.getByLabel("Recent changes")
  await expect(recent.getByRole("button", { name: "Open monitoring-uptime-kuma" })).toBeVisible()
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
  const mark = recent.getByRole("button", { name: "Open monitoring-grafana" })
  await mark.hover()
  const line = recent.locator("[data-slot=bar-reading-line]")
  await expect(line).toContainText("restarted")
  await expect(line).toContainText("just now")
})

for (const width of [1280, 390]) {
  test(`the compose projects are cards of one height that open their stack at ${width}px`, async ({
    page,
  }) => {
    await mockDockerEngine(page)
    await page.setViewportSize({ width, height: 900 })
    await page.goto("/docker")

    const cards = page
      .getByRole("list", { name: "Compose projects" })
      .locator("[data-slot=choice-row]")
    await expect(cards).toHaveCount(DOCKER_STACKS.length)
    await risen(page)
    // A project with five services, one with one and one never deployed: one height.
    const boxes = await cards.evaluateAll((all) =>
      all.map((card) => {
        const box = card.getBoundingClientRect()
        return { height: box.height, top: box.top }
      }),
    )
    expect(new Set(boxes.map((box) => box.height)).size).toBe(1)
    const steps = boxes.slice(1).map((box, index) => Math.round(box.top - boxes[index].top))
    expect(new Set(steps).size).toBe(1)

    const shop = cards.filter({ hasText: "shop" })
    // The strip of its services' states is drawn where there is room for it.
    await expect(shop.getByRole("img", { name: /worker/ })).toHaveCount(width === 390 ? 0 : 1)
    await expect(shop).toContainText("Partially running")
    await shop.getByRole("link", { name: "Open shop" }).click()
    await expect(page).toHaveURL(/\/docker\/stacks\/shop$/, NAVIGATION)
  })
}

test("on a phone the picture keeps its words and nothing is cut off", async ({ page }) => {
  await mockDockerEngine(page)
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto("/docker")
  await expect(drawn(page)).toBeVisible()

  await expect(node(page, "stack:shop")).toContainText("4 of 5 running · 1 failing")
  await expect(node(page, "volume:shop_pgdata")).toBeVisible()
  await expect(page.locator("[data-slot='host-identity']")).toContainText("2 containers failing")
  for (const text of ["shop", "monitoring", "portainer"]) {
    const box = await picture(page).getByText(text, { exact: true }).first().boundingBox()
    expect(box!.x).toBeGreaterThanOrEqual(0)
    expect(box!.x + box!.width).toBeLessThanOrEqual(390)
  }
  const overflow = await page.evaluate(
    () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
  )
  expect(overflow).toBeLessThanOrEqual(1)
})

test("a read-only account sees no command it cannot use", async ({ page }) => {
  await mockDockerEngine(page)
  await page.route("**/api/v1/auth/session", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        authenticated: true,
        needsTotp: false,
        needsEnrollment: false,
        require2fa: false,
        capabilities: ["read"],
        user: {
          id: 2,
          username: "reader",
          role: "readonly",
          totpEnabled: true,
          disabled: false,
          mustChangePassword: false,
          lastLoginAt: new Date().toISOString(),
          createdAt: new Date().toISOString(),
        },
      }),
    }),
  )
  await page.setViewportSize({ width: 1280, height: 900 })
  await page.goto("/docker")
  await expect(drawn(page)).toBeVisible()

  await expect(page.getByRole("link", { name: "Open Deployments" })).toHaveCount(0)
  await expect(page.getByRole("button", { name: /Clean up|Reclaim/ })).toHaveCount(0)
  // Reading is still whole: the picture, its links and the projects.
  await expect(picture(page).getByRole("link", { name: "Open shop", exact: true })).toBeVisible()
  await expect(page.getByRole("list", { name: "Compose projects" })).toBeVisible()
})

test("networks and volumes that cannot be read leave the rest of the picture standing", async ({
  page,
}) => {
  await mockDockerEngine(page, { refuse: 403 })
  await page.setViewportSize({ width: 1280, height: 900 })
  await page.goto("/docker")
  await expect(drawn(page)).toBeVisible()

  await expect(node(page, "stack:shop")).toBeVisible()
  await expect(page.getByRole("region", { name: "Networks and volumes" })).toContainText(
    "No networks or volumes to read",
  )
})

test("a server with nothing on it teaches rather than reports", async ({ page }) => {
  await mockDockerEngine(page, { containers: [], stacks: [], networks: [], volumes: [] })
  await page.setViewportSize({ width: 1280, height: 900 })
  await page.goto("/docker")

  await expect(page.getByText("Nothing is running on this server yet")).toBeVisible()
  await expect(page.getByText("Start from a template")).toBeVisible()
  await expect(picture(page)).toHaveCount(0)
})

test("a host where nothing is failing says so", async ({ page }) => {
  await mockDockerEngine(page, {
    containers: DOCKER_CONTAINERS.filter(
      (c) => c.name !== "shop-worker" && c.name !== "monitoring-uptime-kuma",
    ),
  })
  await page.setViewportSize({ width: 1280, height: 900 })
  await page.goto("/docker")

  const identity = page.locator("[data-slot='host-identity']")
  await expect(identity.getByText("Nothing failing")).toBeVisible()
  await expect(identity.getByRole("button", { name: /failing/ })).toHaveCount(0)
})

test("containers that cannot be read leave the page on Docker's line with a way to try again", async ({
  page,
}) => {
  await mockDockerEngine(page)
  let failing = true
  await page.routeWebSocket(/\/api\/v1\/docker\/containers\/stream/, () => {})
  await page.route("**/api/v1/docker/containers/", async (route) => {
    if (!failing) return route.fallback()
    await route.fulfill({
      status: 500,
      contentType: "application/json",
      body: JSON.stringify({ error: { code: "internal", message: "docker: timeout" } }),
    })
  })
  await page.setViewportSize({ width: 1280, height: 900 })
  await page.goto("/docker")

  const identity = page.locator("[data-slot='host-identity']")
  await expect(identity).toContainText("Docker")
  await expect(identity).toContainText("The containers could not be read")
  await expect(page.locator("[data-slot=error-state]")).toContainText("docker: timeout")

  failing = false
  await page.getByRole("button", { name: "Try again" }).click()
  await expect(drawn(page)).toBeVisible()
  await expect(identity).toContainText("10 of 12 containers running")
})
