import { describe, expect, test } from "bun:test"
import {
  JsonSyntaxError,
  base64Bytes,
  cellOf,
  dotted,
  fromJson,
  idLabel,
  inputOf,
  nodeAt,
  parseDocument,
  parseJson,
  printCanonical,
  printJson,
  printReadable,
  printShell,
  sameValue,
  shellScalar,
  sizeWord,
  typedValue,
} from "./bson"

// One document holding every type the server sends, as it sends it: canonical
// Extended JSON, the fields in an order no alphabet would give them.
const EVERYTHING =
  '{"_id":{"$oid":"6abe79972945ac11a3124bfc"},"zeta":"last in the alphabet, first after _id",' +
  '"10":"a key that looks like an index","2":"and another",' +
  '"int":{"$numberInt":"19"},"long":{"$numberLong":"9007199254740993"},' +
  '"double":{"$numberDouble":"5.0"},"fraction":{"$numberDouble":"3.17"},' +
  '"nan":{"$numberDouble":"NaN"},"decimal":{"$numberDecimal":"19.990"},' +
  '"yes":true,"nothing":null,' +
  '"when":{"$date":{"$numberLong":"1790864263204"}},' +
  '"bytes":{"$binary":{"base64":"AAEC","subType":"00"}},' +
  '"uuid":{"$binary":{"base64":"EjRWeBI0VngSNFZ4EjRWeA==","subType":"04"}},' +
  '"stamp":{"$timestamp":{"t":1700000000,"i":7}},' +
  '"pattern":{"$regularExpression":{"pattern":"^a/b","options":"i"}},' +
  '"code":{"$code":"function () { return 1 }"},"low":{"$minKey":1},"high":{"$maxKey":1},' +
  '"gone":{"$undefined":true},"sym":{"$symbol":"s"},' +
  '"nested":{"b":{"$numberInt":"2"},"a":{"$numberInt":"1"}},' +
  '"list":[{"$numberLong":"1"},"two",{"k":false}],"empty":{},"none":[]}'

describe("a document is read without losing anything", () => {
  // C2: the old editor passed a document through JSON.parse and a grid row,
  // and saving it unchanged rewrote its types and re-ordered its fields.
  test("read and printed back, the text is the text that came", () => {
    expect(printCanonical(parseDocument(EVERYTHING))).toBe(EVERYTHING)
  })

  test("indented for an editor, it is still the same document", () => {
    const laidOut = printCanonical(parseDocument(EVERYTHING), true)
    expect(laidOut).toContain('\n  "long": {"$numberLong":"9007199254740993"},')
    expect(printCanonical(parseDocument(laidOut))).toBe(EVERYTHING)
  })

  test("fields keep their stored order, a key that looks like a number included", () => {
    const root = parseDocument(EVERYTHING)
    expect(root.type).toBe("object")
    expect(root.fields.slice(0, 4).map((field) => field.name)).toEqual(["_id", "zeta", "10", "2"])
    expect(nodeAt(root, ["nested"]).fields.map((field) => field.name)).toEqual(["b", "a"])
  })

  test("every value has the type it was stored with", () => {
    const root = parseDocument(EVERYTHING)
    const types = Object.fromEntries(root.fields.map((field) => [field.name, field.value.type]))
    expect(types).toMatchObject({
      _id: "objectId",
      int: "int",
      long: "long",
      double: "double",
      fraction: "double",
      nan: "double",
      decimal: "decimal",
      yes: "bool",
      nothing: "null",
      when: "date",
      bytes: "binData",
      uuid: "binData",
      stamp: "timestamp",
      pattern: "regex",
      code: "javascript",
      low: "minKey",
      high: "maxKey",
      gone: "undefined",
      sym: "symbol",
      nested: "object",
      list: "array",
      empty: "object",
      none: "array",
    })
  })

  test("a 64-bit integer keeps every digit, and a double is told from an integer", () => {
    const root = parseDocument(EVERYTHING)
    expect(nodeAt(root, ["long"]).text).toBe("9007199254740993")
    expect(nodeAt(root, ["double"]).text).toBe("5.0")
    expect(nodeAt(root, ["decimal"]).text).toBe("19.990")
    expect(nodeAt(root, ["int"]).text).toBe("19")
  })

  test("a document that merely has a $-key is a document, not a typed value", () => {
    const root = parseDocument('{"q":{"$gt":{"$numberInt":"5"}},"r":{"$oid":"x","more":1}}')
    expect(nodeAt(root, ["q"]).type).toBe("object")
    expect(nodeAt(root, ["q", "$gt"]).type).toBe("int")
    expect(nodeAt(root, ["r"]).type).toBe("object")
  })

  test("relaxed text reads a bare number the way the server does", () => {
    expect(fromJson(parseJson("5")).type).toBe("int")
    expect(fromJson(parseJson("2147483648")).type).toBe("long")
    expect(fromJson(parseJson("5.0")).type).toBe("double")
    expect(fromJson(parseJson("1e3")).type).toBe("double")
    expect(fromJson(parseJson('{"$date":"2026-05-01T12:00:00Z"}')).type).toBe("date")
  })
})

