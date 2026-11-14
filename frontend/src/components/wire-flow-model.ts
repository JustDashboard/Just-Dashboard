/**
 * The arithmetic behind `wire-flow.tsx`, kept apart from React so it is tested
 * in `bun test src`: what a line's state draws, which nodes a column has room
 * for, which nodes stay lit while one is pointed at, and the columns of the
 * wide grid.
 */

/**
 * What a line says, in the words the pictures already use (design-system §2):
 *
 * - `up`: the link exists and works. It carries a pulse when `moving`, and is
 *   still when it is up and idle;
 * - `down`: the thing at its end exists and is not up — dashed and still;
 * - `warning`: it works but should not be relied on — amber, and it may move;
 * - `danger`: it broke — red, and still unless it is told it carries;
 * - `idle`: configured but not in use — dashed and still, grey.
 */
export type WireState = "up" | "down" | "warning" | "danger" | "idle"

export type WireLine = {
  state?: WireState
  /** Something travels along it now. Ignored for `down` and `idle`. */
  moving?: boolean
  /** Dashed whatever the state says: a promise rather than a link. */
  dashed?: boolean
  /** The pulse runs from the far end back towards the near one. */
  reverse?: boolean
  /** One pass of the pulse and its rest, in seconds. */
  duration?: number
  /** Seconds before the first pass; by default staggered by the node's place. */
  delay?: number
}

export type BeamProps = {
  still: boolean
  dashed: boolean
  tone: "default" | "warning" | "danger"
}

/** The `AnimatedBeam` a line's state draws. */
export function beamOf(line: WireLine | undefined): BeamProps {
  const state = line?.state ?? "up"
  const quiet = state === "down" || state === "idle"
  return {
    still: quiet || !line?.moving,
    dashed: quiet || !!line?.dashed,
    tone: state === "warning" ? "warning" : state === "danger" ? "danger" : "default",
  }
}

/** Seconds before a line's first pulse: five lines in step, then the next five. */
export function staggerOf(index: number) {
  return (index % 5) * 0.3
}

/**
 * Which of a column's nodes are drawn, and how many are left for the "N more"
 * node. A column with room for one more than `max` draws it rather than a
 * "1 more" that takes the same place.
 */
export function fitColumn<T>(nodes: T[], max?: number): { shown: T[]; hidden: number } {
  if (max === undefined || max < 1 || nodes.length <= max + 1) return { shown: nodes, hidden: 0 }
  return { shown: nodes.slice(0, max), hidden: nodes.length - max }
}

/**
 * Whether a node, and the line to it, stays lit while `focus` is pointed at:
 * every node when nothing is, the pointed one, and whatever `related` says
 * travels with it (the networks trading flows with the internet, the domains
 * of an application).
 */
export function isLit(
  focus: string | null,
  id: string,
  related?: (focus: string, id: string) => boolean,
) {
  return focus === null || focus === id || !!related?.(focus, id)
}

/** Where a line meets a mark of each size: half the mark and a breath. */
export const PORT = { lg: 30, md: 26, sm: 22 } as const

/**
 * The wide grid's five columns: the start column, a gap the lines bend
 * across, the hub, the gap again, the end column. `hub` is the hub column's
 * share against the side columns' one each — wider for a hub with a long
 * line of facts under it.
 */
export function flowTemplate(hub = 0.8) {
  const gap = "clamp(2.5rem,6vw,6rem)"
  return `minmax(0,1fr) ${gap} minmax(0,${hub}fr) ${gap} minmax(0,1fr)`
}

/**
 * The rows a column holds while it loads, so the picture has its final
 * height before the data lands: what the caller expects, else what the
 * column has room for and its "more", else three.
 */
export function loadingRows(column: { loadingRows?: number; max?: number }) {
  if (column.loadingRows !== undefined) return Math.max(0, column.loadingRows)
  if (column.max !== undefined) return column.max + 1
  return 3
}
