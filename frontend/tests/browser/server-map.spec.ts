import { expect, test, type Page } from "@playwright/test"
import { DEFAULT_DOCUMENT, layoutMap, visibleKinds } from "../../src/lib/server-map"
import { busyMap, freshMap, mockServerMap } from "./server-map-fixture"

/**
 * The server map against a busy server (six projects, fourteen containers,
 * four databases — one not answering — nine domains, failing backup, firing
 * alert) and against a server the dashboard was just installed on.
 */

const node = (page: Page, id: string) => page.locator(`.react-flow__node[data-id="${id}"]`)

async function open(page: Page, options?: Parameters<typeof mockServerMap>[1]) {
  await page.setViewportSize({ width: 1600, height: 1000 })
  const mock = await mockServerMap(page, options)
  await page.goto("/map")
  await expect(page.locator(".react-flow__node").first()).toBeVisible()
  return mock
}

/**
 * Open the map with its view on one box at a readable zoom, worked out with
 * the layout the page itself uses: the overview of a busy server is wider and
 * taller than the window, so a box near its edge may start under the toolbar.
 */
async function openOn(page: Page, id: string, zoom = 0.9) {
  const kinds = visibleKinds(busyMap as never, () => true)
  const laid = layoutMap({ map: busyMap as never, kinds, doc: DEFAULT_DOCUMENT })
  const target = laid.nodes.find((n) => n.id === id)
  if (!target) throw new Error(`${id} is not laid out`)
  const parent = "parentId" in target ? laid.nodes.find((n) => n.id === target.parentId) : undefined
  const x = (parent?.position.x ?? 0) + target.position.x + target.width / 2
  const y = (parent?.position.y ?? 0) + target.position.y + target.height / 2
  // The canvas at 1600×1000, between the rail and the inspector.
  const viewport = { x: 500 - x * zoom, y: 460 - y * zoom, zoom }
  return open(page, { layout: { version: 1, viewport } })
}

/** Where a box is on the screen now. */
async function at(page: Page, id: string) {
  const box = await node(page, id).boundingBox()
  if (!box) throw new Error(`${id} is not drawn`)
  return box
}

/**
 * Zoom in the way a reader does, with the wheel over a box — over its middle,
 * or over the far end of its head where a shelf's buttons are, so they stay
 * under the pointer as it grows.
 */
async function zoomAt(page: Page, id: string, notches = 4, where: "middle" | "head" = "middle") {
  const box = await at(page, id)
  const point =
    where === "head"
      ? { x: box.x + box.width - 8, y: box.y + 6 }
      : { x: box.x + box.width / 2, y: box.y + box.height / 2 }
  await page.mouse.move(point.x, point.y)
  for (let i = 0; i < notches; i++) {
    await page.mouse.wheel(0, -200)
    await page.waitForTimeout(60)
  }
  await expect(page.locator("[data-slot=server-map]")).not.toHaveAttribute("data-zoom", "far")
}

async function drag(page: Page, id: string, dx: number, dy: number, alt = false) {
  // From the middle: far out a box is a few pixels tall, and a corner
  // offset lands on the shelf behind it.
  const from = await at(page, id)
  const x = from.x + from.width / 2
  const y = from.y + from.height / 2
  if (alt) await page.keyboard.down("Alt")
  await page.mouse.move(x, y)
  await page.mouse.down()
  for (let step = 1; step <= 8; step++) {
    await page.mouse.move(x + (dx * step) / 8, y + (dy * step) / 8)
  }
  await page.mouse.up()
  if (alt) await page.keyboard.up("Alt")
}

test("the server is the middle, and every branch grows from it on its own side", async ({
  page,
}) => {
  await open(page)
  const server = await at(page, "server")
  await expect(node(page, "server")).toContainText("atlas")
  for (const [branch, side] of [
    ["deployed", "right"],
    ["web", "right"],
    ["host", "right"],
    ["workspace", "left"],
    ["jobs", "left"],
    ["people", "left"],
    ["protection", "left"],
  ] as const) {
    const box = await at(page, `branch:${branch}`)
    if (side === "right") expect(box.x).toBeGreaterThan(server.x + server.width)
    else expect(box.x + box.width).toBeLessThan(server.x)
  }
  // The toolbar counts what is on the map and what is down.
  await expect(page.getByText("5 down").first()).toBeVisible()
})

test("a long shelf draws two rows and opens the rest on a press, which is remembered", async ({
  page,
}) => {
  const mock = await open(page)
  const shelf = node(page, "shelf:containers")
  await expect(shelf).toContainText("14")
  const more = node(page, "more:containers")
  await expect(more).toContainText("+7")
  await more.getByRole("button").click()
  await expect(node(page, "more:containers")).toHaveCount(0)
  await expect.poll(() => mock.saved.puts).toBeGreaterThan(0)
  expect((mock.saved.layout as { expanded: string[] }).expanded).toContain("shelf:containers")
})

