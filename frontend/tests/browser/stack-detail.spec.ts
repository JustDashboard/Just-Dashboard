import { expect, test } from "@playwright/test"
import { recordProgress } from "./progress-recording"
import { risen } from "./risen"
import { CONTAINERS, DETAIL, statsFrame, user } from "./stack-detail-fixture"
import { mockStackDetail } from "./stack-rail-fixture"

/**
 * A stack's own page (`/docker/stacks/<name>`), over `shop`: six services,
 * one in a restart loop, one a step from its memory limit and one compose
 * never created, in a checkout with uncommitted changes.
 *
 * The Stacks list stands beside the page from 1280px, so the blocks that go
 * side by side do so from 1600px: the tests that read the wide table ask for
 * a window that wide.
 */

test("opens on the stack's identity line and says what is failing", async ({ page }) => {
  await mockStackDetail(page)
  await page.goto("/docker/stacks/shop")

  const identity = page.locator("[data-slot='host-identity']")
  await expect(identity.getByText("shop", { exact: true })).toBeVisible()
  await expect(identity.getByText("compose.yaml")).toBeVisible()
  await expect(identity.getByText("/srv/shop")).toBeVisible()
  await expect(identity.getByText("2 uncommitted")).toBeVisible()
  await expect(identity.getByText("1 behind")).toBeVisible()
  await expect(identity.getByText("4 of 6 services running")).toBeVisible()
  await expect(identity.getByText("3 ports published")).toBeVisible()

  // The verdict counts the restart loop, and pressing it narrows the table to it.
  const verdict = identity.getByRole("button", { name: "1 service failing" })
  await expect(verdict).toBeVisible()
  await verdict.click()
  const rows = page.getByRole("region", { name: "Services" }).locator("[data-workspace-item]")
  await expect(rows).toHaveCount(1)
  await expect(rows.first()).toHaveAttribute("data-workspace-item", "worker")
  await expect(verdict).toHaveAttribute("aria-pressed", "true")
  await page.keyboard.press("Escape")
  await expect(rows).toHaveCount(6)
})

test("the services are a table of live readings", async ({ page }) => {
  await mockStackDetail(page)
  await page.setViewportSize({ width: 1900, height: 1000 })
  await page.goto("/docker/stacks/shop")

  const row = (name: string) => page.locator(`tr[data-workspace-item='${name}']`)
  // Worst first: the loop, then the service never created, then the rest by name.
  await expect(page.locator("tr[data-workspace-item]")).toHaveCount(6)
  expect(
    await page
      .locator("tr[data-workspace-item]")
      .evaluateAll((rows) => rows.map((r) => r.getAttribute("data-workspace-item"))),
  ).toEqual(["worker", "search", "api", "cache", "db", "web"])

  await expect(row("worker").getByText("Restarting")).toBeVisible()
  await expect(row("worker").getByText(/^restarted ×5 in .+ · exit 1$/)).toBeVisible()
  await expect(row("api").getByText(/^up 26m/)).toBeVisible()
  await expect(row("cache").getByText(/no health check/)).toBeVisible()
  await expect(row("search").getByText("Not created")).toBeVisible()
  await expect(row("search").getByRole("button", { name: "Create it" })).toBeVisible()

  // Memory against its limit, amber near it; the network's rates from two frames.
  const db = row("db")
  await expect(db.getByText("/ 512 MB")).toBeVisible()
  await expect(db.locator(".text-warning").first()).toBeVisible()
  await expect(row("web").getByTitle("Received")).toContainText("/s")
  await expect(row("web").getByText("80 → 80")).toBeVisible()

  // A running service's dot breathes: the row is fed by the containers socket.
  const breathing = await row("api").evaluate((el) =>
    [...el.querySelectorAll("span")].some((s) => getComputedStyle(s).animationName !== "none"),
  )
  expect(breathing).toBe(true)
})

