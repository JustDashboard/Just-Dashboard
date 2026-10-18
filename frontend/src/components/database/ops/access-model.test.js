import { describe, expect, test } from "bun:test"
import {
  accountCounts,
  accountDsn,
  accountKey,
  accountNameProblem,
  accountTags,
  aclChanged,
  aclDraftOf,
  aclRequest,
  aclSummary,
  attributeValue,
  cellKey,
  filterAccounts,
  grantSummary,
  grantsByAccount,
  heldLater,
  heldOn,
  isOwnAccount,
  lockoutReason,
  pendingWords,
  plannedRequests,
  ruleList,
  scopeKey,
  toggled,
  toggledAll,
} from "./access-model"

const role = (name, over = {}) => ({
  name,
  login: true,
  superuser: false,
  createDb: false,
  createRole: false,
  connectionLimit: -1,
  connections: 0,
  ...over,
})

const TABLE = {
  level: "table",
  privileges: ["SELECT", "INSERT", "UPDATE", "DELETE", "ALL"],
  needs: ["schema", "table"],
  allObjects: true,
  future: true,
  grantOption: true,
}
const DATABASE = {
  level: "database",
  privileges: ["CONNECT", "CREATE", "ALL"],
  needs: ["database"],
  grantOption: true,
}
const ROLE = { level: "role", privileges: [], needs: ["memberOf"] }
const LEVELS = [DATABASE, TABLE, ROLE]

const grant = (table, privileges, over = {}) => ({
  grantee: "app",
  level: "table",
  database: "shop",
  schema: "public",
  table,
  privileges,
  ...over,
})

describe("which account is which", () => {
  test("an account with a host is one account per host", () => {
    expect(accountKey({ name: "root", host: "%" })).toBe("root@%")
    expect(accountKey({ name: "app" })).toBe("app")
  })

  test("the connection's own account is found by name alone, whatever its case or host", () => {
    expect(isOwnAccount({ name: "JDTest" }, { user: "jdtest" })).toBe(true)
    expect(isOwnAccount({ name: "root", host: "localhost" }, { user: "root" })).toBe(true)
    expect(isOwnAccount({ name: "app" }, { user: "jdtest" })).toBe(false)
    // A connection with no account names nobody.
    expect(isOwnAccount({ name: "" }, { user: "" })).toBe(false)
  })

  test("the readings count what the list holds", () => {
    const roles = [
      role("root", { superuser: true, connections: 2 }),
      role("app", { connections: 1 }),
      role("group", { login: false }),
      role("old", { locked: true }),
    ]
    expect(accountCounts(roles)).toEqual({
      all: 4,
      admins: 1,
      sessions: 2,
      connections: 3,
      blocked: 2,
    })
    expect(filterAccounts(roles, "admins", "").map((one) => one.name)).toEqual(["root"])
    expect(filterAccounts(roles, "sessions", "").map((one) => one.name)).toEqual(["root", "app"])
    expect(filterAccounts(roles, "blocked", "").map((one) => one.name)).toEqual(["group", "old"])
    expect(filterAccounts(roles, "", "oO").map((one) => one.name)).toEqual(["root"])
  })

  test("an account's tags say what it is; an administrator's lesser rights are not repeated", () => {
    expect(accountTags(role("root", { superuser: true, createDb: true }), true)).toEqual([
      { word: "this connection" },
      { word: "administrator" },
    ])
    expect(accountTags(role("x", { locked: true, login: false }), false)).toEqual([
      { word: "locked", tone: "warning" },
    ])
    expect(accountTags(role("g", { login: false, createDb: true }), false)).toEqual([
      { word: "no login" },
      { word: "creates databases" },
    ])
  })

  test("a name the server would refuse, or that is taken", () => {
    expect(accountNameProblem("reporting", [])).toBeUndefined()
    expect(accountNameProblem("", [])).toBeDefined()
    expect(accountNameProblem("9lives", [])).toBeDefined()
    expect(accountNameProblem("has space", [])).toBeDefined()
    expect(accountNameProblem("App", ["app"])).toContain("already exists")
  })
})

