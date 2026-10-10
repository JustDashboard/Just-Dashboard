import { describe, expect, test } from "bun:test"
import { createElement } from "react"
import { renderToStaticMarkup } from "react-dom/server"
import { applyChange, changeCounts, EMPTY_CHANGES } from "./change-set"
import { DataGrid } from "./data-grid"

// What the grid draws, read from the markup of a first render. There is no
// document here, so nothing below presses a key or moves a pointer: this holds
// the structure every interaction stands on — the roles and counts a screen
// reader is told, the words a value is drawn as, the marks of a pending change
// — and the browser checks hold the rest.

const COLUMNS = [
  { key: "id", name: "id", typeName: "bigint", kind: "number", primaryKey: true },
  { key: "email", name: "email", typeName: "text", kind: "text" },
  { key: "note", name: "note", typeName: "text", kind: "text", nullable: true },
  { key: "active", name: "active", typeName: "boolean", kind: "boolean" },
  {
    key: "team",
    name: "team",
    typeName: "bigint",
    kind: "number",
    nullable: true,
    foreignKey: { schema: "public", table: "teams", column: "id" },
  },
  { key: "bio", name: "bio", typeName: "text", kind: "text", nullable: true },
]
const ROWS = [
  ["9223372036854775807", "ann@example.com", null, true, "7", "short"],
  ["2", "bo@example.com", "", false, null, "x".repeat(4096)],
  ["3", "cy@example.com", "hello", true, "8", "third"],
]
const rowId = (row) => String(row[0])

const render = (props) =>
  renderToStaticMarkup(
    createElement(DataGrid, {
      label: "people rows",
      columns: COLUMNS,
      rows: ROWS,
      rowId,
      ...props,
    }),
  )

/** A change set as the hook would hand it over, holding what was staged. */
function staged(...actions) {
  const changes = actions.reduce(applyChange, EMPTY_CHANGES)
  const nothing = () => {}
  return {
    changes,
    counts: changeCounts(changes),
    dirty: true,
    canUndo: true,
    canRedo: false,
    dispatch: nothing,
    newRowId: () => "new:next",
    editCell: nothing,
    insertRow: () => "new:next",
    deleteRows: nothing,
    revertCell: nothing,
    revertRow: nothing,
    discardAll: nothing,
    undo: nothing,
    redo: nothing,
    reset: nothing,
  }
}
const origin = (index) => ({
  values: Object.fromEntries(COLUMNS.map((column, i) => [column.key, ROWS[index][i]])),
})

/** The opening tag of every element carrying an attribute, as text. */
const tags = (html, attribute) =>
  html.match(new RegExp(`<[a-z]+ [^>]*${attribute}[^>]*>`, "g")) ?? []
/** One row's markup, from its opening tag to the next row's. */
const rowHTML = (html, index) => html.split(`data-row="${index}"`)[1]?.split('role="row"')[0] ?? ""
/** One cell's opening tag within a row. */
const cellTag = (html, row, col) =>
  tags(rowHTML(html, row), `data-col="${col}"`)[0] ?? "(no such cell)"

