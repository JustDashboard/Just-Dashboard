import { bytes } from "@/lib/format"
import { parseDecimal, compareDecimal, sameNumber } from "./decimal"
import { compactJSON, indentJSON } from "./json-text"
import { numberSpec } from "./kinds"
import type { CellValue, GridColumn, GridColumnKind } from "./types"

/**
 * How a value is read, typed and compared, per kind of column.
 *
 * Everything here is text in, text out. The grid never turns a 64-bit integer
 * or a decimal into a JavaScript number, never moves a timestamp into the
 * browser's time zone, and never re-serialises a JSON document it was only
 * asked to show: what the reader sees is what the engine stored, and what is
 * staged is what the reader typed.
 */

/** "Use the column's default", as a value. The same object the changes route takes. */
export const DEFAULT_VALUE = Object.freeze({ $default: true }) as { readonly $default: true }

/** A value on its way into the change set: a wire value, or the default marker. */
export type EditValue = CellValue | typeof DEFAULT_VALUE

export function isDefault(value: unknown): value is typeof DEFAULT_VALUE {
  return (
    typeof value === "object" &&
    value !== null &&
    !Array.isArray(value) &&
    (value as { $default?: unknown }).$default === true &&
    Object.keys(value).length === 1
  )
}

/* ------------------------------------------------------------------ binary */

const BINARY = /^\\x((?:[0-9a-fA-F]{2})*)(?:… \((\d+) bytes\))?$/

export interface BinaryPreview {
  /** Lowercase hex of the bytes that were sent. */
  hex: string
  /** The whole value's size. */
  size: number
  /** True when `hex` is only the beginning. */
  truncated: boolean
}

/** Reads the server's `\x…` form, including the `… (N bytes)` tail of a preview. */
export function parseBinary(value: string): BinaryPreview | null {
  const match = BINARY.exec(value)
  if (!match) return null
  const hex = match[1].toLowerCase()
  const sent = hex.length / 2
  if (match[2] === undefined) return { hex, size: sent, truncated: false }
  return { hex, size: Number(match[2]), truncated: true }
}

/** Whether a value is a preview the server cut. Such a cell is never written back. */
export function isTruncatedValue(value: CellValue): boolean {
  return typeof value === "string" && value.startsWith("\\x") && /… \(\d+ bytes\)$/.test(value)
}

/* --------------------------------------------------------------- date/time */

interface Instant {
  date: string
  time: string
  /** Fractional digits with trailing zeros removed. */
  fraction: string
  /** "" when the value names no zone, "Z" for UTC, otherwise ±HH:MM. */
  zone: string
}

const DATE_TIME =
  /^(\d{4,}-\d{2}-\d{2})(?:[T ](\d{2}:\d{2})(?::(\d{2})(?:[.,](\d+))?)?)?\s*(Z|z|[+-]\d{2}(?::?\d{2})?(?::?\d{2})?)?$/
const TIME_ONLY = /^(\d{2}:\d{2})(?::(\d{2})(?:[.,](\d+))?)?\s*(Z|z|[+-]\d{2}(?::?\d{2})?)?$/

function canonicalZone(zone: string | undefined): string {
  if (!zone) return ""
  if (zone === "Z" || zone === "z") return "Z"
  const sign = zone[0]
  const digits = zone.slice(1).replace(/:/g, "")
  const hours = digits.slice(0, 2)
  const minutes = digits.slice(2, 4) || "00"
  if (hours === "00" && minutes === "00") return "Z"
  return `${sign}${hours}:${minutes}`
}

function parseInstant(text: string): Instant | null {
  const trimmed = text.trim()
  const full = DATE_TIME.exec(trimmed)
  if (full) {
    return {
      date: full[1],
      time: full[2] ? `${full[2]}:${full[3] ?? "00"}` : "",
      fraction: (full[4] ?? "").replace(/0+$/, ""),
      zone: canonicalZone(full[5]),
    }
  }
  const time = TIME_ONLY.exec(trimmed)
  if (time) {
    return {
      date: "",
      time: `${time[1]}:${time[2] ?? "00"}`,
      fraction: (time[3] ?? "").replace(/0+$/, ""),
      zone: canonicalZone(time[4]),
    }
  }
  return null
}

