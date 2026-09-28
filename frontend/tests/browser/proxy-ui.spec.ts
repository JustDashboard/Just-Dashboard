import { expect, test, type Page } from "@playwright/test"
import {
  availability,
  certs,
  json,
  mockProxy,
  mockShowcase,
  now,
  PROXY_PAGES,
  snippet,
  user,
} from "./proxy-fixtures"

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
  // route, having no file on the host, does not. Every card opens the site's
  // own page, the route's included — it has no file, and it has requests.
  await expect(
    cards.filter({ hasText: "app.example.com" }).getByRole("button", { name: "Raw config" }),
  ).toHaveCount(1)
  await expect(
    cards.filter({ hasText: "just-dashboard-shop" }).getByRole("button", { name: "Raw config" }),
  ).toHaveCount(0)
  await expect(
    cards
      .filter({ hasText: "just-dashboard-shop" })
      .getByRole("link", { name: "Open just-dashboard-shop" }),
  ).toHaveAttribute("href", "/proxy/sites/just-dashboard-shop")
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
  await expect(table.getByText("PostgreSQL exposed")).toBeVisible()
  await page.getByRole("button", { name: /^Loopback/ }).click()
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
  // A site is a page of its own now, which a reader without the form can open too.
  await expect(detail.getByRole("link", { name: "app.example.com", exact: true })).toHaveAttribute(
    "href",
    "/proxy/sites/app.example.com",
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

/*
 * A site's own page, the engine's log and certbot's renewals: each proxy page
 * that has a log reads it where it is, through the lens of what wrote it,
 * rather than sending the reader to the host Logs page with a path.
 */

const minuteAgo = (minutes: number) => new Date(Date.now() - minutes * 60_000).toISOString()
const ACCESS_LOG = "/var/log/nginx/app.example.com.access.log"
const ERROR_LOG = "/var/log/nginx/app.example.com.error.log"

/** app.example.com as its file reads back: the form's, logging where the form puts it. */
const siteRead = {
  spec: {
    name: "app.example.com",
    domains: ["app.example.com"],
    kind: "proxy",
    upstream: "http://127.0.0.1:3000",
    tls: true,
    certPath: "/etc/letsencrypt/live/app.example.com/fullchain.pem",
    keyPath: "/etc/letsencrypt/live/app.example.com/privkey.pem",
    forceHttps: true,
    hsts: true,
    http2: true,
    webSockets: true,
    gzip: true,
    blockExploits: true,
    securityHeaders: true,
    allowFrom: [],
    denyFrom: [],
    accessLog: true,
    accessLogPath: ACCESS_LOG,
    errorLogPath: ERROR_LOG,
    locations: [],
  },
  managed: true,
  content: "# Managed by Just Dashboard.\n",
  warnings: [],
}

/** Its last hour as nginx's combined format records it: no host, and no duration. */
const siteEntries = [
  {
    seq: 42,
    time: minuteAgo(1),
    method: "POST",
    path: "/cart",
    status: 502,
    size: 157,
    remoteIp: "203.0.113.9",
    proto: "HTTP/1.1",
    userAgent: "Mozilla/5.0 Chrome/140.0",
  },
  {
    seq: 41,
    time: minuteAgo(2),
    method: "GET",
    path: "/",
    status: 200,
    size: 612,
    remoteIp: "203.0.113.9",
    proto: "HTTP/1.1",
    userAgent: "Mozilla/5.0 Chrome/140.0",
  },
  {
    seq: 40,
    time: minuteAgo(3),
    method: "GET",
    path: "/.env",
    status: 404,
    size: 0,
    remoteIp: "198.51.100.7",
    proto: "HTTP/1.1",
    userAgent: "zgrab/0.x",
  },
]

const siteRequests = {
  status: "available",
  driver: "nginx",
  format: "nginx-combined",
  latency: false,
  complete: true,
  observedAt: now,
  entries: siteEntries,
  coverage: {
    exists: true,
    from: minuteAgo(3),
    to: minuteAgo(1),
    held: 3,
    complete: true,
    cursor: 42,
    refreshedAt: now,
  },
  summary: {
    total: 3,
    scanned: 3,
    classes: { "2xx": 1, "4xx": 1, "5xx": 1 },
    errorRate: 1 / 3,
    clientErrorRate: 1 / 3,
    bytes: 769,
    perMinute: 0.05,
    pages: 1,
    methods: [
      { value: "GET", count: 2, errors: 0 },
      { value: "POST", count: 1, errors: 1 },
    ],
    statuses: [
      { value: "200", count: 1, errors: 0 },
      { value: "404", count: 1, errors: 0 },
      { value: "502", count: 1, errors: 1 },
    ],
    paths: [
      { value: "/cart", count: 1, errors: 1 },
      { value: "/", count: 1, errors: 0 },
    ],
    hosts: [],
    clients: [
      { value: "203.0.113.9", count: 2, errors: 1 },
      { value: "198.51.100.7", count: 1, errors: 0, refused: 1, probes: 1 },
    ],
    agents: [{ value: "Chrome", count: 2, errors: 1 }],
    referers: [],
    probes: [{ value: "/.env", count: 1, errors: 0, refused: 1, probes: 1 }],
    scanners: [],
    buckets: Array.from({ length: 30 }, (_, i) => ({
      start: minuteAgo(30 - i),
      total: 4 + ((i * 7) % 9),
      counts: { "2xx": 3 + ((i * 7) % 9), "4xx": i % 5 === 0 ? 1 : 0, "5xx": i === 29 ? 1 : 0 },
    })),
    bucketSeconds: 60,
    truncated: false,
  },
}

/** What nginx wrote about the 502, read through the nginx-error lens. */
const refused = {
  text: '2026/09/27 10:00:01 [error] 812#812: *41 connect() failed (111: Connection refused) while connecting to upstream, client: 203.0.113.9, server: app.example.com, request: "POST /cart HTTP/1.1", upstream: "http://127.0.0.1:3000/cart", host: "app.example.com"',
  timestamp: minuteAgo(1),
  level: "error",
  event: "upstream_refused",
  attrs: {
    client: "203.0.113.9",
    host: "app.example.com",
    method: "POST",
    path: "/cart",
    upstream: "http://127.0.0.1:3000/cart",
    conn: "41",
    pid: "812",
  },
}
const handshake = {
  text: "2026/09/27 09:12:44 [warn] 812#812: *17 SSL_do_handshake() failed (SSL: error:0A00006C:SSL routines::bad key share) while SSL handshaking, client: 198.51.100.7, server: 0.0.0.0:443",
  timestamp: minuteAgo(50),
  level: "warn",
  event: "ssl_error",
  attrs: { client: "198.51.100.7", conn: "17", pid: "812" },
}

type LogMocks = {
  sockets: URLSearchParams[]
  searches: URLSearchParams[]
  requests: URLSearchParams[]
}

/** A window of site requests as the server answers it: narrowed by the families asked for. */
function requestWindow(window: typeof siteRequests, params: URLSearchParams) {
  const classes = params.get("classes")?.split(",") ?? []
  const entries = classes.length
    ? window.entries.filter((entry) => classes.includes(`${Math.floor(entry.status / 100)}xx`))
    : window.entries
  return { ...window, entries }
}

/**
 * The proxy's logs behind the log routes: the error log's failures for a
 * search, a reading's counts, and a line per live socket naming the source it
 * was opened on. Every socket and search is kept, so a test can say which
 * question a page asked.
 */
async function mockLogs(page: Page): Promise<LogMocks> {
  const recorded: LogMocks = { sockets: [], searches: [], requests: [] }
  await page.route("**/api/v1/proxy/sites/app.example.com", (route) => json(route, siteRead))
  await page.route("**/api/v1/proxy/sites/app.example.com/requests*", (route) => {
    const params = new URL(route.request().url()).searchParams
    recorded.requests.push(params)
    return json(route, requestWindow(siteRequests, params))
  })
  await page.route("**/api/v1/logs/**", (route) => {
    const url = new URL(route.request().url())
    const params = url.searchParams
    if (url.pathname.endsWith("/logs/source")) return json(route, {})
    if (url.pathname.endsWith("/logs/retention")) return json(route, {})
    if (!url.pathname.endsWith("/logs/search")) return json(route, {})
    recorded.searches.push(params)
    const result = (lines: object[], extra: object = {}) =>
      json(route, {
        lines,
        scanned: 240,
        matched: lines.length,
        truncated: false,
        complete: true,
        files: [],
        histogram: [],
        tookMillis: 3,
        ...extra,
      })
    if (params.get("histogramBy")) {
      // A reading's count: three refusals in the hour.
      return result([], {
        matched: 3,
        facets: {
          event: {
            values: [{ value: "upstream_refused", count: 3, errors: 3 }],
            distinct: 1,
            other: 0,
            missing: 0,
          },
        },
        histogram: [0, 1, 2].map((i) => ({
          start: minuteAgo(40 - i * 10),
          total: 1,
          counts: { upstream_refused: 1 },
        })),
      })
    }
    // The Errors view's day: its counts by kind, and its lines when no kind
    // is chosen — a chosen kind is a search of its own.
    if (params.get("facets") === "event") {
      return result(params.get("limit") === "1" ? [] : [handshake, refused], {
        matched: 2,
        facets: {
          event: {
            values: [
              { value: "upstream_refused", count: 1, errors: 1 },
              { value: "ssl_error", count: 1, errors: 0 },
            ],
            distinct: 2,
            other: 0,
            missing: 0,
          },
        },
      })
    }
    if (params.get("order") === "asc" && params.get("limit") === "20") return result([refused])
    return result([refused])
  })
  await page.routeWebSocket("**/api/v1/logs/stream**", (socket) => {
    const params = new URL(socket.url()).searchParams
    recorded.sockets.push(params)
    socket.send(
      JSON.stringify({
        type: "logs",
        data: [{ text: `a line from ${params.get("source")}`, level: "info" }],
        ts: Date.now(),
      }),
    )
  })
  return recorded
}

test("/proxy/sites/app.example.com reads the site's requests, and what nginx said about a failed one", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  const logs = await mockLogs(page)
  await page.goto("/proxy/sites/app.example.com")

  // What the site is, beside the way back and its verbs.
  await expect(page.getByRole("main").getByRole("link", { name: "Sites" })).toHaveAttribute(
    "href",
    "/proxy/sites",
  )
  const identity = page.locator("[data-slot='host-identity']")
  await expect(identity.getByText("reverse proxy")).toBeVisible()
  // Its two ends in their own columns under the line, as its card draws them.
  const route = page.getByRole("main").locator("[data-slot='proxy-route']")
  await expect(route.getByText("app.example.com", { exact: true })).toBeVisible()
  await expect(route.getByText("http://127.0.0.1:3000", { exact: true })).toBeVisible()
  await expect(identity.locator("img[src='/logos/nginx.svg']")).toHaveCount(1)
  await expect(page.getByRole("button", { name: "Edit" })).toBeVisible()

  // It opens on its requests, from its own record, with the hour's figures
  // over them.
  const views = page.getByRole("navigation", { name: "Log mode" })
  await expect(views.getByRole("button", { name: "Requests" })).toHaveAttribute(
    "aria-pressed",
    "true",
  )
  await expect(page.getByText("/cart", { exact: true })).toBeVisible()
  await expect(page.getByText("/.env", { exact: true })).toBeVisible()
  // The request record exports its own rows; the log's export beside it
  // would be a second Export about other lines.
  await expect(page.getByText("Export", { exact: true })).toHaveCount(1)
  const readings = page.locator("[data-slot='stat-grid']")
  await expect(readings.getByText("Server errors")).toBeVisible()
  await expect(readings.getByText("Upstream failures")).toBeVisible()
  // nginx's combined line has no duration, and the page does not draw one.
  await expect(page.getByText("Took", { exact: true })).toHaveCount(0)

  // A 502 opened says why, from the site's error log, in the second around it.
  await page.getByText("/cart", { exact: true }).click()
  const said = page.getByRole("region", { name: "Proxy said" })
  await expect(said.getByText("upstream refused")).toBeVisible()
  await expect(said.getByText(/Connection refused/)).toBeVisible()
  const around = logs.searches.find((q) => q.get("order") === "asc")!
  expect(around.get("source")).toBe(`file:${ERROR_LOG}`)
  expect(around.get("lens")).toBe("nginx-error")
  const t = Date.parse(siteEntries[0].time)
  expect(Date.parse(around.get("since")!)).toBe(t - 1000)
  expect(Date.parse(around.get("until")!)).toBe(t + 1000)

  // Nothing on the page sends the reader to the host Logs page.
  await expect(page.locator("a[href^='/logs']")).toHaveCount(0)

  // The error log opens in place on the minute around it.
  await said.getByRole("button", { name: "Open in the error log" }).click()
  await expect(page.getByRole("combobox", { name: "Site log" })).toHaveText(/Error log/)
  await expect(page.getByText(/^Around a failed request/)).toBeVisible()
  await expect
    .poll(() =>
      logs.searches.some(
        (q) =>
          q.get("source") === `file:${ERROR_LOG}` &&
          Date.parse(q.get("since") ?? "") === t - 60_000 &&
          Date.parse(q.get("until") ?? "") === t + 60_000,
      ),
    )
    .toBe(true)
  await page
    .getByRole("navigation", { name: "Log mode" })
    .getByRole("button", { name: "Requests" })
    .click()
  await expect(page.getByText(/^Around a failed request/)).toHaveCount(0)

  // The 5xx figure narrows the rows to the failed ones, by asking the
  // site's record for them.
  await readings.getByRole("button", { name: "Show the requests that failed" }).click()
  await expect(
    page.getByRole("button", { name: /^5xx/ }).and(page.locator("[aria-pressed='true']")),
  ).toBeVisible()
  await expect.poll(() => logs.requests.some((q) => q.get("classes") === "5xx")).toBe(true)
  await expect(page.getByText("/cart", { exact: true })).toBeVisible()
  await expect(page.getByText("/.env", { exact: true })).toHaveCount(0)
  await expect(page.getByRole("button", { name: "Show every request again" })).toBeVisible()
})

