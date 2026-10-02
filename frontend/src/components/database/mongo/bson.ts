/**
 * A document as MongoDB stores it, read from the canonical Extended JSON the
 * server sends and written back as the same.
 *
 * `JSON.parse` is not used anywhere on this path, and that is the point of
 * the file. It would round a 64-bit integer, drop the distinction between
 * `5` and `5.0`, and move a field named `"10"` in front of one named `"a"`.
 * The old editor did all three and then saved the result: opening a document
 * and pressing Save rewrote its types. Here the text is read token by token
 * into a tree that keeps every field in its stored order and every value
 * with the type it has, and the tree prints itself back as canonical Extended
 * JSON — so a document that is read and sent back unchanged is the same BSON.
 */

/* --------------------------------------------------------------- JSON text */

export type Json =
  | { k: "object"; entries: [string, Json][] }
  | { k: "array"; items: Json[] }
  | { k: "string"; value: string }
  /** The digits as written: never a JavaScript number. */
  | { k: "number"; raw: string }
  | { k: "bool"; value: boolean }
  | { k: "null" }

export class JsonSyntaxError extends Error {
  constructor(
    message: string,
    readonly offset: number,
    readonly line: number,
    readonly column: number,
  ) {
    super(`Line ${line}, column ${column}: ${message}`)
    this.name = "JsonSyntaxError"
  }
}

const NUMBER = /-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?/y
const SPACE = /[ \t\n\r]*/y

/** Strict JSON, keeping key order and number text. Throws `JsonSyntaxError`. */
export function parseJson(text: string): Json {
  let i = 0

  const fail = (message: string, at = i): never => {
    let line = 1
    let column = 1
    for (let p = 0; p < at && p < text.length; p++) {
      if (text[p] === "\n") {
        line++
        column = 1
      } else column++
    }
    throw new JsonSyntaxError(message, at, line, column)
  }
  const space = () => {
    SPACE.lastIndex = i
    SPACE.exec(text)
    i = SPACE.lastIndex
  }
  /** "unexpected '}' where a value was expected", or — at the end — "the text ends where …". */
  const unexpected = (rest: string) =>
    i >= text.length ? `the text ends ${rest}` : `unexpected '${text[i]}' ${rest}`

  const string = (): string => {
    const start = i
    i++
    while (i < text.length) {
      const char = text[i]
      if (char === "\\") i += 2
      else if (char === '"') {
        i++
        try {
          return JSON.parse(text.slice(start, i)) as string
        } catch {
          return fail("a string with an escape JSON does not have", start)
        }
      } else if (char === "\n") break
      else i++
    }
    return fail("a string that is never closed", start)
  }

  const value = (): Json => {
    space()
    const char = text[i]
    if (char === "{") {
      i++
      const entries: [string, Json][] = []
      space()
      if (text[i] === "}") {
        i++
        return { k: "object", entries }
      }
      for (;;) {
        space()
        if (text[i] !== '"') fail(unexpected("where a field name in quotes was expected"))
        const key = string()
        space()
        if (text[i] !== ":") fail(unexpected("where ':' was expected"))
        i++
        entries.push([key, value()])
        space()
        if (text[i] === ",") i++
        else if (text[i] === "}") {
          i++
          return { k: "object", entries }
        } else fail(unexpected("where ',' or '}' was expected"))
      }
    }
    if (char === "[") {
      i++
      const items: Json[] = []
      space()
      if (text[i] === "]") {
        i++
        return { k: "array", items }
      }
      for (;;) {
        items.push(value())
        space()
        if (text[i] === ",") i++
        else if (text[i] === "]") {
          i++
          return { k: "array", items }
        } else fail(unexpected("where ',' or ']' was expected"))
      }
    }
    if (char === '"') return { k: "string", value: string() }
    if (text.startsWith("true", i)) {
      i += 4
      return { k: "bool", value: true }
    }
    if (text.startsWith("false", i)) {
      i += 5
      return { k: "bool", value: false }
    }
    if (text.startsWith("null", i)) {
      i += 4
      return { k: "null" }
    }
    NUMBER.lastIndex = i
    const number = NUMBER.exec(text)
    if (number && number[0]) {
      i = NUMBER.lastIndex
      return { k: "number", raw: number[0] }
    }
    return fail(unexpected("where a value was expected"))
  }

  const read = value()
  space()
  if (i < text.length) fail(unexpected("after the end of the document"))
  return read
}

