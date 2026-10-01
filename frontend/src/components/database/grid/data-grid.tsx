"use client"

import {
  useCallback,
  useDeferredValue,
  useEffect,
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
import { GridEditorContext, type EditMove, type GridEditorApi } from "./cell-editor"
import {
  duplicateValues,
  EMPTY_CHANGES,
  type CellEdit,
  type ChangeAction,
  type ChangeSetController,
  type RowInsertion,
} from "./change-set"
import { toJSONRows } from "./clipboard"
import { hitTest, measureColumn, trackPointer } from "./dom"
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
  editFor,
  insertedAt,
  lockReason,
  originOf,
  pendingOf,
  previewKeys,
  rowRecord,
  rowValues,
  selectedBlock,
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
import { DEFAULT_VALUE, isDefault, parseInput, type EditValue } from "./values"
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
  /** Stages copies of the selected rows. */
  duplicateSelected: () => void
  /** Marks the selected rows for deletion. */
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
    }),
    [columns, rows, ordered, display, changes, clippedMap, canEdit],
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
  const editorHandle = useRef<{ settle: () => boolean } | null>(null)
  const lastTicked = useRef<number | null>(null)
  // Escape with nothing left to clear lets the next Tab leave the grid.
  const tabOut = useRef(false)
  const messageTimer = useRef<ReturnType<typeof setTimeout>>(undefined)
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
      }
    },
    [liveRef],
  )

  const stage = useCallback(
    (action: ChangeAction) => {
      liveRef.current.changeSet?.dispatch(action)
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
      })
    },
    [announce, liveRef],
  )

  const finishEdit = useCallback(
    (result: { value: EditValue } | null, move: EditMove, fill?: string) => {
      const state = liveRef.current
      const current = state.editing
      if (!current) return
      setEditing(null)
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
        stage({ type: "edit", edits })
      }
      if (move === "none") return
      const here = state.sel.active
      const next =
        move === "stay" || !here
          ? here
          : (moveCell(here, MOVES[move], { rows: state.count, cols: state.ordered.length }) ?? here)
      if (next && !sameCell(next, here)) state.setSelection(selectCell(state.sel, next))
      after.current = { reveal: next, focus: true }
    },
    [liveRef, stage],
  )

  const registerEditor = useCallback((handle: { settle: () => boolean } | null) => {
    editorHandle.current = handle
  }, [])

  const editorApi = useMemo<GridEditorApi | null>(
    () =>
      editing && {
        seed: editing.seed,
        caret: editing.caret,
        expanded: editing.expanded,
        canFill: editing.canFill,
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

  /** Writes one value into every editable cell of the selection. */
  const fillSelection = useCallback(
    (value: (column: GridColumn) => EditValue | undefined, verb: string) => {
      const state = liveRef.current
      const block = selectedBlock(state.model, state.sel)
      if (!block || !state.canEdit) return
      const edits: CellEdit[] = []
      let skipped = 0
      for (const row of block.rows) {
        for (const entry of block.cols) {
          const next = lockReason(state.model, row, entry) ? undefined : value(entry.column)
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
    () => fillSelection((column) => (column.nullable ? null : undefined), "set to NULL"),
    [fillSelection],
  )
  const setDefault = useCallback(
    () =>
      fillSelection(
        (column) => (column.defaultExpr !== undefined ? DEFAULT_VALUE : undefined),
        "set to its default",
      ),
    [fillSelection],
  )
  /** Delete on a selection of cells: NULL where the column allows it, empty text where it does not. */
  const clearSelection = useCallback(
    () =>
      fillSelection(
        (column) => (column.nullable ? null : column.kind === "text" ? "" : undefined),
        "cleared",
      ),
    [fillSelection],
  )

  /* ------------------------------------------------------------------- rows */

  const deleteRows = useCallback(
    (rowsToDelete: readonly number[]) => {
      const state = liveRef.current
      if (!state.canDelete) {
        announce("Rows cannot be deleted here")
        return
      }
      const removals = rowsToDelete.map((row) => ({
        rowId: state.model.ids[row],
        origin: originOf(state.model, row),
      }))
      if (removals.length === 0) return
      stage({ type: "delete", rows: removals })
      announce(
        removals.length === 1
          ? "Row marked for deletion"
          : `${removals.length} rows marked for deletion`,
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
      stage({ type: "insert", rows: inserts })
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
    clearSelection,
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
        state.setSelection({ ...state.sel, rows: rowsNext })
        after.current = { reveal: null, focus: true }
        focusActive()
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
    [focusActive, follow, fromGrid, liveRef, moveTo, settleEditor, startRangeDrag],
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

  const onBodyContextMenu = useCallback(
    (event: React.MouseEvent) => {
      // Inside an editor the browser's own menu is the right one: it has paste.
      if (!fromGrid(event.target)) return
      const hit = hitTest(event.target)
      const state = liveRef.current
      if (!hit || state.ordered.length === 0 || !settleEditor()) {
        event.preventDefault()
        return
      }
      const cell = { row: hit.row, col: hit.gutter ? (state.sel.active?.col ?? 0) : hit.col }
      // A right-click inside the selection acts on the selection; outside it,
      // it moves there first.
      const inside =
        rangeContains(rangeOf(state.sel), cell.row, cell.col) ||
        state.sel.rows.includes(state.model.ids[cell.row])
      if (!inside) state.setSelection(selectCell(state.sel, cell))
      setMenu(cell)
    },
    [fromGrid, liveRef, settleEditor],
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
    clearSelection,
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
        const block = selectedBlock(state.model, state.sel)
        if (block) duplicateRows(block.rows)
      },
      deleteSelected: () => {
        const state = liveRef.current
        const block = selectedBlock(state.model, state.sel)
        if (block) deleteRows(block.rows)
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
    return {
      column: entry.column,
      multi: isRange(sel) || sel.rows.length > 0,
      rows: targetRows(model, sel, menu.row).length,
      isNull: value === null || value === undefined,
      editable: lock === null,
      canNull: lock === null && entry.column.nullable === true,
      canDefault: lock === null && entry.column.defaultExpr !== undefined,
      changed:
        (edited !== undefined && entry.column.key in edited) ||
        (state === "inserted" && value !== undefined),
      rowState: state,
      canInsert: canInsert && canEdit,
      canDelete: canDelete && canEdit,
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
      copy: copyAs,
      copyRowJSON: () => {
        const state = liveRef.current
        const cell = at()
        if (!cell) return
        const list = targetRows(state.model, state.sel, cell.row)
        const text = toJSONRows(
          state.model.columns,
          list.map((row) => rowValues(state.model, row)),
        )
        void copyText(text).then(
          (ok) =>
            ok &&
            announce(
              list.length === 1 ? "Copied the row as JSON" : `Copied ${list.length} rows as JSON`,
            ),
        )
      },
      copySQL: () => {
        const state = liveRef.current
        const cell = at()
        if (!cell) return
        const list = targetRows(state.model, state.sel, cell.row)
        state.onCopySQL?.({
          columns: [...state.model.columns],
          rowIds: list.map((row) => state.model.ids[row]),
          rows: list.map((row) => rowValues(state.model, row)),
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
      setNull,
      setDefault,
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
    setNull,
    stage,
    startEdit,
  ])

  /* ------------------------------------------------------------- rendering */

  const drawn: number[] = []
  for (let row = rowWin.start; row < rowWin.end; row++) drawn.push(row)
  // The active row stays mounted wherever the scroll is, so the keyboard and an
  // open editor are never unmounted from under the reader.
  const keepRow =
    active && active.row >= 0 && (active.row < rowWin.start || active.row >= rowWin.end)
  if (keepRow) drawn.push(active.row)
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
        <ContextMenu onOpenChange={(open) => !open && setMenu(null)}>
          <div
            ref={viewportRef}
            data-slot="data-grid-viewport"
            style={style as React.CSSProperties}
            className="group/grid relative min-h-0 flex-1 overflow-auto overscroll-contain"
            onScroll={syncWindow}
            onFocus={() => {
              ownsFocus.current = true
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
              // The one tab stop is the active cell; until there is one, the grid itself.
              tabIndex={active ? -1 : 0}
              className="w-(--jd-grid-width) min-w-full focus-ring-inset"
              onFocus={(event) => {
                if (event.target === event.currentTarget) enterGrid()
              }}
            >
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
