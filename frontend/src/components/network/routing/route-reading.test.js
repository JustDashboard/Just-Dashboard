import { expect, test } from "bun:test"
import {
  askable,
  defaultRoute,
  forwardingNeeds,
  forwardingReading,
  forwardingState,
  needProduct,
  nexthopProblem,
  parseTarget,
  replyHighlight,
  routeCovers,
  routeRows,
  routeVia,
  ruleSelectors,
  ruleThen,
  targetMatches,
} from "./route-reading"

const route = (destination, extra = {}) => ({
  id: 0,
  family: destination.includes(":") ? "inet6" : "inet",
  destination,
  type: "unicast",
  protocol: "static",
  metric: 0,
  flags: [],
  nexthops: [],
  owner: "system",
  managed: false,
  ...extra,
})

test("a target is an address or a network of either family", () => {
  expect(parseTarget("10.0.0.5")).toEqual({ bytes: [10, 0, 0, 5], bits: 32, family: "inet" })
  expect(parseTarget("2001:db8::/48")?.bits).toBe(48)
  expect(parseTarget("2001:db8::/48")?.family).toBe("inet6")
  for (const bad of ["", "10.0.0.0/33", "host.example", "10.0.0.0/8/1", "10.0.0.0/x"])
    expect(parseTarget(bad)).toBeNull()
})

test("a route covers a target only when it holds all of it in the same family", () => {
  expect(routeCovers(route("10.0.0.0/8"), parseTarget("10.1.2.3"))).toBe(true)
  expect(routeCovers(route("10.0.0.0/8"), parseTarget("10.1.0.0/16"))).toBe(true)
  expect(routeCovers(route("10.1.0.0/16"), parseTarget("10.0.0.0/8"))).toBe(false)
  expect(routeCovers(route("default"), parseTarget("192.0.2.9"))).toBe(true)
  expect(routeCovers(route("default", { family: "inet6" }), parseTarget("192.0.2.9"))).toBe(false)
  expect(routeCovers(route("100.64.0.7"), parseTarget("100.64.0.7"))).toBe(true)
})

test("each table selects its most specific covering route, the lowest metric between equals", () => {
  const tables = [
    {
      id: 254,
      name: "main",
      routes: [
        route("default", { metric: 100 }),
        route("10.0.0.0/8", { metric: 50 }),
        route("10.0.0.0/8", { metric: 10, gateway: "10.9.9.9" }),
        route("10.2.0.0/16"),
      ],
    },
    { id: 100, name: "office", routes: [route("192.168.0.0/16")] },
  ]
  const matches = targetMatches(tables, parseTarget("10.1.2.3"))
  expect(matches.get(254)?.covering).toHaveLength(3)
  expect(matches.get(254)?.selected?.gateway).toBe("10.9.9.9")
  expect(matches.get(100)?.covering).toHaveLength(0)
  expect(matches.get(100)?.selected).toBeUndefined()
})

test("rules are written with every selector and action the kernel reports", () => {
  expect(
    ruleSelectors({
      family: "inet",
      priority: 1,
      not: true,
      from: "10.0.0.0/8",
      uidRange: "1000-1999",
      tos: "0x10",
      ipProto: "tcp",
      dport: "443",
      l3mdev: true,
    }),
  ).toEqual([
    "not",
    "from 10.0.0.0/8",
    "uidrange 1000-1999",
    "tos 0x10",
    "ipproto tcp",
    "dport 443",
    "l3mdev",
  ])
  expect(
    ruleThen({ action: "lookup", table: 254, tableName: "main", suppressPrefixLength: 0 }),
  ).toBe("look up main, ignoring answers /0 or shorter")
  expect(ruleThen({ action: "lookup", l3mdev: true })).toBe("look up its VRF's table")
  expect(ruleThen({ action: "goto", goto: 12000 })).toBe("continue at 12000")
  expect(ruleThen({ action: "goto", goto: 12000, unresolved: true })).toContain("passed over")
  expect(ruleThen({ action: "prohibit" })).toBe("prohibit")
})

test("the reply highlight prefers the kernel's table and names the rule only when evaluated to it", () => {
  const routing = {
    clientPath: { address: "100.110.34.9", device: "tailscale0" },
    tables: [
      { id: 52, name: "tailscale", routes: [route("100.110.34.9", { device: "tailscale0" })] },
    ],
    rules: [{ family: "inet", priority: 5270, action: "lookup", table: 52 }],
    clientDecision: {
      basis: "kernel_and_model",
      table: 52,
      rulePriority: 5270,
      candidates: [5270],
      steps: [],
    },
  }
  expect(replyHighlight(routing, "inet")).toMatchObject({
    basis: "kernel",
    rulePriority: 5270,
    table: 52,
  })
  expect(replyHighlight(routing, "inet6")).toBeUndefined()
  const undecided = {
    ...routing,
    clientDecision: { basis: "kernel", table: 52, candidates: [5270], reason: "UID", steps: [] },
  }
  expect(replyHighlight(undecided, "inet")).toMatchObject({
    basis: "table",
    rulePriority: undefined,
    table: 52,
  })
  const older = { ...routing, clientDecision: undefined }
  expect(replyHighlight(older, "inet")).toMatchObject({
    basis: "inferred",
    rulePriority: 5270,
    table: 52,
  })
})

