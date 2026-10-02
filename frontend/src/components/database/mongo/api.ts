import { del, get, patch, post, postForm, put } from "@/lib/api"
import type {
  MongoAggregateResult,
  MongoClassification,
  MongoCollection,
  MongoCollections,
  MongoCommandResult,
  MongoCount,
  MongoCreateCollection,
  MongoCreateIndex,
  MongoDatabase,
  MongoDoc,
  MongoExplain,
  MongoExplainVerbosity,
  MongoFindResult,
  MongoFindSpec,
  MongoImportReport,
  MongoIndexes,
  MongoInsertResult,
  MongoOperation,
  MongoPreview,
  MongoProfiler,
  MongoProfilerLevel,
  MongoProfilerState,
  MongoReplication,
  MongoSchema,
  MongoServer,
  MongoValidation,
  MongoValidationAction,
  MongoValidationCheck,
  MongoValidationLevel,
  MongoVerdict,
  MongoWriteResult,
} from "@/components/database/mongo/types"

/**
 * Which collection a request is about. `database` left out is the one the
 * connection string names; the pages always name it once they know it.
 */
export type MongoTarget = { id: number; database: string; collection: string }

const at = (id: number) => `/databases/${id}`
const of = (target: MongoTarget) => ({
  database: target.database || undefined,
  collection: target.collection,
})

/**
 * An answer held to the one field the page cannot do without. A server that
 * answers one of these routes with something else — an older backend, a
 * proxy's page — is a failed read with a sentence, not a page that throws
 * half-way through drawing it.
 */
function shaped<T>(value: unknown, field: string, what: string): T {
  if (typeof value === "object" && value !== null && field in value) return value as T
  throw new Error(`The server's answer for ${what} could not be read.`)
}

/* ------------------------------------------------------------- collections */

export function mongoDatabases(id: number, signal?: AbortSignal) {
  return get<unknown>(`${at(id)}/mongo/databases`, undefined, signal).then(
    (answer) =>
      shaped<{ databases: MongoDatabase[] }>(answer, "databases", "the databases").databases,
  )
}

export function mongoCollections(id: number, database: string, signal?: AbortSignal) {
  return get<unknown>(
    `${at(id)}/mongo/collections`,
    { database: database || undefined },
    signal,
  ).then((answer) => shaped<MongoCollections>(answer, "collections", "the collections"))
}

export function mongoCollection(target: MongoTarget, signal?: AbortSignal) {
  return get<unknown>(`${at(target.id)}/mongo/collection`, of(target), signal).then(
    (answer) =>
      shaped<{ database: string; collection: MongoCollection }>(
        answer,
        "collection",
        "the collection",
      ).collection,
  )
}

export function createCollection(id: number, request: MongoCreateCollection) {
  return post<{ ok: true }>(`${at(id)}/mongo/collections`, request)
}

export function renameCollection(target: MongoTarget, to: string, dropTarget = false) {
  return post<{ ok: true }>(`${at(target.id)}/mongo/collections/rename`, {
    ...of(target),
    to,
    dropTarget: dropTarget || undefined,
  })
}

export function dropCollection(target: MongoTarget) {
  return del<{ ok: true }>(`${at(target.id)}/mongo/collections`, { body: of(target) })
}

/* --------------------------------------------------------------- documents */

export function findDocuments(
  target: MongoTarget,
  spec: MongoFindSpec,
  count: boolean,
  signal?: AbortSignal,
) {
  return post<unknown>(
    `${at(target.id)}/mongo/find`,
    { ...of(target), ...spec, count },
    { signal },
  ).then((answer) => shaped<MongoFindResult>(answer, "documents", "the documents"))
}

export function countDocuments(
  target: MongoTarget,
  spec: Pick<MongoFindSpec, "filter" | "collation" | "hint">,
  signal?: AbortSignal,
) {
  return post<unknown>(`${at(target.id)}/mongo/count`, { ...of(target), ...spec }, { signal }).then(
    (answer) => shaped<{ count: MongoCount }>(answer, "count", "the count").count,
  )
}

