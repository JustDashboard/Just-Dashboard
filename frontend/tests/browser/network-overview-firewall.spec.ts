import { expect, test, type Locator, type Page } from "@playwright/test"
import {
  admin,
  firewall,
  json,
  loaded,
  mockNetwork,
  overview,
  type Mutation,
} from "./network-fixture"
import { gateway, overrides as gatewayOverrides } from "./network-gateway-fixture"
import { mockHostLogs } from "./host-logs-fixture"
import { risen } from "./risen"
import type { DriftReport } from "../../src/lib/network-drift"

/**
 * What the overhaul of the Network overview, Firewall, Gateway and Drift
 * pages promises, against the mocked API: each opens on an identity line laid
 * out as a project's header and not on a row of tiles; its opening block
 * stands in one place before and after its read; rows are one height
 * whatever they carry; every verb the page had still does what it did; and an
 * account that cannot use a control is not drawn it.
 */

const reader = { ...admin, capabilities: ["read"], user: { ...admin.user, role: "readonly" } }
/** An administrator's account without the server's destructive routes. */
const careful = {
  ...admin,
  capabilities: admin.capabilities.filter((capability) => capability !== "destructive"),
}

/** Where a block stands in the page, whatever the page is scrolled to. */
const top = (locator: Locator) =>
  locator.evaluate((node) =>
    Math.round(
      node.getBoundingClientRect().top -
        document.querySelector("[data-slot=page]")!.getBoundingClientRect().top,
    ),
  )
const height = async (locator: Locator) => Math.round((await locator.boundingBox())!.height)
const fits = (page: Page) =>
  page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)

/**
 * A note behind an `ⓘ` says what it should. A tooltip opens on the pointer's
 * move onto its mark and not on its resting there, and this page is still
 * drawing its log when the pointer arrives, so the pointer is taken off and
 * brought back until the note is up rather than trusted to one arrival.
 */
async function reads(page: Page, name: string, text: string) {
  const mark = page.getByRole("button", { name })
  await expect(async () => {
    await page.mouse.move(0, 0)
    await mark.hover()
    await expect(page.getByRole("tooltip")).toContainText(text, { timeout: 2000 })
  }).toPass({ timeout: 30_000 })
  await page.mouse.move(0, 0)
  await expect(page.getByRole("tooltip")).toHaveCount(0)
}

/** Holds one route's answer until `release` is called, so the page can be measured while it waits. */
async function held(page: Page, path: string, body: unknown) {
  let release = () => {}
  const gate = new Promise<void>((resolve) => {
    release = resolve
  })
  await page.route(`**/api/v1${path}`, async (route) => {
    if (route.request().method() !== "GET") return route.fallback()
    await gate
    await json(route, body)
  })
  return release
}

/**
 * The identity line and the first section's title stand where they will
 * while the read is out: measured grey, then again once it has landed.
 */
async function holdsStill(
  page: Page,
  options: { path: string; route: string; body: unknown; identity: string; heading: string },
) {
  // Once with its read, so the page holds its own shape from a visit, as it
  // does for anybody who has opened it before; then again with the read out.
  await page.goto(options.path)
  await loaded(page)
  await expect(page.getByRole("heading", { name: options.heading, exact: true })).toBeVisible()
  const release = await held(page, options.route, options.body)
  await page.reload()
  const identity = page.getByRole("region", { name: options.identity })
  const heading = page.getByRole("heading", { name: options.heading, exact: true })
  await expect(heading).toBeVisible()
  await risen(page)
  const before = { identity: await top(identity), heading: await top(heading) }
  release()
  await loaded(page)
  await expect(page.locator("[data-slot=wire-flow][data-loading]")).toHaveCount(0)
  await risen(page)
  expect({ identity: await top(identity), heading: await top(heading) }).toEqual(before)
}

