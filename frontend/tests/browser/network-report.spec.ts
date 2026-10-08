import { expect, test } from "@playwright/test"
import {
  admin,
  firewall,
  json,
  links,
  loaded,
  mockNetwork,
  overview,
  routing,
  vpn,
} from "./network-fixture"
import { overrides as traffic, processTraffic } from "./network-dns-fixture"

const reader = { ...admin, capabilities: ["read"], user: { ...admin.user, role: "readonly" } }

test("an IPv6-only browser sees IPv6 decisions and asks the kernel with read access", async ({
  page,
}) => {
  const ipv6 = {
    ...routing,
    clientPath: {
      address: "2001:db8::9",
      source: "2001:db8::1",
      device: "ens3",
      gateway: "fe80::1",
    },
    rules: [
      {
        id: 0,
        family: "inet6",
        priority: 32766,
        table: 254,
        tableName: "main",
        action: "lookup",
        owner: "system",
        managed: false,
      },
    ],
    tables: [
      {
        id: 254,
        name: "main",
        routes: [{ ...routing.tables[0].routes[0], family: "inet6", gateway: "fe80::1" }],
      },
    ],
  }
  const lookups: URL[] = []
  await mockNetwork(page, [], { session: reader, overrides: { "/network/routing": ipv6 } })
  await page.route("**/api/v1/network/routing/lookup?**", async (route) => {
    lookups.push(new URL(route.request().url()))
    await json(route, { ...ipv6.clientPath, family: "inet6", table: 254 })
  })
  await page.goto("/network/routing")
  await expect(page.getByText("32766 · your replies (inferred)")).toBeVisible()
  await expect(page.getByText("table 254 · answers you (inferred)")).toBeVisible()
  await expect(page.getByText(/Browser path \(IPv6\)/)).toContainText("2001:db8::9")
  await expect(page.getByText(/Highlights infer/)).toBeVisible()
  await page.getByRole("textbox", { name: "Target address", exact: true }).fill("2001:db8::9")
  await page.getByRole("textbox", { name: "Source address (optional)" }).fill("2001:db8::1")
  await page.getByRole("textbox", { name: "Packet mark (optional)" }).fill("0x80000")
  await page.getByRole("button", { name: "Look up route" }).click()
  await expect(
    page.getByRole("region", { name: "Kernel route lookup" }).getByText("254", { exact: true }),
  ).toBeVisible()
  expect(lookups[0].searchParams.get("target")).toBe("2001:db8::9")
  expect(lookups[0].searchParams.get("source")).toBe("2001:db8::1")
  expect(lookups[0].searchParams.get("mark")).toBe("0x80000")
  await page.getByRole("button", { name: "IPv4", exact: true }).first().click()
  await expect(page.getByText("32766 · your replies (inferred)")).toHaveCount(0)
})

for (const status of [409, 500]) {
  test(`a ${status} Tailscale offer keeps its draft and retries without duplicate routes`, async ({
    page,
  }) => {
    let offers = 0
    const preferences = structuredClone(vpn)
    const bodies: { advertiseRoutes: string[] }[] = []
    await mockNetwork(page)
    await page.route("**/api/v1/network/vpn", async (route) => json(route, preferences))
    await page.route("**/api/v1/network/vpn/tailscale", async (route) => {
      offers++
      bodies.push(route.request().postDataJSON())
      if (offers === 1)
        return json(
          route,
          { error: { code: "offer_refused", message: "Try the offer again", retryable: true } },
          status,
        )
      preferences.tailscale.prefs.advertiseRoutes = bodies.at(-1)!.advertiseRoutes
      await json(route, { note: "route offered" })
    })
    await page.goto("/network/vpn")
    const draft = page.getByRole("textbox", { name: "A network to offer" })
    await draft.fill("10.99.0.0/24")
    await page.getByRole("button", { name: "Offer it", exact: true }).click()
    await expect(page.getByRole("button", { name: "Retry offer" })).toBeVisible()
    await expect(draft).toHaveValue("10.99.0.0/24")
    // A lost response may have accepted the route. Retry rechecks preferences.
    if (status === 500) preferences.tailscale.prefs.advertiseRoutes.push("10.99.0.0/24")
    await page.getByRole("button", { name: "Retry offer" }).click()
    await expect(draft).toHaveValue("")
    expect(offers).toBe(status === 500 ? 1 : 2)
    expect(bodies[0].advertiseRoutes.filter((entry) => entry === "10.99.0.0/24")).toHaveLength(1)
  })
}

