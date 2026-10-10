import { describe, expect, test } from "bun:test"
import { dialectOf } from "./dialect"
import { errorPlace } from "./error-place"

const pg = dialectOf("postgres")
const mysql = dialectOf("mysql")
const sqlite = dialectOf("sqlite")
const mssql = dialectOf("sqlserver")
const clickhouse = dialectOf("clickhouse")

/** The stretch of the statement a message places, as text; null where it places none. */
const placed = (message, sql, dialect) => {
  const place = errorPlace(message, sql, dialect)
  return place ? sql.slice(place.start, place.start + place.length) : null
}

// Every message below is one a fixture engine answered with.
describe("where a statement failed, from the engine's own words", () => {
  test("PostgreSQL names the word it could not read, or the name it could not find", () => {
    expect(
      placed('ERROR: syntax error at or near "fro" (SQLSTATE 42601)', "select * fro t", pg),
    ).toBe("fro")
    expect(
      placed(
        'ERROR: column "nope" does not exist (SQLSTATE 42703)',
        "select id, nope from orders",
        pg,
      ),
    ).toBe("nope")
    expect(
      placed(
        'ERROR: relation "nope_table" does not exist (SQLSTATE 42P01)',
        "select * from nope_table",
        pg,
      ),
    ).toBe("nope_table")
    expect(
      placed(
        "ERROR: function nofunc(integer) does not exist (SQLSTATE 42883)",
        "select nofunc(1)",
        pg,
      ),
    ).toBe("nofunc")
  })
  test("a statement cut short is marked at its end", () => {
    expect(placed("ERROR: syntax error at end of input (SQLSTATE 42601)", "select 1 +", pg)).toBe(
      "+",
    )
    expect(placed("SQL logic error: incomplete input (1)", "select 1 +", sqlite)).toBe("+")
    expect(
      placed(
        "Error 1064 (42000): You have an error in your SQL syntax; check the manual that corresponds to your MySQL server version for the right syntax to use near '' at line 1",
        "select 1 +",
        mysql,
      ),
    ).toBe("+")
  })
  test("MySQL quotes the statement from where it stopped reading, with the line", () => {
    const near = (rest, line) =>
      `Error 1064 (42000): You have an error in your SQL syntax; check the manual that corresponds to your MariaDB server version for the right syntax to use near '${rest}' at line ${line}`
    expect(placed(near("fro t", 1), "select * fro t", mysql)).toBe("fro")
    expect(placed(near("fro posts", 2), "select fro\n  , id fro posts", mysql)).toBe("fro")
    expect(errorPlace(near("fro posts", 2), "select fro\n  , id fro posts", mysql).start).toBe(18)
    expect(
      placed("Error 1054 (42S22): Unknown column 'nope' in 'field list'", "select nope", mysql),
    ).toBe("nope")
    expect(
      placed(
        "Error 1146 (42S02): Table 'blog_a4.nope_table' doesn't exist",
        "select * from nope_table",
        mysql,
      ),
    ).toBe("nope_table")
    expect(
      placed(
        "Error 1305 (42000): FUNCTION blog_a4.nofunc does not exist",
        "select nofunc(1)",
        mysql,
      ),
    ).toBe("nofunc")
  })
  test("SQLite and SQL Server name it too", () => {
    expect(placed('SQL logic error: near "fro": syntax error (1)', "select * fro t", sqlite)).toBe(
      "fro",
    )
    expect(
      placed("SQL logic error: no such table: nope_table (1)", "select * from nope_table", sqlite),
    ).toBe("nope_table")
    expect(placed("mssql: Incorrect syntax near 'fro'.", "select * fro t", mssql)).toBe("fro")
    expect(placed("mssql: Invalid column name 'nope'.", "select [nope] from t", mssql)).toBe(
      "[nope]",
    )
    expect(
      placed("mssql: Invalid object name 'nope_table'.", "select * from nope_table", mssql),
    ).toBe("nope_table")
  })
  test("ClickHouse counts characters", () => {
    expect(
      placed(
        "code: 62, message: Syntax error: failed at position 14 ('t'): t. Expected alias cannot be here",
        "select * fro t",
        clickhouse,
      ),
    ).toBe("t")
    expect(
      placed(
        "code: 62, message: Syntax error: failed at position 11 (end of query): . Expected one of: token",
        "select 1 +",
        clickhouse,
      ),
    ).toBe("+")
    expect(
      placed(
        "code: 47, message: Unknown expression identifier 'nope' in scope SELECT nope FROM t",
        "select nope from t",
        clickhouse,
      ),
    ).toBe("nope")
  })
  test("a word that stands in several places is not guessed at: the whole statement is marked instead", () => {
    expect(
      errorPlace(
        'ERROR: syntax error at or near "," (SQLSTATE 42601)',
        "select a, b,, c from t",
        pg,
      ),
    ).toBeNull()
    expect(
      errorPlace(
        'ERROR: column "id" does not exist (SQLSTATE 42703)',
        "select id, o.id from o",
        pg,
      ),
    ).toBeNull()
  })
  test("words that place nothing place nothing", () => {
    expect(errorPlace("ERROR: canceling statement due to user request", "select 1", pg)).toBeNull()
    expect(errorPlace("ORA-00942: table or view does not exist", "select * from t", pg)).toBeNull()
    expect(errorPlace('ERROR: relation "gone" does not exist', "", pg)).toBeNull()
    // A name inside a text is not the name the engine meant.
    expect(errorPlace('ERROR: column "nope" does not exist', "select 'nope' from t", pg)).toBeNull()
  })
})
