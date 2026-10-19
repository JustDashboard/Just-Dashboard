import { expect, test } from "@playwright/test"
import { fleet, mockOverview } from "./overview-fixture"
import { iso, json, now, user } from "./host-fixture"

/**
 * The host Overview, against a server with something in every place: the
 * machine drawn as itself, the shortcuts that start something, each moving
 * reading carrying its last hour, the projects as the fleet draws them, the
 * shells and drawings left open, the databases beside the backups, who is
 * spending the machine beside the day's activity, and a service tile per
 * module naming what it counts with the products.
 */

test.beforeEach(async ({ page }) => {
  await mockOverview(page)
})

test("the host is drawn as itself, and its facts are one line", async ({ page }) => {
  await page.goto("/")
  await expect(page.getByRole("heading", { name: "atlas" })).toHaveClass(/sr-only/)
  await expect(
    page.locator('[data-slot="host-identity"]').getByText("atlas", { exact: true }),
  ).toBeVisible()
  await expect(page.getByText(/^Ubuntu 24\.04 6\.8\.0-45-generic$/)).toBeVisible()
  // The distribution, the processor and the hypervisor, each its own mark.
  for (const logo of ["ubuntu", "amd", "qemu"]) {
    await expect(page.locator(`img[src="/logos/${logo}.svg"]`).first()).toBeVisible()
  }
  await expect(page.getByText("184 processes")).toBeVisible()
})

test("each moving reading carries its last hour in its tile", async ({ page }) => {
  await page.goto("/")
  const cpu = page.locator("[data-slot=stat-tile]", { hasText: "CPU" }).first()
  await expect(cpu.getByRole("img", { name: "CPU over the last hour" })).toBeVisible({
    timeout: 20_000,
  })
  await expect(page.getByRole("img", { name: "Network over the last hour" })).toBeVisible()
  // The panel of sparklines that repeated the tiles is gone.
  await expect(page.getByRole("heading", { name: "Last hour" })).toHaveCount(0)
  // Every core from the same frame, beside the figure they add up to.
  await expect(cpu.getByRole("img", { name: "4 cores, the busiest at 31%" })).toBeVisible()
})

test("the readings say they are live and lead on to Metrics", async ({ page }) => {
  await page.goto("/")
  const identity = page.locator('[data-slot="host-identity"]')
  await expect(identity.getByText("atlas", { exact: true })).toBeVisible()
  // The identity line describes the machine, with nothing to press.
  await expect(identity.getByRole("link")).toHaveCount(0)

  const resources = page.locator("section", {
    has: page.getByRole("heading", { name: "Resources" }),
  })
  await expect(resources.getByText("Live", { exact: true })).toBeVisible()
  const readings = resources.locator("[data-slot=stat-grid] > [data-slot=stat-tile]")
  await expect(readings).toHaveCount(4)
  await expect(readings.filter({ hasText: "Storage" })).toHaveCount(0)
  await resources.getByRole("link", { name: "All metrics" }).click()
  await expect(page).toHaveURL(/\/metrics$/)
})

test("every filesystem is a bar of its own under the readings", async ({ page }) => {
  await page.goto("/")
  const band = page.locator("[data-slot=storage-band]")
  const disks = band.getByRole("list", { name: "Filesystems" }).locator(":scope > li")
  await expect(disks).toHaveCount(2)
  await expect(disks.first()).toContainText("/")
  await expect(disks.first()).toContainText("10.4 GB free")
  await expect(disks.first()).toContainText("87% of 80.0 GB · /dev/vda1")
  await expect(disks.first()).toContainText("1.2 MB/s read · 3.4 MB/s write")
  await expect(disks.nth(1)).toContainText("/srv")
  await expect(band.getByRole("meter", { name: "/ used" })).toHaveAttribute("aria-valuenow", "87")
  // Past the warning line the free space takes the bar's tone.
  await expect(disks.first().getByText("10.4 GB", { exact: true })).toHaveClass(/text-warning/)
  // What the disks are doing is a reading of its own, with its hour.
  const io = band.locator("[data-slot=stat-tile]", { hasText: "Disk I/O" })
  await expect(io).toContainText("1.2 MB/s read · 3.9 MB/s write")
  await expect(io.getByRole("img", { name: "Disk I/O over the last hour" })).toBeVisible()

  // Two disks share a row as two equal bars.
  const [root, srv] = await Promise.all(
    ["/ used", "/srv used"].map((name) => band.getByRole("meter", { name }).boundingBox()),
  )
  expect(root?.y).toBe(srv?.y)
  expect(root?.width).toBe(srv?.width)
})

