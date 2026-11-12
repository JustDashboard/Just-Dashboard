import { expect, test, type Page, type Route } from "@playwright/test"
import { DATABASE_SURFACES, mockDatabases } from "./database-fixture"
import { answerSystemUsers } from "./system-users-fixture"
import { mockProject } from "./deploy-fixture"
import { overview as remoteDesktopOverview } from "./remote-desktop-fixture"
import { busyMap } from "./server-map-fixture"
import { risen } from "./risen"
import { SHIFT_BUDGET, watchShifts } from "./layout-shift"
import { mockWorkbench } from "./workbench-fixture"
import { mockProxy } from "./proxy-fixtures"
import { mockIntrusion } from "./security-intrusion-fixture"
import { mockNetwork } from "./network-fixture"

/**
 * The design system's rules, checked in a browser rather than in a document.
 *
 * Each assertion here corresponds to a defect the September unification pass
 * shipped and a manual audit later found. None of them could have been caught by
 * `tsc` or by a unit test, because in every case the markup was valid and the
 * component rendered — it just rendered something a person could not reach:
 *
 *   five row-action clusters were revealed only by `group-hover`, so on a touch
 *   screen the delete and rename buttons for a Redis key, a query result row and
 *   a table column did not exist;
 *
 *   eleven icon-only buttons carried a native `title` and no accessible name,
 *   so a screen reader announced "button" and nothing else;
 *
 *   an activatable table row drew the same wash for hover, for keyboard focus
 *   and for selection, so a keyboard user could not tell where they were.
 *
 * The checks are deliberately structural — "every X on this page satisfies Y" —
 * rather than pinned to the specific components that were wrong. A rule that
 * only knows about the instances already fixed catches nothing new.
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

async function json(route: Route, body: unknown) {
  await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(body) })
}

/**
 * Just enough of the API to get a signed-in shell with some rows in it. Anything
 * not named answers with an empty list, because a page that renders an empty
 * state is still a page whose controls have to be reachable and labelled.
 *
 * The deployment fleet is the one read that cannot be a list: it is an object,
 * and an empty list in its place is a page that throws rather than one that is
 * empty. The GitHub App's status on Credentials is the other.
 */
async function mockShell(page: Page) {
  await page.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url())
    const path = url.pathname.replace(/^\/api\/v1/, "")
    if (path === "/auth/session") return json(route, user)
    if (path === "/updates/self") return json(route, { current: "0.6.7", latest: "0.6.7" })
    if (await answerSystemUsers(route)) return
    if (path === "/deploy/" && url.searchParams.get("view") === "fleet") {
      return json(route, {
        deployments: [],
        activeWork: [],
        slots: { heavyUsed: 0, heavyCapacity: 2, lightUsed: 0, lightCapacity: 4 },
      })
    }
    if (path === "/deploy/github-app/") return json(route, { configured: false, installations: [] })
    if (path === "/remote-desktop/") return json(route, remoteDesktopOverview())
    if (path === "/map/") return json(route, busyMap)
    if (path === "/map/layout") return json(route, { layout: null })
    if (path === "/security/boundary")
      return json(route, { checks: [], allowlist: [], sshPorts: [] })
    if (path.startsWith("/audit")) {
      return json(route, {
        entries: [
          {
            id: 1,
            at: now,
            actor: "operator",
            action: "container.restart",
            target: "web",
            status: 200,
            method: "POST",
            path: "/docker/containers/web/restart",
            ip: "127.0.0.1",
          },
        ],
        total: 1,
      })
    }
    return json(route, [])
  })
}

const SURFACES = [
  "/audit",
  "/system-users",
  "/system-users/user/ion",
  "/system-users/user/postgres",
  "/system-users/new",
  "/system-users/groups",
  "/system-users/defaults",
  "/packages",
  "/security",
  "/backups",
  "/deploy",
  "/deploy?view=archived",
  "/deploy/notifications",
  "/deploy/credentials",
  "/deploy/variables",
  "/deploy/secret-stores",
  "/deploy/drains",
  "/deploy/new",
  "/account",
  "/account/security",
  "/account/sessions",
  "/account/keys",
  "/account/users",
  "/account/workspace",
  "/welcome",
  "/remote-desktop",
  "/remote-desktop/new",
  "/metrics/alerts",
  "/map",
] as const

/**
 * The deployment pages that need a project to draw anything: the fleet and the
 * archive with rows in them, and every settings page. They are served by the
 * deployment fixture's showcase rather than by `mockShell`, whose empty lists
 * would leave a settings page as a skeleton and a fleet as its empty state —
 * and the controls these checks are about are the ones on the rows: a card's
 * menu, a remove button on a domain, a glyph-only toggle on a variable.
 */
const PROJECT_SURFACES = [
  "/deploy",
  "/deploy?view=archived",
  "/deploy/7/runtime",
  "/deploy/7/settings/general",
  "/deploy/7/settings/build",
  "/deploy/7/settings/runtime",
  "/deploy/7/settings/variables",
  "/deploy/7/settings/domains",
  "/deploy/7/settings/storage",
  "/deploy/7/settings/databases",
  "/deploy/7/settings/automation",
  "/deploy/7/settings/danger",
] as const

