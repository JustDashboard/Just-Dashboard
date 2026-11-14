import { expect, test, type Locator, type Page } from "@playwright/test"
import { admin, json, loaded, mockNetwork, routing, type Mutation } from "./network-fixture"
import { risen } from "./risen"

/**
 * The Routing page as the deployment pages are drawn: what it opens on, that
 * it holds still while it loads, that every route and rule is one line of one
 * height, the picture's question and its answer, the views in the address,
 * forwarding as a setting saved from the page's bar, and what an account
 * that cannot change anything is shown. What the forms send is in
 * `network-routing-firewall.spec.ts` and `network-audit.spec.ts`.
 */

const reader = { ...admin, capabilities: ["read"], user: { ...admin.user, role: "readonly" } }
const careful = { ...admin, capabilities: admin.capabilities.filter((c) => c !== "destructive") }

/** A table with every kind of row: a note, a source, tags, multipath, a guard, the dashboard's own. */
const varied = {
  ...routing,
  tables: [
    {
      ...routing.tables[0],
      routes: [
        ...routing.tables[0].routes,
        {
          id: 9,
          family: "inet6",
          destination: "2001:db8:60::/64",
          type: "unreachable",
          protocol: "static",
          metric: 0,
          flags: [],
          nexthops: [],
          owner: "just-dashboard",
          managed: true,
          comment: "Keep the lab's old prefix from leaking while it is renumbered",
        },
        {
          id: 10,
          family: "inet",
          destination: "10.6.0.0/24",
          type: "unicast",
          protocol: "static",
          metric: 50,
          source: "10.8.0.1",
          flags: [],
          nexthops: [
            { gateway: "10.8.0.2", device: "wg0", weight: 2 },
            { gateway: "10.8.0.3", device: "wg0" },
          ],
          owner: "just-dashboard",
          managed: true,
        },
      ],
    },
    ...routing.tables.slice(1),
  ],
}

const lookupAnswer = {
  address: "10.8.0.7",
  device: "wg0",
  source: "10.8.0.1",
  family: "inet",
  table: 254,
}

async function answerLookups(page: Page, body: unknown = lookupAnswer, seen: URL[] = []) {
  await page.route("**/api/v1/network/routing/lookup?**", async (route) => {
    seen.push(new URL(route.request().url()))
    await json(route, body)
  })
  return seen
}

const box = async (locator: Locator) => (await locator.boundingBox())!

