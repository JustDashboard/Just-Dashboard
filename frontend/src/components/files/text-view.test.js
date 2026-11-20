import { describe, expect, test } from "bun:test"
import {
  indentJson,
  parseDelimited,
  readsAsMarkdown,
  resolveRef,
  textViewOf,
  tokenTone,
} from "./text-view"

describe("which view a text file gets", () => {
  test("by its extension", () => {
    expect(textViewOf("README.md", "hi")).toBe("markdown")
    expect(textViewOf("page.MDX", "hi")).toBe("markdown")
    expect(textViewOf("notes.markdown", "hi")).toBe("markdown")
    expect(textViewOf("sales.csv", "a,b\n")).toBe("csv")
    expect(textViewOf("sales.tsv", "a\tb\n")).toBe("tsv")
    expect(textViewOf("nginx.conf", "server {}")).toBeNull()
  })

  test("JSON only when it arrived on one line", () => {
    expect(textViewOf("data.json", '{"a":1}\n')).toBe("json")
    expect(textViewOf("data.json", '{\n  "a": 1\n}\n')).toBeNull()
    expect(textViewOf("data.json", "")).toBeNull()
  })

  test("a README with no extension when it reads as Markdown", () => {
    expect(textViewOf("README", "# Title\n\nWords")).toBe("markdown")
    expect(textViewOf("readme", "See [the docs](docs/a.md).")).toBe("markdown")
    expect(textViewOf("README", "Plain words, nothing else.\nAnother line.")).toBeNull()
    expect(textViewOf("NOTES", "# Title")).toBeNull()
    expect(readsAsMarkdown("```sh\nmake\n```")).toBe(true)
    expect(readsAsMarkdown("- one\n- two")).toBe(true)
  })
})

describe("indenting JSON that arrived on one line", () => {
  test("objects, arrays, empties and strings holding the punctuation", () => {
    expect(indentJson('{"a":[1,2],"b":{},"c":"x,{y}:\\"z"}')).toBe(
      '{\n  "a": [\n    1,\n    2\n  ],\n  "b": {},\n  "c": "x,{y}:\\"z"\n}\n',
    )
  })

  test("numbers are copied, never re-printed", () => {
    expect(indentJson('{"id":12345678901234567890}')).toContain("12345678901234567890")
  })

  test("a head cut part of the way through still indents what is there", () => {
    expect(indentJson('{"a":[1,{"b":"cut her')).toBe(
      '{\n  "a": [\n    1,\n    {\n      "b": "cut her\n',
    )
  })
})

describe("reading CSV and TSV", () => {
  test("a header, quoted fields with delimiters, quotes and line breaks", () => {
    const table = parseDelimited(
      'name,note,total\n"Smith, J","said ""hi""\nthen left",12\n',
      ",",
      false,
    )
    expect(table.header).toEqual(["name", "note", "total"])
    expect(table.rows).toEqual([["Smith, J", 'said "hi"\nthen left', "12"]])
    expect(table.cut).toBe(false)
  })

  test("numbers are found per column; an empty cell does not spoil one", () => {
    const table = parseDelimited("a,b,c\n1,x,1.5\n2,y,\n-3,z,1e3\n", ",", false)
    expect(table.numeric).toEqual([true, false, true])
  })

  test("a head that stopped inside a row drops that row and says so", () => {
    const table = parseDelimited("a,b\n1,2\n3,", ",", true)
    expect(table.rows).toEqual([["1", "2"]])
    expect(table.cut).toBe(true)
    const quoted = parseDelimited('a,b\n1,"open\nstill', ",", true)
    expect(quoted.rows).toEqual([])
    expect(quoted.cut).toBe(true)
  })

  test("a whole file without a final line break keeps its last row", () => {
    expect(parseDelimited("a,b\n1,2", ",", false).rows).toEqual([["1", "2"]])
  })

  test("ragged rows are filled to the widest; CRLF and a BOM are read", () => {
    const table = parseDelimited("﻿a\tb\r\n1\r\n2\t3\t4\r\n", "\t", false)
    expect(table.header).toEqual(["a", "b", ""])
    expect(table.rows).toEqual([
      ["1", "", ""],
      ["2", "3", "4"],
    ])
  })
})

describe("a reference written in a file", () => {
  const from = "/srv/app/docs/README.md"
  test("siblings, children, parents and absolute paths", () => {
    expect(resolveRef(from, "setup.md")).toEqual({ path: "/srv/app/docs/setup.md", folder: false })
    expect(resolveRef(from, "./img/shot.png")).toEqual({
      path: "/srv/app/docs/img/shot.png",
      folder: false,
    })
    expect(resolveRef(from, "../LICENSE")).toEqual({ path: "/srv/app/LICENSE", folder: false })
    expect(resolveRef(from, "/etc/hosts")).toEqual({ path: "/etc/hosts", folder: false })
    expect(resolveRef(from, "../../../../../x")).toEqual({ path: "/x", folder: false })
  })

  test("a folder, an anchor and an escaped name", () => {
    expect(resolveRef(from, "guides/")).toEqual({ path: "/srv/app/docs/guides", folder: true })
    expect(resolveRef(from, "setup.md#install")?.path).toBe("/srv/app/docs/setup.md")
    expect(resolveRef(from, "my%20notes.md")?.path).toBe("/srv/app/docs/my notes.md")
    expect(resolveRef(from, "#top")).toBeUndefined()
    expect(resolveRef(from, "%E0%A4%A")).toBeUndefined()
  })
})

test("token types become a few hues", () => {
  expect(tokenTone("keyword.ts")).toBe("keyword")
  expect(tokenTone("string.quote.yaml")).toBe("string")
  expect(tokenTone("comment.line.sh")).toBe("comment")
  expect(tokenTone("number.float.go")).toBe("number")
  expect(tokenTone("identifier.ts")).toBeNull()
  expect(tokenTone("")).toBeNull()
})
