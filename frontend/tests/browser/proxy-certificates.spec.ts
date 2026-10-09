import { expect, test, type Page, type WebSocketRoute } from "@playwright/test"
import { availability, certs, json, mockProxy, mockShowcase, now, user } from "./proxy-fixtures"
import {
  certbotJob,
  certbotLineage,
  certbotState,
  reloadHook,
  renewalHealth,
  servedCertificate,
  stagingCertificate,
} from "./fixtures/proxy/certs"
import { healthyOperations, mockProject } from "./deploy-fixture"
import type { DeploymentDomainRoute } from "../../src/lib/types"

/**
 * The Certificates page's certbot controls, checked against what they send
 * and when they may be pressed. Each case is one of the ways the page used to
 * tell an operator something that was not true: a test issuance that left an
 * untrusted certificate live, verbs that re-enabled while certbot still held
 * its lock, a wait field the dialog talked about and never sent, a token
 * saved before the request that needed it was refused, a hand-off link that
 * reopened the form on every reload, and an import that overwrote a name in
 * use without a word.
 */

/** Records every request to a path, answering each with `answer`. */
async function capture(page: Page, pattern: string, answer: (body: unknown) => unknown) {
  const bodies: unknown[] = []
  await page.route(pattern, async (route) => {
    const body = route.request().postDataJSON()
    bodies.push(body)
    await json(route, answer(body))
  })
  return bodies
}

/** Holds each job's stream open so the test decides when the job ends. */
async function holdJobStreams(page: Page) {
  const sockets: WebSocketRoute[] = []
  await page.routeWebSocket(/\/api\/v1\/jobs\/[^/]+\/stream/, (socket) => {
    sockets.push(socket)
  })
  return {
    finish: (job: object) => {
      for (const socket of sockets) {
        socket.send(JSON.stringify({ type: "job", data: job, ts: Date.now() }))
      }
    },
  }
}

test("a test run is certbot's dry run, and the real issuance follows it with the same names", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  const issued = await capture(page, "**/api/v1/certificates/issue", (body) => {
    const test = (body as { staging: boolean }).staging
    return certbotJob({
      kind: "certbot.issue",
      title: `${test ? "Test issuance" : "Issuing a certificate"} for app.example.com`,
      target: "app.example.com",
      status: "succeeded",
    })
  })
  await page.goto("/proxy/certificates")
  await page.getByRole("button", { name: "Issue certificate", exact: true }).click()
  const dialog = page.getByRole("dialog")
  await dialog.getByLabel("Domains").fill("app.example.com")
  await dialog.getByLabel("Contact email").fill("ops@example.com")
  await dialog.getByRole("radio", { name: "A folder", exact: true }).click()
  await expect(dialog.getByText(/saves nothing/)).toBeVisible()
  await dialog.getByRole("button", { name: "Run the test" }).click()

  await expect(page.getByText("The test run for app.example.com passed")).toBeVisible()
  expect(issued[0]).toMatchObject({
    staging: true,
    method: "webroot",
    domains: ["app.example.com"],
  })

  await page.getByRole("button", { name: "Issue the real certificate" }).click()
  const real = page.getByRole("dialog")
  await expect(real.getByLabel("Domains")).toHaveValue("app.example.com")
  await real.getByLabel("Contact email").fill("ops@example.com")
  await real.getByRole("radio", { name: "A folder", exact: true }).click()
  await real.getByRole("button", { name: "Issue", exact: true }).click()
  await expect(real).toBeHidden()
  expect(issued[1]).toMatchObject({ staging: false, domains: ["app.example.com"] })
})

test("certbot's verbs wait while a certbot run is on screen, and come back when it ends", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  const stream = await holdJobStreams(page)
  const running = certbotJob({
    kind: "certbot.renew",
    title: "Renewing app.example.com",
    target: "app.example.com",
  })
  const renewals = await capture(page, "**/api/v1/certificates/renew", () => running)
  await page.goto("/proxy/certificates")

  const lineages = page.getByRole("list", { name: "certbot lineages" })
  await lineages.getByRole("button", { name: "Renew", exact: true }).click()
  // The job is still running after the 202: the lineage says so and nothing
  // certbot does can be pressed until it ends.
  await expect(lineages.getByText("Renewing…")).toBeVisible()
  await expect(page.getByText("certbot is busy")).toBeVisible()
  await expect(lineages.getByRole("button", { name: "Renew", exact: true })).toBeDisabled()
  await expect(page.getByRole("button", { name: "Renew all due" })).toBeDisabled()
  await page.getByRole("button", { name: "Issue certificate", exact: true }).click()
  const dialog = page.getByRole("dialog")
  await dialog.getByLabel("Domains").fill("shop.example.com")
  await dialog.getByLabel("Contact email").fill("ops@example.com")
  await expect(dialog.getByText("certbot is running. Wait for it to finish.")).toBeVisible()
  await expect(dialog.getByRole("button", { name: "Run the test" })).toBeDisabled()
  await page.keyboard.press("Escape")
  expect(renewals).toHaveLength(1)

  stream.finish({ ...running, status: "succeeded", endedAt: now })
  await expect(lineages.getByText("29d left")).toBeVisible()
  await expect(lineages.getByRole("button", { name: "Renew", exact: true })).toBeEnabled()
  await expect(page.getByRole("button", { name: "Renew all due" })).toBeEnabled()
  await expect(page.getByText("certbot is busy")).toHaveCount(0)
})

test("another tab's certbot run is refused by the server, and the page says what is running", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  await page.route("**/api/v1/certificates/renew", (route) =>
    route.fulfill({
      status: 409,
      contentType: "application/json",
      body: JSON.stringify({
        error: {
          code: "certbot_busy",
          message:
            "certbot is already running (Issuing a certificate for shop.example.com, started by operator). Wait for it to finish.",
        },
      }),
    }),
  )
  await page.goto("/proxy/certificates")
  await page.getByRole("button", { name: "Renew", exact: true }).click()
  await expect(page.getByText(/certbot is already running \(Issuing a certificate/)).toBeVisible()
})

test("a DNS issuance sends its propagation wait and its token with the request, and nothing before", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  await page.route("**/api/v1/certificates/dns-providers", (route) =>
    json(route, [
      {
        key: "cloudflare",
        name: "Cloudflare",
        plugin: "dns-cloudflare",
        installed: true,
        hasCredentials: false,
        defaultWait: 30,
        credentials: "dns_cloudflare_api_token = your-scoped-token",
      },
    ]),
  )
  const early: string[] = []
  await page.route("**/api/v1/certificates/dns-credentials", (route) => {
    early.push(route.request().url())
    return json(route, { saved: true })
  })
  const issued = await capture(page, "**/api/v1/certificates/issue", () =>
    certbotJob({
      kind: "certbot.issue",
      title: "Test issuance for *.example.com",
      status: "succeeded",
    }),
  )
  await page.goto("/proxy/certificates")
  await page.getByRole("button", { name: "Issue certificate", exact: true }).click()
  const dialog = page.getByRole("dialog")
  await dialog.getByLabel("Domains").fill("*.example.com")
  await dialog.getByLabel("Contact email").fill("ops@example.com")
  await dialog.getByRole("combobox", { name: "DNS provider" }).click()
  await page.getByRole("option", { name: "Cloudflare" }).click()
  await dialog.getByLabel("Credentials").fill("dns_cloudflare_api_token = t0ken")

  const wait = dialog.getByLabel("Propagation wait")
  await expect(wait).toHaveAttribute("placeholder", "30")
  await wait.fill("0")
  await expect(dialog.getByText("A whole number of seconds from 1 to 3600.")).toBeVisible()
  await expect(dialog.getByRole("button", { name: "Run the test" })).toBeDisabled()
  await wait.fill("120")
  await dialog.getByRole("button", { name: "Run the test" }).click()
  await expect(dialog).toBeHidden()

  expect(issued[0]).toMatchObject({
    method: "dns",
    dnsProvider: "cloudflare",
    dnsWait: 120,
    credentials: "dns_cloudflare_api_token = t0ken",
    staging: true,
  })
  expect(early).toEqual([])
})

