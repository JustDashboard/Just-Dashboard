import { expect, test } from "bun:test"
import {
  edgeRuleName,
  edgeRuleProblem,
  edgeRuleSummary,
  edgeRulesState,
  hitsOf,
  ingressLimitRequests,
  moveRule,
  newEdgeRule,
  refusedRuleIndex,
  savedEdgeRule,
} from "./edge-rules-model"

const block = (name, match) => ({ name, match, action: { kind: "block", status: 403 } })

test("a rule is saved in the one spelling the server keeps", () => {
  expect(
    savedEdgeRule({
      name: " bots ",
      match: {
        pathPrefix: "  ",
        methods: ["POST", "GET", "POST"],
        userAgent: " curl ",
        clientFrom: [" 10.0.0.0/8 ", "", "10.0.0.0/8"],
      },
      action: { kind: "block" },
    }),
  ).toEqual({
    name: "bots",
    match: { methods: ["GET", "POST"], userAgent: "curl", clientFrom: ["10.0.0.0/8"] },
    action: { kind: "block", status: 403 },
  })
  expect(
    savedEdgeRule({
      name: "old",
      match: { pathRegex: "^/old/" },
      action: { kind: "redirect", location: " /new ", code: 301 },
    }).action,
  ).toEqual({ kind: "redirect", location: "/new", code: 301 })
})

test("a rule's problems are named before the server is asked", () => {
  expect(edgeRuleProblem(block("Admin", { pathPrefix: "/a" }), [])).toContain("lower-case")
  expect(edgeRuleProblem(block("a", { pathPrefix: "/a" }), ["a"])).toContain("Another rule")
  expect(edgeRuleProblem(block("a", { pathPrefix: " " }), [])).toContain("Match a path")
  expect(edgeRuleProblem(block("a", { pathPrefix: "admin" }), [])).toContain("starts with /")
  expect(
    edgeRuleProblem(
      { name: "a", match: { pathPrefix: "/a" }, action: { kind: "status", status: 302 } },
      [],
    ),
  ).toContain("200 to 599")
  expect(
    edgeRuleProblem(
      {
        name: "a",
        match: { pathPrefix: "/a" },
        action: { kind: "redirect", location: "javascript:x", code: 302 },
      },
      [],
    ),
  ).toContain("redirect goes")
  expect(edgeRuleProblem(block("a", { userAgent: "curl" }), [])).toBeUndefined()
})

test("names, summaries, order and hits read the way the editor shows them", () => {
  expect(edgeRuleName("Block the Admin!")).toBe("block-the-admin")
  expect(newEdgeRule(["rule-1", "rule-2"]).name).toBe("rule-3")
  expect(newEdgeRule(["rule-2"]).name).toBe("rule-3")
  expect(
    edgeRuleSummary({
      name: "bots",
      match: { methods: ["GET"], userAgent: "curl", clientFrom: ["10.0.0.0/8"] },
      action: { kind: "status", status: 429 },
    }),
  ).toBe("GET · agent ~ curl · from 10.0.0.0/8 → 429")
  expect(edgeRuleSummary(block("a", { pathPrefix: "/admin" }))).toBe("/admin… → 403")
  const rules = [block("a", { pathPrefix: "/a" }), block("b", { pathPrefix: "/b" })]
  expect(moveRule(rules, 1, -1).map((rule) => rule.name)).toEqual(["b", "a"])
  expect(moveRule(rules, 0, -1)).toBe(rules)
  expect(hitsOf([{ value: "a", count: 4, errors: 0 }], "a")).toBe(4)
  expect(hitsOf(undefined, "a")).toBe(0)
  expect(edgeRulesState(rules, { requests: 10, per: "second" }, true)).toBe(
    "2 rules · 10 per second · CrowdSec",
  )
  expect(edgeRulesState([], undefined, false)).toBe("Off")
})

test("a refusal is read back to the rule it names", () => {
  const rules = [block("admin", { pathPrefix: "/a" }), block("Bad Name", { pathPrefix: "/b" })]
  expect(refusedRuleIndex("edge rule admin: a rule matches something", rules)).toBe(0)
  expect(refusedRuleIndex("edge rule 2: a name is lower-case", rules)).toBe(1)
  expect(refusedRuleIndex("at most 32 edge rules", rules)).toBe(-1)
})

// Caddy has no rate-limit module, so the Docker Caddy ingress is given the
// route's limit as the network gateway's: new connections per address, one
// limit on each web port (a gateway limit is one port or one range), refused
// at once. netx's TestTheIngressRateLimitIsMeteredPerSourceOnPublishedWebPorts
// posts these same bodies to the gateway.
test("the host-level limits carry the route's rate and burst per source, one per web port", () => {
  const limit = (port) => ({
    name: `Ingress rate limit ${port}: shop`,
    protocol: "tcp",
    ports: port,
    rate: 20,
    per: "second",
    burst: 40,
    perSource: true,
    maxConnections: 0,
    globalConnections: 0,
    action: "reject",
  })
  expect(ingressLimitRequests({ requests: 20, per: "second", burst: 40 }, "shop")).toEqual([
    limit("443"),
    limit("80"),
  ])
  const long = ingressLimitRequests({ requests: 1, per: "minute" }, "x".repeat(80))
  expect(long.map((request) => request.name.length)).toEqual([64, 64])
  expect(long[0].burst).toBe(0)
})
