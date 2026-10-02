import { describe, expect, test } from "bun:test"
import {
  joinConditions,
  quotedReach,
  schemaModel,
  subjectAt,
  subjectNote,
  suggest,
  tableRefs,
} from "./completion"
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

describe("a name of the reader's own", () => {
  test("after AS nothing is offered: Enter would write a suggestion over the alias being typed", () => {
    expect(at("select count(*) as d|")).toEqual([])
    expect(at("select count(*) as |")).toEqual([])
    expect(at("select * from orders as o|")).toEqual([])
  })
  test("after a table in a FROM list the next word is its alias, and nothing is offered", () => {
    expect(at("select * from orders o|")).toEqual([])
    expect(at("select * from orders o join public.customers c|")).toEqual([])
    expect(at("select * from orders o, customers c|")).toEqual([])
    expect(at("update orders o|")).toEqual([])
  })
  test("…but the table itself is still completed, and so is what follows the alias", () => {
    expect(labels(at("select * from ord|"), "table")).toContain("orders")
    expect(labels(at("select * from orders o where st|"), "column")).toContain("status")
    expect(labels(at("select * from orders o, cust|"), "table")).toContain("customers")
  })
})

describe("joins from foreign keys", () => {
  const keyed = {
    ...model,
    relations: [
      {
        schema: "public",
        table: "orders",
        columns: ["customer_id"],
        refSchema: "public",
        refTable: "customers",
        refColumns: ["id"],
      },
      {
        schema: "public",
        table: "Mixed Case Table",
        columns: ["Id", "select"],
        refSchema: "public",
        refTable: "orders",
        refColumns: ["id", "status"],
      },
    ],
  }
  test("after ON the condition the key states comes first, the joined table's side written first", () => {
    const offered = at("select * from orders o join customers c on |", pg, keyed)
    expect(offered[0]).toMatchObject({
      kind: "join",
      label: "c.id = o.customer_id",
      insert: "c.id = o.customer_id",
    })
    // The columns are still there, after it.
    expect(labels(offered, "column")).toContain("email")
    const other = at("select * from customers c join orders o on |", pg, keyed)
    expect(labels(other, "join")).toEqual(["o.customer_id = c.id"])
  })
  test("a table with no alias is called by its name, and a key of several columns is one condition", () => {
    expect(
      labels(at('select * from orders join "Mixed Case Table" on |', pg, keyed), "join"),
    ).toEqual([
      '"Mixed Case Table"."Id" = orders.id AND "Mixed Case Table"."select" = orders.status',
    ])
  })
  test("tables with no key between them offer no join, and neither does a model without keys", () => {
    expect(
      labels(at("select * from customers c join analytics.events e on |", pg, keyed), "join"),
    ).toEqual([])
    expect(labels(at("select * from orders o join customers c on |"), "join")).toEqual([])
    expect(joinConditions(keyed, pg, [{ schema: "", name: "orders", alias: "o" }])).toEqual([])
  })
  test("after JOIN the tables a key ties to the ones already named come first", () => {
    const offered = at("select * from orders o join |", pg, keyed)
    const first = offered.filter((item) => item.rank === -1).map((item) => item.label)
    expect(first.sort()).toEqual(["Mixed Case Table", "customers"])
    expect(offered.find((item) => item.label === "customers").detail).toBe("joins orders")
    // FROM has nothing to be related to yet.
    expect(at("select * from |", pg, keyed).some((item) => item.rank === -1)).toBe(false)
  })
  test("the keys are read from the server's answer by the outline's own names", () => {
    const outline = {
      schema: "",
      tables: { "public.orders": ["id", "customer_id"], "public.customers": ["id"] },
      entries: [
        { id: "public.orders", schema: "public", name: "orders", type: "table" },
        { id: "public.customers", schema: "public", name: "customers", type: "table" },
      ],
    }
    const answer = {
      "public.orders": [
        {
          name: "fk",
          columns: ["customer_id"],
          refSchema: "public",
          refTable: "customers",
          refColumns: ["id"],
        },
        { name: "broken", columns: ["a", "b"], refTable: "customers", refColumns: ["id"] },
      ],
      "public.gone": [{ columns: ["x"], refTable: "customers", refColumns: ["id"] }],
    }
    expect(schemaModel(outline, undefined, answer).relations).toEqual([
      {
        schema: "public",
        table: "orders",
        columns: ["customer_id"],
        refSchema: "public",
        refTable: "customers",
        refColumns: ["id"],
      },
    ])
    // A server without the route answers with a list: no keys, and no failure.
    expect(schemaModel(outline, undefined, []).relations).toBeUndefined()
  })
})

