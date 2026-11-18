import { describe, expect, test } from "bun:test"
import { createElement } from "react"
import { renderToStaticMarkup } from "react-dom/server"
import { Read, Stale, staleOf } from "./read"

const poll = (state) => ({ data: undefined, error: undefined, refresh: () => {}, ...state })
const skeleton = createElement("i", { "data-bone": "" })
const draw = (props) =>
  renderToStaticMarkup(
    createElement(Read, { what: "the sessions", skeleton, ...props }, (data) =>
      createElement("b", null, data),
    ),
  )

describe("Read", () => {
  test("waits as the skeleton alone: patience is off unless asked for", () => {
    // Without patience nothing reads the database's context, so a block that
    // never had the waiting line cannot gain one.
    expect(draw({ poll: poll({}) })).toBe('<i data-bone=""></i>')
  })

  test("draws what was read, rising into place only when asked", () => {
    expect(draw({ poll: poll({ data: "ten" }) })).toBe("<b>ten</b>")
    expect(draw({ poll: poll({ data: "ten" }), rise: true })).toBe(
      '<div class="animate-rise"><b>ten</b></div>',
    )
  })

  test("keeps what was read when a later poll fails", () => {
    const html = draw({ poll: poll({ data: "ten", error: new Error("gone") }) })
    expect(html).toBe("<b>ten</b>")
  })

  test("says what could not be read when it never answered", () => {
    const html = draw({ poll: poll({ error: new Error("connection refused") }) })
    expect(html).toContain("Could not read the sessions")
    expect(html).toContain("Try again")
    expect(html).not.toContain("data-bone")
  })
})

describe("Stale", () => {
  test("marks a block only while a failed poll sits over data", () => {
    const error = new Error("timeout")
    expect(staleOf(poll({ data: 1, error }))).toBe(error)
    expect(staleOf(poll({ error }))).toBeUndefined()
    expect(renderToStaticMarkup(createElement(Stale, { poll: poll({ error }) }))).toBe("")
    expect(
      renderToStaticMarkup(createElement(Stale, { poll: poll({ data: 1, error }) })),
    ).toContain("Not updating")
  })
})