test("the projects are the fleet's cards, worst first", async ({ page }) => {
  await page.goto("/")
  const section = page.locator("section", {
    has: page.getByRole("heading", { name: "Deployments" }),
  })
  const cards = section.getByRole("list", { name: "Deployment projects" }).locator(":scope > li")
  await expect(cards).toHaveCount(fleet.length)
  await expect(cards.first()).toContainText("docs-site")
  await expect(cards.first()).toContainText("Failed")
  // Drawn as what each one is: the template's product, the framework.
  await expect(section.locator('img[src="/logos/n8n.svg"]').first()).toBeAttached()
  await expect(section.locator('img[src="/logos/astro.svg"]').first()).toBeAttached()
  await expect(section.getByRole("link", { name: "All projects" })).toHaveAttribute(
    "href",
    "/deploy",
  )
  await expect(section.getByRole("link", { name: "New project" })).toHaveAttribute(
    "href",
    "/deploy/new",
  )
})

test("a longer fleet shows two rows and says how many it left out", async ({ page }) => {
  const more = [7, 8].map((n) => ({ ...fleet[0], id: 100 + n, name: `extra-${n}` }))
  await mockOverview(page, { deployments: [...fleet, ...more] })
  await page.goto("/")
  const section = page.locator("section", {
    has: page.getByRole("heading", { name: "Deployments" }),
  })
  await expect(
    section.getByRole("list", { name: "Deployment projects" }).locator(":scope > li"),
  ).toHaveCount(6)
  await expect(section.getByText("Showing 6 of 8")).toBeVisible()
})

test("an empty fleet asks for its first project", async ({ page }) => {
  await mockOverview(page, { deployments: [] })
  await page.goto("/")
  const empty = page.locator("[data-slot=empty-state]", {
    hasText: "Deploy your first project",
  })
  await expect(empty).toBeVisible()
  await expect(empty.getByRole("link", { name: "New project" })).toHaveAttribute(
    "href",
    "/deploy/new",
  )
})

test("who is spending the machine sits beside the day's activity", async ({ page }) => {
  await page.goto("/")
  const processes = page.locator("[data-slot=panel]", {
    has: page.getByRole("heading", { name: "Top processes" }),
  })
  await expect(processes.locator("li").first()).toContainText("postgres")
  await expect(processes.locator('img[src="/logos/postgresql.svg"]')).toBeAttached()

  const activity = page.locator("[data-slot=panel]", {
    has: page.getByRole("heading", { name: "Recent activity" }),
  })
  // Newest first, and the day rather than the hour: the package run ten
  // hours ago is still on it.
  await expect(activity.locator("li").first()).toContainText("shop-stack deployed")
  await expect(activity).toContainText("Installed 4 package updates")
})

for (const width of [1280, 390]) {
  test(`activity shows product identities and truthful outcomes at ${width}`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 })
    await page.route("**/api/v1/system/metrics/events**", (route) =>
      json(route, [
        {
          ts: iso(now - 180_000),
          kind: "deploy",
          title: "shop-stack",
          detail: "deploy running (push) by operator",
          severity: "info",
        },
        {
          ts: iso(now - 120_000),
          kind: "action",
          title: "docker.container.restart shop-web",
          detail: "by operator",
          severity: "error",
        },
        {
          ts: iso(now - 60_000),
          kind: "action",
          title: "terminal.kill d4a0e8350b641a59",
          detail: "by operator",
          severity: "info",
        },
      ]),
    )
    await page.goto("/")
    const activity = page.locator("[data-slot=panel]", {
      has: page.getByRole("heading", { name: "Recent activity", exact: true }),
    })
    const rows = activity.getByRole("list", { name: "Recent server activity" }).locator("li")
    await expect(rows).toHaveCount(3)
    await expect(rows.first()).toContainText("Close terminal")
    await expect(rows.first()).toContainText("Accepted")
    await expect(rows.first().locator('img[src="/logos/terminal.svg"]')).toBeVisible()
    await expect(rows.first().locator('[title="terminal.kill d4a0e8350b641a59"]')).toBeVisible()
    await expect(rows.nth(1)).toContainText("Failed")
    await expect(rows.nth(1).locator('img[src="/logos/docker.svg"]')).toBeVisible()
    await expect(rows.nth(2)).toContainText("Running")
    await expect(activity.getByText("Succeeded", { exact: true })).toHaveCount(0)
    await expect(rows.first().locator("time")).toHaveAttribute("datetime", iso(now - 60_000))
    const bounds = await activity.locator('[data-slot="panel-body"]').evaluate((panel) => ({
      width: panel.clientWidth,
      scrollWidth: panel.scrollWidth,
    }))
    expect(bounds.scrollWidth).toBe(bounds.width)
    await expect(activity.getByRole("link", { name: "Audit log" })).toHaveAttribute(
      "href",
      "/audit",
    )
    await activity.getByRole("link", { name: "Audit log" }).click()
    await expect(page).toHaveURL(/\/audit$/)
  })
}

