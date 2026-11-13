import { expect, test, type Locator, type Page } from "@playwright/test"
import { admin, json, mockNetwork, type Mutation } from "./network-fixture"
import { risen } from "./risen"
import type { EgressGroup, EgressView } from "../../src/lib/network-egress"

/**
 * Egress groups against a mocked API: the list as one card a group, and a
 * group's own page — the member carrying its traffic as its state, the
 * picture of what it does, every member's own measurement, the decision
 * record with its evidence, the simulation automation waits on, and the
 * commands, each sending exactly what the routes take and each destructive
 * one confirmed.
 */

const at = (minutes: number) => new Date(Date.now() - minutes * 60_000).toISOString()

const thresholds = {
  intervalSeconds: 10,
  timeoutMillis: 1000,
  lossPercent: 20,
  latencyMillis: 300,
  window: 5,
  failAfter: 3,
  recoverAfter: 5,
  holdSeconds: 60,
  stableSeconds: 120,
}

const history = (good: boolean, latency: number) =>
  Array.from({ length: 12 }, (_, i) => ({
    at: at(12 - i),
    latencyMillis: good ? latency + (i % 3) : undefined,
    loss: good ? 0 : 100,
    good,
  }))

const providers: EgressGroup = {
  id: 3,
  slot: 0,
  name: "providers",
  family: "inet",
  policy: { kind: "all" },
  probes: [
    { kind: "icmp", target: "1.1.1.1" },
    { kind: "tcp", target: "9.9.9.9", port: 443 },
  ],
  thresholds,
  sticky: true,
  connections: "flush",
  failback: "automatic",
  protected: ["100.110.34.9/32"],
  enabled: true,
  automation: true,
  active: [2],
  decidedAt: at(4),
  decidedBy: "egress monitor",
  simulation: { id: "a1b2c3d4e5f6", fingerprint: "fp-providers", passedAt: at(60) },
  fingerprint: "fp-providers",
  table: 7700,
  members: [
    {
      id: 1,
      name: "provider-a",
      kind: "gateway",
      gateway: "203.0.113.1",
      device: "eth0",
      priority: 1,
      weight: 1,
      table: 7701,
      mark: "0x100/0x7f00",
      probeSource: "203.0.113.20",
      state: "down",
      since: at(4),
      proven: false,
      active: false,
      good: 0,
      bad: 9,
      last: {
        at: at(0),
        probes: [
          { kind: "icmp", target: "1.1.1.1", ok: false, error: "no answer before the timeout" },
          { kind: "tcp", target: "9.9.9.9", ok: false, error: "no answer before the timeout" },
        ],
        ok: 0,
        total: 2,
        loss: 100,
        good: false,
        why: "no probe answered",
      },
      history: history(false, 0),
    },
    {
      id: 2,
      name: "provider-b",
      kind: "gateway",
      gateway: "198.51.100.1",
      device: "eth1",
      priority: 2,
      weight: 1,
      table: 7702,
      mark: "0x200/0x7f00",
      probeSource: "198.51.100.20",
      state: "up",
      since: at(90),
      proven: true,
      active: true,
      good: 30,
      bad: 0,
      last: {
        at: at(0),
        probes: [
          { kind: "icmp", target: "1.1.1.1", ok: true, rttMillis: 18.2 },
          { kind: "tcp", target: "9.9.9.9", ok: true, rttMillis: 21.4 },
        ],
        ok: 2,
        total: 2,
        loss: 0,
        latencyMillis: 19.8,
        good: true,
      },
      history: history(true, 19),
    },
  ],
  advice: { action: "none", reason: "The decided members are the best proven members." },
  runtime: { status: "verified", route: "through eth1 via 198.51.100.1", checkedAt: at(0) },
  readiness: [
    {
      level: "ok",
      text: "Every member's device is up and each gateway is on its device's own network. Provider routing beyond this host is not visible from here.",
    },
  ],
  events: [
    {
      id: 12,
      groupId: 3,
      at: at(4),
      kind: "switch",
      action: "failover",
      outcome: "applied",
      actor: "egress monitor",
      reason: "provider-a is down (no probe answered); provider-b is the best proven member.",
      before: [1],
      after: [2],
      evidence: {
        members: [
          { id: 1, state: "down", loss: 100, ok: 0, total: 2, why: "no probe answered" },
          { id: 2, state: "up", loss: 0, ok: 2, total: 2, latencyMillis: 19.8 },
        ],
        routeBefore: "through eth0 via 203.0.113.1 (table 7700)",
        routeAfter: "through eth1 via 198.51.100.1 (table 7700)",
        connections: {
          read: 214,
          byMember: { "1": 37 },
          moved: 37,
          pinned: 0,
          flushed: 37,
          basis: "Pinned connections by their mark; others by the member's local address.",
        },
        change: "5f0c1d2e3a4b5c6d7e8f901a2b3c4d5e",
      },
    },
    {
      id: 11,
      groupId: 3,
      at: at(4.5),
      kind: "state",
      memberId: 1,
      outcome: "observed",
      actor: "egress monitor",
      reason: "provider-a is down after 3 consecutive bad samples: no probe answered.",
    },
  ],
  lastSimulation: {
    id: "a1b2c3d4e5f6",
    groupId: 3,
    fingerprint: "fp-providers",
    status: "passed",
    actor: "operator",
    startedAt: at(62),
    finishedAt: at(60),
    result: {
      phases: [
        { name: "warmup", description: "Every link clean.", start: 0, end: 5, faults: {} },
        {
          name: "failure",
          description: "provider-a drops every packet.",
          start: 6,
          end: 10,
          faults: { "1": "loss 100%" },
        },
      ],
      steps: Array.from({ length: 11 }, (_, i) => ({
        index: i,
        phase: i < 6 ? "warmup" : "failure",
        seconds: i * 10,
        members: [],
        active: i >= 8 ? [2] : [1],
        decision:
          i === 8
            ? { action: "failover", target: [2], reason: "provider-a is down (no probe answered)" }
            : { action: "none", reason: "steady" },
        switched: i === 8,
        dataPath: "reached 1.1.1.1 through the group table",
      })),
      expectations: [
        {
          name: "Fails over after a sustained failure",
          passed: true,
          detail: "Failed over at sample 3.",
        },
        {
          name: "A flapping member is not failed back to",
          passed: true,
          detail: "0 failbacks while it flapped.",
        },
      ],
      topology: ["jds1a2bh models this host with the group's own tables."],
      limits: ["Each member is modelled as a gateway on a veth link."],
      cleanup: "Every simulation namespace, with its devices and queues, was removed.",
      intervalSeconds: 10,
    },
  },
}

