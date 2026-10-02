import { describe, expect, test } from "bun:test"
import {
  browseQuery,
  compactCount,
  countKey,
  pageFacts,
  readView,
  readableDuration,
  sameRows,
  viewParams,
} from "./view"

const reader = (query) => (name) => new URLSearchParams(query).get(name) ?? ""

describe("the view an address states", () => {
  test("a bare address is the first page of the rows, unsorted and unfiltered", () => {
    expect(readView(reader(""))).toEqual({
      view: "data",
      filters: [],
      match: "all",
      sort: [],
      page: 1,
    })
  })
  test("everything stated is read", () => {
    const filters = JSON.stringify([{ column: "status", op: "eq", value: "paid" }])
    const sort = JSON.stringify([{ column: "placed_at", desc: true }, { column: "id" }])
    const query = new URLSearchParams({ view: "structure", filters, match: "any", sort, page: "3" })
    expect(readView(reader(query.toString()))).toEqual({
      view: "structure",
      filters: [{ column: "status", op: "eq", value: "paid" }],
      match: "any",
      sort: [
        { column: "placed_at", desc: true },
        { column: "id", desc: false },
      ],
      page: 3,
    })
  })
  test("nonsense is the default, never an error", () => {
    const query = new URLSearchParams({
      view: "nope",
      filters: "{",
      sort: "[1]",
      page: "-4",
      match: "x",
    })
    expect(readView(reader(query.toString()))).toEqual({
      view: "data",
      filters: [],
      match: "all",
      sort: [],
      page: 1,
    })
    expect(readView(reader("page=2.5")).page).toBe(1)
  })
  test("the search page's hand-over is read as the filters", () => {
    const where = JSON.stringify([{ column: "id", op: "eq", value: "7" }])
    expect(readView(reader(new URLSearchParams({ where }).toString())).filters).toEqual([
      { column: "id", op: "eq", value: "7" },
    ])
  })
})

describe("a change to the view as address keys", () => {
  test("a default is cleared, not spelled out", () => {
    expect(viewParams({ view: "data", filters: [], match: "all", sort: [], page: 1 })).toEqual({
      view: null,
      filters: null,
      where: null,
      match: null,
      sort: null,
      page: null,
    })
  })
  test("only what is named is written", () => {
    expect(viewParams({ page: 4 })).toEqual({ page: "4" })
    expect(viewParams({ sort: [{ column: "a", desc: true }] })).toEqual({
      sort: '[{"column":"a","desc":true}]',
    })
  })
  test("what is written is read back", () => {
    const view = {
      view: "definition",
      filters: [{ column: "a", op: "in", values: ["1", "2"] }],
      match: "any",
      sort: [{ column: "b", desc: false }],
      page: 2,
    }
    const params = viewParams(view)
    expect(readView((name) => params[name] ?? "")).toEqual(view)
  })
})

describe("what the browse route is asked", () => {
  test("the page becomes an offset of the size", () => {
    expect(
      browseQuery("public", "orders", { filters: [], match: "all", sort: [], page: 3 }, 300),
    ).toEqual({
      schema: "public",
      table: "orders",
      limit: 300,
      offset: 600,
      filters: undefined,
      match: undefined,
      sort: undefined,
    })
  })
  test("a count is tagged with its table and its conditions, so it cannot land on another", () => {
    const a = countKey("public", "orders", { filters: [], match: "all" })
    expect(countKey("public", "customers", { filters: [], match: "all" })).not.toBe(a)
    expect(
      countKey("public", "orders", { filters: [{ column: "a", op: "is_null" }], match: "all" }),
    ).not.toBe(a)
    expect(countKey("public", "orders", { filters: [], match: "all" })).toBe(a)
  })
})

describe("where a page sits", () => {
  test("a table of exactly one page offers no second", () => {
    const facts = pageFacts({ rowCount: 100, truncated: false, offset: 0, limit: 100 }, null)
    expect(facts).toMatchObject({
      from: 1,
      to: 100,
      hasNext: false,
      hasPrevious: false,
      pastEnd: false,
    })
  })
  test("a page past the last row says so instead of calling the table empty", () => {
    const facts = pageFacts({ rowCount: 0, truncated: false, offset: 300, limit: 100 }, 250)
    expect(facts).toMatchObject({ from: 0, to: 300, pastEnd: true, hasPrevious: true, lastPage: 3 })
  })
  test("an empty table is not past its end", () => {
    expect(pageFacts({ rowCount: 0, truncated: false, offset: 0, limit: 100 }, 0)).toMatchObject({
      pastEnd: false,
      lastPage: 1,
    })
  })
  test("the last page comes from a total, when there is one", () => {
    expect(pageFacts({ rowCount: 50, truncated: true, offset: 50, limit: 50 }, 1204).lastPage).toBe(
      25,
    )
    expect(
      pageFacts({ rowCount: 50, truncated: true, offset: 50, limit: 50 }, null).lastPage,
    ).toBeNull()
  })
})

describe("whether two views show the same rows", () => {
  const plain = { view: "data", filters: [], match: "all", sort: [], page: 1 }
  test("which of the three views is open does not change the rows", () => {
    expect(sameRows(plain, { ...plain, view: "structure" })).toBe(true)
  })
  test("another page, order or condition does", () => {
    expect(sameRows(plain, { ...plain, page: 2 })).toBe(false)
    expect(sameRows(plain, { ...plain, sort: [{ column: "id", desc: true }] })).toBe(false)
    expect(sameRows(plain, { ...plain, filters: [{ column: "a", op: "is_null" }] })).toBe(false)
    const two = [
      { column: "a", op: "is_null" },
      { column: "b", op: "eq", value: "" },
    ]
    expect(sameRows({ ...plain, filters: two }, { ...plain, filters: two, match: "any" })).toBe(
      false,
    )
  })
  test("the same conditions written again are the same rows", () => {
    const filters = [{ column: "b", op: "eq", value: "1" }]
    expect(
      sameRows(
        { ...plain, filters },
        { ...plain, filters: [{ column: "b", op: "eq", value: "1" }] },
      ),
    ).toBe(true)
  })
})

test("figures at the width they are printed", () => {
  expect(compactCount(999)).toBe("999")
  expect(compactCount(1204)).toBe("1.2k")
  expect(compactCount(60000)).toBe("60k")
  expect(compactCount(200000)).toBe("200k")
  expect(compactCount(2_500_000)).toBe("2.5M")
  expect(readableDuration("3.797ms")).toBe("3.8 ms")
  expect(readableDuration("744µs")).toBe("744 µs")
  expect(readableDuration("19.79ms")).toBe("19.8 ms")
  expect(readableDuration("1.2s")).toBe("1.2 s")
  expect(readableDuration("soon")).toBe("soon")
})
