import { expect, test, type Page } from "@playwright/test"
import { fleet, mockOverview, selfUpdate } from "./overview-fixture"
import { host, iso, json, now, snapshot, user } from "./host-fixture"
import { risen } from "./risen"

/**
 * The host Overview, against a server with something in every place: the
 * machine drawn as itself with its state and its verbs, the shortcuts that
 * start something, each reading a sentence carrying its last hour, the
 * projects as the fleet draws them, the shells and drawings left open, the
 * databases beside the backups, who is spending the machine beside the day's
 * activity, and a row per module saying what it counts in one sentence.
 */

const viewer = { ...user, capabilities: ["read"], user: { ...user.user, role: "viewer" } }

/**
 * Holds the machine's first frame, by socket and by request, until the
 * returned function is called: the page as it stands while it waits.
 */
async function holdFrame(page: Page, frame: unknown = snapshot) {
  let release = () => {}
  const held = new Promise<void>((resolve) => (release = resolve))
  await page.routeWebSocket("**/api/v1/system/stream**", async (socket) => {
    await held
    socket.send(JSON.stringify({ type: "host", data: host }))
    socket.send(JSON.stringify({ type: "metrics", data: frame }))
  })
  for (const [path, body] of [
    ["/system/host", host],
    ["/system/metrics", frame],
  ] as const)
    await page.route(`**/api/v1${path}`, async (route) => {
      await held
      await json(route, body)
    })
  return release
}

/** One read answered only once the returned function is called. */
async function holdRead(page: Page, pattern: string, body: unknown) {
  let release = () => {}
  const held = new Promise<void>((resolve) => (release = resolve))
  await page.route(pattern, async (route) => {
    await held
    await json(route, body)
  })
  return release
}

const refusal = (code: string, message: string) => ({
  status: 503,
  contentType: "application/json",
  body: JSON.stringify({ error: { code, message } }),
})

test.beforeEach(async ({ page }) => {
  await mockOverview(page)
})

test("the host is drawn as itself, and its facts are one line", async ({ page }) => {
  await page.goto("/")
  await expect(page.getByRole("heading", { name: "atlas" })).toHaveClass(/sr-only/)
  await expect(
    page.locator('[data-slot="host-identity"]').getByText("atlas", { exact: true }),
  ).toBeVisible()
  const identity = page.locator('[data-slot="host-identity"]')
  await expect(identity.getByText("Ubuntu 24.04", { exact: true })).toBeVisible()
  await expect(identity.getByText("6.8.0-45-generic", { exact: true })).toBeVisible()
  // The distribution, the processor and the hypervisor, each its own mark.
  for (const logo of ["ubuntu", "amd", "qemu"]) {
    await expect(page.locator(`img[src="/logos/${logo}.svg"]`).first()).toBeVisible()
  }
  await expect(page.getByText("184 processes")).toBeVisible()
})

/** `GET /dashboard/health` as the store monitor reports it, healthy unless told otherwise. */
function dashboardHealth(store: Record<string, unknown> = {}, tasks: unknown[] = []) {
  const report = {
    ok: true,
    checkedAt: iso(now),
    lastWriteAt: iso(now),
    path: "/var/lib/just-dashboard",
    freeBytes: 30 * 1024 ** 3,
    totalBytes: 40 * 1024 ** 3,
    reserveBytes: 32 * 1024 ** 2,
    reserveHeld: true,
    reserveReleased: false,
    ...store,
  }
  return { ok: report.ok && tasks.length === 0, store: report, tasks }
}

test("a full disk under the dashboard's data is said first, to a reader as well", async ({
  page,
}) => {
  await page.route("**/api/v1/auth/session", (route) =>
    json(route, { ...user, capabilities: ["read"], user: { ...user.user, role: "viewer" } }),
  )
  await page.route("**/api/v1/dashboard/health", (route) =>
    json(
      route,
      dashboardHealth({
        ok: false,
        code: "disk_full",
        detail: "database or disk is full (13)",
        since: iso(now - 5 * 60_000),
        freeBytes: 30 * 1024 ** 2,
        reserveHeld: false,
        reserveReleased: true,
      }),
    ),
  )
  await page.goto("/")
  const title = page.getByText("The server's disk is full", { exact: true })
  await expect(title).toBeVisible()
  await expect(
    page.getByText(
      /is free on the disk holding the dashboard's data \(\/var\/lib\/just-dashboard\)/,
    ),
  ).toBeVisible()
  await expect(page.getByText(/gave up its 32 MB reserve/)).toBeVisible()
  await expect(page.getByText("database or disk is full (13)", { exact: true })).toBeVisible()
  // Before the machine's own line: nothing below it can be trusted to save.
  const said = await title.boundingBox()
  const identity = await page.locator('[data-slot="host-identity"]').boundingBox()
  expect(said!.y).toBeLessThan(identity!.y)
})