test("read users calculate IPv6 subnets without mounting privileged diagnostics", async ({
  page,
}) => {
  let probes = 0
  await mockNetwork(page, [], { session: reader })
  await page.route("**/api/v1/network/probe", async (route) => {
    probes++
    await json(route, {}, 403)
  })
  await page.goto("/network/tools?tool=ping&target=example.com")
  await page.getByRole("textbox", { name: "Address with a prefix" }).fill("2001:db8::1/64")
  await page.getByRole("button", { name: "Run", exact: true }).click()
  await expect(page.getByText("2001:db8::/64", { exact: true })).toBeVisible()
  await expect(page.getByRole("textbox", { name: "Target", exact: true })).toHaveCount(0)
  expect(probes).toBe(0)
})

for (const reading of [
  { path: "/network/traffic/processes", name: "program traffic", data: processTraffic },
  {
    path: "/network/traffic/containers",
    name: "container traffic",
    data: traffic["/network/traffic/containers"],
  },
  { path: "/network/links", name: "link inventory", data: links },
  { path: "/network/ebpf", name: "eBPF inventory", data: traffic["/network/ebpf"] },
]) {
  test(`a failed later ${reading.name} poll shows retained data, age and retry`, async ({
    page,
  }) => {
    let reads = 0
    await mockNetwork(page, [], {
      overrides: { ...traffic, "/network/traffic/live": { now: 0, series: {} } },
    })
    await page.route(`**/api/v1${reading.path}*`, async (route) => {
      reads++
      await json(
        route,
        reads === 2
          ? { error: { code: "reading_failed", message: "The host read failed", retryable: true } }
          : reading.data,
        reads === 2 ? 500 : 200,
      )
    })
    await page.clock.install()
    await page.goto("/network/traffic")
    await loaded(page)
    await expect.poll(() => reads).toBe(1)
    await page.clock.fastForward(reading.name === "program traffic" ? 3100 : 30100)
    const warning = page
      .getByRole("alert")
      .filter({ hasText: `Showing the last known ${reading.name}` })
    await expect(warning).toBeVisible()
    await expect(warning.getByText(/Last successful read:/)).toBeVisible()
    await expect(warning.locator("time")).toHaveAttribute("datetime", /T/)
    await warning.getByRole("button", { name: "Refresh", exact: true }).click()
    await expect(warning).toHaveCount(0)
    expect(reads).toBe(3)
  })
}

test("a failed second Firewall poll retains its rules and offers a dated retry", async ({
  page,
}) => {
  let reads = 0
  await mockNetwork(page)
  await page.route("**/api/v1/firewall/", async (route) => {
    reads++
    await json(
      route,
      reads === 2
        ? {
            error: {
              code: "firewall_failed",
              message: "Firewall could not be read",
              retryable: true,
            },
          }
        : firewall,
      reads === 2 ? 500 : 200,
    )
  })
  await page.clock.install()
  await page.goto("/network/firewall")
  await loaded(page)
  await page.clock.fastForward(30100)
  const warning = page
    .getByRole("alert")
    .filter({ hasText: "Showing the last known firewall state" })
  await expect(warning).toBeVisible()
  await expect(warning.getByText(/Last successful read:/)).toBeVisible()
  await expect(page.getByRole("switch", { name: "Firewall enabled" })).toBeVisible()
  await warning.getByRole("button", { name: "Refresh", exact: true }).click()
  await expect(warning).toHaveCount(0)
})

test("failed Docker candidate reads offer retry without claiming every container is attached", async ({
  page,
}) => {
  const network = {
    id: "lab",
    name: "lab",
    driver: "bridge",
    subnets: ["10.4.0.0/24"],
    gateways: ["10.4.0.1"],
    usedBy: [],
    internal: false,
    attachable: true,
    system: false,
    members: [],
    labels: {},
    options: {},
    enableIpv6: false,
    scope: "local",
  }
  let reads = 0
  await mockNetwork(page, [], {
    overrides: {
      "/docker/ping": { available: true },
      "/docker/networks/": [network],
      "/docker/networks/lab": network,
    },
  })
  await page.route("**/api/v1/docker/containers/", async (route) => {
    reads++
    await json(
      route,
      reads === 1
        ? {
            error: {
              code: "candidate_failed",
              message: "Could not list containers",
              retryable: true,
            },
          }
        : [],
      reads === 1 ? 500 : 200,
    )
  })
  await page.goto("/docker/networks")
  await page.getByRole("button", { name: "lab", exact: true }).click()
  await page.getByRole("button", { name: "Attach a container", exact: true }).click()
  const dialog = page.getByRole("dialog", { name: "Attach a container" })
  await expect(dialog.getByText("Could not list containers", { exact: true })).toBeVisible()
  await expect(dialog.getByText("Every container is already on this network.")).toHaveCount(0)
  await expect(dialog.getByRole("button", { name: "Attach", exact: true })).toBeDisabled()
  await dialog.getByRole("button", { name: "Try again", exact: true }).click()
  await expect(dialog.getByText("Every container is already on this network.")).toBeVisible()
})

