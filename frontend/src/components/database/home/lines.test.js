import { describe, expect, test } from "bun:test"
import { MONGO_VIEWS, REDIS_VIEWS, SQL_VIEWS } from "./charts"
import { lineReading, said } from "./lines"
import { TRANSACTIONS } from "./samples"

const view = (views, id) => views.find((entry) => entry.id === id)
const at = (index) => 1_000_000 + index * 30_000

describe("said", () => {
  test("a reading is its figure in its unit, then what it counts", () => {
    expect(said("count", 12, "Open")).toBe("12 open")
    expect(said("rate", 10, "Transactions")).toBe("10 transactions a second")
    expect(said("rate", 2.5, "Statements", false)).toBe("2.5 statements")
    expect(said("bytes", 2048, "Used")).toBe("2.0 KB used")
    expect(said("bytesRate", 2048, "Received")).toBe("2.0 KB received a second")
    expect(said("percent", 99, "Hit rate")).toBe("99.0% hit rate")
  })
})

describe("lineReading", () => {
  test("sessions are said against the most the server accepts, the others after them", () => {
    const samples = [8, 10, 12].map((sessions, index) => ({
      at: at(index),
      counters: {},
      gauges: { sessions, sessionsActive: 2, sessionsWaiting: 0, sessionsMax: 100 },
    }))
    expect(lineReading(view(SQL_VIEWS, "sessions"), samples)).toEqual({
      words: "12 open of 100",
      rest: "2 active · 0 waiting on a lock",
      values: [8, 10, 12],
      max: undefined,
    })
  })

  test("a rate is read between two samples, and a line the engine does not count is left out", () => {
    const samples = [0, 1, 2].map((index) => ({
      at: at(index),
      counters: { [TRANSACTIONS]: index * 300 },
      gauges: {},
    }))
    const reading = lineReading(view(SQL_VIEWS, "throughput"), samples)
    expect(reading.words).toBe("10 transactions a second")
    expect(reading.rest).toBeUndefined()
    expect(reading.values).toEqual([10, 10])
  })

  test("a share is a percentage on a scale of a hundred", () => {
    const samples = [0, 1].map((index) => ({
      at: at(index),
      counters: { blocksHit: index * 99, blocksRead: index },
      gauges: {},
    }))
    expect(lineReading(view(SQL_VIEWS, "cache"), samples)).toMatchObject({
      words: "99.0% hit rate",
      values: [99],
      max: 100,
    })
  })

  test("nothing recorded yet is no reading, not a zero", () => {
    expect(lineReading(view(SQL_VIEWS, "rows"), [])).toEqual({
      words: undefined,
      rest: undefined,
      values: [],
      max: undefined,
    })
    // One sample has gauges and no rate yet.
    const one = [{ at: at(0), counters: { rowsRead: 5 }, gauges: {} }]
    expect(lineReading(view(SQL_VIEWS, "rows"), one).words).toBeUndefined()
  })

  test("the other families read in their own words", () => {
    const redis = [0, 1].map((index) => ({
      at: at(index),
      counters: { total_net_input_bytes: index * 61_440, total_net_output_bytes: index * 30_720 },
      gauges: { used_memory: 1_048_576, used_memory_rss: 2_097_152, connected_clients: 3 },
    }))
    expect(lineReading(view(REDIS_VIEWS, "memory"), redis).words).toBe("1.0 MB used")
    expect(lineReading(view(REDIS_VIEWS, "memory"), redis).rest).toBe("2.0 MB resident")
    expect(lineReading(view(REDIS_VIEWS, "network"), redis).words).toBe("2.0 KB received a second")
    expect(lineReading(view(REDIS_VIEWS, "network"), redis).rest).toBe("1.0 KB sent")
    const mongo = [{ at: at(0), counters: {}, gauges: { connections: 6, connectionsActive: 1 } }]
    expect(lineReading(view(MONGO_VIEWS, "connections"), mongo).words).toBe("6 open")
  })
})