for (const width of [1280, 390]) {
  test.describe(`${width} wide`, () => {
    test.use({ viewport: { width, height: 900 } })

    test("the overview's line and picture stand in one place loading and loaded", async ({
      page,
    }) => {
      await mockNetwork(page)
      await holdsStill(page, {
        path: "/network",
        route: "/network/overview",
        body: overview,
        identity: "About the network",
        heading: "Topology",
      })
      await expect(page.locator("[data-slot=stat-grid], [data-slot=stat-tile]")).toHaveCount(0)
      expect(await fits(page)).toBe(true)
    })

    test("the firewall's line and picture stand in one place loading and loaded", async ({
      page,
    }) => {
      await mockNetwork(page)
      await mockHostLogs(page)
      await holdsStill(page, {
        path: "/network/firewall",
        route: "/firewall/",
        body: firewall,
        identity: "About the firewall",
        heading: "Inbound",
      })
      await expect(page.locator("[data-slot=stat-grid], [data-slot=stat-tile]")).toHaveCount(0)
      expect(await fits(page)).toBe(true)
    })

    test("the gateway's line and picture stand in one place loading and loaded", async ({
      page,
    }) => {
      await mockNetwork(page, [], { overrides: gatewayOverrides })
      await holdsStill(page, {
        path: "/network/gateway",
        route: "/network/gateway",
        body: gateway,
        identity: "About the gateway",
        heading: "Where traffic goes",
      })
      await expect(page.locator("[data-slot=stat-grid], [data-slot=stat-tile]")).toHaveCount(0)
      expect(await fits(page)).toBe(true)
    })

    test("a forward is one height with a tag, a warning or switched off", async ({ page }) => {
      await mockNetwork(page, [], { overrides: gatewayOverrides })
      await page.goto("/network/gateway")
      await loaded(page)
      const rows = page.locator("[data-slot=choice-row]")
      await expect(rows).toHaveCount(7)
      await risen(page)
      const heights = await rows.evaluateAll((items) =>
        items.map((item) => Math.round(item.getBoundingClientRect().height)),
      )
      // The six rows that open an editor are one height to the pixel. The
      // seventh is a link to the VPN page, and `ChoiceRow` sets a link's
      // name a pixel shorter than a button's.
      expect(new Set(heights.slice(0, 6)).size, `row heights ${heights.join(", ")}`).toBe(1)
      expect(Math.abs(heights[6] - heights[0])).toBeLessThanOrEqual(1)
      if (width < 640) return
      // The state words start on one edge down each list.
      const lefts = await page
        .locator("[data-slot=entry-state]")
        .evaluateAll((items) => items.map((item) => Math.round(item.getBoundingClientRect().left)))
      expect(new Set(lefts.slice(0, 4)).size, `forward states at ${lefts.join(", ")}`).toBe(1)
    })

    test("a repair is one height whether it can be applied, shows a change or is review only", async ({
      page,
    }) => {
      await mockNetwork(page, [], { overrides: { "/network/drift": busy } })
      await page.goto("/network/drift")
      await loaded(page)
      const rows = page.locator("[data-slot=repair-row]")
      await expect(rows).toHaveCount(2)
      // On a phone a row is a card that wraps what it has to say.
      if (width >= 640) expect(await height(rows.nth(0))).toBe(await height(rows.nth(1)))
      const second = await top(rows.nth(1))
      await page
        .getByRole("checkbox", {
          name: "Select repair for /etc/just-dashboard/network/links.batch",
        })
        .check()
      expect(await top(rows.nth(1))).toBe(second)
      // No framed notice stands over the plan: one sentence, then the rows.
      const plan = page.getByRole("region", { name: "Owned repair plan" })
      await expect(plan.getByText("Review only", { exact: true })).toHaveCount(1)
      await expect(plan.getByText("Selected repairs available")).toHaveCount(0)
      await expect(plan).toContainText("Pick what to review.")
      await expect(plan).toContainText("Review only")
      await plan.getByRole("button", { name: "Why 10.30.0.0/24 is review only" }).focus()
      await expect(page.getByRole("tooltip")).toContainText("Routes are restored by the boot unit")
      await page.keyboard.press("Escape")
      await plan.getByRole("button", { name: "1 excluded resource" }).click()
      await expect(page.getByRole("list", { name: "Excluded resources" })).toContainText(
        "A rule this dashboard does not own occupies the owned comment.",
      )
      expect(await fits(page)).toBe(true)
    })
  })
}