test("the site form's hand-off link opens the form once, not on every reload", async ({ page }) => {
  await mockProxy(page, { included: true })
  await page.goto("/proxy/certificates?issue=app.example.com")
  const dialog = page.getByRole("dialog")
  await expect(dialog.getByLabel("Domains")).toHaveValue("app.example.com")
  await page.keyboard.press("Escape")
  await expect(dialog).toBeHidden()
  await expect(page).toHaveURL(/\/proxy\/certificates$/)
  await page.reload()
  await expect(page.getByRole("button", { name: "Issue certificate", exact: true })).toBeVisible()
  await expect(page.getByRole("dialog")).toHaveCount(0)
})

/** One certbot lineage holding a certificate a staging authority signed. */
async function mockStagingLineage(page: Page, overrides: Record<string, unknown> = {}) {
  await page.route("**/api/v1/certificates/certbot", (route) =>
    json(
      route,
      certbotState({
        certs: [
          certbotLineage({
            name: "test.example.com",
            domains: ["test.example.com", "www.test.example.com"],
            certPath: "/etc/letsencrypt/live/test.example.com/fullchain.pem",
            daysLeft: 80,
            staging: true,
          }),
        ],
        ...overrides,
      }),
    ),
  )
}

/**
 * What the sites naming the test certificate answer, in turn: the page asks
 * before it offers the reload and again, settled, after one. Each request's
 * query is kept.
 */
async function mockServed(page: Page, ...answers: object[][]) {
  const asked: URLSearchParams[] = []
  await page.route("**/api/v1/certificates/served**", (route) => {
    asked.push(new URL(route.request().url()).searchParams)
    return json(route, answers[Math.min(asked.length - 1, answers.length - 1)])
  })
  return asked
}

test("a staging lineage reads as a test certificate, and its verb is the real issuance, not a renewal", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  await mockStagingLineage(page)
  const renewals = await capture(page, "**/api/v1/certificates/renew", () => ({}))
  const issued = await capture(page, "**/api/v1/certificates/issue", () =>
    certbotJob({
      kind: "certbot.issue",
      title: "Replacing the test certificate for test.example.com, www.test.example.com",
      target: "test.example.com, www.test.example.com",
    }),
  )
  await page.goto("/proxy/certificates")
  const row = page
    .getByRole("list", { name: "certbot lineages" })
    .getByRole("listitem")
    .filter({ hasText: "test.example.com" })
  await expect(row.getByText("test certificate")).toBeVisible()
  await expect(row.getByText("80d left")).toHaveCount(0)
  await expect(row.locator("img[src='/logos/lets-encrypt.svg']")).toHaveCount(0)
  await expect(row.getByText(/A staging authority signed it, so browsers refuse it/)).toBeVisible()

  // certbot renews from the staging authority the lineage names: none of the
  // renewal verbs would give it anything but another test certificate.
  await expect(row.getByRole("button", { name: "Renew", exact: true })).toHaveCount(0)
  await row.getByRole("button", { name: "More actions for test.example.com" }).click()
  await expect(page.getByRole("menuitem", { name: "Revoke and delete" })).toBeVisible()
  await expect(page.getByRole("menuitem", { name: "Dry run" })).toHaveCount(0)
  await expect(page.getByRole("menuitem", { name: "Force renewal" })).toHaveCount(0)
  await page.keyboard.press("Escape")

  await row.getByRole("button", { name: "Replace with a real certificate" }).click()
  const dialog = page.getByRole("dialog")
  await expect(dialog.getByLabel("Domains")).toHaveValue("test.example.com www.test.example.com")
  await expect(dialog.getByRole("switch", { name: /Test run first/ })).not.toBeChecked()
  await expect(dialog.getByText("This counts against the rate limit")).toBeVisible()
  await dialog.getByLabel("Contact email").fill("ops@example.com")
  await dialog.getByRole("radio", { name: "A folder", exact: true }).click()
  await dialog.getByRole("button", { name: "Issue", exact: true }).click()
  await expect(dialog).toBeHidden()
  expect(issued).toHaveLength(1)
  expect(issued[0]).toMatchObject({
    staging: false,
    domains: ["test.example.com", "www.test.example.com"],
  })
  expect(renewals).toHaveLength(0)
  // The job names the certificate by its names, and the lineage holding
  // them says what is happening to it.
  await expect(row.getByText("Replacing…")).toBeVisible()
  await expect(row.getByRole("button", { name: "Replace with a real certificate" })).toBeDisabled()
})

test("a test certificate's lineage fits a phone with its verb", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await mockProxy(page, { included: true })
  await mockStagingLineage(page)
  await page.goto("/proxy/certificates")
  const verb = page.getByRole("button", { name: "Replace with a real certificate" })
  await verb.scrollIntoViewIfNeeded()
  await expect(verb).toBeInViewport({ ratio: 1 })
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= document.documentElement.clientWidth + 1,
    ),
  ).toBe(true)
})

/** The inventory with one test certificate among the mocked ones. */
async function mockStagingInventory(page: Page, overrides: Record<string, unknown> = {}) {
  await page.route("**/api/v1/certificates/", (route) =>
    json(route, [stagingCertificate(overrides), ...certs]),
  )
}

test("a test certificate in the inventory reads as one, without the Let's Encrypt mark, and its verb is the real issuance", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  await mockStagingInventory(page)
  const issued = await capture(page, "**/api/v1/certificates/issue", () =>
    certbotJob({
      kind: "certbot.issue",
      title: "Replacing the test certificate for test.example.com, www.test.example.com",
      target: "test.example.com, www.test.example.com",
      status: "succeeded",
    }),
  )
  await page.goto("/proxy/certificates")

  // Refused like an expired one, and not counted as expiring.
  const refused = page.locator("[data-slot='stat-grid']")
  await expect(refused.getByText("Refused", { exact: true })).toBeVisible()
  await expect(refused.getByText("1 expired · 1 test certificate")).toBeVisible()

  const inventory = page
    .getByRole("list", { name: "Installed certificates" })
    .locator("[data-slot='choice-row']")
  const card = inventory.filter({ hasText: "(STAGING) Riddling Rhubarb R12" })
  // Refused, so ahead of the one merely expiring: after the expired one,
  // whose days are fewer, and before app.example.com's twenty-nine.
  await expect(inventory.nth(0)).toContainText("old.example.com")
  await expect(inventory.nth(1)).toContainText("test.example.com")
  await expect(inventory.nth(2)).toContainText("app.example.com")
  await expect(card.getByText("test certificate", { exact: true })).toBeVisible()
  await expect(card.getByText("80d left")).toHaveCount(0)
  await expect(card.locator("img[src='/logos/lets-encrypt.svg']")).toHaveCount(0)
  await expect(
    card.getByText(
      "A staging authority signed it, so browsers refuse it. A real issuance for the same names replaces it.",
    ),
  ).toBeVisible()
  await expect(card.getByRole("meter").locator(".bg-destructive")).toHaveCount(1)

  // The detail surface says it too, with the same verb as its one command.
  await card.getByRole("button", { name: "Inspect test.example.com" }).click()
  const sheet = page.getByRole("dialog")
  await expect(sheet.getByText("A test certificate")).toBeVisible()
  await expect(sheet.locator("img[src='/logos/lets-encrypt.svg']")).toHaveCount(0)
  await sheet.getByRole("button", { name: "Replace with a real certificate" }).click()

  const dialog = page.getByRole("dialog", { name: "Issue a certificate" })
  await expect(dialog.getByLabel("Domains")).toHaveValue("test.example.com www.test.example.com")
  await expect(dialog.getByRole("switch", { name: /Test run first/ })).not.toBeChecked()
  await dialog.getByLabel("Contact email").fill("ops@example.com")
  await dialog.getByRole("radio", { name: "A folder", exact: true }).click()
  await dialog.getByRole("button", { name: "Issue", exact: true }).click()
  await expect(dialog).toBeHidden()
  expect(issued[0]).toMatchObject({
    staging: false,
    domains: ["test.example.com", "www.test.example.com"],
  })

  // The card's own verb opens the same issuance.
  await card.getByRole("button", { name: "Replace with a real certificate" }).click()
  await expect(
    page.getByRole("dialog", { name: "Issue a certificate" }).getByLabel("Domains"),
  ).toHaveValue("test.example.com www.test.example.com")
})

