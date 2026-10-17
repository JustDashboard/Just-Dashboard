import { expect, test, type Page } from "@playwright/test"
import { mockDatabases, type DatabaseMock } from "./database-fixture"

/**
 * A database's Network settings, and the section on them that says whether the
 * server speaks TLS with a certificate from the dashboard's own authority.
 *
 * The claims are the ones the section makes to its reader. Turning TLS on,
 * off or toward required recreates the container, so each says what it does
 * before it is taken and sends exactly what was asked; renewing does not ask,
 * because it does not drop a session. An engine whose port speaks one protocol
 * is not offered a choice between TLS and plain text. A server that is not the
 * dashboard's to change says whose it is and offers no control, a role that may
 * not recreate a container is shown the section and cannot press it, and a
 * reader who is not an administrator is not shown a certificate at all.
 */

const NOW = Date.now()
const DAY = 24 * 60
/** Minutes from now, so a fixture never falls on an exact hour. */
const minutes = (n: number) => new Date(NOW + n * 60_000).toISOString()

const TLS_OFF = {
  supported: true,
  enabled: false,
  require: false,
  canOptional: true,
  canRequire: true,
  verified: false,
  container: "shop-db",
  port: 5432,
  exposure: "local",
  names: [],
  linked: 1,
  authority: null,
}

const AUTHORITY = {
  subject: "Just Dashboard database CA",
  fingerprint: "9f3ac2d1e47b08a6c5d9e2f1b3a47c8d0e6f1a2b3c4d5e6f708192a3b4c5d6e7",
  notAfter: minutes(3650 * DAY + 11),
  createdAt: minutes(-(40 * DAY + 37)),
}

const TLS_ON = {
  ...TLS_OFF,
  enabled: true,
  verified: true,
  names: ["127.0.0.1", "::1", "db-1.jd.internal", "localhost", "shop-db"],
  serial: "4F2A81C07B93D5E61A",
  issuedAt: minutes(-(40 * DAY + 37)),
  notAfter: minutes(325 * DAY + 7),
  renewAt: minutes(295 * DAY + 7),
  authority: AUTHORITY,
  client: {
    host: "127.0.0.1",
    port: 5432,
    url: "postgres://jd:<password>@127.0.0.1:5432/shop_main?sslmode=verify-full&sslrootcert=ca.pem",
    command:
      'psql "postgres://jd:<password>@127.0.0.1:5432/shop_main?sslmode=verify-full&sslrootcert=ca.pem"',
    caFile: "ca.pem",
  },
}

const TLS_REDIS = {
  ...TLS_OFF,
  container: "cache-db",
  port: 6379,
  canOptional: false,
  canRequire: false,
  linked: 0,
}

const REDIS_ON = {
  ...TLS_REDIS,
  enabled: true,
  require: true,
  verified: true,
  names: ["127.0.0.1", "::1", "cache-db", "db-4.jd.internal", "localhost"],
  serial: "A6C01E55D2B7",
  issuedAt: minutes(-(3 * DAY + 14)),
  notAfter: minutes(362 * DAY + 3),
  renewAt: minutes(332 * DAY + 3),
  authority: AUTHORITY,
  client: {
    host: "127.0.0.1",
    port: 6379,
    url: "rediss://:<password>@127.0.0.1:6379/0",
    command: "redis-cli --tls --cacert ca.pem -h 127.0.0.1 -p 6379 -a '<password>'",
    caFile: "ca.pem",
  },
}

type Sent = { request: string; body: unknown }

/**
 * The TLS routes of every connection, answered from a table that a write
 * changes: what the server reports is what the last change left, as the real
 * one's is.
 */