test("the overview's command, menu and readings are where the tiles' figures went", async ({
  page,
}) => {
  await mockNetwork(page)
  await page.goto("/network")
  await loaded(page)
  const identity = page.getByRole("region", { name: "About the network" })
  await expect(identity).toHaveAttribute("data-layout", "project")
  await expect(identity).toContainText("203.0.113.20")
  await expect(identity.getByText("Uplink up", { exact: true })).toBeVisible()
  await expect(identity.getByRole("link", { name: "Run a check" })).toHaveAttribute(
    "href",
    "/network/tools",
  )
  await identity.getByRole("button", { name: "Network actions" }).click()
  for (const verb of ["Refresh", "Check from outside", "Capture packets"])
    await expect(page.getByRole("menuitem", { name: verb })).toBeVisible()
  await page.getByRole("menuitem", { name: "Check from outside" }).click()
  await expect(page).toHaveURL(/\/network\/external$/)
  await page.goBack()
  await loaded(page)

  // In and Out are two lines of one height, each the way to the uplink.
  const lines = page.locator("[data-slot=reading-line]")
  await expect(lines).toHaveCount(2)
  await expect(lines.nth(0)).toContainText("received")
  await expect(lines.nth(1)).toContainText("sent")
  expect(await height(lines.nth(0))).toBe(await height(lines.nth(1)))
  await expect(lines.nth(1)).toHaveAttribute("href", "/network/interfaces/ens3")

  // The connections and what was refused are this server's, said on its node;
  // the VPN peers online stayed on the tunnels' own nodes.
  const hub = page.locator("[data-slot=wire-flow]")
  await expect(hub.getByTestId("hub-connections")).toHaveText(
    "214 connections · 11 from the internet",
  )
  await expect(hub).toContainText("27 listening · 18,422 refused")
  await expect(page.locator('li[data-node="link:wg0"]')).toContainText("2 of 4 peers online")
  await expect(page.locator('li[data-node="link:tailscale0"]')).toContainText("4 of 7 online")
})

test("a reader's overview offers the check and the refresh, not an administrator's pages", async ({
  page,
}) => {
  await mockNetwork(page, [], { session: reader })
  await page.goto("/network")
  await loaded(page)
  await expect(page.getByRole("link", { name: "Run a check" })).toBeVisible()
  await page.getByRole("button", { name: "Network actions" }).click()
  await expect(page.getByRole("menuitem")).toHaveText(["Refresh"])
})

test("an uplink that is down, and a server with no way out, are the line's state", async ({
  page,
}) => {
  await mockNetwork(page, [], {
    overrides: {
      "/network/overview": {
        ...overview,
        links: overview.links.map((link) =>
          link.name === "ens3" ? { ...link, carrier: false } : link,
        ),
      },
    },
  })
  await page.goto("/network")
  await loaded(page)
  const identity = page.getByRole("region", { name: "About the network" })
  await expect(identity.getByText("Uplink down", { exact: true })).toBeVisible()

  await mockNetwork(page, [], { overrides: { "/network/overview": { ...overview, defaults: [] } } })
  await page.reload()
  await loaded(page)
  await expect(identity.getByText("No default route", { exact: true })).toBeVisible()
})

test("a failed first read of the overview keeps its line and says so", async ({ page }) => {
  await mockNetwork(page)
  await page.route("**/api/v1/network/overview", (route) =>
    json(route, { error: { code: "read_failed", message: "The overview did not answer" } }, 503),
  )
  await page.goto("/network")
  const identity = page.getByRole("region", { name: "About the network" })
  await expect(identity.getByText("Not read", { exact: true })).toBeVisible()
  await expect(page.getByText("The overview did not answer")).toBeVisible()
  await expect(page.getByRole("heading", { name: "Topology" })).toHaveCount(0)
})

const limited = {
  ...firewall,
  rules: [
    ...firewall.rules.slice(0, 2),
    {
      number: 3,
      action: "LIMIT",
      direction: "IN",
      to: "2222/tcp",
      from: "Anywhere",
      port: "2222",
      protocol: "tcp",
      comment: "bastion",
      raw: "2222/tcp LIMIT IN Anywhere # bastion",
    },
  ],
}

test("the firewall's log counts are on the nodes that did the refusing, and narrow the log", async ({
  page,
}) => {
  await mockNetwork(page, [], { overrides: { "/firewall/": limited } })
  const logs = await mockHostLogs(page)
  await page.goto("/network/firewall")
  await loaded(page)
  const picture = page.locator("[data-slot=firewall-picture]")
  // What was refused is under the default; what the limit turned away is
  // under the one port it limits.
  await expect(picture.getByTestId("refused-reading")).toHaveText(
    "5 refused from 2 addresses in 24h",
  )
  const port = picture.locator('li[data-node="opening:2222"]')
  await expect(port).toContainText("from anyone · rate-limited")
  await expect(port).toContainText("2 turned away in 24h")
  await expect(page.getByLabel("Log lines")).toBeVisible()
  await port.getByRole("button", { name: "Show the lines a limit turned away at 2222" }).click()
  await expect.poll(() => logs.sockets.at(-1)?.getAll("f")).toEqual(["event:limit"])
  await picture.getByRole("button", { name: "Show the lines the firewall refused" }).click()
  await expect.poll(() => logs.sockets.at(-1)?.getAll("f")).toEqual(["event:block"])
})

