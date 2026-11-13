import { expect, test, type Page, type Route } from "@playwright/test"
import { certs, json, mockProxy, user, yesterday } from "./proxy-fixtures"
import { certbotState, stagingCertificate } from "./fixtures/proxy/certs"
import { risen } from "./risen"

/**
 * The Certificates page after its 0.8.7 overhaul, and a certificate's own
 * page, which took the place of the sheet: what each opens on, that neither
 * moves while it loads, that its rows are one height whatever they carry,
 * that the old addresses land, and that a reader is offered nothing to press.
 */

const caddy = {
  name: "shop.example.com",
  path: "/data/caddy/certificates/acme-v02/shop.example.com/shop.example.com.crt",
  domains: ["shop.example.com"],
  issuer: "E6",
  notBefore: yesterday,
  notAfter: new Date(Date.now() + 70 * 86_400_000).toISOString(),
  daysLeft: 70,
  expired: false,
  expiring: false,
  selfSigned: false,
  source: "caddy",
  usedBy: [],
}

const busy = {
  name: "wide.example.com",
  path: "/etc/letsencrypt/live/wide.example.com/fullchain.pem",
  domains: ["wide.example.com", "a.wide.example.com", "b.wide.example.com", "c.wide.example.com"],
  issuer: "R11",
  notBefore: yesterday,
  notAfter: new Date(Date.now() + 60 * 86_400_000).toISOString(),
  daysLeft: 60,
  expired: false,
  expiring: false,
  selfSigned: false,
  source: "certbot",
  usedBy: ["wide.example.com", "a.wide.example.com", "b.wide.example.com"],
  usedByStreams: ["mail"],
}

const unreadable = {
  name: "broken.example.com",
  path: "/etc/ssl/broken.pem",
  domains: [],
  issuer: "",
  daysLeft: 0,
  expired: false,
  expiring: false,
  selfSigned: false,
  source: "site",
  usedBy: ["broken.example.com"],
  error: "no PEM certificate in the file",
}

const inventory = [...certs, stagingCertificate(), caddy, busy, unreadable]

const facts = (name: string) => ({
  subject: `CN=${name}`,
  issuer: "CN=R11,O=Let's Encrypt,C=US",
  dnsNames: [name],
  ipAddresses: [],
  emails: [],
  uris: [],
  serial: "04:1A:2B",
  sha256: "3A:9F:12:C4",
  sha1: "AA:BB",
  notBefore: yesterday,
  notAfter: certs[0].notAfter,
  keyType: "ECDSA",
  keyBits: 256,
  signature: "SHA256-RSA",
  ca: false,
  selfSigned: false,
  ocsp: [],
  issuerUrls: ["http://r11.i.lencr.org/"],
  crl: [],
  scts: 2,
})

/** The inventory above, with what a certificate's page reads about its file. */
async function mockCertificates(page: Page, list: object[] = inventory) {
  await page.route("**/api/v1/certificates/", (route) => json(route, list))
  await page.route("**/api/v1/certificates/detail**", (route) => {
    const path = new URL(route.request().url()).searchParams.get("path") ?? ""
    const name = path.split("/").at(-2) ?? "app.example.com"
    return json(route, {
      path,
      chain: {
        verdict: "complete",
        note: "The leaf and its intermediate, in order.",
        certificates: [facts(name), { ...facts("R11"), subject: "CN=R11", signsPrevious: true }],
      },
      key: {
        path: path.replace("fullchain", "privkey"),
        from: "beside the certificate",
        mode: "0600",
        owner: "root",
        group: "root",
        matches: true,
      },
    })
  })
  await page.route("**/api/v1/certificates/history**", (route) =>
    json(route, { events: [], note: "" }),
  )
  await page.route("**/api/v1/certificates/csr", (route) => json(route, []))
  await page.route("**/api/v1/certificates/local-ca", (route) => json(route, { exists: false }))
}

