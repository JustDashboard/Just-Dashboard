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

/** A foreign key: these columns of one table name those columns of another. */
export interface Relation {
  schema: string
  table: string
  columns: string[]
  refSchema: string
  refTable: string
  refColumns: string[]
}

export interface SchemaModel {
  /** The schema unqualified names resolve to on this connection. */
  defaultSchema: string
  schemas: string[]
  tables: SchemaTable[]
  /** The foreign keys between them, where they have been read: what a join is written from. */
  relations?: Relation[]
}

export const EMPTY_MODEL: SchemaModel = { defaultSchema: "", schemas: [], tables: [] }

/** The model the completion and the schema tree read, from the two reads that describe a connection. */
export function schemaModel(
  outline: Outline | undefined,
  head: CatalogHead | undefined,
  relations?: unknown,
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
  const keys = readRelations(relations, outline.entries)
  return keys.length > 0
    ? { defaultSchema, schemas, tables, relations: keys }
    : { defaultSchema, schemas, tables }
}

/**
 * The foreign keys of `GET /relations`, which names each table by one string
 * (`schema.table`, and a dot may be part of a name): the outline's entries say
 * which schema and table each string is. Anything else that came back — a
 * server without the route answers with a list — is no keys.
 */
function readRelations(answer: unknown, entries: Outline["entries"]): Relation[] {
  if (!answer || typeof answer !== "object" || Array.isArray(answer)) return []
  const byId = new Map(entries.map((entry) => [entry.id, entry]))
  const out: Relation[] = []
  for (const [id, keys] of Object.entries(answer as Record<string, unknown>)) {
    const holder = byId.get(id)
    if (!holder || !Array.isArray(keys)) continue
    for (const key of keys as Partial<Relation>[]) {
      if (!Array.isArray(key?.columns) || !Array.isArray(key.refColumns) || !key.refTable) continue
      if (key.columns.length === 0 || key.columns.length !== key.refColumns.length) continue
      out.push({
        schema: holder.schema,
        table: holder.name,
        columns: key.columns,
        refSchema: key.refSchema || holder.schema,
        refTable: key.refTable,
        refColumns: key.refColumns,
      })
    }
  }
  return out
}

export type SuggestionKind =
  "column" | "table" | "view" | "schema" | "alias" | "function" | "keyword" | "join"

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

/** How a statement calls a table it names: its alias, else its name as it has to be written. */
const calledAs = (ref: TableRef, dialect: Dialect) =>
  quoteName(ref.alias || ref.name, dialect, RESERVED)

const isTable = (table: SchemaTable, schema: string, name: string) =>
  table.schema === schema && table.name === name

/**
 * The conditions that join the table just named to the ones before it, read
 * off the foreign keys between them: `o.customer_id = c.id`. The joined
 * table's side is written first, and a key of several columns is one
 * condition joined with AND.
 */
export function joinConditions(
  model: SchemaModel,
  dialect: Dialect,
  refs: readonly TableRef[],
): Suggestion[] {
  if (refs.length < 2 || !model.relations?.length) return []
  const joined = refs[refs.length - 1]
  const joinedTable = findTable(model, joined.schema, joined.name)
  if (!joinedTable) return []
  const out: Suggestion[] = []
  const seen = new Set<string>()
  const column = (name: string) => quoteName(name, dialect, RESERVED)
  for (const other of refs.slice(0, -1)) {
    const otherTable = findTable(model, other.schema, other.name)
    if (!otherTable) continue
    for (const key of model.relations) {
      const holds = isTable(joinedTable, key.schema, key.table)
      const held = isTable(otherTable, key.schema, key.table)
      const pointsAtOther = holds && isTable(otherTable, key.refSchema, key.refTable)
      const pointsAtJoined = held && isTable(joinedTable, key.refSchema, key.refTable)
      if (!pointsAtOther && !pointsAtJoined) continue
      const text = key.columns
        .map((name, at) =>
          pointsAtOther
            ? `${calledAs(joined, dialect)}.${column(name)} = ${calledAs(other, dialect)}.${column(key.refColumns[at])}`
            : `${calledAs(joined, dialect)}.${column(key.refColumns[at])} = ${calledAs(other, dialect)}.${column(name)}`,
        )
        .join(" AND ")
      if (seen.has(text)) continue
      seen.add(text)
      out.push({ label: text, insert: text, kind: "join", detail: "foreign key", rank: -1 })
    }
  }
  return out
}

