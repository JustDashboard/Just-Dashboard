import { expect, test, type Page, type Route } from "@playwright/test"
import { firewall, json, loaded, mockNetwork, routing, type Mutation } from "./network-fixture"

/**
 * The routing and host-firewall maturity work, against the Network section's
 * fixture host: kernel-backed reply decisions, target matches and observed
 * history, multipath routes and reviewed edits, discard previews, extended
 * policy rules with their effect preview, measured forwarding, BGP and OSPF
 * detail, routing change checks, and on the firewall: detection, preserved
 * access, preflight, identities, findings, plans, history and the owned
 * nftables table.
 */

async function answer(page: Page, path: string, body: unknown, seen?: unknown[]) {
  await page.route(`**/api/v1${path}`, async (route: Route) => {
    seen?.push(
      route.request().postDataJSON() ?? new URL(route.request().url()).searchParams.toString(),
    )
    await json(route, body)
  })
}

const managedRoute = routing.tables[0].routes.find((route) => route.managed)!

test("the reply decision names the kernel's table and the evaluated rule, and says when it cannot", async ({
  page,
}) => {
  await mockNetwork(page, [], {
    overrides: {
      "/network/routing": {
        ...routing,
        clientDecision: {
          basis: "kernel_and_model",
          table: 52,
          tableName: "tailscale",
          device: "tailscale0",
          rulePriority: 5270,
          candidates: [5270],
          steps: [],
        },
      },
    },
  })
  await page.goto("/network/routing")
  await loaded(page)
  await expect(page.getByText("5270 · your replies (evaluated)")).toBeVisible()
  await expect(page.getByText("table 52 · answers you (kernel)")).toBeVisible()
  await expect(
    page.getByText(/the lit rule is the one the evaluated rules reach it by/),
  ).toBeVisible()

  await mockNetwork(page, [], {
    overrides: {
      "/network/routing": {
        ...routing,
        clientDecision: {
          basis: "kernel",
          table: 52,
          candidates: [5270],
          reason: "Rule 10000 selects a socket owner and the packet's UID is not known.",
          steps: [],
        },
      },
    },
  })
  await page.reload()
  await loaded(page)
  await expect(page.getByText("table 52 · answers you (kernel)")).toBeVisible()
  await expect(page.getByText(/your replies \(/)).toHaveCount(0)
  await expect(page.getByText(/the rule is not named: .*UID is not known/)).toBeVisible()
})

test("a target narrows every table to its covering routes and the history to its changes", async ({
  page,
}) => {
  const historyQueries: string[] = []
  await mockNetwork(page)
  await page.route("**/api/v1/network/routing/history**", async (route) => {
    historyQueries.push(new URL(route.request().url()).search)
    const target = new URL(route.request().url()).searchParams.get("target")
    await json(route, {
      intervalSeconds: 30,
      running: true,
      lastReading: new Date().toISOString(),
      events: target
        ? []
        : [
            {
              id: 1,
              observedAt: new Date().toISOString(),
              previousAt: new Date(Date.now() - 30_000).toISOString(),
              object: "route",
              change: "removed",
              family: "inet",
              table: 254,
              tableName: "main",
              destination: "10.0.9.0/24",
              owner: "docker",
              managed: false,
              before: "10.0.9.0/24 dev br-old proto kernel",
            },
          ],
      limits: ["Readings are taken every 30 seconds."],
    })
  })
  await page.goto("/network/routing")
  await loaded(page)
  await expect(page.getByText("10.0.9.0/24 dev br-old proto kernel")).toBeVisible()
  await expect(page.getByText("Readings are taken every 30 seconds.")).toBeVisible()

  await page.getByRole("textbox", { name: "Target", exact: true }).fill("10.8.0.7")
  // main selects its /24; the office table, which only rule 10000 reaches, its default.
  await expect(page.getByText("selected for 10.8.0.7")).toHaveCount(2)
  await expect(page.getByText(/2 of \d+ cover 10\.8\.0\.7/)).toBeVisible()
  await expect(page.getByText("No route in this table covers 10.8.0.7.").first()).toBeVisible()
  await expect(page.getByText("No recorded change touches 10.8.0.7.")).toBeVisible()
  expect(historyQueries.some((q) => q.includes("target=10.8.0.7"))).toBe(true)
  await page.getByRole("textbox", { name: "Target", exact: true }).fill("not an address")
  await expect(page.getByText("Use an IPv4 or IPv6 address or network.")).toBeVisible()
})

test("a multipath route sends each leg, and a discard route is added only after its preview", async ({
  page,
}) => {
  const mutations: Mutation[] = []
  const previews: unknown[] = []
  await mockNetwork(page, mutations)
  await answer(
    page,
    "/network/routing/routes/preview",
    {
      family: "inet",
      table: 254,
      checkedAt: new Date().toISOString(),
      affected: [
        {
          kind: "docker",
          name: "the Docker network db",
          address: "10.0.4.1",
          before: "br-93e5e9c9442b (10.0.4.0/24, table main)",
          after: "discarded",
        },
      ],
      unknown: [],
      unaffected: 4,
      clientAffected: false,
      limits: ["Modeled as packets this host sends."],
    },
    previews,
  )
  await page.goto("/network/routing")
  await loaded(page)
  await page.getByRole("button", { name: "Add route", exact: true }).click()
  const dialog = page.getByRole("dialog")
  await dialog.getByRole("textbox", { name: "Destination" }).fill("10.6.0.0/24")
  await dialog.getByRole("switch", { name: "Spread over several hops" }).click()
  await dialog.getByRole("textbox", { name: "Hop 1 via" }).fill("10.8.0.2")
  await dialog.getByRole("textbox", { name: "Hop 2 via" }).fill("10.8.0.3")
  await dialog.getByRole("textbox", { name: "Weight" }).first().fill("2")
  await dialog.getByRole("button", { name: "Add route", exact: true }).click()
  await expect
    .poll(() => mutations.find((m) => m.path === "/network/routing/routes")?.body)
    .toMatchObject({
      destination: "10.6.0.0/24",
      nexthops: [{ gateway: "10.8.0.2", weight: 2 }, { gateway: "10.8.0.3" }],
    })

  await page.getByRole("button", { name: "Add route", exact: true }).click()
  const second = page.getByRole("dialog")
  await second.getByRole("textbox", { name: "Destination" }).fill("10.0.0.0/8")
  await second.getByRole("combobox", { name: "Kind" }).click()
  await page.getByRole("option", { name: "Drop it silently (blackhole)" }).click()
  const add = second.getByRole("button", { name: "Add route", exact: true })
  await expect(add).toBeDisabled()
  await expect(second.getByText(/added only after its effect has been checked/)).toBeVisible()
  await second.getByRole("button", { name: "Check effect", exact: true }).click()
  await expect(second.getByText("the Docker network db")).toBeVisible()
  await expect(second.getByText("discarded", { exact: true })).toBeVisible()
  expect(previews[0]).toMatchObject({ destination: "10.0.0.0/8", type: "blackhole" })
  await expect(add).toBeEnabled()
  await add.click()
  await expect
    .poll(() => mutations.filter((m) => m.path === "/network/routing/routes").at(-1)?.body)
    .toMatchObject({ destination: "10.0.0.0/8", type: "blackhole" })
})

test("a managed route is edited by applying exactly the plan that was reviewed", async ({
  page,
}) => {
  const mutations: Mutation[] = []
  await mockNetwork(page, mutations)
  await answer(page, `/network/routing/routes/${managedRoute.id}/plan`, {
    before: {
      id: managedRoute.id,
      family: "inet",
      destination: "192.168.1.0/24",
      type: "unicast",
      device: "wg0",
      table: 254,
    },
    after: {
      id: managedRoute.id,
      family: "inet",
      destination: "192.168.1.0/24",
      type: "unicast",
      gateway: "10.8.0.10",
      device: "wg0",
      table: 254,
    },
    replace: true,
    commands: ["ip route replace 192.168.1.0/24 via 10.8.0.10 dev wg0"],
    changes: ["via: none → 10.8.0.10"],
    impact: {
      family: "inet",
      table: 254,
      checkedAt: new Date().toISOString(),
      affected: [],
      unknown: [],
      unaffected: 6,
      clientAffected: false,
      limits: [],
    },
  })
  await page.goto("/network/routing")
  await loaded(page)
  await page.getByRole("button", { name: `Edit the route to ${managedRoute.destination}` }).click()
  const dialog = page.getByRole("dialog")
  await dialog.getByRole("textbox", { name: "Via" }).fill("10.8.0.10")
  const apply = dialog.getByRole("button", { name: "Apply plan", exact: true })
  await expect(apply).toBeDisabled()
  await dialog.getByRole("button", { name: "Review plan", exact: true }).click()
  await expect(dialog.getByText("replaced in place")).toBeVisible()
  await expect(
    dialog.getByText("ip route replace 192.168.1.0/24 via 10.8.0.10 dev wg0"),
  ).toBeVisible()
  await expect(dialog.getByText("via: none → 10.8.0.10")).toBeVisible()
  await apply.click()
  await expect
    .poll(
      () =>
        mutations.find(
          (m) => m.method === "PUT" && m.path === `/network/routing/routes/${managedRoute.id}`,
        )?.body,
    )
    .toMatchObject({ destination: managedRoute.destination, gateway: "10.8.0.10", device: "wg0" })
  // Changing a field after the review asks for another review.
})

test("a policy rule takes UID, TOS and goto selectors and previews its effect before it is added", async ({
  page,
}) => {
  const mutations: Mutation[] = []
  const previews: unknown[] = []
  await mockNetwork(page, mutations)
  await answer(
    page,
    "/network/routing/rules/preview",
    {
      rule: { priority: 10001, family: "inet" },
      checkedAt: new Date().toISOString(),
      after: { ...routing.rules[2] },
      before: { ...routing.rules[3] },
      shadowedBy: [
        {
          priority: 10000,
          owner: "just-dashboard",
          reason:
            "rule 10000 selects everything rule 10001 does and table office holds a route for all of it (default)",
        },
      ],
      shadows: [],
      impact: {
        family: "inet",
        table: 0,
        checkedAt: new Date().toISOString(),
        affected: [],
        unknown: [],
        unaffected: 5,
        clientAffected: false,
        limits: [],
      },
      probe: {
        tuple: { target: "198.51.100.7" },
        kernel: {
          address: "198.51.100.7",
          device: "ens3",
          gateway: "203.0.113.1",
          family: "inet",
          table: 254,
        },
        now: {
          status: "route",
          table: 254,
          tableName: "main",
          rulePriority: 32766,
          route: { ...routing.tables[0].routes[0] },
          steps: [],
        },
        with: {
          status: "discard",
          rulePriority: 10001,
          reason: "Rule 10001 discards it (prohibit).",
          steps: [],
        },
        agreement: "agrees",
        changes: true,
      },
    },
    previews,
  )
  await page.goto("/network/routing")
  await loaded(page)
  await page.getByRole("button", { name: "Add rule", exact: true }).click()
  const dialog = page.getByRole("dialog")
  await dialog.getByRole("textbox", { name: "From" }).fill("192.168.50.0/25")
  await dialog.getByRole("textbox", { name: "Socket owner (UID)" }).fill("1000-1999")
  await dialog.getByRole("textbox", { name: "TOS" }).fill("0x10")
  await dialog.getByRole("combobox", { name: "Then" }).click()
  await page.getByRole("option", { name: "Continue at a later rule (goto)" }).click()
  await dialog.getByRole("combobox", { name: "Continue at" }).click()
  await page.getByRole("option", { name: "10000", exact: true }).click()
  await dialog.getByText("Test a packet in the preview").click()
  await dialog.getByRole("textbox", { name: "Destination" }).fill("198.51.100.7")
  await dialog.getByRole("button", { name: "Preview effect", exact: true }).click()
  await expect(dialog.getByText(/Shadowed: rule 10000 selects everything/)).toBeVisible()
  await expect(dialog.getByText("The model's answer for now matches the kernel's.")).toBeVisible()
  await expect(dialog.getByText(/Model with this rule: Rule 10001 discards it/)).toBeVisible()
  expect(previews[0]).toMatchObject({
    uidRange: "1000-1999",
    tos: "0x10",
    action: "goto",
    goto: 10000,
    probe: { target: "198.51.100.7" },
  })
  await dialog.getByRole("button", { name: "Add rule", exact: true }).click()
  await expect
    .poll(() => mutations.find((m) => m.path === "/network/routing/rules")?.body)
    .toMatchObject({
      from: "192.168.50.0/25",
      uidRange: "1000-1999",
      tos: "0x10",
      action: "goto",
      goto: 10000,
    })
})

test("forwarding shows its measured health and how Docker was counted", async ({ page }) => {
  await mockNetwork(page, [], {
    overrides: {
      "/network/routing": {
        ...routing,
        forwarding: {
          ...routing.forwarding,
          ipv4: {
            ...routing.forwarding.ipv4,
            dockerBasis:
              "Counts all 5 Docker bridge networks: Docker's inventory does not say whether a network disabled IPv4.",
            health: {
              status: "forwarding",
              forwarded: 123456,
              ratePerSecond: 42.5,
              windowSeconds: 15,
              disabled: [],
              checkedAt: new Date().toISOString(),
            },
          },
          ipv6: {
            ...routing.forwarding.ipv6,
            health: {
              status: "partial",
              forwarded: 7,
              disabled: ["wg0"],
              checkedAt: new Date().toISOString(),
              reason: "Traffic arriving on wg0 is not forwarded: its own forwarding switch is off.",
            },
          },
        },
      },
    },
  })
  await page.goto("/network/routing")
  await loaded(page)
  await expect(page.getByText(/Forwarding traffic · 43 datagrams\/s over 15s/)).toBeVisible()
  await expect(page.getByText(/Counts all 5 Docker bridge networks/)).toBeVisible()
  await expect(page.getByText("Some devices do not forward")).toBeVisible()
  await expect(page.getByText(/Traffic arriving on wg0 is not forwarded/)).toBeVisible()
})

test("BGP shows each neighbour's filters, OSPF adjacencies and a bounded route browser", async ({
  page,
}) => {
  const reads: string[] = []
  await mockNetwork(page, [], {
    overrides: {
      "/network/bgp": {
        installed: true,
        running: true,
        readOnly:
          "Read from FRR. Neighbours, policies, OSPF areas and any failover behaviour are configured in FRR itself.",
        families: [
          {
            name: "ipv4Unicast",
            routerId: "192.0.2.1",
            localAs: 65000,
            peers: [
              {
                address: "192.0.2.2",
                remoteAs: 65001,
                state: "Established",
                uptimeSeconds: 93780,
                prefixesReceived: 4,
                prefixesSent: 2,
                messagesReceived: 1,
                messagesSent: 1,
                connectionsDropped: 0,
                policy: { routeMapIn: "EDGE-IN", prefixListOut: "OWN" },
              },
            ],
          },
        ],
        ospf: [
          {
            version: 2,
            routerId: "10.0.0.2",
            address: "192.0.2.2",
            interface: "eth1",
            state: "Full",
            role: "DR",
            priority: 1,
            uptimeSeconds: 61,
          },
        ],
      },
    },
  })
  await page.route("**/api/v1/network/bgp/routes**", async (route) => {
    reads.push(new URL(route.request().url()).search)
    await json(route, {
      family: "ipv4Unicast",
      total: 1,
      truncated: false,
      routes: [
        {
          prefix: "10.10.0.0/24",
          best: true,
          valid: true,
          multipath: false,
          nexthops: ["192.0.2.2"],
          path: "65001",
          weight: 0,
          localPref: 100,
          med: 5,
        },
      ],
    })
  })
  await page.goto("/network/routing")
  await loaded(page)
  await expect(page.getByText("route-map in EDGE-IN")).toBeVisible()
  await expect(page.getByText("prefix-list out OWN")).toBeVisible()
  await expect(page.getByText("Full · DR")).toBeVisible()
  await page.getByRole("button", { name: "Read routes", exact: true }).click()
  await expect(page.getByText("10.10.0.0/24")).toBeVisible()
  await expect(page.getByText("best", { exact: true })).toBeVisible()
  expect(reads[0]).toContain("family=ipv4Unicast")
  await expect(page.getByText(/configured in FRR itself/)).toBeVisible()
})

test("a pending routing change shows what it was checked against and the connections it moved", async ({
  page,
}) => {
  await mockNetwork(page, [], {
    overrides: {
      "/network/changes/current": {
        available: true,
        owned: true,
        change: {
          id: "c1",
          phase: "awaiting_confirmation",
          generation: "a".repeat(64),
          updatedAt: new Date().toISOString(),
          watchdog: "armed",
          runtime: "applied",
          persistence: "written",
          boot: "enabled",
          expiresAt: new Date(Date.now() + 80_000).toISOString(),
          validation: {
            checkedAt: new Date().toISOString(),
            client: true,
            sourceSelected: true,
            anchors: ["the internet", "the WireGuard endpoint 203.0.113.77 of a peer on wg0"],
            flows: 3,
            moved: [
              {
                address: "198.51.100.40",
                before: "through ens3 via 203.0.113.1",
                after: "through wg0 via 10.8.0.10",
              },
            ],
          },
        },
      },
    },
  })
  await page.goto("/network/routing")
  const checks = page.getByTestId("network-change-checks")
  await expect(checks).toContainText("the WireGuard endpoint 203.0.113.77 of a peer on wg0")
  await expect(checks).toContainText("1 of 3 established connections now leave differently")
  await expect(checks).toContainText("198.51.100.40")
})

const firewalldAccess = {
  backend: "firewalld",
  checks: [
    {
      name: "Your connection to the dashboard",
      port: 8443,
      protocol: "tcp",
      family: "ipv4",
      source: "100.110.34.9",
      interface: "tailscale0",
      required: true,
      verdict: "admitted",
      rule: 2,
      reason: "Rule 2 admits it in zone public.",
    },
    {
      name: "Public HTTPS ingress through Caddy",
      port: 443,
      protocol: "tcp",
      family: "ipv4",
      source: "Anywhere",
      required: true,
      verdict: "refused",
      reason: "No rule matches, and the inbound default is reject. (zone public)",
    },
  ],
}

const firewalld = {
  backend: "firewalld",
  available: true,
  enabled: true,
  zone: "public",
  defaultPolicy: "default (zone public)",
  policy: { incoming: "reject", outgoing: "allow", routed: "disabled" },
  logging: "unicast",
  capabilities: {
    editable: true,
    toggle: true,
    defaultPolicy: true,
    logging: true,
    reset: true,
    profiles: true,
  },
  rules: [
    {
      number: 1,
      id: "fw-000000000001",
      action: "ALLOW",
      direction: "IN",
      from: "Anywhere",
      to: "ssh",
      service: "ssh",
      zone: "public",
      raw: "service ssh",
    },
    {
      number: 2,
      id: "fw-000000000002",
      action: "ALLOW",
      direction: "IN",
      from: "Anywhere",
      to: "8443/tcp",
      port: "8443",
      protocol: "tcp",
      zone: "public",
      raw: "port 8443/tcp",
    },
  ],
  detection: [
    {
      backend: "ufw",
      installed: true,
      active: "inactive",
      selected: false,
      reason: "ufw is installed and inactive.",
    },
    {
      backend: "firewalld",
      installed: true,
      active: "active",
      selected: true,
      reason: "firewalld reports itself running.",
    },
    {
      backend: "iptables",
      installed: true,
      active: "not_checked",
      selected: false,
      reason: "firewalld is in charge, so this was not asked.",
    },
  ],
  zones: [
    {
      name: "public",
      default: true,
      target: "default",
      interfaces: ["ens3"],
      sources: [],
      rules: [],
    },
    {
      name: "trusted",
      default: false,
      target: "ACCEPT",
      interfaces: [],
      sources: ["100.64.0.0/10"],
      rules: [],
    },
  ],
  effective: {
    families: [
      {
        family: "ipv4",
        filtered: true,
        incoming: "reject",
        outgoing: "allow",
        routed: "disabled",
        reason: "firewalld applies each zone to both families.",
      },
      { family: "ipv6", filtered: true, incoming: "reject", outgoing: "allow", routed: "disabled" },
    ],
    interfaces: [{ interface: "ens3", zone: "public", incoming: "reject", rules: 2 }],
  },
  findings: [],
  analysis:
    "firewalld evaluates a zone's denials before its allowances regardless of listing order, so ordering findings are not computed.",
}

test("the firewall page says which firewall is in charge, its zones, policy by family and preserved access", async ({
  page,
}) => {
  await mockNetwork(page, [], {
    overrides: { "/firewall/": firewalld, "/firewall/access": firewalldAccess },
  })
  await page.goto("/network/firewall")
  await loaded(page)
  await expect(page.getByText("firewalld reports itself running.")).toBeVisible()
  await expect(page.getByText("in charge", { exact: true })).toBeVisible()
  await expect(page.getByText("firewalld is in charge, so this was not asked.")).toBeVisible()
  const zones = page.getByRole("region", { name: "Active zones" })
  await expect(zones.getByText("100.64.0.0/10")).toBeVisible()
  await expect(zones.getByText("ACCEPT")).toBeVisible()
  await expect(
    page
      .getByRole("region", { name: "Policy by family" })
      .getByText("firewalld applies each zone to both families."),
  ).toBeVisible()
  const access = page.getByRole("heading", { name: "Preserved access" })
  await expect(access).toBeVisible()
  await expect(page.getByText("Rule 2 admits it in zone public.")).toBeVisible()
  await expect(page.getByText(/inbound default is reject\. \(zone public\)/)).toBeVisible()
  await page.getByRole("button", { name: "Reset", exact: true }).click()
  await expect(page.getByRole("dialog")).toContainText("Reload zone public's shipped settings")
})

test("enabling shows the preflight's refusal before anything is sent, and sends nothing on cancel", async ({
  page,
}) => {
  const mutations: Mutation[] = []
  const preflights: string[] = []
  await mockNetwork(page, mutations, {
    overrides: { "/firewall/": { ...firewall, enabled: false } },
  })
  await page.route("**/api/v1/firewall/preflight?**", async (route) => {
    preflights.push(new URL(route.request().url()).search)
    await json(route, {
      backend: "ufw",
      findings: [],
      refusal:
        "this rule would cut off your own connection to the dashboard: Your connection to the dashboard (8443/tcp, ipv4 from 100.110.34.9) is unfiltered now and would be refused: No rule matches, and the inbound default is deny. Add a rule admitting it first.",
      checks: [
        {
          name: "Your connection to the dashboard",
          port: 8443,
          protocol: "tcp",
          family: "ipv4",
          source: "100.110.34.9",
          required: true,
          before: "unfiltered",
          verdict: "refused",
          reason: "No rule matches, and the inbound default is deny.",
        },
        {
          name: "SSH from your address",
          port: 22,
          protocol: "tcp",
          family: "ipv4",
          source: "100.110.34.9",
          required: true,
          before: "unfiltered",
          verdict: "admitted",
          rule: 1,
          reason: "Rule 1 admits it.",
        },
      ],
    })
  })
  await page.goto("/network/firewall")
  await loaded(page)
  await page.getByRole("switch", { name: "Firewall enabled" }).click()
  const dialog = page.getByRole("dialog")
  await expect(dialog.getByTestId("firewall-preflight")).toContainText(
    "The server will refuse this change",
  )
  await expect(dialog.getByText("1 required way in would be refused.")).toBeVisible()
  await expect(dialog.getByText("Rule 1 admits it.")).toBeVisible()
  expect(preflights[0]).toContain("op=enable")
  await dialog.getByRole("button", { name: "Cancel", exact: true }).click()
  expect(mutations).toEqual([])
})

test("rules carry their identity, findings and history, and removal names the identity", async ({
  page,
}) => {
  const mutations: Mutation[] = []
  const withIds = {
    ...firewall,
    rules: [
      { ...firewall.rules[0], id: "fw-0000000000a1" },
      { ...firewall.rules[1], id: "fw-0000000000a2" },
      {
        number: 3,
        id: "fw-0000000000a3",
        action: "DENY",
        direction: "IN",
        from: "203.0.113.9",
        to: "22/tcp",
        port: "22",
        protocol: "tcp",
        raw: "22/tcp DENY IN 203.0.113.9",
      },
    ],
    findings: [
      {
        ruleId: "fw-0000000000a3",
        number: 3,
        kind: "shadowed",
        byId: "fw-0000000000a1",
        byNumber: 1,
        reason:
          "Rule 1 (allow) is read first and decides everything rule 3 selects, so rule 3 never applies.",
      },
    ],
  }
  await mockNetwork(page, mutations, { overrides: { "/firewall/": withIds } })
  await page.route("**/api/v1/firewall/preflight?**", (route) =>
    json(route, { backend: "ufw", findings: [], checks: [] }),
  )
  await page.goto("/network/firewall")
  await loaded(page)
  const finding = page.getByTestId("rule-finding")
  await expect(finding).toHaveText(/shadowed by #1/i)
  await expect(finding).toHaveAttribute("title", /never applies/)
  await expect(page.getByText("Recent firewall changes")).toBeVisible()
  await page.getByRole("button", { name: "Rule history" }).first().click()
  const sheet = page.getByRole("dialog")
  await expect(
    sheet.getByText("Only changes made from this dashboard are recorded", { exact: false }),
  ).toBeVisible()
  await page.keyboard.press("Escape")
  await page
    .getByRole("row")
    .filter({ hasText: "203.0.113.9" })
    .getByRole("button", { name: "Delete rule" })
    .click()
  await expect(
    page.getByRole("dialog").getByText("Every required way in stays admitted", { exact: false }),
  ).toBeVisible()
  await page.getByRole("dialog").getByRole("button", { name: "Delete", exact: true }).click()
  await expect
    .poll(() => mutations.find((m) => m.method === "DELETE")?.path)
    .toBe("/firewall/rules/3")
  // The query keeps the identity; the fixture records the path alone.
})

test("several rule changes are staged, reviewed and applied as one plan", async ({ page }) => {
  const mutations: Mutation[] = []
  const withIds = {
    ...firewall,
    rules: firewall.rules.map((rule, i) => ({ ...rule, id: `fw-00000000000${i + 1}` })),
  }
  await mockNetwork(page, mutations, { overrides: { "/firewall/": withIds } })
  await page.route("**/api/v1/firewall/plans/preview", (route) =>
    json(route, {
      backend: "ufw",
      findings: [],
      checks: [],
      steps: [
        { op: "add", description: "Add: allow in 6379/tcp from 10.0.0.0/8", outcome: "pending" },
        {
          op: "delete",
          ruleId: "fw-000000000003",
          number: 3,
          description: "Remove rule 3: 51820/udp ALLOW IN Anywhere",
          outcome: "pending",
        },
      ],
    }),
  )
  await page.goto("/network/firewall")
  await loaded(page)
  await page.getByRole("button", { name: "Add rule", exact: true }).click()
  const dialog = page.getByRole("dialog")
  await dialog.getByRole("textbox", { name: "Port, range or list" }).fill("6379")
  await dialog.getByRole("button", { name: "Use Private network", exact: true }).click()
  await dialog.getByRole("button", { name: "Add to plan", exact: true }).click()
  await page
    .getByRole("row")
    .filter({ hasText: "51820/udp" })
    .getByRole("button", { name: "Remove in a plan" })
    .click()
  const staged = page.getByRole("list", { name: "Staged changes" })
  await expect(staged.getByRole("listitem")).toHaveCount(2)
  await expect(page.getByRole("button", { name: "Apply plan", exact: true })).toBeDisabled()
  await page.getByRole("button", { name: "Review plan", exact: true }).click()
  await expect(page.getByTestId("firewall-plan-review")).toContainText("Remove rule 3")
  await page.getByRole("button", { name: "Apply plan", exact: true }).click()
  await expect
    .poll(() => mutations.find((m) => m.path === "/firewall/plans")?.body)
    .toEqual({
      operations: [
        {
          op: "add",
          rule: expect.objectContaining({ action: "allow", port: "6379", from: "10.0.0.0/8" }),
        },
        { op: "delete", ruleId: "fw-000000000003" },
      ],
    })
  expect(mutations.some((m) => m.path === "/firewall/rules")).toBe(false)
})

test("an inactive ufw's configured rules and the owned nftables table say what is enforced", async ({
  page,
}) => {
  const configured = {
    ...firewall,
    enabled: false,
    rulesFrom: "configured",
    rules: firewall.rules.map((rule) => ({ ...rule, number: undefined })),
  }
  await mockNetwork(page, [], { overrides: { "/firewall/": configured } })
  await page.goto("/network/firewall")
  await loaded(page)
  await expect(page.getByText("ufw is not enforcing these rules")).toBeVisible()
  await expect(page.getByText("configured · IN").first()).toBeVisible()
  await expect(page.getByRole("button", { name: "Edit rule" })).toHaveCount(0)

  await mockNetwork(page, [], {
    overrides: {
      "/firewall/": {
        ...firewall,
        backend: "nftables",
        defaultPolicy: "policy drop (table inet jd_firewall)",
        policy: { incoming: "deny" },
        logging: undefined,
        capabilities: {
          editable: true,
          toggle: true,
          defaultPolicy: true,
          logging: false,
          reset: true,
          profiles: false,
        },
        foreign: ["ip filter", "inet jd_gateway"],
        detection: [
          {
            backend: "ufw",
            installed: false,
            active: "inactive",
            selected: false,
            reason: "ufw is not installed on this host.",
          },
          {
            backend: "firewalld",
            installed: false,
            active: "inactive",
            selected: false,
            reason: "firewalld is not installed on this host.",
          },
          {
            backend: "nftables",
            installed: true,
            active: "active",
            selected: true,
            reason: "The dashboard's nftables table is switched on and loaded.",
          },
          {
            backend: "iptables",
            installed: true,
            active: "not_checked",
            selected: false,
            reason: "nftables is in charge, so this was not asked.",
          },
        ],
      },
    },
  })
  await page.reload()
  await loaded(page)
  await expect(page.getByText("The dashboard's own nftables table")).toBeVisible()
  await expect(
    page.getByText(/Rules in any other table are never adopted or changed/),
  ).toBeVisible()
  await expect(page.getByText("ip filter, inet jd_gateway")).toBeVisible()
})

test("with temporary apply on, a firewall change asks the host to recover it unless confirmed", async ({
  page,
}) => {
  const headers: (string | null)[] = []
  await mockNetwork(page, [], {
    overrides: { "/network/changes/current": { available: true, owned: false, change: null } },
  })
  await page.route("**/api/v1/firewall/policy", async (route) => {
    headers.push(route.request().headers()["x-jd-network-apply"] ?? null)
    await json(route, { output: "ok" })
  })
  await page.goto("/network/firewall")
  await loaded(page)
  await page.getByRole("combobox", { name: "Outbound default" }).click()
  await page.getByRole("option", { name: "deny", exact: true }).click()
  await expect.poll(() => headers.length).toBe(1)
  expect(headers[0]).toBe("pending")
})
