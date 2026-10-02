import { describe, expect, test } from "bun:test"
import { schemaModel, suggest, tableRefs } from "./completion"
import { dialectOf } from "./dialect"
import { vocabularyOf } from "./keywords"

const pg = dialectOf("postgres")
const mysql = dialectOf("mysql")
const words = vocabularyOf("postgres")

const model = {
  defaultSchema: "public",
  schemas: ["public", "analytics"],
  tables: [
    { schema: "public", name: "orders", type: "table", columns: ["id", "customer_id", "status"] },
    { schema: "public", name: "customers", type: "table", columns: ["id", "email", "tier"] },
    { schema: "public", name: "Mixed Case Table", type: "table", columns: ["Id", "select"] },
    { schema: "public", name: "order_summary", type: "view", columns: ["id", "email"] },
    { schema: "analytics", name: "events", type: "table", columns: ["id", "kind"] },
  ],
}

/** The suggestions where `|` stands in the text. */
const at = (marked, dialect = pg, schema = model) => {
  const offset = marked.indexOf("|")
  return suggest(schema, dialect, words, marked.replace("|", ""), offset)
}
const labels = (list, kind) =>
  list.filter((item) => kind === undefined || item.kind === kind).map((item) => item.label)

describe("the tables a statement names", () => {
  test("FROM and JOIN, with and without AS, qualified or not", () => {
    expect(
      tableRefs("select * from orders o join public.customers as c on c.id = o.customer_id", pg),
    ).toEqual([
      { schema: "", name: "orders", alias: "o" },
      { schema: "public", name: "customers", alias: "c" },
    ])
  })
  test("a list of tables after FROM", () => {
    expect(tableRefs("select * from orders o, customers c where o.id = 1", pg)).toEqual([
      { schema: "", name: "orders", alias: "o" },
      { schema: "", name: "customers", alias: "c" },
    ])
  })
  test("a keyword after the table is not its alias", () => {
    expect(tableRefs("select * from orders where id = 1", pg)).toEqual([
      { schema: "", name: "orders", alias: "" },
    ])
    expect(tableRefs("update orders set status = 'x'", pg)).toEqual([
      { schema: "", name: "orders", alias: "" },
    ])
  })
  test("a quoted name is read without its marks", () => {
    expect(tableRefs('select * from "Mixed Case Table" m', pg)).toEqual([
      { schema: "", name: "Mixed Case Table", alias: "m" },
    ])
    expect(tableRefs("select * from `blog`.`posts` p", mysql)).toEqual([
      { schema: "blog", name: "posts", alias: "p" },
    ])
  })
  test("a comma in a select list introduces no table", () => {
    expect(tableRefs("select a, b from orders", pg)).toEqual([
      { schema: "", name: "orders", alias: "" },
    ])
  })
})

