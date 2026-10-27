import { expect, test, type Locator, type Page } from "@playwright/test"
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

/** How large a piece of the map's text is on the screen: its size times the zoom. */
async function onScreen(locator: Locator) {
  return locator.evaluate((el) => {
    const viewport = el.closest(".react-flow__viewport")
    const zoom = viewport ? new DOMMatrix(getComputedStyle(viewport).transform).a : 1
    return parseFloat(getComputedStyle(el).fontSize) * zoom
  })
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

test("a box pulled off its shelf lands on clear ground, hangs from its shelf, and goes back", async ({
  page,
}) => {
  const mock = await openOn(page, "drawing:1")
  const from = await at(page, "drawing:1")
  const grip = { x: from.x + from.width / 2, y: from.y + from.height / 2 }
  await page.mouse.move(grip.x, grip.y)
  await page.mouse.down()
  for (let step = 1; step <= 10; step++) await page.mouse.move(grip.x, grip.y + step * 17)
  // While it is in the hand its shelf says where it belongs.
  await expect(node(page, "shelf:drawings").locator("[data-home=away]")).toHaveCount(1)
  await expect(node(page, "shelf:drawings")).toContainText("Drop here to put it back")
  // Let go over the next shelf down: it slides off it rather than covering
  // that shelf's things.
  await page.mouse.up()
  await expect
    .poll(() => Object.keys((mock.saved.layout as { positions?: object } | null)?.positions ?? {}))
    .toContain("drawing:1")
  await expect(node(page, "shelf:drawings").locator("[data-home]")).toHaveCount(0)
  const landed = await at(page, "drawing:1")
  const next = await at(page, "shelf:repositories")
  const covers =
    landed.x < next.x + next.width &&
    next.x < landed.x + landed.width &&
    landed.y < next.y + next.height &&
    next.y < landed.y + landed.height
  expect(covers).toBe(false)
  // It hangs from its shelf by a line of its own.
  await expect(page.locator('.react-flow__edge[data-id="e:shelf:drawings:drawing:1"]')).toHaveCount(
    1,
  )

  // Chosen, the inspector offers the way back; the box carries it too.
  await node(page, "drawing:1").click()
  const inspector = page.getByRole("complementary", { name: "Inspector" })
  await expect(inspector.getByRole("button", { name: "Put it back on its shelf" })).toBeVisible()
  await node(page, "drawing:1").hover()
  await node(page, "drawing:1")
    .getByRole("button", { name: "Put Network plan back on its shelf" })
    .click()
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
    inspector.getByRole("button", { name: /events.*not answering.*serves/ }),
  ).toBeVisible()
  await expect(inspector.getByRole("link", { name: "Open container" })).toHaveAttribute(
    "href",
    "/docker/containers/c-mongo",
  )
  // Its details are the facts its own page leads with.
  await expect(inspector.getByRole("definition").filter({ hasText: "mongo:8.0" })).toBeVisible()
  // Pointing at a link rings the box at its other end on the canvas.
  const row = inspector.getByRole("button", { name: /mongo-data/ })
  await row.hover()
  await expect(node(page, "volume:mongo-data").locator("[data-hovered]")).toHaveCount(1)
  await page.mouse.move(5, 500)
  await expect(node(page, "volume:mongo-data").locator("[data-hovered]")).toHaveCount(0)
  // Following a link chooses the other end.
  await row.click()
  await expect(inspector).toContainText("1.1 GB")
  // The box chosen is lit, though the search that found the first one still
  // holds its words.
  const chosen = node(page, "volume:mongo-data").locator("[data-selected]")
  await expect(chosen).toHaveCount(1)
  await expect(chosen).not.toHaveClass(/opacity-35/)
  await page.keyboard.press("Escape")
  await expect(page.locator(".react-flow__edge-relation")).toHaveCount(0)
})

