import { expect, test } from "bun:test"
import { readdirSync, readFileSync } from "node:fs"
import { join, relative, resolve } from "node:path"
import { CANVAS_MOVE, zoomAbout, zoomKey } from "./canvas-move"

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
