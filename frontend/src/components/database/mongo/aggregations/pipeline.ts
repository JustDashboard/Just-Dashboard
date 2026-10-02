import { scan, shapeProblem, textOf } from "@/components/database/mongo/shell"

/**
 * A pipeline as the builder holds it: a list of stages, each an operator and
 * the text of what it is given. The text is the reader's own — Extended JSON
 * or the shell's spelling — and is never parsed here beyond finding where a
 * stage begins and ends: the server reads it, and the server is the one that
 * says whether a pipeline writes.
 */
export type Stage = {
  /** Stable across edits and reorders: what a card is keyed by. */
  id: string
  /** The stage operator, with its `$`. */
  op: string
  /** What the operator is given, as typed. */
  body: string
  enabled: boolean
}

export type StageSpec = {
  op: string
  /** What the stage does, in one line. */
  hint: string
  /** What a new stage of this kind starts as. */
  snippet: string
}

/** The stages the builder offers, in the order a pipeline usually uses them. */
export const STAGES: readonly StageSpec[] = [
  { op: "$match", hint: "Keep the documents a filter matches", snippet: "{\n  \n}" },
  {
    op: "$group",
    hint: "Group by a key and compute over each group",
    snippet: '{\n  _id: "$field",\n  count: { $sum: 1 }\n}',
  },
  { op: "$project", hint: "Choose and reshape fields", snippet: "{\n  field: 1\n}" },
  { op: "$sort", hint: "Order the documents", snippet: "{\n  field: -1\n}" },
  { op: "$limit", hint: "Keep the first n documents", snippet: "10" },
  { op: "$skip", hint: "Leave out the first n documents", snippet: "0" },
  { op: "$addFields", hint: "Add computed fields", snippet: '{\n  field: "$other"\n}' },
  { op: "$set", hint: "Add or overwrite fields", snippet: '{\n  field: "$other"\n}' },
  { op: "$unset", hint: "Remove fields", snippet: '"field"' },
  { op: "$unwind", hint: "One document per element of a list", snippet: '{\n  path: "$field"\n}' },
  {
    op: "$lookup",
    hint: "Join documents of another collection",
    snippet:
      '{\n  from: "collection",\n  localField: "field",\n  foreignField: "_id",\n  as: "joined"\n}',
  },
  { op: "$count", hint: "Count the documents into one field", snippet: '"total"' },
  { op: "$sortByCount", hint: "Group by a value and count, largest first", snippet: '"$field"' },
  { op: "$sample", hint: "A random sample of n documents", snippet: "{\n  size: 10\n}" },
  { op: "$facet", hint: "Several pipelines over the same input", snippet: "{\n  name: [ ]\n}" },
  {
    op: "$bucket",
    hint: "Group into ranges you set",
    snippet: '{\n  groupBy: "$field",\n  boundaries: [0, 10, 100],\n  default: "other"\n}',
  },
  {
    op: "$bucketAuto",
    hint: "Group into n even ranges",
    snippet: '{\n  groupBy: "$field",\n  buckets: 5\n}',
  },
  {
    op: "$replaceRoot",
    hint: "Make a nested document the document",
    snippet: '{\n  newRoot: "$field"\n}',
  },
  { op: "$replaceWith", hint: "Replace each document with a value", snippet: '"$field"' },
  {
    op: "$unionWith",
    hint: "Add the documents of another collection",
    snippet: '{\n  coll: "collection",\n  pipeline: [ ]\n}',
  },
  {
    op: "$graphLookup",
    hint: "Follow references through a collection",
    snippet:
      '{\n  from: "collection",\n  startWith: "$field",\n  connectFromField: "field",\n  connectToField: "_id",\n  as: "path"\n}',
  },
  {
    op: "$setWindowFields",
    hint: "Compute over a window of neighbouring documents",
    snippet:
      '{\n  partitionBy: "$field",\n  sortBy: { at: 1 },\n  output: { running: { $sum: "$value", window: { documents: ["unbounded", "current"] } } }\n}',
  },
  {
    op: "$densify",
    hint: "Fill gaps in a sequence",
    snippet: '{\n  field: "at",\n  range: { step: 1, unit: "hour", bounds: "full" }\n}',
  },
  {
    op: "$fill",
    hint: "Fill missing values",
    snippet: '{\n  output: { field: { method: "locf" } }\n}',
  },
  {
    op: "$redact",
    hint: "Keep or prune parts of each document",
    snippet: '{\n  $cond: { if: { $eq: ["$level", 1] }, then: "$$DESCEND", else: "$$PRUNE" }\n}',
  },
  {
    op: "$geoNear",
    hint: "Documents by distance from a point",
    snippet: '{\n  near: { type: "Point", coordinates: [0, 0] },\n  distanceField: "distance"\n}',
  },
  { op: "$documents", hint: "Start from documents written here", snippet: "[\n  { x: 1 }\n]" },
  { op: "$collStats", hint: "Statistics of the collection", snippet: "{\n  storageStats: {}\n}" },
  { op: "$indexStats", hint: "How much each index is used", snippet: "{}" },
  { op: "$out", hint: "Write the result as a collection, replacing it", snippet: '"collection"' },
  {
    op: "$merge",
    hint: "Write the result into a collection",
    snippet: '{\n  into: "collection",\n  whenMatched: "merge",\n  whenNotMatched: "insert"\n}',
  },
]

