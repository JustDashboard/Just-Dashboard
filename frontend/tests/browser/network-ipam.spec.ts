import { expect, test, type Locator, type Page } from "@playwright/test"
import { admin, json, loaded, mockNetwork, type Mutation } from "./network-fixture"
import { ipamEmpty, ipamView } from "./network-evidence-fixture"
import { risen } from "./risen"
import type { IPAMView } from "../../src/lib/network-ipam"
const now = new Date().toISOString()
const poolId = "a".repeat(32)
const fixture: IPAMView = {
  pools: [
    {
      id: poolId,
      name: "Shared IPv6",
      prefix: "fd48:abcd::/48",
      family: "inet6",
      allocationBits: 64,
      createdAt: now,
    },
  ],
  reservations: [
    {
      id: "b".repeat(32),
      poolId,
      prefix: "fd48:abcd::/64",
      family: "inet6",
      owner: "docker_network",
      resource: "app-private",
      state: "review_required",
      acknowledgedUnknown: true,
      unknownSources: ["provider:unknown"],
      createdAt: now,
      updatedAt: now,
      startedBy: "operator",
      detail: "Native response unavailable; allocation remains held",
    },
  ],
  inventory: {
    checkedAt: now,
    finishedAt: now,
    observations: [],
    coverage: [
      {
        source: "provider",
        state: "unknown",
        detail: "Provider allocations are not authoritative from this host",
        checkedAt: now,
      },
    ],
  },
  utilization: [
    {
      poolId,
      totalAddresses: "1208925819614629174706176",
      totalBlocks: "65536",
      reservedBlocks: "1",
      observedBlocks: "0",
      unavailableBlocks: "1",
      candidateBlocks: "65535",
      coverage: "unknown",
    },
  ],
  limitations: ["Reservations are planning evidence; native ownership remains unchanged."],
}
const readonlySession = {
  ...admin,
  capabilities: ["read"],
  user: { ...admin.user, role: "readonly" },
}
async function setup(page: Page, readonly = false, mutations: Mutation[] = []) {
  await mockNetwork(
    page,
    mutations,
    readonly
      ? { session: readonlySession }
      : { overrides: { "/docker/ping": { available: true } } },
  )
  await page.route("**/api/v1/network/ipam/", (route) => json(route, fixture))
}
const top = async (page: Page, locator: Locator) => {
  await risen(page)
  return (await locator.boundingBox())!
}

test("is named as the rail names it and keeps exact IPv6 counts on the pool's band", async ({
  page,
}) => {
  await setup(page)
  await page.goto("/network/ipam")
  await expect(page.getByRole("heading", { name: "Address planning", level: 1 })).toBeVisible()
  await expect(page.locator('[data-slot="page"]')).toHaveAttribute("data-register", "reading")
  await expect(page.locator('[data-slot="stat-grid"], [data-slot="stat-tile"]')).toHaveCount(0)
  const identity = page.getByRole("region", { name: "About address planning" })
  await expect(identity).toContainText("1 coverage gap")
  await expect(identity).toContainText("1 pool")
  await expect(identity).toContainText("1 reservation held")
  const band = page.getByRole("region", { name: "Shared IPv6, fd48:abcd::/48" })
  await expect(band).toContainText("fd48:abcd::/48 → /64")
  await expect(band).toContainText("65,535 of 65,536 free")
  await expect(band).toContainText("Planned")
  await expect(band).toContainText("1 block")
  await expect(band).toContainText("1 unavailable")
  // The pool's owner is drawn as the product that holds the block.
  await expect(band.getByRole("img", { name: "Held by Docker" })).toBeVisible()
  const plan = page.getByRole("list", { name: "Planning reservations" }).getByRole("listitem")
  await expect(plan).toContainText("fd48:abcd::/64")
  await expect(plan).toContainText("app-private · Docker network")
  await expect(plan).toContainText("Review required · allocation held")
  // Held for review: there is no owner form to open with it.
  await expect(plan.getByRole("link")).toHaveCount(0)
  await plan.getByRole("button", { name: "About the plan for app-private" }).hover()
  await expect(
    page.getByRole("tooltip").filter({ hasText: "Native response unavailable" }),
  ).toBeVisible()
})

