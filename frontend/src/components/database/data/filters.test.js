import { describe, expect, test } from "bun:test"
import {
  buildFilter,
  decodeFilters,
  encodeFilters,
  filterLabel,
  filterText,
  filterValues,
  operatorsFor,
  parseList,
  sameFilter,
} from "./filters"

const ALL = [
  "eq",
  "ne",
  "lt",
  "lte",
  "gt",
  "gte",
  "in",
  "not_in",
  "between",
  "contains",
  "not_contains",
  "icontains",
  "prefix",
  "suffix",
  "is_null",
  "not_null",
  "regex",
]
const NO_REGEX = ALL.filter((op) => op !== "regex")

describe("which operators a column is offered", () => {
  test("only what the engine advertises", () => {
    expect(operatorsFor({ kind: "text", nullable: true }, NO_REGEX)).not.toContain("regex")
    expect(operatorsFor({ kind: "text", nullable: true }, ALL)).toContain("regex")
    expect(operatorsFor({ kind: "text", nullable: true }, [])).toEqual([])
  })
  test("a text is searched first and a number compared first", () => {
    expect(operatorsFor({ kind: "text", nullable: true }, ALL)[0]).toBe("contains")
    expect(operatorsFor({ kind: "number", nullable: true }, ALL)[0]).toBe("eq")
  })
  test("a true-or-false is one or the other", () => {
    expect(operatorsFor({ kind: "boolean", nullable: false }, ALL)).toEqual(["eq", "ne"])
  })
  test("a column that cannot hold NULL is not offered the NULL tests", () => {
    expect(operatorsFor({ kind: "number", nullable: false }, ALL)).not.toContain("is_null")
    expect(operatorsFor({ kind: "number", nullable: true }, ALL)).toContain("is_null")
    expect(operatorsFor({ kind: "number" }, ALL)).toContain("not_null")
  })
})

describe("building a filter", () => {
  test("the empty string is a value", () => {
    expect(buildFilter("name", "eq", [""])).toEqual({
      filter: { column: "name", op: "eq", value: "" },
    })
  })
  test("a NULL test carries no value", () => {
    expect(buildFilter("name", "is_null", ["ignored"])).toEqual({
      filter: { column: "name", op: "is_null" },
    })
  })
  test("a range needs both ends, and a list at least one member", () => {
    expect(buildFilter("n", "between", ["1"]).error).toBeDefined()
    expect(buildFilter("n", "between", ["1", ""]).error).toBeDefined()
    expect(buildFilter("n", "between", ["1", "5"]).filter).toEqual({
      column: "n",
      op: "between",
      values: ["1", "5"],
    })
    expect(buildFilter("n", "in", []).error).toBeDefined()
    expect(buildFilter("n", "in", new Array(201).fill("1")).error).toBeDefined()
  })
  test("a typed list is split on lines, or on commas when it is one line", () => {
    expect(parseList("a, b ,,c")).toEqual(["a", "b", "c"])
    expect(parseList("a, b\nc\n\n")).toEqual(["a, b", "c"])
  })
})

describe("the address", () => {
  test("round-trips every shape and writes only what the server reads", () => {
    const filters = [
      { column: "id", op: "gt", value: "5" },
      { column: "name", op: "eq", value: "" },
      { column: "tier", op: "in", values: ["free", "plus"] },
      { column: "at", op: "between", values: ["2024-01-01", "2024-02-01"] },
      { column: "seen", op: "is_null", value: "left over" },
    ]
    const raw = encodeFilters(filters)
    expect(JSON.parse(raw)[4]).toEqual({ column: "seen", op: "is_null" })
    expect(decodeFilters(raw)).toEqual([...filters.slice(0, 4), { column: "seen", op: "is_null" }])
    expect(encodeFilters([])).toBe("")
  })
  test("what is not a filter is dropped, not sent", () => {
    expect(decodeFilters("not json")).toEqual([])
    expect(decodeFilters('{"column":"a"}')).toEqual([])
    expect(
      decodeFilters(
        JSON.stringify([
          { column: "a", op: "eq", value: "1" },
          { column: "", op: "eq", value: "1" },
          { column: "b", op: "drop table", value: "1" },
          { column: "c", op: "between", values: ["1"] },
          null,
          { column: "d", op: "eq", value: 7 },
          { column: "e", op: "in", value: "solo" },
        ]),
      ),
    ).toEqual([
      { column: "a", op: "eq", value: "1" },
      { column: "d", op: "eq", value: "7" },
      { column: "e", op: "in", values: ["solo"] },
    ])
  })
  test("no more conditions than one browse takes", () => {
    const many = Array.from({ length: 20 }, (_, i) => ({ column: `c${i}`, op: "eq", value: "1" }))
    expect(decodeFilters(JSON.stringify(many))).toHaveLength(12)
  })
})

describe("a filter as a chip says it", () => {
  test("signs for comparisons, words for the rest", () => {
    expect(filterLabel({ column: "total", op: "gte", value: "5000" }, "number")).toBe(
      "total ≥ 5000",
    )
    expect(filterLabel({ column: "email", op: "contains", value: "@acme" })).toBe(
      'email contains "@acme"',
    )
    expect(filterLabel({ column: "seen", op: "not_null" })).toBe("seen is not NULL")
  })
  test("the empty string can be seen", () => {
    expect(filterLabel({ column: "name", op: "eq", value: "" })).toBe('name = ""')
    expect(filterLabel({ column: "n", op: "eq", value: "" }, "number")).toBe('n = ""')
  })
  test("a true-or-false reads as the word although it travels as a digit", () => {
    expect(filterLabel({ column: "ok", op: "eq", value: "1" }, "boolean")).toBe("ok = true")
    expect(filterLabel({ column: "ok", op: "ne", value: "0" }, "boolean")).toBe("ok ≠ false")
  })
  test("a long list is counted, a range names both ends", () => {
    expect(
      filterLabel({ column: "id", op: "in", values: ["1", "2", "3", "4", "5"] }, "number"),
    ).toBe("id is one of 1, 2, 3 +2")
    expect(filterLabel({ column: "n", op: "between", values: ["1", "9"] }, "number")).toBe(
      "n is between 1 and 9",
    )
  })
})

describe("filtering by a cell", () => {
  test("a true-or-false goes as 1 or 0, which every engine reads as one", () => {
    expect(filterText(true)).toBe("1")
    expect(filterText(false)).toBe("0")
  })
  test("digits stay digits and text stays text", () => {
    expect(filterText("9007199254740993")).toBe("9007199254740993")
    expect(filterText(1.5)).toBe("1.5")
    expect(filterText("")).toBe("")
  })
  test("a preview and a structure cannot be compared", () => {
    expect(filterText("\\xabab… (4096 bytes)")).toBeNull()
    expect(filterText({ a: 1 })).toBeNull()
    expect(filterText(null)).toBeNull()
  })
})

test("two filters are the same when they ask the same thing", () => {
  expect(
    sameFilter({ column: "a", op: "in", value: "1" }, { column: "a", op: "in", values: ["1"] }),
  ).toBe(true)
  expect(
    sameFilter({ column: "a", op: "eq", value: "1" }, { column: "a", op: "ne", value: "1" }),
  ).toBe(false)
  expect(filterValues({ column: "a", op: "is_null", value: "x" })).toEqual([])
})
