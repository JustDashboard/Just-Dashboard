import { expect, test, type Locator, type Page, type Route } from "@playwright/test"
import { mockNetworkPage } from "./docker-network-page-fixture"
import { risen } from "./risen"

/**
 * The controls' touch sizes, written once in the primitives (0.9.0, WP-02).
 *
 * Under a finger every small control keeps a 44px press box: a dense or
 * compact button draws 32 or 40 and an `::after` makes up the rest without
 * growing its row, a regular button is 44 itself, and a switch's 18px track
 * sits in a 44px box. A field's text is 16px whatever the caller passed,
 * because iOS zooms into a field under 16px and does not zoom back out.
 * Under a mouse nothing moves: the 1440 checks hold the desktop heights.
 */

const now = new Date().toISOString()

const session = {
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
    username: "ion",
    displayName: "Ion Popescu",
    avatarVersion: 0,
    role: "admin",
    totpEnabled: true,
    disabled: false,
    mustChangePassword: false,
    lastLoginAt: now,
    createdAt: now,
  },
}

const users = [
  session.user,
  { ...session.user, id: 2, username: "maria", displayName: "Maria Rusu", totpEnabled: false },
  {
    ...session.user,
    id: 3,
    username: "guest",
    displayName: "Guest",
    role: "readonly",
    disabled: true,
  },
]

const dir = "/opt/just-dashboard"
const config = {
  supported: true,
  dir,
  compose: `${dir}/docker-compose.yml`,
  envPath: `${dir}/.env`,
  settings: {
    site: "atlas.tail1234.ts.net",
    bind: "100.64.0.7",
    tls: "tailscale",
    port: 8443,
    frontendPort: 3000,
    backendPort: 8080,
    allowedCidrs: "100.64.0.0/10,127.0.0.1/32",
    terminalEnabled: true,
    require2fa: true,
    sessionTtl: "12h",
    idleTtl: "60m",
    updateCheck: true,
  },
  endpoint: "https://atlas.tail1234.ts.net:8443",
  tailscale: {
    available: true,
    running: true,
    hostname: "atlas.tail1234.ts.net",
    ip4: "100.64.0.7",
  },
  certificate: { issued: true },
  drift: [],
}

async function json(route: Route, body: unknown) {
  await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(body) })
}

async function mockApi(page: Page) {
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname.replace(/^\/api\/v1/, "")
    if (path === "/auth/session") return json(route, session)
    if (path === "/dashboard-users/") return json(route, users)
    if (path === "/dashboard/config") return json(route, config)
    if (path === "/dashboard/uninstall") return json(route, { supported: true })
    return json(route, [])
  })
}

/**
 * The box a press lands in: the control's own, grown by its `::after` when the
 * hit area draws one. Read from the computed insets so that a neighbour
 * drawn over the corner cannot make a passing control look like a failing one.
 */
function pressBox(control: Locator) {
  return control.evaluate((el) => {
    const box = el.getBoundingClientRect()
    const after = getComputedStyle(el, "::after")
    if (after.position !== "absolute") return { width: box.width, height: box.height }
    const px = (value: string) => parseFloat(value) || 0
    return {
      width: box.width - px(after.left) - px(after.right),
      height: box.height - px(after.top) - px(after.bottom),
    }
  })
}

/** Whether a press at `dy` pixels above the control's centre reaches the control. */
function reachesFromAbove(control: Locator, dy: number) {
  return control.evaluate((el, offset) => {
    const box = el.getBoundingClientRect()
    const hit = document.elementFromPoint(box.x + box.width / 2, box.y + box.height / 2 - offset)
    return hit !== null && el.contains(hit)
  }, dy)
}

const fontSize = (control: Locator) =>
  control.evaluate((el) => parseFloat(getComputedStyle(el).fontSize))