function instantKey(instant: Instant, kind: GridColumnKind): string {
  const clock = instant.time
    ? `${instant.time}${instant.fraction ? `.${instant.fraction}` : ""}`
    : ""
  if (kind === "date") return instant.date
  if (kind === "time") return `${clock || "00:00:00"}${instant.zone}`
  return `${instant.date}T${clock || "00:00:00"}${instant.zone}`
}

/**
 * A timestamp as a person reads it: the `T` gone, the fraction cut to what is
 * there, the zone spelled. The instant is not moved — a value stored in UTC is
 * shown in UTC, with the raw text one hover away.
 */
export function readableTemporal(value: string, kind: GridColumnKind): string {
  const instant = parseInstant(value)
  if (!instant) return value
  const zone = instant.zone === "Z" ? " UTC" : instant.zone ? ` ${instant.zone}` : ""
  const fraction = instant.fraction ? `.${instant.fraction.slice(0, 6)}` : ""
  if (kind === "date") return instant.date || value
  if (kind === "time") return instant.time ? `${instant.time}${fraction}${zone}` : value
  if (!instant.date) return `${instant.time}${fraction}${zone}`
  if (!instant.time) return instant.date
  return `${instant.date} ${instant.time}${fraction}${zone}`
}

/** The text an editor opens on: the same instant, in the form the engines all accept. */
function editableTemporal(value: string, kind: GridColumnKind): string {
  const instant = parseInstant(value)
  if (!instant) return value
  const fraction = instant.fraction ? `.${instant.fraction}` : ""
  const zone = instant.zone === "Z" ? "+00:00" : instant.zone
  if (kind === "date") return instant.date || value
  if (kind === "time") return instant.time ? `${instant.time}${fraction}${zone}` : value
  if (!instant.date || !instant.time) return value
  return `${instant.date} ${instant.time}${fraction}${zone}`
}

/** What a date or time editor says under the field. */
export function temporalHint(kind: GridColumnKind, typeName: string): string {
  const zoned = /tz|with time zone|offset/i.test(typeName)
  if (kind === "date") return "YYYY-MM-DD"
  if (kind === "time") return zoned ? "HH:MM:SS+00:00" : "HH:MM:SS"
  return zoned ? "YYYY-MM-DD HH:MM:SS+00:00" : "YYYY-MM-DD HH:MM:SS"
}

/* ----------------------------------------------------------------- display */

export type CellTone = "value" | "null" | "empty" | "default"

export interface CellDisplay {
  /** One line, already clamped: what the cell draws. */
  text: string
  /** Why the text is not a value: NULL, the empty string, or a pending default. */
  tone: CellTone
  /** The raw value, where the text is a reading of it rather than the value itself. */
  title?: string
  /** A quiet fact drawn before the text: a binary value's size. */
  lead?: string
  /** True when the text is only the start of the value. */
  clamped?: boolean
}

/**
 * The most a cell ever puts in the DOM. A megabyte of JSON in one cell is a
 * megabyte in every frame the row is drawn; the cell shows the beginning and
 * the row detail shows the rest.
 */
export const CELL_TEXT_LIMIT = 240

function clamp(text: string): { text: string; clamped: boolean } {
  // A cell is one line: a line break inside a value is drawn as the mark for one.
  const oneLine = text.includes("\n") || text.includes("\r") ? text.replace(/\r?\n|\r/g, "↵") : text
  if (oneLine.length <= CELL_TEXT_LIMIT) return { text: oneLine, clamped: false }
  return { text: `${oneLine.slice(0, CELL_TEXT_LIMIT)}…`, clamped: true }
}

function jsonText(value: CellValue): string {
  return typeof value === "string" ? value : JSON.stringify(value)
}