test("a deployment is wired to the database its env names and the checkout it is built from", async ({
  page,
}) => {
  await openOn(page, "project:3")
  await node(page, "project:3").click()
  const inspector = page.getByRole("complementary", { name: "Inspector" })
  // No binding: the server saw its environment name the database.
  await expect(
    inspector.getByRole("button", { name: /analytics.*answers.*names in env/ }),
  ).toBeVisible()
  await expect(page.locator(".react-flow__edge-relation")).toHaveCount(3)
  await expect(page.locator(".react-flow__edgelabel-renderer")).toContainText("names in env")

  // A pair known several ways is one line, named by the surest of them.
  await page.keyboard.press("Escape")
  const search = page.getByRole("textbox", { name: "Find anything on the server" })
  await search.fill("nightly-databases")
  await search.press("Enter")
  await expect(inspector).toContainText("nightly-databases")
  await inspector.getByRole("button", { name: /^shop.*answers/ }).click()
  const bound = inspector.getByRole("button", { name: /^shop.*ready.*bound to it/ })
  await expect(bound).toBeVisible()
  await expect(bound.locator("[data-slot=tag]")).toHaveAttribute(
    "title",
    "bound to it · referred to by · in its env",
  )
  await expect(inspector.getByRole("button", { name: /^blog.*referred to by/ })).toBeVisible()
  await expect(inspector.getByRole("button", { name: /claude-code.*reached by/ })).toBeVisible()
  await bound.click()
  await expect(
    inspector.getByRole("button", { name: /^shop.*3 changes.*built from/ }),
  ).toBeVisible()
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

test("from afar a thing is its mark, its name and its dot, and every name stays readable", async ({
  page,
}) => {
  await open(page)
  const canvas = page.locator("[data-slot=server-map]")
  await page.getByRole("button", { name: "Fit" }).click()
  await expect(canvas).toHaveAttribute("data-zoom", "far")
  const label = node(page, "branch:deployed").getByText("Deployed")
  const box = await label.boundingBox()
  // Held to the screen: a 16px name at a fifth of its size would be 3px.
  expect(box!.height).toBeGreaterThan(10)
  // A thing keeps its name, grown to read at 11px, and its mark beside it
  // grown with it — the logo is what the overview is read by — and gives up
  // its second line and its tile for the room.
  const api = node(page, "container:c-api")
  await expect(api.locator("[data-slot=map-name]")).toHaveText("api-1")
  expect(await onScreen(api.locator("[data-slot=map-name]"))).toBeGreaterThan(10.5)
  await expect(api.getByText("running", { exact: true })).toBeHidden()
  await expect(api.locator("img, svg").first()).toBeHidden()
  const mark = api.locator("[data-slot=map-mark]")
  await expect(mark.locator("img")).toHaveAttribute("src", "/logos/python.svg")
  expect((await mark.boundingBox())!.height).toBeGreaterThan(10)
  // A shelf's name reads at least as large as the things on it.
  const shelf = node(page, "shelf:containers").getByText("Containers", { exact: true })
  expect(await onScreen(shelf)).toBeGreaterThan(10.5)
})

test("a little way out a thing is its mark, its name and its dot", async ({ page }) => {
  await openOn(page, "container:c-api", 0.5)
  await expect(page.locator("[data-slot=server-map]")).toHaveAttribute("data-zoom", "mid")
  const api = node(page, "container:c-api")
  const name = api.locator("[data-slot=map-name]")
  await expect(name).toBeVisible()
  // 14px at half size would be 7px; it is held at 11.
  expect(await onScreen(name)).toBeGreaterThan(10.5)
  await expect(api.locator("img, svg").first()).toBeVisible()
  await expect(api.getByText("running", { exact: true })).toBeHidden()
  // A shelf's name has its head's width; its ways in show when it is pointed at.
  const head = node(page, "shelf:containers")
  const openShelf = head.getByRole("button", { name: "Open Containers" })
  await expect(openShelf).toBeHidden()
  await head.getByText("Containers", { exact: true }).hover()
  await expect(openShelf).toBeVisible()
})

test("a database is a card, its engine on a line of its own under its state", async ({ page }) => {
  await openOn(page, "db:1")
  const shop = node(page, "db:1")
  await expect(shop.locator("[data-face=card]")).toBeVisible()
  const name = await shop.getByText("shop", { exact: true }).boundingBox()
  const state = await shop.getByText("answers", { exact: true }).boundingBox()
  const fact = await shop.getByText("postgres 17.2", { exact: true }).boundingBox()
  expect(state!.y).toBeGreaterThan(name!.y)
  expect(fact!.y).toBeGreaterThan(state!.y + state!.height - 1)
  // Three cards span what four of anything else do, so the branch keeps one edge.
  const card = await shop.boundingBox()
  const row = await node(page, "container:c-api").boundingBox()
  expect(3 * card!.width).toBeGreaterThan(4 * row!.width)
  await expect(node(page, "container:c-api").locator("[data-face=card]")).toHaveCount(0)
})

test("every branch is drawn in its own hue, and every box as the thing it is", async ({ page }) => {
  await open(page)
  // A shelf, its things and the lines to it name their branch, and the hue
  // follows from it: Jobs' lines are not People's.
  await expect(node(page, "shelf:backups").locator("[data-branch]").first()).toHaveAttribute(
    "data-branch",
    "jobs",
  )
  const stroke = (id: string) =>
    page
      .getByTestId(`rf__edge-${id}`)
      .locator(".react-flow__edge-path")
      .evaluate((el) => getComputedStyle(el).stroke)
  const jobs = await stroke("e:branch:jobs:shelf:backups")
  const people = await stroke("e:branch:people:shelf:accounts")
  expect(jobs).not.toBe(people)
  expect(jobs).not.toBe("none")
  // While the server's readings stream, a branch's line carries a pulse out
  // from the heart; a shelf's line does not.
  await expect(
    page.getByTestId("rf__edge-e:server:branch:deployed").locator("[data-slot=map-pulse]"),
  ).toHaveCount(1)
  await expect(
    page.getByTestId("rf__edge-e:branch:jobs:shelf:backups").locator("[data-slot=map-pulse]"),
  ).toHaveCount(0)
  // Every box is the product it is where the dashboard has the mark: a Mac
  // is Apple's, a volume Docker's, a key named for GitHub GitHub's, a
  // domain the proxy's that answers for it, and a person is a face.
  const logo = (id: string) => node(page, id).locator("[data-slot=map-mark] img")
  await expect(logo("computer:a")).toHaveAttribute("src", "/logos/apple.svg")
  await expect(logo("volume:grafana")).toHaveAttribute("src", "/logos/docker.svg")
  await expect(logo("key:1")).toHaveAttribute("src", "/logos/github.svg")
  await expect(logo("domain:api.example.com")).toHaveAttribute("src", "/logos/nginx.svg")
  await expect(node(page, "account:1").locator("[data-slot=user-avatar]").first()).toBeAttached()
  // A thing that is no product is its kind's glyph in its branch's hue.
  const alert = node(page, "alert:host:1")
  await expect(alert.locator("[data-branch=jobs]").first()).toBeAttached()
})
