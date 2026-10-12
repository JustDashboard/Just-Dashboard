"use client"

import { createContext, memo, useContext, useEffect, useLayoutEffect, useRef } from "react"
import { Handle, NodeResizer, Position, useStore, type Node, type NodeProps } from "@xyflow/react"
import { LockClosed } from "@/components/icons"
import { FONT_SIZE, MIN_SIZE, taskProgress, type StickyNote } from "@/lib/stickyboard"
import { cn } from "@/lib/utils"
import { NoteMarkdown } from "@/components/stickyboard/note-markdown"

/**
 * What a note asks of the board. Held in a context rather than in each node's
 * data, so a node's data changes only when the note does and a board of a
 * thousand notes re-renders one of them when one is edited.
 */
export type BoardActions = {
  editable: boolean
  startEditing: (id: string) => void
  stopEditing: () => void
  /** A keystroke: no history step of its own, because editing began with one. */
  editText: (id: string, text: string) => void
  /** Grows a note to fit what is being typed into it. Never shrinks. */
  growTo: (id: string, height: number) => void
  toggleTask: (id: string, index: number) => void
  /** Marks the board before a gesture, so the whole gesture is one undo. */
  checkpoint: () => void
  /** Ends a gesture; a gesture that changed nothing leaves no undo behind. */
  settle: () => void
}

export const BoardContext = createContext<BoardActions | null>(null)

function useBoard() {
  const board = useContext(BoardContext)
  if (!board) throw new Error("A note is drawn outside its board")
  return board
}

export type NoteData = {
  note: StickyNote
  editing: boolean
  /** A search match, and whether it is the one the find bar is on. */
  hit?: "match" | "current"
}

export type NoteNodeType = Node<NoteData, "note">

/**
 * Where a link is dragged out from: a ring a little off each side, so it is
 * never mistaken for the square resize grips on the corners. Shown on the note
 * under the pointer and the selected one (globals.css, `.stickyboard-canvas`),
 * and above the note's text, whose fade makes a layer of its own.
 */
const OFFSET = 10
const SIDES = [
  [Position.Top, "t", { top: -OFFSET }],
  [Position.Right, "r", { right: -OFFSET }],
  [Position.Bottom, "b", { bottom: -OFFSET }],
  [Position.Left, "l", { left: -OFFSET }],
] as const

const RING = 12

/**
 * The four rings as React Flow's handle bounds, for a note of this size.
 * Declared rather than measured: a note off screen on a large board is never
 * drawn, so never measured, and its links still need somewhere to start. A
 * declared list stands in for the measured one every time the note changes,
 * so it has to name all four — a drag from a ring missing from it never
 * starts.
 */
export function noteHandles(w: number, h: number) {
  const ring = (id: string, position: Position, x: number, y: number) => ({
    id,
    type: "source" as const,
    position,
    x: x - RING / 2,
    y: y - RING / 2,
    width: RING,
    height: RING,
  })
  return [
    ring("t", Position.Top, w / 2, -OFFSET),
    ring("r", Position.Right, w + OFFSET, h / 2),
    ring("b", Position.Bottom, w / 2, h + OFFSET),
    ring("l", Position.Left, -OFFSET, h / 2),
  ]
}

function LinkHandles({ connectable }: { connectable: boolean }) {
  return SIDES.map(([position, id, style]) => (
    <Handle
      key={id}
      id={id}
      type="source"
      position={position}
      isConnectable={connectable}
      data-link-handle={connectable || undefined}
      aria-label="Drag to link this note"
      style={style}
      className={cn(
        "!pointer-events-auto !z-10 !size-3 !rounded-full !border-2 !border-brand !bg-background hover:!bg-brand",
        !connectable && "!invisible",
      )}
    />
  ))
}

