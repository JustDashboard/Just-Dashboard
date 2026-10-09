import { expect, test } from "bun:test"
import {
  ageWords,
  flowNeighbours,
  identityVerdict,
  incidentSpan,
  newestPoint,
  nodeFlows,
  observationAge,
  windowBreakdown,
} from "./topology-reading"

test("a private source is never called the public identity", () => {
  expect(identityVerdict({ public: "translated" })).toContain("not observed")
  expect(identityVerdict({ public: "nic" })).toBe("NIC address is public")
  expect(identityVerdict({ public: "unknown" })).toBe("could not be read")
  expect(identityVerdict({ public: "no_route" })).toBe("no route out")
})

test("the live ring's age turns stale past three intervals", () => {
  expect(observationAge(0, 100)).toBeUndefined()
  expect(observationAge(98, 100)).toEqual({ seconds: 2, stale: false })
  expect(observationAge(90, 100)).toEqual({ seconds: 10, stale: true })
  expect(newestPoint({ a: [{ t: 5, rx: 0, tx: 0 }], b: [{ t: 9, rx: 0, tx: 0 }], c: [] })).toBe(9)
  expect(ageWords(42)).toBe("42 s")
  expect(ageWords(185)).toBe("3 min")
  expect(ageWords(7300)).toBe("2 h")
})

test("flow edges light the nodes each one trades with, both ways", () => {
  const edges = [
    { from: "docker:web", to: "internet", flows: 3 },
    { from: "link:tailscale0", to: "host", flows: 2 },
    { from: "internet", to: "docker:web", flows: 1 },
  ]
  const n = flowNeighbours(edges)
  expect([...n.get("internet")]).toEqual(["docker:web"])
  expect([...n.get("host")]).toEqual(["link:tailscale0"])
  expect(nodeFlows(edges, "docker:web")).toBe(4)
  expect(nodeFlows(edges, "link:wg0")).toBe(0)
})

test("a chart window drills into the devices that carried it, busiest first", () => {
  const links = [
    { name: "ens3", role: "uplink", owner: "system" },
    { name: "wg0", role: "tunnel", owner: "wireguard" },
    { name: "veth1", role: "container", owner: "docker" },
    { name: "gre0", role: "tunnel", owner: "kernel" },
  ]
  const series = {
    ens3: [
      { t: 10, rx: 100, tx: 50 },
      { t: 12, rx: 100, tx: 50 },
      { t: 20, rx: 9999, tx: 0 },
    ],
    wg0: [{ t: 12, rx: 400, tx: 0 }],
    veth1: [{ t: 12, rx: 500, tx: 500 }],
    gre0: [{ t: 12, rx: 1, tx: 1 }],
  }
  expect(windowBreakdown(series, links, 10_000, 12_000)).toEqual([
    { name: "wg0", role: "tunnel", bytes: 800 },
    { name: "ens3", role: "uplink", bytes: 600 },
  ])
})

test("an incident's span runs to its resolution or to now", () => {
  const openedAt = "2026-10-09T10:00:00Z"
  expect(incidentSpan({ openedAt, resolvedAt: "2026-10-09T10:05:00Z" }, 0)).toBe("5 min")
  expect(incidentSpan({ openedAt }, Date.parse("2026-10-09T12:00:30Z"))).toBe("2 h")
})