describe("what the pointer rests on", () => {
  const on = (marked) => {
    const offset = marked.indexOf("|")
    return subjectAt(model, pg, marked.replace("|", ""), offset)
  }
  test("a table, by its name or by the alias the statement gives it", () => {
    expect(on("select * from ord|ers o")).toMatchObject({
      kind: "table",
      table: { name: "orders" },
    })
    expect(on("select o|.id from orders o")).toMatchObject({
      kind: "table",
      table: { name: "orders" },
    })
    expect(on("select * from analytics.eve|nts")).toMatchObject({
      kind: "table",
      table: { schema: "analytics", name: "events" },
    })
  })
  test("a column, of the table its qualifier stands for or of a table the statement names", () => {
    expect(on("select o.stat|us from orders o")).toEqual({
      kind: "column",
      table: model.tables[0],
      column: "status",
    })
    expect(on("select em|ail from customers")).toMatchObject({ kind: "column", column: "email" })
    expect(on("select public.orders.customer_|id from orders")).toMatchObject({
      kind: "column",
      column: "customer_id",
    })
  })
  test("a word of the language, a text and a name nobody has are nothing", () => {
    expect(on("sel|ect 1")).toBeNull()
    expect(on("select 'ord|ers'")).toBeNull()
    expect(on("select nope|_column from orders")).toBeNull()
  })
  test("the note names the table and its columns, with their types where they were read", () => {
    const table = { kind: "table", table: model.tables[0] }
    expect(subjectNote(table, undefined)).toBe(
      "`public.orders` — table, 3 columns\n\n- `id`\n- `customer_id`\n- `status`",
    )
    expect(
      subjectNote({ kind: "column", table: model.tables[0], column: "id" }, [
        { name: "id", type: "bigint", nullable: false },
      ]),
    ).toBe("`id` `bigint` not null\n\nA column of `public.orders`.")
  })
})

describe("the stretch a completed name replaces", () => {
  const quotes = [['"', '"']]
  // Columns are 1-based; the cursor stands before the character at its column.
  const reach = (marked, pairs = quotes) => {
    const cursor = marked.indexOf("|") + 1
    const line = marked.replace("|", "")
    const word = /[\w$]*$/.exec(line.slice(0, cursor - 1))[0]
    return quotedReach(line, cursor - word.length, cursor, cursor, pairs)
  }
  const replaced = (marked, pairs) => {
    const { start, end } = reach(marked, pairs)
    return marked.replace("|", "").slice(start - 1, end - 1)
  }
  test("a plain word is the word", () => {
    expect(replaced("select * from ord|")).toBe("ord")
    expect(replaced("select co|")).toBe("co")
  })
  test("inside its quote a name is replaced quote and all — the closing one the editor typed too", () => {
    // The editor closes a quote as it is typed: completing left `"Mixed Case Table""`.
    expect(replaced('select * from "Mi|"')).toBe('"Mi"')
    expect(replaced('select * from "Mi|')).toBe('"Mi')
    expect(replaced('select * from "|"')).toBe('""')
  })
  test("a quoted name holds spaces: the reach runs from its opening mark", () => {
    expect(replaced('select * from "Mixed Ca|"')).toBe('"Mixed Ca"')
    expect(replaced('select "a b" from "Mi|"')).toBe('"Mi"')
  })
  test("a word after a name that is already closed is only the word", () => {
    expect(replaced('select "a b" x|')).toBe("x")
    expect(replaced('select "a b"x|')).toBe("x")
  })
  test("brackets and backticks are quotes where the engine reads them so", () => {
    expect(replaced("select * from [Mi|]", [["[", "]"]])).toBe("[Mi]")
    expect(replaced("select * from `po|`", [["`", "`"]])).toBe("`po`")
  })
})
