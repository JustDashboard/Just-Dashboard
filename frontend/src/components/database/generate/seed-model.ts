/**
 * Seed data, as the server plans and writes it (`POST /databases/{id}/seed/plan`
 * and `POST /databases/{id}/seed`). The server chooses each column's
 * generator, orders the tables parents first and makes the rows; what is here
 * is the request the page sends and the reading of what comes back, so that
 * the page never decides what a column can hold.
 */

export interface SeedChoice {
  id: string
  label: string
  group: string
}

export interface SeedReference {
  schema?: string
  table: string
  columns: string[]
  /** The same seed fills the parent first. */
  seeded: boolean
}

export interface SeedColumn {
  name: string
  type: string
  generator: string
  label: string
  /** What fills it when nobody chooses: a choice of it is no choice. */
  inferred: string
  /** What the reader may choose instead; empty where the server fills the column. */
  choices: SeedChoice[]
  nullable: boolean
  unique: boolean
  reference?: SeedReference
  /** What stands in the preview for a value the write decides. */
  later?: string
}

export interface SeedTable {
  schema: string
  name: string
  rows: number
  asked: number
  existing: number
  after: string[]
  columns: SeedColumn[]
  /** The first rows, a cell per column: text, or null. */
  preview: (string | null)[][]
  statement: string
  problem?: string
  notes: string[]
}

export interface SeedPlan {
  seed: number
  tables: SeedTable[]
  rows: number
  atomic: boolean
  warnings: string[]
}

export interface SeedRowError {
  row: number
  message: string
}

export interface SeedTableReport {
  schema: string
  name: string
  rows: number
  inserted: number
  skipped: number
  errors: SeedRowError[]
  problem?: string
  notes: string[]
}

export interface SeedReport {
  seed: number
  tables: SeedTableReport[]
  inserted: number
  skipped: number
  atomic: boolean
  warnings: string[]
}

/** A table as the outline lists it. */
export interface SeedTarget {
  schema: string
  name: string
}

/** Each table's chosen generators, by column; tables by `seedKey`. */
export type SeedChoices = Record<string, Record<string, string>>
/** Each table's own row count, where it differs from the one for all. */
export type SeedRows = Record<string, number>

export const DEFAULT_SEED_ROWS = 25
export const MAX_SEED_ROWS = 10000
export const MAX_SEED_TABLES = 100

const strings = (value: unknown): string[] =>
  Array.isArray(value) ? value.filter((v): v is string => typeof v === "string") : []

const number = (value: unknown, fallback = 0) =>
  typeof value === "number" && Number.isFinite(value) ? value : fallback

export function seedKey(schema: string, name: string): string {
  return `${schema}\u0000${name}`
}

function readColumn(raw: unknown): SeedColumn | undefined {
  const c = raw as Partial<SeedColumn> | null
  if (!c || typeof c.name !== "string") return undefined
  const reference = c.reference as Partial<SeedReference> | undefined
  return {
    name: c.name,
    type: typeof c.type === "string" ? c.type : "",
    generator: typeof c.generator === "string" ? c.generator : "",
    label: typeof c.label === "string" ? c.label : "",
    inferred:
      typeof c.inferred === "string" && c.inferred
        ? c.inferred
        : typeof c.generator === "string"
          ? c.generator
          : "",
    choices: Array.isArray(c.choices)
      ? c.choices.filter(
          (choice): choice is SeedChoice =>
            typeof choice?.id === "string" && typeof choice?.label === "string",
        )
      : [],
    nullable: c.nullable === true,
    unique: c.unique === true,
    reference:
      reference && typeof reference.table === "string"
        ? {
            schema: reference.schema,
            table: reference.table,
            columns: strings(reference.columns),
            seeded: reference.seeded === true,
          }
        : undefined,
    later: typeof c.later === "string" && c.later ? c.later : undefined,
  }
}