const SPEC = new Map(STAGES.map((spec) => [spec.op, spec]))

export function stageSpec(op: string): StageSpec | undefined {
  return SPEC.get(op)
}

/** The stages the server knows to be reads. Any other is treated as writing, by it and so here. */
const READS = new Set(
  (
    "$addFields $bucket $bucketAuto $collStats $count $currentOp $densify $documents $facet " +
    "$fill $geoNear $graphLookup $group $indexStats $limit $listLocalSessions $listSampledQueries " +
    "$listSearchIndexes $listSessions $lookup $match $planCacheStats $project $querySettings " +
    "$queryStats $rankFusion $redact $replaceRoot $replaceWith $sample $scoreFusion $search " +
    "$searchMeta $set $setWindowFields $shardedDataDistribution $skip $sort $sortByCount " +
    "$unionWith $unset $unwind $vectorSearch"
  ).split(" "),
)

export function newStage(id: string, op = "$match"): Stage {
  return { id, op, body: stageSpec(op)?.snippet ?? "{\n  \n}", enabled: true }
}

/**
 * The stage with another operator. The body follows only while it is still
 * the old operator's untouched starting text: a body somebody typed is theirs.
 */
export function withOperator(stage: Stage, op: string): Stage {
  const pristine = stage.body.trim() === "" || stage.body === stageSpec(stage.op)?.snippet
  return { ...stage, op, body: pristine ? (stageSpec(op)?.snippet ?? stage.body) : stage.body }
}

/** What is wrong with a stage as typed, or `null`. The server's own refusal covers the rest. */
export function stageProblem(stage: Stage): string | null {
  if (!/^\$[A-Za-z]/.test(stage.op)) return "A stage operator begins with $."
  if (!stage.body.trim()) return `Write what ${stage.op} is given.`
  return shapeProblem(stage.body, "value")
}

export const enabledStages = (stages: readonly Stage[]) => stages.filter((stage) => stage.enabled)

/** The pipeline the server is sent: the enabled stages, each body exactly as typed. */
export function pipelineText(stages: readonly Stage[]): string {
  const parts = enabledStages(stages).map(
    (stage) => `{ ${JSON.stringify(stage.op)}: ${stage.body.trim()} }`,
  )
  return `[${parts.join(", ")}]`
}

/** Where a stage sits among the enabled ones — the index a preview asks for — or `-1` when it is off. */
export function previewIndex(stages: readonly Stage[], id: string): number {
  return enabledStages(stages).findIndex((stage) => stage.id === id)
}

export type PipelineKind = {
  /** The stage that writes, when the pipeline ends in one. */
  writes: "$out" | "$merge" | null
  /** Enabled stages the server does not know to be reads: it treats the pipeline as writing. */
  unknown: string[]
}

/**
 * Whether running the pipeline would write, read off the operators the
 * builder holds — never off the text. `$mergeObjects` inside a body is not a
 * `$merge` stage, and a stage whose operator is not on the server's list of
 * reads is one it will ask the destructive capability for.
 */
export function pipelineKind(stages: readonly Stage[]): PipelineKind {
  const enabled = enabledStages(stages)
  const writer = enabled.find((stage) => stage.op === "$out" || stage.op === "$merge")
  return {
    writes: writer ? (writer.op as "$out" | "$merge") : null,
    unknown: [
      ...new Set(
        enabled
          .map((stage) => stage.op)
          .filter((op) => op !== "$out" && op !== "$merge" && !READS.has(op)),
      ),
    ],
  }
}

const reindent = (body: string, by: string) =>
  body
    .trim()
    .split("\n")
    .map((line, index) => (index === 0 ? line : `${by}${line}`))
    .join("\n")

/** A collection as the shell names it. */
export function shellCollection(collection: string): string {
  return /^[A-Za-z_][\w]*$/.test(collection)
    ? `db.${collection}`
    : `db.getCollection(${JSON.stringify(collection)})`
}