describe("what an account's grants add up to", () => {
  test("an administrator has everything, whatever is listed", () => {
    expect(grantSummary(role("root", { superuser: true }), [])).toBe("Everything on the server")
  })

  test("objects are counted once each, by level", () => {
    const grants = [
      { grantee: "app", level: "database", database: "shop", privileges: ["CONNECT"] },
      {
        grantee: "app",
        level: "schema",
        database: "shop",
        schema: "public",
        privileges: ["USAGE"],
      },
      grant("orders", ["SELECT"]),
      grant("orders", ["UPDATE"]),
      grant("customers", ["SELECT"]),
    ]
    expect(grantSummary(role("app"), grants)).toBe("Granted on 1 database, 1 schema, 2 tables")
  })

  test("what is held by owning is counted apart, and a membership is named", () => {
    expect(
      grantSummary(role("owner", { memberOf: ["readers"] }), [
        grant("orders", ["SELECT"], { owner: true }),
      ]),
    ).toBe("Owns 1 object · member of readers")
  })

  test("signing in is not a grant", () => {
    expect(
      grantSummary(role("app"), [{ grantee: "app", level: "server", privileges: ["USAGE"] }]),
    ).toBe("No grant of its own")
    expect(
      grantSummary(role("app"), [
        { grantee: "app", level: "server", privileges: ["SELECT", "INSERT"] },
      ]),
    ).toBe("2 server privileges")
  })

  test("grants are kept by account, with the host where the server says one", () => {
    const map = grantsByAccount([
      { grantee: "app", host: "%", level: "database", database: "a", privileges: ["ALL"] },
      { grantee: "app", host: "10.%", level: "database", database: "b", privileges: ["ALL"] },
      { grantee: "reader", level: "database", database: "a", privileges: ["CONNECT"] },
    ])
    expect([...map.keys()]).toEqual(["app@%", "app@10.%", "reader"])
  })
})

describe("attributes, and the dashboard's own account", () => {
  const detail = {
    ...role("app", { createDb: true }),
    attributes: { inherit: true, replication: false, locked: true },
    members: [],
    config: [],
    grants: [],
    grantsTruncated: false,
    editable: [],
  }

  test("an attribute is read from wherever the detail keeps it", () => {
    expect(attributeValue(detail, "createDb")).toBe(true)
    expect(attributeValue(detail, "inherit")).toBe(true)
    expect(attributeValue(detail, "replication")).toBe(false)
    expect(attributeValue(detail, "locked")).toBe(true)
    expect(attributeValue(detail, "bypassRls")).toBe(false)
  })

  test("what would lock the dashboard out is held, and only on its own account", () => {
    expect(lockoutReason("login", false, true)).toBeDefined()
    expect(lockoutReason("locked", true, true)).toBeDefined()
    expect(lockoutReason("superuser", false, true)).toBeDefined()
    // Giving a right, or changing another account, is not a lockout.
    expect(lockoutReason("login", true, true)).toBeUndefined()
    expect(lockoutReason("createDb", false, true)).toBeUndefined()
    expect(lockoutReason("login", false, false)).toBeUndefined()
  })
})

describe("what an account holds on one object", () => {
  const cell = { level: "table", schema: "public", table: "orders" }

  test("its privileges, in the level's own order", () => {
    const held = heldOn([grant("orders", ["UPDATE", "SELECT"])], cell, TABLE)
    expect(held.privileges).toEqual(["SELECT", "UPDATE"])
    expect(held.owned).toEqual([])
  })

  test("ALL is every privilege of the level", () => {
    expect(heldOn([grant("orders", ["ALL"])], cell, TABLE).privileges).toEqual([
      "SELECT",
      "INSERT",
      "UPDATE",
      "DELETE",
    ])
  })

  test("another table's grants, another level's and a default for later are not this object's", () => {
    const grants = [
      grant("customers", ["SELECT"]),
      { grantee: "app", level: "schema", schema: "public", privileges: ["USAGE"] },
      grant("", ["SELECT"], { future: true }),
    ]
    expect(heldOn(grants, cell, TABLE).privileges).toEqual([])
    expect(heldLater(grants, { level: "table", schema: "public" }, TABLE)).toEqual(["SELECT"])
  })

  test("what is held by owning is marked, unless it was granted as well", () => {
    const owned = heldOn([grant("orders", ["SELECT", "UPDATE"], { owner: true })], cell, TABLE)
    expect(owned.owned).toEqual(["SELECT", "UPDATE"])
    const both = heldOn(
      [grant("orders", ["SELECT", "UPDATE"], { owner: true }), grant("orders", ["SELECT"])],
      cell,
      TABLE,
    )
    expect(both.owned).toEqual(["UPDATE"])
  })

  test("what the chips cannot say is kept beside them", () => {
    const held = heldOn([grant("orders", ["SELECT (`a`, `b`)", "DENY UPDATE"])], cell, TABLE)
    expect(held.privileges).toEqual([])
    expect(held.other).toEqual(["SELECT (`a`, `b`)", "DENY UPDATE"])
  })

  test("a database-level cell matches by its database", () => {
    const grants = [{ grantee: "app", level: "database", database: "shop", privileges: ["ALL"] }]
    expect(heldOn(grants, { level: "database", database: "shop" }, DATABASE).privileges).toEqual([
      "CONNECT",
      "CREATE",
    ])
    expect(heldOn(grants, { level: "database", database: "blog" }, DATABASE).privileges).toEqual([])
  })
})

