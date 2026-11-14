"use client"

import { MenuItemText } from "@/components/ui/menu-item-text"

import { useEffect, useId, useRef, useState } from "react"
import Link from "next/link"
import { useReactFlow, useStore } from "@xyflow/react"
import {
  ChevronDown,
  Crosshair,
  Fingerprint,
  Key,
  Linked,
  LockClosed,
  Minus,
  Plus,
  RotateCounterClockwise,
  Trash,
} from "@/components/icons"
import { relativeTime } from "@/lib/format"
import { cn } from "@/lib/utils"
import { KEYCAP } from "@/components/command-search/keycaps"
import { Field } from "@/components/form"
import { IconAction } from "@/components/icon-action"
import { Modal } from "@/components/modal"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Textarea } from "@/components/ui/textarea"
import { useFocusReturn } from "@/components/database/schema/focus"
import { SchemaMark } from "@/components/database/schema/rail"
import type { DbCatalogSchema } from "@/components/database/schema/types"
import { useDatabase } from "@/components/database/shell/database-context"
import type { MemoryStatus } from "@/components/database/diagram/memory"

export const FIT = { duration: 300 }

/**
 * The room a fitted picture leaves around itself: what the canvas is held to
 * stands over its top, and the zoom bar and the legend over its foot. What
 * is fitted is fitted clear of them, not under them.
 */
export const FIT_PADDING = { top: "56px", right: "24px", bottom: "56px", left: "24px" } as const

/**
 * Which schema the picture is of, said in the toolbar and changed there.
 *
 * The diagram used to draw whichever schema the last table selection had left
 * in the address, with nothing on the page saying which or offering another.
 * "Every schema" is a picture of its own — the one that shows a key crossing
 * from one schema into the next — and has its own arrangement. Each choice is
 * a link: a picture is a place, Back returns to the one before it, and a
 * pasted address opens the picture it names.
 */
