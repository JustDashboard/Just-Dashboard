import { expect, test, type Page, type Route } from "@playwright/test"
import {
  LINKED_ELSEWHERE,
  legacySpec,
  runningContainers,
  sitePreview,
  siteResult,
} from "./fixtures/proxy/siteform"
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

/**
 * legacy.example.com, disabled, as the host reports it: in the listing, read
 * back into the form and in every preview.
 */
async function disableLegacy(
  page: Page,
  preview: Record<string, unknown> = {},
  read: Record<string, unknown> = {},
) {
  await page.route("**/api/v1/proxy/vhosts", (route) =>
    json(
      route,
      vhosts.map((v) => (v.name === "legacy.example.com" ? { ...v, enabled: false } : v)),
    ),
  )
  await page.route("**/api/v1/proxy/sites/legacy.example.com", (route) =>
    json(route, {
      spec: legacySpec,
      managed: true,
      content: "",
      warnings: [],
      enabled: false,
      ...read,
    }),
  )
  await page.route("**/api/v1/proxy/sites/preview", (route) =>
    json(route, sitePreview(route.request().postDataJSON().spec, { enabled: false, ...preview })),
  )
}

async function openLegacy(page: Page) {
  await page.goto("/proxy/sites")
  await page.getByRole("button", { name: "Open legacy.example.com" }).click()
  const sheet = page.getByRole("dialog", { name: "Edit legacy.example.com" })
  await expect(sheet.getByLabel("Domains")).toHaveValue("legacy.example.com")
  return sheet
}

const LEGACY = {
  name: "legacy.example.com",
  path: "/etc/nginx/sites-available/legacy.example.com",
}

const passed = { valid: true, output: "", command: "nginx -t", diagnostics: [] }

test("a disabled site is saved as it is, or enabled on purpose", async ({ page }) => {
  await mockProxy(page, { included: true })
  await disableLegacy(page)
  const saved = await capture(page, "**/api/v1/proxy/sites/", (route, body) =>
    json(
      route,
      body.enable === "enable"
        ? siteResult({ ...LEGACY, validation: passed })
        : siteResult({
            ...LEGACY,
            enabled: false,
            testedAsEnabled: true,
            validation: passed,
            reloaded: false,
          }),
    ),
  )
  const sheet = await openLegacy(page)
  // A path written with root says where its files really come from.
  await expect(sheet.getByText("/srv/legacy/static")).toBeVisible()
  await expect(
    sheet.getByText(
      "Disabled. nginx tests it as if enabled, and it stays off until you enable it.",
    ),
  ).toBeVisible()
  // "Save and reload" enabled nothing and reloaded for a file nginx does
  // not read.
  await expect(sheet.getByRole("button", { name: "Save and reload" })).toHaveCount(0)
  await expect(sheet.getByRole("button", { name: "Save only" })).toHaveCount(0)

  await sheet.getByRole("button", { name: "Save (stays disabled)" }).click()
  await expect(page.getByText("legacy.example.com saved", { exact: true })).toBeVisible()
  await expect(
    page.getByText("nginx tested it as if enabled. It stays disabled until it is enabled."),
  ).toBeVisible()
  await expect(sheet).toBeHidden()
  expect(saved[0]).toMatchObject({ enable: "keep", overwrite: true, reload: false })
  expect((saved[0].spec as { locations: unknown[] }).locations).toEqual([
    { path: "/static", root: "/srv/legacy", rootMode: "root", webSockets: false },
  ])

  const again = await openLegacy(page)
  await again.getByRole("button", { name: "Save and enable" }).click()
  await expect(page.getByText("legacy.example.com is live")).toBeVisible()
  expect(saved[1]).toMatchObject({ enable: "enable", overwrite: true, reload: true })
  expect(saved[1]).not.toHaveProperty("allowConflict")
})

