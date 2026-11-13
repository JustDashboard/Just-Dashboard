import { expect as baseExpect, test, type Page } from "@playwright/test"
import { admin, json, loaded, type Mutation } from "./network-fixture"
import { connectionsWithQuality, openMaturity } from "./network-traffic-maturity-fixture"
import { risen } from "./risen"

// The Connections list and a remote address's own page, against the Network
// mocks: what the list opens on, that its cards hold one height, that the old
// sheet's place is a page with an address, and that every verb the page had
// still works and a read-only account is offered none it cannot use.

const expect = baseExpect.configure({ timeout: 15_000 })
test.describe.configure({ timeout: 120_000 })

const readOnly = { ...admin, capabilities: ["read"], user: { ...admin.user, role: "readonly" } }

/** Cards with and without the optional parts: a second program, a service, a block. */
const mixed = {
  ...connectionsWithQuality,
  peers: [
    { ...connectionsWithQuality.peers[0], processes: ["caddy", "dockerd"] },
    { ...connectionsWithQuality.peers[1] },
    { ...connectionsWithQuality.peers[3], service: undefined },
    ...connectionsWithQuality.peers.slice(4),
  ],
}

const card = (page: Page, address: string) =>
  page.locator("[data-slot=choice-row]").filter({ hasText: address })

async function visit(page: Page, path: string) {
  await page.goto(path)
  await loaded(page)
}

async function cardHeights(page: Page) {
  await risen(page)
  return page
    .locator("[data-slot=choice-list][aria-label='Live addresses'] > [data-slot=choice-row]")
    .evaluateAll((rows) => rows.map((row) => Math.round(row.getBoundingClientRect().height)))
}

for (const width of [1280, 390]) {
  test.describe(`${width} wide`, () => {
    test.use({ viewport: { width, height: 900 } })

    test(`the list opens on its line and its picture, every card one height, at ${width}`, async ({
      page,
    }) => {
      await openMaturity(page, [], { "/connections": mixed })
      await visit(page, "/network/connections")

      await expect(page.locator("[data-slot=stat-grid]")).toHaveCount(0)
      await expect(page.locator("[data-slot=stat-tile]")).toHaveCount(0)
      const identity = page.locator("[data-slot=host-identity]").first()
      await expect(identity).toContainText("214 sockets")
      await expect(identity).toContainText("7 remote addresses, 3 from the internet")
      await expect(page.locator("[data-slot=wire-flow]")).toBeVisible()

      const heights = await cardHeights(page)
      expect(heights.length).toBe(7)
      expect(new Set(heights).size, `card heights ${heights.join(", ")}`).toBe(1)
      // The card after a blocked one, with a second program and without a
      // service, sits where the same card would sit without any of them.
      await expect(card(page, "203.0.113.77")).toContainText("blocked")
      const step = await page
        .locator("[data-slot=choice-list][aria-label='Live addresses'] > [data-slot=choice-row]")
        .evaluateAll((rows) =>
          rows
            .slice(1)
            .map((row, i) =>
              Math.round(row.getBoundingClientRect().top - rows[i].getBoundingClientRect().top),
            ),
        )
      expect(new Set(step).size, `card steps ${step.join(", ")}`).toBe(1)

      // Every control on a card is drawn without a pointer to reveal it.
      const hidden = await page.evaluate(() =>
        Array.from(
          document.querySelectorAll<HTMLElement>(
            "[data-slot='choice-row'] button, [data-slot='choice-row'] a",
          ),
        )
          .filter((el) => parseFloat(getComputedStyle(el).opacity) < 0.1)
          .map((el) => el.getAttribute("aria-label") ?? el.outerHTML.slice(0, 120)),
      )
      expect(hidden).toEqual([])
      const overflow = await page.evaluate(
        () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
      )
      expect(overflow).toBeLessThanOrEqual(0)
    })

    test(`the line opens in the same place loading and loaded, at ${width}`, async ({ page }) => {
      await openMaturity(page, [])
      let release: () => void = () => {}
      const held = new Promise<void>((resolve) => (release = resolve))
      await page.route("**/api/v1/connections", async (route) => {
        await held
        return json(route, connectionsWithQuality)
      })
      await page.goto("/network/connections")
      const identity = page.locator("[data-slot=host-identity]").first()
      await expect(identity).toContainText("Reading the connection table")
      await expect(page.locator("[data-slot=wire-flow][data-loading]")).toBeVisible()
      await risen(page)
      const before = await identity.boundingBox()
      const picture = await page.locator("[data-slot=wire-flow]").boundingBox()
      release()
      await expect(identity).toContainText("214 sockets")
      await expect(page.locator("[data-slot=wire-flow][data-loading]")).toHaveCount(0)
      await risen(page)
      const after = await identity.boundingBox()
      expect(after?.y).toBe(before?.y)
      // On a phone the facts wrap once they are read; wide, the line and the
      // picture under it hold their place too.
      if (width === 1280) {
        expect(after?.height).toBe(before?.height)
        expect((await page.locator("[data-slot=wire-flow]").boundingBox())?.y).toBe(picture?.y)
      }
    })

    test(`an address is a page of its own, at ${width}`, async ({ page }) => {
      await openMaturity(page, [])
      await visit(page, "/network/connections")
      await card(page, "198.51.100.23").getByRole("link", { name: "Open 198.51.100.23" }).click()
      await expect(page).toHaveURL(/\/network\/connections\/198\.51\.100\.23$/)
      const identity = page.locator("[data-slot=host-identity]").first()
      await expect(identity).toContainText("198.51.100.23")
      await expect(identity).toContainText("18 sockets, 14 active")
      await expect(identity).toContainText("connected")
      const tuples = page.getByLabel("Connections with 198.51.100.23", { exact: true })
      await expect(tuples).toContainText("203.0.113.10:443")
      const overflow = await page.evaluate(
        () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
      )
      expect(overflow).toBeLessThanOrEqual(0)
    })
  })
}

