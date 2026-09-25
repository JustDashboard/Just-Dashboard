import { expect, test, type Page, type Route } from "@playwright/test"
import { mockProject } from "./deploy-fixture"

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
    if (path === "/deploy/" && url.searchParams.get("view") === "fleet") {
      return json(route, {
        deployments: [],
        activeWork: [],
        slots: { heavyUsed: 0, heavyCapacity: 2, lightUsed: 0, lightCapacity: 4 },
      })
    }
    if (path === "/deploy/github-app/") return json(route, { configured: false, installations: [] })
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
  "/packages",
  "/security",
  "/backups",
  "/deploy",
  "/deploy?view=archived",
  "/deploy/notifications",
  "/deploy/credentials",
  "/deploy/new",
  "/account",
  "/account/security",
  "/account/sessions",
  "/account/keys",
  "/account/users",
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

/** Every fully rounded, filled element with text in it — the pill §4 deleted. */
function filledPills(page: Page) {
  return page.evaluate(() => {
    const bad: string[] = []
    for (const el of document.querySelectorAll<HTMLElement>("span, div")) {
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

/** The registers the page elements declare, and how many flow panels are drawn. */
function registers(page: Page) {
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
  test.setTimeout(SURFACES.length * 5_000)
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
  test.setTimeout(PROJECT_SURFACES.length * 5_000)
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