/**
 * The pipeline as text a shell runs: every stage on its own lines, a stage
 * that is switched off kept as a comment so the text says what the builder
 * holds.
 */
export function exportText(collection: string, stages: readonly Stage[]): string {
  const lines = stages.map((stage) => {
    const text = `{ ${stage.op}: ${reindent(stage.body, "  ")} }`
    if (stage.enabled) return `  ${text},`
    return text
      .split("\n")
      .map((line) => `  // ${line}`)
      .join("\n")
  })
  return `${shellCollection(collection)}.aggregate([\n${lines.join("\n")}\n])`
}

/** The pipeline alone, as the text editor shows it: a list, one stage after another. */
export function listText(stages: readonly Stage[]): string {
  const parts = enabledStages(stages).map(
    (stage) => `  { ${stage.op}: ${reindent(stage.body, "  ")} }`,
  )
  return parts.length === 0 ? "[\n  \n]" : `[\n${parts.join(",\n")}\n]`
}

export type ParsedPipeline =
  { ok: true; stages: { op: string; body: string }[] } | { ok: false; message: string }

/**
 * Stages from pipeline text: a list of documents with one key each. The text
 * may be a whole `db.collection.aggregate([...])` call pasted from elsewhere;
 * the list inside it is what is read.
 */
export function parsePipeline(input: string): ParsedPipeline {
  let text = input.trim().replace(/;\s*$/, "")
  const call = /\.aggregate\s*\(/.exec(text)
  if (call) {
    const inner = text.slice(call.index + call[0].length)
    const close = inner.lastIndexOf(")")
    text = (close >= 0 ? inner.slice(0, close) : inner).trim()
  }
  if (!text) return { ok: true, stages: [] }
  const read = scan(text)
  if (!read.ok) {
    return {
      ok: false,
      message: `Line ${read.error.line}, column ${read.error.column}: ${read.error.message}.`,
    }
  }
  if (read.value.kind !== "array") return { ok: false, message: "A pipeline is a list: [ … ]." }
  const stages: { op: string; body: string }[] = []
  for (const [index, item] of read.value.items.entries()) {
    if (item.kind !== "document" || item.fields.length !== 1) {
      return {
        ok: false,
        message: `Stage ${index + 1} is not a document with one operator: { $match: { … } }.`,
      }
    }
    const field = item.fields[0]
    if (!field.key.startsWith("$")) {
      return { ok: false, message: `Stage ${index + 1}: ${field.key} is not a stage operator.` }
    }
    stages.push({ op: field.key, body: dedent(textOf(text, field.value)) })
  }
  return { ok: true, stages }
}

/** A body with the indentation its lines share taken off, the first line aside. */
function dedent(body: string): string {
  const lines = body.split("\n")
  if (lines.length < 2) return body
  const rest = lines.slice(1).filter((line) => line.trim())
  if (rest.length === 0) return body.trimEnd()
  // The closing bracket sits one step out from the fields: it sets the depth.
  const shared = Math.min(...rest.map((line) => /^[ \t]*/.exec(line)![0].length))
  return [lines[0], ...lines.slice(1).map((line) => line.slice(shared))].join("\n").trimEnd()
}

/** The stages with one moved a step towards the start or the end. */
export function moved(stages: readonly Stage[], id: string, by: -1 | 1): Stage[] {
  const from = stages.findIndex((stage) => stage.id === id)
  const to = from + by
  if (from < 0 || to < 0 || to >= stages.length) return [...stages]
  const next = [...stages]
  ;[next[from], next[to]] = [next[to], next[from]]
  return next
}

/* ------------------------------------------------------------------- saved */

export type SavedPipeline = {
  name: string
  stages: { op: string; body: string; enabled: boolean }[]
  /** ms since 1970. */
  savedAt: number
}

/** The saved pipelines with one more; one of the same name is replaced. Newest first. */
export function withSaved(list: readonly SavedPipeline[], entry: SavedPipeline): SavedPipeline[] {
  return [entry, ...list.filter((held) => held.name !== entry.name)]
}

export function withoutSaved(list: readonly SavedPipeline[], name: string): SavedPipeline[] {
  return list.filter((held) => held.name !== name)
}

/** Whether the builder's stages are the saved ones, unchanged. */
export function sameAsSaved(stages: readonly Stage[], saved: SavedPipeline | undefined): boolean {
  if (!saved || saved.stages.length !== stages.length) return false
  return stages.every((stage, index) => {
    const held = saved.stages[index]
    return held.op === stage.op && held.body === stage.body && held.enabled === stage.enabled
  })
}
