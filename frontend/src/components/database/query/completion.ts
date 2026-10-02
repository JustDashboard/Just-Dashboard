import { qualifiedName, quoteName, type Dialect } from "@/components/database/query/dialect"
import { RESERVED } from "@/components/database/query/keywords"
import {
  splitStatements,
  statementAt,
  tokenize,
  type Token,
} from "@/components/database/query/sql-text"
import type { CatalogHead, Outline } from "@/components/database/query/types"

/**
 * What to offer at the cursor, worked out from the statement being typed and
 * the schema the connection holds — with no editor in it, so it can be tested
 * as a function.
 *
 * After `orders.` or an alias, the columns of that table and nothing else;
 * after a schema's name, what the schema holds; after FROM or JOIN, tables;
 * anywhere else the columns of the tables the statement names come first,
 * then tables, functions and the words of the language. Inside a text or a
 * comment nothing is offered.
 */

export interface SchemaTable {
  schema: string
  name: string
  /** "table", "view", "materialized view"… as the server says it. */
  type: string
  columns: string[]
}

export interface SchemaModel {
  /** The schema unqualified names resolve to on this connection. */
  defaultSchema: string
  schemas: string[]
  tables: SchemaTable[]
}

export const EMPTY_MODEL: SchemaModel = { defaultSchema: "", schemas: [], tables: [] }

/** The model the completion and the schema tree read, from the two reads that describe a connection. */
export function schemaModel(
  outline: Outline | undefined,
  head: CatalogHead | undefined,
): SchemaModel {
  // A server without the route answers the path with a list: nothing is known, not a failure.
  if (!outline || !Array.isArray(outline.entries)) return EMPTY_MODEL
  const tables = outline.entries.map((entry) => ({
    schema: entry.schema,
    name: entry.name,
    type: entry.type,
    columns: outline.tables?.[entry.id] ?? [],
  }))
  const listed = (head?.schemas ?? [])
    .filter((schema) => !schema.system)
    .map((schema) => schema.name)
  const schemas = [...new Set([...listed, ...tables.map((table) => table.schema)])].filter(Boolean)
  const defaultSchema =
    head?.defaultSchema || (schemas.length === 1 ? schemas[0] : "") || outline.schema || ""
  return { defaultSchema, schemas, tables }
}

export type SuggestionKind =
  "column" | "table" | "view" | "schema" | "alias" | "function" | "keyword"

export interface Suggestion {
  label: string
  /** What is written: the label quoted as the engine needs, a call with its parentheses. */
  insert: string
  kind: SuggestionKind
  detail?: string
  /** Lower comes first. */
  rank: number
}

export interface Vocabulary {
  keywords: string[]
  functions: string[]
}

/** A table the statement names, and what it calls it. */
export interface TableRef {
  schema: string
  name: string
  alias: string
}

const same = (a: string, b: string) => a.toLowerCase() === b.toLowerCase()

/** A name as written in a statement, without the marks that quote it. */
function bare(raw: string, dialect: Dialect): string {
  for (const [open, close] of [dialect.quote, ...dialect.alsoQuotes]) {
    if (raw.length >= 2 && raw.startsWith(open) && raw.endsWith(close)) {
      return raw
        .slice(open.length, raw.length - close.length)
        .split(close + close)
        .join(close)
    }
  }
  return raw
}

const TABLE_WORDS = new Set(["FROM", "JOIN", "UPDATE", "INTO", "TABLE", "DESCRIBE", "TRUNCATE"])
/** Words that end a FROM list: after one of them a comma no longer introduces a table. */
const LIST_ENDS = new Set([
  "WHERE",
  "GROUP",
  "ORDER",
  "HAVING",
  "LIMIT",
  "SET",
  "ON",
  "USING",
  "SELECT",
  "VALUES",
  "RETURNING",
  "UNION",
  "WINDOW",
])

