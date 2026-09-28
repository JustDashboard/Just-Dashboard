import { expect, test, type Page, type Route } from "@playwright/test"

/**
 * The account pages after their 0.7.0 pass, against a mocked API: the
 * profile opens on the account's identity line, sessions are drawn as the
 * browsers, systems and networks they came from, keys as what holds them,
 * users by their faces, and Security is a form whose heads sit in a rail.
 */

const now = Date.now()
const iso = (ms: number) => new Date(ms).toISOString()
const HOUR = 3_600_000
const DAY = 24 * HOUR

const me = {
  id: 1,
  username: "ion",
  displayName: "Ion Moisei",
  avatarVersion: 0,
  role: "admin",
  totpEnabled: true,
  disabled: false,
  mustChangePassword: false,
  lastLoginAt: iso(now - 2 * HOUR),
  createdAt: "2026-03-02T09:00:00Z",
}

function session(twoFactor: boolean, totp = true) {
  return {
    authenticated: true,
    needsTotp: false,
    needsEnrollment: false,
    require2fa: twoFactor,
    capabilities: [
      "read",
      "service.control",
      "file.write",
      "terminal",
      "destructive",
      "system.admin",
    ],
    user: { ...me, totpEnabled: totp },
  }
}

const sessions = [
  {
    id: "s1",
    userId: 1,
    twoFactorPassed: true,
    ip: "100.101.7.12",
    userAgent:
      "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Safari/537.36",
    createdAt: iso(now - 3 * DAY),
    lastSeenAt: iso(now - 10_000),
    expiresAt: iso(now + 27 * DAY),
    current: true,
  },
  {
    id: "s2",
    userId: 1,
    twoFactorPassed: true,
    ip: "192.168.1.20",
    userAgent:
      "Mozilla/5.0 (iPhone; CPU iPhone OS 17_6 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.6 Mobile/15E148 Safari/604.1",
    createdAt: iso(now - 6 * DAY),
    lastSeenAt: iso(now - 30_000),
    expiresAt: iso(now + 24 * DAY),
    current: false,
  },
  {
    id: "s3",
    userId: 1,
    twoFactorPassed: false,
    ip: "203.0.113.7",
    userAgent: "curl/8.5.0",
    createdAt: iso(now - 9 * DAY),
    lastSeenAt: iso(now - 3 * DAY),
    expiresAt: iso(now + 21 * DAY),
    current: false,
  },
]

const tokens = [
  {
    id: 1,
    userId: 1,
    name: "github-actions",
    prefix: "vpsd_ab12",
    role: "limited",
    createdAt: iso(now - 40 * DAY),
    expiresAt: iso(now + 5 * DAY),
    lastUsedAt: iso(now - 2 * HOUR),
    revoked: false,
  },
  {
    id: 2,
    userId: 1,
    name: "backup-cron",
    prefix: "vpsd_cd34",
    role: "readonly",
    createdAt: iso(now - 20 * DAY),
    revoked: false,
  },
  {
    id: 3,
    userId: 2,
    name: "grafana",
    prefix: "vpsd_ef56",
    role: "readonly",
    createdAt: iso(now - 12 * DAY),
    expiresAt: iso(now + 200 * DAY),
    lastUsedAt: iso(now - DAY),
    revoked: false,
  },
  {
    id: 4,
    userId: 1,
    name: "old-laptop",
    prefix: "vpsd_0000",
    role: "admin",
    createdAt: iso(now - 300 * DAY),
    revoked: true,
  },
]

const users = [
  me,
  {
    id: 2,
    username: "maria",
    displayName: "Maria Rusu",
    avatarVersion: 0,
    role: "admin",
    totpEnabled: false,
    disabled: false,
    mustChangePassword: false,
    lastLoginAt: iso(now - DAY),
    createdAt: "2026-04-11T09:00:00Z",
  },
  {
    id: 3,
    username: "deploy",
    displayName: "Deploy bot",
    avatarVersion: 0,
    role: "limited",
    totpEnabled: false,
    disabled: false,
    mustChangePassword: true,
    lastLoginAt: "0001-01-01T00:00:00Z",
    createdAt: "2026-08-01T09:00:00Z",
  },
  {
    id: 4,
    username: "guest",
    displayName: "Guest",
    avatarVersion: 0,
    role: "readonly",
    totpEnabled: false,
    disabled: true,
    mustChangePassword: false,
    lastLoginAt: iso(now - 40 * DAY),
    createdAt: "2026-05-20T09:00:00Z",
  },
]