test("preview retains private owner and unknown provider evidence", async ({ page }) => {
  await setup(page)
  await page.route("**/api/v1/network/ipam/preview", (route) =>
    json(route, {
      prefix: "10.244.0.0/24",
      status: "known_overlap",
      conflicts: [
        {
          prefix: "10.244.0.0/16",
          owner: "wireguard",
          resource: "wg0",
          basis: "configured_native",
        },
      ],
      coverage: fixture.inventory.coverage,
      checkedAt: now,
      limitations: [],
    }),
  )
  await page.goto("/network/ipam")
  await page.getByLabel("Prefix to preview").fill("10.244.0.0/24")
  await page.getByRole("button", { name: "Preview overlap" }).click()
  await expect(page.getByTestId("ipam-preview")).toContainText("Known overlap")
  await expect(page.getByTestId("ipam-preview")).toContainText("wireguard · wg0")
  const coverage = page.getByRole("region", { name: "Coverage" })
  await expect(coverage).toContainText("1 gap")
  await expect(coverage.getByRole("row").filter({ hasText: "provider" })).toContainText("unknown")
  await expect(
    coverage.getByText("Provider allocations are not authoritative from this host"),
  ).toBeVisible()
  await expect(
    page.getByText("Reservations are planning evidence; native ownership remains unchanged."),
  ).toBeVisible()
  await page.getByRole("button", { name: "Dismiss preview" }).click()
  await expect(page.getByTestId("ipam-preview")).toHaveCount(0)
})

test("read account does not request private inventory or enable planner mutation", async ({
  page,
}) => {
  await setup(page, true)
  let calls = 0
  await page.route("**/api/v1/network/ipam/", (route) => {
    calls++
    return json(route, fixture)
  })
  await page.goto("/network/ipam")
  await expect(
    page.getByText(/Shared pools and private native-owner inventories require/),
  ).toBeVisible()
  // No control it cannot use; the one way on is the calculator a read account has.
  await expect(page.locator('[data-slot="page"]').getByRole("button")).toHaveCount(0)
  await expect(page.getByLabel("Prefix to preview")).toHaveCount(0)
  await expect(page.getByRole("link", { name: "Open the subnet calculator" })).toHaveAttribute(
    "href",
    "/network/tools?tool=subnet",
  )
  expect(calls).toBe(0)
})

test("failed preview preserves the prefix", async ({ page }) => {
  await setup(page)
  await page.route("**/api/v1/network/ipam/preview", (route) =>
    json(route, { error: { code: "unavailable", message: "Native source unreadable" } }, 503),
  )
  await page.goto("/network/ipam")
  await page.getByLabel("Prefix to preview").fill("fd48:abcd:1::/64")
  await page.getByRole("button", { name: "Preview overlap" }).click()
  await expect(
    page.getByRole("alert").filter({ hasText: "Native source unreadable" }),
  ).toBeVisible()
  await expect(page.getByLabel("Prefix to preview")).toHaveValue("fd48:abcd:1::/64")
})

test("a pool and a reservation are made from their sheets", async ({ page }) => {
  await setup(page)
  const sent: { path: string; body: unknown }[] = []
  await page.route("**/api/v1/network/ipam/pools", (route) => {
    sent.push({ path: "pools", body: route.request().postDataJSON() })
    return json(route, { ...fixture.pools[0], id: poolId }, 201)
  })
  let refuse = true
  await page.route("**/api/v1/network/ipam/reservations", (route) => {
    sent.push({ path: "reservations", body: route.request().postDataJSON() })
    if (refuse)
      return json(
        route,
        { error: { code: "ipam_reservation_refused", message: "No free block" } },
        409,
      )
    return json(route, fixture.reservations[0], 201)
  })
  await page.goto("/network/ipam")
  await page.getByRole("button", { name: "New pool" }).click()
  const pool = page.getByRole("dialog", { name: "New pool" })
  await expect(pool.getByRole("button", { name: "Create pool" })).toBeDisabled()
  await pool.getByLabel("Pool name").fill("Lab")
  await pool.getByLabel("Pool network").fill("10.99.0.0/16")
  await pool.getByRole("button", { name: "Create pool" }).click()
  await expect(pool).toBeHidden()
  expect(sent[0]).toEqual({
    path: "pools",
    body: { name: "Lab", prefix: "10.99.0.0/16", allocationBits: 24 },
  })

  await page.getByRole("button", { name: "Reserve", exact: true }).click()
  const reserve = page.getByRole("dialog", { name: "Reserve a block" })
  await expect(reserve.getByLabel("Shared pool")).toContainText("Shared IPv6 · fd48:abcd::/48")
  await reserve.getByRole("button", { name: "WireGuard server" }).click()
  await reserve.getByLabel("Exact native resource name").fill("wg-lab")
  await expect(reserve.getByText("Leave empty for the first free /64.")).toBeVisible()
  // The provider's ranges could not be read: reserving has to say it was seen.
  await expect(reserve.getByRole("button", { name: "Reserve", exact: true })).toBeDisabled()
  await reserve.getByRole("switch", { name: "I have reviewed the incomplete coverage" }).click()
  await reserve.getByRole("button", { name: "Reserve", exact: true }).click()
  await expect(reserve.getByRole("alert").filter({ hasText: "No free block" })).toBeVisible()
  await expect(reserve.getByLabel("Exact native resource name")).toHaveValue("wg-lab")
  refuse = false
  await reserve.getByRole("button", { name: "Reserve", exact: true }).click()
  await expect(reserve).toBeHidden()
  expect(sent.at(-1)).toEqual({
    path: "reservations",
    body: { poolId, owner: "wireguard_server", resource: "wg-lab", acknowledgeUnknown: true },
  })
})