export const NoteNode = memo(function NoteNode({
  id,
  data,
  selected,
  dragging,
}: NodeProps<NoteNodeType>) {
  const board = useBoard()
  const { note, editing, hit } = data
  const editable = board.editable && !note.locked
  const fontSize = FONT_SIZE[note.kind][note.size]
  const min = MIN_SIZE[note.kind]
  const progress = note.kind === "sticky" ? taskProgress(note.text) : null

  const ring = cn(
    // Selection is where the reader is on the board, so it is drawn in the
    // brand; a find result is attention, drawn in the signal.
    selected && "outline-2 outline-offset-2 outline-brand",
    !selected && hit === "current" && "outline-2 outline-offset-4 outline-signal",
    !selected && hit === "match" && "outline-1 outline-offset-4 outline-mark",
  )

  const resizer = (
    <NodeResizer
      isVisible={selected && editable && !editing}
      minWidth={min.w}
      minHeight={min.h}
      onResizeStart={board.checkpoint}
      onResizeEnd={board.settle}
      handleClassName="!pointer-events-auto !size-2 !rounded-sm !border-brand !bg-foreground"
      lineClassName="!pointer-events-auto !border-transparent"
    />
  )

  if (note.kind === "section") {
    return (
      <div
        data-note
        data-note-kind="section"
        data-note-color={note.color}
        // The body lets the pointer through to the board: a press inside a
        // section starts a selection or a pan, and a double press adds a note
        // there, as it does on bare board. The title is what takes the section.
        className={cn(
          "pointer-events-none relative size-full rounded-xl border border-(--note-edge) bg-(--note-ground)",
          ring,
        )}
      >
        {resizer}
        <LinkHandles connectable={board.editable} />
        <SectionTitle fontSize={fontSize}>
          {editing ? (
            <TitleEditor note={note} />
          ) : (
            <span className={cn("truncate", !note.text && "text-(--note-ink-soft)")}>
              {note.text || "Section"}
            </span>
          )}
          {note.locked && (
            <LockClosed aria-label="Locked" className="size-[0.8em] shrink-0 opacity-70" />
          )}
        </SectionTitle>
      </div>
    )
  }

  return (
    <div
      data-note
      data-note-kind={note.kind}
      data-note-color={note.color}
      data-note-style={note.style}
      className={cn(
        "relative size-full text-(--note-ink)",
        note.kind === "sticky" && "rounded-md border border-(--note-edge) bg-(--note-ground)",
        note.kind === "text" && "rounded-sm",
        note.kind === "text" &&
          !selected &&
          !editing &&
          "hover:outline-1 hover:outline-(--note-edge) hover:outline-dashed",
        // A note being carried is the one thing on the board that is above
        // it, so it is the one thing that casts a shadow (§2).
        dragging && "shadow-lg",
        note.font === "mono" && "font-mono",
        note.align === "center" && "text-center",
        ring,
      )}
      style={{ fontSize }}
    >
      {resizer}
      <LinkHandles connectable={board.editable} />
      {editing ? (
        <NoteEditor note={note} />
      ) : (
        <div
          className={cn(
            "size-full space-y-[0.4em] overflow-hidden leading-snug break-words",
            note.kind === "sticky" ? "p-[0.85em]" : "px-[0.2em] py-[0.1em]",
            note.align === "center" && "flex flex-col justify-center",
            // Text past the bottom of a note fades rather than being cut mid-line.
            "[mask-image:linear-gradient(to_bottom,black_calc(100%-1.2em),transparent)]",
          )}
        >
          {note.text ? (
            <NoteMarkdown
              text={note.text}
              onToggle={editable ? (index) => board.toggleTask(id, index) : undefined}
            />
          ) : (
            <p className="text-(--note-ink-soft) select-none">
              {editable ? "Double-click to write" : "Empty note"}
            </p>
          )}
        </div>
      )}
      {note.locked && (
        <LockClosed
          aria-label="Locked"
          className="absolute top-[0.5em] right-[0.5em] size-[0.8em] text-(--note-ink-soft)"
        />
      )}
      {progress && progress.total > 0 && !editing && (
        <span
          className="numeric absolute right-[0.7em] bottom-[0.45em] text-[0.72em] font-semibold text-(--note-ink-soft)"
          aria-label={`${progress.done} of ${progress.total} done`}
        >
          {progress.done}/{progress.total}
        </span>
      )}
    </div>
  )
})

/**
 * A section's name, which is what a board is found by once it is zoomed out
 * past reading its notes: where its own size would fall under twelve pixels
 * on screen it grows to stay there, up to four times its size, so a
 * zoomed-out board still reads as its sections. It stands on the section's
 * top edge rather than inside it, so growing never covers the notes on it.
 */
function SectionTitle({ fontSize, children }: { fontSize: number; children: React.ReactNode }) {
  const zoom = useStore((state) => state.transform[2])
  return (
    <div
      className="pointer-events-auto absolute bottom-full left-0 flex max-w-full min-w-24 items-center gap-[0.4em] px-[0.1em] pb-[0.3em] leading-tight font-semibold text-(--note-ink)"
      style={{ fontSize: Math.min(fontSize * 4, Math.max(fontSize, 12 / zoom)) }}
    >
      {children}
    </div>
  )
}

/**
 * The note's text as the field it is typed into, sitting exactly where the
 * drawn text was so nothing jumps when editing starts. It grows the note as
 * the text outgrows it, which is what writing on a note one size too small
 * should do.
 */
function NoteEditor({ note }: { note: StickyNote }) {
  const board = useBoard()
  const field = useRef<HTMLTextAreaElement>(null)

  useEffect(() => {
    const el = field.current
    if (!el) return
    el.focus({ preventScroll: true })
    el.setSelectionRange(el.value.length, el.value.length)
  }, [])

  useLayoutEffect(() => {
    const el = field.current
    if (el && el.scrollHeight > el.clientHeight + 1)
      board.growTo(note.id, note.h + (el.scrollHeight - el.clientHeight))
  }, [board, note.id, note.h, note.text])

  return (
    <textarea
      ref={field}
      aria-label={note.kind === "text" ? "Text" : "Note"}
      value={note.text}
      maxLength={20000}
      spellCheck
      // The board must not read typing, scrolling or a press in the field as
      // a gesture on the note.
      className={cn(
        "nodrag nowheel nopan block size-full resize-none overflow-hidden bg-transparent leading-snug text-(--note-ink) outline-none placeholder:text-(--note-ink-soft)",
        note.kind === "sticky" ? "p-[0.85em]" : "px-[0.2em] py-[0.1em]",
      )}
      placeholder="Write here — Markdown works: **bold**, # heading, - [ ] task"
      onChange={(event) => board.editText(note.id, event.target.value)}
      onBlur={board.stopEditing}
      onKeyDown={(event) => {
        event.stopPropagation()
        if (event.key === "Escape" || (event.key === "Enter" && (event.metaKey || event.ctrlKey))) {
          event.preventDefault()
          board.stopEditing()
        }
      }}
    />
  )
}

function TitleEditor({ note }: { note: StickyNote }) {
  const board = useBoard()
  return (
    <input
      autoFocus
      aria-label="Section title"
      value={note.text}
      maxLength={200}
      placeholder="Section"
      className="nodrag nopan min-w-0 flex-1 bg-transparent font-semibold text-(--note-ink) outline-none placeholder:text-(--note-ink-soft)"
      onFocus={(event) => event.currentTarget.select()}
      onChange={(event) => board.editText(note.id, event.target.value)}
      onBlur={board.stopEditing}
      onKeyDown={(event) => {
        event.stopPropagation()
        if (event.key === "Escape" || event.key === "Enter") {
          event.preventDefault()
          board.stopEditing()
        }
      }}
    />
  )
}
