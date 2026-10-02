import type { DbCatalogGroup } from "@/lib/types"
import type { DbObjectKind } from "@/components/database/schema/types"

/**
 * The kinds of object the catalogue names, in words: which branch of the
 * tree lists each, and what a sentence calls one. Kept apart from the glyphs
 * (`kinds.tsx`) so the address and the forms can ask without drawing anything.
 */

/** The branch each kind of object is listed under. */
const GROUP_OF: Record<DbObjectKind, DbCatalogGroup> = {
  table: "tables",
  view: "views",
  materialized_view: "materializedViews",
  dictionary: "dictionaries",
  function: "functions",
  procedure: "procedures",
  package: "packages",
  trigger: "triggers",
  event: "events",
  sequence: "sequences",
  enum: "types",
  domain: "types",
  composite: "types",
  range: "types",
  type: "types",
  synonym: "synonyms",
}

const KIND_WORDS: Record<DbObjectKind, string> = {
  table: "table",
  view: "view",
  materialized_view: "materialized view",
  dictionary: "dictionary",
  function: "function",
  procedure: "procedure",
  package: "package",
  trigger: "trigger",
  event: "event",
  sequence: "sequence",
  enum: "enum type",
  domain: "domain",
  composite: "composite type",
  range: "range type",
  type: "type",
  synonym: "synonym",
}

/** Whether a word from the address is a kind the catalogue has. */
export function isObjectKind(word: string): word is DbObjectKind {
  return Object.hasOwn(GROUP_OF, word)
}

export function groupOfKind(kind: DbObjectKind): DbCatalogGroup {
  return GROUP_OF[kind]
}

/** A kind in the words a sentence uses: "enum type", "materialized view". */
export function kindWord(kind: string): string {
  return isObjectKind(kind) ? KIND_WORDS[kind] : kind
}
