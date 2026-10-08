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
      : {},
  )
  await page.route("**/api/v1/network/ipam/", (route) => json(route, fixture))
}
test("shows exact IPv6 counts and retains unresolved owner allocation", async ({ page }) => {
  await setup(page)
  await page.goto("/network/ipam")
  await expect(page.getByRole("heading", { name: "Shared address pools" })).toBeVisible()
  await expect(page.getByText("65536", { exact: true })).toBeVisible()
  await expect(page.getByText("Review required · allocation held", { exact: true })).toBeVisible()
  await expect(page.getByRole("link", { name: "Open creation form" })).toHaveCount(0)
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
  await expect(page.getByRole("alert")).toContainText("Native source unreadable")
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