async function json(route: Route, body: unknown) {
  await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(body) })
}

async function mockAccount(page: Page, auth = session(false)) {
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname.replace(/^\/api\/v1/, "")
    if (path === "/auth/session") return json(route, auth)
    if (path === "/account/sessions") return json(route, sessions)
    if (path === "/tokens/") return json(route, tokens)
    if (path === "/dashboard-users/") return json(route, users)
    return json(route, [])
  })
}

test("the profile opens on the account's identity line and its readings", async ({ page }) => {
  await mockAccount(page)
  await page.goto("/account")
  await expect(page.getByRole("heading", { name: "Ion Moisei" })).toBeVisible()
  await expect(page.getByText("@ion", { exact: true })).toBeVisible()
  // The figure strip in the header is gone; its facts are the identity line.
  await expect(page.getByText(/^member since/)).toBeVisible()
  await expect(page.getByText("Chrome on macOS")).toBeVisible()
  await expect(page.locator('img[src="/logos/tailscale.svg"]').first()).toBeVisible()
  await expect(page.getByText("two-factor on").first()).toBeVisible()

  const tiles = page.locator("[data-slot=stat-tile]")
  await expect(tiles).toHaveCount(4)
  // Each reading names what it counts with the things themselves.
  const readings = page.locator("[data-slot=stat-grid]")
  const sessionsTile = readings.getByRole("link", { name: "Sessions" })
  await expect(sessionsTile).toContainText("2 elsewhere")
  for (const logo of ["chrome", "safari", "curl"]) {
    await expect(sessionsTile.locator(`img[src="/logos/${logo}.svg"]`)).toBeVisible()
  }
  await expect(
    readings.getByRole("link", { name: "API keys" }).locator('img[src="/logos/github.svg"]'),
  ).toBeVisible()
  await expect(readings.getByRole("link", { name: "Users" })).toContainText("2 admins can sign in")

  await expect(page.getByText("Open a shell")).toBeVisible()
  await expect(page.getByText("granted")).toHaveCount(6)
})

test("sessions are the browser, the system and the network each came from", async ({ page }) => {
  await mockAccount(page)
  await page.goto("/account/sessions")
  // This device is the identity line, drawn as Chrome, with macOS and the
  // tailnet among its facts.
  await expect(page.getByText("this device")).toBeVisible()
  await expect(page.getByText("password and code").first()).toBeVisible()

  const rows = page.locator("[data-slot=row]")
  await expect(rows).toHaveCount(2)
  const phone = rows.filter({ hasText: "Safari on iOS" })
  await expect(phone.locator('img[src="/logos/safari.svg"]')).toBeVisible()
  await expect(phone.locator('img[src="/logos/apple.svg"]')).toBeVisible()
  await expect(phone).toContainText("local network")
  await expect(phone).toContainText("active now")
  const script = rows.filter({ hasText: "curl" })
  await expect(script.locator('img[src="/logos/curl.svg"]')).toBeVisible()
  await expect(script).toContainText("internet")
  await expect(page.getByRole("button", { name: "Sign out curl from 203.0.113.7" })).toBeVisible()
  await expect(page.getByRole("button", { name: "Sign out other sessions" })).toBeEnabled()
  await expect(
    page
      .locator("[data-slot=panel]", { hasText: "Other sessions" })
      .getByRole("button", { name: "Sign out other sessions" }),
  ).toBeEnabled()
})

