import { describe, expect, test } from "bun:test"
import {
  CHART_UNITS,
  CLICKHOUSE_VIEWS,
  MONGO_VIEWS,
  REDIS_VIEWS,
  SQL_VIEWS,
  offeredViews,
} from "./charts"
import { TRANSACTIONS } from "./samples"

const sample = (counters = {}, gauges = {}) => ({ at: 1000, counters, gauges })
const names = (views) => views.map((view) => view.id)

describe("the views a chart offers", () => {
  test("every family's views, before anything is known", () => {
    expect(names(offeredViews(SQL_VIEWS, undefined))).toEqual([
      "sessions",
      "throughput",
      "rows",
      "cache",
    ])
    expect(names(REDIS_VIEWS)).toEqual(["commands", "memory", "clients", "network", "cache"])
    expect(names(MONGO_VIEWS)).toEqual(["operations", "connections", "cache", "network"])
    expect(names(CLICKHOUSE_VIEWS)).toEqual(["queries", "rows", "memory", "sessions"])
  })

  test("a view none of whose lines the engine reports is not offered", () => {
    // SQL Server counts no rows; an account without the grant sees no sessions.
    const newest = sample({ [TRANSACTIONS]: 5, blocksHit: 1, blocksRead: 1 }, {})
    expect(names(offeredViews(SQL_VIEWS, newest))).toEqual(["throughput", "cache"])
    expect(offeredViews(SQL_VIEWS, sample())).toEqual([])
  })

  test("a segmented control holds at most five views, and a view at most five lines", () => {
    for (const views of [SQL_VIEWS, CLICKHOUSE_VIEWS, REDIS_VIEWS, MONGO_VIEWS]) {
      expect(views.length).toBeLessThanOrEqual(5)
      for (const view of views) {
        expect(view.series.length).toBeLessThanOrEqual(5)
        expect(view.series.map((series) => series.key)).toEqual(
          view.sources.map((source) => source.key),
        )
        // One colour per line within a view, each a chart token.
        const colours = view.series.map((series) => series.color)
        expect(new Set(colours).size).toBe(colours.length)
        for (const colour of colours) expect(colour).toMatch(/^var\(--chart-[1-5]\)$/)
      }
    }
  })
})

test("each unit prints its numbers its own way", () => {
  expect(CHART_UNITS.rate.format(2.5)).toBe("2.5/s")
  expect(CHART_UNITS.count.format(12_345)).toBe("12.3K")
  expect(CHART_UNITS.bytes.format(2048)).toBe("2.0 KB")
  expect(CHART_UNITS.bytes.axisFormat(2048)).toBe("2 KB")
  expect(CHART_UNITS.bytesRate.format(2048)).toBe("2.0 KB/s")
  expect(CHART_UNITS.percent.domain).toEqual([0, 100])
})
