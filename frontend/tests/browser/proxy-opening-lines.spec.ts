import { expect, test, type Page } from "@playwright/test"
import { mockCarrying } from "./fixtures/proxy/stream-traffic"
import { fleet } from "./fixtures/proxy/tls-views"
import { json, mockShowcase } from "./proxy-fixtures"
import { risen } from "./risen"

/**
 * The section's pages open alike. Each was overhauled on its own, and they
 * came out with three openings: the overview's framed bar of five controls,
 * six lines with their verdict and commands in a cluster at the far end, and
 * two of those under a "Proxy & TLS" link the others did not draw. Every one
 * is laid out as a project's header now: a tile, the name with its state
 * beside it, the facts under them, one command at the end and the rest
 * behind a menu.
 */

const PAGES: { name: string; path: string; ready: string | RegExp }[] = [
  { name: "the overview", path: "/proxy", ready: /^up \d/ },
  { name: "Alerts", path: "/proxy/alerts", ready: "No rules on" },
  { name: "Streams", path: "/proxy/streams", ready: /forwards?$/ },
  { name: "Ports", path: "/proxy/ports", ready: /listening$/ },
  { name: "Certificates", path: "/proxy/certificates", ready: /certificates?$/ },
  { name: "the TLS report", path: "/proxy/tls?domain=app.example.com", ready: /^grade / },
  { name: "TLS for every site", path: "/proxy/tls?view=fleet", ready: /names?$/ },
  { name: "the request tester", path: "/proxy/tls?tool=request", ready: "Nothing sent" },
  { name: "Traffic", path: "/proxy/traffic", ready: "last hour" },
  { name: "Configuration", path: "/proxy/config", ready: /folders?$/ },
  { name: "Tunnel", path: "/proxy/tunnel", ready: /edge connections?$/ },
]

async function mockSection(page: Page) {
  await mockShowcase(page)
  await mockCarrying(page)
  await page.route("**/api/v1/proxy/alerts", (route) =>
    json(route, { rules: [], subjects: [], history: [], intervalSeconds: 300 }),
  )
  await page.route("**/api/v1/deploy/notifications", (route) => json(route, []))
  await page.route("**/api/v1/proxy/traffic", (route) =>
    json(route, {
      observedAt: new Date().toISOString(),
      sites: [
        {
          site: "app.example.com",
          file: "/etc/nginx/sites-available/app.example.com",
          engine: "nginx",
          status: "available",
          requests: 120,
          errorRate: 0,
          bytes: 120_000,
          complete: true,
          points: [],
        },
      ],
    }),
  )
  await page.route("**/api/v1/certificates/scans/latest", (route) => json(route, fleet))
}

for (const width of [1280, 390]) {
  test(`every page of the section opens as a project's header, at ${width}`, async ({ page }) => {
    test.slow()
    await page.setViewportSize({ width, height: 900 })
    await mockSection(page)
    const titles = new Set<string>()
    const tiles = new Set<number>()
    for (const { name, path, ready } of PAGES) {
      await test.step(name, async () => {
        await page.goto(path)
        const identity = page.locator("[data-slot='host-identity']").first()
        await expect(identity.getByText(ready).first()).toBeVisible()
        await expect(identity, name).toHaveAttribute("data-layout", "project")
        // Nothing stands over the line: no way back, no row of buttons.
        await expect(page.locator("[data-slot='page-context']"), name).toHaveCount(0)
        // One command wears the brand's face, at most.
        expect(
          await identity.locator("button[data-variant='default']").count(),
          `${name}: brand commands`,
        ).toBeLessThanOrEqual(1)
        await risen(page)
        const box = await identity.boundingBox()
        // The line is the first thing on the page.
        const top = await page.locator("[data-slot='page']").boundingBox()
        expect(box!.y - top!.y, `${name}: under the page's edge`).toBeLessThan(48)
        // One size of name and one size of tile on every page.
        titles.add(
          await identity
            .locator("p")
            .first()
            .evaluate((el) => getComputedStyle(el).fontSize),
        )
        tiles.add(
          Math.round(
            await identity.evaluate(
              (el) => (el.firstElementChild as HTMLElement).getBoundingClientRect().height,
            ),
          ),
        )
        const overflow = await page
          .locator("[data-slot='page']")
          .evaluate((element) => element.scrollWidth > element.clientWidth + 1)
        expect(overflow, `${name}: no sideways scroll`).toBe(false)
      })
    }
    expect([...titles]).toHaveLength(1)
    expect([...tiles]).toEqual([40])
  })
}

test("a line with views under it draws no rule of its own", async ({ page }) => {
  await mockSection(page)
  for (const path of [
    "/proxy/certificates",
    "/proxy/config",
    "/proxy/tls?domain=app.example.com",
    "/proxy/tls?view=fleet",
    "/proxy/tls?tool=request",
  ]) {
    await page.goto(path)
    const identity = page.locator("[data-slot='host-identity']").first()
    await expect(identity).toHaveAttribute("data-layout", "project")
    // The view strip's hairline is the rule; with the line's own there were two.
    expect(await identity.evaluate((el) => getComputedStyle(el).borderBottomWidth), path).toBe(
      "0px",
    )
  }
})

test("the lists of things to open draw one size of row across the section", async ({ page }) => {
  await mockSection(page)
  const heights: Record<string, number> = {}
  for (const [name, path] of [
    ["routes", "/proxy"],
    ["streams", "/proxy/streams"],
    ["certificates", "/proxy/certificates"],
  ]) {
    await page.goto(path)
    const row = page.locator("[data-slot='choice-row']").first()
    await expect(row).toBeVisible()
    await risen(page)
    heights[name] = Math.round((await row.boundingBox())!.height)
  }
  // A certificate's row had a larger name and tile than a stream's, and so
  // had a route's on the overview.
  expect(heights.certificates).toBe(heights.streams)
  expect(heights.routes).toBe(heights.streams)
})

test("a certificate list that cannot be read claims no count", async ({ page }) => {
  await mockSection(page)
  await page.route("**/api/v1/certificates/", (route) =>
    route.fulfill({
      status: 500,
      contentType: "application/json",
      body: JSON.stringify({ error: { code: "internal", message: "The read failed." } }),
    }),
  )
  await page.goto("/proxy/certificates")
  const identity = page.locator("[data-slot='host-identity']").first()
  await expect(identity.getByText("couldn't read the certificates")).toBeVisible()
  await expect(identity.getByText("Not read")).toBeVisible()
  // It said "0 certificates" over the failure.
  await expect(identity.getByText(/\d+ certificates?/)).toHaveCount(0)
})