const vpn: EgressGroup = {
  ...providers,
  id: 4,
  slot: 1,
  name: "vpn clients",
  policy: { kind: "selector", from: "10.8.0.0/24" },
  sticky: false,
  enabled: false,
  automation: false,
  active: [1],
  simulation: undefined,
  fingerprint: "fp-vpn",
  table: 7710,
  members: providers.members.map((m) => ({ ...m, state: "up", proven: true, active: m.id === 1 })),
  events: [],
  lastSimulation: undefined,
  advice: undefined,
  automationBlock:
    "Enable the group before automating it: automation switches a group that carries traffic.",
}

/** A third group with what the other two lack: IPv6, a marked policy, a long name, no decision yet. */
const lab: EgressGroup = {
  ...vpn,
  id: 5,
  slot: 2,
  name: "lab uplinks over the long tunnel",
  family: "inet6",
  policy: { kind: "rule", fwmark: "0x40", iif: "wg-lab" },
  fingerprint: "fp-lab",
  table: 7720,
  decidedAt: undefined,
  decidedBy: undefined,
  protected: undefined,
  members: vpn.members.map((m) => ({ ...m, state: "unknown", proven: false, last: undefined })),
}

const view: EgressView = {
  groups: [providers, vpn],
  capacity: 6,
  persistent: true,
  monitoring: at(120),
}

const reader = { ...admin, capabilities: ["read"], user: { ...admin.user, role: "readonly" } }

type Options = { reader?: boolean; data?: EgressView; path?: string }

async function mockEgress(page: Page, mutations: Mutation[], options: Options = {}) {
  await mockNetwork(page, mutations, {
    session: options.reader ? reader : admin,
    overrides: {
      "/network/egress": options.data ?? view,
      "/network/changes/current": { available: false, owned: false, change: null },
    },
  })
  await page.goto(options.path ?? "/network/egress")
}

/** The list, read. */
async function openList(page: Page, mutations: Mutation[] = [], options: Options = {}) {
  await mockEgress(page, mutations, options)
  await expect(page.getByRole("link", { name: "Open providers" })).toBeVisible()
}

/** A group's page, read. */
async function openGroup(
  page: Page,
  name: string,
  mutations: Mutation[] = [],
  options: Options & { tab?: string } = {},
) {
  const query = options.tab ? `?tab=${options.tab}` : ""
  await mockEgress(page, mutations, {
    ...options,
    path: `/network/egress/${encodeURIComponent(name)}${query}`,
  })
  await expect(header(page)).toContainText(name)
  await expect(page.getByRole("tablist", { name: `Views of ${name}` })).toBeVisible()
}

const header = (page: Page) => page.locator('[data-slot="egress-group-header"]')
const cards = (page: Page) => page.locator('[data-slot="choice-row"]')
const menu = async (page: Page, label = "Group actions") => {
  await page.getByRole("button", { name: label }).click()
  return page.getByRole("menu")
}

async function box(target: Locator) {
  const found = await target.boundingBox()
  if (!found) throw new Error("nothing to measure")
  return found
}

const sideways = (page: Page) =>
  page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)

/** Holds the first read of the groups until released, to look at the page while it loads. */
async function holdEgress(page: Page, data: EgressView = view) {
  let release = () => {}
  const held = new Promise<void>((resolve) => (release = resolve))
  await page.route("**/api/v1/network/egress", async (route) => {
    if (route.request().method() !== "GET") return route.fallback()
    await held
    await json(route, data)
  })
  return release
}

