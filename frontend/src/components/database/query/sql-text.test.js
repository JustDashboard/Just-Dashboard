import { describe, expect, test } from "bun:test"
import scripts from "../../../../../backend/internal/dbx/testdata/statement-splits.json"
import { dialectOf } from "./dialect"
import {
  firstLine,
  readScript,
  runTarget,
  splitStatements,
  statementAt,
  tokenize,
} from "./sql-text"

const pg = dialectOf("postgres")
const mysql = dialectOf("mysql")
const mssql = dialectOf("sqlserver")
const oracle = dialectOf("oracle")
const texts = (sql, dialect = pg) => splitStatements(sql, dialect).map((span) => span.text)

describe("splitting a text into statements", () => {
  test("a semicolon ends a statement, and the last needs none", () => {
    expect(texts("select 1; select 2;\nselect 3")).toEqual(["select 1", "select 2", "select 3"])
  })
  test("a semicolon inside a text, a quoted name or a comment ends nothing", () => {
    expect(texts(`select ';' as "a;b" -- one; two\n, 2 /* three; four */; select 2`)).toEqual([
      `select ';' as "a;b" -- one; two\n, 2`,
      "select 2",
    ])
  })
  test("a doubled quote is the quote itself", () => {
    expect(texts("select 'it''s; fine'; select 2")).toEqual(["select 'it''s; fine'", "select 2"])
  })
  test("a dollar-quoted body is one piece on PostgreSQL, tagged or not", () => {
    const body =
      "create function f() returns int as $fn$ begin return 1; end; $fn$ language plpgsql"
    expect(texts(`${body}; select $$a;b$$`)).toEqual([body, "select $$a;b$$"])
  })
  test("a dollar is not a quote on an engine without them", () => {
    expect(texts("select $$a; select 2", mysql)).toEqual(["select $$a", "select 2"])
  })
  test("MySQL reads a backslash escape, a # comment and a double-quoted text", () => {
    expect(texts(`select 'a\\'; b' # c; d\n; select "x;y"`, mysql)).toEqual([
      `select 'a\\'; b'`,
      `select "x;y"`,
    ])
  })
  test("PostgreSQL reads a backslash as itself, except in an E text", () => {
    expect(texts("select 'a\\'; select 2")).toEqual(["select 'a\\'", "select 2"])
    expect(texts("select E'a\\'; b'; select 2")).toEqual(["select E'a\\'; b'", "select 2"])
  })
  test("SQL Server reads a bracketed name", () => {
    expect(texts("select [a;b] from [t]; select 2", mssql)).toEqual([
      "select [a;b] from [t]",
      "select 2",
    ])
  })
  test("a stretch of nothing but space and comments is not a statement", () => {
    expect(texts("-- only a note\n;  ;\n/* and this */")).toEqual([])
  })
  test("a comment before or after a statement is left out of it, one inside is kept", () => {
    const [span] = splitStatements("-- what this is\nselect 1 -- why\nfrom t -- done\n;", pg)
    expect(span.text).toBe("select 1 -- why\nfrom t")
    expect(span.line).toBe(2)
  })
  test("an unterminated text runs to the end instead of throwing", () => {
    expect(texts("select 'never closed; select 2")).toEqual(["select 'never closed; select 2"])
  })
  test("every character belongs to exactly one token", () => {
    const sql = "select a.b::int, 'x' -- c\nfrom \"T\" where n >= -1.5e3 and j->>'k' = $$z$$;"
    const tokens = tokenize(sql, pg)
    expect(tokens.map((token) => sql.slice(token.start, token.end)).join("")).toBe(sql)
    expect(tokens.some((token) => token.kind === "body")).toBe(true)
  })
  test("an operator stops where a comment opens", () => {
    const sql = "select 1+--two\n2"
    const kinds = tokenize(sql, pg).map((token) => token.kind)
    expect(kinds).toContain("comment")
  })
})

describe("a script read as its engine reads it", () => {
  // The scripts the server's own tests read (`dbx/statement-splits.json`):
  // the editor must find the same statements in them, where the server does,
  // or "run the statement at the cursor" sends a piece of one.
  for (const script of scripts) {
    test(`${script.driver}: ${script.name}`, () => {
      const spans = splitStatements(script.sql, dialectOf(script.driver))
      expect(spans.map((span) => ({ sql: span.text, line: span.line }))).toEqual(script.statements)
    })
  }

  test("every engine with bodies of its own has a script there", () => {
    const drivers = new Set(scripts.map((script) => script.driver))
    for (const driver of ["postgres", "mysql", "sqlite", "sqlserver", "oracle"]) {
      expect(drivers.has(driver)).toBe(true)
    }
  })

  test("a body is told from a statement, and a PL/SQL unit from a SQL Server routine", () => {
    const kinds = (sql, dialect) => splitStatements(sql, dialect).map((span) => span.body ?? "")
    expect(kinds("create procedure p() begin select 1; end; select 2", mysql)).toEqual([
      "block",
      "",
    ])
    expect(kinds("begin null; end;\n/\nselect 1 from dual", oracle)).toEqual(["plsql", ""])
    expect(kinds("create procedure p as select 1\ngo\nexec p", mssql)).toEqual(["routine", ""])
  })

  test("a PL/SQL unit with no / after it runs to the end, and says so", () => {
    const [unit] = splitStatements("begin\n  null;\nend;\nselect 1 from dual;", oracle)
    expect(unit.runsOn).toBe(true)
    const [ended] = splitStatements("begin\n  null;\nend;\n/\nselect 1 from dual;", oracle)
    expect(ended.runsOn).toBeUndefined()
  })

  test("a SQL Server routine that takes in what follows its END says so", () => {
    const [took] = splitStatements("create procedure p as begin select 1 end\nexec p\ngo", mssql)
    expect(took.text).toBe("create procedure p as begin select 1 end\nexec p")
    expect(took.runsOn).toBe(true)
    const [stopped] = splitStatements("create procedure p as begin select 1 end;\nexec p", mssql)
    expect(stopped.runsOn).toBeUndefined()
    // A body with no BEGIN is the batch's to the end, which is how it is meant.
    expect(splitStatements("create procedure p as select 1\ngo", mssql)[0].runsOn).toBeUndefined()
  })

  test("a GO line belongs to the batch it ends, for the cursor", () => {
    const sql = "select 1\ngo\nselect 2"
    const spans = splitStatements(sql, mssql)
    expect(statementAt(spans, sql.indexOf("go") + 2)?.text).toBe("select 1")
    expect(statementAt(spans, sql.indexOf("select 2"))?.text).toBe("select 2")
  })

  test("the separators of a script are part of what it is, and nothing else", () => {
    const sql = "-- two procedures\nDELIMITER $$\nCREATE PROCEDURE p() BEGIN SELECT 1; END$$\n"
    expect(readScript(sql, mysql).reach).toEqual({
      start: sql.indexOf("DELIMITER"),
      end: sql.indexOf("END$$") + 5,
    })
    expect(readScript("select 1;\nselect 2;", pg).reach).toEqual({ start: 0, end: 18 })
    expect(readScript("-- nothing", pg).reach).toBeNull()
  })
})