test("a reserved plan opens its owner's creation form and is released only on review", async ({
  page,
}) => {
  const mutations: Mutation[] = []
  await mockNetwork(page, mutations, { overrides: { "/network/ipam/": ipamView } })
  await page.goto("/network/ipam")
  const plans = page.getByRole("list", { name: "Planning reservations" })
  const reserved = plans.getByRole("listitem").filter({ hasText: "wg-office" })
  await expect(
    reserved.getByRole("link", { name: "Open WireGuard creation for wg-office" }),
  ).toHaveAttribute("href", `/network/vpn?ipamReservation=${ipamView.reservations[1].id}`)
  // A plan an owner already answered for has no form to open.
  await expect(
    plans.getByRole("listitem").filter({ hasText: "shop-backend" }).getByRole("link"),
  ).toHaveCount(0)
  await page.getByRole("button", { name: "Actions for wg-office" }).click()
  await expect(page.getByRole("menuitem", { name: "Open WireGuard creation" })).toBeVisible()
  await page.getByRole("menuitem", { name: "Release plan" }).click()
  await expect(page.getByRole("dialog")).toContainText(
    "does not remove or reconfigure its native resource",
  )
  expect(mutations).toEqual([])
  await page.getByRole("button", { name: "Release plan", exact: true }).click()
  await expect.poll(() => mutations.length).toBe(1)
  expect(mutations[0]).toMatchObject({
    method: "DELETE",
    path: `/network/ipam/reservations/${ipamView.reservations[1].id}`,
  })
  await page
    .getByRole("button", { name: "Actions for Container networks" })
    .click({ timeout: 10_000 })
  await page.getByRole("menuitem", { name: "Retire" }).click()
  await expect(page.getByRole("dialog")).toContainText("Stop new planning allocations from")
  await page.getByRole("button", { name: "Retire pool", exact: true }).click()
  await expect.poll(() => mutations.length).toBe(2)
  expect(mutations[1]).toMatchObject({
    method: "DELETE",
    path: `/network/ipam/pools/${ipamView.pools[1].id}`,
  })
})

test("an administrator who may not destroy sees no Retire or Release", async ({ page }) => {
  await mockNetwork(page, [], {
    session: { ...admin, capabilities: ["read", "system.admin"] },
    overrides: { "/network/ipam/": ipamView },
  })
  await page.goto("/network/ipam")
  await loaded(page)
  await expect(page.getByRole("button", { name: "Reserve", exact: true })).toBeVisible()
  await expect(page.getByRole("button", { name: /^Actions for (Shared|Container)/ })).toHaveCount(0)
  await page.getByRole("button", { name: "Actions for wg-office" }).click()
  await expect(page.getByRole("menuitem", { name: "Open WireGuard creation" })).toBeVisible()
  await expect(page.getByRole("menuitem", { name: "Release plan" })).toHaveCount(0)
})

