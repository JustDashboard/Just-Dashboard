import { expect, test } from "bun:test"
import { addressFamily, decisionRules, decisionTables, inferredAnswer } from "./decision-reading"

const route = (family, device) => ({ family, device, destination: "default" })
const rule = (family, priority, table, extra = {}) => ({
  family,
  priority,
  table,
  action: "lookup",
  ...extra,
})
const routing = {
  clientPath: { address: "2001:db8::9", source: "2001:db8::1", device: "eth0" },
  tables: [
    { id: 254, name: "main", routes: [route("inet", "tailscale0"), route("inet6", "eth0")] },
    { id: 52, name: "tailscale", routes: [route("inet", "tailscale0")] },
  ],
  rules: [rule("inet", 5270, 52), rule("inet6", 32766, 254), rule("inet6", 0, 255)],
}

test("IPv6 browser decisions show only IPv6 policy rules and routes", () => {
  const family = addressFamily(routing.clientPath.address)
  const rules = decisionRules(routing, family)
  expect(family).toBe("inet6")
  expect(rules.map((entry) => entry.priority)).toEqual([32766])
  expect(decisionTables(routing, rules, family)).toEqual([
    { id: 254, name: "main", routes: [route("inet6", "eth0")] },
  ])
  expect(inferredAnswer(routing, family)?.table).toBe(254)
  expect(inferredAnswer(routing, "inet")).toBeUndefined()
})

test("marked and interface-selected rules do not claim to answer browser replies", () => {
  const specific = {
    ...routing,
    rules: [
      rule("inet6", 10, 254, { fwmark: "0x1" }),
      rule("inet6", 20, 254, { iif: "eth0" }),
      rule("inet6", 30, 254, { oif: "eth0" }),
      rule("inet6", 32766, 254),
    ],
  }
  expect(inferredAnswer(specific, "inet6")?.rule.priority).toBe(32766)
})

test("empty policy tables remain visible for the selected family", () => {
  expect(decisionTables(routing, [rule("inet6", 20, 52)], "inet6")[0].routes).toEqual([])
  expect(
    decisionTables(routing, [rule("inet6", 30, 100, { tableName: "lab" })], "inet6")[0],
  ).toEqual({ id: 100, name: "lab", routes: [] })
})