test("reading pages begin with content while retaining an accessible page name", async ({
  page,
}) => {
  await mockShell(page)

  for (const path of ["/audit", "/account", "/deploy"]) {
    await page.goto(path)
    const surface = page.locator("[data-slot=page]").first()
    await expect(surface).toBeVisible()
    await expect(surface.locator("[data-slot=page-header]")).toHaveCount(0)
    await expect(surface.locator("[data-slot=page-context]")).toHaveCount(0)
    await expect(surface.getByRole("heading", { level: 1 })).toHaveClass(/sr-only/)
    if (path === "/deploy") {
      await page.screenshot({ path: "test-results/page-context-deploy-1280.png", fullPage: true })
    }
  }
})

/** Every visible button with no text of its own and no name from anywhere else. */
function unnamedControls(page: Page) {
  return page.evaluate(() => {
    const bad: string[] = []
    for (const el of document.querySelectorAll<HTMLElement>("button, [role='button']")) {
      if (el.offsetParent === null && el.getAttribute("aria-hidden") !== "true") continue
      const text = (el.textContent ?? "").trim()
      if (text.length > 0) continue
      const named =
        el.getAttribute("aria-label") ||
        el.getAttribute("aria-labelledby") ||
        el.querySelector(".sr-only")
      if (!named) bad.push(el.outerHTML.slice(0, 160))
    }
    return bad
  })
}

/** Fully rounded, filled labels; a person's avatar is the one identity exception. */
function filledPills(page: Page) {
  return page.evaluate(() => {
    const bad: string[] = []
    for (const el of document.querySelectorAll<HTMLElement>("span, div")) {
      if (el.dataset.slot === "user-avatar") continue
      const s = getComputedStyle(el)
      const r = parseFloat(s.borderTopLeftRadius)
      const h = el.getBoundingClientRect().height
      if (!h || h > 32 || r < h / 2) continue
      const filled = s.backgroundColor !== "rgba(0, 0, 0, 0)" && s.backgroundColor !== "transparent"
      const text = (el.textContent ?? "").trim()
      // A status dot is a filled circle with no text in it, and is the point.
      if (filled && text.length > 0) bad.push(el.outerHTML.slice(0, 140))
    }
    return bad
  })
}

/**
 * Text a centred row sets off its own centre line. A bare span, link or cell in
 * a flex row is a block that keeps the line box it inherits — the page's 16px
 * one where nothing set another — so a 12px status inside it rests on that
 * box's baseline, three pixels below the button beside it. The database strip,
 * a deployment's route and certificate columns and the pending-changes link
 * all shipped that way; the box has to be a flex box, or carry its content's
 * own type size.
 */
function offCentreText(page: Page) {
  return page.evaluate(() => {
    const bad: string[] = []
    for (const item of document.querySelectorAll<HTMLElement>("body *")) {
      const row = item.parentElement
      if (!row) continue
      const rowStyle = getComputedStyle(row)
      if (!rowStyle.display.endsWith("flex") || !rowStyle.flexDirection.startsWith("row")) continue
      const style = getComputedStyle(item)
      const align = ["auto", "normal"].includes(style.alignSelf)
        ? rowStyle.alignItems
        : style.alignSelf
      if (align !== "center" || style.display !== "block" || style.position === "absolute") continue
      const box = item.getBoundingClientRect()
      if (box.height < 2 || box.width < 2) continue
      const blocks = [...item.children].some((child) => {
        const display = getComputedStyle(child).display
        return display !== "none" && !display.startsWith("inline")
      })
      if (blocks) continue
      const range = document.createRange()
      range.selectNodeContents(item)
      const ink = [...range.getClientRects()].filter((r) => r.width > 0 && r.height > 0)
      if (ink.length === 0) continue
      const top = Math.min(...ink.map((r) => r.top))
      const bottom = Math.max(...ink.map((r) => r.bottom))
      const offset = (top + bottom) / 2 - (box.top + box.bottom) / 2
      if (Math.abs(offset) >= 1.5) {
        bad.push(`${offset.toFixed(1)}px ${item.outerHTML.slice(0, 140)}`)
      }
    }
    return bad
  })
}

/** The registers the page elements declare, and how many flow panels are drawn. */
async function registers(page: Page) {
  // networkidle says the requests are done, not that React has committed the
  // page that the last answer unlocked; on a loaded runner the walk read the
  // DOM a beat too early and reported a page that was there a moment later.
  await page.waitForSelector("[data-slot='page']", { timeout: 5_000 }).catch(() => undefined)
  return page.evaluate(() => {
    const pages = [...document.querySelectorAll<HTMLElement>("[data-slot='page']")]
    return {
      registers: pages.map((el) => el.dataset.register ?? "(unset)"),
      panels: document.querySelectorAll("[data-slot='flow-panel']").length,
    }
  })
}

/**
 * Rule 5: selection, hover and focus are three different mechanisms, so a
 * control can be all three at once and stay readable. Every one of them was
 * `bg-row-hover` on an activatable row, and the focus ring did not exist.
 *
 * Checked against a probe carrying the shipped classes rather than against a
 * specific table, because the contract belongs to `focus-ring-inset` and to
 * `TableRow`, not to whichever page happens to have rows in it today. The probe
 * is a real `<tr>` in a real `<table>`, so the rule is evaluated exactly as the
 * browser evaluates it on the product's own rows.
 */
