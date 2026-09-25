import { expect, test, type Page, type Route } from "@playwright/test"

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

const now = new Date().toISOString()

const user = {
  authenticated: true,
  needsTotp: false,
  needsEnrollment: false,
  require2fa: false,
  capabilities: [
    "read",
    "service.control",
    "file.write",
    "terminal",
    "destructive",
    "system.admin",
  ],
  user: {
    id: 1,
    username: "operator",
    role: "admin",
    totpEnabled: true,
    disabled: false,
    mustChangePassword: false,
    lastLoginAt: now,
    createdAt: now,
  },
}

/** What the backend answers once it parses `nginx -v` rather than storing it. */
const availability = {
  nginx: true,
  nginxVersion: "nginx/1.26.3",
  caddy: false,
  caddyVersion: "",
  nginxDir: "/etc/nginx",
  caddyFile: "/etc/caddy/Caddyfile",
  certbot: true,
}

const snippet = "stream {\n    include /etc/nginx/streams/*.conf;\n}"

const inThirtyDays = new Date(Date.now() + 30 * 86_400_000).toISOString()
const yesterday = new Date(Date.now() - 86_400_000).toISOString()

const vhosts = [
  {
    name: "app.example.com",
    kind: "nginx",
    path: "/etc/nginx/sites-available/app.example.com",
    enabledPath: "/etc/nginx/sites-enabled/app.example.com",
    enabled: true,
    serverNames: ["app.example.com"],
    listen: ["443 ssl"],
    upstreams: ["http://127.0.0.1:3000"],
    tls: true,
    certPath: "/etc/letsencrypt/live/app.example.com/fullchain.pem",
    modified: now,
    size: 1200,
  },
  {
    name: "legacy.example.com",
    kind: "nginx",
    path: "/etc/nginx/sites-available/legacy.example.com",
    enabledPath: "/etc/nginx/sites-enabled/legacy.example.com",
    enabled: true,
    serverNames: ["legacy.example.com"],
    listen: ["80"],
    upstreams: ["http://127.0.0.1:8080"],
    tls: false,
    modified: now,
    size: 400,
  },
  {
    // A shared Docker Caddy route: no file on the host to edit.
    name: "just-dashboard-shop",
    kind: "caddy",
    path: "",
    enabled: true,
    serverNames: ["shop.example.com"],
    listen: ["80", "443"],
    upstreams: ["http://172.18.0.4:3000"],
    tls: true,
    modified: now,
    size: 0,
  },
]

const certs = [
  {
    name: "app.example.com",
    path: "/etc/letsencrypt/live/app.example.com/fullchain.pem",
    domains: ["app.example.com"],
    issuer: "R11",
    notBefore: yesterday,
    notAfter: inThirtyDays,
    daysLeft: 29,
    expired: false,
    expiring: true,
    selfSigned: false,
    source: "certbot",
    usedBy: ["app.example.com"],
  },
  {
    name: "old.example.com",
    path: "/etc/ssl/just-dashboard/old.example.com/fullchain.pem",
    domains: ["old.example.com"],
    issuer: "R11",
    notBefore: yesterday,
    notAfter: yesterday,
    daysLeft: -1,
    expired: true,
    expiring: false,
    selfSigned: false,
    source: "imported",
    usedBy: [],
  },
]

const ports = [
  {
    protocol: "tcp",
    address: "0.0.0.0",
    port: 443,
    pid: 812,
    process: "nginx",
    cmdline: "nginx: master process",
    user: "root",
    exposed: true,
  },
  {
    protocol: "tcp",
    address: "127.0.0.1",
    port: 3000,
    pid: 1400,
    process: "node",
    cmdline: "node server.js",
    user: "app",
    exposed: false,
  },
  {
    protocol: "tcp",
    address: "0.0.0.0",
    port: 5432,
    pid: 900,
    process: "postgres",
    cmdline: "postgres -D /var/lib/postgresql",
    user: "postgres",
    exposed: true,
  },
]

async function json(route: Route, body: unknown) {
  await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(body) })
}

async function mockProxy(page: Page, { included }: { included: boolean }) {
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname.replace(/^\/api\/v1/, "")
    switch (path) {
      case "/auth/session":
        return json(route, user)
      case "/dashboard/update":
        return json(route, { current: "0.6.7", latest: "0.6.7" })
      case "/proxy/status":
        return json(route, availability)
      case "/proxy/streams/":
        return json(route, { included, snippet, dir: "/etc/nginx/streams", streams: [] })
      case "/proxy/vhosts":
        return json(route, vhosts)
      case "/proxy/auth-files/":
        return json(route, [])
      case "/certificates/":
        return json(route, certs)
      case "/certificates/certbot":
        return json(route, {
          available: true,
          version: "certbot 2.9.0",
          certs: [
            {
              name: "app.example.com",
              domains: ["app.example.com"],
              expiry: inThirtyDays,
              daysLeft: 29,
              valid: true,
            },
          ],
          autoRenew: false,
          renewUnit: "certbot.timer",
        })
      case "/certificates/watched":
        return json(route, [])
      case "/certificates/dns-providers":
        return json(route, [])
      case "/systemd/nginx.service":
        return json(route, {
          unit: {
            name: "nginx.service",
            description: "A high performance web server",
            loadState: "loaded",
            activeState: "active",
            subState: "running",
            unitFileState: "enabled",
            enabled: true,
            activeSince: Math.floor(Date.now() / 1000) - 7200,
          },
          properties: {},
        })
      case "/ports":
        return json(route, ports)
      default:
        return route.fulfill({
          status: 503,
          contentType: "application/json",
          body: JSON.stringify({ error: { code: "not_available", message: "Not mocked" } }),
        })
    }
  })
}

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
  const installed = page.locator("[data-slot='cert-list'] [data-slot='row']")
  const row = installed.filter({ hasText: "old.example.com" })
  await expect(row.getByText("no site")).toBeVisible()
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
