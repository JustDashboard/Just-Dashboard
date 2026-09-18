"use client"

import { MoreHorizontal } from "@/components/icons"
import { bytes, relativeTime } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { FileEntry } from "@/lib/types"
import { Checkbox } from "@/components/ui/checkbox"
import { FileActionsMenu, type FileActions, type RowCaps } from "@/components/files/file-actions"
import { Thumbnail } from "@/components/files/thumbnail"
import { Button } from "@/components/ui/button"
import { DropdownMenuTrigger } from "@/components/ui/dropdown-menu"
import { rowReveal } from "@/components/icon-action"
import { useDropTarget, type DropMode } from "@/components/files/dnd"

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
  caps,
  size,
  onToggle,
  onSelect,
  onOpen,
  onDragStart,
  onDropPaths,
  onDropFiles,
  actions,
}: {
  entries: FileEntry[]
  selected: Set<string>
  /** The row the inspector is showing, which is not the same as the selection. */
  activePath: string | null
  /** Paths on the clipboard waiting to be moved. */
  dimmed: Set<string>
  caps: RowCaps
  size: TileSize
  onToggle: (entry: FileEntry, checked: boolean) => void
  onSelect: (entry: FileEntry, event: React.MouseEvent) => void
  onOpen: (entry: FileEntry) => void
  onDragStart?: (entry: FileEntry, event: React.DragEvent) => void
  onDropPaths?: (paths: string[], dir: string, mode: DropMode) => void
  onDropFiles?: (transfer: DataTransfer, dir: string) => void
  actions: (entry: FileEntry) => FileActions
}) {
  const min = size === "sm" ? "7rem" : size === "lg" ? "13rem" : "10rem"
  return (
    <div
      className="grid gap-2 p-3"
      style={{ gridTemplateColumns: `repeat(auto-fill, minmax(${min}, 1fr))` }}
    >
      {entries.map((entry) => (
        <Tile
          key={entry.path}
          entry={entry}
          selected={selected.has(entry.path)}
          active={activePath === entry.path}
          dimmed={dimmed.has(entry.path)}
          caps={caps}
          size={size}
          onToggle={(checked) => onToggle(entry, checked)}
          onSelect={(event) => onSelect(entry, event)}
          onOpen={() => onOpen(entry)}
          onDragStart={onDragStart ? (event) => onDragStart(entry, event) : undefined}
          onDropPaths={onDropPaths}
          onDropFiles={onDropFiles}
          actions={actions(entry)}
        />
      ))}
    </div>
  )
}

function Tile({
  entry,
  selected,
  active,
  dimmed,
  caps,
  size,
  onToggle,
  onSelect,
  onOpen,
  onDragStart,
  onDropPaths,
  onDropFiles,
  actions,
}: {
  entry: FileEntry
  selected: boolean
  active: boolean
  dimmed: boolean
  caps: RowCaps
  size: TileSize
  onToggle: (checked: boolean) => void
  onSelect: (event: React.MouseEvent) => void
  onOpen: () => void
  onDragStart?: (event: React.DragEvent) => void
  onDropPaths?: (paths: string[], dir: string, mode: DropMode) => void
  onDropFiles?: (transfer: DataTransfer, dir: string) => void
  actions: FileActions
}) {
  const drop = useDropTarget({
    dir: entry.isDir ? entry.path : null,
    onDropPaths,
    onDropFiles,
  })

  // The tile is a click target, not a control: its name is the real button,
  // and the wrapper's handlers are a convenience for the pointer. A wrapper
  // with `role="button"` would announce the checkbox, the menu and the name
  // to a screen reader as one control's label.
  return (
    <div
      data-entry-path={entry.path}
      data-state={selected ? "selected" : undefined}
      draggable={caps.write && !!onDragStart}
      onDragStart={onDragStart}
      {...drop.handlers}
      onClick={onSelect}
      onDoubleClick={onOpen}
      className={cn(
        "group relative flex cursor-pointer flex-col items-center gap-1.5 rounded-lg border border-transparent p-2 text-center transition-colors select-none",
        "hover:bg-row-hover",
        (active || selected) && "bg-accent",
        active && "border-rule-brand",
        dimmed && "opacity-50",
        drop.over && "border-rule-brand bg-wash-brand",
      )}
      title={entry.name}
    >
      <span
        className={cn("absolute top-1.5 left-1.5 z-10", rowReveal(), selected && "opacity-100")}
        onClick={(e) => e.stopPropagation()}
        onDoubleClick={(e) => e.stopPropagation()}
      >
        <Checkbox
          checked={selected}
          onCheckedChange={(v) => onToggle(v === true)}
          aria-label={`Select ${entry.name}`}
          className="bg-card"
        />
      </span>

      <span
        className={cn("absolute top-1 right-1 z-10", rowReveal())}
        onClick={(e) => e.stopPropagation()}
        onDoubleClick={(e) => e.stopPropagation()}
      >
        <FileActionsMenu entry={entry} caps={caps} actions={actions}>
          <DropdownMenuTrigger asChild>
            <Button
              size="icon-xs"
              variant="ghost"
              aria-label={`Actions for ${entry.name}`}
              className="bg-card/80 text-muted-foreground hover:text-foreground"
            >
              <MoreHorizontal />
            </Button>
          </DropdownMenuTrigger>
        </FileActionsMenu>
      </span>

      <Thumbnail entry={entry} size={size} hoverPlay />

      <span className="w-full min-w-0">
        <button
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
        <span className="mt-0.5 block truncate text-micro text-muted-foreground">
          {entry.isDir ? relativeTime(entry.modified) : bytes(entry.size)}
        </span>
      </span>
    </div>
  )
}
