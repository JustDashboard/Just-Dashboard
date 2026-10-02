import type { MongoSchemaField, MongoSchemaType } from "@/components/database/mongo/types"

/**
 * What a sampled field's readings are turned into: the filter a press on one
 * of them asks Documents for, and the words a range or a length is said in.
 */

/** The path a filter names a sampled field by: an array's elements match through the array itself. */
export function filterPath(path: string): string {
  return path.replace(/\[\]/g, "")
}

/** Types whose top values the server sends as text that a filter can state exactly. */
const LITERAL: Record<string, (value: string) => string | null> = {
  string: (value) => JSON.stringify(value),
  bool: (value) => (value === "true" || value === "false" ? value : null),
  int: (value) => (/^-?\d+$/.test(value) ? value : null),
  long: (value) => (/^-?\d+$/.test(value) ? `{ "$numberLong": ${JSON.stringify(value)} }` : null),
}

/**
 * The clause that finds the documents holding this value in this field, or
 * `null` when it cannot be stated: a value the server cut short (it marks the
 * cut with an ellipsis) would match nothing.
 */
export function valueClause(path: string, type: string, value: string): string | null {
  const write = Object.hasOwn(LITERAL, type) ? LITERAL[type] : undefined
  if (!write) return null
  if (type === "string" && value.endsWith("…") && new TextEncoder().encode(value).length > 160) {
    return null
  }
  const literal = write(value)
  return literal === null ? null : `{ ${JSON.stringify(filterPath(path))}: ${literal} }`
}

/** The clause that finds the documents where the field has this type. */
export function typeClause(path: string, type: string): string {
  return `{ ${JSON.stringify(filterPath(path))}: { "$type": ${JSON.stringify(type)} } }`
}

/** The clause that finds the documents that do not have the field at all. */
export function missingClause(path: string): string {
  return `{ ${JSON.stringify(filterPath(path))}: { "$exists": false } }`
}

const compact = (value: number) =>
  Number.isInteger(value)
    ? value.toLocaleString("en-US")
    : value.toLocaleString("en-US", { maximumFractionDigits: 2 })

const day = (iso: string) => (/^\d{4}-\d{2}-\d{2}/.test(iso) ? iso.slice(0, 10) : iso)

/**
 * A type's spread in one line: the least and the most for a number or a
 * moment, the lengths for a string or a list. `null` where the server sent
 * neither.
 */
export function rangeWords(type: MongoSchemaType): string | null {
  const name = type.type
  if (type.min !== undefined && type.max !== undefined) {
    if (name === "date" || name === "objectId" || name === "timestamp") {
      const from = day(type.min)
      const to = day(type.max)
      const what = name === "objectId" ? "made " : ""
      return from === to ? `${what}${from}` : `${what}${from} to ${to}`
    }
    const spread = type.min === type.max ? type.min : `${type.min} to ${type.max}`
    return type.avg !== undefined && type.min !== type.max
      ? `${spread} · mean ${compact(type.avg)}`
      : spread
  }
  if (type.minLength !== undefined && type.maxLength !== undefined) {
    const unit = name === "array" ? "elements" : name === "string" ? "bytes" : "long"
    const spread =
      type.minLength === type.maxLength
        ? `${compact(type.minLength)}`
        : `${compact(type.minLength)} to ${compact(type.maxLength)}`
    return `${spread} ${unit}`
  }
  return null
}

/** "58 distinct", "over 1,000 distinct": how many different values a type showed. */
export function distinctWords(type: MongoSchemaType): string | null {
  if (type.distinct === undefined) return null
  return `${type.distinctCapped ? "over " : ""}${type.distinct.toLocaleString("en-US")} distinct`
}

/** A share of the sample as a percentage a reader compares: whole above ten, one decimal below. */
export function shareWords(share: number): string {
  const percent = share * 100
  if (percent >= 99.95) return "100%"
  if (percent > 0 && percent < 0.1) return "<0.1%"
  return `${percent >= 10 ? Math.round(percent) : Math.round(percent * 10) / 10}%`
}

/** Whether every value of a type's top list was seen once: a field of unique values. */
export function allUnique(type: MongoSchemaType): boolean {
  return Boolean(type.top && type.top.length > 1 && type.top.every((entry) => entry.count === 1))
}

/** The fields that match a search, each with the fields above it so the tree stays readable. */
export function matchingFields(fields: readonly MongoSchemaField[], needle: string) {
  const query = needle.trim().toLowerCase()
  if (!query) return [...fields]
  const keep = new Set<string>()
  for (const field of fields) {
    if (!field.path.toLowerCase().includes(query)) continue
    keep.add(field.path)
    // Its ancestors: every prefix of the path that is itself a sampled field.
    for (const other of fields) {
      if (
        other.depth < field.depth &&
        (field.path.startsWith(`${other.path}.`) || field.path.startsWith(`${other.path}[]`))
      ) {
        keep.add(other.path)
      }
    }
  }
  return fields.filter((field) => keep.has(field.path))
}