test("/proxy/sites/app.example.com reads its error log by what failed", async ({ page }) => {
  await mockProxy(page, { included: true })
  const logs = await mockLogs(page)
  await page.goto("/proxy/sites/app.example.com")

  const views = page.getByRole("navigation", { name: "Log mode" })
  await views.getByRole("button", { name: "Errors" }).click()
  // The strip names the log the view reads.
  await expect(page.getByRole("combobox", { name: "Site log" })).toHaveText(/Error log/)
  const search = logs.searches.find((q) => q.get("limit") === "1000")!
  expect(search.get("source")).toBe(`file:${ERROR_LOG}`)
  expect(search.get("levels")).toBe("critical,error,warn")
  // A chip per kind the lens names, with its count over the whole day.
  await expect(page.getByRole("button", { name: /^TLS/ })).toBeVisible()
  await expect(page.getByRole("button", { name: /^Rate limited/ })).toHaveCount(0)
  await expect(page.getByText(/SSL_do_handshake/)).toBeVisible()
  // A chip is a question of its own, so what it shows is every line of its
  // kind in the day rather than those among the newest thousand of all.
  await page.getByRole("button", { name: /^Upstream/ }).click()
  await expect(page.getByText(/SSL_do_handshake/)).toHaveCount(0)
  await expect(page.getByText(/Connection refused/)).toBeVisible()
  const upstream = logs.searches.find(
    (q) => q.getAll("f").includes("event:upstream_refused") && !q.get("histogramBy"),
  )!
  expect(upstream.get("limit")).toBe("1000")
  expect(upstream.get("levels")).toBe("critical,error,warn")
  // The counts still come from the day: the other kinds keep their chips.
  await expect(page.getByRole("button", { name: /^TLS/ })).toBeVisible()
  await expect(page.getByRole("button", { name: /^All/ })).toContainText("2")

  // The day is read again when asked, not on a timer: a poll swapped the
  // lines out from under the one being read, and rescanned a shared log.
  const asked = logs.searches.filter((q) => q.get("limit") === "1000").length
  await page.getByRole("button", { name: "Read the errors again" }).click()
  await expect
    .poll(() => logs.searches.filter((q) => q.get("limit") === "1000").length)
    .toBeGreaterThan(asked)

  // Live reads the error log through its lens, since that is the log named.
  await views.getByRole("button", { name: "Live" }).click()
  await expect(page.getByText(`a line from file:${ERROR_LOG}`)).toBeVisible()
  expect(logs.sockets.at(-1)!.get("lens")).toBe("nginx-error")
  await expect(page.locator("a[href^='/logs']")).toHaveCount(0)
})

