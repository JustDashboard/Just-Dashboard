import { expect, test, type Page, type Route } from "@playwright/test"
import { availability, json, mockProxy, vhosts } from "./proxy-fixtures"

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
    // It was `Open ${meta}`: "Open renewal", "Open certificate" for a list.
    await expect(
      page.getByRole("button", { name: /^Open (renewal|certificate|stream)$/ }),
    ).toHaveCount(0)
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
