import { expect, test, type Locator, type Page } from "@playwright/test"
import { admin, json, loaded, mockNetwork, type Mutation } from "./network-fixture"
import { overrides, pressure, protection } from "./network-gateway-fixture"
import { risen } from "./risen"

/**
 * The Protection page as the overhaul left it: it opens on its identity line
 * and a picture rather than on four tiles, its views are in the address, a row
 * is one height whatever it carries, the page has its shape before its read
 * lands, and nothing it could do was lost on the way. What its forms send is
 * checked in `network-gateway.spec.ts`, beside the gateway's.
 */

const reader = { ...admin, capabilities: ["read"], user: { ...admin.user, role: "readonly" } }
/** An administrator narrowed below what gives protection away. */
const careful = { ...admin, capabilities: admin.capabilities.filter((c) => c !== "destructive") }

type View = typeof protection
const withView = (view: Partial<View> | Record<string, unknown>) => ({
  ...overrides,
  "/network/protection": { ...protection, ...view },
})

const open = async (
  page: Page,
  path = "/network/protection",
  options: { mutations?: Mutation[]; data?: Record<string, unknown>; session?: typeof admin } = {},
) => {
  await mockNetwork(page, options.mutations ?? [], {
    overrides: options.data ?? overrides,
    session: options.session,
  })
  await page.goto(path)
  await loaded(page)
}

/** Where something rests, once the page's rise is over. */
const box = async (page: Page, locator: Locator) => {
  await risen(page)
  const found = await locator.boundingBox()
  if (!found) throw new Error("not on the page")
  return found
}

/**
 * One height, to the hairline: rows divided by a rule carry it as their own
 * edge, so all but the last of a run are a pixel taller than the last.
 */
const sameHeight = (height: number, other: number) =>
  expect(Math.abs(height - other), `${height} against ${other}`).toBeLessThanOrEqual(1)

const section = (page: Page, id: string) => page.locator(`#${id}`)
const rows = (page: Page, id: string) => section(page, id).getByRole("listitem")
const picture = (page: Page) => page.locator("[data-slot=protection-picture]")
const identity = (page: Page) => page.getByRole("region", { name: "About protection" })

/** A list with everything a row used to hang under itself. */
const laden = {
  ...protection.blocklists[0],
  error: "the feed answered 503; the copy from two days ago is in force",
  stale: true,
  failures: 3,
  containsYou: true,
  enforcement: "degraded",
  savedCount: 9_800,
  cache: { status: "ready", count: 9_843, generation: "a".repeat(64) },
  renderedGeneration: "a".repeat(64),
  runtime: { status: "present", count: 9_000, generation: "b".repeat(64) },
}
/** The same list with nothing optional at all. */
const bare = {
  ...protection.blocklists[0],
  coverage: undefined,
  lastDiff: undefined,
  sources: [],
  nextRefresh: null,
}

test("it opens on its identity line and its picture, not on tiles", async ({ page }) => {
  await open(page)
  await expect(page.locator("[data-slot=stat-grid]")).toHaveCount(0)
  await expect(page.locator("[data-slot=stat-tile]")).toHaveCount(0)
  const line = identity(page)
  await expect(line).toContainText("Protection")
  await expect(line).toContainText("In force")
  // A list whose fetch failed is said beside the state, in red.
  await expect(line).toContainText("1 list not fetched")
  await expect(line).toContainText("4 blocklists")
  await expect(line).toContainText("2 limits")
  await expect(line).toContainText("52,123 dropped since load")
  await expect(line).toContainText("2,518 refused")
  await expect(line.getByRole("button", { name: "New blocklist" })).toBeVisible()
  // The line is the first thing on the page and the picture is the next block.
  const lineBox = await box(page, line)
  const pictureBox = await box(page, picture(page))
  const lists = await box(page, section(page, "blocklists"))
  expect(lineBox.y).toBeLessThan(pictureBox.y)
  expect(pictureBox.y).toBeLessThan(lists.y)
  // What each tile's hint said is on its section's own line.
  await expect(section(page, "blocklists")).toContainText(
    "15,654 networks in 3 lists · 3.1 MB dropped since load",
  )
  await expect(section(page, "limits")).toContainText(
    /2 in force · 147\.5 KB refused since load · 12\.5K packets since/,
  )
})

