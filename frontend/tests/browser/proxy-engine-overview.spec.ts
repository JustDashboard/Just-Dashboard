import { expect, test, type Page, type Route } from "@playwright/test"
import { availability, json, mockProxy, user, vhosts } from "./proxy-fixtures"

/**
 * The overview and the engine it drives, for the ways they told an operator
 * something untrue: a list of findings that read "all within limits" while
 * the sites could not be read, a blank page when the proxy status could not,
 * a Restart that took nginx down over a broken file, Test and Reload running
 * the host's caddy for a Docker ingress, and a Caddy "running as a container"
 * that had not been started yet.
 */

function failWith(status: number, error: Record<string, unknown>) {
  return (route: Route) =>
    route.fulfill({ status, contentType: "application/json", body: JSON.stringify({ error }) })
}

async function mockStatus(page: Page, status: Record<string, unknown>) {
  await page.route("**/api/v1/proxy/status", (route) => json(route, status))
}

test("a source the overview cannot read is reported, never cleared", async ({ page }) => {
  await mockProxy(page, { included: true })
  let sitesFail = true
  await page.route("**/api/v1/proxy/vhosts", (route) =>
    sitesFail
      ? failWith(500, {
          code: "internal",
          message: "could not determine Docker ingress ownership",
        })(route)
      : json(route, vhosts),
  )
  await page.route(
    "**/api/v1/certificates/",
    failWith(500, { code: "internal", message: "internal error" }),
  )
  await page.goto("/proxy")

  // The list says what it could not see, and never the all-clear.
  await expect(page.getByText("Sites could not be read")).toBeVisible()
  await expect(page.getByText("Certificates could not be read")).toBeVisible()
  await expect(page.getByText(/all within limits/)).toHaveCount(0)
  // The tiles say so rather than showing a bare dash.
  const sitesTile = page.locator("a[aria-label='Sites'] [data-slot='stat-tile']")
  await expect(sitesTile).toContainText("couldn't read")
  // The reason is on the hint for a pointer resting on the tile.
  await expect(sitesTile.getByText("couldn't read")).toHaveAttribute(
    "title",
    "could not determine Docker ingress ownership",
  )
  await expect(page.locator("a[aria-label='Certificates'] [data-slot='stat-tile']")).toContainText(
    "couldn't read",
  )
  // Routes and expiry show the failure, not an empty host.
  await expect(
    page.getByRole("alert").filter({ hasText: "could not determine Docker ingress ownership" }),
  ).toBeVisible()
  await expect(page.getByText("Nothing configured yet")).toHaveCount(0)
  await expect(page.getByText("No certificates were found on this host.")).toHaveCount(0)

  // The finding's own button reads the source again.
  sitesFail = false
  await page.getByText("Sites could not be read").click()
  await page.getByRole("button", { name: "Try again" }).click()
  await expect(
    page.getByRole("list", { name: "Sites" }).locator("[data-slot='choice-row']"),
  ).toHaveCount(3)
  await expect(page.getByText("Sites could not be read")).toHaveCount(0)
  await expect(sitesTile).toContainText("3")
})

test("a source that fails after answering is not judged from its last answer", async ({ page }) => {
  await mockProxy(page, { included: true })
  let sitesFail = false
  await page.route("**/api/v1/proxy/vhosts", (route) =>
    sitesFail
      ? failWith(500, {
          code: "internal",
          message: "could not determine Docker ingress ownership",
        })(route)
      : json(route, vhosts),
  )
  await page.goto("/proxy")

  const sitesTile = page.locator("a[aria-label='Sites'] [data-slot='stat-tile']")
  const plainText = page.getByText("legacy.example.com serves an application in plain text")
  await expect(plainText).toBeVisible()
  await expect(sitesTile).toContainText("3")

  // Reload reads the sites again, and this time they fail. The poll keeps its
  // last answer, and the page went on judging it: the old plain-text finding
  // and the old count stayed up beside "Sites could not be read".
  sitesFail = true
  await page.locator("[data-slot='host-identity']").getByRole("button", { name: "Reload" }).click()
  await expect(page.getByText("nginx reloaded")).toBeVisible()
  await expect(page.getByText("Sites could not be read")).toBeVisible()
  await expect(plainText).toHaveCount(0)
  await expect(sitesTile).toContainText("—")
  await expect(sitesTile).toContainText("couldn't read")
  await expect(sitesTile).not.toContainText("3")
  await expect(
    page.getByRole("alert").filter({ hasText: "could not determine Docker ingress ownership" }),
  ).toBeVisible()
})