describe("what is offered at the cursor", () => {
  test("after an alias and a dot: that table's columns and nothing else", () => {
    const list = at("select o.| from orders o join customers c on c.id = o.customer_id")
    expect(labels(list)).toEqual(["id", "customer_id", "status"])
  })
  test("the same while the word is half typed", () => {
    expect(labels(at("select c.em| from customers c"))).toEqual(["id", "email", "tier"])
  })
  test("after a table's own name and a dot", () => {
    expect(labels(at("select orders.| from orders"))).toEqual(["id", "customer_id", "status"])
  })
  test("after a schema and a dot: what the schema holds", () => {
    expect(labels(at("select * from analytics.|"))).toEqual(["events"])
  })
  test("after schema.table and a dot: its columns", () => {
    expect(labels(at("select analytics.events.| from analytics.events"))).toEqual(["id", "kind"])
  })
  test("a name nobody knows offers nothing after its dot", () => {
    expect(at("select x.| from orders o")).toEqual([])
  })
  test("after FROM and JOIN: tables, the connection's own schema first, others qualified", () => {
    const list = at("select * from |")
    expect(labels(list, "table").slice(0, 3)).toEqual(["orders", "customers", "Mixed Case Table"])
    expect(labels(list, "view")).toEqual(["order_summary"])
    expect(labels(list)).toContain("analytics.events")
    expect(labels(list, "keyword")).toEqual([])
    expect(labels(at("select * from orders o join |"), "table")).toContain("customers")
  })
  test("after a comma in a FROM list too, and not after one in a select list", () => {
    expect(labels(at("select * from orders o, |"), "column")).toEqual([])
    expect(labels(at("select id, | from orders"), "column")).toEqual([
      "id",
      "customer_id",
      "status",
    ])
  })
  test("elsewhere: the named tables' columns first, then aliases, tables, functions, keywords", () => {
    const list = at("select | from orders o")
    const ranks = Object.fromEntries(list.map((item) => [`${item.kind}:${item.label}`, item.rank]))
    expect(ranks["column:status"]).toBe(0)
    expect(ranks["alias:o"]).toBe(1)
    expect(ranks["table:customers"]).toBe(2)
    expect(ranks["function:COUNT"]).toBeGreaterThan(ranks["table:customers"])
    expect(ranks["keyword:WHERE"]).toBeGreaterThan(ranks["function:COUNT"])
    // The columns of a table the statement does not name are not offered.
    expect(labels(list, "column")).not.toContain("tier")
  })
  test("with no table named yet, every column is offered once", () => {
    const columns = labels(at("select |"), "column")
    expect(columns.filter((name) => name === "id")).toHaveLength(1)
    expect(columns).toContain("tier")
  })
  test("only the statement the cursor is in is read", () => {
    const list = at("select * from customers c;\nselect o.| from orders o")
    expect(labels(list)).toEqual(["id", "customer_id", "status"])
    expect(labels(at("select * from customers c;\nselect | from orders o"), "alias")).toEqual(["o"])
  })
  test("a name is written as the engine needs it: quoted where it would not read back", () => {
    const list = at("select * from |")
    expect(list.find((item) => item.label === "Mixed Case Table")?.insert).toBe(
      '"Mixed Case Table"',
    )
    expect(list.find((item) => item.label === "orders")?.insert).toBe("orders")
    const columns = at('select m.| from "Mixed Case Table" m')
    expect(columns.map((item) => item.insert)).toEqual(['"Id"', '"select"'])
    const my = at("select * from |", mysql)
    expect(my.find((item) => item.label === "Mixed Case Table")?.insert).toBe("`Mixed Case Table`")
  })
  test("a function is written with its parentheses", () => {
    expect(at("select |").find((item) => item.label === "COUNT")?.insert).toBe("COUNT($0)")
  })
  test("nothing inside a text, a comment or an open text", () => {
    expect(at("select 'from |' from orders")).toEqual([])
    expect(at("select 1 -- from |")).toEqual([])
    expect(at("select /* o.| */ 1 from orders o")).toEqual([])
    expect(at("select 'never closed |")).toEqual([])
    expect(labels(at("select 'done' | from orders"), "column")).toContain("status")
  })
})

describe("the model the completion reads", () => {
  const outline = {
    schema: "",
    tables: { "public.orders": ["id"], "analytics.events": ["id", "kind"] },
    entries: [
      { id: "public.orders", schema: "public", name: "orders", type: "table" },
      { id: "analytics.events", schema: "analytics", name: "events", type: "table" },
    ],
    truncated: false,
    total: 2,
    limit: 5000,
  }
  test("the outline's tables with the catalogue's word on which schema is the connection's", () => {
    const head = {
      schema: "public",
      defaultSchema: "public",
      schemas: [
        { name: "public", tables: 1, default: true },
        { name: "analytics", tables: 1 },
        { name: "pg_catalog", tables: 140, system: true },
      ],
    }
    expect(schemaModel(outline, head)).toEqual({
      defaultSchema: "public",
      schemas: ["public", "analytics"],
      tables: [
        { schema: "public", name: "orders", type: "table", columns: ["id"] },
        { schema: "analytics", name: "events", type: "table", columns: ["id", "kind"] },
      ],
    })
  })
  test("nothing read yet is an empty model, and a list where an outline should be is one too", () => {
    expect(schemaModel(undefined, undefined).tables).toEqual([])
    expect(schemaModel([], undefined).tables).toEqual([])
  })
})
