import { expect, test, type Page } from "@playwright/test"
import { availability, certs, json, mockProxy, yesterday } from "./proxy-fixtures"

/**
 * The Certificates page on a server that deploys every app behind the Docker
 * Caddy ingress: its certificates are Caddy's, many of them left by routes of
 * deleted deployments, and certbot may not be installed at all. The page said
 * "no certbot", listed who serves a certificate as a route's file name, hid
 * that below `xl`, counted an expired leftover as refused, and offered no way
 * to delete anything Caddy held.
 */

const STORAGE = "/data/caddy/certificates/acme-v02.api.letsencrypt.org-directory"

function held(name: string, overrides: Record<string, unknown> = {}) {
  return {
    name,
    path: `${STORAGE}/${name}/${name}.crt`,
    domains: [name],
    issuer: "E6",
    notBefore: yesterday,
    notAfter: new Date(Date.now() + 70 * 86_400_000).toISOString(),
    daysLeft: 70,
    expired: false,
    expiring: false,
    selfSigned: false,
    source: "caddy",
    usedBy: [],
    ...overrides,
  }
}

/** One a project serves, one a leftover route serves, one nothing serves, and an expired leftover. */
const served = held("shop.example.com", {
  usedBy: ["just-dashboard-env-12.conf"],
  usedByLabels: [
    { site: "just-dashboard-env-12.conf", label: "shop · production", href: "/deploy/3" },
  ],
})
const leftover = held("left.example.com", {
  usedBy: ["just-dashboard-env-40.conf"],
  usedByLabels: [
    {
      site: "just-dashboard-env-40.conf",
      label: "leftover route",
      href: "/proxy/sites/just-dashboard-env-40.conf",
      leftover: true,
    },
  ],
})
const unused = held("old.example.com")
const expired = held("gone.example.com", {
  notAfter: yesterday,
  daysLeft: -1,
  expired: true,
})
const caddyInventory = [served, leftover, unused, expired]

/** A server where the ingress runs and certbot is not installed. */
async function mockCaddyOnly(page: Page, list: object[] = caddyInventory) {
  await mockProxy(page, { included: true })
  await page.route("**/api/v1/proxy/status", (route) =>
    json(route, {
      ...availability,
      nginx: false,
      nginxVersion: "",
      caddy: true,
      certbot: false,
      ingressContainer: "edge",
      ingressState: "running",
    }),
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
  await page.route("**/api/v1/certificates/", (route) => json(route, list))
  await page.route("**/api/v1/certificates/csr", (route) => json(route, []))
  await page.route("**/api/v1/certificates/local-ca", (route) => json(route, { exists: false }))
  await page.route("**/api/v1/certificates/evidence", (route) => json(route, []))
}

const rows = (page: Page) =>
  page.getByRole("list", { name: "Installed certificates" }).locator("[data-slot='choice-row']")
const row = (page: Page, name: string) => rows(page).filter({ hasText: name })

test("a Caddy-only server says Caddy renews, and keeps certbot's views out of the way", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1280, height: 900 })
  await mockCaddyOnly(page)
  await page.goto("/proxy/certificates")
  await expect(rows(page)).toHaveCount(caddyInventory.length)

  const identity = page.locator("[data-slot='host-identity']")
  await expect(identity).toContainText("renewed by Caddy")
  await expect(identity).not.toContainText("no certbot")
  // An expired certificate nothing serves does not turn the line red.
  await expect(identity).not.toContainText("refused")
  await expect(identity).toContainText("all in use valid")
  await expect(identity).toContainText("2 unused")

  await expect(page.getByRole("tab", { name: "ACME accounts" })).toHaveCount(0)
  await expect(page.getByRole("tab", { name: "Signing requests" })).toHaveCount(0)
  // The tunnel's Cloudflare token is saved here, certbot or not.
  await expect(page.getByRole("tab", { name: "DNS challenge" })).toBeVisible()

  // Renewal leads with what Caddy renews; certbot comes second.
  await page.getByRole("tab", { name: "Renewal" }).click()
  const renewed = page.getByRole("region", { name: "Certificates Caddy renews" })
  await expect(renewed).toContainText("Renewed by Caddy")
  await expect(renewed).toContainText("about 30 days before expiry")
  const renewedRows = renewed.locator("[data-slot='choice-row']")
  await expect(renewedRows).toHaveCount(caddyInventory.length)
  await expect(renewedRows.filter({ hasText: "shop.example.com" })).toContainText("renews about")
  await expect(renewedRows.filter({ hasText: "old.example.com" })).toContainText(
    "no route serves it",
  )
  const install = page.getByRole("button", { name: "Install certbot" })
  await expect(install).toBeVisible()
  expect((await install.boundingBox())!.y).toBeGreaterThan((await renewed.boundingBox())!.y)
  await expect(page.getByText(/for an nginx site or a wildcard certificate/)).toBeVisible()
})

