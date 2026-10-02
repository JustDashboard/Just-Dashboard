import { describe, expect, test } from "bun:test"
import {
  CHART_UNITS,
  CLICKHOUSE_VIEWS,
  MONGO_VIEWS,
  REDIS_VIEWS,
  SQL_VIEWS,
  drawnRows,
  highest,
  offeredViews,
  roundTicks,
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

describe("an axis stepped by the page", () => {
  test("a handful of sessions is counted in ones, with no tick printed twice", () => {
    expect(roundTicks(3, true)).toEqual([0, 1, 2, 3])
    expect(roundTicks(4, true)).toEqual([0, 1, 2, 3, 4])
    expect(roundTicks(1, true)).toEqual([0, 1])
  })

  test("more of them step by two, five or ten, and never past five ticks", () => {
    expect(roundTicks(7, true)).toEqual([0, 2, 4, 6, 8])
    expect(roundTicks(18, true)).toEqual([0, 5, 10, 15, 20])
    expect(roundTicks(100, true)).toEqual([0, 50, 100])
    expect(roundTicks(151, true)).toEqual([0, 50, 100, 150, 200])
    for (const max of [1, 2, 3, 5, 9, 13, 27, 64, 99, 480, 5200]) {
      const ticks = roundTicks(max, true)
      expect(ticks.length).toBeLessThanOrEqual(5)
      expect(ticks.at(-1)).toBeGreaterThanOrEqual(max)
      expect(ticks.every(Number.isInteger)).toBe(true)
    }
  })

  test("a rate may step by fractions, and they multiply out clean", () => {
    expect(roundTicks(0.6, false)).toEqual([0, 0.2, 0.4, 0.6])
    expect(roundTicks(0.3, false)).toEqual([0, 0.1, 0.2, 0.3])
    expect(roundTicks(562, false)).toEqual([0, 200, 400, 600])
  })

  test("a window with nothing above zero still has an axis", () => {
    expect(roundTicks(0, true)).toEqual([0, 1])
    expect(roundTicks(0, false)).toEqual([0, 1])
  })

  test("the rate axis prints as many decimals as tell its ticks apart", () => {
    const axis = CHART_UNITS.rate.axisFormat
    expect(roundTicks(0.2, false).map(axis)).toEqual(["0/s", "0.05/s", "0.1/s", "0.15/s", "0.2/s"])
    expect(roundTicks(3, false).map(axis)).toEqual(["0/s", "1/s", "2/s", "3/s"])
  })
})

describe("the rows a view draws", () => {
  const rows = [
    { ts: 1, open: 3 },
    { ts: 2, open: 3, commits: 1.5 },
    { ts: 3, open: 4, commits: 2 },
  ]

  test("a view of rates leaves out the first row, which holds none", () => {
    expect(drawnRows(rows, [{ key: "commits" }]).map((row) => row.ts)).toEqual([2, 3])
    // Two samples are one rate: the one row left is drawn as a dot.
    expect(drawnRows(rows.slice(0, 2), [{ key: "commits" }])).toHaveLength(1)
  })

  test("a view with a gauge keeps every row", () => {
    expect(drawnRows(rows, [{ key: "open" }, { key: "commits" }])).toHaveLength(3)
  })

  test("the highest figure is over the drawn lines only", () => {
    expect(highest(rows, [{ key: "commits" }])).toBe(2)
    expect(highest(rows, [{ key: "open" }, { key: "commits" }])).toBe(4)
    expect(highest([], [{ key: "open" }])).toBe(0)
  })
})