test("a disabled site nginx would refuse enabled is saved, and says where", async ({ page }) => {
  await mockProxy(page, { included: true })
  await disableLegacy(page)
  await capture(page, "**/api/v1/proxy/sites/", (route) =>
    json(
      route,
      siteResult({
        ...LEGACY,
        enabled: false,
        testedAsEnabled: true,
        reloaded: false,
        validation: {
          valid: false,
          command: "nginx -t",
          output:
            'nginx: [emerg] unknown directive "frobnicate" in /etc/nginx/sites-enabled/legacy.example.com:14\nnginx: configuration file /etc/nginx/nginx.conf test failed',
          diagnostics: [
            {
              level: "emerg",
              message: 'unknown directive "frobnicate"',
              file: LEGACY.path,
              line: 14,
            },
          ],
        },
      }),
    ),
  )
  const sheet = await openLegacy(page)
  await sheet.getByRole("button", { name: "Save (stays disabled)" }).click()
  await expect(
    page.getByText("legacy.example.com saved; enabling it would fail nginx's test"),
  ).toBeVisible()
  await expect(
    page.getByText(
      'Line 14: unknown directive "frobnicate". It stays disabled until it is enabled.',
    ),
  ).toBeVisible()
  await expect(page.getByText("Not applied")).toHaveCount(0)
  await expect(sheet).toBeHidden()
})

test("a disabled site's name conflict is what enabling it would meet", async ({ page }) => {
  await mockProxy(page, { included: true })
  await disableLegacy(page)
  await capture(page, "**/api/v1/proxy/sites/", (route) =>
    json(
      route,
      siteResult({
        ...LEGACY,
        enabled: false,
        testedAsEnabled: true,
        reloaded: false,
        validation: passed,
        conflicts: [
          {
            domain: "legacy.example.com",
            listen: "0.0.0.0:80",
            site: "app.example.com",
            effect: "ignored",
          },
        ],
      }),
    ),
  )
  const sheet = await openLegacy(page)
  await sheet.getByRole("button", { name: "Save (stays disabled)" }).click()
  await expect(
    page.getByText("legacy.example.com saved with a name conflict once enabled"),
  ).toBeVisible()
  await expect(
    page.getByText(
      "Enabled, its claim to legacy.example.com on 0.0.0.0:80 would be ignored: nginx answers it from app.example.com. It stays disabled until it is enabled.",
    ),
  ).toBeVisible()
})

test("enabling a disabled site over a name another site serves asks first", async ({ page }) => {
  await mockProxy(page, { included: true })
  await disableLegacy(page)
  const refusal =
    "Saving anyway takes legacy.example.com on 0.0.0.0:80 from app.example.com, since nginx reads this site first."
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
            ...LEGACY,
            reloaded: body.reload,
            conflicts: [
              {
                domain: "legacy.example.com",
                listen: "0.0.0.0:80",
                site: "app.example.com",
                effect: "takes",
              },
            ],
          }),
        ),
  )
  const sheet = await openLegacy(page)
  await sheet.getByRole("button", { name: "Save and enable" }).click()
  await expect(sheet.getByRole("alert")).toContainText(refusal)
  await sheet.getByRole("button", { name: "Save anyway" }).click()
  await expect(page.getByText("legacy.example.com is live with a name conflict")).toBeVisible()
  await expect(
    page.getByText(
      "nginx now answers legacy.example.com on 0.0.0.0:80 from legacy.example.com, not app.example.com.",
    ),
  ).toBeVisible()
  expect(saved).toHaveLength(2)
  // Saving anyway repeats the save that was refused: enabled, not kept.
  expect(saved[1]).toMatchObject({ enable: "enable", reload: true, allowConflict: true })
})