test("the firewall is drawn as the packet filter it drives", async ({ page }) => {
  await mockNetwork(page)
  await mockHostLogs(page)
  await page.goto("/network/firewall")
  await loaded(page)
  const identity = page.getByRole("region", { name: "About the firewall" })
  await expect(identity).toHaveAttribute("data-layout", "project")
  // No backend has a mark this tree carries: each is drawn as netfilter.
  await expect(identity.locator("img[src='/logos/netfilter.webp']")).toBeVisible()
  await expect(
    page.locator("[data-slot=firewall-picture] img[src='/logos/netfilter.webp']"),
  ).toBeVisible()

  await mockNetwork(page, [], {
    overrides: {
      "/firewall/": { ...firewall, backend: "firewalld", zone: "public", logging: "off" },
    },
  })
  await page.reload()
  await loaded(page)
  await expect(identity).toContainText("firewalld")
  await expect(identity.locator("img[src='/logos/netfilter.webp']")).toBeVisible()
  await expect(
    page.locator("[data-slot=firewall-picture] img[src='/logos/netfilter.webp']"),
  ).toBeVisible()
  // firewalld's own icon is GPL artwork and is not shipped: nothing draws
  // it, and its address answers with the app's page, not a picture.
  await expect(page.locator("img[src*='firewalld']")).toHaveCount(0)
  const answer = await page.request.get("/logos/firewalld.webp")
  expect(answer.headers()["content-type"] ?? "").not.toContain("image")
})

test("defaults are staged as segments and saved together by the page's one bar", async ({
  page,
}) => {
  const mutations: Mutation[] = []
  await mockNetwork(page, mutations)
  await mockHostLogs(page)
  await page.goto("/network/firewall")
  await loaded(page)
  const defaults = page.getByRole("form", { name: "Defaults" })
  const rows = defaults.locator("[data-slot=firewall-default]")
  await expect(rows).toHaveCount(4)
  await expect(page.getByRole("combobox", { name: /default$/ })).toHaveCount(0)
  // Under the rules it finishes, not beside them.
  expect(await top(defaults)).toBeGreaterThan(await top(page.locator("#rules")))
  const third = await top(rows.nth(2))

  await defaults
    .getByRole("radiogroup", { name: "Outbound default" })
    .getByRole("radio", { name: "deny" })
    .click()
  await defaults
    .getByRole("radiogroup", { name: "Logging level" })
    .getByRole("radio", { name: "medium" })
    .click()
  // Staged, marked on its row, and nothing under it moved.
  expect(mutations).toEqual([])
  await expect(defaults.locator("[data-edited]")).toHaveCount(2)
  expect(await top(rows.nth(2))).toBe(third)
  const bar = page.getByRole("region", { name: "Save changes" })
  await expect(bar).toContainText("2 unsaved changes")
  await expect(bar).toContainText("Applies immediately")

  // Discard puts the rows back and sends nothing.
  await bar.getByRole("button", { name: "Discard" }).click()
  await expect(defaults.locator("[data-edited]")).toHaveCount(0)
  await expect(bar).toHaveCount(0)
  expect(mutations).toEqual([])

  await defaults
    .getByRole("radiogroup", { name: "Outbound default" })
    .getByRole("radio", { name: "deny" })
    .click()
  await defaults
    .getByRole("radiogroup", { name: "Logging level" })
    .getByRole("radio", { name: "medium" })
    .click()
  await bar.getByRole("button", { name: /^Save/ }).click()
  await expect.poll(() => mutations.length).toBe(2)
  // The requests each default always sent.
  expect(mutations).toEqual([
    { method: "POST", path: "/firewall/policy", body: { direction: "outgoing", policy: "deny" } },
    { method: "POST", path: "/firewall/logging", body: { level: "medium" } },
  ])
})