describe("JSON text", () => {
  test("a mistake is said with its line and column", () => {
    expect(() => parseJson('{\n  "a": 1,\n  b: 2\n}')).toThrow(JsonSyntaxError)
    try {
      parseJson('{\n  "a": 1,\n  b: 2\n}')
    } catch (err) {
      expect(err.line).toBe(3)
      expect(err.column).toBe(3)
      expect(err.message).toContain("field name in quotes")
    }
    expect(() => parseJson('{"a": 1} trailing')).toThrow(/after the end/)
    expect(() => parseJson('{"a": "open')).toThrow(/never closed/)
    expect(() => parseJson("")).toThrow(/where a value was expected/)
  })

  test("number text is never passed through a JavaScript number", () => {
    expect(printJson(parseJson("[12345678901234567890, 1.10, -0, 1E5]"))).toBe(
      "[12345678901234567890,1.10,-0,1E5]",
    )
  })

  test("strings are their own value, escapes and all", () => {
    const json = parseJson('{"a":"line\\nbreak \\u00e9 \\"q\\""}')
    expect(json.entries[0][1].value).toBe('line\nbreak é "q"')
  })
})

describe("the text a person reads and edits", () => {
  test("a date is its ISO moment, and every other type keeps its canonical wrapper", () => {
    const doc = parseDocument(
      '{"at":{"$date":{"$numberLong":"1790911442889"}},"n":{"$numberInt":"19"},' +
        '"big":{"$numberLong":"9007199254740993"},"list":[{"$date":{"$numberLong":"0"}}]}',
    )
    expect(printReadable(doc)).toBe(
      '{"at":{"$date":"2026-10-02T03:24:02.889Z"},"n":{"$numberInt":"19"},' +
        '"big":{"$numberLong":"9007199254740993"},"list":[{"$date":"1970-01-01T00:00:00.000Z"}]}',
    )
  })

  test("it reads back as the same document", () => {
    const canonical = '{"at":{"$date":{"$numberLong":"1790911442889"}},"n":{"$numberDouble":"5.0"}}'
    const again = parseDocument(printReadable(parseDocument(canonical)))
    expect(again.fields[0].value).toMatchObject({ type: "date", text: "2026-10-02T03:24:02.889Z" })
    expect(again.fields[1].value).toMatchObject({ type: "double", text: "5.0" })
  })

  test("a date ISO text cannot spell stays the milliseconds it is", () => {
    const before = '{"$date":{"$numberLong":"-62135596800000"}}'
    const after = '{"$date":{"$numberLong":"253402300800000"}}'
    expect(printReadable(parseDocument(`{"a":${before},"b":${after}}`))).toBe(
      `{"a":${before},"b":${after}}`,
    )
  })

  test("a date is laid out on one line, like every other typed value", () => {
    expect(printReadable(parseDocument('{"at":{"$date":{"$numberLong":"0"}}}'), true)).toBe(
      '{\n  "at": {"$date":"1970-01-01T00:00:00.000Z"}\n}',
    )
  })
})

