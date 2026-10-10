import { describe, expect, test } from "bun:test"
import { definitePath, nodeAt, nodeSize, parseJsonDoc, printJsonDoc, respaceJson } from "./json-doc"

describe("a document read as text", () => {
  test("a 64-bit number keeps every digit", () => {
    const text = '{"id":12345678901234567890,"price":19.90,"big":1e400}'
    const doc = parseJsonDoc(text)
    expect(printJsonDoc(doc)).toBe(text)
    // What `JSON.parse` would have made of it.
    expect(JSON.stringify(JSON.parse(text))).not.toBe(text)
  })

  test("keys keep their order, and a key written twice is kept twice", () => {
    const text = '{"b":1,"10":2,"2":3,"b":4}'
    expect(printJsonDoc(parseJsonDoc(text))).toBe(text)
  })

  test("strings with quotes, escapes and unicode come back the same value", () => {
    const text = '{"say \\"hi\\"":"line\\nbreak \\u00e9 \\\\ ș"}'
    const doc = parseJsonDoc(text)
    expect(doc.entries[0].key).toBe('say "hi"')
    expect(doc.entries[0].value.value).toBe("line\nbreak é \\ ș")
    expect(JSON.parse(printJsonDoc(doc))).toEqual(JSON.parse(text))
  })

  test("every kind of value", () => {
    const doc = parseJsonDoc(' [ true , false , null , -0.5 , "s" , { } , [ ] ] ')
    expect(doc.items.map((item) => item.kind)).toEqual([
      "boolean",
      "boolean",
      "null",
      "number",
      "string",
      "object",
      "array",
    ])
    expect(printJsonDoc(doc)).toBe('[true,false,null,-0.5,"s",{},[]]')
  })

  test("a scalar is a document too", () => {
    expect(printJsonDoc(parseJsonDoc("42"))).toBe("42")
    expect(printJsonDoc(parseJsonDoc('"x"'))).toBe('"x"')
  })

  test("what is not JSON is null", () => {
    expect(parseJsonDoc("")).toBeNull()
    expect(parseJsonDoc("{a:1}")).toBeNull()
    expect(parseJsonDoc('{"a":1,}')).toBeNull()
    expect(parseJsonDoc("[1 2]")).toBeNull()
  })
})

describe("a document laid out", () => {
  test("indented as JSON.stringify would, with the digits untouched", () => {
    const doc = parseJsonDoc('{"id":12345678901234567890,"tags":["a","b"],"none":{},"empty":[]}')
    expect(printJsonDoc(doc, 2)).toBe(
      [
        "{",
        '  "id": 12345678901234567890,',
        '  "tags": [',
        '    "a",',
        '    "b"',
        "  ],",
        '  "none": {},',
        '  "empty": []',
        "}",
      ].join("\n"),
    )
  })
})

describe("a place in a document", () => {
  const doc = parseJsonDoc('{"json":{"matches":[{"a":[10,20]}],"bytes":9}}')

  test("is reached by keys and positions", () => {
    expect(printJsonDoc(nodeAt(doc, ["json", "matches", 0, "a", 1]))).toBe("20")
    expect(nodeAt(doc, ["json", "matches"]).kind).toBe("array")
  })

  test("a path that leads nowhere is undefined", () => {
    expect(nodeAt(doc, ["json", "nope"])).toBeUndefined()
    expect(nodeAt(doc, ["json", "matches", 5])).toBeUndefined()
    expect(nodeAt(doc, ["json", "bytes", "x"])).toBeUndefined()
    // A position is not a key and a key is not a position.
    expect(nodeAt(doc, ["json", "matches", "0"])).toBeUndefined()
  })

  test("a container says how much it holds", () => {
    expect(nodeSize(parseJsonDoc('{"a":1}'))).toBe("1 key")
    expect(nodeSize(parseJsonDoc("[1,2,3]"))).toBe("3 items")
    expect(nodeSize(parseJsonDoc("1"))).toBe("")
  })
})

describe("a path that names exactly one place", () => {
  test("member names and positions only", () => {
    for (const path of ["$", "$.a", "$.a.b[0]", '$["postal code"].x', "$.items[12].sku"]) {
      expect(definitePath(path)).toBe(true)
    }
  })

  test("a wildcard, a filter, a slice or a descent matches a set of places", () => {
    for (const path of ["$.items[*]", "$..sku", "$.a[?(@.x>1)]", "$.a[0:2]", "a.b", ""]) {
      expect(definitePath(path)).toBe(false)
    }
  })
})

describe("a document spaced again", () => {
  const text =
    '{"id":12345678901234567890,"price":1.10,"n":1e3,"s":"a \\u00e9 \\"q\\" {,}:","e":{},"l":[ ],"t":[true,null]}'

  test("formatting changes the spacing and no digit", () => {
    const spaced = respaceJson(text, 2)
    expect(spaced).toBe(
      [
        "{",
        '  "id": 12345678901234567890,',
        '  "price": 1.10,',
        '  "n": 1e3,',
        '  "s": "a \\u00e9 \\"q\\" {,}:",',
        '  "e": {},',
        '  "l": [],',
        '  "t": [',
        "    true,",
        "    null",
        "  ]",
        "}",
      ].join("\n"),
    )
    // What the old formatter wrote for the same value.
    expect(JSON.stringify(JSON.parse(text), null, 2)).not.toContain("12345678901234567890")
  })

  test("minifying a formatted document gives back the compact one", () => {
    expect(respaceJson(respaceJson(text, 2), 0)).toBe(text.replace("[ ]", "[]"))
  })

  test("an ordinary document is laid out as JSON.stringify lays it out", () => {
    const value = { a: [1, 2, { b: "x" }], c: {}, d: [], e: "s p a c e", f: -0.5 }
    const compact = JSON.stringify(value)
    expect(respaceJson(compact, 2)).toBe(JSON.stringify(value, null, 2))
    expect(respaceJson(JSON.stringify(value, null, 4), 0)).toBe(compact)
  })

  test("a scalar, and text that is not JSON", () => {
    expect(respaceJson(" 12345678901234567890 ", 2)).toBe("12345678901234567890")
    expect(respaceJson("{oops", 2)).toBeNull()
  })
})
