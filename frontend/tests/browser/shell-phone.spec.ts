import { expect, test, type Page, type Route } from "@playwright/test"

/**
 * The shell on a phone, and what the shell owes a keyboard and a screen
 * reader at any width.
 *
 * Below `md` the rail is a drawer and a strip across the top is the only thing
 * that opens it. Three things were wrong with that at once: a link tapped in
 * the drawer changed the page and left the drawer over it, the drawer's rows
 * and the strip's three controls were 32px under a finger, and the strip sat
 * inside `<main>`. The rest of this file is the shell's side of the
 * accessibility contract: a way past the rail, a current page that is said as
 * well as drawn, controls named by the words they show, and the switch that
 * holds the product's animations still.
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

function json(route: Route, body: unknown) {
  return route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(body) })
}

/** A signed-in shell over an API with nothing in it; later routes win over this one. */
async function mockShell(page: Page) {
  await page.route("**/api/v1/**", (route) => {
    const path = new URL(route.request().url()).pathname.replace(/^\/api\/v1/, "")
    if (path === "/auth/session") return json(route, session)
    if (path === "/updates/self") return json(route, { current: "0.9.0", latest: "0.9.0" })
    if (path === "/docker/ping") return json(route, { available: true, serverVersion: "27.0.0" })
    return json(route, [])
  })
}

const drawer = (page: Page) => page.getByRole("dialog", { name: "Sidebar", exact: true })
const rail = (page: Page) => page.getByRole("navigation", { name: "Sidebar" })
const strip = (page: Page) => page.getByRole("banner")

async function openDrawer(page: Page) {
  await strip(page).getByRole("button", { name: "Toggle the sidebar", exact: true }).tap()
  await expect(drawer(page)).toBeVisible()
}

/** The smaller side of each control's box, by name. A finger needs 44. */
function boxes(page: Page, selector: string) {
  return page.locator(selector).evaluateAll((elements) =>
    elements
      .filter((el): el is HTMLElement => el instanceof HTMLElement && el.offsetParent !== null)
      .map((el) => {
        const box = el.getBoundingClientRect()
        return {
          name: el.getAttribute("aria-label") || el.textContent?.trim() || el.dataset.slot || "",
          side: Math.round(Math.min(box.width, box.height) * 100) / 100,
        }
      }),
  )
}

