import { describe, expect, test } from "bun:test"
import { aggregate, formatDecimal, groupDigits, parseDecimal, sameNumber } from "./decimal"
import { compactJSON, indentJSON } from "./json-text"
import { columnKind, kindFromServer, kindFromType, numberSpec } from "./kinds"
import {
  CELL_TEXT_LIMIT,
  compareValues,
  DEFAULT_VALUE,
  editText,
  formatCell,
  holdsText,
  isDefault,
  isTruncatedValue,
  parseBinary,
  parseInput,
  parsePasted,
  readableTemporal,
  sameValue,
  searchText,
} from "./values"

const col = (kind, typeName = "", extra = {}) => ({ key: "c", name: "c", kind, typeName, ...extra })

describe("what a declared type is", () => {
  test("the common types of every engine land on the kind that draws and edits them", () => {
    const cases = {
      bigint: "number",
      INT8: "number",
      "numeric(12,2)": "number",
      "double precision": "number",
      "int unsigned": "number",
      UInt64: "number",
      text: "text",
      "character varying(255)": "text",
      "character(2)": "text",
      BPCHAR: "text",
      String: "text",
      boolean: "boolean",
      BOOL: "boolean",
      "tinyint(1)": "boolean",
      "timestamp with time zone": "datetime",
      TIMESTAMPTZ: "datetime",
      datetime: "datetime",
      "DateTime64(3)": "datetime",
      date: "date",
      "time without time zone": "time",
      jsonb: "json",
      JSON: "json",
      bytea: "binary",
      BLOB: "binary",
      "binary(16)": "binary",
      uuid: "uuid",
      uniqueidentifier: "uuid",
      "text[]": "array",
      _TEXT: "array",
      ARRAY: "array",
      "Array(String)": "array",
      "enum('a','b')": "enum",
      "Enum8('a' = 1)": "enum",
      interval: "text",
      point: "text",
      inet: "text",
      "USER-DEFINED": "unknown",
      17236: "unknown",
      "": "unknown",
    }
    for (const [type, kind] of Object.entries(cases)) {
      expect([type, kindFromType(type)]).toEqual([type, kind])
    }
  })

  test("a name that only contains a number's or a date's word is not one", () => {
    const cases = {
      int4range: "unknown",
      INT8RANGE: "unknown",
      numrange: "unknown",
      daterange: "unknown",
      tsrange: "unknown",
      tstzrange: "unknown",
      int4multirange: "unknown",
      datemultirange: "unknown",
      // An array of ranges is still an array.
      "int4range[]": "array",
      _DATERANGE: "array",
      // And the neighbours the range rule must not take with it.
      interval: "text",
      "INTERVAL DAY TO SECOND": "text",
      integer: "number",
      date: "date",
    }
    for (const [type, kind] of Object.entries(cases)) {
      expect([type, kindFromType(type)]).toEqual([type, kind])
    }
  })

  test("a string of bits is not a boolean, whatever its length; the server says when it is", () => {
    for (const type of ["bit", "BIT", "bit(1)", "bit(8)", "BIT(64)", "bit varying(16)", "VARBIT"]) {
      expect([type, kindFromType(type)]).toEqual([type, "unknown"])
    }
    // SQL Server's bit is its boolean, and only the server knows it is SQL Server.
    expect(columnKind({ typeName: "BIT", serverKind: "boolean" })).toBe("boolean")
    expect(columnKind({ typeName: "BIT", serverKind: "binary" })).toBe("binary")
    expect(kindFromType("tinyint(1)")).toBe("boolean")
    expect(kindFromType("boolean")).toBe("boolean")
  })

  test("a wrapper type is read through to what it wraps", () => {
    expect(kindFromType("Nullable(Int32)")).toBe("number")
    expect(kindFromType("LowCardinality(String)")).toBe("text")
  })

  test("enum labels make a column an enum whatever its type is called, and the server's kind beats the name", () => {
    expect(columnKind({ typeName: "USER-DEFINED", enumValues: ["free", "plus"] })).toBe("enum")
    expect(columnKind({ typeName: "17236", serverKind: "text" })).toBe("text")
    expect(columnKind({ typeName: "jsonb" })).toBe("json")
    expect(kindFromServer("integer")).toBe("number")
    expect(kindFromServer("other")).toBe("unknown")
    expect(kindFromServer(undefined)).toBeUndefined()
  })
})