test("a test certificate a site names outside certbot is issued for, not replaced", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  await mockStagingInventory(page, {
    path: "/etc/nginx/ssl/test.example.com.crt",
    name: "test.example.com",
    source: "nginx:test.example.com",
  })
  await page.goto("/proxy/certificates")
  const card = page
    .getByRole("list", { name: "Installed certificates" })
    .getByRole("listitem")
    .filter({ hasText: "(STAGING) Riddling Rhubarb R12" })
  await expect(card.getByText(/point the site at it/)).toBeVisible()
  await expect(card.getByRole("button", { name: "Replace with a real certificate" })).toHaveCount(0)
  await card.getByRole("button", { name: "Issue a real certificate" }).click()
  await expect(page.getByRole("dialog").getByLabel("Domains")).toHaveValue(
    "test.example.com www.test.example.com",
  )
})

test("replacing a test certificate says so while it runs, then offers the reload its sites need", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  await mockStagingInventory(page)
  await mockStagingLineage(page)
  const stream = await holdJobStreams(page)
  const running = certbotJob({
    kind: "certbot.issue",
    title: "Replacing the test certificate for test.example.com, www.test.example.com",
    target: "test.example.com, www.test.example.com",
  })
  await capture(page, "**/api/v1/certificates/issue", () => running)
  const reloads = await capture(page, "**/api/v1/proxy/reload", () => ({ reloaded: true }))
  const asked = await mockServed(
    page,
    [servedCertificate()],
    [servedCertificate({ current: true, staging: false, issuer: "R11" })],
  )
  await page.goto("/proxy/certificates")

  const card = page
    .getByRole("list", { name: "Installed certificates" })
    .getByRole("listitem")
    .filter({ hasText: "(STAGING) Riddling Rhubarb R12" })
  await card.getByRole("button", { name: "Replace with a real certificate" }).click()
  const dialog = page.getByRole("dialog")
  await dialog.getByLabel("Contact email").fill("ops@example.com")
  await dialog.getByRole("radio", { name: "A folder", exact: true }).click()
  await dialog.getByRole("button", { name: "Issue", exact: true }).click()
  await expect(dialog).toBeHidden()

  // The card and the lineage both say what is happening, and neither can
  // start a second certbot run.
  await expect(card.getByText("Replacing…")).toBeVisible()
  await expect(card.getByRole("button", { name: "Replace with a real certificate" })).toBeDisabled()
  const lineage = page
    .getByRole("list", { name: "certbot lineages" })
    .getByRole("listitem")
    .filter({ hasText: "test.example.com" })
  await expect(lineage.getByText("Replacing…")).toBeVisible()
  await expect(page.getByText("nginx is still serving the test certificate")).toHaveCount(0)

  // Nothing reloaded nginx: the site answers with the test certificate, and
  // the page says so and offers the reload.
  expect(asked).toHaveLength(0)
  stream.finish({ ...running, status: "succeeded", endedAt: now })
  const notice = page.getByText("nginx is still serving the test certificate")
  await expect(notice).toBeVisible()
  await expect(
    page.getByText(/test\.example\.com keeps serving the test one until nginx reloads/),
  ).toBeVisible()
  expect(asked[0].get("path")).toBe("/etc/letsencrypt/live/test.example.com/fullchain.pem")
  expect(asked[0].get("settle")).toBeNull()
  await page.getByRole("button", { name: "Reload nginx" }).click()
  // The toast says what the site answered after the reload, not what a
  // reload is supposed to do.
  await expect(page.getByText("test.example.com serves the real certificate now.")).toBeVisible()
  await expect(notice).toHaveCount(0)
  expect(reloads).toEqual([{ kind: "nginx" }])
  expect(asked[1].get("settle")).toBe("1")
})

test("a replacement a deploy hook already reloaded nginx for offers no reload", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  await mockStagingInventory(page)
  const asked = await mockServed(page, [
    servedCertificate({ current: true, staging: false, issuer: "R11" }),
  ])
  await capture(page, "**/api/v1/certificates/issue", () =>
    certbotJob({
      kind: "certbot.issue",
      title: "Replacing the test certificate for test.example.com, www.test.example.com",
      target: "test.example.com, www.test.example.com",
      status: "succeeded",
    }),
  )
  await page.goto("/proxy/certificates")
  await page
    .getByRole("list", { name: "Installed certificates" })
    .getByRole("button", { name: "Replace with a real certificate" })
    .click()
  const dialog = page.getByRole("dialog")
  await dialog.getByLabel("Contact email").fill("ops@example.com")
  await dialog.getByRole("radio", { name: "A folder", exact: true }).click()
  await dialog.getByRole("button", { name: "Issue", exact: true }).click()
  await expect(dialog).toBeHidden()
  await expect.poll(() => asked.length).toBe(1)
  await expect(page.getByText("nginx is still serving the test certificate")).toHaveCount(0)
  await expect(page.getByRole("button", { name: "Reload nginx" })).toHaveCount(0)
})

test("a reload that leaves a site on the test certificate says so and keeps the offer", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  await mockStagingInventory(page)
  await capture(page, "**/api/v1/proxy/reload", () => ({ reloaded: true }))
  await mockServed(page, [servedCertificate()])
  await capture(page, "**/api/v1/certificates/issue", () =>
    certbotJob({
      kind: "certbot.issue",
      title: "Replacing the test certificate for test.example.com, www.test.example.com",
      target: "test.example.com, www.test.example.com",
      status: "succeeded",
    }),
  )
  await page.goto("/proxy/certificates")
  await page
    .getByRole("list", { name: "Installed certificates" })
    .getByRole("button", { name: "Replace with a real certificate" })
    .click()
  const dialog = page.getByRole("dialog")
  await dialog.getByLabel("Contact email").fill("ops@example.com")
  await dialog.getByRole("radio", { name: "A folder", exact: true }).click()
  await dialog.getByRole("button", { name: "Issue", exact: true }).click()
  await page.getByRole("button", { name: "Reload nginx" }).click()
  await expect(page.getByText("test.example.com still serves the test certificate.")).toBeVisible()
  await expect(page.getByText("serves the real certificate now")).toHaveCount(0)
  await expect(page.getByText("nginx is still serving the test certificate")).toBeVisible()
})

test("an old replacement opened from the recent runs raises nothing once nginx serves the real certificate", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  // A day later the inventory lists a real certificate, and the site serves it.
  await mockStagingInventory(page, { issuer: "R11", staging: false })
  const asked = await mockServed(page, [
    servedCertificate({ current: true, staging: false, issuer: "R11" }),
  ])
  const dayOld = new Date(Date.now() - 86_400_000).toISOString()
  const old = certbotJob({
    id: "job-old",
    kind: "certbot.issue",
    title: "Replacing the test certificate for test.example.com, www.test.example.com",
    target: "test.example.com, www.test.example.com",
    status: "succeeded",
    startedAt: dayOld,
    endedAt: dayOld,
  })
  await page.route("**/api/v1/jobs/", (route) => json(route, [old]))
  await page.route("**/api/v1/jobs/job-old", (route) => json(route, { job: old, lines: [] }))
  await page.goto("/proxy/certificates")
  await page
    .getByRole("button", { name: /test\.example\.com, www\.test\.example\.com/ })
    .first()
    .click()
  await expect(page.getByText(/succeeded/).first()).toBeVisible()
  await expect.poll(() => asked.length).toBe(1)
  await expect(page.getByText("nginx is still serving the test certificate")).toHaveCount(0)
})

