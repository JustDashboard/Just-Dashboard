import { describe, expect, test } from "bun:test"
import { renderToStaticMarkup } from "react-dom/server"
import { createElement, Fragment } from "react"
import { opening, topStatementItems } from "./top-statements"

const statement = (over = {}) => ({
  id: "q1",
  query: "SELECT *\n  FROM orders WHERE id = $1",
  calls: 3200,
  totalMs: 120,
  meanMs: 0.004,
  share: 0.0004,
  rows: 3200,
  hitRatio: 1,
  ...over,
})
const html = (node) => renderToStaticMarkup(createElement(Fragment, null, node))

describe("topStatementItems", () => {
  test("words a row in the shared figures: the share, the calls and the time each", () => {
    const [row] = topStatementItems(7, [statement()])
    expect(row.value).toBe("<0.1%")
    expect(row.hint).toBe("3,200 calls · <0.01 ms each")
    expect(row.title).toBe(
      "SELECT * FROM orders WHERE id = $1 — <0.1% of runtime. Open the statement",
    )
  })

  test("opens the statement's own page, keyed so a digest listed twice is two rows", () => {
    const rows = topStatementItems(7, [statement(), statement({ share: 0.5 })])
    expect(rows.map((row) => row.key)).toEqual(["q1", "q1~1"])
    expect(rows[0].href).toContain("/databases/7/performance/statements/q1")
    expect(rows[1].href).not.toBe(rows[0].href)
    expect(rows[0].onClick).toBeUndefined()
  })

  test("draws the first word in the hue of what it does, on one line", () => {
    const [row] = topStatementItems(7, [statement()])
    expect(html(row.label)).toMatch(/^<span style="color:[^"]+">SELECT<\/span> \* FROM orders/)
  })

  test("says in amber when reads miss the cache", () => {
    const [row] = topStatementItems(7, [statement({ calls: 1, hitRatio: 0.5 })])
    const hint = html(row.hint)
    expect(hint).toContain("1 call · ")
    expect(hint).toContain('<span class="text-warning"> · 50% of reads missed the cache</span>')
  })
})

describe("opening", () => {
  test("cuts a long statement at a word", () => {
    const long = `SELECT ${"column_name, ".repeat(10)}FROM t`
    const cut = opening(long)
    expect(cut.endsWith("…")).toBe(true)
    expect(cut.length).toBeLessThanOrEqual(61)
  })
})
