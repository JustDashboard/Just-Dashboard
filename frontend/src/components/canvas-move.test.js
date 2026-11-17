import { describe, expect, test } from "bun:test"
import { readdirSync, readFileSync } from "node:fs"
import { join, relative, resolve } from "node:path"
import {
  CANVAS_MOVE,
  MOVE_STEP,
  PAN_STEP,
  arrowOf,
  moved,
  panned,
  stepOf,
  zoomAbout,
  zoomKey,
} from "./canvas-move"

const SRC = resolve(import.meta.dir, "..")

/** Every .tsx file under a folder. */
function pages(dir) {
  const out = []
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const path = join(dir, entry.name)
    if (entry.isDirectory()) out.push(...pages(path))
    else if (entry.name.endsWith(".tsx")) out.push(path)
  }
  return out
}

test("every canvas of the product moves the way a whiteboard does", () => {
  // The operator's rule: two fingers slide a canvas, a pinch zooms it, a mouse
  // drags it. The server's map had it and the database diagram did not, because
  // the settings were written into the map's own file. A canvas is a file that
  // renders <ReactFlow>, and each one takes the shared settings and Safari's pinch.
  const canvases = pages(SRC).filter((file) => /<ReactFlow[\s>]/.test(readFileSync(file, "utf8")))
  expect(canvases.map((file) => relative(SRC, file)).sort()).toEqual([
    "components/database/diagram/er-diagram.tsx",
    "components/map/canvas.tsx",
  ])
  for (const file of canvases) {
    const text = readFileSync(file, "utf8")
    const name = relative(SRC, file)
    expect({ name, moves: text.includes("{...CANVAS_MOVE}") }).toEqual({ name, moves: true })
    expect({ name, pinch: /\buseCanvasPinch\(/.test(text) }).toEqual({ name, pinch: true })
    // Nothing a canvas writes beside the spread takes one of its gestures back.
    for (const prop of Object.keys(CANVAS_MOVE)) {
      expect({ name, prop, own: new RegExp(`\\s${prop}=`).test(text) }).toEqual({
        name,
        prop,
        own: false,
      })
    }
  }
})

test("two fingers slide, a pinch zooms, a double click does nothing", () => {
  expect(CANVAS_MOVE).toEqual({
    panOnScroll: true,
    panOnScrollSpeed: 1,
    zoomOnScroll: false,
    zoomOnPinch: true,
    zoomOnDoubleClick: false,
    panOnDrag: [0, 1],
  })
})

test("a whiteboard's zoom keys are Ctrl or ⌘ with +, − and 0", () => {
  const key = (key, over = {}) => ({ key, ctrlKey: false, metaKey: false, altKey: false, ...over })
  expect(zoomKey(key("=", { ctrlKey: true }))).toBe("in")
  expect(zoomKey(key("+", { metaKey: true }))).toBe("in")
  expect(zoomKey(key("-", { ctrlKey: true }))).toBe("out")
  expect(zoomKey(key("0", { metaKey: true }))).toBe("real")
  // A bare key is the page's, and Alt with it is the keyboard layout's.
  expect(zoomKey(key("="))).toBe(null)
  expect(zoomKey(key("0", { ctrlKey: true, altKey: true }))).toBe(null)
  expect(zoomKey(key("f", { ctrlKey: true }))).toBe(null)
})

test("a zoom about a point leaves what is under the point where it was", () => {
  const view = [40, -20, 0.5]
  const point = { x: 300, y: 200 }
  // What the point shows, in the canvas's own coordinates, before and after.
  const under = ([x, y, k]) => ({ x: (point.x - x) / k, y: (point.y - y) / k })
  const next = zoomAbout(view, point, 1.5)
  expect(next.zoom).toBe(1.5)
  const after = under([next.x, next.y, next.zoom])
  expect(after.x).toBeCloseTo(under(view).x, 9)
  expect(after.y).toBeCloseTo(under(view).y, 9)
})

// The arrow keys (T10), which moved here from map/canvas-keys.

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
  const fence = {
    bounds: { x: 0, y: 0, width: 400, height: 200 },
    size: { width: 800, height: 600 },
  }

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