test("a site's Logs verb opens its page rather than the host Logs page", async ({ page }) => {
  await mockProxy(page, { included: true })
  await mockLogs(page)
  await page.goto("/proxy/sites")

  const card = page.locator("[data-slot='choice-row']").filter({ hasText: "app.example.com" })
  await card.getByRole("button", { name: "More actions for app.example.com" }).click()
  await expect(page.getByRole("menuitem", { name: "Access log" })).toHaveCount(0)
  await page.getByRole("menuitem", { name: "Logs" }).click()
  await expect(page).toHaveURL(/\/proxy\/sites\/app\.example\.com$/)
})

test("/proxy reads the engine's own log inline", async ({ page }) => {
  await mockProxy(page, { included: true })
  const logs = await mockLogs(page)
  await page.goto("/proxy")

  await expect(page.getByRole("heading", { name: "Engine log" })).toBeVisible()
  await expect(page.getByText("a line from file:/var/log/nginx/error.log")).toBeVisible()
  expect(logs.sockets[0].get("lens")).toBe("nginx-error")
  // nginx's two files and its unit are one picker.
  await page.getByRole("combobox", { name: "Engine log" }).click()
  await expect(page.getByRole("option", { name: "access.log" })).toBeVisible()
  await expect(page.getByRole("option", { name: "nginx.service" })).toBeVisible()
  await page.keyboard.press("Escape")
  // Service details is still the unit's own page.
  await page.getByRole("button", { name: "More nginx actions" }).click()
  await expect(page.getByRole("menuitem", { name: "Service details" })).toBeVisible()
})

