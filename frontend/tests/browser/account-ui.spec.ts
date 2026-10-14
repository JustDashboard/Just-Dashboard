import { expect, test, type Page, type Route } from "@playwright/test"

/**
 * The account pages after their 0.7.5 pass, against a mocked API: no page
 * opens on tiles; the profile wires everything signed in as the account into
 * its face, sessions are drawn as the path each takes to the server, keys as
 * what holds them wired through their role, users by their faces in a band
 * over cards that chips narrow, and Security opens on what a sign-in passes.
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
    authMethod: "totp",
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
    authMethod: "recovery",
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
    authMethod: "password",
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

test("the profile opens on the account's identity line and what is signed in as it", async ({
  page,
}) => {
  await mockAccount(page)
  await page.goto("/account")
  await expect(page.getByRole("heading", { name: "Ion Moisei" })).toBeVisible()
  await expect(
    page.locator("[data-slot=host-identity]").getByText("@ion", { exact: true }),
  ).toBeVisible()
  await expect(page.getByText(/^member since/)).toBeVisible()
  await expect(page.locator("[data-slot=host-identity]")).toContainText("Chrome on macOS")
  await expect(page.locator("[data-slot=host-identity]")).toContainText("4 accounts")
  await expect(page.locator('img[src="/logos/tailscale.svg"]').first()).toBeVisible()
  await expect(page.getByText("two-factor on").first()).toBeVisible()

  // The four tiles went; what they counted is the picture's two sides.
  await expect(page.locator("[data-slot=stat-tile]")).toHaveCount(0)
  const signedIn = page.getByRole("group", { name: "Signed in" })
  for (const logo of ["chrome", "safari", "curl"]) {
    await expect(signedIn.locator(`img[src="/logos/${logo}.svg"]`)).toBeVisible()
  }
  await expect(page.getByRole("link", { name: /3 sessions/ })).toHaveAttribute(
    "href",
    "/account/sessions",
  )
  const keyring = page.getByRole("group", { name: "API keys" })
  await expect(keyring.locator('img[src="/logos/github.svg"]')).toBeVisible()
  // Maria's grafana key is hers, not something signed in as this account.
  await expect(keyring.locator('img[src="/logos/grafana.svg"]')).toHaveCount(0)
  await expect(keyring).toContainText("never used")

  // The ladder lights up to this account's role and says what each adds.
  const ladder = page.getByRole("list", { name: "Roles" })
  await expect(ladder).toContainText("your role")
  await expect(ladder.getByText("Open a shell")).toBeVisible()
  await expect(ladder.locator('img[src="/logos/docker.svg"]')).toBeVisible()
  await expect(page.getByText("Granted.")).toHaveCount(6)

  // A rename raises the floating Save rather than a button under the fields.
  await page.getByLabel("Name", { exact: true }).fill("Ion M.")
  await expect(page.getByText("Edited", { exact: true })).toBeVisible()
  await expect(
    page.getByRole("img", { name: /The rail draws this account as Ion M\./ }),
  ).toBeVisible()
  await expect(page.getByRole("button", { name: /^Save/ })).toBeVisible()
})

test("sessions are the browser, the system and the network each came from", async ({ page }) => {
  await mockAccount(page)
  await page.goto("/account/sessions")
  // This device is the identity line, drawn as Chrome, with macOS and the
  // tailnet among its facts.
  await expect(page.locator("[data-slot=host-identity]")).toContainText("this device")
  await expect(page.getByText("password and authenticator code").first()).toBeVisible()

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

  // Every session is the path it takes: the networks between them and the server.
  const networks = page.getByRole("group", { name: "Networks" })
  await expect(networks).toContainText("Tailscale")
  // Written lower case and set with a capital, as a sentence would start.
  await expect(networks).toContainText("local network")
  await expect(networks).toContainText("internet")
  await expect(page.getByRole("group", { name: "Sessions" })).toContainText("owes a code")
  // Each other session's life on the shared axis.
  await expect(phone.getByRole("img", { name: /^Signed in .* expires/ })).toBeVisible()
})

test("keys are drawn as what holds them, wired through their role", async ({ page }) => {
  await mockAccount(page)
  await page.goto("/account/keys")
  await expect(page.locator("[data-slot=stat-tile]")).toHaveCount(0)

  const map = page.getByRole("group", { name: "Keys" })
  await expect(map.locator('img[src="/logos/github.svg"]')).toBeVisible()
  await expect(page.getByRole("group", { name: "Roles" })).toContainText("Limited")
  await expect(page.getByRole("group", { name: "Roles" })).toContainText("no key")

  // The tiles' counts are chips now, and a chip narrows the list.
  const chips = page.getByRole("group", { name: "Show" })
  await expect(chips.getByRole("button", { name: /Never used\s*1/ })).toBeVisible()
  await chips.getByRole("button", { name: /Expiring soon\s*1/ }).click()
  const rows = page.locator("[data-slot=panel]", { hasText: "In use" }).locator("[data-slot=row]")
  await expect(rows).toHaveCount(1)
  await expect(rows.first()).toContainText("github-actions")
  await chips.getByRole("button", { name: /^All/ }).click()
  await expect(rows).toHaveCount(3)

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

test("users are cards drawn by their faces, under a band of who they are", async ({ page }) => {
  await mockAccount(page)
  await page.setViewportSize({ width: 1440, height: 1000 })
  await page.goto("/account/users")
  await expect(page.locator("[data-slot=stat-tile]")).toHaveCount(0)
  const line = page.locator("[data-slot=host-identity]")
  await expect(line).toContainText("4 accounts")
  await expect(line).toContainText("2 administrators")

  const band = page.locator("[data-slot=people-band]")
  await expect(band.getByRole("list", { name: "Sign-ins" })).toContainText("Ion Moisei")
  await expect(band).toContainText(/1\s*of 3 ask for a code/)
  await expect(band).toContainText("Administrator")

  const cards = page.locator("[data-slot=choice-row]")
  await expect(cards).toHaveCount(4)
  // Administrators first.
  await expect(cards.first()).toContainText("Ion Moisei")
  await expect(cards.nth(1)).toContainText("Maria Rusu")
  await expect(cards.filter({ hasText: "Deploy bot" })).toContainText("never signed in")
  await expect(page.getByRole("button", { name: "New user" })).toBeVisible()

  // The verdict narrows the cards to exactly the accounts it counts.
  await page.getByRole("button", { name: "Only the accounts a password alone opens" }).click()
  await expect(cards).toHaveCount(2)
  await expect(cards.filter({ hasText: "Guest" })).toHaveCount(0)
  await page.getByRole("group", { name: "Show" }).getByRole("button", { name: /^All/ }).click()
  await expect(cards).toHaveCount(4)

  // Changing the role from its picker does not open the editor behind it.
  await page.getByRole("button", { name: "Edit Maria Rusu" }).click()
  await expect(page.getByRole("dialog", { name: "Edit Maria Rusu" })).toBeVisible()
})

test("security is a form with its state under each head", async ({ page }) => {
  await mockAccount(page, session(false, false))
  await page.goto("/account/security")
  // The picture says it first: the second factor is a ring nobody stands in.
  const path = page.getByRole("list", { name: "What a sign-in passes" })
  await expect(path).toContainText("Not enrolled")
  await expect(path).toContainText("a password alone gets in")
  await expect(page.getByRole("heading", { name: "Two-factor" })).toBeVisible()
  await expect(page.getByText("Your password is the only factor")).toBeVisible()
  await expect(page.getByRole("button", { name: "Enable two-factor" })).toBeVisible()

  // The password rule lights as it is met.
  await page.getByLabel("New password", { exact: true }).fill("Correcthorse1")
  await expect(page.getByText("12 characters or more")).toHaveClass(/text-success/)
  await expect(page.getByText(/three of upper case/)).toHaveClass(/text-success/)

  await expect(page.getByText("3 sessions signed in · 1 awaiting a second factor")).toBeVisible()
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

test("Done discards a one-time key and the next creation starts with a blank form", async ({
  page,
}) => {
  await mockAccount(page)
  let minted = 0
  await page.route("**/api/v1/tokens/", async (route) => {
    if (route.request().method() === "POST")
      return json(route, { secret: `synthetic-key-${++minted}` })
    return json(route, tokens)
  })
  await page.goto("/account/keys")
  await page.getByRole("button", { name: "New key" }).click()
  await page.getByLabel("Name").fill("first key")
  await page.getByRole("button", { name: "Create key" }).click()
  await expect(page.getByRole("dialog")).toContainText("synthetic-key-1")
  await page.getByRole("button", { name: "Done", exact: true }).click()
  await page.getByRole("button", { name: "New key" }).click()
  await expect(page.getByLabel("Name")).toHaveValue("")
  await expect(page.getByRole("dialog")).not.toContainText("synthetic-key-1")
  await page.getByLabel("Name").fill("second key")
  await page.getByRole("button", { name: "Create key" }).click()
  await expect(page.getByRole("dialog")).toContainText("synthetic-key-2")
  expect(minted).toBe(2)
})

