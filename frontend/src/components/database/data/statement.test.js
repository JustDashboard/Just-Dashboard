import { describe, expect, test } from "bun:test"
import { inlineStatement } from "./statement"

const FILTERS = [
  { column: "id", op: "gt", value: "5" },
  { column: "email", op: "contains", value: "a_b" },
  { column: "id", op: "in", values: ["1", "2"] },
]
const KINDS = { id: "number", email: "text" }
const PAGE = { limit: 100, offset: 200 }

describe("the statement of a view, with its values written in", () => {
  test("PostgreSQL's numbered markers", () => {
    const sql =
      'SELECT * FROM "public"."customers" WHERE "id" > $1 AND CAST("email" AS TEXT) LIKE $2 ESCAPE \'!\' AND "id" IN ($3, $4) ORDER BY "id" ASC LIMIT $5 OFFSET $6'
    expect(inlineStatement(sql, FILTERS, PAGE, KINDS)).toBe(
      'SELECT * FROM "public"."customers" WHERE "id" > 5 AND CAST("email" AS TEXT) LIKE \'%a!_b%\' ESCAPE \'!\' AND "id" IN (1, 2) ORDER BY "id" ASC LIMIT 100 OFFSET 200',
    )
  })

  test("MySQL's question marks, with a backslash doubled", () => {
    const sql = "SELECT * FROM `blog`.`posts` WHERE `title` = ? ORDER BY `id` ASC LIMIT ? OFFSET ?"
    expect(
      inlineStatement(sql, [{ column: "title", op: "eq", value: "it's a\\b" }], PAGE, {}),
    ).toBe(
      "SELECT * FROM `blog`.`posts` WHERE `title` = 'it''s a\\\\b' ORDER BY `id` ASC LIMIT 100 OFFSET 200",
    )
  })

  test("SQL Server pages with OFFSET first, and escapes a bracket in a pattern", () => {
    const sql =
      "SELECT * FROM [dbo].[customers] WHERE CAST([email] AS NVARCHAR(MAX)) LIKE @p1 ESCAPE '!' ORDER BY [id] ASC OFFSET @p2 ROWS FETCH NEXT @p3 ROWS ONLY"
    expect(inlineStatement(sql, [{ column: "email", op: "prefix", value: "[é" }], PAGE, {})).toBe(
      "SELECT * FROM [dbo].[customers] WHERE CAST([email] AS NVARCHAR(MAX)) LIKE N'![é%' ESCAPE '!' ORDER BY [id] ASC OFFSET 200 ROWS FETCH NEXT 100 ROWS ONLY",
    )
  })

  test("ClickHouse searches without a pattern, so the value goes as it is", () => {
    const sql =
      "SELECT * FROM `a`.`page_views` WHERE position(toString(`path`), ?) > 0 ORDER BY `ts` ASC LIMIT ? OFFSET ?"
    expect(inlineStatement(sql, [{ column: "path", op: "contains", value: "50%" }], PAGE, {})).toBe(
      "SELECT * FROM `a`.`page_views` WHERE position(toString(`path`), '50%') > 0 ORDER BY `ts` ASC LIMIT 100 OFFSET 200",
    )
  })

  test("a case-insensitive search binds its pattern inside LOWER()", () => {
    const sql =
      'SELECT * FROM "t" WHERE LOWER(CAST("a" AS TEXT)) LIKE LOWER(?) ESCAPE \'!\' LIMIT ? OFFSET ?'
    expect(inlineStatement(sql, [{ column: "a", op: "icontains", value: "X" }], PAGE, {})).toBe(
      "SELECT * FROM \"t\" WHERE LOWER(CAST(\"a\" AS TEXT)) LIKE LOWER('%X%') ESCAPE '!' LIMIT 100 OFFSET 200",
    )
  })

  test("a NULL test binds nothing, and a range binds two", () => {
    const sql = 'SELECT * FROM "t" WHERE "a" IS NULL AND "n" BETWEEN $1 AND $2 LIMIT $3 OFFSET $4'
    expect(
      inlineStatement(
        sql,
        [
          { column: "a", op: "is_null" },
          { column: "n", op: "between", values: ["1", "9007199254740993"] },
        ],
        PAGE,
        { n: "number" },
      ),
    ).toBe(
      'SELECT * FROM "t" WHERE "a" IS NULL AND "n" BETWEEN 1 AND 9007199254740993 LIMIT 100 OFFSET 200',
    )
  })

  test("a marker-like run inside a quoted name or a literal is left alone", () => {
    const sql = 'SELECT * FROM "why?" WHERE "a$1" = $1 AND "b" <> \'?\' LIMIT $2 OFFSET $3'
    expect(inlineStatement(sql, [{ column: "a$1", op: "eq", value: "x" }], PAGE, {})).toBe(
      'SELECT * FROM "why?" WHERE "a$1" = \'x\' AND "b" <> \'?\' LIMIT 100 OFFSET 200',
    )
  })

  test("a number column takes text that is not a number as a literal", () => {
    const sql = 'SELECT * FROM "t" WHERE "n" = $1 LIMIT $2 OFFSET $3'
    expect(
      inlineStatement(sql, [{ column: "n", op: "eq", value: "1; DROP" }], PAGE, { n: "number" }),
    ).toBe('SELECT * FROM "t" WHERE "n" = \'1; DROP\' LIMIT 100 OFFSET 200')
  })

  test("markers that do not add up are not guessed at", () => {
    expect(
      inlineStatement('SELECT * FROM "t" WHERE "a" = $1 LIMIT $2', FILTERS, PAGE, {}),
    ).toBeNull()
    expect(inlineStatement('SELECT * FROM "t" WHERE x = $1 AND y = $2', [], PAGE, {})).toBeNull()
  })
})