test("/proxy/certificates reads every renewal certbot ran", async ({ page }) => {
  await mockProxy(page, { included: true })
  const logs = await mockLogs(page)
  await page.goto("/proxy/certificates")

  await expect(page.getByRole("heading", { name: "Renewals" })).toBeVisible()
  await expect(
    page.getByText("a line from file:/var/log/letsencrypt/letsencrypt.log"),
  ).toBeVisible()
  expect(logs.sockets[0].get("lens")).toBe("certbot")
  // certbot's own quick views, and the job consoles' history beside them.
  await expect(page.getByRole("button", { name: /^Failures/ })).toBeVisible()
  await page.getByRole("combobox", { name: "Renewal log" }).click()
  await expect(page.getByRole("option", { name: "certbot.service" })).toBeVisible()
})

test("/proxy/sites/app.example.com's upstream figure opens its failures, and lets go when pressed again", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  await mockLogs(page)
  await page.goto("/proxy/sites/app.example.com")

  const readings = page.locator("[data-slot='stat-grid']")
  const views = page.getByRole("navigation", { name: "Log mode" })
  await readings.getByRole("button", { name: /upstream failures/ }).click()
  await expect(views.getByRole("button", { name: "Errors" })).toHaveAttribute(
    "aria-pressed",
    "true",
  )
  const chip = (name: RegExp) =>
    page.getByRole("button", { name }).and(page.locator("[aria-pressed='true']"))
  await expect(chip(/^Upstream/)).toBeVisible()
  // Pressed, the figure says it lets go — and does.
  await readings
    .getByRole("button", { name: "Show every line again, not only the upstream failures" })
    .click()
  await expect(chip(/^All/)).toBeVisible()
  await expect(views.getByRole("button", { name: "Errors" })).toHaveAttribute(
    "aria-pressed",
    "true",
  )
})