test("an inbound default that turns traffic away is still asked about first", async ({ page }) => {
  const mutations: Mutation[] = []
  const preflights: string[] = []
  await mockNetwork(page, mutations)
  await mockHostLogs(page)
  await page.route("**/api/v1/firewall/preflight?**", async (route) => {
    preflights.push(new URL(route.request().url()).search)
    await json(route, { backend: "ufw", findings: [], checks: [] })
  })
  await page.goto("/network/firewall")
  await loaded(page)
  const defaults = page.getByRole("form", { name: "Defaults" })
  await defaults
    .getByRole("radiogroup", { name: "Inbound default" })
    .getByRole("radio", { name: "reject" })
    .click()
  await defaults
    .getByRole("radiogroup", { name: "Routed default" })
    .getByRole("radio", { name: "allow" })
    .click()
  const bar = page.getByRole("region", { name: "Save changes" })
  await bar.getByRole("button", { name: /^Save/ }).click()
  const dialog = page.getByRole("dialog")
  await expect(dialog).toContainText("Deny inbound by default")
  await expect(dialog).toContainText(
    "The server refuses a default that would refuse your connection, SSH or public ingress",
  )
  await expect(dialog.getByTestId("firewall-preflight")).toContainText(
    "Every required way in stays admitted",
  )
  expect(preflights[0]).toContain("op=policy")
  expect(preflights[0]).toContain("direction=incoming")
  expect(preflights[0]).toContain("policy=reject")
  // Backing out sends nothing at all, the other staged default included.
  await dialog.getByRole("button", { name: "Cancel", exact: true }).click()
  expect(mutations).toEqual([])
  await expect(bar).toContainText("2 unsaved changes")

  await bar.getByRole("button", { name: /^Save/ }).click()
  await page.getByRole("dialog").getByRole("button", { name: "Apply", exact: true }).click()
  await expect.poll(() => mutations.length).toBe(2)
  expect(mutations).toEqual([
    { method: "POST", path: "/firewall/policy", body: { direction: "incoming", policy: "reject" } },
    { method: "POST", path: "/firewall/policy", body: { direction: "routed", policy: "allow" } },
  ])
  await expect(bar).toContainText("Saved")
})

test("a reader is shown the firewall's defaults in words and no control it cannot use", async ({
  page,
}) => {
  const mutations: Mutation[] = []
  await mockNetwork(page, mutations, { session: reader })
  await mockHostLogs(page)
  await page.goto("/network/firewall")
  await loaded(page)
  const defaults = page.getByRole("form", { name: "Defaults" })
  await expect(defaults.getByTestId("default-incoming")).toHaveText("deny")
  await expect(defaults.getByTestId("default-outgoing")).toHaveText("allow")
  await expect(defaults.getByTestId("default-routed")).toHaveText("deny")
  await expect(defaults.getByTestId("default-logging")).toHaveText("low")
  await expect(defaults.getByRole("radiogroup")).toHaveCount(0)
  await expect(page.getByRole("switch", { name: "Firewall enabled" })).toHaveCount(0)
  for (const control of ["Add rule", "Edit rule", "Delete rule", "Rule history"])
    await expect(page.getByRole("button", { name: control, exact: true })).toHaveCount(0)
  await page.getByRole("button", { name: "Firewall actions" }).click()
  await expect(page.getByRole("menuitem")).toHaveText(["Refresh"])
  expect(mutations).toEqual([])
})

test("an administrator without the destructive capability is offered only what the server takes", async ({
  page,
}) => {
  const withIds = {
    ...firewall,
    rules: firewall.rules.map((rule, i) => ({ ...rule, id: `fw-00000000000${i + 1}` })),
  }
  await mockNetwork(page, [], { session: careful, overrides: { "/firewall/": withIds } })
  await mockHostLogs(page)
  await page.goto("/network/firewall")
  await loaded(page)
  // Adding and editing a rule and the log level are not destructive routes.
  await expect(page.getByRole("button", { name: "Add rule", exact: true })).toBeVisible()
  await expect(page.getByRole("button", { name: "Edit rule" })).toHaveCount(3)
  await expect(page.getByRole("radiogroup", { name: "Logging level" })).toBeVisible()
  // The switch, a default, a removal, a plan and a reset are.
  await expect(page.getByRole("switch", { name: "Firewall enabled" })).toHaveCount(0)
  await expect(page.getByRole("radiogroup", { name: "Inbound default" })).toHaveCount(0)
  await expect(page.getByTestId("default-incoming")).toHaveText("deny")
  for (const control of ["Delete rule", "Remove in a plan"])
    await expect(page.getByRole("button", { name: control })).toHaveCount(0)
  await page.getByRole("button", { name: "Firewall actions" }).click()
  await expect(page.getByRole("menuitem")).toHaveText(["Refresh"])
  await page.keyboard.press("Escape")
  await page.getByRole("button", { name: "Add rule", exact: true }).click()
  await expect(page.getByRole("dialog").getByRole("button", { name: "Add to plan" })).toHaveCount(0)
})

