import { expect, test, type Page, type Route } from "@playwright/test"
import { readTip } from "./read-tip"
import { risen } from "./risen"

/**
 * The dashboard's own two pages — Settings (its version) and Configuration —
 * against a mocked API, in the shape the design system's §15 asks for: the
 * checks that can be asserted, and a screenshot at 1280 and 1720 for the ones
 * that cannot.
 *
 * Both pages ended the 0.6.7 redesign with no framed block on them. That is
 * asserted structurally — every `Panel` on the page carries `data-plain` — so
 * a box cannot come back by accident on either. The 0.7.0 pass took the row of
 * readings off the top of both (each figure moved beside the control or the
 * release it describes) and the tinted banners out of the restart record, and
 * both of those are asserted too. The one frame Configuration has since 0.7.1
 * is the danger rule around Uninstall, which the design system gives every
 * page's acts that end something.
 *
 * The 0.8.7 pass brought both to the deployment pages' shape. Each opens on
 * the identity line laid out as a project's header; Configuration's settings
 * are one centred column with the sentences behind each name's ⓘ and the
 * settings pages' one floating bar, Restart and Rebuild are the line's `⋯`,
 * and what `.env` holds that is not running is the strip at the column's
 * head. The Version page says what an update needs in a line each.
 */

const now = new Date().toISOString()
const earlier = new Date(Date.now() - 40_000).toISOString()

const user = {
  authenticated: true,
  needsTotp: false,
  needsEnrollment: false,
  require2fa: false,
  capabilities: [
    "read",
    "service.control",
    "file.write",
    "terminal",
    "destructive",
    "system.admin",
  ],
  user: {
    id: 1,
    username: "operator",
    role: "admin",
    totpEnabled: true,
    disabled: false,
    mustChangePassword: false,
    lastLoginAt: now,
    createdAt: now,
  },
}

const dir = "/opt/just-dashboard"

const update = {
  version: "0.6.7",
  latest: "0.7.0",
  available: true,
  releases: [
    {
      version: "0.7.0",
      date: "2026-09-10",
      title: "Give the dashboard pages the design system",
      summary: "The panel's own pages read like the host overview.",
      changes: [
        { kind: "added", text: "Configuration is edited from the browser" },
        {
          kind: "fixed",
          text: "The transcript follows the restart it describes",
          detail: "A reload during the restart used to lose it.",
        },
        { kind: "security", text: "The allowlist runs before the login page" },
      ],
      breaking: true,
      breakingNote: "The compose file gains a service; run the installer once after updating.",
    },
  ],
  history: [
    {
      version: "0.6.7",
      date: "2026-08-30",
      title: "Flatten the surfaces",
      changes: [
        { kind: "changed", text: "Nothing lifts" },
        { kind: "removed", text: "The badge" },
      ],
    },
    {
      version: "0.6.6",
      date: "2026-08-01",
      title: "Deployments",
      changes: [{ kind: "added", text: "Automatic Git deployments" }],
    },
  ],
  breaking: true,
  tagged: true,
  check: {
    enabled: true,
    checkedAt: earlier,
    repo: "wayy/just-dashboard",
    ref: "main",
    tagsOnly: true,
  },
  install: { supported: true, dir, compose: `${dir}/docker-compose.yml`, dirty: ["M Caddyfile"] },
}

const settings = {
  site: "atlas.tail1234.ts.net",
  bind: "100.64.0.7",
  tls: "tailscale",
  port: 8443,
  frontendPort: 3000,
  backendPort: 8080,
  allowedCidrs: "100.64.0.0/10,127.0.0.1/32",
  terminalEnabled: true,
  require2fa: true,
  sessionTtl: "12h",
  idleTtl: "60m",
  updateCheck: true,
}

const config = {
  supported: true,
  dir,
  compose: `${dir}/docker-compose.yml`,
  envPath: `${dir}/.env`,
  settings,
  endpoint: "https://atlas.tail1234.ts.net:8443",
  tailscale: {
    available: true,
    running: true,
    hostname: "atlas.tail1234.ts.net",
    ip4: "100.64.0.7",
    httpsEnabled: true,
  },
  certificate: { issued: true },
  drift: [{ key: "JD_PORT", label: "Dashboard port", from: "8443", to: "9443" }],
  run: {
    id: "run-1",
    status: "success",
    phase: "finished",
    action: "apply",
    changes: [{ key: "JD_SITE", label: "Address", from: "localhost", to: "atlas.tail1234.ts.net" }],
    dir,
    compose: `${dir}/docker-compose.yml`,
    image: "just-dashboard:0.6.7",
    container: "jd-config",
    actor: "operator",
    startedAt: earlier,
    updatedAt: now,
    finishedAt: now,
  },
  // The end of the file, as the report carries it; the console reads the
  // whole of it from /dashboard/config/log.
  log: "… earlier output trimmed …\n Container jd-backend-1 Recreated\n Container jd-backend-1 Healthy\n",
}

const wholeLog = [
  "Applying Just Dashboard requested by operator",
  "stack: /opt/just-dashboard (docker-compose.yml)",
  "",
  "$ docker compose -f docker-compose.yml up -d --remove-orphans --wait",
  " Container jd-backend-1 Recreate",
  " Container jd-backend-1 Recreated",
  " Container jd-backend-1 Healthy",
  "",
].join("\n")

