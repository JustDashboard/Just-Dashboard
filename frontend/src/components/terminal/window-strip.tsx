"use client"

import { useEffect, useRef, useState } from "react"
import { Cross, Pencil, Plus } from "@/components/icons"
import { cn } from "@/lib/utils"
import type { TerminalWindow as Window } from "@/lib/types"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { IconAction, rowReveal } from "@/components/icon-action"

/** Compact direct-PTY windows for the terminal title bar. */
export function WindowStrip({
  windows,
  activeId,
  onSelect,
  onRename,
  onNew,
  onClose,
  onReorder,
}: {
  windows: Window[]
  activeId: string | null
  onSelect: (id: string) => void
  onRename: (id: string, name: string) => void
  onNew: () => void
  onClose: (id: string) => void
  onReorder: (id: string, position: number) => void
}) {
  const [renaming, setRenaming] = useState<string | null>(null)
  const [dropAt, setDropAt] = useState<number | null>(null)
  return (
    <div
      className="flex min-w-0 flex-1 items-center gap-0.5 overflow-x-auto"
      aria-label="Terminal windows"
    >
      {windows.map((window, position) =>
        renaming === window.id ? (
          <WindowNameInput
            key={window.id}
            value={window.name}
            onCommit={(value) => {
              if (value) onRename(window.id, value)
              setRenaming(null)
            }}
            onCancel={() => setRenaming(null)}
          />
        ) : (
          <WindowTab
            key={window.id}
            window={window}
            active={window.id === activeId}
            inserting={dropAt === position}
            onSelect={() => onSelect(window.id)}
            onRename={() => setRenaming(window.id)}
            onClose={() => onClose(window.id)}
            onDragOver={(event) => {
              event.preventDefault()
              setDropAt(position)
            }}
            onDrop={(event) => {
              event.preventDefault()
              setDropAt(null)
              const id = event.dataTransfer.getData("application/x-jd-terminal-window")
              if (id && id !== window.id) onReorder(id, position)
            }}
            onDragEnd={() => setDropAt(null)}
          />
        ),
      )}
      <IconAction label="New window" className="size-7 shrink-0" onClick={onNew}>
        <Plus />
      </IconAction>
    </div>
  )
}

function WindowTab({
  window,
  active,
  inserting,
  onSelect,
  onRename,
  onClose,
  onDragOver,
  onDrop,
  onDragEnd,
}: {
  window: Window
  active: boolean
  inserting: boolean
  onSelect: () => void
  onRename: () => void
  onClose: () => void
  onDragOver: (event: React.DragEvent) => void
  onDrop: (event: React.DragEvent) => void
  onDragEnd: () => void
}) {
  const ref = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (active) ref.current?.scrollIntoView({ block: "nearest", inline: "nearest" })
  }, [active])
  return (
    <div
      ref={ref}
      draggable
      data-window={window.id}
      data-active={active}
      onDragStart={(event) => {
        event.dataTransfer.effectAllowed = "move"
        event.dataTransfer.setData("application/x-jd-terminal-window", window.id)
      }}
      onDragOver={onDragOver}
      onDrop={onDrop}
      onDragEnd={onDragEnd}
      className={cn(
        "group flex h-7 max-w-44 min-w-24 shrink-0 items-center rounded-md border border-transparent pr-0.5 pl-2.5 transition-colors",
        active
          ? "bg-accent text-foreground"
          : "text-muted-foreground hover:bg-row-hover hover:text-foreground",
        inserting && "border-l-primary",
      )}
    >
      <button
        aria-current={active ? "page" : undefined}
        title={window.name}
        className="flex h-full min-w-0 flex-1 items-center rounded-sm focus-ring-inset"
        onClick={onSelect}
        onDoubleClick={onRename}
      >
        <span className="truncate text-xs font-medium">{window.name}</span>
      </button>
      {/* Rename and close sit on the tab itself rather than in a menu — but
          they appear under the pointer, as a browser's do. Drawn on every tab
          all the time, two glyphs beside a five-letter name were the tab. */}
      <Button
        size="icon-sm"
        variant="ghost"
        aria-label={`Rename window ${window.name}`}
        className={cn("size-6 shrink-0 text-muted-foreground hover:text-foreground", rowReveal())}
        onClick={onRename}
      >
        <Pencil className="size-3" />
      </Button>
      <Button
        size="icon-sm"
        variant="ghost"
        aria-label={`Close window ${window.name}`}
        className={cn(
          "size-6 shrink-0 text-muted-foreground hover:text-destructive",
          !active && rowReveal(),
        )}
        onClick={onClose}
      >
        <Cross className="size-3" />
      </Button>
    </div>
  )
}

function WindowNameInput({
  value,
  onCommit,
  onCancel,
}: {
  value: string
  onCommit: (value: string) => void
  onCancel: () => void
}) {
  const [draft, setDraft] = useState(value)
  return (
    <Input
      autoFocus
      value={draft}
      className="h-7 w-36 shrink-0 text-xs"
      onChange={(event) => setDraft(event.target.value)}
      onBlur={() => onCommit(draft.trim())}
      onKeyDown={(event) => {
        if (event.key === "Enter") onCommit(draft.trim())
        if (event.key === "Escape") onCancel()
      }}
    />
  )
}