export function readSeedPlan(answer: unknown): SeedPlan {
  const given = (answer ?? {}) as Partial<SeedPlan>
  const tables = Array.isArray(given.tables) ? given.tables : []
  return {
    seed: number(given.seed),
    rows: number(given.rows),
    atomic: given.atomic !== false,
    warnings: strings(given.warnings),
    tables: tables
      .filter((t) => typeof t?.name === "string")
      .map((t) => {
        const columns = (Array.isArray(t.columns) ? t.columns : [])
          .map(readColumn)
          .filter((c): c is SeedColumn => c !== undefined)
        return {
          schema: typeof t.schema === "string" ? t.schema : "",
          name: t.name,
          rows: number(t.rows),
          asked: number(t.asked, number(t.rows)),
          existing: number(t.existing, -1),
          after: strings(t.after),
          columns,
          preview: (Array.isArray(t.preview) ? t.preview : []).map((row) =>
            columns.map((_, i) => {
              const cell = Array.isArray(row) ? row[i] : null
              return typeof cell === "string" ? cell : null
            }),
          ),
          statement: typeof t.statement === "string" ? t.statement : "",
          problem: typeof t.problem === "string" && t.problem ? t.problem : undefined,
          notes: strings(t.notes),
        }
      }),
  }
}

export function readSeedReport(answer: unknown): SeedReport {
  const given = (answer ?? {}) as Partial<SeedReport>
  const tables = Array.isArray(given.tables) ? given.tables : []
  return {
    seed: number(given.seed),
    inserted: number(given.inserted),
    skipped: number(given.skipped),
    atomic: given.atomic !== false,
    warnings: strings(given.warnings),
    tables: tables
      .filter((t) => typeof t?.name === "string")
      .map((t) => ({
        schema: typeof t.schema === "string" ? t.schema : "",
        name: t.name,
        rows: number(t.rows),
        inserted: number(t.inserted),
        skipped: number(t.skipped),
        errors: (Array.isArray(t.errors) ? t.errors : [])
          .filter((e) => typeof e?.message === "string")
          .map((e) => ({ row: number(e.row), message: e.message })),
        problem: typeof t.problem === "string" && t.problem ? t.problem : undefined,
        notes: strings(t.notes),
      })),
  }
}

/** A row count as typed: a whole number the server takes, or nothing. */
export function rowsFrom(text: string): number | undefined {
  const trimmed = text.trim()
  if (!/^\d+$/.test(trimmed)) return undefined
  const n = Number(trimmed)
  return n >= 1 && n <= MAX_SEED_ROWS ? n : undefined
}

/** A new seed number: what makes the next rows different from the last. */
export function newSeed(random: () => number = Math.random): number {
  return 1 + Math.floor(random() * 999999)
}

/**
 * The request for the tables chosen. A table's own row count beats the one
 * for all, and only the generators the reader changed are sent, so a column
 * nobody touched keeps the one the server chooses for it.
 */
export function seedRequest({
  seed,
  targets,
  rows,
  perTable,
  choices,
}: {
  seed: number
  targets: readonly SeedTarget[]
  rows: number
  perTable: SeedRows
  choices: SeedChoices
}): Record<string, unknown> {
  return {
    seed,
    tables: targets.map((t) => {
      const key = seedKey(t.schema, t.name)
      const columns = choices[key]
      return {
        schema: t.schema,
        name: t.name,
        rows: Object.hasOwn(perTable, key) ? perTable[key] : rows,
        ...(columns && Object.keys(columns).length > 0 && { columns }),
      }
    }),
  }
}

/**
 * The generators chosen once the reader picks one for a column: the choice
 * is kept only where it differs from what the server chose before any was
 * made, so picking the first one again forgets the change.
 */