test("no pools is a designed state with the next step as its button", async ({ page }) => {
  await mockNetwork(page, [], { overrides: { "/network/ipam/": ipamEmpty } })
  await page.goto("/network/ipam")
  await loaded(page)
  await expect(page.getByText("No pools yet", { exact: true })).toBeVisible()
  await expect(page.getByText("No planning reservations.", { exact: true })).toBeVisible()
  // Nothing to reserve from yet: the one command on the page is the pool.
  await expect(page.getByRole("button", { name: "Reserve", exact: true })).toHaveCount(0)
  await expect(page.getByText("No sources reported", { exact: true })).toBeVisible()
  await expect(page.getByRole("button", { name: "New pool" })).toHaveCount(1)
  await page.getByRole("button", { name: "New pool" }).click()
  await expect(page.getByRole("dialog", { name: "New pool" })).toBeVisible()
})

for (const width of [1280, 390]) {
  test(`pools and plans keep one height in every state at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 })
    await mockNetwork(page, [], { overrides: { "/network/ipam/": ipamView } })
    await page.goto("/network/ipam")
    await loaded(page)
    await risen(page)
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth),
    ).toBe(true)
    // A pool one block deep, one with observed owners, and a retired one with no counts.
    const pools = page.getByRole("list", { name: "Address pools" }).locator("> li")
    await expect(pools).toHaveCount(ipamView.pools.length)
    const heights = await pools.evaluateAll((items) =>
      items.map((item) => item.getBoundingClientRect().height),
    )
    expect(new Set(heights).size).toBe(1)
    await expect(pools.nth(2)).toContainText("Blocks unknown")
    // A plan under review, one that links to its owner's form, one with an owner ID.
    const plans = page
      .getByRole("list", { name: "Planning reservations" })
      .locator('[data-slot="choice-row"]')
    await expect(plans).toHaveCount(ipamView.reservations.length)
    const boxes = await plans.evaluateAll((items) =>
      items.map((item) => item.getBoundingClientRect()).map((b) => ({ y: b.y, height: b.height })),
    )
    for (const [i, box] of boxes.entries()) {
      expect(box.height).toBe(boxes[0].height)
      if (i > 0) expect(box.y - (boxes[i - 1].y + boxes[i - 1].height)).toBeCloseTo(8, 0)
    }
  })

  test(`the opening holds its place while the plan loads at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 })
    await mockNetwork(page)
    let release!: () => void
    const held = new Promise<void>((resolve) => {
      release = resolve
    })
    await page.route("**/api/v1/network/ipam/", async (route) => {
      await held
      return json(route, { ...ipamView, pools: ipamView.pools.slice(0, 2) })
    })
    await page.goto("/network/ipam")
    const identity = page.getByRole("region", { name: "About address planning" })
    const pools = page.getByRole("region", { name: "Pools", exact: true })
    const plans = page.getByRole("region", { name: "Reservations" })
    await expect(pools.locator('[data-slot="skeleton"]').first()).toBeVisible()
    const before = {
      identity: await top(page, identity),
      pools: await top(page, pools),
      plans: await top(page, plans),
    }
    release()
    await expect(page.getByRole("list", { name: "Address pools" })).toBeVisible()
    const after = {
      identity: await top(page, identity),
      pools: await top(page, pools),
      plans: await top(page, plans),
    }
    expect(after.identity).toEqual(before.identity)
    expect(after.pools).toEqual(before.pools)
    expect(after.plans.y).toBe(before.plans.y)
  })
}

