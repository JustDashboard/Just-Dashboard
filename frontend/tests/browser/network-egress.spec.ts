import { expect, test, type Page } from "@playwright/test"
import { admin, json, mockNetwork, type Mutation } from "./network-fixture"
import type { EgressGroup, EgressView } from "../../src/lib/network-egress"

/**
 * The Egress groups page against a mocked API: what each group carries and
 * through which member, every member's own measurement, the decision record
 * with its evidence, the simulation automation waits on, and the commands —
 * each sending exactly what the routes take, each destructive one confirmed.
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

const view: EgressView = {
  groups: [providers, vpn],
  capacity: 6,
  persistent: true,
  monitoring: at(120),
}

async function mockEgress(
  page: Page,
  mutations: Mutation[],
  reader = false,
  data: EgressView = view,
) {
  await mockNetwork(page, mutations, {
    session: reader
      ? { ...admin, capabilities: ["read"], user: { ...admin.user, role: "readonly" } }
      : admin,
    overrides: {
      "/network/egress": data,
      "/network/changes/current": { available: false, owned: false, change: null },
    },
  })
  await page.goto("/network/egress")
  await expect(page.getByRole("heading", { name: "providers" })).toBeVisible()
}

const providersSection = (page: Page) =>
  page.getByRole("region", { name: "Egress group providers" })

test("a read user sees groups, live members, decisions and the simulation without commands", async ({
  page,
}) => {
  await mockEgress(page, [], true)
  const section = providersSection(page)
  await expect(section.getByText("Carrying traffic through provider-b")).toBeVisible()
  const a = section.getByRole("row", { name: "Member provider-a" })
  await expect(a.getByText("Down", { exact: true })).toBeVisible()
  await expect(a.getByText("no probe answered")).toBeVisible()
  const b = section.getByRole("row", { name: "Member provider-b" })
  await expect(b.getByText("carrying")).toBeVisible()
  await expect(b.getByText("19.8 ms")).toBeVisible()
  await expect(section.getByText("mark 0x200/0x7f00")).toBeVisible()
  await expect(section.getByText("Fails over after a sustained failure")).toBeVisible()
  for (const name of ["Fail over now", "Fail back now", "Disable", "Run simulation", "Remove"]) {
    await expect(section.getByRole("button", { name, exact: true })).toBeDisabled()
  }
  await expect(section.getByRole("switch", { name: "Automate providers" })).toBeDisabled()
  await expect(page.getByRole("button", { name: "New group" })).toBeDisabled()
})

test("the decision record opens on its evidence: routes, measurements and connections", async ({
  page,
}) => {
  await mockEgress(page, [])
  const decisions = page.getByRole("list", { name: "Decisions of providers" })
  await expect(decisions.getByText("Failover", { exact: true })).toBeVisible()
  await decisions.getByText("Evidence").first().click()
  await expect(decisions.getByText("through eth0 via 203.0.113.1 (table 7700)")).toBeVisible()
  await expect(decisions.getByText("through eth1 via 198.51.100.1 (table 7700)")).toBeVisible()
  await expect(
    decisions.getByText(/214 tracked; 37 moved, 0 kept pinned, 37 flushed/),
  ).toBeVisible()
  await expect(decisions.getByText("5f0c1d2e3a4b5c6d7e8f901a2b3c4d5e")).toBeVisible()
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
  await mockEgress(page, mutations, false, steady)
  await page.route("**/api/v1/network/egress/3/switch", async (route) => {
    mutations.push({
      method: "POST",
      path: "/network/egress/3/switch",
      body: route.request().postDataJSON(),
    })
    await json(route, { group: providers, event: 13, before: [1], after: [2] })
  })
  const section = providersSection(page)
  // The best tier already carries traffic: there is nothing to fail back to.
  await expect(section.getByRole("button", { name: "Fail back now" })).toBeDisabled()
  await section.getByRole("button", { name: "Fail over now" }).click()
  const dialog = page.getByRole("dialog")
  await expect(dialog.getByText(/pinned ones stay on their member/)).toBeVisible()
  await dialog.getByRole("button", { name: "Fail over" }).click()
  await expect(dialog).toHaveCount(0)
  expect(mutations.find((m) => m.path === "/network/egress/3/switch")?.body).toEqual({
    action: "failover",
  })
})

