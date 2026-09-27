import { expect, test } from "@playwright/test"
import { certs, json, mockProxy, mockShowcase, PROXY_PAGES, snippet, user } from "./proxy-fixtures"

/**
 * The proxy pages, checked for the things a screenshot cannot.
 *
 * Two of these are the ways the pages once told an operator something that
 * was not true: a "+ New stream" button above a banner explaining that nginx
 * was not reading these files, and a Reverse proxy tile that read "nginx
 * nginx version: nginx/1.26.3". The rest came with the 0.6.7 redesign — the
 * attention list is folded from conditions somebody would act on rather than
 * from every exposed socket, a Docker Caddy route is not offered an editor it
 * cannot use, and the ports page filters what it lists.
 */

test("the reverse proxy is named once, in the identity line", async ({ page }) => {
  await mockProxy(page, { included: true })
  await page.goto("/proxy")

  // The engine's version appears in exactly one place — the identity line,
  // beside its name and drawn as its own logo. It used to be on a tile and
  // in the page description, and both copies were wrong.
  const identity = page.locator("[data-slot='host-identity']")
  await expect(identity.getByText("nginx/1.26.3")).toHaveCount(1)
  await expect(identity.locator("img[src='/logos/nginx.svg']")).toHaveCount(1)
  await expect(page.getByText(/nginx version:/)).toHaveCount(0)
  // The unit's state sits beside it, read through the Services API, and
  // certbot is drawn as what it issues.
  await expect(identity.getByText(/running for/)).toBeVisible()
  await expect(identity.locator("img[src='/logos/lets-encrypt.svg']")).toHaveCount(1)
  // And the engine's verbs are at the line's right end.
  await expect(identity.getByRole("button", { name: "Test config" })).toBeVisible()
  await expect(identity.getByRole("button", { name: "Reload" })).toBeVisible()
})

test("the overview's sites are cards drawn as the engine serving them", async ({ page }) => {
  await mockProxy(page, { included: true })
  await page.goto("/proxy")

  const sites = page.getByRole("list", { name: "Sites" })
  const cards = sites.locator("[data-slot='choice-row']")
  await expect(cards).toHaveCount(3)
  await expect(
    cards.filter({ hasText: "app.example.com" }).locator("img[src='/logos/nginx.svg']"),
  ).toHaveCount(1)
  await expect(
    cards.filter({ hasText: "just-dashboard-shop" }).locator("img[src='/logos/caddy.svg']"),
  ).toHaveCount(1)
  // A site whose certificate is certbot's says so with Let's Encrypt's mark.
  await expect(
    cards.filter({ hasText: "app.example.com" }).locator("img[src='/logos/lets-encrypt.svg']"),
  ).toHaveCount(1)
})

test("the overview's attention list is folded from conditions worth acting on", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  await page.goto("/proxy")

  // An expired certificate, a renewal timer that is off, and a site proxying
  // an application in plain text are findings.
  await expect(page.getByText("old.example.com has expired")).toBeVisible()
  await expect(page.getByText("Nothing is scheduled to renew certbot's certificates")).toBeVisible()
  await expect(
    page.getByText("legacy.example.com serves an application in plain text"),
  ).toBeVisible()
  // A database on every interface is one too; nginx on 443 is not.
  await expect(page.getByText("PostgreSQL answers on every interface")).toBeVisible()
  await expect(page.getByText(/^nginx answers on every interface/)).toHaveCount(0)
})

test("a Docker Caddy route is listed without an editor it cannot use", async ({ page }) => {
  await mockProxy(page, { included: true })
  await page.goto("/proxy/sites")

  // One card per site, at every width: nothing is rendered twice and hidden.
  const cards = page.locator("[data-slot='choice-row']")
  await expect(cards).toHaveCount(3)
  await expect(cards.filter({ hasText: "just-dashboard-shop" })).toBeVisible()
  // The nginx site has its raw file behind an inline verb; the container
  // route, having no file on the host, does not — and has no arrow either,
  // since there is nothing for the card to open.
  await expect(
    cards.filter({ hasText: "app.example.com" }).getByRole("button", { name: "Raw config" }),
  ).toHaveCount(1)
  await expect(
    cards.filter({ hasText: "just-dashboard-shop" }).getByRole("button", { name: "Raw config" }),
  ).toHaveCount(0)
  await expect(
    cards.filter({ hasText: "just-dashboard-shop" }).getByRole("button", { name: /^Open / }),
  ).toHaveCount(0)
  // Worst first: the site proxying an application in plain text is under
  // the attention rule, above the two on TLS.
  await expect(page.getByText("Needs attention")).toBeVisible()
  await expect(cards.first()).toContainText("legacy.example.com")
  // The four readings sit on the page rather than in a box.
  await expect(page.getByText("Plain HTTP", { exact: true }).first()).toBeVisible()
})