/** How one value is drawn in its cell. */
export function formatCell(value: EditValue | undefined, column: GridColumn): CellDisplay {
  if (value === undefined || isDefault(value)) return { text: "DEFAULT", tone: "default" }
  if (value === null) return { text: "NULL", tone: "null" }
  if (value === "") return { text: '""', tone: "empty" }

  switch (column.kind) {
    case "boolean": {
      if (typeof value === "boolean") return { text: value ? "true" : "false", tone: "value" }
      const read = readBoolean(String(value))
      if (read === null) return { ...clamp(String(value)), tone: "value" }
      return { text: read ? "true" : "false", tone: "value", title: String(value) }
    }
    case "datetime":
    case "date":
    case "time": {
      if (typeof value !== "string") return { ...clamp(jsonText(value)), tone: "value" }
      const text = readableTemporal(value, column.kind)
      return { text, tone: "value", title: text === value ? undefined : value }
    }
    case "binary": {
      if (typeof value !== "string") return { ...clamp(jsonText(value)), tone: "value" }
      const binary = parseBinary(value)
      if (!binary) return { ...clamp(value), tone: "value" }
      const shown = binary.hex.slice(0, 32).replace(/(..)/g, "$1 ").trim()
      const more = binary.truncated || binary.hex.length > 32
      return {
        text: binary.size === 0 ? "" : `${shown}${more ? " …" : ""}`,
        lead: binary.size === 0 ? "0 B" : bytes(binary.size),
        tone: "value",
        clamped: more,
      }
    }
    case "json":
    case "array": {
      // The compact form: a stored document's own line breaks and indentation
      // are noise at one line's height.
      const raw = jsonText(value)
      const compact = typeof value === "string" ? raw.replace(/\s*\n\s*/g, " ") : raw
      return { ...clamp(compact), tone: "value" }
    }
    default: {
      if (typeof value === "object") return { ...clamp(JSON.stringify(value)), tone: "value" }
      return { ...clamp(String(value)), tone: "value" }
    }
  }
}

/* ------------------------------------------------------------------ search */

/** The text a find-in-results looks through: the value as written, not as drawn. */
export function searchText(value: EditValue | undefined): string {
  if (value === null || value === undefined || isDefault(value)) return ""
  if (typeof value === "object") return JSON.stringify(value)
  return String(value)
}

/* ----------------------------------------------------------------- editing */

/** The text an editor opens on. NULL and DEFAULT open empty. */
export function editText(value: EditValue | undefined, column: GridColumn): string {
  if (value === null || value === undefined || isDefault(value)) return ""
  if (typeof value === "object") return JSON.stringify(value, null, 2)
  if (typeof value === "boolean") return value ? "true" : "false"
  if (typeof value === "number") return String(value)
  switch (column.kind) {
    case "datetime":
    case "date":
    case "time":
      return editableTemporal(value, column.kind)
    case "json":
      // A document with a layout of its own is opened in it. One that arrived
      // on a single line is indented for reading — by moving whitespace alone,
      // so a number too large for a float and the order of the keys are still
      // what the engine stored when the reader changes something else.
      return value.includes("\n") ? value : (indentJSON(value) ?? value)
    default:
      return value
  }
}

export type ParseResult = { ok: true; value: CellValue } | { ok: false; error: string }

const fail = (error: string): ParseResult => ({ ok: false, error })

function readBoolean(text: string): boolean | null {
  if (/^(true|t|1|yes|y|on)$/i.test(text.trim())) return true
  if (/^(false|f|0|no|n|off)$/i.test(text.trim())) return false
  return null
}

const UUID = /^\{?[0-9a-fA-F]{8}-?(?:[0-9a-fA-F]{4}-?){3}[0-9a-fA-F]{12}\}?$/
const INTEGER = /^[+-]?\d+$/
const DECIMAL = /^[+-]?(?:\d+\.?\d*|\.\d+)$/
const FLOAT = /^[+-]?(?:\d+\.?\d*|\.\d+)(?:[eE][+-]?\d+)?$/
const FLOAT_SPECIAL = /^(?:NaN|[+-]?Infinity)$/i

