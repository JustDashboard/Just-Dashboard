import { describe, expect, test } from "bun:test"
import catalogue from "../../../../../backend/internal/api/testdata/database-drivers.json"
import {
  addEnumValueRequest,
  alterColumnRequest,
  blankColumn,
  commentRequest,
  constraintRequest,
  createEnumRequest,
  dropConstraintRequest,
  enumLabels,
  enumProblem,
  foreignKeyRequest,
  indexRequest,
  keyPreset,
  qualified,
  renameColumnRequest,
  renameTableRequest,
  requestKey,
  spendsBudget,
  tableLimits,
  tableProblems,
  tableRequest,
  truncateRequest,
  dropColumnRequest,
  viewQuery,
  viewRequest,
  BLANK_INDEX,
} from "./changes"

const column = (over) => blankColumn(over)
const typesOf = (driver) => catalogue.find((entry) => entry.id === driver).columnTypes
const operationsOf = (driver) =>
  catalogue.find((entry) => entry.id === driver).capabilities.ddlOperations

describe("a new table", () => {
  test("a column with a name and no type stops the form and is named", () => {
    const half = column({ name: "note" })
    const draft = {
      schema: "public",
      name: "invoices",
      columns: [column({ name: "id", type: "bigserial", primaryKey: true }), half],
    }
    const problems = tableProblems(draft)
    expect(problems.columns[half.key]).toBe("note has no type.")
    expect(problems.summary).toBe("note has no type.")
    // The old form made the table without the column. Here nothing is asked.
    expect(tableRequest(draft)).toBeNull()
  })

  test("a type with no name is typed work too", () => {
    const half = column({ type: "text" })
    const problems = tableProblems({ schema: "", name: "t", columns: [half] })
    expect(problems.columns[half.key]).toBe("This column has no name.")
  })

  test("a row nobody typed in is passed over, not counted against the form", () => {
    const request = tableRequest({
      schema: "public",
      name: " invoices ",
      columns: [column({ name: "id", type: "bigserial", primaryKey: true }), column(), column()],
    })
    expect(request).toEqual({
      method: "POST",
      path: "/ddl/table",
      body: {
        schema: "public",
        table: "invoices",
        columns: [{ name: "id", type: "bigserial", primaryKey: true }],
      },
    })
  })

  test("the table needs a name, a column, and no column twice", () => {
    expect(tableProblems({ schema: "", name: "", columns: [] }).summary).toBe("Name the table.")
    expect(tableProblems({ schema: "", name: "t", columns: [column()] }).summary).toBe(
      "Add at least one column.",
    )
    const twice = column({ name: "ID", type: "int" })
    const problems = tableProblems({
      schema: "",
      name: "t",
      columns: [column({ name: "id", type: "int" }), twice],
    })
    expect(problems.columns[twice.key]).toBe("ID is named twice.")
  })

  test("two unfinished columns are counted, each named on its own row", () => {
    const problems = tableProblems({
      schema: "",
      name: "t",
      columns: [column({ name: "a" }), column({ type: "int" })],
    })
    expect(problems.summary).toBe("2 columns are not finished.")
  })

  test("only what was set is sent, and the default as typed", () => {
    const request = tableRequest({
      schema: "main",
      name: "t",
      columns: [
        column({ name: "at", type: "TEXT", notNull: true, default: " CURRENT_TIMESTAMP " }),
      ],
    })
    expect(request.body.columns).toEqual([
      { name: "at", type: "TEXT", notNull: true, default: "CURRENT_TIMESTAMP" },
    ])
  })
})

describe("the key a new table starts with, by the engine's own type list", () => {
  test("PostgreSQL numbers a bigserial", () => {
    expect(keyPreset(typesOf("postgres")).column).toEqual({
      name: "id",
      type: "bigserial",
      notNull: false,
      primaryKey: true,
      default: "",
    })
  })

  test("MySQL and MariaDB number a serial — never the plain bigint the old form wrote", () => {
    expect(keyPreset(typesOf("mysql")).column.type).toBe("serial")
  })

  test("SQLite numbers an INTEGER primary key as the row id", () => {
    expect(keyPreset(typesOf("sqlite")).column.type).toBe("INTEGER")
  })

  test("SQL Server gets a key that fills itself, and is told it is not numbered", () => {
    const preset = keyPreset(typesOf("sqlserver"))
    expect(preset.column).toMatchObject({ type: "uniqueidentifier", default: "NEWID()" })
    expect(preset.says).toContain("IDENTITY")
    expect(preset.label).toBe("id, self-filling")
  })

  test("an engine none of it fits is offered no key rather than one that only looks like one", () => {
    expect(keyPreset(typesOf("oracle"))).toBeNull()
    expect(keyPreset(typesOf("clickhouse"))).toBeNull()
    expect(keyPreset([])).toBeNull()
  })
})