describe("pressing privileges", () => {
  const cell = { level: "table", schema: "public", table: "orders" }
  const held = { privileges: ["SELECT"], owned: [], grantable: false, other: [] }

  test("a press wants what was not held, and a second press takes the want back", () => {
    const once = toggled({}, cell, held, "UPDATE")
    expect(once[cellKey(cell)].privileges).toEqual(["SELECT", "UPDATE"])
    expect(toggled(once, cell, held, "UPDATE")).toEqual({})
  })

  test("a press on what is held wants it gone", () => {
    expect(toggled({}, cell, held, "SELECT")[cellKey(cell)].privileges).toEqual([])
  })

  test("what is held by owning cannot be pressed", () => {
    const owner = { privileges: ["SELECT"], owned: ["SELECT"], grantable: false, other: [] }
    expect(toggled({}, cell, owner, "SELECT")).toEqual({})
  })

  test("ALL wants every privilege, and pressed again leaves only what is owned", () => {
    const all = toggledAll({}, cell, held, TABLE)
    expect(all[cellKey(cell)].privileges).toEqual(["SELECT", "INSERT", "UPDATE", "DELETE"])
    expect(toggledAll(all, cell, held, TABLE)[cellKey(cell)].privileges).toEqual([])
  })

  test("the change bar counts objects and memberships", () => {
    expect(pendingWords({ a: { cell, privileges: [] } }, [])).toBe("1 change not applied")
    expect(pendingWords({ a: { cell, privileges: [] } }, [{ role: "r", member: true }])).toBe(
      "2 changes not applied",
    )
  })
})

