import { expect, test } from "bun:test"
import {
  egressAdvice,
  egressAutomationWord,
  egressCarrying,
  egressHref,
  egressMemberLine,
  egressMemberState,
  egressNameOf,
  egressPolicyShort,
  egressRowLine,
  egressSibling,
  egressSimulationFact,
  egressTrafficLine,
  findEgressGroup,
} from "./reading"

const member = (fields) => ({
  id: 1,
  name: "provider-a",
  kind: "gateway",
  gateway: "203.0.113.1",
  device: "eth0",
  priority: 1,
  weight: 1,
  table: 7701,
  mark: "0x100/0x7f00",
  state: "up",
  proven: true,
  active: false,
  good: 5,
  bad: 0,
  history: [],
  ...fields,
})

const group = (fields) => ({
  id: 3,
  name: "providers",
  family: "inet",
  policy: { kind: "all" },
  sticky: true,
  enabled: true,
  automation: true,
  active: [2],
  members: [
    member({ id: 1, name: "provider-a", state: "down", proven: false }),
    member({ id: 2, name: "provider-b", active: true }),
  ],
  ...fields,
})

test("a group's address holds its name, encoded, and the view past the first", () => {
  expect(egressHref("providers")).toBe("/network/egress/providers")
  expect(egressHref("vpn clients", "decisions")).toBe("/network/egress/vpn%20clients?tab=decisions")
  expect(egressHref("providers", "members")).toBe("/network/egress/providers")
  expect(egressNameOf("vpn%20clients")).toBe("vpn clients")
  expect(egressNameOf("100%")).toBe("100%")
})

test("a name opens its group whatever its case, as the server refuses both", () => {
  const groups = [{ name: "Providers" }, { name: "vpn clients" }]
  expect(findEgressGroup(groups, "providers")?.name).toBe("Providers")
  expect(findEgressGroup(groups, "gone")).toBeUndefined()
})

test("the card's line says stickiness and how many members", () => {
  expect(egressRowLine(group({}))).toBe("sticky · 2 members")
  expect(egressRowLine(group({ sticky: false, family: "inet6", members: [member({})] }))).toBe(
    "IPv6 · not sticky · 1 member",
  )
})

test("the policy is a short title and one line under it", () => {
  expect(egressPolicyShort({ kind: "all" })).toEqual({
    title: "All traffic",
    hint: "to the default route",
    mono: false,
  })
  expect(egressPolicyShort({ kind: "selector", from: "10.8.0.0/24" })).toMatchObject({
    title: "10.8.0.0/24",
    hint: "as the source",
    mono: true,
  })
  expect(
    egressPolicyShort({ kind: "selector", from: "10.8.0.0/24", to: "192.0.2.0/24" }).hint,
  ).toBe("to 192.0.2.0/24")
  expect(egressPolicyShort({ kind: "selector", to: "192.0.2.0/24" })).toMatchObject({
    title: "192.0.2.0/24",
    hint: "as the destination",
  })
  expect(egressPolicyShort({ kind: "rule", fwmark: "0x1", iif: "wg0" })).toMatchObject({
    title: "mark 0x1",
    hint: "arriving on wg0",
  })
  expect(egressPolicyShort({ kind: "rule", iif: "wg0" }).title).toBe("wg0")
  // Every policy says one line under its title, so the node keeps its height.
  for (const policy of [{ kind: "all" }, { kind: "selector", to: "::/0" }, { kind: "rule" }])
    expect(egressPolicyShort(policy).hint).toBeTruthy()
})

test("the carrying member is the group's state, red when it is down", () => {
  expect(egressCarrying(group({}))).toMatchObject({ label: "Through provider-b", tone: "success" })
  expect(egressCarrying(group({ enabled: false })).label).toBe("Not carrying traffic")
  const failed = group({
    active: [1],
    members: [
      member({ id: 1, name: "provider-a", state: "down", active: true }),
      member({ id: 2, name: "provider-b" }),
    ],
  })
  expect(egressCarrying(failed)).toMatchObject({ label: "provider-a, down", tone: "danger" })
  const stale = group({
    members: [member({ id: 2, name: "provider-b", active: true, proven: false })],
  })
  expect(egressCarrying(stale).tone).toBe("warning")
})

