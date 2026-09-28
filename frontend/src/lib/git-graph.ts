import type { GitGraphCommit } from "@/lib/types"

/**
 * The branch graph's geometry, apart from the component that draws it.
 *
 * The server decides which lane everything is in (gitx.layoutGraph); this
 * decides how a line gets from one commit to another. An edge leaves its commit,
 * crosses into the lane it travels (the parent lane the server named — the
 * commit's own for a first parent, the merged branch's for the rest), runs down
 * that lane, and crosses into the parent's column only in the row above the
 * parent. Each crossing is one S-curve a row tall, so a fork or a merge reads
 * as a branch leaving or joining rather than as a line with a kink in it.
 */

export const GRAPH_ROW = 34 // px per commit row — matches the history list's rhythm
export const GRAPH_COL = 16 // px between lanes
export const GRAPH_PAD = 14 // px from the left edge to lane 0

export const laneX = (col: number) => GRAPH_PAD + col * GRAPH_COL
export const rowY = (row: number) => row * GRAPH_ROW + GRAPH_ROW / 2

export type GraphEdge = {
  key: string
  d: string
  /** The lane the edge travels down, which is the colour it is drawn in. */
  lane: number
  /** Row of the commit the edge leaves. */
  from: number
  /** Row of the parent, or -1 when the parent has not been loaded yet. */
  to: number
  /** Whether this is the edge to the first parent — the line of the branch. */
  first: boolean
}

const cross = (x1: number, y1: number, x2: number, y2: number) => {
  const mid = (y1 + y2) / 2
  return x1 === x2 ? `L ${x2} ${y2}` : `C ${x1} ${mid}, ${x2} ${mid}, ${x2} ${y2}`
}

/**
 * Every edge between the loaded rows. An edge whose parent has not arrived yet
 * runs down its lane to `bottom` — the next page will land on the end of it.
 */
export function graphEdges(rows: GitGraphCommit[], bottom: number): GraphEdge[] {
  const index = new Map<string, number>()
  rows.forEach((c, i) => index.set(c.sha, i))
  const out: GraphEdge[] = []
  rows.forEach((c, i) => {
    ;(c.parents ?? []).forEach((parent, k) => {
      const j = index.get(parent)
      const lane = c.parentLanes?.[k] ?? (k === 0 ? c.col : j === undefined ? c.col : rows[j].col)
      const sx = laneX(c.col)
      const sy = rowY(i)
      const lx = laneX(lane)
      let d = `M ${sx} ${sy} `
      if (j === undefined) {
        d += lx === sx ? `L ${lx} ${bottom}` : `${cross(sx, sy, lx, rowY(i + 1))} L ${lx} ${bottom}`
      } else if (j === i + 1) {
        d += cross(sx, sy, laneX(rows[j].col), rowY(j))
      } else {
        const px = laneX(rows[j].col)
        if (lx !== sx) d += `${cross(sx, sy, lx, rowY(i + 1))} `
        d += `L ${lx} ${rowY(j - 1)} ${cross(lx, rowY(j - 1), px, rowY(j))}`
      }
      out.push({ key: `${c.sha}-${parent}`, d, lane, from: i, to: j ?? -1, first: k === 0 })
    })
  })
  return out
}

/**
 * The rows on one commit's line of history: down its first parents to the
 * root, and up through the children that continue it in the same lane to the
 * tip it hangs from. That is "the branch this commit is on" as a reader means
 * it, and it is what lights when a row is pointed at.
 */
export function graphLineage(rows: GitGraphCommit[], row: number): Set<number> {
  const index = new Map<string, number>()
  rows.forEach((c, i) => index.set(c.sha, i))
  // A commit's lane was inherited from the child in the same column; any other
  // child is a branch that forked from here and has a lane of its own.
  const next = new Map<number, number>()
  rows.forEach((c, i) => {
    const j = c.parents?.[0] === undefined ? undefined : index.get(c.parents[0])
    if (j !== undefined && rows[j].col === c.col) next.set(j, i)
  })
  const line = new Set<number>()
  for (let i: number | undefined = row; i !== undefined;) {
    line.add(i)
    const first: string | undefined = rows[i].parents?.[0]
    i = first === undefined ? undefined : index.get(first)
  }
  for (let i = next.get(row); i !== undefined; i = next.get(i)) line.add(i)
  return line
}

/** The graph's width: every lane a dot sits in or an edge runs down. */
export function graphLanes(rows: GitGraphCommit[], reported: number): number {
  let lanes = Math.max(1, reported)
  for (const c of rows) {
    lanes = Math.max(lanes, c.col + 1, ...(c.parentLanes ?? []).map((l) => l + 1))
  }
  return lanes
}