test("the overview says why when the proxy status cannot be read", async ({ page }) => {
  await mockProxy(page, { included: true })
  let statusFails = true
  await page.route("**/api/v1/proxy/status", (route) =>
    statusFails
      ? failWith(503, {
          code: "unavailable",
          message: "The proxy status could not be read",
          retryable: true,
        })(route)
      : json(route, availability),
  )
  await page.goto("/proxy")

  // It rendered nothing at all.
  await expect(
    page.getByRole("alert").filter({ hasText: "The proxy status could not be read" }),
  ).toBeVisible()
  await expect(page.locator("[data-slot='host-identity']")).toHaveCount(0)

  statusFails = false
  await page.getByRole("button", { name: "Try again" }).click()
  await expect(page.locator("[data-slot='host-identity']").getByText("nginx/1.26.3")).toBeVisible()
  await expect(
    page.getByRole("alert").filter({ hasText: "The proxy status could not be read" }),
  ).toHaveCount(0)
})

test("a finding's button names where it leads", async ({ page }) => {
  await mockProxy(page, { included: true })
  await page.goto("/proxy")

  // One finding open at a time, so each button found is that finding's.
  const open = async (title: string, label: string) => {
    await page.getByText(title).click()
    const button = page.getByRole("button", { name: label, exact: true })
    await expect(button).toBeVisible()
    // It was `Open ${meta}`: "Open renewal", "Open stream" for a list. "Open
    // certificate" is kept for a finding that links to one certificate.
    await expect(page.getByRole("button", { name: /^Open (renewal|stream)$/ })).toHaveCount(0)
    await page.getByText(title).click()
    await expect(button).toHaveCount(0)
  }
  await open("old.example.com has expired", "Open certificates")
  await open("Nothing is scheduled to renew certbot's certificates", "Open certificates")
  await open("PostgreSQL answers on every interface", "Open ports")

  await page.getByText("legacy.example.com serves an application in plain text").click()
  await page.getByRole("button", { name: "Open site", exact: true }).click()
  await expect(page).toHaveURL(/\/proxy\/sites\?site=legacy\.example\.com$/)
})

test("restart is refused with nginx's own reason when its configuration fails", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  const systemd: string[] = []
  await page.route("**/api/v1/systemd/nginx.service/*", (route) => {
    systemd.push(route.request().url())
    return json(route, {})
  })
  const engine: string[] = []
  await page.route("**/api/v1/proxy/engine/restart", (route) => {
    engine.push(route.request().method())
    return failWith(422, {
      code: "invalid_config",
      message:
        'nginx was not restarted: its configuration test failed.\nnginx: [emerg] unknown directive "frobnicate" in /etc/nginx/sites-enabled/app:3\nnginx: configuration file /etc/nginx/nginx.conf test failed',
    })(route)
  })
  await page.goto("/proxy")

  await page.getByRole("button", { name: "More nginx actions" }).click()
  await page.getByRole("menuitem", { name: "Restart nginx", exact: true }).click()
  const dialog = page.getByRole("dialog")
  await expect(dialog.getByText(/configuration is tested first/)).toBeVisible()
  await dialog.getByRole("button", { name: "Restart", exact: true }).click()

  await expect(page.getByText(/unknown directive "frobnicate"/)).toBeVisible()
  await expect(dialog).toBeVisible()
  expect(engine).toEqual(["POST"])
  expect(systemd).toEqual([])
})