async function json(route: Route, body: unknown) {
  await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(body) })
}

const phrase = "atlas"

const uninstallPlan = {
  phrase,
  dir,
  envPath: `${dir}/.env`,
  data: { path: "/var/lib/just-dashboard", files: 412, bytes: 48_000_000 },
  backups: { path: "/var/backups/just-dashboard", files: 6, bytes: 900_000_000 },
  deployments: {
    projects: 2,
    containers: [
      { id: "c1", name: "jd-e1-r3" },
      { id: "c2", name: "jd-e2-web-1" },
    ],
    routes: [{ id: "just-dashboard-env-1", name: "just-dashboard-env-1" }],
    networks: [{ id: "n1", name: "jd-e2_default" }],
    volumes: [{ id: "uploads", name: "uploads-1a2b" }],
    paths: [],
    images: [{ id: "i1", name: "just-dashboard/deployment-1:run-3" }],
    ingress: { id: "c3", name: "just-dashboard-ingress" },
  },
  durableLog: "/var/log/just-dashboard-uninstall.log",
}

const uninstallRun = {
  id: "20261012T120000Z",
  status: "running",
  phase: "pausing",
  mode: "keep",
  deleteBackups: false,
  dir,
  compose: "docker-compose.yml",
  project: "just-dashboard",
  image: "just-dashboard-backend:latest",
  envPath: `${dir}/.env`,
  dataDir: "/var/lib/just-dashboard",
  backupDir: "/var/backups/just-dashboard",
  container: "just-dashboard-uninstall",
  actor: "operator",
  startedAt: earlier,
  updatedAt: now,
}

const usageBody = '{"version":"0.8.2","usedToday":true,"usedThisWeek":true}'
const usagePing = {
  enabled: true,
  choice: "undecided",
  siteUrl: "https://just-dashboard.com",
  nextBody: usageBody,
}

function withoutHistory(report: unknown) {
  const status = { ...(report as Record<string, unknown>) }
  delete status.history
  return status
}

async function mockApi(page: Page, report: unknown = update) {
  await page.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url())
    const path = url.pathname.replace(/^\/api\/v1/, "")
    if (path === "/auth/session") return json(route, user)
    // The page asks for the report without the changelog and reads that once from a route of
    // its own. As on the server, a request that does not say so gets the whole of it.
    if (path === "/dashboard/update") {
      const asked = url.searchParams.get("history") === "false"
      return json(route, asked ? withoutHistory(report) : report)
    }
    if (path === "/dashboard/update/history") {
      return json(route, { history: (report as { history?: unknown[] }).history ?? [] })
    }
    if (path === "/dashboard/config") return json(route, config)
    if (path === "/dashboard/uninstall") return json(route, { supported: true })
    if (path === "/dashboard/uninstall/plan") return json(route, uninstallPlan)
    if (path === "/dashboard/usage-ping") return json(route, usagePing)
    if (path === "/dashboard/config/log") {
      return route.fulfill({ status: 200, contentType: "text/plain", body: wholeLog })
    }
    return json(route, [])
  })
}

/**
 * Every `Panel` on the page is plain: the redesign's one structural promise.
 * The danger rule is the exception the design system makes on every page,
 * around the acts that cannot be undone.
 */
async function framedPanels(page: Page) {
  return page.evaluate(() =>
    Array.from(
      document.querySelectorAll("[data-slot=panel]:not([data-plain]):not(.border-rule-danger)"),
    ).map((el) => el.outerHTML.slice(0, 120)),
  )
}

/** Where something stands in the settings column, whatever the page has scrolled to. */
async function inColumn(page: Page, selector: string) {
  return page.evaluate((selector) => {
    const column = document.querySelector("[data-slot=settings-column]")!
    const target = document.querySelector(selector)!
    return Math.round(target.getBoundingClientRect().top - column.getBoundingClientRect().top)
  }, selector)
}

async function scrollsSideways(page: Page) {
  return page.evaluate(
    () => document.documentElement.scrollWidth > document.documentElement.clientWidth,
  )
}

test("the settling read after a load is one read, and the idle cadence it sets asks nothing", async ({
  page,
}) => {
  // The load reads the cached answer, one read six seconds later picks up the
  // check the load started, and that answer drops the poll to five minutes.
  // The drop used to start the poll again, so the one read was two, 36 ms apart.
  await page.clock.install()
  await mockApi(page)
  let reads = 0
  await page.route("**/api/v1/dashboard/update**", (route) => {
    if (new URL(route.request().url()).pathname.endsWith("/dashboard/update")) reads += 1
    return route.fallback()
  })
  await page.goto("/dashboard")
  await expect(page.getByRole("heading", { name: "Version", exact: true })).toBeVisible()
  expect(reads).toBe(1)
  await page.clock.fastForward(6_500)
  await expect.poll(() => reads).toBe(2)
  await page.clock.fastForward(60_000)
  await page.waitForTimeout(500)
  expect(reads).toBe(2)
})

