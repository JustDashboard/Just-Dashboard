import type { Engine } from "@/components/database/engine"

/**
 * What the backend's contracts say of each engine that the driver catalogue
 * does not yet state: how it quotes a name and asks for a relation's first
 * rows, what a foreign key may do when the row it points at goes, and which
 * index methods the forms' route accepts.
 *
 * The catalogue's flags say which forms an engine has (`ddlOperations`); they
 * do not say which *choices* a form may offer, and an engine's limit must be
 * stated where the choice is made, not found by a refusal. Until those facts
 * are on the registry (`engine.ts`) or in the catalogue — the request is in
 * the area's contract — they are kept here, once, as data keyed by the
 * registry's driver id. Nothing else in the schema browser or the diagram
 * names an engine, and no page compares a driver: they ask `notesFor(engine)`.
 *
 * An engine nobody has described gets the plainest reading — double-quoted
 * names, `LIMIT`, no foreign-key or index choices — which is what the forms
 * then leave out rather than guess.
 */
export type ReferenceAction = "NO ACTION" | "RESTRICT" | "CASCADE" | "SET NULL" | "SET DEFAULT"

export type EngineNotes = {
  /** The pair a name is written between. */
  quote: readonly [string, string]
  /** A statement reading the first `n` rows of a relation already quoted. */
  firstRows: (relation: string, n: number) => string
  /** What a new foreign key may do when the row it points at is deleted; none = the clause is not taken. */
  onDelete: readonly ReferenceAction[]
  /** … and when that row's key is changed. */
  onUpdate: readonly ReferenceAction[]
  /** The index methods the route takes for this engine, in its own words. */
  indexMethods: readonly string[]
  /** Any other access method the engine has installed is taken too (an extension's). */
  otherIndexMethods: boolean
}

const limit = (relation: string, n: number) => `SELECT * FROM ${relation} LIMIT ${n}`

const EVERY_ACTION: readonly ReferenceAction[] = [
  "NO ACTION",
  "RESTRICT",
  "CASCADE",
  "SET NULL",
  "SET DEFAULT",
]

const PLAIN: EngineNotes = {
  quote: ['"', '"'],
  firstRows: limit,
  onDelete: [],
  onUpdate: [],
  indexMethods: [],
  otherIndexMethods: false,
}

const NOTES: Record<string, EngineNotes> = {
  postgres: {
    ...PLAIN,
    onDelete: EVERY_ACTION,
    onUpdate: EVERY_ACTION,
    indexMethods: ["btree", "hash", "gin", "gist", "spgist", "brin"],
    otherIndexMethods: true,
  },
  mysql: {
    ...PLAIN,
    quote: ["`", "`"],
    // The parser reads SET DEFAULT and the storage engine then refuses the table.
    onDelete: ["NO ACTION", "RESTRICT", "CASCADE", "SET NULL"],
    onUpdate: ["NO ACTION", "RESTRICT", "CASCADE", "SET NULL"],
    indexMethods: ["btree", "hash", "fulltext", "spatial"],
  },
  sqlite: PLAIN,
  sqlserver: {
    ...PLAIN,
    quote: ["[", "]"],
    firstRows: (relation, n) => `SELECT TOP (${n}) * FROM ${relation}`,
    // It has no RESTRICT: NO ACTION is its equivalent.
    onDelete: ["NO ACTION", "CASCADE", "SET NULL", "SET DEFAULT"],
    onUpdate: ["NO ACTION", "CASCADE", "SET NULL", "SET DEFAULT"],
    indexMethods: ["nonclustered", "clustered"],
  },
  clickhouse: { ...PLAIN, quote: ["`", "`"] },
  oracle: {
    ...PLAIN,
    firstRows: (relation, n) => `SELECT * FROM ${relation} FETCH FIRST ${n} ROWS ONLY`,
    onDelete: ["NO ACTION", "CASCADE", "SET NULL"],
    // A key that is referenced cannot be made to follow a change: there is no ON UPDATE.
    onUpdate: [],
    indexMethods: ["bitmap"],
  },
}

/** The notes of a connection's engine; one nobody has described is read as plain SQL. */
export function notesFor(engine: Pick<Engine, "driver">): EngineNotes {
  return Object.hasOwn(NOTES, engine.driver) ? NOTES[engine.driver] : PLAIN
}

/** A name as the engine reads it back unchanged: always quoted, its closing mark doubled inside. */
export function quoteName(name: string, notes: Pick<EngineNotes, "quote">): string {
  const [open, close] = notes.quote
  return `${open}${name.split(close).join(close + close)}${close}`
}

/** `schema.name`, each part quoted; the name alone where the engine has no schema to say. */
export function quotedRelation(
  schema: string,
  name: string,
  notes: Pick<EngineNotes, "quote">,
): string {
  const bare = quoteName(name, notes)
  return schema ? `${quoteName(schema, notes)}.${bare}` : bare
}