/** JSON text, on one line or indented by two spaces a level. */
export function printJson(json: Json, indent = false, depth = 0): string {
  const pad = indent ? `\n${"  ".repeat(depth + 1)}` : ""
  const close = indent ? `\n${"  ".repeat(depth)}` : ""
  const colon = indent ? ": " : ":"
  switch (json.k) {
    case "object":
      if (json.entries.length === 0) return "{}"
      return `{${json.entries
        .map(
          ([key, value]) =>
            `${pad}${JSON.stringify(key)}${colon}${printJson(value, indent && !isWrapper(value), depth + 1)}`,
        )
        .join(",")}${close}}`
    case "array":
      if (json.items.length === 0) return "[]"
      return `[${json.items
        .map((item) => `${pad}${printJson(item, indent && !isWrapper(item), depth + 1)}`)
        .join(",")}${close}]`
    case "string":
      return JSON.stringify(json.value)
    case "number":
      return json.raw
    case "bool":
      return json.value ? "true" : "false"
    case "null":
      return "null"
  }
}

/* -------------------------------------------------------------- BSON types */

export type BsonScalarType =
  | "string"
  | "int"
  | "long"
  | "double"
  | "decimal"
  | "bool"
  | "date"
  | "objectId"
  | "null"
  | "binData"
  | "timestamp"
  | "regex"
  | "javascript"
  | "symbol"
  | "dbPointer"
  | "minKey"
  | "maxKey"
  | "undefined"

export type BsonType = BsonScalarType | "object" | "array"

export type BsonField = { name: string; value: BsonNode }

export type BsonNode =
  | { type: "object"; fields: BsonField[] }
  | { type: "array"; items: BsonNode[] }
  | {
      type: BsonScalarType
      /** The value as a reader writes it: the digits, the ISO moment, the 24 hex digits. */
      text: string
      /** Its canonical Extended JSON, kept so that printing it changes nothing. */
      json: Json
    }

export type BsonScalar = Extract<BsonNode, { text: string }>

const single = (json: Json, key: string): Json | undefined =>
  json.k === "object" && json.entries.length === 1 && json.entries[0][0] === key
    ? json.entries[0][1]
    : undefined

const field = (json: Json, key: string): Json | undefined =>
  json.k === "object" ? json.entries.find(([name]) => name === key)?.[1] : undefined

const stringOf = (json: Json | undefined) => (json?.k === "string" ? json.value : undefined)

/** Whether a JSON object is one of Extended JSON's typed values rather than a document. */
function isWrapper(json: Json): boolean {
  return json.k === "object" && scalarOf(json) !== undefined
}

const INT32_MAX = BigInt("2147483647")
const INT32_MIN = BigInt("-2147483648")
const INT64_MAX = BigInt("9223372036854775807")
const INT64_MIN = BigInt("-9223372036854775808")

/** A moment as the ISO text a reader writes, from milliseconds since 1970 as digits. */
function isoFromMillis(digits: string): string {
  const ms = Number(digits)
  // Outside what a JavaScript date holds (±8.64e15 ms), the digits are all there is to show.
  if (!Number.isFinite(ms) || Math.abs(ms) > 8.64e15) return `${digits} ms`
  return new Date(ms).toISOString()
}

/** The size of base64 text in bytes. */
export function base64Bytes(text: string): number {
  const clean = text.replace(/=+$/, "")
  return Math.floor((clean.length * 3) / 4)
}

function hexOfBase64(text: string): string | undefined {
  try {
    return Array.from(atob(text), (char) => char.charCodeAt(0).toString(16).padStart(2, "0")).join(
      "",
    )
  } catch {
    return undefined
  }
}

function scalar(type: BsonScalarType, text: string, json: Json): BsonScalar {
  return { type, text, json }
}

