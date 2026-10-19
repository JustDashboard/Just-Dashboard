import { describe, expect, test } from "bun:test"
import {
  chooseGenerator,
  choiceGroups,
  newSeed,
  planLine,
  pruneChoices,
  readSeedPlan,
  readSeedReport,
  reportLine,
  reportTitle,
  requestedChoices,
  rowsFrom,
  seedKey,
  seedRequest,
  seedTargets,
  tableLabel,
} from "./seed-model"

const plan = {
  seed: 42,
  rows: 33,
  atomic: true,
  warnings: [],
  tables: [
    {
      schema: "public",
      name: "users",
      rows: 25,
      asked: 25,
      existing: 3,
      after: [],
      statement: 'INSERT INTO "public"."users" ("email") VALUES ($1)',
      notes: [],
      columns: [
        {
          name: "id",
          type: "integer",
          generator: "auto",
          label: "Numbered by the server",
          choices: [],
          later: "numbered by the server",
        },
        {
          name: "email",
          type: "varchar(60)",
          generator: "word",
          inferred: "email",
          label: "Word",
          unique: true,
          choices: [
            { id: "email", label: "Email address", group: "Person" },
            { id: "fullName", label: "Full name", group: "Person" },
            { id: "word", label: "Word", group: "Text" },
          ],
        },
      ],
      preview: [[null, "ann.ok.1@example.com"]],
    },
    {
      schema: "public",
      name: "orders",
      rows: 8,
      asked: 10,
      existing: 0,
      after: ["users"],
      statement: "",
      notes: ["capped"],
      columns: [
        {
          name: "user_id",
          type: "integer",
          generator: "reference",
          label: "A row of users",
          choices: [{ id: "reference", label: "A row of users", group: "Server" }],
          reference: { schema: "public", table: "users", columns: ["id"], seeded: true },
          later: "a new users row",
        },
      ],
      preview: [[null], "not a row", [7]],
    },
    { schema: "public", name: "shapes", rows: 0, asked: 5, problem: "no generator", columns: [] },
  ],
}

describe("reading a plan", () => {
  test("keeps what the server said, and only that", () => {
    const read = readSeedPlan(plan)
    expect(read.seed).toBe(42)
    expect(read.tables.map((t) => t.name)).toEqual(["users", "orders", "shapes"])
    const [users, orders, shapes] = read.tables
    expect(users.columns[1]).toMatchObject({
      generator: "word",
      inferred: "email",
      unique: true,
      nullable: false,
    })
    // A server that does not say what it would have chosen chose what it shows.
    expect(orders.columns[0].inferred).toBe("reference")
    expect(users.columns[0].later).toBe("numbered by the server")
    expect(orders.columns[0].reference).toEqual({
      schema: "public",
      table: "users",
      columns: ["id"],
      seeded: true,
    })
    // A cell that is not text is no value, and a row that is not a row is empty.
    expect(orders.preview).toEqual([[null], [null], [null]])
    expect(shapes).toMatchObject({
      problem: "no generator",
      rows: 0,
      existing: -1,
      after: [],
      notes: [],
    })
  })

  test("an answer with nothing in it is a plan of nothing", () => {
    expect(readSeedPlan(null)).toEqual({ seed: 0, rows: 0, atomic: true, warnings: [], tables: [] })
    expect(readSeedPlan({ atomic: false }).atomic).toBe(false)
  })

  test("says what it writes in a line", () => {
    expect(planLine(readSeedPlan(plan))).toBe("2 tables · 33 rows · 1 table left out")
    expect(planLine(readSeedPlan({ rows: 1, tables: [{ name: "a", rows: 1 }] }))).toBe(
      "1 table · 1 row",
    )
  })
})

