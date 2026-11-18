import { describe, expect, test } from "bun:test"
import { renderToStaticMarkup } from "react-dom/server"
import { createElement } from "react"
import { CODE, CodeLines, DOCKERFILE_HUE, dockerfileLines, gutterCh, tokenDockerfile } from "./code"

describe("tokenDockerfile", () => {
  test("an instruction, its flags, its image and its stage are pieces of their own", () => {
    const from = tokenDockerfile("FROM node:22 AS build")
    const kinds = (pieces) =>
      pieces.filter((piece) => piece.kind !== "plain").map((piece) => [piece.kind, piece.text])
    expect(kinds(from)).toEqual([
      ["instruction", "FROM"],
      ["image", "node:22"],
      ["instruction", "AS"],
      ["stage", "build"],
    ])
    const copy = tokenDockerfile('COPY --from=build "$APP_DIR" .')
    expect(kinds(copy)).toEqual([
      ["instruction", "COPY"],
      ["flag", " --from"],
      ["string", '"$APP_DIR"'],
    ])
    // Nothing is lost: the pieces put back together are the line.
    expect(copy.map((piece) => piece.text).join("")).toBe('COPY --from=build "$APP_DIR" .')
  })

  test("a comment is one piece", () => {
    expect(tokenDockerfile("# build stage")).toEqual([{ kind: "comment", text: "# build stage" }])
  })

  test("a continued line keeps its instruction's arguments, read as a whole file", () => {
    const lines = dockerfileLines("RUN apt-get update \\\n  && apt-get install -y curl\n")
    expect(lines).toHaveLength(2)
    expect(lines[1].pieces.some((piece) => piece.kind === "instruction")).toBe(false)
  })

  test("every Dockerfile piece draws in the one code palette", () => {
    expect(DOCKERFILE_HUE.instruction).toBe(CODE.keyword)
    expect(DOCKERFILE_HUE.image).toBe(CODE.string)
    expect(DOCKERFILE_HUE.comment).toBe(CODE.comment)
    for (const hue of Object.values(DOCKERFILE_HUE)) {
      if (hue) expect(Object.values(CODE)).toContain(hue)
    }
  })
})

describe("CodeLines", () => {
  test("the gutter is as wide as the last line number", () => {
    expect(gutterCh(9)).toBe(1)
    expect(gutterCh(10)).toBe(2)
    expect(gutterCh(900)).toBe(3)
    expect(gutterCh(0)).toBe(1)
  })

  test("the gutter is in the faint ink, never a faded muted", () => {
    const html = renderToStaticMarkup(createElement(CodeLines, { lines: ["a", "b"] }))
    const gutters = [...html.matchAll(/data-slot="code-gutter"[^>]*class="([^"]*)"/g)]
    expect(gutters).toHaveLength(2)
    for (const [, classes] of gutters) {
      expect(classes).toContain("text-faint")
      expect(classes).not.toMatch(/text-muted-foreground\/\d+/)
    }
  })

  test("render draws a line's text and gets its index", () => {
    const html = renderToStaticMarkup(
      createElement(CodeLines, {
        lines: ["one", "two"],
        render: (line, index) => createElement("b", null, `${index}:${line}`),
      }),
    )
    expect(html).toContain("<b>0:one</b>")
    expect(html).toContain("<b>1:two</b>")
  })
})