test("draws how the stack is reached and what happened to it", async ({ page }) => {
  await mockStackDetail(page)
  await page.setViewportSize({ width: 1680, height: 1600 })
  await page.goto("/docker/stacks/shop")

  const map = page.locator("[data-slot='stack-map']")
  await expect(map.getByRole("list", { name: "Ways in" }).getByRole("listitem")).toHaveCount(3)
  await expect(map.getByText(":443", { exact: true })).toBeVisible()
  await expect(map.getByText("This server only")).toBeVisible()
  const networks = map.getByRole("list", { name: "Networks" })
  await expect(networks.getByText("backend", { exact: true })).toBeVisible()
  await expect(networks.getByText("frontend", { exact: true })).toBeVisible()

  // A service's changes are a mark each, newest first.
  const recent = page.getByRole("region", { name: "Recent changes" })
  const line = recent.locator("[data-slot=reading-line]")
  await expect(line).toContainText("exited 1")
  await recent.getByRole("button", { name: "Open worker" }).nth(1).hover()
  await expect(line.getByText(/^restarted ×5 in \d+ min · exit 1$/)).toBeVisible()
  await recent.getByRole("button", { name: "Open db" }).first().hover()
  await expect(line).toContainText("passing its check again")

  const memory = page.getByRole("region", { name: "Memory by service" })
  await memory.getByRole("button", { name: "Open db" }).hover()
  await expect(memory.locator("[data-slot=reading-line]")).toContainText(/9\d% of its limit/)
})

test("every block of the band is one height before its readings land and after", async ({
  page,
}) => {
  await mockStackDetail(page)
  let land!: () => void
  const held = new Promise<void>((resolve) => (land = resolve))
  await page.routeWebSocket(/\/api\/v1\/docker\/containers\/stream/, async (socket) => {
    await held
    socket.send(JSON.stringify({ type: "containers", data: CONTAINERS }))
    socket.send(JSON.stringify({ type: "stats", data: statsFrame(0) }))
  })
  await page.setViewportSize({ width: 1680, height: 1600 })
  await page.goto("/docker/stacks/shop")

  const band = page.locator("[data-slot=stack-usage]")
  const blocks = band.locator("[data-slot=bar-reading]")
  await expect(blocks).toHaveCount(3)
  await expect(page.getByLabel("Memory by service")).toContainText("Waiting for the first reading…")
  const measure = () =>
    blocks.evaluateAll((all) => all.map((block) => block.getBoundingClientRect().height))
  await risen(page)
  const before = await measure()
  expect(new Set(before).size).toBe(1)

  land()
  await expect(page.getByLabel("Memory by service").getByRole("button").first()).toBeVisible()
  await risen(page)
  expect(await measure()).toEqual(before)
})

test("a service's compose verbs run for that service", async ({ page }) => {
  const mocks = await mockStackDetail(page)
  await page.setViewportSize({ width: 1680, height: 1000 })
  await page.goto("/docker/stacks/shop")

  const api = page.locator("tr[data-workspace-item='api']")
  await api.hover()
  await api.getByRole("button", { name: "Restart", exact: true }).click()
  const dialog = page.getByRole("dialog")
  await expect(dialog.getByText("docker compose restart api")).toBeVisible()
  await dialog.getByRole("button", { name: "Restart", exact: true }).click()
  await expect.poll(() => mocks.runs).toEqual(["restart:api"])

  await api.getByRole("button", { name: "More actions for api" }).click()
  await page.getByRole("menuitem", { name: "Recreate service" }).click()
  await expect(
    page.getByRole("dialog").getByText(/--force-recreate --remove-orphans api$/),
  ).toBeVisible()
  await page.getByRole("dialog").getByRole("button", { name: "Recreate", exact: true }).click()
  await expect.poll(() => mocks.runs).toEqual(["restart:api", "recreate:api"])

  await page
    .locator("tr[data-workspace-item='search']")
    .getByRole("button", { name: "Create it" })
    .click()
  await expect.poll(() => mocks.runs).toEqual(["restart:api", "recreate:api", "up:search"])
})