async function mockTLS(
  page: Page,
  options: DatabaseMock & {
    state?: Record<number, Record<string, unknown>>
    /** Reading TLS fails with this status. */
    readFails?: number
    /** Capabilities of a role that is not the full administrator. */
    capabilities?: string[]
  } = {},
) {
  const mock = await mockDatabases(page, options)
  const state: Record<number, Record<string, unknown>> = {
    1: TLS_OFF,
    4: TLS_REDIS,
    ...options.state,
  }
  const sent: Sent[] = []
  const seen: string[] = []

  if (options.capabilities) {
    const capabilities = options.capabilities
    await page.route("**/api/v1/auth/session", (route) =>
      route.fulfill({
        json: {
          authenticated: true,
          needsTotp: false,
          needsEnrollment: false,
          require2fa: false,
          capabilities,
          user: {
            id: 2,
            username: "operator",
            displayName: "Operator",
            avatarVersion: 0,
            role: "limited",
            totpEnabled: true,
            disabled: false,
            mustChangePassword: false,
            lastLoginAt: minutes(-90),
            createdAt: minutes(-(60 * DAY)),
          },
        },
      }),
    )
  }
  await page.route("**/api/v1/databases/tls/authority", (route) =>
    route.fulfill({
      status: 200,
      headers: {
        "content-type": "application/x-pem-file",
        "content-disposition": 'attachment; filename="just-dashboard-database-ca.pem"',
      },
      body: "-----BEGIN CERTIFICATE-----\nMIIBjzCCATWgAwIBAgIQ\n-----END CERTIFICATE-----\n",
    }),
  )
  await page.route(/\/api\/v1\/databases\/(\d+)\/tls(\/renew)?$/, async (route) => {
    const url = new URL(route.request().url())
    const [, id, renew] = /\/databases\/(\d+)\/tls(\/renew)?$/.exec(url.pathname) ?? []
    const method = route.request().method()
    seen.push(`${method} ${id}/tls${renew ?? ""}`)
    if (method === "GET") {
      if (options.readFails) {
        return route.fulfill({
          status: options.readFails,
          json: { error: { code: "internal", message: "the Docker daemon did not answer" } },
        })
      }
      return route.fulfill({ json: state[Number(id)] })
    }
    const body = route.request().postDataJSON() as { enabled?: boolean; require?: boolean } | null
    sent.push({ request: `${method} ${id}/tls${renew ?? ""}`, body })
    const current = state[Number(id)]
    if (renew) {
      state[Number(id)] = { ...current, serial: "C91D3E7A5560B2F4", renewedAt: minutes(-1) }
    } else if (body?.enabled === false) {
      state[Number(id)] = Number(id) === 4 ? TLS_REDIS : TLS_OFF
    } else {
      const base = Number(id) === 4 ? REDIS_ON : TLS_ON
      state[Number(id)] = { ...base, require: Boolean(body?.require) || !base.canOptional }
    }
    return route.fulfill({ json: state[Number(id)] })
  })
  return { ...mock, sent, seen }
}

async function visit(page: Page, path: string) {
  const listed = page.waitForResponse(
    (response) => new URL(response.url()).pathname === "/api/v1/databases/",
    { timeout: 45_000 },
  )
  await page.goto(path)
  await listed
}

const NETWORK = "/databases/1/settings/network"
const tls = (page: Page) => page.locator("section#tls")
const dialog = (page: Page) => page.getByRole("dialog")

/** A dialog has finished opening, so a picture of it shows the dialog and not the page through it. */
async function settled(page: Page) {
  await dialog(page).evaluate((el) =>
    Promise.all(el.getAnimations({ subtree: true }).map((a) => a.finished)),
  )
}

test("a server with TLS off says so, and turning it on says what it does before it does it", async ({
  page,
}) => {
  const server = await mockTLS(page)
  await visit(page, NETWORK)
  await expect(tls(page).getByRole("heading", { name: "Encryption in transit" })).toBeVisible()
  await expect(tls(page)).toContainText("Off")
  await expect(tls(page)).toContainText("The server speaks plain text")
  // Nothing about a certificate before there is one.
  await expect(tls(page).getByRole("button", { name: "Renew certificate" })).toHaveCount(0)

  await tls(page).getByRole("button", { name: "Turn on TLS" }).click()
  await expect(dialog(page)).toContainText("Turn on TLS for PostgreSQL")
  await expect(dialog(page)).toContainText("Recreates shop-db")
  await expect(dialog(page)).toContainText("Every open session is dropped")
  await expect(dialog(page)).toContainText("Plain text is still accepted until you require TLS")
  await dialog(page).getByRole("button", { name: "Turn on", exact: true }).click()

  await expect
    .poll(() => server.sent.find((one) => one.request === "PUT 1/tls")?.body)
    .toEqual({
      enabled: true,
      require: false,
    })
  await expect(tls(page)).toContainText("TLS allowed")
  await expect(tls(page)).toContainText("325 days left")
  await expect(tls(page)).toContainText("db-1.jd.internal")
  await expect(tls(page)).toContainText("Just Dashboard database CA")
  await expect(tls(page)).toContainText("Checks the certificate")
})

test("the connection string for another machine carries the mode that verifies the certificate", async ({
  page,
}) => {
  await mockTLS(page, { state: { 1: TLS_ON } })
  await visit(page, NETWORK)
  await expect(tls(page)).toContainText("sslmode=verify-full&sslrootcert=ca.pem")
  await expect(tls(page)).toContainText("jd:<password>@127.0.0.1:5432")
  // The password is a placeholder, never the real one.
  await expect(tls(page)).not.toContainText("s3cret")
  await expect(tls(page).getByRole("button", { name: "Copy connection string" })).toBeVisible()
  await expect(tls(page).getByRole("button", { name: "Copy command" })).toBeVisible()
})