export function getDocument(target: MongoTarget, id: string, signal?: AbortSignal) {
  return post<unknown>(`${at(target.id)}/mongo/document`, { ...of(target), id }, { signal }).then(
    (answer) => shaped<{ document: MongoDoc }>(answer, "document", "the document").document,
  )
}

export function insertDocuments(target: MongoTarget, documents: string, ordered: boolean) {
  return post<MongoInsertResult>(`${at(target.id)}/mongo/documents`, {
    ...of(target),
    documents,
    ordered,
  })
}

/** Replace one document, only if it is still the one that was read (`expectedDigest`). */
export function replaceDocument(
  target: MongoTarget,
  id: string,
  document: string,
  expectedDigest: string | undefined,
) {
  return put<MongoWriteResult>(`${at(target.id)}/mongo/documents`, {
    ...of(target),
    id,
    document,
    expectedDigest,
  })
}

export type MongoUpdateRequest = {
  filter?: string
  update: string
  arrayFilters?: string
  many?: boolean
  upsert?: boolean
  dryRun?: boolean
  /** Required to update every document of the collection. */
  all?: boolean
}

export function updateDocuments(target: MongoTarget, request: MongoUpdateRequest) {
  return patch<MongoWriteResult>(`${at(target.id)}/mongo/documents`, { ...of(target), ...request })
}

export type MongoDeleteRequest = {
  id?: string
  filter?: string
  many?: boolean
  dryRun?: boolean
  /** Required to delete every document of the collection. */
  all?: boolean
}

export function deleteDocuments(target: MongoTarget, request: MongoDeleteRequest) {
  return del<MongoWriteResult>(`${at(target.id)}/mongo/documents`, {
    body: { ...of(target), ...request },
  })
}

export function cloneDocument(target: MongoTarget, id: string) {
  return post<{ document: MongoDoc }>(`${at(target.id)}/mongo/documents/clone`, {
    ...of(target),
    id,
  })
}

/* ----------------------------------------------------------------- indexes */

export function mongoIndexes(target: MongoTarget, signal?: AbortSignal) {
  return get<unknown>(`${at(target.id)}/mongo/indexes`, of(target), signal).then((answer) =>
    shaped<MongoIndexes>(answer, "indexes", "the indexes"),
  )
}

export function createIndex(id: number, request: MongoCreateIndex) {
  return post<{ name: string }>(`${at(id)}/mongo/indexes`, request)
}

export function hideIndex(target: MongoTarget, name: string, hidden: boolean) {
  return patch<{ ok: true }>(`${at(target.id)}/mongo/indexes`, { ...of(target), name, hidden })
}

export function dropIndex(target: MongoTarget, name: string) {
  return del<{ ok: true }>(`${at(target.id)}/mongo/indexes`, { body: { ...of(target), name } })
}

/* -------------------------------------------------------------- validation */

export function mongoValidation(target: MongoTarget, signal?: AbortSignal) {
  return get<unknown>(`${at(target.id)}/mongo/validation`, of(target), signal).then((answer) =>
    shaped<MongoValidation>(answer, "level", "the validation rule"),
  )
}

export function setValidation(
  target: MongoTarget,
  change: { validator?: string; level?: MongoValidationLevel; action?: MongoValidationAction },
) {
  return put<MongoValidation>(`${at(target.id)}/mongo/validation`, { ...of(target), ...change })
}

export function checkValidation(
  target: MongoTarget,
  validator: string | undefined,
  signal?: AbortSignal,
) {
  return post<MongoValidationCheck>(
    `${at(target.id)}/mongo/validation/check`,
    { ...of(target), validator, samples: 5 },
    { signal },
  )
}

/* ------------------------------------------------- aggregation and explain */

export function runPipeline(
  target: MongoTarget,
  pipeline: string,
  limit: number,
  signal?: AbortSignal,
) {
  return post<MongoAggregateResult>(
    `${at(target.id)}/aggregate`,
    { ...of(target), pipeline, limit, allowDiskUse: true },
    { signal },
  )
}

