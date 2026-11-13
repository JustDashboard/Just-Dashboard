import { expect, test, type Page } from "@playwright/test"
import type { MapItem } from "@/lib/types"
import { busyMap, freshMap, mockServerMap } from "./server-map-fixture"

/**
 * The server map against a busy server — this dashboard, six deployments, two
 * stacks nobody deployed, PM2, native services, containers on their own and
 * what nothing claims — and against a server the dashboard was just
 * installed on.
 */

const node = (page: Page, id: string) => page.locator(`.react-flow__node[data-id="${id}"]`)
const frame = (page: Page, system: string) => node(page, `frame:${system}`)
const rail = (page: Page) => page.getByRole("navigation", { name: "Systems" })
const inspector = (page: Page) => page.getByRole("complementary", { name: "Inspector" })
const railRow = (page: Page, system: string) => rail(page).locator(`[data-system="${system}"]`)

async function open(page: Page, path = "/map", options?: Parameters<typeof mockServerMap>[1]) {
  await page.setViewportSize({ width: 1600, height: 1000 })
  const mock = await mockServerMap(page, options)
  await page.goto(path)
  await expect(page.locator(".react-flow__node").first()).toBeVisible()
  return mock
}

/** Where the canvas is and how far in: the transform React Flow puts on its viewport. */
const view = (page: Page) =>
  page.locator(".react-flow__viewport").evaluate((el) => {
    const m = new DOMMatrix(getComputedStyle(el).transform)
    return { x: m.e, y: m.f, zoom: m.a }
  })

/** Waits out a flight: the canvas has stopped moving. */
async function still(page: Page) {
  await expect
    .poll(async () => {
      const a = await view(page)
      await page.waitForTimeout(120)
      return JSON.stringify(a) === JSON.stringify(await view(page))
    })
    .toBe(true)
}

async function at(page: Page, id: string) {
  const box = await node(page, id).boundingBox()
  if (!box) throw new Error(`${id} is not drawn`)
  return box
}

test("every system is its own diagram, listed in the rail by what anchors it", async ({ page }) => {
  await open(page)
  // Seventeen systems at once are a scatter of boxes: the page opens on one.
  await page.getByRole("button", { name: "All", exact: true }).click()
  await still(page)
  for (const [group, names] of [
    ["This dashboard", ["Just Dashboard"]],
    ["Deployments", ["billing", "blog", "api", "n8n", "shop", "docs"]],
    ["Compose stacks", ["data", "monitoring"]],
    ["Processes", ["scraper", "telegram-bot", "gitea"]],
    ["On their own", ["metabase", "uptime-kuma", "vaultwarden", "legacy"]],
    ["Not wired", ["Everything else"]],
  ] as const) {
    const section = rail(page).locator("section", {
      has: page.getByRole("heading", { name: group }),
    })
    for (const name of names) await expect(section.getByRole("button", { name })).toBeVisible()
  }
  for (const system of [
    "system:dashboard:self",
    "system:project:1",
    "system:stack:monitoring",
    "system:pm2:d1:0",
    "system:db:5",
    "system:unit:gitea.service",
    "system:container:c-kuma",
    "system:loose",
  ]) {
    await expect(frame(page, system)).toHaveCount(1)
  }
  await expect(node(page, "system:loose/volume:old-uploads")).toHaveCount(0)
})