describe("a column changed in place", () => {
  const status = { name: "status", type: "text", nullable: false, default: "'new'", position: 1 }

  test("nothing changed is nothing to ask", () => {
    const edit = { type: "text", using: "", nullable: false, default: "'new'" }
    expect(alterColumnRequest("public", "orders", status, edit)).toBeNull()
  })

  test("only the part that changed is sent: a default the engine wrote is never sent back", () => {
    const serial = {
      name: "id",
      type: "bigint",
      nullable: false,
      default: "nextval('s'::regclass)",
      position: 1,
    }
    const request = alterColumnRequest("public", "orders", serial, {
      type: "bigint",
      using: "",
      nullable: true,
      default: "nextval('s'::regclass)",
    })
    expect(request.body).toEqual({ schema: "public", table: "orders", name: "id", nullable: true })
  })

  test("an emptied default is dropped; a typed one is set", () => {
    const drop = alterColumnRequest("public", "orders", status, {
      type: "text",
      using: "",
      nullable: false,
      default: "",
    })
    expect(drop.body).toEqual({
      schema: "public",
      table: "orders",
      name: "status",
      dropDefault: true,
    })
    const set = alterColumnRequest("public", "orders", status, {
      type: "text",
      using: "",
      nullable: false,
      default: "'open'",
    })
    expect(set.body.default).toBe("'open'")
  })

  test("a conversion goes only with a type change", () => {
    const kept = alterColumnRequest("public", "orders", status, {
      type: "text",
      using: "status::text",
      nullable: true,
      default: "'new'",
    })
    expect(kept.body.using).toBeUndefined()
    const changed = alterColumnRequest("public", "orders", status, {
      type: "integer",
      using: "status::integer",
      nullable: false,
      default: "'new'",
    })
    expect(changed.body).toMatchObject({ type: "integer", using: "status::integer" })
    expect(changed.method).toBe("PATCH")
  })
})

describe("names, comments, indexes, keys", () => {
  test("a rename to the same name, or to nothing, is not a rename", () => {
    expect(renameColumnRequest("s", "t", "a", "a")).toBeNull()
    expect(renameTableRequest("s", "t", "  ")).toBeNull()
    expect(renameColumnRequest("s", "t", "a", " b ").body).toEqual({
      schema: "s",
      table: "t",
      kind: "column",
      name: "a",
      to: "b",
    })
    expect(renameTableRequest("s", "t", "u").body).toEqual({ schema: "s", table: "t", to: "u" })
  })

  test("a comment emptied is sent as the empty comment, which removes it", () => {
    expect(commentRequest("s", "t", undefined, "same", "same")).toBeNull()
    expect(commentRequest("s", "t", "c", "", "was").body).toEqual({
      schema: "s",
      table: "t",
      column: "c",
      comment: "",
    })
  })

  test("an index is its columns in the order chosen, and only the options set", () => {
    expect(indexRequest("s", "t", BLANK_INDEX)).toBeNull()
    const request = indexRequest("s", "t", {
      ...BLANK_INDEX,
      fields: ["status", "placed_at"],
      where: " status = 'open' ",
      unique: true,
    })
    expect(request.body).toEqual({
      schema: "s",
      table: "t",
      fields: ["status", "placed_at"],
      unique: true,
      where: "status = 'open'",
    })
  })

  test("a foreign key waits for every pair, and says its schema only when it is another", () => {
    const draft = {
      name: "",
      columns: ["customer_id"],
      refSchema: "public",
      refTable: "customers",
      refColumns: [""],
      onDelete: "NO ACTION",
      onUpdate: "CASCADE",
    }
    expect(foreignKeyRequest("public", "orders", draft)).toBeNull()
    const whole = foreignKeyRequest("public", "orders", { ...draft, refColumns: ["id"] })
    expect(whole.body).toEqual({
      schema: "public",
      table: "orders",
      columns: ["customer_id"],
      refTable: "customers",
      refColumns: ["id"],
      onUpdate: "CASCADE",
    })
    const across = foreignKeyRequest("public", "orders", {
      ...draft,
      refSchema: "crm",
      refColumns: ["id"],
    })
    expect(across.body.refSchema).toBe("crm")
  })

  test("a constraint is a set of columns or a condition, by its kind", () => {
    expect(
      constraintRequest("s", "t", { type: "unique", name: "", columns: [], expression: "x > 0" }),
    ).toBeNull()
    expect(
      constraintRequest("s", "t", { type: "check", name: "", columns: ["a"], expression: " " }),
    ).toBeNull()
    expect(
      constraintRequest("s", "t", {
        type: "check",
        name: "positive",
        columns: [],
        expression: "x > 0",
      }).body,
    ).toEqual({ schema: "s", table: "t", type: "check", expression: "x > 0", name: "positive" })
    // The primary key has no kind to give: the server reads it from the name.
    expect(dropConstraintRequest("s", "t", "PRIMARY").body).toEqual({
      schema: "s",
      table: "t",
      name: "PRIMARY",
    })
  })
})