test("hover, selection and focus are three different marks on a row", async ({ page }) => {
  await mockShell(page)
  await page.goto("/audit")
  await page.waitForLoadState("networkidle")

  // A real `<tr>` in a real `<table>`, carrying exactly the classes `TableRow`
  // ships, so the rule is evaluated the way the browser evaluates it on the
  // product's own rows.
  await page.evaluate(() => {
    const table = document.createElement("table")
    table.style.position = "fixed"
    table.style.top = "0"
    const body = document.createElement("tbody")
    const tr = document.createElement("tr")
    tr.id = "ds-probe-row"
    tr.setAttribute("data-slot", "table-row")
    tr.className =
      "border-b border-hairline transition-colors hover:bg-row-hover data-[state=selected]:bg-accent cursor-pointer focus-ring-inset"
    tr.tabIndex = 0
    tr.innerHTML = "<td>row</td>"
    body.append(tr)
    table.append(body)
    document.body.append(table)
  })

  const read = () =>
    page.locator("#ds-probe-row").evaluate((el) => {
      const s = getComputedStyle(el)
      return {
        background: s.backgroundColor,
        outlineStyle: s.outlineStyle,
        outlineWidth: s.outlineWidth,
        outlineColor: s.outlineColor,
      }
    })

  // Park the pointer away from the probe. It is `position: fixed; top: 0`, which
  // is where Playwright's virtual mouse also starts, so without this the row is
  // hovered — and whether the browser has hit-tested it yet decides whether the
  // resting read already carries the hover wash. That race is what made this
  // assertion fail intermittently on an otherwise correct row.
  await page.mouse.move(900, 700)
  const atRest = await read()
  // `:focus-visible` follows the browser's own modality heuristic, so the focus
  // has to arrive by keyboard. A programmatic `.focus()` alone does not match
  // it, and a test that used one would pass against a ring that no keyboard
  // user ever sees.
  await page.keyboard.press("Tab")
  await page.locator("#ds-probe-row").focus()
  const whenFocused = await read()

  // The resting outline is transparent and the focused one is painted. Compared
  // against each other rather than against a literal: the computed colour comes
  // back in whichever space the browser resolved the token into, which differs
  // between a page whose theme block has loaded and one whose has not.
  expect(whenFocused.outlineStyle).not.toBe("none")
  expect(parseFloat(whenFocused.outlineWidth)).toBeGreaterThan(0)
  expect(whenFocused.outlineColor).not.toBe(atRest.outlineColor)
  // Focus does not repaint the row, so it cannot be mistaken for hover.
  expect(whenFocused.background).toBe(atRest.background)

  // Selection is a fill, and a different one from hover. It used to be
  // `bg-row-hover` — the value hover already uses — which left the two
  // indistinguishable.
  //
  // Read off rendered rows rather than by scanning stylesheet text: Tailwind v4
  // emits its utilities inside `@layer` blocks whose contents the CSSOM does not
  // expose as enumerable rules, so a `cssText` scan reports nothing on a page
  // whose CSS is demonstrably correct. What the assertion is about is the colour
  // a reader sees, which is what `getComputedStyle` answers.
  const fills = await page.evaluate(() => {
    const paint = (attrs: Record<string, string>) => {
      const table = document.createElement("table")
      table.style.cssText = "position:fixed;top:0;left:0"
      const body = document.createElement("tbody")
      const tr = document.createElement("tr")
      tr.className =
        "border-b border-hairline transition-colors hover:bg-row-hover data-[state=selected]:bg-accent"
      for (const [k, v] of Object.entries(attrs)) tr.setAttribute(k, v)
      tr.innerHTML = "<td>row</td>"
      body.append(tr)
      table.append(body)
      document.body.append(table)
      const bg = getComputedStyle(tr).backgroundColor
      table.remove()
      return bg
    }
    // The hover value is read from a `--row-hover` probe rather than by moving
    // the pointer, so the two colours are compared without a hit-test race.
    const swatch = document.createElement("div")
    swatch.className = "bg-row-hover"
    document.body.append(swatch)
    const hover = getComputedStyle(swatch).backgroundColor
    swatch.remove()
    return { rest: paint({}), selected: paint({ "data-state": "selected" }), hover }
  })

  expect(fills.selected, "no fill defined for a selected row").not.toBe(fills.rest)
  expect(fills.selected, "selection paints the hover value").not.toBe(fills.hover)
})

/**
 * The same rule on the rail, read off the product's own rows rather than a
 * probe: every sidebar class list carried `outline-hidden` after `focus-ring`,
 * which set the outline's style back to none, so a keyboard walked through
 * every page's navigation with nothing on screen saying where it was.
 */
test("a sidebar row shows the keyboard where it is", async ({ page }) => {
  await mockShell(page)
  await page.goto("/audit")
  const rows = page.locator(
    '[data-slot="sidebar"] :is([data-slot="sidebar-menu-button"], [data-slot="sidebar-menu-sub-button"], [data-slot="sidebar-menu-action"], [data-slot="sidebar-group-action"])',
  )
  // The audit log opens the rail on its own group; the whole rail is the walk.
  await page.locator('[data-slot="sidebar"]').getByText("All pages").click()
  await expect.poll(() => rows.count()).toBeGreaterThan(5)
  await page.mouse.move(900, 700)

  // `:focus-visible` needs the focus to have come from the keyboard (see the
  // row test above), so one Tab sets the modality and each row is then focused
  // in turn, as Tab itself would reach it.
  await page.keyboard.press("Tab")
  const marks = await rows.evaluateAll((elements) =>
    elements
      .filter((el): el is HTMLElement => el instanceof HTMLElement && el.offsetParent !== null)
      .map((el) => {
        const outline = () => {
          const s = getComputedStyle(el)
          return { style: s.outlineStyle, width: parseFloat(s.outlineWidth), color: s.outlineColor }
        }
        ;(document.activeElement as HTMLElement | null)?.blur()
        const rest = outline()
        el.focus()
        return {
          row: el.textContent?.trim() || el.getAttribute("aria-label") || el.dataset.slot,
          visible: el.matches(":focus-visible"),
          rest,
          focused: outline(),
        }
      }),
  )

  expect(marks.length).toBeGreaterThan(5)
  for (const mark of marks) {
    expect(mark.visible, `${mark.row} takes keyboard focus`).toBe(true)
    expect(mark.focused.style, `${mark.row} draws no outline when focused`).not.toBe("none")
    expect(mark.focused.width, `${mark.row} has no ring width`).toBeGreaterThan(0)
    expect(mark.focused.color, `${mark.row} is the same focused as at rest`).not.toBe(
      mark.rest.color,
    )
  }
})

