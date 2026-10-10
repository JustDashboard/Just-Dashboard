import dagre from "@dagrejs/dagre"
import type { Edge, Node } from "@xyflow/react"
import {
  NODE_WIDTH,
  nodeHeight,
  visibleColumns,
  type DiagramDetail,
} from "@/components/database/diagram/geometry"
import type { DbGraphTable, DbSchemaGraph } from "@/components/database/diagram/types"

/**
 * Where the tables go.
 *
 * Layered by dagre rather than placed by a force simulation: a physics layout
 * looks livelier and puts the same schema somewhere different every time it is
 * opened, which is the opposite of what a reference diagram is for. Dagre is
 * deterministic, so a schema has *a* shape that somebody can learn — and once
 * they have moved a table, that position is theirs and outranks dagre's.
 *
 * A table is its `id` throughout — the schema and the name together. Two
 * schemas may each hold an `orders`; keyed by the name alone they were one
 * node, one position and one note between them.
 */
export type DiagramDirection = "LR" | "TB"
export type DiagramSpacing = "compact" | "comfortable"

export type LayoutOptions = {
  direction: DiagramDirection
  detail: DiagramDetail
  spacing: DiagramSpacing
  /** Ids of the tables kept off the canvas. */
  hidden: Set<string>
  /** Notes by table id: a table with one is a row taller. */
  notes: Record<string, string>
}

const SPACING = {
  compact: { nodesep: 32, ranksep: 96 },
  comfortable: { nodesep: 56, ranksep: 160 },
} as const

export type Placed = { nodes: Node[]; positions: Map<string, { x: number; y: number }> }

/**
 * Dagre's answer for every visible table, ignoring what the operator has
 * moved. `applyPositions` reconciles the two.
 */
export function layoutGraph(graph: DbSchemaGraph, opts: LayoutOptions): Placed {
  const tables = graph.tables.filter((t) => !opts.hidden.has(t.id))
  return place(tables, graph.edges, opts)
}

function place(
  tables: DbGraphTable[],
  allEdges: DbSchemaGraph["edges"],
  opts: LayoutOptions,
): Placed {
  const g = new dagre.graphlib.Graph()
  g.setDefaultEdgeLabel(() => ({}))
  g.setGraph({
    rankdir: opts.direction,
    // Generous, because these nodes are wide and the edges need somewhere to
    // run that is not through another table.
    ...SPACING[opts.spacing],
    marginx: 32,
    marginy: 32,
  })

  const present = new Set(tables.map((t) => t.id))
  const heightOf = (t: DbGraphTable) => nodeHeight(t, opts.detail, opts.notes[t.id])

  // Tables with no relationships at all are laid out separately.
  //
  // Dagre has nothing to rank them by, so it puts every one of them in the
  // same rank — a single column as tall as the schema is wide. A database with
  // six unrelated lookup tables became a strip a thousand pixels tall that fit
  // to view at ten per cent zoom, which is a diagram of nothing. They are
  // packed into a grid below the connected graph instead, where they are still
  // findable and cost almost no space.
  const connectedIds = new Set<string>()
  for (const e of allEdges) {
    if (present.has(e.from) && present.has(e.to) && e.from !== e.to) {
      connectedIds.add(e.from)
      connectedIds.add(e.to)
    }
  }
  const connected = tables.filter((t) => connectedIds.has(t.id))
  const isolated = tables.filter((t) => !connectedIds.has(t.id))

  for (const t of connected) g.setNode(t.id, { width: NODE_WIDTH, height: heightOf(t) })
  // Only edges between tables that are both present, or dagre invents a node
  // for the missing end and lays out a box that is never drawn.
  for (const e of allEdges) {
    if (present.has(e.from) && present.has(e.to) && e.from !== e.to) g.setEdge(e.from, e.to)
  }
  dagre.layout(g)

  const positions = new Map<string, { x: number; y: number }>()
  const nodes: Node[] = []
  let maxY = 0

  for (const t of connected) {
    const laid = g.node(t.id)
    // dagre reports the centre; React Flow positions by the top-left corner.
    const x = laid.x - laid.width / 2
    const y = laid.y - laid.height / 2
    positions.set(t.id, { x, y })
    maxY = Math.max(maxY, y + laid.height)
    nodes.push(node(t, x, y, laid.height))
  }

  // The grid is roughly as wide as it is tall, so the two halves read as one
  // picture rather than as a diagram with a tail.
  const gap = SPACING[opts.spacing].nodesep
  const perRow = Math.max(
    1,
    Math.min(isolated.length, Math.round(Math.sqrt(isolated.length * 1.6))),
  )
  let rowTop = connected.length > 0 ? maxY + 72 : 32
  let rowHeight = 0
  isolated.forEach((t, i) => {
    const col = i % perRow
    if (col === 0 && i > 0) {
      rowTop += rowHeight + gap
      rowHeight = 0
    }
    const h = heightOf(t)
    rowHeight = Math.max(rowHeight, h)
    const x = 32 + col * (NODE_WIDTH + gap)
    positions.set(t.id, { x, y: rowTop })
    nodes.push(node(t, x, rowTop, h))
  })

  return { nodes, positions }
}

