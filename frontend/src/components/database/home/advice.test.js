import { describe, expect, test } from "bun:test"
import { adviceDestination, databaseAdviceTitle, needsDatabaseAttention } from "./advice"

const tools = { id: 7, has: () => true, canQuery: true }
const advice = (over = {}) => ({ id: "check", level: "warning", targets: [], ...over })

describe("database findings lead to their actual controls", () => {
  test("optional statistics, memory caps and unused indexes stay out of Attention", () => {
    for (const id of ["no-stat-statements", "container-no-memory-limit", "unused-index"])
      expect(needsDatabaseAttention(advice({ id, level: "notice" }))).toBe(false)
    expect(needsDatabaseAttention(advice({ id: "no-backup" }))).toBe(true)
    expect(needsDatabaseAttention(advice({ id: "sequence-exhaustion", level: "critical" }))).toBe(
      true,
    )
  })

  test("an absent dashboard dump does not claim that external backups do not exist", () => {
    expect(databaseAdviceTitle({ id: "no-backup", title: "No backup of this database" })).toBe(
      "No dashboard backup recorded",
    )
    expect(
      databaseAdviceTitle({ id: "stale-backup", title: "The newest backup is 9 days old" }),
    ).toBe("The newest backup is 9 days old")
  })

  test("the entire fix reaches Query, and a protected or viewer connection offers inspection", () => {
    const fix = advice({
      sql: 'CREATE INDEX "orders_fk" ON "public"."orders" ("customer_id");',
      targets: [{ kind: "table", schema: "public", name: "orders" }],
    })
    expect(adviceDestination(fix, tools)).toEqual({
      section: "query",
      params: { sql: fix.sql },
      label: "Review the fix in Query",
    })
    expect(adviceDestination(fix, { ...tools, canQuery: false })).toEqual({
      section: "schema",
      params: { schema: "public", table: "orders" },
      label: "Inspect schema",
    })
  })

  test("a cross-connection link never opens or supplies a fix for another database", () => {
    expect(
      adviceDestination(advice({ link: "/databases/8/settings", sql: "SELECT 1" }), tools),
    ).toBeUndefined()
  })

  test("replication, indexes and parameters arrive at their own view", () => {
    for (const [kind, view] of [
      ["slot", "replication"],
      ["index", "indexes"],
      ["table", "tables"],
    ])
      expect(
        adviceDestination(
          advice({ link: "/databases/7/performance", targets: [{ kind, name: "target" }] }),
          tools,
        ).params,
      ).toEqual({ view })
    expect(
      adviceDestination(
        advice({
          link: "/databases/7/settings",
          targets: [{ kind: "setting", name: "max_connections" }],
        }),
        tools,
      ).params,
    ).toEqual({ q: "max_connections" })
    expect(
      adviceDestination(advice({ link: "/databases/7/performance?view=sessions" }), tools).params,
    ).toEqual({ view: "sessions" })
  })

  test("an engine without the destination never gets a dead button", () => {
    expect(
      adviceDestination(advice({ link: "/databases/7/performance" }), {
        ...tools,
        has: () => false,
      }),
    ).toBeUndefined()
  })
})