function parseNumber(text: string, column: GridColumn): ParseResult {
  const spec = numberSpec(column.typeName)
  const plain = text.replace(/^\+/, "")
  if (spec.class === "integer") {
    if (!INTEGER.test(text)) return fail("Whole numbers only")
    const value = BigInt(plain)
    if (value < spec.min || value > spec.max) {
      return fail(`Out of range for ${column.typeName}: ${spec.min} to ${spec.max}`)
    }
    return { ok: true, value: value.toString() }
  }
  if (spec.class === "money") {
    // Plain digits are an amount everywhere. Anything else is taken as typed —
    // the cell itself may read `$1,234.56`, and a reader who changes one digit
    // of that must be able to stage it — and the engine has the last word.
    if (DECIMAL.test(text)) return { ok: true, value: plain }
    return /\d/.test(text) ? { ok: true, value: text } : fail("An amount")
  }
  if (spec.class === "float") {
    if (FLOAT_SPECIAL.test(text)) return { ok: true, value: plain }
    if (!FLOAT.test(text)) return fail("Not a number")
    if (!Number.isFinite(Number(text))) return fail("Outside the range of a float")
    return { ok: true, value: plain }
  }
  if (!DECIMAL.test(text)) return fail("Not a number")
  if (spec.precision !== undefined) {
    const scale = spec.scale ?? 0
    const [whole, fraction = ""] = plain.replace(/^-/, "").split(".")
    const digits = whole.replace(/^0+/, "")
    if (fraction.length > scale) {
      return fail(scale === 0 ? "Whole numbers only" : `At most ${scale} decimal places`)
    }
    if (digits.length > spec.precision - scale) {
      return fail(`At most ${spec.precision - scale} digits before the point`)
    }
  }
  return { ok: true, value: plain }
}

/** Postgres spells an array type `text[]` when declared and `_text` on the wire. */
function isPostgresArray(typeName: string): boolean {
  const type = typeName.trim().toLowerCase()
  return type.endsWith("[]") || type.startsWith("_") || type === "array"
}

/**
 * Whether a column holds free text, where the empty string is a value of its
 * own. Everywhere else an empty field can only mean NULL.
 */
export function holdsText(column: Pick<GridColumn, "kind">): boolean {
  return column.kind === "text" || column.kind === "unknown"
}

/**
 * Turns what was typed into what is staged, or says why it cannot be.
 *
 * An empty field means NULL for every kind but text — where the empty string is
 * a value of its own and NULL has to be asked for by name — and is refused
 * where the column cannot hold NULL.
 */
export function parseInput(text: string, column: GridColumn): ParseResult {
  const kind = column.kind
  if (holdsText(column)) return { ok: true, value: text }

  const trimmed = text.trim()
  if (trimmed === "") {
    if (column.nullable) return { ok: true, value: null }
    return fail("This column cannot be empty")
  }

  switch (kind) {
    case "number":
      return parseNumber(trimmed, column)
    case "boolean": {
      const value = readBoolean(trimmed)
      return value === null ? fail("true or false") : { ok: true, value }
    }
    case "enum": {
      const labels = column.enumValues
      if (!labels || labels.length === 0 || labels.includes(text)) return { ok: true, value: text }
      const match = labels.find((label) => label.toLowerCase() === trimmed.toLowerCase())
      return match === undefined ? fail(`One of: ${labels.join(", ")}`) : { ok: true, value: match }
    }
    case "uuid":
      if (!UUID.test(trimmed)) return fail("Not a UUID")
      return { ok: true, value: trimmed.replace(/[{}]/g, "").toLowerCase() }
    case "date":
      if (!/^\d{4,}-\d{2}-\d{2}$/.test(trimmed)) return fail("YYYY-MM-DD")
      return { ok: true, value: trimmed }
    case "time":
      if (!TIME_ONLY.test(trimmed)) return fail(temporalHint(kind, column.typeName))
      return { ok: true, value: trimmed }
    case "datetime": {
      const instant = DATE_TIME.exec(trimmed)
      if (!instant || !instant[2]) return fail(temporalHint(kind, column.typeName))
      return { ok: true, value: trimmed }
    }
    case "json":
      try {
        JSON.parse(trimmed)
      } catch (err) {
        return fail(err instanceof Error ? err.message : "Not valid JSON")
      }
      // Staged as the text that was typed: sending a parsed object would have
      // the server re-serialise it and lose the key order a `json` column keeps.
      return { ok: true, value: trimmed }
    case "binary": {
      const hex = trimmed.replace(/^(\\x|0x)/i, "")
      if (!/^(?:[0-9a-fA-F]{2})*$/.test(hex)) return fail("Hex digits in pairs, after \\x")
      return { ok: true, value: `\\x${hex.toLowerCase()}` }
    }
    case "array":
      // The engine's own literal, as typed. JSON is not taken for one: the
      // changes route binds a JSON array as JSON text, which an array column
      // refuses, and reading it here would put its numbers through a float.
      if (isPostgresArray(column.typeName) && !/^\{[\s\S]*\}$/.test(trimmed)) {
        return fail("An array literal: {a,b}")
      }
      return { ok: true, value: text }
  }
  return { ok: true, value: text }
}

