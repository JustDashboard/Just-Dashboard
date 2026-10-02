import { describe, expect, test } from "bun:test"
import { INSPECTOR, RAIL, ROWS_BESIDE_PANEL, ROWS_BESIDE_RAIL, arrange } from "./panes"

// The editor's frame at a window width, with the dashboard's sidebar open:
// 248 of sidebar and 12 of gutter each side.
const frameAt = (window) => window - 248 - 24

describe("how the rail, the rows and the row panel share the frame", () => {
  test("a wide window holds all three, at the widths asked for", () => {
    const panes = arrange(frameAt(1720), RAIL.fallback, INSPECTOR.fallback, true)
    expect(panes.rail).toBe("beside")
    expect(panes.inspector).toBe("beside")
    expect(panes.inspectorWidth).toBe(INSPECTOR.fallback)
  })
  test("at 1280 the rail steps aside for the row panel, and the rows keep their floor", () => {
    const frame = frameAt(1280)
    const panes = arrange(frame, RAIL.fallback, INSPECTOR.fallback, true)
    expect(panes.rail).toBe("beside")
    expect(panes.inspector).toBe("alone")
    expect(frame - panes.inspectorWidth).toBeGreaterThanOrEqual(ROWS_BESIDE_PANEL)
  })
  test("at 1024 the rail stays a column and the row panel lies over the table", () => {
    const panes = arrange(frameAt(1024), RAIL.fallback, INSPECTOR.fallback, true)
    expect(panes.rail).toBe("beside")
    expect(panes.inspector).toBe("over")
  })
  test("on a phone both lie over the table", () => {
    const panes = arrange(366, RAIL.fallback, INSPECTOR.fallback, true)
    expect(panes.rail).toBe("over")
    expect(panes.inspector).toBe("over")
  })
  test("a rail the reader has hidden takes nothing from the row panel", () => {
    const frame = frameAt(1280)
    expect(arrange(frame, RAIL.fallback, INSPECTOR.fallback, false).inspector).toBe("beside")
  })
  test("the row panel is drawn narrower than asked rather than take the rows under their floor", () => {
    const frame = RAIL.fallback + ROWS_BESIDE_PANEL + 300
    const panes = arrange(frame, RAIL.fallback, 600, true)
    expect(panes.inspector).toBe("beside")
    expect(panes.inspectorWidth).toBe(300)
    expect(panes.inspectorMax).toBe(300)
  })
  test("the rail cannot be dragged to where the row panel would have to give way", () => {
    const frame = frameAt(1720)
    const panes = arrange(frame, RAIL.fallback, INSPECTOR.fallback, true)
    expect(panes.railMax).toBe(Math.min(RAIL.max, frame - ROWS_BESIDE_PANEL - INSPECTOR.min))
    const dragged = arrange(frame, panes.railMax, INSPECTOR.fallback, true)
    expect(dragged.inspector).toBe("beside")
  })
  test("a rail kept wider than the frame allows lies over the table", () => {
    const frame = RAIL.max + ROWS_BESIDE_RAIL - 1
    expect(arrange(frame, RAIL.max, INSPECTOR.fallback, true).rail).toBe("over")
    expect(arrange(frame, RAIL.fallback, INSPECTOR.fallback, true).rail).toBe("beside")
  })
  test("widths out of range are read as the nearest allowed", () => {
    const panes = arrange(frameAt(1720), 10, 5000, true)
    expect(panes.rail).toBe("beside")
    expect(panes.inspectorWidth).toBeLessThanOrEqual(INSPECTOR.max)
  })
})