test("Docker deep link sends the exact IPv6 reservation through its owner form", async ({
  page,
}) => {
  await setup(page)
  const row = { ...fixture.reservations[0], state: "reserved" as const }
  await page.route("**/api/v1/network/ipam/", (route) =>
    json(route, { ...fixture, reservations: [row] }),
  )
  const calls: unknown[] = []
  await page.route("**/api/v1/docker/networks/", (route) => {
    if (route.request().method() === "POST") {
      calls.push(route.request().postDataJSON())
      return json(route, { id: "native-created", name: row.resource }, 201)
    }
    return json(route, [])
  })
  await page.goto(`/docker/networks?ipamReservation=${row.id}`)
  const dialog = page.getByRole("dialog", { name: "Create network" })
  await expect(dialog.getByLabel("Name", { exact: true })).toHaveValue(row.resource)
  await expect(dialog.getByLabel("IPv6 subnet (optional)")).toHaveValue(row.prefix)
  await dialog.getByRole("button", { name: "Create", exact: true }).click()
  await expect.poll(() => calls.length).toBe(1)
  expect(calls[0]).toMatchObject({
    name: row.resource,
    ipv6: true,
    ipam: [{ subnet: row.prefix }],
    ipamReservationIds: [row.id],
  })
})
test("editing a filled Docker prefix clears the selected planning identity", async ({ page }) => {
  await setup(page)
  const row = { ...fixture.reservations[0], state: "reserved" as const }
  await page.route("**/api/v1/network/ipam/", (route) =>
    json(route, { ...fixture, reservations: [row] }),
  )
  const calls: Record<string, unknown>[] = []
  await page.route("**/api/v1/docker/networks/", (route) => {
    if (route.request().method() === "POST") {
      calls.push(route.request().postDataJSON())
      return json(route, { id: "native-created", name: row.resource }, 201)
    }
    return json(route, [])
  })
  await page.goto(`/docker/networks?ipamReservation=${row.id}`)
  const dialog = page.getByRole("dialog", { name: "Create network" })
  await expect(dialog.getByLabel("IPv6 subnet (optional)")).toHaveValue(row.prefix)
  await dialog.getByLabel("IPv6 subnet (optional)").fill("fd49:abcd::/64")
  await dialog.getByRole("button", { name: "Create", exact: true }).click()
  await expect.poll(() => calls.length).toBe(1)
  expect(calls[0]).not.toHaveProperty("ipamReservationIds")
})

test("an incomplete planning refresh keeps the selected Docker draft and blocks its handoff", async ({
  page,
}) => {
  await page.clock.install()
  await setup(page)
  const row = { ...fixture.reservations[0], state: "reserved" as const }
  let incomplete = false
  await page.route("**/api/v1/network/ipam/", (route) =>
    json(route, incomplete ? [] : { ...fixture, reservations: [row] }),
  )
  await page.route("**/api/v1/docker/networks/", (route) => json(route, []))
  await page.goto(`/docker/networks?ipamReservation=${row.id}`)
  const dialog = page.getByRole("dialog", { name: "Create network" })
  await expect(dialog.getByLabel("Name", { exact: true })).toHaveValue(row.resource)
  await expect(dialog.getByLabel("IPv6 subnet (optional)")).toHaveValue(row.prefix)
  incomplete = true
  await page.clock.runFor(30_100)
  await expect(
    dialog.getByRole("alert").filter({ hasText: "inventory is unavailable" }),
  ).toBeVisible()
  await expect(dialog.getByLabel("Name", { exact: true })).toHaveValue(row.resource)
  await expect(dialog.getByLabel("IPv6 subnet (optional)")).toHaveValue(row.prefix)
  await expect(dialog.getByRole("button", { name: "Create", exact: true })).toBeDisabled()
})

test("WireGuard reservation opts IPv6 addressing in while leaving IPv6 exit off", async ({
  page,
}) => {
  await setup(page)
  const row = {
    ...fixture.reservations[0],
    owner: "wireguard_server" as const,
    resource: "wg-ipam",
    state: "reserved" as const,
  }
  await page.route("**/api/v1/network/ipam/", (route) =>
    json(route, { ...fixture, reservations: [row] }),
  )
  const calls: Record<string, unknown>[] = []
  await page.route("**/api/v1/network/vpn/wireguard", (route) => {
    calls.push(route.request().postDataJSON())
    return json(route, {
      interface: { name: row.resource, listenPort: 51820 },
      warnings: [],
      firewall: { opened: false, reason: "Fixture does not alter a firewall" },
    })
  })
  await page.goto(`/network/vpn?ipamReservation=${row.id}`)
  const dialog = page.getByRole("dialog", { name: "Create WireGuard tunnel" })
  await expect(dialog.getByLabel("Tunnel name")).toHaveValue(row.resource)
  await expect(dialog.getByLabel("IPv6 tunnel network", { exact: true })).toHaveValue(row.prefix)
  await expect(dialog.getByRole("switch", { name: "IPv6 addressing", exact: true })).toBeChecked()
  await expect(dialog.getByRole("switch", { name: "IPv6 exit", exact: true })).not.toBeChecked()
  await dialog.getByRole("button", { name: "Set up WireGuard", exact: true }).click()
  await expect.poll(() => calls.length).toBe(1)
  expect(calls[0]).toMatchObject({
    name: row.resource,
    ipamReservationIds: [row.id],
    ipv6: { subnet: row.prefix, exitNode: false },
  })
})