test("an empty activity panel keeps the way to the audit trail", async ({ page }) => {
  await page.route("**/api/v1/system/metrics/events**", (route) => json(route, []))
  await page.goto("/")
  const activity = page.locator("[data-slot=panel]", {
    has: page.getByRole("heading", { name: "Recent activity", exact: true }),
  })
  await expect(activity).toContainText("Nothing in the last 24 hours.")
  await expect(activity.getByRole("link", { name: "Audit log" })).toBeVisible()
})

test("read-only accounts see activity without an administrator's audit-log link", async ({
  page,
}) => {
  await page.route("**/api/v1/auth/session", (route) =>
    json(route, {
      ...user,
      capabilities: ["read"],
      user: { ...user.user, role: "viewer" },
    }),
  )
  await page.goto("/")
  const activity = page.locator("[data-slot=panel]", {
    has: page.getByRole("heading", { name: "Recent activity", exact: true }),
  })
  await expect(activity.getByRole("list", { name: "Recent server activity" })).toBeVisible()
  await expect(activity.getByText("Last 24 hours")).toBeVisible()
  await expect(activity.getByRole("link", { name: "Audit log" })).toHaveCount(0)
})

test("a service tile names what it counts with the products", async ({ page }) => {
  await page.goto("/")
  const main = page.locator("[data-slot=page]")
  const docker = main.getByRole("link", { name: "Docker", exact: true })
  await expect(docker).toContainText(/9\s*running/)
  await expect(docker).toContainText("11 containers · 2 stopped")
  // The running images, not the stopped ones.
  await expect(docker.locator('img[src="/logos/postgresql.svg"]')).toBeAttached()
  await expect(docker.locator('img[src="/logos/redis.svg"]')).toBeAttached()
  await expect(docker.locator('img[src="/logos/meilisearch.svg"]')).toHaveCount(0)
  await expect(
    main
      .getByRole("link", { name: "Security", exact: true })
      .locator('img[src="/logos/tailscale.svg"]'),
  ).toBeAttached()

  // Deployments, Databases and Backups have blocks of their own, so their
  // tiles went to what systemd runs and the remote computers.
  for (const name of ["Deployments", "Databases", "Backups"]) {
    await expect(
      main.locator("[data-slot=stat-grid]").getByRole("link", { name, exact: true }),
    ).toHaveCount(0)
  }
  const units = main.getByRole("link", { name: "System services", exact: true })
  await expect(units).toContainText(/1\s*failed/)
  await expect(units).toContainText("6 running")
  await expect(units.locator("[data-slot=stat-tile] .text-destructive")).toHaveText("1")
  const remote = main.getByRole("link", { name: "Remote Desktop", exact: true })
  await expect(remote).toContainText(/2\s*computers/)
  await expect(remote).toContainText("1 open now")
  const git = main.getByRole("link", { name: "Git", exact: true })
  await expect(git).toContainText(/4\s*repositories/)
  await expect(git).toContainText("1 uncommitted · 1 behind")
  for (const forge of ["gitlab", "codeberg", "github"]) {
    await expect(git.locator(`img[src="/logos/${forge}.svg"]`)).toBeAttached()
  }
})

for (const width of [1280, 1720]) {
  test(`looks right at ${width}`, async ({ page }) => {
    // Tall enough for the whole page: the shell scrolls inside itself, so a
    // full-page screenshot would stop at the window.
    await page.setViewportSize({ width, height: 2300 })
    await page.goto("/")
    await expect(page.getByRole("img", { name: "CPU over the last hour" })).toBeVisible({
      timeout: 20_000,
    })
    await expect(
      page.locator("[data-slot=page]").getByRole("link", { name: "Git", exact: true }),
    ).toContainText(/4\s*repositories/)
    await expect(page.getByRole("list", { name: "Recent drawings" })).toBeVisible()
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth > document.documentElement.clientWidth,
      ),
    ).toBe(false)
    // The two halves of the process row meet on one hairline.
    const [processes, activity] = await Promise.all(
      ["Top processes", "Recent activity"].map((name) =>
        page
          .locator("[data-slot=panel-header]", { has: page.getByRole("heading", { name }) })
          .boundingBox(),
      ),
    )
    expect(processes?.y).toBe(activity?.y)
    expect(processes?.height).toBe(activity?.height)
    await page.screenshot({ path: `test-results/overview-${width}.png`, fullPage: true })
  })
}