test.describe("on a phone", () => {
  test.use({ viewport: { width: 390, height: 844 }, hasTouch: true, isMobile: true })

  test("a link tapped in the drawer opens its page and puts the drawer away", async ({ page }) => {
    await mockShell(page)
    await page.goto("/account")
    await openDrawer(page)

    await rail(page).getByRole("link", { name: "Security", exact: true }).tap()
    await expect(page).toHaveURL(/\/account\/security$/)
    await expect(drawer(page)).toHaveCount(0)

    // The row of the page you are already on changes no address, and still
    // means "show me the page".
    await openDrawer(page)
    await rail(page).getByRole("link", { name: "Security", exact: true }).tap()
    await expect(drawer(page)).toHaveCount(0)
    await expect(page).toHaveURL(/\/account\/security$/)
  })

  test("a row that only drills into a panel leaves the drawer open", async ({ page }) => {
    await mockShell(page)
    await page.goto("/account")
    await openDrawer(page)

    // Out to everything, into a group that has no page of its own, and into a
    // section of it: three presses that change the list and not the page.
    await rail(page).getByRole("button", { name: "Back to All pages" }).tap()
    await expect(rail(page).getByRole("link", { name: "Overview", exact: true })).toBeVisible()
    await rail(page).getByRole("button", { name: "Monitoring", exact: true }).tap()
    await expect(rail(page).getByRole("link", { name: "Metrics", exact: true })).toBeVisible()
    await expect(drawer(page)).toBeVisible()
    await expect(page).toHaveURL(/\/account$/)

    // The page it was opened to find closes it.
    await rail(page).getByRole("link", { name: "Metrics", exact: true }).tap()
    await expect(page).toHaveURL(/\/metrics$/)
    await expect(drawer(page)).toHaveCount(0)
  })

  test("a page opened from the account menu puts the drawer away too", async ({ page }) => {
    await mockShell(page)
    await page.goto("/account")
    await openDrawer(page)
    await drawer(page).getByRole("button", { name: "Operator, admin, account menu" }).tap()
    await page
      .getByRole("menu", { name: "Account", exact: true })
      .getByRole("menuitem", { name: "Sessions", exact: true })
      .tap()
    await expect(page).toHaveURL(/\/account\/sessions$/)
    await expect(drawer(page)).toHaveCount(0)
  })

  test("the strip's controls and the drawer's rows are a finger tall", async ({ page }) => {
    await mockShell(page)
    await page.goto("/account")
    await expect(strip(page)).toBeVisible()

    const controls = await boxes(page, "header button")
    expect(controls.map((control) => control.name)).toEqual(["Toggle the sidebar", "Search"])
    for (const control of controls)
      expect(control.side, `${control.name} in the strip`).toBeGreaterThanOrEqual(44)

    await openDrawer(page)
    const rows = await boxes(
      page,
      '[data-slot="sidebar"] :is([data-slot="sidebar-menu-button"], [data-slot="sidebar-trigger"], nav > div > button, [aria-label="Search"], [aria-haspopup="menu"])',
    )
    // The way back out, the account's six pages, and the drawer's own three controls.
    expect(rows.length).toBeGreaterThanOrEqual(10)
    for (const row of rows) expect(row.side, `${row.name} in the drawer`).toBeGreaterThanOrEqual(44)
  })

  test("the strip is the phone's banner, beside the main content and not in it", async ({
    page,
  }) => {
    await mockShell(page)
    await page.goto("/account")
    await expect(strip(page)).toHaveCount(1)
    await expect(page.getByRole("main")).toHaveCount(1)
    await expect(page.getByRole("main").getByRole("button", { name: "Search" })).toHaveCount(0)
    await expect(page.getByRole("main").getByRole("heading", { level: 1 })).toHaveCount(1)
  })
})

test("the first Tab is a way past the rail, and it lands on the page", async ({ page }) => {
  await mockShell(page)
  await page.goto("/account")
  await expect(rail(page)).toBeVisible()

  const skip = page.getByRole("link", { name: "Skip to content", exact: true })
  // In the document for a keyboard, and nowhere on screen until it is used.
  await expect(skip).toBeAttached()
  await expect(skip).not.toBeInViewport()

  await page.keyboard.press("Tab")
  await expect(skip).toBeFocused()
  await expect(skip).toBeInViewport({ ratio: 1 })
  expect(await skip.evaluate((el) => el.getBoundingClientRect().height)).toBeGreaterThanOrEqual(24)

  await page.keyboard.press("Enter")
  // The focus moves; the address does not.
  await expect(page.getByRole("main").getByRole("heading", { level: 1 })).toBeFocused()
  expect(new URL(page.url()).hash).toBe("")

  // The next stop is the page's, not the rail's.
  await page.keyboard.press("Tab")
  expect(
    await page.evaluate(() => Boolean(document.activeElement?.closest("main"))),
    "the stop after the skip link is inside the page",
  ).toBe(true)
  // The heading held the focus for that moment only: a click on the page
  // still focuses nothing, so the keyboard scrolls the pane under the pointer.
  expect(await page.locator("main h1").getAttribute("tabindex")).toBeNull()
  expect(await page.getByRole("main").getAttribute("tabindex")).toBeNull()
})

test("an item's page comes before its list for the keyboard, though the list is drawn first", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await mockShell(page)
  await page.goto("/processes/4021")
  await expect(page.locator('[data-slot="process-rail"]')).toBeVisible()

  const heading = page.getByRole("main").getByRole("heading", { level: 1 })
  const order = await heading.evaluate((h1) => {
    const list = document.querySelector('[data-slot="process-rail"]')!
    return {
      listFollows: Boolean(h1.compareDocumentPosition(list) & Node.DOCUMENT_POSITION_FOLLOWING),
      listLeft: list.getBoundingClientRect().left,
      itemLeft: h1.parentElement!.getBoundingClientRect().left,
    }
  })
  expect(order.listFollows, "the rail is after the item's heading in the document").toBe(true)
  expect(order.listLeft, "the rail is drawn on the leading edge").toBeLessThan(order.itemLeft)

  // Past the shell, the first stop is the item's own way back, not the rail's search.
  await page.keyboard.press("Tab")
  await page.keyboard.press("Enter")
  await expect(heading).toBeFocused()
  await page.keyboard.press("Tab")
  await expect(
    page.getByRole("main").getByRole("link", { name: "Processes", exact: true }),
  ).toBeFocused()
})