test("/proxy/sites/legacy.example.com says why it has no requests to read, and reads nginx's shared error log for its names", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  const logs = await mockLogs(page)
  // Written by hand, with no access_log or error_log of its own.
  await page.route("**/api/v1/proxy/sites/legacy.example.com", (route) =>
    json(route, {
      spec: {
        ...siteRead.spec,
        name: "legacy.example.com",
        domains: ["legacy.example.com"],
        upstream: "http://127.0.0.1:8080",
        tls: false,
        accessLogPath: undefined,
        errorLogPath: undefined,
      },
      managed: false,
      content: "server {\n    listen 80;\n    server_name legacy.example.com;\n}\n",
      warnings: [],
    }),
  )
  await page.goto("/proxy/sites/legacy.example.com")

  await expect(page.getByText("This site has no access log of its own")).toBeVisible()
  await expect(page.getByText(/nginx's shared log, syslog or a stream/)).toBeVisible()
  // Nothing records its requests: no Requests view, and no figures over one.
  const views = page.getByRole("navigation", { name: "Log mode" })
  await expect(views.getByRole("button", { name: "Requests" })).toHaveCount(0)
  await expect(views.getByRole("button", { name: "Errors" })).toHaveAttribute(
    "aria-pressed",
    "true",
  )
  await expect(page.locator("[data-slot='stat-grid']")).toHaveCount(0)
  // Its errors are nginx's shared log's, narrowed to its name.
  await expect
    .poll(() =>
      logs.searches.some(
        (q) =>
          q.get("source") === "file:/var/log/nginx/error.log" &&
          q.getAll("f").includes("host:legacy.example.com"),
      ),
    )
    .toBe(true)
  await expect(page.getByText(/Connection refused/)).toBeVisible()

  // The form that turns its log on is a press away, in the notice itself.
  const notice = page
    .locator("div")
    .filter({ has: page.getByText("This site has no access log of its own", { exact: true }) })
    .last()
  await notice.getByRole("button", { name: "Edit" }).click()
  await expect(page.getByText("Edit legacy.example.com")).toBeVisible()
})

/** A route on the Docker Caddy ingress: Caddy's JSON, which records the host and the time taken. */
const shopEntries = [
  {
    seq: 7,
    time: minuteAgo(1),
    method: "POST",
    path: "/cart",
    host: "shop.example.com",
    status: 502,
    size: 0,
    durationMs: 3.2,
    remoteIp: "203.0.113.9",
    proto: "HTTP/2.0",
    tls: true,
    userAgent: "Mozilla/5.0 Chrome/140.0",
  },
  {
    seq: 6,
    time: minuteAgo(2),
    method: "GET",
    path: "/",
    host: "shop.example.com",
    status: 200,
    size: 2048,
    durationMs: 41,
    remoteIp: "203.0.113.9",
    proto: "HTTP/2.0",
    tls: true,
    userAgent: "Mozilla/5.0 Chrome/140.0",
  },
]

const caddyRefused = {
  text: '{"level":"error","logger":"http.log.error","msg":"dial tcp 172.18.0.4:3000: connect: connection refused","request":{"method":"POST","host":"shop.example.com","uri":"/cart"},"status":502}',
  timestamp: minuteAgo(1),
  level: "error",
  event: "upstream_refused",
  message: "dial tcp 172.18.0.4:3000: connect: connection refused",
  attrs: { host: "shop.example.com", upstream: "172.18.0.4:3000", status: "502" },
}

const caddyCertFailed = {
  text: '{"level":"error","logger":"tls.obtain","msg":"will retry","identifier":"shop.example.com","error":"[shop.example.com] Obtain: too many certificates already issued"}',
  timestamp: minuteAgo(30),
  level: "error",
  event: "cert_failed",
  message: "will retry",
  fields: {
    logger: "tls.obtain",
    identifier: "shop.example.com",
    error: "[shop.example.com] Obtain: too many certificates already issued",
  },
  attrs: { domain: "shop.example.com", error: "too many certificates already issued" },
}

test("/proxy/sites/just-dashboard-shop reads its route's requests, and what Caddy said about a failed one", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  const logs = await mockLogs(page)
  await page.route("**/api/v1/proxy/status", (route) =>
    json(route, { ...availability, caddy: true, ingressContainer: "edge" }),
  )
  await page.route("**/api/v1/proxy/sites/just-dashboard-shop/requests*", (route) =>
    json(
      route,
      requestWindow(
        {
          ...siteRequests,
          driver: "docker-caddy",
          format: "caddy-json",
          latency: true,
          entries: shopEntries as typeof siteEntries,
        },
        new URL(route.request().url()).searchParams,
      ),
    ),
  )
  // The ingress's output: the failure under the 502, and a renewal that
  // failed for the route's name, which Caddy writes by domain, not by host.
  await page.route("**/api/v1/logs/search*", (route) => {
    const params = new URL(route.request().url()).searchParams
    if (params.get("source") !== "docker:edge") return route.fallback()
    logs.searches.push(params)
    const lines = params.getAll("f").some((f) => f.startsWith("domain:"))
      ? [caddyCertFailed]
      : params.get("order") === "asc"
        ? [caddyRefused]
        : undefined
    if (!lines) return route.fallback()
    return json(route, {
      lines,
      scanned: 40,
      matched: lines.length,
      truncated: false,
      complete: true,
      files: [],
      histogram: [],
      tookMillis: 2,
    })
  })
  await page.goto("/proxy/sites/just-dashboard-shop")

  const identity = page.locator("[data-slot='host-identity']")
  await expect(identity.getByText("a deployment route")).toBeVisible()
  const views = page.getByRole("navigation", { name: "Log mode" })
  await expect(views.getByRole("button", { name: "Requests" })).toHaveAttribute(
    "aria-pressed",
    "true",
  )
  // The ingress is every route's, so its figures are nobody's in particular.
  const readings = page.locator("[data-slot='stat-grid']")
  await expect(readings.getByText("Server errors")).toBeVisible()
  await expect(readings.getByText("Upstream failures")).toHaveCount(0)

  await page.getByText("/cart", { exact: true }).click()
  const said = page.getByRole("region", { name: "Proxy said" })
  await expect(said.getByText("upstream refused")).toBeVisible()
  await expect(said.getByText(/connection refused/)).toBeVisible()
  const around = logs.searches.find(
    (q) => q.get("source") === "docker:edge" && q.get("order") === "asc",
  )!
  expect(around.get("lens")).toBe("caddy")
  expect(around.getAll("f")).toContain("host:shop.example.com")

  // Opened in Caddy's log, the minute around it is narrowed the same way —
  // the ingress's log is every route's — once the log's own vocabulary has
  // replaced the access log's.
  await said.getByRole("button", { name: "Open in Caddy's log" }).click()
  await expect(page.getByText(/^Around a failed request/)).toBeVisible()
  await expect
    .poll(
      () =>
        logs.searches
          .find((q) => q.get("source") === "docker:edge" && q.get("limit") === "3000")
          ?.getAll("f") ?? [],
    )
    .toContain("host:shop.example.com")

  // Its Errors are the ingress's lines about its names, and the renewal that
  // failed for them, which a narrowing by host would have hidden.
  await views.getByRole("button", { name: "Errors" }).click()
  await expect(page.getByRole("button", { name: /^Certificates/ })).toContainText("1")
  await expect(page.getByRole("button", { name: /^All/ })).toContainText("3")
  await expect(page.getByText("will retry")).toBeVisible()
  await expect(page.getByText("cert failed")).toBeVisible()
  const day = logs.searches.find(
    (q) => q.get("source") === "docker:edge" && q.get("facets") === "event",
  )!
  expect(day.getAll("f")).toEqual(["host:shop.example.com"])
  await expect(page.locator("a[href^='/logs']")).toHaveCount(0)
})

