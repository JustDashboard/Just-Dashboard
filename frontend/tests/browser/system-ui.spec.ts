import { expect, test, type Page, type Route } from "@playwright/test"

/**
 * The two System pages, System users and the audit log, checked in a browser
 * against a mocked host with enough people, daemons and history in it for
 * every mark to have something to draw.
 *
 * What these assert is what a type check cannot (design-system.md §12, §14,
 * §15, §16): that the accounts are cards you open — people as their initials
 * in their own hue, daemons as the product that runs under them — under four
 * readings whose faces say who they count; that the audit trail stays a
 * framed table of readings under four readings of the last day, with every
 * entry drawn as what it touched and who touched it, shelved by day; and that
 * the chips narrow both. The screenshots are the eyes the assertions do not
 * have.
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

const account = (fields: Record<string, unknown>) => ({
  gid: 1000,
  comment: "",
  home: `/home/${fields.username}`,
  shell: "/bin/bash",
  groups: [],
  system: false,
  locked: false,
  noPassword: false,
  sshKeyCount: 0,
  canLogin: true,
  ...fields,
})

const people = [
  account({
    username: "ion",
    uid: 1000,
    comment: "Ion Moisei",
    groups: ["adm", "sudo", "docker"],
    lastLogin: ago(2 * HOUR),
    lastLoginFrom: "100.101.4.12",
    sshKeyCount: 2,
  }),
  account({
    username: "deploy",
    uid: 1001,
    comment: "Deploy bot",
    groups: ["docker"],
    lastLogin: ago(3 * 24 * HOUR),
    lastLoginFrom: "203.0.113.9",
    sshKeyCount: 1,
  }),
  account({ username: "mira", uid: 1002, groups: ["sudo"], noPassword: true }),
  account({
    username: "backup",
    uid: 1003,
    shell: "/usr/sbin/nologin",
    locked: true,
    canLogin: false,
  }),
]

const daemons = [
  account({
    username: "root",
    uid: 0,
    home: "/root",
    groups: ["root"],
    system: true,
    locked: true,
  }),
  account({
    username: "postgres",
    uid: 113,
    home: "/var/lib/postgresql",
    groups: ["postgres", "ssl-cert"],
    system: true,
  }),
  account({
    username: "redis",
    uid: 114,
    home: "/var/lib/redis",
    shell: "/usr/sbin/nologin",
    system: true,
    canLogin: false,
  }),
  account({
    username: "www-data",
    uid: 33,
    home: "/var/www",
    shell: "/usr/sbin/nologin",
    system: true,
    canLogin: false,
  }),
]

const keys = {
  path: "/home/ion/.ssh/authorized_keys",
  keys: [
    {
      line: 1,
      type: "ssh-ed25519",
      comment: "ion@macbook",
      fingerprint: "SHA256:q1w2e3r4t5y6u7i8o9p0",
      bits: 256,
      raw: "",
    },
    {
      line: 2,
      type: "ssh-ed25519",
      comment: "github-actions-deploy",
      fingerprint: "SHA256:a1s2d3f4g5h6j7k8l9",
      bits: 256,
      raw: "",
    },
  ],
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
    if (path === "/system-users/") {
      const system = url.searchParams.get("system") === "true"
      return json(route, system ? [...people, ...daemons] : people)
    }
    if (path === "/system-users/ion/keys") return json(route, keys)
    if (path === "/audit/") return json(route, audit(url))
    return json(route, [])
  })
}

test.describe("system users", () => {
  test("opens on four readings and draws every account as a card", async ({ page }) => {
    await mockHost(page)
    await page.goto("/system-users")
    await page.waitForLoadState("networkidle")

    const tiles = page.locator("[data-slot='stat-tile']")
    await expect(tiles).toHaveCount(4)
    await expect(tiles.nth(0)).toContainText("4")
    // Two hold sudo, and one of them needs no password to sign in.
    await expect(tiles.nth(1)).toContainText("2")
    await expect(tiles.nth(1)).toContainText("1 without a password")
    await expect(tiles.nth(3)).toContainText("ion")

    // Cards you open, not a table: each opens its keys.
    expect(await page.locator("[data-slot='table']").count()).toBe(0)
    const cards = page.locator("[data-slot='choice-row']")
    await expect(cards).toHaveCount(4)
    await expect(page.getByRole("button", { name: "SSH keys for ion" })).toBeVisible()
    // A person is their initials; an administrator's group is said first.
    await expect(cards.first()).toContainText("IO")
    await expect(cards.first()).toContainText("sudo")
    await expect(cards.first().locator("img[src='/logos/docker.svg']")).toHaveCount(1)
    // Where they last signed in from is drawn as the network it is on.
    await expect(cards.first().locator("img[src='/logos/tailscale.svg']")).toHaveCount(1)
  })

  test("the chips narrow the cards and system accounts are their own shelf", async ({ page }) => {
    await mockHost(page)
    await page.goto("/system-users")
    await page.waitForLoadState("networkidle")

    await page.getByRole("button", { name: /^Locked/ }).click()
    await expect(page.locator("[data-slot='choice-row']")).toHaveCount(1)
    await expect(page.locator("[data-slot='choice-row']")).toContainText("backup")
    await page.getByRole("button", { name: /^All/ }).click()

    await page.getByRole("button", { name: "System accounts", exact: true }).click()
    await expect(page.locator("[data-slot='choice-row']")).toHaveCount(8)
    await expect(page.getByText("People", { exact: true })).toBeVisible()
    const postgres = page.locator("[data-slot='choice-row']").filter({ hasText: "postgres" })
    await expect(postgres.locator("img[src='/logos/postgresql.svg']")).toHaveCount(1)
    // www-data is nginx's on one host and Apache's on the next: no guess.
    const web = page.locator("[data-slot='choice-row']").filter({ hasText: "www-data" })
    await expect(web.locator("img")).toHaveCount(0)
  })

  test("a card opens the account's keys, each drawn as where it lives", async ({ page }) => {
    await mockHost(page)
    await page.goto("/system-users")
    await page.waitForLoadState("networkidle")

    await page.getByRole("button", { name: "SSH keys for ion" }).click()
    const sheet = page.getByRole("dialog")
    await expect(sheet).toContainText("ion")
    await expect(sheet).toContainText("github-actions-deploy")
    await expect(sheet.locator("img[src='/logos/github.svg']")).toHaveCount(1)
    await expect(sheet).toContainText("/home/ion/.ssh/authorized_keys")
  })
})

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
    // An API key's change says so beside the person.
    await expect(row("file.write")).toContainText("API key")
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