test("folding a branch folds everything on it, and the map comes back folded", async ({ page }) => {
  const mock = await open(page)
  await expect(node(page, "shelf:backups")).toHaveCount(1)
  await zoomAt(page, "branch:jobs")
  await page.getByRole("button", { name: "Fold Jobs" }).click()
  await expect(node(page, "shelf:backups")).toHaveCount(0)
  await expect(node(page, "branch:jobs")).toContainText("4 shelves folded")
  await expect
    .poll(() => (mock.saved.layout as { collapsed?: string[] } | null)?.collapsed ?? [])
    .toContain("branch:jobs")
  await page.reload()
  await expect(node(page, "branch:jobs")).toBeVisible()
  await expect(node(page, "shelf:backups")).toHaveCount(0)
  await zoomAt(page, "branch:jobs")
  await page.getByRole("button", { name: "Unfold Jobs" }).click()
  // Off-screen boxes are not drawn at all, so the branch says it, not the shelf.
  await expect(node(page, "branch:jobs")).not.toContainText("folded")
  await expect
    .poll(() => (mock.saved.layout as { collapsed?: string[] } | null)?.collapsed ?? [])
    .not.toContain("branch:jobs")
})

test("a branch dragged takes its shelves with it; with Alt it moves alone", async ({ page }) => {
  const mock = await open(page)
  const branch = await at(page, "branch:people")
  const shelf = await at(page, "shelf:accounts")
  await drag(page, "branch:people", -60, 80)
  const movedBranch = await at(page, "branch:people")
  const movedShelf = await at(page, "shelf:accounts")
  expect(movedBranch.y - branch.y).toBeCloseTo(movedShelf.y - shelf.y, 0)
  expect(movedShelf.y).toBeGreaterThan(shelf.y + 20)
  await expect
    .poll(() => Object.keys((mock.saved.layout as { positions?: object } | null)?.positions ?? {}))
    .toEqual(expect.arrayContaining(["branch:people", "shelf:accounts"]))

  const before = await at(page, "shelf:keys")
  await drag(page, "branch:people", 0, 60, true)
  const after = await at(page, "shelf:keys")
  expect(after.y).toBeCloseTo(before.y, 0)
})

test("a box pulled off its shelf stays where it is put, and goes back from the inspector", async ({
  page,
}) => {
  const mock = await openOn(page, "drawing:1")
  // Down, off its shelf and over the next one's.
  await drag(page, "drawing:1", 0, 170)
  await expect
    .poll(() => Object.keys((mock.saved.layout as { positions?: object } | null)?.positions ?? {}))
    .toContain("drawing:1")
  // It hangs from its shelf by a line of its own.
  await expect(page.locator('.react-flow__edge[data-id="e:shelf:drawings:drawing:1"]')).toHaveCount(
    1,
  )

  await node(page, "drawing:1").click()
  const inspector = page.getByRole("complementary", { name: "Inspector" })
  await inspector.getByRole("button", { name: "Back on its shelf" }).click()
  await expect
    .poll(() => Object.keys((mock.saved.layout as { positions?: object } | null)?.positions ?? {}))
    .not.toContain("drawing:1")
  await expect(page.locator('.react-flow__edge[data-id="e:shelf:drawings:drawing:1"]')).toHaveCount(
    0,
  )
})

test("choosing a thing draws what it is wired to, and the inspector says each in words", async ({
  page,
}) => {
  await open(page)
  const search = page.getByRole("textbox", { name: "Find anything on the server" })
  await search.fill("mongo-events")
  await search.press("Enter")
  await expect(page.locator(".react-flow__edge-relation")).toHaveCount(3)
  await expect(page.getByText("3 links drawn")).toBeVisible()

  const inspector = page.getByRole("complementary", { name: "Inspector" })
  await expect(inspector).toContainText("mongo-events")
  await expect(inspector).toContainText("exited (1)")
  await expect(
    inspector.getByRole("button", { name: /events.*serves.*not answering/ }),
  ).toBeVisible()
  await expect(inspector.getByRole("link", { name: "Open" })).toHaveAttribute(
    "href",
    "/docker/containers/c-mongo",
  )
  // Following a link chooses the other end.
  await inspector.getByRole("button", { name: /mongo-data/ }).click()
  await expect(inspector).toContainText("1.1 GB")
  // The box chosen is lit, though the search that found the first one still
  // holds its words.
  const chosen = node(page, "volume:mongo-data").locator("[data-selected]")
  await expect(chosen).toHaveCount(1)
  await expect(chosen).not.toHaveClass(/opacity-35/)
  await page.keyboard.press("Escape")
  await expect(page.locator(".react-flow__edge-relation")).toHaveCount(0)
})

