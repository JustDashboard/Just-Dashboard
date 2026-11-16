import { describe, expect, test } from "bun:test"
import { createElement as h } from "react"
import { renderToStaticMarkup } from "react-dom/server"
import { TranscriptRow, jobLineToTranscript, streamKind } from "./transcript-line"

const at = "2026-11-16T10:00:00Z"

describe("jobLineToTranscript", () => {
  test("a status line is the runner's heading", () => {
    expect(jobLineToTranscript({ seq: 1, stream: "status", text: "Installing nginx", at })).toEqual(
      { number: 1, text: "Installing nginx", kind: "note" },
    )
  })

  test("stderr is a warning line and stdout plain output", () => {
    expect(jobLineToTranscript({ seq: 2, stream: "stderr", text: "W: no key", at }).kind).toBe(
      "warning",
    )
    expect(jobLineToTranscript({ seq: 3, stream: "stdout", text: "Unpacking", at }).kind).toBe(
      "output",
    )
  })

  test("the number is the job's own sequence, past lines the buffer dropped", () => {
    expect(jobLineToTranscript({ seq: 812, stream: "stdout", text: "done", at }).number).toBe(812)
  })

  test("a stream it does not know is output", () => {
    expect(streamKind("")).toBe("output")
    expect(streamKind("stdin")).toBe("output")
  })
})

describe("TranscriptRow gutter", () => {
  const line = { number: 7, text: "W: no key", kind: "warning" }
  test("a gutter in digits sets the number column's width", () => {
    const html = renderToStaticMarkup(
      h("ol", null, h(TranscriptRow, { line, wrap: true, needle: "", gutter: 3 })),
    )
    expect(html).toContain('style="width:3ch"')
    expect(html).not.toContain("w-9")
    expect(html).toContain("bg-wash-warning")
  })

  test("without one it keeps the fixed column the deploy consoles align to", () => {
    const html = renderToStaticMarkup(
      h("ol", null, h(TranscriptRow, { line, wrap: true, needle: "" })),
    )
    expect(html).toContain("w-9")
  })
})
