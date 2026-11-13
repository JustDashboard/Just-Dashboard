"use client"

import { memo } from "react"
import { cn } from "@/lib/utils"
import type { FileEntry } from "@/lib/types"
import { FileSelection } from "@/components/files/file-selection"
import { useNearViewport } from "@/hooks/use-near-viewport"
import type { RowCaps } from "@/components/files/file-actions"
import { Thumbnail } from "@/components/files/thumbnail"
import { rowReveal } from "@/components/icon-action"
import { useDropTarget } from "@/components/files/dnd"
import type { EntryHandlers } from "@/components/files/entry-handlers"

export type TileSize = "sm" | "md" | "lg"

/**
 * The listing as tiles rather than rows.
 *
 * A table is the right shape for "which of these changed last night" and the
 * wrong one for "which of these is the logo": a directory of images in a table
 * is forty rows of identical grey glyphs and a name each. Tiles give the space
 * back to the thing itself — a thumbnail where there is one, a large kind icon
 * where there is not — and folders become targets big enough to hit without
 * aiming, which is most of what browsing actually is. A video tile plays,
 * muted, while the pointer rests on it.
 */
export function GridView({
  entries,
  selected,
  activePath,
  dimmed,
  dragging,
  caps,
  size,
  handlers,
}: {
  entries: FileEntry[]
  selected: Set<string>
  /** The row the inspector is showing, which is not the same as the selection. */
  activePath: string | null
  /** Paths on the clipboard waiting to be moved. */
  dimmed: Set<string>
  dragging: Set<string>
  caps: RowCaps
  size: TileSize
  /** The page's, one object for its life: what lets a tile sit out a render that is not about it. */
  handlers: EntryHandlers
}) {
  const width = size === "sm" ? "5rem" : size === "lg" ? "8rem" : "6.5rem"
  return (
    <div
      data-file-grid
      className="grid content-start justify-start gap-x-1 gap-y-1 p-2 pb-24"
      style={{ gridTemplateColumns: `repeat(auto-fill, minmax(0, ${width}))` }}
    >
      {entries.map((entry) => (
        <Tile
          key={entry.path}
          entry={entry}
          selected={selected.has(entry.path)}
          active={activePath === entry.path}
          dimmed={dimmed.has(entry.path)}
          dragging={dragging.has(entry.path)}
          caps={caps}
          size={size}
          handlers={handlers}
        />
      ))}
    </div>
  )
}

/** `memo` for the reason `FileRow` is: a click draws the two tiles it is about. */
const Tile = memo(function Tile({
  entry,
  selected,
  active,
  dimmed,
  dragging,
  caps,
  size,
  handlers,
}: {
  entry: FileEntry
  selected: boolean
  active: boolean
  dimmed: boolean
  dragging: boolean
  caps: RowCaps
  size: TileSize
  handlers: EntryHandlers
}) {
  const [viewportRef, near] = useNearViewport<HTMLDivElement>()
  const drop = useDropTarget({
    dir: entry.isDir ? entry.path : null,
    onDropPaths: caps.write ? handlers.onDropPaths : undefined,
    onDropFiles: caps.write ? handlers.onDropFiles : undefined,
  })
  const onOpen = () => handlers.onOpen(entry)
  const onSelect = (event: React.MouseEvent) => handlers.onSelect(entry, event)

  // The name owns keyboard activation; the wrapper makes the whole tile
  // available to the pointer without nesting its checkbox in a button.
  return (
    // eslint-disable-next-line jsx-a11y/no-static-element-interactions -- a tile of the grid: the arrows move between tiles and Enter opens one, here in onKeyDown, and its name is a button
    <div
      ref={viewportRef}
      data-entry-path={entry.path}
      // eslint-disable-next-line jsx-a11y/no-noninteractive-tabindex -- a tile of the grid: the arrows move between tiles and Enter opens one, here in onKeyDown, and its name is a button
      tabIndex={active ? 0 : -1}
      data-state={selected ? "selected" : undefined}
      data-dragging={dragging || undefined}
      draggable={caps.write}
      onDragStart={caps.write ? (event) => handlers.onDragStart(entry, event) : undefined}
      {...drop.handlers}
      onClick={onSelect}
      onFocusCapture={(event) => {
        if (!event.currentTarget.contains(event.relatedTarget as Node)) handlers.onFocusEntry(entry)
      }}
      onDoubleClick={onOpen}
      onKeyDown={(event) => {
        if (event.target === event.currentTarget && event.key === "Enter") {
          event.preventDefault()
          onOpen()
        }
      }}
      className={cn(
        "group relative flex cursor-pointer flex-col items-center gap-1 rounded-md border border-transparent px-1.5 py-1.5 text-center transition-[background-color,border-color,opacity] duration-150 select-none",
        "focus-ring-inset hover:bg-row-hover",
        (active || selected) && "bg-accent",
        active && "border-rule-brand",
        dimmed && "opacity-50",
        dragging && "opacity-40",
        drop.over && "border-rule-brand bg-wash-brand",
      )}
      title={entry.name}
    >
      <span
        role="presentation"
        className={cn("absolute top-1.5 left-1.5 z-10", rowReveal(), selected && "opacity-100")}
        onClick={(e) => e.stopPropagation()}
        onDoubleClick={(e) => e.stopPropagation()}
      >
        <FileSelection
          checked={selected}
          onCheckedChange={(v) => handlers.onToggle(entry, v === true)}
          aria-label={`Select ${entry.name}`}
          className="bg-card"
        />
      </span>

      {near || active ? (
        <Thumbnail entry={entry} size={size} hoverPlay />
      ) : (
        <span
          aria-hidden
          className={cn(
            "w-full shrink-0",
            size === "sm" ? "h-12" : size === "lg" ? "h-24" : "h-16",
          )}
        />
      )}

      <span className="w-full min-w-0">
        <button
          data-file-name
          type="button"
          className="line-clamp-2 w-full rounded-sm text-xs leading-snug break-words focus-ring"
          onClick={(e) => {
            e.stopPropagation()
            onSelect(e)
          }}
          onDoubleClick={(e) => {
            e.stopPropagation()
            onOpen()
          }}
          onKeyDown={(e) => {
            if (e.key === "Enter") {
              e.preventDefault()
              onOpen()
            }
          }}
        >
          {entry.name}
        </button>
      </span>
    </div>
  )
})
