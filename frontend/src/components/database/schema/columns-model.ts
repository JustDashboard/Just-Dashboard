import type { DbColumn, DbTableDetail } from "@/components/database/data/types"

/** Below this a table's columns are read down, a column to a block, instead of across. */
export const COLUMNS_NARROW = 640

/** What a column holds when nothing is written to it: its default, or `= expression` where it is computed. */
export function columnDefault(column: Pick<DbColumn, "generated" | "default">): string {
  return column.generated ? `= ${column.generated}` : (column.default ?? "")
}

/**
 * The marks of a table's columns, read once from its keys and indexes: the
 * key's word ("primary", or "sorting key" where the engine's key does not tell
 * rows apart but orders them), a unique index of its own, and the column a
 * foreign key points at.
 */
export function columnMarks(
  detail: Pick<DbTableDetail, "schema" | "primaryKey" | "foreignKeys" | "indexes">,
  rowIdentity: string | boolean,
) {
  const keyWord = rowIdentity === "none" ? "sorting key" : "primary"
  const foreign = new Map(
    detail.foreignKeys.flatMap((key) =>
      key.columns.map((column, index) => [column, { key, index }] as const),
    ),
  )
  const unique = new Set(
    detail.indexes
      .filter(
        (index) => index.unique && !index.primary && index.columns.length === 1 && !index.predicate,
      )
      .map((index) => index.columns[0]),
  )
  return (column: Pick<DbColumn, "name">) => {
    const reference = foreign.get(column.name)
    return {
      key: detail.primaryKey.includes(column.name) ? keyWord : undefined,
      unique: unique.has(column.name),
      reference: reference && {
        schema: reference.key.refSchema ?? detail.schema,
        table: reference.key.refTable,
        column: reference.key.refColumns[reference.index],
      },
    }
  }
}