/**
 * `IconAction` exists so that an icon-only control carries its label in a
 * tooltip *and* in `aria-label`. A native `title` is neither: the browser holds
 * it back for about a second, and a screen reader announces it only weakly.
 */
for (const path of SURFACES) {
  test(`every icon-only control on ${path} has an accessible name`, async ({ page }) => {
    await mockShell(page)
    await page.goto(path)
    await page.waitForLoadState("networkidle")

    const unnamed = await unnamedControls(page)
    expect(unnamed, `unlabelled icon-only controls on ${path}`).toEqual([])
  })
}

for (const path of PROJECT_SURFACES) {
  test(`every icon-only control on ${path} has an accessible name, with a project`, async ({
    page,
  }) => {
    await mockProject(page, { showcase: true })
    await page.goto(path)
    await page.waitForLoadState("networkidle")

    const unnamed = await unnamedControls(page)
    expect(unnamed, `unlabelled icon-only controls on ${path}`).toEqual([])
  })
}

/**
 * The same rule at a phone's width, where the deployment pages trade words
 * for glyphs: a toggle that reads "HTTPS" at 1280 is a padlock at 390, and a
 * padlock with no name is a button a screen reader calls "button".
 */
test.describe("at a phone's width", () => {
  test.use({ viewport: { width: 390, height: 844 } })

  for (const path of PROJECT_SURFACES) {
    test(`every icon-only control on ${path} has an accessible name`, async ({ page }) => {
      await mockProject(page, { showcase: true })
      await page.goto(path)
      await page.waitForLoadState("networkidle")

      const unnamed = await unnamedControls(page)
      expect(unnamed, `unlabelled icon-only controls on ${path} at 390`).toEqual([])
    })
  }
})

/**
 * A database's pages need a connection to draw anything — its strip, its
 * rail, the page under them — so they are walked with the database fixture,
 * on a connection that carries both of its labels: the strip's tags, its
 * status and its three controls are the row these rules are most easily
 * broken on, and they are on every page.
 */
const LABELLED = { rows: { 1: { environment: "production", readOnly: true } } }

for (const [label, viewport] of [
  ["", { width: 1280, height: 800 }],
  [" at a phone's width", { width: 390, height: 844 }],
] as const) {
  test(`the databases pages keep the rules${label}`, async ({ page }) => {
    test.setTimeout(DATABASE_SURFACES.length * 10_000)
    await page.setViewportSize(viewport)
    await mockDatabases(page, LABELLED)

    for (const path of DATABASE_SURFACES) {
      await page.goto(path)
      await page.waitForLoadState("networkidle")
      await expect(page.locator("[data-slot=page]").first()).toBeVisible({ timeout: 15_000 })

      expect(await unnamedControls(page), `unlabelled icon-only controls on ${path}`).toEqual([])
      expect(await offCentreText(page), `text off its row's centre line on ${path}`).toEqual([])
      expect(await filledPills(page), `fully rounded filled chips on ${path}`).toEqual([])
      const seen = await registers(page)
      expectOneRegister(path, seen)
      // Adding a database and bringing one over from another server are the
      // section's sequences with an outcome.
      expect(seen.registers.includes("flow"), `${path} is in the wrong register`).toBe(
        path === "/databases/new" || path === "/databases/1/import",
      )
      const sideways = await page.evaluate(() =>
        [document.documentElement, ...document.querySelectorAll("[data-slot=page]")]
          .map((el) => el.parentElement ?? el)
          .some((el) => el.scrollWidth > el.clientWidth + 1),
      )
      expect(sideways, `${path} scrolls sideways`).toBe(false)
    }
  })

  test(`a database's switcher and its Connect popover keep the rules${label}`, async ({ page }) => {
    await page.setViewportSize(viewport)
    await mockDatabases(page, LABELLED)
    await page.goto("/databases/1/performance")

    await page.getByRole("button", { name: /^Database: shop/ }).click()
    await expect(page.getByRole("option").first()).toBeVisible()
    await page.waitForLoadState("networkidle")
    expect(await unnamedControls(page), "unlabelled controls in the switcher").toEqual([])
    expect(await offCentreText(page), "text off its centre line in the switcher").toEqual([])
    expect(await filledPills(page), "a filled chip in the switcher").toEqual([])
    await page.keyboard.press("Escape")

    await page.getByRole("button", { name: "Connect" }).click()
    await expect(page.locator("[data-slot=connection-string]")).toBeVisible()
    await page.waitForLoadState("networkidle")
    expect(await unnamedControls(page), "unlabelled controls in Connect").toEqual([])
    expect(await offCentreText(page), "text off its centre line in Connect").toEqual([])
    expect(await filledPills(page), "a filled chip in Connect").toEqual([])
  })
}