describe("the request", () => {
  const targets = [
    { schema: "public", name: "users" },
    { schema: "sales", name: "orders" },
  ]

  test("names every table with its schema, its rows and only the generators changed", () => {
    const body = seedRequest({
      seed: 7,
      targets,
      rows: 25,
      perTable: { [seedKey("sales", "orders")]: 400 },
      choices: {
        [seedKey("public", "users")]: { email: "word" },
        [seedKey("sales", "orders")]: {},
      },
    })
    expect(body).toEqual({
      seed: 7,
      tables: [
        { schema: "public", name: "users", rows: 25, columns: { email: "word" } },
        { schema: "sales", name: "orders", rows: 400 },
      ],
    })
  })

  test("a generator chosen again as the server chose it is forgotten", () => {
    const key = seedKey("public", "users")
    let choices = chooseGenerator({}, key, "email", "word", "email")
    expect(choices).toEqual({ [key]: { email: "word" } })
    choices = chooseGenerator(choices, key, "email", "email", "email")
    expect(choices).toEqual({})
  })

  test("a choice the server set aside is forgotten, and one made since is kept", () => {
    const users = seedKey("public", "users")
    const orders = seedKey("public", "orders")
    const read = readSeedPlan(plan)
    // The plan answered a request that chose "word" for email and "fullName"
    // for a column users no longer has; it kept the first and not the second.
    const asked = requestedChoices(
      JSON.stringify({
        tables: [{ schema: "public", name: "users", columns: { email: "word", gone: "fullName" } }],
      }),
    )
    expect(asked).toEqual({ [users]: { email: "word", gone: "fullName" } })
    const held = { [users]: { email: "word", gone: "fullName" }, [orders]: { status: "text" } }
    expect(pruneChoices(held, asked, read)).toEqual({
      [users]: { email: "word" },
      [orders]: { status: "text" },
    })
    // Changed since that request was sent: not the plan's to judge.
    const since = { [users]: { email: "word", gone: "email" } }
    expect(pruneChoices(since, asked, read)).toBe(since)
  })

  test("every table, or the ones ticked", () => {
    const name = (t) => tableLabel(t, true)
    expect(seedTargets(targets, [], name)).toEqual(targets)
    expect(seedTargets(targets, ["sales.orders"], name)).toEqual([targets[1]])
    expect(tableLabel(targets[1], false)).toBe("orders")
  })

  test("a row count is a whole number the server takes", () => {
    expect(rowsFrom("25")).toBe(25)
    expect(rowsFrom(" 10000 ")).toBe(10000)
    for (const bad of ["", "0", "10001", "2.5", "-3", "ten"]) expect(rowsFrom(bad)).toBeUndefined()
  })

  test("a new seed is a number the server takes", () => {
    expect(newSeed(() => 0)).toBe(1)
    expect(newSeed(() => 0.9999999)).toBe(999999)
  })
})

test("a column's choices are grouped in the order the server listed them", () => {
  const groups = choiceGroups(readSeedPlan(plan).tables[0].columns[1].choices)
  expect(groups.map((g) => [g.group, g.choices.map((c) => c.id)])).toEqual([
    ["Person", ["email", "fullName"]],
    ["Text", ["word"]],
  ])
})

describe("reading a report", () => {
  const report = readSeedReport({
    seed: 42,
    inserted: 30,
    skipped: 2,
    atomic: true,
    tables: [
      { schema: "public", name: "users", rows: 25, inserted: 25, skipped: 0, errors: [] },
      {
        schema: "public",
        name: "orders",
        rows: 7,
        inserted: 5,
        skipped: 2,
        errors: [{ row: 3, line: 3, message: "duplicate key" }, { row: "x" }],
      },
      { schema: "public", name: "shapes", problem: "no generator" },
    ],
  })

  test("keeps each table's counts and the reasons rows were refused", () => {
    expect(report.tables[1].errors).toEqual([{ row: 3, message: "duplicate key" }])
    expect(reportTitle(report)).toBe("Inserted 30 rows into 2 tables")
    expect(report.tables.map(reportLine)).toEqual([
      "25 inserted",
      "5 inserted · 2 refused",
      "left out",
    ])
  })

  test("a seed that wrote nothing says so", () => {
    expect(reportTitle(readSeedReport({ tables: [] }))).toBe("Nothing was inserted")
  })
})
