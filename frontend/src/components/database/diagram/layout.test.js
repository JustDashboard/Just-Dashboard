import { describe, expect, test } from "bun:test"
import { NODE_WIDTH, nodeHeight, shortType, visibleColumns } from "./geometry"
import {
  applyPositions,
  buildEdges,
  edgeSides,
  handleId,
  layoutGraph,
  neighbourhood,
} from "./layout"
import { readGraph } from "./types"
import { toDbml, toMermaid } from "./export"

const col = (name, over = {}) => ({
  name,
  type: "int",
  nullable: false,
  primaryKey: false,
  ...over,
})
const table = (schema, name, columns = [col("id", { primaryKey: true })]) => ({
  id: `${schema}.${name}`,
  schema,
  name,
  type: "table",
  rows: 1,
  columns,
})
const edge = (from, to, fromColumn = "order_id", toColumn = "id") => ({
  name: `${from}_${fromColumn}_fkey`,
  from,
  to,
  fromTable: from.split(".")[1],
  fromColumn,
  toTable: to.split(".")[1],
  toColumn,
  cardinality: "many-to-one",
})

const OPTS = {
  direction: "LR",
  detail: "all",
  spacing: "comfortable",
  hidden: new Set(),
  notes: {},
}

// Two schemas, each with an `orders`, and a key from one schema into the other.
const GRAPH = {
  schema: "",
  tables: [
    table("sales", "orders"),
    table("archive", "orders"),
    table("sales", "items", [
      col("id", { primaryKey: true }),
      col("order_id", { foreignKey: "orders" }),
    ]),
    table("sales", "lonely"),
  ],
  edges: [edge("sales.items", "sales.orders"), edge("sales.items", "archive.orders")],
  truncated: false,
  total: 4,
  limit: 120,
}

describe("the diagram keyed by schema and name", () => {
  test("two tables of one name are two nodes, each with its own place", () => {
    const laid = layoutGraph(GRAPH, OPTS)
    const ids = laid.nodes.map((n) => n.id).sort()
    expect(ids).toEqual(["archive.orders", "sales.items", "sales.lonely", "sales.orders"])
    expect(laid.positions.get("sales.orders")).not.toEqual(laid.positions.get("archive.orders"))
  })

  test("hiding one of them hides only that one", () => {
    const laid = layoutGraph(GRAPH, { ...OPTS, hidden: new Set(["archive.orders"]) })
    expect(laid.nodes.map((n) => n.id).sort()).toEqual([
      "sales.items",
      "sales.lonely",
      "sales.orders",
    ])
  })

  test("a saved position is the table's own, and an unplaced table is set down under the placed", () => {
    const laid = layoutGraph(GRAPH, OPTS)
    const nodes = applyPositions(GRAPH, laid, { "sales.orders": { x: 900, y: 40 } }, OPTS)
    const at = Object.fromEntries(nodes.map((n) => [n.id, n.position]))
    expect(at["sales.orders"]).toEqual({ x: 900, y: 40 })
    expect(at["archive.orders"].y).toBeGreaterThan(40)
    expect(nodes).toHaveLength(4)
  })

  test("a focus reaches across schemas, and only as far as the keys go", () => {
    expect([...neighbourhood(GRAPH, "sales.items")].sort()).toEqual([
      "archive.orders",
      "sales.items",
      "sales.orders",
    ])
    expect([...neighbourhood(GRAPH, "archive.orders")].sort()).toEqual([
      "archive.orders",
      "sales.items",
    ])
  })
})

