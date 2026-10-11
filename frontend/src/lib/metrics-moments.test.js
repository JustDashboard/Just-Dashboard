import { describe, expect, test } from "bun:test"
import { groupIncidents, holds, incidentReach } from "./metrics-moments"

const HOUR = 3_600_000
const at = (id, ts, weight = 1) => ({ id, ts, weight })

describe("grouping moments into incidents", () => {
  test("signals within reach of each other are one incident, led by the heaviest", () => {
    const [incident] = groupIncidents(
      [at("cpu", 1_000_000, 1.4), at("deploy", 1_030_000, 100), at("load", 1_060_000, 2)],
      HOUR,
    )
    expect(incident.lead.id).toBe("deploy")
    expect(incident.ts).toBe(1_030_000)
    expect(incident.rest.map((s) => s.id)).toEqual(["load", "cpu"])
    expect([incident.from, incident.to]).toEqual([1_000_000, 1_060_000])
  })

  test("signals further apart than the reach are separate incidents, newest first", () => {
    const incidents = groupIncidents([at("a", 0), at("b", 10 * 60_000)], HOUR)
    expect(incidents.map((i) => i.lead.id)).toEqual(["b", "a"])
  })

  test("a chain of close signals stays one incident", () => {
    const step = incidentReach(HOUR) - 1
    const incidents = groupIncidents([at("a", 0), at("b", step), at("c", step * 2)], HOUR)
    expect(incidents).toHaveLength(1)
  })

  test("past the limit the lightest incidents go, not the oldest", () => {
    const signals = Array.from({ length: 10 }, (_, i) => at(`s${i}`, i * HOUR, i === 0 ? 50 : 1))
    const incidents = groupIncidents(signals, 10 * HOUR, 3)
    expect(incidents).toHaveLength(3)
    expect(incidents.at(-1).lead.id).toBe("s0")
  })

  test("a pinned instant near an incident belongs to it", () => {
    const [incident] = groupIncidents([at("a", HOUR)], HOUR)
    expect(holds(incident, HOUR + 30_000, HOUR)).toBe(true)
    expect(holds(incident, HOUR + 5 * 60_000, HOUR)).toBe(false)
  })
})
