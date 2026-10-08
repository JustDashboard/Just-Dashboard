import { describe, expect, test } from "bun:test"
import { findingArea, findingGauge, heldFor, segmentState } from "./health-vocabulary"

describe("health vocabulary", () => {
  test("a finding without an area is placed by its id", () => {
    expect(findingArea({ id: "disk:/srv" })).toBe("storage")
    expect(findingArea({ id: "psi-cpu" })).toBe("cpu")
    expect(findingArea({ id: "systemd.failed" })).toBe("services")
    expect(findingArea({ id: "docker.dead" })).toBe("containers")
    expect(findingArea({ id: "drops:eth0" })).toBe("network")
    expect(findingArea({ id: "memory", area: "hardware" })).toBe("hardware")
  })
  test("memory is drawn as what is taken, against the line it crossed", () => {
    expect(findingGauge({ id: "memory", value: 4, threshold: 5 })).toEqual({
      value: 96,
      mark: 95,
      label: "4% left",
    })
    expect(findingGauge({ id: "disk:/", value: 91.6, threshold: 85 })?.label).toBe("92%")
    expect(findingGauge({ id: "systemd.failed", value: 2, threshold: 0 })).toBeNull()
  })
  test("a check in flight sweeps every segment, an unread area is dashed", () => {
    expect(segmentState("critical", true)).toBe("running")
    expect(segmentState("critical", false)).toBe("failed")
    expect(segmentState("notice", false)).toBe("passed")
    expect(segmentState("unknown", false)).toBe("absent")
  })
  test("how long a condition has held reads in a few characters", () => {
    const now = Date.parse("2026-10-08T12:00:00Z")
    expect(heldFor("2026-10-08T11:46:00Z", now)).toBe("14 min")
    expect(heldFor("2026-10-08T09:00:00Z", now)).toBe("3 h")
    expect(heldFor(undefined, now)).toBeNull()
  })
})
