import { describe, expect, test } from "bun:test"
import catalogue from "../../../../../backend/internal/api/testdata/database-drivers.json"
import {
  blankArg,
  blankRoutine,
  blankTrigger,
  dropRoutineRequest,
  dropTriggerRequest,
  routineAbilities,
  routineDraftOf,
  routineProblems,
  routineRequest,
  splitQualified,
  starterBody,
  triggerAbilities,
  triggerDraftOf,
  triggerFunctions,
  triggerProblems,
  triggerRequest,
  triggerTables,
} from "./programs"
import { spendsBudget } from "./changes"

const operationsOf = (driver, flavor) => {
  const entry = catalogue.find((item) => item.id === driver)
  const flavored = flavor && entry.flavors.find((item) => item.id === flavor)
  return (flavored ?? entry).capabilities.ddlOperations
}

describe("which engines are offered which program", () => {
  test("the capability table is what decides, per engine and per flavour", () => {
    for (const driver of ["postgres", "mysql", "sqlserver", "oracle"]) {
      for (const op of ["functions", "procedures", "triggers"]) {
        expect(operationsOf(driver)).toContain(op)
      }
    }
    expect(operationsOf("sqlite")).toContain("triggers")
    expect(operationsOf("sqlite")).not.toContain("functions")
    expect(operationsOf("sqlite")).not.toContain("procedures")
    for (const op of ["functions", "procedures", "triggers"]) {
      expect(operationsOf("clickhouse")).not.toContain(op)
      expect(operationsOf("postgres", "cockroachdb")).not.toContain(op)
      expect(operationsOf("mysql", "tidb")).not.toContain(op)
      expect(operationsOf("mysql", "mariadb")).toContain(op)
      expect(operationsOf("postgres", "timescaledb")).toContain(op)
    }
  })
})