test("keys are read as credentials and drawn as what holds them", async ({ page }) => {
  await mockAccount(page)
  await page.goto("/account/keys")
  const tile = (label: string) => page.locator("[data-slot=stat-tile]", { hasText: label })
  await expect(tile("In use")).toContainText("3")
  await expect(tile("Never used")).toContainText("1")
  await expect(tile("Expiring soon")).toContainText("github-actions")

  const github = page.locator("[data-slot=row]", { hasText: "github-actions" })
  await expect(github.locator('img[src="/logos/github.svg"]')).toBeVisible()
  await expect(github).toContainText("expires")
  // An administrator reads everyone's keys, each with its owner's face.
  await expect(page.locator("[data-slot=row]", { hasText: "grafana" })).toContainText("Maria Rusu")
  await expect(page.getByRole("heading", { name: "Revoked or expired" })).toBeVisible()
  await expect(page.getByText(/Authorization: Bearer vpsd_…/)).toBeVisible()
  await expect(
    page
      .locator("[data-slot=panel]", { hasText: "In use" })
      .getByRole("button", { name: "New key" }),
  ).toBeVisible()

  await page.getByRole("button", { name: "New key" }).click()
  await page.getByLabel("Name").fill("uptime-kuma")
  await expect(page.getByRole("dialog").locator('img[src="/logos/uptime-kuma.svg"]')).toBeVisible()
  await expect(
    page.getByRole("dialog").getByText("Look at everything, change nothing"),
  ).toBeVisible()
})

test("users are cards drawn by their faces, opening their editor", async ({ page }) => {
  await mockAccount(page)
  await page.setViewportSize({ width: 1440, height: 1000 })
  await page.goto("/account/users")
  const tile = (label: string) => page.locator("[data-slot=stat-tile]", { hasText: label })
  await expect(tile("Two-factor")).toContainText("1 of 3")
  await expect(tile("Two-factor")).toContainText("1 administrator on a password alone")

  const cards = page.locator("[data-slot=choice-row]")
  await expect(cards).toHaveCount(4)
  // Administrators first.
  await expect(cards.first()).toContainText("Ion Moisei")
  await expect(cards.nth(1)).toContainText("Maria Rusu")
  await expect(cards.filter({ hasText: "Deploy bot" })).toContainText("never signed in")
  await expect(
    page.locator("[data-slot=panel]", { hasText: "Accounts" }).getByRole("button", {
      name: "New user",
    }),
  ).toBeVisible()

  // Changing the role from its picker does not open the editor behind it.
  await page.getByRole("button", { name: "Edit Maria Rusu" }).click()
  await expect(page.getByRole("dialog", { name: "Edit Maria Rusu" })).toBeVisible()
})

test("security is a form with its state in the rail", async ({ page }) => {
  await mockAccount(page, session(false, false))
  await page.goto("/account/security")
  await expect(page.getByRole("heading", { name: "Two-factor" })).toBeVisible()
  await expect(page.getByText("Your password is the only factor")).toBeVisible()
  await expect(page.getByRole("button", { name: "Enable two-factor" })).toBeVisible()

  // The password rule lights as it is met.
  await page.getByLabel("New password", { exact: true }).fill("Correcthorse1")
  await expect(page.getByText("12 characters or more")).toHaveClass(/text-success/)
  await expect(page.getByText(/three of upper case/)).toHaveClass(/text-success/)

  await expect(page.getByText("3 sessions signed in · 1 on a password alone")).toBeVisible()
})

for (const width of [1280, 1720, 390]) {
  test(`the account pages look right at ${width}`, async ({ page }) => {
    await mockAccount(page)
    await page.setViewportSize({ width, height: 1200 })
    for (const path of ["/account", "/account/sessions", "/account/keys", "/account/users"]) {
      await page.goto(path)
      await page.waitForLoadState("networkidle")
      expect(
        await page.evaluate(
          () => document.documentElement.scrollWidth > document.documentElement.clientWidth,
        ),
        `${path} scrolls sideways at ${width}`,
      ).toBe(false)
      const name = path.replaceAll("/", "-").slice(1)
      await page.screenshot({ path: `test-results/${name}-${width}.png`, fullPage: true })
    }
    await mockAccount(page, session(false, false))
    await page.goto("/account/security")
    await page.waitForLoadState("networkidle")
    await page.screenshot({ path: `test-results/account-security-${width}.png`, fullPage: true })
  })
}