for (const later of ["missing", "held", "error"] as const) {
  test(`a detached Docker seed permits an unused draft after a later ${later} read`, async ({
    page,
  }) => {
    await page.clock.install()
    await setup(page)
    const row = { ...fixture.reservations[0], state: "reserved" as const }
    let refreshed = false
    await page.route("**/api/v1/network/ipam/", (route) => {
      if (refreshed && later === "error")
        return json(route, { error: { code: "unavailable", message: "Planning unavailable" } }, 500)
      return json(route, {
        ...fixture,
        reservations: !refreshed
          ? [row]
          : later === "missing"
            ? []
            : [{ ...row, state: "review_required" }],
      })
    })
    const calls: Record<string, unknown>[] = []
    await page.route("**/api/v1/docker/networks/", (route) => {
      if (route.request().method() === "POST") {
        calls.push(route.request().postDataJSON())
        return json(route, { id: "created" }, 201)
      }
      return json(route, [])
    })
    await page.goto(`/docker/networks?ipamReservation=${row.id}`)
    const dialog = page.getByRole("dialog", { name: "Create network" })
    await expect(dialog.getByLabel("IPv6 subnet (optional)")).toHaveValue(row.prefix)
    await dialog.getByLabel("Name", { exact: true }).fill("manual-private")
    await dialog.getByLabel("IPv6 subnet (optional)").fill("fd49:abcd::/64")
    refreshed = true
    await page.clock.runFor(30_100)
    if (later === "error")
      await expect(
        dialog.getByRole("alert").filter({ hasText: "inventory is unavailable" }),
      ).toBeVisible()
    await expect(dialog.getByRole("button", { name: "Create", exact: true })).toBeEnabled()
    await dialog.getByRole("button", { name: "Create", exact: true }).click()
    await expect.poll(() => calls.length).toBe(1)
    expect(calls[0]).toMatchObject({ name: "manual-private", ipam: [{ subnet: "fd49:abcd::/64" }] })
    expect(calls[0]).not.toHaveProperty("ipamReservationIds")
  })
}

test("a delayed Docker seed cannot overwrite explicit input", async ({ page }) => {
  await setup(page)
  const row = { ...fixture.reservations[0], state: "reserved" as const }
  let release!: () => void
  const pending = new Promise<void>((resolve) => {
    release = resolve
  })
  await page.route("**/api/v1/network/ipam/", async (route) => {
    await pending
    return json(route, { ...fixture, reservations: [row] })
  })
  await page.route("**/api/v1/docker/networks/", (route) => json(route, []))
  await page.goto(`/docker/networks?ipamReservation=${row.id}`)
  const dialog = page.getByRole("dialog", { name: "Create network" })
  await dialog.getByLabel("Name", { exact: true }).fill("manual-before-read")
  await dialog.getByLabel("Subnet (optional)", { exact: true }).fill("10.249.0.0/24")
  release()
  await expect(dialog.getByLabel("Shared IPv6 reservation")).toBeEnabled()
  await expect(dialog.getByLabel("Name", { exact: true })).toHaveValue("manual-before-read")
  await expect(dialog.getByLabel("Subnet (optional)", { exact: true })).toHaveValue("10.249.0.0/24")
  await expect(dialog.getByRole("button", { name: "Create", exact: true })).toBeEnabled()
})

test("a missing initial seed initializes when a later readable inventory contains it", async ({
  page,
}) => {
  await page.clock.install()
  await setup(page)
  const row = { ...fixture.reservations[0], state: "reserved" as const }
  let available = false
  await page.route("**/api/v1/network/ipam/", (route) =>
    json(route, { ...fixture, reservations: available ? [row] : [] }),
  )
  await page.route("**/api/v1/docker/networks/", (route) => json(route, []))
  await page.goto(`/docker/networks?ipamReservation=${row.id}`)
  const dialog = page.getByRole("dialog", { name: "Create network" })
  await expect(dialog.getByRole("alert").filter({ hasText: "reservation is absent" })).toBeVisible()
  await expect(dialog.getByRole("button", { name: "Create", exact: true })).toBeDisabled()
  available = true
  await page.clock.runFor(30_100)
  await expect(dialog.getByLabel("Name", { exact: true })).toHaveValue(row.resource)
  await expect(dialog.getByLabel("IPv6 subnet (optional)")).toHaveValue(row.prefix)
  await expect(dialog.getByLabel("Shared IPv6 reservation")).toContainText(row.resource)
})

