/**
 * A rail row's height: 28px, and 36px below `sm`, where a row is pressed by
 * a finger. Every rail's rows, its group heads that fold and its skeleton
 * are drawn at it, so a rail holds its shape while it is read.
 */
export const RAIL_ROW = "h-7 max-sm:h-9"

/** The same heights in pixels, for a list drawn in a window of rows (the Redis keys). */
export const RAIL_ROW_PX = { base: 28, phone: 36 } as const

/** A figure at a rail row's end: a count, a size, a word that tells it apart. */
export type RailFigure = {
  key: string
  text: string
  title?: string
  /** A literal of the engine's (a signature, a table name) rather than a figure. */
  mono?: boolean
  /** The second figure of a pair, a step below the first. */
  quiet?: boolean
  className?: string
}

const WIDTHS = ["w-28", "w-20", "w-32", "w-24", "w-16", "w-28", "w-24", "w-20"]

/**
 * The silhouette of a rail being read: `groups` heads, each over rows of
 * names at varied widths — five under the first, two under the rest, so the
 * shape is a list and not a block. With no groups it is eight rows.
 */
export function railSkeletonRows(
  groups: number,
  lines: 1 | 2 = 1,
): ({ kind: "head" } | { kind: "row"; width: string })[] {
  const out: ({ kind: "head" } | { kind: "row"; width: string })[] = []
  const per = lines === 2 ? 6 : 8
  if (groups <= 0) {
    for (let i = 0; i < per; i++) out.push({ kind: "row", width: WIDTHS[i % WIDTHS.length] })
    return out
  }
  let at = 0
  for (let group = 0; group < groups; group++) {
    out.push({ kind: "head" })
    const count = group === 0 ? 5 : 2
    for (let i = 0; i < count; i++) out.push({ kind: "row", width: WIDTHS[at++ % WIDTHS.length] })
  }
  return out
}