function node(t: DbGraphTable, x: number, y: number, height: number): Node {
  return {
    id: t.id,
    type: "table",
    position: { x, y },
    data: { table: t },
    // Measured by us, so React Flow does not have to guess before first paint.
    width: NODE_WIDTH,
    height,
  }
}

/**
 * The operator's positions over dagre's.
 *
 * A table the operator placed stays exactly where they put it. Tables they
 * have not touched — new since the layout was saved, or unhidden — cannot be
 * dropped at dagre's coordinates, which are relative to a picture the operator
 * has since rearranged; they are laid out among themselves and set down as a
 * block under everything that was placed, where they are findable and overlap
 * nothing.
 */
export function applyPositions(
  graph: DbSchemaGraph,
  laid: Placed,
  saved: Record<string, { x: number; y: number }>,
  opts: LayoutOptions,
): Node[] {
  const placed = laid.nodes.filter((n) => saved[n.id])
  if (placed.length === 0) return laid.nodes
  const unplaced = laid.nodes.filter((n) => !saved[n.id])
  const out: Node[] = placed.map((n) => ({ ...n, position: { ...saved[n.id] } }))
  if (unplaced.length === 0) return out

  let bottom = -Infinity
  let left = Infinity
  for (const n of out) {
    bottom = Math.max(bottom, n.position.y + (n.height ?? 0))
    left = Math.min(left, n.position.x)
  }
  const waiting = new Set(unplaced.map((n) => n.id))
  const tables = graph.tables.filter((t) => waiting.has(t.id))
  const block = place(tables, graph.edges, opts)
  let top = Infinity
  let blockLeft = Infinity
  for (const n of block.nodes) {
    top = Math.min(top, n.position.y)
    blockLeft = Math.min(blockLeft, n.position.x)
  }
  const dx = (Number.isFinite(left) ? left : 0) - blockLeft
  const dy = bottom + 72 - top
  for (const n of block.nodes) {
    out.push({ ...n, position: { x: n.position.x + dx, y: n.position.y + dy } })
  }
  return out
}

/** The handle an edge lands on: a column's row on one side of its table, or the header. */
export function handleId(table: string, column: string, side: "left" | "right", end: "s" | "t") {
  return `${table}\u0000${column}\u0000${side}.${end}`
}

/**
 * Which side each relation leaves its table by, as one string: it changes
 * only when a table crosses to the other side of one it relates to. Edges are
 * rebuilt when this changes and not on every frame of a drag.
 */
export function edgeSides(graph: DbSchemaGraph, nodes: Node[]): string {
  const at = new Map(nodes.map((n) => [n.id, n.position.x]))
  let sides = ""
  for (const e of graph.edges) {
    const from = at.get(e.from)
    const to = at.get(e.to)
    sides += from === undefined || to === undefined ? "-" : to >= from ? "f" : "b"
  }
  return sides
}

/**
 * The foreign keys, anchored to the columns they relate.
 *
 * The side an edge leaves by is chosen from where the two tables actually are
 * now, not where the layout first put them: anchoring every edge to the right
 * of its source sends half of them backwards around the outside of the diagram
 * the moment a table is dragged to the other side. `sides` is `edgeSides` for
 * the same graph: one letter per relation.
 */
export function buildEdges(graph: DbSchemaGraph, sides: string, detail: DiagramDetail): Edge[] {
  const tables = new Map(graph.tables.map((t) => [t.id, t]))
  // An edge lands on its column's row where that row is drawn, and on the
  // table's header where it is not: at names-only detail, and at keys-only
  // for a column that is no key. An edge naming a handle that does not exist
  // is dropped without a word, so the header is the answer, not nothing.
  const drawn = (id: string, column: string) => {
    const table = tables.get(id)
    return table && visibleColumns(table, detail).some((c) => c.name === column) ? column : ""
  }
  return graph.edges.flatMap((e, i) => {
    const side = sides[i]
    if (side !== "f" && side !== "b") return []
    const forward = side === "f"
    const fromCol = drawn(e.from, e.fromColumn)
    const toCol = drawn(e.to, e.toColumn)
    return [
      {
        id: `${e.name}-${i}`,
        source: e.from,
        target: e.to,
        sourceHandle: handleId(e.from, fromCol, forward ? "right" : "left", "s"),
        targetHandle: handleId(e.to, toCol, forward ? "left" : "right", "t"),
        type: "relation",
        data: { relation: e },
      },
    ]
  })
}

/** The ids of everything one table touches, in both directions, itself included. */
export function neighbourhood(graph: DbSchemaGraph, id: string): Set<string> {
  const near = new Set<string>([id])
  for (const e of graph.edges) {
    if (e.from === id) near.add(e.to)
    if (e.to === id) near.add(e.from)
  }
  return near
}
