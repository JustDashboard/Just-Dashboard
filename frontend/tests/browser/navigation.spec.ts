import { expect, test, type Page, type Route } from "@playwright/test"
import { deployment, mockProject, project } from "./deploy-fixture"

/**
 * The sidebar is the navigation, and it is the only navigation.
 *
 * Before 0.6.8 a section was a row that unfolded a second list under it *and* a
 * strip of route tabs across the top of every page in that section, so the six
 * multi-page sections each said where you were twice and the rail could be
 * thirty rows deep. The rail drills into a section now: one list at a time,
 * always the list for where you are. A group — Monitoring, Server configuration
 * — is a row with no page of its own that opens a panel of the pages it holds.
 *
 * These are the claims that makes, plus the one it removes. They are
 * written against the rail's landmark rather than against a page's markup,
 * because the point of the change is that the answer to "which pages does this
 * section have" lives in one place.
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
    displayName: "Operator",
    avatarVersion: 0,
    role: "admin",
    totpEnabled: true,
    disabled: false,
    mustChangePassword: false,
    lastLoginAt: now,
    createdAt: now,
  },
}

async function json(route: Route, body: unknown) {
  await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(body) })
}

/** A signed-in shell with Docker reachable and nothing else to look at. */
async function mockShell(page: Page, auth = user) {
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname.replace(/^\/api\/v1/, "")
    if (path === "/auth/session") return json(route, auth)
    if (path === "/updates/self") return json(route, { current: "0.6.7", latest: "0.6.7" })
    if (path === "/docker/ping") return json(route, { available: true, serverVersion: "27.0.0" })
    return json(route, [])
  })
}

const rail = (page: Page) => page.getByRole("navigation", { name: "Sidebar" })

test("the account menu supports keyboard navigation and returns focus on Escape", async ({
  page,
}) => {
  await mockShell(page)
  await page.goto("/account")

  const trigger = page.getByRole("button", { name: "Account menu for Operator" })
  await trigger.focus()
  await trigger.press("Enter")
  const menu = page.getByRole("menu", { name: "Account", exact: true })
  const profile = menu.getByRole("menuitem", { name: "Profile", exact: true })
  await expect(profile).toBeFocused()
  await expect(profile).toHaveAttribute("aria-current", "page")
  await expect(menu.getByText("@operator", { exact: true })).toBeVisible()
  await expect(menu.getByRole("menuitem", { name: "Security 2FA on" })).toBeVisible()

  await profile.press("ArrowDown")
  const security = menu.getByRole("menuitem", { name: "Security 2FA on" })
  await expect(security).toBeFocused()
  await security.press("Enter")
  await expect(page).toHaveURL(/\/account\/security$/)
  await expect(menu).toHaveCount(0)

  await trigger.press("Enter")
  await expect(security).toHaveAttribute("aria-current", "page")
  await menu.press("Escape")
  await expect(menu).toHaveCount(0)
  await expect(trigger).toBeFocused()
})

test("the account menu keeps role visibility, two-factor state and sign out", async ({ page }) => {
  await mockShell(page, {
    ...user,
    capabilities: ["read"],
    user: { ...user.user, role: "readonly", totpEnabled: false },
  })
  let signedOut = false
  await page.route("**/api/v1/auth/logout", async (route) => {
    signedOut = route.request().method() === "POST"
    await json(route, {})
  })
  await page.goto("/account")
  await page.getByRole("button", { name: "Account menu for Operator" }).click()
  const menu = page.getByRole("menu", { name: "Account", exact: true })
  await expect(menu.getByRole("menuitem", { name: "Users", exact: true })).toHaveCount(0)
  await expect(menu.getByRole("menuitem", { name: "Security 2FA off" })).toBeVisible()
  await menu.getByRole("menuitem", { name: "Sign out", exact: true }).click()
  await expect(page).toHaveURL(/\/login$/)
  expect(signedOut).toBe(true)
})

test("uploaded account pictures stay circular in the button and menu", async ({ page }) => {
  await mockShell(page, { ...user, user: { ...user.user, avatarVersion: 1 } })
  await page.route("**/api/v1/account/avatar?*", (route) =>
    route.fulfill({
      contentType: "image/png",
      body: Buffer.from(
        "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jRZkAAAAASUVORK5CYII=",
        "base64",
      ),
    }),
  )
  await page.goto("/account")
  // The modal menu hides its trigger from assistive technology while it is open.
  const trigger = page.getByRole("button", {
    name: "Account menu for Operator",
    includeHidden: true,
  })
  await trigger.click()
  const menu = page.getByRole("menu", { name: "Account", exact: true })
  for (const image of [trigger.locator("img"), menu.locator("img")]) {
    await expect(image).toBeVisible()
    expect(
      await image.evaluate((el) => {
        const box = el.getBoundingClientRect()
        return (
          Math.abs(box.width - box.height) < 0.1 &&
          parseFloat(getComputedStyle(el).borderRadius) >= box.height / 2
        )
      }),
    ).toBe(true)
  }
})

