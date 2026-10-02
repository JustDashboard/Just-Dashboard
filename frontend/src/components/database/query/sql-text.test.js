import { describe, expect, test } from "bun:test"
import { dialectOf } from "./dialect"
import { firstLine, runTarget, splitStatements, statementAt, tokenize } from "./sql-text"

const pg = dialectOf("postgres")
const mysql = dialectOf("mysql")
const mssql = dialectOf("sqlserver")
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
