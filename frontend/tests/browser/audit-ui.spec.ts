import { readFile } from "node:fs/promises"
import { expect, test, type Page } from "@playwright/test"
import { answer, forbidden, json, mockAudit, trail, type Entry } from "./audit-fixture"
import { risen } from "./risen"

/**
 * The audit log after its 0.8.7 overhaul, against a mocked host whose trail
 * has every kind of caller in it (audit-fixture.ts).
 *
 * What these hold the page to is what the overhaul promised: it opens on an
 * identity line and the day as one strip, in the same place loading and
 * loaded, with no row of tiles; the trail is one line to a row, the same
 * height whatever a row carries; the action is the row's button and opens the
 * entry whole; every filter, the pager and the address still work; the export
 * hands over what the filters match; and an account that may not read the
 * trail is told so and offered nothing. system-ui.spec.ts keeps the marks.
 */

const identity = (page: Page) => page.getByRole("region", { name: "About the audit log" })
const strip = (page: Page) => page.getByTestId("audit-day")
const rows = (page: Page) => page.locator("[data-workspace-item]:visible")
const user = (page: Page) => page.getByRole("textbox", { name: "User" })
const action = (page: Page) => page.getByRole("textbox", { name: "Action" })

/** Every visible row's top and height, in the order they are drawn. */
const boxes = (page: Page) =>
  rows(page).evaluateAll((items) =>
    items.map((item) => {
      const box = item.getBoundingClientRect()
      return { top: Math.round(box.top), height: Math.round(box.height) }
    }),
  )

test("opens on what it is about: the day as a line of facts and one strip, and no tiles", async ({
  page,
}) => {
  await mockAudit(page)
  await page.goto("/audit")
  const line = identity(page)
  await expect(line).toHaveAttribute("data-layout", "project")
  await expect(line).toContainText("Audit log")
  // What the server refused is the state, said as a share nobody works out.
  await expect(line).toContainText("1 of 10 failed")
  await expect(line).toContainText("1 sign-in refused")
  await expect(line).toContainText("atlas")
  await expect(line).toContainText("10 changes in the last day")
  await expect(line).toContainText("3 people and 2 automated changes")
  await expect(line).toContainText("1 sign-in, the last by operator from")
  await expect(line.locator("img[src='/logos/tailscale.svg']")).toHaveCount(1)
  await expect(page.locator("[data-slot=stat-grid]")).toHaveCount(0)
  await expect(page.locator("[data-slot=stat-tile]")).toHaveCount(0)

  // The day is twenty-four hours, and the one refusal in it is the one stretch
  // that is not green or empty.
  const hours = strip(page).getByRole("listitem")
  await expect(hours).toHaveCount(24)
  await expect(strip(page).locator("[data-tone=dented], [data-tone=failed]")).toHaveCount(1)
  await expect(strip(page).locator("[data-tone=quiet]").first()).toBeVisible()
  // Under it, the parts of the product the day touched; pointing at an hour
  // puts that hour there instead, in the same line.
  const under = strip(page).locator("xpath=following-sibling::div[1]")
  await expect(under).toContainText("Sign-in")
  await expect(under).toContainText("24 hours ago to now")
  const before = await under.boundingBox()
  await strip(page).locator("[data-tone=dented], [data-tone=failed]").hover()
  await expect(under).toContainText(/1 change, and it failed|\d+ changes, 1 failed/)
  await expect(under).toContainText("Sign-in")
  expect((await under.boundingBox())?.height).toBe(before?.height)
})

test("the opening stands where it will before the day and the trail are read", async ({ page }) => {
  let gate: Promise<void> = Promise.resolve()
  const entries = trail()
  await mockAudit(page, {
    audit: async (route, url) => {
      await gate
      return json(route, answer(entries, url))
    },
  })
  for (const width of [1280, 390]) {
    let release!: () => void
    gate = new Promise<void>((resolve) => {
      release = resolve
    })
    await page.setViewportSize({ width, height: 900 })
    await page.goto("/audit")
    await expect(strip(page)).toHaveAttribute("aria-busy", "true")
    await expect(page.getByTestId("audit-loading")).toBeVisible()
    const frame = page.locator("[data-slot=panel]").first()
    await risen(page)
    const before = {
      line: await identity(page).boundingBox(),
      strip: await strip(page).boundingBox(),
      frame: (await frame.boundingBox())?.y,
    }
    release()
    await expect(identity(page)).toContainText("10 changes in the last day")
    await expect(rows(page).first()).toBeVisible()
    await risen(page)
    expect(await identity(page).boundingBox(), `the line at ${width}`).toEqual(before.line)
    expect(await strip(page).boundingBox(), `the strip at ${width}`).toEqual(before.strip)
    expect((await frame.boundingBox())?.y, `the trail at ${width}`).toBe(before.frame)
  }
})