test("the version page is the history, with the update in the header", async ({ page }) => {
  await mockApi(page)
  // Every read of the report that is polled says it wants no changelog in it.
  const reports: string[] = []
  page.on("request", (request) => {
    const url = new URL(request.url())
    if (url.pathname === "/api/v1/dashboard/update") reports.push(url.search)
  })
  await page.goto("/dashboard")
  await expect(page.getByRole("heading", { name: "Version", exact: true })).toBeVisible()
  expect(reports.length).toBeGreaterThan(0)
  expect(reports.filter((search) => !search.includes("history=false"))).toEqual([])
  // The readings are gone from the top; what they said is in the identity line.
  await expect(page.locator("[data-slot=stat-tile]")).toHaveCount(0)

  await expect(page.getByRole("button", { name: "Update to 0.7.0" })).toBeVisible()
  // What the check found stands beside the version, so the line is a project's two rows.
  const line = page.locator("[data-slot=host-identity]")
  await expect(line).toHaveAttribute("data-layout", "project")
  await expect(line.getByText("0.7.0 available")).toBeVisible()
  // What is on offer is a release, and the page says how it knows.
  await expect(line.getByText("1 release ahead of yours · tagged v0.7.0")).toBeVisible()
  await risen(page)
  expect((await line.boundingBox())!.height).toBeLessThan(72)

  // The newest release leads the history and the running one is marked.
  const history = page.locator("section", { hasText: "Version history" })
  await expect(history.getByRole("heading", { name: "0.7.0" })).toBeVisible()
  await expect(history.getByText("Installed", { exact: true })).toBeVisible()
  await expect(history.getByText("Before you update")).toBeVisible()

  // What the update needs first is a line each, with its verb at the end.
  const notices = page.locator("[data-slot=notice-lines]")
  await expect(notices.getByRole("listitem")).toHaveCount(2)
  await expect(notices.getByText("0.7.0 needs something done by hand")).toBeVisible()
  await expect(notices.getByText("1 uncommitted change")).toBeVisible()
  await expect(notices).toContainText("/opt/just-dashboard")
  const heights = await notices
    .getByRole("listitem")
    .evaluateAll((items) => items.map((item) => item.getBoundingClientRect().height))
  expect(heights[0]).toBeLessThanOrEqual(48)
  expect(heights[1]).toBe(heights[0])
  // The files are a press away, and nothing on the page moves to show them.
  const before = await history.boundingBox()
  await notices.getByRole("button", { name: "Show" }).click()
  await expect(page.getByText("M Caddyfile")).toBeVisible()
  await expect(page.getByText(/fast-forwards rather than resetting/)).toBeVisible()
  expect((await history.boundingBox())!.y).toBe(before!.y)
  await page.keyboard.press("Escape")

  await page.getByPlaceholder("Find a change, a version or a fix").fill("deployments")
  await expect(history.getByRole("heading", { name: "0.6.6" })).toBeVisible()
  await expect(history.getByRole("heading", { name: "0.7.0" })).toHaveCount(0)
  // The way to the release's own note clears a search that hid it.
  await notices.getByRole("button", { name: "Read the notes" }).click()
  await expect(history.getByRole("heading", { name: "0.7.0" })).toBeVisible()
  await expect(history.getByText("Before you update")).toBeInViewport()

  expect(await framedPanels(page)).toEqual([])
})

test("a version on the branch with no tag is named and not offered", async ({ page }) => {
  await mockApi(page, {
    ...update,
    latest: "0.6.7",
    available: false,
    tagged: false,
    pending: "0.7.0",
    releases: [],
    breaking: false,
  })
  await page.goto("/dashboard")
  await expect(page.getByText("Up to date")).toBeVisible()
  await expect(page.getByText("0.7.0 is on main, not released yet")).toBeVisible()
  await expect(page.getByRole("button", { name: /^Update to/ })).toHaveCount(0)
})

test("a build that is not a release shows the commit it was made from", async ({ page }) => {
  await mockApi(page, { ...update, commit: "98c9349" })
  await page.goto("/dashboard")
  await expect(page.getByText("+98c9349")).toBeVisible()
  await expect(
    page.getByTitle("Built from commit 98c9349, which is not the 0.6.7 release"),
  ).toBeVisible()
})

test("a release shows its version and no commit", async ({ page }) => {
  await mockApi(page)
  await page.goto("/dashboard")
  await expect(page.getByText("Just Dashboard", { exact: true }).first()).toBeVisible()
  await expect(page.getByText(/^\+[0-9a-f]{7,}$/)).toHaveCount(0)
})

test("a fork is told that what it is offered has no release tag", async ({ page }) => {
  await mockApi(page, { ...update, tagged: false, check: { ...update.check, tagsOnly: false } })
  await page.goto("/dashboard")
  await expect(
    page.getByText("1 release ahead of yours · no release tag, installs main as it stands"),
  ).toBeVisible()
  await page.getByRole("button", { name: "Update to 0.7.0" }).click()
  await expect(
    page
      .getByRole("dialog", { name: "Update to 0.7.0" })
      .getByText("from the head of main, which has no release tag"),
  ).toBeVisible()
})

