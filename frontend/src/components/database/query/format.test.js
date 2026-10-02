import { describe, expect, test } from "bun:test"
import { dialectOf } from "./dialect"
import { formatSql } from "./format"
import { tokenize } from "./sql-text"

const pg = dialectOf("postgres")
const mysql = dialectOf("mysql")

/** The tokens that carry meaning, keywords compared without their case. */
const meaning = (sql, dialect = pg) =>
  tokenize(sql, dialect)
    .filter((token) => token.kind !== "space")
    .map((token) => {
      const text = sql.slice(token.start, token.end)
      return token.kind === "word" ? text.toUpperCase() : text
    })

const CASES = [
  "select 1",
  "select a,b , c from t where a=1 and b between 2 and 3 or c is null order by a desc limit 10",
  "SELECT o.id, c.email, sum(i.quantity * i.unit_price_cents) AS total FROM orders o JOIN customers c ON c.id = o.customer_id LEFT OUTER JOIN order_items i ON i.order_id = o.id AND i.quantity > 0 WHERE o.status IN ('paid', 'shipped') GROUP BY o.id, c.email HAVING count(*) > 1 ORDER BY total DESC LIMIT 50 OFFSET 100;",
  "with recent as (select * from orders where placed_at > now() - interval '7 days'), top as (select customer_id, count(*) n from recent group by 1) select * from top where n > (select avg(n) from top)",
  "insert into t (a, b) values (1, 'x;y'), (2, $$z$$) on conflict (a) do update set b = excluded.b returning *",
  "update t set a = a + 1, b = -1 where id = 7; delete from t where id in (select id from gone);",
  "-- sizes\nselect relname, pg_total_relation_size(oid) -- with indexes\nfrom pg_class /* all of them */ where relkind = 'r'",
  "select \"Mixed Case\".\"select\", t.* from \"Mixed Case\" t where x::text ~* '^a' and j->>'k' = 'v'",
  "create table if not exists public.things (id bigserial primary key, name text not null default 'x', price numeric(10,2) check (price >= 0), owner_id bigint references owners(id) on delete cascade, created_at timestamptz not null default now())",
  "select case when a > 1 then 'big' else 'small' end as size, extract(year from d), substring(s from 1 for 3) from t where a is distinct from b",
  "select count(*) filter (where ok) over (partition by k order by ts rows between unbounded preceding and current row) from t",
]

describe("laying a statement out", () => {
  test.each(CASES)("moves space and capitals and nothing else: %s", (sql) => {
    const out = formatSql(sql, pg)
    expect(meaning(out)).toEqual(
      meaning(sql.replace(/;\s*$/, "")).concat(sql.trim().endsWith(";") ? [";"] : []),
    )
  })
  test.each(CASES)("is settled: a second pass changes nothing: %s", (sql) => {
    const once = formatSql(sql, pg)
    expect(formatSql(once, pg)).toBe(once)
  })
  test("a clause to a line, the words of the language in capitals", () => {
    expect(formatSql("select a, b from t where a = 1 order by b limit 5", pg)).toBe(
      "SELECT a, b\nFROM t\nWHERE a = 1\nORDER BY b\nLIMIT 5\n",
    )
  })
  test("a list too long for the line goes one item to a line", () => {
    const out = formatSql(
      "select o.id, c.email, c.full_name, sum(i.quantity * i.unit_price_cents) as total_cents from orders o",
      pg,
    )
    expect(out).toBe(
      "SELECT\n  o.id,\n  c.email,\n  c.full_name,\n  sum(i.quantity * i.unit_price_cents) AS total_cents\nFROM orders o\n",
    )
  })
  test("a join is a line, with its words together", () => {
    const out = formatSql(CASES[2], pg)
    expect(out).toContain("\nJOIN customers c ON c.id = o.customer_id\n")
    expect(out).toContain(
      "\nLEFT OUTER JOIN order_items i ON i.order_id = o.id AND i.quantity > 0\n",
    )
    expect(out).toContain("\nGROUP BY o.id, c.email\n")
    expect(out.endsWith("OFFSET 100;\n")).toBe(true)
  })
  test("a subquery that fits stays on its line; one that does not is indented inside its parentheses", () => {
    expect(formatSql("select * from t where id in (select id from u where ok)", pg)).toBe(
      "SELECT *\nFROM t\nWHERE id IN (SELECT id FROM u WHERE ok)\n",
    )
    expect(
      formatSql(
        "select * from t where id in (select customer_id from orders where status = 'paid' and total_cents > 10000)",
        pg,
      ),
    ).toBe(
      "SELECT *\nFROM t\nWHERE id IN (\n  SELECT customer_id\n  FROM orders\n  WHERE status = 'paid' AND total_cents > 10000\n)\n",
    )
  })
  test("a word beside a dot is a name and keeps its case", () => {
    expect(formatSql('select t.select, "from".order from t', pg)).toBe(
      'SELECT t.select, "from".order\nFROM t\n',
    )
  })
  test("a column that spells a soft keyword is left as written", () => {
    expect(formatSql("select name, type, status, key, value, date, count from t", pg)).toBe(
      "SELECT name, type, status, key, value, date, count\nFROM t\n",
    )
  })
  test("the AND of a BETWEEN stays with it", () => {
    const sql =
      "select * from measurements where taken_at between '2026-01-01' and '2026-02-01' and sensor_id = 12 and value > 100.5"
    expect(formatSql(sql, pg)).toContain(
      "WHERE taken_at BETWEEN '2026-01-01' AND '2026-02-01'\n  AND sensor_id = 12\n  AND value > 100.5",
    )
  })
  test("what follows a line comment starts a new line", () => {
    const out = formatSql("select 1 -- one\n, 2", pg)
    expect(out).toContain("-- one\n")
    expect(meaning(out)).toEqual(meaning("select 1 -- one\n, 2"))
  })
  test("statements are set apart and each keeps its semicolon", () => {
    expect(formatSql("select 1;select 2", pg)).toBe("SELECT 1;\n\nSELECT 2;\n")
  })
  test("MySQL's backticks and # comments are read as MySQL reads them", () => {
    const sql = 'select `from`, `a b` from `t` # note; here\nwhere x = "a;b"'
    const out = formatSql(sql, mysql)
    expect(meaning(out, mysql)).toEqual(meaning(sql, mysql))
    expect(out).toContain("SELECT `from`, `a b`\nFROM `t` # note; here\n")
  })
  test("an empty text stays empty, an unbalanced one is not lost", () => {
    expect(formatSql("   ", pg)).toBe("")
    expect(meaning(formatSql("select (1, (2", pg))).toEqual(meaning("select (1, (2"))
  })
})