test("a replaced test certificate nothing serves asks for no reload", async ({ page }) => {
  await mockProxy(page, { included: true })
  await mockStagingInventory(page, { usedBy: [] })
  const asked = await mockServed(page, [servedCertificate()])
  await capture(page, "**/api/v1/certificates/issue", () =>
    certbotJob({
      kind: "certbot.issue",
      title: "Replacing the test certificate for test.example.com, www.test.example.com",
      target: "test.example.com, www.test.example.com",
      status: "succeeded",
    }),
  )
  await page.goto("/proxy/certificates")
  await page.getByRole("button", { name: "Replace with a real certificate" }).click()
  const dialog = page.getByRole("dialog")
  await dialog.getByLabel("Contact email").fill("ops@example.com")
  await dialog.getByRole("radio", { name: "A folder", exact: true }).click()
  await dialog.getByRole("button", { name: "Issue", exact: true }).click()
  await expect(dialog).toBeHidden()
  await expect(
    page.getByText("Replacing the test certificate for test.example.com").first(),
  ).toBeVisible()
  await expect(page.getByText("nginx is still serving the test certificate")).toHaveCount(0)
  // No site names it, so nothing is asked.
  expect(asked).toHaveLength(0)
})

test("a read-only account reads a test certificate for what it is, with nothing to press", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  await mockStagingInventory(page)
  await page.route("**/api/v1/auth/session", (route) =>
    json(route, { ...user, capabilities: ["read"], user: { ...user.user, role: "viewer" } }),
  )
  await page.goto("/proxy/certificates")
  const card = page
    .getByRole("list", { name: "Installed certificates" })
    .getByRole("listitem")
    .filter({ hasText: "(STAGING) Riddling Rhubarb R12" })
  await expect(card.getByText("test certificate", { exact: true })).toBeVisible()
  await expect(
    card.getByText("A staging authority signed it, so browsers refuse it.", { exact: true }),
  ).toBeVisible()
  await expect(card.getByRole("button", { name: /real certificate/ })).toHaveCount(0)
  await page.getByRole("button", { name: "Inspect test.example.com" }).click()
  await expect(
    page.getByRole("dialog").getByRole("button", { name: /real certificate/ }),
  ).toHaveCount(0)
})

test("a test certificate is a finding on the overview", async ({ page }) => {
  await mockProxy(page, { included: true })
  await mockStagingInventory(page)
  await page.goto("/proxy")
  const finding = page.getByRole("button", { name: /^test\.example\.com is a test certificate/ })
  await expect(finding).toBeVisible()
  await finding.click()
  await expect(
    page.getByText(
      "A staging authority signed it, so every browser refuses it. Used by test.example.com.",
    ),
  ).toBeVisible()
})

test("with a staging authority configured, the overview's test-certificate finding points at the directory, not a real issuance", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  await mockStagingInventory(page)
  await mockStagingLineage(page, { testAuthority: true })
  await page.goto("/proxy")
  await page.getByRole("button", { name: /^test\.example\.com is a test certificate/ }).click()
  await expect(
    page.getByText(
      "JD_ACME_DIRECTORY names a staging authority, so what this dashboard issues is a test certificate too. Clear it or point it at a production directory and restart the dashboard, then replace this one.",
    ),
  ).toBeVisible()
  await expect(page.getByText(/real certificate from the Certificates page/)).toHaveCount(0)
})

test("with certbot not installed, the overview's test-certificate finding points at an import", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  await mockStagingInventory(page)
  // As the backend answers on such a host: the overview never asks certbot.
  await page.route("**/api/v1/proxy/status", (route) =>
    json(route, { ...availability, certbot: false }),
  )
  await page.route("**/api/v1/certificates/certbot", (route) =>
    route.fulfill({
      status: 503,
      contentType: "application/json",
      body: JSON.stringify({
        error: { code: "certbot_unavailable", message: "certbot is not installed on this host" },
      }),
    }),
  )
  await page.goto("/proxy")
  await page.getByRole("button", { name: /^test\.example\.com is a test certificate/ }).click()
  await expect(
    page.getByText(
      "certbot is not installed, so this dashboard cannot issue a real certificate. Import one for these names from the Certificates page and point the site at it.",
    ),
  ).toBeVisible()

  await page.goto("/proxy/certificates")
  await expect(page.getByRole("button", { name: "Import", exact: true })).toBeVisible()
  await expect(page.getByRole("button", { name: "Issue certificate", exact: true })).toHaveCount(0)
})

test("the overview's certificate tile and expiry list count a test certificate as refused", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  // Nothing expired or expiring beside it: the tile read "all valid" in
  // neutral, next to a critical finding for the same certificate.
  await page.route("**/api/v1/certificates/", (route) => json(route, [stagingCertificate()]))
  await page.goto("/proxy")
  await expect(
    page.getByRole("button", { name: /^test\.example\.com is a test certificate/ }),
  ).toBeVisible()
  const tile = page.locator("a[href='/proxy/certificates'][aria-label='Certificates']")
  await expect(tile).toContainText("1 need attention")
  await expect(tile).not.toContainText("all valid")
  await expect(tile.locator(".text-destructive")).toHaveCount(1)
  const expiry = page.locator("[data-slot='panel']").filter({ hasText: "Certificate expiry" })
  await expect(expiry.getByText("test", { exact: true })).toBeVisible()
  await expect(expiry.getByText("80d", { exact: true })).toHaveCount(0)
})

test("a test certificate's card fits a phone with its verb", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await mockProxy(page, { included: true })
  await mockStagingInventory(page)
  await page.goto("/proxy/certificates")
  const verb = page
    .getByRole("list", { name: "Installed certificates" })
    .getByRole("button", { name: "Replace with a real certificate" })
  await verb.scrollIntoViewIfNeeded()
  await expect(verb).toBeInViewport({ ratio: 1 })
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= document.documentElement.clientWidth + 1,
    ),
  ).toBe(true)
  await page.getByRole("button", { name: "Inspect test.example.com" }).click()
  const sheet = page.getByRole("dialog")
  const command = sheet.getByRole("button", { name: "Replace with a real certificate" })
  await expect(command).toBeInViewport({ ratio: 1 })
  expect(await sheet.evaluate((element) => element.scrollWidth <= element.clientWidth + 1)).toBe(
    true,
  )
})

test("with its own ACME authority the issue form names it, and quotes no Let's Encrypt limits", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  await page.route("**/api/v1/certificates/certbot", (route) =>
    json(route, certbotState({ directory: "https://ca.internal:9000/acme/acme/directory" })),
  )
  await page.goto("/proxy/certificates")
  await page.getByRole("button", { name: "Issue certificate", exact: true }).click()
  const dialog = page.getByRole("dialog")
  await expect(
    dialog.getByText(/goes through the whole exchange with ca\.internal:9000 and saves nothing/),
  ).toBeVisible()
  await expect(dialog.getByText(/staging authority/)).toHaveCount(0)
  await dialog.getByRole("switch", { name: /Test run first/ }).click()
  await expect(dialog.getByRole("button", { name: "Issue", exact: true })).toBeVisible()
  await expect(dialog.getByText("This counts against the rate limit")).toHaveCount(0)
})