test("a deployment's diagram runs from its domains through its containers to its data and backups", async ({
  page,
}) => {
  await open(page, "/map?system=system:project:1")
  const s = "system:project:1"
  const domain = await at(page, `${s}/domain:shop.example.com`)
  const project = await at(page, `${s}/project:1`)
  const web = await at(page, `${s}/container:c-shop-web`)
  const db = await at(page, `${s}/db:1`)
  const backup = await at(page, `${s}/backup:1`)
  expect(domain.x).toBeLessThan(project.x)
  expect(project.x).toBeLessThan(web.x)
  expect(web.x).toBeLessThan(db.x)
  expect(db.x).toBeLessThan(backup.x)
  // Its release runs as a stack: one frame round both containers.
  const stack = await at(page, `${s}/frame:stack:jd-shop`)
  expect(stack.y).toBeLessThan(web.y)
  expect(stack.y + stack.height).toBeGreaterThan(web.y + web.height)
  await expect(node(page, `${s}/frame:stack:jd-shop`)).toContainText("jd-shop")
  // What hangs under it: its checkout and its logs, and each domain's certificate.
  const source = node(page, `${s}/project:1~repo:/srv/shop`)
  await expect(source).toContainText("shop")
  expect((await at(page, `${s}/project:1~repo:/srv/shop`)).y).toBeGreaterThan(
    project.y + project.height,
  )
  await expect(node(page, `${s}/project:1~logs:project:1`)).toContainText("Logs")
  await expect(
    node(page, `${s}/domain:shop.example.com~cert:/etc/letsencrypt/live/shop`),
  ).toBeVisible()
  // Each shape is its own.
  await expect(node(page, `${s}/project:1`)).toHaveClass(/react-flow__node-hero/)
  await expect(node(page, `${s}/domain:shop.example.com`)).toHaveClass(/react-flow__node-entry/)
  await expect(node(page, `${s}/db:1`)).toHaveClass(/react-flow__node-datastore/)
  await expect(node(page, `${s}/volume:shop-pg-data`)).toHaveClass(/react-flow__node-volume/)
  await expect(node(page, `${s}/container:c-shop-web`)).toContainText("Web · container")
  await expect(node(page, `${s}/container:c-shop-worker`)).toContainText("Worker · container")
  // What the map leaves to its own pages is not on it.
  await expect(page.locator('.react-flow__node[data-id*="image:"]')).toHaveCount(0)
  await expect(page.locator('.react-flow__node[data-id*="key:"]')).toHaveCount(0)
})

test("this dashboard is drawn as a system: its tailnet, its address, its stack and its store", async ({
  page,
}) => {
  await open(page, "/map?system=system:dashboard:self")
  const s = "system:dashboard:self"
  const tailnet = await at(page, `${s}/vpn:tailscale`)
  const site = await at(page, `${s}/dashboard:site`)
  const hero = await at(page, `${s}/dashboard:self`)
  expect(tailnet.x).toBeLessThan(site.x)
  expect(site.x).toBeLessThan(hero.x)
  await expect(node(page, `${s}/frame:stack:just-dashboard`)).toBeVisible()
  await expect(node(page, `${s}/container:c-jd-caddy`)).toContainText("Proxy · container")
  await expect(node(page, `${s}/container:c-jd-frontend`)).toContainText("Web · container")
  await expect(node(page, `${s}/container:c-jd-backend`)).toContainText("API · container")
  await expect(node(page, `${s}/dashboard:store`)).toContainText("Dashboard store")
  await expect(node(page, `${s}/backup:3`)).toContainText("dashboard data")
})

test("choosing a box lights its wiring and the inspector names each line", async ({ page }) => {
  await open(page, "/map?system=system:project:1")
  const s = "system:project:1"
  await node(page, `${s}/db:1`).click()
  await expect(inspector(page).getByRole("heading", { name: "shop-db" })).toBeVisible()
  const wired = inspector(page).locator("section", { has: page.getByText("Wired to") })
  for (const [name, via] of [
    ["shop", "linked database"],
    ["jd-db-shop-postgres", "serves"],
    ["jd-db-shop-pgbouncer", "pools for"],
    ["shop nightly", "backed up by"],
  ]) {
    await expect(wired.getByRole("button", { name: new RegExp(`${name}.*${via}`) })).toBeVisible()
  }
  await expect(inspector(page).getByRole("link", { name: "Open" })).toHaveAttribute(
    "href",
    "/databases/1",
  )
  // What it is not wired to steps back.
  await expect(node(page, `${s}/container:c-shop-web`).locator("> div")).toHaveClass(/opacity-35/)
  await expect(page.locator(".react-flow__edgelabel-renderer")).toContainText("backed up by")
  await page.keyboard.press("Escape")
  await expect(inspector(page).getByRole("heading", { name: "shop" })).toBeVisible()
})