test("desktop navigation stays expanded despite old preferences and sidebar shortcuts", async ({
  page,
}) => {
  await page.addInitScript(() => {
    localStorage.setItem("jd.view.state", JSON.stringify({ "shell.sidebar": false }))
    document.cookie = "sidebar_state=false; path=/"
  })
  await mockShell(page)
  await page.goto("/account")
  const sidebar = page.locator('[data-slot="sidebar"][data-state]')
  await expect(sidebar).toHaveAttribute("data-state", "expanded")
  await expect(page.getByRole("button", { name: "Toggle the sidebar", exact: true })).toHaveCount(0)
  for (const key of ["Control+b", "Meta+b"]) {
    await page.keyboard.press(key)
    await expect(sidebar).toHaveAttribute("data-state", "expanded")
  }
  await page.reload()
  await expect(sidebar).toHaveAttribute("data-state", "expanded")

  await page.setViewportSize({ width: 390, height: 844 })
  await page.getByRole("button", { name: "Toggle the sidebar", exact: true }).click()
  const drawer = page.getByRole("dialog", { name: "Sidebar", exact: true })
  await expect(drawer).toBeVisible()
  await drawer.getByRole("button", { name: "Toggle the sidebar", exact: true }).click()
  await expect(drawer).toHaveCount(0)
})

for (const layout of ["expanded", "mobile"] as const) {
  test(`the ${layout} account menu fits the viewport with circular avatar fallbacks`, async ({
    page,
  }) => {
    if (layout === "mobile") await page.setViewportSize({ width: 390, height: 844 })
    await mockShell(page, { ...user, user: { ...user.user, avatarVersion: 1 } })
    await page.route("**/api/v1/account/avatar?*", (route) => route.fulfill({ status: 404 }))
    await page.goto("/account")
    if (layout === "mobile") {
      await page.getByRole("button", { name: "Toggle the sidebar", exact: true }).click()
    }
    const trigger = page.getByRole("button", {
      name: "Account menu for Operator",
      includeHidden: true,
    })
    const face = trigger.locator('[data-slot="user-avatar"]')
    await expect(face).toHaveText("OP")
    await trigger.click()
    const menu = page.getByRole("menu", { name: "Account", exact: true })
    await expect(menu).toBeVisible()
    await menu.evaluate(async (el) => {
      await Promise.all(el.getAnimations().map((animation) => animation.finished))
    })
    for (const avatar of [face, menu.locator('[data-slot="user-avatar"]')]) {
      expect(
        await avatar.evaluate((el) => {
          const box = el.getBoundingClientRect()
          return (
            Math.abs(box.width - box.height) < 0.1 &&
            parseFloat(getComputedStyle(el).borderRadius) >= box.height / 2
          )
        }),
      ).toBe(true)
    }
    const box = await menu.boundingBox()
    const viewport = page.viewportSize()!
    // Above the card the menu is the card's own width, edge to edge.
    const card = (await trigger.boundingBox())!
    expect(Math.abs(box!.x - card.x)).toBeLessThan(1)
    expect(Math.abs(box!.width - card.width)).toBeLessThan(1)
    await expect(trigger.locator("svg")).toHaveCount(0)
    expect(box!.x).toBeGreaterThanOrEqual(0)
    expect(box!.y).toBeGreaterThanOrEqual(0)
    expect(box!.x + box!.width).toBeLessThanOrEqual(viewport.width)
    expect(box!.y + box!.height).toBeLessThanOrEqual(viewport.height)
    if (layout === "mobile") {
      for (const item of await menu.getByRole("menuitem").all()) {
        expect((await item.boundingBox())!.height).toBeGreaterThanOrEqual(44)
      }
    }
  })
}

/** The aria-labels the removed strips carried. None of them may come back. */
const STRIPS = ["Section", "Project sections", "Database section", "Project settings"]

async function expectNoStrip(page: Page) {
  for (const label of STRIPS) {
    await expect(page.getByRole("navigation", { name: label })).toHaveCount(0)
  }
}

test("opening a section replaces the rail with that section's pages", async ({ page }) => {
  await mockShell(page)
  await page.goto("/")

  // The top-level list is sections, not their pages: Docker is one row, and
  // Containers is nowhere until you go in.
  await expect(rail(page).getByRole("link", { name: "Docker", exact: true })).toBeVisible()
  await expect(rail(page).getByRole("link", { name: "Containers" })).toHaveCount(0)

  await rail(page).getByRole("link", { name: "Docker", exact: true }).click()
  await expect(page).toHaveURL(/\/docker$/)

  for (const name of [
    "Overview",
    "Containers",
    "Stacks",
    "Images",
    "Volumes",
    "Networks",
    "Events",
  ])
    await expect(rail(page).getByRole("link", { name, exact: true })).toBeVisible()

  // …and the rest of the product is not competing with them.
  await expect(rail(page).getByRole("link", { name: "Metrics" })).toHaveCount(0)
  await expectNoStrip(page)
})