async function asReader(page: Page) {
  await page.route("**/api/v1/auth/session", (route) =>
    json(route, { ...user, capabilities: ["read"], user: { ...user.user, role: "viewer" } }),
  )
}

const rows = (page: Page) =>
  page.getByRole("list", { name: "Installed certificates" }).locator("[data-slot='choice-row']")

for (const width of [1280, 390]) {
  test(`at ${width} the page opens on its identity line, and holds still while it loads`, async ({
    page,
  }) => {
    await page.setViewportSize({ width, height: 900 })
    await mockProxy(page, { included: true })
    await mockCertificates(page)
    // The list is held back until the page has been measured without it.
    let release = () => {}
    const held = new Promise<void>((resolve) => (release = resolve))
    await page.route("**/api/v1/certificates/", async (route: Route) => {
      await held
      await json(route, inventory)
    })
    await page.goto("/proxy/certificates")

    const identity = page.locator("[data-slot='host-identity']")
    const strip = page.getByRole("tablist", { name: "Views of the certificates" })
    const list = page.getByRole("tabpanel")
    await expect(page.locator("[data-slot='cert-list-loading']")).toBeVisible()
    await risen(page)
    const before = {
      identity: await identity.boundingBox(),
      strip: await strip.boundingBox(),
      list: await list.boundingBox(),
    }

    release()
    await expect(rows(page).first()).toBeVisible()
    await risen(page)
    const after = {
      identity: await identity.boundingBox(),
      strip: await strip.boundingBox(),
      list: await list.boundingBox(),
    }
    expect(after.identity?.y).toBe(before.identity?.y)
    expect(after.identity?.height).toBe(before.identity?.height)
    expect(after.strip?.y).toBe(before.strip?.y)
    expect(after.list?.y).toBe(before.list?.y)

    // No row of tiles at the top, and the line says what the tiles said.
    await expect(page.locator("[data-slot='stat-grid']")).toHaveCount(0)
    await expect(identity).toContainText("6 certificates")
    await expect(identity).toContainText("1 expiring")
    await expect(identity).toContainText("3 refused")
    await expect(identity.getByRole("button", { name: "Issue certificate" })).toBeVisible()
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= document.documentElement.clientWidth + 1,
      ),
    ).toBe(true)
  })

  test(`at ${width} every certificate is one line, whatever it carries`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 })
    await mockProxy(page, { included: true })
    await mockCertificates(page)
    await page.goto("/proxy/certificates")
    await expect(rows(page)).toHaveCount(inventory.length)
    await risen(page)
    const heights = await rows(page).evaluateAll((items) =>
      items.map((item) => Math.round(item.getBoundingClientRect().height)),
    )
    // A Caddy certificate (no file to copy), a test one, an unreadable one
    // and one used by four things are the same height as a plain one.
    expect(new Set(heights).size).toBe(1)
    // The next row starts where it did: a row's menu opening moves nothing.
    // An open menu hides the page from the accessibility tree, so the row is
    // found by its slot.
    const next = page.locator("[data-slot='cert-list'] [data-slot='choice-row']").nth(1)
    const second = await next.boundingBox()
    await rows(page)
      .first()
      .getByRole("button", { name: /Actions for/ })
      .click()
    await expect(page.getByRole("menu")).toBeVisible()
    expect((await next.boundingBox())?.y).toBe(second?.y)
  })
}

