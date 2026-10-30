import { expect, test, type Page, type Route } from "@playwright/test"
import { answerSystemUsers } from "./system-users-fixture"

const recordWorkspace = process.env.JD_WORKSPACE_VIDEO === "1"
test.use({ video: recordWorkspace ? "on" : "off" })

/**
 * The audit log, and that both System pages hold their width, checked in a
 * browser against a mocked host with enough history in it for every mark to have
 * something to draw. The accounts have a spec of their own, system-users.spec.ts.
 *
 * What these assert is what a type check cannot (design-system.md §12, §14,
 * §15, §16): that the audit trail stays a framed table of readings under four
 * readings of the last day, with every entry drawn as what it touched and who
 * touched it, shelved by day; and that the chips narrow it. The screenshots are
 * the eyes the assertions do not have.
 */

const now = Date.now()
const ago = (ms: number) => new Date(now - ms).toISOString()
const HOUR = 3_600_000

const session = {
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
    lastLoginAt: ago(0),
    createdAt: ago(0),
  },
}

type Entry = {
  id: number
  ts: string
  userId: number
  username: string
  role: string
  ip: string
  actor: string
  action: string
  target: string
  method: string
  path: string
  status: number
  success: boolean
  detail: string
  keyId?: number
  keyName?: string
}

let id = 0
const entry = (fields: Partial<Entry> & Pick<Entry, "ts" | "action">): Entry => ({
  id: ++id,
  userId: 1,
  username: "operator",
  role: "admin",
  ip: "100.64.1.2",
  actor: "session",
  target: "",
  method: "POST",
  path: "",
  status: 200,
  success: true,
  detail: "",
  ...fields,
})

const trail = [
  entry({
    ts: ago(0.1 * HOUR),
    action: "docker.container.restart",
    target: "web",
    path: "/api/v1/docker/containers/web/restart",
  }),
  entry({
    ts: ago(0.5 * HOUR),
    actor: "webhook",
    username: "",
    userId: 0,
    ip: "140.82.115.4",
    action: "deploy.run",
    target: "14",
    path: "/api/v1/hooks/github-app",
  }),
  entry({
    ts: ago(1 * HOUR),
    actor: "system",
    username: "",
    userId: 0,
    ip: "",
    method: "",
    status: 0,
    action: "proxy.ingress.reconcile",
    target: "jd-ingress",
  }),
  entry({
    ts: ago(2 * HOUR),
    actor: "anonymous",
    username: "admin",
    userId: 0,
    ip: "45.12.3.4",
    action: "auth.login",
    path: "/api/v1/auth/login",
    status: 401,
    success: false,
    detail: "invalid credentials",
  }),
  entry({ ts: ago(2.2 * HOUR), action: "auth.login", path: "/api/v1/auth/login" }),
  entry({
    ts: ago(3 * HOUR),
    actor: "token",
    username: "mira",
    userId: 2,
    keyId: 12,
    keyName: "ci-deploy",
    ip: "192.168.1.20",
    method: "PUT",
    action: "file.write",
    target: "/etc/nginx/sites-available/app.conf",
    path: "/api/v1/files/write",
  }),
  entry({
    ts: ago(4 * HOUR),
    action: "system.packages.install",
    target: "htop",
    path: "/api/v1/packages/install",
  }),
  entry({
    ts: ago(5 * HOUR),
    action: "git.clone",
    target: "Wayy01/api",
    path: "/api/v1/git/clone",
  }),
  entry({
    ts: ago(26 * HOUR),
    method: "DELETE",
    action: "database.row.delete",
    target: "app.users",
    path: "/api/v1/databases/2/rows",
    status: 403,
    success: false,
    detail: "missing capability destructive",
  }),
]

async function json(route: Route, body: unknown) {
  await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(body) })
}

/** The audit query the page makes, answered the way the server's filter would. */
function audit(url: URL) {
  const since = url.searchParams.get("since")
  const action = url.searchParams.get("action") ?? ""
  const user = url.searchParams.get("username") ?? ""
  const failed = url.searchParams.get("failed") === "true"
  const matching = trail.filter(
    (e) =>
      (!since || e.ts >= since) &&
      e.action.includes(action) &&
      e.username.includes(user) &&
      (!failed || !e.success),
  )
  const limit = Number(url.searchParams.get("limit") ?? 100)
  return { entries: matching.slice(0, limit), total: matching.length }
}

async function mockHost(page: Page) {
  await page.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url())
    const path = url.pathname.replace(/^\/api\/v1/, "")
    if (path === "/auth/session") return json(route, session)
    if (path === "/updates/self") return json(route, { current: "0.7.0", latest: "0.7.0" })
    if (await answerSystemUsers(route)) return
    if (path === "/audit/") return json(route, audit(url))
    return json(route, [])
  })
}

