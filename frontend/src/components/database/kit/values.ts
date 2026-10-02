import { kindFromType } from "@/components/database/grid/kinds"

/**
 * What kind of thing one value is, and how it reads on one line.
 *
 * A database hands back more kinds than JSON has: a 64-bit integer arrives as
 * a string so it keeps its digits, bytes arrive as the server's `\x…` preview,
 * a timestamp is an RFC 3339 string, and NULL is not the empty string. The
 * grid, the document views and the key browser all have to tell these apart
 * the same way, so the telling lives here and the drawing is `ValueText`.
 */

export type ValueKind =
  "null" | "empty" | "string" | "number" | "boolean" | "date" | "json" | "binary"

/** The server's preview of bytes that are not text: hex, then how many there were if it cut them. */
const BINARY = /^\\x((?:[0-9a-f]{2})*)(?:… \((\d+) bytes\))?$/i
const NUMERIC = /^[+-]?(?:\d+\.?\d*(?:e[+-]?\d+)?|\.\d+|nan|[+-]?inf(?:inity)?)$/i
/** A whole RFC 3339 moment and nothing after it. */
const INSTANT =
  /^\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}(?::\d{2}(?:\.\d+)?)?(?: ?(?:Z|[+-]\d{2}(?::?\d{2})?))?$/i
const BOOLEAN = /^(?:true|false|t|f)$/i

/**
 * The kind of a value. `type` is what the column holds — the engine's own
 * type name (`bigint`, `character varying(255)`, `BLOB`) or the server's word
 * for it (`integer`, `text`, `binary`, `datetime`) — and where it is given it
 * decides what a string is: digits in a `bigint` column are a number and the
 * same digits in a `text` column are text; `\x41` is a byte in a `bytea`
 * column and four characters in a `varchar` one.
 *
 * What a type name means is the grid's to say (`kindFromType`): a column is
 * read one way whether it is a cell of a grid or a field beside one, and a
 * type the grid learns to read is read here from the same line.
 *
 * With no type there is only the value to go on, so only what cannot be
 * mistaken is claimed: the server's byte preview, and a string that is a
 * moment from its first character to its last. A note that begins with a
 * date is a note.
 */
export function valueKind(value: unknown, type = ""): ValueKind {
  if (value === null || value === undefined) return "null"
  if (typeof value === "boolean") return "boolean"
  if (typeof value === "number" || typeof value === "bigint") return "number"
  if (typeof value === "object") return "json"
  const text = String(value)
  if (text === "") return "empty"
  if (!type.trim()) {
    if (BINARY.test(text)) return "binary"
    return INSTANT.test(text) ? "date" : "string"
  }
  // The column says what the text is; the text still has to look like it. A
  // number column that holds "n/a" is holding text.
  switch (kindFromType(type)) {
    case "json":
      return "json"
    case "binary":
      return BINARY.test(text) ? "binary" : "string"
    case "boolean":
      if (BOOLEAN.test(text)) return "boolean"
      // MySQL's tinyint(1) is its boolean, and arrives as 0 or 1.
      return NUMERIC.test(text) ? "number" : "string"
    case "number":
      return NUMERIC.test(text) ? "number" : "string"
    case "datetime":
    case "date":
    case "time":
      return /^\d/.test(text) ? "date" : "string"
    default:
      // Text, and what is drawn as the engine prints it: a uuid, an enum's
      // label, an array, a range.
      return "string"
  }
}

/** How many bytes a binary preview stands for. */
export function binarySize(preview: string): number {
  const match = BINARY.exec(preview)
  if (!match) return 0
  return match[2] ? Number(match[2]) : match[1].length / 2
}

export type ValuePreview = {
  /** What to draw, on one line. */
  text: string
  /** Whether the value is longer than what is drawn. */
  clipped: boolean
}

/**
 * A value as one line no longer than `clamp` characters. Line breaks become a
 * visible mark rather than breaking the row, and a cut is said with an
 * ellipsis, so a megabyte of text costs the page a hundred characters.
 */
export function valuePreview(value: unknown, kind: ValueKind, clamp = 120): ValuePreview {
  if (kind === "null") return { text: "NULL", clipped: false }
  if (kind === "empty") return { text: "empty", clipped: false }
  if (kind === "binary") {
    const hex = (BINARY.exec(String(value))?.[1] ?? "").toLowerCase()
    const shown = hex.slice(0, 16)
    return { text: `0x${shown}`, clipped: binarySize(String(value)) * 2 > shown.length }
  }
  const full = typeof value === "object" ? JSON.stringify(value) : String(value)
  const line = full.replace(/\r?\n/g, "↵")
  return line.length > clamp
    ? { text: `${line.slice(0, clamp)}…`, clipped: true }
    : { text: line, clipped: false }
}

/** A key that can follow a dot without quoting. */
const IDENTIFIER = /^[A-Za-z_$][\w$]*$/

/**
 * Where a value sits in a document, as the path a query or a script would
 * name it by: `$.user.addresses[0]["postal code"]`.
 */
export function jsonPath(segments: readonly (string | number)[]): string {
  return segments.reduce<string>((path, segment) => {
    if (typeof segment === "number") return `${path}[${segment}]`
    return IDENTIFIER.test(segment) ? `${path}.${segment}` : `${path}[${JSON.stringify(segment)}]`
  }, "$")
}

/** A container's size in its own word: "3 keys", "1 item". */
export function sizeWord(value: object): string {
  const n = Array.isArray(value) ? value.length : Object.keys(value).length
  const word = Array.isArray(value) ? "item" : "key"
  return `${n.toLocaleString()} ${word}${n === 1 ? "" : "s"}`
}