for (const method of ["password", "unknown"]) {
  test(`completed ${method} session reports its recorded proof honestly`, async ({ page }) => {
    await mockAccount(page, session(false, false))
    await page.route("**/api/v1/account/sessions", (route) =>
      json(route, [{ ...sessions[0], authMethod: method }]),
    )
    await page.goto("/account/sessions")
    await expect(page.locator("[data-slot=host-identity]")).toContainText(
      method === "password" ? "password only" : "authentication method not recorded",
    )
    await expect(page.locator("[data-slot=host-identity]")).not.toContainText("password and")
    await page.goto("/account/security")
    await expect(
      page.getByText(
        method === "password" ? /1 on a password alone/ : /1 authentication method not recorded/,
      ),
    ).toBeVisible()
  })
}

for (const required of [false, true]) {
  test(`users describe enrollment and reset under ${required ? "required" : "optional"} two-factor`, async ({
    page,
  }) => {
    await mockAccount(page, session(required))
    await page.goto("/account/users")
    const reading = page.locator("[data-slot=host-identity]")
    await expect(reading).toContainText(
      required
        ? "1 administrator must enrol before signing in"
        : "1 administrator on a password alone",
    )
    const band = page.locator("[data-slot=people-band]")
    await expect(band).toContainText(required ? /1\s*of 3 enrolled/ : /1\s*of 3 ask for a code/)
    const chip = page
      .getByRole("group", { name: "Show" })
      .getByRole("button", { name: required ? /^Enrollment required/ : /^Password only/ })
    await expect(chip).toContainText("2")
    const maria = page.locator("[data-slot=choice-row]", { hasText: "Maria Rusu" })
    await expect(maria).toContainText(required ? "enrolment required" : "password only")
    if (required) {
      await expect(reading).toContainText("3 enabled accounts")
      await expect(page.getByText("password only", { exact: true })).toHaveCount(0)
      await expect(page.getByText(/on a password alone/)).toHaveCount(0)
    }
    await chip.click()
    await expect(page.locator("[data-slot=choice-row]")).toHaveCount(2)
    await expect(page.locator("[data-slot=choice-row]", { hasText: "Guest" })).toHaveCount(0)
    await page.getByRole("group", { name: "Show" }).getByRole("button", { name: /^All/ }).click()
    await page.getByRole("button", { name: "More actions for Ion Moisei" }).click()
    await page.getByRole("menuitem", { name: "Reset two-factor" }).click()
    const reset = page.getByRole("dialog", { name: "Reset two-factor" })
    await expect(reset).toContainText(
      required
        ? "must enrol an authenticator again before completing sign-in"
        : "signs in with a password alone until they enrol an authenticator again",
    )
    await reset.getByRole("button", { name: "Cancel", exact: true }).click()
    await expect(reset).toHaveCount(0)
  })
}