test("a row opens its container", async ({ page }) => {
  await mockStackDetail(page)
  await page.setViewportSize({ width: 1680, height: 1000 })
  await page.goto("/docker/stacks/shop")
  await page
    .locator("tr[data-workspace-item='db']")
    .getByRole("button", { name: "db", exact: true })
    .click()
  await expect(page).toHaveURL(/\/docker\/containers\/a3c8e2f4a6b8/)
})

test("on a phone each service is drawn down the row, with nothing dropped", async ({ page }) => {
  await mockStackDetail(page)
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto("/docker/stacks/shop")

  const db = page.locator("li[data-workspace-item='db']")
  await expect(db.getByText("postgres:16-alpine")).toBeVisible()
  await expect(db.getByText(/^\d+\.\d% CPU$/)).toBeVisible()
  await expect(db.getByText(/^4\d\d\.\d MB$/)).toBeVisible()
  await expect(db.getByText(/^up 3d 4h · healthy$/)).toBeVisible()
  await expect(page.locator("tr[data-workspace-item]")).toHaveCount(0)
  const overflow = await page.evaluate(
    () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
  )
  expect(overflow).toBeLessThanOrEqual(1)
})

test("a stack nothing has been created for says so", async ({ page }) => {
  await mockStackDetail(page, {
    containers: [],
    detail: {
      ...DETAIL,
      services: DETAIL.services.map((s) => ({
        ...s,
        container: "",
        state: "missing",
        status: "",
        missing: true,
      })),
      running: 0,
      containers: 0,
      deployed: false,
      state: "not-deployed",
      summary: "Not deployed · 6 services defined",
      git: undefined,
    },
  })
  await page.goto("/docker/stacks/shop")
  const identity = page.locator("[data-slot='host-identity']")
  await expect(identity.getByText("Not deployed")).toBeVisible()
  await expect(page.locator("[data-slot='stack-map']")).toHaveCount(0)
  await expect(page.getByRole("button", { name: "Create it" }).first()).toBeVisible()
})

test("Deploy preview says what Deploy will do to each service, and deploys", async ({ page }) => {
  const mocks = await mockStackDetail(page)
  await page.setViewportSize({ width: 1680, height: 1000 })
  await page.goto("/docker/stacks/shop?tab=preview")

  await expect(
    page.getByRole("heading", { name: "Deploy will recreate 2 services and create 1" }),
  ).toBeVisible()
  await expect(page.getByText("No volume is removed")).toBeVisible()
  // Most consequential first, each with what it is doing now.
  const rows = page.locator("tr[data-change]")
  expect(await rows.evaluateAll((r) => r.map((row) => row.getAttribute("data-change")))).toEqual([
    "recreate",
    "recreate",
    "create",
    "unchanged",
    "unchanged",
    "unchanged",
  ])
  const worker = rows.filter({ hasText: "worker" })
  await expect(worker.getByText("3.11-slim")).toBeVisible()
  await expect(worker.getByText("3.12-slim")).toBeVisible()
  await expect(rows.filter({ hasText: "api" }).getByText("down while it is replaced")).toBeVisible()
  // The file's changes are headed by the service they change.
  const changes = page.getByRole("region", { name: "Compose file changes" })
  await expect(changes.getByRole("region", { name: "api" })).toContainText("mem_limit: 1g")
  await expect(changes.getByRole("region", { name: "worker" })).toContainText("QUEUE_CONCURRENCY")
  await expect(page.getByText("shop_pgdata")).toBeVisible()

  await page.getByRole("button", { name: "Deploy", exact: true }).last().click()
  await expect.poll(() => mocks.runs).toEqual(["up"])
})

