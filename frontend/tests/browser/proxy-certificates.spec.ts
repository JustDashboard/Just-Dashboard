import { expect, test, type Page, type WebSocketRoute } from "@playwright/test"
import { certs, inThirtyDays, json, mockProxy, mockShowcase, now } from "./proxy-fixtures"
import { certbotJob, certbotState } from "./fixtures/proxy/certs"
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
  await expect(page.getByText("certbot is running. Its other actions wait")).toBeVisible()
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
  await expect(page.getByText("certbot is running. Its other actions wait")).toHaveCount(0)
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
  await dialog.getByRole("combobox").click()
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
async function mockStagingLineage(page: Page) {
  await page.route("**/api/v1/certificates/certbot", (route) =>
    json(
      route,
      certbotState({
        certs: [
          {
            name: "test.example.com",
            domains: ["test.example.com", "www.test.example.com"],
            expiry: inThirtyDays,
            daysLeft: 80,
            valid: true,
            staging: true,
          },
        ],
      }),
    ),
  )
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
      title: "Issuing a certificate for test.example.com",
      target: "test.example.com www.test.example.com",
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

  await row.getByRole("button", { name: "Issue real certificate" }).click()
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
})

test("a test certificate's lineage fits a phone with its verb", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await mockProxy(page, { included: true })
  await mockStagingLineage(page)
  await page.goto("/proxy/certificates")
  const verb = page.getByRole("button", { name: "Issue real certificate" })
  await verb.scrollIntoViewIfNeeded()
  await expect(verb).toBeInViewport({ ratio: 1 })
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= document.documentElement.clientWidth + 1,
    ),
  ).toBe(true)
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
  const bodies: Array<Record<string, unknown>> = []
  await page.route("**/api/v1/certificates/import", async (route) => {
    const body = route.request().postDataJSON()
    bodies.push(body)
    if (!body.replace) {
      return route.fulfill({
        status: 409,
        contentType: "application/json",
        body: JSON.stringify({
          error: {
            code: "certificate_exists",
            message:
              "shop is already imported: it covers shop.example.com and expires 1 Jan 2027. Replace it to overwrite that pair.",
          },
        }),
      })
    }
    return json(route, {
      name: "shop",
      certPath: "/etc/ssl/just-dashboard/shop/fullchain.pem",
      keyPath: "/etc/ssl/just-dashboard/shop/privkey.pem",
      certificate: { ...certs[1], name: "shop", domains: ["shop.example.com"], daysLeft: 300 },
      chainComplete: true,
      replaced: true,
      warnings: [],
    })
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
  await dialog.getByRole("button", { name: "Check and import" }).click()

  await expect(dialog.getByText("That name is taken")).toBeVisible()
  await expect(dialog.getByText(/expires 1 Jan 2027/)).toBeVisible()
  await dialog.getByRole("button", { name: "Replace it" }).click()
  await expect(dialog.getByText("shop was replaced")).toBeVisible()
  expect(bodies.map((b) => b.replace)).toEqual([false, true])

  await expect(dialog.getByText("Sites pick it up on a reload")).toBeVisible()
  await dialog.getByRole("button", { name: "Reload nginx" }).click()
  await expect(
    dialog.getByText("Sites using this certificate serve the new one now."),
  ).toBeVisible()
  await expect(dialog.getByRole("button", { name: "Reload nginx" })).toHaveCount(0)
  expect(reloads).toEqual([{ kind: "nginx" }])
})

test("editing the name after a refusal asks for a plain import again", async ({ page }) => {
  await mockProxy(page, { included: true })
  await page.route("**/api/v1/certificates/import", (route) =>
    route.fulfill({
      status: 409,
      contentType: "application/json",
      body: JSON.stringify({
        error: { code: "certificate_exists", message: "shop is already imported." },
      }),
    }),
  )
  await page.goto("/proxy/certificates")
  await page.getByRole("button", { name: "Import", exact: true }).click()
  const dialog = page.getByRole("dialog")
  await dialog.getByLabel("Name").fill("shop")
  await dialog.getByLabel("Certificate").fill("cert")
  await dialog.getByLabel("Private key").fill("key")
  await dialog.getByRole("button", { name: "Check and import" }).click()
  await expect(dialog.getByRole("button", { name: "Replace it" })).toBeVisible()
  await dialog.getByLabel("Name").fill("shop-2")
  await expect(dialog.getByRole("button", { name: "Check and import" })).toBeVisible()
  await expect(dialog.getByText("That name is taken")).toHaveCount(0)
})

test("the DNS issue form fits a phone with its wait field", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await mockShowcase(page)
  await page.goto("/proxy/certificates")
  await page.getByRole("button", { name: "Issue certificate", exact: true }).click()
  const dialog = page.getByRole("dialog")
  await dialog.getByLabel("Domains").fill("*.example.com")
  await dialog.getByRole("combobox").click()
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
  // The figure over the list names the domain rather than "No certificate".
  await expect(page.getByText("api.example.test · renewed by Caddy", { exact: true })).toBeVisible()
  await expect(page.getByText("No certificate observed yet", { exact: true })).toHaveCount(0)

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
