import { expect, test } from "@playwright/test"
import { admin, json, loaded, mockNetwork, type Mutation } from "./network-fixture"
import { dnsViewManaged, overrides as dnsOverrides, mockNetworkWrites } from "./network-dns-fixture"
import { gateway, overrides as gatewayOverrides } from "./network-gateway-fixture"

const overrides = {
  ...dnsOverrides,
  ...gatewayOverrides,
  "/network/traffic/live": { now: 0, series: {} },
}
const reader = { ...admin, capabilities: ["read"], user: { ...admin.user, role: "readonly" } }

test("read-only accounts see network state and cannot submit mutations", async ({ page }) => {
  const mutations: Mutation[] = []
  await mockNetwork(page, mutations, { overrides, session: reader })
  for (const [path, commands] of [
    ["interfaces", ["New device", "New namespace"]],
    ["routing", ["Add route", "Add rule"]],
    ["gateway", ["Forward a port", "Share a network"]],
    ["protection", ["New blocklist", "New limit"]],
    ["dns", ["Add record", "Resolve"]],
  ] as const) {
    await page.goto(`/network/${path}`)
    await loaded(page)
    for (const command of commands)
      await expect(page.getByRole("button", { name: command, exact: true })).toBeDisabled()
  }
  await page.goto("/network/routing")
  await expect(page.getByRole("switch", { name: "IPv4 forwarding", exact: true })).toBeDisabled()
  await page.goto("/network/traffic")
  await expect(page.getByRole("switch", { name: /BBR/ })).toBeDisabled()
  expect(mutations).toEqual([])
})

test("read-only VPN visits do not request administrator-only peer data", async ({ page }) => {
  let reads = 0
  await mockNetwork(page, [], { overrides, session: reader })
  await page.route("**/api/v1/network/vpn", async (route) => {
    reads++
    await json(route, {})
  })
  await page.goto("/network/vpn")
  await expect(page.getByText("VPN needs the admin capability")).toBeVisible()
  expect(reads).toBe(0)
})

for (const diagnostic of [
  { key: "route", label: "Route lookup", target: "2001:db8::1" },
  { key: "mtu", label: "Path MTU", target: "example.com" },
  { key: "capabilities", label: "Host support", target: "" },
  { key: "capture", label: "Packet snapshot", target: "eno1", option: "all" },
  { key: "wol", label: "Wake-on-LAN", target: "00:11:22:33:44:55", option: "eno1" },
]) {
  test(`${diagnostic.label} sends its real API parameters`, async ({ page }) => {
    const mutations: Mutation[] = []
    await mockNetwork(page, mutations)
    await page.route("**/api/v1/network/probe", async (route) => {
      mutations.push({
        method: "POST",
        path: "/network/probe",
        body: route.request().postDataJSON(),
      })
      await json(route, {
        tool: diagnostic.key,
        target: diagnostic.target,
        ok: true,
        output: "Diagnostic complete",
        duration: "1ms",
      })
    })
    await page.goto(`/network/tools?tool=${diagnostic.key}`)
    const panel = page.locator("[data-slot=security-tools]")
    if (diagnostic.target)
      await panel
        .getByRole("textbox", {
          name:
            diagnostic.key === "wol"
              ? "MAC address"
              : diagnostic.key === "capture"
                ? "Interface"
                : "Target",
          exact: true,
        })
        .fill(diagnostic.target)
    if (diagnostic.key === "wol")
      await page.getByLabel("LAN interface", { exact: true }).fill("eno1")
    await panel.getByRole("button", { name: "Run", exact: true }).click()
    await expect(page.getByText("Diagnostic complete", { exact: true })).toBeVisible()
    expect(mutations.at(-1)?.body).toEqual({
      tool: diagnostic.key,
      target: diagnostic.target,
      ...(diagnostic.option ? { option: diagnostic.option } : {}),
    })
    if (diagnostic.key === "wol")
      await expect(page.getByText("packet sent", { exact: true })).toBeVisible()
  })
}