test("the picture draws who is refused, the filter and what gets through", async ({ page }) => {
  await open(page)
  const refused = page.getByRole("region", { name: "Refused" })
  const through = page.getByRole("region", { name: "Gets through" })
  await expect(refused.getByText("China and Russia")).toBeVisible()
  await expect(refused.getByText("9,843 networks · 48.2K dropped")).toBeVisible()
  await expect(refused.getByText("fetch failed")).toBeVisible()
  await expect(refused.getByText("switched off")).toBeVisible()
  await expect(through.getByText("22/tcp")).toBeVisible()
  await expect(through.getByText("10 a minute per address · 2.5K refused")).toBeVisible()
  await expect(through.getByText("120 let through")).toBeVisible()
  await expect(through.getByText("5 addresses")).toBeVisible()
  await expect(through.getByText("yours among them")).toBeVisible()
  await expect(picture(page).getByText("Filter in force")).toBeVisible()
  // The connection table's fullness moved from its tile onto this server's node.
  await expect(page.getByRole("meter", { name: "Connection table fullness" })).toHaveAttribute(
    "aria-valuenow",
    "18",
  )
  // A node opens what it names.
  await refused.getByRole("button", { name: "Open Spamhaus DROP" }).click()
  await expect(page.getByRole("dialog")).toContainText("Spamhaus DROP")
  await page.keyboard.press("Escape")
  await through.getByRole("button", { name: "Open Website" }).click()
  await expect(page.getByRole("dialog").getByLabel("Ports")).toHaveValue("443")
  await page.keyboard.press("Escape")
  await expect(through.getByRole("link", { name: "Exceptions" })).toHaveAttribute(
    "href",
    "#exceptions",
  )
  await expect(through.getByRole("link", { name: "Never refused" })).toHaveAttribute(
    "href",
    "#never-refused",
  )
})

test("the picture says when the table is not loaded and the reader is not trusted", async ({
  page,
}) => {
  await open(page, "/network/protection", {
    data: withView({ loaded: false, clientTrusted: false }),
  })
  await expect(identity(page)).toContainText("Not loaded")
  await expect(page.getByText("Protection is not loaded into the kernel")).toBeVisible()
  await expect(picture(page).getByText("Not loaded")).toBeVisible()
  await expect(picture(page).getByText("yours is not among them")).toBeVisible()
})

test("the picture draws three lists and counts the rest, and one limit when there are many", async ({
  page,
}) => {
  const lists = Array.from({ length: 9 }, (_, i) => ({
    ...protection.blocklists[1],
    id: 20 + i,
    name: `Feed ${i + 1}`,
  }))
  const limits = Array.from({ length: 5 }, (_, i) => ({
    ...protection.limits[0],
    id: 30 + i,
    name: `Service ${i + 1}`,
    ports: String(8000 + i),
  }))
  await open(page, "/network/protection", { data: withView({ blocklists: lists, limits }) })
  const refused = page.getByRole("region", { name: "Refused" })
  await expect(refused.getByRole("listitem")).toHaveCount(4)
  await expect(refused.getByRole("link", { name: "6 more lists" })).toHaveAttribute(
    "href",
    "#blocklists",
  )
  const through = page.getByRole("region", { name: "Gets through" })
  await expect(through.getByRole("listitem")).toHaveCount(4)
  await expect(through.getByText("4 more limits")).toBeVisible()
  // The list under the picture still holds every one.
  await expect(rows(page, "blocklists")).toHaveCount(9)
  await expect(rows(page, "limits")).toHaveCount(5)
})