test("a recorded change is one line, and what it asked for opens from it", async ({ page }) => {
  await mockNetwork(page, [], {
    overrides: {
      "/firewall/history": {
        events: [
          {
            id: 2,
            at: new Date(Date.now() - 60_000).toISOString(),
            actor: "operator",
            backend: "ufw",
            operation: "delete",
            outcome: "refused",
            detail: "this rule would cut off your own connection to the dashboard",
            rule: { action: "allow", port: "8443", protocol: "tcp", from: "100.64.0.0/10" },
            changeId: "0123456789abcdef",
          },
          {
            id: 1,
            at: new Date(Date.now() - 1_800_000).toISOString(),
            actor: "",
            backend: "ufw",
            operation: "add",
            outcome: "applied",
          },
        ],
        limits: ["Only changes made from this dashboard are recorded."],
      },
    },
  })
  await mockHostLogs(page)
  await page.goto("/network/firewall")
  await loaded(page)
  const changes = page.locator("[data-slot=firewall-change]")
  await expect(changes).toHaveCount(2)
  // What the record cannot see is the note on the title, not a line under
  // the list, and the guard is the note on the rules it protects, not a footer.
  await reads(page, "What the record cannot see", "Only changes made from this dashboard are")
  await reads(page, "About the lock-out guard", "Your current address is protected from lockout.")
  // With a request, a detail and a pending tag, or with none of them.
  expect(await height(changes.nth(0))).toBe(await height(changes.nth(1)))
  await expect(changes.nth(0)).toContainText("refused")
  await expect(changes.nth(0)).toContainText("allow 8443/tcp from 100.64.0.0/10")
  await expect(changes.nth(1)).toContainText("by an API caller")
  const second = await top(changes.nth(1))
  await changes
    .nth(0)
    .getByRole("button", { name: /^What delete asked for/ })
    .click()
  const request = page.getByRole("dialog")
  await expect(request).toContainText(
    "this rule would cut off your own connection to the dashboard",
  )
  await expect(request.getByRole("region", { name: "Asked for" })).toContainText("100.64.0.0/10")
  await expect(request).toContainText("pending 01234567")
  expect(await top(changes.nth(1))).toBe(second)
})

test("a host with no firewall offers ufw where the package manager has it", async ({ page }) => {
  const mutations: Mutation[] = []
  await mockNetwork(page, mutations, { overrides: { "/firewall/": { available: false } } })
  await page.route("**/api/v1/packages/ufw", (route) => json(route, { name: "ufw" }))
  await page.goto("/network/firewall")
  await expect(page.getByText("No firewall on this host", { exact: true })).toBeVisible()
  await expect(page.getByText(/ufw goes in switched off/)).toBeVisible()
  await page.getByRole("button", { name: "Install ufw" }).click()
  await expect
    .poll(() => mutations.find((m) => m.path === "/packages/install")?.body)
    .toEqual({ packages: ["ufw"] })

  // Where it does not, the page says what is missing and offers nothing to press.
  await page.route("**/api/v1/packages/ufw", (route) =>
    json(route, { error: { code: "not_found", message: "no such package" } }, 404),
  )
  await page.reload()
  await expect(page.getByText("No firewall on this host", { exact: true })).toBeVisible()
  await expect(page.getByRole("button", { name: "Install ufw" })).toHaveCount(0)
})

