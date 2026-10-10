import { describe, expect, test } from "bun:test"
import {
  clampWidth,
  defaultWidth,
  displayOrder,
  dropTarget,
  EMPTY_LAYOUT,
  fitWidth,
  isLayout,
  MAX_AUTO_WIDTH,
  MAX_COLUMN_WIDTH,
  MIN_COLUMN_WIDTH,
  moveColumn,
  pruneLayout,
  resetColumnWidth,
  resolveColumns,
  setColumnHidden,
  setColumnPinned,
  setColumnWidth,
} from "./layout"
import { cycleSort, findIndexes, setSort, sortedIndexes, sortState } from "./sort"
import { columnSlice, offsetsOf, revealOffset, rowSlice, sameSlice } from "./window"

const column = (key, kind = "text", extra = {}) => ({
  key,
  name: key,
  typeName: kind,
  kind,
  ...extra,
})
const COLUMNS = [
  column("id", "number"),
  column("email"),
  column("tier", "enum"),
  column("created", "datetime"),
]
const keys = (resolved) => resolved.map((entry) => entry.column.key)

describe("column widths", () => {
  test("a width is held between the narrowest a column can be read at and the widest it may be dragged", () => {
    expect(clampWidth(10)).toBe(MIN_COLUMN_WIDTH)
    expect(clampWidth(5000)).toBe(MAX_COLUMN_WIDTH)
    expect(clampWidth(180.6)).toBe(181)
    expect(clampWidth(NaN)).toBe(MIN_COLUMN_WIDTH)
  })

  test("a column opens wide enough for its kind and for its own header", () => {
    expect(defaultWidth(column("id", "number"))).toBe(112)
    expect(defaultWidth(column("external_id", "uuid"))).toBe(292)
    expect(defaultWidth(column("x", "boolean", { width: 300 }))).toBe(300)
    const long = column("a_really_long_column_name_for_a_flag", "boolean")
    expect(defaultWidth(long)).toBeGreaterThan(defaultWidth(column("flag", "boolean")))
    expect(defaultWidth(column("x".repeat(400)))).toBe(MAX_AUTO_WIDTH)
  })

  test("fitting takes the widest text plus padding, never less than the header, never past the cap", () => {
    const measure = (text) => text.length * 7
    expect(fitWidth(["ab", "abcdefghij"], measure, { padding: 16, header: 60 })).toBe(86)
    expect(fitWidth(["ab"], measure, { padding: 16, header: 120 })).toBe(120)
    expect(fitWidth([], measure, { padding: 16, header: 10 })).toBe(MIN_COLUMN_WIDTH)
    expect(fitWidth(["x".repeat(500)], measure, { padding: 16, header: 60 })).toBe(MAX_AUTO_WIDTH)
  })

  test("setting and resetting a width touches only that column", () => {
    const wide = setColumnWidth(EMPTY_LAYOUT, "email", 420)
    expect(wide.widths).toEqual({ email: 420 })
    expect(setColumnWidth(wide, "id", 5).widths).toEqual({ email: 420, id: MIN_COLUMN_WIDTH })
    expect(resetColumnWidth(wide, "email").widths).toEqual({})
    expect(resetColumnWidth(wide, "id")).toBe(wide)
    expect(EMPTY_LAYOUT.widths).toEqual({})
  })
})