test("a failed diagnostic keeps its error beside the inputs and can be retried", async ({
  page,
}) => {
  await mockNetwork(page)
  await page.route("**/api/v1/network/probe", (route) =>
    json(
      route,
      { error: { code: "tool_unavailable", message: "tracepath is not installed" } },
      503,
    ),
  )
  await page.goto("/network/tools?tool=mtu&target=example.com")
  await page.getByRole("button", { name: "Run", exact: true }).click()
  await expect(
    page.locator("[data-slot=security-tools]").getByText("tracepath is not installed"),
  ).toBeVisible()
  await expect(page.getByRole("button", { name: "Run", exact: true })).toBeEnabled()
})

test("namespaces and historical traffic show failed reads instead of empty results", async ({
  page,
}) => {
  await mockNetwork(page, [], { overrides })
  await page.route("**/api/v1/network/namespaces", (route) =>
    json(route, { error: { code: "unavailable", message: "Could not inspect namespaces" } }, 503),
  )
  await page.goto("/network/interfaces")
  await expect(page.getByText("Could not inspect namespaces", { exact: true })).toBeVisible()
  await page.route("**/api/v1/network/traffic/history?**", (route) =>
    json(route, { error: { code: "unavailable", message: "Could not read traffic history" } }, 503),
  )
  await page.goto("/network/traffic")
  await page.getByRole("radio", { name: "1h", exact: true }).click()
  await expect(page.getByText("Could not read traffic history", { exact: true })).toBeVisible()
})

test("DNS lookup keeps private names on configured resolvers until public comparison is selected", async ({
  page,
}) => {
  const mutations: Mutation[] = []
  await mockNetwork(page, mutations, { overrides })
  await mockNetworkWrites(page, mutations)
  await page.goto("/network/dns")
  await expect(page.getByRole("switch", { name: "Include public resolvers" })).not.toBeChecked()
  await page.getByLabel("Name", { exact: true }).fill("nas.home.arpa")
  await page.getByRole("button", { name: "Resolve", exact: true }).click()
  await expect
    .poll(() => mutations.find((entry) => entry.path === "/network/dns/lookup")?.body)
    .toEqual({ name: "nas.home.arpa", type: "A", includePublic: false })
})

test("turning off a WireGuard exit requires confirmation before sending its mutation", async ({
  page,
}) => {
  const mutations: Mutation[] = []
  await mockNetwork(page, mutations)
  await page.goto("/network/vpn")
  await page.getByRole("switch", { name: "wg0 as an exit node" }).click()
  await expect(page.getByRole("dialog")).toBeVisible()
  expect(mutations).toEqual([])
  await page.getByRole("button", { name: "Turn off", exact: true }).click()
  await expect
    .poll(() => mutations.find((entry) => entry.path.endsWith("/wg0/exit"))?.body)
    .toEqual({ on: false })
})

test("IPv6 policy rules carry the selected family to the backend", async ({ page }) => {
  const mutations: Mutation[] = []
  await mockNetwork(page, mutations)
  await page.goto("/network/routing")
  await page.getByRole("button", { name: "Add rule", exact: true }).click()
  await page.getByLabel("Address family").click()
  await page.getByRole("option", { name: "IPv6", exact: true }).click()
  await page.getByLabel("From", { exact: true }).fill("2001:db8::/64")
  await page.getByLabel("Table", { exact: true }).fill("100")
  await page.getByRole("dialog").getByRole("button", { name: "Add rule", exact: true }).click()
  await expect
    .poll(() => mutations.find((entry) => entry.path === "/network/routing/rules")?.body)
    .toMatchObject({ family: "inet6", from: "2001:db8::/64", table: 100 })
})

test("static routes send a validated preferred source and show returned sources", async ({
  page,
}) => {
  const mutations: Mutation[] = []
  await mockNetwork(page, mutations)
  await page.goto("/network/routing")
  await expect(page.getByText("source 10.0.0.1", { exact: true })).toBeVisible()
  await page.getByRole("button", { name: "Add route", exact: true }).first().click()
  const dialog = page.getByRole("dialog")
  await dialog.getByLabel("Destination", { exact: true }).fill("2001:db8:60::/64")
  await dialog.getByLabel("Via", { exact: true }).fill("2001:db8::2")
  await dialog.getByLabel("Preferred source", { exact: true }).fill("192.0.2.1")
  await expect(dialog.getByRole("button", { name: "Add route", exact: true })).toBeDisabled()
  await dialog.getByLabel("Preferred source", { exact: true }).fill(" 2001:db8::1 ")
  await dialog.getByRole("button", { name: "Add route", exact: true }).click()
  await expect
    .poll(() => mutations.at(-1)?.body)
    .toMatchObject({
      destination: "2001:db8:60::/64",
      gateway: "2001:db8::2",
      source: "2001:db8::1",
    })
})