test("a failed later Docker candidate poll keeps the selected container and alias for retry", async ({
  page,
}) => {
  const network = {
    id: "lab",
    name: "lab",
    driver: "bridge",
    subnets: ["10.4.0.0/24"],
    gateways: ["10.4.0.1"],
    usedBy: [],
    internal: false,
    attachable: true,
    system: false,
    members: [],
    labels: {},
    options: {},
    enableIpv6: false,
    scope: "local",
  }
  let reads = 0
  await mockNetwork(page, [], {
    overrides: {
      "/docker/ping": { available: true },
      "/docker/networks/": [network],
      "/docker/networks/lab": network,
    },
  })
  await page.route("**/api/v1/docker/containers/", async (route) => {
    reads++
    await json(
      route,
      reads === 2
        ? {
            error: {
              code: "candidate_failed",
              message: "Could not list containers",
              retryable: true,
            },
          }
        : [{ id: "db", name: "postgres" }],
      reads === 2 ? 500 : 200,
    )
  })
  await page.clock.install()
  await page.goto("/docker/networks")
  await page.getByRole("button", { name: "lab", exact: true }).click()
  await page.getByRole("button", { name: "Attach a container", exact: true }).click()
  const dialog = page.getByRole("dialog", { name: "Attach a container" })
  await dialog.getByRole("combobox").click()
  await page.getByRole("option", { name: "postgres" }).click()
  await dialog.getByRole("textbox").fill("database")
  await page.clock.fastForward(30100)
  const warning = dialog.getByRole("alert")
  await expect(warning).toContainText("Showing the last known container candidates")
  await expect(warning.getByText(/Last successful read:/)).toBeVisible()
  await expect(dialog.getByRole("button", { name: "Attach", exact: true })).toBeDisabled()
  await warning.getByRole("button", { name: "Refresh", exact: true }).click()
  await expect(dialog.getByRole("combobox")).toContainText("postgres")
  await expect(dialog.getByRole("textbox")).toHaveValue("database")
  await expect(dialog.getByRole("button", { name: "Attach", exact: true })).toBeEnabled()
})

for (const phase of ["saved", "boot_degraded", "unreadable"] as const) {
  test(`the overview reports ${phase} host changes without claiming connectivity confirmation`, async ({
    page,
  }) => {
    await mockNetwork(page, [], {
      overrides: {
        "/network/overview": {
          ...overview,
          persistence: {
            ...overview.persistence,
            change: {
              id: "abcdef",
              generation: "0123456789",
              updatedAt: "2026-10-08T10:00:00Z",
              phase,
              runtime: phase === "unreadable" ? "unknown" : "applied",
              persistence: "written",
              boot: phase === "boot_degraded" ? "failed" : "enabled",
              watchdog: phase === "unreadable" ? "unsupported" : "completed",
              recoveryErrors:
                phase === "unreadable" ? ["The recovery journal could not be read"] : [],
            },
          },
        },
      },
    })
    await page.goto("/network")
    const status = page.getByRole("region", { name: "Latest network change" })
    await expect(
      status.getByText(
        phase === "saved"
          ? "Saved"
          : phase === "boot_degraded"
            ? "Boot restoration failed"
            : "Change status unreadable",
        { exact: true },
      ),
    ).toBeVisible()
    await expect(
      status.getByText(/Browser reconnect and application reachability have not been confirmed/),
    ).toBeVisible()
    if (phase === "unreadable")
      await expect(status.getByRole("alert")).toContainText(
        "The recovery journal could not be read",
      )
    if (phase === "boot_degraded")
      await expect(
        status.getByText("Restore unit could not be enabled", { exact: true }),
      ).toBeVisible()
  })
}

test("an unreadable Tailscale preference refresh keeps its draft and sends no route replacement", async ({
  page,
}) => {
  let refreshFailed = false
  let offers = 0
  await mockNetwork(page)
  await page.route("**/api/v1/network/vpn", async (route) =>
    json(
      route,
      refreshFailed
        ? {
            ...vpn,
            tailscale: {
              ...vpn.tailscale,
              prefsReadable: false,
              warnings: ["The preferences could not be read: permission denied"],
              prefs: { ...vpn.tailscale.prefs, advertiseRoutes: [] },
            },
          }
        : vpn,
    ),
  )
  await page.route("**/api/v1/network/vpn/tailscale", async (route) => {
    offers++
    await json(route, { note: "offered" })
  })
  await page.goto("/network/vpn")
  const draft = page.getByRole("textbox", { name: "A network to offer" })
  await draft.fill("10.99.0.0/24")
  refreshFailed = true
  await page.getByRole("button", { name: "Offer it", exact: true }).click()
  await expect(page.getByRole("button", { name: "Retry offer" })).toBeVisible()
  await expect(draft).toHaveValue("10.99.0.0/24")
  expect(offers).toBe(0)
})