/** The tables a statement names after FROM, JOIN, UPDATE and INTO, with their aliases. */
export function tableRefs(sql: string, dialect: Dialect): TableRef[] {
  const tokens = tokenize(sql, dialect).filter(
    (token) => token.kind !== "space" && token.kind !== "comment",
  )
  const text = (token: Token | undefined) => (token ? sql.slice(token.start, token.end) : "")
  const upper = (token: Token | undefined) =>
    token?.kind === "word" ? text(token).toUpperCase() : ""
  const isName = (token: Token | undefined) =>
    token !== undefined && (token.kind === "name" || token.kind === "word")
  const refs: TableRef[] = []
  let inList = false
  for (let at = 0; at < tokens.length; at++) {
    const word = upper(tokens[at])
    // A word after a dot is read as the keyword it spells all the same: in a
    // statement being typed, `o.` is more often an unfinished name before
    // FROM than a column called "from".
    const introduces = TABLE_WORDS.has(word) || (inList && tokens[at].kind === "comma")
    if (LIST_ENDS.has(word)) inList = false
    if (!introduces) continue
    inList = word === "FROM" || word === "JOIN" || tokens[at].kind === "comma"
    let next = at + 1
    // A subquery in the list has no columns this reading knows.
    if (!isName(tokens[next]) || RESERVED.has(upper(tokens[next]))) continue
    const parts = [bare(text(tokens[next]), dialect)]
    while (tokens[next + 1]?.kind === "dot" && isName(tokens[next + 2])) {
      parts.push(bare(text(tokens[next + 2]), dialect))
      next += 2
    }
    let alias = ""
    let after = next + 1
    if (upper(tokens[after]) === "AS") after++
    const candidate = tokens[after]
    if (isName(candidate) && !(candidate.kind === "word" && RESERVED.has(upper(candidate)))) {
      alias = bare(text(candidate), dialect)
    }
    refs.push({
      schema: parts.length > 1 ? parts[parts.length - 2] : "",
      name: parts[parts.length - 1],
      alias,
    })
    at = next
  }
  return refs
}

function findTable(model: SchemaModel, schema: string, name: string): SchemaTable | undefined {
  const named = model.tables.filter((table) => same(table.name, name))
  if (schema) return named.find((table) => same(table.schema, schema))
  return named.find((table) => table.schema === model.defaultSchema) ?? named[0]
}

const kindOf = (table: SchemaTable): SuggestionKind =>
  /view|dictionary/i.test(table.type) ? "view" : "table"

function columnsOf(table: SchemaTable, dialect: Dialect, rank: number, from: string): Suggestion[] {
  return table.columns.map((column) => ({
    label: column,
    insert: quoteName(column, dialect, RESERVED),
    kind: "column",
    detail: from,
    rank,
  }))
}

function tablesOf(
  model: SchemaModel,
  dialect: Dialect,
  rank: number,
  within?: string,
): Suggestion[] {
  return model.tables
    .filter((table) => within === undefined || same(table.schema, within))
    .map((table) => {
      const local = within !== undefined || table.schema === model.defaultSchema || !table.schema
      return {
        label: local ? table.name : `${table.schema}.${table.name}`,
        insert: local
          ? quoteName(table.name, dialect, RESERVED)
          : qualifiedName(table.schema, table.name, dialect, model.defaultSchema, RESERVED),
        kind: kindOf(table),
        detail: `${table.type || "table"} · ${table.columns.length} ${table.columns.length === 1 ? "column" : "columns"}`,
        // What the connection's own schema holds comes before the rest.
        rank: local ? rank : rank + 1,
      }
    })
}

/** Past this many tables, columns are offered only for the tables a statement names. */
const EVERY_COLUMN_UNTIL = 200

/**
 * The suggestions at `offset` of `text`. The editor filters them by what has
 * been typed of the word; this decides which names belong there at all.
 */