test("a background task that keeps crashing is a warning beside nothing else", async ({ page }) => {
  await page.route("**/api/v1/dashboard/health", (route) =>
    json(
      route,
      dashboardHealth({}, [
        {
          name: "nginx status sampler",
          running: true,
          startedAt: iso(now - 60_000),
          panics: 4,
          lastPanic: "index out of range [3] with length 2",
          lastPanicAt: iso(now - 2 * 60_000),
        },
      ]),
    ),
  )
  await page.goto("/")
  await expect(
    page.getByText("A background task of the dashboard is crashing", { exact: true }),
  ).toBeVisible()
  await expect(page.getByText(/The nginx status sampler failed 4 times/)).toBeVisible()
  await expect(
    page.getByText("index out of range [3] with length 2", { exact: true }),
  ).toBeVisible()
  await expect(page.getByText("The server's disk is full")).toHaveCount(0)
})

test("a dashboard in working order says nothing of itself", async ({ page }) => {
  const asked = page.waitForResponse("**/api/v1/dashboard/health")
  await page.route("**/api/v1/dashboard/health", (route) => json(route, dashboardHealth()))
  await page.goto("/")
  await asked
  await expect(page.locator('[data-slot="host-identity"]')).toBeVisible()
  await expect(page.getByText("The server's disk is full")).toHaveCount(0)
  await expect(page.getByText(/background tasks? of the dashboard/)).toHaveCount(0)
})

test("a reading several components need is asked for once on load", async ({ page }) => {
  // The rail, the dock and the page each read the assistant's settings; they
  // share one keyed poll, where the load used to ask three times within 12 ms.
  const asked: string[] = []
  page.on("request", (request) => {
    const path = new URL(request.url()).pathname
    if (path === "/api/v1/assistant" || path === "/api/v1/deploy/traffic") asked.push(path)
  })
  await page.goto("/")
  await expect(
    page.locator('[data-slot="host-identity"]').getByText("atlas", { exact: true }),
  ).toBeVisible()
  await expect.poll(() => asked.filter((path) => path.endsWith("/deploy/traffic"))).toHaveLength(1)
  await page.waitForTimeout(1000)
  expect(asked.filter((path) => path.endsWith("/assistant"))).toHaveLength(1)
  expect(asked.filter((path) => path.endsWith("/deploy/traffic"))).toHaveLength(1)
})

test("each reading is a sentence that carries its last hour", async ({ page }) => {
  await page.goto("/")
  const cpu = page.locator("[data-slot=reading-line]", { hasText: "CPU" }).first()
  await expect(cpu.getByRole("img", { name: "CPU over the last hour" })).toBeVisible({
    timeout: 20_000,
  })
  await expect(cpu).toContainText("23.4% busy")
  await expect(cpu).toContainText("17% user · 5% sys · 1% wait")
  await expect(page.getByRole("img", { name: "Network over the last hour" })).toBeVisible()
  // The panel of sparklines that repeated the readings is gone.
  await expect(page.getByRole("heading", { name: "Last hour" })).toHaveCount(0)
  // Every core from the same frame, beside the figure they add up to.
  await expect(cpu.getByRole("img", { name: "4 cores, the busiest at 31%" })).toBeVisible()

  const lines = page.locator("[data-slot=resources] [data-slot=reading-line]")
  await expect(lines).toHaveText([
    /CPU.*23\.4% busy/,
    /Memory.*6\.5 GB free.*56% used · 4\.0 GB cached/,
    /Load.*0\.92, 0\.23 a core.*0\.71 · 0\.60 over 5 and 15 min/,
    /Network.*2\.3 MB\/s in.*420\.0 KB\/s out · 312 TCP sockets/,
    /Disk I\/O.*5\.1 MB\/s.*1\.2 MB\/s read · 3\.9 MB\/s write/,
  ])
  // Every line is one height, and each is a way to the page that records it.
  await risen(page)
  const boxes = await lines.evaluateAll((all) => all.map((line) => line.getBoundingClientRect()))
  expect(new Set(boxes.map((box) => box.height)).size).toBe(1)
  await expect(lines.first()).toHaveAttribute("href", "/metrics")
  await expect(lines.first()).toHaveAccessibleName("CPU on Metrics")
})

