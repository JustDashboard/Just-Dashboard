import { describe, expect, test } from "bun:test"
import {
  around,
  countWords,
  entryKeys,
  historyEntries,
  oneLine,
  queryNoun,
  rangeSince,
  refreshEvery,
  sortEntries,
  sourceWords,
} from "./queries"

describe("queryNoun", () => {
  test("each engine is read in its own words", () => {
    expect(queryNoun("postgres").title).toBe("Slow statements")
    expect(queryNoun("redis")).toMatchObject({ view: "Commands", title: "Slow commands" })
    expect(queryNoun("mongodb").title).toBe("Slow operations")
    expect(queryNoun("clickhouse").title).toBe("Queries")
    expect(queryNoun("mysql").view).toBe("Queries")
  })
})

test("a source is named as the reader would look for it", () => {
  expect(sourceWords("log")).toBe("the server log")
  expect(sourceWords("slowlog")).toBe("SLOWLOG")
  expect(sourceWords("slow_log")).toBe("mysql.slow_log")
})

test("a statement on one line keeps its words and loses its layout", () => {
  expect(oneLine("SELECT o.id\n\tFROM orders o\n  WHERE o.total > 100 ")).toBe(
    "SELECT o.id FROM orders o WHERE o.total > 100",
  )
})

test("the dashboard's own statements read as rows, a failure said as one", () => {
  const rows = historyEntries([
    {
      id: 1,
      sql: "SELECT 1",
      risk: "safe",
      success: true,
      durationMs: 3,
      rowCount: 1,
      ranAt: "2026-09-27T10:00:00Z",
    },
    {
      id: 2,
      sql: "DROP TABLE t",
      risk: "critical",
      success: false,
      durationMs: 1,
      rowCount: 0,
      ranAt: "2026-09-27T10:01:00Z",
    },
  ])
  expect(rows[0]).toEqual({
    at: "2026-09-27T10:00:00Z",
    durationMs: 3,
    query: "SELECT 1",
    rows: 1,
    error: undefined,
  })
  expect(rows[1].error).toBe("failed")
})

test("the server log around a statement is a minute either side of it", () => {
  expect(around("2026-09-27T10:00:30Z")).toEqual({
    since: "2026-09-27T09:59:30.000Z",
    until: "2026-09-27T10:01:30.000Z",
  })
})

test("slowest first sorts by duration and leaves the newest-first list alone", () => {
  const entries = [
    { at: "b", durationMs: 10, query: "b" },
    { at: "a", durationMs: 900, query: "a" },
  ]
  expect(sortEntries(entries, "latest")).toBe(entries)
  expect(sortEntries(entries, "slowest").map((e) => e.durationMs)).toEqual([900, 10])
  expect(entries[0].durationMs).toBe(10)
})

test("a window's start is counted back from now", () => {
  const now = Date.parse("2026-09-27T12:00:00Z")
  expect(rangeSince("1h", now)).toBe("2026-09-27T11:00:00.000Z")
  expect(rangeSince("7d", now)).toBe("2026-09-20T12:00:00.000Z")
})

test("a count is said in the view's own noun", () => {
  expect(countWords(1, queryNoun("postgres"))).toBe("1 statement")
  expect(countWords(12, queryNoun("redis"))).toBe("12 commands")
})

test("a row keeps its key when a newer statement arrives above it", () => {
  const older = { at: "2026-09-27T09:58:40Z", durationMs: 312, query: "UPDATE stock SET qty = 1" }
  const slow = { at: "2026-09-27T10:01:03Z", durationMs: 1843, query: "SELECT o.id FROM orders o" }
  const newer = { at: "2026-09-27T10:05:00Z", durationMs: 420, query: "SELECT 1" }
  const before = entryKeys([slow, older])
  const after = entryKeys([newer, slow, older])
  expect(after.slice(1)).toEqual(before)
})

test("the same statement recorded twice at one moment is still two rows", () => {
  const twice = { at: "2026-09-27T10:00:00Z", durationMs: 12, query: "HGET cart:77" }
  const keys = entryKeys([twice, { ...twice }])
  expect(new Set(keys).size).toBe(2)
})

test("a longer window is read again less often", () => {
  expect(refreshEvery("1h")).toBe(30_000)
  expect(refreshEvery("24h")).toBeGreaterThan(refreshEvery("1h"))
  expect(refreshEvery("7d")).toBeGreaterThan(refreshEvery("24h"))
})