test.describe("under a finger", () => {
  test.use({ viewport: { width: 390, height: 844 }, hasTouch: true })

  test("a small button keeps a 44px press box, a regular one is 44 itself", async ({ page }) => {
    await mockApi(page)
    await page.goto("/account/users")
    await risen(page)
    expect(await page.evaluate(() => matchMedia("(pointer: coarse)").matches)).toBe(true)

    // The page's command: a compact button, 40 drawn and 44 to press.
    const create = page.getByRole("button", { name: "New user" })
    await expect(create).toBeVisible()
    expect((await create.boundingBox())!.height).toBe(40)
    const press = await pressBox(create)
    expect(press.height).toBeGreaterThanOrEqual(44)
    expect(press.width).toBeGreaterThanOrEqual(44)
    expect(await reachesFromAbove(create, 21)).toBe(true)

    // Every small button on the page, not only the one the spec knows by name.
    const small = page.locator(
      ":is([data-slot=button], [data-slot=select-trigger]):is([data-size=xs], [data-size=sm], [data-size=icon-xs], [data-size=icon-sm]):visible",
    )
    expect(await small.count()).toBeGreaterThan(0)
    for (const control of await small.all()) {
      const box = await pressBox(control)
      expect
        .soft(box.height, await control.evaluate((el) => el.outerHTML.slice(0, 120)))
        .toBeGreaterThanOrEqual(44)
      expect.soft(box.width).toBeGreaterThanOrEqual(44)
    }
  })

  test("a switch standing alone is a 44px target", async ({ page }) => {
    await mockApi(page)
    await page.goto("/account/users")
    await risen(page)

    const switches = page.getByRole("switch")
    await expect(switches).toHaveCount(users.length)
    for (const control of await switches.all()) {
      const box = await pressBox(control)
      expect(box.height).toBeGreaterThanOrEqual(44)
      expect(box.width).toBeGreaterThanOrEqual(44)
    }
    // The track is still the 18px one it draws; the press reaches it from 20px off.
    const maria = page.getByRole("switch", { name: "Maria Rusu can sign in" })
    expect(Math.round((await maria.boundingBox())!.height)).toBe(18)
    expect(await reachesFromAbove(maria, 20)).toBe(true)
  })

  test("a field asked for text-xs is still 16px and 44 tall", async ({ page }) => {
    // The attach dialog's alias field passes `font-mono text-xs`.
    await mockNetworkPage(page)
    await page.goto("/docker/networks/shop_backend?tab=members")
    await risen(page)
    await page.getByRole("button", { name: "Attach a container", exact: true }).first().click()
    const alias = page.locator("#network-attach-alias")
    await expect(alias).toBeVisible()
    await expect(alias).toHaveClass(/\btext-xs\b/)
    expect(await fontSize(alias)).toBe(16)
    expect((await alias.boundingBox())!.height).toBe(44)
  })

  test("every field on a form is 16px, though each asks for text-body", async ({ page }) => {
    // Configuration's fields all pass `font-mono text-body`.
    await mockApi(page)
    await page.goto("/dashboard/configuration")
    await risen(page)
    const fields = page.locator("input[data-slot=input]:visible, textarea:visible")
    await expect(fields.first()).toBeVisible()
    for (const field of await fields.all()) {
      expect.soft(await fontSize(field)).toBeGreaterThanOrEqual(16)
    }
  })
})

test.describe("under a mouse", () => {
  test.use({ viewport: { width: 1440, height: 900 } })

  test("the desktop heights hold and the label is the body size", async ({ page }) => {
    await mockApi(page)
    await page.goto("/account/users")
    await risen(page)
    expect(await page.evaluate(() => matchMedia("(pointer: fine)").matches)).toBe(true)

    const create = page.getByRole("button", { name: "New user" })
    expect((await create.boundingBox())!.height).toBe(32)
    expect(await fontSize(create)).toBe(13)
    // The switch keeps a 24px box under a mouse, and no more.
    const maria = page.getByRole("switch", { name: "Maria Rusu can sign in" })
    expect(Math.round((await maria.boundingBox())!.height)).toBe(18)
    const box = await pressBox(maria)
    expect(box.height).toBeGreaterThanOrEqual(24)
    expect(box.height).toBeLessThan(44)

    await page.goto("/dashboard/configuration")
    await risen(page)
    const field = page.locator("input[data-slot=input]:visible").first()
    await expect(field).toBeVisible()
    expect((await field.boundingBox())!.height).toBe(36)
    expect(await fontSize(field)).toBe(13)
  })
})