test("malformed later rows retain a selected Docker draft and block its handoff", async ({
  page,
}) => {
  await page.clock.install()
  await setup(page)
  const row = { ...fixture.reservations[0], state: "reserved" as const }
  let malformed = false
  const errors: string[] = []
  page.on("pageerror", (error) => errors.push(error.message))
  await page.route("**/api/v1/network/ipam/", (route) =>
    json(route, { ...fixture, reservations: malformed ? [null] : [row] }),
  )
  await page.route("**/api/v1/docker/networks/", (route) => json(route, []))
  await page.goto(`/docker/networks?ipamReservation=${row.id}`)
  const dialog = page.getByRole("dialog", { name: "Create network" })
  await expect(dialog.getByLabel("IPv6 subnet (optional)")).toHaveValue(row.prefix)
  malformed = true
  await page.clock.runFor(30_100)
  await expect(
    dialog.getByRole("alert").filter({ hasText: "inventory is unavailable" }),
  ).toBeVisible()
  await expect(dialog.getByLabel("Name", { exact: true })).toHaveValue(row.resource)
  await expect(dialog.getByLabel("IPv6 subnet (optional)")).toHaveValue(row.prefix)
  await expect(dialog.getByRole("button", { name: "Create", exact: true })).toBeDisabled()
  expect(errors).toEqual([])
})

test("closing and reopening retains an explicitly detached Docker draft", async ({ page }) => {
  await setup(page)
  const row = { ...fixture.reservations[0], state: "reserved" as const }
  await page.route("**/api/v1/network/ipam/", (route) =>
    json(route, { ...fixture, reservations: [row] }),
  )
  await page.route("**/api/v1/docker/networks/", (route) => json(route, []))
  await page.goto(`/docker/networks?ipamReservation=${row.id}`)
  const dialog = page.getByRole("dialog", { name: "Create network" })
  await expect(dialog.getByLabel("IPv6 subnet (optional)")).toHaveValue(row.prefix)
  await dialog.getByLabel("IPv6 subnet (optional)").fill("fd49:abcd::/64")
  await dialog.getByRole("button", { name: "Cancel", exact: true }).click()
  await page.getByRole("button", { name: "Create network", exact: true }).click()
  await expect(dialog.getByLabel("IPv6 subnet (optional)")).toHaveValue("fd49:abcd::/64")
  await expect(dialog.getByLabel("Shared IPv6 reservation")).toContainText("No selected plan")
  await expect(dialog.getByRole("button", { name: "Create", exact: true })).toBeEnabled()
})

test("a new seed identity can initialize after the earlier one was detached", async ({ page }) => {
  await setup(page)
  const first = { ...fixture.reservations[0], state: "reserved" as const }
  const next = {
    ...first,
    id: "c".repeat(32),
    prefix: "fd48:abcd:1::/64",
    resource: "next-private",
  }
  await page.route("**/api/v1/network/ipam/", (route) =>
    json(route, { ...fixture, reservations: [first, next] }),
  )
  await page.route("**/api/v1/docker/networks/", (route) => json(route, []))
  await page.goto(`/docker/networks?ipamReservation=${first.id}`)
  const dialog = page.getByRole("dialog", { name: "Create network" })
  await expect(dialog.getByLabel("Name", { exact: true })).toHaveValue(first.resource)
  await dialog.getByLabel("Name", { exact: true }).fill("manual-private")
  await page.evaluate(
    (id) => window.history.pushState(null, "", `/docker/networks?ipamReservation=${id}`),
    next.id,
  )
  await expect(dialog.getByLabel("Name", { exact: true })).toHaveValue(next.resource)
  await expect(dialog.getByLabel("IPv6 subnet (optional)")).toHaveValue(next.prefix)
})