test("a stopped nginx is started through the tested route", async ({ page }) => {
  await mockProxy(page, { included: true })
  await page.route("**/api/v1/systemd/nginx.service", (route) =>
    json(route, {
      unit: {
        name: "nginx.service",
        description: "A high performance web server",
        loadState: "loaded",
        activeState: "inactive",
        subState: "dead",
        unitFileState: "enabled",
        enabled: true,
      },
      properties: {},
    }),
  )
  const started: string[] = []
  await page.route("**/api/v1/proxy/engine/start", (route) => {
    started.push(route.request().method())
    return json(route, { action: "start", unit: "nginx.service", output: "" })
  })
  await page.goto("/proxy")

  await page.getByRole("button", { name: "More nginx actions" }).click()
  await page.getByRole("menuitem", { name: "Start nginx", exact: true }).click()
  await expect(page.getByText("nginx started")).toBeVisible()
  expect(started).toEqual(["POST"])
})

test("a running Docker Caddy ingress is tested and reloaded inside its container", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  await mockStatus(page, {
    ...availability,
    nginx: false,
    nginxVersion: "",
    caddy: true,
    caddyVersion: "v2.8.4",
    certbot: false,
    ingressContainer: "edge",
    ingressState: "running",
  })
  const kinds: string[] = []
  for (const path of ["test", "reload"]) {
    await page.route(`**/api/v1/proxy/${path}`, (route) => {
      kinds.push(`${path} ${route.request().postDataJSON().kind}`)
      return route.fallback()
    })
  }
  await page.goto("/proxy")

  const identity = page.locator("[data-slot='host-identity']")
  await expect(identity.getByText("runs as a container")).toBeVisible()
  await expect(identity.getByText("edge", { exact: true })).toBeVisible()
  // The host's Caddyfile path is not the ingress's.
  await expect(identity.getByText("/etc/caddy/Caddyfile")).toHaveCount(0)

  await identity.getByRole("button", { name: "Test config" }).click()
  await expect(page.getByText("Caddy's configuration is valid")).toBeVisible()
  await identity.getByRole("button", { name: "Reload" }).click()
  await expect(page.getByText("Caddy reloaded")).toBeVisible()
  expect(kinds).toEqual(["test caddy-ingress", "reload caddy-ingress"])
})

test("an ingress the first deployment would start is not offered controls", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 900 })
  await mockProxy(page, { included: true })
  await mockStatus(page, {
    ...availability,
    nginx: false,
    nginxVersion: "",
    caddy: false,
    certbot: false,
    ingressState: "provisionable",
  })
  await page.goto("/proxy")

  const identity = page.locator("[data-slot='host-identity']")
  await expect(identity.getByText("nothing found on this host")).toBeVisible()
  await expect(identity.getByText("a Caddy ingress starts with the first deployment")).toBeVisible()
  await expect(identity.getByText("runs as a container")).toHaveCount(0)
  await expect(page.getByRole("button", { name: "Test config" })).toHaveCount(0)
  await expect(page.getByRole("button", { name: "Reload" })).toHaveCount(0)
  const overflow = await page
    .locator("[data-slot='page']")
    .evaluate((element) => element.scrollWidth > element.clientWidth + 1)
  expect(overflow).toBe(false)
})

/** A TLS nginx site, for lists long enough to be cut short. */
function site(name: string, overrides: Record<string, unknown> = {}) {
  return {
    ...vhosts[0],
    name,
    path: `/etc/nginx/sites-available/${name}`,
    enabledPath: `/etc/nginx/sites-enabled/${name}`,
    serverNames: [name],
    ...overrides,
  }
}

/**
 * Ten routes named so the alphabet puts the plain-HTTP site last, with a
 * Caddyfile site and a Docker ingress route among them.
 */
const tenRoutes = [
  site("alpha.example.com"),
  {
    ...vhosts[0],
    name: "blog.example.com",
    kind: "caddy",
    path: "/etc/caddy/Caddyfile",
    enabledPath: undefined,
    serverNames: ["blog.example.com"],
    certPath: undefined,
  },
  vhosts[2],
  ...["one", "two", "three", "four", "five", "six"].map((n) => site(`${n}.example.com`)),
  site("zz-plain.example.com", { tls: false, listen: ["80"], certPath: undefined }),
]