describe("the bounds of a number column", () => {
  test("integer widths are read from the name, signed and unsigned", () => {
    expect(numberSpec("smallint")).toEqual({ class: "integer", min: -32768n, max: 32767n })
    expect(numberSpec("integer").max).toBe(2147483647n)
    expect(numberSpec("INT4").max).toBe(2147483647n)
    expect(numberSpec("bigint").max).toBe(9223372036854775807n)
    expect(numberSpec("INT8").min).toBe(-9223372036854775808n)
    expect(numberSpec("int unsigned")).toEqual({ class: "integer", min: 0n, max: 4294967295n })
    expect(numberSpec("bigint unsigned").max).toBe(18446744073709551615n)
    expect(numberSpec("tinyint(4)").max).toBe(127n)
  })

  test("the same letters in another case are another engine's width", () => {
    expect(numberSpec("Int8").max).toBe(127n)
    expect(numberSpec("UInt8")).toEqual({ class: "integer", min: 0n, max: 255n })
    expect(numberSpec("Nullable(Int64)").max).toBe(9223372036854775807n)
    expect(numberSpec("INTEGER").max).toBe(9223372036854775807n)
  })

  test("decimals carry precision and scale; floats carry neither", () => {
    expect(numberSpec("numeric(12,2)")).toEqual({ class: "decimal", precision: 12, scale: 2 })
    expect(numberSpec("decimal(5)")).toEqual({ class: "decimal", precision: 5, scale: 0 })
    expect(numberSpec("numeric")).toEqual({ class: "decimal" })
    expect(numberSpec("double precision")).toEqual({ class: "float" })
    expect(numberSpec("FLOAT4")).toEqual({ class: "float" })
  })

  test("money is an amount with no shape the grid can hold it to", () => {
    expect(numberSpec("money")).toEqual({ class: "money" })
    expect(numberSpec("MONEY")).toEqual({ class: "money" })
    expect(numberSpec("smallmoney")).toEqual({ class: "money" })
  })
})

describe("how a value is drawn", () => {
  test("NULL, the empty string and a pending default are three different things", () => {
    expect(formatCell(null, col("text"))).toEqual({ text: "NULL", tone: "null" })
    expect(formatCell("", col("text"))).toEqual({ text: '""', tone: "empty" })
    expect(formatCell(DEFAULT_VALUE, col("text"))).toEqual({ text: "DEFAULT", tone: "default" })
    expect(formatCell(undefined, col("text")).tone).toBe("default")
  })

  test("a number is shown digit for digit, however large", () => {
    expect(formatCell("9223372036854775807", col("number", "bigint")).text).toBe(
      "9223372036854775807",
    )
    expect(formatCell("12345678901234567890.1234567890", col("number", "numeric")).text).toBe(
      "12345678901234567890.1234567890",
    )
    expect(formatCell("4193.40", col("number", "numeric(12,2)")).text).toBe("4193.40")
    expect(formatCell(0.1, col("number", "double precision")).text).toBe("0.1")
  })

  test("a boolean is a word, whichever way the engine spelled it", () => {
    expect(formatCell(true, col("boolean")).text).toBe("true")
    expect(formatCell(false, col("boolean")).text).toBe("false")
    expect(formatCell("1", col("boolean"))).toEqual({ text: "true", tone: "value", title: "1" })
    expect(formatCell("maybe", col("boolean")).text).toBe("maybe")
  })

  test("a timestamp is readable, is not moved to another zone, and keeps the raw text for hover", () => {
    expect(formatCell("2026-10-01T05:45:15.124645Z", col("datetime"))).toEqual({
      text: "2026-10-01 05:45:15.124645 UTC",
      tone: "value",
      title: "2026-10-01T05:45:15.124645Z",
    })
    expect(readableTemporal("2026-10-01T05:45:15+02:00", "datetime")).toBe(
      "2026-10-01 05:45:15 +02:00",
    )
    expect(readableTemporal("2026-10-01T05:45:15.500000Z", "datetime")).toBe(
      "2026-10-01 05:45:15.5 UTC",
    )
    expect(readableTemporal("2026-10-01 05:45:15", "datetime")).toBe("2026-10-01 05:45:15")
  })

  test("a date and a time each show only their own part of what the driver sent", () => {
    expect(formatCell("2026-10-01T00:00:00Z", col("date")).text).toBe("2026-10-01")
    expect(formatCell("2026-10-01", col("date"))).toEqual({ text: "2026-10-01", tone: "value" })
    expect(formatCell("0000-01-01T13:05:09Z", col("time")).text).toBe("13:05:09 UTC")
    expect(formatCell("13:05:09.25", col("time")).text).toBe("13:05:09.25")
    expect(formatCell("yesterday", col("datetime")).text).toBe("yesterday")
  })

  test("binary is its size and the start of its bytes", () => {
    expect(formatCell("\\x89504e470d0a1a0a", col("binary"))).toEqual({
      text: "89 50 4e 47 0d 0a 1a 0a",
      lead: "8 B",
      tone: "value",
      clamped: false,
    })
    const preview = formatCell(`\\x${"ab".repeat(256)}… (4096 bytes)`, col("binary"))
    expect(preview.lead).toBe("4.0 KB")
    expect(preview.text.endsWith(" …")).toBe(true)
    expect(preview.clamped).toBe(true)
    expect(formatCell("\\x", col("binary"))).toMatchObject({ text: "", lead: "0 B" })
  })

  test("JSON is one compact line, whether it arrived as text or as a document", () => {
    expect(formatCell('{\n  "a": 1,\n  "b": [1, 2]\n}', col("json")).text).toBe(
      '{ "a": 1, "b": [1, 2] }',
    )
    expect(formatCell({ a: 1, b: [1, 2] }, col("json")).text).toBe('{"a":1,"b":[1,2]}')
    expect(formatCell(["x", "y"], col("array")).text).toBe('["x","y"]')
    expect(formatCell("{newsletter,vip}", col("array")).text).toBe("{newsletter,vip}")
  })

  test("long text is one clamped line, with line breaks drawn as a mark", () => {
    const long = formatCell("x".repeat(CELL_TEXT_LIMIT + 50), col("text"))
    expect(long.text).toHaveLength(CELL_TEXT_LIMIT + 1)
    expect(long.clamped).toBe(true)
    expect(formatCell("first\nsecond\r\nthird", col("text")).text).toBe("first↵second↵third")
    expect(formatCell("  padded  ", col("text")).text).toBe("  padded  ")
  })
})