/** The typed value a wrapper object spells, or `undefined` when it is an ordinary document. */
function scalarOf(json: Json): BsonScalar | undefined {
  if (json.k !== "object" || json.entries.length === 0 || json.entries.length > 2) return undefined
  const key = json.entries[0][0]
  if (key[0] !== "$") return undefined
  const lone = json.entries.length === 1 ? json.entries[0][1] : undefined
  const text = stringOf(lone)
  switch (key) {
    case "$oid":
      return text !== undefined ? scalar("objectId", text, json) : undefined
    case "$numberInt":
      return text !== undefined ? scalar("int", text, json) : undefined
    case "$numberLong":
      return text !== undefined ? scalar("long", text, json) : undefined
    case "$numberDouble":
      return text !== undefined ? scalar("double", doubleText(text), json) : undefined
    case "$numberDecimal":
      return text !== undefined ? scalar("decimal", text, json) : undefined
    case "$date": {
      if (!lone) return undefined
      if (text !== undefined) return scalar("date", text, json)
      const millis = stringOf(single(lone, "$numberLong"))
      return millis !== undefined ? scalar("date", isoFromMillis(millis), json) : undefined
    }
    case "$binary": {
      if (!lone) return undefined
      const base64 = stringOf(field(lone, "base64"))
      const subType = stringOf(field(lone, "subType"))
      if (base64 === undefined || subType === undefined) return undefined
      const hex = subType === "04" || subType === "4" ? hexOfBase64(base64) : undefined
      if (hex?.length === 32) {
        const uuid = `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`
        return scalar("binData", `UUID("${uuid}")`, json)
      }
      return scalar("binData", `BinData(${parseInt(subType, 16)}, "${base64}")`, json)
    }
    case "$timestamp": {
      if (!lone) return undefined
      const t = field(lone, "t")
      const i = field(lone, "i")
      if (t?.k !== "number" || i?.k !== "number") return undefined
      return scalar("timestamp", `Timestamp({ t: ${t.raw}, i: ${i.raw} })`, json)
    }
    case "$regularExpression": {
      if (!lone) return undefined
      const pattern = stringOf(field(lone, "pattern"))
      const options = stringOf(field(lone, "options"))
      if (pattern === undefined || options === undefined) return undefined
      return scalar("regex", `/${pattern.replace(/\//g, "\\/")}/${options}`, json)
    }
    case "$code": {
      const code = stringOf(json.entries[0][1])
      if (code === undefined) return undefined
      if (json.entries.length === 2 && json.entries[1][0] !== "$scope") return undefined
      return scalar("javascript", code, json)
    }
    case "$symbol":
      return text !== undefined ? scalar("symbol", text, json) : undefined
    case "$dbPointer": {
      if (!lone) return undefined
      const ref = stringOf(field(lone, "$ref"))
      const id = stringOf(single(field(lone, "$id") ?? { k: "null" }, "$oid"))
      return ref !== undefined && id !== undefined
        ? scalar("dbPointer", `DBPointer("${ref}", ObjectId("${id}"))`, json)
        : undefined
    }
    case "$minKey":
      return lone ? scalar("minKey", "MinKey()", json) : undefined
    case "$maxKey":
      return lone ? scalar("maxKey", "MaxKey()", json) : undefined
    case "$undefined":
      return lone ? scalar("undefined", "undefined", json) : undefined
    case "$uuid":
      return text !== undefined ? scalar("binData", `UUID("${text}")`, json) : undefined
    default:
      return undefined
  }
}

/** A double as a reader tells it from an integer: `5` is written `5.0`. */
function doubleText(raw: string): string {
  return /^-?\d+$/.test(raw) ? `${raw}.0` : raw
}

/** The tree of a JSON value read as Extended JSON. */
export function fromJson(json: Json): BsonNode {
  switch (json.k) {
    case "object": {
      const typed = scalarOf(json)
      if (typed) return typed
      return {
        type: "object",
        fields: json.entries.map(([name, value]) => ({ name, value: fromJson(value) })),
      }
    }
    case "array":
      return { type: "array", items: json.items.map(fromJson) }
    case "string":
      return scalar("string", json.value, json)
    case "bool":
      return scalar("bool", json.value ? "true" : "false", json)
    case "null":
      return scalar("null", "null", json)
    case "number": {
      // A bare number, which only relaxed text holds. The server reads it as
      // an int32 when it fits one, an int64 when it does not, and a double
      // when it has a fraction or an exponent.
      if (!/^-?\d+$/.test(json.raw)) return scalar("double", json.raw, json)
      const whole = BigInt(json.raw)
      return scalar(whole >= INT32_MIN && whole <= INT32_MAX ? "int" : "long", json.raw, json)
    }
  }
}