test("a deep link opens with the rail already inside the section", async ({ page }) => {
  await mockShell(page)
  await page.goto("/network/firewall")

  await expect(rail(page).getByRole("link", { name: "Gateway", exact: true })).toBeVisible()
  await expect(rail(page).getByRole("link", { name: "Backups" })).toHaveCount(0)
  await expectNoStrip(page)
})

test("stepping back out shows everything without leaving the page", async ({ page }) => {
  await mockShell(page)
  await page.goto("/docker/images")

  await page.getByRole("button", { name: "Back to All pages" }).click()

  // The rail came out a level; the page did not move.
  await expect(page).toHaveURL(/\/docker\/images$/)
  await expect(rail(page).getByRole("link", { name: "Backups", exact: true })).toBeVisible()
  await expect(rail(page).getByRole("link", { name: "Containers" })).toHaveCount(0)

  // And navigating drops the step, so the rail follows where you went.
  await rail(page).getByRole("link", { name: "Security", exact: true }).click()
  await expect(page).toHaveURL(/\/security$/)
  await expect(rail(page).getByRole("link", { name: "Intrusion", exact: true })).toBeVisible()
})

test("a group opens its panel without leaving the page", async ({ page }) => {
  await mockShell(page)
  await page.goto("/")

  // Monitoring is not a page, so it is a button, and what it holds is nowhere
  // until it is pressed.
  await expect(rail(page).getByRole("link", { name: "Metrics" })).toHaveCount(0)
  await rail(page).getByRole("button", { name: "Monitoring", exact: true }).click()
  await expect(page).toHaveURL(/\/$/)
  for (const name of ["Metrics", "Processes", "Logs"])
    await expect(rail(page).getByRole("link", { name, exact: true })).toBeVisible()

  // A section inside a group is a third level, and back from it is the group.
  await rail(page).getByRole("link", { name: "Processes", exact: true }).click()
  await expect(page).toHaveURL(/\/processes$/)
  await expect(rail(page).getByRole("link", { name: "PM2", exact: true })).toBeVisible()
  await page.getByRole("button", { name: "Back to Monitoring" }).click()
  await expect(rail(page).getByRole("link", { name: "Logs", exact: true })).toBeVisible()
  await page.getByRole("button", { name: "Back to All pages" }).click()
  await expect(rail(page).getByRole("button", { name: "Monitoring", exact: true })).toBeVisible()
})

test("a deep link inside a group opens every level of it", async ({ page }) => {
  await mockShell(page)
  await page.goto("/proxy/sites")

  await expect(rail(page).getByRole("link", { name: "Certificates", exact: true })).toBeVisible()

  await page.getByRole("button", { name: "Back to All pages" }).click()
  await expect(page).toHaveURL(/\/proxy\/sites$/)
  for (const name of ["Network", "Proxy & TLS"])
    await expect(rail(page).getByRole("link", { name, exact: true })).toBeVisible()

  // Pressing the section you are inside puts the rail back where it was.
  await rail(page).getByRole("link", { name: "Proxy & TLS", exact: true }).click()
  await expect(rail(page).getByRole("link", { name: "Certificates", exact: true })).toBeVisible()
})

test("a project is a third level, and back from it lands on Deployments", async ({ page }) => {
  await mockProject(page)
  await page.goto("/deploy/7/logs")

  // The project's own pages, and its settings under them — the fourteen the
  // tab strip used to scroll sideways through.
  await expect(rail(page).getByText("api-production")).toBeVisible()
  await expect(rail(page).getByRole("link", { name: "Console", exact: true })).toBeVisible()
  await expect(rail(page).getByRole("link", { name: "Danger zone", exact: true })).toHaveAttribute(
    "href",
    "/deploy/7/settings/danger",
  )
  await expectNoStrip(page)

  await page.getByRole("button", { name: "Back to Deployments" }).click()
  await expect(page).toHaveURL(/\/deploy\/7\/logs$/)
  await expect(rail(page).getByRole("link", { name: "Projects", exact: true })).toBeVisible()

  await page.getByRole("button", { name: "Back to All pages" }).click()
  await expect(rail(page).getByRole("link", { name: "Terminal", exact: true })).toBeVisible()
})

test("a game server gets the two pages nothing else has", async ({ page }) => {
  await mockProject(page)
  await page.route("**/api/v1/deploy/7", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        project,
        running: false,
        deployment: { ...deployment, profile: "game", buildMethod: "image", activeRun: undefined },
      }),
    }),
  )
  await page.goto("/deploy/7")

  await expect(rail(page).getByRole("link", { name: "Players", exact: true })).toBeVisible()
  await expect(rail(page).getByRole("link", { name: "Server settings", exact: true })).toBeVisible()
})