test("no metric card is left on the page", async ({ page }) => {
  await page.goto("/")
  await expect(page.locator("[data-slot=resources] [data-slot=reading-line]")).toHaveCount(5)
  await expect(page.getByRole("list", { name: "Services" }).locator(":scope > li")).toHaveCount(8)
  await expect(page.locator("[data-slot=stat-grid]")).toHaveCount(0)
  await expect(page.locator("[data-slot=stat-tile]")).toHaveCount(0)
})

test("the machine's line says it is answering and carries its verbs", async ({ page }) => {
  await page.goto("/")
  const identity = page.locator('[data-slot="host-identity"]')
  await expect(identity).toHaveAttribute("data-layout", "project")
  await expect(identity.getByText("atlas", { exact: true })).toBeVisible()
  await expect(identity.getByText("Online", { exact: true })).toBeVisible()
  // One command, a shell in the account's home, and the rest behind the menu.
  await expect(identity.getByRole("link")).toHaveCount(1)
  await expect(identity.getByRole("link", { name: "Open terminal" })).toHaveAttribute(
    "href",
    "/terminal?cwd=%2Fhome%2Foperator",
  )
  await identity.getByRole("button", { name: "Server actions" }).click()
  await expect(page.getByRole("menuitem")).toHaveText([
    "Update 7 packages",
    "Restart the dashboard",
  ])
  // Restart is confirmed and followed on the page that owns it.
  await page.getByRole("menuitem", { name: "Restart the dashboard" }).click()
  await expect(page).toHaveURL(/\/dashboard\/configuration$/)
})

test("a version to take is offered from the machine's menu, in the dialog that installs it", async ({
  page,
}) => {
  await page.route("**/api/v1/dashboard/update**", (route) =>
    json(route, {
      ...selfUpdate,
      latest: "0.7.2",
      available: true,
      tagged: true,
      releases: [{ version: "0.7.2", date: "2026-10-20", title: "A fix", changes: [] }],
    }),
  )
  await page.goto("/")
  await expect(
    page
      .getByRole("list", { name: "Services" })
      .getByRole("listitem")
      .filter({ hasText: "Settings" }),
  ).toContainText("Version 0.7.1, 1 release behind")
  const identity = page.locator('[data-slot="host-identity"]')
  await identity.getByRole("button", { name: "Server actions" }).click()
  await page.getByRole("menuitem", { name: "Update to 0.7.2" }).click()
  await expect(page.getByRole("dialog", { name: "Update to 0.7.2" })).toBeVisible()
})

test("a restart the packages owe is said beside the machine's state", async ({ page }) => {
  await page.route("**/api/v1/packages/updates", (route) =>
    json(route, {
      available: true,
      manager: "apt",
      packages: [],
      securityCount: 0,
      securityFiltering: true,
      rebootRequired: true,
      lastChecked: iso(now),
    }),
  )
  await page.goto("/")
  const identity = page.locator('[data-slot="host-identity"]')
  await expect(identity.getByText("Restart needed", { exact: true })).toBeVisible()
  await expect(
    page
      .getByRole("list", { name: "Services" })
      .getByRole("listitem")
      .filter({ hasText: "Packages" }),
  ).toContainText("Nothing to update, restart needed")
})

test("the readings lead on to Metrics", async ({ page }) => {
  await page.goto("/")
  const resources = page.locator("section", {
    has: page.getByRole("heading", { name: "Resources" }),
  })
  await expect(resources.locator("[data-slot=reading-line]")).toHaveCount(5)
  await resources.getByRole("link", { name: "All metrics" }).click()
  await expect(page).toHaveURL(/\/metrics$/)
})

