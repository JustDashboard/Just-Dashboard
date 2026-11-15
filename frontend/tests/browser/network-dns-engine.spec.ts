import { expect, test, type Locator, type Page } from "@playwright/test"
import {
  dnsChange,
  dnsConnection,
  mockDNSServicePage,
  type DNSServicePageControl,
} from "./network-dns-services-fixture"
import { admin, json } from "./network-fixture"
import { risen } from "./risen"

// A connected DNS engine as a page of its own (`/network/dns/services/<id>`): the line it
// opens on, its views in the address, the other engines as a rail, a retained review at an
// address. What each view holds is `network-dns-services.spec.ts` and the two filter specs.

const HOME = "/network/dns/services/dns-fixture"
const engine = (page: Page) => page.locator("[data-slot=dns-engine]")
const header = (page: Page) => page.locator("[data-slot=dns-engine-header]")
const strip = (page: Page) => page.getByRole("tablist", { name: "Views of Fixture DNS" })
const view = (page: Page, name: string) =>
  strip(page)
    .getByRole("tab", { name: new RegExp(`^${name}`) })
    .click()

async function box(target: Locator) {
  const found = await target.boundingBox()
  if (!found) throw new Error("nothing to measure")
  return found
}

/** The engine's page, read. The fixture opens the DNS page first; this goes on to the engine. */
async function open(
  page: Page,
  options: Parameters<typeof mockDNSServicePage>[1] = {},
  path = HOME,
): Promise<DNSServicePageControl> {
  const control = await mockDNSServicePage(page, options)
  await page.goto(path)
  await expect(header(page)).toContainText("Fixture DNS")
  return control
}

test("it opens on the engine: its line, its command, its views, and no tiles", async ({ page }) => {
  const control = await open(page)
  await expect(page.locator('[data-slot="stat-grid"], [data-slot="stat-tile"]')).toHaveCount(0)
  await expect(page.getByRole("dialog")).toHaveCount(0)
  await expect(header(page)).toContainText("Connected")
  await expect(header(page)).toContainText("AdGuard Home v0.107.71")
  await expect(header(page)).toContainText("https://192.0.2.10:8443")
  await expect(header(page)).toContainText("reviewed changes")
  await expect(strip(page).getByRole("tab")).toHaveText([
    "Overview",
    "Clients",
    "Records",
    /^Queries/,
    "Filters",
    "DHCP",
    "Changes",
  ])
  // The way back is the Services view it was opened from.
  await expect(
    page.getByRole("main").getByRole("link", { name: "DNS", exact: true }),
  ).toHaveAttribute("href", "/network/dns?tab=services")
  // The one command leads to the form, and steps aside once the form is the page.
  await header(page).getByRole("button", { name: "Review a change", exact: true }).click()
  await expect(page).toHaveURL(/\?tab=changes$/)
  await expect(
    engine(page).getByRole("button", { name: "Create retained review", exact: true }),
  ).toBeVisible()
  await expect(header(page).getByRole("button", { name: "Review a change" })).toHaveCount(0)
  expect(control.unexpectedReads).toEqual([])
  expect(control.mutations).toEqual([])
})

test("a read-only connection has no command, and its form says why", async ({ page }) => {
  await open(page, { management: false })
  await expect(header(page)).toContainText("read-only")
  await expect(header(page).getByRole("button", { name: "Review a change" })).toHaveCount(0)
  await view(page, "Changes")
  await expect(engine(page).getByText(/This connection is read-only/)).toBeVisible()
})

test("a retained review has an address, and Changes leads back to the list", async ({ page }) => {
  const control = await mockDNSServicePage(page)
  control.changes = [dnsChange()]
  await page.goto(`${HOME}?tab=changes&change=change-fixture`)
  await expect(
    engine(page).getByRole("heading", { name: "Reviewed native change", exact: true }),
  ).toBeVisible()
  await expect(
    engine(page).getByRole("button", { name: "Apply reviewed native change", exact: true }),
  ).toBeEnabled()
  // The form that would stage another is not on screen under a review.
  await expect(
    engine(page).getByRole("button", { name: "Create retained review", exact: true }),
  ).toBeHidden()
  await engine(page).getByRole("button", { name: "Changes", exact: true }).click()
  await expect(page).toHaveURL(/\?tab=changes$/)
  await expect(
    engine(page).getByRole("button", { name: "Read DNS change change-fixture", exact: true }),
  ).toBeVisible()
  await engine(page).getByRole("button", { name: "Read DNS change change-fixture" }).click()
  await expect(page).toHaveURL(/\?tab=changes&change=change-fixture$/)
  // Any other view leaves the review.
  await view(page, "Queries")
  await expect(page).toHaveURL(/\?tab=queries$/)
})