test("a multipath draft is checked leg by leg", () => {
  const leg = (gateway, device = "", weight = "") => ({ gateway, device, weight })
  expect(nexthopProblem([leg("10.0.0.1")], "10.6.0.0/24")).toContain("at least two")
  expect(nexthopProblem([leg("10.0.0.1"), leg("")], "10.6.0.0/24")).toContain("Nexthop 2 needs")
  expect(nexthopProblem([leg("10.0.0.1"), leg("2001:db8::1")], "10.6.0.0/24")).toContain("family")
  expect(nexthopProblem([leg("10.0.0.1"), leg("10.0.0.1")], "10.6.0.0/24")).toContain("repeats")
  expect(nexthopProblem([leg("10.0.0.1", "", "300"), leg("10.0.0.2")], "10.6.0.0/24")).toContain(
    "weight",
  )
  expect(nexthopProblem([leg("10.0.0.1", "eth0", "2"), leg("", "eth1")], "default")).toBeUndefined()
  expect(
    routeVia(
      route("10.6.0.0/24", {
        nexthops: [
          { gateway: "10.0.0.1", weight: 2 },
          { device: "eth1", weight: 1 },
        ],
      }),
    ),
  ).toBe("10.0.0.1 ×2, eth1")
})

test("the page names itself by main's default route, IPv4 first and the lowest metric", () => {
  const routing = {
    tables: [
      { id: 100, name: "office", routes: [route("default", { gateway: "10.8.0.10" })] },
      {
        id: 254,
        name: "main",
        routes: [
          route("default", { family: "inet6", gateway: "fe80::1" }),
          route("default", { gateway: "203.0.113.9", metric: 200 }),
          route("default", { gateway: "203.0.113.1", metric: 100 }),
          route("default", { type: "blackhole" }),
        ],
      },
    ],
  }
  expect(defaultRoute(routing)?.gateway).toBe("203.0.113.1")
  const six = { tables: [{ id: 254, name: "main", routes: [routing.tables[1].routes[0]] }] }
  expect(defaultRoute(six)?.gateway).toBe("fe80::1")
  expect(defaultRoute({ tables: [{ id: 254, name: "main", routes: [] }] })).toBeUndefined()
  expect(defaultRoute({ tables: [] })).toBeUndefined()
})

test("every route is a row with its table, and two alike in one table keep apart", () => {
  const rows = routeRows([
    { id: 254, name: "main", routes: [route("10.0.0.0/8"), route("10.0.0.0/8")] },
    { id: 52, name: "tailscale", routes: [route("100.64.0.7", { device: "tailscale0" })] },
  ])
  expect(rows.map((row) => row.table.name)).toEqual(["main", "main", "tailscale"])
  expect(new Set(rows.map((row) => row.key)).size).toBe(3)
})

test("forwarding is said in one state for the page and one reading for a family", () => {
  const family = (extra = {}) => ({
    available: true,
    enabled: true,
    persisted: false,
    neededBy: [],
    ...extra,
  })
  const off = family({ enabled: false })
  expect(forwardingState({ ipv4: family(), ipv6: family() })).toEqual({
    on: true,
    label: "Forwarding IPv4 and IPv6",
  })
  expect(forwardingState({ ipv4: family(), ipv6: off }).label).toBe("Forwarding IPv4 only")
  expect(forwardingState({ ipv4: off, ipv6: family() }).label).toBe("Forwarding IPv6 only")
  expect(forwardingState({ ipv4: off, ipv6: off })).toEqual({ on: false, label: "Not forwarding" })

  expect(forwardingReading(family({ available: false }))).toMatchObject({
    tone: "unknown",
    label: "Not on this kernel",
  })
  expect(forwardingReading(off)).toMatchObject({ tone: "stopped", label: "Off" })
  expect(forwardingReading(family())).toMatchObject({ tone: "running", label: "On" })
  const health = (extra) => family({ health: { disabled: [], checkedAt: "", ...extra } })
  expect(
    forwardingReading(health({ status: "forwarding", ratePerSecond: 42.5, windowSeconds: 15 })),
  ).toEqual({
    tone: "running",
    word: "Forwarding traffic",
    rate: "43 datagrams/s over 15s",
    label: "Forwarding traffic · 43 datagrams/s over 15s",
  })
  expect(
    forwardingReading(health({ status: "idle", ratePerSecond: 0.25, windowSeconds: 15 })).label,
  ).toBe("On · idle · 0.3 datagrams/s over 15s")
  expect(forwardingReading(health({ status: "partial" }))).toMatchObject({
    tone: "warning",
    label: "Some devices do not forward",
  })
  expect(forwardingReading(health({ status: "unknown" })).label).toBe("On · not measured")
})

test("a thing that needs forwarding is listed once, with the families it needs", () => {
  const family = (neededBy) => ({ available: true, enabled: true, persisted: false, neededBy })
  expect(
    forwardingNeeds({
      ipv4: family(["5 Docker networks", "Tailscale's exit node"]),
      ipv6: family(["Tailscale's exit node"]),
    }),
  ).toEqual([
    { need: "5 Docker networks", ipv4: true, ipv6: false },
    { need: "Tailscale's exit node", ipv4: true, ipv6: true },
  ])
  expect(forwardingNeeds({ ipv4: family([]), ipv6: family([]) })).toEqual([])
})

test("a dependency is drawn with the product it names", () => {
  expect(needProduct("5 Docker networks")).toBe("docker")
  expect(needProduct("the WireGuard exit through wg0")).toBe("wireguard")
  expect(needProduct("Tailscale's exit node")).toBe("tailscale")
  expect(needProduct("a NAT rule")).toBeUndefined()
})

test("the kernel is asked about one literal address, never a network", () => {
  expect(askable("10.8.0.7", "")).toBe(true)
  expect(askable(" 2001:db8::1 ", " 2001:db8::2 ")).toBe(true)
  expect(askable("10.8.0.0/24", "")).toBe(false)
  expect(askable("10.8.0.7", "10.8.0.0/24")).toBe(false)
  expect(askable("10.8.0.7", "nope")).toBe(false)
  expect(askable("", "")).toBe(false)
  expect(askable("host.example", "")).toBe(false)
})