test("automation is a word, and a group that is off switches nothing", () => {
  expect(egressAutomationWord({ enabled: true, automation: true })).toBe("Automatic")
  expect(egressAutomationWord({ enabled: true, automation: false })).toBe("By hand")
  expect(egressAutomationWord({ enabled: false, automation: false })).toBe("Off")
})

test("a member's state names its part, and nothing carries for a group that is off", () => {
  const on = { enabled: true }
  expect(egressMemberState(on, member({ active: true })).label).toBe("Carrying")
  expect(egressMemberState({ enabled: false }, member({ active: true })).label).toBe("Would carry")
  expect(egressMemberState(on, member({})).label).toBe("Standby")
  expect(egressMemberState(on, member({ state: "down" })).tone).toBe("danger")
  expect(egressMemberState(on, member({ proven: false })).tone).toBe("warning")
  expect(egressMemberState(on, member({ state: "unknown" })).label).toBe("Measuring")
})

test("only the carrying member's wire moves, and only while the group is on", () => {
  const g = group({})
  expect(egressMemberLine(g, g.members[1])).toEqual({ state: "up", moving: true })
  expect(egressMemberLine(g, g.members[0])).toEqual({ state: "down" })
  const off = group({ enabled: false })
  expect(egressMemberLine(off, off.members[1])).toEqual({ state: "idle" })
  const standby = group({ members: [member({ id: 1 }), member({ id: 2, active: true })] })
  expect(egressMemberLine(standby, standby.members[0])).toEqual({ state: "idle" })
  const failed = group({ members: [member({ id: 1, state: "down", active: true })] })
  expect(egressMemberLine(failed, failed.members[0])).toEqual({ state: "danger" })
  expect(egressTrafficLine(failed)).toEqual({ state: "danger" })
  expect(egressTrafficLine(g)).toEqual({ state: "up", moving: true })
  expect(egressTrafficLine(off)).toEqual({ state: "idle" })
})

test("advice is said only when the rules would act on a group that carries traffic", () => {
  const g = group({})
  expect(egressAdvice(g, { action: "none", reason: "steady" })).toBeUndefined()
  expect(egressAdvice(g, undefined)).toBeUndefined()
  expect(egressAdvice(g, { action: "failback", target: [1], reason: "provider-a recovered" })).toBe(
    "The rules would fail back to provider-a now: provider-a recovered",
  )
  expect(
    egressAdvice(group({ enabled: false }), { action: "failover", reason: "x" }),
  ).toBeUndefined()
})

test("the next and previous groups walk the list, from its ends when the name is not in it", () => {
  const groups = [{ name: "a" }, { name: "b" }, { name: "c" }]
  expect(egressSibling(groups, "b", 1)?.name).toBe("c")
  expect(egressSibling(groups, "a", -1)).toBeUndefined()
  expect(egressSibling(groups, "gone", 1)?.name).toBe("a")
  expect(egressSibling(groups, "gone", -1)?.name).toBe("c")
})

test("the last simulation is a short fact that says when it is for an older configuration", () => {
  expect(egressSimulationFact(undefined, "a")).toBe("not simulated")
  expect(egressSimulationFact({ status: "passed", fingerprint: "a" }, "a")).toBe(
    "simulation passed",
  )
  expect(egressSimulationFact({ status: "passed", fingerprint: "a" }, "b")).toBe(
    "simulation out of date",
  )
  expect(egressSimulationFact({ status: "failed", fingerprint: "a" }, "a")).toBe(
    "simulation failed",
  )
  expect(egressSimulationFact({ status: "error", fingerprint: "a" }, "a")).toBe(
    "simulation incomplete",
  )
})