test("a draft outlives a look at another view", async ({ page }) => {
  await open(page, {}, `${HOME}?tab=changes`)
  await engine(page).getByRole("button", { name: "Review upstreams", exact: true }).click()
  const draft = engine(page).getByLabel("Classic DNS upstream endpoints", { exact: true })
  await draft.fill("192.0.2.54:53")
  await view(page, "Records")
  await expect(
    engine(page).getByRole("heading", { name: "Local overrides", exact: true }),
  ).toBeVisible()
  await expect(draft).toBeHidden()
  await view(page, "Changes")
  await expect(draft).toHaveValue("192.0.2.54:53")
})

test("disconnecting is in the menu, confirmed, and goes back to the Services view", async ({
  page,
}) => {
  const control = await open(page)
  // The shared fixture knows no disconnect: this test answers it.
  const deleted: string[] = []
  await page.route(/\/api\/v1\/network\/dns\/services\/dns-fixture$/, async (route) => {
    if (route.request().method() !== "DELETE") return route.fallback()
    deleted.push(new URL(route.request().url()).pathname)
    control.connections = []
    await route.fulfill({ status: 204 })
  })
  await page.getByRole("button", { name: "Engine actions", exact: true }).click()
  await expect(page.getByRole("menuitem")).toHaveText(["Update connection", "Disconnect engine"])
  await page.getByRole("menuitem", { name: "Disconnect engine", exact: true }).click()
  const dialog = page.getByRole("dialog", { name: "Disconnect Fixture DNS" })
  await expect(dialog).toContainText("The native engine and its policy keep running.")
  expect(deleted).toEqual([])
  await dialog.getByRole("button", { name: "Disconnect engine", exact: true }).click()
  await expect.poll(() => deleted).toEqual(["/api/v1/network/dns/services/dns-fixture"])
  await expect(page).toHaveURL(/\/network\/dns\?tab=services$/)
  expect(control.mutations).toEqual([])
})

test("a connection that changed hands while the dialog stood open is not disconnected", async ({
  page,
}) => {
  const control = await open(page)
  await page.getByRole("button", { name: "Engine actions", exact: true }).click()
  await page.getByRole("menuitem", { name: "Disconnect engine", exact: true }).click()
  const dialog = page.getByRole("dialog", { name: "Disconnect Fixture DNS" })
  control.view.connection = { ...control.view.connection, generation: 2 }
  await dialog.getByRole("button", { name: "Disconnect engine", exact: true }).click()
  await expect(engine(page).getByText(/The connection changed\./)).toBeVisible()
  expect(control.mutations).toEqual([])
})

test("an account sees only the verbs the server will let it use", async ({ page }) => {
  // An administrator who may not destroy: no disconnecting.
  const careful = {
    ...admin,
    capabilities: admin.capabilities.filter((capability) => capability !== "destructive"),
  }
  await mockDNSServicePage(page)
  await page.route("**/api/v1/auth/session", (route) => json(route, careful))
  await page.goto(HOME)
  await expect(header(page)).toContainText("Fixture DNS")
  await page.getByRole("button", { name: "Engine actions", exact: true }).click()
  await expect(page.getByRole("menuitem")).toHaveText(["Update connection"])
})

test("a session opened through Just Hub is not offered a connection's credential", async ({
  page,
}) => {
  await mockDNSServicePage(page)
  await page.route("**/api/v1/account/sessions", (route) =>
    json(route, [
      {
        id: "fixture-hub-session",
        userId: 1,
        authMethod: "hub",
        hubUser: "ana",
        twoFactorPassed: true,
        ip: "100.64.0.9",
        userAgent: "fixture",
        createdAt: new Date().toISOString(),
        lastSeenAt: new Date().toISOString(),
        expiresAt: new Date(Date.now() + 3_600_000).toISOString(),
        current: true,
      },
    ]),
  )
  await page.reload()
  // On the Services view: the engines are read, and none can be connected.
  await expect(page.getByRole("link", { name: "Inspect Fixture DNS", exact: true })).toBeVisible()
  await expect(page.getByRole("button", { name: "Connect engine", exact: true })).toHaveCount(0)
  await page.getByRole("button", { name: "DNS actions" }).click()
  await expect(page.getByRole("menuitem", { name: "Connect a DNS engine" })).toHaveCount(0)
  await page.keyboard.press("Escape")
  // On an engine: it is read and disconnected, and its connection is not changed.
  await page.goto(HOME)
  await expect(header(page)).toContainText("Fixture DNS")
  await page.getByRole("button", { name: "Engine actions", exact: true }).click()
  await expect(page.getByRole("menuitem")).toHaveText(["Disconnect engine"])
})

test("it is an administrator's, and reads nothing for anybody else", async ({ page }) => {
  const control = await mockDNSServicePage(page, { reader: true })
  await page.goto(HOME)
  await expect(
    page.getByText("A DNS engine's inventory needs the admin capability", { exact: true }),
  ).toBeVisible()
  await expect(page.locator("[data-slot=page]").getByRole("button")).toHaveCount(0)
  expect(control.reads).toEqual([])
})

