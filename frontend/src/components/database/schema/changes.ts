import type { DbColumn } from "@/components/database/schema/types"

/**
 * A structure change, as the request that makes it.
 *
 * Every form of the schema browser ends in one of these, and the same one is
 * sent twice: with `?preview=1`, which plans the statement and runs nothing,
 * and without, which runs it. The server writes both from one function, so
 * the statement a form shows is the statement its command runs — and nothing
 * here builds SQL. What is here is the part that can be decided without the
 * server: which route a change is, what its body holds, and whether a form is
 * filled in far enough to be asked about at all.
 */
export type DdlRequest = {
  method: "POST" | "PATCH" | "DELETE"
  /** Under `/databases/<id>`: "/ddl/column". */
  path: string
  body: Record<string, unknown>
}

/** What two requests are compared by: a preview is the answer to exactly one. */
export function requestKey(request: DdlRequest | null): string {
  return request ? `${request.method} ${request.path} ${JSON.stringify(request.body)}` : ""
}

/**
 * Whether asking for the statement costs something. The routes of the
 * destructive group spend the role's destructive budget even for a preview,
 * so they are previewed once, when their confirmation opens, and never while
 * somebody types.
 */
export function spendsBudget(request: DdlRequest): boolean {
  return request.method === "DELETE" || request.path === "/ddl/truncate"
}

/** `schema.name`, or the name alone where the engine has no schema to say. */
export function qualified(schema: string, name: string): string {
  return schema ? `${schema}.${name}` : name
}

/* ----------------------------------------------------------------- tables */

export type ColumnDraft = {
  /** Stable across edits and reorders; never sent. */
  key: string
  name: string
  type: string
  notNull: boolean
  primaryKey: boolean
  default: string
}

export type TableDraft = {
  schema: string
  name: string
  columns: ColumnDraft[]
}

let serial = 0

export function blankColumn(over: Partial<ColumnDraft> = {}): ColumnDraft {
  serial += 1
  return {
    key: `c${Date.now().toString(36)}${serial}`,
    name: "",
    type: "",
    notNull: false,
    primaryKey: false,
    default: "",
    ...over,
  }
}

/** A row nobody has typed in: it is not a column and not a mistake. */
export function isBlank(column: ColumnDraft): boolean {
  return (
    column.name.trim() === "" &&
    column.type.trim() === "" &&
    column.default.trim() === "" &&
    !column.notNull &&
    !column.primaryKey
  )
}

export type TableProblems = {
  /** What is wrong with the table's own name. */
  name?: string
  /** What is wrong with a column, by its key. */
  columns: Record<string, string>
  /** One line for the foot of the form; absent when the draft can be sent. */
  summary?: string
}

/**
 * What stops a new table from being asked about.
 *
 * A row with a name and no type — or a type and no name — is typed work, not
 * a blank: the old form left such a row out of the statement and made the
 * table without it. Here it stops the form and is named, and only a row with
 * nothing in it at all is passed over.
 */
export function tableProblems(draft: TableDraft): TableProblems {
  const problems: TableProblems = { columns: {} }
  if (draft.name.trim() === "") problems.name = "Name the table."
  const seen = new Map<string, string>()
  let filled = 0
  for (const column of draft.columns) {
    if (isBlank(column)) continue
    const name = column.name.trim()
    if (name === "") problems.columns[column.key] = "This column has no name."
    else if (column.type.trim() === "") problems.columns[column.key] = `${name} has no type.`
    else {
      const folded = name.toLowerCase()
      if (seen.has(folded)) problems.columns[column.key] = `${name} is named twice.`
      else seen.set(folded, column.key)
      filled += 1
    }
  }
  const unfinished = Object.keys(problems.columns).length
  if (problems.name) problems.summary = problems.name
  else if (unfinished > 0) {
    problems.summary =
      unfinished === 1
        ? Object.values(problems.columns)[0]
        : `${unfinished} columns are not finished.`
  } else if (filled === 0) problems.summary = "Add at least one column."
  return problems
}

