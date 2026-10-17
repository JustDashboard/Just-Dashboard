import { describe, expect, test } from "bun:test"
import {
  candidateKey,
  columnsText,
  costChange,
  costText,
  hypopgFact,
  missingColumnsText,
  parseColumns,
  stepLines,
  usesText,
} from "./index-advice-model"

const plan = (node) => [{ Plan: node }]

describe("hypopgFact", () => {
  test("says nothing when the extension can be used", () => {
    expect(hypopgFact({ state: "ready", version: "1.4.1" })).toBeUndefined()
  })
  test("names the extension when it is missing, as a fact", () => {
    expect(hypopgFact({ state: "not_installed" })).toBe(
      "HypoPG (the hypopg extension) is not installed on this server.",
    )
    expect(hypopgFact({ state: "not_created" })).toContain("not created in this database")
  })
})

describe("columns", () => {
  test("plain names are bare and the rest are quoted", () => {
    expect(columnsText(["status", "total"])).toBe("status, total")
    expect(columnsText(["Order Id", 'we"ird', "a,b"])).toBe('"Order Id", "we""ird", "a,b"')
  })
  test("the field's text reads back to the names", () => {
    expect(parseColumns("status, total")).toEqual(["status", "total"])
    expect(parseColumns("  status ,, total ,")).toEqual(["status", "total"])
    expect(parseColumns('"Order Id" , "a,b", "we""ird"')).toEqual(["Order Id", "a,b", 'we"ird'])
    expect(parseColumns("")).toEqual([])
    expect(parseColumns("   ")).toEqual([])
  })
  test("what columnsText writes parses back to the same names", () => {
    const names = ["status", "Order Id", 'we"ird', "a,b", "total"]
    expect(parseColumns(columnsText(names))).toEqual(names)
  })
})

describe("candidates", () => {
  const candidate = {
    schema: "public",
    table: "orders",
    columns: ["status", "total"],
    uses: ["equality", "range"],
  }
  test("each column says what the plan does with it", () => {
    expect(usesText(candidate)).toBe("status equals · total range")
  })
  test("a candidate is told apart by its table and columns", () => {
    expect(candidateKey(candidate)).toBe("public.orders(status,total)")
  })
})

describe("costChange", () => {
  test("a cheaper plan falls, a dearer one rises", () => {
    expect(costChange(2236, 1180)).toEqual({ saved: (2236 - 1180) / 2236, words: "falls by 47%" })
    expect(costChange(100, 112).words).toBe("rises by 12%")
  })
  test("less than half a percent is no change", () => {
    expect(costChange(2236, 2230)).toEqual({ saved: 0, words: "does not change" })
    expect(costChange(0, 5).words).toBe("does not change")
  })
  test("costs print with the precision a planner has", () => {
    expect(costText(2236.4)).toBe("2,236")
    expect(costText(12.3456)).toBe("12.35")
  })
})

describe("stepLines", () => {
  const before = plan({
    "Node Type": "Seq Scan",
    "Relation Name": "orders",
    Alias: "o",
    "Total Cost": 2236,
  })
  const after = plan({
    "Node Type": "Limit",
    "Total Cost": 20,
    Plans: [
      {
        "Node Type": "Index Scan",
        "Relation Name": "orders",
        Alias: "o",
        "Index Name": "<13551>btree_orders_status_total",
        "Total Cost": 20,
      },
    ],
  })
  test("lists the steps top down with their depth", () => {
    expect(stepLines(before)).toEqual([
      { depth: 0, text: "Seq Scan orders o", hypothetical: false },
    ])
    expect(stepLines(after).map((line) => [line.depth, line.text])).toEqual([
      [0, "Limit"],
      [1, "Index Scan orders o using <13551>btree_orders_status_total"],
    ])
  })
  test("marks the step that reads the hypothetical index", () => {
    const lines = stepLines(after, "<13551>btree_orders_status_total")
    expect(lines.map((line) => line.hypothetical)).toEqual([false, true])
  })
  test("a document that is not a plan has no steps", () => {
    expect(stepLines(null)).toEqual([])
    expect(stepLines({ nothing: true })).toEqual([])
  })
})

describe("missing indexes", () => {
  test("keys lead and included columns follow", () => {
    expect(
      missingColumnsText({
        equality: ["customer_id"],
        inequality: ["total"],
        include: ["Order Id"],
      }),
    ).toBe('customer_id, total · includes "Order Id"')
    expect(missingColumnsText({ equality: ["a"], inequality: [], include: [] })).toBe("a")
  })
})
