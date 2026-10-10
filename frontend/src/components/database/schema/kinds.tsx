import {
  Box,
  Clock,
  Code,
  Hash,
  Lightning,
  Link,
  ListOrdered,
  Puzzle,
  type Icon,
} from "@/components/icons"
import { cn } from "@/lib/utils"
import type { DbCatalogGroup } from "@/lib/types"
import { ROW_GROUPS, ROW_OBJECT_KINDS } from "@/components/database/data/kinds"

/**
 * Every kind of object a schema holds, as the schema browser draws it: one
 * glyph and one hue per branch of the tree, the same in the rail, in the head
 * of the object and wherever one object names another.
 *
 * The four kinds that hold rows are the table editor's own legend
 * (`data/kinds.tsx`), read from there so a view is the same violet eye on
 * both pages. This file adds the kinds that hold no rows. The hues are the
 * `--tag-*` set — one lightness, none of them a reading of state — and where
 * two branches share one they are of one family and never stand in the same
 * tree without their glyphs telling them apart: what runs (functions,
 * procedures, packages) is green, what fires by itself (triggers, events) is
 * amber, what only names or counts (sequences, synonyms) is slate.
 */
export type GroupSpec = {
  /** One of them: "Function". */
  label: string
  /** The branch's heading: "Functions". */
  plural: string
  icon: Icon
  /** A text-colour class in the kind's hue. */
  hue: string
  /** Holds rows: opens as a table, with columns, and has a place in Data. */
  rows: boolean
}

const ROWLESS: Partial<Record<DbCatalogGroup, Omit<GroupSpec, "rows">>> = {
  functions: { label: "Function", plural: "Functions", icon: Code, hue: "text-(--tag-green)" },
  procedures: {
    label: "Procedure",
    plural: "Procedures",
    icon: ListOrdered,
    hue: "text-(--tag-green)",
  },
  packages: { label: "Package", plural: "Packages", icon: Box, hue: "text-(--tag-green)" },
  triggers: { label: "Trigger", plural: "Triggers", icon: Lightning, hue: "text-(--tag-amber)" },
  events: { label: "Event", plural: "Events", icon: Clock, hue: "text-(--tag-amber)" },
  sequences: { label: "Sequence", plural: "Sequences", icon: Hash, hue: "text-(--tag-slate)" },
  types: { label: "Type", plural: "Types", icon: Puzzle, hue: "text-(--tag-pink)" },
  synonyms: { label: "Synonym", plural: "Synonyms", icon: Link, hue: "text-(--tag-slate)" },
}

/** The legend of one branch of the catalogue. */
export function groupSpec(group: DbCatalogGroup): GroupSpec {
  const row = ROW_GROUPS.find((entry) => entry.group === group)
  if (row) return { ...ROW_OBJECT_KINDS[row.kind], rows: true }
  const spec = ROWLESS[group]
  // A branch the server adds before this file learns of it: named as sent.
  return spec
    ? { ...spec, rows: false }
    : { label: group, plural: group, icon: Box, hue: "text-muted-foreground", rows: false }
}

export { groupOfKind, isObjectKind, kindWord } from "@/components/database/schema/kind-names"

/** A branch's glyph in its hue, at a line's height. */
export function GroupGlyph({ group, className }: { group: DbCatalogGroup; className?: string }) {
  const spec = groupSpec(group)
  return <spec.icon aria-hidden className={cn("size-3.5 shrink-0", spec.hue, className)} />
}
