import { expect, test } from "bun:test"
import {
  accessAdmits,
  accessLosses,
  accessUnknowns,
  deniedSources,
  findingsByRule,
  isAnywhere,
  openings,
  planOperations,
  refusesByDefault,
  rulePath,
} from "./firewall-reading"

const rule = (fields) => ({
  action: "ALLOW",
  direction: "IN",
  from: "Anywhere",
  to: "",
  raw: "",
  ...fields,
})

test("every backend's spelling of anywhere is anywhere", () => {
  for (const from of ["", "Anywhere", "Anywhere (v6)", "0.0.0.0/0", "::/0", "any"])
    expect(isAnywhere(from)).toBe(true)
  expect(isAnywhere("100.64.0.0/10")).toBe(false)
})

test("openings fold a port's rules into one, and anyone wins over a named source", () => {
  const status = {
    rules: [
      rule({ number: 1, port: "22", from: "100.64.0.0/10", service: "SSH" }),
      rule({ number: 2, port: "22", from: "192.168.1.0/24" }),
      rule({ number: 3, port: "443", service: "HTTPS" }),
      rule({ number: 4, port: "443", from: "Anywhere (v6)", ipv6: true }),
      rule({ number: 5, port: "6379", danger: "Never open this to the world." }),
      rule({ number: 6, port: "6379", from: "10.0.0.0/8" }),
      rule({ number: 7, action: "DENY", from: "203.0.113.9" }),
      rule({ number: 8, port: "53", direction: "OUT" }),
      rule({ number: 9, action: "ACCEPT", direction: "INPUT", port: "80" }),
    ],
  }
  const byKey = Object.fromEntries(openings(status).map((o) => [o.key, o]))
  expect(openings(status).map((o) => o.key)).toEqual(["22", "443", "6379", "80"])
  expect(byKey["22"]).toMatchObject({
    anyone: false,
    sources: ["100.64.0.0/10", "192.168.1.0/24"],
    service: "SSH",
    rules: [1, 2],
  })
  expect(byKey["443"]).toMatchObject({ anyone: true, rules: [3] })
  expect(byKey["6379"]).toMatchObject({ anyone: true, danger: "Never open this to the world." })
  expect(byKey["80"].anyone).toBe(true)
  expect(deniedSources(status)).toEqual(["203.0.113.9"])
})

test("a default refuses when it denies, rejects or drops", () => {
  expect(refusesByDefault("deny")).toBe(true)
  expect(refusesByDefault("DROP")).toBe(true)
  expect(refusesByDefault("reject")).toBe(true)
  expect(refusesByDefault("allow")).toBe(false)
  expect(refusesByDefault(undefined)).toBe(false)
})

test("findings are keyed by rule identity", () => {
  const map = findingsByRule({
    findings: [
      { ruleId: "fw-b", number: 2, kind: "shadowed", byId: "fw-a", byNumber: 1, reason: "r" },
    ],
  })
  expect(map.get("fw-b")?.byNumber).toBe(1)
  expect(map.get("fw-a")).toBeUndefined()
})

test("a change loses only required access that was admitted before", () => {
  const check = (name, required, before, verdict) => ({
    name,
    required,
    before,
    verdict,
    port: 1,
    protocol: "tcp",
    family: "ipv4",
    source: "x",
    reason: "",
  })
  const losses = accessLosses([
    check("dashboard", true, "unfiltered", "refused"),
    check("ssh", true, "admitted", "admitted"),
    check("http", true, "refused", "refused"),
    check("extra", false, "admitted", "refused"),
    check("limited", true, "limited", "refused"),
  ])
  expect(losses.map((c) => c.name)).toEqual(["dashboard", "limited"])
  expect(accessUnknowns([check("a", true, "admitted", "unknown")])).toHaveLength(1)
  expect(accessAdmits("unfiltered")).toBe(true)
  expect(accessAdmits("unknown")).toBe(false)
})

test("staged changes become plan operations and rules are addressed by identity", () => {
  expect(
    planOperations([
      { op: "add", rule: { action: "allow", port: "22" }, label: "allow 22" },
      { op: "delete", ruleId: "fw-1", label: "rule 3" },
    ]),
  ).toEqual([
    { op: "add", rule: { action: "allow", port: "22" } },
    { op: "delete", ruleId: "fw-1" },
  ])
  expect(rulePath({ number: 3, id: "fw-0a1b", action: "ALLOW", from: "", to: "", raw: "" })).toBe(
    "/firewall/rules/3?id=fw-0a1b",
  )
  expect(rulePath({ number: 3, action: "ALLOW", from: "", to: "", raw: "" })).toBe(
    "/firewall/rules/3",
  )
})
