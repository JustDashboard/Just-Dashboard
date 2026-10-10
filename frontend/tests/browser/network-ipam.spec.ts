import { expect, test, type Page } from "@playwright/test"
import { admin, json, mockNetwork } from "./network-fixture"
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
async function setup(page: Page, readonly = false) {
  await mockNetwork(
    page,
    [],
    readonly
      ? { session: { ...admin, capabilities: ["read"], user: { ...admin.user, role: "readonly" } } }
      : { overrides: { "/docker/ping": { available: true } } },
  )
  await page.route("**/api/v1/network/ipam/", (route) => json(route, fixture))
}
test("shows exact IPv6 counts and retains unresolved owner allocation", async ({ page }) => {
  await setup(page)
  await page.goto("/network/ipam")
  await expect(page.getByRole("heading", { name: "Shared address pools" })).toBeVisible()
  await expect(page.getByText("65536", { exact: true })).toBeVisible()
  await expect(page.getByText("Docker network · Review required · allocation held")).toBeVisible()
  await expect(page.getByRole("link", { name: /Open (Docker|WireGuard) creation/ })).toHaveCount(0)
  await expect(page.locator('[data-slot="page"]')).toHaveAttribute("data-register", "reading")
  await expect(page.locator('[data-slot="panel"]:not([data-plain])')).toHaveCount(0)
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
  await page.getByRole("button", { name: "Preview known overlap" }).click()
  await expect(page.getByTestId("ipam-preview")).toContainText("Known overlap")
  await expect(page.getByTestId("ipam-preview")).toContainText("wireguard · wg0")
  await page.getByRole("button", { name: /Coverage and ownership limits/ }).click()
  await expect(
    page.getByText("Provider allocations are not authoritative from this host"),
  ).toBeVisible()
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
  await expect(page.getByRole("button", { name: "Preview known overlap" })).toBeDisabled()
  expect(calls).toBe(0)
})
test("failed preview preserves the prefix", async ({ page }) => {
  await setup(page)
  await page.route("**/api/v1/network/ipam/preview", (route) =>
    json(route, { error: { code: "unavailable", message: "Native source unreadable" } }, 503),
  )
  await page.goto("/network/ipam")
  await page.getByLabel("Prefix to preview").fill("fd48:abcd:1::/64")
  await page.getByRole("button", { name: "Preview known overlap" }).click()
  await expect(
    page.getByRole("alert").filter({ hasText: "Native source unreadable" }),
  ).toBeVisible()
  await expect(page.getByLabel("Prefix to preview")).toHaveValue("fd48:abcd:1::/64")
})

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