/**
 * The reveal rule's touch clause. A cluster shown only on `group-hover` is
 * permanently invisible on a device that has no hover, which is how five of
 * these ended up unreachable on a phone.
 */
test.describe("with no hover available", () => {
  test.use({ hasTouch: true, viewport: { width: 390, height: 844 } })

  for (const path of ["/audit", "/system-users"] as const) {
    test(`row actions on ${path} are visible without a pointer`, async ({ page }) => {
      await mockShell(page)
      await page.goto(path)
      await page.waitForLoadState("networkidle")

      const hidden = await page.evaluate(() => {
        const bad: string[] = []
        for (const el of document.querySelectorAll<HTMLElement>(
          "[data-slot='table-row'] button, [data-slot='table-row'] a",
        )) {
          if (parseFloat(getComputedStyle(el).opacity) < 0.1) {
            bad.push(el.getAttribute("aria-label") ?? el.outerHTML.slice(0, 120))
          }
        }
        return bad
      })
      expect(hidden, `controls hidden behind hover on ${path}`).toEqual([])
    })
  }
})

test("a centred row sets every item's text on its centre line", async ({ page }) => {
  test.setTimeout(SURFACES.length * 10_000)
  await mockShell(page)

  for (const path of SURFACES) {
    await page.goto(path)
    await page.waitForLoadState("networkidle")
    // A figure that has just landed is four pixels into its rise, and off any line.
    await risen(page)
    expect(await offCentreText(page), `text off its row's centre line on ${path}`).toEqual([])
  }
})

test("a centred row sets every item's text on its centre line, with a project", async ({
  page,
}) => {
  test.setTimeout(PROJECT_SURFACES.length * 10_000)
  await mockProject(page, { showcase: true })

  for (const path of PROJECT_SURFACES) {
    await page.goto(path)
    await page.waitForLoadState("networkidle")
    await expect(page.locator("[data-slot=page]").first()).toBeVisible({ timeout: 15_000 })
    expect(await offCentreText(page), `text off its row's centre line on ${path}`).toEqual([])
  }
})

/**
 * Rule 2: there is no pill. The one status indicator is a dot and a word, and a
 * fixed property is a squared hairline `Tag`. A fully rounded filled chip is the
 * shape this product reserves for nothing at all.
 */
test("no page renders a filled pill", async ({ page }) => {
  await mockShell(page)
  await page.goto("/audit")
  await page.waitForLoadState("networkidle")

  const pills = await filledPills(page)
  expect(pills, "fully rounded filled chips").toEqual([])
})

/**
 * The deployment pages are where a pill is most tempting — a run's state, a
 * change's kind, a count of pending changes, a strip of outcomes — so each is
 * checked with the rows that would carry one.
 */
for (const path of PROJECT_SURFACES) {
  test(`${path} renders no filled pill`, async ({ page }) => {
    await mockProject(page, { showcase: true })
    await page.goto(path)
    await page.waitForLoadState("networkidle")

    const pills = await filledPills(page)
    expect(pills, `fully rounded filled chips on ${path}`).toEqual([])
  })
}

/**
 * Credentials drew the GitHub App's setup as numbered filled circles and an
 * installed account as a letter in one, and Notifications drew each channel as
 * a monogram disc — the pill three times over — so both are checked with the
 * showcase's App, accounts and channels in them.
 */
for (const path of ["/deploy/credentials", "/deploy/notifications"]) {
  test(`${path} renders no filled pill`, async ({ page }) => {
    await mockProject(page, { showcase: true })
    await page.goto(path)
    await page.waitForLoadState("networkidle")

    const pills = await filledPills(page)
    expect(pills, `fully rounded filled chips on ${path}`).toEqual([])
  })
}

/**
 * Motion is a preference, not a per-component decision. The rule lives once at
 * the root of `globals.css`, so a new animation is covered by default.
 */
test("reduced motion is honoured globally", async ({ page }) => {
  await page.emulateMedia({ reducedMotion: "reduce" })
  await mockShell(page)
  await page.goto("/audit")

  const moving = await page.evaluate(() => {
    const bad: string[] = []
    for (const el of document.querySelectorAll<HTMLElement>("*")) {
      const s = getComputedStyle(el)
      if (s.animationName !== "none" && parseFloat(s.animationDuration) > 0.05) {
        bad.push(`${el.tagName}.${el.className} ${s.animationName} ${s.animationDuration}`)
      }
    }
    return bad
  })
  expect(moving, "animations still running under prefers-reduced-motion").toEqual([])
})

/**
 * §11: a wire's pulse says *live* for as long as its page is open, which on a
 * dashboard is all day. So it is the browser's own animation of `transform`,
 * which runs off the main thread, and no script's: until 0.8.6 a JavaScript
 * frame loop rewrote a gradient sixty times a second on every page with a
 * wire on it, and a page that looked still was never idle. The page here has
 * wires pulsing and asks for no animation frames at all.
 */