test("requiring TLS warns the deployments that connect by name, and sends the choice", async ({
  page,
}) => {
  const server = await mockTLS(page, { state: { 1: TLS_ON } })
  await visit(page, NETWORK)
  const choice = tls(page).getByRole("group", { name: "Whether plain text is accepted" })
  await expect(choice.getByRole("button", { name: /TLS allowed/ })).toHaveAttribute(
    "aria-pressed",
    "true",
  )
  await choice.getByRole("button", { name: /TLS required/ }).click()
  await expect(dialog(page)).toContainText("Require TLS for PostgreSQL")
  await expect(dialog(page)).toContainText("1 deployment environment reaches this database")
  await dialog(page).getByRole("button", { name: "Require", exact: true }).click()
  await expect
    .poll(() => server.sent.find((one) => one.request === "PUT 1/tls")?.body)
    .toEqual({
      enabled: true,
      require: true,
    })
  await expect(tls(page)).toContainText("TLS required")
  await expect(choice.getByRole("button", { name: /TLS required/ })).toHaveAttribute(
    "aria-pressed",
    "true",
  )
})

test("an engine whose port speaks one protocol is not offered a choice", async ({ page }) => {
  const server = await mockTLS(page)
  await visit(page, "/databases/4/settings/network")
  await expect(tls(page)).toContainText("Off")
  await expect(
    tls(page).getByRole("group", { name: "Whether plain text is accepted" }),
  ).toHaveCount(0)
  await tls(page).getByRole("button", { name: "Turn on TLS" }).click()
  await expect(dialog(page)).toContainText("plain text stops being accepted")
  await dialog(page).getByRole("button", { name: "Turn on", exact: true }).click()
  await expect
    .poll(() => server.sent.find((one) => one.request === "PUT 4/tls")?.body)
    .toEqual({
      enabled: true,
      require: true,
    })
  await expect(tls(page)).toContainText("TLS only")
  await expect(
    tls(page).getByRole("group", { name: "Whether plain text is accepted" }),
  ).toHaveCount(0)
  await expect(tls(page)).toContainText("rediss://:<password>@127.0.0.1:6379/0")
})

test("renewing replaces the certificate without a confirmation, because no session is dropped", async ({
  page,
}) => {
  const server = await mockTLS(page, { state: { 1: TLS_ON } })
  await visit(page, NETWORK)
  await tls(page).getByRole("button", { name: "Renew certificate" }).click()
  await expect(dialog(page)).toHaveCount(0)
  await expect.poll(() => server.sent.some((one) => one.request === "POST 1/tls/renew")).toBe(true)
  await expect(tls(page)).toContainText("Last renewed")
})

test("the authority's certificate is a download", async ({ page }) => {
  await mockTLS(page, { state: { 1: TLS_ON } })
  await visit(page, NETWORK)
  const download = page.waitForEvent("download")
  await tls(page).getByRole("button", { name: "Download CA certificate" }).click()
  expect((await download).suggestedFilename()).toBe("just-dashboard-database-ca.pem")
})

test("turning TLS off is confirmed and sends nothing but the choice", async ({ page }) => {
  const server = await mockTLS(page, { state: { 1: TLS_ON } })
  await visit(page, NETWORK)
  await tls(page).getByRole("button", { name: "Turn off TLS" }).click()
  await expect(dialog(page)).toContainText("Turn off TLS for PostgreSQL")
  await expect(dialog(page)).toContainText("removes the certificate")
  await dialog(page).getByRole("button", { name: "Turn off", exact: true }).click()
  await expect
    .poll(() => server.sent.find((one) => one.request === "PUT 1/tls")?.body)
    .toEqual({
      enabled: false,
    })
  await expect(tls(page)).toContainText("Off")
  await expect(tls(page).getByRole("button", { name: "Turn on TLS" })).toBeVisible()
})

test("a server that is not the dashboard's to change says whose it is, and offers no control", async ({
  page,
}) => {
  await mockTLS(page, {
    state: {
      1: {
        ...TLS_OFF,
        supported: false,
        container: "stack-db-1",
        reason: "stack-db-1 belongs to the compose project stack. Set up TLS in its compose file.",
      },
    },
  })
  await visit(page, NETWORK)
  await expect(tls(page).locator("[data-slot=tls-refusal]")).toContainText(
    "belongs to the compose project stack",
  )
  await expect(tls(page)).toContainText("Not managed here")
  await expect(tls(page).getByRole("button", { name: "Turn on TLS" })).toHaveCount(0)
})

test("a role that may not recreate a container sees the section and cannot press it", async ({
  page,
}) => {
  await mockTLS(page, {
    capabilities: ["read", "service.control", "system.admin"],
    state: { 1: TLS_ON },
  })
  await visit(page, NETWORK)
  await expect(tls(page)).toContainText("TLS allowed")
  await expect(tls(page).getByRole("button", { name: "Renew certificate" })).toBeDisabled()
  await expect(tls(page).getByRole("button", { name: "Turn off TLS" })).toBeDisabled()
  await expect(tls(page)).toContainText("which your role may not do")
})