for (const width of [1280, 390]) {
  test.describe(`at ${width}`, () => {
    test.use({ viewport: { width, height: 900 } })

    test("a row is one height whatever its list carries, and the next row does not move", async ({
      page,
    }) => {
      const others = protection.blocklists.slice(1)
      await open(page, "/network/protection", {
        data: withView({ blocklists: [bare, ...others] }),
      })
      const plain = await box(page, rows(page, "blocklists").nth(0))
      const nextPlain = await box(page, rows(page, "blocklists").nth(1))
      const limitsPlain = await box(page, section(page, "limits"))

      await page.unrouteAll({ behavior: "ignoreErrors" })
      await open(page, "/network/protection", {
        data: withView({ blocklists: [laden, ...others] }),
      })
      const full = await box(page, rows(page, "blocklists").nth(0))
      const nextFull = await box(page, rows(page, "blocklists").nth(1))
      expect(full.height).toBe(plain.height)
      // A notice says the list holds the reader's address, above the list;
      // under it every row and the section after them keep their places.
      expect(nextFull.y - full.y).toBe(nextPlain.y - plain.y)
      const limitsFull = await box(page, section(page, "limits"))
      expect(limitsFull.y - full.y).toBe(limitsPlain.y - plain.y)
      // Nothing hangs under the row: its error and its evidence are in its editor.
      const row = rows(page, "blocklists").nth(0)
      await expect(row.getByText("the feed answered 503")).toHaveCount(0)
      await expect(row.getByText("Policy generations")).toHaveCount(0)
      await expect(row.getByText("Fetch failed").filter({ visible: true })).toBeVisible()
      // Every row of the list is that height, and so is every limit's.
      for (const id of ["blocklists", "limits"]) {
        const all = rows(page, id)
        for (let i = 0; i < (await all.count()); i++) {
          expect((await box(page, all.nth(i))).height).toBe(plain.height)
        }
      }
    })

    test("address rows are one height with and without a second line", async ({ page }) => {
      await open(page, "/network/protection", {
        data: withView({
          exceptions: [
            protection.exceptions[0],
            {
              ...protection.exceptions[0],
              id: 8,
              address: "203.0.113.0/24",
              scope: "all",
              scopeName: "every list and limit",
              reason: "Old vendor",
              expiresAt: new Date(Date.now() - 3_600_000).toISOString(),
              expired: true,
              createdBy: "",
            },
            { ...protection.exceptions[0], id: 9, address: "192.0.2.4/32", expiresAt: null },
          ],
        }),
      })
      for (const id of ["exceptions", "never-refused"]) {
        const all = rows(page, id)
        const first = await box(page, all.nth(0))
        for (let i = 1; i < (await all.count()); i++) {
          sameHeight((await box(page, all.nth(i))).height, first.height)
        }
      }
      // An exception's standing is a state word, not a tag on its address.
      await expect(rows(page, "exceptions").nth(1)).toContainText(/Ended .* ago/)
      await expect(rows(page, "exceptions").nth(2)).toContainText("Until removed")
      await expect(rows(page, "never-refused").last()).toContainText("Stale")
    })

    test("the page has its shape before its read lands", async ({ page }) => {
      let release: () => void = () => {}
      const held = new Promise<void>((resolve) => (release = resolve))
      await mockNetwork(page, [], { overrides })
      await page.route("**/api/v1/network/protection", async (route) => {
        await held
        return json(route, protection)
      })
      await page.goto("/network/protection")
      await page.waitForSelector("[data-slot=protection-picture][data-loading]")
      const parts = {
        line: identity(page),
        tabs: page.getByRole("tablist", { name: "Views of protection" }),
        picture: picture(page),
        lists: section(page, "blocklists").getByRole("heading"),
        firstRow: section(page, "blocklists").locator("li").first(),
      }
      const before: Record<string, { x: number; y: number; width: number; height: number }> = {}
      for (const [name, part] of Object.entries(parts)) before[name] = await box(page, part)

      release()
      await loaded(page)
      await expect(page.getByRole("button", { name: "Edit China and Russia" })).toBeVisible()
      for (const [name, part] of Object.entries(parts)) {
        const after = await box(page, part)
        expect(after.y, `${name} moved`).toBe(before[name].y)
        expect(after.height, `${name} changed height`).toBe(before[name].height)
      }
    })

    test("a second visit draws every section where it will be", async ({ page }) => {
      await open(page)
      await expect(page.getByRole("button", { name: "Edit China and Russia" })).toBeVisible()
      const headings = ["blocklists", "limits", "exceptions", "never-refused"].map((id) =>
        section(page, id).getByRole("heading"),
      )
      const settled = []
      for (const heading of headings) settled.push((await box(page, heading)).y)

      // The page remembers how many rows each list held, and reads again.
      await page.unrouteAll({ behavior: "ignoreErrors" })
      await mockNetwork(page, [], { overrides })
      await page.route("**/api/v1/network/protection", () => new Promise(() => {}))
      await page.reload()
      await page.waitForSelector("[data-slot=protection-picture][data-loading]")
      // The notice over the trusted addresses is not known before the read:
      // every heading down to it is in its place.
      for (const [i, heading] of headings.entries()) {
        expect((await box(page, heading)).y, `section ${i} moved`).toBe(settled[i])
      }
    })

    test("the picture is one height whatever the server has", async ({ page }) => {
      const heights: number[] = []
      const many = Array.from({ length: 9 }, (_, i) => ({
        ...protection.blocklists[1],
        id: 20 + i,
        name: `Feed ${i + 1}`,
      }))
      for (const view of [
        {},
        { blocklists: [], limits: [], exceptions: [] },
        { blocklists: protection.blocklists.slice(0, 1), limits: [] },
        { blocklists: many },
      ]) {
        await page.unrouteAll({ behavior: "ignoreErrors" })
        await open(page, "/network/protection", { data: withView(view) })
        heights.push((await box(page, picture(page))).height)
      }
      expect(new Set(heights).size, `picture heights ${heights.join(", ")}`).toBe(1)
    })

    test("nothing is wider than the page", async ({ page }) => {
      for (const tab of ["", "?tab=table", "?tab=kernel"]) {
        await page.unrouteAll({ behavior: "ignoreErrors" })
        await open(page, `/network/protection${tab}`)
        // The page scrolls a column of its own: neither it nor the window
        // may have anything to scroll to sideways.
        const overflow = await page.locator("[data-slot=page]").evaluate((element) => {
          let scroller = element.parentElement
          while (scroller && !/auto|scroll/.test(getComputedStyle(scroller).overflowY)) {
            scroller = scroller.parentElement
          }
          const root = document.scrollingElement!
          return [
            (scroller?.scrollWidth ?? 0) - (scroller?.clientWidth ?? 0),
            root.scrollWidth - root.clientWidth,
          ].filter((excess) => excess > 0)
        })
        expect(overflow, `on ${tab || "the overview"}`).toEqual([])
      }
    })
  })
}