test.describe("the list", () => {
  test("opens on the identity line and one card a group, with no tiles", async ({ page }) => {
    await openList(page)
    await expect(page.locator('[data-slot="stat-grid"], [data-slot="stat-tile"]')).toHaveCount(0)
    const identity = page.locator('[data-slot="egress-identity"]')
    await expect(identity).toContainText("Egress groups")
    await expect(identity).toContainText("2 of 6 groups")
    await expect(identity).toContainText("1 carrying traffic")
    await expect(identity).toContainText("1 automatic")
    await expect(identity).toContainText("3 of 4 members proven")
    await expect(identity).toContainText("1 member down")
    await expect(cards(page)).toHaveCount(2)
    const first = cards(page).nth(0)
    await expect(first).toContainText("providers")
    await expect(first).toContainText("sticky · 2 members")
    await expect(first).toContainText("Through provider-b")
    await expect(first).toContainText("Automatic")
    const second = cards(page).nth(1)
    await expect(second).toContainText("not sticky · 2 members")
    await expect(second).toContainText("Not carrying traffic")
    await expect(second).toContainText("Off")
    // Nothing of a group's detail is laid out on the list any more.
    await expect(page.getByRole("table")).toHaveCount(0)
  })

  test("a card opens the group's page", async ({ page }) => {
    await openList(page)
    await page.getByRole("link", { name: "Open vpn clients" }).click()
    await expect(page).toHaveURL(/\/network\/egress\/vpn%20clients$/)
    await expect(header(page)).toContainText("vpn clients")
  })

  for (const width of [1280, 390]) {
    test(`every card is one height at ${width}, whatever its group holds`, async ({ page }) => {
      await page.setViewportSize({ width, height: 900 })
      await openList(page, [], {
        data: {
          ...view,
          groups: [providers, vpn, lab],
          running: {
            id: "f00d",
            groupId: 4,
            step: 11,
            steps: 63,
            phase: "failure",
            startedAt: at(1),
          },
        },
      })
      await expect(cards(page)).toHaveCount(3)
      await risen(page)
      const boxes = await Promise.all([0, 1, 2].map((i) => box(cards(page).nth(i))))
      expect(boxes[1].height).toBe(boxes[0].height)
      expect(boxes[2].height).toBe(boxes[0].height)
      // The next card starts where a card of plain data would have put it.
      expect(boxes[2].y - boxes[1].y).toBe(boxes[1].y - boxes[0].y)
      expect(await sideways(page)).toBeLessThanOrEqual(1)
    })

    test(`the identity line and the first card stand where they will while loading at ${width}`, async ({
      page,
    }) => {
      await page.setViewportSize({ width, height: 900 })
      await mockNetwork(page, [], { overrides: { "/network/egress": view } })
      const release = await holdEgress(page)
      await page.goto("/network/egress")
      const identity = page.locator('[data-slot="egress-identity"]')
      await expect(identity).toContainText("Reading the groups")
      await risen(page)
      const before = await box(identity)
      const ground = await box(page.getByRole("list", { name: "Reading the egress groups" }))
      release()
      await expect(cards(page)).toHaveCount(2)
      await risen(page)
      const after = await box(identity)
      expect(after.y).toBe(before.y)
      expect(after.height).toBe(before.height)
      expect((await box(cards(page).first())).y).toBe(ground.y)
    })
  }

  test("a read-only account sees the groups and no control it cannot use", async ({ page }) => {
    await openList(page, [], { reader: true })
    await expect(cards(page)).toHaveCount(2)
    await expect(page.getByRole("button", { name: "New group" })).toHaveCount(0)
    await expect(page.getByRole("button", { name: /^Actions for/ })).toHaveCount(0)
    await expect(
      page.locator('[data-slot="page"]').getByRole("button", { disabled: true }),
    ).toHaveCount(0)
  })

  test("a card's menu holds the group's verbs, confirmed as on its page", async ({ page }) => {
    const mutations: Mutation[] = []
    await openList(page, mutations)
    const items = await menu(page, "Actions for vpn clients")
    for (const name of ["Run simulation", "Enable", "Automate", "Edit", "Remove"])
      await expect(items.getByRole("menuitem", { name, exact: true })).toBeVisible()
    // A group that carries nothing has nothing to fail over.
    await expect(items.getByRole("menuitem", { name: "Fail over now" })).toHaveCount(0)
    await items.getByRole("menuitem", { name: "Remove" }).click()
    const dialog = page.getByRole("dialog")
    await expect(dialog.getByText(/tables, rules and connection pinning are removed/)).toBeVisible()
    await dialog.getByRole("button", { name: "Remove" }).click()
    await expect
      .poll(() => mutations.find((m) => m.method === "DELETE" && m.path === "/network/egress/4"))
      .toBeTruthy()
  })

  test("a running simulation is said on its card", async ({ page }) => {
    await openList(page, [], {
      data: {
        ...view,
        running: {
          id: "f00d",
          groupId: 4,
          step: 11,
          steps: 63,
          phase: "failure",
          startedAt: at(1),
        },
      },
    })
    await expect(cards(page).nth(1)).toContainText("Simulating 12 of 63")
  })

  test("no group yet is one sentence and the way to make one", async ({ page }) => {
    await mockEgress(page, [], { data: { ...view, groups: [] } })
    await expect(page.getByText("No egress groups")).toBeVisible()
    await expect(page.locator('[data-slot="egress-identity"]')).toContainText("0 of 6 groups")
    // One way to make the first group, not two.
    await expect(page.getByRole("button", { name: "New group" })).toHaveCount(1)
    await page
      .locator('[data-slot="empty-state"]')
      .getByRole("button", { name: "New group" })
      .click()
    await expect(page.getByRole("dialog")).toContainText("New egress group")
  })

  test("a read that fails says so and can be tried again", async ({ page }) => {
    await mockNetwork(page, [])
    let fail = true
    await page.route("**/api/v1/network/egress", (route) =>
      fail
        ? json(
            route,
            {
              error: { code: "unavailable", message: "netlink is not answering", retryable: true },
            },
            503,
          )
        : json(route, view),
    )
    await page.goto("/network/egress")
    await expect(page.getByText("netlink is not answering")).toBeVisible()
    await expect(page.locator('[data-slot="egress-identity"]')).toContainText("Not read")
    fail = false
    await page.getByRole("button", { name: "Try again" }).click()
    await expect(cards(page)).toHaveCount(2)
  })

  test("a store that cannot keep the record says so", async ({ page }) => {
    await openList(page, [], { data: { ...view, persistent: false } })
    await expect(page.getByText("Decisions are kept in memory only")).toBeVisible()
  })

  test("a new group sends exactly its draft and keeps it after a refusal", async ({ page }) => {
    const mutations: Mutation[] = []
    await openList(page, mutations)
    let status = 409
    await page.route("**/api/v1/network/egress", async (route) => {
      if (route.request().method() !== "POST") return route.fallback()
      mutations.push({
        method: "POST",
        path: "/network/egress",
        body: route.request().postDataJSON(),
      })
      if (status === 409)
        return json(
          route,
          {
            error: {
              code: "would_lock_you_out",
              message: "member lan: jd-lan is not a tunnel this dashboard owns",
            },
          },
          409,
        )
      return json(route, { ...vpn, id: 9, name: "uplinks" }, 201)
    })
    await page.getByRole("button", { name: "New group" }).click()
    const dialog = page.getByRole("dialog")
    await dialog.locator("#egress-name").fill("uplinks")
    await dialog.locator("#egress-m0-name").fill("a")
    await dialog.locator("#egress-m0-gw").fill("192.0.2.1")
    await dialog.locator("#egress-m0-dev").fill("eth0")
    await dialog.locator("#egress-m1-name").fill("b")
    await dialog.locator("#egress-m1-gw").fill("198.51.100.1")
    await dialog.locator("#egress-m1-dev").fill("eth1")
    await dialog.locator("#egress-p0-target").fill("1.1.1.1")
    await dialog.getByLabel("Stable before failback (s)").fill("180")
    await dialog.getByRole("button", { name: "Create group" }).click()
    await expect(dialog.getByRole("alert")).toContainText("not a tunnel this dashboard owns")
    await expect(dialog.locator("#egress-m1-gw")).toHaveValue("198.51.100.1")
    await expect(dialog.getByLabel("Stable before failback (s)")).toHaveValue("180")
    expect(mutations.find((m) => m.path === "/network/egress")?.body).toEqual({
      name: "uplinks",
      family: "inet",
      policy: { kind: "all" },
      members: [
        {
          name: "a",
          kind: "gateway",
          gateway: "192.0.2.1",
          device: "eth0",
          priority: 1,
          weight: 1,
        },
        {
          name: "b",
          kind: "gateway",
          gateway: "198.51.100.1",
          device: "eth1",
          priority: 2,
          weight: 1,
        },
      ],
      probes: [{ kind: "icmp", target: "1.1.1.1" }],
      thresholds: { ...thresholds, stableSeconds: 180 },
      sticky: false,
      connections: "flush",
      failback: "automatic",
      protected: [],
    })
    status = 201
    await dialog.getByRole("button", { name: "Create group" }).click()
    await expect(dialog).toHaveCount(0)
  })

  test("a draft the server would refuse is checked before anything is sent", async ({ page }) => {
    const mutations: Mutation[] = []
    await openList(page, mutations)
    await page.getByRole("button", { name: "New group" }).click()
    const dialog = page.getByRole("dialog")
    await dialog.getByRole("button", { name: "Create group" }).click()
    await expect(dialog.getByText("Name the group.")).toBeVisible()
    await expect(dialog.getByText(/needs a gateway address/).first()).toBeVisible()
    expect(mutations.filter((m) => m.path === "/network/egress")).toHaveLength(0)
  })

  test("every slot taken leaves nothing to add", async ({ page }) => {
    await openList(page, [], { data: { ...view, capacity: 2 } })
    await expect(page.getByRole("button", { name: "New group" })).toBeDisabled()
  })
})

