import { describe, expect, test } from "bun:test"
import { MOVE_STEP, PAN_STEP, arrowOf, moved, panned, stepOf } from "./canvas-keys"

const press = (key, mods = {}) => ({ key, ctrlKey: false, metaKey: false, altKey: false, ...mods })

describe("which key is an arrow for the canvas", () => {
  test("the four arrows, alone or with Shift, point their way", () => {
    expect(arrowOf(press("ArrowUp"))).toEqual({ x: 0, y: -1 })
    expect(arrowOf(press("ArrowDown"))).toEqual({ x: 0, y: 1 })
    expect(arrowOf(press("ArrowLeft"))).toEqual({ x: -1, y: 0 })
    expect(arrowOf(press("ArrowRight", { shiftKey: true }))).toEqual({ x: 1, y: 0 })
  })

  test("with Ctrl, ⌘ or Alt an arrow is somebody else's, and other keys are nothing", () => {
    expect(arrowOf(press("ArrowLeft", { ctrlKey: true }))).toBeNull()
    expect(arrowOf(press("ArrowLeft", { metaKey: true }))).toBeNull()
    expect(arrowOf(press("ArrowLeft", { altKey: true }))).toBeNull()
    expect(arrowOf(press("a"))).toBeNull()
    expect(arrowOf(press("Enter"))).toBeNull()
  })

  test("Shift takes the larger step", () => {
    expect(stepOf(PAN_STEP, false)).toBe(48)
    expect(stepOf(PAN_STEP, true)).toBe(240)
    expect(stepOf(MOVE_STEP, false)).toBe(8)
    expect(stepOf(MOVE_STEP, true)).toBe(40)
  })
})

describe("sliding the picture", () => {
  const view = { x: 100, y: 50, zoom: 0.5 }

  test("the view goes the way the arrow points, so the picture goes the other", () => {
    expect(panned(view, { x: 1, y: 0 }, false)).toEqual({ x: 52, y: 50, zoom: 0.5 })
    expect(panned(view, { x: -1, y: 0 }, false)).toEqual({ x: 148, y: 50, zoom: 0.5 })
    expect(panned(view, { x: 0, y: 1 }, true)).toEqual({ x: 100, y: -190, zoom: 0.5 })
  })

  test("a step is the same on the screen at any zoom", () => {
    for (const zoom of [0.1, 1, 2]) {
      const next = panned({ x: 0, y: 0, zoom }, { x: 0, y: -1 }, false)
      expect(next).toEqual({ x: 0, y: 48, zoom })
    }
  })

  // A 400 by 200 picture at the canvas's origin, drawn at zoom 1 on an 800 by 600 canvas.
  const fence = { bounds: { x: 0, y: 0, width: 400, height: 200 }, size: { width: 800, height: 600 } }

  test("a held arrow stops with a margin of the picture still on the screen", () => {
    let at = { x: 0, y: 0, zoom: 1 }
    for (let i = 0; i < 50; i++) at = panned(at, { x: 1, y: 0 }, true, fence)
    // The picture's right edge, at 400 + x, keeps 48px on the screen.
    expect(at.x).toBe(48 - 400)
    for (let i = 0; i < 50; i++) at = panned(at, { x: -1, y: 0 }, true, fence)
    // Its left edge stops 48px short of the canvas's right.
    expect(at.x).toBe(800 - 48)
    for (let i = 0; i < 50; i++) at = panned(at, { x: 0, y: 1 }, false, fence)
    expect(at.y).toBe(48 - 200)
  })

  test("a view a trackpad put outside the bounds is not pulled back, only kept from going further", () => {
    const far = { x: -2000, y: 0, zoom: 1 }
    expect(panned(far, { x: 1, y: 0 }, false, fence)).toEqual(far)
    // The other way brings the picture back by a step.
    expect(panned(far, { x: -1, y: 0 }, false, fence)).toEqual({ x: -1952, y: 0, zoom: 1 })
  })
})

describe("moving what is chosen", () => {
  test("by the step on the canvas, and the larger one with Shift", () => {
    expect(moved({ x: 10, y: 20 }, { x: 1, y: 0 }, false)).toEqual({ x: 18, y: 20 })
    expect(moved({ x: 10, y: 20 }, { x: 0, y: -1 }, true)).toEqual({ x: 10, y: -20 })
  })

  test("on a grid, by whole cells and onto the lines", () => {
    expect(moved({ x: 32, y: 16 }, { x: 1, y: 0 }, false, { grid: 16 })).toEqual({ x: 48, y: 16 })
    expect(moved({ x: 32, y: 16 }, { x: -1, y: 0 }, true, { grid: 16 })).toEqual({ x: -16, y: 16 })
    // Placed off the grid: the first press lands on the next line that way.
    expect(moved({ x: 37, y: 5 }, { x: 1, y: 0 }, false, { grid: 16 })).toEqual({ x: 48, y: 5 })
    expect(moved({ x: 37, y: 5 }, { x: 0, y: -1 }, false, { grid: 16 })).toEqual({ x: 37, y: 0 })
  })

  test("held inside its bounds", () => {
    const bounds = { minX: 0, minY: 0, maxX: 100, maxY: 100 }
    expect(moved({ x: 4, y: 50 }, { x: -1, y: 0 }, false, { bounds })).toEqual({ x: 0, y: 50 })
    expect(moved({ x: 90, y: 50 }, { x: 1, y: 0 }, true, { bounds })).toEqual({ x: 100, y: 50 })
    expect(moved({ x: 50, y: 98 }, { x: 0, y: 1 }, false, { bounds })).toEqual({ x: 50, y: 100 })
  })
})
