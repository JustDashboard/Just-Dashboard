"use client"

import { useEffect, useId, useRef, useState } from "react"
import Link from "next/link"
import { useReactFlow } from "@xyflow/react"
import {
  ChevronDown,
  Crosshair,
  Fingerprint,
  Key,
  Linked,
  LockClosed,
  Minus,
  Plus,
  Trash,
} from "@/components/icons"
import { relativeTime } from "@/lib/format"
import { cn } from "@/lib/utils"
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
 * The room a fitted picture leaves around itself, by side: the zoom controls
 * stand over the top of the canvas, the legend over the bottom, and the
 * minimap — when it is drawn — over the bottom right. What is fitted is
 * fitted clear of them, not under them.
 */
export function fitPadding(minimap: boolean) {
  return { top: "56px", right: "24px", bottom: minimap ? "132px" : "56px", left: "24px" } as const
}

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
        <span className="min-w-0 flex-1 truncate font-mono text-xs">{schema.name}</span>
        {schema.tables >= 0 && (
          <span className="numeric shrink-0 text-hint text-muted-foreground">{schema.tables}</span>
        )}
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

/**
 * Zoom controls of our own rather than React Flow's, which ship their own
 * borders, shadows and icon set and look like a different product dropped onto
 * the page.
 */
export function ZoomControls({
  focus,
  locked,
  onFit,
  onClear,
  onUnlock,
}: {
  onFit: () => void
  /** The focused table's name, as the canvas says it. */
  focus: string | null
  locked: boolean
  onClear: () => void
  onUnlock: () => void
}) {
  const flow = useReactFlow()
  return (
    <div className="pointer-events-none absolute top-3 left-3 flex flex-wrap items-center gap-2">
      <div className="pointer-events-auto flex items-center gap-0.5 rounded-md border bg-card p-0.5">
        <IconAction
          label="Zoom in"
          className="size-7"
          onClick={() => flow.zoomIn({ duration: 150 })}
        >
          <Plus />
        </IconAction>
        <IconAction
          label="Zoom out"
          className="size-7"
          onClick={() => flow.zoomOut({ duration: 150 })}
        >
          <Minus />
        </IconAction>
        <IconAction label="Fit to view (F)" className="size-7" onClick={onFit}>
          <Crosshair />
        </IconAction>
      </div>
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

/** How many schemas the legend names before it counts the rest. */
const SCHEMAS_NAMED = 4

/**
 * What the three marks beside a column mean — and, in a picture of several
 * schemas, which hue is which schema's.
 */
export function Legend({ schemas }: { schemas?: readonly string[] }) {
  return (
    <div className="pointer-events-none absolute bottom-3 left-3 flex max-w-[calc(100%-13rem)] flex-wrap items-center gap-x-3 gap-y-1 rounded-md border bg-card px-2.5 py-1.5 text-micro text-muted-foreground max-sm:hidden">
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
