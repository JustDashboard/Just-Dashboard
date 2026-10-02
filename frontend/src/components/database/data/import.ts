import { bytes } from "@/lib/format"
import type { DbImportColumn, DbNewColumn, DbTableDetail } from "@/components/database/data/types"

/**
 * The parts of an import that are arithmetic: which keys an upsert can match
 * rows by, what a new table is called and made of, and how far an upload has
 * got — with no React in them.
 */

export interface UpsertKey {
  /** What the picker calls it: "Primary key", or the index's own name. */
  label: string
  columns: string[]
  primary: boolean
}

/**
 * What "add or update" can find an existing row by: the primary key first,
 * then every unique index or constraint over plain columns. An index over an
 * expression, or one that covers only some rows, is not a key the server takes.
 */
export function upsertKeys(
  detail: Pick<DbTableDetail, "primaryKey" | "indexes" | "constraints">,
): UpsertKey[] {
  const keys: UpsertKey[] = []
  const seen = new Set<string>()
  const add = (label: string, columns: readonly string[], primary: boolean) => {
    const signature = columns.join("\u0000")
    if (columns.length === 0 || seen.has(signature)) return
    seen.add(signature)
    keys.push({ label, columns: [...columns], primary })
  }
  add("Primary key", detail.primaryKey, true)
  for (const index of detail.indexes) {
    if (!index.unique || index.primary || index.expression || index.predicate || index.invalid) {
      continue
    }
    add(index.name, index.columns, false)
  }
  for (const constraint of detail.constraints) {
    if (constraint.type === "unique") add(constraint.name, constraint.columns, false)
  }
  return keys
}

/** The longest name every engine here takes for a table. */
const NAME_MAX = 63

/**
 * A table name read off a file's name: `Orders 2026-Q1.csv` → `orders_2026_q1`.
 * Only a suggestion — the reader types over it — so it is the plainest name
 * that needs no quoting anywhere.
 */
export function tableNameFrom(fileName: string): string {
  const stem = fileName.replace(/\.[A-Za-z0-9]{1,6}$/, "")
  const plain = stem
    .normalize("NFKD")
    .replace(/[̀-ͯ]/g, "")
    .toLowerCase()
    .replace(/[^a-z0-9_]+/g, "_")
    .replace(/^_+|_+$/g, "")
  if (plain === "") return "imported"
  return (/^\d/.test(plain) ? `t_${plain}` : plain).slice(0, NAME_MAX)
}

/** What the reader changed about one column of a table to be made, by its name in the file. */
export interface ColumnOverride {
  type?: string
  key?: boolean
}

/**
 * The columns of a new table that are not left to the server: the ones whose
 * type the reader wrote or that are part of the key. Named as the table will
 * name them — the mapping's word where there is one — and never for a column
 * that is left out of the import.
 */
export function newColumns(
  columns: readonly Pick<DbImportColumn, "source" | "target">[],
  overrides: Readonly<Record<string, ColumnOverride>>,
): DbNewColumn[] {
  return columns.flatMap((column) => {
    const override = overrides[column.source]
    if (!override || column.target === "") return []
    const type = (override.type ?? "").trim()
    const key = override.key === true
    if (type === "" && !key) return []
    return [{ name: column.target, type, notNull: key, primaryKey: key }]
  })
}

/** How far a file has got, as the dialog's foot says it. */
export function uploadProgress(
  sent: number,
  total: number,
): { percent: number; words: string; sending: boolean } {
  if (total <= 0) return { percent: 0, words: "Sending the file…", sending: true }
  if (sent >= total) {
    return {
      percent: 100,
      words: `${bytes(total)} sent. The rows are being written, in one request.`,
      sending: false,
    }
  }
  const percent = Math.floor((sent / total) * 100)
  return {
    percent,
    words: `Sending the file: ${bytes(sent)} of ${bytes(total)}, ${percent}%`,
    sending: true,
  }
}
