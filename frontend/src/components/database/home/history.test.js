import { expect, test } from "bun:test"
import {
  historySamples,
  chartRows,
  rateBetween,
  sqlSample,
  redisSample,
  mongoSample,
} from "./samples"

test("retained SQL totals yield rates immediately, using the recorder's clock", () => {
  const stats = (queries) => ({
    at: "2026-10-02T00:00:00Z",
    supported: true,
    databaseBytes: 1,
    counters: { queries },
    gauges: {},
  })
  const samples = historySamples(
    [
      { at: 1000, stats: stats(100), gap: false },
      { at: 31000, stats: stats(400), gap: false },
    ],
    sqlSample,
  )
  expect(rateBetween(samples[0], samples[1], "queries")).toBe(10)
})

test("outages break rates and gauge lines; resumed collection starts a new interval", () => {
  const samples = [
    { at: 1000, counters: { queries: 100 }, gauges: { sessions: 2 } },
    { at: 31000, counters: { queries: 400 }, gauges: { sessions: 3 }, gap: true },
    { at: 61000, counters: { queries: 700 }, gauges: { sessions: 4 } },
  ]
  const rows = chartRows(samples, [
    { key: "rate", rate: "queries" },
    { key: "sessions", gauge: "sessions" },
  ])
  expect(rows[1]).toEqual({ ts: 30999 })
  expect(rows[2]).toEqual({ ts: 31000, sessions: 3 })
  expect(rows[3].rate).toBe(10)
})

test("retained Redis and Mongo counters use the same history contract", () => {
  const redis = historySamples(
    [
      {
        at: 123,
        gap: false,
        stats: {
          server: {
            sampledAtMs: 1,
            counters: { total_commands_processed: 42, connected_clients: 3 },
          },
        },
      },
    ],
    redisSample,
  )
  expect(redis[0]).toMatchObject({
    at: 123,
    counters: { total_commands_processed: 42 },
    gauges: { connected_clients: 3 },
  })
  const mongo = historySamples(
    [
      {
        at: 123,
        gap: false,
        stats: { server: { timestamp: 1, opcounters: { query: 42 }, connections: { current: 3 } } },
      },
    ],
    mongoSample,
  )
  expect(mongo[0]).toMatchObject({
    at: 123,
    counters: { "ops.query": 42 },
    gauges: { connections: 3 },
  })
})

test("recorded sessions distinguish working from waiting without a live session list", () => {
  const sample = sqlSample({
    at: "2026-10-02T00:00:00Z",
    supported: true,
    counters: {},
    gauges: {},
    connections: { total: 5, active: 3, idle: 1, idleInTransaction: 1, waiting: 1, max: 100 },
  })
  expect(sample.gauges.sessionsWorking).toBe(2)
  expect(sample.gauges.sessionsWaiting).toBe(1)
})

test("missing or invalid retained snapshots cannot break the page", () => {
  expect(
    historySamples([null, { at: 1 }, { at: 1, stats: null }, { at: NaN, stats: {} }], sqlSample),
  ).toEqual([])
})