test("with a staging authority configured nothing offers a real certificate, and the form says what it signs", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  await mockStagingInventory(page)
  await mockStagingLineage(page, { testAuthority: true })
  await page.goto("/proxy/certificates")
  const lineage = page
    .getByRole("list", { name: "certbot lineages" })
    .getByRole("listitem")
    .filter({ hasText: "test.example.com" })
  await expect(lineage.getByText("test certificate")).toBeVisible()
  await expect(page.getByRole("button", { name: /real certificate/ })).toHaveCount(0)
  const card = page
    .getByRole("list", { name: "Installed certificates" })
    .getByRole("listitem")
    .filter({ hasText: "(STAGING) Riddling Rhubarb R12" })
  await expect(
    card.getByText("A staging authority signed it, so browsers refuse it.", { exact: true }),
  ).toBeVisible()

  await page.getByRole("button", { name: "Issue certificate", exact: true }).click()
  const dialog = page.getByRole("dialog")
  await expect(
    dialog.getByText(
      /Let's Encrypt's staging authority checks you control the domain, then signs a test certificate that browsers refuse/,
    ),
  ).toBeVisible()
  await expect(dialog.getByText(/The real limit is five failures an hour/)).toHaveCount(0)
  await dialog.getByRole("switch", { name: /Test run first/ }).click()
  await expect(dialog.getByText("This issues a test certificate")).toBeVisible()
  await expect(dialog.getByText("This counts against the rate limit")).toHaveCount(0)
})

test("lineages that cannot be read say so, and the renewal reading still stands", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  await page.route("**/api/v1/certificates/certbot", (route) =>
    json(
      route,
      certbotState({
        certs: [],
        autoRenew: true,
        renewSource: "certbot.timer",
        renewUnit: undefined,
        error:
          "certbot's renewal directory could not be read: open /etc/letsencrypt/renewal: permission denied",
      }),
    ),
  )
  await page.goto("/proxy/certificates")
  await expect(page.getByText("certbot's lineages could not be read")).toBeVisible()
  await expect(page.getByText(/permission denied/)).toBeVisible()
  await expect(page.getByText("certbot manages no certificates yet")).toHaveCount(0)
  await expect(page.getByText("Scheduled", { exact: true })).toBeVisible()
})

test("importing under a name in use asks to replace it, keeps the old pair, and offers the reload", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  const shop = {
    name: "shop",
    certPath: "/etc/ssl/just-dashboard/shop/fullchain.pem",
    keyPath: "/etc/ssl/just-dashboard/shop/privkey.pem",
    certificate: { ...certs[1], name: "shop", domains: ["shop.example.com"], daysLeft: 300 },
    chainComplete: true,
    chain: ["shop.example.com", "R11"],
    replaced: true,
    warnings: [],
  }
  await page.route("**/api/v1/certificates/import/inspect", (route) =>
    json(route, {
      ...shop,
      suggestedName: "shop.example.com",
      existing: { ...certs[1], domains: ["shop.example.com"], notAfter: "2027-01-01T12:00:00Z" },
      usedBy: ["shop.conf"],
    }),
  )
  const bodies: Array<Record<string, unknown>> = []
  await page.route("**/api/v1/certificates/import", async (route) => {
    bodies.push(route.request().postDataJSON())
    return json(route, { ...shop, usedBy: ["shop.conf"] })
  })
  const reloads = await capture(page, "**/api/v1/proxy/reload", () => ({ reloaded: true }))

  await page.goto("/proxy/certificates")
  await page.getByRole("button", { name: "Import", exact: true }).click()
  const dialog = page.getByRole("dialog")
  await dialog.getByLabel("Name").fill("shop")
  await dialog
    .getByLabel("Certificate")
    .fill("-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----")
  await dialog
    .getByLabel("Private key")
    .fill("-----BEGIN PRIVATE KEY-----\nMIIE\n-----END PRIVATE KEY-----")
  await dialog.getByRole("button", { name: "Inspect" }).click()

  await expect(dialog.getByText("shop is already imported")).toBeVisible()
  await expect(dialog.getByText(/Served by shop.conf/)).toBeVisible()
  const replace = dialog.getByRole("button", { name: "Replace it" })
  await expect(replace).toBeDisabled()
  await dialog.getByRole("checkbox").check()
  await replace.click()
  await expect(dialog.getByText("shop was replaced")).toBeVisible()
  expect(bodies.map((b) => b.replace)).toEqual([true])

  await expect(dialog.getByText("Sites pick it up on a reload")).toBeVisible()
  await dialog.getByRole("button", { name: "Reload nginx now" }).click()
  await expect(dialog.getByText(/shop.conf serve the new one now/)).toBeVisible()
  await expect(dialog.getByRole("button", { name: "Reload nginx" })).toHaveCount(0)
  expect(reloads).toEqual([{ kind: "nginx" }])
})

test("editing the name after an inspection asks to inspect again", async ({ page }) => {
  await mockProxy(page, { included: true })
  await page.route("**/api/v1/certificates/import/inspect", (route) =>
    json(route, {
      name: "shop",
      certPath: "/etc/ssl/just-dashboard/shop/fullchain.pem",
      keyPath: "/etc/ssl/just-dashboard/shop/privkey.pem",
      certificate: { ...certs[1], name: "shop", domains: ["shop.example.com"] },
      chainComplete: true,
      chain: ["shop.example.com"],
      replaced: true,
      warnings: [],
      suggestedName: "shop.example.com",
      usedBy: [],
    }),
  )
  await page.goto("/proxy/certificates")
  await page.getByRole("button", { name: "Import", exact: true }).click()
  const dialog = page.getByRole("dialog")
  await dialog.getByLabel("Name").fill("shop")
  await dialog.getByLabel("Certificate").fill("cert")
  await dialog.getByLabel("Private key").fill("key")
  await dialog.getByRole("button", { name: "Inspect" }).click()
  await expect(dialog.getByRole("button", { name: "Replace it" })).toBeVisible()
  await dialog.getByRole("button", { name: "Back" }).click()
  await dialog.getByLabel("Name").fill("shop-2")
  await expect(dialog.getByRole("button", { name: "Inspect" })).toBeVisible()
  await expect(dialog.getByText("shop is already imported")).toHaveCount(0)
})

test("the DNS issue form fits a phone with its wait field", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await mockShowcase(page)
  await page.goto("/proxy/certificates")
  await page.getByRole("button", { name: "Issue certificate", exact: true }).click()
  const dialog = page.getByRole("dialog")
  await dialog.getByLabel("Domains").fill("*.example.com")
  await dialog.getByRole("combobox", { name: "DNS provider" }).click()
  await page.getByRole("option", { name: "Cloudflare" }).click()
  await expect(dialog.getByLabel("Propagation wait")).toBeVisible()
  await expect(dialog).toBeInViewport({ ratio: 1 })
  expect(await dialog.evaluate((element) => element.scrollWidth <= element.clientWidth + 1)).toBe(
    true,
  )
})

/**
 * A Docker Caddy release keeps a copy of the certificate Caddy issued, and
 * Caddy renews the one it serves, never the copy. A deployment covered by a
 * copy says who renews it instead of counting down the copy's days, and it
 * offers no link to the Certificates page, which does not list the copy.
 */
const caddyRoute: DeploymentDomainRoute = {
  hostname: "api.example.test",
  https: true,
  ownership: "managed",
  route: "served",
  servedBy: "just-dashboard-env-12.conf",
  certificate: "valid",
  certificateName: "caddy-0da2f3126af1d760c968313b",
  certificateIssuer: "E6",
  certificateRenewedBy: "caddy",
  deepLink: "/proxy/sites?site=just-dashboard-env-12.conf",
}
const caddyOperations = {
  ...healthyOperations,
  domains: { ...healthyOperations.domains, domains: [caddyRoute] },
}

