import { describe, expect, test } from "bun:test"
import { createElement as h } from "react"
import { renderToStaticMarkup } from "react-dom/server"
import { ROW_NAME, SECONDARY_COLUMN, SortHead, TableCell, TableHead, TableRow } from "./table"

// A head or a cell is drawn inside the row it belongs to, as a browser would parse it.
const inRow = (cell) =>
  renderToStaticMarkup(h("table", null, h("thead", null, h("tr", null, cell))))

const head = (props) => inRow(h(SortHead, { label: "Size", onSort: () => {}, ...props }))

describe("SortHead", () => {
  test("the head says which way the column runs, and the others say none", () => {
    expect(head({ active: true, descending: true })).toContain('aria-sort="descending"')
    expect(head({ active: true, descending: false })).toContain('aria-sort="ascending"')
    expect(head({ active: false, descending: true })).toContain('aria-sort="none"')
  })

  test("aria-sort is on the th, and the words are a button with the focus ring", () => {
    const html = head({ active: true, descending: false })
    expect(html).toMatch(/<th[^>]*aria-sort="ascending"/)
    expect(html).toMatch(/<button type="button"[^>]*class="[^"]*\bfocus-ring\b/)
    expect(html).toContain(">Size<")
  })

  test("one chevron, only on the column in use, turned to the way the rows run", () => {
    const on = head({ active: true, descending: true })
    expect(on.match(/data-sort-glyph/g)).toHaveLength(1)
    expect(on).not.toMatch(/data-sort-glyph=""[^>]*rotate-180/)
    expect(head({ active: true, descending: false })).toMatch(/rotate-180/)
    expect(head({ active: false, descending: false })).not.toContain("data-sort-glyph")
  })

  test("a right-aligned head keeps its words on the figures' edge", () => {
    const html = head({ active: true, descending: true, align: "right" })
    expect(html).toMatch(/<th[^>]*class="[^"]*\btext-right\b/)
    expect(html).toMatch(/<button[^>]*class="[^"]*\bflex-row-reverse\b/)
  })

  test("what sits after the button is beside it, not inside it", () => {
    const html = head({ active: false, descending: false, after: h("i", null, "tip") })
    expect(html).toMatch(/<\/button><span[^>]*><i>tip<\/i><\/span><\/th>/)
  })
})

describe("TableHead", () => {
  test("a column with no visible title is named for a screen reader", () => {
    expect(inRow(h(TableHead, { label: "Actions" }))).toMatch(
      /<th[^>]*><span class="sr-only">Actions<\/span><\/th>/,
    )
  })

  test("a visible title wins over the label", () => {
    expect(inRow(h(TableHead, { label: "Actions" }, "Verbs"))).toMatch(/<th[^>]*>Verbs<\/th>/)
  })

  test("a secondary column is not drawn on a phone, head and cell alike", () => {
    expect(inRow(h(TableHead, { secondary: true }, "Created"))).toContain(SECONDARY_COLUMN)
    expect(inRow(h(TableCell, { secondary: true }, "today"))).toContain(SECONDARY_COLUMN)
    expect(inRow(h(TableCell, null, "today"))).not.toContain(SECONDARY_COLUMN)
  })
})

describe("TableRow", () => {
  const row = (props, ...cells) =>
    renderToStaticMarkup(h("table", null, h("tbody", null, h(TableRow, props, ...cells))))

  test("a row that opens something is a stop until it is seen to hold its name", () => {
    // The first paint is the server's, before the row has been looked into.
    expect(row({ onActivate: () => {} }, h("td", null, "web"))).toContain('tabindex="0"')
  })

  test("a row that opens nothing is never a stop", () => {
    expect(row({}, h("td", null, "web"))).not.toContain("tabindex")
  })

  test("the row's name carries the mark the row and the Workspace look for", () => {
    expect(ROW_NAME).toEqual({ "data-workspace-primary": "" })
  })
})