describe("the shell's spelling", () => {
  test("each type is written as the shell writes it", () => {
    const root = parseDocument(EVERYTHING)
    const shell = (name) => shellScalar(nodeAt(root, [name]))
    expect(shell("_id")).toBe('ObjectId("6abe79972945ac11a3124bfc")')
    expect(shell("long")).toBe('Long("9007199254740993")')
    expect(shell("decimal")).toBe('Decimal128("19.990")')
    expect(shell("when")).toBe('ISODate("2026-10-01T14:17:43.204Z")')
    expect(shell("int")).toBe("19")
    expect(shell("double")).toBe("5.0")
    expect(shell("yes")).toBe("true")
    expect(shell("nothing")).toBe("null")
    expect(shell("bytes")).toBe('BinData(0, "AAEC")')
    expect(shell("uuid")).toBe('UUID("12345678-1234-5678-1234-567812345678")')
    expect(shell("stamp")).toBe("Timestamp({ t: 1700000000, i: 7 })")
    expect(shell("pattern")).toBe("/^a\\/b/i")
    expect(shell("low")).toBe("MinKey()")
  })

  test("a whole document, with bare keys where a key allows it", () => {
    const root = parseDocument('{"a":{"$numberInt":"1"},"b c":["x",{"$numberLong":"2"}],"e":{}}')
    expect(printShell(root)).toBe('{ a: 1, "b c": [ "x", Long("2") ], e: {} }')
  })

  test("an _id is named as the shell names it", () => {
    expect(idLabel('{"$oid":"6abe79972945ac11a3124bfc"}')).toBe(
      'ObjectId("6abe79972945ac11a3124bfc")',
    )
    expect(idLabel('"abc"')).toBe('"abc"')
    expect(idLabel('{"$numberInt":"3"}')).toBe("3")
    expect(idLabel("")).toBe("no _id")
  })
})

describe("a value typed by a person", () => {
  test("the type is chosen, not guessed from the digits", () => {
    const long = typedValue("long", "4")
    expect(long.ok && printCanonical(long.node)).toBe('{"$numberLong":"4"}')
    const int = typedValue("int", "4")
    expect(int.ok && printCanonical(int.node)).toBe('{"$numberInt":"4"}')
    const double = typedValue("double", "4")
    expect(double.ok && printCanonical(double.node)).toBe('{"$numberDouble":"4.0"}')
    expect(double.ok && double.node.text).toBe("4.0")
    const decimal = typedValue("decimal", "19.990")
    expect(decimal.ok && printCanonical(decimal.node)).toBe('{"$numberDecimal":"19.990"}')
  })

  test("a 64-bit integer past 2^53 is kept digit for digit", () => {
    const typed = typedValue("long", "9007199254740993")
    expect(typed.ok && printCanonical(typed.node)).toBe('{"$numberLong":"9007199254740993"}')
  })

  test("a value outside its type is refused with the reason", () => {
    expect(typedValue("int", "2147483648")).toMatchObject({ ok: false })
    expect(typedValue("int", "1.5")).toMatchObject({ ok: false })
    expect(typedValue("long", "9223372036854775808")).toMatchObject({ ok: false })
    expect(typedValue("objectId", "abc")).toMatchObject({ ok: false })
    expect(typedValue("bool", "yes")).toMatchObject({ ok: false })
    expect(typedValue("date", "soon")).toMatchObject({ ok: false })
    expect(typedValue("decimal", "1,5")).toMatchObject({ ok: false })
    expect(typedValue("ejson", "{ unquoted: 1 }")).toMatchObject({ ok: false })
  })

  test("a string is exactly what was typed, an empty one included", () => {
    const empty = typedValue("string", "")
    expect(empty.ok && printCanonical(empty.node)).toBe('""')
    const spaced = typedValue("string", "  two  ")
    expect(spaced.ok && printCanonical(spaced.node)).toBe('"  two  "')
  })

  test("a moment with no zone is UTC; null takes no text", () => {
    const date = typedValue("date", "2026-05-01T12:00:00")
    expect(date.ok && printCanonical(date.node)).toBe('{"$date":{"$numberLong":"1777636800000"}}')
    const day = typedValue("date", "2026-05-01")
    expect(day.ok && day.node.text).toBe("2026-05-01T00:00:00.000Z")
    const nothing = typedValue("null", "")
    expect(nothing.ok && printCanonical(nothing.node)).toBe("null")
  })

  test("Extended JSON is taken for every other type, and for whole documents", () => {
    const binary = typedValue("ejson", '{"$binary":{"base64":"AAEC","subType":"00"}}')
    expect(binary.ok && binary.node.type).toBe("binData")
    const doc = typedValue("ejson", '{"a":[1,{"$numberLong":"2"}]}')
    expect(doc.ok && doc.node.type).toBe("object")
  })

  test("an editor opens on the value's own type and text", () => {
    const root = parseDocument(EVERYTHING)
    expect(inputOf(nodeAt(root, ["long"]))).toEqual({ type: "long", text: "9007199254740993" })
    expect(inputOf(nodeAt(root, ["nothing"]))).toEqual({ type: "null", text: "" })
    expect(inputOf(nodeAt(root, ["bytes"])).type).toBe("ejson")
    expect(inputOf(nodeAt(root, ["nested"])).type).toBe("ejson")
    // What it opens on, typed back unchanged, is the value it was.
    for (const name of ["int", "long", "double", "decimal", "yes", "when", "_id", "zeta"]) {
      const node = nodeAt(root, [name])
      const opened = inputOf(node)
      const back = typedValue(opened.type, opened.text)
      expect(back.ok && printCanonical(back.node)).toBe(printCanonical(node))
    }
  })
})