test("an edited WireGuard seed stays detached when the original becomes held", async ({ page }) => {
  await page.clock.install()
  await setup(page)
  const row = {
    ...fixture.reservations[0],
    owner: "wireguard_server" as const,
    resource: "wg-ipam",
    state: "reserved" as const,
  }
  let held = false
  await page.route("**/api/v1/network/ipam/", (route) =>
    json(route, {
      ...fixture,
      reservations: [{ ...row, state: held ? "review_required" : "reserved" }],
    }),
  )
  const calls: Record<string, unknown>[] = []
  await page.route("**/api/v1/network/vpn/wireguard", (route) => {
    calls.push(route.request().postDataJSON())
    return json(route, {
      interface: { name: "wg-manual", listenPort: 51820 },
      warnings: [],
      firewall: { opened: false },
    })
  })
  await page.goto(`/network/vpn?ipamReservation=${row.id}`)
  const dialog = page.getByRole("dialog", { name: "Create WireGuard tunnel" })
  await expect(dialog.getByLabel("IPv6 tunnel network", { exact: true })).toHaveValue(row.prefix)
  await dialog.getByLabel("Tunnel name").fill("wg-manual")
  await dialog.getByLabel("IPv6 tunnel network", { exact: true }).fill("fd49:abcd::/64")
  held = true
  await page.clock.runFor(30_100)
  await expect(dialog.getByRole("button", { name: "Set up WireGuard", exact: true })).toBeEnabled()
  await dialog.getByRole("button", { name: "Set up WireGuard", exact: true }).click()
  await expect.poll(() => calls.length).toBe(1)
  expect(calls[0]).toMatchObject({
    name: "wg-manual",
    ipv6: { subnet: "fd49:abcd::/64", exitNode: false },
  })
  expect(calls[0]).not.toHaveProperty("ipamReservationIds")
})

for (const later of ["missing", "error"] as const) {
  test(`a selected Docker reservation still blocks after a later ${later} read`, async ({
    page,
  }) => {
    await page.clock.install()
    await setup(page)
    const row = { ...fixture.reservations[0], state: "reserved" as const }
    let refreshed = false
    await page.route("**/api/v1/network/ipam/", (route) =>
      refreshed && later === "error"
        ? json(route, { error: { code: "unavailable", message: "Planning unavailable" } }, 500)
        : json(route, { ...fixture, reservations: refreshed ? [] : [row] }),
    )
    await page.route("**/api/v1/docker/networks/", (route) => json(route, []))
    await page.goto(`/docker/networks?ipamReservation=${row.id}`)
    const dialog = page.getByRole("dialog", { name: "Create network" })
    await expect(dialog.getByLabel("IPv6 subnet (optional)")).toHaveValue(row.prefix)
    refreshed = true
    await page.clock.runFor(30_100)
    await expect(
      dialog.getByRole("alert").filter({
        hasText: later === "error" ? "inventory is unavailable" : "reservation is absent",
      }),
    ).toBeVisible()
    await expect(dialog.getByLabel("Name", { exact: true })).toHaveValue(row.resource)
    await expect(dialog.getByLabel("IPv6 subnet (optional)")).toHaveValue(row.prefix)
    await expect(dialog.getByRole("button", { name: "Create", exact: true })).toBeDisabled()
  })
}

test("a creation link initializes after delayed admin authentication", async ({ page }) => {
  await setup(page)
  const row = { ...fixture.reservations[0], state: "reserved" as const }
  let release!: () => void
  const pending = new Promise<void>((resolve) => {
    release = resolve
  })
  await page.route("**/api/v1/auth/session", async (route) => {
    await pending
    return json(route, admin)
  })
  let privateReads = 0
  await page.route("**/api/v1/network/ipam/", (route) => {
    privateReads++
    return json(route, { ...fixture, reservations: [row] })
  })
  await page.route("**/api/v1/docker/networks/", (route) => json(route, []))
  await page.goto(`/docker/networks?ipamReservation=${row.id}`)
  expect(privateReads).toBe(0)
  release()
  const dialog = page.getByRole("dialog", { name: "Create network" })
  await expect(dialog.getByLabel("Name", { exact: true })).toHaveValue(row.resource)
  await expect(dialog.getByLabel("IPv6 subnet (optional)")).toHaveValue(row.prefix)
  await expect(dialog.getByRole("button", { name: "Create", exact: true })).toBeEnabled()
})