test("a deployment domain Caddy renews says so, with no days left and no certificate link", async ({
  page,
}) => {
  await mockProject(page, { operations: caddyOperations })
  await page.goto("/deploy/7/settings/domains")

  const form = page.getByRole("form", { name: "Domains" })
  await expect(form.getByText("Certificate valid", { exact: true })).toBeVisible()
  await expect(form.getByText("Renewed by Caddy", { exact: true })).toBeVisible()
  await expect(page.getByText(/\bdays? left$/)).toHaveCount(0)

  await page.getByRole("button", { name: "Actions for api.example.test" }).click()
  await expect(page.getByRole("menuitem", { name: "Open the serving site" })).toBeVisible()
  await expect(page.getByRole("menuitem", { name: "Open the certificate" })).toHaveCount(0)
})

test("the runtime page's domain row says Caddy renews it, and fits at every width", async ({
  page,
}) => {
  await mockProject(page, { operations: caddyOperations })
  await page.routeWebSocket(/\/api\/v1\/docker\/containers\/.*\/stats\/stream/, () => {})

  for (const width of [390, 1280, 1600]) {
    await page.setViewportSize({ width, height: 900 })
    await page.goto("/deploy/7/runtime")
    const domains = page.getByRole("list", { name: "Deployment domains" })
    const renewed = domains.getByText("Renewed by Caddy", { exact: true })
    await expect(renewed).toBeVisible()
    await expect(domains.getByText(/\bdays? left$/)).toHaveCount(0)
    // The reading sits in a fixed column on a wide row; it must not run out of it.
    expect(
      await renewed.evaluate((element) => {
        const reading = element.parentElement!.getBoundingClientRect()
        const column = element.parentElement!.parentElement!.getBoundingClientRect()
        return (
          element.getBoundingClientRect().right <= column.right + 1 &&
          reading.right <= column.right + 1
        )
      }),
    ).toBe(true)
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth),
    ).toBe(true)
  }
})

/** A host whose certbot.timer is active and whose last run failed on app.example.com. */
async function mockFailingRenewal(page: Page, overrides: Record<string, unknown> = {}) {
  await page.route("**/api/v1/certificates/certbot", (route) =>
    json(
      route,
      certbotState({
        autoRenew: true,
        renewSource: "certbot.timer",
        renewUnit: undefined,
        health: renewalHealth(),
        certs: [
          certbotLineage({
            lastFailure: { lineage: "app.example.com", reason: "Some challenges have failed." },
          }),
        ],
        ...overrides,
      }),
    ),
  )
}

test("an active timer whose last run failed reads failing, with certbot's reason on the certificate", async ({
  page,
}, testInfo) => {
  await mockProxy(page, { included: true })
  await mockFailingRenewal(page)
  await page.goto("/proxy/certificates")

  const tile = page.locator("[data-slot='stat-grid']")
  await expect(tile.getByText("Failing", { exact: true })).toBeVisible()
  await expect(tile.getByText(/^last run (yesterday )?\d\d:\d\d · 1 failed$/)).toBeVisible()
  await expect(tile.getByText("Scheduled", { exact: true })).toHaveCount(0)

  const notice = page.getByText("The last renewal failed").locator("..")
  await expect(notice).toContainText(/certbot\.service exited 1 (yesterday )?at \d\d:\d\d\./)
  await expect(notice).toContainText("app.example.com: Some challenges have failed.")

  const lineages = page.getByRole("list", { name: "certbot lineages" })
  await expect(
    lineages.getByText("Last renewal failed: Some challenges have failed."),
  ).toBeVisible()
  // How to check a fix comes out of the menu.
  await expect(lineages.getByRole("button", { name: "Dry run", exact: true })).toBeVisible()
  await expect(lineages.getByText("Renews with")).toBeVisible()

  await page.screenshot({ path: testInfo.outputPath("renewal-failing.png") })
  await lineages.getByRole("button", { name: "Show log", exact: true }).click()
  const sheet = page.getByRole("dialog")
  await expect(sheet.getByText("Renewal log")).toBeVisible()
  await expect(sheet.getByRole("region")).toHaveCount(2)
  const newest = sheet.getByRole("region").first()
  await expect(
    newest.getByText(
      "Failed to renew certificate app.example.com with error: Some challenges have failed.",
    ),
  ).toHaveClass(/text-destructive/)
  await expect(newest.getByText("Starting certbot.service - Certbot...")).toHaveClass(
    /text-muted-foreground/,
  )
  await expect(newest.getByText("failed", { exact: true })).toBeVisible()
})

test("a failure renewed since reads recovered, and a passing run healthy until the next", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  await mockFailingRenewal(page, {
    health: renewalHealth({
      state: "recovered",
      failures: [
        { lineage: "app.example.com", reason: "Some challenges have failed.", renewedSince: true },
      ],
    }),
    certs: [certbotLineage()],
  })
  await page.goto("/proxy/certificates")
  const tiles = page.locator("[data-slot='stat-grid']")
  await expect(tiles.getByText("Recovered", { exact: true })).toBeVisible()
  await expect(page.getByText("The last renewal failed")).toHaveCount(0)
  await expect(page.getByText("renewed since", { exact: true })).toBeVisible()
  await expect(page.getByText("Last renewal failed:")).toHaveCount(0)

  await mockFailingRenewal(page, {
    health: renewalHealth({ state: "ok", exitStatus: 0, failures: [] }),
    certs: [certbotLineage()],
  })
  await page.reload()
  await expect(tiles.getByText("Healthy", { exact: true })).toBeVisible()
  await expect(tiles.getByText(/^next run (tomorrow )?\d\d:\d\d$/)).toBeVisible()
  await expect(page.getByText(/^Last run (yesterday )?\d\d:\d\d passed$/)).toBeVisible()
})

test("Run now starts the timer's service, holds every certbot verb while it runs, and reads the record again after", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  await mockFailingRenewal(page)
  const stream = await holdJobStreams(page)
  const running = certbotJob({
    kind: "certbot.renewal",
    title: "Running certbot.service",
    target: "certbot.service",
  })
  const runs = await capture(page, "**/api/v1/certificates/renewal/run", () => running)
  let reads = 0
  page.on("request", (request) => {
    if (request.url().endsWith("/api/v1/certificates/certbot")) reads++
  })
  await page.goto("/proxy/certificates")
  await expect(page.getByText("The last renewal failed")).toBeVisible()
  const before = reads

  await page.getByRole("button", { name: "Run now", exact: true }).click()
  expect(runs).toHaveLength(1)
  await expect(page.getByText("Running…", { exact: true })).toBeVisible()
  await expect(page.getByRole("button", { name: "Run now", exact: true })).toBeDisabled()
  const lineages = page.getByRole("list", { name: "certbot lineages" })
  await expect(lineages.getByRole("button", { name: "Renew", exact: true })).toBeDisabled()
  await expect(page.getByRole("button", { name: "Renew all due" })).toBeDisabled()

  // It failed again: the record is read again all the same.
  stream.finish({
    ...running,
    status: "failed",
    error: "certbot.service failed to renew app.example.com: Some challenges have failed.",
    endedAt: now,
  })
  await expect(page.getByRole("button", { name: "Run now", exact: true })).toBeEnabled()
  await expect.poll(() => reads).toBeGreaterThan(before)
})