for (const width of [1280, 390]) {
  test(`a row is one height whatever it carries, at ${width}`, async ({ page }) => {
    await page.setViewportSize({ width, height: 1400 })
    await mockAudit(page)
    await page.goto("/audit")
    await expect(rows(page)).toHaveCount(12)
    await risen(page)
    // The fixture's rows differ in everything optional: a path, a target, a
    // detail, an address, a key, a status.
    // One height, but for the hairline the last row of a day does not draw.
    const all = await boxes(page)
    const heights = all.map((box) => box.height)
    expect(Math.max(...heights) - Math.min(...heights), JSON.stringify(all)).toBeLessThanOrEqual(1)
    // Today's ten are one run with nothing between them: each starts where
    // the one before it ended, give or take the hairline.
    for (let i = 1; i < 10; i++) {
      expect(all[i].top - all[i - 1].top - all[0].height, `row ${i}`).toBeLessThanOrEqual(1)
    }
    // And a row's words stay on its one line: nothing is cut off below it.
    const clipped = await rows(page).evaluateAll((items) =>
      items.filter((item) => item.scrollHeight > item.clientHeight + 1).map((i) => i.textContent),
    )
    expect(clipped).toEqual([])
    expect(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth)).toBe(false)
  })
}

test("the action is the row's button and opens the entry whole", async ({ page }) => {
  await mockAudit(page)
  await page.goto("/audit")
  const row = page.getByRole("row").filter({ hasText: "file.write" })
  // A one-line cell cuts the long path short; the entry has all of it.
  await row.getByRole("button", { name: "file.write" }).click()
  const entry = page.getByRole("dialog")
  await expect(entry).toContainText(
    "/etc/nginx/sites-available/a-rather-long-name-for-a-site-configuration.example.test.conf",
  )
  await expect(entry).toContainText("PUT /api/v1/files/write")
  await expect(entry).toContainText("mira · operator")
  await expect(entry).toContainText("key ci-deploy · key #12")
  await expect(entry).toContainText("192.168.1.20")
  await expect(entry).toContainText("200")
  await page.keyboard.press("Escape")
  await expect(entry).toHaveCount(0)

  // A refusal says why, and a hub session whose it was.
  await page
    .getByRole("row")
    .filter({ hasText: "45.12.3.4" })
    .getByRole("button", { name: "auth.login" })
    .click()
  await expect(entry).toContainText("invalid credentials")
  await expect(entry).toContainText("401")
  await page.keyboard.press("Escape")
  await page
    .getByRole("row")
    .filter({ hasText: "terminal.create" })
    .getByRole("button", { name: "terminal.create" })
    .click()
  await expect(entry).toContainText("through Just Hub as dana@hub · hub session hs_4f2a")
})

test("the keyboard walks the rows by their buttons and Enter opens one", async ({ page }) => {
  await mockAudit(page)
  await page.goto("/audit")
  const buttons = page.locator("[data-workspace-item]:visible [data-workspace-primary]")
  await expect(buttons.first()).toBeVisible()
  await buttons.first().focus()
  await page.keyboard.press("ArrowDown")
  await expect(buttons.nth(1)).toBeFocused()
  await page.keyboard.press("Enter")
  await expect(page.getByRole("dialog")).toContainText("/api/v1/hooks/github-app")
  await page.keyboard.press("Escape")
  await expect(page.getByRole("dialog")).toHaveCount(0)
  await expect(buttons.nth(1)).toBeFocused()
})