test("the views are in the address", async ({ page }) => {
  await open(page, "/network/protection?tab=kernel")
  await expect(page.getByRole("tab", { name: "Kernel" })).toHaveAttribute("aria-selected", "true")
  await expect(page.getByRole("form", { name: "Kernel protections" })).toBeVisible()
  // The lists are another view, so they are not under the settings.
  await expect(section(page, "blocklists")).toHaveCount(0)

  await page.getByRole("tab", { name: "Connection table" }).click()
  await expect(page).toHaveURL(/\?tab=table$/)
  await expect(page.getByLabel("Connection table pressure")).toBeVisible()

  await page.getByRole("tab", { name: "Overview" }).click()
  await expect(page).toHaveURL(/\/network\/protection$/)
  await expect(section(page, "blocklists")).toBeVisible()

  // An address that names no view opens the overview.
  await page.goto("/network/protection?tab=nonsense")
  await expect(page.getByRole("tab", { name: "Overview" })).toHaveAttribute("aria-selected", "true")
})

test("the kernel's settings are one column of rows at one height, saved by one bar", async ({
  page,
}) => {
  const mutations: Mutation[] = []
  await open(page, "/network/protection?tab=kernel", { mutations })
  const settings = page.locator("[data-slot=kernel-setting]")
  await expect(settings).toHaveCount(15)
  // One column: every row starts on one edge and is as wide as the next.
  const first = await box(page, settings.nth(0))
  for (let i = 1; i < 15; i++) {
    const row = await box(page, settings.nth(i))
    expect(row.x).toBe(first.x)
    expect(row.width).toBe(first.width)
    sameHeight(row.height, first.height)
  }
  expect(first.width).toBeLessThanOrEqual(768)
  // No bar until something is staged, and no Save in any section.
  const bar = page.getByRole("region", { name: "Save changes" })
  await expect(bar).toHaveCount(0)
  await expect(page.getByRole("button", { name: "Apply" })).toHaveCount(0)

  // Staging, a number out of range and its correction change no row's height
  // and move no row under them.
  const redirects = settings.filter({ hasText: "Accept ICMP redirects (IPv6)" })
  const below = settings.filter({ hasText: "Send ICMP redirects" })
  // The page may scroll to a control it is asked to press, so a row's place
  // is read against the row over it.
  const gap = async (upper: Locator, lower: Locator) =>
    (await box(page, lower)).y - (await box(page, upper)).y
  const redirectsAt = await box(page, redirects)
  const belowAt = await gap(redirects, below)
  await page.getByRole("switch", { name: "Accept ICMP redirects (IPv6)" }).click()
  await expect(redirects).toContainText("running On")
  await expect(redirects).toHaveAttribute("data-edited", "true")
  expect((await box(page, redirects)).height).toBe(redirectsAt.height)
  expect(await gap(redirects, below)).toBe(belowAt)

  const retries = settings.filter({ hasText: "SYN-ACK retries" })
  const tableSize = settings.filter({ hasText: "Connection tracking table size" })
  const retriesAt = await box(page, retries)
  const tableSizeAt = await gap(retries, tableSize)
  await page.getByLabel("SYN-ACK retries", { exact: true }).fill("9")
  await expect(retries.getByRole("alert")).toHaveText("A whole number from 1 to 5.")
  expect((await box(page, retries)).height).toBe(retriesAt.height)
  expect(await gap(retries, tableSize)).toBe(tableSizeAt)
  await expect(bar.getByRole("button", { name: "Save" })).toBeDisabled()
  await page.getByLabel("SYN-ACK retries", { exact: true }).fill("3")
  await expect(bar).toContainText("2 unsaved changes")
  await expect(bar).toContainText("Applies immediately")
  // The section that holds an edit says so on its head, and the chip narrows to them.
  await expect(page.getByRole("heading", { name: "Redirects and broadcasts" })).toBeVisible()
  await expect(page.getByText("Unsaved changes", { exact: true })).toHaveCount(2)
  await page.getByRole("button", { name: /Edited/ }).click()
  await expect(settings).toHaveCount(2)
  await page.getByRole("button", { name: /^All/ }).click()

  // A look at another view keeps what is staged, and the tab counts it.
  await expect(page.getByRole("tab", { name: "Kernel" })).toContainText("2")
  await page.getByRole("tab", { name: "Overview" }).click()
  await expect(bar).toHaveCount(0)
  await page.getByRole("tab", { name: "Kernel" }).click()
  await expect(bar).toContainText("2 unsaved changes")

  // Closing the confirmation applies nothing and leaves the edits staged.
  await bar.getByRole("button", { name: "Save" }).click()
  await page.getByRole("dialog").getByRole("button", { name: "Cancel" }).click()
  expect(mutations).toEqual([])
  await expect(bar).toContainText("2 unsaved changes")

  await bar.getByRole("button", { name: "Save" }).click()
  const dialog = page.getByRole("dialog")
  await expect(dialog).toContainText("SYN-ACK retries")
  await expect(dialog).toContainText("2 → 3")
  await dialog.getByRole("button", { name: "Apply" }).click()
  await expect.poll(() => mutations.length).toBe(1)
  expect(mutations[0]).toEqual({
    method: "POST",
    path: "/network/protection/settings",
    body: {
      values: { "net.ipv6.conf.all.accept_redirects": "0", "net.ipv4.tcp_synack_retries": "3" },
    },
  })
  await expect(page.getByRole("tab", { name: "Kernel" })).not.toContainText("2")
})