test.describe("a group's page", () => {
  test("opens on the carrying member, the picture and the members", async ({ page }) => {
    await openGroup(page, "providers")
    await expect(page.locator('[data-slot="stat-grid"], [data-slot="stat-tile"]')).toHaveCount(0)
    await expect(header(page)).toContainText("Through provider-b")
    await expect(header(page)).toContainText("automatic")
    await expect(header(page)).toContainText("simulation passed")
    await expect(header(page)).toContainText("route verified")
    await expect(header(page).getByRole("button", { name: "Run simulation" })).toBeEnabled()

    const picture = page.locator('[data-slot="wire-flow"]')
    await expect(picture).toContainText("Selected traffic")
    await expect(picture).toContainText("All traffic")
    await expect(picture).toContainText("to the default route")
    await expect(picture).toContainText("Group table 7700")
    await expect(picture.getByText(/^decided .+ ago$/)).toHaveAttribute(
      "title",
      /by egress monitor$/,
    )
    await expect(picture).toContainText("Down · no probe answered")
    await expect(picture).toContainText("Carrying · 19.8 ms")
    await expect(picture).toContainText("198.51.100.1 · eth1")

    const members = page.getByRole("table", { name: "Members of providers" })
    const a = members.getByRole("row", { name: "Member provider-a" })
    await expect(a.getByText("Down", { exact: true })).toBeVisible()
    await expect(a.getByText("no probe answered")).toBeVisible()
    const b = members.getByRole("row", { name: "Member provider-b" })
    await expect(b.getByText("Up", { exact: true })).toBeVisible()
    await expect(b.getByText("carrying", { exact: true })).toBeVisible()
    await expect(b.getByText("19.8 ms")).toBeVisible()
    await expect(b.getByRole("img", { name: "12 of 12 recent samples good" })).toBeVisible()
    await expect(page.getByText(/Every member's device is up/)).toBeVisible()
    await expect(page.getByRole("list", { name: "What each member is bound to" })).toContainText(
      "via 198.51.100.1 on eth1 · tier 2 · mark 0x200/0x7f00 · table 7702 · from 198.51.100.20",
    )
    // What only the form used to show.
    const rules = page.locator('[data-slot="panel"]', { hasText: "How it decides" })
    await expect(rules).toContainText("icmp 1.1.1.1, tcp 9.9.9.9:443")
    await expect(rules).toContainText("after 3 bad samples")
    await expect(rules).toContainText("100.110.34.9/32")
  })

  test("its views are in the address, and the address opens on one", async ({ page }) => {
    await openGroup(page, "providers", [], { tab: "simulation" })
    await expect(page.getByRole("tab", { name: "Simulation" })).toHaveAttribute(
      "aria-selected",
      "true",
    )
    await expect(page.getByText("Fails over after a sustained failure")).toBeVisible()
    await expect(page.getByRole("table", { name: "Simulated phases" })).toContainText(
      "Sample 3: failover to provider-b",
    )
    await page.getByText("Topology, limits and cleanup").click()
    await expect(page.getByText(/Every simulation namespace/)).toBeVisible()
    await page.getByRole("tab", { name: /Decisions/ }).click()
    await expect(page).toHaveURL(/\/network\/egress\/providers\?tab=decisions$/)
    await expect(page.getByRole("list", { name: "Decisions of providers" })).toBeVisible()
    await page.getByRole("tab", { name: /Members/ }).click()
    await expect(page).toHaveURL(/\/network\/egress\/providers$/)
  })

  test("the decision record opens on its evidence: routes, measurements and connections", async ({
    page,
  }) => {
    await openGroup(page, "providers", [], { tab: "decisions" })
    const decisions = page.getByRole("list", { name: "Decisions of providers" })
    await expect(decisions.getByText("Failover", { exact: true })).toBeVisible()
    await expect(decisions.getByText("Member down", { exact: true })).toBeVisible()
    await decisions.getByText("Evidence").first().click()
    await expect(decisions.getByText("through eth0 via 203.0.113.1 (table 7700)")).toBeVisible()
    await expect(decisions.getByText("through eth1 via 198.51.100.1 (table 7700)")).toBeVisible()
    await expect(
      decisions.getByText(/214 tracked; 37 moved, 0 kept pinned, 37 flushed/),
    ).toBeVisible()
    await expect(decisions.getByText("5f0c1d2e3a4b5c6d7e8f901a2b3c4d5e")).toBeVisible()
  })

  test("a read-only account reads everything and sees no command", async ({ page }) => {
    await openGroup(page, "providers", [], { reader: true })
    await expect(header(page)).toContainText("Through provider-b")
    await expect(page.getByRole("row", { name: "Member provider-b" })).toBeVisible()
    await expect(page.getByRole("button", { name: "Run simulation" })).toHaveCount(0)
    await expect(page.getByRole("button", { name: "Group actions" })).toHaveCount(0)
    await expect(page.getByRole("switch")).toHaveCount(0)
    await page.getByRole("tab", { name: /Decisions/ }).click()
    await expect(page.getByText("Failover", { exact: true })).toBeVisible()
    await page.getByRole("tab", { name: "Simulation" }).click()
    await expect(page.getByText("Fails over after a sustained failure")).toBeVisible()
  })

  test("fail over now is confirmed and sends the bounded switch", async ({ page }) => {
    const mutations: Mutation[] = []
    const steady: EgressView = {
      ...view,
      groups: [
        {
          ...providers,
          active: [1],
          members: providers.members.map((m) => ({
            ...m,
            state: "up",
            proven: true,
            active: m.id === 1,
          })),
        },
        vpn,
      ],
    }
    await openGroup(page, "providers", mutations, { data: steady })
    await page.route("**/api/v1/network/egress/3/switch", async (route) => {
      mutations.push({
        method: "POST",
        path: "/network/egress/3/switch",
        body: route.request().postDataJSON(),
      })
      await json(route, { group: providers, event: 13, before: [1], after: [2] })
    })
    const items = await menu(page)
    // The best tier already carries traffic: there is nothing to fail back to.
    await expect(items.getByRole("menuitem", { name: "Fail back now" })).toBeDisabled()
    await items.getByRole("menuitem", { name: "Fail over now" }).click()
    const dialog = page.getByRole("dialog")
    await expect(dialog.getByText(/pinned ones stay on their member/)).toBeVisible()
    await dialog.getByRole("button", { name: "Fail over" }).click()
    await expect(dialog).toHaveCount(0)
    expect(mutations.find((m) => m.path === "/network/egress/3/switch")?.body).toEqual({
      action: "failover",
    })
  })

  test("fail back now sends its own switch when a better tier is up again", async ({ page }) => {
    const mutations: Mutation[] = []
    const recovered: EgressView = {
      ...view,
      groups: [
        {
          ...providers,
          members: providers.members.map((m) => ({ ...m, state: "up", proven: true })),
          advice: { action: "failback", target: [1], reason: "provider-a has been up for 2m." },
        },
        vpn,
      ],
    }
    await openGroup(page, "providers", mutations, { data: recovered })
    // What the rules would do now, which the old page read and never showed.
    await expect(
      page.getByText("The rules would fail back to provider-a now: provider-a has been up for 2m."),
    ).toBeVisible()
    const items = await menu(page)
    await items.getByRole("menuitem", { name: "Fail back now" }).click()
    await page.getByRole("dialog").getByRole("button", { name: "Fail back" }).click()
    await expect
      .poll(() => mutations.find((m) => m.path === "/network/egress/3/switch")?.body)
      .toEqual({ action: "failback" })
  })

  test("a dead member leaves nothing to fail over to", async ({ page }) => {
    await openGroup(page, "providers")
    const items = await menu(page)
    await expect(items.getByRole("menuitem", { name: "Fail over now" })).toBeDisabled()
    await expect(items.getByRole("menuitem", { name: "Fail back now" })).toBeDisabled()
  })

  test("disable and enable are confirmed with what changes", async ({ page }) => {
    const mutations: Mutation[] = []
    await openGroup(page, "providers", mutations)
    await (await menu(page)).getByRole("menuitem", { name: "Disable", exact: true }).click()
    const dialog = page.getByRole("dialog")
    await expect(dialog.getByText(/returns to whatever the main table says/)).toBeVisible()
    await dialog.getByRole("button", { name: "Disable" }).click()
    await expect
      .poll(() => mutations.find((m) => m.path === "/network/egress/3/disable"))
      .toBeTruthy()

    await page.goto("/network/egress/vpn%20clients")
    await expect(header(page)).toContainText("Not carrying traffic")
    // The member it has decided on carries nothing while the group is off.
    await expect(page.locator('[data-slot="wire-flow"]')).toContainText("Would carry")
    const decided = page.getByRole("row", { name: "Member provider-a" })
    await expect(decided.getByText("decided", { exact: true })).toBeVisible()
    await expect(decided.getByText("carrying", { exact: true })).toHaveCount(0)
    await (await menu(page)).getByRole("menuitem", { name: "Enable" }).click()
    await expect(page.getByRole("dialog").getByText(/Traffic from 10\.8\.0\.0\/24/)).toBeVisible()
    await page.getByRole("dialog").getByRole("button", { name: "Enable" }).click()
    await expect
      .poll(() => mutations.find((m) => m.path === "/network/egress/4/enable"))
      .toBeTruthy()
  })

  test("automation waits for a carrying group and a passed simulation of its configuration", async ({
    page,
  }) => {
    const mutations: Mutation[] = []
    const stale: EgressView = {
      ...view,
      groups: [
        {
          ...providers,
          automation: false,
          fingerprint: "fp-edited",
          automationBlock:
            "The configuration changed since the last passed simulation; run it again.",
        },
        vpn,
      ],
    }
    await openGroup(page, "providers", mutations, { data: stale, tab: "simulation" })
    await expect(header(page)).toContainText("simulation out of date")
    await expect(
      page.getByText("Passed for an earlier configuration", { exact: true }),
    ).toBeVisible()
    await expect(
      page.getByText(/configuration changed since the last passed simulation/),
    ).toBeVisible()
    await expect((await menu(page)).getByRole("menuitem", { name: "Automate" })).toBeDisabled()
    await page.keyboard.press("Escape")

    await page.goto("/network/egress/vpn%20clients?tab=simulation")
    await expect(header(page)).toContainText("not simulated")
    await expect(page.getByText("Not simulated", { exact: true })).toBeVisible()
    await expect(page.getByText(/Enable the group before automating it/)).toBeVisible()
    await expect((await menu(page)).getByRole("menuitem", { name: "Automate" })).toBeDisabled()
    await page.keyboard.press("Escape")
    await header(page).getByRole("button", { name: "Run simulation" }).click()
    await expect
      .poll(() => mutations.find((m) => m.path === "/network/egress/4/simulate"))
      .toBeTruthy()
  })

  test("turning automation on is confirmed", async ({ page }) => {
    const mutations: Mutation[] = []
    const ready: EgressView = { ...view, groups: [{ ...providers, automation: false }, vpn] }
    await openGroup(page, "providers", mutations, { data: ready })
    await expect(header(page)).toContainText("switches by hand")
    await (await menu(page)).getByRole("menuitem", { name: "Automate" }).click()
    const dialog = page.getByRole("dialog")
    await expect(
      dialog.getByText(/Simulation a1b2c3d4e5f6 showed these rules deciding/),
    ).toBeVisible()
    await dialog.getByRole("button", { name: "Turn on" }).click()
    await expect
      .poll(() => mutations.find((m) => m.path === "/network/egress/3/automation/on"))
      .toBeTruthy()
  })

  test("turning automation off needs no confirmation", async ({ page }) => {
    const mutations: Mutation[] = []
    await openGroup(page, "providers", mutations)
    await (await menu(page)).getByRole("menuitem", { name: "Stop automating" }).click()
    await expect
      .poll(() => mutations.find((m) => m.path === "/network/egress/3/automation/off"))
      .toBeTruthy()
    await expect(page.getByRole("dialog")).toHaveCount(0)
  })

  test("a running simulation shows its progress and holds the command", async ({ page }) => {
    await openGroup(page, "vpn clients", [], {
      tab: "simulation",
      data: {
        ...view,
        running: {
          id: "f00d",
          groupId: 4,
          step: 11,
          steps: 63,
          phase: "failure",
          startedAt: at(1),
        },
      },
    })
    await expect(page.getByText("Running · sample 12 of 63 · failure")).toBeVisible()
    await expect(header(page).getByRole("button", { name: /Simulating/ })).toBeDisabled()
  })

  test("edit waits for a disabled group, saves it and follows a new name", async ({ page }) => {
    const mutations: Mutation[] = []
    await openGroup(page, "providers", mutations)
    await expect(
      (await menu(page)).getByRole("menuitem", { name: "Edit (disable it first)" }),
    ).toBeDisabled()
    await page.keyboard.press("Escape")

    await page.goto("/network/egress/vpn%20clients?tab=decisions")
    await (await menu(page)).getByRole("menuitem", { name: "Edit", exact: true }).click()
    const dialog = page.getByRole("dialog")
    await expect(dialog).toContainText("Edit vpn clients")
    await expect(dialog.locator("#egress-name")).toHaveValue("vpn clients")
    await dialog.locator("#egress-name").fill("vpn")
    await dialog.getByRole("button", { name: "Save" }).click()
    await expect
      .poll(() => mutations.find((m) => m.method === "PUT" && m.path === "/network/egress/4"))
      .toMatchObject({ body: { name: "vpn", policy: { kind: "selector", from: "10.8.0.0/24" } } })
    // The address is the name, so it moves with it, and keeps the view.
    await expect(page).toHaveURL(/\/network\/egress\/vpn\?tab=decisions$/)
  })

  test("remove is confirmed and goes back to the list", async ({ page }) => {
    const mutations: Mutation[] = []
    await openGroup(page, "vpn clients", mutations)
    await (await menu(page)).getByRole("menuitem", { name: "Remove" }).click()
    await page.getByRole("dialog").getByRole("button", { name: "Remove" }).click()
    await expect
      .poll(() => mutations.find((m) => m.method === "DELETE" && m.path === "/network/egress/4"))
      .toBeTruthy()
    await expect(page).toHaveURL(/\/network\/egress$/)
  })

  test("a route the kernel does not hold is said above the picture", async ({ page }) => {
    await openGroup(page, "providers", [], {
      data: {
        ...view,
        groups: [
          {
            ...providers,
            runtime: {
              status: "drift",
              missing: ["default via 198.51.100.1 table 7700"],
              route: "",
              checkedAt: at(0),
            },
          },
          vpn,
        ],
      },
    })
    await expect(page.getByText("The kernel does not hold the decided route")).toBeVisible()
    await expect(page.getByText("Missing: default via 198.51.100.1 table 7700.")).toBeVisible()
  })

  test("the list stands beside it from xl, and Alt+↓ walks it with the view kept", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1440, height: 900 })
    await openGroup(page, "providers", [], { tab: "decisions" })
    const rail = page.locator('[data-slot="egress-rail"]')
    await expect(rail).toBeVisible()
    await expect(rail.getByRole("link", { name: "providers" })).toHaveAttribute(
      "aria-current",
      "page",
    )
    await expect(rail.getByRole("link", { name: "vpn clients" })).toContainText(
      "Not carrying traffic",
    )
    await expect(page.getByRole("button", { name: "Previous group" })).toBeDisabled()
    await page.keyboard.press("Alt+ArrowDown")
    await expect(page).toHaveURL(/\/network\/egress\/vpn%20clients\?tab=decisions$/)
    await expect(header(page)).toContainText("vpn clients")
    await expect(page.getByText("Nothing has been decided yet.")).toBeVisible()
    await expect(page.getByRole("button", { name: "Next group" })).toBeDisabled()
    await page.getByRole("button", { name: "Previous group" }).click()
    await expect(page).toHaveURL(/\/network\/egress\/providers\?tab=decisions$/)
    await rail.getByRole("link", { name: "vpn clients" }).click()
    await expect(page).toHaveURL(/\/network\/egress\/vpn%20clients\?tab=decisions$/)
  })

  test("a read that fails says so, with the way back", async ({ page }) => {
    await mockNetwork(page, [])
    await page.route("**/api/v1/network/egress", (route) =>
      json(route, { error: { code: "unavailable", message: "netlink is not answering" } }, 503),
    )
    await page.goto("/network/egress/providers")
    await expect(page.getByText("netlink is not answering")).toBeVisible()
    await expect(page.getByRole("link", { name: "Egress groups" }).first()).toBeVisible()
    await expect(page.getByText("Reading the group")).toHaveCount(0)
  })

  test("a name no group has says so and leads back", async ({ page }) => {
    await mockEgress(page, [], { path: "/network/egress/gone" })
    await expect(page.getByText("No egress group named gone")).toBeVisible()
    await page
      .locator('[data-slot="empty-state"]')
      .getByRole("link", { name: "Egress groups" })
      .click()
    await expect(page).toHaveURL(/\/network\/egress$/)
  })

  for (const width of [1280, 390]) {
    test(`the header and the picture stand where they will while loading at ${width}`, async ({
      page,
    }) => {
      await page.setViewportSize({ width, height: 900 })
      await mockNetwork(page, [], { overrides: { "/network/egress": view } })
      const release = await holdEgress(page)
      await page.goto("/network/egress/providers")
      await expect(header(page)).toContainText("Reading the group")
      const picture = page.locator('[data-slot="wire-flow"]')
      await expect(picture).toBeVisible()
      await risen(page)
      const before = await box(header(page))
      const drawn = await box(picture)
      release()
      await expect(header(page)).toContainText("Through provider-b")
      await risen(page)
      const after = await box(header(page))
      expect(after.y).toBe(before.y)
      expect(after.height).toBe(before.height)
      const settled = await box(picture)
      expect(settled.y).toBe(drawn.y)
      expect(settled.height).toBe(drawn.height)
    })

    test(`members are one height with and without a reason at ${width}`, async ({ page }) => {
      await page.setViewportSize({ width, height: 900 })
      await openGroup(page, "providers")
      const rows =
        width >= 768
          ? page.getByRole("row", { name: /^Member provider/ })
          : page.getByRole("listitem", { name: /^Member provider/ })
      await expect(rows).toHaveCount(2)
      await risen(page)
      const a = await box(rows.nth(0))
      const b = await box(rows.nth(1))
      expect(a.height).toBe(b.height)
      expect(b.y).toBe(a.y + a.height)
      expect(await sideways(page)).toBeLessThanOrEqual(1)
    })
  }

  test("a phone gets one pane: no rail, every verb, nothing sideways", async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 900 })
    await openGroup(page, "providers")
    await expect(page.locator('[data-slot="egress-rail"]')).toBeHidden()
    await expect(header(page).getByRole("button", { name: "Run simulation" })).toBeVisible()
    const items = await menu(page)
    await expect(items.getByRole("menuitem", { name: "Disable", exact: true })).toBeVisible()
    await page.keyboard.press("Escape")
    for (const tab of ["Decisions", "Simulation"]) {
      await page.getByRole("tab", { name: new RegExp(tab) }).click()
      expect(await sideways(page)).toBeLessThanOrEqual(1)
    }
  })
})