describe("the same value", () => {
  test("a double is the same number however it is spelled", () => {
    const a = fromJson(parseJson('{"$numberDouble":"5.0"}'))
    const b = fromJson(parseJson('{"$numberDouble":"5"}'))
    const c = fromJson(parseJson('{"$numberDouble":"5E+0"}'))
    expect(sameValue(a, b)).toBe(true)
    expect(sameValue(a, c)).toBe(true)
    const nan = fromJson(parseJson('{"$numberDouble":"NaN"}'))
    expect(sameValue(nan, nan)).toBe(true)
  })

  test("an Int32 and an Int64 of the same digits are not the same value", () => {
    const int = fromJson(parseJson('{"$numberInt":"3"}'))
    const long = fromJson(parseJson('{"$numberLong":"3"}'))
    expect(sameValue(int, long)).toBe(false)
    expect(sameValue(int, int)).toBe(true)
  })
})

describe("paths", () => {
  test("a path an update can name", () => {
    expect(dotted(["address", "city"])).toBe("address.city")
    expect(dotted(["roles", 0])).toBe("roles.0")
  })

  test("a field whose name holds a dot, starts with $ or is empty has none", () => {
    expect(dotted(["a.b"])).toBeNull()
    expect(dotted(["$set"])).toBeNull()
    expect(dotted([""])).toBeNull()
    expect(dotted([])).toBeNull()
  })

  test("a value by its path", () => {
    const root = parseDocument(EVERYTHING)
    expect(nodeAt(root, ["list", 2, "k"]).text).toBe("false")
    expect(nodeAt(root, ["list", 9])).toBeUndefined()
    expect(nodeAt(root, ["zeta", "x"])).toBeUndefined()
  })
})

describe("a cell of the table", () => {
  test("numbers are their digits, never a rounded JavaScript number", () => {
    const root = parseDocument(EVERYTHING)
    expect(cellOf(nodeAt(root, ["long"]))).toEqual({ value: "9007199254740993", kind: "number" })
    expect(cellOf(nodeAt(root, ["decimal"]))).toEqual({ value: "19.990", kind: "number" })
  })

  test("each kind is the kind the grid draws", () => {
    const root = parseDocument(EVERYTHING)
    expect(cellOf(nodeAt(root, ["_id"])).kind).toBe("uuid")
    expect(cellOf(nodeAt(root, ["when"]))).toEqual({
      value: "2026-10-01T14:17:43.204Z",
      kind: "datetime",
    })
    expect(cellOf(nodeAt(root, ["yes"]))).toEqual({ value: true, kind: "boolean" })
    expect(cellOf(nodeAt(root, ["nothing"]))).toEqual({ value: null, kind: "unknown" })
    expect(cellOf(nodeAt(root, ["nested"])).kind).toBe("json")
    expect(cellOf(nodeAt(root, ["list"])).kind).toBe("array")
    expect(cellOf(nodeAt(root, ["stamp"])).value).toBe("Timestamp({ t: 1700000000, i: 7 })")
  })
})

test("sizes in words", () => {
  const root = parseDocument('{"a":{"x":1},"b":[1,2],"c":[]}')
  expect(sizeWord(nodeAt(root, ["a"]))).toBe("1 field")
  expect(sizeWord(nodeAt(root, ["b"]))).toBe("2 elements")
  expect(sizeWord(nodeAt(root, ["c"]))).toBe("0 elements")
  expect(base64Bytes("AAEC")).toBe(3)
  expect(base64Bytes("AA==")).toBe(1)
})