test("a row opens the certificate's page, with the sheet's contents as views", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await mockProxy(page, { included: true })
  await mockCertificates(page)
  await page.goto("/proxy/certificates")
  await rows(page).filter({ hasText: "wide.example.com" }).getByRole("link").click()
  await expect(page).toHaveURL(/\/proxy\/certificates\/wide\.example\.com$/)

  const identity = page.locator("[data-slot='host-identity']")
  await expect(identity).toContainText("wide.example.com")
  await expect(identity).toContainText("60d left")
  // The picture: who signed it, the certificate, who serves it.
  const picture = page.locator("[data-slot='certificate-picture']")
  await expect(picture).toContainText("Signed by")
  await expect(picture.getByRole("link", { name: "Open site a.wide.example.com" })).toBeVisible()
  await expect(picture.getByRole("link", { name: "Open stream mail" })).toBeVisible()
  await expect(page.getByText("CN=wide.example.com")).toBeVisible()

  const views = page.getByRole("tablist", { name: "Views of wide.example.com" })
  for (const name of ["Overview", "Key", "Chain", "Files", "Timeline"]) {
    await expect(views.getByRole("tab", { name })).toBeVisible()
  }
  await views.getByRole("tab", { name: "Chain" }).click()
  await expect(page).toHaveURL(/tab=chain/)
  await expect(page.getByText("Complete", { exact: true })).toBeVisible()
  await views.getByRole("tab", { name: "Key" }).click()
  await expect(page.getByText("The key belongs to this certificate")).toBeVisible()
  await views.getByRole("tab", { name: "Files" }).click()
  await expect(page.getByRole("link", { name: "Full chain" })).toBeVisible()
  await expect(page.getByRole("button", { name: "Export key…" })).toBeVisible()

  // The rail is the list, the one on screen filled, and Alt+↓ walks it
  // carrying the view.
  const rail = page.getByRole("navigation", { name: "Certificates" })
  await expect(rail.locator("[aria-current='page']")).toContainText("wide.example.com")
  await page.keyboard.press("Alt+ArrowDown")
  await expect(page).not.toHaveURL(/wide\.example\.com/)
  await expect(page).toHaveURL(/tab=files/)
})

test("a certificate's verbs are its page's: the brand command and the menu", async ({ page }) => {
  await mockProxy(page, { included: true })
  await mockCertificates(page)
  await page.route("**/api/v1/certificates/certbot", (route) =>
    json(route, certbotState({ autoRenew: true })),
  )
  const renewed: unknown[] = []
  await page.route("**/api/v1/certificates/renew", async (route) => {
    renewed.push(route.request().postDataJSON())
    await json(route, { id: "job-9", kind: "certbot.renew", status: "running", title: "Renewing" })
  })
  await page.goto("/proxy/certificates/app.example.com")
  await page
    .getByRole("button", { name: /^Renew/ })
    .first()
    .click()
  await expect.poll(() => renewed.length).toBe(1)
  expect(renewed[0]).toMatchObject({ name: "app.example.com", dryRun: false })

  await page.getByRole("button", { name: "More actions for app.example.com" }).click()
  const menu = page.getByRole("menu")
  for (const name of [
    "Dry run",
    "Renew now",
    "Change names",
    "TLS report for app.example.com",
    "Watch app.example.com",
    "Copy path",
    "Delete",
    "Revoke and delete",
  ]) {
    await expect(menu.getByRole("menuitem", { name, exact: true })).toBeVisible()
  }
})

test("the sheet's old address lands on the certificate's page", async ({ page }) => {
  await mockProxy(page, { included: true })
  await mockCertificates(page)
  await page.goto(`/proxy/certificates?cert=${encodeURIComponent(busy.path)}`)
  await expect(page).toHaveURL(/\/proxy\/certificates\/wide\.example\.com$/)
  await expect(page.locator("[data-slot='host-identity']")).toContainText("wide.example.com")

  // A file no longer listed lands on the list.
  await page.goto("/proxy/certificates?cert=%2Fgone.pem")
  await expect(page).toHaveURL(/\/proxy\/certificates$/)
})

test("a name two certificates share is told apart by its file", async ({ page }) => {
  const copy = { ...certs[0], path: "/etc/nginx/ssl/app.example.com.pem", source: "site" }
  await mockProxy(page, { included: true })
  await mockCertificates(page, [...certs, copy])
  await page.goto("/proxy/certificates")
  await rows(page).filter({ hasText: "site file" }).getByRole("link").click()
  await expect(page).toHaveURL(/path=%2Fetc%2Fnginx%2Fssl%2Fapp\.example\.com\.pem/)
  await expect(page.getByText("/etc/nginx/ssl/app.example.com.pem")).toBeVisible()
})