test("a disabled site whose name another site's link holds can only stay disabled", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  const held =
    "sites-enabled/legacy.example.com already enables /etc/nginx/sites-available/legacy.conf"
  await disableLegacy(page, { enabledElsewhere: held })
  const note = `${held}, so nginx could not test this site as enabled.`
  const saved = await capture(page, "**/api/v1/proxy/sites/", (route) =>
    json(
      route,
      siteResult({
        ...LEGACY,
        enabled: false,
        reloaded: false,
        validation: { ...passed, note },
      }),
    ),
  )
  const sheet = await openLegacy(page)
  await expect(
    sheet.getByText(`${held}, so this site can be neither enabled nor tested under its name.`),
  ).toBeVisible()
  await expect(sheet.getByRole("button", { name: "Save and enable" })).toHaveCount(0)
  await sheet.getByRole("button", { name: "Save (stays disabled)" }).click()
  await expect(page.getByText("legacy.example.com saved", { exact: true })).toBeVisible()
  await expect(page.getByText(`${note} It stays disabled.`)).toBeVisible()
  expect(saved[0]).toMatchObject({ enable: "keep", reload: false })
})

test("a conf.d site that is off can only stay off, and says how it is turned on", async ({
  page,
}) => {
  // At a phone's width, where its longer sentence has to fit beside the button.
  await page.setViewportSize({ width: 390, height: 844 })
  await mockProxy(page, { included: true })
  const path = "/etc/nginx/conf.d/legacy.example.com"
  await disableLegacy(page, { confd: true, path }, { confd: true })
  const saved = await capture(page, "**/api/v1/proxy/sites/", (route) =>
    json(
      route,
      siteResult({
        ...LEGACY,
        path,
        enabled: false,
        testedAsEnabled: true,
        validation: passed,
        reloaded: false,
      }),
    ),
  )
  const sheet = await openLegacy(page)
  await expect(
    sheet.getByText(
      "Disabled: nginx reads only the conf.d files ending in .conf. nginx tests it as if it did, and it stays off until it is renamed.",
    ),
  ).toBeVisible()
  // Enabling a conf.d site is renaming its file, which a save does not do:
  // "Save and enable" wrote a copy under a .conf name that nginx read.
  await expect(sheet.getByRole("button", { name: "Save and enable" })).toHaveCount(0)
  const keep = sheet.getByRole("button", { name: "Save (stays disabled)" })
  await expect(keep).toBeEnabled()
  await expect(keep).toBeInViewport({ ratio: 1 })
  expect(await sheet.evaluate((element) => element.scrollWidth <= element.clientWidth + 1)).toBe(
    true,
  )
  await keep.click()
  await expect(page.getByText("legacy.example.com saved", { exact: true })).toBeVisible()
  await expect(
    page.getByText("nginx tested it as if enabled. It stays disabled until it is enabled."),
  ).toBeVisible()
  expect(saved).toHaveLength(1)
  expect(saved[0]).toMatchObject({ enable: "keep", overwrite: true, reload: false })
})

test("a disabled site's footer fits a phone", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await mockProxy(page, { included: true })
  await disableLegacy(page)
  const sheet = await openLegacy(page)
  const keep = sheet.getByRole("button", { name: "Save (stays disabled)" })
  const enable = sheet.getByRole("button", { name: "Save and enable" })
  await expect(keep).toBeEnabled()
  await expect(keep).toBeInViewport({ ratio: 1 })
  await expect(enable).toBeInViewport({ ratio: 1 })
  expect(await sheet.evaluate((element) => element.scrollWidth <= element.clientWidth + 1)).toBe(
    true,
  )
})