/** `POST /ddl/table` for a draft, or null while something stops it. */
export function tableRequest(draft: TableDraft): DdlRequest | null {
  if (tableProblems(draft).summary) return null
  return {
    method: "POST",
    path: "/ddl/table",
    body: {
      schema: draft.schema,
      table: draft.name.trim(),
      columns: draft.columns.filter((column) => !isBlank(column)).map(columnBody),
    },
  }
}

function columnBody(
  column: Pick<ColumnDraft, "name" | "type" | "notNull" | "primaryKey" | "default">,
) {
  const body: Record<string, unknown> = { name: column.name.trim(), type: column.type.trim() }
  if (column.notNull) body.notNull = true
  if (column.primaryKey) body.primaryKey = true
  if (column.default.trim() !== "") body.default = column.default.trim()
  return body
}

export type KeyPreset = {
  /** The preset's own word on its button. */
  label: string
  /** What the key does by itself, said under the columns once it is added. */
  says: string
  column: Pick<ColumnDraft, "name" | "type" | "notNull" | "primaryKey" | "default">
}

/**
 * The key column a new table usually starts with, in the engine's own words.
 *
 * The old form offered one "id" for every engine and wrote `bigint PRIMARY
 * KEY`, which numbers itself on none of them. A key that fills itself is
 * spelled differently everywhere, and the route takes a column as a type and
 * nothing after it — so what can be offered is what a *type* can say. The
 * engine's own type list (the server's, from the driver catalogue) is what
 * tells them apart: a list that has `bigserial` takes it; one that counts
 * `bigint unsigned` among its types reads `serial` as an auto-numbered one;
 * one whose integer is the affinity `INTEGER` numbers an `INTEGER` primary
 * key as the row id; one with `uniqueidentifier` can fill a key with `NEWID()`
 * but cannot be given an IDENTITY through this form. An engine none of this
 * fits gets no preset rather than a key that only looks like one.
 */
export function keyPreset(types: readonly string[]): KeyPreset | null {
  const has = (type: string) => types.includes(type)
  const key = (type: string, fill = "") => ({
    name: "id",
    type,
    notNull: false,
    primaryKey: true,
    default: fill,
  })
  if (has("bigserial")) {
    return {
      label: "id, numbered",
      says: "id is numbered by the engine: a row added without one takes the next number.",
      column: key("bigserial"),
    }
  }
  if (has("bigint unsigned")) {
    return {
      label: "id, numbered",
      says: "id is numbered by the engine: serial is an unsigned bigint that counts up by itself.",
      column: key("serial"),
    }
  }
  if (has("INTEGER")) {
    return {
      label: "id, numbered",
      says: "id is the row id: an INTEGER primary key is numbered by the engine.",
      column: key("INTEGER"),
    }
  }
  if (has("uniqueidentifier")) {
    return {
      label: "id, self-filling",
      says: "id fills itself with a new identifier. A numbered key (IDENTITY) cannot be written by this form: make that table in Query.",
      column: key("uniqueidentifier", "NEWID()"),
    }
  }
  return null
}

/* ---------------------------------------------------------------- columns */

export function addColumnRequest(
  schema: string,
  table: string,
  column: Pick<ColumnDraft, "name" | "type" | "notNull" | "default">,
): DdlRequest | null {
  if (column.name.trim() === "" || column.type.trim() === "") return null
  return {
    method: "POST",
    path: "/ddl/column",
    body: { schema, table, column: columnBody({ ...column, primaryKey: false }) },
  }
}

export type ColumnEdit = {
  type: string
  /** PostgreSQL's conversion expression for the values already stored. */
  using: string
  nullable: boolean
  /** "" with a default on the column removes it. */
  default: string
}

export function columnEdit(column: DbColumn): ColumnEdit {
  return { type: column.type, using: "", nullable: column.nullable, default: column.default ?? "" }
}

