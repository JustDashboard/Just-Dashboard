import { bytes } from "@/lib/format"
import { compactCount } from "@/components/database/data/view"

/**
 * One fact in a line of them: a figure or a name in the foreground, with the
 * word that says what it is before or after it in the quiet voice.
 */
export type Fact = {
  key: string
  /** The reading itself: "~9,000", "3.2 MB", "app". */
  text: string
  /** The word before it: "owned by". */
  lead?: string
  /** The word after it: "rows". */
  tail?: string
  /** A literal of the database's — an owner, an engine's own value. */
  mono?: boolean
  title?: string
}

const grouped = (value: number) => value.toLocaleString("en-US")

const plural = (count: number, one: string, many: string) => (count === 1 ? one : many)

/** What of a table the line is made from; every engine fills a different part of it. */
export type TableFigures = {
  estimatedRows: number
  size?: number
  dataSize?: number
  indexSize?: number
  owner?: string
  facts?: readonly { name: string; value: string }[]
}

/**
 * A table as one line: about how many rows, how much of the disk and how
 * that splits between its rows and its indexes, whose it is, and whatever
 * its engine adds. An estimate the engine did not give is said as such
 * rather than left out: "rows not counted" is a fact about the table too.
 */
export function tableFacts(table: TableFigures, columns?: number): Fact[] {
  const facts: Fact[] = [
    table.estimatedRows >= 0
      ? {
          key: "rows",
          text: `~${grouped(table.estimatedRows)}`,
          tail: plural(table.estimatedRows, "row", "rows"),
          title: "The engine's own estimate",
        }
      : { key: "rows", text: "not counted", lead: "rows" },
  ]
  if (table.size !== undefined) facts.push({ key: "size", text: bytes(table.size) })
  if (table.dataSize !== undefined && table.indexSize !== undefined) {
    facts.push({ key: "data", text: bytes(table.dataSize), tail: "data" })
    facts.push({ key: "indexes", text: bytes(table.indexSize), tail: "indexes" })
  }
  if (columns !== undefined) {
    facts.push({
      key: "columns",
      text: grouped(columns),
      tail: plural(columns, "column", "columns"),
    })
  }
  if (table.owner) facts.push({ key: "owner", text: table.owner, lead: "owned by", mono: true })
  for (const fact of table.facts ?? []) {
    facts.push({ key: `fact:${fact.name}`, text: fact.value, lead: fact.name.toLowerCase() })
  }
  return facts
}

/** What a schema holds, counted: how many of each kind, about how many rows, how much disk. */
export type SchemaFigures = {
  /** Each kind that holds rows, with the two words for one and for many of it. */
  counts: readonly { key: string; count: number; one: string; many: string }[]
  rows: number | null
  size: number | null
}

/** A schema as one line: "3 tables · 1 view · ~1.2M rows · 48 MB". */
export function schemaFacts(schema: SchemaFigures): Fact[] {
  const facts: Fact[] = schema.counts.map(({ key, count, one, many }) => ({
    key,
    text: grouped(count),
    tail: plural(count, one, many),
  }))
  if (schema.rows !== null) {
    facts.push({
      key: "rows",
      text: `~${compactCount(schema.rows)}`,
      tail: plural(schema.rows, "row", "rows"),
      title: "Estimated by the engine",
    })
  }
  if (schema.size !== null) facts.push({ key: "size", text: bytes(schema.size), tail: "on disk" })
  return facts
}
