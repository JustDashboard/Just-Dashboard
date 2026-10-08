import { expect, test, type Page } from "@playwright/test"
import { admin, json, mockNetwork } from "./network-fixture"

async function setup(page: Page, options: { role?: "admin" | "limited" | "read" } = {}) {
  const role = options.role ?? "admin"
  const session = {
    ...admin,
    capabilities:
      role === "read"
        ? ["read"]
        : role === "limited"
          ? ["read", "service.control"]
          : admin.capabilities,
    user: { ...admin.user, role: role === "read" ? "read-only" : role },
  }
  await mockNetwork(page, [], { session })
  await page.route("**/api/v1/docker/networks/", (route) => json(route, []))
  await page.goto("/docker/networks")
  if (role !== "read") {
    await page.getByRole("button", { name: "Create network", exact: true }).click()
    await expect(page.getByRole("dialog")).toBeVisible()
  }
}

for (const width of [390, 1440]) {
  test(`advanced network allocation keeps both families and metadata at ${width}px`, async ({
    page,
  }) => {
    await page.setViewportSize({ width, height: 900 })
    await setup(page)
    const dialog = page.getByRole("dialog")
    await dialog.getByLabel("Name", { exact: true }).fill("app-dual")
    await dialog.getByLabel("Subnet (optional)").fill("192.0.2.0/24")
    await dialog.getByRole("button", { name: "Advanced settings", exact: true }).click()
    await dialog.getByLabel("IPv4 gateway (optional)").fill("192.0.2.1")
    await dialog.getByLabel("IPv4 allocation range (optional)").fill("192.0.2.128/25")
    await dialog.getByRole("switch", { name: "Allow standalone containers to join" }).check()
    await dialog.getByRole("switch", { name: "Enable IPv6", exact: true }).check()
    await dialog.getByLabel("IPv6 subnet (optional)").fill("fd00:1::/64")
    await dialog.getByLabel("IPv6 gateway (optional)").fill("fd00:1::1")
    await dialog.getByLabel("IPv6 allocation range (optional)").fill("fd00:1::1000/116")
    await dialog.getByLabel("Labels", { exact: true }).fill("purpose=application")
    await dialog
      .getByLabel("Driver options", { exact: true })
      .fill("com.docker.network.driver.mtu=1400")
    let request: unknown
    await page.route("**/api/v1/docker/networks/", async (route) => {
      if (route.request().method() === "GET") return json(route, [])
      request = route.request().postDataJSON()
      return json(route, { id: "fixture" }, 201)
    })
    await expect(dialog.getByRole("button", { name: "Create", exact: true })).toBeEnabled()
    await expect(dialog).toHaveJSProperty(
      "scrollWidth",
      await dialog.evaluate((node) => node.clientWidth),
    )
    await dialog.getByRole("button", { name: "Create", exact: true }).click()
    await expect(dialog).not.toBeVisible()
    expect(request).toEqual({
      name: "app-dual",
      internal: false,
      attachable: true,
      ipv6: true,
      ipam: [
        { subnet: "192.0.2.0/24", gateway: "192.0.2.1", ipRange: "192.0.2.128/25" },
        { subnet: "fd00:1::/64", gateway: "fd00:1::1", ipRange: "fd00:1::1000/116" },
      ],
      labels: { purpose: "application" },
      options: { "com.docker.network.driver.mtu": "1400" },
    })
  })
}

test("an Engine refusal keeps the allocation draft for correction and retry", async ({ page }) => {
  await setup(page)
  const dialog = page.getByRole("dialog")
  await dialog.getByLabel("Name", { exact: true }).fill("app-private")
  await dialog.getByLabel("Subnet (optional)").fill("192.0.2.0/24")
  let attempts = 0
  await page.route("**/api/v1/docker/networks/", async (route) => {
    if (route.request().method() === "GET") return json(route, [])
    attempts++
    return attempts === 1
      ? json(route, { error: { code: "conflict", message: "allocation conflict" } }, 409)
      : json(route, { id: "fixture" }, 201)
  })
  await dialog.getByRole("button", { name: "Create", exact: true }).click()
  await expect(page.getByText("Could not create the network", { exact: true })).toBeVisible()
  await expect(dialog.getByLabel("Name", { exact: true })).toHaveValue("app-private")
  await expect(dialog.getByLabel("Subnet (optional)")).toHaveValue("192.0.2.0/24")
  await dialog.getByLabel("Subnet (optional)").fill("198.51.100.0/24")
  await dialog.getByRole("button", { name: "Create", exact: true }).click()
  await expect(dialog).not.toBeVisible()
  expect(attempts).toBe(2)
})

test("an unreadable refreshed inventory keeps the draft and requires a successful refresh", async ({
  page,
}) => {
  await page.clock.install()
  await setup(page)
  const dialog = page.getByRole("dialog")
  await dialog.getByLabel("Name", { exact: true }).fill("app-private")
  await page.route("**/api/v1/docker/networks/", (route) =>
    json(route, { error: { code: "unavailable", message: "daemon unavailable" } }, 503),
  )
  await page.clock.runFor(30_100)
  await expect(dialog.getByText(/The network inventory could not be refreshed/)).toBeVisible()
  await expect(dialog.getByLabel("Name", { exact: true })).toHaveValue("app-private")
  await expect(dialog.getByRole("button", { name: "Create", exact: true })).toBeDisabled()
  await page.route("**/api/v1/docker/networks/", (route) => json(route, []))
  await dialog.getByRole("button", { name: "Refresh network inventory", exact: true }).click()
  await expect(dialog.getByRole("button", { name: "Create", exact: true })).toBeEnabled()
})

test("limited operators use bridge settings and readers have no creation action", async ({
  page,
}) => {
  await setup(page, { role: "limited" })
  const dialog = page.getByRole("dialog")
  await dialog.getByRole("button", { name: "Advanced settings", exact: true }).click()
  await expect(dialog.getByLabel("Network driver", { exact: true })).toHaveCount(0)
  await expect(dialog.getByLabel("Driver options", { exact: true })).toHaveCount(0)
  await expect(
    dialog.getByText(/Other drivers and driver options need administrator/),
  ).toBeVisible()
  await dialog.getByRole("button", { name: "Cancel", exact: true }).click()
  await setup(page, { role: "read" })
  await expect(page.getByRole("button", { name: "Create network", exact: true })).toHaveCount(0)
})
