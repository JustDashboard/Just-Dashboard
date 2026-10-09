import { expect, test } from "bun:test"
import {
  nexthopProblem,
  parseTarget,
  replyHighlight,
  routeCovers,
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