test("the sidebar's update notice leads to the Version page and installs through its own dialog", async ({
  page,
}) => {
  await mockApi(page)
  await page.goto("/dashboard/configuration")

  const notice = page.getByRole("region", { name: "Dashboard update" })
  await expect(notice.getByText("New version")).toBeVisible()
  await expect(notice.getByText("Give the dashboard pages the design system")).toBeVisible()
  // What the releases bring, by kind, in the kinds' own words.
  await expect(notice.getByText("security", { exact: true })).toBeVisible()

  // The patch notes are the Version page, not a panel over this one.
  await notice.getByRole("link", { name: "View patch notes" }).click()
  await expect(page).toHaveURL(/\/dashboard$/)
  await expect(page.getByRole("heading", { name: "Version", exact: true })).toBeVisible()
  await expect(page.locator("[data-slot=sheet-content]")).toHaveCount(0)

  // Installing is taking something, not deleting it: the command is the brand
  // face, and nothing in the dialog is drawn in destructive red.
  await notice.getByRole("button", { name: "Update now" }).click()
  const dialog = page.getByRole("dialog", { name: "Update to 0.7.0" })
  await expect(dialog).toBeVisible()
  const command = dialog.getByRole("button", { name: "Update", exact: true })
  await expect(command).toHaveAttribute("data-variant", "default")
  await expect(dialog.locator("[data-variant=destructive]")).toHaveCount(0)
  // A yes or a no: the notes and what the updater does are on the Version page.
  await expect(dialog.getByText("Before you update")).toHaveCount(0)

  const install = page.waitForRequest(
    (request) =>
      request.method() === "POST" && request.url().endsWith("/api/v1/dashboard/update/install"),
  )
  await command.click()
  expect((await install).postDataJSON()).toEqual({ version: "0.7.0" })
  await expect(dialog).toHaveCount(0)

  // The page's own command opens the same dialog.
  await page.getByRole("button", { name: "Update to 0.7.0" }).click()
  await expect(page.getByRole("dialog", { name: "Update to 0.7.0" })).toBeVisible()
})

test("an update in flight lights the notice and points at its progress", async ({ page }) => {
  await mockApi(page, {
    ...update,
    run: {
      id: "run-1",
      status: "running",
      phase: "building",
      fromVersion: "0.6.7",
      toVersion: "0.7.0",
      ref: "main",
      dir,
      compose: `${dir}/docker-compose.yml`,
      image: "just-dashboard-updater",
      container: "just-dashboard-updater",
      actor: "operator",
      startedAt: earlier,
      updatedAt: now,
    },
  })
  await page.goto("/dashboard/configuration")

  const notice = page.getByRole("region", { name: "Dashboard update" })
  await expect(notice.getByText("Updating", { exact: true })).toBeVisible()
  await expect(notice.getByText("Building and restarting")).toBeVisible()
  await expect(notice.getByRole("button", { name: "Update now" })).toHaveCount(0)
  await expect(notice.getByRole("link", { name: "View progress" })).toHaveAttribute(
    "href",
    "/dashboard",
  )
})

test("the allowlist destination lands on Access after the configuration loads", async ({
  page,
}) => {
  await mockApi(page)
  await page.goto("/dashboard/configuration#allowlist")
  const access = page.locator("#allowlist")
  await expect(access).toBeVisible()
  await expect
    .poll(async () => {
      const box = await access.boundingBox()
      return box?.y ?? -1
    })
    .toBeGreaterThanOrEqual(0)
  const box = await access.boundingBox()
  expect(box!.y).toBeLessThan(page.viewportSize()!.height / 2)
  await expect(access.getByRole("textbox").first()).toBeVisible()
})

test("the configuration page edits in place and says where it lives", async ({ page }) => {
  await mockApi(page)
  await page.goto("/dashboard/configuration")
  await expect(page.getByRole("heading", { name: "Configuration" })).toBeVisible()

  // The paths are the identity line's facts, relative to the checkout.
  await expect(page.getByText(dir, { exact: true })).toBeVisible()
  await expect(page.getByText("docker-compose.yml", { exact: true })).toBeVisible()
  await expect(page.getByText("Last restart")).toBeVisible()
  await expect(page.getByText("Read-only")).toHaveCount(0)
  await expect(page.locator("[data-slot=stat-tile]")).toHaveCount(0)

  // Restart and Rebuild are the identity line's menu, each with what it runs.
  await page.getByRole("button", { name: "Restart or rebuild" }).click()
  await expect(page.getByRole("menuitem", { name: /Restart the dashboard/ })).toBeVisible()
  await expect(page.getByRole("menuitem", { name: /Rebuild and restart/ })).toBeVisible()
  await page.keyboard.press("Escape")

  // The restart record says what happened without a tinted banner, and its
  // transcript is the whole file rather than the tail the report carries.
  const record = page.locator("section", { hasText: "Last restart" })
  await expect(record.locator(".bg-wash-warning, .bg-wash-success")).toHaveCount(0)
  await record.getByRole("button", { name: /Transcript/ }).click()
  const transcript = record.getByRole("list", { name: "Apply transcript" })
  await expect(transcript.getByText("Applying Just Dashboard requested by operator")).toBeVisible()
  await expect(transcript.getByText("earlier output trimmed")).toHaveCount(0)

  // The one pointer to linking the server to an account; the page itself is account-link.spec.ts.
  await expect(page.getByRole("link", { name: "Settings → Connect" })).toHaveAttribute(
    "href",
    "/dashboard/link",
  )

  await expect(page.getByText("unsaved change")).toHaveCount(0)
  await page.getByLabel("Port", { exact: true }).fill("9443")
  await expect(page.getByText("1 unsaved change")).toBeVisible()
  await page.getByRole("button", { name: "Discard" }).click()
  await expect(page.getByText("unsaved change")).toHaveCount(0)

  expect(await framedPanels(page)).toEqual([])
})