test("the routes lead with what needs attention and say how many there are", async ({ page }) => {
  await mockProxy(page, { included: true })
  await page.route("**/api/v1/proxy/vhosts", (route) => json(route, tenRoutes))
  await page.goto("/proxy")

  const routes = page.getByRole("list", { name: "Sites" })
  const cards = routes.locator("[data-slot='choice-row']")
  await expect(cards).toHaveCount(8)
  // The list was the first eight by name and never said there were more.
  await expect(page.getByText("Showing 8 of 10", { exact: true })).toBeVisible()
  await expect(cards.first()).toContainText("zz-plain.example.com")
  // A Caddyfile site and a Docker ingress route were both "Caddy route".
  await expect(cards.filter({ hasText: "blog.example.com" })).toContainText("Caddyfile")
  await expect(cards.filter({ hasText: "just-dashboard-shop" })).toContainText(
    "Docker Caddy ingress",
  )

  // An administrator opens a site on the Sites page, and an ingress route —
  // which has no file there to open — as its live TLS report.
  await expect(routes.getByRole("link", { name: "Open alpha.example.com" })).toHaveAttribute(
    "href",
    "/proxy/sites?site=alpha.example.com",
  )
  await expect(routes.getByRole("link", { name: "Open blog.example.com" })).toHaveAttribute(
    "href",
    "/proxy/sites?site=blog.example.com",
  )

  // The count and the freshness line fit a phone beside what they sit with.
  await page.setViewportSize({ width: 390, height: 900 })
  await expect(page.getByText("Showing 8 of 10", { exact: true })).toBeVisible()
  await expect(page.getByText(/^Updated (just now|\d+s ago)$/)).toBeVisible()
  const overflow = await page
    .locator("[data-slot='page']")
    .evaluate((element) => element.scrollWidth > element.clientWidth + 1)
  expect(overflow).toBe(false)

  await routes.getByRole("link", { name: "TLS report for shop.example.com" }).click()
  await expect(page).toHaveURL(/\/proxy\/tls\?domain=shop\.example\.com$/)
})

test("a read-only account reads a route's file and is never sent to the site form", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  await page.route("**/api/v1/auth/session", (route) =>
    json(route, { ...user, capabilities: ["read"], user: { ...user.user, role: "viewer" } }),
  )
  const reads: string[] = []
  await page.route("**/api/v1/proxy/config?**", (route) => {
    reads.push(new URL(route.request().url()).searchParams.get("path") ?? "")
    return json(route, { content: "server {\n    listen 443 ssl;\n    # served-from-disk\n}\n" })
  })
  await page.goto("/proxy")

  const routes = page.getByRole("list", { name: "Sites" })
  await expect(routes.locator("[data-slot='choice-row']")).toHaveCount(3)
  // Nothing on the list leads to the Sites page's form.
  await expect(routes.locator("a[href*='/proxy/sites?site=']")).toHaveCount(0)
  // The ingress route has nothing a reader can open: its report needs an administrator.
  await expect(routes.getByRole("link", { name: /TLS report/ })).toHaveCount(0)
  await expect(routes.getByRole("button", { name: /just-dashboard-shop/ })).toHaveCount(0)

  await routes.getByRole("button", { name: "View app.example.com", exact: true }).click()
  const sheet = page.getByRole("dialog")
  // The editor loads on first use, which takes a while on a busy machine.
  await expect(sheet.locator(".monaco-editor .view-lines")).toContainText("served-from-disk", {
    timeout: 20_000,
  })
  expect(reads).toEqual(["/etc/nginx/sites-available/app.example.com"])
  await expect(sheet.getByRole("button", { name: /Save/ })).toHaveCount(0)
  await expect(sheet.getByRole("button", { name: "Test config" })).toHaveCount(0)
  await expect(page).toHaveURL(/\/proxy$/)
  await page.keyboard.press("Escape")
  await expect(sheet).toHaveCount(0)
})