test("a signing request waiting on a Caddy-only server keeps its view", async ({ page }) => {
  await mockCaddyOnly(page)
  await page.route("**/api/v1/certificates/csr", (route) =>
    json(route, [
      {
        name: "bought.example.com",
        domains: ["bought.example.com"],
        keyType: "ECDSA",
        createdAt: yesterday,
        csr: "-----BEGIN CERTIFICATE REQUEST-----\nMIIB\n-----END CERTIFICATE REQUEST-----\n",
      },
    ]),
  )
  await page.goto("/proxy/certificates")
  await expect(page.getByRole("tab", { name: "Signing requests" })).toBeVisible()
  await expect(page.getByRole("tab", { name: "ACME accounts" })).toHaveCount(0)
})

test("a Caddy-only server with no certificate points at a project's domain", async ({ page }) => {
  await mockCaddyOnly(page, [])
  await page.goto("/proxy/certificates")
  await expect(page.getByText("No certificates on this server")).toBeVisible()
  await expect(page.getByRole("link", { name: "Add a domain to a project" })).toHaveAttribute(
    "href",
    "/deploy",
  )
})

for (const width of [1280, 800]) {
  test(`at ${width} a row says who serves it in words, and what nothing serves is Unused`, async ({
    page,
  }) => {
    await page.setViewportSize({ width, height: 900 })
    await mockCaddyOnly(page)
    await page.goto("/proxy/certificates")
    await expect(rows(page)).toHaveCount(caddyInventory.length)

    const project = row(page, "shop.example.com").getByRole("link", { name: "shop · production" })
    await expect(project).toBeVisible()
    await expect(project).toHaveAttribute("href", "/deploy/3")
    await expect(row(page, "shop.example.com")).not.toContainText("just-dashboard-env-12.conf")

    const left = row(page, "left.example.com").getByRole("link", { name: "leftover route" })
    await expect(left).toBeVisible()
    await expect(left).toHaveAttribute("href", "/proxy/sites/just-dashboard-env-40.conf")

    await expect(
      row(page, "old.example.com").locator("[data-slot='cert-unused']").getByText("Unused"),
    ).toBeVisible()

    // One height, whatever a row says about who serves it.
    const heights = await rows(page).evaluateAll((items) =>
      items.map((item) => Math.round(item.getBoundingClientRect().height)),
    )
    expect(new Set(heights).size).toBe(1)
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= document.documentElement.clientWidth + 1,
      ),
    ).toBe(true)
  })
}

test("the Unused and Expired chips narrow the list, and the line's count does the same", async ({
  page,
}) => {
  await mockCaddyOnly(page, [...caddyInventory, ...certs])
  await page.goto("/proxy/certificates")
  await expect(rows(page)).toHaveCount(6)

  const chips = page.locator("[aria-label='Source']")
  // old and gone at Caddy, and the fixture's expired import.
  await chips.getByRole("button", { name: /^Unused/ }).click()
  await expect(rows(page)).toHaveCount(3)
  await expect(rows(page)).toContainText(["gone.example.com", "old.example.com", "old.example.com"])

  await chips.getByRole("button", { name: /^Expired/ }).click()
  await expect(rows(page)).toHaveCount(2)
  await chips.getByRole("button", { name: /^All/ }).click()
  await expect(rows(page)).toHaveCount(6)

  await page.locator("[data-slot='unused-fact']").click()
  await expect(rows(page)).toHaveCount(3)
  await expect(chips.getByRole("button", { name: /^Unused/ })).toHaveAttribute(
    "aria-pressed",
    "true",
  )
})