test("what two systems both use is drawn in each, and the inspector flies between them", async ({
  page,
}) => {
  await open(page, "/map?system=system:project:2")
  await node(page, "system:project:2/db:3").click()
  const also = inspector(page).locator("section", { has: page.getByText("Also drawn in") })
  for (const name of [/^n8n/, /^billing/, /^data/]) {
    await expect(also.getByRole("button", { name })).toBeVisible()
  }
  await also.getByRole("button", { name: /^data/ }).click()
  await expect(railRow(page, "system:stack:data")).toHaveAttribute("aria-current", "true")
  await expect(inspector(page).getByRole("heading", { name: "data", exact: true })).toBeVisible()
})

test("the rail flies the canvas to a system", async ({ page }) => {
  await open(page)
  await railRow(page, "system:stack:monitoring").click()
  await expect(railRow(page, "system:stack:monitoring")).toHaveAttribute("aria-current", "true")
  await expect(node(page, "system:stack:monitoring/container:c-grafana")).toBeInViewport()
})

test("a box dragged stays where it was put, across a reload, until the map is tidied", async ({
  page,
}) => {
  const mock = await open(page, "/map?system=system:stack:monitoring")
  const id = "system:stack:monitoring/volume:prom-data"
  const from = await at(page, id)
  await page.mouse.move(from.x + from.width / 2, from.y + from.height / 2)
  await page.mouse.down()
  for (let step = 1; step <= 8; step++) {
    await page.mouse.move(from.x + from.width / 2 + step * 10, from.y + from.height / 2 + step * 25)
  }
  await page.mouse.up()
  await expect.poll(() => mock.saved.puts).toBeGreaterThan(0)
  const saved = mock.saved.layout as { version: number; positions: Record<string, unknown> }
  expect(saved.version).toBe(2)
  expect(Object.keys(saved.positions)).toEqual([id])
  // Where it is against the box it hangs off, which nobody moved: the view
  // refits a system whose frame grew, so the screen is no measure.
  const anchor = "system:stack:monitoring/container:c-prom"
  const offset = async () => {
    const [a, b] = [await at(page, id), await at(page, anchor)]
    const zoom = await page
      .locator(".react-flow__viewport")
      .evaluate((el) => new DOMMatrix(getComputedStyle(el).transform).a)
    return Math.round((a.y - b.y) / zoom)
  }
  const before = await offset()
  expect(before).toBeGreaterThan(100)

  await page.reload()
  await expect(node(page, id)).toBeVisible()
  expect(Math.abs((await offset()) - before)).toBeLessThanOrEqual(2)

  await page.getByRole("button", { name: /Tidy up/ }).click()
  await expect
    .poll(() => Object.keys((mock.saved.layout as { positions: object }).positions))
    .toEqual([])
})

test("an arrangement the mind map saved is read as a fresh one", async ({ page }) => {
  await open(page, "/map", {
    layout: { version: 1, positions: { "container:c-api": { x: 4000, y: 4000 } }, minimap: false },
  })
  await expect(page.locator(".react-flow__minimap")).toHaveCount(0)
  // Fresh, so it opens where a crowded server does: on the dashboard's own system.
  await expect(frame(page, "system:dashboard:self")).toBeVisible()
  await expect(node(page, "system:project:1/container:c-api")).toHaveCount(0)
})

test("the desk has the canvas and its panels, with no bar over them and no minimap", async ({
  page,
}) => {
  const mock = await open(page, "/map?system=system:stack:monitoring")
  for (const gone of ["Map options", "Show all"]) {
    await expect(page.getByRole("button", { name: gone })).toHaveCount(0)
  }
  await expect(page.getByRole("textbox", { name: /Find a system/ })).toHaveCount(0)
  await expect(page.locator(".react-flow__minimap")).toHaveCount(0)
  await expect(rail(page).getByText("Systems", { exact: true })).toHaveCount(0)
  // The rail starts at the top of the frame, and the canvas under nothing.
  const pane = (await page.locator("[data-slot=pane]").boundingBox())!
  expect((await page.locator("[data-slot=server-map]").boundingBox())!.y - pane.y).toBeLessThan(2)
  // Nothing moved, nothing to tidy.
  await expect(page.getByRole("button", { name: /Tidy up/ })).toHaveCount(0)

  const canvas = page.locator("[data-slot=server-map]")
  await canvas.getByRole("button", { name: /Hide the inspector/ }).click()
  await expect(inspector(page)).toHaveCount(0)
  await canvas.getByRole("button", { name: /Show the inspector/ }).click()
  await expect(inspector(page)).toBeVisible()
  await expect(canvas.getByRole("button", { name: "Full screen" })).toBeVisible()
  expect(mock.saved.puts).toBe(0)
})