test("the raw editor reads its file afresh on every opening", async ({ page }) => {
  await mockProxy(page, { included: true })
  // A new revision on every read, so a reopened editor showing the first one
  // is a buffer kept across the close rather than the file on disk.
  let reads = 0
  await page.route("**/api/v1/proxy/config?**", (route) => {
    reads += 1
    return json(route, { content: `# revision-${reads}\n` })
  })
  await page.goto("/proxy/sites")
  const card = page.locator("[data-slot='choice-row']").filter({ hasText: "app.example.com" })
  await expect(card).toBeVisible()
  expect(reads).toBe(0)

  await card.getByRole("button", { name: "Raw config" }).click()
  const sheet = page.getByRole("dialog")
  const lines = sheet.locator(".monaco-editor .view-lines")
  await expect(lines).toContainText("revision-1")
  await lines.click()
  await page.keyboard.press("End")
  await page.keyboard.type("typed-then-closed")
  await expect(sheet.getByRole("button", { name: "Discard" })).toBeEnabled()
  await sheet.getByRole("button", { name: "Close", exact: true }).click()
  await expect(sheet).toHaveCount(0)

  await card.getByRole("button", { name: "Raw config" }).click()
  await expect(lines).toContainText("revision-2")
  await expect(lines).not.toContainText("typed-then-closed")
  await expect(sheet.getByRole("button", { name: "Discard" })).toBeDisabled()
  expect(reads).toBe(2)
})

test("the ports page filters what it lists and names a database on a public address", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  await page.goto("/proxy/ports")

  const table = page.getByRole("table")
  await expect(table.getByText("PostgreSQL · Every interface")).toBeVisible()
  await page.getByRole("button", { name: /^This server/ }).click()
  await expect(table.getByText("node server.js")).toBeVisible()
  await expect(table.getByText("nginx: master process")).toHaveCount(0)
  await page.getByPlaceholder("Port, process, user or address").fill("5432")
  // A search inside a filter that excludes the row finds nothing, honestly.
  await expect(page.getByText("No sockets match")).toBeVisible()
})

test("a stream cannot be created as though nginx were reading it", async ({ page }) => {
  await mockProxy(page, { included: false })
  await page.goto("/proxy/streams")

  // The banner is unchanged — it was already honest.
  await expect(page.getByText("nginx is not reading these yet")).toBeVisible()

  // The button no longer claims to create a working forward.
  await expect(page.getByRole("button", { name: "New stream" })).toHaveCount(0)
  await page.getByRole("button", { name: "Prepare a stream" }).click()

  // And the form repeats it at the point of commit, with the fix to hand.
  await expect(page.getByText("This will not forward anything yet")).toBeVisible()
  await expect(page.getByText(/include/).first()).toBeVisible()
  await expect(page.getByRole("button", { name: "Save for later" })).toBeVisible()
  await expect(page.getByRole("button", { name: "Save and reload" })).toHaveCount(0)
})

test("once nginx is reading them, the stream form promises a live forward again", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  await page.goto("/proxy/streams")

  await expect(page.getByText("nginx is not reading these yet")).toHaveCount(0)
  await page.getByRole("button", { name: "New stream" }).click()

  await expect(page.getByText("This will not forward anything yet")).toHaveCount(0)
  await expect(page.getByRole("button", { name: "Save and reload" })).toBeVisible()
})

test("the certificates page offers to turn the renewal timer on", async ({ page }) => {
  await mockProxy(page, { included: true })
  await page.goto("/proxy/certificates")

  await expect(page.getByText("Nothing is scheduled to renew these")).toBeVisible()
  await expect(page.getByRole("button", { name: "Turn it on" })).toBeVisible()
  // The installed list says which site uses a certificate, and draws each
  // as who signed it: R11 is one of Let's Encrypt's intermediates.
  const installed = page
    .getByRole("list", { name: "Installed certificates" })
    .locator("[data-slot='choice-row']")
  const row = installed.filter({ hasText: "old.example.com" })
  await expect(row.getByText("Used by no site")).toBeVisible()
  await expect(row.locator("img[src='/logos/lets-encrypt.svg']")).toHaveCount(1)
  // And the expired one is first, over a meter with nothing left in it.
  await expect(installed.first()).toContainText("old.example.com")
  await expect(row.getByRole("meter")).toHaveAttribute("aria-valuenow", "0")
})