describe("what the grid tells a screen reader", () => {
  test("it is a grid, named, with the real counts whatever is drawn", () => {
    const html = render({})
    const grid = tags(html, 'role="grid"')[0]
    expect(grid).toContain('aria-label="people rows"')
    // One more of each than the data: the header row and the row-number column.
    expect(grid).toContain('aria-rowcount="4"')
    expect(grid).toContain('aria-colcount="7"')
    expect(grid).toContain('aria-multiselectable="true"')
    expect(tags(html, 'data-row="0"')[0]).toContain('aria-rowindex="2"')
    expect(cellTag(html, 0, 0)).toContain('aria-colindex="2"')
  })

  test("without a change set it is read-only, and says so", () => {
    expect(tags(render({}), 'role="grid"')[0]).toContain('aria-readonly="true"')
    const editable = render({ editable: true, changeSet: staged() })
    expect(tags(editable, 'role="grid"')[0]).toContain('aria-readonly="false"')
  })

  test("the way out of the grid is part of its description", () => {
    const html = render({})
    const id = /aria-describedby="([^"]+)"/.exec(tags(html, 'role="grid"')[0])?.[1]
    expect(id).toBeTruthy()
    const described = html.split(`id="${id}"`)[1].split("</p>")[0]
    expect(described).toContain("Escape, then Tab, leaves the grid")
    expect(described).toContain("Shift with Space ticks the row")
  })

  test("there is one tab stop before anything is active: the grid itself", () => {
    const html = render({})
    expect(html.match(/tabindex="0"/g)).toHaveLength(1)
    expect(tags(html, 'role="grid"')[0]).toContain('tabindex="0"')
  })

  test("the corner is a named checkbox that says how much of the page is ticked", () => {
    const corner = (selection) => tags(render({ selection }), "data-select-all")[0]
    expect(corner(undefined)).toContain('role="checkbox"')
    expect(corner(undefined)).toContain('aria-label="Select every row on this page"')
    expect(corner(undefined)).toContain('aria-checked="false"')
    expect(corner({ active: null, anchor: null, rows: ["2"] })).toContain('aria-checked="mixed"')
    expect(
      corner({ active: null, anchor: null, rows: ["9223372036854775807", "2", "3"] }),
    ).toContain('aria-checked="true"')
    expect(render({ selectable: false })).not.toContain("data-select-all")
  })

  test("every button has a name", () => {
    const html = render({
      editable: true,
      changeSet: staged(),
      findable: true,
      onFollowForeignKey: () => {},
      onSortChange: () => {},
    })
    const buttons = html.match(/<button[^>]*>.*?<\/button>/gs) ?? []
    expect(buttons.length).toBeGreaterThan(8)
    for (const button of buttons) {
      const text = button.replace(/<svg.*?<\/svg>/gs, "").replace(/<[^>]+>/g, "")
      expect(text.trim() !== "" || /aria-label="[^"]+"/.test(button)).toBe(true)
    }
  })

  test("a header says its column's type, key and sort", () => {
    const html = render({ sort: [{ column: "email", desc: true }], onSortChange: () => {} })
    const email = html.split('data-hcol="1"')[1].split('role="columnheader"')[0]
    expect(tags(html, 'data-hcol="1"')[0]).toContain('aria-sort="descending"')
    expect(tags(html, 'data-hcol="0"')[0]).toContain('aria-sort="none"')
    expect(email).toContain(">text<")
    expect(html.split('data-hcol="0"')[1]).toContain('aria-label="Primary key"')
    expect(html.split('data-hcol="4"')[1]).toContain('aria-label="References teams"')
  })
})

