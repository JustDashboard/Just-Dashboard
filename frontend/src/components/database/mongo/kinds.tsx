import {
  ChartActivity,
  Eye,
  Layers,
  RotateClockwise,
  SettingsGear,
  type Icon,
} from "@/components/icons"
import { cn } from "@/lib/utils"
import { Tag } from "@/components/tag"
import { KIND_HUE } from "@/components/database/grid/legend"
import { TYPE_LABEL, type BsonType } from "@/components/database/mongo/bson"
import type { MongoCollection } from "@/components/database/mongo/types"

/**
 * The two legends of the MongoDB pages: what kind of thing a collection is,
 * and what type a value has.
 *
 * Both draw from the `--tag-*` set, which sits at one lightness so no kind
 * reads louder than another, and neither borrows green, amber or red — on
 * these pages those mean a field added, changed and removed.
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

/* -------------------------------------------------------- collection kinds */

export type CollectionKind = "collection" | "view" | "timeseries" | "capped" | "system"

type CollectionKindSpec = { label: string; icon: Icon; hue: string }

export const COLLECTION_KINDS: Record<CollectionKind, CollectionKindSpec> = {
  collection: { label: "Collection", icon: Layers, hue: "text-(--tag-blue)" },
  // The same hue a view has among SQL objects: one kind, one colour, across engines.
  view: { label: "View", icon: Eye, hue: "text-(--tag-violet)" },
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
