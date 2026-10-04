import { describe, expect, test } from "bun:test"
import { readDockerfile } from "./dockerfile"

const kinds = (line) => line.pieces.map((piece) => [piece.kind, piece.text])

describe("readDockerfile", () => {
  test("names instructions, flags, variables and strings", () => {
    const { lines } = readDockerfile('COPY --from=build /app $DIR\nENV A="b"')
    expect(kinds(lines[0])).toEqual([
      ["instruction", "COPY"],
      ["flag", " --from"],
      ["plain", "=build /app "],
      ["variable", "$DIR"],
    ])
    expect(kinds(lines[1]).map(([kind]) => kind)).toEqual(["instruction", "plain", "string"])
  })

  test("reads stages and the images they start from", () => {
    const { lines, facts } = readDockerfile(
      "FROM node:22 AS build\nRUN bun install\nFROM build AS test\nFROM scratch\nFROM nginx:1",
    )
    expect(facts).toEqual({ lines: 5, stages: 4, bases: ["node:22", "nginx:1"] })
    expect(lines[0].fromStage).toBe("build")
    expect(kinds(lines[0]).map(([kind]) => kind)).toEqual([
      "instruction",
      "plain",
      "image",
      "plain",
      "instruction",
      "plain",
      "stage",
    ])
  })

  test("a continued line is arguments, not an instruction", () => {
    const { lines } = readDockerfile("RUN apt-get update && \\\n  copy x\n# note")
    expect(kinds(lines[1]).some(([kind]) => kind === "instruction")).toBe(false)
    expect(kinds(lines[2])).toEqual([["comment", "# note"]])
  })

  test("joins back to the source", () => {
    const source = 'FROM --platform=$BUILDPLATFORM node:22 AS  web\n\tRUN echo "hi $X" # tail\n'
    const { lines } = readDockerfile(source)
    expect(lines.map((line) => line.pieces.map((p) => p.text).join("")).join("\n")).toBe(source)
  })
})