test("the stack is the path a request takes, and the allowlist is its networks", async ({
  page,
}) => {
  await mockApi(page)
  await page.goto("/dashboard/configuration")

  // Browser → tailnet → Caddy → the two loopback services, each as its product.
  const path = page.getByRole("list", { name: "How a request reaches the dashboard" })
  await expect(path.getByText("Tailnet", { exact: true })).toBeVisible()
  await expect(path.getByText("atlas.tail1234.ts.net", { exact: true })).toBeVisible()
  await expect(path.getByText(":8443")).toBeVisible()
  await expect(path.getByText("Let's Encrypt")).toBeVisible()
  await expect(path.getByText(":3000")).toBeVisible()
  await expect(path.getByText(":8080")).toBeVisible()

  // Each command says what it runs.
  await page.getByRole("button", { name: "Restart or rebuild" }).click()
  await expect(page.getByText("docker compose up -d --force-recreate")).toBeVisible()
  await expect(page.getByText("docker compose build", { exact: true })).toBeVisible()
  await page.keyboard.press("Escape")

  // Durations are said as a person says them, in the section's head.
  await expect(page.getByText("a sign-in lasts 12 hours, idle ones end after 1 hour")).toBeVisible()
  await page.getByLabel("Idle timeout").fill("soon")
  await expect(page.getByText("Not a duration — try 60m or 2h.")).toBeVisible()
  await page.getByRole("button", { name: "Discard" }).click()

  // The networks are rows; the only loopback entry cannot be removed, since
  // the server refuses a list without it.
  const networks = page.getByRole("list", { name: "Allowed networks" })
  await expect(networks.getByRole("listitem")).toHaveCount(2)
  await expect(networks.getByText("your tailnet")).toBeVisible()
  await expect(page.getByRole("button", { name: "Remove 127.0.0.1/32" })).toHaveCount(0)

  const access = page.locator("section", { hasText: "Network allowlist" }).last()
  await expect(access.getByText("Edited")).toHaveCount(0)
  await page.getByLabel("Network allowlist").fill("192.168.1.0/24")
  await page.getByRole("button", { name: "Add", exact: true }).click()
  await expect(networks.getByRole("listitem")).toHaveCount(3)
  await expect(networks.getByText("local network")).toBeVisible()
  await expect(access.getByText("Edited")).toBeVisible()
  await expect(page.getByText("1 unsaved change")).toBeVisible()

  await page.getByRole("button", { name: "Remove 100.64.0.0/10" }).click()
  await expect(networks.getByRole("listitem")).toHaveCount(2)
  await page.getByRole("button", { name: "Discard" }).click()
  await expect(networks.getByRole("listitem")).toHaveCount(2)
  await expect(networks.getByText("your tailnet")).toBeVisible()
})

test("the settings are one column, a switch is one line, and nothing explains itself under a field", async ({
  page,
}) => {
  await mockApi(page)
  for (const width of [1280, 390]) {
    await page.setViewportSize({ width, height: 900 })
    await page.goto("/dashboard/configuration")
    const column = page.locator("[data-slot=settings-column]")
    await expect(column.getByRole("heading", { name: "Certificate" })).toBeVisible()
    await risen(page)
    await expect(page.locator("[data-slot=stat-grid]")).toHaveCount(0)

    // One section to a row, each on the column's edges, the column no wider than its fields.
    const sections = await column.locator("[data-slot=setting]").evaluateAll((nodes) =>
      nodes.map((node) => {
        const box = node.getBoundingClientRect()
        return { x: Math.round(box.x), y: box.y, bottom: box.bottom, width: Math.round(box.width) }
      }),
    )
    expect(sections).toHaveLength(7)
    for (const [index, section] of sections.entries()) {
      expect(section.x).toBe(sections[0].x)
      expect(section.width).toBe(sections[0].width)
      if (index > 0) expect(section.y).toBeGreaterThanOrEqual(sections[index - 1].bottom)
    }
    expect(sections[0].width).toBeLessThanOrEqual(768)

    // No sentence under a field or a switch: the reasons are behind each name's ⓘ.
    await expect(column.locator("p.text-hint")).toHaveCount(0)
    const rows = column.locator("[data-slot=switch-row]")
    await expect(rows).toHaveCount(4)
    const heights = await rows.evaluateAll((nodes) =>
      nodes.map((node) => Math.round(node.getBoundingClientRect().height)),
    )
    expect(new Set(heights).size).toBe(1)
    await readTip(
      page,
      rows
        .filter({ hasText: "Require two-factor" })
        .getByRole("button", { name: "What this setting does" }),
      /Every account must enrol/,
    )

    expect(await scrollsSideways(page)).toBe(false)
    await page.screenshot({
      path: `test-results/dashboard-configuration-column-${width}.png`,
      fullPage: true,
    })
  }
})