/** A document's tree from the canonical Extended JSON text the server sent. */
export function parseDocument(canonical: string): BsonNode {
  return fromJson(parseJson(canonical))
}

/** The tree as JSON again: canonical where it was read from canonical text. */
export function toJson(node: BsonNode): Json {
  if (node.type === "object") {
    return { k: "object", entries: node.fields.map((entry) => [entry.name, toJson(entry.value)]) }
  }
  if (node.type === "array") return { k: "array", items: node.items.map(toJson) }
  return node.json
}

/** Canonical Extended JSON: what a write sends. */
export function printCanonical(node: BsonNode, indent = false): string {
  return printJson(toJson(node), indent)
}

/* ---------------------------------------------------------- shell spelling */

const PLAIN_KEY = /^[A-Za-z_$][\w$]*$/

/** A field name as the shell writes it: bare where it can be. */
export function shellKey(name: string): string {
  return PLAIN_KEY.test(name) ? name : JSON.stringify(name)
}

/** One value as the shell writes it, which names its type: `Long("3")`, `ISODate("…")`. */
export function shellScalar(node: BsonScalar): string {
  switch (node.type) {
    case "string":
      return JSON.stringify(node.text)
    case "objectId":
      return `ObjectId("${node.text}")`
    case "long":
      return `Long("${node.text}")`
    case "decimal":
      return `Decimal128("${node.text}")`
    case "date":
      return `ISODate("${node.text}")`
    case "symbol":
      return `Symbol(${JSON.stringify(node.text)})`
    case "javascript":
      return `Code(${JSON.stringify(node.text)})`
    default:
      // int, double, bool, null and the types whose text is already the call.
      return node.text
  }
}

/** A whole value in the shell's spelling, on one line or indented. */
export function printShell(node: BsonNode, indent = false, depth = 0): string {
  const pad = indent ? `\n${"  ".repeat(depth + 1)}` : " "
  const close = indent ? `\n${"  ".repeat(depth)}` : " "
  if (node.type === "object") {
    if (node.fields.length === 0) return "{}"
    return `{${node.fields
      .map(
        (entry) => `${pad}${shellKey(entry.name)}: ${printShell(entry.value, indent, depth + 1)}`,
      )
      .join(",")}${close}}`
  }
  if (node.type === "array") {
    if (node.items.length === 0) return "[]"
    return `[${node.items.map((item) => `${pad}${printShell(item, indent, depth + 1)}`).join(",")}${close}]`
  }
  return shellScalar(node)
}

/* ------------------------------------------------------------------- words */

/** What each type is called where a reader chooses or reads one. */
export const TYPE_LABEL: Record<BsonType, string> = {
  string: "String",
  int: "Int32",
  long: "Int64",
  double: "Double",
  decimal: "Decimal128",
  bool: "Boolean",
  date: "Date",
  objectId: "ObjectId",
  null: "Null",
  object: "Object",
  array: "Array",
  binData: "Binary",
  timestamp: "Timestamp",
  regex: "Regular expression",
  javascript: "Code",
  symbol: "Symbol",
  dbPointer: "DBPointer",
  minKey: "MinKey",
  maxKey: "MaxKey",
  undefined: "Undefined",
}

/** "3 fields", "1 element": what a folded value holds. */
export function sizeWord(node: BsonNode): string {
  if (node.type === "object") {
    const n = node.fields.length
    return `${n.toLocaleString("en-US")} ${n === 1 ? "field" : "fields"}`
  }
  if (node.type === "array") {
    const n = node.items.length
    return `${n.toLocaleString("en-US")} ${n === 1 ? "element" : "elements"}`
  }
  return ""
}

/* ------------------------------------------------------------------- paths */

export type Segment = string | number