test("the current page is said, and the pointer's fill is not the selection's", async ({
  page,
}) => {
  await mockShell(page)
  await page.goto("/docker/containers")
  const nav = rail(page)
  const current = nav.getByRole("link", { name: "Containers", exact: true })
  await expect(current).toHaveAttribute("aria-current", "page")
  await expect(nav.locator('[aria-current="page"]')).toHaveCount(1)

  const fill = (name: string) =>
    nav.getByRole("link", { name, exact: true }).evaluate(async (el) => {
      await Promise.all(el.getAnimations().map((animation) => animation.finished))
      return getComputedStyle(el).backgroundColor
    })

  await page.mouse.move(900, 700)
  const atRest = await fill("Images")
  const selected = await fill("Containers")
  await nav.getByRole("link", { name: "Images", exact: true }).hover()
  const hovered = await fill("Images")
  expect(selected, "the current row has no fill").not.toBe(atRest)
  expect(hovered, "hover paints nothing").not.toBe(atRest)
  expect(hovered, "hover paints the selection's fill").not.toBe(selected)
  // The fill is the only mark: no bar on the leading edge.
  expect(await current.evaluate((el) => getComputedStyle(el, "::before").content === "none")).toBe(
    true,
  )
  // Under the pointer the current row keeps its own fill.
  await current.hover()
  expect(await fill("Containers")).toBe(selected)

  // Out to everything: the section you are inside is lit as the current one
  // of the list, and it is not the page its link opens.
  await nav.getByRole("button", { name: "Back to All pages" }).click()
  await expect(nav.getByRole("link", { name: "Docker", exact: true })).toHaveAttribute(
    "aria-current",
    "true",
  )
  await expect(nav.locator('[aria-current="page"]')).toHaveCount(0)
})

/**
 * WCAG 2.5.3, label in name: a control that shows words is named by them, so
 * "click Search" reaches Search. The check is axe's `label-content-name-mismatch`
 * in miniature, over every control of the shell that carries an `aria-label`:
 * the words on screen, with punctuation and what is hidden from assistive
 * technology left out, are in the label.
 */
test("the shell's controls are named by the words they show", async ({ page }) => {
  await mockShell(page)
  await page.goto("/account")
  await expect(rail(page)).toBeVisible()

  const named = await page
    .locator('[data-slot="sidebar"] :is(button, a)[aria-label]')
    .evaluateAll((elements) => {
      const plain = (text: string) =>
        text
          .toLowerCase()
          .replace(/[^\p{L}\p{N}\s]/gu, "")
          .replace(/\s+/g, " ")
          .trim()
      return elements.map((el) => {
        const words: string[] = []
        const walker = document.createTreeWalker(el, NodeFilter.SHOW_TEXT)
        for (let node = walker.nextNode(); node; node = walker.nextNode())
          if (!node.parentElement?.closest('[aria-hidden="true"], .sr-only, svg'))
            words.push(node.textContent ?? "")
        return {
          label: plain(el.getAttribute("aria-label") ?? ""),
          shown: plain(words.join(" ")),
        }
      })
    })
  expect(named.map((control) => control.label)).toEqual(
    expect.arrayContaining(["search", "operator admin account menu"]),
  )
  for (const control of named)
    expect(control.label, `"${control.shown}" is not in its control's name`).toContain(
      control.shown,
    )

  // Search is "Search" and nothing more: its keycaps are a picture of the shortcut.
  await expect(
    page.getByRole("banner").getByRole("button", { name: "Search", exact: true }),
  ).toBeVisible()
})