test.describe("screenshots", () => {
  const dir = process.env.JD_EGRESS_SHOTS
  test.skip(!dir, "set JD_EGRESS_SHOTS to a directory to capture them")
  const running = {
    id: "f00d",
    groupId: 4,
    step: 11,
    steps: 63,
    phase: "failure",
    startedAt: at(1),
  }
  for (const width of [390, 1280]) {
    test.describe(`at ${width}`, () => {
      const shot = async (page: Page, name: string) => {
        await risen(page)
        await page.waitForTimeout(900)
        await page.screenshot({ path: `${dir}/after-${name}-${width}.png`, fullPage: true })
      }
      test.beforeEach(async ({ page }) => {
        await page.setViewportSize({ width, height: 900 })
      })
      test(`the list at ${width}`, async ({ page }) => {
        await openList(page, [], { data: { ...view, groups: [providers, vpn, lab], running } })
        await shot(page, "egress-full")
      })
      test(`the list, read-only, at ${width}`, async ({ page }) => {
        await openList(page, [], { reader: true })
        await shot(page, "egress-readonly")
      })
      test(`the list, empty, at ${width}`, async ({ page }) => {
        await mockEgress(page, [], { data: { ...view, groups: [] } })
        await expect(page.getByText("No egress groups")).toBeVisible()
        await shot(page, "egress-empty")
      })
      test(`the list, loading, at ${width}`, async ({ page }) => {
        await mockNetwork(page, [])
        await holdEgress(page)
        await page.goto("/network/egress")
        await expect(page.getByText("Reading the groups")).toBeVisible()
        await shot(page, "egress-loading")
      })
      test(`the list, failed, at ${width}`, async ({ page }) => {
        await mockNetwork(page, [])
        await page.route("**/api/v1/network/egress", (route) =>
          json(route, { error: { code: "unavailable", message: "netlink is not answering" } }, 503),
        )
        await page.goto("/network/egress")
        await expect(page.getByText("netlink is not answering")).toBeVisible()
        await shot(page, "egress-failed")
      })
      test(`a group at ${width}`, async ({ page }) => {
        await openGroup(page, "providers")
        await shot(page, "group-full")
      })
      for (const tab of ["decisions", "simulation"]) {
        test(`a group's ${tab} at ${width}`, async ({ page }) => {
          await openGroup(page, "providers", [], { tab })
          await shot(page, `group-${tab}`)
        })
      }
      test(`a group that is off at ${width}`, async ({ page }) => {
        await openGroup(page, "vpn clients", [], { tab: "simulation" })
        await shot(page, "group-off")
      })
      test(`a group, read-only, at ${width}`, async ({ page }) => {
        await openGroup(page, "providers", [], { reader: true })
        await shot(page, "group-readonly")
      })
      test(`a group, loading, at ${width}`, async ({ page }) => {
        await mockNetwork(page, [])
        await holdEgress(page)
        await page.goto("/network/egress/providers")
        await expect(header(page)).toContainText("Reading the group")
        await shot(page, "group-loading")
      })
      test(`a group that is gone at ${width}`, async ({ page }) => {
        await mockEgress(page, [], { path: "/network/egress/gone" })
        await expect(page.getByText("No egress group named gone")).toBeVisible()
        await shot(page, "group-gone")
      })
      test(`the menu at ${width}`, async ({ page }) => {
        await openGroup(page, "providers")
        await menu(page)
        await risen(page)
        await page.waitForTimeout(600)
        await page.screenshot({ path: `${dir}/after-group-menu-${width}.png` })
      })
    })
  }
})