describe("a routine form", () => {
  test("each engine has the fields it has a word for and no others", () => {
    const pg = routineAbilities("postgres", "function")
    expect(pg).toMatchObject({
      language: true,
      volatility: true,
      strict: true,
      security: true,
      defaults: true,
    })
    expect(pg.modes).toEqual(["out", "inout"])
    expect(routineAbilities("postgres", "procedure").volatility).toBe(false)

    const mysql = routineAbilities("mysql", "function")
    expect(mysql).toMatchObject({
      deterministic: true,
      dataAccess: true,
      security: true,
      defaults: false,
      language: false,
    })
    expect(mysql.modes).toEqual([])
    expect(routineAbilities("mysql", "procedure").modes).toEqual(["out", "inout"])
    expect(routineAbilities("mysql", "procedure").deterministic).toBe(false)

    expect(routineAbilities("sqlserver", "procedure").modes).toEqual(["out"])
    expect(routineAbilities("sqlserver", "function").modes).toEqual([])
    expect(routineAbilities("oracle", "function")).toMatchObject({
      deterministic: true,
      defaults: true,
    })
    // Procedures have no return type anywhere.
    expect(routineAbilities("oracle", "procedure").returns).toBe(false)
  })

  test("a new one starts from the engine's own frame", () => {
    expect(blankRoutine("postgres", "function", "public")).toMatchObject({
      language: "plpgsql",
      schema: "public",
      body: "BEGIN\n  RETURN NULL;\nEND;",
    })
    expect(starterBody("postgres", "function", "sql")).toBe("SELECT NULL")
    expect(starterBody("mysql", "procedure")).toBe("BEGIN\n  SELECT 1;\nEND")
    expect(starterBody("sqlserver", "procedure")).toBe("SELECT 1")
    expect(starterBody("oracle", "procedure")).toBe("BEGIN\n  NULL;\nEND;")
  })

  test("nothing is asked of the server until the form says enough", () => {
    const abilities = routineAbilities("postgres", "function")
    const draft = blankRoutine("postgres", "function", "public")
    expect(routineRequest(draft, abilities)).toBeNull()
    expect(routineProblems(draft, abilities, false).summary).toBe("Name the routine.")

    draft.name = "add_tax"
    expect(routineProblems(draft, abilities, false).summary).toBe("Say what it returns.")
    draft.returns = "numeric"
    const half = blankArg({ name: "price" })
    draft.args = [half]
    expect(routineProblems(draft, abilities, false).args[half.key]).toBe(
      "Parameter 1 needs a type.",
    )
    expect(routineRequest(draft, abilities)).toBeNull()
    half.type = "numeric"

    expect(routineRequest(draft, abilities)).toEqual({
      method: "POST",
      path: "/ddl/routine",
      body: {
        kind: "function",
        schema: "public",
        name: "add_tax",
        args: [{ name: "price", mode: "in", type: "numeric" }],
        body: "BEGIN\n  RETURN NULL;\nEND;",
        returns: "numeric",
        language: "plpgsql",
      },
    })
    expect(spendsBudget(routineRequest(draft, abilities))).toBe(false)
  })

  test("OUT parameters stand in for a Postgres return type", () => {
    const abilities = routineAbilities("postgres", "function")
    const draft = {
      ...blankRoutine("postgres", "function", "public"),
      name: "split",
      args: [
        blankArg({ name: "s", type: "text" }),
        blankArg({ name: "head", mode: "out", type: "text" }),
      ],
    }
    expect(routineProblems(draft, abilities, false).returns).toBeUndefined()
    const sent = routineRequest(draft, abilities).body
    expect(sent.returns).toBeUndefined()
    expect(sent.args[1]).toEqual({ name: "head", mode: "out", type: "text" })
    // MySQL has no such thing: a function there returns something.
    const mysql = { ...blankRoutine("mysql", "function", "shop"), name: "f" }
    expect(routineProblems(mysql, routineAbilities("mysql", "function"), false).returns).toBe(
      "Say what it returns.",
    )
  })

  test("a field the engine has no word for is not sent", () => {
    const mysql = routineAbilities("mysql", "procedure")
    const draft = {
      ...blankRoutine("mysql", "procedure", "shop"),
      name: "bump",
      volatility: "stable",
      strict: true,
      language: "sql",
      args: [blankArg({ name: "n", type: "int", default: "1", mode: "out" })],
      security: "invoker",
      dataAccess: "reads sql data",
    }
    expect(routineRequest(draft, mysql).body).toEqual({
      kind: "procedure",
      schema: "shop",
      name: "bump",
      args: [{ name: "n", mode: "out", type: "int" }],
      body: "BEGIN\n  SELECT 1;\nEND",
      dataAccess: "reads sql data",
      security: "invoker",
    })
    // SQL Server has no INOUT: a mode it cannot say is sent as IN.
    const mssql = routineAbilities("sqlserver", "procedure")
    const asked = routineRequest(
      {
        ...blankRoutine("sqlserver", "procedure", "dbo"),
        name: "p",
        args: [blankArg({ name: "n", type: "int", mode: "inout" })],
      },
      mssql,
    )
    expect(asked.body.args[0].mode).toBe("in")
  })

  test("a blank parameter row is ignored, and an edit names the routine it replaces", () => {
    const abilities = routineAbilities("oracle", "function")
    const read = {
      kind: "function",
      schema: "APP",
      name: "ADD_ONE",
      args: [{ name: "N", mode: "in", type: "NUMBER", default: "1" }],
      returns: "NUMBER",
      body: "BEGIN RETURN n + 1; END;",
      deterministic: true,
      security: "invoker",
    }
    const draft = routineDraftOf(read)
    draft.args.push(blankArg())
    const request = routineRequest(draft, abilities, { signature: "N NUMBER" })
    expect(request.body.args).toEqual([{ name: "N", mode: "in", type: "NUMBER", default: "1" }])
    expect(request.body).toMatchObject({
      replaces: { signature: "N NUMBER" },
      deterministic: true,
      security: "invoker",
    })
    // An edit is not asked for a name: it keeps the routine's.
    expect(routineProblems({ ...draft, name: "" }, abilities, true).name).toBeUndefined()
  })

  test("a drop names the routine and the overload", () => {
    expect(dropRoutineRequest("function", "public", "armor", "bytea, text[]")).toEqual({
      method: "DELETE",
      path: "/ddl/routine",
      body: { kind: "function", schema: "public", name: "armor", signature: "bytea, text[]" },
    })
    expect(dropRoutineRequest("procedure", "shop", "bump", "").body).not.toHaveProperty("signature")
    expect(spendsBudget(dropRoutineRequest("function", "", "f", ""))).toBe(true)
  })
})