test("an edit raises the floating bar, and Apply asks what it always asked and sends what it always sent", async ({
  page,
}) => {
  await mockApi(page)
  const sent: unknown[] = []
  await page.route("**/api/v1/dashboard/config", async (route) => {
    if (route.request().method() !== "PUT") return route.fallback()
    sent.push(route.request().postDataJSON())
    await route.fulfill({
      status: 202,
      contentType: "application/json",
      body: JSON.stringify({ ...config.run, status: "running", phase: "applying" }),
    })
  })
  await page.goto("/dashboard/configuration")
  const bar = page.getByRole("region", { name: "Save changes" })
  await expect(bar).toHaveCount(0)

  // Editing does not move the field being typed in, or anything under it.
  const port = page.getByLabel("Port", { exact: true })
  const features = page.locator("[data-slot=setting]", { hasText: "Features" })
  await risen(page)
  const placed = {
    port: await inColumn(page, "#cfg-port"),
    idle: await inColumn(page, "#cfg-idle"),
  }
  await port.fill("9443")
  await features.getByRole("switch", { name: "Web terminal" }).click()
  await expect(bar.getByText("2 unsaved changes")).toBeVisible()
  await expect(bar.getByText("Address and Features · Restarts the dashboard")).toBeVisible()
  expect(await inColumn(page, "#cfg-port")).toBe(placed.port)
  expect(await inColumn(page, "#cfg-idle")).toBe(placed.idle)
  const address = page.locator("[data-slot=setting]", { hasText: "Listening interface" })
  await expect(address.getByText("Edited")).toBeVisible()
  await expect(address.getByText("will answer at")).toBeVisible()

  // The bar's command is the question, not the write.
  await bar.getByRole("button", { name: /^Apply and restart/ }).click()
  const dialog = page.getByRole("dialog", { name: "Apply and restart" })
  await expect(dialog.getByText("Dashboard port")).toBeVisible()
  await expect(dialog.getByText("Web terminal")).toBeVisible()
  await expect(dialog.getByText("https://atlas.tail1234.ts.net:9443")).toBeVisible()
  expect(sent).toEqual([])
  // Declined, the edits are still on the bar.
  await dialog.getByRole("button", { name: "Cancel", exact: true }).click()
  await expect(bar.getByText("2 unsaved changes")).toBeVisible()

  await bar.getByRole("button", { name: /^Apply and restart/ }).click()
  await dialog.getByRole("button", { name: "Apply and restart" }).click()
  await expect.poll(() => sent.length).toBe(1)
  expect(sent[0]).toEqual({ ...settings, port: 9443, terminalEnabled: false })
  await expect(bar).toHaveCount(0)
})

test("what .env holds that is not running is the strip at the column's head, with the restart that adopts it", async ({
  page,
}) => {
  await mockApi(page)
  const restarts: unknown[] = []
  await page.route("**/api/v1/dashboard/restart", async (route) => {
    restarts.push(route.request().postDataJSON())
    await route.fulfill({
      status: 202,
      contentType: "application/json",
      body: JSON.stringify({ ...config.run, action: "restart", status: "running" }),
    })
  })
  await page.goto("/dashboard/configuration")
  const strip = page.getByRole("region", { name: "Settings not running yet" })
  await expect(strip.getByText(".env differs from what is running")).toBeVisible()
  await expect(strip).toContainText("Dashboard port")
  await expect(strip).toContainText("9443")
  // The strip is the first thing in the settings column.
  const first = await page
    .locator("[data-slot=settings-column]")
    .evaluate((column) => column.firstElementChild?.getAttribute("aria-label"))
  expect(first).toBe("Settings not running yet")

  await strip.getByRole("button", { name: "Restart" }).click()
  const dialog = page.getByRole("dialog", { name: "Restart the dashboard" })
  await expect(dialog.getByText(/Sessions survive/)).toBeVisible()
  await dialog.getByRole("button", { name: "Cancel", exact: true }).click()
  expect(restarts).toEqual([])

  // The same two questions from the line's menu, and the same two requests.
  await page.getByRole("button", { name: "Restart or rebuild" }).click()
  await page.getByRole("menuitem", { name: /Rebuild and restart/ }).click()
  const rebuild = page.getByRole("dialog", { name: "Rebuild and restart" })
  await expect(rebuild.getByText(dir)).toBeVisible()
  await rebuild.getByRole("button", { name: "Rebuild", exact: true }).click()
  await expect.poll(() => restarts).toEqual([{ rebuild: true }])
})

test("the configuration page opens where it will stand, and an install nobody can restart offers no control", async ({
  page,
}) => {
  // Held, so the outline is measured before the answer replaces it.
  let release: () => void = () => {}
  const held = new Promise<void>((resolve) => (release = resolve))
  await mockApi(page)
  await page.route("**/api/v1/dashboard/config", async (route) => {
    await held
    await json(route, {
      ...config,
      supported: false,
      reason: "The dashboard is not running under Docker Compose.",
      drift: [],
    })
  })
  await page.goto("/dashboard/configuration")
  const outline = page.locator("[data-slot=skeleton]").first()
  await expect(outline).toBeVisible()
  const waiting = await outline.boundingBox()
  release()
  const line = page.locator("[data-slot=host-identity]")
  await expect(line).toBeVisible()
  await risen(page)
  expect((await line.boundingBox())!.y).toBe(waiting!.y)

  // Read-only, said once in the line; one line says why, and nothing can be pressed.
  await expect(line.getByText("Read-only")).toBeVisible()
  await expect(page.locator("[data-slot=notice-lines]")).toContainText("Configured by hand")
  await expect(page.locator("[data-slot=notice-lines]")).toContainText(
    "The dashboard is not running under Docker Compose.",
  )
  await expect(page.getByRole("button", { name: "Restart or rebuild" })).toHaveCount(0)
  await expect(page.getByLabel("Port", { exact: true })).toBeDisabled()
  await expect(page.getByRole("switch", { name: "Web terminal" })).toBeDisabled()
  // A card that cannot be taken carries no control at all.
  await expect(page.getByRole("button", { name: "Use a self-signed certificate" })).toHaveCount(0)
  await expect(page.getByRole("button", { name: "Add", exact: true })).toHaveCount(0)
  await expect(page.getByRole("button", { name: /^Remove / })).toHaveCount(0)
  await page.screenshot({
    path: "test-results/dashboard-configuration-by-hand.png",
    fullPage: true,
  })
})