test("discard drops what is staged, and a kept setting can be let go", async ({ page }) => {
  const mutations: Mutation[] = []
  await open(page, "/network/protection?tab=kernel", { mutations })
  await page.getByRole("switch", { name: "Send ICMP redirects" }).click()
  const bar = page.getByRole("region", { name: "Save changes" })
  await bar.getByRole("button", { name: "Discard" }).click()
  await expect(bar).toHaveCount(0)
  await expect(page.getByRole("switch", { name: "Send ICMP redirects" })).toBeChecked()

  const cookies = page.locator("[data-slot=kernel-setting]").filter({ hasText: "SYN cookies" })
  await expect(cookies).toContainText("kept for boot")
  await page.getByRole("button", { name: "Stop keeping SYN cookies" }).click()
  await expect(page.getByRole("dialog")).toContainText("Resetting removes the setting")
  await page.getByRole("dialog").getByRole("button", { name: "Stop keeping" }).click()
  await expect.poll(() => mutations.length).toBe(1)
  expect(mutations[0]).toMatchObject({
    method: "DELETE",
    path: "/network/protection/settings/net.ipv4.tcp_syncookies",
  })
})

test("every verb a row had still works", async ({ page }) => {
  const mutations: Mutation[] = []
  await open(page, "/network/protection", { mutations })
  const last = () => mutations.at(-1)

  // A list: refresh, switch on, switch off behind a confirmation, remove.
  await page.getByRole("button", { name: "Spamhaus DROP actions" }).click()
  await page.getByRole("menuitem", { name: "Refresh now" }).click()
  await expect.poll(() => last()?.path).toBe("/network/protection/blocklists/2/refresh")
  await page.getByRole("switch", { name: "Scanners I found in force" }).click()
  await expect.poll(() => last()?.path).toBe("/network/protection/blocklists/4")
  expect(last()).toMatchObject({ method: "PUT", body: { kind: "manual", enabled: true } })
  await page.getByRole("switch", { name: "China and Russia in force" }).click()
  await page.getByRole("dialog").getByRole("button", { name: "Switch off" }).click()
  await expect.poll(() => last()?.path).toBe("/network/protection/blocklists/1")
  expect(last()).toMatchObject({ method: "PUT", body: { countries: ["cn", "ru"], enabled: false } })
  await page.getByRole("button", { name: "Scanners I found actions" }).click()
  // A typed list has nothing to fetch.
  await expect(page.getByRole("menuitem", { name: "Refresh now" })).toHaveCount(0)
  await page.getByRole("menuitem", { name: "Remove" }).click()
  await page.getByRole("dialog").getByRole("button", { name: "Remove" }).click()
  await expect.poll(() => last()?.method).toBe("DELETE")
  expect(last()?.path).toBe("/network/protection/blocklists/4")

  // A limit: its menu opens its editor and removes it, its switch asks first.
  await page.getByRole("button", { name: "SSH actions" }).click()
  await page.getByRole("menuitem", { name: "Edit" }).click()
  await expect(page.getByRole("dialog").getByLabel("Ports")).toHaveValue("22")
  await page.keyboard.press("Escape")
  await page.getByRole("switch", { name: "Website in force" }).click()
  await page.getByRole("dialog").getByRole("button", { name: "Switch off" }).click()
  await expect.poll(() => last()?.path).toBe("/network/protection/limits/2")
  expect(last()).toMatchObject({ method: "PUT", body: { ports: "443", enabled: false } })
  await page.getByRole("button", { name: "SSH actions" }).click()
  await page.getByRole("menuitem", { name: "Remove" }).click()
  await page.getByRole("dialog").getByRole("button", { name: "Remove" }).click()
  await expect
    .poll(() => `${last()?.method} ${last()?.path}`)
    .toBe("DELETE /network/protection/limits/1")

  // An exception is removed from its row.
  await page.getByRole("button", { name: "Remove the exception for 198.51.100.7/32" }).click()
  await page.getByRole("dialog").getByRole("button", { name: "Remove" }).click()
  await expect
    .poll(() => `${last()?.method} ${last()?.path}`)
    .toBe("DELETE /network/protection/exceptions/7")

  // A kept address is forgotten from its menu.
  await page.getByRole("button", { name: "198.51.100.23 actions" }).click()
  await page.getByRole("menuitem", { name: "Stop trusting" }).click()
  await page.getByRole("dialog").getByRole("button", { name: "Stop trusting" }).click()
  await expect
    .poll(() => `${last()?.method} ${last()?.path}`)
    .toBe("DELETE /network/protection/trusted")
})