describe("the requests a set of presses becomes", () => {
  const cell = (table) => ({ level: "table", schema: "public", table })
  const nothing = { privileges: [], owned: [], grantable: false, other: [] }
  const holds = (held) => (asked) => held[asked.table ?? asked.database] ?? nothing

  test("one object, one grant and one revoke, each naming exactly what was pressed", () => {
    const wanted = {
      [cellKey(cell("orders"))]: { cell: cell("orders"), privileges: ["SELECT", "UPDATE"] },
      [cellKey(cell("customers"))]: { cell: cell("customers"), privileges: [] },
    }
    const planned = plannedRequests(
      wanted,
      holds({
        orders: { ...nothing, privileges: ["SELECT"] },
        customers: { ...nothing, privileges: ["SELECT"] },
      }),
      LEVELS,
      {},
      [],
    )
    // Revokes first.
    expect(planned.map((one) => [one.action, one.body.table, one.body.privileges])).toEqual([
      ["revoke", "customers", ["SELECT"]],
      ["grant", "orders", ["UPDATE"]],
    ])
    expect(planned[1].body).toEqual({
      level: "table",
      schema: "public",
      table: "orders",
      privileges: ["UPDATE"],
    })
    expect(planned[0].about).toBe("Revoke on table public.customers")
    // The act and the name apart: the name is never set in the label's small caps.
    expect(planned[0].act).toBe("Revoke on table")
    expect(planned[0].on).toBe("public.customers")
  })

  test("every privilege of the level at once is asked for by its own word", () => {
    const wanted = {
      [cellKey(cell("orders"))]: {
        cell: cell("orders"),
        privileges: ["SELECT", "INSERT", "UPDATE", "DELETE"],
      },
    }
    expect(plannedRequests(wanted, holds({}), LEVELS, {}, [])[0].body.privileges).toEqual(["ALL"])
    // Three of four were missing: those three are named.
    const partly = plannedRequests(
      wanted,
      holds({ orders: { ...nothing, privileges: ["SELECT"] } }),
      LEVELS,
      {},
      [],
    )
    expect(partly[0].body.privileges).toEqual(["INSERT", "UPDATE", "DELETE"])
  })

  test("what is owned is never revoked", () => {
    const wanted = { [cellKey(cell("orders"))]: { cell: cell("orders"), privileges: [] } }
    const planned = plannedRequests(
      wanted,
      holds({ orders: { ...nothing, privileges: ["SELECT", "UPDATE"], owned: ["SELECT"] } }),
      LEVELS,
      {},
      [],
    )
    expect(planned).toHaveLength(1)
    expect(planned[0].body.privileges).toEqual(["UPDATE"])
  })

  test("a privilege pressed on every table of a schema is one request for the schema", () => {
    const tables = ["a", "b", "c"]
    const wanted = Object.fromEntries(
      tables.map((table) => [
        cellKey(cell(table)),
        { cell: cell(table), privileges: table === "a" ? ["SELECT", "UPDATE"] : ["SELECT"] },
      ]),
    )
    const planned = plannedRequests(
      wanted,
      holds({}),
      LEVELS,
      { [scopeKey({ level: "table", schema: "public" })]: tables },
      [],
      { future: true, grantOption: true },
    )
    expect(planned.map((one) => [one.body.table, one.body.privileges])).toEqual([
      ["", ["SELECT"]],
      ["a", ["UPDATE"]],
    ])
    // Only the whole-schema request can cover the tables created later.
    expect(planned[0].body.future).toBe(true)
    expect(planned[1].body.future).toBeUndefined()
    expect(planned[0].body.grantOption).toBe(true)
    expect(planned[0].about).toBe("Grant on every table in public")
  })

  test("a privilege pressed on some tables of a schema stays table by table", () => {
    const wanted = {
      [cellKey(cell("a"))]: { cell: cell("a"), privileges: ["SELECT"] },
      [cellKey(cell("b"))]: { cell: cell("b"), privileges: ["SELECT"] },
    }
    const planned = plannedRequests(
      wanted,
      holds({}),
      LEVELS,
      { [scopeKey({ level: "table", schema: "public" })]: ["a", "b", "c"] },
      [],
    )
    expect(planned.map((one) => one.body.table)).toEqual(["a", "b"])
  })

  test("a membership is a request at the role level, with the account's host carried", () => {
    const planned = plannedRequests(
      {},
      holds({}),
      LEVELS,
      {},
      [
        { role: "readers", member: true },
        { role: "writers", member: false },
      ],
      { host: "%" },
    )
    expect(planned.map((one) => [one.action, one.body])).toEqual([
      ["revoke", { host: "%", level: "role", memberOf: "writers" }],
      ["grant", { host: "%", level: "role", memberOf: "readers" }],
    ])
  })

  test("a want that changes nothing is no request", () => {
    const wanted = { [cellKey(cell("a"))]: { cell: cell("a"), privileges: ["SELECT"] } }
    expect(
      plannedRequests(wanted, holds({ a: { ...nothing, privileges: ["SELECT"] } }), LEVELS, {}, []),
    ).toEqual([])
  })
})