test("the compose file has an outline of its services and keeps an edit across views", async ({
  page,
}) => {
  await mockStackDetail(page)
  await page.setViewportSize({ width: 1680, height: 1000 })
  await page.goto("/docker/stacks/shop?tab=compose")

  const outline = page.getByRole("navigation", { name: "Outline" })
  for (const service of ["web", "api", "worker", "db", "cache", "search"]) {
    await expect(outline.getByRole("button", { name: new RegExp(`^${service}`) })).toBeVisible()
  }
  await expect(page.getByText("Saved", { exact: true })).toBeVisible()

  // Jumping to a service puts the cursor on its line and says where it is.
  await outline.getByRole("button", { name: /^db/ }).click()
  await expect(page.getByText(/^Ln \d+, Col 1/)).toBeVisible()
  await expect(page.locator("[data-slot='pane-footer']").getByText("services · db")).toBeVisible()

  // An edit is reviewable as a diff and survives a look at another view.
  await page.evaluate(() => {
    type Editor = { getValue(): string; setValue(v: string): void }
    const editor = (
      window as unknown as { monaco: { editor: { getEditors(): Editor[] } } }
    ).monaco.editor.getEditors()[0]
    editor.setValue(editor.getValue().replace("\n  db:\n", "\n  db:  # pinned\n"))
  })
  await expect(page.getByText("Unsaved changes")).toBeVisible()
  await page.getByRole("button", { name: /Review changes/ }).click()
  await expect(page.getByRole("region", { name: "db" })).toContainText("# pinned")
  await page.getByRole("tab", { name: /^Deploy preview/ }).click()
  await page.getByRole("tab", { name: /^Compose file/ }).click()
  await expect(page.getByText("Unsaved changes")).toBeVisible()
})

test("History lays the deployments on time and reads one against the file now", async ({
  page,
}) => {
  await mockStackDetail(page)
  await page.setViewportSize({ width: 1680, height: 1000 })
  await page.goto("/docker/stacks/shop?tab=history")

  await expect(page.getByText("6 deployments")).toBeVisible()
  await expect(page.getByText(/of 3 compose files/)).toBeVisible()
  const rail = page.getByRole("list", { name: "Deployments" })
  await expect(rail.getByRole("button")).toHaveCount(6)
  await expect(page.getByRole("heading", { name: /^Restarted/ })).toBeVisible()
  // The newest record ran the worker on 3.11; the file on disk has moved on.
  const since = page.getByRole("region", { name: "Since then" })
  await expect(since).toContainText("python:3.12-slim")
  await expect(page.getByText(/can be brought back exactly/)).toBeVisible()

  // An older one lost an image; its file goes into the editor, unsaved.
  await rail.getByRole("button", { name: /Recreated/ }).click()
  await expect(page.getByText(/api is no longer on this server/)).toBeVisible()
  await page.getByRole("button", { name: "Edit from this version" }).click()
  await expect(page.getByRole("tab", { name: /^Compose file/ })).toHaveAttribute(
    "aria-selected",
    "true",
  )
  await expect(page.getByText(/^This is the file from the deployment/)).toBeVisible()
  await expect(page.getByText("Unsaved changes")).toBeVisible()
})

test("Files says what compose reads from the stack's directory", async ({ page }) => {
  await mockStackDetail(page)
  await page.setViewportSize({ width: 1680, height: 1000 })
  await page.goto("/docker/stacks/shop?tab=files")

  const reads = page.getByRole("region", { name: "What compose reads here" })
  const row = (path: string) => reads.locator(`[data-path='${path}']`)
  await expect(row("compose.yaml")).toContainText("every service is defined here")
  await expect(row("compose.yaml")).toContainText("modified")
  await expect(row("app")).toContainText("built from it")
  await expect(row("nginx")).toContainText("/etc/nginx/conf.d")
  await expect(row(".env")).toContainText("reads its variables")
  await expect(row("uploads")).toContainText("Missing: Docker creates it empty")
  // The same words follow the names into the browser below.
  await expect(page.getByRole("row", { name: /backups/ })).toContainText("untracked")
})

