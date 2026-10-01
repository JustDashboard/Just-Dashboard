import { describe, expect, test } from "bun:test"
import {
  addChild,
  parseJsonDoc,
  removeChild,
  replaceNode,
  scalarLiteral,
  scalarText,
  sizeOf,
} from "./json-doc"

const DOC =
  '{"id": 9007199254740993, "b": 1, "10": [true, null, "x"], "who": {"name": "Ana \\"A\\""}}'

describe("a document read as spans", () => {
  test("a number is the digits it was written with, and the keys keep their order", () => {
    const doc = parseJsonDoc(DOC)
    expect(doc.kind).toBe("object")
    expect(doc.entries.map((entry) => entry.key)).toEqual(["id", "b", "10", "who"])
    expect(doc.entries[0].value).toMatchObject({ kind: "number", raw: "9007199254740993" })
    expect(DOC.slice(doc.entries[0].value.start, doc.entries[0].value.end)).toBe("9007199254740993")
  })
  test("strings are decoded, escapes and all", () => {
    const who = parseJsonDoc(DOC).entries[3].value
    expect(who.entries[0].value).toMatchObject({ kind: "string", value: 'Ana "A"' })
  })
  test("arrays, booleans and null", () => {
    const list = parseJsonDoc(DOC).entries[2].value
    expect(list.items.map((item) => item.kind)).toEqual(["boolean", "null", "string"])
    expect(sizeOf(list)).toBe("3 items")
    expect(sizeOf(parseJsonDoc('{"a":1}'))).toBe("1 key")
  })
  test("a scalar on its own is a document, and whitespace is not part of a node", () => {
    expect(parseJsonDoc("  42 ")).toEqual({ kind: "number", start: 2, end: 4, raw: "42" })
    expect(parseJsonDoc('""')).toMatchObject({ kind: "string", value: "" })
    expect(parseJsonDoc("[ ]")).toMatchObject({ kind: "array", items: [] })
    expect(parseJsonDoc("{ }")).toMatchObject({ kind: "object", entries: [] })
  })
  test("what is not JSON is not a tree", () => {
    expect(parseJsonDoc("{a: 1}")).toBeNull()
    expect(parseJsonDoc("")).toBeNull()
    expect(parseJsonDoc('{"a": 1,}')).toBeNull()
  })
  test("two keys of one name are both there", () => {
    expect(parseJsonDoc('{"a": 1, "a": 2}').entries).toHaveLength(2)
  })
})

describe("what a typed text stands for", () => {
  test("a number keeps its digits", () => {
    expect(scalarLiteral("12345678901234567890")).toEqual({
      ok: true,
      literal: "12345678901234567890",
    })
    expect(scalarLiteral(" 1.50 ")).toEqual({ ok: true, literal: "1.50" })
    expect(scalarLiteral("-0.5e3")).toEqual({ ok: true, literal: "-0.5e3" })
  })
  test("the three words are themselves, and anything else is a string as typed", () => {
    expect(scalarLiteral("true")).toEqual({ ok: true, literal: "true" })
    expect(scalarLiteral("null")).toEqual({ ok: true, literal: "null" })
    expect(scalarLiteral("dark")).toEqual({ ok: true, literal: '"dark"' })
    expect(scalarLiteral("01")).toEqual({ ok: true, literal: '"01"' })
    expect(scalarLiteral('say "hi"')).toEqual({ ok: true, literal: '"say \\"hi\\""' })
    expect(scalarLiteral("")).toEqual({ ok: true, literal: '""' })
  })
  test("quotes make a string of what would be something else", () => {
    expect(scalarLiteral('"42"')).toEqual({ ok: true, literal: '"42"' })
    expect(scalarLiteral('"open').ok).toBe(false)
  })
  test("a whole object or array can be written in", () => {
    expect(scalarLiteral("{}")).toEqual({ ok: true, literal: "{}" })
    expect(scalarLiteral('["a", 1]')).toEqual({ ok: true, literal: '["a", 1]' })
    expect(scalarLiteral("[1,").ok).toBe(false)
  })
})