describe("functions and columns", () => {
  const FUNCTION = {
    level: "function",
    privileges: ["EXECUTE", "ALL"],
    needs: ["schema", "routine"],
    allObjects: true,
    future: true,
    grantOption: true,
  }
  const COLUMN = {
    level: "column",
    privileges: ["SELECT", "INSERT", "UPDATE", "REFERENCES", "ALL"],
    needs: ["schema", "table", "columns"],
    grantOption: true,
  }
  const OVERLOADS = [FUNCTION, COLUMN]
  const nothing = { privileges: [], owned: [], grantable: false, other: [] }
  const fn = (name, signature) => ({ level: "function", schema: "public", table: name, signature })
  const column = (name, table = "orders") => ({
    level: "column",
    schema: "public",
    table,
    column: name,
  })
  const holdsNothing = () => nothing

  test("what an account was given on functions and columns is counted in its summary", () => {
    const grants = [
      {
        grantee: "app",
        level: "function",
        schema: "public",
        routine: "total",
        arguments: "integer",
        privileges: ["EXECUTE"],
      },
      {
        grantee: "app",
        level: "function",
        schema: "public",
        routine: "total",
        arguments: "text",
        privileges: ["EXECUTE"],
      },
      {
        grantee: "app",
        level: "column",
        schema: "public",
        table: "orders",
        column: "id",
        privileges: ["SELECT"],
      },
      {
        grantee: "app",
        level: "column",
        schema: "public",
        table: "orders",
        column: "total",
        privileges: ["SELECT"],
      },
      {
        grantee: "app",
        level: "column",
        schema: "public",
        table: "orders",
        column: "total",
        privileges: ["UPDATE"],
        grantor: "other",
      },
    ]
    expect(grantSummary({ superuser: false }, grants)).toBe("Granted on 2 functions, 2 columns")
  })

  test("a function is told from its namesakes by the types it takes", () => {
    expect(cellKey(fn("total", "integer"))).not.toBe(cellKey(fn("total", "text")))
    const grants = [
      {
        grantee: "app",
        level: "function",
        schema: "public",
        routine: "total",
        arguments: "integer",
        privileges: ["EXECUTE"],
      },
    ]
    expect(heldOn(grants, fn("total", "integer"), FUNCTION).privileges).toEqual(["EXECUTE"])
    expect(heldOn(grants, fn("total", "text"), FUNCTION).privileges).toEqual([])
    expect(heldOn(grants, fn("other", "integer"), FUNCTION).privileges).toEqual([])
    // A function with no arguments is told from one that takes some.
    const none = [
      {
        grantee: "app",
        level: "function",
        schema: "public",
        routine: "now_utc",
        arguments: "",
        privileges: ["EXECUTE"],
      },
    ]
    expect(heldOn(none, fn("now_utc", ""), FUNCTION).privileges).toEqual(["EXECUTE"])
    expect(heldOn(none, fn("now_utc", "integer"), FUNCTION).privileges).toEqual([])
  })

  test("a function grant names the routine and its types, and says them in a review as written", () => {
    const wanted = {
      [cellKey(fn("total", "n integer"))]: {
        cell: fn("total", "n integer"),
        privileges: ["EXECUTE"],
      },
    }
    const [planned] = plannedRequests(wanted, holdsNothing, OVERLOADS, {}, [])
    expect(planned.body).toEqual({
      level: "function",
      schema: "public",
      routine: "total",
      arguments: "n integer",
      privileges: ["EXECUTE"],
    })
    expect(planned.act).toBe("Grant on function")
    expect(planned.on).toBe("public.total(n integer)")
  })

  test("EXECUTE on every function of a schema is one request for the schema, which can cover later ones", () => {
    const cells = [fn("a", ""), fn("b", "integer")]
    const wanted = Object.fromEntries(
      cells.map((cell) => [cellKey(cell), { cell, privileges: ["EXECUTE"] }]),
    )
    const planned = plannedRequests(
      wanted,
      holdsNothing,
      OVERLOADS,
      { [scopeKey({ level: "function", schema: "public" })]: ["a()", "b(integer)"] },
      [],
      { future: true },
    )
    expect(planned).toHaveLength(1)
    expect(planned[0].body).toEqual({
      level: "function",
      schema: "public",
      privileges: ["EXECUTE"],
      future: true,
    })
    expect(planned[0].about).toBe("Grant on every function in public")
  })

  test("a schema that holds functions the matrix does not list is never asked for as a whole", () => {
    // Pressed on every function that is listed; an extension installed more
    // in the schema, and ALL ROUTINES IN SCHEMA would grant those as well.
    const cells = [fn("a", ""), fn("b", "integer")]
    const key = scopeKey({ level: "function", schema: "public" })
    const grant = Object.fromEntries(
      cells.map((cell) => [cellKey(cell), { cell, privileges: ["EXECUTE"] }]),
    )
    const asked = (wanted, holds, options) =>
      plannedRequests(wanted, holds, OVERLOADS, { [key]: ["a()", "b(integer)"] }, [], options)

    const planned = asked(grant, holdsNothing, { future: true, unlisted: [key] })
    expect(planned.map((one) => [one.action, one.body])).toEqual([
      [
        "grant",
        {
          level: "function",
          schema: "public",
          routine: "a",
          arguments: "",
          privileges: ["EXECUTE"],
        },
      ],
      [
        "grant",
        {
          level: "function",
          schema: "public",
          routine: "b",
          arguments: "integer",
          privileges: ["EXECUTE"],
        },
      ],
    ])
    expect(planned.some((one) => one.body.future)).toBe(false)

    // The same for a revoke of what is held on every listed function.
    const held = () => ({ ...nothing, privileges: ["EXECUTE"] })
    const revoke = Object.fromEntries(
      cells.map((cell) => [cellKey(cell), { cell, privileges: [] }]),
    )
    const revoked = asked(revoke, held, { unlisted: [key] })
    expect(revoked.map((one) => [one.action, one.body.routine])).toEqual([
      ["revoke", "a"],
      ["revoke", "b"],
    ])

    // And without the extension's functions in the schema, the whole is still one request.
    expect(asked(grant, holdsNothing, {})).toHaveLength(1)
    expect(
      asked(grant, holdsNothing, { unlisted: [scopeKey({ level: "function", schema: "other" })] }),
    ).toHaveLength(1)
  })

  test("a column is held on its table, by its name", () => {
    const grants = [
      {
        grantee: "app",
        level: "column",
        schema: "public",
        table: "orders",
        column: "total",
        privileges: ["SELECT", "UPDATE"],
      },
      { grantee: "app", level: "table", schema: "public", table: "orders", privileges: ["DELETE"] },
    ]
    expect(heldOn(grants, column("total"), COLUMN).privileges).toEqual(["SELECT", "UPDATE"])
    expect(heldOn(grants, column("status"), COLUMN).privileges).toEqual([])
    expect(heldOn(grants, column("total", "customers"), COLUMN).privileges).toEqual([])
    expect(cellKey(column("total"))).not.toBe(cellKey(column("status")))
  })

  test("columns pressed the same way are one request, and different ones are two", () => {
    const presses = {
      id: ["SELECT"],
      total: ["SELECT"],
      status: ["SELECT", "UPDATE"],
    }
    const wanted = Object.fromEntries(
      Object.entries(presses).map(([name, privileges]) => [
        cellKey(column(name)),
        { cell: column(name), privileges },
      ]),
    )
    const planned = plannedRequests(wanted, holdsNothing, OVERLOADS, {}, [])
    expect(planned.map((one) => one.body)).toEqual([
      {
        level: "column",
        schema: "public",
        table: "orders",
        columns: ["id", "total"],
        privileges: ["SELECT"],
      },
      {
        level: "column",
        schema: "public",
        table: "orders",
        columns: ["status"],
        privileges: ["SELECT", "UPDATE"],
      },
    ])
    expect(planned[0].act).toBe("Grant on columns of")
    expect(planned[0].on).toBe("public.orders (id, total)")
    expect(planned[1].act).toBe("Grant on column")
  })

  test("a column revoke takes only what was held there, and a column of another table stays apart", () => {
    const held = (cell) =>
      cell.column === "total" ? { ...nothing, privileges: ["SELECT", "UPDATE"] } : nothing
    const wanted = {
      [cellKey(column("total"))]: { cell: column("total"), privileges: ["SELECT"] },
      [cellKey(column("ref", "customers"))]: {
        cell: column("ref", "customers"),
        privileges: ["SELECT"],
      },
    }
    const planned = plannedRequests(wanted, held, OVERLOADS, {}, [])
    expect(
      planned.map((one) => [one.action, one.body.table, one.body.columns, one.body.privileges]),
    ).toEqual([
      ["revoke", "orders", ["total"], ["UPDATE"]],
      ["grant", "customers", ["ref"], ["SELECT"]],
    ])
  })

  test("every privilege of the column level at once is its own word, and the grant option is carried", () => {
    const every = ["SELECT", "INSERT", "UPDATE", "REFERENCES"]
    const wanted = { [cellKey(column("id"))]: { cell: column("id"), privileges: every } }
    const [planned] = plannedRequests(wanted, holdsNothing, OVERLOADS, {}, [], {
      grantOption: true,
    })
    expect(planned.body.privileges).toEqual(["ALL"])
    expect(planned.body.grantOption).toBe(true)
  })
})