describe("previews", () => {
  test("the server's hex form is read with or without the tail that marks a cut", () => {
    expect(parseBinary("\\x00ff")).toEqual({ hex: "00ff", size: 2, truncated: false })
    expect(parseBinary("\\x00FF… (4096 bytes)")).toEqual({
      hex: "00ff",
      size: 4096,
      truncated: true,
    })
    expect(parseBinary("hello")).toBeNull()
    expect(parseBinary("\\x0")).toBeNull()
  })

  test("only a cut value counts as truncated", () => {
    expect(isTruncatedValue("\\x00ff… (4096 bytes)")).toBe(true)
    expect(isTruncatedValue("\\x00ff")).toBe(false)
    expect(isTruncatedValue("text that ends… (12 bytes)")).toBe(false)
    expect(isTruncatedValue(null)).toBe(false)
  })
})

describe("the text an editor opens on", () => {
  test("NULL and default open empty; scalars open as themselves", () => {
    expect(editText(null, col("text"))).toBe("")
    expect(editText(DEFAULT_VALUE, col("number"))).toBe("")
    expect(editText("9223372036854775807", col("number"))).toBe("9223372036854775807")
    expect(editText(true, col("boolean"))).toBe("true")
    expect(editText(1.5, col("number"))).toBe("1.5")
  })

  test("a timestamp opens in the form every engine takes back", () => {
    expect(editText("2026-10-01T05:45:15.124645Z", col("datetime"))).toBe(
      "2026-10-01 05:45:15.124645+00:00",
    )
    expect(editText("2026-10-01T05:45:15", col("datetime"))).toBe("2026-10-01 05:45:15")
    expect(editText("2026-10-01T00:00:00Z", col("date"))).toBe("2026-10-01")
    expect(editText("0000-01-01T13:05:09Z", col("time"))).toBe("13:05:09+00:00")
  })

  test("a JSON document opens pretty-printed; text that is not JSON opens as it is", () => {
    expect(editText('{"a":1}', col("json"))).toBe('{\n  "a": 1\n}')
    expect(editText({ a: 1 }, col("json"))).toBe('{\n  "a": 1\n}')
    expect(editText('"scalar"', col("json"))).toBe('"scalar"')
    expect(editText("{broken", col("json"))).toBe("{broken")
  })

  test("opening a JSON document changes nothing in it but the whitespace", () => {
    const stored =
      '{"discord_id": 1234567890123456789, "plan": "free", "amount": 12345678901234567890.123456789}'
    const opened = editText(stored, col("json"))
    expect(opened).toBe(
      '{\n  "discord_id": 1234567890123456789,\n  "plan": "free",\n  "amount": 12345678901234567890.123456789\n}',
    )
    // Changing one field and staging the text leaves the others digit for digit.
    const staged = parseInput(opened.replace('"free"', '"pro"'), col("json"))
    expect(staged.value).toContain("1234567890123456789,")
    expect(staged.value).toContain("12345678901234567890.123456789")
  })

  test("keys that look like numbers keep their place, and a repeated key is not dropped", () => {
    expect(editText('{"b":1,"10":2,"2":3}', col("json"))).toBe(
      '{\n  "b": 1,\n  "10": 2,\n  "2": 3\n}',
    )
    expect(editText('{"a":1,"a":2}', col("json"))).toBe('{\n  "a": 1,\n  "a": 2\n}')
  })

  test("a document that already has a layout is opened in it", () => {
    const laid = '{\n\t"a": [1,\n\t2]\n}'
    expect(editText(laid, col("json"))).toBe(laid)
  })
})