test("a wire pulses as the browser's animation, and its page asks for no frames", async ({
  page,
}) => {
  await page.addInitScript(() => {
    const counted = window as unknown as { frames: number }
    counted.frames = 0
    const request = window.requestAnimationFrame.bind(window)
    window.requestAnimationFrame = (callback) => {
      counted.frames++
      return request(callback)
    }
  })
  await mockShell(page)
  // A session seen this minute: its line to the account is one that pulses.
  const seen = new Date().toISOString()
  await page.route("**/api/v1/account/sessions", (route) =>
    json(route, [
      {
        id: "session-1",
        userId: 1,
        authMethod: "passkey",
        twoFactorPassed: true,
        ip: "100.64.0.7",
        userAgent: "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 Chrome/141.0 Safari/537.36",
        createdAt: seen,
        lastSeenAt: seen,
        expiresAt: new Date(Date.now() + 86_400_000).toISOString(),
        current: true,
      },
    ]),
  )
  await page.goto("/account")
  const pulse = page.locator("[data-slot=beam-pulse] rect").first()
  await expect(pulse).toBeAttached()
  const moves = await pulse.evaluate((band) =>
    band.getAnimations().map((pass) => ({
      name: (pass as CSSAnimation).animationName,
      endless: pass.effect?.getComputedTiming().iterations === Infinity,
      moves: [
        ...new Set(
          (pass.effect as KeyframeEffect)
            .getKeyframes()
            .flatMap((frame) => Object.keys(frame))
            .filter((key) => !["offset", "computedOffset", "easing", "composite"].includes(key)),
        ),
      ],
    })),
  )
  expect(moves).toEqual([{ name: "jd-beam", endless: true, moves: ["transform"] }])

  // What arrives with the page (a rise, a figure counting up) has arrived.
  await risen(page)
  await page.waitForTimeout(1500)
  await page.evaluate(() => {
    ;(window as unknown as { frames: number }).frames = 0
  })
  await page.waitForTimeout(1000)
  const frames = await page.evaluate(() => (window as unknown as { frames: number }).frames)
  // The loop asked for sixty a second. A poll landing in the second may ask for a few.
  expect(frames, "animation frames asked for in an idle second").toBeLessThan(10)
})

/**
 * §16: a page is in exactly one register, and register B's affordances belong
 * only to the pages that declared it.
 *
 * The section promises this check by name, so it exists rather than being a
 * sentence — an unenforced rule in this document is the drift the whole file
 * was written to stop. Three properties, each of which is a way the register
 * could rot back into the thing it replaced:
 *
 *   a page declares a register, and it is one of the two;
 *   at most one surface per screen carries depth, because a second foreground
 *   is no foreground (§16) — the rule the rollout was most likely to break;
 *   no reading page grows a `FlowPanel`, which is how "just this once" becomes
 *   forty-nine raised surfaces again.
 */
test("every page declares one register, and only a flow page has a foreground", async ({
  page,
}) => {
  // One test walks every surface, so its budget grows with the list: at the
  // default thirty seconds, thirteen pages under a parallel run timed out.
  test.setTimeout(SURFACES.length * 10_000)
  await mockShell(page)

  for (const path of SURFACES) {
    await page.goto(path)
    await page.waitForLoadState("networkidle")
    await expect(page.locator("[data-slot=page]").first()).toBeVisible({ timeout: 15_000 })
    expectOneRegister(path, await registers(page))
  }
})

/**
 * The deployment settings are §16's named example of a reading page that
 * happens to be editable, and the fleet is one you read — so none of them may
 * grow a foreground, however many lit choices the redesign gives them.
 */
test("the deployment pages are reading pages with no foreground", async ({ page }) => {
  test.setTimeout(PROJECT_SURFACES.length * 10_000)
  await mockProject(page, { showcase: true })

  for (const path of PROJECT_SURFACES) {
    await page.goto(path)
    await page.waitForLoadState("networkidle")
    await expect(page.locator("[data-slot=page]").first()).toBeVisible({ timeout: 15_000 })
    const seen = await registers(page)
    expectOneRegister(path, seen)
    expect(seen.registers, `${path} is not a reading page`).not.toContain("flow")
  }
})

function expectOneRegister(path: string, seen: { registers: string[]; panels: number }) {
  expect(seen.registers.length, `no page element rendered on ${path}`).toBeGreaterThan(0)
  for (const register of seen.registers) {
    expect(["reading", "flow"], `${path} declares an unknown register`).toContain(register)
  }

  const isFlow = seen.registers.includes("flow")
  // One is the contract; zero is fine, because a flow screen may be a
  // question with a grid of choices under it and no focused surface at all.
  expect(seen.panels, `more than one foreground on ${path}`).toBeLessThanOrEqual(1)
  if (!isFlow) {
    expect(seen.panels, `a reading page drew a flow panel on ${path}`).toBe(0)
  }
}

/**
 * The structure a screen reader walks by: one `h1` on every page, the
 * workbenches that draw no header included; headings that never skip a level,
 * so a settings page's sections sit under its title rather than a step past
 * it; and nothing drawn outside a landmark, which the version beside the
 * wordmark was on every page.
 */
const headings = (page: Page) =>
  page.evaluate(() =>
    [...document.querySelectorAll("h1, h2, h3, h4, h5, h6")]
      .filter((node) => !node.closest("[hidden], [aria-hidden=true], [role=dialog]"))
      .map((node) => ({ level: Number(node.tagName[1]), text: node.textContent?.trim() ?? "" })),
  )

for (const [path, name] of [
  ["/files", "Files"],
  ["/terminal", "Terminal"],
] as const) {
  test(`${path} names itself in one h1 though it draws no header`, async ({ page }) => {
    await mockShell(page)
    await page.goto(path)
    await expect(page.locator("[data-slot=page]").first()).toBeVisible()
    await expect(page.getByRole("heading", { level: 1 })).toHaveText(name)
    expect((await headings(page)).filter((heading) => heading.level === 1)).toHaveLength(1)
  })
}

