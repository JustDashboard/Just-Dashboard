import { describe, expect, test } from "bun:test"
import {
  clipText,
  decodeGridClip,
  encodeGridClip,
  parseDelimited,
  parseTSV,
  previewNote,
  toCSV,
  toJSONRows,
  toMarkdown,
  toTSV,
  uniqueNames,
} from "./clipboard"
import { DEFAULT_VALUE } from "./values"

describe("writing tab-separated text", () => {
  test("plain fields are joined by tabs and rows by line feeds", () => {
    expect(
      toTSV([
        ["1", "ann", ""],
        ["2", "bo", "x"],
      ]),
    ).toBe("1\tann\t\n2\tbo\tx")
  })

  test("a field holding a tab, a line break or a quote is quoted, with its quotes doubled", () => {
    expect(toTSV([["a\tb", "line1\nline2", 'say "hi"', "cr\rhere"]])).toBe(
      '"a\tb"\t"line1\nline2"\t"say ""hi"""\t"cr\rhere"',
    )
  })

  test("a comma needs no quoting in TSV", () => {
    expect(toTSV([["a,b", "c"]])).toBe("a,b\tc")
  })
})

describe("reading tab-separated text", () => {
  test("rows and fields come back, and the one trailing line end is not an extra row", () => {
    expect(parseTSV("1\tann\n2\tbo\n")).toEqual([
      ["1", "ann"],
      ["2", "bo"],
    ])
    expect(parseTSV("1\tann\r\n2\tbo\r\n")).toEqual([
      ["1", "ann"],
      ["2", "bo"],
    ])
  })

  test("empty fields survive at the start, the middle and the end of a row", () => {
    expect(parseTSV("\ta\t\n\t\t")).toEqual([
      ["", "a", ""],
      ["", "", ""],
    ])
  })

  test("a quoted field keeps its tabs, line breaks and doubled quotes", () => {
    expect(parseTSV('"a\tb"\t"line1\nline2"\t"say ""hi"""\n')).toEqual([
      ["a\tb", "line1\nline2", 'say "hi"'],
    ])
  })

  test("a quote in the middle of a field is text, not the start of a quoted one", () => {
    expect(parseTSV('5" disk\t3.5"\n')).toEqual([['5" disk', '3.5"']])
  })

  test("a single cell, with or without a line end, is one row of one field", () => {
    expect(parseTSV("hello")).toEqual([["hello"]])
    expect(parseTSV("hello\n")).toEqual([["hello"]])
    expect(parseTSV('""')).toEqual([[""]])
    expect(parseTSV("")).toEqual([])
  })

  test("a blank line between rows is a row of one empty field", () => {
    expect(parseTSV("a\n\nb\n")).toEqual([["a"], [""], ["b"]])
  })

  test("an unterminated quote takes the rest of the text rather than losing it", () => {
    expect(parseTSV('"never closed\tx\ny')).toEqual([["never closed\tx\ny"]])
  })

  test("what was written is what is read back", () => {
    const matrix = [
      ["plain", "with\ttab", 'with "quotes"'],
      ["multi\nline", "", "trailing "],
      ['"', "\r\n", "ünïcödé"],
    ]
    expect(parseTSV(toTSV(matrix))).toEqual(matrix)
    expect(parseTSV(`${toTSV(matrix)}\n`)).toEqual(matrix)
  })
})

describe("comma-separated text", () => {
  test("fields with commas, quotes and line breaks are quoted; rows end in CRLF", () => {
    expect(
      toCSV(
        [
          ["1", "Smith, Ann", 'the "boss"'],
          ["2", "two\nlines", ""],
        ],
        { header: ["id", "full name", "note"] },
      ),
    ).toBe('id,full name,note\r\n1,"Smith, Ann","the ""boss"""\r\n2,"two\nlines",')
  })

  test("the formula guard is off unless asked for, and then marks only what a spreadsheet would run", () => {
    const matrix = [["=1+1", "+1", "-1", "@x", "plain", "a=b"]]
    expect(toCSV(matrix)).toBe("=1+1,+1,-1,@x,plain,a=b")
    expect(toCSV(matrix, { guardFormulas: true })).toBe("'=1+1,'+1,'-1,'@x,plain,a=b")
  })

  test("written CSV reads back through the same parser", () => {
    const matrix = [
      ["a,b", 'c"d', "e\nf"],
      ["", " ", "g"],
    ]
    expect(parseDelimited(toCSV(matrix), ",")).toEqual(matrix)
  })
})