test("the address page holds what the sheet held, and its views are in the address", async ({
  page,
}) => {
  await openMaturity(page, [])
  await page.goto("/network/connections/198.51.100.23")
  await expect(page.getByLabel("What the summary folded")).toContainText(
    "TCP/UDP · from 51022, 51023, 51024 +15 · 14 established, 3 time-wait, 1 connected",
  )
  const tuples = page.getByLabel("Connections with 198.51.100.23", { exact: true })
  await expect(tuples).toContainText("51022")
  await expect(tuples).toContainText(/≥ 1h \d+m/)
  await expect(tuples).toContainText("2.0 GB")
  await expect(tuples).toContainText("no counters")
  await expect(page.getByLabel("Closed connections with 198.51.100.23")).toContainText(
    "open at least 10m",
  )
  const layers = page.getByLabel("Across the layers")
  await expect(layers).toContainText("deny in from 198.51.100.0/24 to port 22")
  await expect(layers).toContainText("Answered through ens3 via 203.0.113.1 from 203.0.113.10")
  await expect(layers).toContainText("2 socket rows in the last day")
  await expect(page.getByRole("link", { name: "Trace the route" })).toHaveAttribute(
    "href",
    "/network/tools?tool=traceroute&target=198.51.100.23",
  )
  await expect(page.getByLabel("What this read cannot see")).toContainText(
    "connections that opened and closed between two reads are not in it",
  )

  await page.getByRole("tab", { name: "A past hour" }).click()
  await expect(page).toHaveURL(/\?tab=past$/)
  const recorded = page.getByLabel("Recorded sockets with 198.51.100.23")
  await expect(recorded).toContainText("caddy")
  await page.getByRole("combobox", { name: "Which hour" }).click()
  await page.getByRole("option").nth(2).click()
  await expect(page).toHaveURL(/\?tab=past&hour=\d{4}-\d\d-\d\dT\d\d%3A00%3A00Z$/)
  await page.reload()
  await expect(page.getByRole("tab", { name: "A past hour" })).toHaveAttribute(
    "aria-selected",
    "true",
  )
})