test("Logs lists the services that write into it and narrows to one", async ({ page }) => {
  await mockStackDetail(page)
  await page.setViewportSize({ width: 1680, height: 1000 })
  await page.goto("/docker/stacks/shop?tab=logs")

  const strip = page.getByRole("navigation", { name: "Services in this log" })
  await expect(strip.getByRole("button", { name: "Only worker" })).toContainText("46 errors")
  await expect(strip.getByRole("button", { name: "Every service" })).toHaveAttribute(
    "aria-pressed",
    "true",
  )
  await strip.getByRole("button", { name: "Only worker" }).click()
  await expect(strip.getByRole("button", { name: "Only worker" })).toHaveAttribute(
    "aria-pressed",
    "true",
  )
})

test("compose conflict keeps the draft until the current file is reviewed", async ({ page }) => {
  await mockStackDetail(page)
  let revision = "opened"
  let content = "name: shop\nservices:\n  web:\n    image: nginx:old\n"
  const writes: Record<string, unknown>[] = []
  await page.route("**/api/v1/docker/stacks/shop/config", (route) => {
    if (route.request().method() === "GET")
      return route.fulfill({
        contentType: "application/json",
        body: JSON.stringify({ path: "/srv/shop/compose.yaml", content, revision }),
      })
    const body = route.request().postDataJSON()
    writes.push(body)
    if (body.expectedRevision !== revision)
      return route.fulfill({
        status: 409,
        contentType: "application/json",
        body: JSON.stringify({ error: { code: "content_conflict", message: "Changed on disk" } }),
      })
    content = body.content
    return route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({ validation: { valid: true, services: ["web"] }, revision: "saved" }),
    })
  })
  await page.goto("/docker/stacks/shop?tab=compose")
  await expect(page.locator(".monaco-editor")).toBeVisible({ timeout: 20_000 })
  await page.locator(".monaco-editor").click()
  await page.keyboard.press("Control+End")
  await page.keyboard.insertText("# MY DRAFT")
  revision = "external"
  content = "name: shop\nservices:\n  web:\n    image: nginx:native\n"
  await page.getByRole("button", { name: "Save", exact: true }).click()
  await expect(page.getByRole("alert").filter({ hasText: "Your draft is kept" })).toContainText(
    "Your draft is kept",
  )
  await expect(page.locator(".monaco-editor")).toContainText("MY DRAFT")
  expect(writes[0].expectedRevision).toBe("opened")
  await page.getByRole("button", { name: "Review current file", exact: true }).click()
  await expect(page.getByText("nginx:native", { exact: false })).toBeVisible()
  await page.getByRole("button", { name: "Save", exact: true }).click()
  await expect.poll(() => writes.length).toBe(2)
  expect(writes[1].expectedRevision).toBe("external")
})