describe("the string a new account connects with", () => {
  const conn = { driver: "postgres", host: "127.0.0.1", port: "5432", database: "shop" }

  test("the connection's address with the account's own name and password", () => {
    expect(accountDsn(conn, { user: "reporting", password: "p@ss word" })).toBe(
      "postgres://reporting:p%40ss%20word@127.0.0.1:5432/shop",
    )
  })

  test("it states no transport option of its own", () => {
    expect(accountDsn(conn, { user: "a", password: "b" })).not.toContain("sslmode")
  })

  test("the database it was given, and where a document account lives", () => {
    expect(
      accountDsn(
        { ...conn, driver: "mysql", port: "3306" },
        { user: "a", password: "b", database: "blog" },
      ),
    ).toBe("a:b@tcp(127.0.0.1:3306)/blog")
    expect(
      accountDsn(
        { driver: "mongodb", host: "127.0.0.1", port: "27017", database: "app" },
        { user: "a", password: "b", authSource: "admin" },
      ),
    ).toBe("mongodb://a:b@127.0.0.1:27017/app?authSource=admin")
    expect(
      accountDsn(
        { driver: "redis", host: "127.0.0.1", port: "6379", database: "9" },
        { user: "worker", password: "pw" },
      ),
    ).toBe("redis://worker:pw@127.0.0.1:6379/9")
  })
})