test("an account that may not read the settings is told so, with nothing to press", async ({
  page,
}) => {
  await mockApi(page)
  await page.route("**/api/v1/auth/session", (route) =>
    json(route, {
      ...user,
      capabilities: ["read"],
      user: { ...user.user, role: "readonly" },
    }),
  )
  await page.route("**/api/v1/dashboard/config", (route) =>
    route.fulfill({
      status: 403,
      contentType: "application/json",
      body: JSON.stringify({
        error: { code: "forbidden", message: "your role does not permit this action" },
      }),
    }),
  )
  await page.goto("/dashboard/configuration")
  await expect(page.getByRole("heading", { name: "Configuration" })).toBeVisible()
  await expect(page.locator("[data-slot=error-state]")).toBeVisible()
  await expect(page.locator("[data-slot=settings-column]")).toHaveCount(0)
  await expect(page.getByRole("switch")).toHaveCount(0)
  await expect(page.getByRole("button", { name: "Restart or rebuild" })).toHaveCount(0)

  // The Version page is every role's to read; installing is not offered.
  await page.goto("/dashboard")
  await expect(page.getByText("0.7.0 available")).toBeVisible()
  await expect(page.getByRole("button", { name: "Check now" })).toBeVisible()
  await expect(page.getByRole("button", { name: /^Update to/ })).toHaveCount(0)
})

test("the version page fits a phone, its two commands on the line's first row", async ({
  page,
}) => {
  await mockApi(page)
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto("/dashboard")
  await expect(page.getByRole("button", { name: "Update to 0.7.0" })).toBeVisible()
  await expect(page.getByRole("button", { name: "Check now" })).toBeVisible()
  await expect(page.getByText("Just Dashboard", { exact: true }).first()).toBeVisible()
  expect(await scrollsSideways(page)).toBe(false)
  await page.screenshot({ path: "test-results/dashboard-version-390.png", fullPage: true })
})

test("uninstalling asks what stays, and a wipe asks for the server's name", async ({ page }) => {
  await mockApi(page)
  const sent: { body: unknown; confirm?: string }[] = []
  await page.route("**/api/v1/dashboard/uninstall", async (route) => {
    if (route.request().method() !== "POST") return json(route, { supported: true })
    sent.push({
      body: route.request().postDataJSON(),
      confirm: route.request().headers()["x-confirm"],
    })
    await route.fulfill({
      status: 202,
      contentType: "application/json",
      body: JSON.stringify({ ...uninstallRun, mode: "wipe" }),
    })
  })
  await page.goto("/dashboard/configuration")

  const zone = page.locator("section", { hasText: "Danger zone" })
  await zone.getByRole("button", { name: "Uninstall…" }).click()
  const dialog = page.getByRole("dialog", { name: "Uninstall Just Dashboard" })
  await expect(dialog.getByText("Just Dashboard on atlas")).toBeVisible()

  // Keeping is the default, and needs nothing typed.
  await expect(
    dialog.getByRole("button", { name: "Keep the deployments and data" }),
  ).toHaveAttribute("aria-pressed", "true")
  const kept = dialog.getByRole("region", { name: "Kept" })
  await expect(kept.getByText("2 deployments, still running")).toBeVisible()
  await expect(kept.getByText(/2 containers · 1 volume · 1 image/)).toBeVisible()
  await expect(dialog.getByRole("button", { name: "Uninstall", exact: true })).toBeEnabled()
  await expect(dialog.getByLabel(/Type atlas to confirm/)).toHaveCount(0)

  // A wipe moves the deployments to what goes and asks for the server's name.
  await dialog.getByRole("button", { name: "Wipe the deployments and data" }).click()
  const removed = dialog.getByRole("region", { name: "Removed" })
  await expect(removed.getByText("2 deployments", { exact: true })).toBeVisible()
  await expect(removed.getByText("Its key")).toBeVisible()
  // The backups are the operator's, and stay unless asked.
  await expect(dialog.getByRole("switch", { name: "Delete the backups too" })).not.toBeChecked()
  await expect(
    dialog.getByRole("region", { name: "Kept" }).getByText("Backups", { exact: true }),
  ).toBeVisible()
  const wipe = dialog.getByRole("button", { name: "Wipe and uninstall" })
  await expect(wipe).toBeDisabled()
  await dialog.getByLabel(/Type atlas to confirm/).fill("atlas")
  await expect(wipe).toBeEnabled()
  await wipe.click()

  await expect(dialog).toHaveCount(0)
  expect(sent).toEqual([
    { body: { mode: "wipe", deleteBackups: false }, confirm: encodeURIComponent(phrase) },
  ])
  expect(await framedPanels(page)).toEqual([])
})