test("progress: Compose output is visible inside the confirmation while the command runs", async ({
  page,
}) => {
  await mockStackDetail(page)
  let socket: { send: (message: string) => void } | undefined
  await page.routeWebSocket(/\/docker\/stacks\/shop\/run/, (ws) => {
    socket = ws
    ws.send(
      JSON.stringify({
        type: "output",
        data: [{ stream: "stdout", text: "Restarting api container" }],
      }),
    )
  })
  await page.setViewportSize({ width: 1680, height: 900 })
  await page.goto("/docker/stacks/shop")
  const api = page.locator("tr[data-workspace-item='api']")
  await api.hover()
  await api.getByRole("button", { name: "Restart", exact: true }).click()
  const dialog = page.getByRole("dialog")
  await recordProgress(
    page,
    "docker-compose",
    async () => {
      await dialog.getByRole("button", { name: "Restart", exact: true }).click()
      await expect(dialog.getByText("Restarting api container", { exact: true })).toBeVisible()
      socket!.send(
        JSON.stringify({
          type: "output",
          data: [
            {
              stream: "stdout",
              text: "Waiting for the container to finish restarting. ".repeat(20),
            },
          ],
        }),
      )
      const progress = dialog.locator("[data-slot=operation-progress]")
      await expect(progress).toContainText("Waiting for the container")
      // The latest line shares the footer with the buttons: cut to one line
      // there rather than growing the footer as the command talks.
      const cancel = await dialog.getByRole("button", { name: "Cancel", exact: true }).boundingBox()
      const box = await progress.boundingBox()
      expect(box!.height).toBeLessThanOrEqual(cancel!.height + 2)
      const line = progress.locator("p").last()
      expect(await line.evaluate((element) => element.scrollWidth > element.clientWidth)).toBe(true)
    },
    dialog.locator("[data-slot=operation-progress]"),
    async () => {
      socket!.send(JSON.stringify({ type: "done", data: { exitCode: 0 } }))
      await expect(dialog).toHaveCount(0)
    },
  )
})

test("the view reads in a container page's order: readings, the picture, then the table", async ({
  page,
}) => {
  await mockStackDetail(page)
  await page.setViewportSize({ width: 1680, height: 1000 })
  await page.goto("/docker/stacks/shop")

  const band = page.locator("[data-slot=stack-usage]")
  const map = page.locator("[data-slot='stack-map']")
  const table = page.getByRole("region", { name: "Services" })
  await expect(table.locator("tr[data-workspace-item]")).toHaveCount(6)
  await risen(page)
  const top = async (locator: typeof band) =>
    locator.evaluate((el) => el.getBoundingClientRect().top)
  expect(await top(band)).toBeLessThan(await top(map))
  expect(await top(map)).toBeLessThan(await top(table))
  // No row of tiles opens it.
  await expect(page.locator("[data-slot='stat-grid']")).toHaveCount(0)

  // The verdict narrows the table and brings it to the reader, now that it is the last block.
  await page.getByRole("button", { name: "1 service failing" }).click()
  await expect(table.locator("tr[data-workspace-item]")).toHaveCount(1)
  await expect(table).toBeInViewport()
})

test("the stacks stand beside the page, and Alt+↓ walks them with the view carried", async ({
  page,
}) => {
  await mockStackDetail(page)
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto("/docker/stacks/shop?tab=history")

  const rail = page.locator("[data-slot=stack-rail]")
  await expect(rail).toBeVisible()
  const names = rail.getByRole("list", { name: "Stacks to open" }).getByRole("button")
  await expect(names).toHaveCount(5)
  // The list's own order: running first by name, then stopped, then never deployed.
  expect(
    await names.evaluateAll((all) => all.map((each) => each.getAttribute("aria-label"))),
  ).toEqual(["analytics", "monitoring", "shop", "mailserver", "wiki"])
  const current = rail.getByRole("button", { name: "shop", exact: true })
  await expect(current).toHaveAttribute("aria-current", "page")
  await expect(current).toContainText("4/6 up")
  await expect(rail.getByRole("button", { name: "wiki", exact: true })).toContainText("2 defined")
  await expect(rail.getByText("5 stacks · running first")).toBeVisible()

  // Every row is one height, whatever it has to say.
  const heights = await names.evaluateAll((all) =>
    all.map((each) => each.getBoundingClientRect().height),
  )
  expect(new Set(heights).size).toBe(1)

  await expect(page.getByRole("button", { name: "Previous stack" })).toBeEnabled()
  await page.keyboard.press("Alt+ArrowDown")
  await expect(page).toHaveURL(/\/docker\/stacks\/mailserver\?tab=history$/, { timeout: 15_000 })
  await page.goBack()
  await expect(page).toHaveURL(/\/docker\/stacks\/shop\?tab=history$/)
  await rail.getByRole("button", { name: "analytics", exact: true }).click()
  await expect(page).toHaveURL(/\/docker\/stacks\/analytics\?tab=history$/, { timeout: 15_000 })
})