describe("a trigger form", () => {
  test("timings, events and clauses follow the engine", () => {
    expect(triggerAbilities("postgres")).toMatchObject({
      singleEvent: false,
      function: true,
      condition: true,
    })
    expect(triggerAbilities("postgres").events).toContain("truncate")
    expect(triggerAbilities("mysql")).toMatchObject({
      singleEvent: true,
      condition: false,
      forEach: ["row"],
    })
    expect(triggerAbilities("mysql").timings).toEqual(["before", "after"])
    expect(triggerAbilities("sqlserver")).toMatchObject({
      forEach: ["statement"],
      timings: ["after", "instead of"],
    })
    expect(triggerAbilities("oracle").events).not.toContain("truncate")
    expect(triggerAbilities("sqlite")).toMatchObject({ updateOf: true, singleEvent: true })
    expect(triggerAbilities("postgres").updateOf).toBe(false)
  })

  test("a Postgres trigger executes a function; the others carry a body", () => {
    const pg = triggerAbilities("postgres")
    const draft = { ...blankTrigger("postgres", "public", "orders"), name: "stamp" }
    expect(triggerProblems(draft, pg, false).summary).toBe("Choose the function it executes.")
    draft.function = "shop.touch"
    draft.functionArgs = "updated_at\n\n  created_at \n"
    draft.condition = " new.total > 0 "
    draft.events = ["insert", "update"]
    expect(triggerRequest(draft, pg, false)).toEqual({
      method: "POST",
      path: "/ddl/trigger",
      body: {
        schema: "public",
        name: "stamp",
        table: "orders",
        timing: "after",
        events: ["insert", "update"],
        forEach: "row",
        condition: "new.total > 0",
        function: "touch",
        functionSchema: "shop",
        functionArgs: ["updated_at", "created_at"],
      },
    })

    const mysql = triggerAbilities("mysql")
    const body = {
      ...blankTrigger("mysql", "shop", "orders"),
      name: "stamp",
      condition: "ignored",
      body: "",
    }
    expect(triggerProblems(body, mysql, false).summary).toBe("Write the body.")
    body.body = "SET @n = 1"
    const sent = triggerRequest(body, mysql, false).body
    expect(sent).toEqual({
      schema: "shop",
      name: "stamp",
      table: "orders",
      timing: "after",
      events: ["insert"],
      forEach: "row",
      body: "SET @n = 1",
    })
  })

  test("what the form needs is said in order, and an edit is not asked for a name", () => {
    const abilities = triggerAbilities("sqlserver")
    const draft = blankTrigger("sqlserver", "dbo")
    expect(triggerProblems(draft, abilities, false).summary).toBe("Name the trigger.")
    draft.name = "t"
    expect(triggerProblems(draft, abilities, false).summary).toBe("Choose the table it fires on.")
    draft.table = "orders"
    draft.events = []
    expect(triggerProblems(draft, abilities, false).summary).toBe("Choose at least one event.")
    draft.events = ["insert"]
    expect(triggerRequest(draft, abilities, false)).not.toBeNull()
    const edit = triggerRequest({ ...draft, name: "" }, abilities, true)
    expect(edit.body.replace).toBe(true)
    // SQLite narrows an update to columns, and only an update.
    const lite = triggerAbilities("sqlite")
    const update = {
      ...blankTrigger("sqlite", "main", "orders"),
      name: "t",
      events: ["update"],
      updateOf: ["total"],
      body: "SELECT 1",
    }
    expect(triggerRequest(update, lite, false).body.updateOf).toEqual(["total"])
    expect(
      triggerRequest({ ...update, events: ["insert"] }, lite, false).body.updateOf,
    ).toBeUndefined()
  })

  test("an edit is drawn from what the server read", () => {
    const draft = triggerDraftOf({
      schema: "public",
      name: "stamp",
      table: "orders",
      timing: "before",
      events: ["insert", "update"],
      forEach: "row",
      function: "touch",
      functionSchema: "public",
      functionArgs: ["a", "b"],
    })
    expect(draft).toMatchObject({
      function: "public.touch",
      functionArgs: "a\nb",
      condition: "",
      body: "",
    })
    expect(dropTriggerRequest("public", "stamp", "orders")).toEqual({
      method: "DELETE",
      path: "/ddl/trigger",
      body: { schema: "public", name: "stamp", table: "orders" },
    })
  })

  test("the catalogue offers the tables and the functions that return trigger", () => {
    const catalog = {
      objects: {
        tables: [{ name: "orders" }, { name: "items" }],
        views: [{ name: "order_summary" }],
        functions: [
          { schema: "public", name: "touch", returns: "trigger" },
          { schema: "public", name: "total", returns: "bigint" },
          { schema: "audit", name: "log_row", returns: "trigger" },
        ],
      },
    }
    expect(triggerTables(catalog)).toEqual(["orders", "items", "order_summary"])
    expect(triggerFunctions(catalog)).toEqual(["public.touch", "audit.log_row"])
    expect(triggerTables(undefined)).toEqual([])
    expect(splitQualified("audit.log_row")).toEqual({ schema: "audit", name: "log_row" })
    expect(splitQualified("touch")).toEqual({ schema: "", name: "touch" })
  })
})