/** What an edit changes, in the words the dialog says them. */
export function columnChanges(column: DbColumn, edit: ColumnEdit) {
  const before = column.default ?? ""
  return {
    type: edit.type.trim() !== "" && edit.type.trim() !== column.type,
    nullable: edit.nullable !== column.nullable,
    default: edit.default.trim() !== before.trim(),
  }
}

/**
 * `PATCH /ddl/column` holding only what the edit changes, or null when it
 * changes nothing. A part left as it was is left out, which is the route's
 * "keep": a default the catalogue reads back in a form the route would not
 * accept again (`nextval(…)`, a cast) is then never sent back.
 */
export function alterColumnRequest(
  schema: string,
  table: string,
  column: DbColumn,
  edit: ColumnEdit,
): DdlRequest | null {
  const changes = columnChanges(column, edit)
  if (!changes.type && !changes.nullable && !changes.default) return null
  const body: Record<string, unknown> = { schema, table, name: column.name }
  if (changes.type) {
    body.type = edit.type.trim()
    if (edit.using.trim() !== "") body.using = edit.using.trim()
  }
  if (changes.nullable) body.nullable = edit.nullable
  if (changes.default) {
    if (edit.default.trim() === "") body.dropDefault = true
    else body.default = edit.default.trim()
  }
  return { method: "PATCH", path: "/ddl/column", body }
}

export function renameColumnRequest(
  schema: string,
  table: string,
  column: string,
  to: string,
): DdlRequest | null {
  const next = to.trim()
  if (next === "" || next === column) return null
  return {
    method: "POST",
    path: "/ddl/rename",
    body: { schema, table, kind: "column", name: column, to: next },
  }
}

export function renameTableRequest(schema: string, table: string, to: string): DdlRequest | null {
  const next = to.trim()
  if (next === "" || next === table) return null
  return { method: "POST", path: "/ddl/rename", body: { schema, table, to: next } }
}

export function dropColumnRequest(schema: string, table: string, name: string): DdlRequest {
  return { method: "DELETE", path: "/ddl/column", body: { schema, table, name } }
}

export function dropTableRequest(schema: string, table: string): DdlRequest {
  return { method: "DELETE", path: "/ddl/table", body: { schema, table } }
}

export function truncateRequest(schema: string, table: string): DdlRequest {
  return { method: "POST", path: "/ddl/truncate", body: { schema, table } }
}

/** `comment` empty removes the comment; the same as before changes nothing. */
export function commentRequest(
  schema: string,
  table: string,
  column: string | undefined,
  comment: string,
  before: string,
): DdlRequest | null {
  if (comment.trim() === before.trim()) return null
  const body: Record<string, unknown> = { schema, table, comment: comment.trim() }
  if (column) body.column = column
  return { method: "POST", path: "/ddl/comment", body }
}

/* ---------------------------------------------------------------- indexes */

export type IndexDraft = {
  name: string
  fields: string[]
  unique: boolean
  method: string
  where: string
  concurrently: boolean
  ifNotExists: boolean
}

export const BLANK_INDEX: IndexDraft = {
  name: "",
  fields: [],
  unique: false,
  method: "",
  where: "",
  concurrently: false,
  ifNotExists: false,
}

export function indexRequest(schema: string, table: string, draft: IndexDraft): DdlRequest | null {
  if (draft.fields.length === 0) return null
  const body: Record<string, unknown> = { schema, table, fields: draft.fields }
  if (draft.name.trim() !== "") body.name = draft.name.trim()
  if (draft.unique) body.unique = true
  if (draft.method.trim() !== "") body.method = draft.method.trim()
  if (draft.where.trim() !== "") body.where = draft.where.trim()
  if (draft.concurrently) body.concurrently = true
  if (draft.ifNotExists) body.ifNotExists = true
  return { method: "POST", path: "/ddl/index", body }
}

export function dropIndexRequest(schema: string, table: string, name: string): DdlRequest {
  return { method: "DELETE", path: "/ddl/index", body: { schema, table, name } }
}

/* ------------------------------------------------------ keys, constraints */