test.describe("on a phone", () => {
  test.use({ hasTouch: true, isMobile: true, viewport: { width: 390, height: 844 } })

  async function openPhone(page: Page, path = "/map") {
    const mock = await mockServerMap(page)
    await page.goto(path)
    await expect(page.locator(".react-flow__node").first()).toBeVisible()
    await still(page)
    return mock
  }

  test("it is only the map, edge to edge", async ({ page }) => {
    await openPhone(page)
    await expect(rail(page)).toHaveCount(0)
    await expect(inspector(page)).toHaveCount(0)
    await expect(page.locator(".react-flow__minimap")).toHaveCount(0)
    for (const gone of [/inspector/, "Full screen", "Zoom in", "Fit", "All"]) {
      await expect(page.getByRole("button", { name: gone, exact: true })).toHaveCount(0)
    }
    await expect(page.getByRole("link", { name: /^Open/ })).toHaveCount(0)
    const canvas = (await page.locator("[data-slot=server-map]").boundingBox())!
    expect(canvas.x).toBe(0)
    expect(canvas.width).toBe(390)
    // The first system across its width, and at the top rather than centred under a gap.
    const first = await at(page, "frame:system:dashboard:self")
    expect(first.y - canvas.y).toBeLessThan(16)
    expect(first.x).toBeGreaterThanOrEqual(0)
    expect(first.x + first.width).toBeLessThanOrEqual(390)
  })

  test("a tap on a box floats the way to its page, and a tap on open ground takes it away", async ({
    page,
  }) => {
    const mock = await openPhone(page, "/map?system=system:project:1")
    const s = "system:project:1"
    await node(page, `${s}/db:1`).tap()
    const open = page.getByRole("link", { name: "Open shop-db" })
    await expect(open).toHaveAttribute("href", "/databases/1")
    await expect(open).toBeInViewport({ ratio: 1 })

    const f = await at(page, `frame:${s}`)
    await page.touchscreen.tap(f.x + f.width / 2, f.y + f.height + 12)
    await expect(open).toHaveCount(0)

    // A system's head opens the system.
    const head = (await frame(page, s).locator("[title], .map-name").first().boundingBox())!
    await page.touchscreen.tap(head.x + head.width / 2, head.y + head.height / 2)
    await page.getByRole("link", { name: "Open shop", exact: true }).click()
    await expect(page).toHaveURL(/\/deploy\/|\/projects\//)
    // Looking from a phone leaves the desk's arrangement as it was.
    expect(mock.saved.puts).toBe(0)
  })

  test("a finger dragged across a box slides the canvas and moves nothing", async ({ page }) => {
    const mock = await openPhone(page, "/map?system=system:project:1")
    const box = await at(page, "system:project:1/db:1")
    const before = await view(page)
    const cdp = await page.context().newCDPSession(page)
    const from = { x: box.x + box.width / 2, y: box.y + box.height / 2 }
    await cdp.send("Input.dispatchTouchEvent", { type: "touchStart", touchPoints: [from] })
    for (let step = 1; step <= 6; step++) {
      await cdp.send("Input.dispatchTouchEvent", {
        type: "touchMove",
        touchPoints: [{ x: from.x + step * 10, y: from.y + step * 15 }],
      })
    }
    await cdp.send("Input.dispatchTouchEvent", { type: "touchEnd", touchPoints: [] })
    await expect.poll(async () => (await view(page)).x).not.toBe(before.x)
    expect((await view(page)).y).toBeGreaterThan(before.y)
    expect(mock.saved.puts).toBe(0)
  })
})

test("nothing changes size but by the zoom: far out, every word is the same size, only smaller", async ({
  page,
}) => {
  await open(page, "/map?system=system:project:1")
  const title = frame(page, "system:project:1").locator(".map-name").first()
  const name = node(page, "system:project:1/db:1").locator(".map-name")
  const sizes = async () => ({
    title: await title.evaluate((el) => getComputedStyle(el).fontSize),
    name: await name.evaluate((el) => getComputedStyle(el).fontSize),
  })
  const near = await sizes()
  await page.getByRole("button", { name: "All", exact: true }).click()
  await expect.poll(async () => (await view(page)).zoom).toBeLessThan(0.3)
  expect(await sizes()).toEqual(near)
  await expect(node(page, "system:project:1/db:1")).not.toHaveCSS("opacity", "0.5")
})

test("a trackpad's two fingers slide the canvas and a pinch zooms it, as on a whiteboard", async ({
  page,
}) => {
  await open(page, "/map?system=system:project:1")
  const box = (await page.locator("[data-slot=server-map]").boundingBox())!
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2)

  const start = await view(page)
  await page.mouse.wheel(120, 80)
  await expect
    .poll(() => view(page))
    .toEqual({
      x: start.x - 120,
      y: start.y - 80,
      zoom: start.zoom,
    })

  // A pinch is a wheel with Ctrl held, which is also how a mouse zooms.
  const before = await view(page)
  await page.keyboard.down("Control")
  await page.mouse.wheel(0, -40)
  await page.keyboard.up("Control")
  await expect.poll(async () => (await view(page)).zoom).toBeGreaterThan(before.zoom)

  // The keys a whiteboard has: Ctrl+0 is the real size, Shift+1 everything.
  await page.keyboard.press("Control+0")
  await expect(page.getByRole("button", { name: "100%" })).toBeVisible()
  await page.keyboard.press("Shift+Digit1")
  await expect.poll(async () => (await view(page)).zoom).toBeLessThan(0.5)
})