test("the rail keeps the list's search and state, and still names the stack on screen", async ({
  page,
}) => {
  await mockStackDetail(page)
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto("/docker/stacks/shop")

  const rail = page.locator("[data-slot=stack-rail]")
  const names = rail.getByRole("list", { name: "Stacks to open" }).getByRole("button")
  await expect(names).toHaveCount(5)
  await rail.getByPlaceholder("Stack, service or image").fill("grafana")
  await expect(names).toHaveCount(2)
  // The stack on screen stays, set apart from what the search found.
  await expect(names.first()).toHaveAttribute("aria-label", "shop")
  await expect(names.first()).toHaveAttribute("aria-current", "page")
  await expect(names.nth(1)).toHaveAttribute("aria-label", "monitoring")
  await expect(rail.getByText("1 of 5")).toBeVisible()

  // The search is the Stacks page's own: it is still there on the way back.
  await page.getByRole("link", { name: "Stacks" }).first().click()
  await expect(page.getByPlaceholder("Stack, service or image")).toHaveValue("grafana", {
    timeout: 15_000,
  })
})

test("beside the rail at 1280 the services are drawn down the row, with nothing cut off", async ({
  page,
}) => {
  await mockStackDetail(page)
  await page.setViewportSize({ width: 1280, height: 900 })
  await page.goto("/docker/stacks/shop")

  await expect(page.locator("[data-slot=stack-rail]")).toBeVisible()
  const db = page.locator("li[data-workspace-item='db']")
  await expect(db.getByText("postgres:16-alpine")).toBeVisible()
  await expect(page.locator("tr[data-workspace-item]")).toHaveCount(0)
  const main = page.getByRole("region", { name: "Services" })
  const box = await main.boundingBox()
  for (const row of await main.locator("li[data-workspace-item]").all()) {
    const each = await row.boundingBox()
    expect(each!.x).toBeGreaterThanOrEqual(box!.x - 1)
    expect(each!.x + each!.width).toBeLessThanOrEqual(box!.x + box!.width + 1)
  }
  const overflow = await page.evaluate(
    () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
  )
  expect(overflow).toBeLessThanOrEqual(1)
})

test("on a phone the page is one pane with no rail, and the picture comes before the services", async ({
  page,
}) => {
  await mockStackDetail(page)
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto("/docker/stacks/shop")

  await expect(page.locator("li[data-workspace-item='db']")).toBeVisible()
  await expect(page.locator("[data-slot=stack-rail]")).toBeHidden()
  const order = await page.evaluate(() => {
    const top = (selector: string) => document.querySelector(selector)!.getBoundingClientRect().top
    return [
      top("[data-slot=stack-usage]"),
      top("[data-slot='stack-map']"),
      top("section[aria-label='Services']"),
    ]
  })
  expect(order[0]).toBeLessThan(order[1])
  expect(order[1]).toBeLessThan(order[2])
})

test("a read-only account reads the stack and is offered no compose verb", async ({ page }) => {
  await mockStackDetail(page)
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
  await page.setViewportSize({ width: 1680, height: 1000 })
  await page.goto("/docker/stacks/shop")

  await expect(page.locator("tr[data-workspace-item]")).toHaveCount(6)
  await expect(page.getByRole("button", { name: "Deploy", exact: true })).toHaveCount(0)
  await expect(page.getByRole("button", { name: "Restart", exact: true })).toHaveCount(0)
  await expect(page.getByRole("button", { name: "Create it" })).toHaveCount(0)
  await expect(page.getByText("Administrator access required")).toBeVisible()
  // The rail and the walk are reading, and stay.
  await expect(page.locator("[data-slot=stack-rail]")).toBeVisible()
  await expect(page.getByRole("button", { name: "Next stack" })).toBeEnabled()
})