export function chooseGenerator(
  choices: SeedChoices,
  key: string,
  column: string,
  generator: string,
  inferred: string | undefined,
): SeedChoices {
  const table = { ...choices[key] }
  if (generator === inferred) delete table[column]
  else table[column] = generator
  const next = { ...choices }
  if (Object.keys(table).length === 0) delete next[key]
  else next[key] = table
  return next
}

/**
 * The choices once a plan has answered for them: one the server set aside —
 * its column dropped, retyped, or now filled by the server — is forgotten,
 * so it is said once and not sent with every plan after. Only what the
 * request that plan answers carried is judged; a choice made since is kept.
 */
export function pruneChoices(
  choices: SeedChoices,
  asked: SeedChoices,
  plan: SeedPlan,
): SeedChoices {
  let next = choices
  for (const table of plan.tables) {
    const key = seedKey(table.schema, table.name)
    const sent = asked[key]
    if (!sent || !choices[key]) continue
    for (const [column, generator] of Object.entries(sent)) {
      const held = table.columns.find((c) => c.name === column)
      if (choices[key][column] !== generator || held?.generator === generator) continue
      const rest = { ...next[key] }
      delete rest[column]
      next = { ...next }
      if (Object.keys(rest).length === 0) delete next[key]
      else next[key] = rest
    }
  }
  return next
}

/** What a request asked each table's columns to be filled with. */
export function requestedChoices(body: string): SeedChoices {
  const parsed = JSON.parse(body) as {
    tables?: { schema?: string; name?: string; columns?: Record<string, string> }[]
  }
  const out: SeedChoices = {}
  for (const t of parsed.tables ?? []) {
    if (t.columns && typeof t.name === "string") out[seedKey(t.schema ?? "", t.name)] = t.columns
  }
  return out
}

/** A column's choices by group, in the order the server listed them. */
export function choiceGroups(choices: readonly SeedChoice[]): {
  group: string
  choices: SeedChoice[]
}[] {
  const groups: { group: string; choices: SeedChoice[] }[] = []
  for (const choice of choices) {
    const held = groups.find((g) => g.group === choice.group)
    if (held) held.choices.push(choice)
    else groups.push({ group: choice.group, choices: [choice] })
  }
  return groups
}

const count = (n: number, one: string, many = `${one}s`) =>
  `${n.toLocaleString("en-US")} ${n === 1 ? one : many}`

/** What a plan writes, in a line: "4 tables · 100 rows". */
export function planLine(plan: SeedPlan): string {
  const written = plan.tables.filter((t) => !t.problem && t.rows > 0).length
  const left = plan.tables.length - written
  return [
    count(written, "table"),
    count(plan.rows, "row"),
    left > 0 ? `${count(left, "table")} left out` : "",
  ]
    .filter(Boolean)
    .join(" · ")
}

/** What a seed inserted, as a notice's title. */
export function reportTitle(report: SeedReport): string {
  const tables = report.tables.filter((t) => t.inserted > 0).length
  if (report.inserted === 0) return "Nothing was inserted"
  return `Inserted ${count(report.inserted, "row")} into ${count(tables, "table")}`
}

/** One table of a report, in a line: "25 inserted · 2 refused". */
export function reportLine(table: SeedTableReport): string {
  if (table.problem) return "left out"
  return [
    `${table.inserted.toLocaleString("en-US")} inserted`,
    table.skipped > 0 ? `${table.skipped.toLocaleString("en-US")} refused` : "",
  ]
    .filter(Boolean)
    .join(" · ")
}

/** The tables a seed fills: the ones ticked, or every one when none is. */
export function seedTargets<T extends SeedTarget>(
  all: readonly T[],
  picked: readonly string[],
  nameOf: (t: T) => string,
): T[] {
  if (picked.length === 0) return [...all]
  return all.filter((t) => picked.includes(nameOf(t)))
}

/** How a table is named to the reader: bare, or with its schema where several are read. */
export function tableLabel(t: SeedTarget, several: boolean): string {
  return several && t.schema ? `${t.schema}.${t.name}` : t.name
}
