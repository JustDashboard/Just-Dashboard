"use client"

import {
  useCallback,
  useEffect,
  useEffectEvent,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from "react"
import { useRouter } from "next/navigation"
import {
  Background,
  BackgroundVariant,
  ConnectionMode,
  MiniMap,
  ReactFlow,
  ReactFlowProvider,
  SelectionMode,
  ViewportPortal,
  useConnection,
  useReactFlow,
  type EdgeChange,
  type NodeChange,
  type OnConnectEnd,
  type Viewport,
} from "@xyflow/react"
import "@xyflow/react/dist/style.css"
import {
  ArrowLeft,
  Check,
  Copy,
  Crosshair,
  MagnifyingGlass,
  MoreHorizontal,
  Plus,
  RefreshClockwise,
  RotateClockwise,
  RotateCounterClockwise,
  Trash,
} from "@/components/icons"
import { ApiError, del, get, patch, post } from "@/lib/api"
import { plural, relativeTime } from "@/lib/format"
import {
  boundsOf,
  carriedBy,
  clipboardText,
  copyNotes,
  createLink,
  createNote,
  linkGeometry,
  LIMITS,
  NOTE_COLORS,
  noteAt,
  noteTitle,
  readClipboard,
  rectOf,
  removeLinks,
  removeNotes,
  reorder,
  searchNotes,
  toggleTask,
  type Background as BoardBackground,
  type NoteColor,
  type NoteKind,
  type Point,
  type StickyBoard,
  type StickyLink,
  type StickyNote,
} from "@/lib/stickyboard"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"
import { useViewState } from "@/lib/view-state"
import { useAuth } from "@/hooks/use-auth"
import { useMediaQuery } from "@/hooks/use-mobile"
import { useConfirm } from "@/components/confirm-dialog"
import { IconAction } from "@/components/icon-action"
import { Modal } from "@/components/modal"
import { ErrorState, LoadingPanel } from "@/components/state"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuShortcut,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Input } from "@/components/ui/input"
import {
  EmptyHint,
  FindBar,
  ShortcutsDialog,
  Toolbox,
  ZoomBar,
  type Tool,
} from "@/components/stickyboard/chrome"
import { Floating, LinkBar, NoteBar } from "@/components/stickyboard/format-bar"
import {
  BringToFront,
  SectionGlyph,
  SendToBack,
  StickyGlyph,
  TextGlyph,
} from "@/components/stickyboard/glyphs"
import {
  LinkContext,
  LinkEdge,
  linkStroke,
  type LinkActions,
  type LinkEdgeType,
} from "@/components/stickyboard/link-edge"
import {
  BoardContext,
  NoteNode,
  noteHandles,
  type BoardActions,
  type NoteNodeType,
} from "@/components/stickyboard/note-node"
import { useBoardDocument, type SaveState } from "@/components/stickyboard/use-board"

const nodeTypes = { note: NoteNode }
const edgeTypes = { link: LinkEdge }
const ZOOM = { min: 0.05, max: 4 }
const GRID = 20
const FIT = { padding: 0.15, maxZoom: 1, duration: 350 }

type Selection = { notes: ReadonlySet<string>; links: ReadonlySet<string> }
const NOTHING: Selection = { notes: new Set(), links: new Set() }
type Editing = { kind: "note" | "link"; id: string } | null
type Menu = { x: number; y: number; at: Point; note?: string } | null

export default function StickyWorkspace({ id }: { id: number }) {
  const [board, setBoard] = useState<StickyBoard | null>(null)
  const [error, setError] = useState<Error | null>(null)
  const valid = Number.isSafeInteger(id) && id > 0

  useEffect(() => {
    if (!valid) return
    let cancelled = false
    get<StickyBoard>(`/stickyboards/${id}`)
      .then((data) => !cancelled && setBoard(data))
      .catch(
        (cause) =>
          !cancelled && setError(cause instanceof Error ? cause : new Error(String(cause))),
      )
    return () => {
      cancelled = true
    }
  }, [id, valid])

  if (!valid || error || !board) {
    return (
      <div className="p-6">
        {!valid ? (
          <ErrorState error={new Error("Invalid board address")} />
        ) : error ? (
          <ErrorState error={error} />
        ) : (
          <LoadingPanel />
        )}
      </div>
    )
  }
  return (
    <ReactFlowProvider>
      <LoadedBoard key={board.id} board={board} />
    </ReactFlowProvider>
  )
}

const STATUS: Record<SaveState, string> = {
  saved: "Saved on this server",
  pending: "Unsaved changes",
  saving: "Saving…",
  error: "Save failed — try again",
  conflict: "Changed in another tab — reload to review",
  missing: "This board was deleted — changes can’t be saved",
}

function isTyping(target: EventTarget | null) {
  const el = target as HTMLElement | null
  return Boolean(
    el &&
    (el.isContentEditable ||
      el.closest("input, textarea, select, [role=menu], [role=dialog], [role=listbox]")),
  )
}

function eventPoint(event: MouseEvent | TouchEvent | React.MouseEvent): { x: number; y: number } {
  if ("changedTouches" in event && event.changedTouches.length > 0) {
    return { x: event.changedTouches[0].clientX, y: event.changedTouches[0].clientY }
  }
  const mouse = event as MouseEvent
  return { x: mouse.clientX, y: mouse.clientY }
}

/** The colour a note shows in the minimap: the paper, or a quieter mark for text and sections. */
function minimapColor(note: StickyNote | undefined): string {
  if (!note) return "var(--muted)"
  if (note.kind === "sticky") return `var(--note-${note.color})`
  const ink = linkStroke(note.color)
  return note.kind === "section"
    ? `color-mix(in oklab, ${ink} 22%, transparent)`
    : `color-mix(in oklab, ${ink} 70%, transparent)`
}

/**
 * The node drawn for a note in a given state. Every change makes new note
 * objects only for the notes it touched, so the last node built for a note
 * object, in the same state, is that note's node still: React Flow and the
 * memoised note then skip it, and dragging one note across a board of a
 * thousand re-renders one note. Keyed weakly, so a note object an edit
 * replaced is forgotten with it.
 */
const nodes = new WeakMap<StickyNote, { key: string; node: NoteNodeType }>()

function noteNode(
  note: StickyNote,
  state: { selected: boolean; editing: boolean; hit?: "match" | "current"; editable: boolean },
): NoteNodeType {
  const draggable = state.editable && !note.locked && !state.editing
  const key = `${state.selected}:${state.editing}:${state.hit}:${draggable}:${state.editable}`
  const cached = nodes.get(note)
  if (cached?.key === key) return cached.node
  const node: NoteNodeType = {
    id: note.id,
    type: "note",
    position: { x: note.x, y: note.y },
    width: note.w,
    height: note.h,
    // The note's size is known before it is drawn. Left to be measured, a
    // note off screen on a large board — never drawn, because only what is
    // visible is — was missing from the first fit and from the minimap.
    measured: { width: note.w, height: note.h },
    data: { note, editing: state.editing, hit: state.hit },
    selected: state.selected,
    draggable,
    connectable: state.editable,
    deletable: false,
    // A section is the ground under its notes and lets the pointer through
    // to the board; only its title takes a press (note-node.tsx).
    zIndex: note.kind === "section" ? -1 : 0,
    style: note.kind === "section" ? { pointerEvents: "none" } : undefined,
    handles: noteHandles(note.w, note.h),
  }
  nodes.set(note, { key, node })
  return node
}