test("a site nginx serves from a file of its own in sites-enabled is not called disabled", async ({
  page,
}) => {
  // At a phone's width, where the sentence has to fit beside the button.
  await page.setViewportSize({ width: 390, height: 844 })
  await mockProxy(page, { included: true })
  // The listing calls it serving, as nginx answers it; only the form knows
  // the file it edits is not the one nginx reads.
  const copy = { enabled: false, servedCopy: true }
  await page.route("**/api/v1/proxy/sites/legacy.example.com", (route) =>
    json(route, { spec: legacySpec, managed: true, content: "", warnings: [], ...copy }),
  )
  await page.route("**/api/v1/proxy/sites/preview", (route) =>
    json(
      route,
      sitePreview(route.request().postDataJSON().spec, {
        ...copy,
        enabledElsewhere: "sites-enabled/legacy.example.com is a file of its own, not a link",
      }),
    ),
  )
  const saved = await capture(page, "**/api/v1/proxy/sites/", (route) =>
    json(
      route,
      siteResult({
        ...LEGACY,
        enabled: false,
        servedCopy: true,
        reloaded: false,
        validation: {
          ...passed,
          note: "nginx serves sites-enabled/legacy.example.com, a file of its own, and not this one, so it did not test this file and the save changes nothing it serves.",
        },
      }),
    ),
  )
  const sheet = await openLegacy(page)
  await expect(
    sheet.getByText(
      "nginx serves sites-enabled/legacy.example.com, a file of its own, not this one. Saving here changes nothing it serves until that file is replaced by a link to this one.",
    ),
  ).toBeVisible()
  // Neither "disabled" nor a reload or an enable that would not reach it.
  for (const name of ["Save (stays disabled)", "Save and enable", "Save and reload"]) {
    await expect(sheet.getByRole("button", { name })).toHaveCount(0)
  }
  await expect(sheet.getByText(/disabled/i)).toHaveCount(0)
  const keep = sheet.getByRole("button", { name: "Save", exact: true })
  await expect(keep).toBeEnabled()
  await expect(keep).toBeInViewport({ ratio: 1 })
  expect(await sheet.evaluate((element) => element.scrollWidth <= element.clientWidth + 1)).toBe(
    true,
  )
  await keep.click()
  await expect(page.getByText("legacy.example.com saved", { exact: true })).toBeVisible()
  await expect(
    page.getByText(
      "nginx serves sites-enabled/legacy.example.com, a file of its own, not this one, so this save changes nothing it serves.",
    ),
  ).toBeVisible()
  await expect(page.getByText(/stays disabled/)).toHaveCount(0)
  expect(saved).toHaveLength(1)
  expect(saved[0]).toMatchObject({ enable: "keep", overwrite: true, reload: false })
})

test("a hand-written site's save says where the previous version was kept", async ({ page }) => {
  await mockProxy(page, { included: true })
  await page.route("**/api/v1/proxy/sites/legacy.example.com", (route) =>
    json(route, { spec: legacySpec, managed: false, content: "", warnings: [], enabled: true }),
  )
  const backup = `${LEGACY.path}.bak`
  await capture(page, "**/api/v1/proxy/sites/", (route) =>
    json(route, siteResult({ ...LEGACY, validation: passed, backup })),
  )
  const sheet = await openLegacy(page)
  await expect(sheet.getByText("This file was written by hand")).toBeVisible()
  await sheet.getByRole("button", { name: "Save and reload" }).click()
  await expect(page.getByText("legacy.example.com is live")).toBeVisible()
  await expect(page.getByText(`The hand-written version is kept as ${backup}.`)).toBeVisible()
})