test("every filesystem is a bar of its own after the readings", async ({ page }) => {
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
  const io = page.locator("[data-slot=reading-line]", { hasText: "Disk I/O" })
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

test("a service row says what it counts in a sentence, with the products", async ({ page }) => {
  await page.goto("/")
  const services = page.getByRole("list", { name: "Services" })
  const row = (name: string) =>
    services.locator(":scope > li", { has: page.getByRole("link", { name: `Open ${name}` }) })
  const docker = row("Docker")
  await expect(docker).toContainText("9 containers running, 2 stopped")
  await expect(docker).toContainText("Running")
  await expect(docker.getByRole("link", { name: "Open Docker" })).toHaveAttribute("href", "/docker")
  // Drawn as itself, and the running images, not the stopped ones.
  await expect(docker.locator('img[src="/logos/docker.svg"]')).toBeAttached()
  await expect(docker.locator('img[src="/logos/postgresql.svg"]')).toBeAttached()
  await expect(docker.locator('img[src="/logos/redis.svg"]')).toBeAttached()
  await expect(docker.locator('img[src="/logos/meilisearch.svg"]')).toHaveCount(0)
  await expect(row("Security")).toContainText("Tailscale only, 1 allowed range on 1 interface")
  await expect(row("Security").locator('img[src="/logos/tailscale.svg"]')).toBeAttached()
  await expect(row("Proxy & TLS")).toContainText("3 certificates, the next renews in 12 days")
  await expect(row("Proxy & TLS").getByText("Expiring", { exact: true })).toHaveClass(
    /text-warning/,
  )
  await expect(row("Packages")).toContainText("7 updates waiting, 2 for security")

  // Deployments, Databases and Backups have blocks of their own, so their
  // rows went to what systemd runs and the remote computers.
  for (const name of ["Deployments", "Databases", "Backups"]) {
    await expect(services.getByRole("link", { name: `Open ${name}`, exact: true })).toHaveCount(0)
  }
  const units = row("System services")
  await expect(units).toContainText("1 failed, 6 services running")
  await expect(units.getByText("Failed", { exact: true })).toHaveClass(/text-destructive/)
  await expect(row("Remote Desktop")).toContainText("2 computers, 1 open now")
  const git = row("Git")
  await expect(git).toContainText("4 repositories, 1 uncommitted, 1 behind")
  for (const forge of ["gitlab", "codeberg", "github"]) {
    await expect(git.locator(`img[src="/logos/${forge}.svg"]`)).toBeAttached()
  }
  await expect(row("Settings")).toContainText("Version 0.7.1, the newest")
  // A press anywhere on a row opens its page.
  await units.click({ position: { x: 300, y: 20 } })
  await expect(page).toHaveURL(/\/processes\/services$/)
})

for (const width of [1280, 390]) {
  test(`a service row is one height whatever its read says, at ${width}`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 })
    // Docker is not on this host, the certificates fail to read, and the
    // units are still out: three other states beside the rows that answered.
    await page.route("**/api/v1/docker/containers/**", (route) =>
      route.fulfill(refusal("docker_unavailable", "docker is not reachable")),
    )
    await page.route("**/api/v1/certificates/", (route) =>
      route.fulfill(refusal("unavailable", "the store could not be read")),
    )
    const answer = await holdRead(page, "**/api/v1/systemd/", {
      available: true,
      units: [{ name: "a.service", activeState: "active" }],
    })
    await page.goto("/")
    const rows = page.getByRole("list", { name: "Services" }).locator(":scope > li")
    await expect(rows).toHaveCount(8)
    await expect(rows.filter({ hasText: "Docker" })).toContainText("Not on this server")
    await expect(rows.filter({ hasText: "Docker" })).toContainText("Not installed")
    await expect(rows.filter({ hasText: "Proxy & TLS" })).toContainText(
      "Its reading did not arrive",
    )
    await expect(rows.filter({ hasText: "Proxy & TLS" })).toContainText("Unreachable")
    await expect(rows.filter({ hasText: "Git" })).toContainText("4 repositories")
    const waiting = rows.filter({ hasText: "System services" })
    await expect(waiting.locator("[data-slot=skeleton]")).toBeVisible()
    await risen(page)
    const measure = () =>
      rows.evaluateAll((all) =>
        all.map((row) => {
          const box = row.getBoundingClientRect()
          return { y: Math.round(box.y), height: box.height }
        }),
      )
    const before = await measure()
    expect(new Set(before.map((box) => box.height)).size).toBe(1)
    answer()
    await expect(waiting).toContainText("1 service running, none failed")
    await risen(page)
    // The read landing moved no row.
    expect(await measure()).toEqual(before)
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth > document.documentElement.clientWidth,
      ),
    ).toBe(false)
  })

  test(`the page holds its shape until the machine's first frame, at ${width}`, async ({
    page,
  }) => {
    await page.setViewportSize({ width, height: 900 })
    // One disk, the common server: its bar sits beside the disks' reading.
    const frame = { ...snapshot, mounts: snapshot.mounts.slice(0, 1) }
    const release = await holdFrame(page, frame)
    await page.goto("/")
    const identity = page.locator('[data-slot="host-identity"]')
    const blocks = [
      identity,
      page.locator("[data-slot=shortcuts]"),
      page.locator("[data-slot=resources]"),
      page.getByRole("heading", { name: "Deployments" }),
      page.getByRole("heading", { name: "Databases", exact: true }),
      page.getByRole("list", { name: "Services" }),
    ]
    // Everything under the machine's line is already there and reading.
    await expect(page.getByRole("list", { name: "Deployment projects" })).toBeVisible()
    await expect(page.getByRole("list", { name: "Databases" })).toBeVisible()
    await expect(identity.locator("[data-slot=skeleton]").first()).toBeVisible()
    await expect(page.locator("[data-slot=resources] [data-slot=reading-line]")).toHaveCount(6)
    await risen(page)
    const measure = () =>
      Promise.all(
        blocks.map(async (block) => {
          const box = (await block.boundingBox())!
          return { y: Math.round(box.y), height: Math.round(box.height) }
        }),
      )
    const before = await measure()
    release()
    await expect(identity.getByText("atlas", { exact: true })).toBeVisible()
    await expect(page.locator("[data-slot=storage-band]")).toContainText("10.4 GB free")
    await risen(page)
    expect(await measure()).toEqual(before)
  })
}