describe("which columns are drawn, and where", () => {
  test("with no layout the table's own order stands, each column at its default width", () => {
    const resolved = resolveColumns(COLUMNS, EMPTY_LAYOUT)
    expect(keys(resolved)).toEqual(["id", "email", "tier", "created"])
    expect(resolved.map((entry) => entry.source)).toEqual([0, 1, 2, 3])
    expect(resolved.map((entry) => entry.index)).toEqual([0, 1, 2, 3])
    expect(resolved.map((entry) => entry.offset)).toEqual([0, 112, 320, 452])
  })

  test("order, hidden and pinned are applied together, and each column still knows where its value is", () => {
    const layout = {
      order: ["created", "tier", "email", "id"],
      widths: { tier: 100 },
      hidden: ["email"],
      pinned: ["id"],
    }
    const resolved = resolveColumns(COLUMNS, layout)
    expect(keys(resolved)).toEqual(["id", "created", "tier"])
    expect(resolved.map((entry) => entry.source)).toEqual([0, 3, 2])
    expect(resolved.map((entry) => entry.pinned)).toEqual([true, false, false])
    expect(resolved[2].width).toBe(100)
    expect(resolved[2].offset).toBe(resolved[0].width + resolved[1].width)
  })

  test("a column the layout has never heard of follows the ones it names", () => {
    const layout = { ...EMPTY_LAYOUT, order: ["tier", "id"] }
    expect(keys(resolveColumns(COLUMNS, layout))).toEqual(["tier", "id", "email", "created"])
    expect(displayOrder(COLUMNS, layout)).toEqual(["tier", "id", "email", "created"])
  })

  test("a layout naming columns that are gone is harmless, and can be pruned before it is stored", () => {
    const stale = {
      order: ["dropped", "email", "email", "id"],
      widths: { dropped: 300, email: 250 },
      hidden: ["dropped"],
      pinned: ["dropped", "id"],
    }
    expect(keys(resolveColumns(COLUMNS, stale))).toEqual(["id", "email", "tier", "created"])
    expect(displayOrder(COLUMNS, stale)).toEqual(["email", "id", "tier", "created"])
    expect(pruneLayout(COLUMNS, stale)).toEqual({
      order: ["email", "email", "id"],
      widths: { email: 250 },
      hidden: [],
      pinned: ["id"],
    })
  })

  test("hiding and pinning are idempotent", () => {
    const hidden = setColumnHidden(EMPTY_LAYOUT, "tier", true)
    expect(hidden.hidden).toEqual(["tier"])
    expect(setColumnHidden(hidden, "tier", true)).toBe(hidden)
    expect(setColumnHidden(hidden, "tier", false).hidden).toEqual([])
    const pinned = setColumnPinned(EMPTY_LAYOUT, "id", true)
    expect(setColumnPinned(pinned, "id", true)).toBe(pinned)
    expect(setColumnPinned(pinned, "id", false).pinned).toEqual([])
  })
})

describe("moving a column", () => {
  test("it lands before the column it was dropped on, or last", () => {
    expect(moveColumn(COLUMNS, EMPTY_LAYOUT, "created", "email").order).toEqual([
      "id",
      "created",
      "email",
      "tier",
    ])
    expect(moveColumn(COLUMNS, EMPTY_LAYOUT, "id", null).order).toEqual([
      "email",
      "tier",
      "created",
      "id",
    ])
    expect(moveColumn(COLUMNS, EMPTY_LAYOUT, "id", "id")).toBe(EMPTY_LAYOUT)
  })

  test("a column dropped among the pinned ones is pinned, and one dragged out is released", () => {
    const layout = { ...EMPTY_LAYOUT, pinned: ["id"] }
    const into = moveColumn(COLUMNS, layout, "tier", "id")
    expect(into.pinned).toEqual(["tier", "id"])
    expect(keys(resolveColumns(COLUMNS, into))).toEqual(["tier", "id", "email", "created"])
    const out = moveColumn(COLUMNS, into, "id", "created")
    expect(out.pinned).toEqual(["tier"])
    expect(keys(resolveColumns(COLUMNS, out))).toEqual(["tier", "email", "id", "created"])
  })

  test("the drop goes before whichever column's middle the pointer has not passed", () => {
    const resolved = resolveColumns(COLUMNS, EMPTY_LAYOUT)
    expect(dropTarget(resolved, 0)).toBe("id")
    expect(dropTarget(resolved, 55)).toBe("id")
    expect(dropTarget(resolved, 57)).toBe("email")
    expect(dropTarget(resolved, 330)).toBe("tier")
    expect(dropTarget(resolved, 5000)).toBeNull()
  })
})

describe("a layout that came out of storage", () => {
  test("the right shape is accepted and anything else is not", () => {
    expect(isLayout(EMPTY_LAYOUT)).toBe(true)
    expect(isLayout({ order: ["a"], widths: { a: 100 }, hidden: [], pinned: ["a"] })).toBe(true)
    expect(isLayout(null)).toBe(false)
    expect(isLayout({ order: [], hidden: [], pinned: [] })).toBe(false)
    expect(isLayout({ order: [1], widths: {}, hidden: [], pinned: [] })).toBe(false)
    expect(isLayout({ order: [], widths: { a: "wide" }, hidden: [], pinned: [] })).toBe(false)
  })
})

