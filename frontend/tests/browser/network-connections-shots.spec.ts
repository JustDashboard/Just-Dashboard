import { test, type Page } from "@playwright/test"
import { admin, json, loaded } from "./network-fixture"
import { openMaturity } from "./network-traffic-maturity-fixture"

/**
 * Review captures of the Connections pages in every state, for the eyes the
 * checks do not have. Written only when asked for, into the directory named
 * — never into the tree: `JD_CONNECTIONS_SHOTS=<dir>`, with
 * `JD_CONNECTIONS_SHOTS_PREFIX` (`after` by default) naming the set.
 */
const dir = process.env.JD_CONNECTIONS_SHOTS
const prefix = process.env.JD_CONNECTIONS_SHOTS_PREFIX ?? "after"
const readOnly = { ...admin, capabilities: ["read"], user: { ...admin.user, role: "readonly" } }

async function settle(page: Page) {
  await page.waitForTimeout(1200)
  await page.waitForFunction(() =>
    document
      .getAnimations()
      .filter((a) => a.effect?.getTiming().iterations !== Infinity)
      .every((a) => a.playState !== "running"),
  )
}

test.describe("captures", () => {
  test.skip(!dir, "set JD_CONNECTIONS_SHOTS to a directory to capture them")
  test.describe.configure({ timeout: 120_000 })

  for (const width of [1280, 390]) {
    test.describe(`${width} wide`, () => {
      test.use({ viewport: { width, height: 900 } })
      const shot = (page: Page, name: string) =>
        page.screenshot({ path: `${dir}/${prefix}-${name}-${width}.png`, fullPage: true })

      test(`connections full at ${width}`, async ({ page }) => {
        await openMaturity(page, [])
        await page.goto("/network/connections")
        await loaded(page)
        await settle(page)
        await shot(page, "connections-full")
      })

      test(`connections empty at ${width}`, async ({ page }) => {
        await openMaturity(page, [], {
          "/connections": { total: 3, listening: 27, loopback: 3, peers: [] },
          "/firewall/blocks": [],
        })
        await page.goto("/network/connections")
        await loaded(page)
        await settle(page)
        await shot(page, "connections-empty")
      })

      test(`connections loading at ${width}`, async ({ page }) => {
        await openMaturity(page, [])
        await page.route("**/api/v1/connections", () => new Promise(() => {}))
        await page.goto("/network/connections")
        await page.waitForSelector("[data-slot=page]")
        await page.waitForTimeout(1500)
        await shot(page, "connections-loading")
      })

      test(`connections failed at ${width}`, async ({ page }) => {
        await openMaturity(page, [])
        await page.route("**/api/v1/connections", (route) =>
          json(
            route,
            { error: { code: "unavailable", message: "the table could not be read" } },
            503,
          ),
        )
        await page.goto("/network/connections")
        await page.waitForSelector("[data-slot=page]")
        await page.waitForTimeout(2500)
        await shot(page, "connections-failed")
      })

      test(`connections read-only at ${width}`, async ({ page }) => {
        await openMaturity(page, [], {})
        await page.route("**/api/v1/auth/session", (route) => json(route, readOnly))
        await page.goto("/network/connections")
        await loaded(page)
        await settle(page)
        await shot(page, "connections-readonly")
      })

      test(`a past hour at ${width}`, async ({ page }) => {
        await openMaturity(page, [])
        await page.goto("/network/connections?when=recorded")
        await loaded(page)
        await settle(page)
        await shot(page, "connections-past")
      })

      test(`an address at ${width}`, async ({ page }) => {
        await openMaturity(page, [])
        await page.goto("/network/connections/198.51.100.23")
        await page.waitForSelector("[data-slot=page]")
        await settle(page)
        await shot(page, "address-full")
        await page.goto("/network/connections/198.51.100.23?tab=past")
        await settle(page)
        await shot(page, "address-past")
      })

      test(`an address read-only at ${width}`, async ({ page }) => {
        await openMaturity(page, [])
        await page.route("**/api/v1/auth/session", (route) => json(route, readOnly))
        await page.goto("/network/connections/100.110.34.9")
        await page.waitForSelector("[data-slot=page]")
        await settle(page)
        await shot(page, "address-readonly")
      })

      test(`an address gone at ${width}`, async ({ page }) => {
        await openMaturity(page, [])
        await page.route("**/api/v1/connections/*", (route) =>
          json(route, {
            address: "203.0.113.200",
            private: false,
            sockets: [],
            closed: [],
            quality: {
              readAt: new Date().toISOString(),
              intervalSeconds: 10,
              closedSinceLast: 0,
              unconnectedUdp: 0,
              limits: [],
            },
            countersTruncated: false,
          }),
        )
        await page.goto("/network/connections/203.0.113.200")
        await page.waitForSelector("[data-slot=page]")
        await settle(page)
        await shot(page, "address-gone")
      })

      test(`the old sheet at ${width}`, async ({ page }) => {
        test.skip(prefix !== "before", "the sheet is gone after")
        await openMaturity(page, [])
        await page.goto("/network/connections")
        await loaded(page)
        await page.getByRole("button", { name: "Inspect 198.51.100.23" }).first().click()
        await settle(page)
        await page.screenshot({ path: `${dir}/${prefix}-peer-sheet-${width}.png` })
      })
    })
  }
})