test("the rail walks the list with the open view, and the arrows say where they go", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await openMaturity(page, [])
  await page.goto("/network/connections/198.51.100.23?tab=past")
  const rail = page.getByRole("navigation", { name: "Remote addresses" })
  await expect(rail.getByRole("button", { name: "198.51.100.23" })).toHaveAttribute(
    "aria-current",
    "page",
  )
  await page.keyboard.press("Alt+ArrowDown")
  await expect(page).toHaveURL(/\/network\/connections\/203\.0\.113\.77\?tab=past/)
  await page.getByRole("button", { name: "Previous address" }).click()
  await expect(page).toHaveURL(/\/network\/connections\/198\.51\.100\.23\?tab=past/)
  // The rail's chip is the list's: the next address from the internet skips the tailnet.
  await rail.getByRole("radio", { name: /From the internet/ }).click()
  await expect(rail.getByRole("button", { name: "100.110.34.9" })).toHaveCount(0)
  await page.goto("/network/connections")
  await expect(page.getByRole("radio", { name: /From the internet/ })).toHaveAttribute(
    "aria-checked",
    "true",
  )
})

test("a block from a card's menu asks why and until when", async ({ page }) => {
  const mutations: Mutation[] = []
  await openMaturity(page, mutations)
  await visit(page, "/network/connections")
  // Already blocked, and on the tailnet: neither is offered a block.
  for (const address of ["203.0.113.77", "100.110.34.9"]) {
    await card(page, address)
      .getByRole("button", { name: `Actions for ${address}` })
      .click()
    await expect(page.getByRole("menuitem", { name: "Who owns this address" })).toBeVisible()
    await expect(page.getByRole("menuitem", { name: "Block at the firewall" })).toHaveCount(0)
    await page.keyboard.press("Escape")
  }
  await card(page, "192.0.2.145").getByRole("button", { name: "Actions for 192.0.2.145" }).click()
  await page.getByRole("menuitem", { name: "Block at the firewall" }).click()
  const dialog = page.getByRole("dialog")
  await expect(dialog.getByLabel("Reason")).toHaveValue("connected to SSH on 22")
  await dialog.getByRole("button", { name: "Block 192.0.2.145" }).click()
  await expect
    .poll(() => mutations.find((m) => m.path === "/firewall/blocks")?.body)
    .toMatchObject({ address: "192.0.2.145", reason: "connected to SSH on 22" })

  await card(page, "198.51.100.23")
    .getByRole("button", { name: "Actions for 198.51.100.23" })
    .click()
  await page.getByRole("menuitem", { name: "Trace the route to it" }).click()
  await expect(page).toHaveURL(/\/network\/tools\?tool=traceroute&target=198\.51\.100\.23/)
})

test("the address page blocks, lifts, and never offers a private address a block", async ({
  page,
}) => {
  const mutations: Mutation[] = []
  await openMaturity(page, mutations)
  await page.goto("/network/connections/198.51.100.23")
  await page.getByRole("button", { name: "Block at the firewall" }).click()
  const dialog = page.getByRole("dialog")
  await expect(dialog.getByLabel("Reason")).toHaveValue("connected to HTTPS on 443")
  await dialog.getByLabel("Reason").fill("scraping")
  await dialog.getByRole("button", { name: "Block 198.51.100.23" }).click()
  await expect
    .poll(() => mutations.find((m) => m.path === "/firewall/blocks")?.body)
    .toMatchObject({ address: "198.51.100.23", reason: "scraping" })

  await page.goto("/network/connections/203.0.113.77")
  await expect(page.locator("[data-slot=host-identity]").first()).toContainText(/blocked · ends in/)
  await expect(page.getByRole("button", { name: "Block at the firewall" })).toHaveCount(0)
  await page.getByRole("button", { name: "Lift the block" }).click()
  await page.getByRole("button", { name: "Lift", exact: true }).click()
  await expect
    .poll(() => mutations.find((m) => m.method === "DELETE")?.path)
    .toBe(`/firewall/blocks/${"b".repeat(32)}`)

  // An address on the tailnet with no live row — opened from a past hour —
  // is still private: the sheet offered it a block.
  await page.route("**/api/v1/connections/*", (route) =>
    json(route, {
      address: "100.64.9.9",
      private: true,
      sockets: [],
      closed: [],
      quality: connectionsWithQuality.quality,
      countersTruncated: false,
    }),
  )
  await page.goto("/network/connections/100.64.9.9")
  await expect(page.locator("[data-slot=host-identity]").first()).toContainText("not connected")
  await expect(page.getByText("No live socket to 100.64.9.9 at this read.")).toBeVisible()
  await page.getByRole("button", { name: "Actions for 100.64.9.9" }).click()
  await expect(page.getByRole("menuitem", { name: "Reverse lookup" })).toBeVisible()
  await expect(page.getByRole("menuitem", { name: "Block at the firewall" })).toHaveCount(0)
})

