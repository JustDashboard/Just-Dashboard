import { expect, test, type Page } from "@playwright/test"
import { deployment, expectSaved, json, mockProject, saveSettings } from "./deploy-fixture"

/**
 * Domains → Edge rules: the route's ordered rules, its request rate per
 * address and CrowdSec's bans, saved through the configuration PUT the rest
 * of the page writes through, with each rule's hits read from the request
 * record. On the Docker Caddy ingress the rate limit is the host's, and the
 * form hands it to the network gateway's own route.
 */

async function mockRequests(page: Page, driver: "nginx" | "docker-caddy") {
  await page.route("**/api/v1/deploy/7/requests**", (route) =>
    json(route, {
      status: "available",
      driver,
      latency: driver === "docker-caddy",
      complete: true,
      observedAt: new Date().toISOString(),
      entries: [],
      summary: { edgeRules: [{ value: "admin", count: 12, errors: 0 }] },
      coverage: {},
    }),
  )
}

test("rules are added, ordered and saved with a rate limit and CrowdSec, and each says how often it decided", async ({
  page,
}) => {
  await mockProject(page)
  await mockRequests(page, "nginx")
  const puts: { runtime: Record<string, unknown> }[] = []
  page.on("request", (request) => {
    if (request.method() === "PUT" && request.url().endsWith("/configuration")) {
      puts.push(request.postDataJSON())
    }
  })
  await page.goto("/deploy/7/settings/domains")
  const form = page.getByRole("form", { name: "Edge rules" })
  await expect(form).toBeVisible()
  await expect(form.getByText(/No rules: every request reaches the application/)).toBeVisible()

  // A rule that blocks the admin pages.
  await form.getByRole("button", { name: "Add rule" }).click()
  const first = form.getByRole("listitem", { name: /^Rule 1:/ })
  await first.getByLabel("Name", { exact: true }).fill("Admin")
  await expect(first.getByLabel("Name", { exact: true })).toHaveValue("admin")
  await first.locator("#edge-rule-0-path").fill("/admin")
  await expect(first.getByText("/admin… → 403")).toBeVisible()

  // A rule for scrapers: any path, a user agent and two methods, answered 429.
  await form.getByRole("button", { name: "Add rule" }).click()
  const second = form.getByRole("listitem", { name: /^Rule 2:/ })
  await second.getByLabel("Name", { exact: true }).fill("bots")
  await second.getByRole("combobox", { name: "Path match" }).click()
  await page.getByRole("option", { name: "Any path" }).click()
  await second.getByLabel("User agent", { exact: true }).fill("curl|python-requests")
  await second.getByRole("button", { name: "POST", exact: true }).click()
  await second.getByRole("button", { name: "GET", exact: true }).click()
  await second.getByRole("combobox", { name: "Then" }).click()
  await page.getByRole("option", { name: "Answer with a status" }).click()
  await second.getByLabel("Status", { exact: true }).fill("429")
  // Moved first: the first rule a request meets decides.
  await second.getByRole("button", { name: "Move bots up" }).click()
  await expect(form.getByRole("listitem", { name: /^Rule 1: bots/ })).toBeVisible()

  await form.getByRole("switch", { name: "Limit requests per address" }).click()
  await form.getByLabel("Requests", { exact: true }).fill("20")
  await form.getByLabel("Burst", { exact: true }).fill("40")
  await form.getByRole("switch", { name: "Refuse CrowdSec's bans" }).click()
  await expect(form.getByText("2 rules · 20 per second · CrowdSec")).toBeVisible()
  // nginx serves this route, so its limit is the route's own.
  await expect(form.getByText(/has no rate-limit module/)).toHaveCount(0)

  await saveSettings(page)
  await expectSaved(page)
  await expect.poll(() => puts.length).toBe(1)
  expect(puts[0].runtime.edgeRules).toEqual([
    {
      name: "bots",
      match: { methods: ["GET", "POST"], userAgent: "curl|python-requests" },
      action: { kind: "status", status: 429 },
    },
    { name: "admin", match: { pathPrefix: "/admin" }, action: { kind: "block", status: 403 } },
  ])
  expect(puts[0].runtime.rateLimit).toEqual({ requests: 20, per: "second", burst: 40 })
  expect(puts[0].runtime.crowdSec).toBe(true)

  // Read back, each rule with its hits in the last day.
  await page.reload()
  await expect(
    form.getByRole("listitem", { name: /^Rule 2: admin/ }).getByText("12 in 24 h"),
  ).toBeVisible()
  await expect(
    form.getByRole("listitem", { name: /^Rule 1: bots/ }).getByText("0 in 24 h"),
  ).toBeVisible()
})

test("on the Caddy ingress the limit is offered as the host's, and a refused rule is named on its row", async ({
  page,
}) => {
  await mockProject(page)
  await mockRequests(page, "docker-caddy")
  const limits: Record<string, unknown>[] = []
  await page.route("**/api/v1/network/protection/limits", async (route) => {
    limits.push(route.request().postDataJSON() as Record<string, unknown>)
    return json(route, { id: 9, name: "Ingress rate limit" })
  })
  await page.route("**/api/v1/deploy/7/environments/12/configuration", async (route) => {
    if (route.request().method() !== "PUT") return route.fallback()
    await route.fulfill({
      status: 422,
      contentType: "application/json",
      body: JSON.stringify({
        error: {
          code: "invalid_plan",
          message:
            "edge rule rule-1: the path pattern is not a regular expression both proxies read",
          field: "runtime.edgeRules",
        },
      }),
    })
  })
  await page.goto("/deploy/7/settings/domains")
  const form = page.getByRole("form", { name: "Edge rules" })
  await form.getByRole("switch", { name: "Limit requests per address" }).click()
  await expect(form.getByText(/ingress is Caddy, which has no rate-limit module/)).toBeVisible()
  await form.getByRole("button", { name: "Apply on the host" }).click()
  await expect
    .poll(() => limits)
    .toEqual([
      {
        name: `Ingress rate limit: ${deployment.name}`.slice(0, 64),
        protocol: "tcp",
        ports: "80,443",
        rate: 10,
        per: "second",
        burst: 20,
        perSource: true,
        maxConnections: 0,
        globalConnections: 0,
        action: "reject",
      },
    ])

  await form.getByRole("button", { name: "Add rule" }).click()
  const rule = form.getByRole("listitem", { name: /^Rule 1:/ })
  await rule.getByRole("combobox", { name: "Path match" }).click()
  await page.getByRole("option", { name: "Pattern" }).click()
  await rule.locator("#edge-rule-0-path").fill("^/(?!public)")
  await saveSettings(page)
  await expect(rule.getByText(/not a regular expression both proxies read/)).toBeVisible()
})