export const REFERENCE_ACTIONS = [
  "NO ACTION",
  "RESTRICT",
  "CASCADE",
  "SET NULL",
  "SET DEFAULT",
] as const

export type ForeignKeyDraft = {
  name: string
  /** This table's columns, paired in order with `refColumns`. */
  columns: string[]
  refSchema: string
  refTable: string
  refColumns: string[]
  onDelete: string
  onUpdate: string
}

export function foreignKeyRequest(
  schema: string,
  table: string,
  draft: ForeignKeyDraft,
): DdlRequest | null {
  const pairs = draft.columns.length
  if (pairs === 0 || draft.refTable === "" || draft.refColumns.length !== pairs) return null
  if (draft.columns.some((c) => c === "") || draft.refColumns.some((c) => c === "")) return null
  const body: Record<string, unknown> = {
    schema,
    table,
    columns: draft.columns,
    refTable: draft.refTable,
    refColumns: draft.refColumns,
  }
  if (draft.refSchema !== "" && draft.refSchema !== schema) body.refSchema = draft.refSchema
  if (draft.name.trim() !== "") body.name = draft.name.trim()
  // The engine's own default is NO ACTION: saying it adds nothing to the statement's meaning.
  if (draft.onDelete !== "" && draft.onDelete !== "NO ACTION") body.onDelete = draft.onDelete
  if (draft.onUpdate !== "" && draft.onUpdate !== "NO ACTION") body.onUpdate = draft.onUpdate
  return { method: "POST", path: "/ddl/foreign-key", body }
}

export function dropForeignKeyRequest(schema: string, table: string, name: string): DdlRequest {
  return { method: "DELETE", path: "/ddl/foreign-key", body: { schema, table, name } }
}

export type ConstraintDraft = {
  type: "unique" | "check"
  name: string
  columns: string[]
  expression: string
}

export function constraintRequest(
  schema: string,
  table: string,
  draft: ConstraintDraft,
): DdlRequest | null {
  const body: Record<string, unknown> = { schema, table, type: draft.type }
  if (draft.type === "unique") {
    if (draft.columns.length === 0) return null
    body.columns = draft.columns
  } else {
    if (draft.expression.trim() === "") return null
    body.expression = draft.expression.trim()
  }
  if (draft.name.trim() !== "") body.name = draft.name.trim()
  return { method: "POST", path: "/ddl/constraint", body }
}

/** `type` is what MySQL needs to pick its statement; the primary key has none to give. */
export function dropConstraintRequest(
  schema: string,
  table: string,
  name: string,
  type?: "unique" | "check",
): DdlRequest {
  const body: Record<string, unknown> = { schema, table, name }
  if (type) body.type = type
  return { method: "DELETE", path: "/ddl/constraint", body }
}

/* ------------------------------------------------- views, schemas, types */

export type ViewDraft = {
  schema: string
  name: string
  query: string
  replace: boolean
  materialized: boolean
}

export function viewRequest(draft: ViewDraft): DdlRequest | null {
  if (draft.name.trim() === "" || draft.query.trim() === "") return null
  const body: Record<string, unknown> = {
    schema: draft.schema,
    name: draft.name.trim(),
    // One statement is what the route takes: the closing semicolon a pasted
    // query ends in is the reader's habit, not a second statement.
    query: draft.query.trim().replace(/;\s*$/, ""),
  }
  if (draft.replace) body.replace = true
  if (draft.materialized) body.materialized = true
  return { method: "POST", path: "/ddl/view", body }
}

export function dropViewRequest(schema: string, name: string, materialized: boolean): DdlRequest {
  const body: Record<string, unknown> = { schema, name }
  if (materialized) body.materialized = true
  return { method: "DELETE", path: "/ddl/view", body }
}

/**
 * The query inside a view's CREATE statement: what follows the first `AS`
 * after the word VIEW, outside any quoted name. Null when the text is not a
 * statement this can read — the form then opens empty rather than on a guess.
 */
