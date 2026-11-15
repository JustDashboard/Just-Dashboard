import { describe, expect, test } from "bun:test"
import { createElement } from "react"
import { renderToStaticMarkup } from "react-dom/server"
import { readFileSync, readdirSync } from "node:fs"
import { join, relative } from "node:path"
import { ApiError } from "@/lib/api"
import { Rail, RailName, RailRow } from "./rail"

// The rail as its first render draws it: the rows, the one on screen, and
// what stands in for the rows while the list is read or when it failed.

const ROWS = [
  { name: "alpha", href: "/things/alpha" },
  { name: "bravo", href: "/things/bravo" },
]

const draw = (props = {}) =>
  renderToStaticMarkup(
    createElement(Rail, {
      slot: "thing-rail",
      label: "Things",
      rows: ROWS,
      keyOf: (row) => row.name,
      current: (row) => row.name === "bravo",
      row: (row, on) =>
        createElement(
          RailRow,
          { item: row.name, name: row.name, current: on, href: row.href, mark: null },
          createElement(RailName, { current: on }, row.name),
        ),
      loading: false,
      empty: "No thing matches.",
      search: { placeholder: "Name", query: "", onQuery: () => {} },
      caption: "2 things",
      ...props,
    }),
  )

describe("Rail", () => {
  test("the row on screen is a link marked as the current page", () => {
    const html = draw()
    const current = html.match(/<a [^>]*aria-current="page"[^>]*>/)
    expect(current).not.toBeNull()
    expect(current[0]).toContain('href="/things/bravo"')
    expect(current[0]).toContain('data-workspace-item="bravo"')
    // Every row is a link, so it opens in a new tab and is announced as one.
    expect(html.match(/<a /g)).toHaveLength(2)
    expect(html).not.toContain("<button type=\"button\" data-workspace-item")
    expect(html).toContain('data-slot="thing-rail"')
    expect(html).toContain('aria-label="Search the things"')
  })

  test("while the list is read it draws ghost rows at the 48px row, not 36px bars", () => {
    const html = draw({ rows: [], loading: true, loadingRows: 4 })
    const ghosts = html.match(/<li [^>]*data-slot="rail-ghost"[^>]*>/g)
    expect(ghosts).toHaveLength(4)
    for (const ghost of ghosts) expect(ghost).toContain("h-12")
    // The rows the list comes with are the same height.
    expect(draw()).toMatch(/<a [^>]*class="[^"]*\bh-12\b/)
    expect(html).not.toContain("h-9")
    expect(html).not.toContain("No thing matches.")
    expect(html).toContain('aria-busy="true"')
  })

  test("a list that failed says so with ErrorState, never that nothing matches", () => {
    const error = new ApiError(500, "internal", "the daemon did not answer")
    const html = draw({ rows: [], error, onRetry: () => {} })
    expect(html).toContain('data-slot="error-state"')
    expect(html).toContain("Try again")
    expect(html).not.toContain("No thing matches.")
    expect(html).not.toContain('data-slot="rail-ghost"')
  })

  test("a search that matches nothing says so, under the item on screen", () => {
    const pinned = { name: "charlie", href: "/things/charlie" }
    const html = draw({ rows: [], pinned, current: (row) => row.name === "charlie" })
    expect(html).toContain('href="/things/charlie"')
    expect(html).toContain("No thing matches.")
  })

  test("the width is the rail's, whatever a page still asks for", () => {
    const html = draw({ className: "hidden w-64 shrink-0 xl:flex" })
    const nav = html.match(/<nav [^>]*>/)[0]
    expect(nav).toContain("w-rail")
    expect(nav).not.toContain("w-64")
  })
})

// The rails were twenty-two hand-written columns; this keeps a twenty-third
// out. A rail row's signature is the 48px row with the selected fill.
const SRC = join(import.meta.dir, "..")
const ROW = /flex h-12 w-full min-w-0 items-center gap-2\.5 rounded-lg px-2/

function sources(dir) {
  return readdirSync(dir, { withFileTypes: true }).flatMap((entry) => {
    const path = join(dir, entry.name)
    if (entry.isDirectory()) return sources(path)
    return /-rail\.tsx$/.test(entry.name) ? [path] : []
  })
}

test("no sibling rail file draws its own row", () => {
  const found = sources(join(SRC, "components"))
    .filter((path) => ROW.test(readFileSync(path, "utf8")))
    .map((path) => relative(SRC, path))
  expect(found).toEqual([])
})
