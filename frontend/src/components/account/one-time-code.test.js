import { describe, expect, test } from "bun:test"
import { createElement as h } from "react"
import { renderToStaticMarkup } from "react-dom/server"
import { Field } from "../form"
import { OneTimeCodeInput, RecoveryCodes } from "./one-time-code"

const input = (html) => html.match(/<input[^>]*>/)?.[0] ?? ""
const attr = (tag, name) => tag.match(new RegExp(`\\s${name}="([^"]*)"`))?.[1]

describe("OneTimeCodeInput", () => {
  test("is always numeric, a one-time code and six characters, at both sizes", () => {
    for (const size of ["flow", "reading", undefined]) {
      const tag = input(
        renderToStaticMarkup(h(OneTimeCodeInput, { id: "code", value: "", onChange() {}, size })),
      )
      expect(attr(tag, "inputMode")).toBe("numeric")
      expect(attr(tag, "autoComplete")).toBe("one-time-code")
      expect(attr(tag, "maxLength")).toBe("6")
      expect(attr(tag, "id")).toBe("code")
    }
  })

  test("one letter spacing in both registers", () => {
    const flow = attr(
      input(renderToStaticMarkup(h(OneTimeCodeInput, { value: "", onChange() {}, size: "flow" }))),
      "class",
    )
    const reading = attr(
      input(renderToStaticMarkup(h(OneTimeCodeInput, { value: "", onChange() {} }))),
      "class",
    )
    expect(flow).toContain("tracking-[0.4em]")
    expect(reading).toContain("tracking-[0.4em]")
    expect(flow).toContain("h-14")
    expect(reading).toContain("h-11")
  })

  test("inside a Field it is named and described by it", () => {
    const html = renderToStaticMarkup(
      h(
        Field,
        { label: "Code", hint: "From your app" },
        h(OneTimeCodeInput, { value: "", onChange() {} }),
      ),
    )
    const tag = input(html)
    const id = attr(tag, "id")
    expect(id).toBeTruthy()
    expect(html).toContain(`for="${id}"`)
    expect(attr(tag, "aria-describedby")).toBeTruthy()
  })
})

describe("RecoveryCodes", () => {
  test("lists every code once; framed carries its own warning", () => {
    const codes = ["aaaa-bbbb", "cccc-dddd"]
    const plain = renderToStaticMarkup(h(RecoveryCodes, { codes, onDone() {} }))
    const framed = renderToStaticMarkup(h(RecoveryCodes, { codes, onDone() {}, framed: true }))
    for (const html of [plain, framed]) {
      for (const code of codes) expect(html.split(code)).toHaveLength(2)
      expect(html).toContain("I have saved them")
      expect(html).toContain("Copy all")
    }
    expect(plain).not.toContain("Save these recovery codes")
    expect(framed).toContain("Save these recovery codes")
  })
})