test("a dead member leaves nothing to fail over to", async ({ page }) => {
  await mockEgress(page, [])
  const section = providersSection(page)
  await expect(section.getByRole("button", { name: "Fail over now" })).toBeDisabled()
  await expect(section.getByRole("button", { name: "Fail back now" })).toBeDisabled()
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
  await mockEgress(page, mutations, false, stale)
  const section = providersSection(page)
  await expect(section.getByRole("switch", { name: "Automate providers" })).toBeDisabled()
  await expect(section.getByText("Passed for an earlier configuration")).toBeVisible()
  await expect(
    section.getByText(/configuration changed since the last passed simulation/),
  ).toBeVisible()
  const vpnSection = page.getByRole("region", { name: "Egress group vpn clients" })
  await expect(vpnSection.getByRole("switch", { name: "Automate vpn clients" })).toBeDisabled()
  await expect(vpnSection.getByText("Not simulated")).toBeVisible()
  await vpnSection.getByRole("button", { name: "Run simulation" }).click()
  await expect
    .poll(() => mutations.find((m) => m.path === "/network/egress/4/simulate"))
    .toBeTruthy()
})

test("turning automation on is confirmed and turning it off is not", async ({ page }) => {
  const mutations: Mutation[] = []
  const ready: EgressView = { ...view, groups: [{ ...providers, automation: false }, vpn] }
  await mockEgress(page, mutations, false, ready)
  const toggle = providersSection(page).getByRole("switch", { name: "Automate providers" })
  await expect(toggle).toBeEnabled()
  await toggle.click()
  const dialog = page.getByRole("dialog")
  await expect(
    dialog.getByText(/Simulation a1b2c3d4e5f6 showed these rules deciding/),
  ).toBeVisible()
  await dialog.getByRole("button", { name: "Turn on" }).click()
  await expect
    .poll(() => mutations.find((m) => m.path === "/network/egress/3/automation/on"))
    .toBeTruthy()
})

test("a running simulation shows its progress", async ({ page }) => {
  await mockEgress(page, [], false, {
    ...view,
    running: { id: "f00d", groupId: 4, step: 11, steps: 63, phase: "failure", startedAt: at(1) },
  })
  const vpnSection = page.getByRole("region", { name: "Egress group vpn clients" })
  await expect(vpnSection.getByText("Running · sample 12 of 63 · failure")).toBeVisible()
  await expect(vpnSection.getByRole("button", { name: "Run simulation" })).toBeDisabled()
})

test("a new group sends exactly its draft and keeps it after a refusal", async ({ page }) => {
  const mutations: Mutation[] = []
  await mockEgress(page, mutations)
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
      { name: "a", kind: "gateway", gateway: "192.0.2.1", device: "eth0", priority: 1, weight: 1 },
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
  await mockEgress(page, mutations)
  await page.getByRole("button", { name: "New group" }).click()
  const dialog = page.getByRole("dialog")
  await dialog.getByRole("button", { name: "Create group" }).click()
  await expect(dialog.getByText("Name the group.")).toBeVisible()
  await expect(dialog.getByText(/needs a gateway address/).first()).toBeVisible()
  expect(mutations.filter((m) => m.path === "/network/egress")).toHaveLength(0)
})

test("the page fits a phone without sideways scrolling", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 900 })
  await mockEgress(page, [])
  const overflow = await page.evaluate(
    () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
  )
  expect(overflow).toBeLessThanOrEqual(1)
})

test.describe("screenshots", () => {
  const dir = process.env.JD_EGRESS_SHOTS
  test.skip(!dir, "set JD_EGRESS_SHOTS to a directory to capture them")
  for (const width of [390, 1280, 1720]) {
    test(`egress at ${width}`, async ({ page }) => {
      await page.setViewportSize({ width, height: 1000 })
      await mockEgress(page, [])
      await page.waitForTimeout(800)
      await page.screenshot({ path: `${dir}/egress-${width}.png`, fullPage: true })
    })
  }
})