export function DiagramSchemaPicker({
  schemas,
  current,
}: {
  /** Absent while the list is being read, or where it could not be. */
  schemas: readonly DbCatalogSchema[] | undefined
  current: string
}) {
  const { engine, href } = useDatabase()
  const own = (schemas ?? []).filter((schema) => !schema.system)
  const system = (schemas ?? []).filter((schema) => schema.system)
  const every = `Every ${engine.nouns.container}`
  const item = (schema: DbCatalogSchema) => (
    <DropdownMenuItem
      key={schema.name}
      asChild
      className={cn(schema.name === current && "bg-accent")}
    >
      <Link href={href("diagram", { schema: schema.name, limit: null, every: null })}>
        <SchemaMark name={schema.name} />
        <MenuItemText
          hint={
            schema.tables >= 0 && (
              <span className="numeric text-hint text-muted-foreground">{schema.tables}</span>
            )
          }
        >
          <span className="min-w-0 flex-1 truncate font-mono text-xs">{schema.name}</span>
        </MenuItemText>
      </Link>
    </DropdownMenuItem>
  )
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button
          size="sm"
          variant="ghost"
          aria-label={`${engine.nouns.container}: ${current || every}`}
          className="h-7 max-w-56 min-w-0 justify-start gap-1.5 px-1.5"
        >
          {current ? <SchemaMark name={current} /> : null}
          <span className={cn("min-w-0 truncate text-xs", current && "font-mono")}>
            {current || every}
          </span>
          <ChevronDown className="size-3 shrink-0 text-muted-foreground" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="max-h-80 w-64 overflow-y-auto">
        {/* Said in so many words: an address with the schema left out would be
            completed from where the reader last was. */}
        <DropdownMenuItem asChild className={cn(current === "" && "bg-accent")}>
          <Link href={href("diagram", { every: "1", limit: null })}>{every}</Link>
        </DropdownMenuItem>
        {own.length > 0 && <DropdownMenuSeparator />}
        {own.map(item)}
        {system.length > 0 && (
          <>
            <DropdownMenuSeparator />
            <DropdownMenuLabel>The engine&rsquo;s own</DropdownMenuLabel>
            {system.map(item)}
          </>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

/** Whether the arrangement is kept, in a word the operator can trust. */
export function Saved({
  status,
  updatedAt,
  arranged,
}: {
  status: MemoryStatus
  updatedAt?: string
  arranged: boolean
}) {
  let text: string | null = null
  if (status === "saving") text = "Saving…"
  else if (status === "unsaved") text = "Not saved yet"
  else if (status === "failed") text = "Not saved"
  else if (status === "local") text = arranged ? "Kept in this browser" : null
  else if (status === "saved") text = updatedAt ? `Saved ${relativeTime(updatedAt)}` : null
  return (
    <span
      aria-live="polite"
      className={cn(
        "text-hint whitespace-nowrap max-md:hidden",
        // A save that failed is a reading of state; the rest are quiet facts.
        status === "failed" ? "text-destructive" : "text-muted-foreground",
      )}
    >
      {text}
    </span>
  )
}

/** A control floating on the canvas, drawn as the server map draws its own. */
export const FLOAT =
  "absolute flex items-center gap-0.5 rounded-md border border-hairline bg-popover p-0.5"

/**
 * The bar at the foot of the canvas, as the server's map has it: out, how
 * far in the picture is drawn (pressed, the real size), in, Fit, and — while
 * something has been moved — putting every table back where the layout would.
 *
 * It took the place of three glyphs in a card at the top of the canvas and a
 * minimap in the corner opposite. A whiteboard in this product is zoomed and
 * fitted from one place, and where the reader is in the picture is said by
 * the percentage rather than by a second, smaller picture.
 */
export function ZoomBar({ onFit, onTidy }: { onFit: () => void; onTidy?: () => void }) {
  const flow = useReactFlow()
  const percent = useStore((state) => Math.round(state.transform[2] * 100))
  return (
    <div data-slot="diagram-zoom" className={cn(FLOAT, "bottom-3 left-3")}>
      <IconAction
        label="Zoom out"
        className="size-7"
        onClick={() => void flow.zoomOut({ duration: 150 })}
      >
        <Minus />
      </IconAction>
      <Button
        size="xs"
        variant="ghost"
        title="Back to the real size (Ctrl 0)"
        aria-label={`Zoom ${percent}%: back to the real size`}
        className="numeric h-7 min-w-12 px-1.5 text-hint"
        onClick={() => void flow.zoomTo(1, { duration: 150 })}
      >
        {percent}%
      </Button>
      <IconAction
        label="Zoom in"
        className="size-7"
        onClick={() => void flow.zoomIn({ duration: 150 })}
      >
        <Plus />
      </IconAction>
      <Button
        size="xs"
        variant="ghost"
        title="Fit the picture to the canvas (F)"
        className="h-7 px-2 text-hint"
        onClick={onFit}
      >
        Fit
      </Button>
      {onTidy && (
        <IconAction label="Tidy: lay every table out again" className="size-7" onClick={onTidy}>
          <RotateCounterClockwise />
        </IconAction>
      )}
    </div>
  )
}

/**
 * What the canvas is held to, said where it is and undone by pressing it:
 * the table it is focused on, and a layout that is locked.
 */
export function CanvasHolds({
  focus,
  locked,
  onClear,
  onUnlock,
}: {
  /** The focused table's name, as the canvas says it. */
  focus: string | null
  locked: boolean
  onClear: () => void
  onUnlock: () => void
}) {
  if (!focus && !locked) return null
  return (
    <div className="pointer-events-none absolute top-3 left-3 flex max-w-[calc(100%-7rem)] flex-wrap items-center gap-2">
      {focus && (
        <Button
          size="xs"
          variant="outline"
          className="pointer-events-auto max-w-64"
          onClick={onClear}
        >
          <Crosshair className="size-3" />
          <span className="min-w-0 truncate font-mono">{focus}</span>
          <span className="text-muted-foreground">· clear</span>
        </Button>
      )}
      {locked && (
        <Button
          size="xs"
          variant="outline"
          className="pointer-events-auto text-muted-foreground"
          onClick={onUnlock}
        >
          <LockClosed className="size-3" />
          Locked · unlock
        </Button>
      )}
    </div>
  )
}

/** The diagram's keys, as caps, at the foot of its inspector — where the map says its own. */
export function KeyLegend() {
  return (
    <div
      data-slot="diagram-keys"
      className="flex shrink-0 flex-wrap items-center gap-x-3 gap-y-1.5 border-t border-hairline px-3 py-2.5 text-hint text-muted-foreground"
    >
      <Keycap cap="F">Fit</Keycap>
      <Keycap cap="/">Find</Keycap>
      <Keycap cap="I">Panel</Keycap>
      <Keycap cap="H">Hide</Keycap>
      <Keycap cap="Esc">Let go</Keycap>
    </div>
  )
}

function Keycap({ cap, children }: { cap: string; children: React.ReactNode }) {
  return (
    <span className="flex items-center gap-1.5">
      <kbd className={KEYCAP}>{cap}</kbd>
      {children}
    </span>
  )
}

/** How many schemas the legend names before it counts the rest. */
const SCHEMAS_NAMED = 4

/**
 * What the three marks beside a column mean — and, in a picture of several
 * schemas, which hue is which schema's.
 */
export function Legend({ schemas }: { schemas?: readonly string[] }) {
  return (
    <div className="pointer-events-none absolute right-3 bottom-3 flex max-w-[calc(100%-17rem)] flex-wrap items-center justify-end gap-x-3 gap-y-1 rounded-md border border-hairline bg-popover px-2.5 py-1.5 text-micro text-muted-foreground max-md:hidden">
      <span className="flex items-center gap-1">
        <Key className="size-3 text-chart-2" /> primary key
      </span>
      <span className="flex items-center gap-1">
        <Linked className="size-3 text-chart-1" /> foreign key
      </span>
      <span className="flex items-center gap-1">
        <Fingerprint className="size-3 text-muted-foreground/60" /> unique
      </span>
      {schemas && schemas.length > 1 && (
        <>
          <span aria-hidden className="h-3 w-px bg-border" />
          {schemas.slice(0, SCHEMAS_NAMED).map((schema) => (
            <span key={schema} className="flex min-w-0 items-center gap-1 font-mono">
              <SchemaMark name={schema} />
              <span className="max-w-28 truncate">{schema}</span>
            </span>
          ))}
          {schemas.length > SCHEMAS_NAMED && (
            <span className="numeric">+{schemas.length - SCHEMAS_NAMED}</span>
          )}
        </>
      )}
    </div>
  )
}

/**
 * A note on a table. What was typed is not lost to a slip: Escape and a press
 * outside ask before they discard a changed note, and while they are asking
 * either means "keep editing"; Cancel closes at once. The table is named as
 * the picture names it — with its schema where the picture holds several.
 */
export function NoteDialog({
  name,
  initial,
  returnTo,
  onClose,
  onSave,
}: {
  /** The table, as the canvas says it. */
  name: string
  initial: string
  /** The control the keyboard goes back to: the table's own button on the canvas. */
  returnTo: () => HTMLElement | null
  onClose: () => void
  onSave: (text: string) => void
}) {
  const id = useId()
  const [text, setText] = useState(initial)
  const [asking, setAsking] = useState(false)
  const dirty = text.trim() !== initial.trim()
  useFocusReturn(true, returnTo)
  // The question takes the keyboard, and hands it back to the note.
  const keep = useRef<HTMLButtonElement>(null)
  const note = useRef<HTMLTextAreaElement>(null)
  useEffect(() => {
    if (!asking) return
    keep.current?.focus()
    const field = note.current
    return () => field?.focus()
  }, [asking])
  return (
    <Modal
      open
      onOpenChange={(open) => {
        if (open) return
        if (asking) setAsking(false)
        else if (dirty) setAsking(true)
        else onClose()
      }}
      size="sm"
      title={initial ? "Edit note" : "Add a note"}
      description={`A short note about ${name}, shown on the diagram and kept with it.`}
      footer={
        asking ? (
          <>
            <p role="alert" className="mr-auto min-w-0 text-body">
              Close and lose what you typed?
            </p>
            <Button ref={keep} variant="outline" onClick={() => setAsking(false)}>
              Keep editing
            </Button>
            <Button variant="destructive" onClick={onClose}>
              Discard
            </Button>
          </>
        ) : (
          <>
            {initial && (
              <Button variant="ghost" className="mr-auto" onClick={() => onSave("")}>
                <Trash />
                Remove
              </Button>
            )}
            <Button variant="outline" onClick={onClose}>
              Cancel
            </Button>
            <Button onClick={() => onSave(text)} disabled={!dirty}>
              Save note
            </Button>
          </>
        )
      }
    >
      <Field
        label={
          <>
            Note on <span className="font-mono">{name}</span>
          </>
        }
        htmlFor={`${id}-note`}
        hint="One line shows on the table; the whole note is in the inspector."
      >
        <Textarea
          ref={note}
          id={`${id}-note`}
          value={text}
          onChange={(e) => setText(e.target.value)}
          className="min-h-24"
          placeholder="What this table is for, who writes to it, what to be careful of…"
          autoFocus
        />
      </Field>
    </Modal>
  )
}