/** The value at a path, or `undefined` when the document has none there. */
export function nodeAt(root: BsonNode, path: readonly Segment[]): BsonNode | undefined {
  let node: BsonNode | undefined = root
  for (const segment of path) {
    if (!node) return undefined
    if (node.type === "object" && typeof segment === "string") {
      node = node.fields.find((entry) => entry.name === segment)?.value
    } else if (node.type === "array" && typeof segment === "number") {
      node = node.items[segment]
    } else return undefined
  }
  return node
}

/**
 * A path as an update or a filter names it: `address.city`, `roles.0`. Not
 * every field has such a name — one whose own name holds a dot, starts with
 * `$` or is empty cannot be addressed by a path, and is `null` here.
 */
export function dotted(path: readonly Segment[]): string | null {
  if (path.length === 0) return null
  for (const segment of path) {
    if (typeof segment === "number") continue
    if (segment === "" || segment.includes(".") || segment.startsWith("$")) return null
  }
  return path.join(".")
}

/* --------------------------------------------------------- typed by a person */

/** The types a reader can give a value by choosing one and typing its text. */
export const TYPED_INPUTS = [
  "string",
  "int",
  "long",
  "double",
  "decimal",
  "bool",
  "date",
  "objectId",
  "null",
] as const satisfies readonly BsonScalarType[]

export type TypedInput = (typeof TYPED_INPUTS)[number]

/** `"ejson"`: the value written out as Extended JSON, for every other type and for whole documents. */
export type InputType = TypedInput | "ejson"

export type Typed = { ok: true; node: BsonNode } | { ok: false; message: string }

const wrap = (key: string, text: string): Json => ({
  k: "object",
  entries: [[key, { k: "string", value: text }]],
})

const DECIMAL = /^[+-]?(?:(?:\d+\.?\d*|\.\d+)(?:[eE][+-]?\d+)?|Infinity|NaN)$/
const SPECIAL_DOUBLES = new Set(["NaN", "Infinity", "-Infinity"])

/**
 * A value of a chosen type from the text a reader typed. The type is chosen,
 * not guessed: `4` typed into an Int64 field stays an Int64, where sending the
 * bare digit would have made it an Int32.
 */
export function typedValue(type: InputType, input: string): Typed {
  const text = input.trim()
  const refuse = (message: string): Typed => ({ ok: false, message })
  switch (type) {
    case "string":
      return { ok: true, node: scalar("string", input, { k: "string", value: input }) }
    case "int": {
      if (!/^[+-]?\d+$/.test(text)) return refuse("A whole number.")
      const whole = BigInt(text)
      if (whole < INT32_MIN || whole > INT32_MAX) {
        return refuse("Outside what an Int32 holds (±2,147,483,647). Choose Int64.")
      }
      return {
        ok: true,
        node: scalar("int", whole.toString(), wrap("$numberInt", whole.toString())),
      }
    }
    case "long": {
      if (!/^[+-]?\d+$/.test(text)) return refuse("A whole number.")
      const whole = BigInt(text)
      if (whole < INT64_MIN || whole > INT64_MAX) return refuse("Outside what an Int64 holds.")
      return {
        ok: true,
        node: scalar("long", whole.toString(), wrap("$numberLong", whole.toString())),
      }
    }
    case "double": {
      if (SPECIAL_DOUBLES.has(text)) {
        return { ok: true, node: scalar("double", text, wrap("$numberDouble", text)) }
      }
      if (!/^[+-]?(?:\d+\.?\d*|\.\d+)(?:[eE][+-]?\d+)?$/.test(text)) {
        return refuse("A number, NaN, Infinity or -Infinity.")
      }
      const spelled = String(Number(text))
      if (!Number.isFinite(Number(text))) return refuse("Too large for a Double.")
      // Spelled as the server spells one: a whole double carries its ".0".
      const canonical = doubleText(spelled)
      return { ok: true, node: scalar("double", canonical, wrap("$numberDouble", canonical)) }
    }
    case "decimal": {
      if (!DECIMAL.test(text)) return refuse("A decimal number, such as 19.99.")
      const digits = text.replace(/^\+/, "")
      return { ok: true, node: scalar("decimal", digits, wrap("$numberDecimal", digits)) }
    }
    case "bool":
      if (text !== "true" && text !== "false") return refuse("true or false.")
      return {
        ok: true,
        node: scalar("bool", text, { k: "bool", value: text === "true" }),
      }
    case "date": {
      // A date with no zone is UTC, as the server reads one.
      const zoned =
        /(?:Z|[+-]\d{2}:?\d{2})$/i.test(text) || !/T|\d:\d/.test(text) ? text : `${text}Z`
      const ms = Date.parse(zoned)
      if (!text || Number.isNaN(ms)) return refuse("A moment, such as 2026-05-01T12:00:00Z.")
      return {
        ok: true,
        node: scalar("date", new Date(ms).toISOString(), {
          k: "object",
          entries: [["$date", wrap("$numberLong", String(ms))]],
        }),
      }
    }
    case "objectId":
      if (!/^[0-9a-fA-F]{24}$/.test(text)) return refuse("24 hexadecimal digits.")
      return {
        ok: true,
        node: scalar("objectId", text.toLowerCase(), wrap("$oid", text.toLowerCase())),
      }
    case "null":
      return { ok: true, node: scalar("null", "null", { k: "null" }) }
    case "ejson":
      try {
        return { ok: true, node: fromJson(parseJson(input)) }
      } catch (err) {
        return refuse(err instanceof Error ? `${err.message}.` : "Not Extended JSON.")
      }
  }
}