test("a settings page's sections are the level under its title", async ({ page }) => {
  test.setTimeout(60_000)
  await mockProject(page, { showcase: true })
  for (const path of [
    "/deploy/7/settings/general",
    "/deploy/7/settings/domains",
    "/deploy/7/settings/variables",
  ]) {
    await page.goto(path)
    await page.waitForLoadState("networkidle")
    // The reads have landed; the sections are drawn a moment after, and a
    // block still held under a loading one is not a heading yet.
    await expect(page.locator("[data-slot=page] h2").first()).toBeVisible()
    const seen = await headings(page)
    expect(seen[0]?.level, `${path} does not open on an h1`).toBe(1)
    const skipped = seen.filter(
      (heading, index) => index > 0 && heading.level > seen[index - 1].level + 1,
    )
    expect(skipped, `headings that skip a level on ${path}`).toEqual([])
    expect(seen.filter((heading) => heading.level === 2).length).toBeGreaterThan(0)
  }
})

test("the wordmark's version stands inside a landmark", async ({ page }) => {
  await mockShell(page)
  await page.goto("/audit")
  const banner = page.getByRole("banner")
  await expect(banner).toHaveCount(1)
  await expect(banner).toContainText(/\d+\.\d+\.\d+/)
  await expect(banner.getByRole("button", { name: "Search" })).toBeVisible()
})

/**
 * Installs `window.jdContrast(element)`: the WCAG contrast ratio of an
 * element's text against what is drawn behind it, as the screen has it.
 *
 * Measured, not computed from the tokens: every ancestor's background is
 * painted onto one pixel of a canvas, outermost first, then the element's own
 * colour at the opacity it and its ancestors give it, and the two pixels are
 * read back as the 8-bit sRGB the browser resolved them to.
 */
async function installContrast(page: Page) {
  await page.evaluate(() => {
    const canvas = document.createElement("canvas")
    canvas.width = canvas.height = 1
    const ctx = canvas.getContext("2d", { willReadFrequently: true })!
    const paint = (colour: string, alpha = 1) => {
      ctx.globalAlpha = alpha
      ctx.fillStyle = colour
      ctx.fillRect(0, 0, 1, 1)
    }
    const luminance = () => {
      const [r, g, b] = [...ctx.getImageData(0, 0, 1, 1).data].map((v) => {
        const c = v / 255
        return c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4
      })
      return 0.2126 * r + 0.7152 * g + 0.0722 * b
    }
    Reflect.set(window, "jdContrast", (element: Element) => {
      const chain: Element[] = []
      for (let node: Element | null = element; node; node = node.parentElement) chain.unshift(node)
      ctx.clearRect(0, 0, 1, 1)
      let opacity = 1
      for (const node of chain) {
        const style = getComputedStyle(node)
        paint(style.backgroundColor)
        opacity *= Number(style.opacity)
      }
      const ground = luminance()
      paint(getComputedStyle(element).color, opacity)
      const ink = luminance()
      return (Math.max(ink, ground) + 0.05) / (Math.min(ink, ground) + 0.05)
    })
  })
}

test("faded secondary text keeps 4.5:1 on every ground text is set on", async ({ page }) => {
  await mockShell(page)
  await page.goto("/account")
  await expect(page.locator('[data-slot="page"]')).toBeVisible()
  await installContrast(page)

  // The faint ink, and the faded spellings of the muted ink that resolve to
  // it: /60 measured 3.4:1 and /40 2.1:1 before they had a floor.
  const inks = [
    "text-faint",
    "text-muted-foreground/70",
    "text-muted-foreground/60",
    "text-muted-foreground/50",
    "text-muted-foreground/45",
    "text-muted-foreground/40",
  ]
  const grounds = ["--background", "--card", "--popover", "--muted", "--surface-header"]
  const measured = await page.evaluate(
    ({ inks, grounds }) => {
      const contrast = Reflect.get(window, "jdContrast") as (element: Element) => number
      const out: { ink: string; ground: string; ratio: number }[] = []
      for (const ground of grounds) {
        const plate = document.createElement("div")
        plate.style.background = `var(${ground})`
        document.body.append(plate)
        for (const ink of inks) {
          const word = document.createElement("span")
          word.className = `text-micro ${ink}`
          word.textContent = "12 rows"
          plate.append(word)
          out.push({ ink, ground, ratio: Math.round(contrast(word) * 100) / 100 })
        }
        plate.remove()
      }
      return out
    },
    { inks, grounds },
  )
  expect(measured).toHaveLength(inks.length * grounds.length)
  expect(measured.filter((m) => m.ratio < 4.5)).toEqual([])
  // The token is the floor, not a second muted: on the page it is the faint
  // ink's 5.3:1, well under the muted ink's 7.6:1.
  const onPage = measured.find((m) => m.ink === "text-faint" && m.ground === "--background")!
  expect(onPage.ratio).toBeGreaterThan(5)
  expect(onPage.ratio).toBeLessThan(6)

  // A glyph keeps its fade, and a colour asked for on hover still wins.
  const kept = await page.evaluate(() => {
    const colour = (markup: string) => {
      const holder = document.createElement("div")
      holder.innerHTML = markup
      document.body.append(holder)
      const value = getComputedStyle(holder.firstElementChild!).color
      holder.remove()
      return value
    }
    return {
      word: colour('<span class="text-muted-foreground/60">x</span>'),
      faint: colour('<span class="text-faint">x</span>'),
      glyph: colour('<svg class="text-muted-foreground/60"></svg>'),
    }
  })
  expect(kept.word).toBe(kept.faint)
  expect(kept.glyph).not.toBe(kept.faint)
  await page.evaluate(() => {
    const probe = document.createElement("button")
    probe.id = "jd-hover-probe"
    probe.className = "fixed top-0 left-0 z-50 text-muted-foreground/60 hover:text-foreground"
    probe.textContent = "probe"
    document.body.append(probe)
  })
  await page.hover("#jd-hover-probe")
  expect(
    await page.evaluate(() => {
      const probe = document.getElementById("jd-hover-probe")!
      const ink = document.createElement("span")
      ink.className = "text-foreground"
      document.body.append(ink)
      return getComputedStyle(probe).color === getComputedStyle(ink).color
    }),
  ).toBe(true)
})