describe("JSON as text", () => {
  test("indenting matches what JSON.stringify would lay out, for a document it can hold", () => {
    const documents = [
      '{"a":1,"b":[true,false,null],"c":{"d":"x","e":[]},"f":{}}',
      '[1,2,{"a":[{"b":[]}]}]',
      '"just a string"',
      "125",
      "[]",
      '{"quote":"she said \\"hi\\"","brace":"}{][,:","slash":"a\\\\"}',
    ]
    for (const text of documents) {
      expect(indentJSON(text)).toBe(JSON.stringify(JSON.parse(text), null, 2))
    }
  })

  test("a number keeps every digit and every spelling", () => {
    expect(indentJSON("[9007199254740993, 1.10, 1E5, -0]")).toBe(
      "[\n  9007199254740993,\n  1.10,\n  1E5,\n  -0\n]",
    )
    expect(compactJSON('{ "n" : 12345678901234567890 }')).toBe('{"n":12345678901234567890}')
  })

  test("a string is carried through untouched, escapes and all", () => {
    expect(compactJSON('{ "a" : "x  y\\u0041\\n" }')).toBe('{"a":"x  y\\u0041\\n"}')
  })

  test("text that is not JSON is neither indented nor compacted", () => {
    expect(indentJSON("{broken")).toBeNull()
    expect(compactJSON("{a,b}")).toBeNull()
    expect(indentJSON("")).toBeNull()
  })
})

