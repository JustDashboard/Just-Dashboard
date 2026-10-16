import { describe, expect, test } from "bun:test"
import { bySchema, copyTone, schemaDetail } from "./holdings"

describe("what a database holds, by schema", () => {
  const tables = [
    { schema: "public", table: "orders", rows: 900, bytes: 4_000 },
    { schema: "analytics", table: "events", rows: 60_000, bytes: 9_000 },
    { schema: "public", table: "customers", rows: 100, bytes: 6_000 },
  ]

  test("sums each schema's tables and puts the largest first", () => {
    expect(bySchema(tables, true)).toEqual([
      { schema: "public", bytes: 10_000, rows: 1_000, tables: 2 },
      { schema: "analytics", bytes: 9_000, rows: 60_000, tables: 1 },
    ])
  })

  test("ranks by rows where the engine states no sizes", () => {
    expect(bySchema(tables, false).map((share) => share.schema)).toEqual(["analytics", "public"])
  })

  test("a count the engine does not know is not taken away from the sum", () => {
    const [only] = bySchema([{ schema: "main", table: "notes", rows: -1, bytes: -1 }], true)
    expect(only).toEqual({ schema: "main", bytes: 0, rows: 0, tables: 1 })
  })

  test("says the size where it is known, and the rows where it is not", () => {
    const share = { schema: "public", bytes: 2_048, rows: 1_500, tables: 3 }
    expect(schemaDetail(share, true)).toBe("2.0 KB · 3 tables")
    expect(schemaDetail(share, false)).toBe("1,500 rows · 3 tables")
  })
})

describe("the wire to a database's dumps", () => {
  const now = Date.parse("2026-10-16T12:00:00Z")
  const ago = (hours) => new Date(now - hours * 3_600_000).toISOString()

  test("is green while the newest dump is today's, plain after, and amber past a week", () => {
    expect(copyTone(ago(3), now)).toBe("success")
    expect(copyTone(ago(30), now)).toBe("default")
    expect(copyTone(ago(24 * 8), now)).toBe("warning")
  })

  test("is plain where there is no dump, or no date to read", () => {
    expect(copyTone(undefined, now)).toBe("default")
    expect(copyTone("not a date", now)).toBe("default")
  })
})
