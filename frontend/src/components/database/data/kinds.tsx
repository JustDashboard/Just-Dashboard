import { BookOpen, Eye, Layers, Table, type Icon } from "@/components/icons"
import { cn } from "@/lib/utils"
import type { DbCatalogGroup } from "@/lib/types"

/**
 * The kinds of object that hold rows, as the table editor draws them: one
 * glyph and one hue each, the same in the rail, in the strip over the grid
 * and in a foreign key's target, so a view is told from a table before its
 * name is read.
 *
 * The hues are the `--tag-*` set, which sits at one lightness, and none of
 * them is a status colour: a table is not "fine" and a view is not "warning".
 * The legend is this area's own until the section has one file for it
 * (`kit/kinds.ts`, which the design brief names and nobody has written yet).
 */
export type RowObjectKind = "table" | "view" | "materialized view" | "dictionary"

type KindSpec = { label: string; plural: string; icon: Icon; hue: string }

export const ROW_OBJECT_KINDS: Record<RowObjectKind, KindSpec> = {
  table: { label: "Table", plural: "Tables", icon: Table, hue: "text-(--tag-blue)" },
  view: { label: "View", plural: "Views", icon: Eye, hue: "text-(--tag-violet)" },
  "materialized view": {
    label: "Materialized view",
    plural: "Materialized views",
    icon: Layers,
    hue: "text-(--tag-cyan)",
  },
  dictionary: {
    label: "Dictionary",
    plural: "Dictionaries",
    icon: BookOpen,
    hue: "text-(--tag-pink)",
  },
}

/** The catalogue's groups that hold rows, in the rail's order, and the kind each lists. */
export const ROW_GROUPS: readonly { group: DbCatalogGroup; kind: RowObjectKind }[] = [
  { group: "tables", kind: "table" },
  { group: "views", kind: "view" },
  { group: "materializedViews", kind: "materialized view" },
  { group: "dictionaries", kind: "dictionary" },
]

/**
 * The kind a table's own `type` word names. The server has several words for
 * a thing that is a table to its reader (a partition, a foreign table); what
 * matters here is which of the four it is drawn as.
 */
export function rowObjectKind(type: string | undefined): RowObjectKind {
  const word = (type ?? "table").toLowerCase()
  if (word === "view") return "view"
  if (word === "materialized view" || word === "materialized_view") return "materialized view"
  if (word === "dictionary") return "dictionary"
  return "table"
}

/** A kind's glyph in its hue, at a line's height. */
export function KindGlyph({ kind, className }: { kind: RowObjectKind; className?: string }) {
  const spec = ROW_OBJECT_KINDS[kind]
  return <spec.icon aria-hidden className={cn("size-3.5 shrink-0", spec.hue, className)} />
}