test("a reader sees no command on the machine's line that would refuse them", async ({ page }) => {
  await page.route("**/api/v1/auth/session", (route) => json(route, viewer))
  await page.route("**/api/v1/packages/updates", (route) =>
    json(route, {
      available: true,
      manager: "apt",
      packages: [],
      securityCount: 0,
      securityFiltering: true,
      rebootRequired: false,
      lastChecked: iso(now),
    }),
  )
  await page.goto("/")
  const identity = page.locator('[data-slot="host-identity"]')
  await expect(identity.getByText("Online", { exact: true })).toBeVisible()
  await expect(identity.getByRole("link")).toHaveCount(0)
  await expect(identity.getByRole("button")).toHaveCount(0)
})

test("a process in the top list opens its own page", async ({ page }) => {
  await page.goto("/")
  const processes = page.locator("[data-slot=panel]", {
    has: page.getByRole("heading", { name: "Top processes" }),
  })
  const rows = processes.locator("[data-slot=rank-row]")
  await expect(rows.first()).toContainText("postgres")
  await expect(rows.first()).toContainText("12.4%")
  await expect(rows.first()).toContainText("812 MB")
  const link = rows.first().getByRole("link", { name: "Open postgres, process 1204" })
  await expect(link).toHaveAttribute("href", "/processes/1204")
  // By memory the columns change places, and the block keeps its height.
  await risen(page)
  const before = (await processes.boundingBox())!.height
  await processes.getByRole("radio", { name: "Memory" }).click()
  await expect(rows.first().locator("[data-slot=rank-value]")).toHaveText("2 GB")
  await risen(page)
  expect((await processes.boundingBox())!.height).toBe(before)
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
      page.getByRole("list", { name: "Services" }).getByRole("listitem").filter({ hasText: "Git" }),
    ).toContainText("4 repositories")
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
  const cards = shortcuts.locator("[data-slot=shortcut]")
  await expect(cards).toHaveText([
    /New project.*6 projects/,
    /New terminal.*3 open · operator/,
    /New drawing.*1 drawing/,
    /Browse files.*atlas/,
  ])
  // One line of four at this width, and no taller than the label and its count need.
  const boxes = await Promise.all([0, 1, 2, 3].map((n) => cards.nth(n).boundingBox()))
  expect(new Set(boxes.map((box) => box!.y)).size).toBe(1)
  expect(boxes[0]!.height).toBeLessThanOrEqual(64)

  // A drawing is made where the shortcut is pressed, and opened.
  await cards.filter({ hasText: "New drawing" }).click()
  await expect(page).toHaveURL(/\/excalidraw\/9$/)
  expect(created).toEqual({ name: "Untitled drawing" })
})

