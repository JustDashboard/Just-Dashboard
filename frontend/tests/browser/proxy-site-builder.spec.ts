import { expect, test, type Page, type Route } from "@playwright/test"
import { siteResult } from "./fixtures/proxy/siteform"
import { json, mockProxy, vhosts } from "./proxy-fixtures"

/**
 * The site form, checked for what it sends and what it then says.
 *
 * Each of these was a way the form told an operator something that was not
 * true: a site typed as app.example.com saved as "a", a disabled site
 * switched back on by an edit, a site nginx ignores reported as live, and a
 * saved site whose reload failed reported as not applied.
 */

/** Every body the form posts to one endpoint, answered by `answer`. */
async function capture(
  page: Page,
  url: string,
  answer: (route: Route, body: Record<string, unknown>, index: number) => Promise<void> | void,
) {
  const bodies: Record<string, unknown>[] = []
  await page.route(url, async (route) => {
    const body = route.request().postDataJSON()
    bodies.push(body)
    await answer(route, body, bodies.length - 1)
  })
  return bodies
}

async function openNewSite(page: Page) {
  await page.goto("/proxy/sites")
  await page.getByRole("button", { name: "New site", exact: true }).click()
  return page.getByRole("dialog", { name: "New site" })
}

test("a new site is named after the domain typed, not after its first letter", async ({ page }) => {
  await mockProxy(page, { included: true })
  const saved = await capture(page, "**/api/v1/proxy/sites/", (route, body) =>
    json(route, siteResult({ name: (body.spec as { name: string }).name })),
  )
  const sheet = await openNewSite(page)
  // One key at a time, as a person types: fill() sends one change and never
  // saw the name freeze on the first character.
  await sheet.getByLabel("Domains").pressSequentially("app.example.com www.app.example.com")
  await sheet.getByRole("switch", { name: "Serve over HTTPS" }).click()
  await expect(sheet.getByText("Saved as app.example.com in nginx's site directory.")).toBeVisible()
  await expect(sheet.getByLabel("Certificate", { exact: true })).toHaveValue(
    "/etc/letsencrypt/live/app.example.com/fullchain.pem",
  )

  await sheet.getByRole("button", { name: "Save and reload" }).click()
  await expect(page.getByText("app.example.com is live")).toBeVisible()
  expect(saved).toHaveLength(1)
  expect(saved[0]).toMatchObject({
    enable: "enable",
    overwrite: false,
    reload: true,
    spec: {
      name: "app.example.com",
      domains: ["app.example.com", "www.app.example.com"],
      certPath: "/etc/letsencrypt/live/app.example.com/fullchain.pem",
      keyPath: "/etc/letsencrypt/live/app.example.com/privkey.pem",
    },
  })
})

test("a certificate path typed by hand stays while the domains change", async ({ page }) => {
  await mockProxy(page, { included: true })
  const sheet = await openNewSite(page)
  const domains = sheet.getByLabel("Domains")
  await domains.pressSequentially("*.example.com")
  await sheet.getByRole("switch", { name: "Serve over HTTPS" }).click()
  // A wildcard is certified for its parent zone.
  await expect(sheet.getByLabel("Private key", { exact: true })).toHaveValue(
    "/etc/letsencrypt/live/example.com/privkey.pem",
  )
  await sheet.getByLabel("Certificate", { exact: true }).fill("/etc/ssl/wildcard.pem")
  await domains.fill("")
  await domains.pressSequentially("shop.example.com")
  await expect(sheet.getByLabel("Certificate", { exact: true })).toHaveValue(
    "/etc/ssl/wildcard.pem",
  )
  await expect(sheet.getByLabel("Private key", { exact: true })).toHaveValue(
    "/etc/letsencrypt/live/shop.example.com/privkey.pem",
  )
  await expect(
    sheet.getByText("Saved as shop.example.com in nginx's site directory."),
  ).toBeVisible()
})

test("HSTS reaches the server only with TLS", async ({ page }) => {
  await mockProxy(page, { included: true })
  const previews = await capture(page, "**/api/v1/proxy/sites/preview", (route, body) =>
    json(route, { content: `# ${(body.spec as { name: string }).name}\n`, warnings: [] }),
  )
  const sheet = await openNewSite(page)
  await sheet.getByLabel("Domains").fill("plain.example.com")
  await expect.poll(() => previews.length).toBeGreaterThan(0)
  expect(previews.at(-1)).toMatchObject({ spec: { tls: false, hsts: false } })

  const before = previews.length
  await sheet.getByRole("switch", { name: "Serve over HTTPS" }).click()
  await expect.poll(() => previews.length).toBeGreaterThan(before)
  expect(previews.at(-1)).toMatchObject({ spec: { tls: true, hsts: true } })
  await expect(sheet.getByRole("switch", { name: "HSTS" })).toBeChecked()
})

