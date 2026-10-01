import type { GridColumnKind } from "./types"

/**
 * From an engine's type name to what the grid draws and edits.
 *
 * The server sends a kind per column once it can (`QueryResult.kinds`); until
 * it does, and for an engine it has no opinion on, the declared type name is
 * all there is. Both paths end in the same twelve kinds so a renderer or an
 * editor never looks at a type name itself.
 */

/** The server's vocabulary (`ValueKind` in the workbench contract). */
const SERVER_KINDS: Record<string, GridColumnKind> = {
  text: "text",
  integer: "number",
  decimal: "number",
  float: "number",
  boolean: "boolean",
  date: "date",
  time: "time",
  datetime: "datetime",
  // An interval has no editor of its own and no arithmetic the grid can do
  // exactly, so it is read and typed as the text the engine prints.
  interval: "text",
  json: "json",
  uuid: "uuid",
  binary: "binary",
  array: "array",
  other: "unknown",
}

export function kindFromServer(kind: string | undefined): GridColumnKind | undefined {
  return kind ? SERVER_KINDS[kind] : undefined
}

/**
 * The kind a declared type implies.
 *
 * Order matters: `timestamp` contains `time`, `interval` contains `int`,
 * `point` contains `int`, and `tinyint(1)` is MySQL's boolean.
 */
export function kindFromType(typeName: string): GridColumnKind {
  const type = typeName.trim().toLowerCase()
  if (!type) return "unknown"
  // Postgres spells an array either way round: `text[]` declared, `_TEXT` on the wire.
  if (type.endsWith("[]") || /^_[a-z]/.test(type) || type === "array") return "array"
  if (/^(array|map|tuple|nested)\(/.test(type)) return "array"
  if (/^(nullable|lowcardinality)\((.*)\)$/.test(type)) {
    return kindFromType(type.replace(/^(nullable|lowcardinality)\((.*)\)$/, "$2"))
  }
  if (/^enum|^set\(/.test(type)) return "enum"
  if (/^bool|^bit$|^bit\(1\)$|^tinyint\(1\)$/.test(type)) return "boolean"
  if (/json/.test(type)) return "json"
  if (/uuid|uniqueidentifier/.test(type)) return "uuid"
  if (/bytea|blob|binary|^raw|^image$|^bytes$/.test(type)) return "binary"
  if (/timestamp|datetime|^smalldatetime/.test(type)) return "datetime"
  if (/^date/.test(type)) return "date"
  if (/^time/.test(type)) return "time"
  if (/interval|point|^inet|^cidr|^macaddr/.test(type)) return "text"
  if (
    /int|serial|numeric|decimal|^number|real|double|float|^money$|^smallmoney$|^year$|^oid$/.test(
      type,
    )
  ) {
    return "number"
  }
  if (/char|text|clob|string|^name$|^citext$|^xml$/.test(type)) return "text"
  return "unknown"
}

/**
 * The kind of a column, from everything known about it.
 *
 * A column with enum labels is an enum whatever its type name says — Postgres
 * reports one as `USER-DEFINED` or by a bare OID. The server's kind wins over
 * the name otherwise, because it was decided from the catalogue.
 */
export function columnKind(input: {
  typeName: string
  serverKind?: string
  enumValues?: readonly string[]
}): GridColumnKind {
  if (input.enumValues && input.enumValues.length > 0) return "enum"
  return kindFromServer(input.serverKind) ?? kindFromType(input.typeName)
}

/** What a number column will accept, so an edit is refused before it is staged. */
export type NumberSpec =
  | { class: "integer"; min: bigint; max: bigint }
  | { class: "decimal"; precision?: number; scale?: number }
  | { class: "float" }

const INTEGER_BITS: [RegExp, number][] = [
  [/^(tinyint|int1)$/, 8],
  [/^(smallint|int2|smallserial|serial2)$/, 16],
  [/^(mediumint|int3)$/, 24],
  [/^(int|integer|int4|serial|serial4)$/, 32],
  [/^(bigint|int8|bigserial|serial8|oid)$/, 64],
]

function integerRange(bits: number, unsigned: boolean): { min: bigint; max: bigint } {
  const span = BigInt(1) << BigInt(bits)
  if (unsigned) return { min: BigInt(0), max: span - BigInt(1) }
  return { min: -(span / BigInt(2)), max: span / BigInt(2) - BigInt(1) }
}

/**
 * Reads the bounds out of a type name.
 *
 * Two engines spell different widths the same way, and the case of the name is
 * the only thing that tells them apart: ClickHouse's `Int8` is one byte where
 * Postgres's `int8` is eight, and SQLite's `INTEGER` holds 64 bits where
 * Postgres's `integer` holds 32. Where the name does not say, the wider
 * reading wins — refusing a value the engine would have taken is the worse of
 * the two mistakes, and the engine still refuses the other one.
 */
export function numberSpec(typeName: string): NumberSpec {
  const declared = typeName.trim().replace(/^(?:Nullable|LowCardinality)\((.*)\)$/, "$1")
  const clickhouse = /^(U?)Int(8|16|32|64|128|256)$/.exec(declared)
  if (clickhouse) {
    return { class: "integer", ...integerRange(Number(clickhouse[2]), clickhouse[1] === "U") }
  }

  const raw = declared.toLowerCase()
  const unsigned = /unsigned/.test(raw)
  const args = /\(([^)]*)\)/.exec(raw)?.[1]
  const base = raw
    .replace(/\(.*?\)/g, "")
    .replace(/\b(unsigned|signed|zerofill)\b/g, "")
    .trim()

  if (/^(numeric|decimal|number|dec|money|smallmoney)/.test(base)) {
    if (!args) return { class: "decimal" }
    const [precision, scale] = args.split(",").map((part) => Number(part.trim()))
    if (!Number.isInteger(precision) || precision <= 0) return { class: "decimal" }
    return { class: "decimal", precision, scale: Number.isInteger(scale) ? scale : 0 }
  }
  if (/real|double|float/.test(base)) return { class: "float" }
  if (base === "year") return { class: "integer", min: BigInt(0), max: BigInt(9999) }
  if (declared === "INTEGER" || declared === "INT") {
    return { class: "integer", ...integerRange(64, false) }
  }
  for (const [pattern, bits] of INTEGER_BITS) {
    if (pattern.test(base)) return { class: "integer", ...integerRange(bits, unsigned) }
  }
  if (/int|serial/.test(base)) return { class: "integer", ...integerRange(64, unsigned) }
  return { class: "decimal" }
}
