"use client"

import {
  useCallback,
  useDeferredValue,
  useEffect,
  useId,
  useImperativeHandle,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from "react"
import { SearchInput } from "@/components/page"
import { PaneHeader } from "@/components/panel"
import { EmptyNote, ErrorState } from "@/components/state"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { ContextMenu, ContextMenuContent, ContextMenuTrigger } from "@/components/ui/context-menu"
import { errorMessage } from "@/lib/api"
import { copyText } from "@/lib/clipboard"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"
import {
  GridEditorContext,
  type EditMove,
  type GridEditorApi,
  type GridEditorHandle,
} from "./cell-editor"
import {
  duplicateValues,
  EMPTY_CHANGES,
  exceedsLimit,
  MAX_CHANGES,
  type CellEdit,
  type ChangeAction,
  type ChangeSetController,
  type RowInsertion,
} from "./change-set"
import { previewNote, toJSONRows } from "./clipboard"
import { hitTest, measureColumn, trackPointer, type Hit } from "./dom"
import { GridHeader, type ColumnActions } from "./grid-header"
import {
  CellMenuItems,
  GridColumnsMenu,
  type CellMenuActions,
  type CellMenuTarget,
  type CopyFormat,
} from "./grid-menus"
import { GridRowView, GridSkeletonRows } from "./grid-row"
import { GridStatusBar } from "./grid-status"
import {
  columnWidths,
  EMPTY_LAYOUT,
  HEADER_HEIGHT,
  orderColumns,
  resetColumnWidth,
  setColumnHidden,
  setColumnPinned,
  setColumnWidth,
} from "./layout"
import { useLive, type AfterRender, type Editing } from "./live"
import {
  actionBlock,
  defaultAllowed,
  editFor,
  insertedAt,
  isPreview,
  KEY_PREVIEW,
  keyIsPreview,
  keySources,
  lockReason,
  originOf,
  pendingOf,
  previewKeys,
  rowRecord,
  rowValues,
  selectedRows,
  targetRows,
  valueAt,
  type GridModel,
} from "./model"
import {
  allRowsState,
  clampSelection,
  EMPTY_SELECTION,
  extendTo,
  HEADER_ROW,
  isRange,
  moveCell,
  rangeContains,
  rangeOf,
  rangeSize,
  rowSpan,
  sameCell,
  selectCell,
  selectColumn,
  toggleRow,
  toggleRowRange,
  type Move,
} from "./selection"
import { cycleSort, findIndexes, setSort as withSort, sortedIndexes } from "./sort"
import { rangeAggregate, type GridStatus } from "./status"
import { useGridClipboard } from "./use-grid-clipboard"
import { useGridKeyboard } from "./use-grid-keyboard"
import { useHeaderGestures } from "./use-header-gestures"
import { DEFAULT_VALUE, holdsText, isDefault, parseInput, type EditValue } from "./values"
import { columnSlice, offsetsOf, revealOffset, rowSlice, sameSlice } from "./window"
import type {
  CellValue,
  ClippedCell,
  GridCellRef,
  GridColumn,
  GridFilterRequest,
  GridForeignKeyTarget,
  GridLayout,
  GridRow,
  GridRowRef,
  GridSelection,
  GridSelectionData,
  GridSort,
} from "./types"

/** Rows kept drawn above and below what shows, so a fast wheel never outruns a frame. */
const ROW_OVERSCAN = 8
const COLUMN_OVERSCAN = 2
const EMPTY_SORT: GridSort = []
const indexId = (_row: GridRow, index: number) => String(index)

/** What the owner can ask of a mounted grid. */
export interface DataGridHandle {
  /** Puts the keyboard in the grid, on the active cell. */
  focus: () => void
  /** Stages a new row and moves to it. Returns its temporary id, or null when rows cannot be added. */
  insertRow: (values?: Readonly<Record<string, EditValue>>) => string | null
  /** Stages copies of the ticked rows — or, with none ticked, of the rows the selection crosses. */
  duplicateSelected: () => void
  /** Marks the same rows for deletion. */
  deleteSelected: () => void
  /** Copies the selection in a given form. */
  copy: (format: CopyFormat) => void
  /** Brings a row into view and makes its first cell active. */
  revealRow: (index: number) => void
}

export interface DataGridProps {
  ref?: React.Ref<DataGridHandle>
  /** What the grid is, for a screen reader: "customers rows", "Query result". */
  label: string
  /**
   * The columns, in the order their values sit in a row. Memoise this: a new
   * array on every render is a new grid on every render.
   */
  columns: readonly GridColumn[]
  /** One array of wire values per row, aligned to `columns`. */
  rows: readonly GridRow[]
  /**
   * A stable id for a row — its primary key, joined. Defaults to the row's
   * index, which is right for a query result and for a table with no key.
   * Pass a stable function.
   */
  rowId?: (row: GridRow, index: number) => string
  /** Cells the server cut for display (`QueryResult.clipped`). They are never editable. */
  clipped?: readonly ClippedCell[]

  /** The sort in force. Omit to let the grid keep its own. */
  sort?: GridSort
  /** Makes column names sortable. With `sortMode="server"` the owner refetches. */
  onSortChange?: (sort: GridSort) => void
  /**
   * `"client"` orders the rows here — for a result that is already whole in
   * memory. `"server"` (the default) only reports the request: a page of a
   * table is never sorted on its own.
   */
  sortMode?: "server" | "client"
  /** Column order, widths, hidden and pinned. Omit to let the grid keep its own. */
  layout?: GridLayout
  onLayoutChange?: (layout: GridLayout) => void
  /** The active cell, the range and the ticked rows. Omit to let the grid keep its own. */
  selection?: GridSelection
  onSelectionChange?: (selection: GridSelection) => void

  /** The pending change set. Without one the grid has nowhere to put an edit and is read-only. */
  changeSet?: ChangeSetController
  /** Whether cells can be edited at all. */
  editable?: boolean
  /** Whether rows can be added — by a paste past the end, a duplicate, `insertRow`. */
  canInsert?: boolean
  /** Whether rows can be marked for deletion. False for a role without `destructive`. */
  canDelete?: boolean
  /**
   * Whether a row that already exists can be sent back to a column's default.
   * False for SQLite, which has no `SET column = DEFAULT`: "Default" is then
   * offered on new rows only, where it means leaving the column out.
   */
  defaultOnUpdate?: boolean
  /** Why this cannot be edited, stated above the rows: no primary key, a view, the engine. */
  readOnlyReason?: React.ReactNode
  /** Row id → why the server refused that row's change. Drawn in the gutter. */
  rowErrors?: Readonly<Record<string, string>>

  /** Offer a find box that narrows the rows in memory. For results, not for paged tables. */
  findable?: boolean
  find?: string
  onFindChange?: (find: string) => void
  /** Draw the Columns menu in the grid's strip. On by default. */
  columnsMenu?: boolean
  /** Draw the selector column. On by default. */
  selectable?: boolean

  /** Space on a row, Enter on a read-only row, or the menu: show this row in full. */
  onOpenRow?: (row: GridRowRef) => void
  /** "Filter by this value" and "Exclude this value" from a cell. */
  onFilter?: (request: GridFilterRequest) => void
  onFollowForeignKey?: (target: GridForeignKeyTarget) => void
  /** "Copy as SQL INSERT". The owner renders the statements: only the server knows the dialect. */
  onCopySQL?: (data: GridSelectionData) => void
  /** The status line as data, for an owner that draws it elsewhere. Pass a stable function. */
  onStatusChange?: (status: GridStatus) => void

  /** A fetch is in flight. With no rows yet, skeleton rows; with rows, they stay and a bar sweeps. */
  loading?: boolean
  /**
   * The fetch failed. With no rows it is drawn in their place, under the header;
   * with rows already on screen they stay, and the failure is said above them.
   */
  error?: Error | null
  onRetry?: () => void
  /** What to say when there are no rows. */
  empty?: React.ReactNode

  /** The owner's own controls, at the left of the grid's strip. */
  toolbar?: React.ReactNode
  /** The owner's own controls at the right of the status strip: pagination, a commit bar. */
  footer?: React.ReactNode
  /** Anything the owner needs between the strip and the rows. */
  banner?: React.ReactNode

  /** 28, 30 or 32 pixels. Every row is the same height. */
  rowHeight?: number
  /** Added to the row numbers, so page two starts where page one ended. */
  rowNumberOffset?: number
  className?: string
}

function useControlled<T>(
  value: T | undefined,
  fallback: T,
  onChange: ((next: T) => void) | undefined,
): [T, (next: T) => void] {
  const [inner, setInner] = useState(fallback)
  const controlled = value !== undefined
  const set = useCallback(
    (next: T) => {
      if (!controlled) setInner(next)
      onChange?.(next)
    },
    [controlled, onChange],
  )
  return [controlled ? value : inner, set]
}

const MOVES: Record<Exclude<EditMove, "stay" | "none">, Move> = {
  down: "down",
  up: "up",
  right: "next",
  left: "previous",
}

/**
 * The data grid: every surface that shows rows is this one component.
 *
 * It draws what it is given and reports what the reader did. It does not
 * fetch, page, persist or send: rows, sort, layout, selection and the pending
 * change set all come in as props and go out as callbacks, so the table editor,
 * a query result and a document view differ only in what they pass.
 *
 * Three things make it fast enough to be a spreadsheet:
 *
 * - **Only what shows is in the DOM.** Rows are one fixed height and are placed
 *   by index, so the slice in view is arithmetic; columns are sliced the same
 *   way against their running widths. Scrolling re-renders only when that
 *   slice changes, never per pixel.
 * - **Geometry never reaches a cell.** Column widths are one
 *   `grid-template-columns` string on each row. A column being dragged changes
 *   that string and nothing a cell is given, so no cell is redrawn for it.
 * - **One listener, not one per cell.** The body handles the pointer and the
 *   keyboard once and reads which cell it was from the element. Rows and cells
 *   are memoised on plain values, so moving the selection redraws the rows it
 *   crossed and an edit redraws its own cell.
 */
export function DataGrid({
  ref,
  label,
  columns,
  rows,
  rowId = indexId,
  clipped,
  sort: sortProp,
  onSortChange,
  sortMode = "server",
  layout: layoutProp,
  onLayoutChange,
  selection: selectionProp,
  onSelectionChange,
  changeSet,
  editable = false,
  canInsert = editable,
  canDelete = editable,
  defaultOnUpdate = true,
  readOnlyReason,
  rowErrors,
  findable = false,
  find: findProp,
  onFindChange,
  columnsMenu = true,
  selectable = true,
  onOpenRow,
  onFilter,
  onFollowForeignKey,
  onCopySQL,
  onStatusChange,
  loading = false,
  error,
  onRetry,
  empty,
  toolbar,
  footer,
  banner,
  rowHeight = 30,
  rowNumberOffset = 0,
  className,
}: DataGridProps) {
  const viewportRef = useRef<HTMLDivElement>(null)
  const findRef = useRef<HTMLInputElement>(null)
  const keysId = useId()

  const [sort, setSort] = useControlled(sortProp, EMPTY_SORT, onSortChange)
  const [layout, setLayout] = useControlled(layoutProp, EMPTY_LAYOUT, onLayoutChange)
  const [selection, setSelection] = useControlled(selectionProp, EMPTY_SELECTION, onSelectionChange)
  const [find, setFind] = useControlled(findProp, "", onFindChange)
  const deferredFind = useDeferredValue(find)

  const changes = changeSet?.changes ?? EMPTY_CHANGES
  const canEdit = editable && changeSet !== undefined
  const sortable = onSortChange !== undefined || sortMode === "client"

  /* ------------------------------------------------------------- the rows */

  // Which rows are drawn, and in what order: a find narrows them and a
  // client-side sort orders them. Null means "as given".
  const view = useMemo(() => {
    const query = deferredFind.trim()
    const found = query ? findIndexes(rows, query) : undefined
    if (sortMode === "client" && sort.length > 0) return sortedIndexes(rows, columns, sort, found)
    return found ?? null
  }, [rows, columns, deferredFind, sortMode, sort])

  const display = useMemo(() => {
    const serverCount = view ? view.length : rows.length
    const ids = new Array<string>(serverCount)
    const sources = new Array<number>(serverCount)
    for (let i = 0; i < serverCount; i++) {
      const from = view ? view[i] : i
      sources[i] = from
      ids[i] = rowId(rows[from], from)
    }
    for (const row of changes.inserts) {
      ids.push(row.id)
      sources.push(-1)
    }
    return { ids, sources, serverCount }
  }, [rows, view, rowId, changes.inserts])
  const count = display.ids.length

  const clippedMap = useMemo(() => {
    const map = new Map<number, Map<number, number>>()
    for (const cell of clipped ?? []) {
      let row = map.get(cell.row)
      if (!row) map.set(cell.row, (row = new Map()))
      row.set(cell.column, cell.size)
    }
    return map
  }, [clipped])

  /* ---------------------------------------------------------- the columns */

  const { order, hidden, pinned: pinnedKeys, widths: storedWidths } = layout
  const ordered = useMemo(
    () => orderColumns(columns, { order, hidden, pinned: pinnedKeys }),
    [columns, order, hidden, pinnedKeys],
  )
  // The width a column is being dragged to, before it is committed to the layout.
  const [draft, setDraft] = useState<{ key: string; width: number } | null>(null)
  const widths = useMemo(
    () =>
      columnWidths(ordered, draft ? { ...storedWidths, [draft.key]: draft.width } : storedWidths),
    [ordered, storedWidths, draft],
  )
  const offsets = useMemo(() => offsetsOf(widths), [widths])
  const total = offsets[offsets.length - 1]

  const keys = useMemo(() => keySources(columns), [columns])
  const model = useMemo<GridModel>(
    () => ({
      columns,
      rows,
      ordered,
      ids: display.ids,
      sources: display.sources,
      serverCount: display.serverCount,
      changes,
      clipped: clippedMap,
      editable: canEdit,
      defaultOnUpdate,
      keys,
    }),
    [columns, rows, ordered, display, changes, clippedMap, canEdit, defaultOnUpdate, keys],
  )

  /* ------------------------------------------------------------ the window */

  // A plausible size until the observer reports the real one, so the first
  // paint already holds a screenful instead of the overscan alone.
  const [size, setSize] = useState({ width: 1280, height: 720 })
  const [scroll, setScroll] = useState({ top: 0, left: 0 })

  const digits = String(rowNumberOffset + Math.max(display.serverCount, 1)).length
  // Room for the padding, the tick, the change sign and the widest row number.
  const gutter = 30 + (selectable ? 20 : 0) + digits * 7
  const pinnedDeclared = ordered.reduce((n, entry) => n + (entry.pinned ? 1 : 0), 0)
  // Pinned columns that would leave less than two fifths of the width to
  // scroll in are released for as long as the grid is that narrow: on a phone
  // a pinned key column is most of the screen.
  const pinnedCount = gutter + offsets[pinnedDeclared] <= size.width * 0.6 ? pinnedDeclared : 0
  const pinnedWidth = offsets[pinnedCount]
  const scrollOffsets = useMemo(() => offsetsOf(widths.slice(pinnedCount)), [widths, pinnedCount])

  const rowWin = rowSlice({
    scrollTop: scroll.top,
    viewport: size.height - HEADER_HEIGHT,
    rowHeight,
    count,
    overscan: ROW_OVERSCAN,
  })
  const colWin = columnSlice({
    scrollLeft: scroll.left,
    viewport: size.width - gutter - pinnedWidth,
    offsets: scrollOffsets,
    overscan: COLUMN_OVERSCAN,
  })

  const pinnedCols = useMemo(() => ordered.slice(0, pinnedCount), [ordered, pinnedCount])
  const windowCols = useMemo(
    () => ordered.slice(pinnedCount + colWin.start, pinnedCount + colWin.end),
    [ordered, pinnedCount, colWin.start, colWin.end],
  )

  /* -------------------------------------------------------- the selection */

  const bounds = useMemo(() => ({ rows: count, cols: ordered.length }), [count, ordered.length])
  const sel = useMemo(() => clampSelection(selection, bounds), [selection, bounds])
  const active = sel.active
  const range = useMemo(() => (isRange(sel) ? rangeOf(sel) : null), [sel])
  const ticked = useMemo(() => new Set(sel.rows), [sel.rows])
  const allRows = useMemo(() => allRowsState(sel.rows, display.ids), [sel.rows, display.ids])

  const [edit, setEditing] = useState<Editing | null>(null)
  const editingRow = edit ? display.ids.indexOf(edit.rowId) : -1
  const editingCol = edit ? ordered.findIndex((entry) => entry.column.key === edit.column) : -1
  // An edit whose row or column has gone — the page was refetched, the column
  // hidden — is over, whatever the state still says. Left standing it would
  // keep the keyboard switched off for an editor nobody can see.
  const editing = editingRow >= 0 && editingCol >= 0 ? edit : null

  const [menu, setMenu] = useState<GridCellRef | null>(null)
  const [headerMenu, setHeaderMenu] = useState<number | null>(null)
  const [moving, setMoving] = useState<{ key: string; before: string | null; x: number } | null>(
    null,
  )
  const [message, setMessage] = useState("")

  /* ------------------------------------------------------------ the status */

  const deferredSel = useDeferredValue(sel)
  const status = useMemo<GridStatus>(() => {
    const current = clampSelection(deferredSel, { rows: count, cols: ordered.length })
    const cells = rangeOf(current)
    return {
      rows: count,
      totalRows: rows.length + changes.inserts.length,
      selectedRows: current.rows.length,
      selectedCells: rangeSize(cells).cells,
      aggregate: isRange(current)
        ? rangeAggregate(
            cells,
            ordered.map((entry) => entry.column.kind),
            (row, col) => valueAt(model, row, ordered[col]),
          )
        : null,
    }
  }, [deferredSel, model, ordered, count, rows.length, changes.inserts.length])

  useEffect(() => {
    onStatusChange?.(status)
  }, [status, onStatusChange])

  /* ------------------------------------------------- handlers' view of it */

  const liveRef = useLive({
    model,
    count,
    ordered,
    widths,
    offsets,
    scrollOffsets,
    gutter,
    pinnedCount,
    pinnedWidth,
    rowHeight,
    rowWin,
    colWin,
    size,
    sel,
    setSelection,
    sort,
    setSort,
    sortable,
    layout,
    setLayout,
    changeSet,
    canEdit,
    canInsert: canInsert && canEdit,
    canDelete: canDelete && canEdit,
    editing,
    menu,
    moving,
    columns,
    rows,
    onOpenRow,
    onFilter,
    onFollowForeignKey,
    onCopySQL,
    findable,
  })

  // What to do once the next render has landed: where to scroll, and whether
  // the active cell should take the keyboard.
  const after = useRef<AfterRender>({ reveal: null, focus: false })
  // Whether the keyboard is in the grid, kept by hand because an element that
  // is removed while focused says nothing on its way out.
  const ownsFocus = useRef(false)
  const editorHandle = useRef<GridEditorHandle | null>(null)
  const lastTicked = useRef<number | null>(null)
  // Escape with nothing left to clear lets the next Tab leave the grid.
  const tabOut = useRef(false)
  const messageTimer = useRef<ReturnType<typeof setTimeout>>(undefined)
  // The cell last pressed with a finger or a pen, for a long press (see
  // `onMenuOpenChange`), and whether a `contextmenu` has already said which
  // cell the menu that is opening is for.
  const pressed = useRef<Hit | null>(null)
  const menuPlaced = useRef(false)
  // Whether the way out of the grid has been said yet.
  const hinted = useRef(false)
  const drag = useRef<{ x: number; y: number; frame: number } | null>(null)
  const dragCleanup = useRef<(() => void) | null>(null)

  const announce = useCallback((text: string) => {
    setMessage(text)
    clearTimeout(messageTimer.current)
    messageTimer.current = setTimeout(() => setMessage(""), 5000)
  }, [])

  useEffect(
    () => () => {
      clearTimeout(messageTimer.current)
      dragCleanup.current?.()
    },
    [],
  )

  const cellElement = useCallback((cell: GridCellRef | null): HTMLElement | null => {
    const root = viewportRef.current
    if (!root || !cell) return null
    return cell.row < 0
      ? root.querySelector<HTMLElement>(`[data-hcol="${cell.col}"]`)
      : root.querySelector<HTMLElement>(`[data-row="${cell.row}"] [data-col="${cell.col}"]`)
  }, [])

  const reveal = useCallback(
    (cell: GridCellRef) => {
      const root = viewportRef.current
      if (!root) return
      const state = liveRef.current
      if (cell.row >= 0) {
        const top = revealOffset({
          start: cell.row * state.rowHeight,
          end: (cell.row + 1) * state.rowHeight,
          scroll: root.scrollTop,
          viewport: root.clientHeight,
          lead: HEADER_HEIGHT,
        })
        if (top !== root.scrollTop) root.scrollTop = top
      }
      // A pinned column is always in view.
      if (cell.col >= state.pinnedCount) {
        const left = revealOffset({
          start: state.offsets[cell.col] - state.pinnedWidth,
          end: state.offsets[cell.col + 1] - state.pinnedWidth,
          scroll: root.scrollLeft,
          viewport: root.clientWidth,
          lead: state.gutter + state.pinnedWidth,
        })
        if (left !== root.scrollLeft) root.scrollLeft = left
      }
    },
    [liveRef],
  )

  useLayoutEffect(() => {
    const pending = after.current
    if (pending.reveal) {
      reveal(pending.reveal)
      pending.reveal = null
    }
    if (editing) return
    // The cell that held the keyboard can be unmounted from under it — a column
    // that moved between the drawn slice and the slot kept beside it, a header
    // scrolled out sideways. The browser then leaves focus on the body, where
    // no key reaches the grid; hand it back to the active cell, or to the grid
    // itself while that cell is not drawn.
    const root = viewportRef.current
    const focused = document.activeElement
    const element = cellElement(active) ?? root?.querySelector<HTMLElement>('[role="grid"]')
    if (!element) return
    const adrift =
      ownsFocus.current &&
      focused !== element &&
      (focused === null ||
        focused === document.body ||
        (root?.contains(focused) && !focused.closest("[data-grid-editor]")))
    if (pending.focus || adrift) {
      element.focus({ preventScroll: true })
      pending.focus = false
    }
  })

  /** Moves the active cell, optionally growing the range, and follows it. */
  const moveTo = useCallback(
    (cell: GridCellRef, extend = false) => {
      const state = liveRef.current
      state.setSelection(extend ? extendTo(state.sel, cell) : selectCell(state.sel, cell))
      after.current = { reveal: cell, focus: true }
    },
    [liveRef],
  )

  /**
   * Focus arrived on the grid with no cell active — a Tab into it, a press on
   * the gutter. The first cell that is showing becomes the active one, without
   * scrolling: the reader has not asked to go anywhere.
   */
  const enterGrid = useCallback(() => {
    const root = viewportRef.current
    const state = liveRef.current
    if (!root || state.sel.active || state.ordered.length === 0) return
    const row = Math.min(Math.ceil(root.scrollTop / state.rowHeight), state.count - 1)
    const first = state.scrollOffsets.findIndex((offset) => offset >= root.scrollLeft)
    const col = state.pinnedCount > 0 ? 0 : Math.max(first, 0)
    state.setSelection(
      selectCell(state.sel, {
        row: state.count > 0 ? row : HEADER_ROW,
        col: Math.min(col, state.ordered.length - 1),
      }),
    )
    after.current = { reveal: null, focus: true }
  }, [liveRef])

  /** Puts the keyboard on the active cell — or on the grid, while that cell is not drawn. */
  const focusActive = useCallback(() => {
    const element =
      cellElement(liveRef.current.sel.active) ??
      viewportRef.current?.querySelector<HTMLElement>('[role="grid"]')
    element?.focus({ preventScroll: true })
  }, [cellElement, liveRef])

  /* -------------------------------------------------------------- scrolling */

  const syncWindow = useCallback(() => {
    const root = viewportRef.current
    if (!root) return
    const state = liveRef.current
    const nextRows = rowSlice({
      scrollTop: root.scrollTop,
      viewport: state.size.height - HEADER_HEIGHT,
      rowHeight: state.rowHeight,
      count: state.count,
      overscan: ROW_OVERSCAN,
    })
    const nextCols = columnSlice({
      scrollLeft: root.scrollLeft,
      viewport: state.size.width - state.gutter - state.pinnedWidth,
      offsets: state.scrollOffsets,
      overscan: COLUMN_OVERSCAN,
    })
    // The scroll position is state only so that render can slice by it; it is
    // written when the slice would change, not on every pixel.
    if (!sameSlice(nextRows, state.rowWin) || !sameSlice(nextCols, state.colWin)) {
      setScroll({ top: root.scrollTop, left: root.scrollLeft })
    }
  }, [liveRef])

  useEffect(() => {
    const root = viewportRef.current
    if (!root) return
    const observer = new ResizeObserver(() => {
      const width = root.clientWidth
      const height = root.clientHeight
      setSize((previous) =>
        previous.width === width && previous.height === height ? previous : { width, height },
      )
    })
    observer.observe(root)
    return () => observer.disconnect()
  }, [])

  /* ---------------------------------------------------------------- editing */

  const describeRow = useCallback(
    (row: number): GridRowRef => {
      const { model: current } = liveRef.current
      const from = current.sources[row]
      return {
        id: current.ids[row],
        index: row,
        values: rowValues(current, row),
        original: from >= 0 ? current.rows[from] : null,
        previews: previewKeys(current, row),
      }
    },
    [liveRef],
  )

  /**
   * Hands an action to the change set — unless it would leave more rows
   * changed than one apply can carry. The set goes in one transaction or not
   * at all, so a set past the limit is one that could never be applied; it is
   * refused here, while the reader can still do something about it.
   */
  const stage = useCallback(
    (action: ChangeAction): boolean => {
      const staged = liveRef.current.changeSet
      if (!staged) return false
      if (exceedsLimit(staged.changes, action)) {
        notify.warning("Too many changes for one apply", {
          description: `At most ${MAX_CHANGES.toLocaleString("en-US")} rows can be changed at once. Apply or discard what is staged, then carry on.`,
        })
        return false
      }
      staged.dispatch(action)
      return true
    },
    [liveRef],
  )

  const startEdit = useCallback(
    (cell: GridCellRef, how: { seed?: string; caret?: "select" | "end"; expanded?: boolean }) => {
      const state = liveRef.current
      if (cell.row < 0) return
      const entry = state.ordered[cell.col]
      if (!entry) return
      const reason = lockReason(state.model, cell.row, entry)
      if (reason) {
        announce(reason)
        return
      }
      after.current = { reveal: cell, focus: false }
      setEditing({
        rowId: state.model.ids[cell.row],
        column: entry.column.key,
        seed: how.seed ?? null,
        caret: how.caret ?? "select",
        expanded: how.expanded ?? false,
        canFill: isRange(state.sel),
        canDefault: defaultAllowed(state.model, cell.row, entry.column),
      })
    },
    [announce, liveRef],
  )

  const finishEdit = useCallback(
    (result: { value: EditValue } | null, move: EditMove, fill?: string): boolean => {
      const state = liveRef.current
      const current = state.editing
      if (!current) return true
      const row = state.model.ids.indexOf(current.rowId)
      const entry = state.ordered.find((candidate) => candidate.column.key === current.column)
      if (result && row >= 0 && entry) {
        const edits: CellEdit[] = [editFor(state.model, row, entry, result.value)]
        const cells = fill !== undefined ? rangeOf(state.sel) : null
        if (cells && fill !== undefined) {
          // One value typed into a range fills it, each column reading the
          // text by its own rules; a cell that cannot take it is left alone.
          for (let r = cells.top; r <= cells.bottom; r++) {
            for (let c = cells.left; c <= cells.right; c++) {
              const other = state.ordered[c]
              if ((r === row && other === entry) || lockReason(state.model, r, other)) continue
              const parsed = parseInput(fill, other.column)
              if (parsed.ok) edits.push(editFor(state.model, r, other, parsed.value))
            }
          }
        }
        // Refused, the editor stays open: what was typed is still in it, and
        // the reader has just been told why it could not be staged.
        if (!stage({ type: "edit", edits })) return false
      }
      setEditing(null)
      if (move === "none") return true
      // The move is from the cell that was edited, which the menu's Edit can
      // open somewhere other than the active one.
      const here = row >= 0 && entry ? { row, col: entry.index } : state.sel.active
      const next =
        move === "stay" || !here
          ? here
          : (moveCell(here, MOVES[move], { rows: state.count, cols: state.ordered.length }) ?? here)
      const settled = move === "stay" && rangeContains(rangeOf(state.sel), row, entry?.index ?? -1)
      if (next && !settled && !sameCell(next, state.sel.active)) {
        state.setSelection(selectCell(state.sel, next))
      }
      after.current = { reveal: next, focus: true }
      return true
    },
    [liveRef, stage],
  )

  const registerEditor = useCallback((handle: GridEditorHandle | null) => {
    editorHandle.current = handle
  }, [])

  const editorApi = useMemo<GridEditorApi | null>(
    () =>
      editing && {
        seed: editing.seed,
        caret: editing.caret,
        expanded: editing.expanded,
        canFill: editing.canFill,
        canDefault: editing.canDefault,
        finish: finishEdit,
        register: registerEditor,
      },
    [editing, finishEdit, registerEditor],
  )

  /** Asks an open editor to settle. False when it holds something it cannot stage. */
  const settleEditor = useCallback(() => {
    if (!liveRef.current.editing) return true
    return editorHandle.current?.settle() ?? true
  }, [liveRef])

  /**
   * Writes a value into every cell a verb reaches from the cell it was invoked
   * on, leaving alone the ones that cannot take it.
   */
  const fill = useCallback(
    (
      at: GridCellRef | null,
      value: (column: GridColumn, row: number) => EditValue | undefined,
      verb: string,
    ) => {
      const state = liveRef.current
      const block = actionBlock(state.model, state.sel, at)
      if (!block || !state.canEdit) return
      const edits: CellEdit[] = []
      let skipped = 0
      for (const row of block.rows) {
        for (const entry of block.cols) {
          const next = lockReason(state.model, row, entry) ? undefined : value(entry.column, row)
          if (next === undefined) skipped++
          else edits.push(editFor(state.model, row, entry, next))
        }
      }
      if (edits.length > 0) stage({ type: "edit", edits })
      if (edits.length === 0 && skipped > 0) announce(`Nothing here can be ${verb}`)
    },
    [announce, liveRef, stage],
  )

  const setNull = useCallback(
    (at: GridCellRef | null = liveRef.current.sel.active) =>
      fill(at, (column) => (column.nullable ? null : undefined), "set to NULL"),
    [fill, liveRef],
  )
  const setEmpty = useCallback(
    (at: GridCellRef | null = liveRef.current.sel.active) =>
      fill(at, (column) => (holdsText(column) ? "" : undefined), "set to the empty string"),
    [fill, liveRef],
  )
  const setDefault = useCallback(
    (at: GridCellRef | null = liveRef.current.sel.active) =>
      fill(
        at,
        (column, row) =>
          defaultAllowed(liveRef.current.model, row, column) ? DEFAULT_VALUE : undefined,
        "set to its default",
      ),
    [fill, liveRef],
  )
  /** Delete on a selection of cells: NULL where the column allows it, empty text where it does not. */
  const clearCells = useCallback(
    (at: GridCellRef | null = liveRef.current.sel.active) =>
      fill(
        at,
        (column) => (column.nullable ? null : holdsText(column) ? "" : undefined),
        "cleared",
      ),
    [fill, liveRef],
  )

  /* ------------------------------------------------------------------- rows */

  const deleteRows = useCallback(
    (rowsToDelete: readonly number[]) => {
      const state = liveRef.current
      if (!state.canDelete) {
        announce("Rows cannot be deleted here")
        return
      }
      // A row whose key arrived cut cannot be found again to be deleted.
      const reachable = rowsToDelete.filter((row) => !keyIsPreview(state.model, row))
      const removals = reachable.map((row) => ({
        rowId: state.model.ids[row],
        origin: originOf(state.model, row),
      }))
      if (removals.length === 0) {
        if (rowsToDelete.length > 0) announce(KEY_PREVIEW)
        return
      }
      if (!stage({ type: "delete", rows: removals })) return
      const left = rowsToDelete.length - removals.length
      announce(
        (removals.length === 1
          ? "Row marked for deletion"
          : `${removals.length} rows marked for deletion`) +
          (left > 0 ? ` — ${left} left alone, their key was only partly loaded` : ""),
      )
    },
    [announce, liveRef, stage],
  )

  const insertRows = useCallback(
    (list: readonly Readonly<Record<string, EditValue>>[]): string[] => {
      const state = liveRef.current
      const staged = state.changeSet
      if (!state.canInsert || !staged) {
        announce("Rows cannot be added here")
        return []
      }
      const inserts: RowInsertion[] = list.map((values) => ({ id: staged.newRowId(), values }))
      if (!stage({ type: "insert", rows: inserts })) return []
      // New rows land after everything drawn now; go to the first of them.
      const first = state.ordered.findIndex(
        (entry) => !entry.column.generated && entry.column.editable !== false,
      )
      const cell = { row: state.count, col: Math.max(first, 0) }
      state.setSelection({ ...state.sel, active: cell, anchor: null })
      after.current = { reveal: cell, focus: true }
      return inserts.map((row) => row.id)
    },
    [announce, liveRef, stage],
  )

  const duplicateRows = useCallback(
    (rowsToCopy: readonly number[]) => {
      const { model: current } = liveRef.current
      const copies = rowsToCopy
        .filter((row) => pendingOf(current, row) !== "deleted")
        .map((row) =>
          duplicateValues(current.columns, rowRecord(current, row), previewKeys(current, row)),
        )
      if (copies.length > 0) insertRows(copies)
    },
    [insertRows, liveRef],
  )

  const revertRows = useCallback(
    (rowsToRevert: readonly number[]) => {
      const { model: current } = liveRef.current
      stage({
        type: "batch",
        actions: rowsToRevert.map((row) => ({ type: "revertRow", rowId: current.ids[row] })),
      })
    },
    [liveRef, stage],
  )

  const openRow = useCallback(
    (row: number) => {
      const state = liveRef.current
      if (row >= 0 && row < state.count) state.onOpenRow?.(describeRow(row))
    },
    [describeRow, liveRef],
  )

  const follow = useCallback(
    (cell: GridCellRef) => {
      const state = liveRef.current
      const entry = state.ordered[cell.col]
      const foreignKey = entry?.column.foreignKey
      const value = entry && valueAt(state.model, cell.row, entry)
      if (!foreignKey || value === null || value === undefined || isDefault(value)) return
      state.onFollowForeignKey?.({
        column: entry.column,
        foreignKey,
        value,
        row: describeRow(cell.row),
      })
    },
    [describeRow, liveRef],
  )

  /* -------------------------------------------------------------- clipboard */

  /** Whether an event came from the grid itself rather than an editor or a portalled menu. */
  const fromGrid = useCallback((target: EventTarget | null) => {
    const root = viewportRef.current
    if (!root || !(target instanceof Element) || !root.contains(target)) return false
    return !target.closest("[data-grid-editor]")
  }, [])

  const { copyAs, pasteText, onCopy, onCut, onPaste } = useGridClipboard({
    liveRef,
    fromGrid,
    announce,
    stage,
    clearCells,
  })

  /* ---------------------------------------------------------------- columns */

  const sortBy = useCallback(
    (column: GridColumn, additive: boolean) => {
      const state = liveRef.current
      if (state.sortable) state.setSort(cycleSort(state.sort, column.key, additive))
    },
    [liveRef],
  )

  const fitColumn = useCallback(
    (column: GridColumn) => {
      const root = viewportRef.current
      const state = liveRef.current
      const entry = state.ordered.find((candidate) => candidate.column.key === column.key)
      if (!root || !entry) return
      const width = measureColumn(root, state.model, entry, state.rowWin.start)
      if (width !== null) state.setLayout(setColumnWidth(state.layout, column.key, width))
    },
    [liveRef],
  )

  const columnActions = useMemo<ColumnActions>(
    () => ({
      sort: (column, desc, additive) => {
        const state = liveRef.current
        state.setSort(withSort(state.sort, column.key, desc, additive))
      },
      pin: (column, pinned) => {
        const state = liveRef.current
        state.setLayout(setColumnPinned(state.layout, column.key, pinned))
      },
      hide: (column) => {
        const state = liveRef.current
        state.setLayout(setColumnHidden(state.layout, column.key, true))
      },
      fit: fitColumn,
      resetWidth: (column) => {
        const state = liveRef.current
        state.setLayout(resetColumnWidth(state.layout, column.key))
      },
      select: (index) => {
        const state = liveRef.current
        const bounds = { rows: state.count, cols: state.ordered.length }
        state.setSelection(selectColumn(state.sel, index, bounds))
        after.current = { reveal: null, focus: true }
      },
      copyName: (column) =>
        void copyText(column.name).then((ok) => ok && announce(`Copied "${column.name}"`)),
      setMenu: setHeaderMenu,
      restoreFocus: () => {
        after.current.focus = true
        focusActive()
      },
    }),
    [announce, fitColumn, focusActive, liveRef],
  )

  /** Follows the pointer until release, and is ended if the grid goes away first. */
  const track = useCallback((onMove: (event: PointerEvent) => void, onEnd: () => void) => {
    dragCleanup.current = trackPointer(onMove, () => {
      dragCleanup.current = null
      onEnd()
    })
  }, [])

  const headerHandlers = useHeaderGestures({
    liveRef,
    viewportRef,
    afterRef: after,
    track,
    setDraft,
    setMoving,
    setHeaderMenu,
    fitColumn,
    sortBy,
  })

  /* ---------------------------------------------------------------- pointer */

  const dragTo = useCallback(
    (x: number, y: number) => {
      const hit = hitTest(document.elementFromPoint(x, y))
      if (!hit || hit.gutter) return
      const state = liveRef.current
      const next = extendTo(state.sel, { row: hit.row, col: hit.col })
      if (!sameCell(next.active, state.sel.active)) state.setSelection(next)
    },
    [liveRef],
  )

  const startRangeDrag = useCallback(
    (event: React.PointerEvent) => {
      drag.current = { x: event.clientX, y: event.clientY, frame: 0 }
      // While the pointer is held past an edge the grid keeps scrolling that
      // way and the range keeps growing, without waiting for it to move.
      const tick = () => {
        const current = drag.current
        const root = viewportRef.current
        if (!current || !root) return
        const state = liveRef.current
        const rect = root.getBoundingClientRect()
        const top = rect.top + HEADER_HEIGHT
        const left = rect.left + state.gutter + state.pinnedWidth
        const dy = current.y < top + 8 ? -12 : current.y > rect.bottom - 8 ? 12 : 0
        const dx = current.x < left + 8 ? -18 : current.x > rect.right - 8 ? 18 : 0
        if (dx !== 0 || dy !== 0) {
          root.scrollBy(dx, dy)
          dragTo(
            Math.min(Math.max(current.x, left + 2), rect.right - 2),
            Math.min(Math.max(current.y, top + 2), rect.bottom - 2),
          )
        }
        current.frame = requestAnimationFrame(tick)
      }
      drag.current.frame = requestAnimationFrame(tick)
      track(
        (move) => {
          if (!drag.current) return
          drag.current.x = move.clientX
          drag.current.y = move.clientY
          dragTo(move.clientX, move.clientY)
        },
        () => {
          if (drag.current) cancelAnimationFrame(drag.current.frame)
          drag.current = null
        },
      )
    },
    [dragTo, liveRef, track],
  )

  const onBodyPointerDown = useCallback(
    (event: React.PointerEvent) => {
      if (event.button !== 0 || !fromGrid(event.target)) return
      const hit = hitTest(event.target)
      if (!hit) return
      pressed.current = event.pointerType === "mouse" ? null : hit
      // An editor holding something it cannot stage keeps the keyboard.
      if (!settleEditor()) {
        event.preventDefault()
        return
      }
      const state = liveRef.current
      tabOut.current = false
      if (hit.gutter) {
        const id = state.model.ids[hit.row]
        const from = lastTicked.current
        const rowsNext =
          event.shiftKey && from !== null
            ? toggleRowRange(
                state.sel.rows,
                state.model.ids,
                from,
                hit.row,
                !state.sel.rows.includes(id),
              )
            : toggleRow(state.sel.rows, id)
        lastTicked.current = hit.row
        // The cursor goes to the row that was pressed, as it would for a press
        // on one of its cells. A key pressed next acts from where the cursor
        // is: on every ticked row when this one was just ticked, on this row
        // alone when it was just unticked.
        state.setSelection({
          rows: rowsNext,
          active: { row: hit.row, col: state.sel.active?.col ?? 0 },
          anchor: null,
        })
        // The gutter is not focusable, so the press would hand focus to the
        // grid itself, after the cell has taken it; the cell is to keep it.
        event.preventDefault()
        after.current = { reveal: null, focus: true }
        return
      }
      const cell = { row: hit.row, col: hit.col }
      if ((event.target as Element).closest("[data-follow]")) {
        follow(cell)
        return
      }
      moveTo(cell, event.shiftKey)
      // A finger drags to scroll; only a mouse drags out a range.
      if (event.pointerType === "mouse") startRangeDrag(event)
    },
    [follow, fromGrid, liveRef, moveTo, settleEditor, startRangeDrag],
  )

  const onBodyDoubleClick = useCallback(
    (event: React.MouseEvent) => {
      if (!fromGrid(event.target)) return
      const hit = hitTest(event.target)
      if (!hit) return
      if (hit.gutter) openRow(hit.row)
      else if (liveRef.current.canEdit)
        startEdit({ row: hit.row, col: hit.col }, { caret: "select" })
      else openRow(hit.row)
    },
    [fromGrid, liveRef, openRow, startEdit],
  )

  /**
   * Opens the cell menu on a cell. Inside the selection — the range, or a
   * ticked row — the menu is about the selection; anywhere else the cursor
   * moves there first, and the menu is about that cell.
   */
  const openMenuAt = useCallback(
    (hit: Hit): boolean => {
      const state = liveRef.current
      if (state.ordered.length === 0 || !settleEditor()) return false
      const cell = { row: hit.row, col: hit.gutter ? (state.sel.active?.col ?? 0) : hit.col }
      const inside =
        rangeContains(rangeOf(state.sel), cell.row, cell.col) ||
        state.sel.rows.includes(state.model.ids[cell.row])
      if (!inside) state.setSelection(selectCell(state.sel, cell))
      setMenu(cell)
      return true
    },
    [liveRef, settleEditor],
  )

  // An editor never reaches this: it keeps its right-click to itself (see
  // `EDITOR_PROPS`), so the browser's own menu, with Paste in it, opens there.
  const onBodyContextMenu = useCallback(
    (event: React.MouseEvent) => {
      const hit = hitTest(event.target)
      // Prevented, the menu primitive stays shut — and so does the browser's.
      if (hit && openMenuAt(hit)) menuPlaced.current = true
      else event.preventDefault()
    },
    [openMenuAt],
  )

  /**
   * A long press opens the menu too, and on a browser that raises no
   * `contextmenu` for one the menu primitive opens on its own timer, without
   * asking what was pressed. The cell under the finger is kept for that.
   */
  const onMenuOpenChange = useCallback(
    (open: boolean) => {
      if (open && !menuPlaced.current && pressed.current) openMenuAt(pressed.current)
      if (!open) setMenu(null)
      menuPlaced.current = false
    },
    [openMenuAt],
  )

  const onKeyDown = useGridKeyboard({
    liveRef,
    tabOutRef: tabOut,
    findRef,
    fromGrid,
    moveTo,
    startEdit,
    openRow,
    sortBy,
    setNull,
    setDefault,
    deleteRows,
    clearCells,
  })

  /* ------------------------------------------------------------ the handle */

  useImperativeHandle(
    ref,
    () => ({
      focus: () => {
        after.current.focus = true
        focusActive()
      },
      insertRow: (values) => insertRows([values ?? {}])[0] ?? null,
      duplicateSelected: () => {
        const state = liveRef.current
        duplicateRows(selectedRows(state.model, state.sel))
      },
      deleteSelected: () => {
        const state = liveRef.current
        deleteRows(selectedRows(state.model, state.sel))
      },
      copy: copyAs,
      revealRow: (index) => {
        const state = liveRef.current
        if (index >= 0 && index < state.count)
          moveTo({ row: index, col: state.sel.active?.col ?? 0 })
      },
    }),
    [copyAs, deleteRows, duplicateRows, focusActive, insertRows, liveRef, moveTo],
  )

  /* --------------------------------------------------------- the cell menu */

  const menuTarget = useMemo<CellMenuTarget | null>(() => {
    const entry = menu && ordered[menu.col]
    if (!menu || !entry || menu.row < 0 || menu.row >= count) return null
    const value = valueAt(model, menu.row, entry)
    const lock = lockReason(model, menu.row, entry)
    const state = pendingOf(model, menu.row)
    const edited = model.changes.updates[model.ids[menu.row]]?.values
    // What the cell verbs would reach from here, and whether any of it can
    // take each value: the menu offers what it will do, to what it will do it.
    const block = actionBlock(model, sel, menu)
    let canNull = false
    let canEmpty = false
    let canDefault = false
    for (const row of block?.rows ?? []) {
      for (const other of block?.cols ?? []) {
        if (canNull && canEmpty && canDefault) break
        if (lockReason(model, row, other)) continue
        canNull ||= other.column.nullable === true
        canEmpty ||= holdsText(other.column) && valueAt(model, row, other) !== ""
        canDefault ||= defaultAllowed(model, row, other.column)
      }
    }
    return {
      column: entry.column,
      cells: block ? block.rows.length * block.cols.length : 1,
      rows: targetRows(model, sel, menu.row).length,
      isNull: value === null || value === undefined,
      editable: lock === null,
      canNull,
      canEmpty,
      canDefault,
      changed:
        (edited !== undefined && entry.column.key in edited) ||
        (state === "inserted" && value !== undefined),
      rowState: state,
      canInsert: canInsert && canEdit,
      canDelete: canDelete && canEdit && !keyIsPreview(model, menu.row),
      canFilter: onFilter !== undefined && !isDefault(value),
      canOpen: onOpenRow !== undefined,
      canFollow:
        onFollowForeignKey !== undefined &&
        entry.column.foreignKey !== undefined &&
        value !== null &&
        value !== undefined &&
        !isDefault(value),
      canCopySQL: onCopySQL !== undefined,
      canPaste:
        canEdit && typeof navigator !== "undefined" && navigator.clipboard?.readText !== undefined,
    }
  }, [
    menu,
    ordered,
    count,
    model,
    sel,
    canInsert,
    canDelete,
    canEdit,
    onFilter,
    onOpenRow,
    onFollowForeignKey,
    onCopySQL,
  ])

  const menuActions = useMemo<CellMenuActions>(() => {
    const at = () => liveRef.current.menu
    return {
      copy: (format) => copyAs(format, at()),
      copyRowJSON: () => {
        const state = liveRef.current
        const cell = at()
        if (!cell) return
        const list = targetRows(state.model, state.sel, cell.row)
        const text = toJSONRows(
          state.model.columns,
          list.map((row) => rowValues(state.model, row)),
        )
        const cut = list.reduce((n, row) => n + previewKeys(state.model, row).length, 0)
        void copyText(text).then(
          (ok) =>
            ok &&
            announce(
              (list.length === 1
                ? "Copied the row as JSON"
                : `Copied ${list.length} rows as JSON`) + previewNote(cut),
            ),
        )
      },
      copySQL: () => {
        const state = liveRef.current
        const cell = at()
        if (!cell) return
        const list = targetRows(state.model, state.sel, cell.row)
        // Whole rows, whatever columns are showing: an INSERT is of a row.
        const previews = list.flatMap((row, r) =>
          state.model.columns.flatMap((_, column) =>
            isPreview(state.model, row, column) ? [{ row: r, column }] : [],
          ),
        )
        state.onCopySQL?.({
          columns: [...state.model.columns],
          rowIds: list.map((row) => state.model.ids[row]),
          rows: list.map((row) => rowValues(state.model, row)),
          previews,
        })
      },
      paste: () => {
        navigator.clipboard.readText().then(pasteText, () =>
          notify.error("Could not paste", undefined, {
            description:
              "The browser would not let the page read the clipboard. Press Ctrl+V instead.",
          }),
        )
      },
      filter: (op) => {
        const state = liveRef.current
        const cell = at()
        const entry = cell && state.ordered[cell.col]
        if (!cell || !entry) return
        const value = valueAt(state.model, cell.row, entry)
        state.onFilter?.({
          column: entry.column,
          op,
          value: op === "eq" || op === "ne" ? (value as CellValue) : undefined,
        })
      },
      follow: () => {
        const cell = at()
        if (cell) follow(cell)
      },
      edit: () => {
        const cell = at()
        if (cell) startEdit(cell, { caret: "select" })
      },
      setNull: () => setNull(at()),
      setEmpty: () => setEmpty(at()),
      setDefault: () => setDefault(at()),
      revertCell: () => {
        const state = liveRef.current
        const cell = at()
        const entry = cell && state.ordered[cell.col]
        if (cell && entry) {
          stage({ type: "revertCell", rowId: state.model.ids[cell.row], column: entry.column.key })
        }
      },
      revertRows: () => {
        const state = liveRef.current
        const cell = at()
        if (cell) revertRows(targetRows(state.model, state.sel, cell.row))
      },
      openRow: () => {
        const cell = at()
        if (cell) openRow(cell.row)
      },
      duplicate: () => {
        const state = liveRef.current
        const cell = at()
        if (cell) duplicateRows(targetRows(state.model, state.sel, cell.row))
      },
      deleteRows: () => {
        const state = liveRef.current
        const cell = at()
        if (cell) deleteRows(targetRows(state.model, state.sel, cell.row))
      },
    }
  }, [
    announce,
    copyAs,
    deleteRows,
    duplicateRows,
    follow,
    liveRef,
    openRow,
    pasteText,
    revertRows,
    setDefault,
    setEmpty,
    setNull,
    stage,
    startEdit,
  ])

  /* ------------------------------------------------------------- rendering */

  // The active row stays mounted wherever the scroll is, so the keyboard is
  // never unmounted from under the reader. So does a row being edited that is
  // not the active one — the menu's Edit opens the cell it was asked on — or
  // its editor would go with it, taking what was typed and leaving the
  // keyboard switched off for nothing.
  const kept = new Set<number>()
  const outside = (row: number) => row >= 0 && (row < rowWin.start || row >= rowWin.end)
  if (active && outside(active.row)) kept.add(active.row)
  if (editing && outside(editingRow)) kept.add(editingRow)
  // Always in row order, kept rows included: rows that stay keep their order
  // among themselves, so none is ever moved in the document — and an element
  // that is moved loses focus, which an open editor reads as being left.
  const drawn: number[] = []
  for (const row of [...kept].sort((a, b) => a - b)) if (row < rowWin.start) drawn.push(row)
  for (let row = rowWin.start; row < rowWin.end; row++) drawn.push(row)
  for (const row of [...kept].sort((a, b) => a - b)) if (row >= rowWin.end) drawn.push(row)
  // And within it one cell is held in place: the one being edited, or the
  // active one once it has scrolled out of the drawn columns.
  const inWindow = (col: number) =>
    col < pinnedCount || (col >= pinnedCount + colWin.start && col < pinnedCount + colWin.end)
  const heldRow = editing ? editingRow : active ? active.row : -1
  const heldCol = editing ? editingCol : active && !inWindow(active.col) ? active.col : -1
  const heldTrack =
    heldCol < 0 || !inWindow(heldCol)
      ? 0
      : heldCol < pinnedCount
        ? heldCol + 2
        : heldCol - colWin.start + 3

  // The column tracks every row lays its cells out in: the gutter, the pinned
  // columns, one track standing in for the columns scrolled past, then the
  // drawn ones.
  const template = [
    `${gutter}px`,
    ...pinnedCols.map((_, i) => `${widths[i]}px`),
    `${scrollOffsets[colWin.start] ?? 0}px`,
    ...windowCols.map((entry) => `${widths[entry.index]}px`),
  ].join(" ")
  const style: Record<string, string> = {
    "--jd-grid-width": `${gutter + total}px`,
    "--jd-grid-row": `${rowHeight}px`,
    "--jd-grid-view": `${size.width}px`,
  }
  pinnedCols.forEach((_, i) => {
    style[`--jd-grid-pin-${i}`] = `${gutter + offsets[i]}px`
  })
  if (heldCol >= 0 && heldTrack === 0) {
    style["--jd-grid-held-left"] = `${gutter + offsets[heldCol]}px`
    style["--jd-grid-held-width"] = `${widths[heldCol]}px`
  }

  const hasStrip = toolbar !== undefined || findable || columnsMenu
  // A refresh that failed does not take away rows already on screen: they stay,
  // with the failure said above them. Only a first load that failed has nothing
  // to show but the error.
  const stale = Boolean(error) && count > 0
  const body = stale ? null : error ? (
    <div className="sticky left-0 w-(--jd-grid-view) p-4">
      <ErrorState error={error} onRetry={onRetry} />
    </div>
  ) : loading && count === 0 ? (
    <GridSkeletonRows
      rows={Math.min(Math.max(Math.floor((size.height - HEADER_HEIGHT) / rowHeight), 6), 24)}
      leading={pinnedCols.length + 1}
      trailing={windowCols.length}
      template={template}
    />
  ) : count === 0 ? (
    <div className="sticky left-0 flex w-(--jd-grid-view) justify-center p-6">
      {empty ?? <EmptyNote>No rows</EmptyNote>}
    </div>
  ) : null

  return (
    <div
      data-slot="data-grid"
      className={cn(
        "flex min-h-0 min-w-0 flex-1 flex-col [--jd-grid-ground:var(--panel-ground,var(--card))]",
        className,
      )}
    >
      {hasStrip && (
        <PaneHeader className="flex-wrap">
          <div className="flex min-w-0 flex-1 flex-wrap items-center gap-1.5">{toolbar}</div>
          {findable && (
            <SearchInput
              ref={findRef}
              dense
              value={find}
              placeholder="Find in these rows"
              aria-label="Find in these rows"
              containerClassName="sm:w-56"
              onChange={(event) => setFind(event.target.value)}
              onKeyDown={(event) => {
                if (event.key !== "Escape") return
                setFind("")
                focusActive()
              }}
            />
          )}
          {columnsMenu && (
            <GridColumnsMenu columns={columns} layout={layout} onLayoutChange={setLayout} />
          )}
        </PaneHeader>
      )}
      {readOnlyReason && (
        <div className="flex shrink-0 items-center gap-2 border-b border-hairline px-2.5 py-1 text-hint text-muted-foreground">
          <Tag>Read-only</Tag>
          <span className="min-w-0 truncate">{readOnlyReason}</span>
        </div>
      )}
      {stale && error && (
        <div
          role="alert"
          className="flex shrink-0 items-center gap-2 border-b border-rule-danger bg-wash-danger px-2.5 py-1 text-hint"
        >
          <span className="min-w-0 truncate">
            These rows could not be refreshed: {errorMessage(error)}
          </span>
          {onRetry && (
            <Button type="button" size="xs" variant="outline" className="ml-auto" onClick={onRetry}>
              Try again
            </Button>
          )}
        </div>
      )}
      {banner}
      <GridEditorContext.Provider value={editorApi}>
        <ContextMenu onOpenChange={onMenuOpenChange}>
          <div
            ref={viewportRef}
            data-slot="data-grid-viewport"
            style={style as React.CSSProperties}
            className="group/grid relative min-h-0 flex-1 overflow-auto overscroll-contain"
            onScroll={syncWindow}
            onFocus={(event) => {
              ownsFocus.current = true
              // Tab walks the cells here, which is not what Tab does anywhere
              // else; the first time the keyboard arrives on a cell, the way
              // out is said where it can be seen as well as heard. (A text
              // field is always `:focus-visible`, so an editor does not count.)
              if (hinted.current || !fromGrid(event.target)) return
              if (!event.target.matches(":focus-visible")) return
              hinted.current = true
              announce("Tab moves between cells. Escape, then Tab, leaves the grid.")
            }}
            onBlur={(event) => {
              // A cell unmounted while it had focus is not the reader leaving.
              if (!event.target.isConnected) return
              const next = event.relatedTarget
              if (!(next instanceof Node) || !event.currentTarget.contains(next)) {
                ownsFocus.current = false
              }
            }}
            onKeyDown={onKeyDown}
            onCopy={onCopy}
            onCut={onCut}
            onPaste={onPaste}
          >
            <div
              role="grid"
              aria-label={label}
              aria-rowcount={count + 1}
              aria-colcount={ordered.length + 1}
              aria-multiselectable
              aria-readonly={!canEdit}
              aria-busy={loading || undefined}
              aria-describedby={keysId}
              // The one tab stop is the active cell; until there is one, the grid itself.
              tabIndex={active ? -1 : 0}
              className="w-(--jd-grid-width) min-w-full focus-ring-inset"
              onFocus={(event) => {
                if (event.target === event.currentTarget) enterGrid()
              }}
            >
              <p id={keysId} className="sr-only">
                Arrow keys and Tab move between cells. Shift with Space ticks the row. Escape, then
                Tab, leaves the grid.
              </p>
              <div role="rowgroup" className="sticky top-0 z-20" {...headerHandlers}>
                <GridHeader
                  template={template}
                  pinned={pinnedCols}
                  cols={windowCols}
                  sort={sort}
                  sortable={sortable}
                  customWidths={storedWidths}
                  activeCol={active && active.row < 0 ? active.col : -1}
                  menu={headerMenu}
                  moving={moving?.key ?? null}
                  selectable={selectable}
                  allRows={allRows}
                  actions={columnActions}
                />
                {moving && (
                  <div aria-hidden className="pointer-events-none sticky left-0 h-0 w-0">
                    <div
                      className="absolute bottom-0 h-9 w-0.5 -translate-x-1/2 bg-foreground"
                      style={{ left: moving.x }}
                    />
                  </div>
                )}
                {loading && count > 0 && (
                  <div
                    aria-hidden
                    className="pointer-events-none sticky left-0 h-0 w-(--jd-grid-view)"
                  >
                    <div className="absolute inset-x-0 top-0 h-0.5 overflow-hidden">
                      <span className="absolute inset-y-0 left-0 w-1/3 animate-sweep bg-brand" />
                    </div>
                  </div>
                )}
              </div>
              {!body && (
                <ContextMenuTrigger asChild onContextMenu={onBodyContextMenu}>
                  <div
                    role="rowgroup"
                    className="relative select-none"
                    style={{ height: count * rowHeight }}
                    onPointerDown={onBodyPointerDown}
                    onDoubleClick={onBodyDoubleClick}
                  >
                    {drawn.map((row) => {
                      const id = display.ids[row]
                      const from = display.sources[row]
                      const [spanStart, spanEnd] = rowSpan(range, row)
                      const isActive = active !== null && active.row === row
                      return (
                        <GridRowView
                          key={id}
                          id={id}
                          index={row}
                          label={from < 0 ? "" : String(rowNumberOffset + row + 1)}
                          top={row * rowHeight}
                          template={template}
                          values={from < 0 ? null : rows[from]}
                          insert={insertedAt(model, row)}
                          update={from < 0 ? undefined : changes.updates[id]}
                          deleted={from >= 0 && changes.deletes[id] !== undefined}
                          clipped={from < 0 ? undefined : clippedMap.get(from)}
                          keyCut={keyIsPreview(model, row)}
                          error={rowErrors?.[id]}
                          pinned={pinnedCols}
                          cols={windowCols}
                          held={row === heldRow && heldCol >= 0 ? ordered[heldCol] : null}
                          heldTrack={row === heldRow ? heldTrack : 0}
                          selected={ticked.has(id)}
                          spanStart={spanStart}
                          spanEnd={spanEnd}
                          activeCol={isActive ? active.col : -1}
                          editingCol={row === editingRow ? editingCol : -1}
                          editable={canEdit}
                          selectable={selectable}
                          find={deferredFind}
                          follow={onFollowForeignKey !== undefined}
                        />
                      )
                    })}
                  </div>
                </ContextMenuTrigger>
              )}
            </div>
            {body}
          </div>
          <ContextMenuContent
            className="min-w-52"
            onCloseAutoFocus={(event) => {
              event.preventDefault()
              // The menu's Edit has opened an editor by now, and the keyboard
              // belongs in it; after anything else it goes back to the cell.
              if (liveRef.current.editing) {
                editorHandle.current?.focus()
                return
              }
              after.current.focus = true
              focusActive()
            }}
          >
            {menuTarget && <CellMenuItems target={menuTarget} actions={menuActions} />}
          </ContextMenuContent>
        </ContextMenu>
      </GridEditorContext.Provider>
      <GridStatusBar
        status={count === 0 && (error || loading) ? null : status}
        counts={changeSet?.counts ?? null}
        message={message}
      >
        {footer}
      </GridStatusBar>
    </div>
  )
}