test("a mouse drags the canvas by open ground, inside a system's frame as well as between them", async ({
  page,
}) => {
  await open(page, "/map?system=system:project:1")
  const f = await at(page, "frame:system:project:1")
  // Inside the frame, under its head, where no box is.
  const from = { x: f.x + 12, y: f.y + f.height - 12 }
  const start = await view(page)
  await page.mouse.move(from.x, from.y)
  await page.mouse.down()
  await page.mouse.move(from.x + 90, from.y - 40, { steps: 6 })
  await page.mouse.up()
  const after = await view(page)
  expect(after.x - start.x).toBeCloseTo(90, 0)
  expect(after.y - start.y).toBeCloseTo(-40, 0)
})

test("a system is carried by its head, everything in it with it, and stays where it was put", async ({
  page,
}) => {
  const mock = await open(page, "/map?system=system:stack:monitoring")
  const s = "system:stack:monitoring"
  const head = frame(page, s).locator(".map-frame-grip")
  const was = {
    frame: await at(page, `frame:${s}`),
    box: await at(page, `${s}/container:c-grafana`),
  }
  const grip = (await head.boundingBox())!
  const camera = await view(page)
  await page.mouse.move(grip.x + grip.width / 2, grip.y + grip.height / 2)
  await page.mouse.down()
  await page.mouse.move(grip.x + grip.width / 2 + 160, grip.y + grip.height / 2 + 120, { steps: 8 })
  await page.mouse.up()

  const now = {
    frame: await at(page, `frame:${s}`),
    box: await at(page, `${s}/container:c-grafana`),
  }
  expect(now.frame.x - was.frame.x).toBeCloseTo(160, 0)
  expect(now.frame.y - was.frame.y).toBeCloseTo(120, 0)
  expect(now.box.x - was.box.x).toBeCloseTo(160, 0)
  expect(now.box.y - was.box.y).toBeCloseTo(120, 0)

  await expect.poll(() => mock.saved.puts).toBeGreaterThan(0)
  const saved = mock.saved.layout as { frames: Record<string, { x: number; y: number }> }
  expect(Object.keys(saved.frames)).toEqual([s])
  // Picking it up and putting it down is not a click: the camera stayed put.
  expect(await view(page)).toEqual(camera)

  await page.reload()
  await expect(frame(page, s)).toBeVisible()
  // Everything in view, so the system it is measured against is drawn too.
  await page.getByRole("button", { name: "All", exact: true }).click()
  await still(page)
  const other = "system:stack:data"
  const gap = async () => {
    const [a, b] = [await at(page, `frame:${s}`), await at(page, `frame:${other}`)]
    const z = (await view(page)).zoom
    return { x: Math.round((a.x - b.x) / z), y: Math.round((a.y - b.y) / z) }
  }
  const kept = await gap()
  // Across the reload it is still where it was put against a system nobody carried.
  expect(kept.x).not.toBe(0)
  await page.getByRole("button", { name: /Tidy up/ }).click()
  await expect.poll(() => Object.keys((mock.saved.layout as { frames: object }).frames)).toEqual([])
  await expect.poll(gap).not.toEqual(kept)
})