describe("how values are drawn", () => {
  const html = render({})

  test("a 64-bit integer is drawn digit for digit, to the right", () => {
    expect(rowHTML(html, 0)).toContain(">9223372036854775807<")
    expect(cellTag(html, 0, 0)).toContain("justify-end")
    expect(cellTag(html, 0, 1)).not.toContain("justify-end")
  })

  test("NULL and the empty string are two different words, neither of them a value", () => {
    expect(rowHTML(html, 0)).toContain(">NULL<")
    expect(rowHTML(html, 1)).toContain(">&quot;&quot;<")
    expect(rowHTML(html, 2)).toContain(">hello<")
  })

  test("a boolean is a word, in a sanctioned kind colour and never a palette one", () => {
    expect(rowHTML(html, 0)).toContain(">true<")
    expect(rowHTML(html, 1)).toContain(">false<")
    expect(html).toContain("text-(--tag-violet)")
    expect(html).not.toMatch(/\b(?:text|bg|border)-(?:red|green|blue|amber|violet|pink)-\d{2,3}\b/)
  })

  test("long text is clamped before it reaches the document", () => {
    expect(rowHTML(html, 1)).not.toContain("x".repeat(300))
    expect(rowHTML(html, 1)).toContain(`${"x".repeat(240)}…`)
  })

  test("only a window of rows is drawn, however many there are", () => {
    const rows = Array.from({ length: 1000 }, (_, i) => [String(i), "e", null, true, null, "b"])
    const many = render({ rows })
    expect(tags(many, 'role="grid"')[0]).toContain('aria-rowcount="1001"')
    const drawn = (many.match(/data-row="/g) ?? []).length
    expect(drawn).toBeGreaterThan(10)
    expect(drawn).toBeLessThan(60)
  })

  test("two result columns with one name are both drawn", () => {
    const columns = ["id", "id", "email"].map((name, i) => ({
      key: String(i),
      name,
      typeName: "text",
      kind: "text",
    }))
    const result = render({ columns, rows: [["1", "2", "a"]], rowId: undefined, selectable: false })
    expect(tags(result, "data-hcol")).toHaveLength(3)
    expect(rowHTML(result, 0)).toContain(">1<")
    expect(rowHTML(result, 0)).toContain(">2<")
  })

  test("a foreign key with a value offers the way to its row; a NULL one does not", () => {
    const followed = render({ onFollowForeignKey: () => {} })
    expect(rowHTML(followed, 0)).toContain('aria-label="Open the teams row this points to"')
    expect(rowHTML(followed, 1)).not.toContain("data-follow")
    expect(html).not.toContain("data-follow")
  })
})

describe("how a pending change is drawn", () => {
  const changeSet = staged(
    {
      type: "edit",
      edits: [
        { rowId: "2", column: "email", value: "new@example.com", kind: "text", origin: origin(1) },
      ],
    },
    { type: "delete", rows: [{ rowId: "3", origin: origin(2) }] },
    { type: "insert", rows: [{ id: "new:1", values: { email: "fresh@example.com" } }] },
  )
  const html = render({ editable: true, changeSet })

  test("an edited cell shows the new value, is marked, and keeps the old one for hover", () => {
    const cell = cellTag(html, 1, 1)
    expect(cell).toContain("data-changed")
    expect(cell).toContain('title="Was bo@example.com"')
    expect(cell).toContain("--git-modified")
    expect(rowHTML(html, 1)).toContain(">new@example.com<")
    expect(tags(html, 'data-row="1"')[0]).toContain('data-state="updated"')
  })

  test("a deleted row is struck through in the deleted hue and cannot be edited", () => {
    expect(tags(html, 'data-row="2"')[0]).toContain('data-state="deleted"')
    expect(tags(html, 'data-row="2"')[0]).toContain("--git-deleted")
    expect(cellTag(html, 2, 1)).toContain("line-through")
    expect(cellTag(html, 2, 1)).toContain('aria-readonly="true"')
  })

  test("an inserted row follows the server's, in the added hue, unset columns reading DEFAULT", () => {
    expect(tags(html, 'role="grid"')[0]).toContain('aria-rowcount="5"')
    expect(tags(html, 'data-row="3"')[0]).toContain('data-state="inserted"')
    expect(tags(html, 'data-row="3"')[0]).toContain("--git-added")
    expect(rowHTML(html, 3)).toContain(">fresh@example.com<")
    expect(rowHTML(html, 3)).toContain(">DEFAULT<")
  })

  test("an unset cell of a new row says what its default will be, where the column says", () => {
    const columns = COLUMNS.map((column) =>
      column.key === "note" ? { ...column, defaultExpr: "now()" } : column,
    )
    const withDefault = render({ columns, editable: true, changeSet })
    expect(cellTag(withDefault, 3, 2)).toContain('title="Default: now()"')
    expect(cellTag(withDefault, 3, 4)).not.toContain("title=")
  })

  test("the status strip counts the three kinds with the same signs", () => {
    const strip = html.split('data-slot="data-grid-status"')[1]
    expect(strip).toContain("+1")
    expect(strip).toContain("~1")
    expect(strip).toContain("−1")
    expect(strip).toContain("4 rows")
  })

  test("a row the server refused carries the reason in its gutter", () => {
    const refused = render({ editable: true, changeSet, rowErrors: { 2: "matched 0 rows" } })
    expect(rowHTML(refused, 1)).toContain('aria-label="matched 0 rows"')
  })
})

describe("what cannot be edited", () => {
  const changeSet = staged()

  test("a cell the server cut is read-only in an editable grid, and its neighbours are not", () => {
    const html = render({
      editable: true,
      changeSet,
      clipped: [{ row: 1, column: 5, size: 20000 }],
    })
    expect(cellTag(html, 1, 5)).toContain('aria-readonly="true"')
    expect(cellTag(html, 1, 1)).not.toContain("aria-readonly")
    expect(cellTag(html, 0, 5)).not.toContain("aria-readonly")
  })

  test("a row whose key was cut is read-only from end to end", () => {
    const columns = COLUMNS.map((column) => ({ ...column, primaryKey: column.key === "bio" }))
    const html = render({
      columns,
      editable: true,
      changeSet,
      clipped: [{ row: 1, column: 5, size: 20000 }],
    })
    for (let col = 0; col < COLUMNS.length; col++) {
      expect(cellTag(html, 1, col)).toContain('aria-readonly="true"')
    }
    expect(cellTag(html, 0, 1)).not.toContain("aria-readonly")
  })

  test("computed and locked columns are read-only in every row", () => {
    const columns = COLUMNS.map((column) => ({
      ...column,
      generated: column.key === "note",
      editable: column.key === "email" ? false : undefined,
    }))
    const html = render({ columns, editable: true, changeSet })
    expect(cellTag(html, 0, 1)).toContain('aria-readonly="true"')
    expect(cellTag(html, 0, 2)).toContain('aria-readonly="true"')
    expect(cellTag(html, 0, 3)).not.toContain("aria-readonly")
  })
})

describe("the states around the rows", () => {
  test("loading with nothing yet keeps the header over skeleton rows", () => {
    const html = render({ rows: [], loading: true })
    expect(tags(html, "data-hcol")).toHaveLength(COLUMNS.length)
    expect((html.match(/data-slot="skeleton"/g) ?? []).length).toBeGreaterThan(20)
    expect(tags(html, 'role="grid"')[0]).toContain('aria-busy="true"')
    expect(html).not.toContain("0 rows")
  })

  test("a refresh keeps the rows that are there", () => {
    const html = render({ loading: true })
    expect(html).toContain('data-row="0"')
    expect(html).not.toContain('data-slot="skeleton"')
  })

  test("no rows shows the owner's words under the header, or the default", () => {
    const owned = render({ rows: [], empty: createElement("p", null, "Nothing in this table yet") })
    expect(owned).toContain("Nothing in this table yet")
    expect(tags(owned, "data-hcol")).toHaveLength(COLUMNS.length)
    expect(render({ rows: [] })).toContain("No rows")
  })

  test("a first load that failed shows the error in place of the rows", () => {
    const html = render({ rows: [], error: new Error("relation does not exist") })
    expect(html).toContain("relation does not exist")
    expect(html).not.toContain("data-row=")
  })

  test("a refresh that failed keeps the rows and says so above them", () => {
    const html = render({ error: new Error("connection reset"), onRetry: () => {} })
    expect(html).toContain("These rows could not be refreshed: connection reset")
    expect(html).toContain("Try again")
    expect(html).toContain('data-row="2"')
  })

  test("the reason a table cannot be edited is stated above it", () => {
    const html = render({ readOnlyReason: "A view cannot be edited" })
    expect(html).toContain(">Read-only<")
    expect(html).toContain("A view cannot be edited")
  })

  test("the owner's toolbar, banner and footer are drawn where they belong", () => {
    const html = render({
      toolbar: createElement("span", null, "TOOLBAR"),
      banner: createElement("div", null, "BANNER"),
      footer: createElement("span", null, "FOOTER"),
    })
    const [top, rest] = html.split('data-slot="data-grid-viewport"')
    expect(top).toContain("TOOLBAR")
    expect(top).toContain("BANNER")
    expect(rest.split('data-slot="data-grid-status"')[1]).toContain("FOOTER")
  })
})
