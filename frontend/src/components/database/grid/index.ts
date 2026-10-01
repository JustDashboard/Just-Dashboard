/**
 * The data grid: the one primitive every surface that shows rows is built on.
 *
 * Import from here. The files beside this one are the grid's own parts and may
 * be rearranged; what is exported below is the module's contract.
 */

export { DataGrid } from "./data-grid"
export type { DataGridHandle, DataGridProps } from "./data-grid"

export { GridColumnsMenu } from "./grid-menus"
export type { CopyFormat } from "./grid-menus"

export {
  applyChange,
  buildChanges,
  changeCounts,
  changeSetReducer,
  duplicateValues,
  EMPTY_CHANGE_STATE,
  EMPTY_CHANGES,
  isEmpty as isEmptyChangeSet,
  isNewRowId,
  rowKey,
  useChangeSet,
} from "./change-set"
export type {
  CellEdit,
  Change,
  ChangeAction,
  ChangeColumn,
  ChangeCounts,
  ChangeRef,
  ChangeSet,
  ChangeSetController,
  ChangeSetState,
  ChangeSetStore,
  ChangesPayload,
  ChangesTarget,
  DeletedRow,
  InsertedRow,
  RowInsertion,
  RowOrigin,
  RowRemoval,
  RowSnapshot,
  UpdatedRow,
} from "./change-set"

export { useGridLayout } from "./use-grid-layout"
export {
  EMPTY_LAYOUT,
  moveColumn,
  pruneLayout,
  setColumnHidden,
  setColumnPinned,
  setColumnWidth,
} from "./layout"

export { EMPTY_SELECTION } from "./selection"
export { cycleSort } from "./sort"
export { columnKind, kindFromServer, kindFromType } from "./kinds"
export { describeAggregate, describeStatus } from "./status"
export type { GridStatus } from "./status"
export type { Aggregate } from "./decimal"

export { clipText, parseTSV, toCSV, toJSONRows, toTSV } from "./clipboard"
export {
  DEFAULT_VALUE,
  formatCell,
  isDefault,
  isTruncatedValue,
  parseInput,
  sameValue,
} from "./values"
export type { CellDisplay, EditValue, ParseResult } from "./values"

export type {
  CellValue,
  ClippedCell,
  GridCellRef,
  GridColumn,
  GridColumnKind,
  GridFilterRequest,
  GridForeignKey,
  GridForeignKeyTarget,
  GridLayout,
  GridRange,
  GridRow,
  GridRowRef,
  GridSelection,
  GridSelectionData,
  GridSort,
  GridSortKey,
} from "./types"