test("no box's words run out of it, however long a name or a state is", async ({ page }) => {
  const long = (i: MapItem): MapItem => ({
    ...i,
    name: `${i.name}-with-a-name-longer-than-any-box`,
    state: i.state && `${i.state} and a good deal more besides`,
    detail: i.detail && `${i.detail} · and a long tail of detail`,
    facts: i.facts?.map((f) => ({ ...f, value: `${f.value} and a long tail` })),
  })
  for (const map of [
    busyMap,
    {
      ...busyMap,
      kinds: busyMap.kinds.map((k) => ({
        ...k,
        items: (k.items as MapItem[] | undefined)?.map(long),
      })),
    },
  ]) {
    await page.setViewportSize({ width: 1920, height: 1080 })
    await mockServerMap(page, { map: map as typeof busyMap })
    await page.goto("/map")
    await expect(page.locator(".react-flow__node").first()).toBeVisible()
    await page.getByRole("button", { name: "All", exact: true }).click()
    await expect.poll(async () => (await view(page)).zoom).toBeLessThan(0.3)
    const spilled = await page.evaluate(() => {
      const out: string[] = []
      const vp = document.querySelector(".react-flow__viewport") as HTMLElement
      const zoom = new DOMMatrix(getComputedStyle(vp).transform).a
      for (const n of document.querySelectorAll<HTMLElement>(".react-flow__node")) {
        const r = n.getBoundingClientRect()
        for (const el of n.querySelectorAll<HTMLElement>("*")) {
          // A port sits on the outline, and the shared tag on the corner, by design.
          if (el.closest(".react-flow__handle, [title^='Also drawn in']")) continue
          const e = el.getBoundingClientRect()
          if (!e.width || !e.height) continue
          const over =
            Math.max(r.left - e.left, e.right - r.right, r.top - e.top, e.bottom - r.bottom) / zoom
          if (over > 1)
            out.push(`${n.dataset.id}: ${el.textContent?.slice(0, 30)} by ${Math.round(over)}px`)
        }
      }
      return out
    })
    expect(spilled).toEqual([])
  }
})

