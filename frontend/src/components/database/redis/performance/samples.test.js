import { describe, expect, test } from "bun:test"
import {
  addSample,
  lifetimeHitRate,
  micros,
  perSecond,
  rateBetween,
  seriesOf,
  statRows,
} from "./samples"
import { scheduleWords } from "./schedule"

const stats = (sampledAtMs, counters) => ({
  sampledAtMs,
  counters,
  flavor: "redis",
  version: "7.4",
  role: "primary",
  mode: "standalone",
})
const sample = (at, counters) => ({ at, counters })

describe("keeping samples", () => {
  test("a new sample goes after the ones held", () => {
    const held = addSample([], stats(1000, { keys: 1 }))
    expect(addSample(held, stats(4000, { keys: 2 })).map((s) => s.at)).toEqual([1000, 4000])
  })

  test("a reading taken at a moment already held is not a second point", () => {
    const held = addSample([], stats(1000, { keys: 1 }))
    expect(addSample(held, stats(1000, { keys: 1 }))).toBe(held)
    expect(addSample(held, stats(900, { keys: 1 }))).toBe(held)
  })

  test("the oldest are dropped past the cap", () => {
    let held = []
    for (let i = 1; i <= 5; i++) held = addSample(held, stats(i * 1000, {}), 3)
    expect(held.map((s) => s.at)).toEqual([3000, 4000, 5000])
  })
})

describe("a rate between two samples", () => {
  const before = sample(1000, { total_commands_processed: 100 })

  test("is the difference over the seconds between them", () => {
    expect(
      rateBetween(
        before,
        sample(3000, { total_commands_processed: 160 }),
        "total_commands_processed",
      ),
    ).toBe(30)
  })

  test("a counter that went down is a restart, not a negative rate", () => {
    expect(
      rateBetween(
        before,
        sample(3000, { total_commands_processed: 5 }),
        "total_commands_processed",
      ),
    ).toBeNull()
  })

  test("a counter the server does not report, or no time between, says nothing", () => {
    expect(rateBetween(before, sample(3000, {}), "total_commands_processed")).toBeNull()
    expect(
      rateBetween(
        before,
        sample(1000, { total_commands_processed: 160 }),
        "total_commands_processed",
      ),
    ).toBeNull()
  })
})

describe("samples as chart rows", () => {
  const samples = [
    sample(0, {
      total_commands_processed: 0,
      keyspace_hits: 0,
      keyspace_misses: 0,
      used_memory: 100,
      connected_clients: 2,
      keys: 5,
    }),
    sample(2000, {
      total_commands_processed: 40,
      keyspace_hits: 30,
      keyspace_misses: 10,
      used_memory: 120,
      connected_clients: 3,
      keys: 5,
    }),
    sample(4000, {
      total_commands_processed: 40,
      keyspace_hits: 30,
      keyspace_misses: 10,
      used_memory: 110,
      connected_clients: 3,
      keys: -1,
    }),
  ]
  const rows = statRows(samples)

  test("the first row has gauges and no rates: nothing came before it", () => {
    expect(rows[0]).toMatchObject({ ts: 0, ops: null, hitRate: null, memory: 100, clients: 2 })
  })

  test("rates and the hit share come from the interval before each row", () => {
    expect(rows[1]).toMatchObject({ ops: 20, hits: 15, misses: 5, hitRate: 75, memory: 120 })
  })

  test("an interval with no lookups has no hit rate, rather than a hundred or zero", () => {
    expect(rows[2].ops).toBe(0)
    expect(rows[2].hitRate).toBeNull()
  })

  test("a counter the server answers -1 for is absent, not minus one", () => {
    expect(rows[2].keys).toBeNull()
  })

  test("a series is its readings, without the moments it has none for", () => {
    expect(seriesOf(rows, "ops")).toEqual([20, 0])
    expect(seriesOf(rows, "hitRate")).toEqual([75])
    expect(seriesOf(rows, "keys")).toEqual([5, 5])
  })
})

describe("readings in words", () => {
  test("hits as a share of every lookup since the server started", () => {
    expect(lifetimeHitRate({ keyspace_hits: 90, keyspace_misses: 10 })).toBe(90)
    expect(lifetimeHitRate({ keyspace_hits: 0, keyspace_misses: 0 })).toBeNull()
    expect(lifetimeHitRate({})).toBeNull()
  })

  test("a count a second, in the fewest digits that say it", () => {
    expect(perSecond(2146.4)).toBe("2,146")
    expect(perSecond(12.44)).toBe("12.4")
    expect(perSecond(1.65)).toBe("1.65")
    expect(perSecond(0.3)).toBe("0.3")
    expect(perSecond(0)).toBe("0")
  })

  test("microseconds in the unit a person reads", () => {
    expect(micros(0.4)).toBe("0.4 µs")
    expect(micros(458)).toBe("458 µs")
    expect(micros(1370)).toBe("1.4 ms")
    expect(micros(86_609)).toBe("87 ms")
    expect(micros(2_500_000)).toBe("2.5 s")
  })

  test("a save schedule as the sentences it means", () => {
    expect(scheduleWords("3600 1 300 100")).toEqual([
      "after 1h if 1 key changed",
      "after 5m if 100 keys changed",
    ])
    expect(scheduleWords("something else")).toEqual(["something else"])
  })
})