test("the gateway opens on its line: what is in force, what forwards, and admission as a word", async ({
  page,
}) => {
  const mutations: Mutation[] = []
  await mockNetwork(page, mutations, { overrides: gatewayOverrides })
  await page.goto("/network/gateway")
  await loaded(page)
  const identity = page.getByRole("region", { name: "About the gateway" })
  await expect(identity).toHaveAttribute("data-layout", "project")
  await expect(identity.getByText("In force", { exact: true })).toBeVisible()
  await expect(identity.getByTestId("gateway-forwarding")).toHaveText("IPv4 forwarding · IPv6 off")
  await expect(identity).toContainText("3 of 4 forwards")
  await expect(identity).toContainText("3 of 3 NAT · 1 made by the VPN page")
  await expect(identity.getByText("Admission present", { exact: true })).toBeVisible()
  // The block of sentences the page opened on is gone from its flow.
  await expect(page.getByRole("heading", { name: "Policy evidence" })).toHaveCount(0)
  await expect(page.getByText("as modeled")).toHaveCount(0)

  // What each kind carried is its section's own total.
  const forwards = page.locator("[data-slot=panel]").filter({ hasText: "Port forwards" })
  await expect(forwards.locator("[data-slot=panel-header]")).toContainText(
    /^Port forwards4 · [\d.]+K packets · [\d.]+ MB since load/,
  )

  // The command and the menu make what the section heads made.
  await identity.getByRole("button", { name: "Forward a port" }).click()
  await expect(page.getByRole("dialog").getByLabel("Public port")).toBeVisible()
  await page.keyboard.press("Escape")
  await identity.getByRole("button", { name: "Gateway actions" }).click()
  await page.getByRole("menuitem", { name: "Share a network" }).click()
  await expect(page.getByRole("dialog").getByLabel("Leaves through")).toBeVisible()
  await page.keyboard.press("Escape")
  expect(mutations.filter((m) => m.path !== "/network/gateway/preview")).toEqual([])
})

test("entries saved and not in the kernel, and a gateway with nothing made, are the line's state", async ({
  page,
}) => {
  await mockNetwork(page, [], {
    overrides: { ...gatewayOverrides, "/network/gateway": { ...gateway, loaded: false } },
  })
  await page.goto("/network/gateway")
  await loaded(page)
  const identity = page.getByRole("region", { name: "About the gateway" })
  await expect(identity.getByText("Not loaded", { exact: true })).toBeVisible()
  await expect(identity).toContainText("0 of 4 forwards")

  await mockNetwork(page, [], {
    overrides: {
      ...gatewayOverrides,
      "/network/gateway": {
        ...gateway,
        forwards: [],
        nat: [],
        flows: [],
        admission: { needed: false, present: false },
      },
    },
  })
  await page.reload()
  await loaded(page)
  await expect(identity.getByText("Nothing passed on", { exact: true })).toBeVisible()
  await expect(identity.getByText("Nothing to admit", { exact: true })).toBeVisible()
  await expect(page.getByText("No port is forwarded.")).toBeVisible()
  await expect(page.getByText("No network is shared out.")).toBeVisible()
})

const resource = "/etc/just-dashboard/network/links.batch"
const observation = {
  id: `render:${resource}`,
  domain: "render",
  resource,
  status: "drift" as const,
  coverage: "configuration",
  owned: true,
  repairable: true,
  reason: "The owned file differs from the saved configuration.",
  expected: { sha256: "expected-render" },
  observed: { sha256: "changed-render" },
}

