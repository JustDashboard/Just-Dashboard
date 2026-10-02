import { ChartActivity, Layers, RotateClockwise, SettingsGear, type Icon } from "@/components/icons"
import { cn } from "@/lib/utils"
import { Tag } from "@/components/tag"
import { ROW_OBJECT_KINDS } from "@/components/database/data/kinds"
import { KIND_HUE } from "@/components/database/grid/legend"
import { statementVerb } from "@/components/database/home/kinds"
import { TYPE_LABEL, type BsonType } from "@/components/database/mongo/bson"
import type { MongoCollection } from "@/components/database/mongo/types"

/**
 * The legends of the MongoDB pages: what kind of thing a collection is, what
 * type a value has, and what an operation does.
 *
 * All draw from the `--tag-*` set, which sits at one lightness so no kind
 * reads louder than another, and none is a reading of state. A value is
 * never written in green, amber or red: beside a document those mean a
 * field added, changed and removed. (Green is the fill of text in a
 * composition bar, on a page where nothing is being edited.)
 */

/* ----------------------------------------------------------- BSON families */

/**
 * A value's type, grouped the way a reader tells them apart at a glance. The
 * hue is one per family: every number is the number hue in a composition bar,
 * and its exact type is the word beside it.
 */
export type TypeFamily =
  "text" | "number" | "boolean" | "moment" | "id" | "nested" | "nothing" | "other"

type FamilySpec = {
  /** The hue as a bare colour, for a bar's segment or a legend's dot. */
  color: string
  /** The class a value of this family is written in. Text and plain numbers keep the foreground. */
  text: string
}

// The value classes are the grid's own legend wherever the two name the same
// kind, so a moment or a document is one colour as a cell and as a field.
const FAMILY: Record<TypeFamily, FamilySpec> = {
  text: { color: "var(--tag-green)", text: KIND_HUE.text },
  number: { color: "var(--tag-pink)", text: KIND_HUE.number },
  boolean: { color: "var(--tag-violet)", text: KIND_HUE.boolean },
  moment: { color: "var(--tag-cyan)", text: KIND_HUE.datetime },
  id: { color: "var(--tag-slate)", text: KIND_HUE.uuid },
  nested: { color: "var(--tag-blue)", text: KIND_HUE.json },
  nothing: { color: "var(--muted-foreground)", text: "text-muted-foreground/70" },
  other: { color: "var(--tag-slate)", text: KIND_HUE.binary },
}

const FAMILY_OF: Record<BsonType, TypeFamily> = {
  string: "text",
  symbol: "text",
  int: "number",
  double: "number",
  long: "number",
  decimal: "number",
  bool: "boolean",
  date: "moment",
  timestamp: "moment",
  objectId: "id",
  object: "nested",
  array: "nested",
  null: "nothing",
  undefined: "nothing",
  minKey: "nothing",
  maxKey: "nothing",
  binData: "other",
  regex: "other",
  javascript: "other",
  dbPointer: "other",
}

/** The `$type` aliases the server's schema analysis uses that are not this file's own words. */
const ALIASES: Record<string, BsonType> = {
  javascriptWithScope: "javascript",
  bool: "bool",
  boolean: "bool",
}

/** A type by the word the server or a tree uses for it; an unknown word is an "other" of its own name. */
export function typeInfo(type: string): { label: string; family: TypeFamily } & FamilySpec {
  const known = Object.hasOwn(FAMILY_OF, type)
    ? (type as BsonType)
    : Object.hasOwn(ALIASES, type)
      ? ALIASES[type]
      : undefined
  const family = known ? FAMILY_OF[known] : "other"
  return { label: known ? TYPE_LABEL[known] : type, family, ...FAMILY[family] }
}

/**
 * The class a value is written in. A 64-bit integer and a Decimal128 take the
 * number hue where an Int32 and a Double keep the foreground: they are the
 * two a JavaScript number cannot hold, and the two whose spelling in the
 * shell is a call rather than a digit.
 */
export function valueClass(type: BsonType): string {
  if (type === "long" || type === "decimal") return "numeric text-(--tag-pink)"
  return FAMILY[FAMILY_OF[type]].text
}

/** A type as a small word in its family's hue. */
export function TypeTag({ type, className }: { type: string; className?: string }) {
  const info = typeInfo(type)
  return (
    <Tag data-type={type} className={className} style={{ color: info.color }}>
      {info.label}
    </Tag>
  )
}

/* ------------------------------------------------------------- operations */

/** What an operation does to the data, whatever the server calls it. */
export type OperationEffect = "read" | "insert" | "change" | "remove"

// The hue the section gives a statement's first word wherever statements are
// listed — a read blue, an insert green, a change violet, a removal pink —
// taken from that one legend rather than written out again.
const EFFECT_VERB: Record<OperationEffect, string> = {
  read: "select",
  insert: "insert",
  change: "update",
  remove: "delete",
}

/** The colour of an effect, as the statement legend has it. */
export function effectColor(effect: OperationEffect): string | undefined {
  return statementVerb(EFFECT_VERB[effect])?.color
}

const OPERATION_EFFECT: Record<string, OperationEffect> = {
  query: "read",
  getmore: "read",
  command: "read",
  count: "read",
  distinct: "read",
  insert: "insert",
  update: "change",
  remove: "remove",
  delete: "remove",
}

/** The colour of an operation by the word the profiler and `currentOp` use for it; none for a word they do not. */
export function operationColor(op: string): string | undefined {
  return Object.hasOwn(OPERATION_EFFECT, op) ? effectColor(OPERATION_EFFECT[op]) : undefined
}

/* -------------------------------------------------------- collection kinds */

export type CollectionKind = "collection" | "view" | "timeseries" | "capped" | "system"

type CollectionKindSpec = { label: string; icon: Icon; hue: string }

export const COLLECTION_KINDS: Record<CollectionKind, CollectionKindSpec> = {
  collection: { label: "Collection", icon: Layers, hue: "text-(--tag-blue)" },
  // The same hue a view has among SQL objects: one kind, one colour, across engines.
  view: { label: "View", icon: ROW_OBJECT_KINDS.view.icon, hue: ROW_OBJECT_KINDS.view.hue },
  timeseries: { label: "Time series", icon: ChartActivity, hue: "text-(--tag-cyan)" },
  capped: { label: "Capped", icon: RotateClockwise, hue: "text-(--tag-pink)" },
  system: { label: "System", icon: SettingsGear, hue: "text-muted-foreground" },
}

/** What a collection is drawn as: what it is first, then how it is kept. */
export function collectionKind(
  collection: Pick<MongoCollection, "type" | "capped" | "system">,
): CollectionKind {
  if (collection.type === "view") return "view"
  if (collection.type === "timeseries") return "timeseries"
  if (collection.system) return "system"
  return collection.capped ? "capped" : "collection"
}

/** A kind's glyph in its hue, at a line's height. */
export function CollectionMark({ kind, className }: { kind: CollectionKind; className?: string }) {
  const spec = COLLECTION_KINDS[kind]
  return <spec.icon aria-hidden className={cn("size-3.5 shrink-0", spec.hue, className)} />
}

/** A kind as a word in its hue; nothing for an ordinary collection, which is the unmarked case. */
export function CollectionKindTag({
  kind,
  className,
}: {
  kind: CollectionKind
  className?: string
}) {
  if (kind === "collection") return null
  const spec = COLLECTION_KINDS[kind]
  return (
    <Tag data-kind={kind} className={cn(spec.hue, className)}>
      {spec.label}
    </Tag>
  )
}
