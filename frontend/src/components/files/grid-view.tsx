"use client"

import { useState } from "react"
import { MoreHorizontal } from "@/components/icons"
import { bytes, relativeTime } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { FileEntry } from "@/lib/types"
import { Checkbox } from "@/components/ui/checkbox"
import { FileIcon } from "@/components/files/file-icon"
import { FileActionsMenu, type RowCaps } from "@/components/files/file-actions"
import { rawUrl } from "@/components/files/preview-panel"
import { Button } from "@/components/ui/button"
import { DropdownMenuTrigger } from "@/components/ui/dropdown-menu"
import { rowReveal } from "@/components/icon-action"

/**
 * The listing as tiles rather than rows.
 *
 * A table is the right shape for "which of these changed last night" and the
 * wrong one for "which of these is the logo": a directory of images in a table
 * is forty rows of identical grey glyphs and a name each. Tiles give the space
 * back to the thing itself — a thumbnail where there is one, a large kind icon
 * where there is not — and folders become targets big enough to hit without
 * aiming, which is most of what browsing actually is.
 *
 * The thumbnail is the file itself at its own size, drawn small by the
 * browser. That is honest for the sizes a server holds — an icon, a
 * screenshot, a photograph — and it needs no thumbnail cache to go stale or
 * to fill a disk this page exists to keep tidy. The cap on which files are
 * tried is what keeps it from pulling a 40 MB scan over a VPN.
 */
const THUMBNAIL_MAX_BYTES = 4 << 20

const THUMBNAIL_KINDS = new Set(["png", "jpg", "jpeg", "gif", "webp", "avif", "bmp", "ico", "svg"])

function thumbnailFor(entry: FileEntry): string | null {
  if (entry.isDir || entry.size > THUMBNAIL_MAX_BYTES || entry.size === 0) return null
  const ext = entry.name.split(".").pop()?.toLowerCase()
  return ext && THUMBNAIL_KINDS.has(ext) ? rawUrl(entry.path, entry.modified) : null
}

export function GridView({
  entries,
  selected,
  activePath,
  caps,
  size,
  onToggle,
  onSelect,
  onOpen,
  actions,
}: {
  entries: FileEntry[]
  selected: string[]
  /** The row the preview is showing, which is not the same as the selection. */
  activePath: string | null
  caps: RowCaps
  size: "sm" | "md" | "lg"
  onToggle: (entry: FileEntry, checked: boolean) => void
  onSelect: (entry: FileEntry) => void
  onOpen: (entry: FileEntry) => void
  actions: (entry: FileEntry) => React.ComponentProps<typeof FileActionsMenu>["actions"]
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
          selected={selected.includes(entry.path)}
          active={activePath === entry.path}
          caps={caps}
          size={size}
          onToggle={(checked) => onToggle(entry, checked)}
          onSelect={() => onSelect(entry)}
          onOpen={() => onOpen(entry)}
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
  caps,
  size,
  onToggle,
  onSelect,
  onOpen,
  actions,
}: {
  entry: FileEntry
  selected: boolean
  active: boolean
  caps: RowCaps
  size: "sm" | "md" | "lg"
  onToggle: (checked: boolean) => void
  onSelect: () => void
  onOpen: () => void
  actions: React.ComponentProps<typeof FileActionsMenu>["actions"]
}) {
  const [broken, setBroken] = useState(false)
  const thumbnail = broken ? null : thumbnailFor(entry)
  const iconSize = size === "sm" ? "size-8" : size === "lg" ? "size-14" : "size-11"

  return (
    <div
      role="button"
      tabIndex={0}
      onClick={onSelect}
      onDoubleClick={onOpen}
      onKeyDown={(e) => {
        if (e.key === "Enter") {
          e.preventDefault()
          onOpen()
        }
      }}
      data-state={selected ? "selected" : undefined}
      className={cn(
        "group relative flex cursor-pointer flex-col items-center gap-1.5 rounded-lg border border-transparent p-2 text-center transition-colors",
        "hover:border-hairline hover:bg-menu-hover",
        active && "border-border bg-accent",
        selected && "border-border bg-accent",
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

      <span
        className={cn(
          "flex w-full items-center justify-center rounded-md",
          size === "sm" ? "h-14" : size === "lg" ? "h-28" : "h-20",
          thumbnail && "overflow-hidden border border-hairline checkerboard",
        )}
      >
        {thumbnail ? (
          // eslint-disable-next-line @next/next/no-img-element
          <img
            src={thumbnail}
            alt=""
            loading="lazy"
            onError={() => setBroken(true)}
            className="max-h-full max-w-full object-contain"
          />
        ) : (
          <FileIcon entry={entry} className={iconSize} badgeClassName="bg-background" />
        )}
      </span>

      <span className="w-full min-w-0">
        <span className="line-clamp-2 text-xs leading-snug break-words">{entry.name}</span>
        <span className="mt-0.5 block truncate text-micro text-muted-foreground">
          {entry.isDir ? relativeTime(entry.modified) : bytes(entry.size)}
        </span>
      </span>
    </div>
  )
}
