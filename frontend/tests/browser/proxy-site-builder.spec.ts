import { expect, test, type Page, type Route } from "@playwright/test"
import { runningContainers, siteResult } from "./fixtures/proxy/siteform"
import { json, mockProxy, ports, user, vhosts } from "./proxy-fixtures"

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
  await sheet.getByLabel("Domains").pressSequentially("shop.example.com www.shop.example.com")
  await sheet.getByRole("switch", { name: "Serve over HTTPS" }).click()
  await expect(sheet.getByLabel("File name")).toHaveValue("shop.example.com")
  await expect(
    sheet.getByText("Saved as /etc/nginx/sites-available/shop.example.com"),
  ).toBeVisible()
  await expect(sheet.getByLabel("Certificate", { exact: true })).toHaveValue(
    "/etc/letsencrypt/live/shop.example.com/fullchain.pem",
  )

  await sheet.getByRole("button", { name: "Save and reload" }).click()
  await expect(page.getByText("shop.example.com is live")).toBeVisible()
  expect(saved).toHaveLength(1)
  expect(saved[0]).toMatchObject({
    enable: "enable",
    overwrite: false,
    reload: true,
    spec: {
      name: "shop.example.com",
      domains: ["shop.example.com", "www.shop.example.com"],
      certPath: "/etc/letsencrypt/live/shop.example.com/fullchain.pem",
      keyPath: "/etc/letsencrypt/live/shop.example.com/privkey.pem",
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
  await expect(sheet.getByLabel("File name")).toHaveValue("shop.example.com")
  // And the way back to the domain's own certificate.
  await sheet.getByRole("button", { name: "Use the domain’s certificate" }).click()
  await expect(sheet.getByLabel("Certificate", { exact: true })).toHaveValue(
    "/etc/letsencrypt/live/shop.example.com/fullchain.pem",
  )
  await expect(sheet.getByRole("button", { name: "Use the domain’s certificate" })).toHaveCount(0)
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
            "Saving anyway takes store.example.com on 0.0.0.0:80 from legacy, since nginx reads this site first.",
        },
      }),
    }),
  )
  const sheet = await openNewSite(page)
  await sheet.getByLabel("Domains").fill("store.example.com")
  await sheet.getByRole("button", { name: "Save and reload" }).click()
  await expect(sheet.getByRole("button", { name: "Save anyway" })).toBeVisible()
  await sheet.getByLabel("Domains").fill("new.example.com")
  await expect(sheet.getByRole("button", { name: "Save anyway" })).toHaveCount(0)
})

test("a file name typed by hand stays until it is matched to the domain again", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  const saved = await capture(page, "**/api/v1/proxy/sites/", (route, body) =>
    json(route, siteResult({ name: (body.spec as { name: string }).name })),
  )
  const sheet = await openNewSite(page)
  const domains = sheet.getByLabel("Domains")
  const name = sheet.getByLabel("File name")
  await domains.fill("blog.example.com")
  await expect(name).toHaveValue("blog.example.com")
  await expect(sheet.getByRole("button", { name: "Match the domain" })).toHaveCount(0)

  // What the server would refuse is said at the field, and nothing saves.
  await name.fill("My Blog")
  await expect(sheet.getByText(/Lowercase letters, digits, dots/)).toBeVisible()
  await expect(sheet.getByRole("button", { name: "Save and reload" })).toBeDisabled()

  await name.fill("blog")
  await domains.fill("news.example.com")
  await expect(name).toHaveValue("blog")
  await expect(sheet.getByText("Saved as /etc/nginx/sites-available/blog")).toBeVisible()
  await sheet.getByRole("button", { name: "Match the domain" }).click()
  await expect(name).toHaveValue("news.example.com")
  await expect(
    sheet.getByText("Saved as /etc/nginx/sites-available/news.example.com"),
  ).toBeVisible()
  // Following again: the next domain renames it.
  await domains.fill("daily.example.com")
  await expect(name).toHaveValue("daily.example.com")

  await sheet.getByRole("button", { name: "Save and reload" }).click()
  await expect(page.getByText("daily.example.com is live")).toBeVisible()
  expect(saved[0]).toMatchObject({ spec: { name: "daily.example.com" } })
})

test("a new site whose file name is already a site's is stopped before saving", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  const sheet = await openNewSite(page)
  await sheet.getByLabel("Domains").fill("app.example.com")
  await expect(
    sheet.getByText(
      "A site called app.example.com already exists. Pick another name, or open that site to edit it.",
    ),
  ).toBeVisible()
  await expect(sheet.getByRole("button", { name: "Save and reload" })).toBeDisabled()
  await sheet.getByLabel("File name").fill("app-v2.example.com")
  await expect(
    sheet.getByText("Saved as /etc/nginx/sites-available/app-v2.example.com"),
  ).toBeVisible()
  await expect(sheet.getByRole("button", { name: "Save and reload" })).toBeEnabled()
})