test("the header says how old the page is, and Refresh reads every source again", async ({
  page,
}) => {
  await page.clock.install()
  await mockProxy(page, { included: true })
  const asked: string[] = []
  page.on("request", (request) => {
    const path = new URL(request.url()).pathname.replace(/^\/api\/v1/, "")
    if (request.method() === "GET") asked.push(path)
  })
  // The sites answer when told to, so the refresh can be seen in flight.
  let hold = Promise.resolve()
  let release = () => {}
  await page.route("**/api/v1/proxy/vhosts", async (route) => {
    await hold
    return json(route, vhosts)
  })
  await page.goto("/proxy")

  const header = page.locator("[data-slot='page-context']")
  // Fresh reads carry a few seconds' age at most — the clock runs on while a
  // busy machine renders.
  const fresh = header.getByText(/^Updated (just now|[5-9]s ago)$/)
  await expect(fresh).toBeVisible()
  // The age is the oldest reading's, as the clock moves.
  await page.clock.fastForward(20_000)
  await expect(header.getByText(/^Updated [2-5]\ds ago$/)).toBeVisible()

  hold = new Promise((resolve) => (release = resolve))
  asked.length = 0
  const refresh = header.getByRole("button", { name: "Refresh" })
  await refresh.click()
  await expect(header.getByText("Refreshing…")).toBeVisible()
  await expect(refresh).toHaveAttribute("aria-busy", "true")
  // Every source the page shows is asked again, not only the fast ones.
  await expect
    .poll(() => asked, { timeout: 15_000 })
    .toEqual(
      expect.arrayContaining([
        "/certificates/",
        "/certificates/certbot",
        "/ports",
        "/proxy/status",
        "/proxy/streams/",
        "/proxy/vhosts",
        "/systemd/nginx.service",
      ]),
    )
  // The one still out keeps it refreshing.
  await expect(header.getByText("Refreshing…")).toBeVisible()
  release()
  await expect(fresh).toBeVisible()
  await expect(refresh).not.toHaveAttribute("aria-busy", "true")
})

test("an expiry bar leads to its certificate", async ({ page }) => {
  await mockProxy(page, { included: true })
  await page.goto("/proxy")

  await page.getByRole("button", { name: "Show old.example.com in Certificates" }).click()
  await expect(page).toHaveURL(
    "/proxy/certificates?cert=%2Fetc%2Fssl%2Fjust-dashboard%2Fold.example.com%2Ffullchain.pem",
  )
})

test("a status that fails after answering is reported rather than drawn as current", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  let statusFails = false
  await page.route("**/api/v1/proxy/status", (route) =>
    statusFails
      ? failWith(503, { code: "unavailable", message: "The proxy status could not be read" })(route)
      : json(route, availability),
  )
  await page.goto("/proxy")
  const identity = page.locator("[data-slot='host-identity']")
  await expect(identity.getByText("nginx/1.26.3")).toBeVisible()

  statusFails = true
  await page.locator("[data-slot='page-context']").getByRole("button", { name: "Refresh" }).click()
  // The engine line stays — it is the last answer — and the list says so.
  await expect(page.getByText("The proxy status could not be read")).toBeVisible()
  await expect(identity.getByText("nginx/1.26.3")).toBeVisible()
  await expect(page.getByText(/all within limits/)).toHaveCount(0)

  statusFails = false
  await page.getByText("The proxy status could not be read").click()
  await page.getByRole("button", { name: "Try again" }).click()
  await expect(page.getByText("The proxy status could not be read")).toHaveCount(0)
})

test("a service state that cannot be read is not drawn from its last answer", async ({ page }) => {
  await mockProxy(page, { included: true })
  let unitFails = false
  await page.route("**/api/v1/systemd/nginx.service", (route) =>
    unitFails
      ? failWith(500, { code: "internal", message: "systemd did not answer" })(route)
      : route.fallback(),
  )
  await page.goto("/proxy")
  const identity = page.locator("[data-slot='host-identity']")
  await expect(identity.getByText(/running for/)).toBeVisible()

  unitFails = true
  await page.locator("[data-slot='page-context']").getByRole("button", { name: "Refresh" }).click()
  const unread = identity.getByText("couldn't read nginx.service")
  await expect(unread).toBeVisible()
  await expect(unread).toHaveAttribute("title", "systemd did not answer")
  await expect(identity.getByText(/running for/)).toHaveCount(0)
  await expect(identity.getByText("no service unit")).toHaveCount(0)
})