test("a new terminal opens in the account's home, and is a link the whole way across", async ({
  page,
}) => {
  await page.goto("/")
  const card = page.getByRole("link", { name: /New terminal/ })
  await expect(card).toHaveAttribute("href", "/terminal?cwd=%2Fhome%2Foperator")
  const box = (await card.boundingBox())!
  await page.mouse.click(box.x + box.width - 4, box.y + box.height - 4)
  // Only the path: the terminal takes the directory off the address once it has used it.
  await expect(page).toHaveURL(/\/terminal(\?|$)/)
})

test("nothing but the four shortcuts is offered", async ({ page }) => {
  const requested: string[] = []
  page.on("request", (request) => requested.push(new URL(request.url()).pathname))
  await page.goto("/")
  await expect(page.locator("[data-slot=shortcut]")).toHaveCount(4)
  // The list of sites was only ever read to count them on a card that has gone.
  expect(requested).not.toContain("/api/v1/proxy/vhosts")
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
  await expect(page.locator("[data-slot=shortcut]")).toHaveText([/Browse files/])
  // No terminal: its listing needs the terminal, and is never asked for.
  await expect(page.getByRole("heading", { name: "Terminal", exact: true })).toHaveCount(0)
  await expect(page.getByRole("list", { name: "Recent drawings" })).toBeVisible()
  await expect(page.getByRole("link", { name: "Open Remote Desktop", exact: true })).toHaveCount(0)
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
  await expect(page.locator("[data-slot=shortcut]", { hasText: "New terminal" })).toHaveCount(0)
})

test("on a phone the readings are one column and the fleet keeps its worst three", async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 900 })
  await page.goto("/")
  const cards = page.getByRole("list", { name: "Deployment projects" }).locator(":scope > li")
  await expect(cards).toHaveCount(3)
  await expect(cards.first()).toContainText("docs-site")
  await expect(page.getByText("Showing 3 of 6")).toBeVisible()
  // The shortcuts stand two by two, and every label is whole.
  const shortcuts = page.locator("[data-slot=shortcut]")
  const boxes = await Promise.all([0, 1, 2, 3].map((n) => shortcuts.nth(n).boundingBox()))
  expect(boxes[0]?.y).toBe(boxes[1]?.y)
  expect(boxes[2]?.y).toBe(boxes[3]?.y)
  expect(boxes[2]!.y).toBeGreaterThan(boxes[0]!.y)
  expect(
    await shortcuts.evaluateAll((cards) =>
      cards.every((card) => {
        const label = card.querySelector<HTMLElement>("span.font-medium")!
        return label.scrollWidth <= label.clientWidth
      }),
    ),
  ).toBe(true)
  const resources = page.locator("section", {
    has: page.getByRole("heading", { name: "Resources" }),
  })
  // One reading to a line, each the phone's width, none cut off.
  const lines = resources.locator("[data-slot=reading-line]")
  await expect(lines).toHaveCount(5)
  const [cpu, memory] = await Promise.all([0, 1].map((n) => lines.nth(n).boundingBox()))
  expect(memory!.y).toBeGreaterThan(cpu!.y)
  expect(cpu!.width).toBe(memory!.width)
  expect(
    await lines.evaluateAll((all) =>
      all.every((line) =>
        [...line.querySelectorAll<HTMLElement>(".truncate")].every(
          (text) => text.scrollWidth <= text.clientWidth,
        ),
      ),
    ),
  ).toBe(true)
  // The machine's line keeps its command beside its name.
  const identity = page.locator('[data-slot="host-identity"]')
  await expect(identity.getByRole("link", { name: "Open terminal" })).toBeVisible()
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth > document.documentElement.clientWidth,
    ),
  ).toBe(false)
})