describe("values on the clipboard", () => {
  test("NULL and a pending default are the empty field; everything else is its own text", () => {
    expect(clipText(null)).toBe("")
    expect(clipText(undefined)).toBe("")
    expect(clipText(DEFAULT_VALUE)).toBe("")
    expect(clipText("9223372036854775807")).toBe("9223372036854775807")
    expect(clipText(false)).toBe("false")
    expect(clipText(1.5)).toBe("1.5")
    expect(clipText({ a: [1, null] })).toBe('{"a":[1,null]}')
  })

  test("the grid's own format keeps NULL apart from the empty string and a string apart from a number", () => {
    const cells = [
      [null, "", "12", 12, true],
      [{ a: 1 }, ["x"], "\\x00ff", "", null],
    ]
    expect(decodeGridClip(encodeGridClip(cells))).toEqual({ cells, previews: [] })
  })

  test("anything that is not that format is refused rather than guessed at", () => {
    expect(decodeGridClip("not json")).toBeNull()
    expect(decodeGridClip('{"v":2,"cells":[]}')).toBeNull()
    expect(decodeGridClip('{"v":1,"cells":[1,2]}')).toBeNull()
    expect(decodeGridClip('{"v":1}')).toBeNull()
  })
})

describe("a Markdown table", () => {
  test("has a header, a rule and a row per row", () => {
    expect(
      toMarkdown(
        ["id", "name"],
        [
          ["1", "Ann"],
          ["2", ""],
        ],
      ),
    ).toBe("| id | name |\n| --- | --- |\n| 1 | Ann |\n| 2 |  |")
  })

  test("a pipe is escaped and a line break does not end the row", () => {
    expect(toMarkdown(["a"], [["x | y"], ["one\ntwo\r\nthree"]])).toBe(
      "| a |\n| --- |\n| x \\| y |\n| one<br>two<br>three |",
    )
  })
})

describe("previews on the clipboard", () => {
  test("a copy says how much of it is only the start of a value", () => {
    expect(previewNote(0)).toBe("")
    expect(previewNote(1)).toBe(" — one value is only its start")
    expect(previewNote(1200)).toBe(" — 1,200 values are only their start")
  })

  test("the cells that were only the start of a value travel marked", () => {
    const cells = [
      ["1", "whole"],
      ["2", "cut…"],
    ]
    const text = encodeGridClip(cells, [{ row: 1, column: 1 }])
    expect(decodeGridClip(text)).toEqual({ cells, previews: [{ row: 1, column: 1 }] })
  })

  test("a block with none says nothing about them, and a mangled mark is dropped", () => {
    expect(encodeGridClip([["a"]])).toBe('{"v":1,"cells":[["a"]]}')
    expect(decodeGridClip('{"v":1,"cells":[["a"]],"previews":[[0,"x"],"no",[0,0]]}')).toEqual({
      cells: [["a"]],
      previews: [{ row: 0, column: 0 }],
    })
  })
})

describe("rows as JSON", () => {
  test("two columns with one name both survive", () => {
    const columns = [{ name: "id" }, { name: "id" }, { name: "name" }, { name: "id" }]
    expect(uniqueNames(columns)).toEqual(["id", "id_2", "name", "id_3"])
    expect(JSON.parse(toJSONRows(columns, [["1", "7", "ann", null]]))).toEqual([
      { id: "1", id_2: "7", name: "ann", id_3: null },
    ])
  })

  test("a renamed duplicate does not collide with a column already called that", () => {
    expect(uniqueNames([{ name: "id" }, { name: "id_2" }, { name: "id" }])).toEqual([
      "id",
      "id_2",
      "id_3",
    ])
  })

  test("values are the wire values: a big integer stays a string, NULL stays null", () => {
    const text = toJSONRows([{ name: "n" }, { name: "v" }], [["9007199254740993", null]])
    expect(text).toContain('"n": "9007199254740993"')
    expect(text).toContain('"v": null')
  })
})
