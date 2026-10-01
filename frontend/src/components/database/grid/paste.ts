import type { CellEdit, ChangeAction, RowInsertion } from "./change-set"
import { clipText } from "./clipboard"
import { editFor, lockReason, type GridModel } from "./model"
import { isRange, rangeOf } from "./selection"
import { parseInput, parsePasted, type EditValue, type ParseResult } from "./values"
import type { CellValue, GridColumn, GridRange, GridSelection } from "./types"

/**
 * What a paste would do, worked out before anything is staged.
 *
 * A block from a spreadsheet is laid down once from the first cell of the
 * selection: each field is read by the rules of the column it lands in, a cell
 * that cannot take its field is skipped and counted, and rows that run past
 * the end become new rows when the owner allows them. One value pasted over a
 * range fills the range. The result is a single batch — a paste is one step of
 * undo, however many cells it touched.
 */

/** A field from the clipboard: text from anywhere, or an exact value from another grid. */
export type PastedCell = { text: string } | { value: CellValue }

export interface PastePlan {
  /** What to stage. Empty when nothing could be pasted. */
  actions: ChangeAction[]
  /** Cells that will take a value. */
  pasted: number
  /** Cells that were refused. */
  skipped: number
  /** Why the first refused cell was refused. */
  reason: string
  /** The cells the paste covered, for selecting afterwards. Null for a fill, which keeps its range. */
  block: GridRange | null
}

function resolve(cell: PastedCell, column: GridColumn): ParseResult {
  if (!("value" in cell)) return parsePasted(cell.text, column)
  // The grid's own format: NULL is NULL and "" is "", so neither is guessed at.
  if (cell.value === null) {
    return column.nullable ? { ok: true, value: null } : { ok: false, error: "cannot be NULL" }
  }
  return parseInput(clipText(cell.value), column)
}

export function planPaste(
  model: GridModel,
  selection: GridSelection,
  matrix: readonly (readonly PastedCell[])[],
  options: { canInsert: boolean; newRowId: () => string },
): PastePlan | null {
  const cells = rangeOf(selection)
  if (!cells || matrix.length === 0) return null
  const count = model.ids.length
  const fill = matrix.length === 1 && matrix[0].length === 1 && isRange(selection)
  const height = fill ? cells.bottom - cells.top + 1 : matrix.length

  const edits: CellEdit[] = []
  const inserts: RowInsertion[] = []
  let pasted = 0
  let skipped = 0
  let reason = ""
  let right = cells.left
  const refuse = (why: string) => {
    skipped++
    reason ||= why
  }

  for (let r = 0; r < height; r++) {
    const source = fill ? matrix[0] : matrix[r]
    const width = fill ? cells.right - cells.left + 1 : source.length
    const row = cells.top + r
    const fresh: Record<string, EditValue> = {}
    for (let c = 0; c < width; c++) {
      const entry = model.ordered[cells.left + c]
      if (!entry) {
        refuse("There are more columns on the clipboard than in the grid")
        continue
      }
      const lock =
        row < count
          ? lockReason(model, row, entry)
          : !options.canInsert
            ? "Rows cannot be added here"
            : entry.column.generated
              ? `${entry.column.name} is computed by the database`
              : null
      if (lock) {
        refuse(lock)
        continue
      }
      const parsed = resolve(fill ? source[0] : source[c], entry.column)
      if (!parsed.ok) {
        refuse(`${entry.column.name}: ${parsed.error}`)
        continue
      }
      pasted++
      right = Math.max(right, cells.left + c)
      if (row < count) edits.push(editFor(model, row, entry, parsed.value))
      else fresh[entry.column.key] = parsed.value
    }
    if (row >= count && options.canInsert) {
      inserts.push({ id: options.newRowId(), values: fresh })
    }
  }

  const actions: ChangeAction[] = []
  if (inserts.length > 0) actions.push({ type: "insert", rows: inserts })
  if (edits.length > 0) actions.push({ type: "edit", edits })
  const bottom = Math.min(cells.top + height - 1, count + inserts.length - 1)
  return {
    actions,
    pasted,
    skipped,
    reason,
    block: fill || pasted === 0 ? null : { top: cells.top, left: cells.left, bottom, right },
  }
}