test("thirty checkouts nothing claims stand in rows, not in one column", async ({ page }) => {
  const checkouts = Array.from({ length: 30 }, (_, i) => ({
    id: `repo:/home/deploy/checkouts/c${i}`,
    name: `checkout-${String(i).padStart(2, "0")}`,
    tone: i < 3 ? "warning" : "ok",
    state: i < 3 ? `${i + 2} changes` : "clean",
    href: `/git?repo=c${i}`,
    product: "git",
    detail: "main",
  }))
  const crowded = {
    ...busyMap,
    kinds: busyMap.kinds.map((k) =>
      k.key === "repositories" ? { ...k, items: [...k.items, ...checkouts] } : k,
    ),
  }
  await open(page, "/map?system=system:loose", { map: crowded as typeof busyMap })
  await still(page)

  // What it holds leads with what there is most of, in the rail and on its head.
  await expect(railRow(page, "system:loose")).toContainText("31 checkouts")
  await expect(frame(page, "system:loose")).toContainText("31 checkouts")

  const drawn = page.locator('.react-flow__node[data-id^="system:loose/"]')
  await expect(drawn).toHaveCount(35)
  const outline = await at(page, "frame:system:loose")
  expect(outline.width).toBeGreaterThan(outline.height)
  const boxes = await drawn.evaluateAll((nodes) =>
    nodes.map((n) => {
      const r = n.getBoundingClientRect()
      return { id: (n as HTMLElement).dataset.id, x: r.x, y: r.y, right: r.right, bottom: r.bottom }
    }),
  )
  for (const a of boxes) {
    expect(a.x, a.id).toBeGreaterThanOrEqual(outline.x)
    expect(a.y, a.id).toBeGreaterThanOrEqual(outline.y)
    expect(a.right, a.id).toBeLessThanOrEqual(outline.x + outline.width)
    expect(a.bottom, a.id).toBeLessThanOrEqual(outline.y + outline.height)
    for (const b of boxes) {
      if (a === b) continue
      const apart =
        a.right <= b.x + 0.5 ||
        b.right <= a.x + 0.5 ||
        a.bottom <= b.y + 0.5 ||
        b.bottom <= a.y + 0.5
      expect(apart, `${a.id} over ${b.id}`).toBe(true)
    }
  }
  // The whole of it reads at once: it is not zoomed out to fit a tower.
  expect((await view(page)).zoom).toBeGreaterThan(0.5)
})

test("a server with nothing on it offers the first deployment, and says what it could not read", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1600, height: 1000 })
  await mockServerMap(page, { map: freshMap })
  await page.goto("/map")
  await expect(page.getByText("Nothing to draw yet")).toBeVisible()
  await expect(page.getByRole("link", { name: "Deploy a project" })).toHaveAttribute(
    "href",
    "/deploy/new",
  )
  // Said in the inspector, as on its way rather than as a failure.
  await expect(inspector(page).getByRole("list", { name: "Not read" })).toHaveText(
    "Still reading certificates",
  )
  await expect(page.locator("[data-slot=server-map]").getByText(/Still reading/)).toHaveCount(0)
})

const daemon =
  "Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?"
/** The busy server with a Docker that does not answer: what it would have listed is unread. */
const withoutDocker = {
  ...busyMap,
  kinds: busyMap.kinds.map((k) =>
    ["containers", "stacks", "volumes"].includes(k.key)
      ? { ...k, state: "unread", reason: daemon }
      : k,
  ),
} as typeof busyMap

test("with no place left behind, a crowded server opens on the dashboard's own system, readable", async ({
  page,
}) => {
  const mock = await open(page)
  await still(page)
  await expect(railRow(page, "system:dashboard:self")).toHaveAttribute("aria-current", "true")
  await expect(inspector(page).getByRole("heading", { name: "Just Dashboard" })).toBeVisible()
  expect((await view(page)).zoom).toBeGreaterThanOrEqual(0.5)
  // Its head is on the canvas, at the start of the diagram.
  const canvas = (await page.locator("[data-slot=server-map]").boundingBox())!
  const first = await at(page, "frame:system:dashboard:self")
  expect(first.x).toBeGreaterThanOrEqual(canvas.x)
  expect(first.y).toBeGreaterThanOrEqual(canvas.y)
  expect(first.y).toBeLessThan(canvas.y + canvas.height / 2)
  // Looked at, not chosen: nothing was written down as the reader's.
  expect(mock.saved.puts).toBe(0)

  // All is still everything, however small, and Fit the whole of a system.
  await page.getByRole("button", { name: "All", exact: true }).click()
  await expect.poll(async () => (await view(page)).zoom).toBeLessThan(0.3)
  await expect(inspector(page).getByRole("heading", { name: "17 systems" })).toBeVisible()
})

