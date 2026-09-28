import { describe, expect, test } from "bun:test"
import { GRAPH_ROW, graphEdges, graphLanes, graphLineage, laneX, rowY } from "./git-graph"

// The fixture is gitx's own: main C1 → C2 → C3, a feature F1 → F2 off C1, and
// C4 merging it. Topological order puts the feature below main's commits, so
// the feature's last edge has two of main's commits between it and C1.
//
//   C4  ●─╮
//   C3  ● │
//   C2  ● │
//   F2  │ ●
//   F1  │ ●
//   C1  ●─╯
const rows = [
  { sha: "C4", parents: ["C3", "F2"], col: 0, parentLanes: [0, 1] },
  { sha: "C3", parents: ["C2"], col: 0, parentLanes: [0] },
  { sha: "C2", parents: ["C1"], col: 0, parentLanes: [0] },
  { sha: "F2", parents: ["F1"], col: 1, parentLanes: [1] },
  { sha: "F1", parents: ["C1"], col: 1, parentLanes: [1] },
  { sha: "C1", parents: [], col: 0 },
]
const bottom = rows.length * GRAPH_ROW

describe("how the graph draws a line between two commits", () => {
  test("a branch rejoining its fork point stays in its lane until the row above", () => {
    const edge = graphEdges(rows, bottom).find((e) => e.key === "F1-C1")
    // F1 and C1 are adjacent rows, so the crossing is the whole edge…
    expect(edge.d).toBe(
      `M ${laneX(1)} ${rowY(4)} C ${laneX(1)} ${(rowY(4) + rowY(5)) / 2}, ${laneX(0)} ${(rowY(4) + rowY(5)) / 2}, ${laneX(0)} ${rowY(5)}`,
    )
    // …and with rows between, it runs straight down lane 1 before it crosses.
    const far = graphEdges([rows[0], rows[3], rows[4], rows[1], rows[2], rows[5]], bottom).find(
      (e) => e.key === "F1-C1",
    )
    expect(far.d.startsWith(`M ${laneX(1)} ${rowY(2)} L ${laneX(1)} ${rowY(4)} C`)).toBe(true)
    expect(far.lane).toBe(1)
  })

  test("a merge crosses into the merged branch's lane on the way down, not at the end", () => {
    const edge = graphEdges(rows, bottom).find((e) => e.key === "C4-F2")
    expect(edge.d.startsWith(`M ${laneX(0)} ${rowY(0)} C`)).toBe(true)
    expect(edge.d).toContain(`${laneX(1)} ${rowY(1)} L ${laneX(1)} ${rowY(2)}`)
    expect(edge.first).toBe(false)
  })

  test("an edge whose parent is on a page not yet loaded runs to the foot of the canvas", () => {
    const edge = graphEdges(rows.slice(0, 2), 2 * GRAPH_ROW).find((e) => e.key === "C3-C2")
    expect(edge.d).toBe(`M ${laneX(0)} ${rowY(1)} L ${laneX(0)} ${2 * GRAPH_ROW}`)
    expect(edge.to).toBe(-1)
  })
})

describe("the line a pointed-at commit is on", () => {
  test("a feature commit lights its branch up to the tip and down its first parents", () => {
    expect([...graphLineage(rows, 4)].sort()).toEqual([3, 4, 5])
  })

  test("a main commit lights main, not the branch that merged into it", () => {
    expect([...graphLineage(rows, 2)].sort()).toEqual([0, 1, 2, 5])
  })
})

test("the canvas is as wide as the widest lane anything uses", () => {
  expect(graphLanes(rows, 0)).toBe(2)
  expect(graphLanes([{ sha: "a", parents: ["b"], col: 0, parentLanes: [3] }], 1)).toBe(4)
})
