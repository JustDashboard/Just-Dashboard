import dagre from "@dagrejs/dagre"
import type { Edge, Node } from "@xyflow/react"
import type { DbGraphColumn, DbGraphTable, DbSchemaGraph } from "@/lib/types"
import type { DiagramDetail, DiagramDirection, DiagramSpacing } from "./memory"
import { HEADER_HEIGHT, NODE_WIDTH, NOTE_HEIGHT, ROW_HEIGHT } from "./table-node"

/**
 * Where the tables go.
 *
 * Layered by dagre rather than placed by a force simulation: a physics layout
 * looks livelier and puts the same schema somewhere different every time it is
 * opened, which is the opposite of what a reference diagram is for. Dagre is
 * deterministic, so a schema has *a* shape that somebody can learn — and once
 * they have moved a table, that position is theirs and outranks dagre's.
 *
 * Heights are computed rather than measured because the node renders at a size
 * this file decides — a header, a row per visible column, a note. Handing
 * dagre a wrong height is what produces boxes overlapping the edges beneath
 * them, and it is invisible until a table has thirty columns.
 */
export type LayoutOptions = {
  direction: DiagramDirection
  detail: DiagramDetail
  spacing: DiagramSpacing
  hidden: Set<string>
  notes: Record<string, string>
}

const SPACING = {
  compact: { nodesep: 32, ranksep: 96 },
  comfortable: { nodesep: 56, ranksep: 160 },
} as const

/** The columns a table draws at this level of detail. */
export function visibleColumns(table: DbGraphTable, detail: DiagramDetail): DbGraphColumn[] {
  if (detail === "names") return []
  if (detail === "keys")
    return table.columns.filter((c) => c.primaryKey || c.foreignKey || c.unique)
  return table.columns
}

/** The node's height, as the node component will draw it. */
export function nodeHeight(table: DbGraphTable, detail: DiagramDetail, note?: string): number {
  const shown = visibleColumns(table, detail).length
  // The "n more columns" row takes space too; a names-only table draws none.
  const more = detail === "keys" && shown < table.columns.length ? 1 : 0
  const rows = detail === "names" ? 0 : Math.max(1, shown + more)
  return HEADER_HEIGHT + rows * ROW_HEIGHT + (note ? NOTE_HEIGHT : 0)
}

export type Placed = { nodes: Node[]; positions: Map<string, { x: number; y: number }> }

/**
 * Dagre's answer for every visible table, ignoring what the operator has
 * moved. `applyPositions` reconciles the two.
 */
export function layoutGraph(graph: DbSchemaGraph, opts: LayoutOptions): Placed {
  const tables = graph.tables.filter((t) => !opts.hidden.has(t.name))
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

  const present = new Set(tables.map((t) => t.name))
  const heightOf = (t: DbGraphTable) => nodeHeight(t, opts.detail, opts.notes[t.name])

  // Tables with no relationships at all are laid out separately.
  //
  // Dagre has nothing to rank them by, so it puts every one of them in the
  // same rank — a single column as tall as the schema is wide. A database with
  // six unrelated lookup tables became a strip a thousand pixels tall that fit
  // to view at ten per cent zoom, which is a diagram of nothing. They are
  // packed into a grid below the connected graph instead, where they are still
  // findable and cost almost no space.
  const connectedNames = new Set<string>()
  for (const e of allEdges) {
    if (present.has(e.fromTable) && present.has(e.toTable) && e.fromTable !== e.toTable) {
      connectedNames.add(e.fromTable)
      connectedNames.add(e.toTable)
    }
  }
  const connected = tables.filter((t) => connectedNames.has(t.name))
  const isolated = tables.filter((t) => !connectedNames.has(t.name))

  for (const t of connected) g.setNode(t.name, { width: NODE_WIDTH, height: heightOf(t) })
  // Only edges between tables that are both present, or dagre invents a node
  // for the missing end and lays out a box that is never drawn.
  for (const e of allEdges) {
    if (present.has(e.fromTable) && present.has(e.toTable) && e.fromTable !== e.toTable) {
      g.setEdge(e.fromTable, e.toTable)
    }
  }
  dagre.layout(g)

  const positions = new Map<string, { x: number; y: number }>()
  const nodes: Node[] = []
  let maxY = 0

  for (const t of connected) {
    const laid = g.node(t.name)
    // dagre reports the centre; React Flow positions by the top-left corner.
    const x = laid.x - laid.width / 2
    const y = laid.y - laid.height / 2
    positions.set(t.name, { x, y })
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
    positions.set(t.name, { x, y: rowTop })
    nodes.push(node(t, x, rowTop, h))
  })

  return { nodes, positions }
}

function node(t: DbGraphTable, x: number, y: number, height: number): Node {
  return {
    id: t.name,
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
  const tables = graph.tables.filter((t) => unplaced.some((n) => n.id === t.name))
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

/**
 * The foreign keys, anchored to the columns they relate.
 *
 * The side an edge leaves by is chosen from where the two tables actually are
 * now, not where the layout first put them: anchoring every edge to the right
 * of its source sends half of them backwards around the outside of the diagram
 * the moment a table is dragged to the other side.
 */
export function buildEdges(graph: DbSchemaGraph, nodes: Node[], detail: DiagramDetail): Edge[] {
  const at = new Map(nodes.map((n) => [n.id, n.position]))
  return graph.edges
    .filter((e) => at.has(e.fromTable) && at.has(e.toTable))
    .map((e, i) => {
      const from = at.get(e.fromTable)!
      const to = at.get(e.toTable)!
      const forward = to.x >= from.x
      // At names-only detail there are no column rows to land on, so the
      // edge attaches to the header instead.
      const fromCol = detail === "names" ? "" : e.fromColumn
      const toCol = detail === "names" ? "" : e.toColumn
      return {
        id: `${e.name}-${i}`,
        source: e.fromTable,
        target: e.toTable,
        sourceHandle: `${e.fromTable}.${fromCol}.${forward ? "right" : "left"}.s`,
        targetHandle: `${e.toTable}.${toCol}.${forward ? "left" : "right"}.t`,
        type: "relation",
        data: { relation: e },
      }
    })
}