describe("which rows are drawn", () => {
  test("the slice is what the viewport shows plus the overscan on both sides", () => {
    expect(
      rowSlice({ scrollTop: 0, viewport: 300, rowHeight: 30, count: 1000, overscan: 5 }),
    ).toEqual({
      start: 0,
      end: 15,
    })
    expect(
      rowSlice({ scrollTop: 3000, viewport: 300, rowHeight: 30, count: 1000, overscan: 5 }),
    ).toEqual({ start: 95, end: 115 })
    expect(
      rowSlice({ scrollTop: 3015, viewport: 300, rowHeight: 30, count: 1000, overscan: 0 }),
    ).toEqual({ start: 100, end: 111 })
  })

  test("it never runs past either end, however far the scroll position is", () => {
    expect(
      rowSlice({ scrollTop: 29_900, viewport: 300, rowHeight: 30, count: 1000, overscan: 5 }),
    ).toEqual({ start: 991, end: 1000 })
    expect(
      rowSlice({ scrollTop: 90_000, viewport: 300, rowHeight: 30, count: 1000, overscan: 5 }),
    ).toEqual({ start: 1000, end: 1000 })
    expect(
      rowSlice({ scrollTop: -50, viewport: 300, rowHeight: 30, count: 3, overscan: 5 }),
    ).toEqual({
      start: 0,
      end: 3,
    })
    expect(rowSlice({ scrollTop: 0, viewport: 300, rowHeight: 30, count: 0, overscan: 5 })).toEqual(
      {
        start: 0,
        end: 0,
      },
    )
  })

  test("a thousand rows are drawn a screenful at a time", () => {
    const slice = rowSlice({
      scrollTop: 12_345,
      viewport: 720,
      rowHeight: 30,
      count: 1000,
      overscan: 8,
    })
    expect(slice.end - slice.start).toBeLessThanOrEqual(24 + 1 + 16)
    expect(slice.start * 30).toBeLessThanOrEqual(12_345)
    expect(slice.end * 30).toBeGreaterThanOrEqual(12_345 + 720)
  })
})

describe("which columns are drawn", () => {
  const offsets = offsetsOf([100, 200, 50, 150, 300, 120])

  test("running totals start at zero and end at the full width", () => {
    expect(offsets).toEqual([0, 100, 300, 350, 500, 800, 920])
    expect(offsetsOf([])).toEqual([0])
  })

  test("the slice covers the columns the viewport touches, plus the overscan", () => {
    expect(columnSlice({ scrollLeft: 0, viewport: 320, offsets, overscan: 0 })).toEqual({
      start: 0,
      end: 3,
    })
    expect(columnSlice({ scrollLeft: 310, viewport: 100, offsets, overscan: 0 })).toEqual({
      start: 2,
      end: 4,
    })
    expect(columnSlice({ scrollLeft: 310, viewport: 100, offsets, overscan: 1 })).toEqual({
      start: 1,
      end: 5,
    })
    expect(columnSlice({ scrollLeft: 300, viewport: 50, offsets, overscan: 0 })).toEqual({
      start: 2,
      end: 3,
    })
  })

  test("it is clamped at both ends and empty when there is nothing to scroll", () => {
    expect(columnSlice({ scrollLeft: 5000, viewport: 400, offsets, overscan: 2 })).toEqual({
      start: 3,
      end: 6,
    })
    expect(columnSlice({ scrollLeft: -20, viewport: 0, offsets, overscan: 0 })).toEqual({
      start: 0,
      end: 1,
    })
    expect(columnSlice({ scrollLeft: 0, viewport: 400, offsets: [0], overscan: 2 })).toEqual({
      start: 0,
      end: 0,
    })
    expect(sameSlice({ start: 1, end: 4 }, { start: 1, end: 4 })).toBe(true)
    expect(sameSlice({ start: 1, end: 4 }, { start: 1, end: 5 })).toBe(false)
  })

  test("sixty columns are drawn a screenful at a time", () => {
    const wide = offsetsOf(Array.from({ length: 60 }, () => 160))
    const slice = columnSlice({ scrollLeft: 4000, viewport: 1100, offsets: wide, overscan: 2 })
    expect(slice).toEqual({ start: 23, end: 34 })
  })
})

describe("bringing a cell into view", () => {
  test("a cell already showing scrolls nothing", () => {
    expect(revealOffset({ start: 300, end: 330, scroll: 200, viewport: 400, lead: 36 })).toBe(200)
  })

  test("a cell above the fold is brought to the top, clear of the sticky lead", () => {
    expect(revealOffset({ start: 90, end: 120, scroll: 200, viewport: 400, lead: 36 })).toBe(90)
    expect(revealOffset({ start: 0, end: 30, scroll: 200, viewport: 400, lead: 36 })).toBe(0)
  })

  test("a cell below the fold is brought just inside the bottom", () => {
    expect(revealOffset({ start: 600, end: 630, scroll: 200, viewport: 400, lead: 36 })).toBe(266)
  })

  test("a span larger than the room shows its start", () => {
    expect(revealOffset({ start: 1000, end: 2000, scroll: 0, viewport: 400, lead: 100 })).toBe(1000)
  })
})