test("a server whose systems can all be read at once still opens on all of them", async ({
  page,
}) => {
  const few = {
    ...busyMap,
    kinds: busyMap.kinds.map((k) => (k.key === "dashboard" ? k : { ...k, items: [], total: 0 })),
    links: [],
  } as typeof busyMap
  await open(page, "/map", { map: few })
  await still(page)
  await expect(rail(page).locator("[aria-current=true]")).toHaveCount(0)
  await expect(inspector(page).getByRole("heading", { name: /^1 system$/ })).toBeVisible()
})

test("the place the reader left is kept over the page's own choice", async ({ page }) => {
  await open(page, "/map", {
    layout: { version: 2, positions: {}, frames: {}, focus: "system:project:1" },
  })
  await still(page)
  await expect(railRow(page, "system:project:1")).toHaveAttribute("aria-current", "true")
  await expect(railRow(page, "system:dashboard:self")).not.toHaveAttribute("aria-current", "true")
})

test("what Docker did not answer for is one line in the inspector, not four over the canvas", async ({
  page,
}) => {
  await open(page, "/map", { map: withoutDocker })
  const notes = inspector(page).getByRole("list", { name: "Not read" })
  await expect(notes.getByRole("listitem")).toHaveCount(1)
  await expect(notes).toContainText(
    "Docker is not answering: containers, stacks and volumes not drawn",
  )
  await expect(notes.getByRole("link", { name: "Open Docker" })).toHaveAttribute("href", "/docker")
  // The client's question is one press away, never the line.
  await expect(notes.getByText(daemon)).toBeHidden()
  await notes.getByText("What the server said").click()
  await expect(notes.getByText(daemon)).toBeVisible()
  // Nothing is said over the canvas while the inspector says it.
  const canvas = page.locator("[data-slot=server-map]")
  await expect(canvas.getByText(/Docker is not answering/)).toHaveCount(0)
  await expect(canvas.getByText(/not read/)).toHaveCount(0)
  // It stays through every view of the inspector.
  await railRow(page, "system:project:1").click()
  await expect(notes).toContainText("Docker is not answering")

  // With the inspector put away the same line floats on the canvas, once.
  await canvas.getByRole("button", { name: /Hide the inspector/ }).click()
  const floating = canvas.getByRole("list", { name: "Not read" })
  await expect(floating.getByRole("listitem")).toHaveCount(1)
  await expect(floating.getByRole("link", { name: "Open Docker" })).toBeVisible()
  await expect(floating.getByText("What the server said")).toHaveCount(0)
})

test("on this server is a row for each kind, and a row lights that kind's boxes on the canvas", async ({
  page,
}) => {
  await open(page)
  await page.getByRole("button", { name: "All", exact: true }).click()
  await still(page)
  const rows = inspector(page).locator("button[data-kind]")
  await expect(rows.first()).toBeVisible()
  await expect(inspector(page).locator("dl")).toHaveCount(0)
  const databases = inspector(page).locator('button[data-kind="databases"]')
  await expect(databases).toHaveAttribute("aria-pressed", "false")
  await expect(databases.locator("svg")).toHaveCount(1)
  // One height, with the figure at the end of each.
  const heights = await rows.evaluateAll((all) =>
    all.map((row) => Math.round(row.getBoundingClientRect().height)),
  )
  expect(new Set(heights).size).toBe(1)

  await databases.click()
  await expect(databases).toHaveAttribute("aria-pressed", "true")
  await still(page)
  // Every database drawn is lit and what is not one steps back.
  const lit = page.locator('.react-flow__node[data-id$="/db:1"]').first()
  await expect(lit).toBeVisible()
  await expect(lit.locator(".opacity-35")).toHaveCount(0)
  await expect.poll(() => page.locator(".react-flow__node .opacity-35").count()).toBeGreaterThan(0)
  // The whole view stays, so another kind is one press away, and the same
  // row or Escape lets them go.
  await expect(inspector(page).getByRole("heading", { name: "17 systems" })).toBeVisible()
  await databases.click()
  await expect(databases).toHaveAttribute("aria-pressed", "false")
  await expect(page.locator(".react-flow__node .opacity-35")).toHaveCount(0)
})
