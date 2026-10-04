import { describe, expect, test } from "bun:test"
import {
  SPLIT_GAP,
  canSplit,
  layoutGeometry,
  leaves,
  neighbour,
  reconcileLayouts,
  resizeLayout,
  splitLayout,
} from "./terminal-layout"

const window = (id) => ({ window: id })

describe("terminal split layouts", () => {
  test.each(["left", "right", "up", "down"])(
    "%s places a new terminal on the requested side",
    (direction) => {
      const tree = splitLayout(window("shell"), "shell", "new", direction, "split")
      const { panes } = layoutGeometry(tree, 1000, 800)
      const original = panes.shell
      const added = panes.new
      expect(leaves(tree)).toEqual(
        direction === "left" || direction === "up" ? ["new", "shell"] : ["shell", "new"],
      )
      if (direction === "left" || direction === "right") {
        expect(added.y).toBe(original.y)
        expect(added.height).toBe(original.height)
        expect(direction === "left" ? added.x < original.x : added.x > original.x).toBe(true)
      } else {
        expect(added.x).toBe(original.x)
        expect(added.width).toBe(original.width)
        expect(direction === "up" ? added.y < original.y : added.y > original.y).toBe(true)
      }
    },
  )

  test("nested splits leave the other terminal's rectangle unchanged", () => {
    const sideBySide = splitLayout(window("a"), "a", "b", "right", "columns")
    const before = layoutGeometry(sideBySide, 1000, 800)
    const nested = splitLayout(sideBySide, "b", "c", "down", "rows")
    const after = layoutGeometry(nested, 1000, 800)
    expect(after.panes.a).toEqual(before.panes.a)
    expect(after.panes.b.x).toBe(before.panes.b.x)
    expect(after.panes.c.x).toBe(before.panes.b.x)
    expect(after.panes.b.height + after.panes.c.height + SPLIT_GAP).toBe(before.panes.b.height)
    expect(leaves(nested)).toEqual(["a", "b", "c"])
    expect(leaves(sideBySide)).toEqual(["a", "b"])
  })

  test("resizing one nested boundary keeps its siblings and the outer boundary intact", () => {
    const tree = splitLayout(
      splitLayout(window("a"), "a", "b", "right", "columns"),
      "b",
      "c",
      "down",
      "rows",
    )
    const before = layoutGeometry(tree, 1200, 800)
    const resized = resizeLayout(tree, "rows", 0.7)
    const after = layoutGeometry(resized, 1200, 800)
    expect(after.panes.a).toEqual(before.panes.a)
    expect(after.dividers.find((divider) => divider.id === "columns")).toEqual(
      before.dividers.find((divider) => divider.id === "columns"),
    )
    expect(after.panes.b.height).toBeGreaterThan(before.panes.b.height)
    expect(after.panes.c.height).toBeLessThan(before.panes.c.height)
    expect(tree.second.ratio).toBe(0.5)
  })

  test("a dragged boundary cannot starve a subtree that contains another split", () => {
    let tree = splitLayout(window("a"), "a", "b", "right", "outer")
    tree = splitLayout(tree, "a", "c", "left", "inner")
    tree = resizeLayout(tree, "outer", 0.01)
    const { panes, dividers } = layoutGeometry(tree, 1000, 500)
    expect(panes.a.width).toBeGreaterThanOrEqual(160)
    expect(panes.c.width).toBeGreaterThanOrEqual(160)
    expect(panes.b.width).toBeGreaterThanOrEqual(160)
    const outer = dividers.find((divider) => divider.id === "outer")
    expect(outer.ratio).toBeGreaterThanOrEqual(outer.min)
    expect(outer.ratio).toBeLessThanOrEqual(outer.max)
  })

  test.each([
    [1000, 800],
    [320, 240],
    [100, 80],
    [0, 0],
    [-10, -20],
  ])("nested terminals stay inside a %s × %s viewport", (width, height) => {
    let tree = splitLayout(window("a"), "a", "b", "right", "columns")
    tree = splitLayout(tree, "b", "c", "down", "rows")
    tree = splitLayout(tree, "a", "d", "up", "other-rows")
    const geometry = layoutGeometry(tree, width, height)
    const rectangles = [...Object.values(geometry.panes), ...geometry.dividers]
    for (const rect of rectangles) {
      expect(Number.isFinite(rect.width)).toBe(true)
      expect(Number.isFinite(rect.height)).toBe(true)
      expect(rect.width).toBeGreaterThanOrEqual(0)
      expect(rect.height).toBeGreaterThanOrEqual(0)
      expect(rect.x).toBeGreaterThanOrEqual(0)
      expect(rect.y).toBeGreaterThanOrEqual(0)
      expect(rect.x + rect.width).toBeLessThanOrEqual(Math.max(0, width) + 0.00001)
      expect(rect.y + rect.height).toBeLessThanOrEqual(Math.max(0, height) + 0.00001)
    }
    const totalArea = rectangles.reduce((total, rect) => total + rect.width * rect.height, 0)
    expect(totalArea).toBeCloseTo(Math.max(0, width) * Math.max(0, height), 5)
  })

  test("split availability uses the chosen axis and includes the divider", () => {
    const rect = { x: 0, y: 0, width: 326, height: 246 }
    for (const direction of ["left", "right", "up", "down"]) {
      expect(canSplit(rect, direction)).toBe(true)
      expect(canSplit(undefined, direction)).toBe(false)
    }
    expect(canSplit({ ...rect, width: 325 }, "left")).toBe(false)
    expect(canSplit({ ...rect, width: 325 }, "right")).toBe(false)
    expect(canSplit({ ...rect, height: 245 }, "up")).toBe(false)
    expect(canSplit({ ...rect, height: 245 }, "down")).toBe(false)
    expect(canSplit({ ...rect, width: 200 }, "down")).toBe(true)
    expect(canSplit({ ...rect, height: 100 }, "right")).toBe(true)
  })

  test("closing a nested leaf collapses its boundary and preserves the other split", () => {
    const tree = splitLayout(
      splitLayout(window("a"), "a", "b", "right", "columns"),
      "b",
      "c",
      "down",
      "rows",
    )
    const [remaining] = reconcileLayouts([tree], ["a", "c"])
    expect(remaining).toEqual({
      id: "columns",
      axis: "x",
      ratio: 0.5,
      first: window("a"),
      second: window("c"),
    })
    expect(reconcileLayouts([remaining], ["c"])).toEqual([window("c")])
  })

  test("reconciliation retains valid layout order and adds externally created windows", () => {
    const tree = splitLayout(window("b"), "b", "a", "left", "columns")
    expect(reconcileLayouts([tree, window("ended")], ["b", "a", "external"])).toEqual([
      tree,
      window("external"),
    ])
  })

  test("corrupt and duplicated saved entries cannot render a window twice", () => {
    const invalid = [
      null,
      "bad",
      { id: "bad-axis", axis: "z", first: window("b"), second: window("c") },
      { id: "split", axis: "x", ratio: Infinity, first: window("a"), second: window("a") },
      { id: "split", axis: "y", first: window("b"), second: window("c") },
      window("a"),
    ]
    const groups = reconcileLayouts(invalid, ["a", "b", "c"])
    expect(groups.flatMap(leaves)).toEqual(["a", "b", "c"])
    expect(reconcileLayouts({}, ["a", "b"])).toEqual([window("a"), window("b")])
  })

  test("saved non-finite ratios recover a balanced layout and extreme ratios stay bounded", () => {
    const tree = splitLayout(window("a"), "a", "b", "right", "split")
    expect(reconcileLayouts([{ ...tree, ratio: NaN }], ["a", "b"])[0].ratio).toBe(0.5)
    expect(reconcileLayouts([{ ...tree, ratio: -10 }], ["a", "b"])[0].ratio).toBe(0.05)
    expect(reconcileLayouts([{ ...tree, ratio: 10 }], ["a", "b"])[0].ratio).toBe(0.95)
  })

  test("cyclic or excessively deep stored data terminates and recovers available windows", () => {
    const cyclic = { id: "cycle", axis: "x", ratio: 0.5, second: window("a") }
    cyclic.first = cyclic
    const groups = reconcileLayouts([cyclic], ["a", "b"])
    expect(groups.flatMap(leaves)).toEqual(["a", "b"])
    let deep = window("b")
    for (let depth = 0; depth < 100; depth++) {
      deep = {
        id: `level-${depth}`,
        axis: "x",
        ratio: 0.5,
        first: deep,
        second: window(depth === 99 ? "a" : "missing"),
      }
    }
    expect(reconcileLayouts([deep], ["a", "b"]).flatMap(leaves)).toEqual(["a", "b"])
  })

  test("directional focus selects the aligned terminal in a mixed layout", () => {
    let tree = splitLayout(window("left"), "left", "top-right", "right", "columns")
    tree = splitLayout(tree, "top-right", "bottom-right", "down", "rows")
    const { panes } = layoutGeometry(tree, 1000, 800)
    expect(neighbour(panes, "left", "right")).toBe("top-right")
    expect(neighbour(panes, "top-right", "down")).toBe("bottom-right")
    expect(neighbour(panes, "bottom-right", "up")).toBe("top-right")
    expect(neighbour(panes, "bottom-right", "left")).toBe("left")
    expect(neighbour(panes, "left", "left")).toBeUndefined()
    expect(neighbour(panes, "missing", "right")).toBeUndefined()
  })

  test("directional focus prefers an aligned pane over a nearer diagonal pane", () => {
    const panes = {
      active: { x: 0, y: 0, width: 200, height: 200 },
      diagonal: { x: 206, y: 206, width: 200, height: 200 },
      aligned: { x: 900, y: 0, width: 200, height: 200 },
    }
    expect(neighbour(panes, "active", "right")).toBe("aligned")
  })
})