test("editing a disabled site keeps it disabled and says so", async ({ page }) => {
  await mockProxy(page, { included: true })
  await page.route("**/api/v1/proxy/vhosts", (route) =>
    json(
      route,
      vhosts.map((v) => (v.name === "legacy.example.com" ? { ...v, enabled: false } : v)),
    ),
  )
  const saved = await capture(page, "**/api/v1/proxy/sites/", (route) =>
    json(
      route,
      siteResult({
        name: "legacy.example.com",
        path: "/etc/nginx/sites-available/legacy.example.com",
        enabled: false,
        reloaded: true,
      }),
    ),
  )
  await page.goto("/proxy/sites")
  await page.getByRole("button", { name: "Open legacy.example.com" }).click()
  const sheet = page.getByRole("dialog", { name: "Edit legacy.example.com" })
  await expect(sheet.getByLabel("Domains")).toHaveValue("legacy.example.com")
  // A path written with root says where its files really come from.
  await expect(sheet.getByText("/srv/legacy/static")).toBeVisible()

  await sheet.getByRole("button", { name: "Save and reload" }).click()
  await expect(page.getByText("legacy.example.com saved", { exact: true })).toBeVisible()
  await expect(page.getByText(/disabled and stays that way/)).toBeVisible()
  expect(saved[0]).toMatchObject({ enable: "keep", overwrite: true })
  expect((saved[0].spec as { locations: unknown[] }).locations).toEqual([
    { path: "/static", root: "/srv/legacy", rootMode: "root", webSockets: false },
  ])
})

test("a reload that fails after a clean test is reported as saved", async ({ page }) => {
  await mockProxy(page, { included: true })
  // What nginx says when it is not running: the most common reload failure,
  // and one where nothing at all is being served.
  const reloadError = 'nginx: [error] open() "/run/nginx.pid" failed (2: No such file or directory)'
  await capture(page, "**/api/v1/proxy/sites/", (route) =>
    json(route, siteResult({ reloaded: false, reloadError })),
  )
  await page.goto("/proxy/sites")
  await page.getByRole("button", { name: "Open app.example.com" }).click()
  const sheet = page.getByRole("dialog", { name: "Edit app.example.com" })
  await expect(sheet.getByLabel("Domains")).toHaveValue("app.example.com")
  await sheet.getByRole("button", { name: "Save and reload" }).click()
  await expect(page.getByText("app.example.com saved and tested; reload failed")).toBeVisible()
  await expect(
    page.getByText(`nginx did not pick it up; start or reload nginx to apply it. ${reloadError}`),
  ).toBeVisible()
  await expect(page.getByText(/still serving/)).toHaveCount(0)
  await expect(page.getByText("Not applied")).toHaveCount(0)
  await expect(sheet).toBeHidden()
})

test("a name another site serves is refused until the operator saves anyway", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await mockProxy(page, { included: true })
  // legacy.example.com sorts first in sites-enabled, so nginx keeps
  // answering the name from it and ignores the new site's claim.
  const refusal =
    "nginx answers legacy.example.com on 0.0.0.0:80 from legacy.example.com, which it reads first, and ignores this site's claim."
  const saved = await capture(page, "**/api/v1/proxy/sites/", (route, body, index) =>
    index === 0
      ? route.fulfill({
          status: 409,
          contentType: "application/json",
          body: JSON.stringify({ error: { code: "name_conflict", message: refusal } }),
        })
      : json(
          route,
          siteResult({
            name: "www.legacy.example.com",
            conflicts: [
              {
                domain: "legacy.example.com",
                listen: "0.0.0.0:80",
                site: "legacy.example.com",
                effect: "ignored",
              },
            ],
            reloaded: body.reload,
          }),
        ),
  )
  const sheet = await openNewSite(page)
  await sheet.getByLabel("Domains").fill("www.legacy.example.com legacy.example.com")
  await sheet.getByRole("button", { name: "Save and reload" }).click()

  // Announced, and focus moves onto it from the footer button that was
  // pressed, so the next Tab is the question it asks.
  const alert = sheet.getByRole("alert")
  await expect(alert).toContainText(refusal)
  await expect(alert).toBeFocused()
  await expect(sheet).toBeVisible()
  expect(await sheet.evaluate((element) => element.scrollWidth <= element.clientWidth + 1)).toBe(
    true,
  )
  await page.keyboard.press("Tab")
  await expect(sheet.getByRole("button", { name: "Save anyway" })).toBeFocused()
  await page.keyboard.press("Enter")
  await expect(page.getByText("www.legacy.example.com is live with a name conflict")).toBeVisible()
  await expect(
    page.getByText(
      "nginx answers legacy.example.com on 0.0.0.0:80 from legacy.example.com, not www.legacy.example.com.",
    ),
  ).toBeVisible()
  expect(saved).toHaveLength(2)
  expect(saved[0]).not.toHaveProperty("allowConflict")
  expect(saved[1]).toMatchObject({ allowConflict: true, reload: true, enable: "enable" })
})

test("changing the domains puts a refused save's question away", async ({ page }) => {
  await mockProxy(page, { included: true })
  await capture(page, "**/api/v1/proxy/sites/", (route) =>
    route.fulfill({
      status: 409,
      contentType: "application/json",
      body: JSON.stringify({
        error: {
          code: "name_conflict",
          message:
            "Saving anyway takes app.example.com on 0.0.0.0:80 from legacy, since nginx reads this site first.",
        },
      }),
    }),
  )
  const sheet = await openNewSite(page)
  await sheet.getByLabel("Domains").fill("app.example.com")
  await sheet.getByRole("button", { name: "Save and reload" }).click()
  await expect(sheet.getByRole("button", { name: "Save anyway" })).toBeVisible()
  await sheet.getByLabel("Domains").fill("new.example.com")
  await expect(sheet.getByRole("button", { name: "Save anyway" })).toHaveCount(0)
})