/** A host whose boot unit ran, with two repairs of different kinds and an excluded resource. */
const busy = {
  checkedAt: "2026-10-08T12:00:00Z",
  finishedAt: "2026-10-08T12:00:01Z",
  status: "drift",
  consistent: true,
  savedGeneration: "saved-generation",
  canonicalGeneration: "canonical-generation",
  spec: {
    ...observation,
    id: "spec",
    resource: "spec.json",
    status: "matching",
    repairable: false,
  },
  journal: {
    ...observation,
    id: "journal",
    resource: "change.json",
    status: "not_required",
    repairable: false,
  },
  files: [observation],
  runtime: [
    {
      ...observation,
      id: "route:3",
      domain: "route",
      resource: "3",
      status: "missing",
      reason: "The saved managed route is absent.",
      expected: { family: "inet", destination: "10.30.0.0/24", table: "100" },
      observed: undefined,
    },
    {
      ...observation,
      id: "admission:inet/DOCKER-USER",
      domain: "admission",
      resource: "inet/DOCKER-USER",
      status: "conflict",
      owned: false,
      reason: "A rule this dashboard does not own occupies the owned comment.",
      expected: { position: "1" },
      observed: { position: "3", tool: "iptables" },
    },
  ],
  blocklists: [],
  boot: {
    status: "drift",
    unit: "just-dashboard-network.service",
    owned: true,
    repairable: true,
    reason: "The unit file differs from the one the dashboard writes.",
    loadState: "loaded",
    unitFileState: "enabled",
    activeState: "active",
    needDaemonReload: "yes",
    execution: {
      status: "succeeded",
      source: "systemd",
      scope: "current boot, last activation",
      bootTrigger: "unknown",
      bootId: "4f1c2d3e4f5a6b7c8d9e0f1a2b3c4d5e",
      invocationId: "9a8b7c6d5e4f3a2b1c0d9e8f7a6b5c4d",
      startedAt: "Thu 2026-10-08 11:58:01 UTC",
      finishedAt: "Thu 2026-10-08 11:58:02 UTC",
      startedMonotonicUs: 12_000_000,
      finishedMonotonicUs: 12_640_000,
      commands: [
        { path: "/usr/sbin/sysctl", exitStatus: 0, ignoreErrors: false },
        { path: "/usr/sbin/bridge", exitStatus: 1, ignoreErrors: true },
        { path: "/usr/sbin/nft", exitStatus: 0, ignoreErrors: false },
      ],
    },
  },
  repairPlan: {
    generation: "saved-generation",
    createdAt: "2026-10-08T12:00:01Z",
    status: "review_required",
    executable: true,
    blockers: [],
    excluded: [
      {
        observationId: "admission:inet/DOCKER-USER",
        reason: "A rule this dashboard does not own occupies the owned comment.",
      },
    ],
    preconditions: ["Saved generation must still match."],
    items: [
      {
        id: `repair:${observation.id}`,
        observationId: observation.id,
        domain: "render",
        resource,
        action: "regenerate_owned_file",
        reason: observation.reason,
        preconditions: ["Refuse foreign replacements."],
        executable: true,
        reviewToken: "reviewed-token",
        effect: "boot_files",
        before: "# Generated owned render\n# old boot input\n",
        after: "# Generated owned render\n# reviewed boot input\n",
      },
      {
        id: "repair:route:3",
        observationId: "route:3",
        domain: "route",
        resource: "3",
        action: "restore_managed_route",
        reason: "The saved managed route is absent.",
        blocker: "Routes are restored by the boot unit, not from this page.",
        preconditions: ["Refuse foreign replacements."],
      },
    ],
  },
} as unknown as DriftReport

test("the boot unit is one line with its state, and its activation opens from the line", async ({
  page,
}) => {
  await mockNetwork(page, [], { overrides: { "/network/drift": busy } })
  await page.goto("/network/drift")
  await loaded(page)
  const identity = page.getByRole("region", { name: "About the managed network" })
  await expect(identity).toHaveAttribute("data-layout", "project")
  await expect(identity.getByRole("button", { name: "Inspect again" })).toBeVisible()

  const section = page.getByRole("region", { name: "Boot unit" })
  const unit = section.locator("[data-slot=boot-unit]")
  await expect(unit).toContainText("just-dashboard-network.service")
  await expect(unit).toContainText("needs reload yes")
  await expect(unit).toContainText("Last activation succeeded")
  await expect(unit).toContainText("ran 640 ms")
  await expect(unit).toContainText("Changed")
  await expect(unit).toContainText("The unit file differs from the one the dashboard writes.")
  // One line: no heading, sentence or notice stacked under the unit.
  expect(await height(unit)).toBeLessThanOrEqual(60)
  await expect(section.getByText("Reboot attribution is unknown")).toHaveCount(0)
  await expect(section.getByText("Execution evidence")).toHaveCount(0)
  const after = await top(page.getByRole("region", { name: "Owned repair plan" }))

  await unit.getByRole("button", { name: "Activation", exact: true }).click()
  const activation = page.getByRole("dialog")
  const commands = activation.getByRole("region", { name: "Commands the unit ran" })
  await expect(commands.getByRole("listitem")).toHaveCount(3)
  await expect(commands).toContainText("exit 1, ignored by the unit")
  await expect(activation.getByRole("region", { name: "Execution evidence" })).toContainText(
    "Thu 2026-10-08 11:58:02 UTC",
  )
  // Nothing under the line moved to make room for it.
  expect(await top(page.getByRole("region", { name: "Owned repair plan" }))).toBe(after)
})

test("drift's line stands before the first inspection lands, saying one is in flight", async ({
  page,
}) => {
  await mockNetwork(page)
  const release = await held(page, "/network/drift", busy)
  await page.goto("/network/drift")
  const identity = page.getByRole("region", { name: "About the managed network" })
  await expect(identity).toContainText("Inspecting…")
  await risen(page)
  const before = await top(identity)
  release()
  await expect(identity.getByRole("button", { name: /^\d differences$/ })).toBeVisible()
  await risen(page)
  expect(await top(identity)).toBe(before)
})