test("every filter narrows the trail, sits in the address and lands from it", async ({ page }) => {
  await mockAudit(page)
  await page.goto("/audit")
  await expect(rows(page)).toHaveCount(12)

  await user(page).fill("mira")
  await expect(rows(page)).toHaveCount(1)
  await expect(page).toHaveURL(/username=mira/)
  await expect(page.getByLabel("1 matching")).toBeVisible()
  await user(page).fill("")
  await expect(rows(page)).toHaveCount(12)

  await action(page).fill("auth.")
  await expect(rows(page)).toHaveCount(2)
  await expect(page).toHaveURL(/action=auth\./)
  const failures = page.getByRole("button", { name: "Failures only" })
  await failures.click()
  await expect(failures).toHaveAttribute("aria-pressed", "true")
  await expect(rows(page)).toHaveCount(1)
  await expect(page).toHaveURL(/failed=1/)
  // The day above is not narrowed with the trail.
  await expect(identity(page)).toContainText("10 changes in the last day")

  // Escape clears every filter at once.
  await rows(page).first().locator("[data-workspace-primary]").focus()
  await page.keyboard.press("Escape")
  await expect(rows(page)).toHaveCount(12)
  await expect(action(page)).toHaveValue("")
  await expect(failures).toHaveAttribute("aria-pressed", "false")

  // A chip is the action filter, and a second press lets go of it.
  const git = page.getByRole("group", { name: "Sections" }).getByRole("button", { name: "Git" })
  await git.click()
  await expect(action(page)).toHaveValue("git.")
  await expect(rows(page)).toHaveCount(1)
  await git.click()
  await expect(action(page)).toHaveValue("")
})

test("a filter in the address lands on the filtered trail", async ({ page }) => {
  await mockAudit(page)
  const asked = page.waitForRequest(
    (request) =>
      request.url().includes("/audit/") &&
      request.url().includes("action=database.") &&
      request.url().includes("failed=true"),
  )
  await page.goto("/audit?action=database.&failed=1")
  await asked
  await expect(action(page)).toHaveValue("database.")
  await expect(page.getByRole("button", { name: "Failures only" })).toHaveAttribute(
    "aria-pressed",
    "true",
  )
  await expect(
    page.getByRole("group", { name: "Sections" }).getByRole("button", { name: "Databases" }),
  ).toHaveAttribute("aria-pressed", "true")
  await expect(rows(page)).toHaveCount(1)
  await expect(rows(page).first()).toContainText("database.row.delete")
  await expect(rows(page).first()).toContainText("403")
})

/** A trail long enough to page and to export in more than one request. */
function long(count: number): Entry[] {
  const [first] = trail()
  return Array.from({ length: count }, (_, index) => ({
    ...first,
    id: count - index,
    ts: new Date(Date.now() - (index + 1) * 60_000).toISOString(),
    target: `web-${count - index}`,
  }))
}

test("the pager walks the trail a hundred at a time", async ({ page }) => {
  await mockAudit(page, { entries: long(230) })
  await page.goto("/audit")
  const footer = page.locator("[data-slot=panel-footer]")
  await expect(footer).toContainText("1–100 of 230")
  await expect(page.getByRole("button", { name: "Previous" })).toBeDisabled()
  await page.getByRole("button", { name: "Next" }).click()
  await expect(footer).toContainText("101–200 of 230")
  await expect(page).toHaveURL(/offset=100/)
  await page.getByRole("button", { name: "Next" }).click()
  await expect(footer).toContainText("201–230 of 230")
  await expect(page.getByRole("button", { name: "Next" })).toBeDisabled()
  await expect(rows(page)).toHaveCount(30)
  await page.getByRole("button", { name: "Previous" }).click()
  await expect(footer).toContainText("101–200 of 230")
})

test("Export hands over what the filters match, every page of it", async ({ page }) => {
  const entries = [...long(620), ...trail().slice(5, 6)]
  const asked: URL[] = []
  await mockAudit(page, {
    entries,
    audit: (route, url) => {
      if (url.searchParams.has("until")) asked.push(url)
      return undefined
    },
  })
  await page.goto("/audit")
  await expect(rows(page).first()).toBeVisible()

  await identity(page).getByRole("button", { name: "Audit log actions" }).click()
  const whole = page.waitForEvent("download")
  await page.getByRole("menuitem", { name: "Export as CSV" }).click()
  const file = await whole
  expect(file.suggestedFilename()).toMatch(/^audit-\d{4}-\d{2}-\d{2}\.csv$/)
  const lines = (await readFile(await file.path(), "utf8")).trimEnd().split("\n")
  expect(lines[0]).toBe(
    "id,time,user,role,actor,key,key id,hub user,session,address,action,target,method,path,status,result,detail",
  )
  expect(lines).toHaveLength(622)
  expect(lines.at(-1)).toContain(",mira,operator,token,ci-deploy,12,")
  await expect(page.getByText("Exported 621 entries")).toBeVisible()
  // Two pages of five hundred, none of it newer than the press.
  expect(asked.map((url) => url.searchParams.get("offset"))).toEqual(["0", "500"])
  expect(asked.every((url) => url.searchParams.get("limit") === "500")).toBe(true)
  expect(new Set(asked.map((url) => url.searchParams.get("until"))).size).toBe(1)

  // Narrowed, it is the narrowed trail, and the menu says so.
  await user(page).fill("mira")
  await expect(rows(page)).toHaveCount(1)
  await identity(page).getByRole("button", { name: "Audit log actions" }).click()
  const narrowed = page.waitForEvent("download")
  await page.getByRole("menuitem", { name: "Export what matches as CSV" }).click()
  const one = await narrowed
  expect(one.suggestedFilename()).toMatch(/^audit-mira-\d{4}-\d{2}-\d{2}\.csv$/)
  expect((await readFile(await one.path(), "utf8")).trimEnd().split("\n")).toHaveLength(2)
  expect(asked.at(-1)?.searchParams.get("username")).toBe("mira")
})