describe("a key–value server's ACL users", () => {
  const user = {
    name: "worker",
    enabled: true,
    noPassword: false,
    passwords: 1,
    keys: ["~app:*"],
    channels: ["resetchannels"],
    commands: ["-@all", "+@read"],
    unrestricted: false,
    system: false,
    self: false,
    rule: "on ~app:* resetchannels -@all +@read",
  }

  test("a rule is said in a line", () => {
    expect(aclSummary(user)).toBe("-@all +@read on ~app:*")
    expect(aclSummary({ ...user, unrestricted: true })).toBe("Every command on every key")
    expect(aclSummary({ ...user, keys: null, commands: null })).toBe("no command on no key")
  })

  test("a list is typed one to a line or with spaces", () => {
    expect(ruleList(" +@read\n+@write   -@dangerous\n")).toEqual([
      "+@read",
      "+@write",
      "-@dangerous",
    ])
    expect(ruleList("")).toEqual([])
  })

  test("the draft leaves out the rule every list starts from", () => {
    expect(aclDraftOf(user).commands).toBe("+@read")
    expect(aclDraftOf(undefined)).toMatchObject({ enabled: true, noPassword: false, keys: "" })
  })

  test("only what was edited is sent for a user that exists", () => {
    const saved = aclDraftOf(user)
    expect(aclRequest(user, saved)).toEqual({})
    expect(aclChanged(aclRequest(user, saved))).toBe(false)
    expect(aclRequest(user, { ...saved, keys: "~app:*\n%R~shared:*" })).toEqual({
      keys: ["~app:*", "%R~shared:*"],
    })
    expect(aclRequest(user, { ...saved, enabled: false })).toEqual({ enabled: false })
    expect(aclRequest(user, { ...saved, password: "new" })).toEqual({ password: "new" })
    expect(aclRequest(user, { ...saved, noPassword: true, password: "ignored" })).toEqual({
      noPassword: true,
    })
  })

  test("a new user states everything", () => {
    const request = aclRequest(undefined, {
      enabled: true,
      password: "pw",
      noPassword: false,
      keys: "a9:*",
      channels: "",
      commands: "+@read\n+@write",
    })
    expect(request).toEqual({
      create: true,
      enabled: true,
      password: "pw",
      keys: ["a9:*"],
      channels: [],
      commands: ["+@read", "+@write"],
    })
    expect(aclChanged(request)).toBe(true)
  })
})