test("a reader who is not an administrator is shown no certificate and asks for none", async ({
  page,
}) => {
  const server = await mockTLS(page, { viewer: true, state: { 1: TLS_ON } })
  await visit(page, NETWORK)
  await expect(tls(page)).toContainText("an administrator’s to read and to change")
  await expect(tls(page)).not.toContainText("4F2A81C07B93D5E61A")
  await expect(tls(page).getByRole("button")).toHaveCount(0)
  expect(server.seen).not.toContain("GET 1/tls")
})

test("a reading that fails says so and can be tried again", async ({ page }) => {
  await mockTLS(page, { readFails: 502 })
  await visit(page, NETWORK)
  await expect(tls(page)).toContainText("Could not read whether it speaks TLS")
  await expect(tls(page).getByRole("button", { name: /Retry|Try again/ })).toBeVisible()
  await expect(tls(page).getByRole("button", { name: "Turn on TLS" })).toHaveCount(0)
})

test("a change that is refused leaves the section as it was and says why", async ({ page }) => {
  await mockTLS(page)
  await page.route(/\/api\/v1\/databases\/1\/tls$/, (route) =>
    route.request().method() === "PUT"
      ? route.fulfill({
          status: 502,
          json: {
            error: {
              code: "tls_failed",
              message:
                "shop-db did not accept connections with TLS on (connection refused). It was put back as it was.",
            },
          },
        })
      : route.fallback(),
  )
  await visit(page, NETWORK)
  await tls(page).getByRole("button", { name: "Turn on TLS" }).click()
  await dialog(page).getByRole("button", { name: "Turn on", exact: true }).click()
  await expect(page.getByText("It was put back as it was.")).toBeVisible()
  await expect(tls(page)).toContainText("Off")
})

test("the section fits a phone and can be worked by keyboard", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await mockTLS(page, { state: { 1: TLS_ON } })
  await visit(page, NETWORK)
  await expect(tls(page)).toContainText("TLS allowed")
  expect(await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth)).toBe(
    false,
  )
  await tls(page).getByRole("button", { name: "Renew certificate" }).focus()
  await expect(tls(page).getByRole("button", { name: "Renew certificate" })).toBeFocused()
  await page.keyboard.press("Tab")
  await expect(tls(page).getByRole("button", { name: "Turn off TLS" })).toBeFocused()
})

test("every state of the section @shots", async ({ page }) => {
  test.skip(!process.env.JD_SHOTS, "set JD_SHOTS to a directory to write the screenshots to")
  const out = process.env.JD_SHOTS as string
  await page.setViewportSize({ width: 1440, height: 900 })
  const server = await mockTLS(page)
  await visit(page, NETWORK)
  const section = tls(page)
  await section.scrollIntoViewIfNeeded()
  await page.screenshot({ path: `${out}/tls-off-1440.png` })

  await section.getByRole("button", { name: "Turn on TLS" }).click()
  await settled(page)
  await page.screenshot({ path: `${out}/tls-turn-on-dialog-1440.png` })
  await dialog(page).getByRole("button", { name: "Turn on", exact: true }).click()
  await expect.poll(() => server.sent.length).toBe(1)
  await expect(section).toContainText("TLS allowed")
  await section.scrollIntoViewIfNeeded()
  await page.screenshot({ path: `${out}/tls-allowed-1440.png`, fullPage: true })

  await section.getByRole("button", { name: /TLS required/ }).click()
  await settled(page)
  await page.screenshot({ path: `${out}/tls-require-dialog-1440.png` })
  await dialog(page).getByRole("button", { name: "Require", exact: true }).click()
  await expect(section).toContainText("TLS required")
  await section.scrollIntoViewIfNeeded()
  await page.screenshot({ path: `${out}/tls-required-1440.png`, fullPage: true })

  await page.goto("/databases/4/settings/network")
  await expect(tls(page)).toContainText("Off")
  await tls(page).getByRole("button", { name: "Turn on TLS" }).click()
  await settled(page)
  await page.screenshot({ path: `${out}/tls-redis-dialog-1440.png` })
  await dialog(page).getByRole("button", { name: "Turn on", exact: true }).click()
  await expect(tls(page)).toContainText("TLS only")
  await tls(page).scrollIntoViewIfNeeded()
  await page.screenshot({ path: `${out}/tls-redis-only-1440.png`, fullPage: true })

  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto(NETWORK)
  await expect(tls(page)).toContainText("TLS required")
  await tls(page).scrollIntoViewIfNeeded()
  await page.screenshot({ path: `${out}/tls-required-390.png`, fullPage: true })
})
