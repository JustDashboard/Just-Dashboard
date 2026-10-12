import { expect, test, type Page, type Route } from "@playwright/test"

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
  check: { enabled: true, checkedAt: earlier, repo: "wayy/just-dashboard", ref: "main" },
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

async function mockApi(page: Page) {
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname.replace(/^\/api\/v1/, "")
    if (path === "/auth/session") return json(route, user)
    if (path === "/dashboard/update") return json(route, update)
    if (path === "/dashboard/config") return json(route, config)
    if (path === "/dashboard/uninstall") return json(route, { supported: true })
    if (path === "/dashboard/uninstall/plan") return json(route, uninstallPlan)
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

async function scrollsSideways(page: Page) {
  return page.evaluate(
    () => document.documentElement.scrollWidth > document.documentElement.clientWidth,
  )
}

test("the version page is the history, with the update in the header", async ({ page }) => {
  await mockApi(page)
  await page.goto("/dashboard")
  await expect(page.getByRole("heading", { name: "Version", exact: true })).toBeVisible()
  // The readings are gone from the top; what they said is in the identity line.
  await expect(page.locator("[data-slot=stat-tile]")).toHaveCount(0)

  await expect(page.getByRole("button", { name: "Update to 0.7.0" })).toBeVisible()
  await expect(page.getByText("0.7.0 available")).toBeVisible()
  await expect(page.getByText("1 release ahead of yours")).toBeVisible()

  // The newest release leads the history and the running one is marked.
  const history = page.locator("section", { hasText: "Version history" })
  await expect(history.getByRole("heading", { name: "0.7.0" })).toBeVisible()
  await expect(history.getByText("Installed", { exact: true })).toBeVisible()
  await expect(history.getByText("Before you update")).toBeVisible()

  await page.getByPlaceholder("Find a change, a version or a fix").fill("deployments")
  await expect(history.getByRole("heading", { name: "0.6.6" })).toBeVisible()
  await expect(history.getByRole("heading", { name: "0.7.0" })).toHaveCount(0)

  expect(await framedPanels(page)).toEqual([])
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

  // Restart and Rebuild are the two commands, drawn as the two ways to restart.
  await expect(page.getByRole("button", { name: "Restart the dashboard" })).toBeVisible()
  await expect(page.getByRole("button", { name: "Rebuild and restart" })).toBeVisible()

  // The restart record says what happened without a tinted banner, and its
  // transcript is the whole file rather than the tail the report carries.
  const record = page.locator("section", { hasText: "Last restart" })
  await expect(record.locator(".bg-wash-warning, .bg-wash-success")).toHaveCount(0)
  await record.getByRole("button", { name: /Transcript/ }).click()
  const transcript = record.getByRole("list", { name: "Apply transcript" })
  await expect(transcript.getByText("Applying Just Dashboard requested by operator")).toBeVisible()
  await expect(transcript.getByText("earlier output trimmed")).toHaveCount(0)

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
  await expect(page.getByText("docker compose up -d --force-recreate")).toBeVisible()
  await expect(page.getByText("docker compose build", { exact: true })).toBeVisible()

  // Durations are said as a person says them, beside the field.
  await expect(page.getByText("12 hours from signing in")).toBeVisible()
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
  await expect(page.getByRole("button", { name: "Restart the dashboard" })).toHaveCount(0)
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
          : page.getByRole("button", { name: "Rebuild and restart" }),
      ).toBeVisible()
      await expect(page.locator("[data-slot=page]")).toHaveClass(/animate-rise/)
      expect(await scrollsSideways(page)).toBe(false)
      const name = path === "/dashboard" ? "version" : "configuration"
      await page.screenshot({ path: `test-results/dashboard-${name}-${width}.png`, fullPage: true })
    })
  }
}