test("a stream is drawn as the service its port is", async ({ page }) => {
  await mockProxy(page, { included: true })
  // Registered after the catch-all: Playwright tries the newest route first.
  await page.route("**/api/v1/proxy/streams/", (route) =>
    json(route, {
      included: true,
      snippet,
      dir: "/etc/nginx/streams",
      streams: [
        {
          name: "postgres-replica",
          listen: 5432,
          protocol: "tcp",
          upstream: "10.0.0.5:5432",
          proxyProtocol: false,
          allowFrom: [],
        },
        {
          name: "bastion",
          listen: 2222,
          protocol: "tcp",
          upstream: "10.0.0.9:22",
          proxyProtocol: false,
          allowFrom: ["10.0.0.0/8"],
        },
      ],
    }),
  )
  await page.goto("/proxy/streams")

  const cards = page.locator("[data-slot='choice-row']")
  await expect(cards).toHaveCount(2)
  // A database port open to anyone comes first, as Postgres.
  await expect(cards.first()).toContainText("postgres-replica")
  await expect(cards.first().locator("img[src='/logos/postgresql.svg']")).toHaveCount(1)
  await expect(cards.first().getByText("PostgreSQL to anyone")).toBeVisible()
  // A port nothing names keeps a glyph, not a guessed logo.
  await expect(cards.last().locator("img")).toHaveCount(0)
  await expect(cards.last().getByText("10.0.0.0/8")).toBeVisible()
})

for (const width of [390, 1280, 1720]) {
  test(`every proxy page has readable content and contained controls at ${width}`, async ({
    page,
  }, testInfo) => {
    await page.setViewportSize({ width, height: 1000 })
    await mockShowcase(page)
    const failures: string[] = []
    page.on("pageerror", (error) => failures.push(error.message))
    for (const path of PROXY_PAGES) {
      await page.goto(path)
      await expect(page.locator("[data-slot='stat-grid']")).toBeVisible()
      await page.waitForLoadState("networkidle")
      const overflow = await page
        .locator("[data-slot='page']")
        .evaluate((element) => element.scrollWidth > element.clientWidth + 1)
      expect(overflow, `${path} overflows at ${width}`).toBe(false)
      const unnamed = await page
        .locator("[data-slot='page'] button")
        .evaluateAll((buttons) =>
          buttons
            .filter(
              (button) =>
                (button as HTMLElement).offsetWidth > 0 &&
                !button.textContent?.trim() &&
                !button.getAttribute("aria-label") &&
                !button.getAttribute("aria-labelledby"),
            )
            .map((button) => button.outerHTML),
        )
      expect(unnamed).toEqual([])
      if (path === "/proxy/streams" && width >= 1280) {
        const cards = page
          .getByRole("list", { name: "Streams" })
          .locator("[data-slot='choice-row']")
        const first = await cards.nth(0).boundingBox()
        const second = await cards.nth(1).boundingBox()
        expect(Math.abs(first!.y - second!.y)).toBeLessThan(1)
        expect(Math.abs(first!.height - second!.height)).toBeLessThan(1)
      }
      await page.screenshot({
        path: testInfo.outputPath(`${path.split("?")[0].replaceAll("/", "-")}-${width}.png`),
        fullPage: true,
      })
    }
    expect(failures).toEqual([])
  })
}

test("certificate cards filter, reveal complete details and link to their owning site", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  await page.goto("/proxy/certificates")
  await page.getByPlaceholder("Certificate, domain or issuer").fill("app.example.com")
  const list = page.getByRole("list", { name: "Installed certificates" })
  await expect(list.locator("[data-slot='choice-row']")).toHaveCount(1)
  await list.getByRole("button", { name: "Inspect app.example.com" }).click()
  const detail = page.getByRole("dialog")
  await expect(detail.getByText(certs[0].path)).toBeVisible()
  await expect(detail.getByRole("link", { name: "app.example.com", exact: true })).toHaveAttribute(
    "href",
    "/proxy/sites?site=app.example.com",
  )
  await expect(detail.getByRole("link", { name: "TLS report" })).toHaveAttribute(
    "href",
    "/proxy/tls?domain=app.example.com",
  )
  await expect(detail.getByRole("button", { name: "Copy path" })).toBeVisible()
})

test("a watched service retains its port when opening the live report", async ({ page }) => {
  await mockShowcase(page)
  await page.goto("/proxy/certificates")
  const watched = page.getByRole("list", { name: "Watched domains" })
  await expect(watched.getByRole("link", { name: "Inspect mail.example.com" })).toHaveAttribute(
    "href",
    "/proxy/tls?domain=mail.example.com%3A993",
  )
})

