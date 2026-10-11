"use client"

import { useEffect, useRef } from "react"
import { useReactFlow, useViewport } from "@xyflow/react"
import { ChevronDown, ChevronUp, Cross, Crosshair, Minus, Plus } from "@/components/icons"
import { NOTE_COLOR_NAMES, NOTE_COLORS, type NoteColor, type NoteKind } from "@/lib/stickyboard"
import { cn } from "@/lib/utils"
import { Keycaps } from "@/components/command-search/keycaps"
import { IconAction } from "@/components/icon-action"
import { Modal } from "@/components/modal"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import {
  HandTool,
  MinimapGlyph,
  PointerTool,
  SectionGlyph,
  StickyGlyph,
  TextGlyph,
} from "@/components/stickyboard/glyphs"

export type Tool = "select" | "hand" | NoteKind

/**
 * A control that stands on the board: the card's ground and a border, as the
 * database diagram's zoom controls are, and no shadow — these sit on the
 * canvas, they do not float over the page.
 */
export const BOARD_CONTROL =
  "pointer-events-auto flex items-center gap-0.5 rounded-lg border bg-card p-0.5"

function ToolButton({
  label,
  keys,
  pressed,
  onClick,
  children,
}: {
  label: string
  keys: string
  pressed: boolean
  onClick: () => void
  children: React.ReactNode
}) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button
          variant="ghost"
          size="icon-sm"
          aria-label={label}
          aria-pressed={pressed}
          onClick={onClick}
          className={cn(
            "size-9 [&_svg]:size-[18px]",
            pressed && "bg-accent text-brand hover:bg-accent",
          )}
        >
          {children}
        </Button>
      </TooltipTrigger>
      <TooltipContent side="right" className="flex items-center gap-2">
        {label} <Keycaps keys={keys} />
      </TooltipContent>
    </Tooltip>
  )
}

/**
 * The tools down the board's left edge: what a press on bare board does, and
 * the three things that can be put on it. Choosing a kind arms it — the next
 * press on the board places one there — and the pointer comes back after.
 */
export function Toolbox({
  tool,
  onTool,
  color,
  onColor,
  editable,
}: {
  tool: Tool
  onTool: (tool: Tool) => void
  color: NoteColor
  onColor: (color: NoteColor) => void
  editable: boolean
}) {
  return (
    <div className="pointer-events-none absolute top-1/2 left-3 z-10 -translate-y-1/2">
      <div
        className={cn(BOARD_CONTROL, "flex-col gap-1 p-1")}
        role="toolbar"
        aria-label="Tools"
        aria-orientation="vertical"
      >
        <ToolButton
          label="Select"
          keys="V"
          pressed={tool === "select"}
          onClick={() => onTool("select")}
        >
          <PointerTool />
        </ToolButton>
        <ToolButton
          label="Pan"
          keys="H · Space"
          pressed={tool === "hand"}
          onClick={() => onTool("hand")}
        >
          <HandTool />
        </ToolButton>
        {editable && (
          <>
            <span aria-hidden className="mx-1.5 h-px bg-hairline" />
            <ToolButton
              label="Sticky note"
              keys="N"
              pressed={tool === "sticky"}
              onClick={() => onTool(tool === "sticky" ? "select" : "sticky")}
            >
              <StickyGlyph />
            </ToolButton>
            <ToolButton
              label="Text"
              keys="T"
              pressed={tool === "text"}
              onClick={() => onTool(tool === "text" ? "select" : "text")}
            >
              <TextGlyph />
            </ToolButton>
            <ToolButton
              label="Section"
              keys="S"
              pressed={tool === "section"}
              onClick={() => onTool(tool === "section" ? "select" : "section")}
            >
              <SectionGlyph />
            </ToolButton>
            <span aria-hidden className="mx-1.5 h-px bg-hairline" />
            <Popover>
              <Tooltip>
                <TooltipTrigger asChild>
                  <PopoverTrigger asChild>
                    <Button
                      variant="ghost"
                      size="icon-sm"
                      className="size-9"
                      aria-label={`New notes are ${NOTE_COLOR_NAMES[color].toLowerCase()}`}
                    >
                      <span
                        data-note-color={color}
                        className="size-4 rounded-sm border border-(--note-edge) bg-(--note)"
                      />
                    </Button>
                  </PopoverTrigger>
                </TooltipTrigger>
                <TooltipContent side="right">Colour of new notes</TooltipContent>
              </Tooltip>
              <PopoverContent side="right" align="center" className="w-auto p-1.5">
                <div
                  className="grid grid-cols-3 gap-1"
                  role="radiogroup"
                  aria-label="Colour of new notes"
                >
                  {NOTE_COLORS.map((option) => (
                    <button
                      key={option}
                      type="button"
                      role="radio"
                      aria-checked={option === color}
                      aria-label={NOTE_COLOR_NAMES[option]}
                      onClick={() => onColor(option)}
                      className={cn(
                        "flex size-9 items-center justify-center rounded-md focus-ring hover:bg-accent",
                        option === color && "bg-accent",
                      )}
                    >
                      <span
                        data-note-color={option}
                        className="size-5 rounded-sm border border-(--note-edge) bg-(--note)"
                      />
                    </button>
                  ))}
                </div>
              </PopoverContent>
            </Popover>
          </>
        )}
      </div>
    </div>
  )
}