describe("turning what was typed into a value", () => {
  test("text is taken exactly, spaces and emptiness included", () => {
    expect(parseInput("", col("text"))).toEqual({ ok: true, value: "" })
    expect(parseInput("  x ", col("text"))).toEqual({ ok: true, value: "  x " })
    expect(parseInput("", col("unknown"))).toEqual({ ok: true, value: "" })
  })

  test("an empty field is NULL for the other kinds, and refused where NULL is not allowed", () => {
    expect(parseInput("", col("number", "bigint", { nullable: true }))).toEqual({
      ok: true,
      value: null,
    })
    expect(parseInput("  ", col("number", "bigint")).ok).toBe(false)
    expect(parseInput("", col("boolean", "boolean")).ok).toBe(false)
  })

  test("an integer is checked against its width and kept as its digits", () => {
    const big = col("number", "bigint")
    expect(parseInput("9223372036854775807", big)).toEqual({
      ok: true,
      value: "9223372036854775807",
    })
    expect(parseInput("9223372036854775808", big).ok).toBe(false)
    expect(parseInput("-9223372036854775808", big)).toEqual({
      ok: true,
      value: "-9223372036854775808",
    })
    expect(parseInput("+42", big)).toEqual({ ok: true, value: "42" })
    expect(parseInput("007", big)).toEqual({ ok: true, value: "7" })
    expect(parseInput("1.5", big).ok).toBe(false)
    expect(parseInput("1e3", big).ok).toBe(false)
    expect(parseInput("32768", col("number", "smallint")).ok).toBe(false)
    expect(parseInput("-1", col("number", "int unsigned")).ok).toBe(false)
  })

  test("a decimal is checked against its precision and scale and never passes through a float", () => {
    const money = col("number", "numeric(12,2)")
    expect(parseInput("4193.40", money)).toEqual({ ok: true, value: "4193.40" })
    expect(parseInput("9999999999.99", money)).toEqual({ ok: true, value: "9999999999.99" })
    expect(parseInput("10000000000", money).ok).toBe(false)
    expect(parseInput("1.234", money).ok).toBe(false)
    expect(parseInput("1e3", money).ok).toBe(false)
    expect(parseInput("12345678901234567890.1234567890", col("number", "numeric"))).toEqual({
      ok: true,
      value: "12345678901234567890.1234567890",
    })
  })

  test("money takes plain digits, or the engine's own spelling as it was typed", () => {
    const money = col("number", "money", { nullable: true })
    expect(parseInput("1234.5", money)).toEqual({ ok: true, value: "1234.5" })
    expect(parseInput("+7", money)).toEqual({ ok: true, value: "7" })
    // What a Postgres money cell reads, in two locales, with one digit changed.
    expect(parseInput("$1,300.00", money)).toEqual({ ok: true, value: "$1,300.00" })
    expect(parseInput("-$5.00", money)).toEqual({ ok: true, value: "-$5.00" })
    expect(parseInput(" 1.234,56 € ", money)).toEqual({ ok: true, value: "1.234,56 €" })
    expect(parseInput("$", money).ok).toBe(false)
    expect(parseInput("lots", money).ok).toBe(false)
    expect(parseInput("", money)).toEqual({ ok: true, value: null })
    // The cell as it was read, put back, is not an edit; another spelling is one.
    expect(sameValue("$1,234.56", "$1,234.56", "number")).toBe(true)
    expect(sameValue("$1,300.00", "$1,234.56", "number")).toBe(false)
    // SQL Server prints money as plain digits, and those are still compared as numbers.
    expect(sameValue("1234.5", "1234.5000", "number")).toBe(true)
    // A decimal column is still held to its digits.
    expect(parseInput("$5", col("number", "numeric(10,2)")).ok).toBe(false)
  })

  test("a float takes an exponent and the specials, as the text that was typed", () => {
    const float = col("number", "double precision")
    expect(parseInput("1.5e-7", float)).toEqual({ ok: true, value: "1.5e-7" })
    expect(parseInput("NaN", float)).toEqual({ ok: true, value: "NaN" })
    expect(parseInput("-Infinity", float)).toEqual({ ok: true, value: "-Infinity" })
    expect(parseInput("1e999", float).ok).toBe(false)
    expect(parseInput("abc", float).ok).toBe(false)
  })

  test("a boolean takes the spellings people use", () => {
    for (const yes of ["true", "T", "1", "yes", "on"]) {
      expect(parseInput(yes, col("boolean"))).toEqual({ ok: true, value: true })
    }
    for (const no of ["false", "f", "0", "No", "off"]) {
      expect(parseInput(no, col("boolean"))).toEqual({ ok: true, value: false })
    }
    expect(parseInput("maybe", col("boolean")).ok).toBe(false)
  })

  test("an enum takes one of its labels, forgiving the case and nothing else", () => {
    const tier = col("enum", "customer_tier", { enumValues: ["free", "plus", "Pro"] })
    expect(parseInput("plus", tier)).toEqual({ ok: true, value: "plus" })
    expect(parseInput("pro", tier)).toEqual({ ok: true, value: "Pro" })
    expect(parseInput("gold", tier)).toEqual({ ok: false, error: "One of: free, plus, Pro" })
    expect(parseInput("anything", col("enum", "x"))).toEqual({ ok: true, value: "anything" })
  })

  test("dates and times are held to their shape", () => {
    expect(parseInput("2026-10-01", col("date")).ok).toBe(true)
    expect(parseInput("01/10/2026", col("date")).ok).toBe(false)
    expect(parseInput("13:05", col("time")).ok).toBe(true)
    expect(parseInput("13:05:09.25+02:00", col("time", "timetz")).ok).toBe(true)
    expect(parseInput("1pm", col("time")).ok).toBe(false)
    expect(parseInput("2026-10-01 05:45:15+00:00", col("datetime", "timestamptz"))).toEqual({
      ok: true,
      value: "2026-10-01 05:45:15+00:00",
    })
    expect(parseInput("2026-10-01T05:45:15Z", col("datetime")).ok).toBe(true)
    expect(parseInput("2026-10-01", col("datetime", "timestamp"))).toEqual({
      ok: false,
      error: "YYYY-MM-DD HH:MM:SS",
    })
  })

  test("JSON must parse, and is staged as the text that was typed", () => {
    expect(parseInput(' {"b": 1, "a": 2} ', col("json"))).toEqual({
      ok: true,
      value: '{"b": 1, "a": 2}',
    })
    expect(parseInput("{broken", col("json")).ok).toBe(false)
    expect(parseInput("null", col("json"))).toEqual({ ok: true, value: "null" })
  })

  test("a UUID and a run of bytes are normalised to the server's own spelling", () => {
    expect(parseInput("{AEE68060-3389-42D1-B87E-F29E8AACB145}", col("uuid"))).toEqual({
      ok: true,
      value: "aee68060-3389-42d1-b87e-f29e8aacb145",
    })
    expect(parseInput("not-a-uuid", col("uuid")).ok).toBe(false)
    expect(parseInput("0x00FF", col("binary"))).toEqual({ ok: true, value: "\\x00ff" })
    expect(parseInput("\\x00f", col("binary")).ok).toBe(false)
  })

  test("an array is the engine's own literal, staged as the text that was typed", () => {
    expect(parseInput("{a,b}", col("array", "text[]"))).toEqual({ ok: true, value: "{a,b}" })
    expect(parseInput("{9223372036854775807,1}", col("array", "_int8"))).toEqual({
      ok: true,
      value: "{9223372036854775807,1}",
    })
    // JSON is not that literal: bound as JSON text, an array column refuses it.
    expect(parseInput("[9223372036854775807, 1]", col("array", "bigint[]"))).toEqual({
      ok: false,
      error: "An array literal: {a,b}",
    })
    expect(parseInput('["a","b"]', col("array", "ARRAY")).ok).toBe(false)
    // Another engine's literal is its own business; it is never parsed here.
    expect(parseInput("[1, 2]", col("array", "Array(UInt64)"))).toEqual({
      ok: true,
      value: "[1, 2]",
    })
  })

  test("only free text has an empty string that is a value of its own", () => {
    expect(holdsText(col("text"))).toBe(true)
    expect(holdsText(col("unknown"))).toBe(true)
    expect(holdsText(col("json"))).toBe(false)
    expect(holdsText(col("number"))).toBe(false)
  })

  test("a pasted empty field is NULL wherever the column allows it, text included", () => {
    expect(parsePasted("", col("text", "text", { nullable: true }))).toEqual({
      ok: true,
      value: null,
    })
    expect(parsePasted("", col("text", "text"))).toEqual({ ok: true, value: "" })
    expect(parsePasted("7", col("number", "integer"))).toEqual({ ok: true, value: "7" })
  })
})