describe("what an editor opens on", () => {
  const open = (text) => scalarText(parseJsonDoc(text), text)
  test("a string is its bare content when that could be nothing else", () => {
    expect(open('"dark"')).toBe("dark")
    expect(open('"a b"')).toBe("a b")
  })
  test("and keeps its quotes when bare it would read as another kind", () => {
    expect(open('"42"')).toBe('"42"')
    expect(open('"null"')).toBe('"null"')
    expect(open('""')).toBe('""')
    expect(open('" padded"')).toBe('" padded"')
    expect(open('"{x}"')).toBe('"{x}"')
    expect(open('"two\\nlines"')).toBe('"two\\nlines"')
  })
  test("everything else is its own literal", () => {
    expect(open("9007199254740993")).toBe("9007199254740993")
    expect(open("false")).toBe("false")
    expect(open("null")).toBe("null")
  })
  test("a field opened and left alone stands for the value it held", () => {
    for (const text of ['"dark"', '"42"', '""', '" x"', "17", "true", "null", '"a\\"b"']) {
      const node = parseJsonDoc(text)
      const literal = scalarLiteral(scalarText(node, text))
      expect(literal.ok).toBe(true)
      expect(JSON.parse(literal.literal)).toEqual(JSON.parse(text))
    }
  })
})

describe("changing one node", () => {
  test("only that node's characters change", () => {
    const doc = parseJsonDoc(DOC)
    const next = replaceNode(DOC, doc.entries[1].value, "2")
    expect(next).toBe(DOC.replace('"b": 1', '"b": 2'))
    // The big integer and the key order are as they came.
    expect(next).toContain("9007199254740993")
    expect(parseJsonDoc(next).entries.map((entry) => entry.key)).toEqual(["id", "b", "10", "who"])
  })
  test("a value inside a nested object", () => {
    const who = parseJsonDoc(DOC).entries[3].value
    const next = replaceNode(DOC, who.entries[0].value, '"Bea"')
    expect(JSON.parse(next).who).toEqual({ name: "Bea" })
  })
})

describe("removing a child", () => {
  const without = (text, path, index) => {
    let parent = parseJsonDoc(text)
    for (const step of path) {
      parent = parent.kind === "object" ? parent.entries[step].value : parent.items[step]
    }
    return removeChild(text, parent, index)
  }
  test("the first goes with the comma after it", () => {
    expect(without('{"a": 1, "b": 2, "c": 3}', [], 0)).toBe('{"b": 2, "c": 3}')
  })
  test("one in the middle", () => {
    expect(without('{"a": 1, "b": 2, "c": 3}', [], 1)).toBe('{"a": 1, "c": 3}')
  })
  test("the last goes with the comma before it", () => {
    expect(without('{"a": 1, "b": 2, "c": 3}', [], 2)).toBe('{"a": 1, "b": 2}')
    expect(without("[1, 2, 3]", [], 2)).toBe("[1, 2]")
  })
  test("the only one leaves the container empty", () => {
    expect(without('{ "a": 1 }', [], 0)).toBe("{}")
    expect(without("[ [1] ]", [0], 0)).toBe("[ [] ]")
  })
  test("every result is still JSON, and nothing else moved", () => {
    for (let index = 0; index < 4; index++) {
      const next = without(DOC, [], index)
      const keys = ["id", "b", "10", "who"].filter((_, at) => at !== index)
      expect(parseJsonDoc(next).entries.map((entry) => entry.key)).toEqual(keys)
    }
    expect(without(DOC, [], 1)).toContain("9007199254740993")
  })
  test("an index that is not there changes nothing", () => {
    expect(without("[1]", [], 4)).toBe("[1]")
  })
})

describe("adding a child", () => {
  test("to an object, at the end, under its key", () => {
    const text = '{"a": 1}'
    expect(addChild(text, parseJsonDoc(text), "true", "b")).toEqual({
      ok: true,
      text: '{"a": 1, "b": true}',
    })
  })
  test("to an empty object and an empty array", () => {
    expect(addChild("{}", parseJsonDoc("{}"), '"x"', "a b")).toEqual({
      ok: true,
      text: '{"a b": "x"}',
    })
    expect(addChild("[ ]", parseJsonDoc("[ ]"), "1")).toEqual({ ok: true, text: "[1]" })
  })
  test("to an array", () => {
    expect(addChild("[1, 2]", parseJsonDoc("[1, 2]"), "3")).toEqual({ ok: true, text: "[1, 2, 3]" })
  })
  test("a key the object already has is refused", () => {
    const text = '{"a": 1}'
    expect(addChild(text, parseJsonDoc(text), "2", "a").ok).toBe(false)
    expect(addChild(text, parseJsonDoc(text), "2").ok).toBe(false)
  })
  test("a scalar holds nothing", () => {
    expect(addChild("1", parseJsonDoc("1"), "2").ok).toBe(false)
  })
})
