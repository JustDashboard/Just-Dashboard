import {
  dotted,
  nodeAt,
  printCanonical,
  printShell,
  shellKey,
  type BsonNode,
  type Segment,
} from "@/components/database/mongo/bson"

/**
 * The edits staged on one document before they are sent.
 *
 * A field edited in place is not a replacement of the document: it is an
 * update that names the fields it touches and leaves every other one alone.
 * The edits are held here, by path, until the reader sends them — and what
 * is sent is the update they are (`$set` for a value given or changed,
 * `$unset` for a field removed), together with a filter that holds the
 * document to what was read: its `_id`, and the old value of every field
 * about to change. A document somebody else changed in the meantime matches
 * nothing, and the page says so instead of writing over it.
 */
export type Edit =
  { op: "set"; path: Segment[]; value: BsonNode } | { op: "unset"; path: Segment[] }

/** Edits by the path they are on, written as JSON so a path is one key. */
export type Edits = Readonly<Record<string, Edit>>

export const NO_EDITS: Edits = {}

export const pathKey = (path: readonly Segment[]) => JSON.stringify(path)

const startsWith = (path: readonly Segment[], prefix: readonly Segment[]) =>
  prefix.length <= path.length && prefix.every((segment, index) => path[index] === segment)

/**
 * The edits with one more. An edit of a field replaces the one already
 * staged on it, and takes with it any staged below it: a field that is being
 * removed, or given a whole new value, has no parts left to change.
 */
export function withEdit(edits: Edits, edit: Edit): Edits {
  const next: Record<string, Edit> = {}
  for (const [key, held] of Object.entries(edits)) {
    if (startsWith(held.path, edit.path)) continue
    next[key] = held
  }
  next[pathKey(edit.path)] = edit
  return next
}

/** The edits without the one on this path. */
export function withoutEdit(edits: Edits, path: readonly Segment[]): Edits {
  const key = pathKey(path)
  if (!Object.hasOwn(edits, key)) return edits
  return Object.fromEntries(Object.entries(edits).filter(([held]) => held !== key))
}

/** What is staged on a path itself, and whether a field above it is being removed or replaced. */
export function editAt(edits: Edits, path: readonly Segment[]) {
  const own = edits[pathKey(path)] as Edit | undefined
  const above = Object.values(edits).find(
    (held) => held.path.length < path.length && startsWith(path, held.path),
  )
  return { own, above }
}

/** Fields staged under an object that the document does not have yet. */
export function addedUnder(edits: Edits, root: BsonNode, parent: readonly Segment[]): Edit[] {
  return Object.values(edits).filter(
    (held) =>
      held.op === "set" &&
      held.path.length === parent.length + 1 &&
      startsWith(held.path, parent) &&
      nodeAt(root, held.path) === undefined,
  )
}

export type EditCounts = { changed: number; added: number; removed: number }

export function countEdits(edits: Edits, root: BsonNode): EditCounts {
  const counts = { changed: 0, added: 0, removed: 0 }
  for (const edit of Object.values(edits)) {
    if (edit.op === "unset") counts.removed++
    else if (nodeAt(root, edit.path) === undefined) counts.added++
    else counts.changed++
  }
  return counts
}

/** "2 changed, 1 added": the staged edits in words. */
export function editSummary(counts: EditCounts): string {
  return [
    counts.changed && `${counts.changed} changed`,
    counts.added && `${counts.added} added`,
    counts.removed && `${counts.removed} removed`,
  ]
    .filter(Boolean)
    .join(", ")
}

/** Types an equality in a filter cannot be trusted to match by value. */
const UNGUARDED = new Set(["regex", "undefined", "minKey", "maxKey", "javascript", "dbPointer"])

export type Update = {
  /** `_id`, and the old value of each field the update touches. */
  filter: string
  /** The update operators, as canonical Extended JSON. */
  update: string
  /** The same update as the shell writes it, for the reader. */
  statement: string
}

/**
 * The one update the staged edits are. `null` when there is nothing to send
 * or when a path cannot be named in an update (the field editor does not
 * offer an edit there, so this is a guard and not a state a reader reaches).
 */
export function buildUpdate(id: string, root: BsonNode, edits: Edits): Update | null {
  const list = Object.values(edits)
  if (list.length === 0 || !id) return null
  const guards: string[] = [`"_id":${id}`]
  const sets: [string, BsonNode][] = []
  const unsets: string[] = []
  for (const edit of list) {
    const name = dotted(edit.path)
    if (name === null) return null
    const had = nodeAt(root, edit.path)
    if (had === undefined) guards.push(`${JSON.stringify(name)}:{"$exists":false}`)
    else if (!UNGUARDED.has(had.type)) {
      guards.push(`${JSON.stringify(name)}:${printCanonical(had)}`)
    }
    if (edit.op === "set") sets.push([name, edit.value])
    else unsets.push(name)
  }
  const operators: string[] = []
  const shown: string[] = []
  if (sets.length > 0) {
    operators.push(
      `"$set":{${sets.map(([name, value]) => `${JSON.stringify(name)}:${printCanonical(value)}`).join(",")}}`,
    )
    shown.push(
      `$set: { ${sets.map(([name, value]) => `${shellKey(name)}: ${printShell(value)}`).join(", ")} }`,
    )
  }
  if (unsets.length > 0) {
    operators.push(`"$unset":{${unsets.map((name) => `${JSON.stringify(name)}:""`).join(",")}}`)
    shown.push(`$unset: { ${unsets.map((name) => `${shellKey(name)}: ""`).join(", ")} }`)
  }
  return {
    filter: `{${guards.join(",")}}`,
    update: `{${operators.join(",")}}`,
    statement: `{ ${shown.join(", ")} }`,
  }
}