describe("whether an edit changed anything", () => {
  test("NULL equals only NULL, and the default marker only itself", () => {
    expect(sameValue(null, null, "text")).toBe(true)
    expect(sameValue(null, "", "text")).toBe(false)
    expect(sameValue("", null, "number")).toBe(false)
    expect(sameValue(DEFAULT_VALUE, { $default: true }, "text")).toBe(true)
    expect(sameValue(DEFAULT_VALUE, null, "text")).toBe(false)
    expect(isDefault({ $default: true, other: 1 })).toBe(false)
    expect(isDefault([])).toBe(false)
  })

  test("numbers are compared exactly, not as floats", () => {
    expect(sameValue("4193.4", "4193.40", "number")).toBe(true)
    expect(sameValue("+7", "7", "number")).toBe(true)
    expect(sameValue("9007199254740993", "9007199254740992", "number")).toBe(false)
    expect(sameValue("0.1", 0.1, "number")).toBe(true)
    expect(sameValue("1e3", 1000, "number")).toBe(true)
    expect(sameValue("NaN", "NaN", "number")).toBe(true)
    expect(sameNumber("12345678901234567890.10", "12345678901234567890.1")).toBe(true)
  })

  test("the other kinds are compared by what they mean", () => {
    expect(sameValue("t", true, "boolean")).toBe(true)
    expect(sameValue("0", true, "boolean")).toBe(false)
    expect(sameValue("2026-10-01 05:45:15+00:00", "2026-10-01T05:45:15Z", "datetime")).toBe(true)
    expect(sameValue("2026-10-01 05:45:15.500", "2026-10-01T05:45:15.5", "datetime")).toBe(true)
    expect(sameValue("2026-10-01 05:45:16+00:00", "2026-10-01T05:45:15Z", "datetime")).toBe(false)
    expect(sameValue("2026-10-01", "2026-10-01T00:00:00Z", "date")).toBe(true)
    expect(sameValue('{ "a": 1 }', '{"a":1}', "json")).toBe(true)
    expect(sameValue('{"a":1}', { a: 1 }, "json")).toBe(true)
    expect(sameValue('{"a":2}', '{"a":1}', "json")).toBe(false)
    // One digit past what a float can tell apart is still a different document.
    expect(sameValue('{"a":9007199254740993}', '{"a":9007199254740992}', "json")).toBe(false)
    expect(sameValue('{"a":1,"a":2}', '{"a":2}', "json")).toBe(false)
    expect(sameValue('{\n  "a": 9007199254740993\n}', '{"a":9007199254740993}', "json")).toBe(true)
    expect(
      sameValue(
        "AEE68060-3389-42d1-b87e-f29e8aacb145",
        "aee68060-3389-42d1-b87e-f29e8aacb145",
        "uuid",
      ),
    ).toBe(true)
    expect(sameValue("a", "A", "text")).toBe(false)
    expect(sameValue(" a", "a", "text")).toBe(false)
  })
})