test("an address no engine answers to says so and leads back", async ({ page }) => {
  await mockDNSServicePage(page)
  await page.goto("/network/dns/services/nobody")
  await expect(
    page.getByText("No DNS engine is connected under that address", { exact: true }),
  ).toBeVisible()
  await expect(header(page)).toHaveCount(0)
  await page.getByRole("link", { name: "DNS services", exact: true }).click()
  await expect(page).toHaveURL(/\/network\/dns\?tab=services$/)
})

test("the address above an engine's is the list of them", async ({ page }) => {
  await mockDNSServicePage(page)
  await page.goto("/network/dns/services")
  await expect(page).toHaveURL(/\/network\/dns\?tab=services$/)
  await expect(page.getByRole("link", { name: "Inspect Fixture DNS", exact: true })).toBeVisible()
})

test("a first read that fails says so on the line and offers it again", async ({ page }) => {
  const control = await mockDNSServicePage(page)
  control.inspectFailure = true
  await page.goto(HOME)
  await expect(header(page)).toContainText("Not read")
  await expect(engine(page).getByText("Fixture owner reading failed.")).toBeVisible()
  control.inspectFailure = false
  await engine(page).getByRole("button", { name: "Try again" }).click()
  await expect(header(page)).toContainText("Connected")
})

test("the engines stand beside it from xl, and Alt+↓ walks them with the view kept", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  const control = await mockDNSServicePage(page)
  control.connections = [
    control.view.connection,
    { ...dnsConnection(false, "pihole"), id: "dns-second", name: "Second DNS" },
  ]
  await page.goto(`${HOME}?tab=records`)
  const rail = page.locator("[data-slot=engine-rail]")
  await expect(rail).toBeVisible()
  await expect(rail.getByRole("link")).toHaveText([/^Fixture DNS/, /^Second DNS/])
  await expect(rail.getByRole("link", { name: "Fixture DNS" })).toHaveAttribute(
    "aria-current",
    "page",
  )
  await expect(rail.getByRole("link", { name: "Second DNS" })).toContainText("Pi-hole · read-only")
  await expect(page.getByRole("button", { name: "Previous engine" })).toBeDisabled()
  await page.keyboard.press("Alt+ArrowDown")
  await expect(page).toHaveURL(/\/network\/dns\/services\/dns-second\?tab=records$/)
  await expect(rail.getByRole("link", { name: "Second DNS" })).toHaveAttribute(
    "aria-current",
    "page",
  )
  await expect(page.getByRole("button", { name: "Next engine" })).toBeDisabled()
  await page.getByRole("button", { name: "Previous engine" }).click()
  await expect(page).toHaveURL(/\/network\/dns\/services\/dns-fixture\?tab=records$/)
})

for (const width of [1280, 390]) {
  test(`the line and the strip stand where they will while the engine is read, at ${width}`, async ({
    page,
  }) => {
    await page.setViewportSize({ width, height: 900 })
    await mockDNSServicePage(page)
    let release: () => void = () => {}
    const held = new Promise<void>((resolve) => (release = resolve))
    await page.route(/\/api\/v1\/network\/dns\/services\/dns-fixture$/, async (route) => {
      await held
      await route.fallback()
    })
    await page.goto(HOME)
    await expect(header(page)).toContainText("Reading the engine")
    await risen(page)
    const before = await box(header(page))
    const rule = await box(engine(page).locator("[data-slot=engine-strip]"))
    release()
    await expect(header(page)).toContainText("Connected")
    await risen(page)
    const after = await box(header(page))
    expect(after.y).toBe(before.y)
    expect(after.height).toBe(before.height)
    const settled = await box(engine(page).locator("[data-slot=engine-strip]"))
    expect(settled.y).toBe(rule.y)
    expect(settled.height).toBe(rule.height)
    // The first section starts where the waiting rows did.
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
      ),
    ).toBeLessThanOrEqual(0)
  })
}

test.describe("on a phone", () => {
  test.use({ viewport: { width: 390, height: 844 } })

  test("it is one pane: no rail, every verb, nothing sideways", async ({ page }) => {
    await open(page)
    await expect(page.locator("[data-slot=engine-rail]")).toBeHidden()
    await expect(header(page).getByRole("button", { name: "Review a change" })).toBeVisible()
    await page.getByRole("button", { name: "Engine actions", exact: true }).click()
    await expect(page.getByRole("menuitem")).toHaveText(["Update connection", "Disconnect engine"])
    await page.keyboard.press("Escape")
    for (const name of ["Clients", "Changes"]) {
      await view(page, name)
      await risen(page)
      expect(
        await page.evaluate(
          () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
        ),
      ).toBeLessThanOrEqual(0)
    }
  })
})