test("the reload switch installs the hook, and removing it asks first", async ({ page }) => {
  await mockProxy(page, { included: true })
  let hook = reloadHook("missing")
  await page.route("**/api/v1/certificates/certbot", (route) =>
    json(
      route,
      certbotState({
        autoRenew: true,
        renewSource: "certbot.timer",
        renewUnit: undefined,
        health: renewalHealth({ state: "ok", exitStatus: 0, failures: [] }),
        certs: [
          certbotLineage({
            authenticator: "webroot",
            installer: undefined,
            webroots: ["/var/www/app"],
          }),
        ],
        reloadHook: hook,
      }),
    ),
  )
  const calls: string[] = []
  await page.route("**/api/v1/certificates/renewal-hook", async (route) => {
    calls.push(route.request().method())
    hook = reloadHook(route.request().method() === "DELETE" ? "missing" : "installed")
    await json(route, hook)
  })
  await page.goto("/proxy/certificates")

  const lineages = page.getByRole("list", { name: "certbot lineages" })
  await expect(lineages.getByText("/var/www/app")).toBeVisible()
  const option = page.getByRole("switch", { name: "Reload nginx after every renewal" })
  await expect(option).not.toBeChecked()
  await expect(
    page.getByText(
      "certbot reloads nginx itself only for certificates issued through nginx. 1 of 1 here is not.",
    ),
  ).toBeVisible()
  await option.click()
  await expect(page.getByText("nginx reloads after every renewal")).toBeVisible()
  await expect(option).toBeChecked()
  await expect(
    page.getByText(/certbot runs 50-just-dashboard-reload-nginx after each renewal/),
  ).toBeVisible()

  await option.click()
  const dialog = page.getByRole("dialog")
  await expect(dialog.getByText("Stop reloading nginx after renewals")).toBeVisible()
  await dialog.getByRole("button", { name: "Remove the hook" }).click()
  await expect(option).not.toBeChecked()
  expect(calls).toEqual(["PUT", "DELETE"])
})

test("a hook changed by hand offers its restore, and somebody else's file is left alone", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  let state = reloadHook("modified", ["reload-haproxy"])
  await page.route("**/api/v1/certificates/certbot", (route) =>
    json(route, certbotState({ reloadHook: state })),
  )
  const calls = await capture(page, "**/api/v1/certificates/renewal-hook", () => {
    state = reloadHook("installed", ["reload-haproxy"])
    return state
  })
  await page.goto("/proxy/certificates")
  await expect(page.getByText(/was changed by hand, or is no longer executable/)).toBeVisible()
  await expect(page.getByText(/certbot also runs reload-haproxy after each renewal/)).toBeVisible()
  await page.getByRole("button", { name: "Restore the hook" }).click()
  await expect(page.getByText("The hook is restored")).toBeVisible()
  expect(calls).toHaveLength(1)
  await expect(page.getByRole("button", { name: "Restore the hook" })).toHaveCount(0)

  state = reloadHook("foreign")
  await page.reload()
  await expect(
    page.getByRole("switch", { name: "Reload nginx after every renewal" }),
  ).toBeDisabled()
  await expect(page.getByText(/is not this dashboard's file, so it is left as it is/)).toBeVisible()
})

test("a lineage that will fail says why before the run does, and the overview raises it", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  const reason =
    "certbot renews it with the dns-cloudflare plugin, which the host's, certbot 2.11.0 does not have."
  await page.route("**/api/v1/certificates/certbot", (route) =>
    json(
      route,
      certbotState({
        autoRenew: true,
        renewSource: "certbot.timer",
        renewUnit: undefined,
        health: renewalHealth({ state: "ok", exitStatus: 0, failures: [] }),
        certs: [
          certbotLineage({
            authenticator: "dns-cloudflare",
            installer: undefined,
            dnsProvider: "Cloudflare",
            willFail: [reason],
          }),
        ],
      }),
    ),
  )
  await page.goto("/proxy/certificates")
  const lineages = page.getByRole("list", { name: "certbot lineages" })
  await expect(lineages.getByText("The next renewal will fail")).toBeVisible()
  await expect(lineages.getByText(reason)).toBeVisible()
  await expect(lineages.getByText("Cloudflare", { exact: true })).toBeVisible()
  await expect(lineages.getByRole("button", { name: "Dry run", exact: true })).toBeVisible()

  await page.goto("/proxy")
  const finding = page.getByRole("button", { name: /^app\.example\.com will fail to renew/ })
  await expect(finding).toBeVisible()
  await finding.click()
  await expect(page.getByText(reason)).toBeVisible()
})

