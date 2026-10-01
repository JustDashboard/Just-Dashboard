import type { GridColumnKind } from "./types"

/**
 * The legend: which sanctioned `--tag-*` hue a kind of value is drawn in.
 *
 * Text and numbers keep the foreground — they are most of any table, and a
 * grid where everything is coloured is a grid where nothing is. Green, amber
 * and red are left out on purpose: in this grid those three hues mean a row
 * was added, a cell was changed and a row is going, and a boolean drawn green
 * beside an inserted row would be two meanings for one colour.
 *
 * It is the one legend for a value wherever one is drawn — a cell here, a
 * field of a document, a key's value (`ValueText` and `JsonTree` in the kit
 * read it) — so a kind keeps its colour across a database's pages.
 */
export const KIND_HUE: Record<GridColumnKind, string> = {
  number: "numeric",
  text: "",
  unknown: "",
  boolean: "text-(--tag-violet)",
  datetime: "text-(--tag-cyan)",
  date: "text-(--tag-cyan)",
  time: "text-(--tag-cyan)",
  json: "text-(--tag-blue)",
  array: "text-(--tag-blue)",
  enum: "text-(--tag-pink)",
  uuid: "text-(--tag-slate)",
  binary: "text-(--tag-slate)",
}