describe("sort keys", () => {
  test("a click cycles one column: ascending, descending, off", () => {
    const asc = cycleSort([], "email", false)
    expect(asc).toEqual([{ column: "email", desc: false }])
    const desc = cycleSort(asc, "email", false)
    expect(desc).toEqual([{ column: "email", desc: true }])
    expect(cycleSort(desc, "email", false)).toEqual([])
    expect(cycleSort(desc, "id", false)).toEqual([{ column: "id", desc: false }])
  })

  test("with Shift a column joins the keys, cycles where it stands, and drops out without disturbing the rest", () => {
    let sort = cycleSort([{ column: "tier", desc: false }], "created", true)
    expect(sort).toEqual([
      { column: "tier", desc: false },
      { column: "created", desc: false },
    ])
    sort = cycleSort(sort, "tier", true)
    expect(sort).toEqual([
      { column: "tier", desc: true },
      { column: "created", desc: false },
    ])
    sort = cycleSort(sort, "tier", true)
    expect(sort).toEqual([{ column: "created", desc: false }])
  })

  test("a column knows its direction and, among several keys, its rank", () => {
    const sort = [
      { column: "tier", desc: true },
      { column: "created", desc: false },
    ]
    expect(sortState(sort, "created")).toEqual({ desc: false, order: 2 })
    expect(sortState(sort, "id")).toBeNull()
    expect(sortState([{ column: "id", desc: true }], "id")).toEqual({ desc: true, order: null })
  })

  test("a menu sets a direction outright, alone or as one more key", () => {
    const sort = [{ column: "tier", desc: false }]
    expect(setSort(sort, "id", true, false)).toEqual([{ column: "id", desc: true }])
    expect(setSort(sort, "id", true, true)).toEqual([
      { column: "tier", desc: false },
      { column: "id", desc: true },
    ])
    expect(setSort(sort, "tier", true, true)).toEqual([{ column: "tier", desc: true }])
    expect(setSort(sort, "tier", null, false)).toEqual([])
  })
})

describe("a result held in memory", () => {
  const columns = [column("0", "number"), column("1", "text"), column("2", "number")]
  const rows = [
    ["10", "b", null],
    ["9", "a", "2"],
    ["9007199254740993", "a", "1"],
    ["9007199254740992", null, "1"],
  ]

  test("it sorts by several keys, exactly, and stably", () => {
    expect(sortedIndexes(rows, columns, [{ column: "0", desc: false }])).toEqual([1, 0, 3, 2])
    expect(sortedIndexes(rows, columns, [{ column: "0", desc: true }])).toEqual([2, 3, 0, 1])
    expect(
      sortedIndexes(rows, columns, [
        { column: "1", desc: false },
        { column: "0", desc: true },
      ]),
    ).toEqual([2, 1, 0, 3])
    expect(sortedIndexes(rows, columns, [{ column: "2", desc: false }])).toEqual([2, 3, 1, 0])
  })

  test("NULL sorts last in both directions, and an unknown key sorts nothing", () => {
    expect(sortedIndexes(rows, columns, [{ column: "2", desc: true }])).toEqual([1, 2, 3, 0])
    expect(sortedIndexes(rows, columns, [{ column: "missing", desc: false }])).toEqual([0, 1, 2, 3])
    expect(sortedIndexes(rows, columns, [])).toEqual([0, 1, 2, 3])
  })

  test("two columns with one name are told apart by their keys", () => {
    const joined = [column("0", "number", { name: "id" }), column("1", "number", { name: "id" })]
    const data = [
      ["1", "9"],
      ["2", "3"],
    ]
    expect(sortedIndexes(data, joined, [{ column: "1", desc: false }])).toEqual([1, 0])
  })

  test("a find keeps the rows that hold the text anywhere, whatever its case", () => {
    expect(findIndexes(rows, "A")).toEqual([1, 2])
    expect(findIndexes(rows, "900719")).toEqual([2, 3])
    expect(findIndexes(rows, "  ")).toEqual([0, 1, 2, 3])
    expect(findIndexes(rows, "nothing")).toEqual([])
    expect(
      sortedIndexes(rows, columns, [{ column: "0", desc: true }], findIndexes(rows, "a")),
    ).toEqual([2, 1])
  })
})