test("a thing behind “+N more” is brought onto its shelf when it is chosen", async ({ page }) => {
  await open(page)
  await expect(node(page, "container:c-vault")).toHaveCount(0)
  const search = page.getByRole("textbox", { name: "Find anything on the server" })
  await search.fill("vaultwarden")
  await search.press("Enter")
  await expect(node(page, "container:c-vault")).toBeVisible()
})

test("Add goes where each thing is added, and a drawing is made in place", async ({ page }) => {
  const mock = await open(page)
  await page.getByRole("button", { name: "Add", exact: true }).click()
  const menu = page.getByRole("menu")
  for (const label of ["New project", "Add a database", "Add a site", "New shell", "New drawing"]) {
    await expect(menu.getByRole("menuitem", { name: label })).toBeVisible()
  }
  await menu.getByRole("menuitem", { name: "New drawing" }).click()
  await expect(page).toHaveURL(/\/excalidraw\/41$/, { timeout: 15_000 })
  expect(mock.created).toEqual(["drawing"])

  await page.goto("/map")
  await expect(node(page, "server")).toBeVisible()
  // The same verb in the inspector of the shelf chosen, and the shelf's own +.
  await openOn(page, "shelf:projects", 0.85)
  await node(page, "shelf:projects").getByText("Projects").click()
  const inspector = page.getByRole("complementary", { name: "Inspector" })
  await expect(inspector.getByRole("button", { name: "New project" })).toBeVisible()
  await node(page, "shelf:projects").getByRole("button", { name: "New project" }).click()
  await expect(page).toHaveURL(/\/deploy\/new$/, { timeout: 15_000 })
})

test("a reading role is offered only what it may add: its own API key", async ({ page }) => {
  await page.setViewportSize({ width: 1600, height: 1000 })
  await mockServerMap(page)
  await page.route("**/api/v1/auth/session", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        authenticated: true,
        needsTotp: false,
        needsEnrollment: false,
        require2fa: false,
        capabilities: ["read", "metrics.read"],
        user: { id: 2, username: "viewer", role: "readonly", totpEnabled: true },
      }),
    }),
  )
  const map = {
    ...busyMap,
    kinds: busyMap.kinds.filter(
      (k) =>
        !["accounts", "terminals", "alerts", "systemUsers", "computers", "tunnels"].includes(k.key),
    ),
  }
  await page.route("**/api/v1/map/", (route) =>
    route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(map) }),
  )
  await page.goto("/map")
  await expect(node(page, "server")).toBeVisible()
  await page.getByRole("button", { name: "Add", exact: true }).click()
  await expect(page.getByRole("menuitem")).toHaveCount(1)
  await expect(page.getByRole("menuitem", { name: "New API key" })).toBeVisible()
  await page.keyboard.press("Escape")
  // Far out a head's buttons are not shown; counting the hidden ones too says
  // the head was drawn with its way out and without a way to add.
  const head = node(page, "shelf:projects")
  await expect(
    head.getByRole("button", { name: "Open Projects", includeHidden: true }),
  ).toHaveCount(1)
  await expect(head.getByRole("button", { name: "New project", includeHidden: true })).toHaveCount(
    0,
  )
  await expect(node(page, "shelf:accounts")).toHaveCount(0)
})

test("a fresh server offers the first of each, and a source still being read says so", async ({
  page,
}) => {
  await open(page, { map: freshMap })
  await expect(node(page, "add:projects")).toContainText("New project")
  await expect(node(page, "add:databases")).toContainText("Add a database")
  await expect(node(page, "shelf:certificates")).toContainText("reading…")
  await expect(node(page, "add:certificates")).toHaveCount(0)
  // A module the server does not run has no shelf at all.
  await expect(node(page, "shelf:pm2")).toHaveCount(0)
  await expect(node(page, "shelf:tunnels")).toHaveCount(0)
})

test("from afar a thing is its mark and its dot, and the names that find it stay readable", async ({
  page,
}) => {
  await open(page)
  const canvas = page.locator("[data-slot=server-map]")
  await page.getByRole("button", { name: "Fit" }).click()
  await expect(canvas).toHaveAttribute("data-zoom", "far")
  const label = node(page, "branch:deployed").getByText("Deployed")
  const box = await label.boundingBox()
  // Held to the screen: a 15px title at a third of its size would be 5px.
  expect(box!.height).toBeGreaterThan(10)
  await expect(node(page, "container:c-api").getByText("api-1")).toBeHidden()
})