/** Zoom out, the zoom as a figure that resets it, zoom in, fit, and the minimap. */
export function ZoomBar({
  onFit,
  minimap,
  onMinimap,
}: {
  onFit: () => void
  minimap: boolean
  onMinimap: () => void
}) {
  const flow = useReactFlow()
  const { zoom } = useViewport()
  return (
    <div className="pointer-events-none absolute right-3 bottom-3 z-10 flex items-center gap-2">
      <div className={BOARD_CONTROL}>
        <IconAction
          label="Zoom out (Ctrl −)"
          className="size-7"
          onClick={() => flow.zoomOut({ duration: 150 })}
        >
          <Minus />
        </IconAction>
        <Tooltip>
          <TooltipTrigger asChild>
            <button
              type="button"
              aria-label={`Zoom ${Math.round(zoom * 100)}%, reset to 100%`}
              className="numeric h-7 w-12 rounded-md text-xs text-muted-foreground focus-ring hover:bg-accent hover:text-foreground"
              onClick={() => flow.zoomTo(1, { duration: 200 })}
            >
              {Math.round(zoom * 100)}%
            </button>
          </TooltipTrigger>
          <TooltipContent>Reset to 100%</TooltipContent>
        </Tooltip>
        <IconAction
          label="Zoom in (Ctrl +)"
          className="size-7"
          onClick={() => flow.zoomIn({ duration: 150 })}
        >
          <Plus />
        </IconAction>
        <span aria-hidden className="mx-0.5 h-5 w-px bg-hairline" />
        <IconAction label="Fit the board (Shift 1)" className="size-7" onClick={onFit}>
          <Crosshair />
        </IconAction>
        <IconAction
          label={minimap ? "Hide the minimap" : "Show the minimap"}
          aria-pressed={minimap}
          className={cn("size-7 max-sm:hidden", minimap && "bg-accent text-foreground")}
          onClick={onMinimap}
        >
          <MinimapGlyph />
        </IconAction>
      </div>
    </div>
  )
}

/**
 * Find on the board: every note holding the words is outlined, and the one
 * the bar is on is brought to the middle of the screen. Enter steps forward,
 * Shift+Enter back.
 */
export function FindBar({
  query,
  onQuery,
  count,
  index,
  onStep,
  onClose,
}: {
  query: string
  onQuery: (query: string) => void
  count: number
  index: number
  onStep: (by: 1 | -1) => void
  onClose: () => void
}) {
  const field = useRef<HTMLInputElement>(null)
  useEffect(() => {
    field.current?.focus()
    field.current?.select()
  }, [])
  return (
    <div className="pointer-events-none absolute top-3 right-3 z-10">
      <div className={cn(BOARD_CONTROL, "gap-1 p-1")} role="search">
        <Input
          ref={field}
          value={query}
          aria-label="Find on this board"
          placeholder="Find notes"
          className="h-7 w-48 border-transparent bg-transparent text-body sm:h-7"
          onChange={(event) => onQuery(event.target.value)}
          onKeyDown={(event) => {
            event.stopPropagation()
            if (event.key === "Enter") {
              event.preventDefault()
              onStep(event.shiftKey ? -1 : 1)
            } else if (event.key === "Escape") {
              event.preventDefault()
              onClose()
            }
          }}
        />
        <span
          aria-live="polite"
          className="numeric min-w-12 text-center text-hint text-muted-foreground"
        >
          {query.trim() ? (count ? `${index + 1} of ${count}` : "None") : ""}
        </span>
        <IconAction
          label="Previous (Shift Enter)"
          className="size-7"
          disabled={count === 0}
          onClick={() => onStep(-1)}
        >
          <ChevronUp />
        </IconAction>
        <IconAction
          label="Next (Enter)"
          className="size-7"
          disabled={count === 0}
          onClick={() => onStep(1)}
        >
          <ChevronDown />
        </IconAction>
        <IconAction label="Close (Esc)" className="size-7" onClick={onClose}>
          <Cross />
        </IconAction>
      </div>
    </div>
  )
}