test("the shortcuts start something, each counting what it adds to", async ({ page }) => {
  let created: unknown
  await page.route("**/api/v1/boards/", (route) => {
    if (route.request().method() !== "POST") return route.fallback()
    created = route.request().postDataJSON()
    return json(route, { id: 9, name: "Untitled drawing", revision: 1, scene: {} })
  })
  await page.goto("/")
  const shortcuts = page.locator("section", {
    has: page.getByRole("heading", { name: "Shortcuts" }),
  })
  const cards = shortcuts.locator("[data-slot=choice-card]")
  await expect(cards).toHaveText([
    /New project.*6 projects/,
    /Add a database.*4 databases/,
    /New shell.*3 open · operator/,
    /New drawing.*1 drawing/,
    /Browse files.*atlas/,
    /Add a computer.*2 computers/,
    /Back up.*3 jobs/,
  ])
  // Four across at this width.
  const [first, fifth] = await Promise.all([cards.nth(0).boundingBox(), cards.nth(4).boundingBox()])
  expect(fifth?.x).toBe(first?.x)
  expect(fifth!.y).toBeGreaterThan(first!.y)

  // A drawing is made where the shortcut is pressed, and opened.
  await cards.filter({ hasText: "New drawing" }).click()
  await expect(page).toHaveURL(/\/excalidraw\/9$/)
  expect(created).toEqual({ name: "Untitled drawing" })
})

test("a new shell opens in the account's home", async ({ page }) => {
  await page.goto("/")
  await page.locator("[data-slot=choice-card]", { hasText: "New shell" }).click()
  await expect(page).toHaveURL(/\/terminal\?cwd=%2Fhome%2Foperator$/)
})

test("the shells left open lead with the one working, and open on themselves", async ({ page }) => {
  await page.goto("/")
  const panel = page.locator("[data-slot=panel]", {
    has: page.getByRole("heading", { name: "Terminal", exact: true }),
  })
  await expect(panel).toContainText("3 sessions · 1 working")
  const rows = panel.getByRole("list", { name: "Terminal sessions" }).locator(":scope > li")
  await expect(rows).toHaveCount(3)
  await expect(rows.first()).toContainText("claude")
  await expect(rows.first()).toContainText("~/srv/shop")
  await expect(rows.first()).toContainText("Working")
  await expect(rows.first().locator('img[src="/logos/claude.svg"]')).toBeVisible()
  await expect(rows.nth(1)).toContainText("Idle")
  await expect(rows.first().getByRole("link", { name: "Open claude" })).toHaveAttribute(
    "href",
    "/terminal?session=s-agent",
  )
})

test("the drawings edited last are each drawn as themselves", async ({ page }) => {
  await page.goto("/")
  const list = page.getByRole("list", { name: "Recent drawings" })
  const cards = list.locator(":scope > li")
  await expect(cards).toHaveText([/Production map/])
  await expect(cards.first().getByRole("link", { name: "Open Production map" })).toHaveAttribute(
    "href",
    "/excalidraw/4",
  )
  // The picture is the drawing's own shapes.
  await expect(cards.first().locator("svg text", { hasText: "caddy" })).toBeAttached()
  await cards.first().click()
  await expect(page).toHaveURL(/\/excalidraw\/4$/)
})

test("databases lead with the one not answering, and say what each holds", async ({ page }) => {
  await page.goto("/")
  const panel = page.locator("[data-slot=panel]", {
    has: page.getByRole("heading", { name: "Databases", exact: true }),
  })
  await expect(panel).toContainText("4 databases · 1 not answering")
  const rows = panel.getByRole("list", { name: "Databases" }).locator(":scope > li")
  await expect(rows.first()).toContainText("blog")
  await expect(rows.first()).toContainText("unreachable")
  await expect(rows.first()).toContainText("connection refused")
  await expect(rows.nth(3)).toContainText("PostgreSQL 16.4 · 2.1 GB · 42 tables")
  await expect(rows.nth(3).getByRole("link", { name: "Open shop" })).toHaveAttribute(
    "href",
    "/databases/1",
  )
})