test("a save nginx warns about says how many warnings and where", async ({ page }) => {
  await mockProxy(page, { included: true })
  await capture(page, "**/api/v1/proxy/sites/", (route) =>
    json(
      route,
      siteResult({
        testWarnings: [
          {
            level: "warn",
            message: "protocol options redefined for 0.0.0.0:443",
            file: "/etc/nginx/sites-available/app.example.com",
            line: 12,
          },
          {
            level: "warn",
            message: 'duplicate MIME type "text/html"',
            file: "/etc/nginx/sites-available/app.example.com",
            line: 40,
          },
        ],
      }),
    ),
  )
  await page.goto("/proxy/sites")
  await page.getByRole("button", { name: "Open app.example.com" }).click()
  const sheet = page.getByRole("dialog", { name: "Edit app.example.com" })
  await expect(sheet.getByLabel("Domains")).toHaveValue("app.example.com")
  await sheet.getByRole("button", { name: "Save and reload" }).click()
  await expect(page.getByText("app.example.com is live with 2 warnings")).toBeVisible()
  await expect(
    page.getByText(
      'Line 12: protocol options redefined for 0.0.0.0:443; Line 40: duplicate MIME type "text/html"',
    ),
  ).toBeVisible()
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

test("a new site whose name another site's link holds is stopped before saving", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  const saved = await capture(page, "**/api/v1/proxy/sites/", (route, body) =>
    json(route, siteResult({ name: (body.spec as { name: string }).name })),
  )
  const sheet = await openNewSite(page)
  // The name follows the domain, so the operator never typed it: saving it
  // would have unlinked the hand-written site that link enables.
  await sheet.getByLabel("Domains").fill("wiki.example.com")
  await expect(
    sheet.getByText(
      `${LINKED_ELSEWHERE["wiki.example.com"]}, so the name is taken. Pick another name.`,
    ),
  ).toBeVisible()
  await expect(sheet.getByLabel("File name")).toHaveAttribute("aria-invalid", "true")
  await expect(sheet.getByRole("button", { name: "Save and reload" })).toBeDisabled()
  await expect(sheet.getByRole("button", { name: "Save only" })).toBeDisabled()
  await sheet.getByLabel("File name").fill("wiki-v2.example.com")
  await expect(sheet.getByText(/so the name is taken/)).toHaveCount(0)
  await sheet.getByRole("button", { name: "Save and reload" }).click()
  await expect(page.getByText("wiki-v2.example.com is live")).toBeVisible()
  expect(saved[0]).toMatchObject({
    overwrite: false,
    spec: { name: "wiki-v2.example.com", domains: ["wiki.example.com"] },
  })
})

test("the new-site form opens with the keyboard in Domains, above the presets", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  const sheet = await openNewSite(page)
  const domains = sheet.getByLabel("Domains")
  await expect(domains).toBeFocused()
  await page.keyboard.type("typed.example.com")
  await expect(domains).toHaveValue("typed.example.com")
  await expect(sheet.getByLabel("File name")).toHaveValue("typed.example.com")
  // Domains and the file name come first; the presets follow them.
  const name = await sheet.getByLabel("File name").boundingBox()
  const firstPreset = await sheet.getByRole("button", { name: /^Node\.js app/ }).boundingBox()
  expect(name!.y).toBeLessThan(firstPreset!.y)

  // On a phone the one field every site needs is in view when the form opens.
  await page.keyboard.press("Escape")
  await page.setViewportSize({ width: 390, height: 844 })
  const phone = await openNewSite(page)
  await expect(phone.getByLabel("Domains")).toBeFocused()
  await expect(phone.getByLabel("Domains")).toBeInViewport({ ratio: 1 })
  await expect(phone.getByLabel("File name")).toBeInViewport({ ratio: 1 })

  // A preset's note is brought into view under the card that was picked.
  await phone.getByLabel("Domains").fill("dash.example.com")
  const jellyfin = phone.getByRole("button", { name: /^Jellyfin/ })
  await jellyfin.scrollIntoViewIfNeeded()
  await jellyfin.click()
  await expect(phone.getByText(/Known proxies under Networking/)).toBeInViewport({ ratio: 1 })
})