test("an existing site shows its file read-only and offers no presets", async ({ page }) => {
  await mockProxy(page, { included: true })
  await page.goto("/proxy/sites")
  await page.getByRole("button", { name: "Open app.example.com" }).click()
  const sheet = page.getByRole("dialog", { name: "Edit app.example.com" })
  const name = sheet.getByLabel("File name")
  await expect(name).toHaveValue("app.example.com")
  await expect(name).toHaveAttribute("readonly", "")
  await expect(sheet.getByText("Saved as /etc/nginx/sites-available/app.example.com")).toBeVisible()
  await expect(sheet.getByText(/already exists/)).toHaveCount(0)
  await expect(sheet.getByText("Start from")).toHaveCount(0)
  await expect(sheet.getByRole("button", { name: "Save and reload" })).toBeEnabled()
})

test("a preset fills in what its application needs and says what to set on its side", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  const previews = await capture(page, "**/api/v1/proxy/sites/preview", (route, body) =>
    json(route, {
      content: `# ${(body.spec as { name: string }).name}\n`,
      warnings: [],
      path: `/etc/nginx/sites-available/${(body.spec as { name: string }).name}`,
      exists: false,
    }),
  )
  const saved = await capture(page, "**/api/v1/proxy/sites/", (route, body) =>
    json(route, siteResult({ name: (body.spec as { name: string }).name })),
  )
  const sheet = await openNewSite(page)
  await sheet.getByLabel("Domains").fill("home.example.com")

  const homeAssistant = sheet.getByRole("button", { name: /^Home Assistant/ })
  await homeAssistant.click()
  await expect(homeAssistant).toHaveAttribute("aria-pressed", "true")
  await expect(sheet.getByLabel("Send it to")).toHaveValue("http://127.0.0.1:8123")
  await expect(sheet.getByLabel("Timeout")).toHaveValue("300")
  await expect(sheet.getByText(/trusted_proxies: 127\.0\.0\.1/)).toBeVisible()
  await expect
    .poll(() => previews.at(-1)?.spec)
    .toMatchObject({ upstream: "http://127.0.0.1:8123", webSockets: true, proxyTimeout: 300 })

  // A registry: no upload limit, request buffering off in the extra lines.
  await sheet.getByRole("button", { name: /^Docker registry/ }).click()
  await expect(homeAssistant).toHaveAttribute("aria-pressed", "false")
  await expect(sheet.getByLabel("Send it to")).toHaveValue("http://127.0.0.1:5000")
  await expect(sheet.getByLabel("Upload limit")).toHaveValue("0")
  await expect(sheet.getByLabel("Extra configuration")).toHaveValue(/proxy_request_buffering off;/)
  await expect(sheet.getByText(/trusted_proxies/)).toHaveCount(0)

  // A single-page app is files with the fallback on; the registry's lines go.
  await sheet.getByRole("button", { name: /^Single-page app/ }).click()
  await expect(sheet.getByRole("button", { name: "Files", exact: true })).toHaveAttribute(
    "aria-pressed",
    "true",
  )
  await expect(sheet.getByRole("switch", { name: "Single-page app" })).toBeChecked()
  await expect(sheet.getByLabel("Extra configuration")).toHaveValue("")
  await sheet.getByLabel("Directory").fill("/var/www/home")
  await sheet.getByRole("button", { name: "Save and reload" }).click()
  await expect(page.getByText("home.example.com is live")).toBeVisible()
  expect(saved[0]).toMatchObject({
    spec: { name: "home.example.com", kind: "static", spa: true, root: "/var/www/home" },
  })
  expect((saved[0].spec as { custom?: string }).custom).toBeUndefined()
})

test("picking a running service fills the upstream, and an unpublished port says why not", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  await page.route("**/api/v1/ports", (route) =>
    json(route, [
      ...ports,
      {
        protocol: "tcp",
        address: "::",
        port: 8080,
        pid: 1500,
        process: "caddy",
        user: "caddy",
        exposed: true,
      },
      {
        protocol: "tcp",
        address: "0.0.0.0",
        port: 8081,
        pid: 1600,
        process: "docker-proxy",
        user: "root",
        exposed: true,
      },
    ]),
  )
  await page.route("**/api/v1/docker/containers/**", (route) => {
    expect(new URL(route.request().url()).searchParams.get("all")).toBe("false")
    return json(route, runningContainers)
  })
  const sheet = await openNewSite(page)
  await sheet.getByLabel("Domains").fill("shop.example.com")
  await sheet.getByRole("button", { name: "Pick from running services" }).click()

  const list = page.getByRole("listbox", { name: "Running services" })
  await expect(list.getByRole("option")).toHaveCount(5)
  // nginx itself and Postgres are not upstreams; docker-proxy's 8081 is the
  // shop container, listed once by its name.
  await expect(list.getByRole("option", { name: /postgres|docker-proxy|:443|:5432/ })).toHaveCount(
    0,
  )
  const worker = list.getByRole("option", { name: /worker/ })
  await expect(worker).toHaveAttribute("aria-disabled", "true")
  await expect(worker).toContainText("Publish it on 127.0.0.1 to reach it from nginx.")
  await list.getByRole("option").filter({ hasText: "127.0.0.1:8081" }).click()
  await expect(list).toBeHidden()
  await expect(sheet.getByLabel("Send it to")).toHaveValue("http://127.0.0.1:8081")

  // The list filters as it is typed into, and a path's upstream has its own.
  await sheet.getByRole("button", { name: "Add a path" }).click()
  await sheet.getByLabel("Path", { exact: true }).fill("/admin")
  await sheet.getByRole("button", { name: "Pick from running services for /admin" }).click()
  const filter = page.getByPlaceholder("Port, process or container")
  await filter.fill("8080")
  await expect(list.getByRole("option")).toHaveCount(1)
  await expect(list.getByRole("option")).toContainText("caddy")
  await filter.press("Enter")
  await expect(sheet.getByLabel("Upstream", { exact: true })).toHaveValue("http://[::1]:8080")
})

