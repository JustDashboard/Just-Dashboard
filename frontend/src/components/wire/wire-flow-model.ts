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
 * The wide grid's five columns: the start side, a gap the lines bend across,
 * the hub, the gap again, the end side. The sides share what the hub and the
 * gaps leave, so the picture is as wide as its page. `hub` scales the hub's
 * column, which holds the words hung under its mark: 10rem at the default,
 * wider for a hub with a long line of facts under it.
 */
export function flowTemplate(hub = 0.8) {
  const gap = "clamp(3rem,6vw,6rem)"
  const width = Math.round(hub * 12.5 * 100) / 100
  return `minmax(0,1fr) ${gap} ${width}rem ${gap} minmax(0,1fr)`
}

/**
 * The narrowest a node is drawn wide, by the size of its mark, and the gap
 * between two in a row, in px. A picture of small marks is a dense one, whose
 * nodes say a name and a word.
 */
export const CELL = { md: 116, sm: 100, gap: 12 } as const

/** How many nodes of this size a side `width` wide has room for in one row. */
export function fitsRow(width: number, size: "md" | "sm" = "md") {
  return Math.max(1, Math.floor((width + CELL.gap) / (CELL[size] + CELL.gap)))
}

/**
 * The form the wide picture takes, from the room a side has and how many
 * nodes each side holds.
 *
 * `rows`: each side is rows of nodes with their words under them, each row
 * one level line into the hub. It is as wide as the page and as short as it
 * can be, and it is the form wherever a side holds its nodes in one row or
 * has room for three across.
 *
 * `column`: each side is one column, a node's words beside its mark and a
 * line fanning from each to the hub. Words under a mark cost a row twice the
 * height of words beside it, so rows of one or two would stand taller than
 * the column they replace: beside a rail, or on a page that keeps a narrow
 * measure, the picture stays a column.
 */
export function flowForm(
  width: number,
  counts: { start: number; end: number },
  size: "md" | "sm" = "md",
): "rows" | "column" {
  const fits = fitsRow(width, size)
  const room = (count: number) => count <= fits || fits >= 3
  return room(counts.start) && room(counts.end) ? "rows" : "column"
}

/**
 * How many nodes stand in each row of a side `width` wide. A side is rows of
 * nodes, each row one level line into the hub, so a picture grows sideways
 * into the room its page has before it grows down: eight nodes are two rows
 * of four on a wide page, where one column of eight stood taller than the
 * page's first screen with nothing on either side of it. The rows are evened
 * out, so five nodes are three and two rather than four and one.
 */
export function perRow(width: number, count: number, size: "md" | "sm" = "md") {
  if (count <= 1) return 1
  const rows = Math.ceil(count / fitsRow(width, size))
  return Math.ceil(count / rows)
}

/**
 * The grid column a node starts in, where it has to be said: the first node
 * of a last row that is short, on the start side, which is pushed against the
 * hub so its line is not drawn across empty places.
 */
export function columnStart(index: number, count: number, per: number, side: "start" | "end") {
  const short = count % per
  if (side !== "start" || short === 0 || index !== count - short) return undefined
  return per - short + 1
}

/**
 * The order the lines are painted in. The lines of one row lie on each other
 * between the nearest mark and the hub, and the one painted last is the one
 * read there: a line that needs a look over one that works, and a line that
 * works over one that is down.
 */
export function paintOrder(line: WireLine | undefined) {
  const beam = beamOf(line)
  if (beam.tone === "danger") return 3
  if (beam.tone === "warning") return 2
  return beam.dashed ? 0 : 1
}

/**
 * The two nodes a side's level is read from: the first of its middle row, or
 * of the two rows about its middle where it has an even number. With three
 * rows the hub then stands exactly on the second and that row's line is
 * level, whatever the words under the first and the third make their rows.
 */
export function middleRows(count: number, per: number): [number, number] {
  const last = Math.max(0, Math.ceil(count / Math.max(1, per)) - 1)
  return [Math.floor(last / 2) * per, Math.ceil(last / 2) * per]
}

/**
 * How far each of the three parts is dropped so that they stand on one level:
 * a side on the middle of its rows' marks (`middleRows`), the hub on its
 * mark. `mid` is that level measured from the part's own top.
 */
export function levelDrops(mid: { start: number; hub: number; end: number }) {
  const level = Math.max(mid.start, mid.hub, mid.end)
  return {
    start: Math.round(level - mid.start),
    hub: Math.round(level - mid.hub),
    end: Math.round(level - mid.end),
  }
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

/**
 * The lines of detail a waiting node holds: a count, or a count for the wide
 * picture and one for the narrow, where a node says a line more down a phone
 * (a hub that names there what its marks no longer do).
 */
export type WaitingHint = number | { wide: number; narrow: number }

/** Where one waiting line of detail is drawn. */
export type WaitingLine = "both" | "wide" | "narrow"

/**
 * A waiting node's lines of detail, one entry a line: the lines both widths
 * have first, then the ones only the longer of the two has. With no shape
 * given a node waits on one line, as every picture did before a node could
 * say how many it has.
 */
export function waitingLines(hint: WaitingHint | undefined): WaitingLine[] {
  const { wide, narrow } = typeof hint === "object" ? hint : { wide: hint ?? 1, narrow: hint ?? 1 }
  const count = (n: number) => (Number.isFinite(n) ? Math.max(0, Math.floor(n)) : 0)
  const shared = Math.min(count(wide), count(narrow))
  const only: WaitingLine = count(wide) > count(narrow) ? "wide" : "narrow"
  return [
    ...Array.from({ length: shared }, (): WaitingLine => "both"),
    ...Array.from({ length: Math.abs(count(wide) - count(narrow)) }, () => only),
  ]
}