test("the Caddyfile's page reads Caddy's journal whole, since its names are addresses no line carries", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  const logs = await mockLogs(page)
  await page.route("**/api/v1/proxy/status", (route) =>
    json(route, { ...availability, nginx: false, caddy: true, caddyVersion: "v2.8.4" }),
  )
  await page.route("**/api/v1/proxy/vhosts", (route) =>
    json(route, [
      {
        name: "Caddyfile",
        kind: "caddy",
        path: "/etc/caddy/Caddyfile",
        enabled: true,
        serverNames: [":80", "https://blog.example.com", "shop.example.com:443"],
        listen: [],
        upstreams: ["localhost:8080"],
        tls: true,
        modified: now,
        size: 300,
      },
    ]),
  )
  await page.goto("/proxy/sites/Caddyfile")

  await expect(
    page.locator("[data-slot='host-identity']").getByText("a site in the Caddyfile"),
  ).toBeVisible()
  const views = page.getByRole("navigation", { name: "Log mode" })
  await expect(views.getByRole("button", { name: "Requests" })).toHaveCount(0)
  await expect(views.getByRole("button", { name: "Errors" })).toHaveAttribute(
    "aria-pressed",
    "true",
  )
  await expect
    .poll(() =>
      logs.searches.find(
        (q) => q.get("source") === "journal:caddy.service" && q.get("facets") === "event",
      ),
    )
    .toBeTruthy()
  for (const search of logs.searches.filter((q) => q.get("source") === "journal:caddy.service")) {
    expect(search.getAll("f")).toEqual([])
  }
  await expect(page.getByText(/Connection refused/)).toBeVisible()
})