test("an outgoing-interface policy rule sends its selector and note", async ({ page }) => {
  const mutations: Mutation[] = []
  await mockNetwork(page, mutations)
  await page.goto("/network/routing")
  await page.getByRole("button", { name: "Add rule", exact: true }).click()
  const dialog = page.getByRole("dialog")
  await dialog.getByLabel("Leaving on", { exact: true }).click()
  await page.getByRole("option", { name: "jd-lab", exact: true }).click()
  await dialog.getByLabel("Table", { exact: true }).fill("100")
  await dialog.getByLabel("Note", { exact: true }).fill("  Local lab replies  ")
  await dialog.getByRole("button", { name: "Add rule", exact: true }).click()
  await expect
    .poll(() => mutations.at(-1)?.body)
    .toMatchObject({
      family: "inet",
      oif: "jd-lab",
      table: 100,
      comment: "Local lab replies",
    })
})

test("IPv6 GRE creation reaches the existing endpoint with IPv6 ends", async ({ page }) => {
  const mutations: Mutation[] = []
  await mockNetwork(page, mutations)
  await page.goto("/network/interfaces")
  await page.getByRole("button", { name: "New device", exact: true }).click()
  const dialog = page.getByRole("dialog")
  await dialog.getByRole("button", { name: "Make a IPv6 GRE tunnel" }).click()
  await dialog.getByLabel("Remote end", { exact: true }).fill("2001:db8::7")
  await dialog.getByLabel("Local end", { exact: true }).fill("2001:db8::1")
  await dialog.getByRole("button", { name: /^Create gre6-/ }).click()
  await expect
    .poll(() => mutations.at(-1)?.body)
    .toMatchObject({ kind: "ip6gre", remote: "2001:db8::7", local: "2001:db8::1" })
})

test("invalid VXLAN ports cannot silently create a tunnel on the default port", async ({
  page,
}) => {
  const mutations: Mutation[] = []
  await mockNetwork(page, mutations)
  await page.goto("/network/interfaces")
  await page.getByRole("button", { name: "New device", exact: true }).click()
  const dialog = page.getByRole("dialog")
  await dialog.getByRole("button", { name: "Make a VXLAN" }).click()
  await dialog.getByLabel("VNI", { exact: true }).fill("42")
  await dialog.getByLabel("Remote end", { exact: true }).fill("198.51.100.7")
  const create = dialog.getByRole("button", { name: "Create vx42", exact: true })
  for (const port of ["bad", "0", "65536", ""]) {
    await dialog.getByLabel("UDP port", { exact: true }).fill(port)
    await expect(create).toBeDisabled()
    await expect(dialog.getByText("Use a whole port number from 1 to 65535.")).toBeVisible()
  }
  expect(mutations).toEqual([])
  await dialog.getByLabel("UDP port", { exact: true }).fill("65535")
  await create.click()
  await expect.poll(() => mutations.at(-1)?.body).toMatchObject({ kind: "vxlan", port: 65535 })
})

test("failed interface address writes retain the user's draft", async ({ page }) => {
  await mockNetwork(page)
  await page.route("**/api/v1/network/links/jd-lab/addresses", (route) =>
    json(
      route,
      { error: { code: "invalid_address", message: "The address is already in use" } },
      409,
    ),
  )
  await page.goto("/network/interfaces")
  await page.getByRole("button", { name: "Open jd-lab" }).click()
  const dialog = page.getByRole("dialog")
  await dialog.getByLabel("Address in CIDR form").fill("192.168.50.2/24")
  await dialog.getByRole("button", { name: "Add", exact: true }).click()
  await expect(page.getByText("The address is already in use")).toBeVisible()
  await expect(dialog.getByLabel("Address in CIDR form")).toHaveValue("192.168.50.2/24")
})