/** What an empty board says, under the pointer's way: nothing here takes a press. */
export function EmptyHint({ editable }: { editable: boolean }) {
  return (
    <div className="pointer-events-none absolute inset-0 z-[1] flex items-center justify-center p-6">
      <div className="flex max-w-sm animate-rise flex-col items-center gap-3 text-center">
        <span
          data-note-color="yellow"
          className="flex size-12 items-center justify-center rounded-md border border-(--note-edge) bg-(--note) text-(--note-ink)"
        >
          <StickyGlyph className="size-6" />
        </span>
        <p className="text-body font-medium">This board is empty</p>
        {editable ? (
          <p className="flex flex-wrap items-center justify-center gap-1.5 text-xs leading-relaxed text-muted-foreground">
            Double-click anywhere for a sticky note, or press <Keycaps keys="N" /> for a note,{" "}
            <Keycaps keys="T" /> for text and <Keycaps keys="S" /> for a section.
          </p>
        ) : (
          <p className="text-xs text-muted-foreground">Nobody has put anything on it yet.</p>
        )}
      </div>
    </div>
  )
}

const SHORTCUTS: { group: string; rows: [string, string][] }[] = [
  {
    group: "Adding",
    rows: [
      ["Sticky note at the pointer", "N · Double-click"],
      ["Text", "T"],
      ["Section — around the selection, if any", "S"],
      ["Linked note", "Drag a note's edge dot onto bare board"],
      ["Paste notes, or text as a note", "Ctrl/⌘+V"],
    ],
  },
  {
    group: "Editing",
    rows: [
      ["Edit the selected note or link", "Enter"],
      ["Stop editing", "Esc · Ctrl/⌘+Enter"],
      ["Colour of the selection", "1 … 9"],
      ["Nudge", "Arrows · Shift+Arrows"],
      ["Bring to front · send to back", "] · ["],
      ["Duplicate", "Ctrl/⌘+D"],
      ["Copy · cut", "Ctrl/⌘+C · Ctrl/⌘+X"],
      ["Delete", "Delete · Backspace"],
      ["Undo · redo", "Ctrl/⌘+Z · Ctrl/⌘+Shift+Z"],
      ["Save now", "Ctrl/⌘+S"],
    ],
  },
  {
    group: "Moving around",
    rows: [
      ["Select · pan", "V · H"],
      ["Pan while held", "Space"],
      ["Pan", "Scroll · middle-drag"],
      ["Zoom", "Ctrl/⌘+Scroll · pinch"],
      ["Zoom in · out", "Ctrl/⌘++ · Ctrl/⌘+-"],
      ["Fit the board · the selection", "Shift+1 · Shift+2"],
      ["Zoom to 100%", "Shift+0"],
      ["Select all", "Ctrl/⌘+A"],
      ["Find", "Ctrl/⌘+F"],
      ["These shortcuts", "?"],
    ],
  },
]

export function ShortcutsDialog({
  open,
  onOpenChange,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  return (
    <Modal
      open={open}
      onOpenChange={onOpenChange}
      title="Keyboard shortcuts"
      size="lg"
      initialFocus="body"
    >
      <div className="grid gap-x-8 gap-y-5 md:grid-cols-2">
        {SHORTCUTS.map(({ group, rows }) => (
          <section key={group} className="min-w-0 space-y-1.5">
            <h3 className="eyebrow">{group}</h3>
            <dl className="divide-y divide-hairline">
              {rows.map(([label, keys]) => (
                <div key={label} className="flex items-center justify-between gap-3 py-1.5">
                  <dt className="min-w-0 text-body">{label}</dt>
                  <dd className="shrink-0">
                    {keys.includes("Drag") ? (
                      <span className="text-hint text-muted-foreground">{keys}</span>
                    ) : (
                      <Keycaps keys={keys} />
                    )}
                  </dd>
                </div>
              ))}
            </dl>
          </section>
        ))}
      </div>
    </Modal>
  )
}