const edges = new WeakMap<StickyLink, { key: string; edge: LinkEdgeType }>()

function linkEdge(link: StickyLink, state: { selected: boolean; editing: boolean }): LinkEdgeType {
  const key = `${state.selected}:${state.editing}`
  const cached = edges.get(link)
  if (cached?.key === key) return cached.edge
  const edge: LinkEdgeType = {
    id: link.id,
    type: "link",
    source: link.from,
    target: link.to,
    data: { link, editing: state.editing },
    selected: state.selected,
    deletable: false,
  }
  edges.set(link, { key, edge })
  return edge
}

function LoadedBoard({ board }: { board: StickyBoard }) {
  const router = useRouter()
  const flow = useReactFlow()
  const { can } = useAuth()
  const canWrite = can("service.control")
  const canDelete = can("destructive")
  const coarse = useMediaQuery("(pointer: coarse)")
  const { confirm, dialog } = useConfirm()
  // Taken apart so each callback depends on the stable functions inside rather
  // than on the hook's result, which is a new object every render: the note
  // actions below are a context every note reads, and a value that changed on
  // each render re-rendered every note on the board with it.
  const {
    doc,
    docRef,
    change,
    checkpoint,
    settle,
    undo,
    redo,
    flush,
    isDirty,
    saveState,
    canUndo,
    canRedo,
  } = useBoardDocument(board, canWrite)

  const [name, setName] = useState(board.name)
  const savedName = useRef(board.name)
  const [selection, setSelection] = useState<Selection>(NOTHING)
  const [editing, setEditing] = useState<Editing>(null)
  const [tool, setTool] = useState<Tool | null>(null)
  const [menu, setMenu] = useState<Menu>(null)
  const [finding, setFinding] = useState(false)
  const [query, setQuery] = useState("")
  const [hitIndex, setHitIndex] = useState(0)
  const [shortcuts, setShortcuts] = useState(false)
  const [leaving, setLeaving] = useState<string | null>(null)
  const [dragging, setDragging] = useState(false)
  const [view, setView] = useViewState<Viewport | null>(`stickyboard.view.${board.id}`, null)
  const [minimap, setMinimap] = useViewState("stickyboard.minimap", true)
  const [snap, setSnap] = useViewState("stickyboard.snap", false)
  const [color, setColor] = useViewState<NoteColor>("stickyboard.color", "yellow")

  // Decided once, when the board opens: React Flow fits again whenever this
  // turns true, so derived from the notes it moved the board out from under
  // the first note put on an empty one.
  const [fitOnOpen] = useState(() => !view && board.document.notes.length > 0)
  const canvas = useRef<HTMLDivElement>(null)
  const pointer = useRef<{ x: number; y: number } | null>(null)
  const gesture = useRef(false)
  const carry = useRef(
    new Map<string, { section: string; x: number; y: number; sx: number; sy: number }>(),
  )
  const clipboard = useRef<string | null>(null)
  const [copied, setCopied] = useState(false)
  const selectionRef = useRef(selection)
  useLayoutEffect(() => {
    selectionRef.current = selection
  }, [selection])

  // Touch has no hover and no spare button, so a finger on bare board pans
  // unless the reader asks for a selection box.
  const activeTool: Tool = tool ?? (coarse ? "hand" : "select")
  const placing = activeTool === "sticky" || activeTool === "text" || activeTool === "section"
  const stopped = saveState === "conflict" || saveState === "missing"
  const editable = canWrite && !stopped

  const byId = useMemo(() => new Map(doc.notes.map((n) => [n.id, n])), [doc.notes])
  const hits = useMemo(
    () => (finding ? searchNotes(doc.notes, query) : []),
    [doc.notes, finding, query],
  )
  const current = hits.length > 0 ? hits[Math.min(hitIndex, hits.length - 1)] : undefined

  // ---- What the canvas draws ------------------------------------------------
  //
  // Nodes and edges are derived from the document on every change. A node
  // whose note, selection, edit and find state are unchanged is handed back as
  // the same object, so React Flow and the memoised note skip it: dragging one
  // note across a board of a thousand re-renders one note.

  const nodes = useMemo(() => {
    const hitIds = new Set(hits.map((n) => n.id))
    return doc.notes.map((note) =>
      noteNode(note, {
        selected: selection.notes.has(note.id),
        editing: editing?.kind === "note" && editing.id === note.id,
        hit: current?.id === note.id ? "current" : hitIds.has(note.id) ? "match" : undefined,
        editable,
      }),
    )
  }, [current, doc.notes, editable, editing, hits, selection.notes])

  const edges = useMemo(
    () =>
      doc.links.map((link) =>
        linkEdge(link, {
          selected: selection.links.has(link.id),
          editing: editing?.kind === "link" && editing.id === link.id,
        }),
      ),
    [doc.links, editing, selection.links],
  )

  // ---- Changing the board ---------------------------------------------------

  const select = useCallback((notes: Iterable<string> = [], links: Iterable<string> = []) => {
    setSelection({ notes: new Set(notes), links: new Set(links) })
  }, [])

  const patchNotes = useCallback(
    (
      ids: ReadonlySet<string>,
      patch: Partial<StickyNote>,
      mode: Parameters<typeof change>[1] = "step",
    ) => {
      change(
        (d) => ({ ...d, notes: d.notes.map((n) => (ids.has(n.id) ? { ...n, ...patch } : n)) }),
        mode,
      )
    },
    [change],
  )

  const patchLinks = useCallback(
    (
      ids: ReadonlySet<string>,
      patch: Partial<StickyLink>,
      mode: Parameters<typeof change>[1] = "step",
    ) => {
      change(
        (d) => ({ ...d, links: d.links.map((l) => (ids.has(l.id) ? { ...l, ...patch } : l)) }),
        mode,
      )
    },
    [change],
  )

  const screenCenter = useCallback((): Point => {
    const rect = canvas.current?.getBoundingClientRect()
    if (!rect) return { x: 0, y: 0 }
    return flow.screenToFlowPosition({
      x: rect.left + rect.width / 2,
      y: rect.top + rect.height / 2,
    })
  }, [flow])

  /** Where a new note goes: under the pointer if it is on the board, else the middle of the screen. */
  const placeAt = useCallback((): Point => {
    const rect = canvas.current?.getBoundingClientRect()
    const p = pointer.current
    if (
      rect &&
      p &&
      p.x >= rect.left &&
      p.x <= rect.right &&
      p.y >= rect.top &&
      p.y <= rect.bottom
    ) {
      return flow.screenToFlowPosition(p)
    }
    return screenCenter()
  }, [flow, screenCenter])

  const full = useCallback(
    (adding: number) => {
      if (docRef.current.notes.length + adding <= LIMITS.notes) return false
      notify.warning(`A board holds at most ${LIMITS.notes.toLocaleString()} notes`)
      return true
    },
    [docRef],
  )

  const addNote = useCallback(
    (kind: NoteKind, at: Point, edit = true) => {
      if (!editable || full(1)) return null
      const snapped = snap
        ? { x: Math.round(at.x / GRID) * GRID, y: Math.round(at.y / GRID) * GRID }
        : at
      const note = createNote(kind, snapped, kind === "sticky" ? { color } : {})
      // A section goes under every note, so it never hides what is on it.
      change((d) => ({ ...d, notes: kind === "section" ? [note, ...d.notes] : [...d.notes, note] }))
      select([note.id])
      if (edit) setEditing({ kind: "note", id: note.id })
      return note
    },
    [change, color, editable, full, select, snap],
  )

  /** A section drawn around the selected notes, a margin clear of them. */
  const wrapInSection = useCallback(() => {
    const picked = docRef.current.notes.filter((n) => selectionRef.current.notes.has(n.id))
    const bounds = boundsOf(picked.map(rectOf))
    if (!bounds || !editable || full(1)) return false
    const pad = 40
    const section = createNote(
      "section",
      { x: 0, y: 0 },
      {
        x: Math.round(bounds.x - pad),
        y: Math.round(bounds.y - pad),
        w: Math.round(bounds.w + pad * 2),
        h: Math.round(bounds.h + pad * 2),
      },
    )
    change((d) => ({ ...d, notes: [section, ...d.notes] }))
    select([section.id])
    setEditing({ kind: "note", id: section.id })
    return true
  }, [change, docRef, editable, full, select])

  const removeSelection = useCallback(() => {
    const { notes, links } = selectionRef.current
    const unlocked = new Set([...notes].filter((id) => !byId.get(id)?.locked))
    if (unlocked.size === 0 && links.size === 0) return
    change((d) => removeLinks(removeNotes(d, unlocked), links))
    select([...notes].filter((id) => !unlocked.has(id)))
    setEditing(null)
  }, [byId, change, select])

  const selectedNotes = useCallback(
    () => docRef.current.notes.filter((n) => selectionRef.current.notes.has(n.id)),
    [docRef],
  )

  const copySelection = useCallback(
    (event?: ClipboardEvent) => {
      const notes = selectedNotes()
      if (notes.length === 0) return false
      const ids = new Set(notes.map((n) => n.id))
      const text = clipboardText(
        notes,
        docRef.current.links.filter((l) => ids.has(l.from) && ids.has(l.to)),
      )
      clipboard.current = text
      setCopied(true)
      if (event?.clipboardData) {
        event.clipboardData.setData("text/plain", text)
        event.preventDefault()
      } else {
        void navigator.clipboard?.writeText(text).catch(() => {})
      }
      return true
    },
    [docRef, selectedNotes],
  )

  const pasteText = useCallback(
    (text: string) => {
      if (!editable) return
      const at = placeAt()
      const copied = readClipboard(text)
      if (copied && copied.notes.length > 0) {
        if (full(copied.notes.length)) return
        const bounds = boundsOf(copied.notes.map(rectOf))!
        const offset = { x: at.x - (bounds.x + bounds.w / 2), y: at.y - (bounds.y + bounds.h / 2) }
        const pasted = copyNotes(copied.notes, copied.links, offset)
        change((d) => ({
          ...d,
          notes: reorder([...d.notes, ...pasted.notes], new Set(), "front"),
          links: [...d.links, ...pasted.links].slice(0, LIMITS.links),
        }))
        select(pasted.notes.map((n) => n.id))
        return
      }
      const plain = text.trim().slice(0, LIMITS.text)
      if (!plain || full(1)) return
      const note = createNote("sticky", at, { color, text: plain })
      change((d) => ({ ...d, notes: [...d.notes, note] }))
      select([note.id])
    },
    [change, color, editable, full, placeAt, select],
  )

  const duplicate = useCallback(() => {
    const notes = selectedNotes()
    if (notes.length === 0 || !editable || full(notes.length)) return
    const ids = new Set(notes.map((n) => n.id))
    const copied = copyNotes(
      notes,
      docRef.current.links.filter((l) => ids.has(l.from) && ids.has(l.to)),
      { x: 32, y: 32 },
    )
    change((d) => ({
      ...d,
      notes: reorder([...d.notes, ...copied.notes], new Set(), "front"),
      links: [...d.links, ...copied.links],
    }))
    select(copied.notes.map((n) => n.id))
  }, [change, docRef, editable, full, selectedNotes, select])

  const fitAll = useCallback(() => {
    void flow.fitView({ padding: FIT.padding, maxZoom: FIT.maxZoom, duration: FIT.duration })
  }, [flow])

  const fitSelection = useCallback(() => {
    const ids = [...selectionRef.current.notes]
    if (ids.length === 0) return fitAll()
    void flow.fitView({
      nodes: ids.map((id) => ({ id })),
      padding: 0.3,
      maxZoom: 1.5,
      duration: FIT.duration,
    })
  }, [fitAll, flow])

  const focusNote = useCallback(
    (note: StickyNote) => {
      void flow.setCenter(note.x + note.w / 2, note.y + note.h / 2, {
        zoom: Math.max(flow.getZoom(), 0.8),
        duration: 300,
      })
    },
    [flow],
  )

  // ---- Leaving, renaming, the board itself ---------------------------------

  const go = useCallback(
    async (href: string) => {
      if ((await flush()) && !isDirty()) router.push(href)
      else setLeaving(href)
    },
    [flush, isDirty, router],
  )

  const rename = useCallback(async () => {
    const next = name.trim()
    if (!next) {
      setName(savedName.current)
      return
    }
    if (next === savedName.current) {
      setName(next)
      return
    }
    try {
      const saved = await patch<{ name: string }>(`/stickyboards/${board.id}`, { name: next })
      savedName.current = saved.name
      setName(saved.name)
    } catch (cause) {
      notify.error("Could not rename the board", cause)
      setName(savedName.current)
    }
  }, [board.id, name])

  const duplicateBoard = useCallback(async () => {
    if (!(await flush()) || isDirty()) {
      notify.error(
        "Save the board before duplicating it",
        new Error("Your latest changes have not been saved."),
      )
      return
    }
    try {
      const copy = await post<StickyBoard>(`/stickyboards/${board.id}/duplicate`, {
        name: `${savedName.current} copy`.slice(0, 100),
      })
      if (isDirty()) {
        notify.info("Board copied; staying here with your newer unsaved changes")
        return
      }
      router.push(`/stickyboard/${copy.id}`)
    } catch (cause) {
      notify.error("Could not duplicate the board", cause)
    }
  }, [board.id, flush, isDirty, router])

  const deleteBoard = useCallback(async () => {
    await flush()
    const notes = docRef.current.notes.filter((n) => n.kind === "sticky").length
    confirm({
      title: "Delete board",
      description: `Delete “${savedName.current}” with its ${plural(notes, "note")} and ${plural(docRef.current.links.length, "link")}? This cannot be undone.`,
      subject: {
        mark: <StickyGlyph className="size-5 text-muted-foreground" />,
        name: savedName.current,
      },
      confirmLabel: "Delete board",
      action: async () => {
        try {
          await del(`/stickyboards/${board.id}`)
        } catch (cause) {
          if (!(cause instanceof ApiError && cause.code === "stickyboard_not_found")) throw cause
        }
        router.push("/stickyboard")
      },
    })
  }, [board.id, confirm, docRef, flush, router])

  // ---- What notes and links ask of the board --------------------------------

  const editingRef = useRef(editing)
  useLayoutEffect(() => {
    editingRef.current = editing
  }, [editing])

  const actions = useMemo<BoardActions & LinkActions>(
    () => ({
      editable,
      startEditing: (id) => setEditing({ kind: "note", id }),
      stopEditing: () => {
        settle()
        const now = editingRef.current
        setEditing(null)
        // Text that was left empty is nothing on the board; a sticky left
        // empty is still a sticky, waiting for words.
        if (now?.kind !== "note") return
        const note = docRef.current.notes.find((n) => n.id === now.id)
        if (note?.kind === "text" && !note.text.trim()) {
          change((d) => removeNotes(d, new Set([note.id])), "live")
        }
      },
      editText: (id, text) => patchNotes(new Set([id]), { text }, "live"),
      growTo: (id, height) =>
        change(
          (d) => ({
            ...d,
            notes: d.notes.map((n) =>
              n.id === id && height > n.h ? { ...n, h: Math.ceil(height) } : n,
            ),
          }),
          "live",
        ),
      toggleTask: (id, index) => {
        const note = docRef.current.notes.find((n) => n.id === id)
        if (note) patchNotes(new Set([id]), { text: toggleTask(note.text, index) })
      },
      checkpoint: () => {
        gesture.current = true
        checkpoint()
      },
      settle: () => {
        gesture.current = false
        settle()
      },
      selectLink: (id) => select([], [id]),
      startEditingLink: (id) => {
        select([], [id])
        setEditing({ kind: "link", id })
      },
      editLabel: (id, label) => patchLinks(new Set([id]), { label }, "live"),
    }),
    [change, checkpoint, docRef, editable, patchLinks, patchNotes, select, settle],
  )

  // However an edit starts — a double-click, Enter, a new note — the history
  // mark is taken here, once, so the whole edit is one undo.
  const editingKey = editing ? `${editing.kind}:${editing.id}` : null
  const lastEditing = useRef<string | null>(null)
  useEffect(() => {
    if (editingKey && editingKey !== lastEditing.current && !gesture.current) checkpoint()
    lastEditing.current = editingKey
  }, [checkpoint, editingKey])

  // ---- React Flow's events --------------------------------------------------

  const onNodesChange = useCallback(
    (changes: NodeChange<NoteNodeType>[]) => {
      const picks = changes.filter((c) => c.type === "select")
      if (picks.length > 0) {
        setSelection((prev) => {
          const notes = new Set(prev.notes)
          for (const c of picks) {
            if (c.type !== "select") continue
            if (c.selected) notes.add(c.id)
            else notes.delete(c.id)
          }
          return { notes, links: prev.links }
        })
      }
      const moves = new Map<string, { x: number; y: number }>()
      const sizes = new Map<string, { w: number; h: number }>()
      for (const c of changes) {
        if (c.type === "position" && c.position) moves.set(c.id, c.position)
        // Measurement reports a size too; only the resizer's carries `resizing`.
        if (c.type === "dimensions" && c.dimensions && c.resizing !== undefined) {
          sizes.set(c.id, { w: c.dimensions.width, h: c.dimensions.height })
        }
      }
      if (moves.size === 0 && sizes.size === 0) return
      // What a moving section carries moves by as much as the section has.
      for (const [id, start] of carry.current) {
        const section = moves.get(start.section)
        if (section && !moves.has(id)) {
          moves.set(id, {
            x: start.x + (section.x - start.sx),
            y: start.y + (section.y - start.sy),
          })
        }
      }
      change(
        (d) => {
          let changed = false
          const notes = d.notes.map((n) => {
            const p = moves.get(n.id)
            const s = sizes.get(n.id)
            const x = p ? Math.round(p.x) : n.x
            const y = p ? Math.round(p.y) : n.y
            const w = s ? Math.round(s.w) : n.w
            const h = s ? Math.round(s.h) : n.h
            if (x === n.x && y === n.y && w === n.w && h === n.h) return n
            changed = true
            return { ...n, x, y, w, h }
          })
          return changed ? { ...d, notes } : d
        },
        gesture.current ? "live" : { merge: "nudge" },
      )
    },
    [change],
  )

  const onEdgesChange = useCallback((changes: EdgeChange<LinkEdgeType>[]) => {
    const picks = changes.filter((c) => c.type === "select")
    if (picks.length === 0) return
    setSelection((prev) => {
      const links = new Set(prev.links)
      for (const c of picks) {
        if (c.type !== "select") continue
        if (c.selected) links.add(c.id)
        else links.delete(c.id)
      }
      return { notes: prev.notes, links }
    })
  }, [])

  const onNodeDragStart = useCallback(
    (_: unknown, __: unknown, dragged: NoteNodeType[]) => {
      gesture.current = true
      setDragging(true)
      checkpoint()
      const moving = new Set(dragged.map((n) => n.id))
      carry.current.clear()
      for (const node of dragged) {
        if (node.data.note.kind !== "section") continue
        for (const id of carriedBy(docRef.current.notes, node.id)) {
          const note = byId.get(id)
          if (!note || moving.has(id)) continue
          carry.current.set(id, {
            section: node.id,
            x: note.x,
            y: note.y,
            sx: node.position.x,
            sy: node.position.y,
          })
        }
      }
    },
    [byId, checkpoint, docRef],
  )

  const onNodeDragStop = useCallback(() => {
    gesture.current = false
    carry.current.clear()
    setDragging(false)
    settle()
  }, [settle])

  const onConnectEnd: OnConnectEnd = useCallback(
    (event, state) => {
      const from = state.fromNode?.id
      const source = from ? docRef.current.notes.find((n) => n.id === from) : undefined
      if (!source || !editable) return
      const at = flow.screenToFlowPosition(eventPoint(event))
      const zoom = flow.getZoom()
      const target = noteAt(docRef.current.notes, at, 10 / zoom, source.id)
      if (target) {
        const existing = docRef.current.links.find(
          (l) =>
            (l.from === source.id && l.to === target.id) ||
            (l.from === target.id && l.to === source.id),
        )
        if (existing) return select([], [existing.id])
        if (docRef.current.links.length >= LIMITS.links) {
          notify.warning(`A board holds at most ${LIMITS.links.toLocaleString()} links`)
          return
        }
        const link = createLink(source.id, target.id)
        change((d) => ({ ...d, links: [...d.links, link] }))
        select([], [link.id])
        return
      }
      // Dropped on bare board: a new note there, already linked — the fastest
      // way to grow an idea outwards. A short drag is a slip, not a request.
      const c = { x: source.x + source.w / 2, y: source.y + source.h / 2 }
      if (Math.hypot(at.x - c.x, at.y - c.y) < Math.max(source.w, source.h) / 2 + 40 || full(1))
        return
      const kind = source.kind === "section" ? "sticky" : source.kind
      const note = createNote(kind, at, {
        color: source.kind === "sticky" ? source.color : kind === "sticky" ? color : source.color,
        style: source.style,
        size: kind === "text" ? source.size : "m",
      })
      const link = createLink(source.id, note.id)
      change((d) => ({ ...d, notes: [...d.notes, note], links: [...d.links, link] }))
      select([note.id])
      setEditing({ kind: "note", id: note.id })
    },
    [change, color, docRef, editable, flow, full, select],
  )

  // ---- The keyboard and the clipboard ----------------------------------------

  const onKeyDown = useEffectEvent((event: KeyboardEvent) => {
    const mod = event.metaKey || event.ctrlKey
    const key = event.key
    const lower = key.toLowerCase()
    // On a non-Latin layout the letter a key prints is not its name, so the
    // physical key stands in, as it does for Boards' Ctrl+S.
    const code = event.code
    const is = (letter: string) =>
      lower === letter || (!/^[a-z]$/.test(lower) && code === `Key${letter.toUpperCase()}`)
    const typing = isTyping(event.target)

    if (mod && is("s")) {
      event.preventDefault()
      void flush()
      return
    }
    if (mod && is("f")) {
      event.preventDefault()
      setFinding(true)
      return
    }
    if (typing || shortcuts || menu) return

    if (mod && is("z")) {
      event.preventDefault()
      if (event.shiftKey) redo()
      else undo()
      return
    }
    if (mod && is("y")) {
      event.preventDefault()
      redo()
      return
    }
    if (mod && is("a")) {
      event.preventDefault()
      select(
        docRef.current.notes.map((n) => n.id),
        docRef.current.links.map((l) => l.id),
      )
      return
    }
    if (mod && is("d")) {
      event.preventDefault()
      duplicate()
      return
    }
    if (mod && (key === "=" || key === "+")) {
      event.preventDefault()
      void flow.zoomIn({ duration: 150 })
      return
    }
    if (mod && key === "-") {
      event.preventDefault()
      void flow.zoomOut({ duration: 150 })
      return
    }
    if (mod || event.altKey) return

    const { notes, links } = selectionRef.current
    if (key === "Escape") {
      if (placing) setTool("select")
      else if (finding) setFinding(false)
      else select()
      return
    }
    if (key === "Delete" || key === "Backspace") {
      if (!editable || (notes.size === 0 && links.size === 0)) return
      event.preventDefault()
      removeSelection()
      return
    }
    if (key === "Enter") {
      if (!editable) return
      if (notes.size === 1 && links.size === 0) {
        const note = byId.get([...notes][0])
        if (note && !note.locked) {
          event.preventDefault()
          setEditing({ kind: "note", id: note.id })
        }
      } else if (links.size === 1 && notes.size === 0) {
        event.preventDefault()
        setEditing({ kind: "link", id: [...links][0] })
      }
      return
    }
    if (key.startsWith("Arrow") && notes.size > 0 && editable) {
      event.preventDefault()
      // React Flow moves a focused node with the arrows too; this one moves the
      // whole selection whatever has focus, so its own must not run as well.
      event.stopPropagation()
      const step = event.shiftKey ? GRID * 2 : snap ? GRID : 1
      const dx = key === "ArrowLeft" ? -step : key === "ArrowRight" ? step : 0
      const dy = key === "ArrowUp" ? -step : key === "ArrowDown" ? step : 0
      change(
        (d) => ({
          ...d,
          notes: d.notes.map((n) =>
            notes.has(n.id) && !n.locked ? { ...n, x: n.x + dx, y: n.y + dy } : n,
          ),
        }),
        { merge: "nudge" },
      )
      return
    }
    if (event.shiftKey && code === "Digit1") return fitAll()
    if (event.shiftKey && code === "Digit2") return fitSelection()
    if (event.shiftKey && code === "Digit0") return void flow.zoomTo(1, { duration: 200 })
    if (key === "?") return setShortcuts(true)
    if (is("v")) return setTool("select")
    if (is("h")) return setTool("hand")
    if (!editable) return
    if (is("n") || is("t") || is("s")) {
      // The new note's field takes focus at once, and without this the key
      // that made it arrives there as its first letter.
      event.preventDefault()
      if (is("s") && notes.size > 0 && wrapInSection()) return
      return void addNote(is("n") ? "sticky" : is("t") ? "text" : "section", placeAt())
    }
    if (key === "]" || key === "[") {
      if (notes.size === 0) return
      change((d) => ({ ...d, notes: reorder(d.notes, notes, key === "]" ? "front" : "back") }))
      return
    }
    const digit = /^Digit([1-9])$/.exec(code)
    if (digit && !event.shiftKey && (notes.size > 0 || links.size > 0)) {
      const picked = NOTE_COLORS[Number(digit[1]) - 1]
      change((d) => ({
        ...d,
        notes: d.notes.map((n) => (notes.has(n.id) ? { ...n, color: picked } : n)),
        links: d.links.map((l) => (links.has(l.id) ? { ...l, color: picked } : l)),
      }))
    }
  })
  const onCopy = useEffectEvent((event: ClipboardEvent) => {
    if (!isTyping(event.target)) copySelection(event)
  })
  const onCut = useEffectEvent((event: ClipboardEvent) => {
    if (isTyping(event.target) || !editable) return
    if (copySelection(event)) removeSelection()
  })
  const onPaste = useEffectEvent((event: ClipboardEvent) => {
    if (isTyping(event.target) || !editable) return
    const text = event.clipboardData?.getData("text/plain") || clipboard.current
    if (!text) return
    event.preventDefault()
    pasteText(text)
  })

  useEffect(() => {
    const keydown = (event: KeyboardEvent) => onKeyDown(event)
    const copy = (event: ClipboardEvent) => onCopy(event)
    const cut = (event: ClipboardEvent) => onCut(event)
    const paste = (event: ClipboardEvent) => onPaste(event)
    // Capture, so a nudge can be kept from also reaching React Flow's own
    // arrow handling on a focused note.
    window.addEventListener("keydown", keydown, true)
    window.addEventListener("copy", copy)
    window.addEventListener("cut", cut)
    window.addEventListener("paste", paste)
    return () => {
      window.removeEventListener("keydown", keydown, true)
      window.removeEventListener("copy", copy)
      window.removeEventListener("cut", cut)
      window.removeEventListener("paste", paste)
    }
  }, [])

  // The find bar keeps its place as the board changes under it, and moves the
  // board to each result it steps to.
  useEffect(() => {
    if (current) focusNote(current)
    // Only a step to another note moves the board; typing more of the same
    // word keeps the reader where they are.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [current?.id])

  // ---- Drawing ----------------------------------------------------------------

  const pickedNotes = useMemo(
    () => doc.notes.filter((n) => selection.notes.has(n.id)),
    [doc.notes, selection.notes],
  )
  const pickedLinks = useMemo(
    () => doc.links.filter((l) => selection.links.has(l.id)),
    [doc.links, selection.links],
  )
  const barLink = pickedNotes.length === 0 && pickedLinks.length > 0 ? pickedLinks[0] : undefined
  const barLinkAt = useMemo(() => {
    if (!barLink) return null
    const a = byId.get(barLink.from)
    const z = byId.get(barLink.to)
    if (!a || !z) return null
    const at = linkGeometry(rectOf(a), rectOf(z), barLink.path).label
    return { x: at.x, y: at.y, w: 0, h: 0 }
  }, [barLink, byId])
  const pickedBounds = useMemo(() => boundsOf(pickedNotes.map(rectOf)), [pickedNotes])
  const menuNote = menu?.note ? byId.get(menu.note) : undefined

  const background: Record<BoardBackground, React.ReactNode> = {
    dots: (
      <Background variant={BackgroundVariant.Dots} gap={24} size={1.25} color="var(--grid-dot)" />
    ),
    lines: <Background variant={BackgroundVariant.Lines} gap={48} color="var(--grid-dot)" />,
    none: null,
  }

  const counts = `${plural(doc.notes.filter((n) => n.kind === "sticky").length, "note")} · ${plural(doc.links.length, "link")}`

  return (
    <BoardContext.Provider value={actions}>
      <LinkContext.Provider value={actions}>
        <div className="flex h-full min-h-0 min-w-0 flex-col">
          <div className="flex min-h-14 flex-wrap items-center gap-2 border-b border-hairline bg-background px-3 py-2 md:px-4">
            <IconAction label="Back to Stickyboard" onClick={() => void go("/stickyboard")}>
              <ArrowLeft />
            </IconAction>
            <Input
              aria-label="Board name"
              value={name}
              maxLength={100}
              readOnly={!canWrite}
              onChange={(event) => setName(event.target.value)}
              onBlur={(event) => {
                if (event.currentTarget.dataset.cancelRename === "true") {
                  delete event.currentTarget.dataset.cancelRename
                  return
                }
                void rename()
              }}
              onKeyDown={(event) => {
                if (event.key === "Enter") event.currentTarget.blur()
                if (event.key === "Escape") {
                  event.currentTarget.dataset.cancelRename = "true"
                  setName(savedName.current)
                  event.currentTarget.blur()
                }
              }}
              className="h-8 max-w-xs min-w-28 flex-1 border-transparent bg-transparent text-title font-semibold sm:h-8"
            />
            <span
              role="status"
              className={cn(
                "mr-auto flex items-center gap-1.5 text-hint whitespace-nowrap",
                saveState === "error" || stopped ? "text-destructive" : "text-muted-foreground",
              )}
            >
              {saveState === "saved" && <Check className="size-3" />}
              {canWrite ? STATUS[saveState] : "Read only"}
              <span className="text-muted-foreground max-md:hidden">· {counts}</span>
            </span>
            {saveState === "conflict" && (
              <Button variant="outline" size="sm" onClick={() => window.location.reload()}>
                <RefreshClockwise /> Reload
              </Button>
            )}
            {canWrite && (
              <>
                <IconAction label="Undo (Ctrl Z)" disabled={!canUndo || stopped} onClick={undo}>
                  <RotateCounterClockwise />
                </IconAction>
                <IconAction
                  label="Redo (Ctrl Shift Z)"
                  disabled={!canRedo || stopped}
                  onClick={redo}
                >
                  <RotateClockwise />
                </IconAction>
              </>
            )}
            <IconAction label="Find (Ctrl F)" onClick={() => setFinding((open) => !open)}>
              <MagnifyingGlass />
            </IconAction>
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Button variant="ghost" size="icon-sm" aria-label="Board options">
                  <MoreHorizontal />
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end" className="w-60">
                <DropdownMenuLabel>Background</DropdownMenuLabel>
                <DropdownMenuRadioGroup
                  value={doc.background}
                  onValueChange={(value) =>
                    change((d) => ({ ...d, background: value as BoardBackground }))
                  }
                >
                  <DropdownMenuRadioItem value="dots" disabled={!editable}>
                    Dots
                  </DropdownMenuRadioItem>
                  <DropdownMenuRadioItem value="lines" disabled={!editable}>
                    Grid lines
                  </DropdownMenuRadioItem>
                  <DropdownMenuRadioItem value="none" disabled={!editable}>
                    Plain
                  </DropdownMenuRadioItem>
                </DropdownMenuRadioGroup>
                <DropdownMenuSeparator />
                <DropdownMenuCheckboxItem
                  checked={snap}
                  onCheckedChange={(on) => setSnap(on === true)}
                >
                  Snap to grid
                </DropdownMenuCheckboxItem>
                <DropdownMenuCheckboxItem
                  checked={minimap}
                  onCheckedChange={(on) => setMinimap(on === true)}
                >
                  Minimap
                </DropdownMenuCheckboxItem>
                <DropdownMenuSeparator />
                <DropdownMenuItem onSelect={fitAll}>
                  <Crosshair />
                  Fit the board
                  <DropdownMenuShortcut>⇧1</DropdownMenuShortcut>
                </DropdownMenuItem>
                <DropdownMenuItem onSelect={() => setShortcuts(true)}>
                  Keyboard shortcuts
                  <DropdownMenuShortcut>?</DropdownMenuShortcut>
                </DropdownMenuItem>
                {canWrite && (
                  <>
                    <DropdownMenuSeparator />
                    <DropdownMenuItem onSelect={() => void duplicateBoard()}>
                      <Copy />
                      Duplicate board
                    </DropdownMenuItem>
                  </>
                )}
                {canDelete && (
                  <DropdownMenuItem variant="destructive" onSelect={() => void deleteBoard()}>
                    <Trash />
                    Delete board
                  </DropdownMenuItem>
                )}
              </DropdownMenuContent>
            </DropdownMenu>
            {editable && (
              <Button size="sm" onClick={() => addNote("sticky", screenCenter())}>
                <Plus /> Note
              </Button>
            )}
          </div>

          <div
            ref={canvas}
            className="stickyboard-canvas relative min-h-0 flex-1 overflow-hidden bg-background"
            data-placing={placing || undefined}
            aria-label={`${name} board`}
            onPointerMove={(event) => {
              pointer.current = { x: event.clientX, y: event.clientY }
            }}
            onDoubleClick={(event) => {
              // In React Flow 12 the pane is the ancestor of every note and
              // link, so bare board is a press whose target is the pane
              // itself — which a press inside a section is too, since a
              // section's body lets the pointer through.
              const target = event.target as HTMLElement
              if (!editable || !target.classList.contains("react-flow__pane")) return
              addNote("sticky", flow.screenToFlowPosition(eventPoint(event)))
            }}
          >
            <ReactFlow<NoteNodeType, LinkEdgeType>
              nodes={nodes}
              edges={edges}
              nodeTypes={nodeTypes}
              edgeTypes={edgeTypes}
              onNodesChange={onNodesChange}
              onEdgesChange={onEdgesChange}
              onNodeDragStart={onNodeDragStart}
              onNodeDragStop={onNodeDragStop}
              onNodeDoubleClick={(_, node) => {
                const note = node.data.note
                if (editable && !note.locked) setEditing({ kind: "note", id: note.id })
              }}
              onEdgeDoubleClick={(_, edge) => {
                if (editable) setEditing({ kind: "link", id: edge.id })
              }}
              onNodeContextMenu={(event, node) => {
                event.preventDefault()
                if (!selectionRef.current.notes.has(node.id)) select([node.id])
                setMenu({
                  x: event.clientX,
                  y: event.clientY,
                  at: flow.screenToFlowPosition(eventPoint(event)),
                  note: node.id,
                })
              }}
              onPaneContextMenu={(event) => {
                event.preventDefault()
                setMenu({
                  x: event.clientX,
                  y: event.clientY,
                  at: flow.screenToFlowPosition(eventPoint(event as MouseEvent)),
                })
              }}
              onPaneClick={(event) => {
                setEditing(null)
                if (placing) {
                  addNote(activeTool as NoteKind, flow.screenToFlowPosition(eventPoint(event)))
                  setTool("select")
                }
              }}
              onConnectEnd={onConnectEnd}
              connectOnClick={false}
              connectionMode={ConnectionMode.Loose}
              connectionRadius={12}
              connectionLineStyle={{
                stroke: "var(--brand)",
                strokeWidth: 2,
                strokeDasharray: "6 5",
              }}
              defaultViewport={view ?? undefined}
              fitView={fitOnOpen}
              onInit={(instance) => {
                // A place remembered on another screen — a desktop's, opened
                // on a phone — can hold none of the board; then the board is
                // fitted rather than opened on an empty stretch of it.
                if (!view || doc.notes.length === 0) return
                const rect = canvas.current?.getBoundingClientRect()
                if (!rect) return
                const a = instance.screenToFlowPosition({ x: rect.left, y: rect.top })
                const z = instance.screenToFlowPosition({ x: rect.right, y: rect.bottom })
                const seen = { x: a.x, y: a.y, w: z.x - a.x, h: z.y - a.y }
                const visible = doc.notes.some(
                  (n) =>
                    n.x < seen.x + seen.w &&
                    n.x + n.w > seen.x &&
                    n.y < seen.y + seen.h &&
                    n.y + n.h > seen.y,
                )
                if (!visible) void instance.fitView({ padding: FIT.padding, maxZoom: FIT.maxZoom })
              }}
              fitViewOptions={{ padding: FIT.padding, maxZoom: FIT.maxZoom }}
              onMoveEnd={(event, viewport) => {
                // A null event is a move the board made itself — a fit, a find.
                if (event) setView(viewport)
              }}
              minZoom={ZOOM.min}
              maxZoom={ZOOM.max}
              panOnScroll
              zoomOnScroll={false}
              zoomOnPinch
              zoomOnDoubleClick={false}
              panOnDrag={activeTool === "hand" ? true : [1]}
              selectionOnDrag={activeTool === "select"}
              selectionMode={SelectionMode.Partial}
              multiSelectionKeyCode={["Shift", "Meta", "Control"]}
              deleteKeyCode={null}
              nodesDraggable={editable}
              nodesConnectable={editable}
              elementsSelectable={!placing}
              snapToGrid={snap}
              snapGrid={[GRID, GRID]}
              elevateNodesOnSelect={false}
              elevateEdgesOnSelect={false}
              onlyRenderVisibleElements
              nodeDragThreshold={2}
              colorMode="dark"
              proOptions={{ hideAttribution: true }}
              aria-label={`Notes on ${name}`}
              className={cn(
                "[--xy-background-color:transparent]",
                "[--xy-selection-background-color:var(--wash-brand)] [--xy-selection-border:1px_solid_var(--rule-brand)]",
              )}
            >
              {background[doc.background]}
              {minimap && (
                <MiniMap<NoteNodeType>
                  pannable
                  zoomable
                  ariaLabel="Minimap"
                  className="!right-3 !bottom-14 !m-0 !h-28 !w-44 overflow-hidden !rounded-lg !border !bg-card max-sm:!hidden"
                  maskColor="color-mix(in oklab, var(--color-background) 72%, transparent)"
                  nodeColor={(node) => minimapColor(node.data?.note)}
                  nodeStrokeWidth={0}
                  nodeBorderRadius={4}
                />
              )}
              {editable && pickedBounds && !dragging && (
                <Floating rect={pickedBounds}>
                  <NoteBar
                    notes={pickedNotes}
                    onChange={(p) => patchNotes(selection.notes, p)}
                    onEdit={
                      pickedNotes.length === 1 && !pickedNotes[0].locked
                        ? () => setEditing({ kind: "note", id: pickedNotes[0].id })
                        : undefined
                    }
                    onReorder={(to) =>
                      change((d) => ({ ...d, notes: reorder(d.notes, selection.notes, to) }))
                    }
                    onDuplicate={duplicate}
                    onCopy={() => copySelection()}
                    onDelete={removeSelection}
                  />
                </Floating>
              )}
              {editable && barLink && barLinkAt && !dragging && (
                <Floating rect={barLinkAt}>
                  <LinkBar
                    links={pickedLinks}
                    onChange={(p) => patchLinks(selection.links, p)}
                    onLabel={
                      pickedLinks.length === 1
                        ? () => setEditing({ kind: "link", id: barLink.id })
                        : undefined
                    }
                    onReverse={() =>
                      change((d) => ({
                        ...d,
                        links: d.links.map((l) =>
                          selection.links.has(l.id) ? { ...l, from: l.to, to: l.from } : l,
                        ),
                      }))
                    }
                    onDelete={removeSelection}
                  />
                </Floating>
              )}
              <ConnectionTarget notes={doc.notes} />
            </ReactFlow>

            <Toolbox
              tool={activeTool}
              onTool={setTool}
              color={color}
              onColor={setColor}
              editable={editable}
            />
            <ZoomBar onFit={fitAll} minimap={minimap} onMinimap={() => setMinimap(!minimap)} />
            {finding && (
              <FindBar
                query={query}
                onQuery={(q) => {
                  setQuery(q)
                  setHitIndex(0)
                }}
                count={hits.length}
                index={Math.min(hitIndex, Math.max(0, hits.length - 1))}
                onStep={(by) =>
                  hits.length && setHitIndex((i) => (i + by + hits.length) % hits.length)
                }
                onClose={() => setFinding(false)}
              />
            )}
            {doc.notes.length === 0 && <EmptyHint editable={editable} />}
            {placing && (
              <p className="pointer-events-none absolute top-3 left-1/2 z-10 -translate-x-1/2 rounded-md border bg-card px-2.5 py-1 text-xs text-muted-foreground">
                Click the board to place the {activeTool === "sticky" ? "sticky note" : activeTool}{" "}
                · Esc to cancel
              </p>
            )}
          </div>
        </div>

        {/* One menu for a right-click anywhere on the board, opened where it
            was asked for, as the database diagram opens its table menu. */}
        <DropdownMenu open={menu !== null} onOpenChange={(open) => !open && setMenu(null)}>
          <DropdownMenuTrigger asChild>
            <span
              aria-hidden
              className="pointer-events-none fixed size-0"
              style={{ left: menu?.x ?? 0, top: menu?.y ?? 0 }}
            />
          </DropdownMenuTrigger>
          {menu && (
            <DropdownMenuContent align="start" className="w-56">
              {menuNote ? (
                <>
                  <DropdownMenuLabel className="truncate">
                    {selection.notes.size > 1
                      ? plural(selection.notes.size, "item")
                      : noteTitle(menuNote, menuNote.kind === "section" ? "Section" : "Empty note")}
                  </DropdownMenuLabel>
                  {editable && selection.notes.size === 1 && !menuNote.locked && (
                    <DropdownMenuItem
                      onSelect={() => setEditing({ kind: "note", id: menuNote.id })}
                    >
                      Edit
                      <DropdownMenuShortcut>Enter</DropdownMenuShortcut>
                    </DropdownMenuItem>
                  )}
                  <DropdownMenuItem onSelect={fitSelection}>
                    <Crosshair />
                    Zoom to {selection.notes.size > 1 ? "selection" : "note"}
                    <DropdownMenuShortcut>⇧2</DropdownMenuShortcut>
                  </DropdownMenuItem>
                  {editable && (
                    <>
                      <DropdownMenuSeparator />
                      <DropdownMenuItem onSelect={duplicate}>
                        <Copy />
                        Duplicate
                      </DropdownMenuItem>
                      <DropdownMenuItem onSelect={() => copySelection()}>
                        <Copy />
                        Copy
                      </DropdownMenuItem>
                      <DropdownMenuItem onSelect={wrapInSection}>
                        <SectionGlyph />
                        Group in a section
                        <DropdownMenuShortcut>S</DropdownMenuShortcut>
                      </DropdownMenuItem>
                      <DropdownMenuItem
                        onSelect={() =>
                          change((d) => ({
                            ...d,
                            notes: reorder(d.notes, selection.notes, "front"),
                          }))
                        }
                      >
                        <BringToFront />
                        Bring to front
                      </DropdownMenuItem>
                      <DropdownMenuItem
                        onSelect={() =>
                          change((d) => ({
                            ...d,
                            notes: reorder(d.notes, selection.notes, "back"),
                          }))
                        }
                      >
                        <SendToBack />
                        Send to back
                      </DropdownMenuItem>
                      <DropdownMenuItem
                        onSelect={() =>
                          patchNotes(selection.notes, {
                            locked: !pickedNotes.every((n) => n.locked),
                          })
                        }
                      >
                        {pickedNotes.every((n) => n.locked) ? "Unlock" : "Lock in place"}
                      </DropdownMenuItem>
                      <DropdownMenuSeparator />
                      <DropdownMenuItem variant="destructive" onSelect={removeSelection}>
                        <Trash />
                        Delete
                        <DropdownMenuShortcut>Del</DropdownMenuShortcut>
                      </DropdownMenuItem>
                    </>
                  )}
                </>
              ) : (
                <>
                  {editable && (
                    <>
                      <DropdownMenuItem onSelect={() => addNote("sticky", menu.at)}>
                        <StickyGlyph />
                        Sticky note here
                        <DropdownMenuShortcut>N</DropdownMenuShortcut>
                      </DropdownMenuItem>
                      <DropdownMenuItem onSelect={() => addNote("text", menu.at)}>
                        <TextGlyph />
                        Text here
                        <DropdownMenuShortcut>T</DropdownMenuShortcut>
                      </DropdownMenuItem>
                      <DropdownMenuItem onSelect={() => addNote("section", menu.at)}>
                        <SectionGlyph />
                        Section here
                        <DropdownMenuShortcut>S</DropdownMenuShortcut>
                      </DropdownMenuItem>
                      {copied && (
                        <DropdownMenuItem
                          onSelect={() => {
                            pointer.current = { x: menu.x, y: menu.y }
                            pasteText(clipboard.current ?? "")
                          }}
                        >
                          <Copy />
                          Paste here
                        </DropdownMenuItem>
                      )}
                      <DropdownMenuSeparator />
                    </>
                  )}
                  <DropdownMenuItem
                    onSelect={() =>
                      select(
                        doc.notes.map((n) => n.id),
                        doc.links.map((l) => l.id),
                      )
                    }
                  >
                    Select all
                  </DropdownMenuItem>
                  <DropdownMenuItem onSelect={fitAll}>
                    <Crosshair />
                    Fit the board
                    <DropdownMenuShortcut>⇧1</DropdownMenuShortcut>
                  </DropdownMenuItem>
                </>
              )}
            </DropdownMenuContent>
          )}
        </DropdownMenu>

        <ShortcutsDialog open={shortcuts} onOpenChange={setShortcuts} />
        {dialog}
        <Modal
          open={leaving !== null}
          onOpenChange={(open) => !open && setLeaving(null)}
          title="Leave without saving?"
          size="sm"
          footer={
            <>
              <Button variant="outline" onClick={() => setLeaving(null)}>
                Stay
              </Button>
              <Button variant="destructive" onClick={() => leaving && router.push(leaving)}>
                Leave without saving
              </Button>
            </>
          }
        >
          <p className="text-body leading-relaxed">
            Your latest changes to this board are not on the server
            {board.updatedAt ? ` (last saved ${relativeTime(board.updatedAt)})` : ""}. Leaving
            discards them.
          </p>
        </Modal>
      </LinkContext.Provider>
    </BoardContext.Provider>
  )
}

/**
 * While a link is being dragged out, the note it would join is outlined where
 * it lies on the board, so the drop is aimed rather than guessed.
 */
function ConnectionTarget({ notes }: { notes: readonly StickyNote[] }) {
  const connection = useConnection()
  const flow = useReactFlow()
  if (!connection.inProgress) return null
  const target = noteAt(notes, connection.to, 10 / flow.getZoom(), connection.fromNode.id)
  if (!target) return null
  return (
    <ViewportPortal>
      <div
        aria-hidden
        className="pointer-events-none absolute rounded-lg outline-2 outline-offset-4 outline-brand"
        style={{
          transform: `translate(${target.x}px, ${target.y}px)`,
          width: target.w,
          height: target.h,
        }}
      />
    </ViewportPortal>
  )
}
