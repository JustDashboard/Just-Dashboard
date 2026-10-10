import { expect, test } from "@playwright/test"
import { json, mockNetwork, vpn, type Mutation } from "./network-fixture"

function dualVPN(ipv6Runtime = "verified") {
  const family = (subnet: string, uplink: string, runtime = "verified") => ({
    configured: true,
    subnet,
    runtime: "present",
    exit: {
      configured: true,
      interface: uplink,
      runtime,
      reason: runtime === "degraded" ? "IPv6 admission could not be verified." : undefined,
      capability: { writable: true, firewall: "iptables", docker: false },
    },
  })
  return {
    ...vpn,
    wireguard: {
      ...vpn.wireguard,
      interfaces: [
        {
          ...vpn.wireguard.interfaces[0],
          ipv6Enabled: true,
          addresses: ["10.8.0.1/24", "fd42:8::1/64"],
          endpointReachability: "not_tested",
          families: {
            ipv4: family("10.8.0.0/24", "wan4"),
            ipv6: family("fd42:8::/64", "wan6", ipv6Runtime),
          },
        },
      ],
    },
  }
}

for (const status of [409, 500]) {
  test(`a ${status} dual-stack creation preserves its opt-in and subnet draft`, async ({
    page,
  }) => {
    const mutations: Mutation[] = []
    await mockNetwork(page, mutations, {
      overrides: { "/network/vpn": { ...vpn, wireguard: { ...vpn.wireguard, interfaces: [] } } },
    })
    let failed = false
    await page.route("**/api/v1/network/vpn/wireguard", async (route) => {
      mutations.push({
        method: "POST",
        path: "/network/vpn/wireguard",
        body: route.request().postDataJSON(),
      })
      if (!failed) {
        failed = true
        return json(
          route,
          { error: { code: "guarded", message: "IPv6 exit could not be verified." } },
          status,
        )
      }
      return json(route, {
        interface: dualVPN().wireguard.interfaces[0],
        warnings: [],
        firewall: { opened: true },
      })
    })
    await page.goto("/network/vpn")
    const submit = page.getByRole("button", { name: "Set up WireGuard", exact: true })
    await expect(
      page.getByRole("switch", { name: "IPv6 addressing", exact: true }),
    ).not.toBeChecked()
    await page.getByRole("switch", { name: "IPv6 addressing", exact: true }).click()
    await page.getByLabel("IPv6 tunnel network", { exact: true }).fill("2001:db8::/64")
    await expect(submit).toBeDisabled()
    await page.getByLabel("IPv6 tunnel network", { exact: true }).fill("fd42:8::/64")
    await page.getByRole("switch", { name: "IPv6 exit", exact: true }).click()
    await submit.click()
    await expect(page.getByRole("alert").filter({ hasText: "could not be verified" })).toBeVisible()
    await expect(page.getByLabel("IPv6 tunnel network", { exact: true })).toHaveValue("fd42:8::/64")
    await expect(page.getByRole("switch", { name: "IPv6 exit", exact: true })).toBeChecked()
    await submit.click()
    await expect
      .poll(() => mutations.filter((m) => m.path === "/network/vpn/wireguard").length)
      .toBe(2)
    expect(mutations.at(-1)?.body).toMatchObject({
      exitNode: true,
      ipv6: { subnet: "fd42:8::/64", exitNode: true },
    })
  })
}

test("legacy creation keeps the IPv6 object absent", async ({ page }) => {
  const mutations: Mutation[] = []
  await mockNetwork(page, mutations, {
    overrides: { "/network/vpn": { ...vpn, wireguard: { ...vpn.wireguard, interfaces: [] } } },
  })
  await page.route("**/api/v1/network/vpn/wireguard", async (route) => {
    mutations.push({ method: "POST", path: "create", body: route.request().postDataJSON() })
    return json(route, {
      interface: vpn.wireguard.interfaces[0],
      warnings: [],
      firewall: { opened: true },
    })
  })
  await page.goto("/network/vpn")
  await page.getByRole("button", { name: "Set up WireGuard", exact: true }).click()
  await expect.poll(() => mutations.length).toBe(1)
  expect(mutations[0].body).not.toHaveProperty("ipv6")
})

test("family evidence stays independent and IPv6 withdrawal is confirmed", async ({ page }) => {
  const mutations: Mutation[] = []
  await mockNetwork(page, mutations, { overrides: { "/network/vpn": dualVPN("degraded") } })
  await page.goto("/network/vpn")
  const evidence = page.getByLabel("wg0 address family evidence", { exact: true })
  await expect(evidence).toContainText("Exit configured through wan4")
  await expect(evidence).toContainText("Rules verified")
  await expect(evidence).toContainText("Exit configured through wan6")
  await expect(evidence).toContainText("Degraded")
  await expect(evidence).toContainText("provider reachability")
  await page.getByRole("switch", { name: "wg0 IPv6 exit", exact: true }).click()
  expect(mutations).toEqual([])
  const dialog = page.getByRole("dialog")
  await expect(dialog).toContainText("IPv4 exit remains configured")
  await dialog.getByRole("button", { name: "Turn off IPv6", exact: true }).click()
  await expect.poll(() => mutations.at(-1)?.body).toEqual({ on: true, ipv6: false })
})

test("a later VPN failure retains dated family evidence without a live halo", async ({ page }) => {
  await mockNetwork(page, [], { overrides: { "/network/vpn": dualVPN() } })
  await page.goto("/network/vpn")
  await expect(page.getByLabel("wg0 address family evidence", { exact: true })).toBeVisible()
  await page.route("**/api/v1/network/vpn", (route) =>
    json(
      route,
      { error: { code: "unavailable", message: "VPN state unavailable", retryable: true } },
      503,
    ),
  )
  await expect(page.getByText("Showing the last known network state", { exact: true })).toBeVisible(
    {
      timeout: 15_000,
    },
  )
  await expect(
    page.getByRole("alert").filter({ hasText: "Last successful read" }).locator("time"),
  ).toBeVisible()
  await expect(page.getByLabel("wg0 address family evidence", { exact: true })).toContainText(
    "wan6",
  )
  await expect(
    page.getByLabel("wg0 address family evidence", { exact: true }).locator(".animate-breathe"),
  ).toHaveCount(0)
})