/** The tables a foreign key ties to the ones a statement already names: what a JOIN most often wants next. */
function relatedTables(model: SchemaModel, refs: readonly TableRef[]): Map<SchemaTable, string> {
  const related = new Map<SchemaTable, string>()
  if (!model.relations?.length) return related
  for (const ref of refs) {
    const named = findTable(model, ref.schema, ref.name)
    if (!named) continue
    for (const key of model.relations) {
      const other = isTable(named, key.schema, key.table)
        ? model.tables.find((table) => isTable(table, key.refSchema, key.refTable))
        : isTable(named, key.refSchema, key.refTable)
          ? model.tables.find((table) => isTable(table, key.schema, key.table))
          : undefined
      if (other && !related.has(other)) related.set(other, named.name)
    }
  }
  return related
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

  // A name of the reader's own is being written — after AS, and after a table
  // in a FROM list, where the next word is its alias. Nothing is offered:
  // whatever was, Enter would write it over the name being typed.
  if (lastWord === "AS") return []
  if (namesATable(prior, textOf)) return []

  // After FROM, JOIN, INTO and UPDATE — or a comma in a FROM list — a table is what comes next.
  const inFromList = last?.kind === "comma" && fromListOpen(prior, textOf)
  if (TABLE_WORDS.has(lastWord) || inFromList) {
    const tables = tablesOf(model, dialect, 0)
    if (lastWord === "JOIN") {
      // What a foreign key ties to the tables already named comes first.
      const related = relatedTables(model, tableRefs(sql.slice(0, cursor), dialect))
      for (const [table, to] of related) {
        const local = table.schema === model.defaultSchema || !table.schema
        const label = local ? table.name : `${table.schema}.${table.name}`
        const item = tables.find((entry) => entry.label === label)
        if (item) {
          item.rank = -1
          item.detail = `joins ${to}`
        }
      }
    }
    return [...tables, ...schemas.map((s) => ({ ...s, rank: 2 }))]
  }

  const out: Suggestion[] = []
  const seen = new Set<string>()
  // After ON, the condition the foreign key between the two tables states.
  if (lastWord === "ON") {
    out.push(...joinConditions(model, dialect, tableRefs(sql.slice(0, cursor), dialect)))
  }
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

/** The words after which the next name is a table that may then be given an alias. */
const ALIASED_AFTER = new Set(["FROM", "JOIN", "UPDATE"])

/**
 * Whether the tokens before the word being typed end in a table named in a
 * FROM list (`from orders`, `join public.customers`, `from a, b`): the word
 * being typed is then that table's alias.
 */
function namesATable(prior: Token[], textOf: (token: Token) => string): boolean {
  const isName = (token: Token | undefined) =>
    token !== undefined && (token.kind === "name" || token.kind === "word")
  let at = prior.length - 1
  if (!isName(prior[at])) return false
  if (prior[at].kind === "word" && RESERVED.has(textOf(prior[at]).toUpperCase())) return false
  while (at >= 2 && prior[at - 1].kind === "dot" && isName(prior[at - 2])) at -= 2
  const before = prior[at - 1]
  if (!before) return false
  if (before.kind === "word") return ALIASED_AFTER.has(textOf(before).toUpperCase())
  return before.kind === "comma" && fromListOpen(prior.slice(0, at), textOf)
}

/** What the pointer rests on, for the editor to describe: a table, or one column of one. */
export type Subject =
  { kind: "table"; table: SchemaTable } | { kind: "column"; table: SchemaTable; column: string }

/**
 * The table or column a name in the text is, read the way the completion
 * reads it: an alias is its table, `o.status` is that column of the table `o`
 * stands for, and a bare name is a column of a table the statement names
 * before it is a table of its own.
 */
export function subjectAt(
  model: SchemaModel,
  dialect: Dialect,
  text: string,
  offset: number,
): Subject | null {
  const spans = splitStatements(text, dialect)
  const span = statementAt(spans, offset)
  const from = span ? span.from : 0
  const sql = text.slice(from, span ? Math.max(span.to, offset) : text.length)
  const cursor = offset - from
  const tokens = tokenize(sql, dialect).filter(
    (token) => token.kind !== "space" && token.kind !== "comment",
  )
  const at = tokens.findIndex((token) => cursor >= token.start && cursor <= token.end)
  const token = tokens[at]
  if (!token || (token.kind !== "word" && token.kind !== "name")) return null
  const textOf = (one: Token) => sql.slice(one.start, one.end)
  const isName = (one: Token | undefined) =>
    one !== undefined && (one.kind === "name" || one.kind === "word")
  const name = bare(textOf(token), dialect)
  const refs = tableRefs(sql, dialect)
  const column = (table: SchemaTable | undefined, wanted: string): Subject | null => {
    const found = table?.columns.find((entry) => same(entry, wanted))
    return table && found ? { kind: "column", table, column: found } : null
  }

  if (tokens[at - 1]?.kind === "dot" && isName(tokens[at - 2])) {
    const first = bare(textOf(tokens[at - 2]), dialect)
    const second =
      tokens[at - 3]?.kind === "dot" && isName(tokens[at - 4])
        ? bare(textOf(tokens[at - 4]), dialect)
        : ""
    // `schema.table.column`, then `alias.column` or `table.column`, then `schema.table`.
    if (second) return column(findTable(model, second, first), name)
    const ref = refs.find((entry) => entry.alias && same(entry.alias, first))
    const named = ref ?? refs.find((entry) => same(entry.name, first))
    const owner = named ? findTable(model, named.schema, named.name) : findTable(model, "", first)
    const asColumn = column(owner, name)
    if (asColumn) return asColumn
    const inSchema = model.schemas.some((schema) => same(schema, first))
      ? findTable(model, first, name)
      : undefined
    return inSchema ? { kind: "table", table: inSchema } : null
  }

  if (token.kind === "word" && RESERVED.has(name.toUpperCase())) return null
  const aliased = refs.find((entry) => entry.alias && same(entry.alias, name))
  if (aliased) {
    const table = findTable(model, aliased.schema, aliased.name)
    return table ? { kind: "table", table } : null
  }
  const ref = refs.find((entry) => same(entry.name, name))
  if (ref) {
    const table = findTable(model, ref.schema, ref.name)
    if (table) return { kind: "table", table }
  }
  for (const entry of refs) {
    const found = column(findTable(model, entry.schema, entry.name), name)
    if (found) return found
  }
  const table = findTable(model, "", name)
  return table ? { kind: "table", table } : null
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

/** A column as the pointer's note says it: its name, its type, whether it takes NULL. */
export interface TableColumn {
  name: string
  type: string
  nullable: boolean
}

/** How many columns a table's note lists before it counts the rest. */
const NOTE_COLUMNS = 24

/** The note as Markdown. The names and types are the database's own words, so they are written as code. */
export function subjectNote(subject: Subject, columns: TableColumn[] | undefined): string {
  const code = (text: string) => `\`${text.replace(/\`/g, "'")}\``
  const { table } = subject
  const where = table.schema ? `${table.schema}.${table.name}` : table.name
  const typed = (name: string) => {
    const info = columns?.find((column) => column.name === name)
    return info ? `${code(name)} ${code(info.type)}${info.nullable ? "" : " not null"}` : code(name)
  }
  if (subject.kind === "column") {
    return `${typed(subject.column)}\n\nA column of ${code(where)}.`
  }
  const names = table.columns
  const listed = names.slice(0, NOTE_COLUMNS).map((name) => `- ${typed(name)}`)
  const more =
    names.length > NOTE_COLUMNS ? `\n- and ${names.length - NOTE_COLUMNS} more columns` : ""
  return `${code(where)} — ${table.type || "table"}, ${names.length} ${names.length === 1 ? "column" : "columns"}\n\n${listed.join("\n")}${more}`
}

/**
 * The stretch of a line a completed name replaces, as 1-based columns.
 *
 * A name being typed inside its quote is replaced quote and all, so the
 * quoted form that is written does not open a second one — and the editor
 * closes a quote as it is typed, so the mark standing right after the cursor
 * is replaced with it: completing `"Mi|"` writes `"Mixed Case Table"`, not
 * `"Mixed Case Table""`. A quoted name may hold spaces, so the reach runs
 * from the opening mark to the cursor, not from the last word.
 */
export function quotedReach(
  line: string,
  wordStart: number,
  wordEnd: number,
  cursor: number,
  quotes: readonly (readonly [string, string])[],
): { start: number; end: number } {
  // The nearest opening mark before the cursor with no closing one after it.
  for (let at = cursor - 2; at >= 0; at--) {
    const ch = line[at]
    const quote = quotes.find(([open]) => open === ch)
    if (!quote) {
      // Only what a name may hold lies between its mark and the cursor.
      if (/[\w $.-]/.test(ch)) continue
      break
    }
    const inside = line.slice(at + 1, cursor - 1)
    if (inside.includes(quote[1]) || /^\s/.test(inside)) break
    // An even number of this mark before it: it opens a name rather than closing one.
    const earlier = line.slice(0, at).split(ch).length - 1
    if (quote[0] === quote[1] && earlier % 2 === 1) break
    const closes = line[Math.max(wordEnd, cursor) - 1] === quote[1]
    return { start: at + 1, end: Math.max(wordEnd, cursor) + (closes ? 1 : 0) }
  }
  return { start: wordStart, end: wordEnd }
}
