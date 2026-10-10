import { expect, test } from "bun:test"
import {
  egressDraftFromGroup,
  egressDraftProblems,
  egressDuration,
  egressEventReading,
  egressGroupReading,
  egressHop,
  egressManualActions,
  egressMemberReading,
  egressPhaseSummary,
  egressPolicySummary,
  egressRequestFromDraft,
  egressSimulationReading,
  emptyEgressDraft,
} from "./network-egress"

const member = (id, overrides = {}) => ({
  id,
  name: id === 1 ? "provider-a" : "provider-b",
  kind: "gateway",
  gateway: id === 1 ? "192.0.2.1" : "198.51.100.1",
  device: id === 1 ? "eth0" : "eth1",
  priority: id,
  weight: 1,
  table: 7700 + id,
  mark: `0x${id}00/0x7f00`,
  state: "up",
  proven: true,
  active: id === 1,
  good: 5,
  bad: 0,
  history: [],
  ...overrides,
})

const group = (overrides = {}) => ({
  id: 3,
  slot: 0,
  name: "uplinks",
  family: "inet",
  policy: { kind: "all" },
  probes: [{ kind: "icmp", target: "1.1.1.1" }],
  thresholds: {
    intervalSeconds: 10,
    timeoutMillis: 1000,
    lossPercent: 20,
    latencyMillis: 300,
    window: 5,
    failAfter: 3,
    recoverAfter: 5,
    holdSeconds: 60,
    stableSeconds: 120,
  },
  sticky: false,
  connections: "flush",
  failback: "automatic",
  enabled: true,
  automation: false,
  active: [1],
  fingerprint: "f1",
  table: 7700,
  members: [member(1), member(2)],
  readiness: [],
  events: [],
  ...overrides,
})

test("policies and hops read as the traffic and path they stand for", () => {
  expect(egressPolicySummary({ kind: "all" })).toContain("default route")
  expect(egressPolicySummary({ kind: "selector", from: "10.8.0.0/24" })).toBe(
    "Traffic from 10.8.0.0/24",
  )
  expect(egressPolicySummary({ kind: "rule", fwmark: "0x10000/0xff0000", iif: "lan0" })).toBe(
    "Packets marked 0x10000/0xff0000 and arriving on lan0",
  )
  expect(egressHop({ kind: "gateway", gateway: "192.0.2.1", device: "eth0" })).toBe(
    "via 192.0.2.1 on eth0",
  )
  expect(egressHop({ kind: "tunnel", device: "wg0" })).toBe("through wg0")
})

test("a member is up only with fresh proof, and an unmeasured member is not down", () => {
  expect(egressMemberReading(member(1)).label).toBe("Up")
  expect(egressMemberReading(member(1, { proven: false })).tone).toBe("warning")
  expect(egressMemberReading(member(1, { state: "down", proven: false })).tone).toBe("danger")
  expect(egressMemberReading(member(1, { state: "unknown", proven: false })).label).toBe(
    "Measuring",
  )
})

test("the group reading names the carrying member and a dead one", () => {
  expect(egressGroupReading(group()).label).toBe("Carrying traffic through provider-a")
  expect(egressGroupReading(group({ enabled: false })).tone).toBe("default")
  const dead = group({ members: [member(1, { state: "down", proven: false }), member(2)] })
  expect(egressGroupReading(dead)).toEqual({
    label: "Carrying traffic through provider-a, which is down",
    tone: "danger",
  })
})

test("manual switches are offered only where a usable member waits", () => {
  expect(egressManualActions(group())).toEqual({ failover: true, failback: false })
  const failedOver = group({
    active: [2],
    members: [member(1, { active: false }), member(2, { active: true })],
  })
  expect(egressManualActions(failedOver)).toEqual({ failover: true, failback: true })
  const backupDown = group({ members: [member(1), member(2, { state: "down" })] })
  expect(egressManualActions(backupDown)).toEqual({ failover: false, failback: false })
})

test("events read by what they did, with refusals and interruptions marked", () => {
  expect(
    egressEventReading({ kind: "switch", action: "failover", outcome: "applied", reason: "" }),
  ).toEqual({
    label: "Failover",
    tone: "success",
  })
  expect(
    egressEventReading({ kind: "switch", action: "manual", outcome: "applying", reason: "" }).tone,
  ).toBe("warning")
  expect(
    egressEventReading({ kind: "switch", action: "failback", outcome: "interrupted", reason: "" })
      .label,
  ).toBe("Failback, interrupted")
  expect(egressEventReading({ kind: "refused", outcome: "refused", reason: "x" }).tone).toBe(
    "danger",
  )
  expect(
    egressEventReading({ kind: "switch", action: "manual", outcome: "pending", reason: "" }).label,
  ).toBe("Switched by hand, awaiting confirmation")
  expect(
    egressEventReading({ kind: "switch", action: "manual", outcome: "recovered", reason: "" })
      .label,
  ).toBe("Switched by hand, restored")
  expect(
    egressEventReading({
      kind: "state",
      outcome: "observed",
      reason: "provider-a is down after 3 bad samples",
    }),
  ).toEqual({ label: "Member down", tone: "danger" })
})