test("a new site's default upstream is not warned about until somebody sets it", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  // Nothing listens on 3000 here, the port the blank form suggests.
  await page.route("**/api/v1/ports", (route) =>
    json(
      route,
      ports.filter((p) => p.port !== 3000),
    ),
  )
  const listed = page.waitForResponse((r) => r.url().endsWith("/api/v1/ports"))
  const sheet = await openNewSite(page)
  await listed
  await sheet.getByLabel("Domains").fill("fresh.example.com")
  const upstream = sheet.getByLabel("Send it to")
  await expect(upstream).toHaveValue("http://127.0.0.1:3000")
  await expect(
    sheet.getByText("Where the application is listening. Usually loopback on this machine."),
  ).toBeVisible()
  await expect(sheet.getByText(/Nothing is listening/)).toHaveCount(0)

  // A preset that picks that same port is somebody choosing it.
  await sheet.getByRole("button", { name: /^Node\.js app/ }).click()
  await expect(upstream).toHaveValue("http://127.0.0.1:3000")
  await expect(
    sheet.getByText(
      "Nothing is listening on 127.0.0.1:3000 right now, so nginx answers 502 until something does.",
    ),
  ).toBeVisible()
  await expect(sheet.getByRole("button", { name: "Save and reload" })).toBeEnabled()
  await page.keyboard.press("Escape")

  // So is typing it, and so is a link that names it.
  const typed = await openNewSite(page)
  await typed.getByLabel("Domains").fill("typed.example.com")
  await expect(typed.getByText(/Nothing is listening/)).toHaveCount(0)
  await typed.getByLabel("Send it to").fill("")
  await typed.getByLabel("Send it to").pressSequentially("http://127.0.0.1:3000")
  await expect(typed.getByText(/Nothing is listening on 127\.0\.0\.1:3000/)).toBeVisible()
  await page.keyboard.press("Escape")

  await page.goto(
    "/proxy/sites?new=1&upstream=http%3A%2F%2F127.0.0.1%3A3000&domain=linked.example.com",
  )
  const linked = page.getByRole("dialog", { name: "New site" })
  await expect(linked.getByLabel("Send it to")).toHaveValue("http://127.0.0.1:3000")
  await expect(linked.getByText(/Nothing is listening on 127\.0\.0\.1:3000/)).toBeVisible()
})

test("a kind picked by hand after a preset lets the preset go", async ({ page }) => {
  await mockProxy(page, { included: true })
  const previews = await capture(page, "**/api/v1/proxy/sites/preview", (route, body) =>
    json(route, {
      content: `# ${(body.spec as { name: string }).name}\n`,
      warnings: [],
      path: `/etc/nginx/sites-available/${(body.spec as { name: string }).name}`,
      exists: false,
    }),
  )
  const sheet = await openNewSite(page)
  await sheet.getByLabel("Domains").fill("kinds.example.com")

  const spa = sheet.getByRole("button", { name: /^Single-page app/ })
  await spa.click()
  await expect(spa).toHaveAttribute("aria-pressed", "true")
  await sheet.getByRole("button", { name: "An app", exact: true }).click()
  await expect(spa).toHaveAttribute("aria-pressed", "false")
  await expect.poll(() => previews.at(-1)?.spec).toMatchObject({ kind: "proxy" })
  expect(previews.at(-1)?.spec).not.toHaveProperty("spa")

  const registry = sheet.getByRole("button", { name: /^Docker registry/ })
  await registry.click()
  await expect(sheet.getByText(/Docker refuses a registry over plain HTTP/)).toBeVisible()
  await expect(sheet.getByLabel("Extra configuration")).toHaveValue(/proxy_request_buffering off;/)
  await sheet.getByRole("button", { name: "A redirect", exact: true }).click()
  await expect(registry).toHaveAttribute("aria-pressed", "false")
  await expect(sheet.getByText(/Docker refuses a registry over plain HTTP/)).toHaveCount(0)
  await expect(sheet.getByLabel("Extra configuration")).toHaveValue("")
  await expect.poll(() => previews.at(-1)?.spec).toMatchObject({ kind: "redirect" })
  const sent = previews.at(-1)?.spec as Record<string, unknown>
  expect(sent.custom).toBeUndefined()
  expect(sent.clientMaxBody).toBe("50m")
  expect(sent.upstream).toBe("http://127.0.0.1:3000")
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