test("the old ways in still land: ?peer= opens the address, ?q= and ?when= keep the list", async ({
  page,
}) => {
  await openMaturity(page, [])
  await page.goto("/network/connections?peer=198.51.100.23")
  await expect(page).toHaveURL(/\/network\/connections\/198\.51\.100\.23$/)
  await expect(page.getByLabel("Connections with 198.51.100.23", { exact: true })).toBeVisible()

  await visit(page, "/network/connections?q=5432")
  await expect(page.locator("[data-slot=choice-list] > [data-slot=choice-row]")).toHaveCount(2)

  await visit(page, "/network/connections?when=recorded")
  const recorded = page.getByLabel("Recorded connections")
  await expect(card(page, "198.51.100.23")).toContainText("2.0 GiB")
  await expect(recorded).toContainText("Sockets that opened and closed between two samples")
  await card(page, "203.0.113.200").getByRole("link", { name: "Open 203.0.113.200" }).click()
  await expect(page).toHaveURL(/\/network\/connections\/203\.0\.113\.200\?tab=past&hour=/)
  await expect(page.getByRole("tab", { name: "A past hour" })).toHaveAttribute(
    "aria-selected",
    "true",
  )

  await page.goto("/network/connections/not-an-address")
  await expect(page.getByText("That is not an address")).toBeVisible()
})

test("the picture narrows the list: the internet to its chip, a program to its callers", async ({
  page,
}) => {
  await openMaturity(page, [])
  await visit(page, "/network/connections")
  await page.getByRole("button", { name: "Show only the addresses from the internet" }).click()
  await expect(page.getByRole("radio", { name: /From the internet/ })).toHaveAttribute(
    "aria-checked",
    "true",
  )
  await expect(page.locator("[data-slot=choice-list] > [data-slot=choice-row]")).toHaveCount(4)
  await page.getByRole("button", { name: "Show every address" }).click()
  await page.getByRole("button", { name: "Show the addresses connected to postgres" }).click()
  await expect(page.getByRole("textbox", { name: "Filter connections" })).toHaveValue("postgres")
  await expect(page.locator("[data-slot=choice-list] > [data-slot=choice-row]")).toHaveCount(2)
})

test("an empty server keeps its picture and says nobody is connected", async ({ page }) => {
  await openMaturity(page, [], {
    "/connections": { total: 3, listening: 27, loopback: 3, peers: [] },
    "/firewall/blocks": [],
  })
  await visit(page, "/network/connections")
  await expect(page.locator("[data-slot=wire-flow]")).toContainText(
    "Nobody from outside is connected",
  )
  await expect(page.getByText("No connections", { exact: true })).toBeVisible()
  await expect(page.getByRole("link", { name: "What is listening" })).toBeVisible()
})

test("a read-only account reads the list and an address, with nothing it cannot use", async ({
  page,
}) => {
  await openMaturity(page, [], { "/auth/session": readOnly })
  await visit(page, "/network/connections")
  await expect(page.getByRole("tab", { name: "A past hour" })).toHaveCount(0)
  await card(page, "198.51.100.23")
    .getByRole("button", { name: "Actions for 198.51.100.23" })
    .click()
  await expect(page.getByRole("menuitem", { name: "Who owns this address" })).toBeVisible()
  await expect(page.getByRole("menuitem", { name: "Block at the firewall" })).toHaveCount(0)
  await page.keyboard.press("Escape")
  await expect(
    page.getByLabel("Blocks from this page").getByRole("button", { name: /Lift/ }),
  ).toHaveCount(0)

  const detail: string[] = []
  page.on("request", (r) => {
    if (/\/api\/v1\/(connections\/|network\/flows)/.test(r.url())) detail.push(r.url())
  })
  await page.goto("/network/connections/203.0.113.77")
  await expect(page.getByLabel("What the summary folded")).toContainText("caddy")
  await expect(page.getByRole("tab")).toHaveCount(0)
  await expect(page.getByRole("button", { name: "Block at the firewall" })).toHaveCount(0)
  await expect(page.getByRole("button", { name: "Lift the block" })).toHaveCount(0)
  // The tuples and the history are an administrator's: not even asked for.
  expect(detail).toEqual([])
})
