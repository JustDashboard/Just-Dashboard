import { describe, expect, test } from "bun:test"
import { chartEvents } from "./events"

const sample = (at, uptime) => ({ at, counters: {}, gauges: { uptimeSeconds: uptime } })
const iso = (ms) => new Date(ms).toISOString()

describe("what the chart marks on its time axis", () => {
  const run = [sample(10_000, 500), sample(15_000, 505), sample(20_000, 510), sample(25_000, 515)]

  test("a dump taken while the page was open is set on the next reading", () => {
    const events = chartEvents(run, "uptimeSeconds", [
      { takenAt: iso(17_200), summary: "12 tables" },
    ])
    expect(events).toEqual([
      {
        ts: iso(20_000),
        kind: "backup",
        title: "Backup taken",
        detail: "12 tables",
        severity: "info",
      },
    ])
  })

  test("a dump from before the first reading, or after the last, is not marked", () => {
    expect(chartEvents(run, "uptimeSeconds", [{ takenAt: iso(9_000) }])).toEqual([])
    expect(chartEvents(run, "uptimeSeconds", [{ takenAt: iso(26_000) }])).toEqual([])
    expect(chartEvents(run, "uptimeSeconds", [{ takenAt: "not a date" }])).toEqual([])
  })

  test("an uptime that went down is a restart, at the reading that shows it", () => {
    const restarted = [
      sample(10_000, 500),
      sample(15_000, 505),
      sample(20_000, 2),
      sample(25_000, 7),
    ]
    expect(chartEvents(restarted, "uptimeSeconds")).toEqual([
      { ts: iso(20_000), kind: "reboot", title: "Server restarted", severity: "warning" },
    ])
  })

  test("a family that reports no uptime marks no restart", () => {
    expect(chartEvents(run, "uptime_in_seconds")).toEqual([])
  })

  test("one reading is not a chart yet", () => {
    expect(chartEvents(run.slice(0, 1), "uptimeSeconds", [{ takenAt: iso(11_000) }])).toEqual([])
  })
})