/**
 * What a pasted field becomes in a column. The same reading as typing it,
 * except that an empty field in a nullable text column is NULL: a spreadsheet
 * has no other way to say it.
 */
export function parsePasted(text: string, column: GridColumn): ParseResult {
  if (text === "" && column.nullable) return { ok: true, value: null }
  return parseInput(text, column)
}

/* -------------------------------------------------------------- comparison */

/** A document as the text two spellings of it are compared by: its tokens, untouched. */
function canonicalJSON(value: CellValue): string | null {
  return compactJSON(typeof value === "string" ? value : JSON.stringify(value))
}

/**
 * Whether an edit put back what was already there.
 *
 * Compared by meaning for the kinds that have more than one spelling of the
 * same value — `4193.4` for `4193.40`, `t` for `true`, a timestamp typed with a
 * space for one stored with a `T`, a JSON document with different whitespace —
 * because an UPDATE that sets a column to the value it already holds is noise
 * in the review and a lock on the row.
 */
export function sameValue(a: EditValue, b: EditValue, kind: GridColumnKind): boolean {
  if (isDefault(a) || isDefault(b)) return isDefault(a) && isDefault(b)
  if (a === null || b === null) return a === b
  if (a === b) return true
  switch (kind) {
    case "number":
      if (typeof a === "object" || typeof b === "object") return false
      if (typeof a === "boolean" || typeof b === "boolean") return false
      return sameNumber(a, b)
    case "boolean": {
      const x = typeof a === "boolean" ? a : readBoolean(String(a))
      const y = typeof b === "boolean" ? b : readBoolean(String(b))
      return x !== null && x === y
    }
    case "datetime":
    case "date":
    case "time": {
      if (typeof a !== "string" || typeof b !== "string") return false
      const x = parseInstant(a)
      const y = parseInstant(b)
      return x !== null && y !== null && instantKey(x, kind) === instantKey(y, kind)
    }
    case "json":
    case "array": {
      const x = canonicalJSON(a)
      const y = canonicalJSON(b)
      return x !== null && x === y
    }
    case "uuid":
      return String(a).toLowerCase() === String(b).toLowerCase()
    default:
      if (typeof a === "object" || typeof b === "object") {
        return JSON.stringify(a) === JSON.stringify(b)
      }
      return false
  }
}

/**
 * Order for a client-side sort of a result already in memory. NULL sorts after
 * every value in either direction, as it reads in a spreadsheet.
 */
export function compareValues(a: CellValue, b: CellValue, kind: GridColumnKind): number {
  if (a === null || b === null) return a === b ? 0 : a === null ? 1 : -1
  if (kind === "number") {
    const x = typeof a === "string" || typeof a === "number" ? parseDecimal(a) : null
    const y = typeof b === "string" || typeof b === "number" ? parseDecimal(b) : null
    if (x && y) return compareDecimal(x, y)
    const p = Number(a)
    const q = Number(b)
    if (!Number.isNaN(p) && !Number.isNaN(q)) return p < q ? -1 : p > q ? 1 : 0
  }
  if (typeof a === "boolean" && typeof b === "boolean") return a === b ? 0 : a ? 1 : -1
  const x = typeof a === "object" ? JSON.stringify(a) : String(a)
  const y = typeof b === "object" ? JSON.stringify(b) : String(b)
  return x < y ? -1 : x > y ? 1 : 0
}

/** A fresh random UUID, where the browser will not hand one over on plain HTTP. */
export function randomUUID(): string {
  if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") {
    return crypto.randomUUID()
  }
  const b = new Uint8Array(16)
  crypto.getRandomValues(b)
  b[6] = (b[6] & 0x0f) | 0x40
  b[8] = (b[8] & 0x3f) | 0x80
  const hex = Array.from(b, (x) => x.toString(16).padStart(2, "0")).join("")
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`
}