test.describe("Pause animations", () => {
  /**
   * What is moving, by name: every running animation or transition that takes
   * longer than a frame from start to end. A paused page still starts them (a
   * hover's colour, a state's class) and each is over in a hundredth of a
   * millisecond, so "running" alone would count whatever a sample happened to
   * catch in that instant; what must be absent is anything with time in it.
   */
  const moving = (page: Page) =>
    page.evaluate(() =>
      document
        .getAnimations()
        .filter((animation) => {
          if (animation.playState !== "running") return false
          const whole = Number(animation.effect?.getComputedTiming().activeDuration ?? 0)
          return whole > 16
        })
        .map((animation) =>
          animation instanceof CSSAnimation ? animation.animationName : "transition",
        ),
    )

  async function pause(page: Page) {
    await page.getByRole("button", { name: "Operator, admin, account menu" }).click()
    const menu = page.getByRole("menu", { name: "Account", exact: true })
    const item = menu.getByRole("menuitemcheckbox", { name: "Pause animations", exact: true })
    await expect(item).toHaveAttribute("aria-checked", "false")
    await item.click()
    // The menu stays open, so the mark is seen to change.
    await expect(item).toHaveAttribute("aria-checked", "true")
    await page.keyboard.press("Escape")
    await expect(menu).toHaveCount(0)
  }

  test("holds every animation still, and the next load never starts one", async ({ page }) => {
    await mockShell(page)
    // A read that never answers: the page stays on its loading shape, which
    // pulses for as long as it is on screen.
    await page.route("**/api/v1/docker/containers**", () => {})
    await page.goto("/docker/containers")
    await expect(rail(page)).toBeVisible()
    await expect
      .poll(() => moving(page), "nothing on this page moves to begin with")
      .not.toEqual([])

    await pause(page)
    await expect(page.locator("html")).toHaveAttribute("data-motion", "paused")
    await expect.poll(() => moving(page)).toEqual([])

    await page.reload()
    await expect(rail(page)).toBeVisible()
    await expect(page.locator("html")).toHaveAttribute("data-motion", "paused")
    await expect.poll(() => moving(page)).toEqual([])
    // Held, not sampled at a lucky moment.
    await page.waitForTimeout(400)
    expect(await moving(page)).toEqual([])

    // And back: the switch is a switch.
    await page.getByRole("button", { name: "Operator, admin, account menu" }).click()
    const item = page.getByRole("menuitemcheckbox", { name: "Pause animations", exact: true })
    await expect(item).toHaveAttribute("aria-checked", "true")
    await item.click()
    await expect(page.locator("html")).not.toHaveAttribute("data-motion", "paused")
    await page.keyboard.press("Escape")
    await expect.poll(() => moving(page)).not.toEqual([])
  })

  test("is on the page before the app is: the document's own script reads it", async ({ page }) => {
    await page.addInitScript(() => localStorage.setItem("just-dashboard.motion", "paused"))
    await mockShell(page)
    // No application script runs, so only the inline bootstrap can have set it.
    await page.route("**/_next/static/**/*.js", (route) => route.abort())
    await page.goto("/account", { waitUntil: "domcontentloaded" })
    await expect(page.locator("html")).toHaveAttribute("data-motion", "paused")
  })

  test("is what motion-reduce and motion-safe answer to", async ({ page }) => {
    await mockShell(page)
    await page.goto("/account")
    await expect(rail(page)).toBeVisible()

    // Two classes the product ships (a beam's pulse, a pipeline's still mark),
    // on probes: hidden under reduced motion, and hidden without it.
    const shown = () =>
      page.evaluate(() => {
        const read = (className: string) => {
          const probe = document.createElement("div")
          probe.className = className
          document.body.append(probe)
          const display = getComputedStyle(probe).display
          probe.remove()
          return display !== "none"
        }
        return { reduce: read("motion-reduce:hidden"), safe: read("motion-safe:hidden") }
      })

    expect(await shown()).toEqual({ reduce: true, safe: false })
    await pause(page)
    expect(await shown()).toEqual({ reduce: false, safe: true })
  })
})