describe("the statement under the cursor", () => {
  const sql = "select 1;\n\nselect 2\nfrom t;\n\n"
  const spans = splitStatements(sql, pg)
  const at = (offset) => statementAt(spans, offset)?.text

  test("a cursor inside a statement is on it", () => {
    expect(at(3)).toBe("select 1")
    expect(at(sql.indexOf("from"))).toBe("select 2\nfrom t")
  })
  test("a cursor just after a semicolon is still on the statement it closed", () => {
    expect(at(sql.indexOf(";") + 1)).toBe("select 1")
  })
  test("a cursor in the blank lines before a statement is on that statement", () => {
    expect(at(sql.indexOf(";") + 2)).toBe("select 2\nfrom t")
  })
  test("a cursor in the blank lines after the last statement is on the last", () => {
    expect(at(sql.length)).toBe("select 2\nfrom t")
  })
  test("no statement, no answer", () => {
    expect(statementAt([], 0)).toBeUndefined()
  })
})

describe("what Run sends", () => {
  const sql = "select 1;\nselect 2;\nselect 3;"
  test("the selection, when there is one", () => {
    const target = runTarget(sql, { start: 10, end: 28 }, 28, pg)
    expect(target).toEqual({ scope: "selection", sql: "select 2;\nselect 3", offset: 10, line: 2 })
  })
  test("a selection of only space is no selection", () => {
    expect(runTarget(sql, { start: 9, end: 10 }, 10, pg)?.sql).toBe("select 2")
  })
  test("else the statement the cursor is on, without its semicolon", () => {
    expect(runTarget(sql, null, 12, pg)).toEqual({
      scope: "statement",
      sql: "select 2",
      offset: 10,
      line: 2,
    })
  })
  test("everything, when asked for all of it", () => {
    const target = runTarget(sql, { start: 10, end: 18 }, 12, pg, true)
    expect(target?.scope).toBe("all")
    expect(target?.sql).toBe("select 1;\nselect 2;\nselect 3")
  })
  test("a text of one statement is that statement, comments before it left out", () => {
    expect(runTarget("-- note\nselect 1", null, 0, pg)).toEqual({
      scope: "statement",
      sql: "select 1",
      offset: 8,
      line: 2,
    })
  })
  test("nothing to run in an empty text", () => {
    expect(runTarget("  \n-- nothing\n", null, 0, pg)).toBeNull()
  })
  test("the cursor in a procedure's body runs the whole procedure", () => {
    const sql =
      "DELIMITER $$\nCREATE PROCEDURE p()\nBEGIN\n  SELECT 1;\n  SELECT 2;\nEND$$\nDELIMITER ;\nCALL p();"
    expect(runTarget(sql, null, sql.indexOf("SELECT 2"), mysql)).toEqual({
      scope: "statement",
      sql: "CREATE PROCEDURE p()\nBEGIN\n  SELECT 1;\n  SELECT 2;\nEND",
      offset: sql.indexOf("CREATE"),
      line: 2,
    })
  })
  test("everything goes with the DELIMITER it needs, and the count of its last GO", () => {
    const delimited =
      "DELIMITER $$\nCREATE PROCEDURE p() BEGIN SELECT 1; END$$\nDELIMITER ;\nCALL p()"
    expect(runTarget(delimited, null, 0, mysql, true)?.sql).toBe(delimited)
    const repeated = "INSERT INTO t DEFAULT VALUES\nGO 3"
    expect(runTarget(repeated, null, 0, mssql)).toEqual({
      scope: "statement",
      sql: repeated,
      offset: 0,
      line: 1,
    })
  })
  test("a PL/SQL block is sent with the semicolon after its END and without its /", () => {
    const sql = "BEGIN\n  NULL;\nEND;\n/\nSELECT 1 FROM dual"
    expect(runTarget(sql, null, 3, oracle)?.sql).toBe("BEGIN\n  NULL;\nEND;")
  })
})

describe("a statement's first line", () => {
  test("leading comments and line breaks are left out", () => {
    expect(firstLine("-- sizes\n/* of tables */\nselect\n  a,\n  b\nfrom t")).toBe(
      "select a, b from t",
    )
  })
  test("a long one is cut", () => {
    expect(firstLine("select " + "x".repeat(200), 20)).toHaveLength(20)
  })
})