test("an unused Caddy certificate is deleted from its row, by its path", async ({ page }) => {
  await mockCaddyOnly(page)
  const deletes: { method: string; body: unknown }[] = []
  let list = caddyInventory
  await page.route("**/api/v1/certificates/", (route) => json(route, list))
  await page.route("**/api/v1/certificates/caddy", (route) => {
    deletes.push({ method: route.request().method(), body: route.request().postDataJSON() })
    list = list.filter((cert) => cert.name !== "old.example.com")
    return json(route, {
      name: "old.example.com",
      path: unused.path,
      copies: [],
      keptCopies: [],
    })
  })
  await page.goto("/proxy/certificates")
  await expect(rows(page)).toHaveCount(caddyInventory.length)

  await row(page, "old.example.com")
    .getByRole("button", { name: "Actions for old.example.com" })
    .click()
  await page.getByRole("menuitem", { name: "Delete" }).click()
  const dialog = page.getByRole("dialog")
  await expect(dialog).toContainText("Delete old.example.com")
  await expect(dialog).toContainText("Caddy no longer has this certificate")
  await expect(dialog).toContainText("Caddy orders a new one")
  await dialog.getByRole("button", { name: "Delete" }).click()

  await expect(rows(page)).toHaveCount(caddyInventory.length - 1)
  expect(deletes).toEqual([{ method: "DELETE", body: { path: unused.path } }])
})

test("a Caddy certificate a project serves keeps Delete held, and says who serves it", async ({
  page,
}) => {
  await mockCaddyOnly(page)
  let asked = 0
  await page.route("**/api/v1/certificates/caddy", (route) => {
    asked++
    return json(route, {})
  })
  await page.goto("/proxy/certificates")
  await row(page, "shop.example.com")
    .getByRole("button", { name: "Actions for shop.example.com" })
    .click()
  const item = page.getByRole("menuitem", { name: "Delete" })
  await expect(item).toHaveAttribute("aria-disabled", "true")
  await item.hover()
  await expect(page.getByRole("tooltip")).toContainText("Served by shop · production")
  await item.click({ force: true })
  await expect(page.getByRole("dialog")).toHaveCount(0)
  expect(asked).toBe(0)
})

test("a Caddy certificate's page says who serves it and offers Delete once nothing does", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await mockCaddyOnly(page)
  await page.route("**/api/v1/certificates/history**", (route) =>
    json(route, { events: [], note: "" }),
  )
  await page.route("**/api/v1/proxy/vhosts", (route) => json(route, []))
  const deletes: unknown[] = []
  await page.route("**/api/v1/certificates/caddy", (route) => {
    deletes.push(route.request().postDataJSON())
    return json(route, { name: "old.example.com", path: unused.path, copies: [], keptCopies: [] })
  })

  await page.goto("/proxy/certificates/shop.example.com")
  const identity = page.locator("[data-slot='host-identity']")
  await expect(identity.getByRole("link", { name: "shop · production" })).toHaveAttribute(
    "href",
    "/deploy/3",
  )
  const picture = page.locator("[data-slot='certificate-picture']")
  await expect(picture).toContainText("shop · production")

  await page.goto("/proxy/certificates/old.example.com")
  await expect(identity).toContainText("unused")
  await identity.getByRole("button", { name: "More actions for old.example.com" }).click()
  await page.getByRole("menuitem", { name: "Delete" }).click()
  await page.getByRole("dialog").getByRole("button", { name: "Delete" }).click()
  await expect(page).toHaveURL(/\/proxy\/certificates$/)
  expect(deletes).toEqual([{ path: unused.path }])
})