test("an account that may not read the trail is told so and offered nothing", async ({ page }) => {
  const asked: string[] = []
  await mockAudit(page, {
    capabilities: ["read"],
    role: "viewer",
    audit: (route, url) => {
      asked.push(url.search)
      return forbidden(route)
    },
  })
  await page.goto("/audit")
  await expect(page.getByText("The audit log is for administrators")).toBeVisible()
  await expect(page.getByRole("textbox")).toHaveCount(0)
  await expect(page.getByRole("button", { name: "Failures only" })).toHaveCount(0)
  await expect(page.getByRole("button", { name: "Audit log actions" })).toHaveCount(0)
  await expect(page.locator("[data-slot=error-state]")).toHaveCount(0)
  // Nor is the server asked for what it would refuse.
  expect(asked).toEqual([])
})

test("an administrator the server still refuses is told the same, not shown an error", async ({
  page,
}) => {
  await mockAudit(page, { audit: forbidden })
  await page.goto("/audit")
  await expect(page.getByText("The audit log is for administrators")).toBeVisible()
  await expect(page.locator("[data-slot=error-state]")).toHaveCount(0)
})

test("a trail that cannot be read says so in its place and reads again", async ({ page }) => {
  let failing = true
  await mockAudit(page, {
    audit: (route) =>
      failing
        ? json(route, { error: { code: "internal", message: "database is locked" } }, 500)
        : undefined,
  })
  await page.goto("/audit")
  await expect(identity(page)).toContainText("Not read")
  await expect(page.getByText("The last day could not be read.")).toBeVisible()
  const alert = page.locator("[data-slot=error-state]")
  await expect(alert).toContainText(/database is locked/i)
  // The filters stay where they are, over the failure.
  await expect(action(page)).toBeVisible()
  failing = false
  await page.getByRole("button", { name: "Read again" }).click()
  await expect(identity(page)).toContainText("10 changes in the last day")
  await alert.getByRole("button", { name: "Try again" }).click()
  await expect(rows(page)).toHaveCount(12)
})

test("a server nothing has changed yet says that, with its filters in place", async ({ page }) => {
  await mockAudit(page, { entries: [] })
  await page.goto("/audit")
  await expect(identity(page)).toContainText("Nothing was changed")
  await expect(identity(page)).toContainText("nobody signed in")
  await expect(strip(page).locator("[data-tone=quiet]")).toHaveCount(24)
  await expect(page.getByText("Nothing recorded yet").first()).toBeVisible()
  await user(page).fill("mira")
  await expect(page.getByText("No entries match")).toBeVisible()
})

test.describe("on a phone", () => {
  test.use({ viewport: { width: 390, height: 844 }, hasTouch: true })

  test("a row is two lines and a tap opens the entry", async ({ page }) => {
    await mockAudit(page)
    await page.goto("/audit")
    await expect(rows(page)).toHaveCount(12)
    await expect(page.getByRole("table")).toBeHidden()
    const row = rows(page).filter({ hasText: "file.write" })
    await expect(row).toContainText("mira")
    await expect(row).toContainText("200")
    await row.getByRole("button").tap()
    const entry = page.getByRole("dialog")
    await expect(entry).toContainText("PUT /api/v1/files/write")
    await expect(entry).toContainText("key ci-deploy")
    const box = await entry.boundingBox()
    expect(box!.x).toBeGreaterThanOrEqual(0)
    expect(box!.x + box!.width).toBeLessThanOrEqual(390)
    expect(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth)).toBe(false)
  })

  test("the day's hours answer a thumb", async ({ page }) => {
    await mockAudit(page)
    await page.goto("/audit")
    const under = strip(page).locator("xpath=following-sibling::div[1]")
    await expect(under).toContainText("Sign-in")
    await strip(page).locator("[data-tone=dented], [data-tone=failed]").tap()
    await expect(under).toContainText(/failed/)
  })
})