export function suggest(
  model: SchemaModel,
  dialect: Dialect,
  vocabulary: Vocabulary,
  text: string,
  offset: number,
): Suggestion[] {
  const spans = splitStatements(text, dialect)
  const span = statementAt(spans, offset)
  const from = span ? span.from : 0
  const to = span ? Math.max(span.to, offset) : text.length
  const sql = text.slice(from, to)
  const cursor = offset - from
  const tokens = tokenize(sql, dialect)

  const holding = tokens.find((token) => cursor > token.start && cursor <= token.end)
  if (holding && insideProse(holding, sql, cursor)) return []
  // What stands before the word being typed, nearest last.
  const typing = holding?.kind === "word" || holding?.kind === "name" ? holding : undefined
  const prior = tokens.filter(
    (token) =>
      token.end <= cursor && token.kind !== "space" && token.kind !== "comment" && token !== typing,
  )
  const textOf = (token: Token) => sql.slice(token.start, token.end)

  // `a.b.` — the names that qualify the word being typed.
  const qualifier: string[] = []
  let at = prior.length - 1
  while (
    at >= 1 &&
    prior[at].kind === "dot" &&
    (prior[at - 1].kind === "word" || prior[at - 1].kind === "name")
  ) {
    qualifier.unshift(bare(textOf(prior[at - 1]), dialect))
    at -= 2
  }

  const refs = tableRefs(sql, dialect)

  if (qualifier.length > 0) {
    const [first, second] = qualifier
    if (qualifier.length >= 2) {
      const table = findTable(model, first, second)
      return table ? columnsOf(table, dialect, 0, `${table.schema}.${table.name}`) : []
    }
    const aliased = refs.find((ref) => ref.alias && same(ref.alias, first))
    const named = aliased ?? refs.find((ref) => same(ref.name, first))
    const table = named ? findTable(model, named.schema, named.name) : findTable(model, "", first)
    const out: Suggestion[] = []
    if (table) out.push(...columnsOf(table, dialect, 0, table.name))
    // A schema's name is followed by what it holds — unless an alias or a table took the name.
    if (!aliased && model.schemas.some((schema) => same(schema, first))) {
      out.push(...tablesOf(model, dialect, table ? 1 : 0, first))
    }
    return out
  }

  const last = prior[prior.length - 1]
  const lastWord = last?.kind === "word" ? textOf(last).toUpperCase() : ""
  const schemas: Suggestion[] = model.schemas
    .filter((schema) => schema !== model.defaultSchema || model.schemas.length > 1)
    .map((schema) => ({
      label: schema,
      insert: quoteName(schema, dialect, RESERVED),
      kind: "schema",
      detail: "schema",
      rank: 6,
    }))

  // After FROM, JOIN, INTO and UPDATE — or a comma in a FROM list — a table is what comes next.
  const inFromList = last?.kind === "comma" && fromListOpen(prior, textOf)
  if (TABLE_WORDS.has(lastWord) || inFromList) {
    return [...tablesOf(model, dialect, 0), ...schemas.map((s) => ({ ...s, rank: 2 }))]
  }

  const out: Suggestion[] = []
  const seen = new Set<string>()
  const scoped = refs
    .map((ref) => ({ ref, table: findTable(model, ref.schema, ref.name) }))
    .filter((entry): entry is { ref: TableRef; table: SchemaTable } => entry.table !== undefined)
  for (const { ref, table } of scoped) {
    for (const column of columnsOf(table, dialect, 0, ref.alias || table.name)) {
      if (seen.has(column.label)) continue
      seen.add(column.label)
      out.push(column)
    }
  }
  for (const { ref, table } of scoped) {
    if (ref.alias) {
      out.push({
        label: ref.alias,
        insert: quoteName(ref.alias, dialect, RESERVED),
        kind: "alias",
        detail: table.name,
        rank: 1,
      })
    }
  }
  out.push(...tablesOf(model, dialect, 2))
  if (scoped.length === 0 && model.tables.length <= EVERY_COLUMN_UNTIL) {
    // No table named yet — a SELECT list written before its FROM: every column, once.
    for (const table of model.tables) {
      for (const column of columnsOf(table, dialect, 4, table.name)) {
        if (seen.has(column.label)) continue
        seen.add(column.label)
        out.push(column)
      }
    }
  }
  out.push(...schemas)
  for (const name of vocabulary.functions) {
    out.push({ label: name, insert: `${name}($0)`, kind: "function", rank: 7 })
  }
  for (const word of vocabulary.keywords) {
    out.push({ label: word, insert: word, kind: "keyword", rank: 8 })
  }
  return out
}

/** Whether the cursor is inside a text, a comment or a dollar-quoted body: words, not SQL. */
function insideProse(token: Token, sql: string, cursor: number): boolean {
  if (token.kind !== "text" && token.kind !== "comment" && token.kind !== "body") return false
  if (cursor < token.end) return true
  const raw = sql.slice(token.start, token.end)
  // At its end: a line comment goes on, and so does a text nobody has closed.
  if (token.kind === "comment") return !raw.startsWith("/*") || !raw.endsWith("*/")
  if (token.kind === "body") return false
  return raw.length < 2 || !raw.endsWith(raw[0])
}

/** Whether the comma just typed sits in a FROM list: a FROM before it with no clause word since. */
function fromListOpen(prior: Token[], textOf: (token: Token) => string): boolean {
  let depth = 0
  for (let at = prior.length - 1; at >= 0; at--) {
    const token = prior[at]
    if (token.kind === "close") depth++
    else if (token.kind === "open") {
      if (depth === 0) return false
      depth--
    } else if (depth === 0 && token.kind === "word") {
      const word = textOf(token).toUpperCase()
      if (word === "FROM") return true
      if (LIST_ENDS.has(word)) return false
    }
  }
  return false
}