test("the line's menu makes a limit and an exception and reads again", async ({ page }) => {
  let reads = 0
  await mockNetwork(page, [], { overrides })
  await page.route("**/api/v1/network/protection", (route) => {
    reads++
    return json(route, protection)
  })
  await page.goto("/network/protection")
  await loaded(page)
  const menu = page.getByRole("button", { name: "Protection actions" })
  await menu.click()
  await page.getByRole("menuitem", { name: "New limit" }).click()
  await expect(page.getByRole("dialog")).toContainText("New rate limit")
  await page.keyboard.press("Escape")
  await menu.click()
  await page.getByRole("menuitem", { name: "New exception" }).click()
  await expect(page.getByRole("dialog")).toContainText("New exception")
  await page.keyboard.press("Escape")
  const before = reads
  await menu.click()
  await page.getByRole("menuitem", { name: "Refresh" }).click()
  await expect.poll(() => reads).toBeGreaterThan(before)
})

test("saving a kept address's note leaves the end it already has", async ({ page }) => {
  const mutations: Mutation[] = []
  const until = new Date(Date.now() + 5 * 24 * 3_600_000).toISOString().replace(/\.\d{3}Z$/, "Z")
  await open(page, "/network/protection", {
    mutations,
    data: withView({
      trusted: protection.trusted.map((t) =>
        t.address === "198.51.100.23" ? { ...t, expiresAt: until, stale: false } : t,
      ),
    }),
  })
  await page.getByRole("button", { name: "198.51.100.23 actions" }).click()
  await page.getByRole("menuitem", { name: "Edit why and until when" }).click()
  const dialog = page.getByRole("dialog")
  await expect(dialog.getByRole("combobox", { name: "Trust ends" })).toContainText("As it is")
  await dialog.getByLabel("Why").fill("Branch office")
  await dialog.getByRole("button", { name: "Save" }).click()
  await expect.poll(() => mutations.length).toBe(1)
  expect(mutations[0]).toEqual({
    method: "PUT",
    path: "/network/protection/trusted",
    body: { address: "198.51.100.23", reason: "Branch office", expiresAt: until, confirm: true },
  })
})