test.describe("audit log", () => {
  test("opens on the last day and draws each entry as what it touched", async ({ page }) => {
    await mockHost(page)
    await page.goto("/audit")
    await page.waitForLoadState("networkidle")

    const tiles = page.locator("[data-slot='stat-tile']")
    await expect(tiles).toHaveCount(4)
    await expect(tiles.nth(0)).toContainText("8")
    await expect(tiles.nth(1)).toContainText("1")
    await expect(tiles.nth(2)).toContainText("2")
    await expect(tiles.nth(3)).toContainText("1 attempt refused")

    // A table of readings keeps its frame and its rows, shelved by day.
    const table = page.getByRole("table")
    await expect(table.getByText("Today", { exact: true })).toBeVisible()
    await expect(table.getByText("Yesterday", { exact: true })).toBeVisible()

    const row = (text: string) => table.getByRole("row").filter({ hasText: text })
    await expect(
      row("docker.container.restart").locator("img[src='/logos/docker.svg']"),
    ).toHaveCount(1)
    await expect(row("git.clone").locator("img[src='/logos/git.svg']")).toHaveCount(1)
    await expect(row("deploy.run").locator("img[src='/logos/webhook.svg']")).toHaveCount(1)
    // An address on the tailnet is drawn as Tailscale.
    await expect(
      row("docker.container.restart").locator("img[src='/logos/tailscale.svg']"),
    ).toHaveCount(1)
    // A refusal says why in its code's word.
    await expect(row("auth.login").filter({ hasText: "401" })).toContainText("Unauthorized")
    // An API key's change says so beside the person, naming the key.
    await expect(row("file.write")).toContainText("key ci-deploy")
    await expect(row("file.write").getByTitle("key #12")).toBeVisible()
  })

  test("a section chip narrows the trail to its actions", async ({ page }) => {
    await mockHost(page)
    await page.goto("/audit")
    await page.waitForLoadState("networkidle")

    const request = page.waitForRequest((r) => r.url().includes("action=docker."))
    await page
      .getByRole("group", { name: "Sections" })
      .getByRole("button", { name: "Docker" })
      .click()
    await request
    const table = page.getByRole("table")
    await expect(table.getByText("docker.container.restart")).toBeVisible()
    await expect(table.getByText("git.clone")).toHaveCount(0)
    await expect(page.getByPlaceholder("Action, e.g. docker.container")).toHaveValue("docker.")
  })
})

for (const width of [1280, 1720]) {
  test(`looks right at ${width}`, async ({ page }) => {
    await mockHost(page)
    await page.setViewportSize({ width, height: 1000 })
    for (const path of ["/system-users", "/audit"]) {
      await page.goto(path)
      await page.waitForLoadState("networkidle")
      const overflow = await page.evaluate(
        () => document.documentElement.scrollWidth > document.documentElement.clientWidth,
      )
      expect(overflow, `${path} scrolls sideways at ${width}`).toBe(false)
      await page.screenshot({
        path: `test-results/system${path.replace("/", "-")}-${width}.png`,
        fullPage: true,
      })
    }
  })
}

test.describe("on a phone", () => {
  test.use({ viewport: { width: 390, height: 844 }, hasTouch: true })

  test("nothing scrolls sideways", async ({ page }) => {
    await mockHost(page)
    for (const path of ["/system-users", "/audit"]) {
      await page.goto(path)
      await page.waitForLoadState("networkidle")
      const overflow = await page.evaluate(
        () => document.documentElement.scrollWidth > document.documentElement.clientWidth,
      )
      expect(overflow, `${path} scrolls sideways on a phone`).toBe(false)
      await page.screenshot({
        path: `test-results/system${path.replace("/", "-")}-390.png`,
        fullPage: true,
      })
    }
  })
})

test("workspace: audit filter history and incoming entries preserve the row being read", async ({
  page,
}) => {
  await mockHost(page)
  let incoming = false
  await page.route("**/api/v1/audit/**", (route) => {
    const answer = audit(new URL(route.request().url()))
    if (incoming) answer.entries.unshift({ ...trail[0], id: 9999, action: "new.entry" })
    return json(route, answer)
  })
  await page.goto("/audit")
  const rows = page.locator("[data-workspace-item]")
  await expect(rows.first()).toBeVisible()
  const first = await rows.first().getAttribute("data-workspace-item")
  await rows.first().focus()
  incoming = true
  await page.keyboard.press("F5")
  await expect(page.getByRole("button", { name: "Show 1 new audit entry" })).toBeVisible()
  await expect(rows.first()).toHaveAttribute("data-workspace-item", first!)
  await page.getByRole("button", { name: "Show 1 new audit entry" }).click()
  await expect(page.locator('[data-workspace-item="9999"]:visible')).toBeVisible()
  const action = page.getByPlaceholder("Action, e.g. docker.container")
  await action.fill("docker.")
  await expect(page).toHaveURL(/action=docker\./)
  await action.fill("git.")
  await expect(page).toHaveURL(/action=git\./)
  await page.goBack()
  await expect(action).toHaveValue("docker.")
})

test("workspace: mobile audit navigation respects input and sidebar focus", async ({
  page,
}, testInfo) => {
  await mockHost(page)
  await page.setViewportSize({ width: 390, height: 844 })
  await page.emulateMedia({ reducedMotion: "reduce" })
  await page.goto("/audit")
  const rows = page.locator("[data-workspace-item]:visible")
  await expect(rows.first()).toBeVisible()
  await rows.first().focus()
  await page.keyboard.press("ArrowDown")
  await expect(rows.nth(1)).toBeFocused()
  if (recordWorkspace) {
    await page.waitForTimeout(700)
    await page.screenshot({ path: testInfo.outputPath("audit-phone.png") })
  }
  await page.keyboard.press("?")
  await expect(page.getByRole("dialog", { name: "Audit shortcuts" })).toBeVisible()
  await expect(page.getByRole("dialog")).not.toContainText("Open item")
  if (recordWorkspace) await page.waitForTimeout(700)
  await page.keyboard.press("Escape")
  await expect(page.getByRole("dialog")).toHaveCount(0)
  await page.keyboard.press("Control+f")
  const action = page.getByPlaceholder("Action, e.g. docker.container")
  await expect(action).toBeFocused()
  await action.press("?")
  await expect(action).toHaveValue("?")
  await expect(page.getByRole("dialog")).toHaveCount(0)
  expect(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth)).toBe(false)
})
