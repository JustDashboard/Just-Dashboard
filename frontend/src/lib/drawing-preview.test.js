import { describe, expect, test } from "bun:test"
import {
  arrowhead,
  cornerOf,
  extentOf,
  faceOf,
  frameOf,
  linePath,
  shapePath,
} from "./drawing-preview"

const rect = (extra = {}) => ({ type: "rectangle", x: 10, y: 20, w: 200, h: 100, ...extra })

describe("extentOf", () => {
  test("a closed shape is its own box", () => {
    expect(extentOf(rect())).toEqual({ x: 10, y: 20, w: 200, h: 100 })
  })

  test("an arrow drawn up and to the left is measured from its points, not its origin", () => {
    const arrow = {
      type: "arrow",
      x: 100,
      y: 100,
      w: 80,
      h: 60,
      points: [
        [0, 0],
        [-80, -60],
      ],
    }
    expect(extentOf(arrow)).toEqual({ x: 20, y: 40, w: 80, h: 60 })
  })
})

describe("frameOf", () => {
  test("an empty drawing has no frame", () => {
    expect(frameOf([])).toBeNull()
  })

  test("one small shape sits in a canvas with a floor, centred on it", () => {
    const frame = frameOf([rect({ x: 0, y: 0, w: 40, h: 40 })])
    expect(frame.w).toBe(480)
    expect(frame.h).toBe(270)
    expect(frame.x + frame.w / 2).toBe(20)
    expect(frame.y + frame.h / 2).toBe(20)
  })

  test("a large drawing keeps room on every side", () => {
    const frame = frameOf([rect({ x: 0, y: 0, w: 2000, h: 1000 })])
    expect(frame.x).toBeLessThan(0)
    expect(frame.y).toBeLessThan(0)
    expect(frame.x + frame.w).toBeGreaterThan(2000)
    expect(frame.y + frame.h).toBeGreaterThan(1000)
  })
})

describe("cornerOf", () => {
  test("square corners unless the element is rounded", () => {
    expect(cornerOf(rect())).toBe(0)
  })

  test("a quarter of the shorter side, up to 32 units, as Excalidraw rounds", () => {
    expect(cornerOf(rect({ round: true, w: 200, h: 100 }))).toBe(25)
    expect(cornerOf(rect({ round: true, w: 600, h: 400 }))).toBe(32)
  })
})

describe("linePath", () => {
  const points = [
    [0, 0],
    [50, 20],
    [100, 0],
  ]

  test("straight segments for a sharp line", () => {
    expect(linePath(points, false)).toBe("M0 0L50 20L100 0")
  })

  test("a curve through every point for a rounded one", () => {
    const d = linePath(points, true)
    expect(d.startsWith("M0 0C")).toBe(true)
    expect(d).toContain(" 50 20C")
    expect(d.endsWith(" 100 0")).toBe(true)
  })

  test("two points stay a straight line even when rounded", () => {
    expect(
      linePath(
        [
          [0, 0],
          [10, 10],
        ],
        true,
      ),
    ).toBe("M0 0L10 10")
  })
})

describe("arrowhead", () => {
  test("two wings meeting at the tip, behind it", () => {
    const d = arrowhead([100, 0], [0, 0], 2)
    const [a, tip, b] = d
      .slice(1)
      .split("L")
      .map((pair) => pair.split(" ").map(Number))
    expect(tip).toEqual([100, 0])
    expect(a[0]).toBeLessThan(100)
    expect(b[0]).toBeLessThan(100)
    expect(Math.sign(a[1])).toBe(-Math.sign(b[1]))
  })

  test("never longer than half the segment it ends", () => {
    const d = arrowhead([10, 0], [0, 0], 4)
    const wing = Number(d.slice(1).split(" ")[0])
    expect(wing).toBeGreaterThanOrEqual(5 - 0.1)
  })

  test("nothing for a segment of no length", () => {
    expect(arrowhead([5, 5], [5, 5])).toBe("")
  })
})

describe("shapePath", () => {
  test("a diamond's four points sit on the middles of its box", () => {
    expect(shapePath({ type: "diamond", x: 0, y: 0, w: 100, h: 60 })).toBe(
      "M50 0L100 30L50 60L0 30Z",
    )
  })

  test("an ellipse is two arcs across its box", () => {
    expect(shapePath({ type: "ellipse", x: 0, y: 0, w: 100, h: 60 })).toBe(
      "M0 30A50 30 0 1 0 100 30A50 30 0 1 0 0 30Z",
    )
  })

  test("a box drawn with a negative width is the same box", () => {
    expect(shapePath(rect({ x: 210, w: -200 }))).toBe(shapePath(rect()))
  })
})

describe("faceOf", () => {
  test("Excalidraw's hands, monos and sans by font id", () => {
    expect(faceOf(undefined)).toBe("hand")
    expect(faceOf(1)).toBe("hand")
    expect(faceOf(5)).toBe("hand")
    expect(faceOf(3)).toBe("mono")
    expect(faceOf(8)).toBe("mono")
    expect(faceOf(2)).toBe("sans")
    expect(faceOf(6)).toBe("sans")
  })
})