for (const width of [1280, 390]) {
  test.describe(`${width} wide`, () => {
    test.use({ viewport: { width, height: 900 } })

    test("the page opens on its identity line and its picture, with no tiles", async ({ page }) => {
      await mockNetwork(page)
      await page.goto("/network/routing")
      await loaded(page)
      const identity = page.getByRole("region", { name: "About routing" })
      await expect(identity).toContainText("Routing")
      await expect(identity).toContainText("Forwarding IPv4 and IPv6")
      await expect(identity).toContainText("default via 203.0.113.1 on ens3")
      await expect(identity).toContainText("3 tables")
      await expect(identity).toContainText("11 routes")
      await expect(identity).toContainText("5 rules")
      await expect(identity.getByRole("button", { name: "Add route", exact: true })).toBeVisible()
      await expect(page.locator('[data-slot="stat-grid"]')).toHaveCount(0)
      // The identity line is the first thing on the page, the picture the next.
      await risen(page)
      const top = (await box(identity)).y
      const picture = await box(page.getByRole("region", { name: "How a packet is routed" }))
      expect(picture.y).toBeGreaterThan(top)
      expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(
        width,
      )
    })

    test("the opening line, the picture and the tables are where they were while loading", async ({
      page,
    }) => {
      await mockNetwork(page)
      // A first visit, so the remembered shape is the fixture's own.
      await page.goto("/network/routing")
      await loaded(page)
      let release = () => {}
      const held = new Promise<void>((resolve) => (release = resolve))
      await page.route("**/api/v1/network/routing", async (route) => {
        await held
        await json(route, routing)
      })
      await page.reload()
      const identity = page.getByRole("region", { name: "About routing" })
      const picture = page.getByRole("region", { name: "How a packet is routed" })
      const routes = page.getByRole("region", { name: "Routes", exact: true })
      const rules = page.getByRole("region", { name: "Policy rules", exact: true })
      await expect(picture).toHaveAttribute("aria-busy", "true")
      await risen(page)
      const before = {
        identity: await box(identity),
        picture: await box(picture),
        routes: await box(routes),
        rules: await box(rules),
      }
      release()
      await expect(picture).not.toHaveAttribute("aria-busy", "true")
      await loaded(page)
      await risen(page)
      const after = {
        identity: await box(identity),
        picture: await box(picture),
        routes: await box(routes),
        rules: await box(rules),
      }
      expect(after.identity.y).toBe(before.identity.y)
      expect(after.identity.height).toBe(before.identity.height)
      expect(after.picture.y).toBe(before.picture.y)
      expect(Math.abs(after.picture.height - before.picture.height)).toBeLessThanOrEqual(1)
      expect(Math.abs(after.routes.y - before.routes.y)).toBeLessThanOrEqual(1)
      expect(Math.abs(after.rules.y - before.rules.y)).toBeLessThanOrEqual(2)
    })

    test("every route and rule is one line of one height, whatever it carries", async ({
      page,
    }) => {
      await mockNetwork(page, [], { overrides: { "/network/routing": varied } })
      await page.goto("/network/routing")
      await loaded(page)
      await risen(page)
      for (const slot of ["route", "rule"]) {
        const rows = page.locator(`[data-routing-row="${slot}"]`)
        const boxes = await rows.evaluateAll((elements) =>
          elements.map((element) => {
            const { y, height } = element.getBoundingClientRect()
            return { y, height }
          }),
        )
        expect(boxes.length).toBeGreaterThan(4)
        for (const [index, row] of boxes.entries()) {
          expect(row.height, `${slot} ${index}`).toBe(boxes[0].height)
          // The next row starts where this one ends: nothing hangs under a row.
          if (index > 0)
            expect(row.y, `${slot} ${index}`).toBeCloseTo(
              boxes[index - 1].y + boxes[index - 1].height,
              0,
            )
        }
      }
      // What a row carries is still said: the source, the note, the kinds.
      const table = page.getByRole("region", { name: "Routes", exact: true })
      await expect(table.getByText("source 10.0.0.1", { exact: true })).toBeVisible()
      await expect(table.getByText(/Keep the lab's old prefix/)).toBeAttached()
      await expect(table.getByText("unreachable", { exact: true })).toBeVisible()
      await expect(table.getByText("multipath", { exact: true })).toBeVisible()
      // Nothing in the table runs past its frame.
      const overflow = await table.evaluate((element) => element.scrollWidth - element.clientWidth)
      expect(overflow).toBeLessThanOrEqual(0)
    })
  })
}

test("the tables are one table, narrowed by a chip", async ({ page }) => {
  await mockNetwork(page)
  await page.goto("/network/routing")
  await loaded(page)
  const table = page.getByRole("region", { name: "Routes", exact: true })
  await expect(table.locator('[data-routing-row="route"]')).toHaveCount(11)
  await expect(page.locator("table")).toHaveCount(2)
  await table.getByRole("button", { name: /^tailscale/ }).click()
  await expect(table.locator('[data-routing-row="route"]')).toHaveCount(3)
  await table.getByRole("button", { name: /^office/ }).click()
  await expect(table.locator('[data-routing-row="route"]')).toHaveCount(1)
  await table.getByRole("button", { name: /^All/ }).click()
  await expect(table.locator('[data-routing-row="route"]')).toHaveCount(11)
  // What is not listed is said behind the title line's mark, not under the table.
  await table.getByRole("button", { name: "What is not listed" }).focus()
  await expect(page.getByRole("tooltip")).toContainText("The local table’s 31 entries")
})

test("a guarded row says why it stays to a keyboard, and the dashboard's own keep their verbs", async ({
  page,
}) => {
  const mutations: Mutation[] = []
  await mockNetwork(page, mutations)
  await page.goto("/network/routing")
  await loaded(page)
  await page.getByRole("button", { name: "Why the route to default stays" }).focus()
  await expect(page.getByRole("tooltip")).toContainText(
    "The default route: how this server reaches the internet.",
  )
  await page.keyboard.press("Escape")
  await page.getByRole("button", { name: "Why rule 5270 stays" }).focus()
  await expect(page.getByRole("tooltip")).toContainText("Tailscale's.")
  await page.keyboard.press("Escape")

  await page.getByRole("button", { name: "Remove the route to 192.168.1.0/24" }).click()
  await page.getByRole("dialog").getByRole("button", { name: "Remove", exact: true }).click()
  await expect
    .poll(() => mutations.find((m) => m.method === "DELETE")?.path)
    .toBe("/network/routing/routes/3")
  await page.getByRole("button", { name: "Remove rule 10000" }).click()
  await page.getByRole("dialog").getByRole("button", { name: "Remove", exact: true }).click()
  await expect
    .poll(() => mutations.filter((m) => m.method === "DELETE").at(-1)?.path)
    .toBe("/network/routing/rules/5")
  await page.getByRole("button", { name: "Edit the route to 192.168.1.0/24" }).click()
  await expect(page.getByRole("dialog")).toContainText("Edit the route to 192.168.1.0/24")
})

test("an address asked about lights the kernel's table, narrows the routes and the history", async ({
  page,
}) => {
  await mockNetwork(page)
  const lookups = await answerLookups(page)
  const histories: string[] = []
  await page.route("**/api/v1/network/routing/history**", async (route) => {
    histories.push(new URL(route.request().url()).search)
    await json(route, { intervalSeconds: 30, running: true, events: [], limits: [] })
  })
  await page.goto("/network/routing")
  await loaded(page)
  const picture = page.getByRole("region", { name: "How a packet is routed" })
  const answer = page.locator('[data-slot="route-answer"]')
  // With nothing asked the picture answers for this browser.
  await expect(answer).toContainText("Your replies to 100.110.34.9 leave on tailscale0")
  await expect(picture.getByText("table 52 · answers you (inferred)")).toBeVisible()

  await page.getByRole("textbox", { name: "Target", exact: true }).fill("10.8.0.7")
  await expect(picture.getByText("table 254 · answers 10.8.0.7 (kernel)")).toBeVisible()
  await expect(picture.getByText("32766 · the one rule that looks there")).toBeVisible()
  await expect(picture.getByText(/answers you/)).toHaveCount(0)
  await expect(answer).toContainText("10.8.0.7 leaves on wg0 from 10.8.0.1 directly")
  await expect(answer).toContainText("table main")
  expect(lookups[0].searchParams.get("target")).toBe("10.8.0.7")

  // main selects its /24, office its default; the kernel named main, so that row is the washed one.
  const table = page.getByRole("region", { name: "Routes", exact: true })
  await expect(table).toContainText("3 of 11 cover 10.8.0.7")
  await expect(table.locator('[data-routing-row="route"]')).toHaveCount(3)
  await expect(table.getByText("selected", { exact: true })).toHaveCount(2)
  await expect(table.locator('[data-routing-row="route"][data-selected]')).toHaveCount(1)
  await expect(table.locator('[data-routing-row="route"][data-selected]')).toContainText(
    "10.8.0.0/24",
  )
  await table.getByRole("button", { name: /^tailscale/ }).click()
  await expect(table.getByText("No route in this table covers 10.8.0.7.")).toBeVisible()

  // The packet's source and mark go with the question.
  await page.getByRole("button", { name: "Source and mark" }).click()
  await page.getByRole("textbox", { name: "Source address" }).fill("10.8.0.1")
  await page.getByRole("textbox", { name: "Packet mark" }).fill("0x80000")
  await expect.poll(() => lookups.at(-1)?.searchParams.get("mark")).toBe("0x80000")
  expect(lookups.at(-1)?.searchParams.get("source")).toBe("10.8.0.1")
  await page.keyboard.press("Escape")

  await page.getByRole("tab", { name: "History" }).click()
  await expect(page.getByText("No recorded change touches 10.8.0.7")).toBeVisible()
  expect(histories.some((q) => q.includes("target=10.8.0.7"))).toBe(true)
  await page.getByRole("button", { name: "Show every change" }).click()
  await expect(page.getByText("Nothing has changed")).toBeVisible()
  await page.getByRole("tab", { name: "Overview" }).click()
  await expect(page.getByRole("textbox", { name: "Target", exact: true })).toHaveValue("")
  await expect(picture.getByText("table 52 · answers you (inferred)")).toBeVisible()
})

test("a network narrows the routes without asking the kernel, and a wrong value says so", async ({
  page,
}) => {
  await mockNetwork(page)
  const lookups = await answerLookups(page)
  await page.goto("/network/routing")
  await loaded(page)
  const target = page.getByRole("textbox", { name: "Target", exact: true })
  const answer = page.locator('[data-slot="route-answer"]')
  await target.fill("10.8.0.0/24")
  await expect(answer).toContainText("Routes covering 10.8.0.0/24 are marked below")
  await expect(page.getByRole("region", { name: "Routes", exact: true })).toContainText(
    "3 of 11 cover 10.8.0.0/24",
  )
  await target.fill("not an address")
  await expect(answer).toContainText("Use an IPv4 or IPv6 address or network.")
  await expect(page.locator('[data-routing-row="route"]')).toHaveCount(11)
  await page.getByRole("button", { name: "Clear the target" }).click()
  await expect(answer).toContainText("Your replies to")
  expect(lookups).toHaveLength(0)
})

test("a lookup the kernel refuses is said on the answer's line, with a retry", async ({ page }) => {
  await mockNetwork(page)
  let calls = 0
  await page.route("**/api/v1/network/routing/lookup?**", async (route) => {
    calls++
    if (calls === 1)
      return json(
        route,
        { error: { code: "bad_request", message: "the kernel could not select a route" } },
        400,
      )
    await json(route, lookupAnswer)
  })
  await page.goto("/network/routing")
  await loaded(page)
  await page.getByRole("textbox", { name: "Target", exact: true }).fill("10.8.0.7")
  const answer = page.locator('[data-slot="route-answer"]')
  await expect(answer.getByRole("alert")).toContainText("the kernel could not select a route")
  await answer.getByRole("button", { name: "Retry" }).click()
  await expect(answer).toContainText("10.8.0.7 leaves on wg0")
})

test("each view is in the address, and the old hand-off still opens the form", async ({ page }) => {
  await mockNetwork(page)
  await page.goto("/network/routing?tab=history")
  await loaded(page)
  await expect(page.getByRole("tab", { name: "History" })).toHaveAttribute("aria-selected", "true")
  await expect(page.getByText("default via 10.8.0.10 dev wg0 proto static")).toBeVisible()
  await expect(page.getByText("10.0.9.0/24 dev br-old proto kernel")).toBeVisible()
  await expect(page.getByText("while not reading")).toBeVisible()
  await page.getByRole("button", { name: "What the history cannot see" }).focus()
  await expect(page.getByRole("tooltip")).toContainText("Readings are taken every 30 seconds")
  await page.keyboard.press("Escape")

  await page.getByRole("tab", { name: "BGP" }).click()
  await expect(page).toHaveURL(/\?tab=bgp$/)
  await expect(page.getByText("FRR is not installed", { exact: true })).toBeVisible()
  await expect(page.getByRole("link", { name: "Find FRR in Packages" })).toHaveAttribute(
    "href",
    "/packages?q=frr",
  )
  await page.getByRole("tab", { name: "Forwarding" }).click()
  await expect(page).toHaveURL(/\?tab=forwarding$/)
  await expect(page.getByRole("switch", { name: "IPv4 forwarding" })).toBeVisible()
  await page.getByRole("tab", { name: "Overview" }).click()
  await expect(page).toHaveURL(/\/network\/routing$/)

  await page.goto("/network/routing?route=new&device=jd-lab")
  const dialog = page.getByRole("dialog")
  await expect(dialog).toContainText("Add a route")
  await expect(dialog.getByRole("combobox", { name: "Device" })).toContainText("jd-lab")
  await expect(page).toHaveURL(/\/network\/routing$/)
})

test("forwarding is a setting: switched as a draft, saved from the bar, stopping asked about", async ({
  page,
}) => {
  const mutations: Mutation[] = []
  let state = {
    ...routing,
    forwarding: {
      ipv4: { available: true, enabled: false, persisted: false, neededBy: [] as string[] },
      ipv6: { available: true, enabled: true, persisted: true, neededBy: [] as string[] },
    },
  }
  await mockNetwork(page, mutations)
  await page.route("**/api/v1/network/routing", (route) => json(route, state))
  await page.goto("/network/routing?tab=forwarding")
  await loaded(page)
  const identity = page.getByRole("region", { name: "About routing" })
  await expect(identity).toContainText("Forwarding IPv6 only")
  const v4 = page.getByRole("switch", { name: "IPv4 forwarding" })
  const v6 = page.getByRole("switch", { name: "IPv6 forwarding" })
  const bar = page.getByRole("region", { name: "Save changes" })
  await expect(bar).toHaveCount(0)

  // A switch pressed is a draft: nothing is sent until Save.
  await v4.click()
  await expect(v4).toBeChecked()
  await expect(bar).toContainText("1 unsaved change")
  await expect(bar).toContainText("Applies immediately")
  expect(mutations).toEqual([])
  // The draft outlives a look at another view.
  await page.getByRole("tab", { name: "Overview" }).click()
  await expect(bar).toBeVisible()
  await page.getByRole("tab", { name: "Forwarding" }).click()
  await bar.getByRole("button", { name: "Discard" }).click()
  await expect(v4).not.toBeChecked()
  await expect(bar).toHaveCount(0)

  await v4.click()
  state = {
    ...state,
    forwarding: { ...state.forwarding, ipv4: { ...state.forwarding.ipv4, enabled: true } },
  }
  await bar.getByRole("button", { name: /^Save/ }).click()
  await expect.poll(() => mutations.map((m) => m.path)).toEqual(["/network/forwarding/ipv4/on"])
  await expect(identity).toContainText("Forwarding IPv4 and IPv6")
  await expect(bar).toHaveCount(0)

  // Turning one off is asked about at Save, and cancelling sends nothing.
  await v6.click()
  await bar.getByRole("button", { name: /^Save/ }).click()
  const dialog = page.getByRole("dialog")
  await expect(dialog).toContainText("Stop forwarding IPv6")
  await dialog.getByRole("button", { name: "Cancel" }).click()
  expect(mutations).toHaveLength(1)
  await expect(bar).toContainText("1 unsaved change")
  await bar.getByRole("button", { name: /^Save/ }).click()
  state = {
    ...state,
    forwarding: { ...state.forwarding, ipv6: { ...state.forwarding.ipv6, enabled: false } },
  }
  await page.getByRole("dialog").getByRole("button", { name: "Turn off", exact: true }).click()
  await expect
    .poll(() => mutations.map((m) => m.path))
    .toEqual(["/network/forwarding/ipv4/on", "/network/forwarding/ipv6/off"])
  await expect(identity).toContainText("Forwarding IPv4 only")
  await expect(v6).not.toBeChecked()
  // One confirmation: the bar's, not a second toast.
  await expect(page.getByText("Stop forwarding IPv6 completed")).toHaveCount(0)
})

test("a switch something needs is locked, the picture says by what, and its row carries no footnotes", async ({
  page,
}) => {
  await mockNetwork(page, [], {
    overrides: {
      "/network/routing": {
        ...routing,
        forwarding: {
          ...routing.forwarding,
          ipv4: {
            ...routing.forwarding.ipv4,
            dockerBasis: "Counts all 5 Docker bridge networks.",
            health: {
              status: "forwarding",
              forwarded: 123456,
              ratePerSecond: 42.5,
              windowSeconds: 15,
              disabled: [],
              checkedAt: new Date().toISOString(),
            },
          },
        },
      },
    },
  })
  await page.goto("/network/routing?tab=forwarding")
  await loaded(page)
  const rows = page.locator('[data-slot="forwarding-row"]')
  await expect(rows).toHaveCount(2)
  await expect(page.getByRole("switch", { name: "IPv4 forwarding" })).toBeDisabled()
  // What it carries and for whom is the picture: each family, this server, what needs it.
  const picture = page.locator('[data-slot="wire-flow"]')
  await expect(picture).toContainText("Forwarding traffic")
  await expect(picture).toContainText("43 datagrams/s over 15s")
  await expect(picture).toContainText("5 Docker networks")
  await expect(picture).toContainText("The WireGuard exit through wg0")
  await expect(picture).toContainText("IPv4 and IPv6")
  await expect(picture.locator("img")).toHaveCount(3)
  // A row is its name and its switch: one line.
  const heights = await rows.evaluateAll((elements) =>
    elements.map((element) => element.getBoundingClientRect().height),
  )
  expect(heights[0]).toBe(heights[1])
  expect(heights[0]).toBeLessThanOrEqual(48)
  await page.getByRole("button", { name: "About IPv4 forwarding" }).focus()
  const tip = page.getByRole("tooltip")
  await expect(tip).toContainText("Turning it off would stop 5 Docker networks")
  await expect(tip).toContainText("Counts all 5 Docker bridge networks.")
  await expect(tip).toContainText("123,456 forwarded since the kernel started counting.")
})

test("an account that can only read sees no control it cannot use", async ({ page }) => {
  const mutations: Mutation[] = []
  await mockNetwork(page, mutations, { session: reader })
  const lookups = await answerLookups(page)
  await page.goto("/network/routing")
  await loaded(page)
  await expect(page.getByRole("region", { name: "About routing" })).toContainText("11 routes")
  await expect(page.getByRole("button", { name: "Add route", exact: true })).toHaveCount(0)
  await expect(page.getByRole("button", { name: "Add rule", exact: true })).toHaveCount(0)
  await expect(page.getByRole("button", { name: /^(Edit|Remove) / })).toHaveCount(0)
  await expect(page.locator("button:disabled")).toHaveCount(0)
  await page.getByRole("button", { name: "Routing actions" }).click()
  await expect(page.getByRole("menuitem")).toHaveText(["Refresh"])
  await page.keyboard.press("Escape")
  // Reading is whole: the question is still answered.
  await page.getByRole("textbox", { name: "Target", exact: true }).fill("10.8.0.7")
  await expect(page.locator('[data-slot="route-answer"]')).toContainText("leaves on wg0")
  expect(lookups.length).toBeGreaterThan(0)

  await page.getByRole("tab", { name: "Forwarding" }).click()
  await expect(page.locator('[data-slot="forwarding-row"]')).toHaveCount(2)
  await expect(page.getByRole("switch")).toHaveCount(0)
  await expect(page.locator('[data-slot="forwarding-row"]').first()).toContainText("On")
  expect(mutations).toEqual([])
})

test("an administrator who may not destroy can add, and cannot stop or remove", async ({
  page,
}) => {
  await mockNetwork(page, [], {
    session: careful,
    overrides: {
      "/network/routing": {
        ...routing,
        forwarding: {
          ipv4: { available: true, enabled: true, persisted: false, neededBy: [] },
          ipv6: { available: true, enabled: false, persisted: false, neededBy: [] },
        },
      },
    },
  })
  await page.goto("/network/routing")
  await loaded(page)
  await expect(page.getByRole("button", { name: "Add route", exact: true })).toBeEnabled()
  await expect(page.getByRole("button", { name: "Add rule", exact: true })).toBeEnabled()
  await expect(page.getByRole("button", { name: /^(Edit|Remove) / })).toHaveCount(0)
  await page.getByRole("tab", { name: "Forwarding" }).click()
  await expect(page.getByRole("switch", { name: "IPv4 forwarding" })).toBeDisabled()
  await expect(page.getByRole("switch", { name: "IPv6 forwarding" })).toBeEnabled()
})

test("a first read that fails says so under the identity line, and the other views still open", async ({
  page,
}) => {
  await mockNetwork(page)
  let fail = true
  await page.route("**/api/v1/network/routing", (route) =>
    fail
      ? json(route, { error: { code: "unavailable", message: "ip could not be run" } }, 503)
      : json(route, routing),
  )
  await page.goto("/network/routing")
  const identity = page.getByRole("region", { name: "About routing" })
  await expect(identity).toContainText("Not read")
  await expect(page.getByText("ip could not be run")).toBeVisible()
  await page.getByRole("tab", { name: "History" }).click()
  await expect(page.getByText("default via 10.8.0.10 dev wg0 proto static")).toBeVisible()
  await page.getByRole("tab", { name: "Overview" }).click()
  fail = false
  await page.getByRole("button", { name: "Try again" }).click()
  await expect(identity).toContainText("11 routes")
})