describe("edges", () => {
  const at = (positions) =>
    Object.entries(positions).map(([id, x]) => ({ id, position: { x, y: 0 } }))

  test("leave by the side the other table is on, and say so in one letter each", () => {
    const sides = edgeSides(
      GRAPH,
      at({ "sales.items": 500, "sales.orders": 900, "archive.orders": 100 }),
    )
    expect(sides).toBe("fb")
    const [forward, back] = buildEdges(GRAPH, sides, "all")
    expect(forward.sourceHandle).toBe(handleId("sales.items", "order_id", "right", "s"))
    expect(forward.targetHandle).toBe(handleId("sales.orders", "id", "left", "t"))
    expect(back.sourceHandle).toBe(handleId("sales.items", "order_id", "left", "s"))
    expect(back.target).toBe("archive.orders")
  })

  test("the letter does not change while a table is dragged on its own side", () => {
    const before = edgeSides(
      GRAPH,
      at({ "sales.items": 500, "sales.orders": 900, "archive.orders": 100 }),
    )
    const dragged = edgeSides(
      GRAPH,
      at({ "sales.items": 520, "sales.orders": 940, "archive.orders": 60 }),
    )
    expect(dragged).toBe(before)
  })

  test("an edge to a table that is not drawn is not drawn", () => {
    const sides = edgeSides(GRAPH, at({ "sales.items": 500, "sales.orders": 900 }))
    expect(sides).toBe("f-")
    expect(buildEdges(GRAPH, sides, "all")).toHaveLength(1)
  })

  test("with no row to land on, an edge lands on the table's header", () => {
    const sides = "ff"
    const [named] = buildEdges(GRAPH, sides, "names")
    expect(named.sourceHandle).toBe(handleId("sales.items", "", "right", "s"))
    // At keys-only detail a referenced column that is no key has no row either.
    const odd = { ...GRAPH, edges: [edge("sales.items", "sales.orders", "order_id", "label")] }
    const [keys] = buildEdges(odd, "f", "keys")
    expect(keys.sourceHandle).toBe(handleId("sales.items", "order_id", "right", "s"))
    expect(keys.targetHandle).toBe(handleId("sales.orders", "", "left", "t"))
  })
})

describe("what a table measures", () => {
  const wide = table("s", "t", [
    col("id", { primaryKey: true }),
    col("a"),
    col("b"),
    col("ref", { foreignKey: "u" }),
  ])

  test("its height is its rows at the level of detail, plus a note", () => {
    expect(visibleColumns(wide, "keys").map((c) => c.name)).toEqual(["id", "ref"])
    expect(nodeHeight(wide, "all")).toBe(38 + 4 * 26)
    // Two keys and the row that says two more are not shown.
    expect(nodeHeight(wide, "keys")).toBe(38 + 3 * 26)
    expect(nodeHeight(wide, "names", "a note")).toBe(38 + 24)
    expect(NODE_WIDTH).toBe(264)
  })

  test("a long type name is said in the short form a reader knows", () => {
    expect(shortType("timestamp with time zone")).toBe("timestamptz")
    expect(shortType("character varying(255)")).toBe("character var…")
  })
})

describe("a graph as the page reads it", () => {
  test("a server that sends no ids has them filled in the way they are now named", () => {
    const graph = readGraph(
      {
        schema: "public",
        tables: [{ schema: "public", name: "orders", type: "table", rows: 1, columns: [] }],
        edges: [
          {
            name: "fk",
            fromTable: "items",
            fromColumn: "order_id",
            toTable: "orders",
            toColumn: "id",
            cardinality: "many-to-one",
          },
        ],
        truncated: true,
      },
      "public",
    )
    expect(graph.tables[0].id).toBe("public.orders")
    expect(graph.edges[0]).toMatchObject({ from: "public.items", to: "public.orders" })
    expect(graph).toMatchObject({ truncated: true, total: 1, limit: 1 })
  })

  test("nothing at all is an empty picture, not a crash", () => {
    expect(readGraph(undefined, "main")).toEqual({
      schema: "main",
      tables: [],
      edges: [],
      truncated: false,
      total: 0,
      limit: 0,
    })
  })
})

describe("the picture taken away as text", () => {
  test("Mermaid names two schemas' orders as two entities", () => {
    const text = toMermaid(GRAPH, new Set(["sales.lonely"]))
    expect(text).toContain("  sales_orders {")
    expect(text).toContain("  archive_orders {")
    expect(text).toContain('  sales_items }o--|| archive_orders : "order_id"')
    expect(text).not.toContain("lonely")
  })

  test("one schema alone keeps the bare names", () => {
    const one = {
      ...GRAPH,
      tables: GRAPH.tables.filter((t) => t.schema === "sales"),
      edges: [GRAPH.edges[0]],
    }
    expect(toMermaid(one, new Set())).toContain("  items }o--|| orders")
  })

  test("DBML says each end of a key with its schema, and keeps a table's own note", () => {
    const text = toDbml(GRAPH, new Set(), { "archive.orders": "kept for audits" })
    expect(text).toContain("Table archive.orders {")
    expect(text).toContain("Note: 'kept for audits'")
    expect(text).toContain("Ref: sales.items.order_id > archive.orders.id")
    // The other orders has no note of its own.
    expect(text.match(/Note:/g)).toHaveLength(1)
  })
})