test("required enrollment remains visible when only non-administrators need a factor", async ({
  page,
}) => {
  await mockAccount(page, session(true))
  await page.route("**/api/v1/dashboard-users/", (route) =>
    json(
      route,
      users.map((user) => (user.id === 2 ? { ...user, role: "limited" } : user)),
    ),
  )
  await page.goto("/account/users")
  const reading = page.locator("[data-slot=host-identity]")
  await expect(reading).toContainText("2 accounts must enrol before signing in")
  await expect(reading).not.toContainText("password alone")
  await expect(page.locator("[data-slot=people-band]")).toContainText(/1\s*of 3 enrolled/)
  await expect(
    page.getByRole("group", { name: "Show" }).getByRole("button", { name: /^Enrollment required/ }),
  ).toContainText("2")
})

test("profile and sign-in pictures distinguish required enrollment from a password-only path", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1440, height: 1000 })
  for (const required of [false, true]) {
    await mockAccount(page, session(required, false))
    await page.goto("/account")
    await expect(
      page.getByText(required ? "enrolment required" : "password only", { exact: true }).first(),
    ).toBeVisible()
    if (required) await expect(page.getByText("password only", { exact: true })).toHaveCount(0)
    await page.goto("/account/security")
    const path = page.getByRole("list", { name: "What a sign-in passes" })
    await expect(path).toContainText("Not enrolled")
    await expect(path).toContainText(
      required ? "must enrol before completing sign-in" : "a password alone gets in",
    )
    // Only an optional second factor has a wire bypassing the empty authenticator.
    await expect(path.locator("..").locator(":scope > svg")).toHaveCount(required ? 3 : 4)
  }
})