/** The input type a value opens its editor on, and the text it opens with. */
export function inputOf(node: BsonNode): { type: InputType; text: string } {
  if (node.type === "object" || node.type === "array") {
    return { type: "ejson", text: printCanonical(node, true) }
  }
  if ((TYPED_INPUTS as readonly string[]).includes(node.type)) {
    return { type: node.type as TypedInput, text: node.type === "null" ? "" : node.text }
  }
  return { type: "ejson", text: printJson(node.json) }
}

/**
 * Whether two values are the same value: the same canonical text, or two
 * doubles that are the same number however each is spelled (`5.0`, `5`,
 * `5E+0`).
 */
export function sameValue(a: BsonNode, b: BsonNode): boolean {
  if (a.type === "double" && b.type === "double") {
    const [x, y] = [Number(a.text), Number(b.text)]
    return x === y || (Number.isNaN(x) && Number.isNaN(y))
  }
  return printCanonical(a) === printCanonical(b)
}

/* ------------------------------------------------------------------ the id */

/** A document's `_id` as the shell writes it, from the canonical text the server names it by. */
export function idLabel(id: string): string {
  if (!id) return "no _id"
  try {
    return printShell(fromJson(parseJson(id)))
  } catch {
    return id
  }
}

/* ------------------------------------------------------------------- cells */

/**
 * One value as a cell of a table holds it: a string or a boolean as itself,
 * a number as its digits (an Int64 past 2^53 is not rounded on the way), a
 * moment as ISO text, a document or a list as the JSON it reads as. `kind`
 * says which of those the column is drawn as.
 */
export type CellKind =
  "text" | "number" | "boolean" | "datetime" | "uuid" | "json" | "array" | "unknown"

export function cellOf(node: BsonNode): { value: unknown; kind: CellKind } {
  switch (node.type) {
    case "object":
      return { value: relaxedOf(node), kind: "json" }
    case "array":
      return { value: relaxedOf(node), kind: "array" }
    case "string":
      return { value: node.text, kind: "text" }
    case "int":
    case "long":
    case "double":
    case "decimal":
      return { value: node.text, kind: "number" }
    case "bool":
      return { value: node.text === "true", kind: "boolean" }
    case "date":
      return { value: node.text, kind: "datetime" }
    case "objectId":
      return { value: node.text, kind: "uuid" }
    case "null":
      return { value: null, kind: "unknown" }
    default:
      return { value: shellScalar(node), kind: "unknown" }
  }
}

/** A nested value as plain data for a cell's one-line preview: typed values as the shell spells them. */
function relaxedOf(node: BsonNode): unknown {
  if (node.type === "object") {
    return Object.fromEntries(node.fields.map((entry) => [entry.name, relaxedOf(entry.value)]))
  }
  if (node.type === "array") return node.items.map(relaxedOf)
  if (node.type === "string") return node.text
  if (node.type === "bool") return node.text === "true"
  if (node.type === "null") return null
  if (node.type === "int") return Number(node.text)
  return shellScalar(node)
}