test("a failed backup job leads even after a newer run elsewhere succeeded", async ({ page }) => {
  let started = ""
  await page.route("**/api/v1/backups/*/run", (route) => {
    started = new URL(route.request().url()).pathname
    return json(route, {})
  })
  await page.goto("/")
  const panel = page.locator("[data-slot=panel]", {
    has: page.getByRole("heading", { name: "Backups", exact: true }),
  })
  await expect(panel).toContainText("3 jobs · 1 failed")
  const rows = panel.getByRole("list", { name: "Backup jobs" }).locator(":scope > li")
  await expect(rows.first()).toContainText("Databases nightly")
  await expect(rows.first()).toContainText(/failed 7h/)
  await expect(rows.first().getByRole("link", { name: "Open Databases nightly" })).toHaveAttribute(
    "href",
    "/backups/1",
  )
  await rows.first().getByRole("button", { name: "Back up Databases nightly now" }).click()
  await expect.poll(() => started).toBe("/api/v1/backups/1/run")
  await expect(page).toHaveURL(/\/$/)
})

test("a reader without write access sees no shortcut that would refuse them", async ({ page }) => {
  const requested: string[] = []
  page.on("request", (request) => requested.push(new URL(request.url()).pathname))
  await page.route("**/api/v1/auth/session", (route) =>
    json(route, { ...user, capabilities: ["read"], user: { ...user.user, role: "viewer" } }),
  )
  await page.goto("/")
  await expect(page.locator("[data-slot=choice-card]")).toHaveText([/Browse files/])
  // No terminal: its listing needs the terminal, and is never asked for.
  await expect(page.getByRole("heading", { name: "Terminal", exact: true })).toHaveCount(0)
  await expect(page.getByRole("list", { name: "Recent drawings" })).toBeVisible()
  await expect(page.getByRole("link", { name: "Remote Desktop", exact: true })).toHaveCount(0)
  expect(requested).not.toContain("/api/v1/terminal/")
  expect(requested).not.toContain("/api/v1/remote-desktop/")
})

test("a section kept out of the sidebar is kept off the Overview, read and all", async ({
  page,
}) => {
  const requested: string[] = []
  page.on("request", (request) => requested.push(new URL(request.url()).pathname))
  await page.route("**/api/v1/auth/session", (route) =>
    json(route, {
      ...user,
      user: { ...user.user, hiddenSections: ["terminal", "excalidraw", "backups"] },
    }),
  )
  await page.goto("/")
  await expect(page.getByRole("heading", { name: "Databases", exact: true })).toBeVisible()
  for (const name of ["Terminal", "Drawings", "Backups"]) {
    await expect(page.getByRole("heading", { name, exact: true })).toHaveCount(0)
  }
  // Alone in its row, the databases take the whole of it.
  const panel = page.locator("[data-slot=panel]", {
    has: page.getByRole("heading", { name: "Databases", exact: true }),
  })
  const width = (await panel.boundingBox())!.width
  const page_ = (await page.locator("[data-slot=page] > [data-slot=host-identity]").boundingBox())!
  expect(width).toBe(page_.width)
  for (const path of ["/api/v1/terminal/", "/api/v1/boards/", "/api/v1/backups/"]) {
    expect(requested).not.toContain(path)
  }
  await expect(page.locator("[data-slot=choice-card]", { hasText: "New shell" })).toHaveCount(0)
})

test("on a phone the tiles are two to a row and the fleet keeps its worst three", async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 900 })
  await page.goto("/")
  const cards = page.getByRole("list", { name: "Deployment projects" }).locator(":scope > li")
  await expect(cards).toHaveCount(3)
  await expect(cards.first()).toContainText("docs-site")
  await expect(page.getByText("Showing 3 of 6")).toBeVisible()
  const shortcuts = page.locator("[data-slot=choice-card]")
  const [a, b] = await Promise.all([shortcuts.nth(0).boundingBox(), shortcuts.nth(1).boundingBox()])
  expect(a?.y).toBe(b?.y)
  const resources = page.locator("section", {
    has: page.getByRole("heading", { name: "Resources" }),
  })
  const [cpu, memory] = await Promise.all(
    ["CPU", "Memory"].map((name) =>
      resources.locator("[data-slot=stat-tile]", { hasText: name }).first().boundingBox(),
    ),
  )
  expect(cpu?.y).toBe(memory?.y)
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth > document.documentElement.clientWidth,
    ),
  ).toBe(false)
})