export function previewPipeline(
  target: MongoTarget,
  pipeline: string,
  stage: number,
  sample: number,
  signal?: AbortSignal,
) {
  return post<MongoPreview>(
    `${at(target.id)}/mongo/aggregate/preview`,
    { ...of(target), pipeline, stage, sample },
    { signal },
  )
}

export function explain(
  target: MongoTarget,
  request: ({ pipeline: string } | MongoFindSpec) & { verbosity: MongoExplainVerbosity },
  signal?: AbortSignal,
) {
  return post<unknown>(
    `${at(target.id)}/mongo/explain`,
    { ...of(target), ...request },
    { signal },
  ).then((answer) => shaped<MongoExplain>(answer, "plan", "the plan"))
}

export function analyseSchema(
  target: MongoTarget,
  request: { sample: number; filter?: string },
  signal?: AbortSignal,
) {
  return post<unknown>(
    `${at(target.id)}/mongo/schema`,
    { ...of(target), ...request },
    { signal },
  ).then((answer) => shaped<MongoSchema>(answer, "fields", "the schema"))
}

/* ------------------------------------------------------------------ server */

export function mongoServer(id: number, signal?: AbortSignal) {
  return get<unknown>(`${at(id)}/mongo/server`, undefined, signal).then((answer) =>
    shaped<MongoServer>(answer, "opcounters", "the server's counters"),
  )
}

export function mongoOperations(id: number, all: boolean, signal?: AbortSignal) {
  return get<unknown>(`${at(id)}/mongo/ops`, all ? { all: 1 } : undefined, signal).then(
    (answer) =>
      shaped<{ operations: MongoOperation[] }>(answer, "operations", "the operations").operations,
  )
}

export function killOperation(id: number, opId: string) {
  return post<{ ok: true }>(`${at(id)}/mongo/killop`, { opId })
}

export function mongoProfiler(id: number, database: string, signal?: AbortSignal) {
  return get<unknown>(
    `${at(id)}/mongo/profiler`,
    { database: database || undefined, limit: 200 },
    signal,
  ).then((answer) => shaped<MongoProfiler>(answer, "entries", "the profiler"))
}

export function setProfiler(
  id: number,
  database: string,
  change: { level?: MongoProfilerLevel; slowMs?: number; sampleRate?: number },
) {
  return put<MongoProfilerState>(`${at(id)}/mongo/profiler`, {
    database: database || undefined,
    ...change,
  })
}

export function mongoReplication(id: number, signal?: AbortSignal) {
  return get<unknown>(`${at(id)}/mongo/replication`, undefined, signal).then((answer) =>
    shaped<MongoReplication>(answer, "members", "the replica set"),
  )
}

/* ----------------------------------------------------------------- console */

export function classifyCommand(
  id: number,
  database: string,
  command: string,
  signal?: AbortSignal,
) {
  return post<MongoClassification>(
    `${at(id)}/mongo/command/classify`,
    { database: database || undefined, command },
    { signal },
  )
}

export function runCommand(id: number, database: string, command: string) {
  return post<MongoCommandResult>(`${at(id)}/mongo/command`, {
    database: database || undefined,
    command,
  })
}

export function mongoCommands(id: number, signal?: AbortSignal) {
  return get<unknown>(`${at(id)}/mongo/commands`, undefined, signal).then(
    (answer) => shaped<{ commands: MongoVerdict[] }>(answer, "commands", "the commands").commands,
  )
}

/* ------------------------------------------------------------------ import */

export type MongoImportOptions = {
  format?: "csv" | "tsv" | "json" | "ndjson"
  mode: "insert" | "replace"
  skipBadRows: boolean
  dryRun: boolean
}

/**
 * A file into a collection, through the section's own import route. The
 * options go first: the server reads the file as a stream and has to know
 * what to do with it before the first byte.
 */
export function importFile(
  target: MongoTarget,
  file: Blob,
  name: string,
  options: MongoImportOptions,
) {
  const form = new FormData()
  form.append(
    "options",
    JSON.stringify({ schema: target.database || undefined, table: target.collection, ...options }),
  )
  form.append("file", file, name)
  return postForm<MongoImportReport>(`${at(target.id)}/import/upload`, form)
}
