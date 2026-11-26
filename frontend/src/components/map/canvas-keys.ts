/**
 * The arrow keys on a canvas: the server map, the databases map, a
 * database's diagram and a drawing (T10, WCAG 2.1.1). Each of them is moved
 * by a trackpad, a mouse and a finger (`canvas-move.ts`); these keys do the
 * same two things without a drag. Keys are not a single pointer (2.5.7): on
 * the maps that is the inspector's Move buttons, which take `moved` too.
 *
 * With nothing chosen an arrow slides the picture: the view goes the way the
 * arrow points, as a page scrolls, by a fixed distance on the screen whatever
 * the zoom. With a box or a frame chosen it moves that, by a distance on the
 * canvas, and the move is kept the way a drag is. Shift takes the larger
 * step. Ctrl, ⌘ and Alt are left to the browser and the canvas's own keys.
 *
 * Pure, so the steps and the bounds are tested without a browser
 * (`canvas-keys.test.js`); the canvases decide what is chosen and where the
 * keyboard is (`isTypingTarget` from `lib/keys.ts`).
 */

/** How far one press slides the picture, in screen pixels; with Shift, the larger. */
export const PAN_STEP = { step: 48, large: 240 } as const

/** How far one press moves what is chosen, in canvas units; with Shift, the larger. */
export const MOVE_STEP = { step: 8, large: 40 } as const

/** The legend's caps for the four arrows, and what they do, as `Keycaps` draws them. */
export const ARROW_CAPS = "←+↑+↓+→"

export type Point = { x: number; y: number }

/** A view of a canvas: what is at canvas point `p` is drawn at `p * zoom + (x, y)`. */
export type View = { x: number; y: number; zoom: number }

/** A rectangle on the canvas. */
export type Rect = { x: number; y: number; width: number; height: number }

/** Which way an arrow points: one of x and y is 0, the other 1 or -1. */
export type Direction = { x: -1 | 0 | 1; y: -1 | 0 | 1 }

const ARROWS: Record<string, Direction> = {
  ArrowUp: { x: 0, y: -1 },
  ArrowDown: { x: 0, y: 1 },
  ArrowLeft: { x: -1, y: 0 },
  ArrowRight: { x: 1, y: 0 },
}

type KeyLike = { key: string; ctrlKey: boolean; metaKey: boolean; altKey: boolean }

/**
 * The direction of an arrow pressed alone or with Shift, or null for any
 * other key and for an arrow with Ctrl, ⌘ or Alt, which mean something else
 * (the browser's history, a word's end, a system's own shortcut).
 */
export function arrowOf(event: KeyLike): Direction | null {
  if (event.ctrlKey || event.metaKey || event.altKey) return null
  return ARROWS[event.key] ?? null
}

/** One press's distance: the step, or the larger one with Shift. */
export function stepOf(steps: { step: number; large: number }, shift: boolean) {
  return shift ? steps.large : steps.step
}

/**
 * Keeps one axis of a pan inside its bounds without jumping: a view already
 * outside them (put there by a trackpad) is not pulled back, it only stops
 * going further out.
 */
function clampAxis(current: number, next: number, low: number, high: number) {
  if (next < low && next < current) return Math.min(current, Math.max(low, next))
  if (next > high && next > current) return Math.max(current, Math.min(high, next))
  return next
}

/**
 * The view after one press of an arrow: the picture slides the other way by
 * the step, in screen pixels, at any zoom.
 *
 * With `bounds` (what is drawn, on the canvas) and `size` (the canvas on the
 * screen), the picture is not slid out of sight: at least `margin` pixels of
 * it stay on the screen, so a held key cannot lose it.
 */
export function panned(
  view: View,
  direction: Direction,
  shift: boolean,
  fence?: { bounds: Rect; size: { width: number; height: number }; margin?: number },
): View {
  const step = stepOf(PAN_STEP, shift)
  let x = view.x - direction.x * step
  let y = view.y - direction.y * step
  if (fence) {
    const { bounds, size, margin = PAN_STEP.step } = fence
    const z = view.zoom
    x = clampAxis(
      view.x,
      x,
      margin - (bounds.x + bounds.width) * z,
      size.width - margin - bounds.x * z,
    )
    y = clampAxis(
      view.y,
      y,
      margin - (bounds.y + bounds.height) * z,
      size.height - margin - bounds.y * z,
    )
  }
  return { x, y, zoom: view.zoom }
}

/**
 * Where a chosen box or frame lands after one press, on the canvas.
 *
 * On a canvas that snaps to a grid the step is the grid's (the larger step a
 * whole number of cells) and the place is put on it, so a press never leaves
 * a box between two lines. `bounds` holds the box's corner inside a rectangle
 * of the canvas, where a canvas has one.
 */
export function moved(
  at: Point,
  direction: Direction,
  shift: boolean,
  options: {
    grid?: number
    bounds?: { minX: number; minY: number; maxX: number; maxY: number }
  } = {},
): Point {
  const { grid, bounds } = options
  let step = stepOf(MOVE_STEP, shift)
  if (grid && grid > 0) step = Math.max(1, Math.ceil(step / grid)) * grid
  let x = at.x + direction.x * step
  let y = at.y + direction.y * step
  if (grid && grid > 0) {
    // Off the grid to start with (placed before the grid was on): the first
    // press lands on the next line that way, not a full step past it.
    x = direction.x === 0 ? at.x : snapToward(at.x, direction.x, grid, step)
    y = direction.y === 0 ? at.y : snapToward(at.y, direction.y, grid, step)
  }
  if (bounds) {
    x = Math.min(bounds.maxX, Math.max(bounds.minX, x))
    y = Math.min(bounds.maxY, Math.max(bounds.minY, y))
  }
  return { x, y }
}

function snapToward(value: number, sign: -1 | 1, grid: number, step: number) {
  const on = Math.round(value / grid) * grid
  if (Math.abs(on - value) > 1e-9) {
    // The nearest line the way the arrow points, then whatever is left of the step.
    const line = sign > 0 ? Math.ceil(value / grid) * grid : Math.floor(value / grid) * grid
    return line + sign * (step - grid)
  }
  return on + sign * step
}