test("site routing choices keep each kind's fields and the save guard", async ({ page }) => {
  await mockProxy(page, { included: true })
  await page.goto("/proxy/sites")
  await page.getByRole("button", { name: "New site", exact: true }).click()
  const sheet = page.getByRole("dialog")
  await expect(sheet.getByLabel("Send it to")).toBeVisible()
  await sheet.getByRole("button", { name: "Files", exact: true }).click()
  await expect(sheet.getByLabel("Directory", { exact: true })).toBeVisible()
  await expect(sheet.getByLabel("Send it to")).toHaveCount(0)
  await sheet.getByRole("button", { name: "A redirect", exact: true }).click()
  await expect(sheet.getByLabel("Redirect to", { exact: true })).toBeVisible()
  await expect(sheet.getByRole("button", { name: "Save and reload" })).toBeDisabled()
})

test("read-only users can inspect certificates without proxy mutation controls or network scans", async ({
  page,
}) => {
  await mockShowcase(page)
  await page.route("**/api/v1/auth/session", (route) =>
    json(route, { ...user, capabilities: ["read"], user: { ...user.user, role: "viewer" } }),
  )
  await page.goto("/proxy/sites")
  await expect(page.getByRole("button", { name: "New site", exact: true })).toHaveCount(0)
  await page.goto("/proxy/certificates")
  await expect(page.getByRole("button", { name: "Issue certificate", exact: true })).toHaveCount(0)
  await page.getByRole("button", { name: "Inspect app.example.com", exact: true }).click()
  await expect(page.getByRole("dialog").getByRole("link", { name: "TLS report" })).toHaveCount(0)
  await page.goto("/proxy/tls")
  await expect(page.getByRole("button", { name: "Scan", exact: true })).toBeDisabled()
  await expect(page.getByText("Scanning needs an administrator")).toBeVisible()
})

test("unreadable certificates keep their error in the detail surface without overflowing a phone", async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await mockProxy(page, { included: true })
  const message =
    "Could not read /etc/letsencrypt/live/an-extremely-long-hostname.example.com/fullchain.pem: permission denied"
  await page.route("**/api/v1/certificates/", (route) =>
    json(route, [
      {
        ...certs[0],
        domains: [],
        issuer: "",
        notBefore: "0001-01-01T00:00:00Z",
        notAfter: "0001-01-01T00:00:00Z",
        daysLeft: 0,
        expired: false,
        expiring: false,
        error: message,
      },
    ]),
  )
  await page.goto("/proxy/certificates")
  const inventory = page.getByRole("list", { name: "Installed certificates" })
  await expect(inventory.getByText("unreadable")).toBeVisible()
  await expect(inventory.getByRole("meter")).toHaveCount(0)
  expect(
    await page
      .locator("[data-slot='page']")
      .evaluate((element) => element.scrollWidth <= element.clientWidth + 1),
  ).toBe(true)
  await inventory.getByRole("button", { name: "Inspect app.example.com" }).click()
  const detail = page.getByRole("dialog")
  await expect(detail).toBeInViewport({ ratio: 1 })
  await expect(detail.getByText(message)).toBeVisible()
  for (const label of ["Issued", "Expires", "Self-signed"]) {
    await expect(detail.locator(`dt:has-text("${label}") + dd`)).toHaveText("—")
  }
  expect(await detail.evaluate((element) => element.scrollWidth <= element.clientWidth + 1)).toBe(
    true,
  )
})

test("proxy editors and lower sections remain usable on a phone", async ({ page }, testInfo) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await mockShowcase(page)
  for (const [path, action] of [
    ["/proxy/sites", "New site"],
    ["/proxy/streams", "New stream"],
    ["/proxy/certificates", "Issue certificate"],
  ]) {
    await page.goto(path)
    await page.getByRole("button", { name: action, exact: true }).click()
    const sheet = page.getByRole("dialog")
    await expect(sheet).toBeInViewport({ ratio: 1 })
    expect(await sheet.evaluate((element) => element.scrollWidth <= element.clientWidth + 1)).toBe(
      true,
    )
    await page.screenshot({
      path: testInfo.outputPath(`${action.replaceAll(" ", "-")}-390.png`),
      animations: "disabled",
    })
    await page.keyboard.press("Escape")
  }
  for (const path of ["/proxy/sites", "/proxy/certificates", "/proxy/tls?domain=app.example.com"]) {
    await page.goto(path)
    await page.waitForLoadState("networkidle")
    await page.locator("[data-slot='page']").locator("p").last().scrollIntoViewIfNeeded()
    await page.screenshot({
      path: testInfo.outputPath(`${path.split("?")[0].replaceAll("/", "-")}-bottom-390.png`),
    })
  }
})