export function viewQuery(definition: string): string | null {
  const head = /\bVIEW\b/i.exec(definition)
  if (!head) return null
  const closers: Record<string, string> = { '"': '"', "`": "`", "[": "]", "'": "'" }
  let i = head.index + head[0].length
  while (i < definition.length) {
    const close = closers[definition[i]]
    if (close) {
      i += 1
      while (i < definition.length && definition[i] !== close) i += 1
      i += 1
      continue
    }
    if (/^AS\b/i.test(definition.slice(i, i + 3)) && /\s/.test(definition[i - 1] ?? " ")) {
      const query = definition
        .slice(i + 2)
        .trim()
        .replace(/;\s*$/, "")
      return query === "" ? null : query
    }
    i += 1
  }
  return null
}

export function createSchemaRequest(name: string): DdlRequest | null {
  return name.trim() === ""
    ? null
    : { method: "POST", path: "/ddl/schema", body: { name: name.trim() } }
}

export function dropSchemaRequest(name: string): DdlRequest {
  return { method: "DELETE", path: "/ddl/schema", body: { name } }
}

/** The labels of an enum as typed, one to a line: blank lines are not labels. */
export function enumLabels(text: string): string[] {
  return text
    .split("\n")
    .map((line) => line.trim())
    .filter((line) => line !== "")
}

/** What is wrong with a list of labels, or nothing. */
export function enumProblem(labels: readonly string[]): string | undefined {
  const seen = new Set<string>()
  for (const label of labels) {
    if (seen.has(label)) return `${label} is listed twice.`
    seen.add(label)
    if (new TextEncoder().encode(label).length > 63) return `${label} is longer than 63 bytes.`
  }
  return undefined
}

export function createEnumRequest(schema: string, name: string, text: string): DdlRequest | null {
  const values = enumLabels(text)
  if (name.trim() === "" || values.length === 0 || enumProblem(values)) return null
  return { method: "POST", path: "/ddl/enum", body: { schema, name: name.trim(), values } }
}

export function addEnumValueRequest(
  schema: string,
  name: string,
  value: string,
  /** Where it goes: last, or before or after a label the type already has. */
  place: { at: "end" } | { at: "before" | "after"; label: string },
): DdlRequest | null {
  if (value.trim() === "") return null
  const body: Record<string, unknown> = { schema, name, value: value.trim() }
  if (place.at !== "end") body[place.at] = place.label
  return { method: "POST", path: "/ddl/enum/value", body }
}

/* ----------------------------------------------------------------- limits */

type Operation = string

/**
 * What the engine's forms cannot do to a table, as sentences. The driver
 * catalogue lists the changes each engine can be asked for (`ddlOperations`);
 * a change it does not list has no control here, and the reader is told so
 * once, where the control would be — rather than finding out from an error.
 */
export function tableLimits(
  operations: readonly Operation[],
  engine: string,
): { columns: string[]; indexes: string[]; keys: string[] } {
  const lacks = (operation: Operation) => !operations.includes(operation)
  const columns: string[] = []
  const indexes: string[] = []
  const keys: string[] = []
  if (lacks("alterColumn")) {
    columns.push(
      `${engine} cannot change a column in place: its type, whether it takes NULL and its default stay as they were declared.`,
    )
  }
  if (lacks("columnComments")) columns.push(`${engine} keeps no comments on columns.`)
  if (lacks("createIndex")) {
    indexes.push(`An index on a ${engine} table is made in Query: this form cannot write one.`)
  }
  if (lacks("foreignKeys")) {
    keys.push(`Foreign keys cannot be added to or dropped from a ${engine} table here.`)
  }
  if (lacks("uniqueConstraints") && !lacks("createIndex")) {
    keys.push(`${engine} takes no unique constraint after a table is made: add a unique index.`)
  }
  if (lacks("checkConstraints")) {
    keys.push(`Check constraints cannot be added to a ${engine} table here.`)
  }
  return { columns, indexes, keys }
}