test("an uninstall follows the stack down, then says what is left to do", async ({ page }) => {
  await mockApi(page)
  let down = false
  await page.route("**/api/v1/dashboard/uninstall", async (route) => {
    // The sibling has taken over and is stopping the stack: the next poll
    // finds nothing answering, which is the uninstall working.
    if (down) return route.abort("connectionrefused")
    down = true
    return json(route, {
      supported: true,
      run: { ...uninstallRun, phase: "stack" },
      log: "Uninstalling Just Dashboard, requested by operator\nPausing deployments, schedules and backups\n\nHanding over to just-dashboard-uninstall, which stops the dashboard\n",
    })
  })
  await page.goto("/dashboard/configuration")

  const record = page.locator("section", { hasText: "Uninstalling" })
  await expect(record.getByText("The dashboard has stopped")).toBeVisible({ timeout: 10_000 })
  await expect(record.getByText("sudo tail -f /var/log/just-dashboard-uninstall.log")).toBeVisible()
  await expect(record.getByText(`cd ${dir} && sudo ./install.sh`)).toBeVisible()
  // The page stays drawn: losing the API is not an error state here.
  await expect(page.getByRole("heading", { name: "Configuration" })).toBeVisible()
  // Nothing is offered that the stack going away has made meaningless.
  await expect(page.getByRole("button", { name: "Restart or rebuild" })).toHaveCount(0)
  await expect(page.getByRole("button", { name: "Uninstall…" })).toHaveCount(0)
})

for (const path of ["/dashboard", "/dashboard/configuration"] as const) {
  for (const width of [1280, 1720]) {
    test(`${path} looks right at ${width}`, async ({ page }) => {
      await mockApi(page)
      await page.setViewportSize({ width, height: 2200 })
      await page.goto(path)
      await expect(
        path === "/dashboard"
          ? page.getByRole("heading", { name: "0.7.0" })
          : page.getByRole("button", { name: "Restart or rebuild" }),
      ).toBeVisible()
      await expect(page.locator("[data-slot=page]")).toHaveClass(/animate-rise/)
      expect(await scrollsSideways(page)).toBe(false)
      const name = path === "/dashboard" ? "version" : "configuration"
      await page.screenshot({ path: `test-results/dashboard-${name}-${width}.png`, fullPage: true })
    })
  }
}

test.describe("the usage count on Configuration", () => {
  const panel = (page: Page) => page.locator("[data-slot=setting]", { hasText: "Usage count" })
  const toggle = (page: Page) => panel(page).getByRole("switch", { name: "Count this server" })

  async function withStatus(page: Page, initial: Record<string, unknown>) {
    const world = { status: initial, puts: [] as unknown[] }
    await mockApi(page)
    await page.route("**/api/v1/dashboard/usage-ping", async (route) => {
      if (route.request().method() === "PUT") {
        const body = route.request().postDataJSON() as { on: boolean }
        world.puts.push(body)
        world.status = {
          ...world.status,
          choice: body.on ? "on" : "off",
          ...(body.on ? { lastBody: usageBody, lastSentAt: now } : {}),
        }
      }
      return json(route, world.status)
    })
    return world
  }

  test("it prints what it would send, and a yes takes effect with no Apply", async ({ page }) => {
    const world = await withStatus(page, usagePing)
    await page.goto("/dashboard/configuration")

    await expect(toggle(page)).not.toBeChecked()
    await expect(panel(page).getByText("Everything it would send:")).toBeVisible()
    await expect(panel(page).getByText(usageBody)).toBeVisible()
    await expect(panel(page).getByRole("link", { name: "See the totals" })).toHaveAttribute(
      "href",
      "https://just-dashboard.com/stats",
    )
    expect(world.puts).toEqual([])

    await toggle(page).click()
    await expect(toggle(page)).toBeChecked()
    await expect(panel(page).getByText(/^Everything sent, /)).toBeVisible()
    expect(world.puts).toEqual([{ on: true }])
  })

  test("a report that did not arrive says so and says it will try again", async ({ page }) => {
    await withStatus(page, {
      ...usagePing,
      choice: "on",
      lastError: "the site answered 502 Bad Gateway",
    })
    await page.goto("/dashboard/configuration")
    await expect(toggle(page)).toBeChecked()
    await expect(panel(page).getByText(/did not reach just-dashboard.com/)).toBeVisible()
    await expect(panel(page).getByText(/502 Bad Gateway/)).toBeVisible()
  })

  test("switched off in .env, it names the switch and offers nothing", async ({ page }) => {
    await withStatus(page, { ...usagePing, enabled: false, offBy: "JD_USAGE_PING" })
    await page.goto("/dashboard/configuration")
    await expect(panel(page).getByText("JD_USAGE_PING=false")).toBeVisible()
    await expect(toggle(page)).toHaveCount(0)
    await expect(panel(page).getByRole("link", { name: "See the totals" })).toHaveCount(0)
  })

  test("the version check says how often it really runs", async ({ page }) => {
    await withStatus(page, usagePing)
    await page.goto("/dashboard/configuration")
    // Behind the ⓘ beside the switch's name, where a sentence under it was.
    await readTip(
      page,
      page
        .locator("[data-slot=switch-row]", { hasText: "Check for new versions" })
        .getByRole("button", { name: "What this setting does" }),
      /every two hours and at most every five minutes/,
    )
  })
})