describe("views and enum types", () => {
  test("the query inside a view's own definition", () => {
    expect(viewQuery('CREATE OR REPLACE VIEW "public"."v" AS\nSELECT 1 AS one;')).toBe(
      "SELECT 1 AS one",
    )
    expect(
      viewQuery(
        "CREATE ALGORITHM=UNDEFINED DEFINER=`a`@`%` SQL SECURITY DEFINER VIEW `v` AS select `t`.`id` AS `id` from `t`",
      ),
    ).toBe("select `t`.`id` AS `id` from `t`")
    // A name that holds the word is quoted, and is skipped as a name.
    expect(viewQuery('CREATE VIEW "seen AS new" AS SELECT 2')).toBe("SELECT 2")
    expect(viewQuery("CREATE VIEW [dbo].[v] (a, b) AS SELECT a, b FROM t")).toBe(
      "SELECT a, b FROM t",
    )
    expect(viewQuery("CREATE TABLE t (a int)")).toBeNull()
  })

  test("a pasted query's closing semicolon is the reader's habit, not a second statement", () => {
    const request = viewRequest({
      schema: "public",
      name: "v",
      query: "select 1;\n",
      replace: true,
      materialized: false,
    })
    expect(request.body).toEqual({ schema: "public", name: "v", query: "select 1", replace: true })
  })

  test("labels are the lines that hold something, checked before they are sent", () => {
    expect(enumLabels(" a \n\n b\n")).toEqual(["a", "b"])
    expect(enumProblem(["a", "b", "a"])).toBe("a is listed twice.")
    expect(createEnumRequest("public", "mood", "ok\nok")).toBeNull()
    expect(createEnumRequest("public", "mood", "sad\nok").body.values).toEqual(["sad", "ok"])
  })

  test("a new label goes last, or beside one the type has", () => {
    expect(addEnumValueRequest("public", "mood", "fine", { at: "end" }).body).toEqual({
      schema: "public",
      name: "mood",
      value: "fine",
    })
    expect(
      addEnumValueRequest("public", "mood", "fine", { at: "before", label: "ok" }).body.before,
    ).toBe("ok")
    expect(addEnumValueRequest("public", "mood", " ", { at: "end" })).toBeNull()
  })
})

describe("what a request costs, and what an engine's forms cannot do", () => {
  test("a preview of a drop or of emptying a table spends the destructive budget", () => {
    expect(spendsBudget(dropColumnRequest("s", "t", "c"))).toBe(true)
    expect(spendsBudget(truncateRequest("s", "t"))).toBe(true)
    expect(spendsBudget(renameTableRequest("s", "t", "u"))).toBe(false)
  })

  test("a request is its route and its body: the same form is the same question", () => {
    expect(requestKey(null)).toBe("")
    expect(requestKey(truncateRequest("s", "t"))).toBe(requestKey(truncateRequest("s", "t")))
    expect(requestKey(truncateRequest("s", "t"))).not.toBe(requestKey(truncateRequest("s", "u")))
  })

  test("SQLite is told what it cannot change, from the server's own list", () => {
    const limits = tableLimits(operationsOf("sqlite"), "SQLite")
    expect(limits.columns[0]).toContain("cannot change a column in place")
    expect(limits.columns[1]).toContain("no comments")
    expect(limits.keys.join(" ")).toContain("unique index")
    expect(limits.keys.join(" ")).toContain("Foreign keys cannot be added")
    expect(limits.indexes).toEqual([])
  })

  test("ClickHouse is told an index is made in Query, and is not offered a unique index it cannot make", () => {
    const limits = tableLimits(operationsOf("clickhouse"), "ClickHouse")
    expect(limits.indexes[0]).toContain("made in Query")
    expect(limits.keys.some((line) => line.includes("unique index"))).toBe(false)
    expect(limits.columns).toEqual([])
  })

  test("PostgreSQL has nothing to be told", () => {
    expect(tableLimits(operationsOf("postgres"), "PostgreSQL")).toEqual({
      columns: [],
      indexes: [],
      keys: [],
    })
  })

  test("a name is said with its schema where it has one", () => {
    expect(qualified("public", "orders")).toBe("public.orders")
    expect(qualified("", "notes")).toBe("notes")
  })
})