test("/proxy reads the Docker Caddy ingress beside nginx, once it is there", async ({ page }) => {
  await mockProxy(page, { included: true })
  const logs = await mockLogs(page)
  await page.route("**/api/v1/proxy/status", (route) =>
    json(route, { ...availability, caddy: true, ingressContainer: "edge" }),
  )
  await page.goto("/proxy")

  await expect(page.getByRole("heading", { name: "Engine log" })).toBeVisible()
  await page.getByRole("combobox", { name: "Engine log" }).click()
  await expect(page.getByRole("option", { name: "error.log" })).toBeVisible()
  await expect(page.getByRole("option", { name: "edge" })).toBeVisible()
  await page.getByRole("option", { name: "edge" }).click()
  await expect(page.getByText("a line from docker:edge")).toBeVisible()
  expect(logs.sockets.at(-1)!.get("lens")).toBe("caddy")
})

test("/proxy does not offer an ingress that is only the name one would have", async ({ page }) => {
  await mockProxy(page, { included: true })
  await mockLogs(page)
  // Nothing provisioned: the status names the container it would create.
  await page.route("**/api/v1/proxy/status", (route) =>
    json(route, {
      ...availability,
      nginx: false,
      nginxVersion: "",
      caddy: true,
      ingressContainer: "just-dashboard-ingress",
    }),
  )
  const probed: string[] = []
  await page.route("**/api/v1/logs/source*", (route) => {
    probed.push(new URL(route.request().url()).searchParams.get("source") ?? "")
    return route.fulfill({
      status: 404,
      contentType: "application/json",
      body: JSON.stringify({ error: { code: "not_found", message: "No such container" } }),
    })
  })
  await page.goto("/proxy")

  await expect(page.getByRole("list", { name: "Sites" })).toBeVisible()
  await expect.poll(() => probed).toContain("docker:just-dashboard-ingress")
  await expect(page.getByRole("heading", { name: "Engine log" })).toHaveCount(0)
})