test("the overview raises a failing renewal, and renewals no reload reaches", async ({ page }) => {
  await mockProxy(page, { included: true })
  await mockFailingRenewal(page, {
    certs: [
      certbotLineage({
        authenticator: "webroot",
        installer: undefined,
        servedBy: ["app.example.com"],
        lastFailure: { lineage: "app.example.com", reason: "Some challenges have failed." },
      }),
    ],
  })
  await page.goto("/proxy")
  const failing = page.getByRole("button", { name: /^certbot's last renewal failed/ })
  await expect(failing).toBeVisible()
  await failing.click()
  await expect(
    page.getByText(
      /certbot\.service failed (yesterday )?at \d\d:\d\d\. app\.example\.com: Some challenges have failed\./,
    ),
  ).toBeVisible()
  const unreloaded = page.getByRole("button", {
    name: /^Renewed certificates will not reach nginx/,
  })
  await expect(unreloaded).toBeVisible()
  await unreloaded.click()
  await expect(
    page.getByText(
      "certbot renews app.example.com without reloading nginx, so app.example.com keeps serving the old certificate until it expires.",
    ),
  ).toBeVisible()
})

test("a read-only account reads the failure, with nothing to press", async ({ page }) => {
  await mockProxy(page, { included: true })
  await mockFailingRenewal(page)
  await page.route("**/api/v1/auth/session", (route) =>
    json(route, { ...user, capabilities: ["read"], user: { ...user.user, role: "viewer" } }),
  )
  await page.goto("/proxy/certificates")
  await expect(page.getByText("The last renewal failed")).toBeVisible()
  await expect(page.getByText("Last renewal failed: Some challenges have failed.")).toBeVisible()
  for (const name of ["Run now", "Show log", "Dry run", "Renew"]) {
    await expect(page.getByRole("button", { name, exact: true })).toHaveCount(0)
  }
  await expect(page.getByRole("switch", { name: "Reload nginx after every renewal" })).toHaveCount(
    0,
  )
})

test("the failing renewal, its lineage and the switch fit a phone", async ({ page }, testInfo) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await mockProxy(page, { included: true })
  await mockFailingRenewal(page, {
    certs: [
      certbotLineage({
        authenticator: "webroot",
        installer: undefined,
        webroots: ["/var/www/static", "/srv/very/long/path/to/the/public/folder/of/an/application"],
        lastFailure: {
          lineage: "app.example.com",
          reason:
            "The manual plugin is not working; there may be problems with your existing configuration. The error was: PluginError('An authentication script must be provided with --manual-auth-hook when using the manual plugin non-interactively.')",
        },
        willFail: [
          "It was issued by hand with the manual plugin, which renews unattended only with an auth hook, and it has none.",
        ],
      }),
    ],
  })
  await page.goto("/proxy/certificates")
  await page.getByText("The last renewal failed").scrollIntoViewIfNeeded()
  await expect(page.getByText("The last renewal failed")).toBeInViewport()
  await page.screenshot({ path: testInfo.outputPath("renewal-record-phone.png") })
  await expect(page.getByRole("switch", { name: "Reload nginx after every renewal" })).toBeVisible()
  const overflow = await page
    .locator("[data-slot='page']")
    .evaluate((el) => el.scrollWidth - el.clientWidth)
  expect(overflow).toBeLessThanOrEqual(1)
  const lineages = page.getByRole("list", { name: "certbot lineages" })
  // Each webroot folder wraps inside the lineage's row: the page not
  // scrolling sideways says nothing when an ancestor clips what overflows.
  const folders = lineages.locator("code")
  await expect(folders).toHaveCount(2)
  await expect(folders.last()).toHaveText(
    "/srv/very/long/path/to/the/public/folder/of/an/application",
  )
  for (const folder of await folders.all()) {
    expect(
      await folder.evaluate((el) => {
        const row = el.closest("li")!.getBoundingClientRect()
        const box = el.getBoundingClientRect()
        return {
          overflow: el.scrollWidth - el.clientWidth,
          inside: box.left >= row.left - 1 && box.right <= row.right + 1,
        }
      }),
    ).toEqual({ overflow: 0, inside: true })
  }
  const showLog = lineages.getByRole("button", { name: "Show log", exact: true })
  await showLog.scrollIntoViewIfNeeded()
  await expect(showLog).toBeInViewport({ ratio: 1 })
  await page.screenshot({ path: testInfo.outputPath("renewal-phone.png"), fullPage: true })
  await showLog.click()
  const sheet = page.getByRole("dialog")
  await expect(sheet).toBeInViewport({ ratio: 1 })
  const sheetOverflow = await sheet.evaluate((el) => el.scrollWidth - el.clientWidth)
  expect(sheetOverflow).toBeLessThanOrEqual(1)
  await page.screenshot({ path: testInfo.outputPath("renewal-log-phone.png") })
})

test("a run that passed while its reload hook failed reads Hook failed, in the hook's own words, and the overview raises it", async ({
  page,
}, testInfo) => {
  await mockProxy(page, { included: true })
  const output =
    "nginx -t failed, so nginx was not reloaded for /etc/letsencrypt/live/app.example.com"
  await mockFailingRenewal(page, {
    health: renewalHealth({
      state: "ok",
      exitStatus: 0,
      failures: [],
      hookFailures: [
        {
          kind: "deploy-hook",
          command: "/etc/letsencrypt/renewal-hooks/deploy/50-just-dashboard-reload-nginx",
          code: 1,
          output,
        },
      ],
    }),
    certs: [certbotLineage({ servedBy: ["app.example.com"] })],
    reloadHook: reloadHook("installed"),
  })
  await page.goto("/proxy/certificates")
  const tiles = page.locator("[data-slot='stat-grid']")
  await expect(tiles.getByText("Hook failed", { exact: true })).toBeVisible()
  await expect(tiles.getByText("Healthy", { exact: true })).toHaveCount(0)
  const notice = page.getByText("A renewal hook failed").locator("..")
  await expect(notice).toContainText(
    /certbot\.service passed (yesterday )?at \d\d:\d\d, but certbot only warns when a hook it runs fails\./,
  )
  await expect(page.getByRole("list", { name: "Failed hooks" })).toHaveText(
    `deploy-hook 50-just-dashboard-reload-nginx exited 1: ${output}`,
  )
  await expect(page.getByText(/^Last run .* passed$/)).toHaveCount(0)
  await expect(page.getByRole("button", { name: "Run now", exact: true })).toBeVisible()
  await page.screenshot({ path: testInfo.outputPath("renewal-hook-failed.png") })

  // The hook's words are a long line of paths: on a phone they wrap.
  await page.setViewportSize({ width: 390, height: 844 })
  const failed = page.getByRole("list", { name: "Failed hooks" })
  await failed.scrollIntoViewIfNeeded()
  await expect(failed).toBeInViewport({ ratio: 1 })
  expect(await failed.evaluate((el) => el.scrollWidth - el.clientWidth)).toBeLessThanOrEqual(1)
  const overflow = await page
    .locator("[data-slot='page']")
    .evaluate((el) => el.scrollWidth - el.clientWidth)
  expect(overflow).toBeLessThanOrEqual(1)
  await page.screenshot({ path: testInfo.outputPath("renewal-hook-failed-phone.png") })
  await page.setViewportSize({ width: 1280, height: 900 })

  await page.goto("/proxy")
  const finding = page.getByRole("button", { name: /^A renewal hook failed/ })
  await expect(finding).toBeVisible()
  await finding.click()
  await expect(
    page.getByText(`deploy-hook 50-just-dashboard-reload-nginx exited 1: ${output}`, {
      exact: false,
    }),
  ).toBeVisible()
})

test("recent renewal runs wrap in the renewal column, each told apart by its time, and Renew all due stays inside it", async ({
  page,
}, testInfo) => {
  await page.setViewportSize({ width: 1280, height: 900 })
  await mockProxy(page, { included: true })
  await mockFailingRenewal(page)
  const run = (hoursAgo: number) =>
    certbotJob({
      id: `run-${hoursAgo}`,
      kind: "certbot.renewal",
      title: "Running certbot.service",
      target: "certbot.service",
      status: "failed",
      startedAt: new Date(Date.now() - hoursAgo * 3_600_000).toISOString(),
    })
  await page.route("**/api/v1/jobs/", (route) => json(route, [run(1), run(2), run(3), run(4)]))
  await page.goto("/proxy/certificates")

  const chips = page.getByRole("button", { name: /^certbot\.service \d\d:\d\d$/ })
  await expect(chips).toHaveCount(4)
  const labels = await chips.allTextContents()
  expect(new Set(labels).size).toBe(4)
  const renewAll = page.getByRole("button", { name: "Renew all due" })
  await expect(renewAll).toBeVisible()
  const panel = renewAll.locator("xpath=ancestor::*[@data-slot='panel'][1]")
  const right = (await panel.boundingBox())!.x + (await panel.boundingBox())!.width
  for (const element of [renewAll, ...(await chips.all())]) {
    const box = (await element.boundingBox())!
    expect(box.x + box.width).toBeLessThanOrEqual(right + 1)
  }
  const overflow = await page
    .locator("[data-slot='page']")
    .evaluate((el) => el.scrollWidth - el.clientWidth)
  expect(overflow).toBeLessThanOrEqual(1)
  await page.screenshot({ path: testInfo.outputPath("renewal-recent-1280.png") })
})

test("a failed renewal says where validation failed and whose it is to fix", async ({ page }) => {
  await mockProxy(page, { included: true })
  await mockFailingRenewal(page, {
    health: renewalHealth({
      problems: [
        {
          stage: "connect",
          stageTitle: "Reaching port 80",
          owner: "A firewall in front of port 80: this host's or the provider's",
          domain: "app.example.com",
          address: "203.0.113.5",
          here: true,
          detail:
            "203.0.113.5: Fetching http://app.example.com/.well-known/acme-challenge/x1: Timeout during connect (likely firewall problem)",
          action:
            "The authority's connection went unanswered: open port 80 to the internet in the host firewall and any provider firewall or security group.",
          links: [
            { label: "The host firewall", href: "/network/firewall" },
            { label: "Check from outside", href: "/network/external" },
          ],
        },
        {
          stage: "dns",
          stageTitle: "Name resolution",
          owner: "The DNS provider or registrar",
          domain: "www.example.com",
          detail: "DNS problem: NXDOMAIN looking up A for www.example.com",
          action:
            "Create an A or AAAA record for the name pointing at this server, then try again.",
          links: [
            { label: "Look the name up", href: "/network/tools?tool=dns&target=www.example.com" },
          ],
        },
      ],
    }),
  })
  await page.goto("/proxy/certificates")
  const where = page.getByRole("region", { name: "Where it failed" })
  await expect(where).toBeVisible()
  const connect = where.getByRole("listitem").filter({ hasText: "app.example.com" })
  await expect(connect).toContainText("Reaching port 80 — A firewall in front of port 80")
  await expect(connect).toContainText("203.0.113.5 (this host): Fetching http://app.example.com/")
  await expect(connect).toContainText("Timeout during connect (likely firewall problem)")
  await expect(connect.getByRole("link", { name: "The host firewall" })).toHaveAttribute(
    "href",
    "/network/firewall",
  )
  const dns = where.getByRole("listitem").filter({ hasText: "www.example.com" })
  await expect(dns).toContainText("Name resolution — The DNS provider or registrar")
  await expect(dns.getByRole("link", { name: "Look the name up" })).toHaveAttribute(
    "href",
    "/network/tools?tool=dns&target=www.example.com",
  )
})
