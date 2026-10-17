import { describe, expect, test } from "bun:test"
import {
  draftOfPooling,
  micros,
  poolerAddresses,
  poolerIsIdle,
  poolerReading,
  poolingChanges,
  poolingProblems,
  poolingSettings,
  poolTotals,
  restartsPooler,
  statTotals,
} from "./settings-pooling-model"

const LIMITS = { poolSize: 500, maxClientConn: 10000 }
const SAVED = { poolMode: "transaction", poolSize: 20, maxClientConn: 200, authMode: "userlist" }

describe("the form against the server's limits", () => {
  test("a saved pooler's draft holds its numbers as typed text", () => {
    expect(draftOfPooling(SAVED)).toEqual({
      poolMode: "transaction",
      poolSize: "20",
      maxClientConn: "200",
      authMode: "userlist",
    })
  })

  test("each number is held to the bounds the server states", () => {
    const draft = draftOfPooling(SAVED)
    expect(poolingProblems(draft, LIMITS)).toEqual({
      poolSize: undefined,
      maxClientConn: undefined,
    })
    expect(poolingProblems({ ...draft, poolSize: "0" }, LIMITS).poolSize).toContain("1 to 500")
    expect(poolingProblems({ ...draft, poolSize: "501" }, LIMITS).poolSize).toBeDefined()
    expect(poolingProblems({ ...draft, poolSize: "2.5" }, LIMITS).poolSize).toBeDefined()
    expect(poolingProblems({ ...draft, poolSize: "" }, LIMITS).poolSize).toBeDefined()
    expect(poolingProblems({ ...draft, maxClientConn: "10001" }, LIMITS).maxClientConn).toContain(
      "10000",
    )
    expect(poolingProblems({ ...draft, maxClientConn: "-3" }, LIMITS).maxClientConn).toBeDefined()
    expect(poolingProblems({ ...draft, poolSize: " 7 " }, LIMITS).poolSize).toBeUndefined()
  })

  test("a draft with a number out of range means no settings", () => {
    const draft = draftOfPooling(SAVED)
    expect(poolingSettings({ ...draft, poolSize: "9", poolMode: "session" }, LIMITS)).toEqual({
      ...SAVED,
      poolSize: 9,
      poolMode: "session",
    })
    expect(poolingSettings({ ...draft, poolSize: "x" }, LIMITS)).toBeUndefined()
  })

  test("a change is counted by the fields that differ, and an unchanged draft by none", () => {
    const draft = draftOfPooling(SAVED)
    expect(poolingChanges(SAVED, draft)).toBe(0)
    expect(poolingChanges(SAVED, { ...draft, poolSize: " 20 " })).toBe(0)
    expect(poolingChanges(SAVED, { ...draft, poolSize: "21", poolMode: "session" })).toBe(2)
  })

  test("only a change of authentication restarts the pooler", () => {
    const draft = draftOfPooling(SAVED)
    expect(restartsPooler(SAVED, { ...draft, poolSize: "30", poolMode: "session" })).toBe(false)
    expect(restartsPooler(SAVED, { ...draft, authMode: "auth_query" })).toBe(true)
  })
})

describe("what the pooler reads as", () => {
  test("off, running, stopped and missing each have a tone and a word", () => {
    expect(poolerReading({ enabled: false })).toEqual({ tone: "unknown", word: "Off" })
    expect(poolerReading({ enabled: true, state: "running" })).toEqual({
      tone: "running",
      word: "Running",
    })
    expect(poolerReading({ enabled: true, state: "exited" })).toEqual({
      tone: "stopped",
      word: "Stopped",
    })
    expect(poolerReading({ enabled: true, state: "missing" }).tone).toBe("warning")
  })

  test("its addresses are the host's, when it is published, and the name inside Docker", () => {
    const pooler = { port: 6433, linkedHost: "db-3-pool.jd.internal", linkedPort: 6432 }
    expect(poolerAddresses({ ...pooler, exposure: "local" }, "127.0.0.1")).toEqual([
      { id: "host", label: "This server", address: "127.0.0.1:6433" },
      { id: "container", label: "Inside Docker", address: "db-3-pool.jd.internal:6432" },
    ])
    expect(poolerAddresses({ ...pooler, exposure: "public" }, "127.0.0.1")[0].label).toBe(
      "This server and anywhere",
    )
    expect(
      poolerAddresses({ linkedHost: "db-3-pool.jd.internal", linkedPort: 6432 }, "127.0.0.1"),
    ).toHaveLength(1)
  })
})

describe("the figures", () => {
  test("microseconds are written in the unit a reader compares", () => {
    expect(micros(0)).toBe("0 ms")
    expect(micros(420)).toBe("0.4 ms")
    expect(micros(12_400)).toBe("12 ms")
    expect(micros(1_250_000)).toBe("1.3 s")
    expect(micros(Number.NaN)).toBe("0 ms")
  })

  const pool = (over) => ({
    database: "app",
    user: "app",
    poolMode: "transaction",
    clActive: 0,
    clWaiting: 0,
    svActive: 0,
    svIdle: 0,
    svUsed: 0,
    svTested: 0,
    svLogin: 0,
    maxWaitSec: 0,
    ...over,
  })

  test("pools add up, and the longest wait is the longest of them", () => {
    expect(
      poolTotals([
        pool({ clActive: 3, clWaiting: 1, svActive: 2, svIdle: 3, svUsed: 1, maxWaitSec: 2 }),
        pool({ clActive: 1, svActive: 1, svIdle: 1, maxWaitSec: 5 }),
      ]),
    ).toEqual({
      clientsActive: 4,
      clientsWaiting: 1,
      serversActive: 3,
      serversIdle: 5,
      maxWaitSec: 5,
    })
    expect(poolTotals([]).clientsActive).toBe(0)
  })

  test("rates add up and times are weighed by the queries they were spent on", () => {
    const totals = statTotals([
      {
        avgQueryCount: 30,
        avgXactCount: 10,
        avgQueryTime: 1000,
        avgWaitTime: 100,
        avgRecv: 5,
        avgSent: 7,
        totalQueryCount: 300,
      },
      {
        avgQueryCount: 10,
        avgXactCount: 5,
        avgQueryTime: 5000,
        avgWaitTime: 300,
        avgRecv: 1,
        avgSent: 1,
        totalQueryCount: 50,
      },
    ])
    expect(totals.queriesPerSecond).toBe(40)
    expect(totals.transactionsPerSecond).toBe(15)
    expect(totals.queryTime).toBe(2000)
    expect(totals.waitTime).toBe(150)
    expect(totals.receivedPerSecond).toBe(6)
    expect(totals.queries).toBe(350)
    expect(statTotals([]).queryTime).toBe(0)
  })

  test("a pooler nothing has used lists no pool and counts no query", () => {
    expect(poolerIsIdle({ pools: [], stats: [] })).toBe(true)
    expect(poolerIsIdle({ pools: [], stats: [{ totalQueryCount: 0 }] })).toBe(true)
    expect(poolerIsIdle({ pools: [], stats: [{ totalQueryCount: 4 }] })).toBe(false)
    expect(poolerIsIdle({ pools: [pool({})], stats: [] })).toBe(false)
  })
})