describe("ordering and searching a result in memory", () => {
  test("numbers sort as numbers at any size, text as text, NULL after both", () => {
    const sorted = (values, kind) => [...values].sort((a, b) => compareValues(a, b, kind))
    expect(sorted(["10", "9", "9007199254740993", "9007199254740992", "-1"], "number")).toEqual([
      "-1",
      "9",
      "10",
      "9007199254740992",
      "9007199254740993",
    ])
    expect(sorted(["b", null, "a", "B"], "text")).toEqual(["B", "a", "b", null])
    expect(sorted([true, false, null], "boolean")).toEqual([false, true, null])
    expect(sorted(["1.50", 1.25, "1e1"], "number")).toEqual([1.25, "1.50", "1e1"])
  })

  test("a find looks at the value as written", () => {
    expect(searchText(null)).toBe("")
    expect(searchText({ city: "Berlin" })).toBe('{"city":"Berlin"}')
    expect(searchText("2026-10-01T05:45:15Z")).toBe("2026-10-01T05:45:15Z")
  })
})

describe("exact arithmetic for the status line", () => {
  test("a decimal is read and written back without loss", () => {
    expect(formatDecimal(parseDecimal("-0012.3400"), 4)).toBe("-12.3400")
    expect(formatDecimal(parseDecimal("-0012.3400"))).toBe("-12.34")
    expect(formatDecimal(parseDecimal(".5"))).toBe("0.5")
    expect(formatDecimal(parseDecimal("-0.0"))).toBe("0")
    expect(parseDecimal("1e3")).toBeNull()
    expect(parseDecimal("NaN")).toBeNull()
    expect(parseDecimal(1e21)).toBeNull()
  })

  test("a sum past 2^53 is still right to the last digit", () => {
    const result = aggregate(["9007199254740993", "9007199254740993", "0.10", "0.20", null, ""])
    expect(result).toEqual({
      count: 4,
      sum: "18014398509481986.3",
      avg: "4503599627370496.575",
      min: "0.1",
      max: "9007199254740993",
      exact: true,
    })
  })

  test("an average is rounded half away from zero at four more places than the sum", () => {
    expect(aggregate(["1", "2", "2"]).avg).toBe("1.6667")
    expect(aggregate(["-1", "-2", "-2"]).avg).toBe("-1.6667")
    expect(aggregate(["0.005", "0.005"]).avg).toBe("0.005")
  })

  test("a float with no exact digits makes the figures approximate, and says so", () => {
    const result = aggregate(["1.5", 1e21])
    expect(result.exact).toBe(false)
    expect(result.count).toBe(2)
    expect(result.min).toBe("1.5")
  })

  test("nothing numeric is no figures at all", () => {
    expect(aggregate([null, "", "abc"])).toBeNull()
    expect(aggregate([])).toBeNull()
  })

  test("thousands are grouped in the whole part only", () => {
    expect(groupDigits("18014398509481986.3")).toBe("18,014,398,509,481,986.3")
    expect(groupDigits("-1234.56789")).toBe("-1,234.56789")
    expect(groupDigits("999")).toBe("999")
    expect(groupDigits("1e21")).toBe("1e21")
  })
})