test("a reader is shown no control it cannot use", async ({ page }) => {
  const mutations: Mutation[] = []
  await open(page, "/network/protection", { mutations, session: reader })
  await expect(identity(page)).toContainText("4 blocklists")
  for (const name of ["New blocklist", "New limit", "New exception"]) {
    await expect(page.getByRole("button", { name })).toHaveCount(0)
  }
  await expect(page.getByRole("switch")).toHaveCount(0)
  await expect(page.getByRole("button", { name: /actions$/ })).toHaveCount(1)
  await expect(page.getByRole("button", { name: /^Remove the exception/ })).toHaveCount(0)
  // The line's menu holds the one thing a reader can do.
  await page.getByRole("button", { name: "Protection actions" }).click()
  await expect(page.getByRole("menuitem")).toHaveCount(1)
  await expect(page.getByRole("menuitem", { name: "Refresh" })).toBeVisible()
  await page.keyboard.press("Escape")
  // With no switch to say it, a row says whether it is in force.
  await expect(rows(page, "blocklists").nth(0)).toContainText("In force")
  await expect(rows(page, "blocklists").nth(3)).toContainText("Off")
  await expect(rows(page, "limits").nth(0)).toContainText("In force")
  // A row still opens, to be read.
  await section(page, "limits").getByRole("button", { name: "Open SSH" }).click()
  const dialog = page.getByRole("dialog")
  await expect(dialog.getByLabel("Ports")).toBeDisabled()
  await expect(dialog.getByRole("button", { name: /Save|Make|Remove/ })).toHaveCount(0)
  await dialog.getByRole("button", { name: "Close" }).first().click()

  await page.getByRole("tab", { name: "Kernel" }).click()
  const settings = page.locator("[data-slot=kernel-setting]")
  await expect(settings).toHaveCount(15)
  await expect(settings.first()).toContainText("On")
  for (const role of ["switch", "radio", "textbox"] as const) {
    await expect(settings.getByRole(role)).toHaveCount(0)
  }
  await expect(page.getByRole("combobox", { name: "Stage a workload profile" })).toHaveCount(0)
  await expect(page.getByRole("button", { name: /^Stop keeping/ })).toHaveCount(0)
  expect(mutations).toEqual([])
})

test("an administrator without the destructive capability cannot give protection away", async ({
  page,
}) => {
  await open(page, "/network/protection", { session: careful })
  // Switching off is refused by the server for this account; switching on is not.
  await expect(page.getByRole("switch", { name: "China and Russia in force" })).toBeDisabled()
  await expect(page.getByRole("switch", { name: "Scanners I found in force" })).toBeEnabled()
  await expect(page.getByRole("switch", { name: "SSH in force" })).toBeDisabled()
  await expect(page.getByRole("button", { name: "New exception" })).toHaveCount(0)
  await page.getByRole("button", { name: "China and Russia actions" }).click()
  await expect(page.getByRole("menuitem", { name: "Refresh now" })).toBeVisible()
  await expect(page.getByRole("menuitem", { name: "Remove" })).toHaveCount(0)
  await page.keyboard.press("Escape")
  await page.getByRole("tab", { name: "Kernel" }).click()
  await expect(page.getByText("kept for boot").first()).toBeVisible()
  await expect(page.getByRole("button", { name: /^Stop keeping/ })).toHaveCount(0)
})

