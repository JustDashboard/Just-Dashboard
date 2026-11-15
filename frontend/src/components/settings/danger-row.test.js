import { expect, test } from "bun:test"
import { createElement as h } from "react"
import { renderToStaticMarkup } from "react-dom/server"
import { DangerRow } from "./danger-row"

test("an act's title is an h3, under the zone's h2", () => {
  const html = renderToStaticMarkup(h(DangerRow, { title: "Forget this connection" }))
  expect(html).toMatch(/<h3[^>]*>Forget this connection<\/h3>/)
  expect(html).not.toContain("<h4")
})

test("an act with no button goes quiet; one with a button does not", () => {
  const held = renderToStaticMarkup(h(DangerRow, { title: "Delete", sentence: "Protected." }))
  const open = renderToStaticMarkup(
    h(DangerRow, { title: "Delete", action: h("button", null, "Delete…") }),
  )
  expect(held).toMatch(/<h3 class="[^"]*text-muted-foreground/)
  expect(open).not.toMatch(/<h3 class="[^"]*text-muted-foreground/)
})

test("a mark takes a column of its own; without one there is none", () => {
  const marked = renderToStaticMarkup(
    h(DangerRow, { title: "Delete", mark: h("span", { "data-mark": "" }) }),
  )
  const bare = renderToStaticMarkup(h(DangerRow, { title: "Delete" }))
  expect(marked).toContain("grid-cols-[auto_minmax(0,1fr)]")
  expect(bare).not.toContain("grid-cols-[auto_minmax(0,1fr)]")
})