test("only a passed run of this exact configuration authorises automation", () => {
  expect(egressSimulationReading(undefined, "f1")).toEqual({
    label: "Not simulated",
    tone: "warning",
    current: false,
  })
  const sim = { id: "s", groupId: 3, fingerprint: "f1", status: "passed", startedAt: "" }
  expect(egressSimulationReading(sim, "f1")).toEqual({
    label: "Passed",
    tone: "success",
    current: true,
  })
  expect(egressSimulationReading(sim, "f2").label).toBe("Passed for an earlier configuration")
  expect(egressSimulationReading({ ...sim, status: "failed" }, "f1").tone).toBe("danger")
})

test("a run is summarised phase by phase with its switches", () => {
  const step = (index, phase, switched, action = "none") => ({
    index,
    phase,
    seconds: index * 10,
    members: [],
    active: switched ? [2] : [1],
    decision: { action, reason: switched ? "provider-a is down" : "steady" },
    switched,
    dataPath: "reached",
  })
  const sim = {
    id: "s",
    groupId: 3,
    fingerprint: "f1",
    status: "passed",
    startedAt: "",
    result: {
      phases: [
        { name: "warmup", description: "clean", start: 0, end: 1, faults: {} },
        { name: "failure", description: "lost", start: 2, end: 4, faults: { 1: "loss 100%" } },
      ],
      steps: [
        step(0, "warmup", false),
        step(1, "warmup", false),
        step(2, "failure", false),
        step(3, "failure", false, "hold"),
        step(4, "failure", true, "failover"),
      ],
      expectations: [],
      topology: [],
      limits: [],
      cleanup: "",
      intervalSeconds: 10,
    },
  }
  expect(egressPhaseSummary(sim)).toEqual([
    { name: "warmup", description: "clean", samples: 2, switches: [], holds: 0 },
    {
      name: "failure",
      description: "lost",
      samples: 3,
      switches: [{ sample: 3, action: "failover", active: [2], reason: "provider-a is down" }],
      holds: 1,
    },
  ])
})

test("a draft is checked before it is sent and becomes exactly the request", () => {
  const draft = emptyEgressDraft()
  expect(egressDraftProblems(draft)).toContain("Name the group.")
  draft.name = "uplinks"
  draft.members[0] = { ...draft.members[0], name: "a", gateway: "192.0.2.1", device: "eth0" }
  draft.members[1] = { ...draft.members[1], name: "b", kind: "tunnel", gateway: "", device: "wg0" }
  draft.probes = [
    { kind: "icmp", target: "1.1.1.1", port: "", name: "" },
    { kind: "dns", target: "9.9.9.9", port: "", name: "example.com" },
  ]
  draft.protected = "203.0.113.9/32, 198.51.100.0/24"
  expect(egressDraftProblems(draft)).toEqual([])
  expect(egressRequestFromDraft(draft)).toEqual({
    name: "uplinks",
    family: "inet",
    policy: { kind: "all" },
    members: [
      { name: "a", kind: "gateway", gateway: "192.0.2.1", device: "eth0", priority: 1, weight: 1 },
      { name: "b", kind: "tunnel", device: "wg0", priority: 2, weight: 1 },
    ],
    probes: [
      { kind: "icmp", target: "1.1.1.1" },
      { kind: "dns", target: "9.9.9.9", name: "example.com" },
    ],
    thresholds: {
      intervalSeconds: 10,
      timeoutMillis: 1000,
      lossPercent: 20,
      latencyMillis: 300,
      window: 5,
      failAfter: 3,
      recoverAfter: 5,
      holdSeconds: 60,
      stableSeconds: 120,
    },
    sticky: false,
    connections: "flush",
    failback: "automatic",
    protected: ["203.0.113.9/32", "198.51.100.0/24"],
  })
  draft.thresholds.latencyMillis = "1500"
  expect(egressDraftProblems(draft)).toContain(
    "The latency threshold must be below the probe timeout.",
  )
  draft.thresholds.latencyMillis = "300"
  draft.members[1] = { ...draft.members[0] }
  expect(egressDraftProblems(draft)).toContain("Two members are the same next hop.")
  draft.family = "inet6"
  expect(egressDraftProblems(draft).some((p) => p.includes("family"))).toBe(true)
})

test("an edit keeps member ids and the configured source", () => {
  const draft = egressDraftFromGroup(
    group({
      members: [
        member(1, { source: "192.0.2.10", probeSource: "192.0.2.10" }),
        member(2, { probeSource: "198.51.100.9" }),
      ],
    }),
  )
  expect(draft.members.map((m) => [m.id, m.source])).toEqual([
    [1, "192.0.2.10"],
    [2, ""],
  ])
  expect(egressRequestFromDraft(draft).members[0].id).toBe(1)
})

test("durations read in the unit that fits", () => {
  expect(egressDuration(45)).toBe("45 s")
  expect(egressDuration(120)).toBe("2 min")
  expect(egressDuration(90)).toBe("1 min 30 s")
})
