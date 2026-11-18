/**
 * How a side column of a database workbench — the rail of tables, of objects,
 * of saved queries, of collections, of keys — shares the frame with the work.
 *
 * The five workbenches each wrote this and had drifted: four default widths
 * (256, 272, 300, 320) and two rules for when the rail stands beside the work
 * — the frame's own width on Data and Query, a 1024px window on MongoDB,
 * Redis and Schema — so at a 900px window SQL showed its rail beside the work
 * and the engines laid theirs over it. One width scale and one rule now.
 */

/** Every database workbench's rail: the width it opens at, and how far it is dragged. */
export const RAIL_SIZE = { base: 256, min: 200, max: 520 } as const

/** The least the work keeps beside a side column: under it, the column lies over the work. */
export const WORK_BESIDE_SIDE = 480

export function clamp(value: number, min: number, max: number) {
  return Math.min(Math.max(value, min), max)
}

/**
 * Whether a column `width` wide stands beside the work in a frame `frame`
 * wide: while the work keeps its floor. Before the frame is measured (one
 * render, never painted) the window answers.
 */
export function sideBeside(frame: number, width: number, windowWide: boolean): boolean {
  if (frame <= 0) return windowWide
  return frame - width >= WORK_BESIDE_SIDE
}

/** The widest the column can be dragged to here: as far as leaves the work its floor. */
export function sideMax(frame: number, size: { min: number; max: number }): number {
  if (frame <= 0) return size.max
  return clamp(frame - WORK_BESIDE_SIDE, size.min, size.max)
}