test("private DNS verification names accompany upstream changes", async ({ page }) => {
  const mutations: Mutation[] = []
  await mockNetwork(page, mutations, { overrides })
  await mockNetworkWrites(page, mutations)
  await page.goto("/network/dns")
  await page.getByLabel("Verification name", { exact: true }).fill("nas.home.arpa")
  await page.getByRole("button", { name: /^Quad9/ }).click()
  await page.getByRole("button", { name: "Apply", exact: true }).click()
  await page.getByRole("dialog").getByRole("button", { name: "Apply", exact: true }).click()
  await expect
    .poll(() => mutations.find((entry) => entry.path === "/network/dns/")?.body)
    .toMatchObject({ verificationName: "nas.home.arpa" })
})

test("DNS fallback endpoints and cache policy reach the confirmed resolver apply", async ({
  page,
}) => {
  const mutations: Mutation[] = []
  await mockNetwork(page, mutations, { overrides })
  await mockNetworkWrites(page, mutations)
  await page.goto("/network/dns")
  await page.getByLabel("Fallback servers", { exact: true }).fill("192.0.2.53")
  await page.getByRole("button", { name: "DNS over TLS required", exact: true }).click()
  await expect(page.getByRole("button", { name: "Apply", exact: true })).toBeDisabled()
  await page
    .getByLabel("Fallback servers", { exact: true })
    .fill("[fe80::53%ens3]:5353#resolver.home.arpa\n192.0.2.53#resolver.home.arpa")
  await page.getByLabel("Cache mode", { exact: true }).click()
  await page.getByRole("option", { name: "Positive answers only", exact: true }).click()
  await page.getByRole("button", { name: "Apply", exact: true }).click()
  expect(mutations).toEqual([])
  await page.getByRole("dialog").getByRole("button", { name: "Apply", exact: true }).click()
  await expect
    .poll(() => mutations.at(-1)?.body)
    .toMatchObject({
      fallback: ["[fe80::53%ens3]:5353#resolver.home.arpa", "192.0.2.53#resolver.home.arpa"],
      cache: "no-negative",
      dnsOverTLS: "yes",
    })
})

test("unrelated resolver changes preserve managed fallback and cache choices", async ({ page }) => {
  const mutations: Mutation[] = []
  const fallback = ["192.0.2.53#resolver.home.arpa"]
  await mockNetwork(page, mutations, {
    overrides: {
      ...overrides,
      "/network/dns/": {
        ...dnsViewManaged,
        managed: { ...dnsViewManaged.managed, fallback, cache: "no" },
      },
    },
  })
  await mockNetworkWrites(page, mutations)
  await page.goto("/network/dns")
  await expect(page.getByLabel("Fallback servers", { exact: true })).toHaveValue(fallback[0])
  await expect(page.getByLabel("Cache mode", { exact: true })).toHaveText("Disabled")
  await page.getByLabel("Domains", { exact: true }).fill("~home.arpa")
  await page.getByRole("button", { name: "Apply", exact: true }).click()
  await page.getByRole("dialog").getByRole("button", { name: "Apply", exact: true }).click()
  await expect.poll(() => mutations.at(-1)?.body).toMatchObject({ fallback, cache: "no" })
})

test("failed polling marks retained gateway readings until a fresh read succeeds", async ({
  page,
}) => {
  let fail = false
  await mockNetwork(page, [], { overrides })
  await page.route("**/api/v1/network/gateway", (route) =>
    fail
      ? json(route, { error: { code: "read_failed", message: "Gateway read failed" } }, 503)
      : json(route, gateway),
  )
  await page.goto("/network/gateway")
  await expect(page.getByText("Where traffic goes", { exact: true })).toBeVisible()
  fail = true
  await expect(page.getByText("Showing the last known network state", { exact: true })).toBeVisible(
    { timeout: 15_000 },
  )
  await expect(page.getByText("Gateway read failed", { exact: true })).toBeVisible()
  await expect(page.getByText("Where traffic goes", { exact: true })).toBeVisible()
  fail = false
  await page.getByRole("button", { name: "Refresh", exact: true }).click()
  await expect(page.getByText("Showing the last known network state", { exact: true })).toHaveCount(
    0,
  )
})