test("a row's verb at rest and a tab's count are still text: 4.5:1", async ({ page }) => {
  await mockShell(page)
  await page.route("**/api/v1/account/sessions", (route) =>
    json(route, [
      {
        id: "this",
        userId: 1,
        twoFactorPassed: true,
        ip: "203.0.113.7",
        userAgent: "Mozilla/5.0 (X11; Linux x86_64) Chrome/141.0",
        createdAt: now,
        lastSeenAt: now,
        expiresAt: now,
        current: true,
      },
      {
        id: "other",
        userId: 1,
        twoFactorPassed: true,
        ip: "198.51.100.9",
        userAgent: "Mozilla/5.0 (Macintosh) Safari/605.1.15",
        createdAt: now,
        lastSeenAt: now,
        expiresAt: now,
        current: false,
      },
    ]),
  )
  await page.goto("/account/sessions")
  // `exact`: the verb's accessible name says which session, and the page's own
  // "Sign out other sessions" is another button.
  const verb = page.getByText("Sign out", { exact: true }).first()
  await expect(verb).toBeVisible()
  await risen(page)
  await installContrast(page)
  // At rest, with the pointer nowhere near the row: the red verb drawn at 55%
  // measured 2.3:1.
  await page.mouse.move(0, 0)
  const contrast = (target: typeof verb) =>
    target.evaluate((node) =>
      (Reflect.get(window, "jdContrast") as (element: Element) => number)(node),
    )
  expect(await contrast(verb)).toBeGreaterThanOrEqual(4.5)
  // On the row it is the verb's own colour again.
  const resting = await verb.evaluate((node) => getComputedStyle(node).color)
  await verb.hover()
  await expect.poll(() => verb.evaluate((node) => getComputedStyle(node).color)).not.toBe(resting)

  // The count in an unselected chip: 60% of the muted ink was 3.4:1 at 10px.
  await page.evaluate(() => {
    const chip = document.createElement("div")
    chip.className = "text-muted-foreground"
    chip.innerHTML =
      '<span id="jd-count-probe" class="numeric text-micro tabular-nums opacity-80">13</span>'
    document.querySelector('[data-slot="page"]')!.append(chip)
  })
  expect(await contrast(page.locator("#jd-count-probe"))).toBeGreaterThanOrEqual(4.5)
})

/**
 * Nothing already on a page moves while the page loads. Eight pages did: a
 * block was a skeleton shorter than what replaced it, or was not there until
 * its read answered, and everything under it was pushed down when it landed,
 * past a quarter of the window on Certificates. The fixtures answer every
 * read at once, which hides it, so the reads are made to land apart, in one
 * order and then in the other (`watchShifts`), and the browser's own measure
 * of what moved is held to the budget, at a desk's width and at a phone's.
 */
const ARRIVING: [path: string, mock: (page: Page) => Promise<unknown>][] = [
  ["/audit", mockShell],
  ["/backups", (page) => mockWorkbench(page)],
  ["/deploy/credentials", mockShell],
  ["/proxy/certificates", (page) => mockProxy(page, { included: true })],
  ["/security/intrusion", (page) => mockIntrusion(page)],
  [
    "/network/firewall",
    // With a way in to preserve: the block that lists them is not on the page
    // until its read answers, and stands over the rules.
    (page) =>
      mockNetwork(page, [], {
        overrides: {
          "/firewall/access": {
            checks: [
              {
                name: "This dashboard",
                port: 18443,
                protocol: "tcp",
                family: "ipv4",
                source: "100.64.0.0/10",
                required: true,
                verdict: "admitted",
                reason: "rule 2 admits it",
              },
              {
                name: "SSH",
                port: 22,
                protocol: "tcp",
                family: "ipv4",
                source: "Anywhere",
                required: true,
                verdict: "admitted",
                reason: "rule 1 admits it",
              },
            ],
          },
        },
      }),
  ],
  ["/network/dns", (page) => mockNetwork(page)],
  ["/network/interfaces", (page) => mockNetwork(page)],
]

for (const [label, viewport] of [
  ["", { width: 1280, height: 800 }],
  [" on a phone", { width: 390, height: 844 }],
] as const) {
  for (const [path, mock] of ARRIVING) {
    test(`nothing on ${path} moves while its reads land${label}`, async ({ page }) => {
      test.setTimeout(90_000)
      await page.setViewportSize(viewport)
      await mock(page)
      const shifts = await watchShifts(page)
      for (const order of ["forward", "backward"] as const) {
        shifts.arrive(order)
        await page.goto(path)
        await expect(page.locator("[data-slot=page]").first()).toBeVisible()
        const { total, shifts: moved } = await shifts.moved()
        expect(
          total,
          `${path} moved by ${total.toFixed(3)} with its reads landing ${order}: ${JSON.stringify(moved)}`,
        ).toBeLessThan(SHIFT_BUDGET)
      }
    })
  }
}