test("Delete unused lists every source, ticks what is safe, and deletes Caddy's by path", async ({
  page,
}) => {
  const validImport = {
    ...certs[1],
    name: "kept.example.com",
    path: "/etc/ssl/just-dashboard/kept.example.com/fullchain.pem",
    domains: ["kept.example.com"],
    notAfter: new Date(Date.now() + 200 * 86_400_000).toISOString(),
    daysLeft: 200,
    expired: false,
  }
  // certs[1] is the fixture's expired import that nothing serves.
  await mockCaddyOnly(page, [...caddyInventory, certs[1], validImport])
  const calls: string[] = []
  await page.route("**/api/v1/certificates/caddy", (route) => {
    const path = (route.request().postDataJSON() as { path: string }).path
    calls.push(`caddy ${path}`)
    if (path === expired.path) {
      // A route claimed the name since the list was read.
      return route.fulfill({
        status: 409,
        contentType: "application/json",
        body: JSON.stringify({
          error: { code: "in_use", message: "gone.example.com is served by shop · production." },
        }),
      })
    }
    return json(route, { name: "old.example.com", path, copies: [], keptCopies: [] })
  })
  await page.route("**/api/v1/certificates/imported/**", (route) => {
    calls.push(`imported ${new URL(route.request().url()).pathname.split("/").at(-1)}`)
    return route.fulfill({ status: 204 })
  })
  await page.goto("/proxy/certificates")

  const notice = page.locator("[data-slot='unused-cleanup']")
  await expect(notice).toContainText("4 unused certificates")
  await notice.getByRole("button", { name: "Delete unused" }).click()

  const dialog = page.getByRole("dialog", { name: "Delete unused certificates" })
  const items = dialog.getByRole("list", { name: "Unused certificates" }).getByRole("listitem")
  await expect(items).toHaveCount(4)
  // Each names its source.
  await expect(items.filter({ hasText: "old.example.com" }).first()).toContainText("Caddy")
  await expect(items.filter({ hasText: "kept.example.com" })).toContainText("imported")
  // Caddy's are ticked; an import waits to be, expired or not, since a Caddy
  // route's copy may be refreshed from it.
  await expect(
    items.filter({ hasText: "kept.example.com" }).getByRole("checkbox"),
  ).not.toBeChecked()
  await expect(
    items
      .filter({ hasText: "old.example.com" })
      .filter({ hasText: "imported" })
      .getByRole("checkbox"),
  ).not.toBeChecked()
  await expect(items.filter({ hasText: "gone.example.com" }).getByRole("checkbox")).toBeChecked()
  await expect(dialog.getByRole("button", { name: "Delete 2 certificates" })).toBeVisible()

  // Ticked by hand, the expired import goes with them.
  await items
    .filter({ hasText: "old.example.com" })
    .filter({ hasText: "imported" })
    .getByRole("checkbox")
    .click()
  await dialog.getByRole("button", { name: "Delete 3 certificates" }).click()
  await expect(dialog).toBeHidden()
  expect(calls.sort()).toEqual(
    [`caddy ${unused.path}`, `caddy ${expired.path}`, "imported old.example.com"].sort(),
  )
  // The one a route serves by now is said, not passed over.
  await expect(page.getByText("Some certificates were not deleted")).toBeVisible()
  await expect(page.getByText(/gone\.example\.com is served by shop · production/)).toBeVisible()
})

test("leftover certificate copies are named in plain words", async ({ page }) => {
  await mockCaddyOnly(page)
  await page.route("**/api/v1/certificates/evidence", (route) => {
    if (route.request().method() === "DELETE") {
      return json(route, { removed: ["caddy-0da2f3126af1d760c968313b"], kept: [] })
    }
    return json(route, [
      {
        name: "caddy-0da2f3126af1d760c968313b",
        path: "/etc/ssl/just-dashboard/caddy-0da2f3126af1d760c968313b",
        domains: ["retired.example.com"],
        notAfter: yesterday,
      },
    ])
  })
  await page.goto("/proxy/certificates")
  const notice = page.locator("[data-slot='leftover-copies']")
  await expect(notice).toContainText("1 leftover certificate copy")
  await expect(page.getByText(/release cop|Prune evidence/)).toHaveCount(0)
  await notice.getByRole("button", { name: "Remove" }).click()
  const dialog = page.getByRole("dialog")
  await expect(dialog).toContainText("Remove leftover certificate copies")
  await expect(dialog).toContainText("retired.example.com")
  await dialog.getByRole("button", { name: "Remove 1 copy" }).click()
  await expect(page.getByText("Removed 1 copy")).toBeVisible()
})

test("a Caddy certificate overdue for renewal is amber in the list and on the line", async ({
  page,
}) => {
  await mockCaddyOnly(page, [
    {
      ...served,
      expiring: true,
      daysLeft: 12,
      notAfter: new Date(Date.now() + 12.5 * 86_400_000).toISOString(),
    },
    unused,
  ])
  await page.goto("/proxy/certificates")
  const identity = page.locator("[data-slot='host-identity']")
  await expect(identity).toContainText("1 expiring")
  await expect(row(page, "shop.example.com")).toContainText("12d left")
})