test("an unknown certificate says so, with the way back", async ({ page }) => {
  await mockProxy(page, { included: true })
  await mockCertificates(page)
  await page.goto("/proxy/certificates/nobody.example.com")
  await expect(page.getByText("No certificate called nobody.example.com")).toBeVisible()
  await expect(page.getByRole("link", { name: "All certificates" })).toHaveAttribute(
    "href",
    "/proxy/certificates",
  )
})

test("the views are in the address, and a link to one opens it", async ({ page }) => {
  await mockProxy(page, { included: true })
  await mockCertificates(page)
  await page.goto("/proxy/certificates?tab=renewal")
  await expect(page.getByRole("tab", { name: "Renewal" })).toHaveAttribute("aria-selected", "true")
  await expect(page.getByRole("list", { name: "certbot lineages" })).toBeVisible()
  await page.getByRole("tab", { name: "Watched domains" }).click()
  await expect(page).toHaveURL(/tab=watched/)
  await page.getByRole("tab", { name: "Certificates" }).click()
  await expect(page).toHaveURL(/\/proxy\/certificates$/)
  for (const tab of ["DNS challenge", "ACME accounts", "Local CA", "Signing requests"]) {
    await page.getByRole("tab", { name: tab }).click()
    await expect(page.getByRole("tab", { name: tab })).toHaveAttribute("aria-selected", "true")
  }
})

test("a read-only account is offered nothing it cannot use, here or on a certificate", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  await mockCertificates(page)
  await asReader(page)
  await page.goto("/proxy/certificates")
  await expect(rows(page)).toHaveCount(inventory.length)
  await expect(page.getByRole("button", { name: "Issue certificate" })).toHaveCount(0)
  await expect(page.getByRole("tab", { name: "DNS challenge" })).toHaveCount(0)
  await expect(page.getByRole("tab", { name: "ACME accounts" })).toHaveCount(0)
  await page.getByRole("button", { name: "More certificate actions" }).click()
  await expect(page.getByRole("menuitem", { name: "Import" })).toHaveCount(0)
  await expect(page.getByRole("menuitem", { name: "Paste a certificate" })).toBeVisible()
  await page.keyboard.press("Escape")

  await page.goto("/proxy/certificates/app.example.com")
  await expect(page.locator("[data-slot='host-identity']")).toContainText("app.example.com")
  await expect(page.getByRole("button", { name: /^Renew/ })).toHaveCount(0)
  await page.getByRole("button", { name: "More actions for app.example.com" }).click()
  const menu = page.getByRole("menu")
  await expect(menu.getByRole("menuitem", { name: "Copy path" })).toBeVisible()
  for (const name of ["Delete", "Revoke and delete", "Change names", "Dry run"]) {
    await expect(menu.getByRole("menuitem", { name })).toHaveCount(0)
  }
  await page.keyboard.press("Escape")
  await expect(page.getByRole("tab", { name: "Timeline" })).toHaveCount(0)
  await page.getByRole("tab", { name: "Files" }).click()
  await expect(page.getByRole("button", { name: "Export key…" })).toHaveCount(0)
})

test("a certificate's page is one pane on a phone", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await mockProxy(page, { included: true })
  await mockCertificates(page)
  await page.goto("/proxy/certificates/test.example.com")
  await expect(page.getByRole("navigation", { name: "Certificates" })).toBeHidden()
  const command = page.getByRole("button", { name: "Replace with a real certificate" }).first()
  await expect(command).toBeInViewport({ ratio: 1 })
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= document.documentElement.clientWidth + 1,
    ),
  ).toBe(true)
})