test("with nothing made, each section says so and offers the next step", async ({ page }) => {
  await open(page, "/network/protection", {
    data: withView({ blocklists: [], limits: [], exceptions: [] }),
  })
  await expect(identity(page)).toContainText("Nothing in force")
  await expect(picture(page).getByText("Nothing in force")).toBeVisible()
  // The picture's rings open the forms that make one.
  await page.getByRole("button", { name: "New blocklist from the picture" }).click()
  await expect(page.getByRole("dialog")).toContainText("New blocklist")
  await page.keyboard.press("Escape")
  await page.getByRole("button", { name: "New limit from the picture" }).click()
  await expect(page.getByRole("dialog")).toContainText("New rate limit")
  await page.keyboard.press("Escape")
  for (const [id, sentence, step] of [
    ["blocklists", "No network is blocked", "Block a country, a feed or addresses"],
    ["limits", "No port is limited", "Limit a port"],
    ["exceptions", "No network is excepted from the drops", "New exception"],
  ]) {
    const empty = section(page, id).locator("[data-slot=empty-state]")
    await expect(empty).toContainText(sentence)
    await expect(empty.getByRole("button", { name: step })).toBeVisible()
  }
  await section(page, "blocklists").getByRole("button", { name: "Block a country" }).click()
  await expect(page.getByRole("dialog")).toContainText("New blocklist")
})

test("a first read that fails says so on the line and offers the retry", async ({ page }) => {
  let fail = true
  await mockNetwork(page, [], { overrides })
  await page.route("**/api/v1/network/protection", (route) =>
    fail
      ? json(route, { error: { code: "internal", message: "the table could not be read" } }, 500)
      : json(route, protection),
  )
  await page.goto("/network/protection")
  await expect(identity(page)).toContainText("Not read")
  await expect(page.getByText(/the table could not be read/i)).toBeVisible()
  await expect(page.locator("[data-slot=stat-grid]")).toHaveCount(0)
  fail = false
  await page.getByRole("button", { name: /Try again|Retry/ }).click()
  await expect(identity(page)).toContainText("In force")
})

test("the connection table has its band before its breakdown lands", async ({ page }) => {
  let release: () => void = () => {}
  const held = new Promise<void>((resolve) => (release = resolve))
  await mockNetwork(page, [], { overrides })
  await page.route("**/api/v1/network/protection/pressure", async (route) => {
    await held
    return json(route, pressure)
  })
  await page.goto("/network/protection?tab=table")
  const view = page.getByLabel("Connection table pressure")
  const band = view.locator("[data-slot=bar-reading]")
  // The band is drawn from the page's own reading of the table.
  await expect(band).toContainText("18% full")
  await expect(band).toContainText("11,796 of 65,536")
  const parts = {
    band,
    charts: view.getByRole("heading", { name: "Tracked connections, last day" }),
    ranks: view.getByRole("region", { name: "By state", exact: true }),
    findings: view.getByRole("heading", { name: "What the shares point at" }),
  }
  const before: Record<string, number> = {}
  for (const [name, part] of Object.entries(parts)) before[name] = (await box(page, part)).y
  release()
  await expect(view.getByText("tcp SYN_RECV").first()).toBeVisible()
  await expect(view.getByText("11,796 entries read")).toBeVisible()
  await loaded(page)
  for (const [name, part] of Object.entries(parts)) {
    expect((await box(page, part)).y, `${name} moved`).toBe(before[name])
  }
  // What the closing paragraph said is the line under the band and a tip.
  await expect(view.getByText("6,100 never answered")).toBeVisible()
  await view.getByRole("button", { name: "How the table is read" }).hover()
  await expect(page.getByRole("tooltip")).toContainText("Read from the kernel's connection table")
})