test("a loopback upstream with nothing listening is warned about, not refused", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  const sheet = await openNewSite(page)
  await sheet.getByLabel("Domains").fill("idle.example.com")
  const upstream = sheet.getByLabel("Send it to")
  // node listens on 127.0.0.1:3000 in the mocked /ports.
  await expect(upstream).toHaveValue("http://127.0.0.1:3000")
  await expect(sheet.getByText(/Nothing is listening/)).toHaveCount(0)
  await upstream.fill("http://127.0.0.1:3999")
  await expect(
    sheet.getByText(
      "Nothing is listening on 127.0.0.1:3999 right now, so nginx answers 502 until something does.",
    ),
  ).toBeVisible()
  await expect(sheet.getByRole("button", { name: "Save and reload" })).toBeEnabled()
  await upstream.fill("http://app.internal:3999")
  await expect(sheet.getByText(/Nothing is listening/)).toHaveCount(0)
})

test("a link from elsewhere opens the new-site form with its upstream and domain", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  await page.goto(
    "/proxy/sites?new=1&upstream=http%3A%2F%2F127.0.0.1%3A8081&domain=shop.example.com",
  )
  const sheet = page.getByRole("dialog", { name: "New site" })
  await expect(sheet.getByLabel("Domains")).toHaveValue("shop.example.com")
  await expect(sheet.getByLabel("Send it to")).toHaveValue("http://127.0.0.1:8081")
  await expect(sheet.getByLabel("File name")).toHaveValue("shop.example.com")
  // Read once: the address no longer asks, so closing the form closes it.
  await expect(page).toHaveURL(/\/proxy\/sites$/)
  await page.keyboard.press("Escape")
  await expect(sheet).toBeHidden()
  await page.reload()
  await expect(page.getByRole("button", { name: "New site", exact: true })).toBeVisible()
  await expect(page.getByRole("dialog")).toHaveCount(0)

  // The next New site starts blank, not from the link.
  await page.getByRole("button", { name: "New site", exact: true }).click()
  await expect(page.getByRole("dialog", { name: "New site" }).getByLabel("Domains")).toHaveValue("")
})

test("a read-only account following the link gets no form", async ({ page }) => {
  await mockProxy(page, { included: true })
  await page.route("**/api/v1/auth/session", (route) =>
    json(route, { ...user, capabilities: ["read"], user: { ...user.user, role: "viewer" } }),
  )
  await page.goto("/proxy/sites?new=1&upstream=http%3A%2F%2F127.0.0.1%3A8081")
  // Taken off the address once the account is known, and nothing opened.
  await expect(page).toHaveURL(/\/proxy\/sites$/)
  await expect(page.locator("[data-slot='stat-grid']")).toBeVisible()
  await expect(page.getByRole("dialog")).toHaveCount(0)
})

test("the new-site form and its picker fit a phone", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await mockProxy(page, { included: true })
  await page.route("**/api/v1/docker/containers/**", (route) => json(route, runningContainers))
  const sheet = await openNewSite(page)
  await expect(sheet.getByRole("button", { name: /^Jellyfin/ })).toBeVisible()
  expect(await sheet.evaluate((element) => element.scrollWidth <= element.clientWidth + 1)).toBe(
    true,
  )
  await sheet.getByRole("button", { name: "Pick from running services" }).click()
  const popover = page.locator("[data-slot='popover-content']")
  await expect(popover).toBeInViewport({ ratio: 1 })
  expect(await popover.evaluate((element) => element.scrollWidth <= element.clientWidth + 1)).toBe(
    true,
  )
  await page.keyboard.press("Escape")
  await expect(popover).toBeHidden()
  // Escape closed the list, not the form.
  await expect(sheet).toBeVisible()
})
